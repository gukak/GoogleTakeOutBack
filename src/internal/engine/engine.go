// Package engine implements the core takeoutback commands: sync, verify,
// stats, compact and startup recovery.
package engine

import (
	"bufio"
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
	"syscall"
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
	yesFlag := false
	forceFlag := false
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
		if args[i] == "--yes" {
			yesFlag = true
			i++
			continue
		}
		if args[i] == "--force" {
			forceFlag = true
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

	currentArchive, err := env.CurrentArchive()
	if err != nil {
		return fmt.Errorf("cannot locate current archive: %w", err)
	}

	incomingFiles, err := discoverIncoming(incomingDir)
	if err != nil {
		return err
	}
	if incomingDir != env.Incoming {
		env.Logf("info", "using custom incoming directory: %s", incomingDir)
	}

	plan, err := buildSyncPlan(env, currentArchive, incomingFiles)
	if err != nil {
		return fmt.Errorf("cannot evaluate disk space: %w", err)
	}
	printPlan(env, plan, incomingDir)

	if !plan.HasEnoughSpace {
		if forceFlag {
			env.Logf("warn", "disk space estimate reports insufficient space; continuing because --force was given")
			fmt.Println("Warning: disk space estimate reports insufficient space. Continuing because --force was given.")
		} else {
			return fmt.Errorf("not enough free space; backup cancelled")
		}
	}

	if !yesFlag && !forceFlag {
		ok, err := confirm("Proceed with backup? [y/N] ")
		if err != nil {
			return fmt.Errorf("cannot read confirmation: %w", err)
		}
		if !ok {
			return fmt.Errorf("backup cancelled by user")
		}
	}

	lockFile, err := acquireLock(env.LockPath)
	if err != nil {
		return err
	}
	defer releaseLock(lockFile)

	runDir, err := makeTempRunDir(env.TempDir)
	if err != nil {
		return fmt.Errorf("cannot create temp run directory: %w", err)
	}
	defer os.RemoveAll(runDir)

	// Ensure Archive/ only contains final Consolidated and Added zips.
	cleanupArchiveTempFiles(env.Archive)

	report := &Report{ArchivesScanned: len(incomingFiles)}

	// Backup the current consolidated archive before modifying anything.
	if currentArchive != "" {
		if err := backupArchive(env, currentArchive); err != nil {
			return fmt.Errorf("cannot backup current archive: %w", err)
		}
		if err := rotateBackups(env.Backup, 5); err != nil {
			env.Logf("warn", "backup rotation failed: %v", err)
		}
	}

	if err := recoverArchive(env, currentArchive, runDir); err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}

	existing, existingEntries, err := loadExistingIndex(currentArchive)
	if err != nil {
		return err
	}

	printArchiveList(incomingFiles)

	isInitial := currentArchive == ""
	ts := time.Now()
	newArchiveName := timestampName("Consolidated", ts)
	newArchivePath := uniqueArchivePath(env.Archive, newArchiveName)
	newArchiveTmp := filepath.Join(runDir, newArchiveName+".tmp")

	newDst, err := zipx.OpenOrCreate(newArchiveTmp)
	if err != nil {
		return fmt.Errorf("cannot create new archive: %w", err)
	}

	var addedDst *os.File
	var addedArchivePath, addedArchiveTmp string
	var addedEntries []*zipx.Entry
	if !isInitial {
		addedArchiveName := timestampName("Added", ts)
		addedArchivePath = uniqueArchivePath(env.Archive, addedArchiveName)
		addedArchiveTmp = filepath.Join(runDir, addedArchiveName+".tmp")
		addedDst, err = zipx.OpenOrCreate(addedArchiveTmp)
		if err != nil {
			_ = newDst.Close()
			cleanupSync(newArchiveTmp)
			return fmt.Errorf("cannot create added archive: %w", err)
		}
	}

	var allEntries []*zipx.Entry
	allEntries = append(allEntries, existingEntries...)

	// Copy existing entries into the new archive first.
	if currentArchive != "" {
		oldFile, err := os.Open(currentArchive)
		if err != nil {
			cleanupSync(newArchiveTmp, addedArchiveTmp)
			return fmt.Errorf("cannot open current archive: %w", err)
		}
		for _, e := range existingEntries {
			if _, err := zipx.CopyRawEntry(newDst, oldFile, e); err != nil {
				_ = oldFile.Close()
				cleanupSync(newArchiveTmp, addedArchiveTmp)
				return fmt.Errorf("cannot copy existing entry %s: %w", e.Name, err)
			}
		}
		_ = oldFile.Close()
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
			var written *zipx.Entry
			if head, ok := existing[key]; ok {
				_, next, found := findVersion(existing, head.Name, e.CRC32, e.UncompressedSize)
				if found {
					report.SkippedFiles++
					continue
				}
				e.Name = app.InsertVersionSuffix(head.Name, next)
				written, err = zipx.CopyRawEntry(newDst, src.File, e)
				if err != nil {
					env.Logf("warn", "cannot append %s: %v", e.Name, err)
					continue
				}
				report.ModifiedFiles++
			} else {
				written, err = zipx.CopyRawEntry(newDst, src.File, e)
				if err != nil {
					env.Logf("warn", "cannot append %s: %v", e.Name, err)
					continue
				}
				report.NewFiles++
			}
			report.BytesAppended += int64(written.CompressedSize)
			allEntries = append(allEntries, written)
			existing[app.NormalizeKey(written.Name)] = written
			appended = true

			// Copy the same entry to the Added archive only for subsequent imports.
			if addedDst != nil {
				added, err := zipx.CopyRawEntry(addedDst, src.File, e)
				if err != nil {
					env.Logf("warn", "cannot copy added entry %s: %v", e.Name, err)
				} else {
					addedEntries = append(addedEntries, added)
				}
			}
			env.Logf("info", "appended %s (%d bytes compressed)", written.Name, written.CompressedSize)
		}
		bar.finish()
		_ = src.Close()
	}

	if !appended {
		_ = newDst.Close()
		if addedDst != nil {
			_ = addedDst.Close()
		}
		cleanupSync(newArchiveTmp, addedArchiveTmp)
		env.Log("info", "no new or modified files to append")
		printReport(env, report, start)
		return nil
	}

	sort.SliceStable(allEntries, func(i, j int) bool {
		return allEntries[i].LocalHeaderOff < allEntries[j].LocalHeaderOff
	})

	// Write the new consolidated archive.
	eocd, err := zipx.WriteCentralDir(newDst, allEntries)
	if err != nil {
		cleanupSync(newArchiveTmp, addedArchiveTmp)
		return fmt.Errorf("write central directory: %w", err)
	}
	if err := newDst.Sync(); err != nil {
		cleanupSync(newArchiveTmp, addedArchiveTmp)
		return err
	}
	if err := newDst.Close(); err != nil {
		cleanupSync(newArchiveTmp, addedArchiveTmp)
		return err
	}

	// Write the Added archive only for subsequent imports.
	if addedDst != nil {
		if err := writeAddedArchive(addedDst, addedEntries); err != nil {
			cleanupSync(newArchiveTmp, addedArchiveTmp)
			return fmt.Errorf("write added archive: %w", err)
		}
	}

	// Move temporary files to their final timestamped names.
	if err := os.Rename(newArchiveTmp, newArchivePath); err != nil {
		cleanupSync(newArchiveTmp, addedArchiveTmp)
		return fmt.Errorf("rename new archive: %w", err)
	}
	if addedDst != nil {
		if err := os.Rename(addedArchiveTmp, addedArchivePath); err != nil {
			_ = os.Remove(newArchivePath)
			cleanupSync(newArchiveTmp, addedArchiveTmp)
			return fmt.Errorf("rename added archive: %w", err)
		}
	}

	// The old consolidated archive is now superseded; remove it from Archive
	// because a copy already lives in Backup.
	if currentArchive != "" {
		_ = os.Remove(currentArchive)
	}

	cdBytes, err := readTailCD(newArchivePath, eocd)
	if err != nil {
		return err
	}

	s := state.New(filepath.Base(newArchivePath), app.Version)
	s.ArchiveEnd = eocd.Offset + zipx.EOCDSize
	if eocd.HasZip64 {
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
	env.Summary("Archive: %s", newArchivePath)
	if addedArchivePath != "" {
		env.Summary("Added:   %s", addedArchivePath)
	}
	env.Logf("info", "sync completed in %s, archive=%s", report.Duration, newArchivePath)
	return nil
}

