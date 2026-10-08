package innodb

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	PageSize  = 16 * 1024
	infimum   = 99  // origin of the compact infimum record
	supremum  = 112 // origin of the compact supremum record
	dataStart = 120
)

var be = binary.BigEndian

// Page contains FIL and INDEX metadata for a visited tree page.
// Pages returned by Read have passed strict CRC32C and trailer LSN validation.
type Page struct {
	Number    uint32
	SpaceID   uint32
	Type      uint16
	Previous  uint32
	Next      uint32
	LSN       uint64
	Checksum  uint32
	Trailer   [8]byte
	IndexID   uint64
	Level     uint16
	HeapTop   uint16
	HeapCount uint16
	Records   uint16 // Linked physical records, including delete-marked records.
	Free      uint16
	Garbage   uint16
	Slots     []uint16 // key order, stored backwards from the end of the page
}

func readPage(r io.ReaderAt, size int64, number uint32) ([]byte, error) {
	offset := int64(number) * PageSize
	if size < PageSize || size%PageSize != 0 || offset > size-PageSize {
		return nil, fmt.Errorf("%w: page %d outside aligned file of %d bytes", ErrCorrupt, number, size)
	}
	b := make([]byte, PageSize)
	n, err := r.ReadAt(b, offset)
	if err != nil {
		return nil, fmt.Errorf("read page %d at file offset %d: %w", number, offset, err)
	}
	if n != len(b) {
		return nil, fmt.Errorf("read page %d: %w", number, io.ErrUnexpectedEOF)
	}
	if err := verifyPageChecksum(b); err != nil {
		return nil, fmt.Errorf("page %d at file offset %d: %w", number, offset, err)
	}
	if be.Uint32(b[4:8]) != number {
		return nil, fmt.Errorf("%w: FIL page number does not match %d", ErrCorrupt, number)
	}
	return b, nil
}

func checkTablespace(b []byte, s Schema) error {
	if be.Uint16(b[24:26]) != 8 || be.Uint32(b[34:38]) != s.SpaceID || be.Uint32(b[38:42]) != s.SpaceID {
		return fmt.Errorf("%w: page 0 type or space ID mismatch", ErrCorrupt)
	}
	flags := be.Uint32(b[54:58])
	// fsp0types.h: POST_ANTELOPE bit 0, ZIP_SSIZE bits 1..4,
	// ATOMIC_BLOBS bit 5, PAGE_SSIZE bits 6..9. 0 is legacy 16 KiB.
	ssize := (flags >> 6) & 15
	required := uint32(0x21)
	if s.RowFormat == "COMPACT" {
		required = 0
	}
	if flags&0x21 != required || flags&0x1e != 0 || (ssize != 0 && ssize != 5) ||
		flags & ^uint32(0x21|0x3c0|0x400|0x4000) != 0 {
		return fmt.Errorf("%w: tablespace flags %#x; need matching DYNAMIC/COMPACT, 16 KiB, private unencrypted tablespace", ErrUnsupported, flags)
	}
	return nil
}

func parseIndex(b []byte, s Schema) (Page, error) {
	return parseIndexMinimum(b, s, 10)
}

func parseIndexMinimum(b []byte, s Schema, minimum int) (Page, error) {
	return parseIndexTypeMinimum(b, s, 17855, minimum)
}

func parseIndexType(b []byte, s Schema, pageType uint16) (Page, error) {
	return parseIndexTypeMinimum(b, s, pageType, 10)
}

func parseIndexTypeMinimum(b []byte, s Schema, pageType uint16, minimum int) (Page, error) {
	p := Page{
		Number: be.Uint32(b[4:8]), SpaceID: be.Uint32(b[34:38]), Type: be.Uint16(b[24:26]),
		Previous: be.Uint32(b[8:12]), Next: be.Uint32(b[12:16]),
		LSN: be.Uint64(b[16:24]), Checksum: be.Uint32(b[:4]),
		IndexID: be.Uint64(b[66:74]), Level: be.Uint16(b[64:66]),
		HeapTop: be.Uint16(b[40:42]), HeapCount: be.Uint16(b[42:44]) & 0x7fff,
		Records: be.Uint16(b[54:56]), Free: be.Uint16(b[44:46]), Garbage: be.Uint16(b[46:48]),
	}
	copy(p.Trailer[:], b[PageSize-8:])
	if p.Type != pageType {
		return p, fmt.Errorf("%w: page %d has type %d, expected %d", ErrUnsupported, p.Number, p.Type, pageType)
	}
	if p.SpaceID != s.SpaceID || p.IndexID != s.IndexID {
		return p, fmt.Errorf("%w: space/index ID mismatch on page %d", ErrCorrupt, p.Number)
	}
	if be.Uint16(b[42:44])&0x8000 == 0 {
		return p, fmt.Errorf("%w: REDUNDANT record format", ErrUnsupported)
	}
	nslots := int(be.Uint16(b[38:40]))
	if nslots < 2 || nslots > (PageSize-8-dataStart)/2 {
		return p, fmt.Errorf("%w: invalid directory slot count %d", ErrCorrupt, nslots)
	}
	// The clustered/SDI default is a 5-byte header, 1-byte key and 4-byte child.
	// Secondary leaves may be shorter; callers select their coarse minimum.
	// Exact schema-dependent record bounds are checked during decoding.
	if int(p.HeapTop) < dataStart || int(p.HeapTop) > PageSize-8-2*nslots ||
		p.HeapCount < 2 || int(p.HeapCount) < int(p.Records)+2 || int(p.Records) > (int(p.HeapTop)-dataStart)/minimum ||
		int(p.Garbage) > int(p.HeapTop)-dataStart || (p.Free != 0 && p.Garbage == 0) ||
		(p.Free != 0 && (p.Free < dataStart+5 || p.Free >= p.HeapTop)) {
		return p, fmt.Errorf("%w: inconsistent heap/record bounds", ErrCorrupt)
	}
	for i := 0; i < nslots; i++ {
		o := PageSize - 8 - 2*(i+1)
		p.Slots = append(p.Slots, be.Uint16(b[o:o+2]))
	}
	if p.Slots[0] != infimum || p.Slots[nslots-1] != supremum {
		return p, fmt.Errorf("%w: directory must start at infimum and end at supremum", ErrCorrupt)
	}
	return p, nil
}
