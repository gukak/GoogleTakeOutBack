// Package engine implements the core takeoutback commands: sync, verify,
// stats, compact and startup recovery.
package engine

import (
	"compress/flate"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gukak/GoogleTakeOutBack/internal/app"
	"github.com/gukak/GoogleTakeOutBack/internal/state"
	"github.com/gukak/GoogleTakeOutBack/internal/zipx"
)

// Report summarizes a sync run.
type Report struct {
	ArchivesScanned int
	FilesScanned    int
	NewFiles        int
	ModifiedFiles   int
	SkippedFiles    int
	BytesAppended   int64
	Duration        time.Duration
}

// Sync runs the consolidation process.
func Sync(env *app.Env, args []string) error {
	start := time.Now()
	env.Logf("info", "starting sync, version=%s", app.Version)

	incomingDir := env.Incoming
	i := 0
	for i < len(args) {
		if args[i] == "--incoming" && i+1 < len(args) {
			incomingDir = args[i+1]
			i += 2
			continue
		}
		if strings.HasPrefix(args[i], "--incoming=") {
			incomingDir = strings.TrimPrefix(args[i], "--incoming=")
			i++
			continue
		}
		i++
	}

	if incomingDir == "" {
		return fmt.Errorf("incoming directory cannot be empty")
	}
	if _, err := os.Stat(incomingDir); err != nil {
		return fmt.Errorf("cannot access incoming directory %s: %w", incomingDir, err)
	}

	lockFile, err := acquireLock(env.LockPath)
	if err != nil {
		return err
	}
	defer releaseLock(lockFile)

	report := &Report{}

	if err := recoverArchive(env); err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}

	existing, err := loadExistingIndex(env.ArchiveZip)
	if err != nil {
		return err
	}

	incomingFiles, err := discoverIncoming(incomingDir)
	if err != nil {
		return err
	}
	report.ArchivesScanned = len(incomingFiles)
	if incomingDir != env.Incoming {
		env.Logf("info", "using custom incoming directory: %s", incomingDir)
	}

	printArchiveList(incomingFiles)

	dst, err := zipx.OpenOrCreate(env.ArchiveZip)
	if err != nil {
		return err
	}

	var allEntries []*zipx.Entry
	for _, e := range existing {
		allEntries = append(allEntries, e)
	}

	appended := false
	for _, path := range incomingFiles {
		src, err := zipx.OpenFileRead(path)
		if err != nil {
			env.Logf("warn", "skipping invalid archive %s: %v", path, err)
			fmt.Printf("Skipping invalid archive: %s\n", filepath.Base(path))
			continue
		}
		bar := newProgressBar(len(src.Entries), filepath.Base(path))
		for i, e := range src.Entries {
			bar.update(i)
			report.FilesScanned++
			if shouldSkip(env, e) {
				continue
			}
			key := app.NormalizeKey(e.Name)
			if head, ok := existing[key]; ok {
				_, next, found := findVersion(existing, head.Name, e.CRC32, e.UncompressedSize)
				if found {
					report.SkippedFiles++
					continue
				}
				e.Name = app.InsertVersionSuffix(head.Name, next)
				written, err := zipx.CopyRawEntry(dst, src.File, e)
				if err != nil {
					env.Logf("warn", "cannot append %s: %v", e.Name, err)
					continue
				}
				report.BytesAppended += int64(written.CompressedSize)
				allEntries = append(allEntries, written)
				existing[app.NormalizeKey(written.Name)] = written
				report.ModifiedFiles++
				appended = true
				env.Logf("info", "appended %s (%d bytes compressed)", written.Name, written.CompressedSize)
			} else {
				written, err := zipx.CopyRawEntry(dst, src.File, e)
				if err != nil {
					env.Logf("warn", "cannot append %s: %v", e.Name, err)
					continue
				}
				report.BytesAppended += int64(written.CompressedSize)
				allEntries = append(allEntries, written)
				existing[app.NormalizeKey(written.Name)] = written
				report.NewFiles++
				appended = true
				env.Logf("info", "appended %s (%d bytes compressed)", written.Name, written.CompressedSize)
			}
		}
		bar.finish()
		_ = src.Close()
	}

	if !appended {
		_ = dst.Close()
		env.Log("info", "no new or modified files to append")
		printReport(env, report, start)
		return nil
	}

	sort.SliceStable(allEntries, func(i, j int) bool {
		return allEntries[i].LocalHeaderOff < allEntries[j].LocalHeaderOff
	})

	eocd, err := zipx.WriteCentralDir(dst, allEntries)
	if err != nil {
		_ = dst.Close()
		return fmt.Errorf("write central directory: %w", err)
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}

	cdBytes, err := readTailCD(env.ArchiveZip, eocd)
	if err != nil {
		return err
	}

	s := state.New(filepath.Base(env.ArchiveZip), app.Version)
	s.ArchiveEnd = eocd.Offset + zipx.EOCDSize
	if eocd.HasZip64 {
		// approximate end; EOCD is always last
		s.ArchiveEnd = eocd.Offset + zipx.EOCDSize
	}
	s.Entries = int64(len(allEntries))
	s.CDOffset = int64(eocd.CDOffset)
	s.CDSize = int64(eocd.CDSize)
	s.CDSha256 = state.CDHash(cdBytes)
	s.EOCDOffset = eocd.Offset
	s.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
	s.LastSyncDurationS = time.Since(start).Seconds()

	if err := state.SaveAtomic(env.StatePath, s); err != nil {
		return err
	}
	if err := state.BackupCD(env.BackupCD, cdBytes); err != nil {
		env.Logf("warn", "could not backup central directory: %v", err)
	}

	report.Duration = time.Since(start)
	printReport(env, report, start)
	env.Logf("info", "sync completed in %s", report.Duration)
	return nil
}