func loadExistingIndex(path string) (map[string]*zipx.Entry, []*zipx.Entry, error) {
	emptyIdx := map[string]*zipx.Entry{}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyIdx, nil, nil
		}
		return nil, nil, err
	}
	if st.Size() == 0 {
		return emptyIdx, nil, nil
	}
	fr, err := zipx.OpenFileRead(path)
	if err != nil {
		return nil, nil, err
	}
	defer fr.Close()
	idx := make(map[string]*zipx.Entry)
	for _, e := range fr.Entries {
		key := app.NormalizeKey(e.Name)
		if _, ok := idx[key]; !ok {
			idx[key] = e
		}
	}
	return idx, fr.Entries, nil
}

func discoverIncoming(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func timestampName(prefix string, t time.Time) string {
	return fmt.Sprintf("%s-%s.zip", prefix, t.Local().Format("20060102-150405.000"))
}

func uniqueArchivePath(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	base := strings.TrimSuffix(name, ".zip")
	for i := 1; i < 1000; i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s_%d.zip", base, i))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return filepath.Join(dir, name)
}

// makeTempRunDir creates a fresh per-execution temp directory under parent and
// removes any leftover run-* directories from previous interrupted runs.
func makeTempRunDir(parent string) (string, error) {
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "run-") {
			_ = os.RemoveAll(filepath.Join(parent, e.Name()))
		}
	}
	base := filepath.Join(parent, fmt.Sprintf("run-%s", time.Now().Local().Format("20060102-150405.000")))
	runDir := base
	for i := 1; i < 1000; i++ {
		if _, err := os.Stat(runDir); os.IsNotExist(err) {
			break
		}
		runDir = fmt.Sprintf("%s_%d", base, i)
	}
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return "", err
	}
	return runDir, nil
}

