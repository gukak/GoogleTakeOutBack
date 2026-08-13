package safestorage

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Storage is the minimal interface implemented by each safe-mode backend.
type Storage interface {
	Connect() error
	Close() error
	RemoteSize(remotePath string) (int64, error)
	Upload(localPath, remotePath string, offset int64, progress func(sent, total int64)) error
}

// New creates a Storage implementation based on cfg.Protocol.
func New(cfg Config) (Storage, error) {
	switch strings.ToLower(cfg.Protocol) {
	case "sftp":
		return newSFTP(cfg), nil
	case "ftp":
		return newFTP(cfg), nil
	case "":
		return nil, fmt.Errorf("safe mode storage protocol not configured")
	default:
		return nil, fmt.Errorf("unsupported safe mode storage protocol %q", cfg.Protocol)
	}
}

// progressReader wraps an io.Reader and reports bytes read through a callback.
type progressReader struct {
	Reader   io.Reader
	progress func(n int)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	if n > 0 && pr.progress != nil {
		pr.progress(n)
	}
	return n, err
}

// localFileSize returns the size of path, or 0 if it cannot be determined.
func localFileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}