func loadExistingIndex(path string) (map[string]*zipx.Entry, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*zipx.Entry{}, nil
		}
		return nil, err
	}
	if st.Size() == 0 {
		return map[string]*zipx.Entry{}, nil
	}
	fr, err := zipx.OpenFileRead(path)
	if err != nil {
		return nil, err
	}
	defer fr.Close()
	idx := make(map[string]*zipx.Entry)
	for _, e := range fr.Entries {
		key := app.NormalizeKey(e.Name)
		if _, ok := idx[key]; !ok {
			idx[key] = e
		}
	}
	return idx, nil
}

func discoverIncoming(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func shouldSkip(env *app.Env, e *zipx.Entry) bool {
	// Use path (forward slashes) because ZIP entry names always use '/'.
	base := path.Base(e.Name)
	for _, pattern := range env.Policy.SkipNames {
		if matched, _ := path.Match(pattern, base); matched {
			return true
		}
	}
	if !env.Settings.KeepMetadataSidecars {
		if base == "archive_browser.html" || base == "index.html" {
			return true
		}
	}
	return false
}

func findVersion(existing map[string]*zipx.Entry, plainName string, crc uint32, size uint64) (match *zipx.Entry, next int, found bool) {
	key := app.NormalizeKey(plainName)
	if e, ok := existing[key]; ok {
		if e.CRC32 == crc && e.UncompressedSize == size {
			return e, 1, true
		}
	}
	next = 2
	for {
		vkey := app.NormalizeKey(app.InsertVersionSuffix(plainName, next))
		if e, ok := existing[vkey]; ok {
			if e.CRC32 == crc && e.UncompressedSize == size {
				return e, next, true
			}
			next++
			continue
		}
		break
	}
	return nil, next, false
}

func readTailCD(archivePath string, eocd *zipx.EOCD) ([]byte, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, eocd.CDSize)
	if _, err := f.ReadAt(buf, int64(eocd.CDOffset)); err != nil {
		return nil, err
	}
	return buf, nil
}

func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("cannot acquire lock (another instance running?): %w", err)
	}
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
	return f, nil
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
}

