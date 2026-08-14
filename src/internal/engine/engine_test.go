package engine

import (
	"testing"

	"github.com/gukak/GoogleTakeOutBack/internal/app"
	"github.com/gukak/GoogleTakeOutBack/internal/zipx"
)

func TestFindVersion(t *testing.T) {
	versions := []*zipx.Entry{
		{
			Name:             "Photos/image.jpg",
			CRC32:            0x11111111,
			UncompressedSize: 100,
		},
		{
			Name:             "Photos/image__v2.jpg",
			CRC32:            0x22222222,
			UncompressedSize: 110,
		},
	}

	// Exact match with head.
	_, _, found := findVersion(versions, 0x11111111, 100)
	if !found {
		t.Fatal("expected to find head version")
	}

	// Exact match with v2.
	_, _, found = findVersion(versions, 0x22222222, 110)
	if !found {
		t.Fatal("expected to find v2 version")
	}

	// New modified version should get v3.
	_, next, found := findVersion(versions, 0x33333333, 120)
	if found {
		t.Fatal("expected no match for new CRC")
	}
	if next != 3 {
		t.Fatalf("expected next version 3, got %d", next)
	}
}

func TestVersionKey(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"Photos/image.jpg", "photos/image.jpg"},
		{"Photos/image__v2.jpg", "photos/image.jpg"},
		{"Photos/image.v1.raw__v3.jpg", "photos/image.v1.raw.jpg"},
		{"README", "readme"},
	}
	for _, c := range cases {
		got := versionKey(c.name)
		if got != c.want {
			t.Errorf("versionKey(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestInsertVersionSuffix(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want string
	}{
		{"Photos/image.jpg", 2, "Photos/image__v2.jpg"},
		{"Photos/image.v1.raw.jpg", 3, "Photos/image.v1.raw__v3.jpg"},
		{"README", 2, "README__v2"},
		{"dir/subdir/file.txt", 5, "dir/subdir/file__v5.txt"},
	}
	for _, c := range cases {
		got := app.InsertVersionSuffix(c.name, c.n)
		if got != c.want {
			t.Errorf("InsertVersionSuffix(%q, %d) = %q, want %q", c.name, c.n, got, c.want)
		}
	}
}
