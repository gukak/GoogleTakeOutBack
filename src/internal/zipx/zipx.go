// Package zipx provides low-level ZIP reading and writing helpers used by the
// synchronization engine. It intentionally avoids the standard archive/zip
// package for write operations so that the consolidated archive can be updated
// append-only and payloads can be copied byte-for-byte.
package zipx

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gukak/GoogleTakeOutBack/internal/app"
)

// ZIP format signatures.
const (
	SigLocalHeader    uint32 = 0x04034b50
	SigCentralDir     uint32 = 0x02014b50
	SigEOCD           uint32 = 0x06054b50
	SigZip64EOCD      uint32 = 0x06064b50
	SigZip64EOCDLoc   uint32 = 0x07064b50

	MethodStore   uint16 = 0
	MethodDeflate uint16 = 8

	FlagDataDescriptor uint16 = 0x0008
	FlagUTF8           uint16 = 0x0800

	ExtraZip64 uint16 = 0x0001

	EOCDSize              = 22
	Zip64EOCDLocatorSize  = 20
	Zip64EOCDRecordSize   = 56
	LocalHeaderBaseSize   = 30
	CentralDirBaseSize    = 46
	MaxEOCDScan           = 1<<16 + EOCDSize
)

// Entry describes a single file inside a ZIP archive.
type Entry struct {
	Name             string
	Method           uint16
	CRC32            uint32
	CompressedSize   uint64
	UncompressedSize uint64
	ModifiedTime     uint16
	ModifiedDate     uint16
	Flags            uint16
	ExternalAttr     uint32
	CreatorVersion   uint16
	ReaderVersion    uint16
	LocalHeaderOff   uint64
	Extra            []byte
	Comment          []byte
	DiskNumberStart  uint16
}

// FileRead provides random access to a source archive.
type FileRead struct {
	File    *os.File
	Entries []*Entry
	EOCD    *EOCD
}

// EOCD describes the end-of-central-directory record.
type EOCD struct {
	DiskNumber     uint16
	StartDisk      uint16
	EntriesOnDisk  uint64
	TotalEntries   uint64
	CDSize         uint64
	CDOffset       uint64
	Comment        []byte
	Offset         int64
	HasZip64       bool
}