func printReport(env *app.Env, r *Report, start time.Time) {
	d := time.Since(start)
	env.Summary("TakeOutBack %s", app.Version)
	env.Summary("Archives scanned : %d", r.ArchivesScanned)
	env.Summary("Files scanned    : %d", r.FilesScanned)
	env.Summary("New files        : %d", r.NewFiles)
	env.Summary("Modified files   : %d", r.ModifiedFiles)
	env.Summary("Skipped files    : %d", r.SkippedFiles)
	env.Summary("Bytes appended   : %s", humanSize(r.BytesAppended))
	env.Summary("Duration         : %s", formatDuration(d))
	env.Summary("Status           : OK")
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// recoverArchive validates and, if necessary, repairs the consolidated archive.
func recoverArchive(env *app.Env) error {
	st, err := os.Stat(env.ArchiveZip)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Size() == 0 {
		return nil
	}

	f, err := os.Open(env.ArchiveZip)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = zipx.FindEOCD(f)
	if err == nil {
		return nil
	}

	env.Logf("warn", "archive EOCD missing or corrupt: %v", err)

	// Try to restore using sidecar truncation.
	if s, err := state.Load(env.StatePath); err == nil && s.ArchiveEnd > 0 && s.ArchiveEnd <= st.Size() {
		env.Logf("info", "truncating archive to last known good size %d", s.ArchiveEnd)
		_ = f.Close()
		if err := os.Truncate(env.ArchiveZip, s.ArchiveEnd); err != nil {
			return err
		}
		check, err := os.Open(env.ArchiveZip)
		if err != nil {
			return err
		}
		_, err2 := zipx.FindEOCD(check)
		_ = check.Close()
		if err2 == nil {
			return nil
		}
	}

	// Fallback: full LFH scan and rebuild CD.
	env.Log("warn", "falling back to full local-header scan")
	entries, err := zipx.ScanLocalHeaders(env.ArchiveZip)
	if err != nil {
		return err
	}
	env.Logf("info", "rebuilt index from %d local headers", len(entries))

	if len(entries) == 0 {
		return os.Truncate(env.ArchiveZip, 0)
	}

	dst, err := zipx.OpenOrCreate(env.ArchiveZip + ".rebuild")
	if err != nil {
		return err
	}
	for _, e := range entries {
		src, err := os.Open(env.ArchiveZip)
		if err != nil {
			_ = dst.Close()
			return err
		}
		_, err = zipx.CopyRawEntry(dst, src, e)
		_ = src.Close()
		if err != nil {
			_ = dst.Close()
			return err
		}
	}
	if _, err := zipx.WriteCentralDir(dst, entries); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := os.Rename(env.ArchiveZip+".rebuild", env.ArchiveZip); err != nil {
		return err
	}
	return nil
}

// Verify checks archive integrity.
func Verify(env *app.Env, args []string) error {
	st, err := os.Stat(env.ArchiveZip)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("archive does not exist")
		}
		return err
	}
	if st.Size() == 0 {
		env.Summary("Archive is empty")
		return nil
	}
	fr, err := zipx.OpenFileRead(env.ArchiveZip)
	if err != nil {
		return err
	}
	defer fr.Close()

	deep := false
	for _, a := range args {
		if a == "--deep" {
			deep = true
		}
	}

	var errors []string
	checked := 0
	for _, e := range fr.Entries {
		checked++
		if err := verifyEntry(fr.File, e, deep); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", e.Name, err))
			env.Logf("error", "verify %s: %v", e.Name, err)
		}
	}

	env.Summary("Entries checked: %d", checked)
	env.Summary("Errors found:    %d", len(errors))
	if len(errors) > 0 {
		for _, e := range errors {
			env.Summary("  %s", e)
		}
		return fmt.Errorf("verify failed with %d errors", len(errors))
	}
	env.Summary("Status:          OK")
	return nil
}

