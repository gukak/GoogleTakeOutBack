// Package state manages the small JSON sidecar used for fast recovery and
// caching. The archive itself remains the source of truth.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SidecarVersion is the current sidecar schema version.
const SidecarVersion = 1

// State is the JSON sidecar stored next to the consolidated archive.
type State struct {
	Version           int     `json:"version"`
	ArchivePath       string  `json:"archive_path"`
	ArchiveEnd        int64   `json:"archive_end"`
	Entries           int64   `json:"entries"`
	CDOffset          int64   `json:"cd_offset"`
	CDSize            int64   `json:"cd_size"`
	CDSha256          string  `json:"cd_sha256"`
	EOCDOffset        int64   `json:"eocd_offset"`
	LastSyncAt        string  `json:"last_sync_at"`
	LastSyncDurationS float64 `json:"last_sync_duration_s"`
	ToolVersion       string  `json:"tool_version"`
}

// New returns an empty State for the given archive name.
func New(archiveName, toolVersion string) *State {
	return &State{
		Version:     SidecarVersion,
		ArchivePath: archiveName,
		ToolVersion: toolVersion,
	}
}

// Load reads a State from path.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("invalid state file %s: %w", path, err)
	}
	if s.Version != SidecarVersion {
		return nil, fmt.Errorf("unsupported sidecar version %d", s.Version)
	}
	return &s, nil
}

// SaveAtomic writes state to a temporary file and renames it into place.
func SaveAtomic(path string, s *State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CDHash returns the SHA-256 hex digest of the central directory bytes.
func CDHash(cd []byte) string {
	h := sha256.Sum256(cd)
	return hex.EncodeToString(h[:])
}

// BackupCD writes the raw central directory bytes to a backup file atomically.
func BackupCD(path string, cd []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, cd, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadCDBytes reads the central directory block from the archive using offsets
// recorded in s.
func ReadCDBytes(archivePath string, s *State) ([]byte, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, s.CDSize)
	if _, err := f.ReadAt(buf, s.CDOffset); err != nil {
		return nil, err
	}
	return buf, nil
}

// IsConsistent reports whether the central directory described by s matches
// the actual file on disk.
func IsConsistent(archivePath string, s *State) (bool, error) {
	cd, err := ReadCDBytes(archivePath, s)
	if err != nil {
		return false, err
	}
	return CDHash(cd) == s.CDSha256, nil
}