// OpenFileRead opens a ZIP file and reads its central directory.
func OpenFileRead(path string) (*FileRead, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	eocd, err := FindEOCD(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	entries, err := ReadCentralDir(f, eocd)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileRead{File: f, Entries: entries, EOCD: eocd}, nil
}

// Close closes the underlying file.
func (fr *FileRead) Close() error {
	return fr.File.Close()
}

// FindEOCD locates the end-of-central-directory record by scanning backward.
func FindEOCD(f *os.File) (*EOCD, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size < EOCDSize {
		return nil, fmt.Errorf("file too small for EOCD")
	}
	scan := int64(MaxEOCDScan)
	if size < scan {
		scan = size
	}
	buf := make([]byte, scan)
	if _, err := f.ReadAt(buf, size-scan); err != nil {
		return nil, err
	}
	for i := len(buf) - EOCDSize; i >= 0; i-- {
		if binary.LittleEndian.Uint32(buf[i:]) != SigEOCD {
			continue
		}
		commentLen := binary.LittleEndian.Uint16(buf[i+20:])
		if int64(i)+EOCDSize+int64(commentLen) != int64(len(buf)) {
			continue
		}
		eocd := &EOCD{Offset: size - scan + int64(i)}
		eocd.DiskNumber = binary.LittleEndian.Uint16(buf[i+4:])
		eocd.StartDisk = binary.LittleEndian.Uint16(buf[i+6:])
		eocd.EntriesOnDisk = uint64(binary.LittleEndian.Uint16(buf[i+8:]))
		eocd.TotalEntries = uint64(binary.LittleEndian.Uint16(buf[i+10:]))
		eocd.CDSize = uint64(binary.LittleEndian.Uint32(buf[i+12:]))
		eocd.CDOffset = uint64(binary.LittleEndian.Uint32(buf[i+16:]))
		eocd.Comment = buf[i+EOCDSize : i+EOCDSize+int(commentLen)]
		if eocd.TotalEntries == 0xFFFF || eocd.CDSize == 0xFFFFFFFF || eocd.CDOffset == 0xFFFFFFFF {
			if err := readZip64EOCD(f, eocd); err != nil {
				return nil, err
			}
		}
		return eocd, nil
	}
	return nil, fmt.Errorf("EOCD not found")
}

func readZip64EOCD(f *os.File, eocd *EOCD) error {
	if eocd.Offset < int64(Zip64EOCDLocatorSize) {
		return fmt.Errorf("no room for zip64 locator")
	}
	loc := make([]byte, Zip64EOCDLocatorSize)
	if _, err := f.ReadAt(loc, eocd.Offset-int64(Zip64EOCDLocatorSize)); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(loc) != SigZip64EOCDLoc {
		return fmt.Errorf("zip64 locator signature missing")
	}
	eocd.HasZip64 = true
	off := int64(binary.LittleEndian.Uint64(loc[8:]))
	rec := make([]byte, Zip64EOCDRecordSize)
	if _, err := f.ReadAt(rec, off); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(rec) != SigZip64EOCD {
		return fmt.Errorf("zip64 eocd signature missing")
	}
	eocd.TotalEntries = binary.LittleEndian.Uint64(rec[32:])
	eocd.EntriesOnDisk = binary.LittleEndian.Uint64(rec[24:])
	eocd.CDSize = binary.LittleEndian.Uint64(rec[40:])
	eocd.CDOffset = binary.LittleEndian.Uint64(rec[48:])
	return nil
}

// ReadCentralDir parses central directory records described by eocd.
func ReadCentralDir(f *os.File, eocd *EOCD) ([]*Entry, error) {
	buf := make([]byte, eocd.CDSize)
	if _, err := f.ReadAt(buf, int64(eocd.CDOffset)); err != nil {
		return nil, err
	}
	var entries []*Entry
	off := 0
	for off < len(buf) {
		if len(buf)-off < CentralDirBaseSize {
			return nil, fmt.Errorf("truncated central directory record")
		}
		if binary.LittleEndian.Uint32(buf[off:]) != SigCentralDir {
			return nil, fmt.Errorf("central directory signature missing at %d", off)
		}
		e := &Entry{}
		e.CreatorVersion = binary.LittleEndian.Uint16(buf[off+4:])
		e.ReaderVersion = binary.LittleEndian.Uint16(buf[off+6:])
		e.Flags = binary.LittleEndian.Uint16(buf[off+8:])
		e.Method = binary.LittleEndian.Uint16(buf[off+10:])
		e.ModifiedTime = binary.LittleEndian.Uint16(buf[off+12:])
		e.ModifiedDate = binary.LittleEndian.Uint16(buf[off+14:])
		e.CRC32 = binary.LittleEndian.Uint32(buf[off+16:])
		cs := binary.LittleEndian.Uint32(buf[off+20:])
		us := binary.LittleEndian.Uint32(buf[off+24:])
		namelen := binary.LittleEndian.Uint16(buf[off+28:])
		extralen := binary.LittleEndian.Uint16(buf[off+30:])
		commentlen := binary.LittleEndian.Uint16(buf[off+32:])
		e.DiskNumberStart = binary.LittleEndian.Uint16(buf[off+34:])
		e.ExternalAttr = binary.LittleEndian.Uint32(buf[off+38:])
		offset := binary.LittleEndian.Uint32(buf[off+42:])
		base := off + CentralDirBaseSize
		if len(buf) < base+int(namelen)+int(extralen)+int(commentlen) {
			return nil, fmt.Errorf("central directory entry truncated")
		}
		e.Name = string(buf[base : base+int(namelen)])
		e.Extra = append([]byte(nil), buf[base+int(namelen):base+int(namelen)+int(extralen)]...)
		e.Comment = append([]byte(nil), buf[base+int(namelen)+int(extralen):base+int(namelen)+int(extralen)+int(commentlen)]...)
		e.CompressedSize = uint64(cs)
		e.UncompressedSize = uint64(us)
		e.LocalHeaderOff = uint64(offset)
		resolveZip64Extra(e)
		entries = append(entries, e)
		off = base + int(namelen) + int(extralen) + int(commentlen)
	}
	return entries, nil
}

func resolveZip64Extra(e *Entry) {
	needCS := e.CompressedSize == 0xFFFFFFFF
	needUS := e.UncompressedSize == 0xFFFFFFFF
	needOff := e.LocalHeaderOff == 0xFFFFFFFF
	if !needCS && !needUS && !needOff {
		return
	}
	p := 0
	for p+4 <= len(e.Extra) {
		id := binary.LittleEndian.Uint16(e.Extra[p:])
		sz := int(binary.LittleEndian.Uint16(e.Extra[p+2:]))
		if id == ExtraZip64 && p+4+sz <= len(e.Extra) {
			q := p + 4
			if needUS && sz >= 8 {
				e.UncompressedSize = binary.LittleEndian.Uint64(e.Extra[q:])
				q += 8
				sz -= 8
			}
			if needCS && sz >= 8 {
				e.CompressedSize = binary.LittleEndian.Uint64(e.Extra[q:])
				q += 8
				sz -= 8
			}
			if needOff && sz >= 8 {
				e.LocalHeaderOff = binary.LittleEndian.Uint64(e.Extra[q:])
			}
			return
		}
		p += 4 + sz
	}
}

// ReadLocalHeader reads the local file header at the given offset.
func ReadLocalHeader(f *os.File, off int64) (*Entry, int64, error) {
	base := make([]byte, LocalHeaderBaseSize)
	if _, err := f.ReadAt(base, off); err != nil {
		return nil, 0, err
	}
	if binary.LittleEndian.Uint32(base) != SigLocalHeader {
		return nil, 0, fmt.Errorf("local header signature missing at %d", off)
	}
	e := &Entry{}
	e.ReaderVersion = binary.LittleEndian.Uint16(base[4:])
	e.Flags = binary.LittleEndian.Uint16(base[6:])
	e.Method = binary.LittleEndian.Uint16(base[8:])
	e.ModifiedTime = binary.LittleEndian.Uint16(base[10:])
	e.ModifiedDate = binary.LittleEndian.Uint16(base[12:])
	e.CRC32 = binary.LittleEndian.Uint32(base[14:])
	cs := binary.LittleEndian.Uint32(base[18:])
	us := binary.LittleEndian.Uint32(base[22:])
	namelen := binary.LittleEndian.Uint16(base[26:])
	extralen := binary.LittleEndian.Uint16(base[28:])
	e.CompressedSize = uint64(cs)
	e.UncompressedSize = uint64(us)
	nameOff := off + LocalHeaderBaseSize
	nameBuf := make([]byte, namelen+extralen)
	if _, err := f.ReadAt(nameBuf, nameOff); err != nil {
		return nil, 0, err
	}
	e.Name = string(nameBuf[:namelen])
	e.Extra = append([]byte(nil), nameBuf[namelen:]...)
	resolveZip64Extra(e)
	dataOff := nameOff + int64(namelen) + int64(extralen)
	return e, dataOff, nil
}

// CopyRawEntry copies the compressed payload from src to dst without
// decompressing. It returns the destination entry with LocalHeaderOff filled in.
// If progress is non-nil, it is called with the number of payload bytes written
// after each chunk. If progress returns an error, copying stops and the error is
// returned to the caller.
func CopyRawEntry(dst *os.File, src *os.File, e *Entry, progress func(int64) error) (*Entry, error) {
	h, err := copyRawEntry(dst, src, e, progress)
	if err == nil {
		return h, nil
	}
	// Fallback: some archives (e.g. large Google Takeout zips) may have central
	// directory offsets that our raw parser cannot reconcile. In that case we
	// scan local file headers to locate the entry and copy from there. If that
	// also fails, the caller is informed so it can skip the file instead of
	// aborting the whole sync.
	return scanAndCopyEntry(dst, src, e, progress)
}

func copyRawEntry(dst *os.File, src *os.File, e *Entry, progress func(int64) error) (*Entry, error) {
	h, dataOff, err := ReadLocalHeader(src, int64(e.LocalHeaderOff))
	if err != nil {
		return nil, fmt.Errorf("read local header for %s: %w", e.Name, err)
	}
	h.Name = e.Name
	h.CRC32 = e.CRC32
	h.CompressedSize = e.CompressedSize
	h.UncompressedSize = e.UncompressedSize
	h.Method = e.Method
	h.ModifiedTime = e.ModifiedTime
	h.ModifiedDate = e.ModifiedDate
	h.Flags = (e.Flags &^ FlagDataDescriptor) | FlagUTF8
	h.Extra = nil
	h.ExternalAttr = e.ExternalAttr
	h.CreatorVersion = e.CreatorVersion

	cur, err := dst.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	h.LocalHeaderOff = uint64(cur)
	if err := WriteLocalHeader(dst, h); err != nil {
		return nil, err
	}
	if _, err := src.Seek(dataOff, io.SeekStart); err != nil {
		return nil, err
	}
	n := int64(h.CompressedSize)
	if err := copyNWithProgress(dst, src, n, progress); err != nil {
		return nil, err
	}
	return h, nil
}

func copyNWithProgress(dst io.Writer, src io.Reader, n int64, progress func(int64) error) error {
	if progress == nil {
		_, err := io.CopyN(dst, src, n)
		return err
	}
	buf := make([]byte, 256*1024)
	var copied int64
	for copied < n {
		toRead := int64(len(buf))
		if n-copied < toRead {
			toRead = n - copied
		}
		nr, err := src.Read(buf[:toRead])
		if nr > 0 {
			nw, werr := dst.Write(buf[:nr])
			if werr != nil {
				return werr
			}
			copied += int64(nw)
			if perr := progress(int64(nw)); perr != nil {
				return perr
			}
		}
		if err != nil {
			if err == io.EOF && copied == n {
				return nil
			}
			return err
		}
	}
	return nil
}

func scanAndCopyEntry(dst *os.File, src *os.File, e *Entry, progress func(int64) error) (*Entry, error) {
	entries, err := ScanLocalHeaders(src.Name())
	if err != nil {
		return nil, fmt.Errorf("scan local headers for %s: %w", e.Name, err)
	}
	for _, se := range entries {
		if se.Name == e.Name {
			return copyRawEntry(dst, src, se, progress)
		}
	}
	return nil, fmt.Errorf("entry %s not found by local header scan", e.Name)
}

// WriteLocalHeader writes a local file header to w.
func WriteLocalHeader(w io.Writer, e *Entry) error {
	var buf [LocalHeaderBaseSize]byte
	binary.LittleEndian.PutUint32(buf[0:], SigLocalHeader)
	binary.LittleEndian.PutUint16(buf[4:], e.ReaderVersion)
	binary.LittleEndian.PutUint16(buf[6:], e.Flags)
	binary.LittleEndian.PutUint16(buf[8:], e.Method)
	binary.LittleEndian.PutUint16(buf[10:], e.ModifiedTime)
	binary.LittleEndian.PutUint16(buf[12:], e.ModifiedDate)
	binary.LittleEndian.PutUint32(buf[14:], e.CRC32)
	cs, us, _, extra := prepareZip64Fields(e, true)
	binary.LittleEndian.PutUint32(buf[18:], cs)
	binary.LittleEndian.PutUint32(buf[22:], us)
	binary.LittleEndian.PutUint16(buf[26:], uint16(len(e.Name)))
	binary.LittleEndian.PutUint16(buf[28:], uint16(len(extra)))
	if _, err := w.Write(buf[:]); err != nil {
		return err
	}
	if _, err := io.WriteString(w, e.Name); err != nil {
		return err
	}
	if len(extra) > 0 {
		if _, err := w.Write(extra); err != nil {
			return err
		}
	}
	return nil
}

// WriteCentralDir writes the central directory, EOCD and optional ZIP64 records.
func WriteCentralDir(w *os.File, entries []*Entry) (*EOCD, error) {
	cdStart, err := w.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	needsZip64 := len(entries) > 0xFFFF
	for _, e := range entries {
		if err := writeCentralDirRecord(w, e); err != nil {
			return nil, err
		}
		if e.CompressedSize > 0xFFFFFFFF || e.UncompressedSize > 0xFFFFFFFF || e.LocalHeaderOff > 0xFFFFFFFF {
			needsZip64 = true
		}
	}
	cdEnd, err := w.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	cdSize := cdEnd - cdStart

	eocd := &EOCD{
		TotalEntries: uint64(len(entries)),
		EntriesOnDisk: uint64(len(entries)),
		CDSize: uint64(cdSize),
		CDOffset: uint64(cdStart),
		Offset: cdEnd,
	}

	if needsZip64 || cdStart > 0xFFFFFFFF || cdSize > 0xFFFFFFFF || uint64(len(entries)) > 0xFFFF {
		if err := WriteZip64EOCD(w, eocd); err != nil {
			return nil, err
		}
	}
	if err := WriteEOCD(w, eocd, needsZip64); err != nil {
		return nil, err
	}
	return eocd, nil
}

func writeCentralDirRecord(w io.Writer, e *Entry) error {
	var buf [CentralDirBaseSize]byte
	binary.LittleEndian.PutUint32(buf[0:], SigCentralDir)
	binary.LittleEndian.PutUint16(buf[4:], e.CreatorVersion)
	binary.LittleEndian.PutUint16(buf[6:], e.ReaderVersion)
	binary.LittleEndian.PutUint16(buf[8:], e.Flags)
	binary.LittleEndian.PutUint16(buf[10:], e.Method)
	binary.LittleEndian.PutUint16(buf[12:], e.ModifiedTime)
	binary.LittleEndian.PutUint16(buf[14:], e.ModifiedDate)
	binary.LittleEndian.PutUint32(buf[16:], e.CRC32)
	cs, us, off, extra := prepareZip64Fields(e, false)
	binary.LittleEndian.PutUint32(buf[20:], cs)
	binary.LittleEndian.PutUint32(buf[24:], us)
	binary.LittleEndian.PutUint16(buf[28:], uint16(len(e.Name)))
	binary.LittleEndian.PutUint16(buf[30:], uint16(len(extra)))
	binary.LittleEndian.PutUint16(buf[32:], uint16(len(e.Comment)))
	binary.LittleEndian.PutUint16(buf[34:], e.DiskNumberStart)
	binary.LittleEndian.PutUint16(buf[36:], 0) // internal attrs
	binary.LittleEndian.PutUint32(buf[38:], e.ExternalAttr)
	binary.LittleEndian.PutUint32(buf[42:], off)
	if _, err := w.Write(buf[:]); err != nil {
		return err
	}
	if _, err := io.WriteString(w, e.Name); err != nil {
		return err
	}
	if len(extra) > 0 {
		if _, err := w.Write(extra); err != nil {
			return err
		}
	}
	if len(e.Comment) > 0 {
		if _, err := w.Write(e.Comment); err != nil {
			return err
		}
	}
	return nil
}

func prepareZip64Fields(e *Entry, local bool) (cs32, us32, off32 uint32, extra []byte) {
	cs32 = uint32(e.CompressedSize)
	us32 = uint32(e.UncompressedSize)
	off32 = uint32(e.LocalHeaderOff)
	needCS := e.CompressedSize > 0xFFFFFFFF
	needUS := e.UncompressedSize > 0xFFFFFFFF
	needOff := e.LocalHeaderOff > 0xFFFFFFFF
	if !needCS && !needUS && !needOff {
		return
	}
	if needCS {
		cs32 = 0xFFFFFFFF
	}
	if needUS {
		us32 = 0xFFFFFFFF
	}
	if needOff {
		off32 = 0xFFFFFFFF
	}
	var data []byte
	if needUS {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, e.UncompressedSize)
		data = append(data, b...)
	}
	if needCS {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, e.CompressedSize)
		data = append(data, b...)
	}
	if needOff {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, e.LocalHeaderOff)
		data = append(data, b...)
	}
	extra = make([]byte, 4+len(data))
	binary.LittleEndian.PutUint16(extra, ExtraZip64)
	binary.LittleEndian.PutUint16(extra[2:], uint16(len(data)))
	copy(extra[4:], data)
	return
}

