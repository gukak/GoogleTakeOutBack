package zipx

import (
	"fmt"
	"io"
	"os"
	"testing"
)

func TestWriteCentralDirTwice(t *testing.T) {
	path := "/tmp/test_cd.zip"
	_ = os.Remove(path)
	f, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	e1 := &Entry{Name: "file1.txt", CRC32: 0x12345678, CompressedSize: 5, UncompressedSize: 5}
	if err := WriteLocalHeader(f, e1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	entries := []*Entry{e1}
	eocd, err := WriteCentralDir(f, entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	expected := eocd.Offset + EOCDSize
	fmt.Printf("TEST first write: size=%d expected=%d eocd=%+v\n", st.Size(), expected, eocd)
	if st.Size() != expected {
		t.Fatalf("first write: size=%d expected=%d eocd=%+v", st.Size(), expected, eocd)
	}

	fr0, err := OpenFileRead(path)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	fmt.Printf("TEST first read entries: %d\n", len(fr0.Entries))
	fr0.Close()

	// Append a second entry (re-open like the next Sync does).
	f, err = os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	e2 := &Entry{Name: "file2.txt", CRC32: 0x87654321, CompressedSize: 5, UncompressedSize: 5}
	if err := WriteLocalHeader(f, e2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, e2)

	// Append a third entry via CopyRawEntry, reading the first entry from the archive.
	f2, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	srcEntry := &Entry{
		Name:             "file1__v2.txt",
		CRC32:            0x11111111,
		CompressedSize:   5,
		UncompressedSize: 5,
		LocalHeaderOff:   e1.LocalHeaderOff,
		Method:           e1.Method,
		ModifiedTime:     e1.ModifiedTime,
		ModifiedDate:     e1.ModifiedDate,
		Flags:            e1.Flags,
		ExternalAttr:     e1.ExternalAttr,
		CreatorVersion:   e1.CreatorVersion,
		ReaderVersion:    e1.ReaderVersion,
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	written, err := CopyRawEntry(f, f2, srcEntry, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = f2.Close()
	entries = append(entries, written)

	eocd, err = WriteCentralDir(f, entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	st, _ = os.Stat(path)
	expected = eocd.Offset + EOCDSize
	fmt.Printf("TEST second write: size=%d expected=%d eocd=%+v\n", st.Size(), expected, eocd)
	if st.Size() != expected {
		t.Fatalf("second write: size=%d expected=%d eocd=%+v", st.Size(), expected, eocd)
	}

	fr2, err := OpenFileRead(path)
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	defer fr2.Close()
	fmt.Printf("TEST final entries: %d\n", len(fr2.Entries))
	if len(fr2.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(fr2.Entries))
	}
}
