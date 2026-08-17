package safestorage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

type ftpStorage struct {
	cfg    Config
	client *ftp.ServerConn
}

func newFTP(cfg Config) *ftpStorage {
	return &ftpStorage{cfg: cfg}
}

func (s *ftpStorage) Connect() error {
	port := s.cfg.Port
	if port == 0 {
		port = 21
	}
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, port)

	c, err := ftp.Dial(addr, ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return err
	}

	if err := c.Login(s.cfg.User, s.cfg.Password); err != nil {
		_ = c.Quit()
		return err
	}

	s.client = c
	return nil
}

func (s *ftpStorage) Close() error {
	if s.client != nil {
		return s.client.Quit()
	}
	return nil
}

func (s *ftpStorage) RemoteSize(remotePath string) (int64, error) {
	return s.client.FileSize(remotePath)
}

func (s *ftpStorage) Upload(ctx context.Context, localPath, remotePath string, offset int64, progress func(sent, total int64)) error {
	local, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer local.Close()

	st, err := local.Stat()
	if err != nil {
		return err
	}
	total := st.Size()

	if offset > 0 {
		if _, err := local.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}

	if err := s.ensureDir(path.Dir(remotePath)); err != nil {
		return err
	}

	sent := offset
	pr := &progressReader{
		Reader: &ctxReader{ctx: ctx, r: local},
		progress: func(n int) {
			sent += int64(n)
			if progress != nil {
				progress(sent, total)
			}
		},
	}

	return s.client.StorFrom(remotePath, pr, uint64(offset))
}

func (s *ftpStorage) ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	parts := strings.Split(dir, "/")
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		current += "/" + part
		// MakeDir is idempotent on many servers; ignore errors so we keep
		// walking upward.
		_ = s.client.MakeDir(current)
	}
	return nil
}