func WriteZip64EOCD(w *os.File, eocd *EOCD) error {
	rec := make([]byte, Zip64EOCDRecordSize)
	binary.LittleEndian.PutUint32(rec[0:], SigZip64EOCD)
	binary.LittleEndian.PutUint64(rec[4:], uint64(Zip64EOCDRecordSize-12))
	binary.LittleEndian.PutUint16(rec[12:], 45) // version made by
	binary.LittleEndian.PutUint16(rec[14:], 45) // version needed
	binary.LittleEndian.PutUint32(rec[16:], 0)  // disk number
	binary.LittleEndian.PutUint32(rec[20:], 0)  // start disk
	binary.LittleEndian.PutUint64(rec[24:], eocd.EntriesOnDisk)
	binary.LittleEndian.PutUint64(rec[32:], eocd.TotalEntries)
	binary.LittleEndian.PutUint64(rec[40:], eocd.CDSize)
	binary.LittleEndian.PutUint64(rec[48:], eocd.CDOffset)
	loc := make([]byte, Zip64EOCDLocatorSize)
	off, err := w.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(loc[0:], SigZip64EOCDLoc)
	binary.LittleEndian.PutUint32(loc[4:], 0)
	binary.LittleEndian.PutUint64(loc[8:], uint64(off))
	binary.LittleEndian.PutUint32(loc[16:], 1)
	if _, err := w.Write(rec); err != nil {
		return err
	}
	_, err = w.Write(loc)
	return err
}