func verifyEntry(f *os.File, e *zipx.Entry, deep bool) error {
	_, dataOff, err := zipx.ReadLocalHeader(f, int64(e.LocalHeaderOff))
	if err != nil {
		return err
	}
	if !deep {
		// Metadata-only check: ensure the payload region exists.
		end := dataOff + int64(e.CompressedSize)
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if end > st.Size() {
			return fmt.Errorf("payload extends past EOF")
		}
		return nil
	}
	r := io.NewSectionReader(f, dataOff, int64(e.CompressedSize))
	var reader io.Reader = r
	if e.Method == zipx.MethodDeflate {
		reader = flate.NewReader(r)
	}
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, reader); err != nil {
		if c, ok := reader.(io.ReadCloser); ok {
			_ = c.Close()
		}
		return err
	}
	if c, ok := reader.(io.ReadCloser); ok {
		if err := c.Close(); err != nil {
			return err
		}
	}
	if h.Sum32() != e.CRC32 {
		return fmt.Errorf("CRC mismatch: got %08x want %08x", h.Sum32(), e.CRC32)
	}
	return nil
}

// Stats prints archive statistics.
func Stats(env *app.Env, args []string) error {
	st, err := os.Stat(env.ArchiveZip)
	if err != nil {
		if os.IsNotExist(err) {
			env.Summary("Archive does not exist yet")
			return nil
		}
		return err
	}
	fr, err := zipx.OpenFileRead(env.ArchiveZip)
	if err != nil {
		return err
	}
	defer fr.Close()

	var totalComp, totalUncomp int64
	versions := map[string]int{}
	for _, e := range fr.Entries {
		totalComp += int64(e.CompressedSize)
		totalUncomp += int64(e.UncompressedSize)
		key := versionKey(e.Name)
		versions[key]++
	}

	env.Summary("TakeOutBack statistics")
	env.Summary("Archive size:       %s", humanSize(st.Size()))
	env.Summary("Entries:            %d", len(fr.Entries))
	env.Summary("Unique paths:       %d", len(versions))
	env.Summary("Compressed total:   %s", humanSize(totalComp))
	env.Summary("Uncompressed total: %s", humanSize(totalUncomp))
	env.Summary("Ratio:              %.1f%%", ratio(totalComp, totalUncomp))
	return nil
}

func versionKey(name string) string {
	s := strings.ToLower(name)
	if idx := strings.LastIndex(s, "__v"); idx > 0 {
		base := s[:idx]
		if dot := strings.LastIndex(s, "."); dot > idx {
			return base + s[dot:]
		}
		return base
	}
	return s
}

func ratio(comp, uncomp int64) float64 {
	if uncomp == 0 {
		return 0
	}
	return float64(comp) * 100.0 / float64(uncomp)
}

// Compact rewrites the archive removing dead central directory blocks.
func Compact(env *app.Env, args []string) error {
	st, err := os.Stat(env.ArchiveZip)
	if err != nil {
		if os.IsNotExist(err) {
			env.Summary("Archive does not exist")
			return nil
		}
		return err
	}
	if st.Size() == 0 {
		env.Summary("Archive is empty")
		return nil
	}

	fr, err := zipx.OpenFileRead(env.ArchiveZip)
	if err != nil {
		return err
	}
	defer fr.Close()

	tmp := env.ArchiveZip + ".compact"
	dst, err := zipx.OpenOrCreate(tmp)
	if err != nil {
		return err
	}
	for _, e := range fr.Entries {
		_, err := zipx.CopyRawEntry(dst, fr.File, e)
		if err != nil {
			_ = dst.Close()
			return err
		}
	}
	if _, err := zipx.WriteCentralDir(dst, fr.Entries); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, env.ArchiveZip); err != nil {
		return err
	}
	st2, err := os.Stat(env.ArchiveZip)
	if err != nil {
		return err
	}
	_ = os.Remove(env.StatePath)
	_ = os.Remove(env.BackupCD)
	env.Summary("Compacted archive: %s -> %s", humanSize(st.Size()), humanSize(st2.Size()))
	return nil
}
