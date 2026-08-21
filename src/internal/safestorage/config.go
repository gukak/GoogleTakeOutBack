// Package safestorage provides a masked, resumable remote upload backend for
// TakeOutBack archives. It is deliberately exposed as "safe mode storage" in
// the UI and logs so that no remote-storage terminology leaks into the public
// documentation.
package safestorage

// Config describes a single safe-mode storage destination.
type Config struct {
	// Enabled turns safe mode storage on/off. When disabled all other fields
	// are ignored and no remote connection is attempted.
	Enabled bool `json:"enabled"`

	// Protocol is one of "sftp", "ftp" or "local".
	// "local" copies archives to a local path or a mounted network share.
	Protocol string `json:"protocol"`

	// Host is the remote server hostname or IP address.
	Host string `json:"host"`

	// Port is the remote server port. Zero means use the protocol default
	// (22 for sftp, 21 for ftp).
	Port int `json:"port"`

	// User is the account name used to authenticate.
	User string `json:"user"`

	// Password is the account password used to authenticate.
	Password string `json:"password"`

	// RemotePath is the base directory on the remote server where files will
	// be uploaded. A dated sub-folder may be appended by the uploader.
	RemotePath string `json:"remote_path"`

	// UploadMode selects when uploads happen. Currently only "end" is
	// supported, which uploads archives after the local sync finishes.
	UploadMode string `json:"upload_mode"`

	// ResumePartial allows the uploader to continue an interrupted upload
	// instead of restarting from the beginning.
	ResumePartial bool `json:"resume_partial"`

	// UploadTargets lists which archive files to upload. Recognised values
	// are "takeOutBack" (the consolidated archive) and "takeOutBack-Added"
	// (the companion added archive).
	UploadTargets []string `json:"upload_targets"`
}

// IsEmpty reports whether the configuration is effectively disabled or blank.
func (c Config) IsEmpty() bool {
	if !c.Enabled || len(c.UploadTargets) == 0 {
		return true
	}
	if c.Protocol == "local" {
		return c.RemotePath == ""
	}
	return c.Host == "" || c.User == ""
}

// ShouldUpload returns true when target ("takeOutBack" or "takeOutBack-Added")
// is present in UploadTargets.
func (c Config) ShouldUpload(target string) bool {
	for _, t := range c.UploadTargets {
		if t == target {
			return true
		}
	}
	return false
}
