// Package app provides the runtime environment, layout paths, configuration,
// logging, and constants for the takeoutback command.
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Version is the current takeoutback version. It is overridden at build time.
const Version = "v0.4.4"

// OwnerRepo is the GitHub owner/repository used by the installer and updater.
// Change this to the real repository before the first release.
const OwnerRepo = "gukak/GoogleTakeOutBack"

// Layout constants relative to the project root.
const (
	IncomingDir = "Incoming"
	ArchiveDir  = "Archive"
	BackupDir   = "Backup"
	AppDir      = "TakeOutBack"

	ToolsDir      = "tools"
	ConfigDir     = "config"
	LogsDir       = "logs"
	TempDir       = "temp"
	ScriptsDir    = "scripts"
	DocsDir       = "docs"

	StateName     = "state.json"
	BackupCDName  = "cd.bak"
	LockName      = ".consolidated.lock"
	SettingsName  = "settings.json"
	PolicyName    = "policy.json"
	VersionFileName = "VERSION"
	RootMarkerName  = ".takeoutback-root"

	WindowsBinaryName = "takeoutback.exe"
	LinuxBinaryName   = "takeoutback"
)

// Settings controls runtime behavior.
type Settings struct {
	Version               int    `json:"version"`
	LogLevel              string `json:"log_level"`
	LogRetentionDays      int    `json:"log_retention_days"`
	FetchBothPlatforms    bool   `json:"fetch_both_platforms"`
	KeepMetadataSidecars  bool   `json:"keep_metadata_sidecars"`
	DropIncomingAfterSync bool   `json:"drop_incoming_after_sync"`
	ReDeflateStore        bool   `json:"re_deflate_store"`
	AutoCompactThresholdMB int   `json:"auto_compact_threshold_mb"`
}

// DefaultSettings returns the default settings.
func DefaultSettings() Settings {
	return Settings{
		Version:               1,
		LogLevel:              "info",
		LogRetentionDays:      90,
		FetchBothPlatforms:    true,
		KeepMetadataSidecars:  true,
		DropIncomingAfterSync: false,
		ReDeflateStore:        false,
		AutoCompactThresholdMB: 0,
	}
}

// Policy controls entry filtering.
type Policy struct {
	Version   int      `json:"version"`
	SkipNames []string `json:"skip_names"`
}

// DefaultPolicy returns the default policy.
func DefaultPolicy() Policy {
	return Policy{
		Version:   1,
		SkipNames: []string{},
	}
}

// EnvOptions configures the runtime environment.
type EnvOptions struct {
	Root      string
	ArchiveDir string
	TempDir   string
	BackupDir string
}

// Env holds the resolved project layout and runtime services.
type Env struct {
	Root      string
	Incoming  string
	Archive   string
	Backup    string
	StatePath string
	BackupCD  string
	LockPath  string
	AppRoot   string
	ToolsLinux string
	ToolsWin   string
	ConfigDir  string
	LogsDir    string
	TempDir    string
	Settings   Settings
	Policy     Policy

	logFile  *os.File
	logStart time.Time
}

// NewEnv resolves the project root from the executable or the explicit flag.
// Optional TempDir and BackupDir override the default layout.
func NewEnv(opts EnvOptions) (*Env, error) {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return nil, err
	}

	e := &Env{Root: root}
	e.Incoming = filepath.Join(root, IncomingDir)
	if opts.ArchiveDir != "" {
		if e.Archive, err = filepath.Abs(opts.ArchiveDir); err != nil {
			return nil, fmt.Errorf("cannot resolve archive directory %q: %w", opts.ArchiveDir, err)
		}
	} else {
		e.Archive = filepath.Join(root, ArchiveDir)
	}
	if opts.BackupDir != "" {
		if e.Backup, err = filepath.Abs(opts.BackupDir); err != nil {
			return nil, fmt.Errorf("cannot resolve backup directory %q: %w", opts.BackupDir, err)
		}
	} else {
		e.Backup = filepath.Join(root, BackupDir)
	}
	e.StatePath = filepath.Join(e.Archive, StateName)
	e.BackupCD = filepath.Join(e.Archive, BackupCDName)
	e.LockPath = filepath.Join(e.Archive, LockName)
	e.AppRoot = filepath.Join(root, AppDir)
	e.ToolsLinux = filepath.Join(e.AppRoot, ToolsDir, "linux", LinuxBinaryName)
	e.ToolsWin = filepath.Join(e.AppRoot, ToolsDir, "windows", WindowsBinaryName)
	e.ConfigDir = filepath.Join(e.AppRoot, ConfigDir)
	e.LogsDir = filepath.Join(e.AppRoot, LogsDir)
	if opts.TempDir != "" {
		if e.TempDir, err = filepath.Abs(opts.TempDir); err != nil {
			return nil, fmt.Errorf("cannot resolve temp directory %q: %w", opts.TempDir, err)
		}
	} else {
		e.TempDir = filepath.Join(e.AppRoot, TempDir)
	}
	e.logStart = time.Now()

	if err := ensureLayout(root, e.Archive, e.Backup, e.TempDir); err != nil {
		return nil, err
	}
	if err := e.loadSettings(); err != nil {
		return nil, err
	}
	if err := e.loadPolicy(); err != nil {
		return nil, err
	}
	if err := e.openLog(); err != nil {
		return nil, err
	}
	return e, nil
}

// NewEnvRoot is a convenience wrapper for callers that only need to override the root.
func NewEnvRoot(root string) (*Env, error) {
	return NewEnv(EnvOptions{Root: root})
}