// cleanupArchiveTempFiles removes any leftover temporary files from the Archive
// directory. After cleanup only final Consolidated and Added zips should remain.
func cleanupArchiveTempFiles(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".rebuild") || strings.HasSuffix(name, ".compact") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func backupArchive(env *app.Env, src string) error {
	if src == "" {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dst := uniqueArchivePath(env.Backup, filepath.Base(src))
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func rotateBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "Consolidated-") && strings.HasSuffix(name, ".zip") {
			files = append(files, e)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() > files[j].Name() // newest first
	})
	for i := keep; i < len(files); i++ {
		_ = os.Remove(filepath.Join(dir, files[i].Name()))
	}
	return nil
}

func cleanupSync(paths ...string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

func writeAddedArchive(dst *os.File, entries []*zipx.Entry) error {
	if len(entries) == 0 {
		_ = dst.Close()
		cleanupSync(dst.Name())
		return nil
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].LocalHeaderOff < entries[j].LocalHeaderOff
	})
	if _, err := zipx.WriteCentralDir(dst, entries); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
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
	if err == nil {
		_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
		return f, nil
	}
	if !os.IsExist(err) {
		return nil, fmt.Errorf("cannot acquire lock: %w", err)
	}

	// Lock file already exists: check whether it is stale.
	data, err := os.ReadFile(path)
	if err == nil {
		pidStr := strings.TrimSpace(string(data))
		pid, _ := strconv.Atoi(pidStr)
		if pid > 0 && processExists(pid) {
			return nil, fmt.Errorf("cannot acquire lock (another instance running with PID %d?)", pid)
		}
	}
	// Stale lock: remove and retry once.
	_ = os.Remove(path)
	f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("cannot acquire lock after removing stale lock: %w", err)
	}
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
	return f, nil
}

func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
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

// syncPlan estimates the space required for a sync operation per disk.
type syncPlan struct {
	IncomingCount       int
	IncomingSize        int64
	ExistingArchiveSize int64
	ExistingAddedSize   int64
	RequiredByDisk      map[string]diskRequirement
	HasEnoughSpace      bool
}

type diskRequirement struct {
	Path     string
	Free     uint64
	Required int64
}

// sameDisk reports whether two paths are on the same filesystem.
func sameDisk(a, b string) bool {
	ka, err := diskKey(a)
	if err != nil {
		return false
	}
	kb, err := diskKey(b)
	if err != nil {
		return false
	}
	return ka == kb
}

