package safestorage

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// UploadTask describes one file that should be uploaded.
type UploadTask struct {
	LocalPath  string
	RemoteName string
	Label      string // "takeOutBack" or "takeOutBack-Added"
}

// UploadResult summarises what happened to a single task.
type UploadResult struct {
	Label      string
	LocalPath  string
	RemotePath string
	Uploaded   bool
	Skipped    bool
	Error      error
}

// Uploader performs the configured safe-mode uploads after a sync.
type Uploader struct {
	cfg      Config
	storage  Storage
	progress func(label string, sent, total int64)
}

// NewUploader creates an uploader from configuration. If safe mode storage is
// disabled it returns a nil uploader and no error.
func NewUploader(cfg Config, progress func(label string, sent, total int64)) (*Uploader, error) {
	if cfg.IsEmpty() {
		return nil, nil
	}
	if cfg.UploadMode != "" && cfg.UploadMode != "end" {
		return nil, fmt.Errorf("unsupported safe mode storage upload_mode %q", cfg.UploadMode)
	}
	st, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &Uploader{cfg: cfg, storage: st, progress: progress}, nil
}

// Connect opens the remote connection.
func (u *Uploader) Connect() error {
	if u == nil {
		return nil
	}
	return u.storage.Connect()
}

// Close closes the remote connection.
func (u *Uploader) Close() error {
	if u == nil || u.storage == nil {
		return nil
	}
	return u.storage.Close()
}

// Upload runs all configured tasks and returns a result for each one. Errors
// are returned inside the result slice so callers can report per-file failures.
// The context can be used to abort the upload early.
func (u *Uploader) Upload(ctx context.Context, tasks []UploadTask) []UploadResult {
	if u == nil {
		return nil
	}
	results := make([]UploadResult, 0, len(tasks))
	for _, task := range tasks {
		select {
		case <-ctx.Done():
			results = append(results, UploadResult{
				Label:     task.Label,
				LocalPath: task.LocalPath,
				Error:     ctx.Err(),
			})
			continue
		default:
		}
		res := u.uploadOne(ctx, task)
		results = append(results, res)
	}
	return results
}

func (u *Uploader) uploadOne(ctx context.Context, task UploadTask) UploadResult {
	res := UploadResult{
		Label:     task.Label,
		LocalPath: task.LocalPath,
	}

	info, err := os.Stat(task.LocalPath)
	if err != nil {
		res.Error = err
		return res
	}

	base := filepath.Base(task.LocalPath)
	remotePath := joinRemote(u.cfg.RemotePath, datedDir(), base)
	res.RemotePath = remotePath

	var offset int64
	if u.cfg.ResumePartial {
		rs, err := u.storage.RemoteSize(remotePath)
		if err == nil && rs > 0 && rs < info.Size() {
			offset = rs
		}
	}

	if offset == info.Size() {
		res.Skipped = true
		return res
	}

	label := maskLabel(task.Label)
	err = u.storage.Upload(ctx, task.LocalPath, remotePath, offset, func(sent, total int64) {
		if u.progress != nil {
			u.progress(label, sent, total)
		}
	})
	if err != nil {
		res.Error = err
		return res
	}

	res.Uploaded = true
	return res
}

func maskLabel(label string) string {
	switch label {
	case "takeOutBack":
		return "primary safe copy"
	case "takeOutBack-Added":
		return "incremental safe copy"
	default:
		return "safe copy"
	}
}

func datedDir() string {
	return time.Now().Format("20060102-150405")
}

func joinRemote(parts ...string) string {
	return path.Join(parts...)
}

// MaskError replaces any remote details with a generic message. Use it when
// logging safe-mode failures so that host names, ports, paths and credentials
// never leak into the console or log files.
func MaskError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "auth") ||
		strings.Contains(msg, "password") ||
		strings.Contains(msg, "credential") ||
		strings.Contains(msg, "handshake") ||
		strings.Contains(msg, "unable to authenticate") {
		return fmt.Errorf("safe mode storage authentication failed")
	}
	return fmt.Errorf("safe mode storage transfer failed")
}
