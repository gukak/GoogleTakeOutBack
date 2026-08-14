package safestorage

import (
	"path"
	"testing"
)

func TestJoinRemoteUsesForwardSlashes(t *testing.T) {
	got := joinRemote("ftpbackup", "tmg", "20260814", "file.zip")
	want := "ftpbackup/tmg/20260814/file.zip"
	if got != want {
		t.Fatalf("joinRemote = %q, want %q", got, want)
	}
}

func TestPathDirUsesForwardSlashes(t *testing.T) {
	got := path.Dir("ftpbackup/tmg/20260814/file.zip")
	want := "ftpbackup/tmg/20260814"
	if got != want {
		t.Fatalf("path.Dir = %q, want %q", got, want)
	}
}