func WriteEOCD(w *os.File, eocd *EOCD, zip64 bool) error {
	var buf [EOCDSize]byte
	binary.LittleEndian.PutUint32(buf[0:], SigEOCD)
	binary.LittleEndian.PutUint16(buf[4:], 0)
	binary.LittleEndian.PutUint16(buf[6:], 0)
	if zip64 {
		binary.LittleEndian.PutUint16(buf[8:], 0xFFFF)
		binary.LittleEndian.PutUint16(buf[10:], 0xFFFF)
		binary.LittleEndian.PutUint32(buf[12:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(buf[16:], 0xFFFFFFFF)
	} else {
		binary.LittleEndian.PutUint16(buf[8:], uint16(eocd.EntriesOnDisk))
		binary.LittleEndian.PutUint16(buf[10:], uint16(eocd.TotalEntries))
		binary.LittleEndian.PutUint32(buf[12:], uint32(eocd.CDSize))
		binary.LittleEndian.PutUint32(buf[16:], uint32(eocd.CDOffset))
	}
	binary.LittleEndian.PutUint16(buf[20:], 0)
	_, err := w.Write(buf[:])
	return err
}

// ScanLocalHeaders rebuilds a list of entries by scanning LFH signatures.
// It is the slow fallback recovery path.
func ScanLocalHeaders(path string) ([]*Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var entries []*Entry
	off := int64(0)
	for off < st.Size() {
		e, dataOff, err := ReadLocalHeader(f, off)
		if err != nil {
			break
		}
		e.LocalHeaderOff = uint64(off)
		next := dataOff + int64(e.CompressedSize)
		if next <= off || next > st.Size() {
			break
		}
		entries = append(entries, e)
		off = next
	}
	return entries, nil
}

// DOSDateTime converts a time.Time to DOS date and time fields.
func DOSDateTime(t time.Time) (uint16, uint16) {
	return dosTime(t), dosDate(t)
}

func dosTime(t time.Time) uint16 {
	t = t.UTC()
	return uint16(t.Second()/2) | uint16(t.Minute()<<5) | uint16(t.Hour()<<11)
}

func dosDate(t time.Time) uint16 {
	t = t.UTC()
	return uint16(t.Day()) | uint16(int(t.Month())<<5) | uint16((t.Year()-1980)<<9)
}

// ValidateCRC reads the compressed payload and verifies CRC32.
func ValidateCRC(f *os.File, e *Entry) error {
	_, dataOff, err := ReadLocalHeader(f, int64(e.LocalHeaderOff))
	if err != nil {
		return err
	}
	h := crc32.NewIEEE()
	zr := io.NewSectionReader(f, dataOff, int64(e.CompressedSize))
	if e.Method == MethodDeflate {
		zr = io.NewSectionReader(f, dataOff, int64(e.CompressedSize))
	}
	// For deflate we need to decompress; for store we read raw.
	var r io.Reader
	if e.Method == MethodDeflate {
		// We avoid importing compress/flate in zipx to keep it focused;
		// callers that need deep CRC validation use engine.Verify.
		return fmt.Errorf("deflate CRC validation delegated to engine")
	}
	r = zr
	if _, err := io.Copy(h, r); err != nil {
		return err
	}
	if h.Sum32() != e.CRC32 {
		return fmt.Errorf("CRC mismatch for %s", e.Name)
	}
	return nil
}

// NormalizePath returns a slash-only path suitable for storing in a ZIP entry.
func NormalizePath(p string) string {
	return strings.TrimSpace(filepath.ToSlash(p))
}

// MatchesPath reports whether candidate is the same logical path as ref,
// ignoring case per the cross-platform policy.
func MatchesPath(candidate, ref string) bool {
	return strings.EqualFold(candidate, ref)
}

// NextVersionName returns the versioned name for a new modified version.
func NextVersionName(storedName string, n int) string {
	return app.InsertVersionSuffix(storedName, n)
}

// MinimalCRC computes the CRC32 of a byte slice.
func MinimalCRC(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// OpenOrCreate creates the consolidated archive if it does not exist.
func OpenOrCreate(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// IsEmptyArchive reports whether the archive has no entries and no CD.
func IsEmptyArchive(f *os.File) (bool, error) {
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	return st.Size() == 0, nil
}