// buildSyncPlan evaluates the peak space required for the sync per disk.
// The incoming ZIP files are only read, so the incoming disk is not counted.
// The estimate takes into account which directories (Archive, Backup, Temp)
// share the same filesystem.
func buildSyncPlan(env *app.Env, currentArchive string, incomingFiles []string) (*syncPlan, error) {
	plan := &syncPlan{
		IncomingCount:  len(incomingFiles),
		RequiredByDisk: make(map[string]diskRequirement),
	}
	for _, p := range incomingFiles {
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cannot stat incoming archive %s: %w", p, err)
		}
		plan.IncomingSize += st.Size()
	}
	if currentArchive != "" {
		st, err := os.Stat(currentArchive)
		if err != nil {
			return nil, fmt.Errorf("cannot stat current archive %s: %w", currentArchive, err)
		}
		plan.ExistingArchiveSize = st.Size()
	}
	addedFiles, err := filepath.Glob(filepath.Join(env.Archive, "Added-*.zip"))
	if err != nil {
		return nil, fmt.Errorf("cannot list added archives: %w", err)
	}
	for _, p := range addedFiles {
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cannot stat added archive %s: %w", p, err)
		}
		plan.ExistingAddedSize += st.Size()
	}

	const buffer = 100 * 1024 * 1024 // 100 MiB safety margin

	isInitial := plan.ExistingArchiveSize == 0
	newAddedSize := int64(0)
	if !isInitial {
		newAddedSize = plan.IncomingSize
	}
	newConsolidatedSize := plan.ExistingArchiveSize + plan.IncomingSize
	// Temp file: the new consolidated archive plus a possible Added archive.
	tempSize := newConsolidatedSize + newAddedSize
	// Final files in Archive: the new consolidated archive plus existing Added
	// archives plus a possible new Added archive.
	finalSize := newConsolidatedSize + plan.ExistingAddedSize + newAddedSize
	// Base: what already exists in Archive before the operation starts.
	baseSize := plan.ExistingArchiveSize + plan.ExistingAddedSize

	// Archive disk peak. If Temp is on the same filesystem, the temporary file is
	// created there (and then renamed). The peak is the existing Archive content
	// plus the temporary file. If Temp is elsewhere, the peak is the existing
	// content plus the final files.
	archiveNeed := baseSize
	if sameDisk(env.TempDir, env.Archive) {
		archiveNeed += tempSize
	} else {
		archiveNeed += finalSize
	}
	// If Backup is also on the Archive filesystem, account for the backup copy.
	if plan.ExistingArchiveSize > 0 && sameDisk(env.Backup, env.Archive) {
		archiveNeed += plan.ExistingArchiveSize
	}
	if err := addDiskRequirement(plan, env.Archive, archiveNeed, buffer); err != nil {
		return nil, err
	}

	// Backup disk: copy of the current consolidated archive (unless already counted
	// because it shares the Archive filesystem).
	if plan.ExistingArchiveSize > 0 && !sameDisk(env.Backup, env.Archive) {
		backupNeed := plan.ExistingArchiveSize
		if err := addDiskRequirement(plan, env.Backup, backupNeed, buffer); err != nil {
			return nil, err
		}
	}

	// Temp disk: the temporary files, unless Temp is on the Archive filesystem.
	if !sameDisk(env.TempDir, env.Archive) {
		tempNeed := tempSize
		if err := addDiskRequirement(plan, env.TempDir, tempNeed, buffer); err != nil {
			return nil, err
		}
	}

	plan.HasEnoughSpace = true
	for _, req := range plan.RequiredByDisk {
		if req.Required > int64(req.Free) {
			plan.HasEnoughSpace = false
		}
	}
	return plan, nil
}

func addDiskRequirement(plan *syncPlan, path string, required int64, buffer int64) error {
	key, err := diskKey(path)
	if err != nil {
		return fmt.Errorf("cannot identify disk for %s: %w", path, err)
	}
	req := plan.RequiredByDisk[key]
	if req.Path == "" {
		req.Path = path
		free, err := freeSpace(path)
		if err != nil {
			return fmt.Errorf("cannot read free space for %s: %w", path, err)
		}
		req.Free = free
		req.Required += buffer
	}
	req.Required += required
	plan.RequiredByDisk[key] = req
	return nil
}