// Close finalizes the runtime environment.
func (e *Env) Close() {
	if e.logFile != nil {
		_ = e.logFile.Close()
	}
}

// Log writes a log entry to the daily log file.
func (e *Env) Log(level string, msg string) {
	if e.logFile == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	line := fmt.Sprintf("{\"t\":%q,\"lvl\":%q,\"msg\":%q}\n", now, level, msg)
	_, _ = e.logFile.WriteString(line)
}

// Logf formats a log entry.
func (e *Env) Logf(level string, format string, args ...any) {
	e.Log(level, fmt.Sprintf(format, args...))
}

// Summary prints a line to stdout.
func (e *Env) Summary(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

// PrintVersion prints the version string.
func (e *Env) PrintVersion() {
	fmt.Println(Version)
}

// Duration returns the elapsed time since the env was created.
func (e *Env) Duration() time.Duration {
	return time.Since(e.logStart)
}

func resolveRoot(rootFlag string) (string, error) {
	if rootFlag != "" {
		return filepath.Abs(rootFlag)
	}
	ex, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot locate executable: %w", err)
	}
	exDir := filepath.Dir(ex)
	// Walk up from the binary looking for a directory that contains Incoming/Archive.
	for i := 0; i < 8; i++ {
		d := exDir
		for j := 0; j < i; j++ {
			d = filepath.Dir(d)
		}
		if hasRootMarkers(d) {
			return filepath.Abs(d)
		}
	}
	// Fallback to the current working directory.
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if hasRootMarkers(cwd) {
		return cwd, nil
	}
	return cwd, nil
}

func hasRootMarkers(d string) bool {
	st1, err := os.Stat(filepath.Join(d, IncomingDir))
	if err != nil || !st1.IsDir() {
		return false
	}
	st2, err := os.Stat(filepath.Join(d, AppDir))
	if err != nil || !st2.IsDir() {
		return false
	}
	st3, err := os.Stat(filepath.Join(d, ArchiveDir))
	if err != nil || !st3.IsDir() {
		return false
	}
	return true
}

func ensureLayout(root, archiveDir, backupDir, tempDir string) error {
	dirs := []string{
		filepath.Join(root, IncomingDir),
		archiveDir,
		backupDir,
		filepath.Join(root, AppDir),
		filepath.Join(root, AppDir, ToolsDir, "linux"),
		filepath.Join(root, AppDir, ToolsDir, "windows"),
		filepath.Join(root, AppDir, ConfigDir),
		filepath.Join(root, AppDir, LogsDir),
		tempDir,
		filepath.Join(root, AppDir, ScriptsDir),
		filepath.Join(root, AppDir, DocsDir),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", d, err)
		}
	}
	marker := filepath.Join(root, RootMarkerName)
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		_ = os.WriteFile(marker, []byte("TakeOutBack project root\n"), 0644)
	}
	return nil
}

func (e *Env) loadSettings() error {
	path := filepath.Join(e.ConfigDir, SettingsName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			e.Settings = DefaultSettings()
			return e.writeSettings()
		}
		return err
	}
	if err := json.Unmarshal(data, &e.Settings); err != nil {
		return fmt.Errorf("invalid settings file %s: %w", path, err)
	}
	return nil
}

func (e *Env) writeSettings() error {
	path := filepath.Join(e.ConfigDir, SettingsName)
	data, err := json.MarshalIndent(e.Settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func (e *Env) loadPolicy() error {
	path := filepath.Join(e.ConfigDir, PolicyName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			e.Policy = DefaultPolicy()
			return e.writePolicy()
		}
		return err
	}
	if err := json.Unmarshal(data, &e.Policy); err != nil {
		return fmt.Errorf("invalid policy file %s: %w", path, err)
	}
	return nil
}

func (e *Env) writePolicy() error {
	path := filepath.Join(e.ConfigDir, PolicyName)
	data, err := json.MarshalIndent(e.Policy, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func (e *Env) openLog() error {
	day := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(e.LogsDir, day+".log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	e.logFile = f
	return nil
}

// BinaryName returns the platform-specific binary name.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return WindowsBinaryName
	}
	return LinuxBinaryName
}

// BinaryPath returns the local path to the current platform's binary.
func (e *Env) BinaryPath() string {
	if runtime.GOOS == "windows" {
		return e.ToolsWin
	}
	return e.ToolsLinux
}

// NormalizeKey returns the lowercased, slash-only path used for identity comparison.
func NormalizeKey(p string) string {
	return strings.ToLower(filepath.ToSlash(p))
}

// CurrentArchive returns the most recent Consolidated-YYYYMMDD-HHMMSS.zip in
// the Archive directory, or an empty string if none exists. Names are sorted
// lexicographically because the embedded timestamp format is sortable.
func (e *Env) CurrentArchive() (string, error) {
	entries, err := os.ReadDir(e.Archive)
	if err != nil {
		return "", err
	}
	var best string
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasPrefix(name, "Consolidated-") || !strings.HasSuffix(name, ".zip") {
			continue
		}
		if best == "" || name > best {
			best = name
		}
	}
	if best == "" {
		return "", nil
	}
	return filepath.Join(e.Archive, best), nil
}

// InsertVersionSuffix inserts __vN before the final extension of the basename.
// It uses path (forward slashes) because names inside ZIP archives always use
// '/' as separators, independent of the host OS.
func InsertVersionSuffix(name string, n int) string {
	base := path.Base(name)
	dir := path.Dir(name)
	suffix := fmt.Sprintf("__v%d", n)
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i] + suffix + base[i:]
	} else {
		base = base + suffix
	}
	if dir == "." {
		return base
	}
	return path.Join(dir, base)
}
