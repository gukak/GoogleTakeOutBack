// Package engine implements the core takeoutback commands: sync, verify,
// stats, compact and startup recovery.
package engine

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	Errors          int
	BytesAppended   int64
	Duration        time.Duration
	ErrorDetails    []string
}

// Sync runs the consolidation process.
//
// The implementation is append-only: existing payloads are never rewritten.
// New and modified entries are appended at the end of the current consolidated
// archive, then a fresh central directory is written after them. The archive is
// finally renamed to a new timestamped name.
func Sync(env *app.Env, args []string) error {
	start := time.Now()
	env.Logf("info", "starting sync, version=%s", app.Version)

	incomingDir := env.Incoming
	noBackup := false
	noAdded := false
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
		if args[i] == "--no-backup" {
			noBackup = true
			i++
			continue
		}
		if args[i] == "--no-added" {
			noAdded = true
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

	if currentArchive != "" {
		if err := checkArchiveIntegrity(currentArchive); err != nil {
			fmt.Printf("Warning: current archive integrity check failed: %v\n", err)
			if !confirm("Continue anyway? (y/N): ") {
				return fmt.Errorf("sync aborted by user")
			}
		}
	}

	incomingFiles, err := discoverIncoming(incomingDir)
	if err != nil {
		return err
	}
	if incomingDir != env.Incoming {
		env.Logf("info", "using custom incoming directory: %s", incomingDir)
	}

	printArchiveList(incomingFiles)

	lockFile, err := acquireLock(env.LockPath)
	if err != nil {
		return err
	}
	defer releaseLock(lockFile)

	report := &Report{ArchivesScanned: len(incomingFiles)}

	// Backup the current consolidated archive before modifying anything.
	if !noBackup && currentArchive != "" {
		if err := backupArchive(env, currentArchive); err != nil {
			return fmt.Errorf("cannot backup current archive: %w", err)
		}
		if err := rotateBackups(env.Backup, 5); err != nil {
			env.Logf("warn", "backup rotation failed: %v", err)
		}
	}

	if err := recoverArchive(env, currentArchive); err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}

	var existing map[string]*zipx.Entry
	var existingEntries []*zipx.Entry
	if currentArchive != "" {
		fmt.Println("Loading existing consolidated archive...")
		existing, existingEntries, err = loadExistingIndex(currentArchive)
		if err != nil {
			return err
		}
		fmt.Printf("Loaded %d existing entries.\n", len(existingEntries))
	} else {
		existing = map[string]*zipx.Entry{}
	}

	// Build a canonical-JSON index of existing Google Photos metadata sidecars.
	// Takeout sometimes re-encodes these small JSON files with different
	// whitespace or key order, so byte/CRC comparison falsely treats them as
	// modified. We compare them semantically. Some Takeout exports also truncate
	// long sidecar names, so we additionally deduplicate by content hash.
	var existingMetadataIdx map[string][]byte
	var existingMetadataSet map[string]struct{}
	if currentArchive != "" {
		existingMetadataIdx, existingMetadataSet, err = buildMetadataIndex(currentArchive)
		if err != nil {
			env.Logf("warn", "cannot build metadata index: %v", err)
		}
	}

	isInitial := currentArchive == ""
	ts := time.Now()
	newArchiveName := timestampName("Consolidated", ts)
	newArchivePath := uniqueArchivePath(env.Archive, newArchiveName)

	// Nothing to import: rotate the timestamp without touching payloads.
	if len(incomingFiles) == 0 {
		return finalizeEmptySync(env, currentArchive, newArchivePath, existingEntries, report, start)
	}

	// Prepare Added archive for subsequent imports (unless disabled).
	var addedDst *os.File
	var addedArchivePath string
	var addedEntries []*zipx.Entry
	if !isInitial && !noAdded {
		addedArchiveName := timestampName("Added", ts)
		addedArchivePath = uniqueArchivePath(env.Archive, addedArchiveName)
		addedDst, err = zipx.OpenOrCreate(addedArchivePath)
		if err != nil {
			return fmt.Errorf("cannot create added archive: %w", err)
		}
	}

	// Preserve the current archive's valid state before modifying it.
	// If the sync is interrupted, recoverArchive can use this to restore the
	// archive to its pre-sync state without duplicating payloads.
	if currentArchive != "" {
		if err := preserveCurrentState(env, currentArchive); err != nil {
			if addedDst != nil {
				_ = addedDst.Close()
				_ = os.Remove(addedArchivePath)
			}
			return fmt.Errorf("cannot preserve current archive state: %w", err)
		}
	}

	// Open the consolidated archive for appending. For the initial import,
	// create a fresh archive.
	var archiveDst *os.File
	if currentArchive != "" {
		archiveDst, err = os.OpenFile(currentArchive, os.O_RDWR|os.O_APPEND, 0644)
		if err != nil {
			if addedDst != nil {
				_ = addedDst.Close()
				_ = os.Remove(addedArchivePath)
			}
			return fmt.Errorf("cannot open current archive: %w", err)
		}
	} else {
		archiveDst, err = zipx.OpenOrCreate(newArchivePath)
		if err != nil {
			if addedDst != nil {
				_ = addedDst.Close()
				_ = os.Remove(addedArchivePath)
			}
			return fmt.Errorf("cannot create new archive: %w", err)
		}
	}

	allEntries := make([]*zipx.Entry, len(existingEntries))
	copy(allEntries, existingEntries)

	appended := false

	for _, path := range incomingFiles {
		src, err := zipx.OpenFileRead(path)
		if err != nil {
			env.Logf("warn", "skipping invalid archive %s: %v", path, err)
			fmt.Printf("Skipping invalid archive: %s\n", filepath.Base(path))
			continue
		}
		var totalBytes int64
		for _, e := range src.Entries {
			totalBytes += int64(e.CompressedSize)
		}
		bar := newByteProgressBar(totalBytes, filepath.Base(path))
		for _, e := range src.Entries {
			report.FilesScanned++
			if shouldSkip(env, e) {
				report.SkippedFiles++
				env.Logf("info", "SKIP policy: %s", e.Name)
				bar.add(int64(e.CompressedSize))
				continue
			}
			key := app.NormalizeKey(e.Name)
			var written *zipx.Entry
			if head, ok := existing[key]; ok {
				_, next, found := findVersion(existing, head.Name, e.CRC32, e.UncompressedSize)
				if !found && existingMetadataSet != nil && (isMetadataJSON(e.Name) || isJSONCandidate(e.Name)) {
					incomingData, rerr := readZipEntry(path, e.Name)
					if rerr == nil && looksLikeMetadataJSON(incomingData) {
						incomingCanon, rerr := canonicalJSON(incomingData)
						if rerr == nil {
							incomingHash := metadataHash(incomingCanon)
							if _, ok := existingMetadataSet[incomingHash]; ok {
								found = true
							} else if canon, ok := existingMetadataIdx[e.Name]; ok && bytes.Equal(incomingCanon, canon) {
								found = true
							}
							if found {
								existingMetadataSet[incomingHash] = struct{}{}
							}
						}
					}
				}
				if found {
					report.SkippedFiles++
					env.Logf("info", "SKIP identical: %s (crc=%08x size=%d)", e.Name, e.CRC32, e.UncompressedSize)
					bar.add(int64(e.CompressedSize))
					continue
				}
				if isMetadataJSON(e.Name) && existingMetadataIdx != nil {
					env.Logf("info", "MODIFIED metadata: %s existing(crc=%08x size=%d) incoming(crc=%08x size=%d)", head.Name, head.CRC32, head.UncompressedSize, e.CRC32, e.UncompressedSize)
				} else {
					env.Logf("info", "MODIFIED: %s existing(crc=%08x size=%d) incoming(crc=%08x size=%d)", head.Name, head.CRC32, head.UncompressedSize, e.CRC32, e.UncompressedSize)
				}
				e.Name = app.InsertVersionSuffix(head.Name, next)
				written, err = zipx.CopyRawEntry(archiveDst, src.File, e, bar.add)
				if err != nil {
					report.Errors++
					report.ErrorDetails = append(report.ErrorDetails, fmt.Sprintf("%s: %v", e.Name, err))
					env.Logf("warn", "cannot append %s: %v", e.Name, err)
					continue
				}
				report.ModifiedFiles++
			} else {
				// Content-based dedup for metadata JSONs whose names changed between
				// Takeout exports (e.g. truncated sidecar names).
				if existingMetadataSet != nil && (isMetadataJSON(e.Name) || isJSONCandidate(e.Name)) {
					incomingData, rerr := readZipEntry(path, e.Name)
					if rerr == nil && looksLikeMetadataJSON(incomingData) {
						incomingCanon, rerr := canonicalJSON(incomingData)
						if rerr == nil {
							incomingHash := metadataHash(incomingCanon)
							if _, ok := existingMetadataSet[incomingHash]; ok {
								report.SkippedFiles++
								env.Logf("info", "SKIP canonical metadata (renamed): %s (crc=%08x size=%d)", e.Name, e.CRC32, e.UncompressedSize)
								existingMetadataSet[incomingHash] = struct{}{}
								bar.add(int64(e.CompressedSize))
								continue
							}
						}
					}
				}
				env.Logf("info", "NEW: %s (crc=%08x size=%d)", e.Name, e.CRC32, e.UncompressedSize)
				written, err = zipx.CopyRawEntry(archiveDst, src.File, e, bar.add)
				if err != nil {
					report.Errors++
					report.ErrorDetails = append(report.ErrorDetails, fmt.Sprintf("%s: %v", e.Name, err))
					env.Logf("warn", "cannot append %s: %v", e.Name, err)
					continue
				}
				report.NewFiles++
			}
			report.BytesAppended += int64(written.CompressedSize)
			allEntries = append(allEntries, written)
			existing[app.NormalizeKey(written.Name)] = written
			appended = true

			// Keep the metadata index up to date so later entries in the same sync
			// can be deduplicated by content hash even if their names differ.
			if existingMetadataIdx != nil && (isMetadataJSON(written.Name) || isJSONCandidate(written.Name)) {
				if data, rerr := readZipEntry(path, e.Name); rerr == nil {
					if looksLikeMetadataJSON(data) {
						if canon, rerr := canonicalJSON(data); rerr == nil {
							existingMetadataIdx[written.Name] = canon
							existingMetadataSet[metadataHash(canon)] = struct{}{}
						}
					}
				}
			}

			// Copy the same entry to the Added archive only for subsequent imports.
			if addedDst != nil {
				added, err := zipx.CopyRawEntry(addedDst, src.File, e, nil)
				if err != nil {
					report.ErrorDetails = append(report.ErrorDetails, fmt.Sprintf("added-copy %s: %v", e.Name, err))
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
		_ = archiveDst.Close()
		if addedDst != nil {
			_ = addedDst.Close()
			_ = os.Remove(addedArchivePath)
		}
		if currentArchive != "" {
			return finalizeEmptySync(env, currentArchive, newArchivePath, existingEntries, report, start)
		}
		_ = os.Remove(newArchivePath)
		report.Duration = time.Since(start)
		printReport(env, report, start)
		return nil
	}

	sort.SliceStable(allEntries, func(i, j int) bool {
		return allEntries[i].LocalHeaderOff < allEntries[j].LocalHeaderOff
	})

	// Append the new central directory at the end of the consolidated archive.
	eocd, err := zipx.WriteCentralDir(archiveDst, allEntries)
	if err != nil {
		_ = archiveDst.Close()
		if addedDst != nil {
			_ = addedDst.Close()
			_ = os.Remove(addedArchivePath)
		}
		return fmt.Errorf("write central directory: %w", err)
	}
	if err := archiveDst.Sync(); err != nil {
		_ = archiveDst.Close()
		if addedDst != nil {
			_ = addedDst.Close()
			_ = os.Remove(addedArchivePath)
		}
		return err
	}
	if err := archiveDst.Close(); err != nil {
		if addedDst != nil {
			_ = addedDst.Close()
			_ = os.Remove(addedArchivePath)
		}
		return err
	}

	// Rename the modified archive to its final timestamped name.
	if currentArchive != "" && currentArchive != newArchivePath {
		if err := os.Rename(currentArchive, newArchivePath); err != nil {
			if addedDst != nil {
				_ = addedDst.Close()
				_ = os.Remove(addedArchivePath)
			}
			return fmt.Errorf("rename archive: %w", err)
		}
	}

	// Write the Added archive only for subsequent imports when something changed.
	if addedDst != nil {
		if err := writeAddedArchive(addedDst, addedEntries); err != nil {
			_ = os.Remove(newArchivePath)
			_ = os.Remove(addedArchivePath)
			return fmt.Errorf("write added archive: %w", err)
		}
	}

	cdBytes, eocd, err := readArchiveCD(newArchivePath)
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

	// Write a human-readable summary next to the consolidated archive.
	summaryPath := strings.TrimSuffix(newArchivePath, ".zip") + ".txt"
	if err := writeSummary(summaryPath, env, report, start, newArchivePath, addedArchivePath); err != nil {
		env.Logf("warn", "could not write summary file: %v", err)
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
	if files, _ := listBackups(env.Backup); len(files) > 0 {
		fmt.Printf("Found %d previous backup(s) in %s.\n", len(files), env.Backup)
		if confirm("Delete previous backups to free space? (y/N): ") {
			for _, f := range files {
				_ = os.Remove(filepath.Join(env.Backup, f))
			}
		}
	}
	dst := uniqueArchivePath(env.Backup, filepath.Base(src))
	fmt.Println("Backing up current consolidated archive...")
	return copyFileWithProgress(src, dst)
}

func listBackups(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "Consolidated-") && strings.HasSuffix(name, ".zip") {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	return files, nil
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

func confirm(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func checkArchiveIntegrity(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		return nil
	}
	fr, err := zipx.OpenFileRead(path)
	if err != nil {
		return err
	}
	defer fr.Close()
	for _, e := range fr.Entries {
		if err := verifyEntry(fr.File, e, false); err != nil {
			return fmt.Errorf("entry %s: %w", e.Name, err)
		}
	}
	return nil
}

func printReport(env *app.Env, r *Report, start time.Time) {
	d := time.Since(start)
	env.Summary("TakeOutBack %s", app.Version)
	env.Summary("Archives scanned : %d", r.ArchivesScanned)
	env.Summary("Files scanned    : %d", r.FilesScanned)
	env.Summary("New files        : %d", r.NewFiles)
	env.Summary("Modified files   : %d", r.ModifiedFiles)
	env.Summary("Skipped files    : %d", r.SkippedFiles)
	env.Summary("Errors           : %d", r.Errors)
	env.Summary("Bytes appended   : %s", humanSize(r.BytesAppended))
	env.Summary("Duration         : %s", formatDuration(d))
	if r.Errors > 0 {
		env.Summary("Status           : Completed with errors")
		logPath := filepath.Join(env.LogsDir, time.Now().UTC().Format("2006-01-02")+".log")
		env.Summary("Details          : %d error(s) logged to %s", r.Errors, logPath)
	} else {
		env.Summary("Status           : OK")
	}
}

func writeSummary(path string, env *app.Env, r *Report, start time.Time, archivePath, addedPath string) error {
	var b strings.Builder
	d := time.Since(start)
	fmt.Fprintf(&b, "TakeOutBack %s\n", app.Version)
	fmt.Fprintf(&b, "Archives scanned : %d\n", r.ArchivesScanned)
	fmt.Fprintf(&b, "Files scanned    : %d\n", r.FilesScanned)
	fmt.Fprintf(&b, "New files        : %d\n", r.NewFiles)
	fmt.Fprintf(&b, "Modified files   : %d\n", r.ModifiedFiles)
	fmt.Fprintf(&b, "Skipped files    : %d\n", r.SkippedFiles)
	fmt.Fprintf(&b, "Errors           : %d\n", r.Errors)
	fmt.Fprintf(&b, "Bytes appended   : %s\n", humanSize(r.BytesAppended))
	fmt.Fprintf(&b, "Duration         : %s\n", formatDuration(d))
	if r.Errors > 0 {
		fmt.Fprintln(&b, "Status           : Completed with errors")
		logPath := filepath.Join(env.LogsDir, time.Now().UTC().Format("2006-01-02")+".log")
		fmt.Fprintf(&b, "Details          : %d error(s) logged to %s\n", r.Errors, logPath)
		fmt.Fprintln(&b, "Errors:")
		for _, detail := range r.ErrorDetails {
			fmt.Fprintf(&b, "  - %s\n", detail)
		}
	} else {
		fmt.Fprintln(&b, "Status           : OK")
	}
	fmt.Fprintf(&b, "Archive: %s\n", archivePath)
	if addedPath != "" {
		fmt.Fprintf(&b, "Added:   %s\n", addedPath)
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// finalizeEmptySync rotates the timestamp of the current archive when no
// incoming files need to be imported. It renames the existing archive in place
// instead of copying every entry, which is much faster for large archives.
func finalizeEmptySync(env *app.Env, currentArchive, newArchivePath string, existingEntries []*zipx.Entry, report *Report, start time.Time) error {
	if currentArchive == "" {
		// Initial sync with nothing to import.
		report.Duration = time.Since(start)
		printReport(env, report, start)
		env.Log("info", "no archives to process and no existing archive")
		return nil
	}

	fmt.Println("No new or modified files to append; rotating archive timestamp...")
	if err := os.Rename(currentArchive, newArchivePath); err != nil {
		return fmt.Errorf("rename archive: %w", err)
	}

	cdBytes, eocd, err := readArchiveCD(newArchivePath)
	if err != nil {
		return err
	}

	s := state.New(filepath.Base(newArchivePath), app.Version)
	s.ArchiveEnd = eocd.Offset + zipx.EOCDSize
	if eocd.HasZip64 {
		s.ArchiveEnd = eocd.Offset + zipx.EOCDSize
	}
	s.Entries = int64(len(existingEntries))
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

	summaryPath := strings.TrimSuffix(newArchivePath, ".zip") + ".txt"
	if err := writeSummary(summaryPath, env, report, start, newArchivePath, ""); err != nil {
		env.Logf("warn", "could not write summary file: %v", err)
	}
	env.Logf("info", "sync completed in %s, archive=%s", report.Duration, newArchivePath)
	return nil
}

func readArchiveCD(path string) ([]byte, *zipx.EOCD, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	eocd, err := zipx.FindEOCD(f)
	if err != nil {
		return nil, nil, err
	}
	buf := make([]byte, eocd.CDSize)
	if _, err := f.ReadAt(buf, int64(eocd.CDOffset)); err != nil {
		return nil, nil, err
	}
	return buf, eocd, nil
}

// preserveCurrentState records the current archive's EOCD/CD offsets and backs
// up its central directory bytes. If a later sync step corrupts the archive,
// recoverArchive can use this information to truncate back to the last known
// good size and rewrite the original central directory.
func preserveCurrentState(env *app.Env, archivePath string) error {
	cdBytes, eocd, err := readArchiveCD(archivePath)
	if err != nil {
		return err
	}
	s := state.New(filepath.Base(archivePath), app.Version)
	s.ArchiveEnd = eocd.Offset + zipx.EOCDSize
	if eocd.HasZip64 {
		s.ArchiveEnd = eocd.Offset + zipx.EOCDSize
	}
	s.Entries = int64(eocd.TotalEntries)
	s.CDOffset = int64(eocd.CDOffset)
	s.CDSize = int64(eocd.CDSize)
	s.CDSha256 = state.CDHash(cdBytes)
	s.EOCDOffset = eocd.Offset
	s.LastSyncAt = time.Now().UTC().Format(time.RFC3339)

	if err := state.SaveAtomic(env.StatePath, s); err != nil {
		return err
	}
	if err := state.BackupCD(env.BackupCD, cdBytes); err != nil {
		env.Logf("warn", "could not backup central directory: %v", err)
	}
	return nil
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
// Any rebuilt archive is written to a system temp directory and then renamed
// over archivePath.
func recoverArchive(env *app.Env, archivePath string) error {
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

	tempDir, err := os.MkdirTemp(env.TempDir, "recover-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

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
		_, err = zipx.CopyRawEntry(dst, src, e, nil)
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

// Clean removes all user data (Incoming, Archive and Backup files) and resets
// TakeOutBack to a fresh-install state. Settings and logs are preserved.
func Clean(env *app.Env, args []string) error {
	fmt.Println("Clean will remove all files from:")
	fmt.Printf("  Incoming: %s\n", env.Incoming)
	fmt.Printf("  Archive:  %s\n", env.Archive)
	fmt.Printf("  Backup:   %s\n", env.Backup)
	if !confirm("Are you sure? (y/N): ") {
		return nil
	}
	dirs := []string{env.Incoming, env.Archive, env.Backup}
	for _, d := range dirs {
		if err := removeDirContents(d); err != nil {
			return err
		}
	}
	env.Summary("TakeOutBack reset to fresh-install state")
	return nil
}

func removeDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			return err
		}
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

	runDir, err := os.MkdirTemp(env.TempDir, "compact-*")
	if err != nil {
		return fmt.Errorf("cannot create temp directory: %w", err)
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
		_, err := zipx.CopyRawEntry(dst, fr.File, e, nil)
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

// isMetadataJSON reports whether name is a Google Photos supplemental-metadata
// sidecar. These small JSON files are frequently re-encoded with different
// formatting between Takeout exports, so we compare them semantically.
func isMetadataJSON(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".supplemental-metadata.json")
}

// isJSONCandidate reports whether a file might be a JSON sidecar that Takeout
// truncated to just ".json" when the full name exceeded the archive's limits.
func isJSONCandidate(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".json")
}

// looksLikeMetadataJSON inspects JSON content for the characteristic fields of a
// Google Photos supplemental-metadata sidecar.
func looksLikeMetadataJSON(data []byte) bool {
	var v map[string]interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return false
	}
	_, hasTitle := v["title"]
	_, hasCreationTime := v["creationTime"]
	_, hasPhotoTakenTime := v["photoTakenTime"]
	_, hasGeoData := v["geoData"]
	_, hasGeoDataExif := v["geoDataExif"]
	_, hasURL := v["url"]
	_, hasGooglePhotosOrigin := v["googlePhotosOrigin"]
	return hasTitle && (hasCreationTime || hasPhotoTakenTime || hasGeoData || hasGeoDataExif || hasURL || hasGooglePhotosOrigin)
}

// canonicalJSON parses JSON data and re-serializes it with sorted keys and
// compact formatting. This yields a byte representation that is insensitive to
// whitespace and key-order differences.
func canonicalJSON(data []byte) ([]byte, error) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// buildMetadataIndex returns a map of metadata-sidecar names to their canonical
// JSON bytes and a set of canonical-content hashes for every such entry in the
// given archive. The content-hash set allows deduplication even when Takeout
// truncates or otherwise changes the sidecar file name between exports.
func buildMetadataIndex(archivePath string) (map[string][]byte, map[string]struct{}, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, nil, err
	}
	defer zr.Close()
	idx := make(map[string][]byte)
	set := make(map[string]struct{})
	for _, f := range zr.File {
		if !isMetadataJSON(f.Name) && !isJSONCandidate(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			continue
		}
		// Only index files that are really Google Photos metadata sidecars.
		// Some exports truncate the suffix to ".json", so we also inspect content.
		if !isMetadataJSON(f.Name) && !looksLikeMetadataJSON(data) {
			continue
		}
		canon, err := canonicalJSON(data)
		if err != nil {
			continue
		}
		idx[f.Name] = canon
		set[metadataHash(canon)] = struct{}{}
	}
	return idx, set, nil
}

// metadataHash returns a compact string hash of canonical JSON bytes.
func metadataHash(canon []byte) string {
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

// readZipEntry reads and returns the uncompressed content of a single entry.
func readZipEntry(zipPath, entryName string) ([]byte, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != entryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, fmt.Errorf("entry %s not found in %s", entryName, zipPath)
}
