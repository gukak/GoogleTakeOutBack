package safestorage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// localStorage copies archives to a local directory or a mounted network share.
// It treats cfg.RemotePath as the destination base directory.
type localStorage struct {
	cfg Config
}

func newLocal(cfg Config) *localStorage {
	return &localStorage{cfg: cfg}
}

func (s *localStorage) Connect() error {
	return os.MkdirAll(s.cfg.RemotePath, 0755)
}

func (s *localStorage) Close() error {
	return nil
}

func (s *localStorage) RemoteSize(remotePath string) (int64, error) {
	st, err := os.Stat(remotePath)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (s *localStorage) Upload(ctx context.Context, localPath, remotePath string, offset int64, progress func(sent, total int64)) error {
	localDst := filepath.FromSlash(remotePath)
	if err := os.MkdirAll(filepath.Dir(localDst), 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	in, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	total := info.Size()

	if offset > 0 {
		if _, err := in.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(localDst, flags, 0644)
	if err != nil {
		return fmt.Errorf("open destination file: %w", err)
	}
	defer out.Close()

	buf := make([]byte, 1024*1024)
	var sent int64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			sent += int64(n)
			if progress != nil {
				progress(offset+sent, total)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return out.Sync()
}