func printPlan(env *app.Env, plan *syncPlan, incomingDir string) {
	fmt.Println("Sync plan:")
	fmt.Printf("  Incoming directory: %s\n", incomingDir)
	fmt.Printf("  Incoming archives: %d, total size: %s\n", plan.IncomingCount, humanSize(plan.IncomingSize))
	fmt.Printf("  Archive directory:  %s\n", env.Archive)
	fmt.Printf("  Backup directory:   %s\n", env.Backup)
	fmt.Printf("  Temp directory:     %s\n", env.TempDir)
	if plan.ExistingArchiveSize > 0 {
		fmt.Printf("  Existing consolidated archive: %s\n", humanSize(plan.ExistingArchiveSize))
	} else {
		fmt.Println("  No existing consolidated archive (initial import)")
	}
	if plan.ExistingAddedSize > 0 {
		fmt.Printf("  Existing added archives: %s\n", humanSize(plan.ExistingAddedSize))
	}
	fmt.Println("  Estimated peak space required by disk:")
	for _, req := range plan.RequiredByDisk {
		status := "OK"
		if req.Required > int64(req.Free) {
			status = "INSUFFICIENT"
		}
		fmt.Printf("    %s: required %s, free %s [%s]\n", req.Path, humanSize(req.Required), humanSize(int64(req.Free)), status)
	}
	if plan.HasEnoughSpace {
		fmt.Println("  Status: OK")
	} else {
		fmt.Println("  Status: INSUFFICIENT SPACE")
	}
}

func confirm(prompt string) (bool, error) {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes", nil
}

// recoverArchive validates and, if necessary, repairs the consolidated archive.
// tempDir is a per-execution temporary directory; any rebuilt archive is written
// there and then renamed over archivePath.
func recoverArchive(env *app.Env, archivePath, tempDir string) error {
	if archivePath == "" {
		return nil
	}
	st, err := os.Stat(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Size() == 0 {
		return nil
	}

	f, err := os.Open(archivePath)
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
		if err := os.Truncate(archivePath, s.ArchiveEnd); err != nil {
			return err
		}
		check, err := os.Open(archivePath)
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
	entries, err := zipx.ScanLocalHeaders(archivePath)
	if err != nil {
		return err
	}
	env.Logf("info", "rebuilt index from %d local headers", len(entries))

	if len(entries) == 0 {
		return os.Truncate(archivePath, 0)
	}

	rebuildTmp := filepath.Join(tempDir, filepath.Base(archivePath)+".rebuild")
	dst, err := zipx.OpenOrCreate(rebuildTmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		src, err := os.Open(archivePath)
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
	if err := os.Rename(rebuildTmp, archivePath); err != nil {
		return err
	}
	return nil
}

// Verify checks archive integrity.
func Verify(env *app.Env, args []string) error {
	archivePath, err := env.CurrentArchive()
	if err != nil {
		return fmt.Errorf("cannot locate current archive: %w", err)
	}
	if archivePath == "" {
		env.Summary("Archive does not exist yet")
		return nil
	}
	st, err := os.Stat(archivePath)
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
	fr, err := zipx.OpenFileRead(archivePath)
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
	archivePath, err := env.CurrentArchive()
	if err != nil {
		return fmt.Errorf("cannot locate current archive: %w", err)
	}
	if archivePath == "" {
		env.Summary("Archive does not exist yet")
		return nil
	}
	st, err := os.Stat(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			env.Summary("Archive does not exist yet")
			return nil
		}
		return err
	}
	fr, err := zipx.OpenFileRead(archivePath)
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

// Compact rewrites the current archive to remove any dead central directory
// blocks. Because the archive is fully rewritten on every sync, this command is
// rarely needed; it is kept for manual repair.
func Compact(env *app.Env, args []string) error {
	archivePath, err := env.CurrentArchive()
	if err != nil {
		return fmt.Errorf("cannot locate current archive: %w", err)
	}
	if archivePath == "" {
		env.Summary("Archive does not exist")
		return nil
	}
	st, err := os.Stat(archivePath)
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

	runDir, err := makeTempRunDir(env.TempDir)
	if err != nil {
		return fmt.Errorf("cannot create temp run directory: %w", err)
	}
	defer os.RemoveAll(runDir)

	fr, err := zipx.OpenFileRead(archivePath)
	if err != nil {
		return err
	}
	defer fr.Close()

	tmp := filepath.Join(runDir, filepath.Base(archivePath)+".compact")
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
	if err := os.Rename(tmp, archivePath); err != nil {
		return err
	}
	st2, err := os.Stat(archivePath)
	if err != nil {
		return err
	}
	_ = os.Remove(env.StatePath)
	_ = os.Remove(env.BackupCD)
	env.Summary("Compacted archive: %s -> %s", humanSize(st.Size()), humanSize(st2.Size()))
	return nil
}
