package innodb

import (
	"bytes"
	"fmt"
	"io"
)

// ExternalField locates a 20-byte reference in the clustered record. Offset is
// page-relative; Column indexes Schema.Columns. Chunks are in logical value order.
type ExternalField struct {
	Format                              string // "BLOB" for old page chains; empty for the existing new LOB format.
	HeaderOffset                        uint32 // Old BLOB first header offset; Version is zero for BLOB.
	Prefix                              []byte // Independently owned local prefix preceding Reference.
	Column, Offset                      int
	Reference                           [20]byte
	SpaceID, FirstPage, Version, Length uint32
	Chunks                              []LOBChunk
}

type LOBChunk struct {
	IndexPage      uint32 // page containing this chunk’s index entry; zero for old BLOB
	IndexOffset    int    // offset within IndexPage; zero for old BLOB
	PageNumber     uint32
	Offset, Length int // data bytes within this page
}

func parseExternal(value []byte, maximum uint64, space uint32) (ExternalField, error) {
	var f ExternalField
	if len(value) < 20 {
		return f, fmt.Errorf("%w: truncated external reference", ErrCorrupt)
	}
	if len(value) != 20 {
		return f, fmt.Errorf("%w: external local prefix", ErrUnsupported)
	}
	copy(f.Reference[:], value)
	f.SpaceID, f.FirstPage, f.Version, f.Length = be.Uint32(value), be.Uint32(value[4:]), be.Uint32(value[8:]), be.Uint32(value[16:])
	if value[12]&0x20 != 0 {
		return f, fmt.Errorf("%w: external being-modified flag", ErrUnsupported)
	}
	if be.Uint32(value[12:16])&0x3fffffff != 0 || f.SpaceID != space || f.FirstPage == 0 || f.FirstPage == ^uint32(0) || f.Length == 0 || uint64(f.Length) > maximum {
		return f, fmt.Errorf("%w: external space/page/length", ErrCorrupt)
	}
	return f, nil
}

type lobAddress struct {
	page   uint32
	offset uint16
}

func lobAddr(b []byte) lobAddress { return lobAddress{be.Uint32(b), be.Uint16(b[4:])} }

var lobNull = lobAddress{^uint32(0), 0}

// MaxLOBValueBytes bounds one materialized external value, including any local prefix and LONG types.
const MaxLOBValueBytes uint32 = 16 * 1024 * 1024

// readExternal reads current noncompressed LOBs. Old row versions need undo;
// version numbers alone cannot reverse small in-place updates (8.0.45 lob0update.cc).
func readExternal(r io.ReaderAt, size int64, f *ExternalField) ([]byte, error) {
	total := uint64(f.Length) + uint64(len(f.Prefix))
	if total > uint64(MaxLOBValueBytes) {
		return nil, fmt.Errorf("%w: LOB value exceeds %d-byte implementation limit", ErrUnsupported, MaxLOBValueBytes)
	}
	if int64(f.Length) > size {
		return nil, fmt.Errorf("%w: LOB length exceeds file", ErrCorrupt)
	}
	data := make([]byte, 0, int(total))
	err := walkExternal(r, size, f, func(b []byte, chunk LOBChunk, prefix bool) error {
		data = append(data, b...)
		if !prefix {
			f.Chunks = append(f.Chunks, chunk)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// walkExternal validates the same current-value structure for typed and raw reads.
// emit borrows data only for the call; the public stream copies before delivery.
func walkExternal(r io.ReaderAt, size int64, f *ExternalField, emit func([]byte, LOBChunk, bool) error) error {
	if int64(f.Length) > size {
		return fmt.Errorf("%w: LOB length exceeds file", ErrCorrupt)
	}
	firstPage, err := readPage(r, size, f.FirstPage)
	if err != nil {
		return err
	}
	if be.Uint32(firstPage[34:]) != f.SpaceID {
		return fmt.Errorf("%w: LOB first page space mismatch", ErrCorrupt)
	}
	if be.Uint16(firstPage[24:]) == 10 {
		if f.Version != 38 {
			return fmt.Errorf("%w: old BLOB header offset %d, expected 38", ErrUnsupported, f.Version)
		}
		f.Format, f.HeaderOffset, f.Version = "BLOB", f.Version, 0
		if len(f.Prefix) > 0 {
			if err := emit(f.Prefix, LOBChunk{}, true); err != nil {
				return err
			}
		}
		return walkLegacyExternal(r, size, f, emit)
	}
	load := func(number uint32, kind uint16) ([]byte, error) {
		b := firstPage
		if number != f.FirstPage {
			var err error
			b, err = readPage(r, size, number)
			if err != nil {
				return nil, err
			}
		}
		if be.Uint32(b[34:]) != f.SpaceID {
			return nil, fmt.Errorf("%w: LOB page %d space mismatch", ErrCorrupt, number)
		}
		if be.Uint16(b[24:]) != kind {
			return nil, fmt.Errorf("%w: LOB page %d type %d, expected %d", ErrUnsupported, number, be.Uint16(b[24:]), kind)
		}
		if b[38] != 0 {
			return nil, fmt.Errorf("%w: LOB page format version", ErrUnsupported)
		}
		return b, nil
	}
	first, err := load(f.FirstPage, 24)
	if err != nil {
		return err
	}
	version := be.Uint32(first[40:])
	if first[39]&^byte(1) != 0 {
		return fmt.Errorf("%w: unknown LOB flags", ErrUnsupported)
	}
	if f.Version == 0 || version == 0 || f.Version > version {
		return fmt.Errorf("%w: invalid LOB reference version", ErrCorrupt)
	}
	if f.Version < version {
		return fmt.Errorf("%w: historical LOB reference requires undo", ErrUnsupported)
	}
	count := be.Uint32(first[64:])
	if count == 0 || uint64(count) > uint64(f.Length) || int64(count) > size/PageSize {
		return fmt.Errorf("%w: invalid LOB index count %d", ErrCorrupt, count)
	}
	// FIL_NEXT links allocated index pages, newest first; it is not value order.
	if len(f.Prefix) > 0 {
		if err := emit(f.Prefix, LOBChunk{}, true); err != nil {
			return err
		}
	}
	if err := scanEntries(r, 1); err != nil {
		return err
	}
	indexPages := map[uint32]bool{f.FirstPage: true}
	for number := be.Uint32(first[12:]); number != ^uint32(0); {
		if _, exists := indexPages[number]; exists || int64(len(indexPages)) >= size/PageSize {
			return fmt.Errorf("%w: cyclic/excess LOB index page allocation chain", ErrCorrupt)
		}
		if err := scanEntries(r, 1); err != nil {
			return err
		}
		page, err := load(number, 22)
		if err != nil {
			return err
		}
		indexPages[number] = true
		number = be.Uint32(page[12:])
	}
	current, last := lobAddr(first[68:]), lobAddr(first[74:])
	previous := lobNull
	slots := make(map[lobAddress]bool)
	pages := make(map[uint32]bool)
	var total uint64
	// All active and historical entries share the allocation pool. A slot may
	// occur only once, including across different version lists.
	entryAt := func(addr lobAddress) ([]byte, error) {
		_, exists := indexPages[addr.page]
		base, end := 96, 696
		if addr.page != f.FirstPage {
			base, end = 39, 39+272*60
		}
		off := int(addr.offset)
		if !exists || off < base || off >= end || (off-base)%60 != 0 || slots[addr] {
			return nil, fmt.Errorf("%w: invalid/reused LOB index address", ErrCorrupt)
		}
		if err := scanEntries(r, 1); err != nil {
			return nil, err
		}
		slots[addr] = true
		page := first
		if addr.page != f.FirstPage {
			var err error
			page, err = load(addr.page, 22)
			if err != nil {
				return nil, err
			}
		}
		return page[off : off+60], nil
	}
	for i := uint32(0); i < count; i++ {
		e, err := entryAt(current)
		if err != nil {
			return err
		}
		if lobAddr(e) != previous {
			return fmt.Errorf("%w: LOB index previous link", ErrCorrupt)
		}
		v := be.Uint32(e[56:])
		if v == 0 || v > version {
			return fmt.Errorf("%w: LOB active block version", ErrCorrupt)
		}
		historyCount := be.Uint32(e[12:])
		if uint64(historyCount) > uint64(len(indexPages))*272 {
			return fmt.Errorf("%w: LOB history count", ErrCorrupt)
		}
		history, historyLast, historyPrev := lobAddr(e[16:]), lobAddr(e[22:]), lobNull
		for j := uint32(0); j < historyCount; j++ {
			old, err := entryAt(history)
			if err != nil {
				return err
			}
			oldVersion := be.Uint32(old[56:])
			if lobAddr(old) != historyPrev || oldVersion == 0 || oldVersion > v ||
				be.Uint32(old[12:]) != 0 || lobAddr(old[16:]) != lobNull || lobAddr(old[22:]) != lobNull {
				return fmt.Errorf("%w: LOB historical entry", ErrCorrupt)
			}
			oldPage, oldLength := be.Uint32(old[48:]), int(be.Uint16(old[52:]))
			capacity := PageSize - 8 - 49
			if oldPage == f.FirstPage {
				capacity = PageSize - 8 - 696
			}
			if oldPage == 0 || int64(oldPage) >= size/PageSize || oldLength == 0 || oldLength > capacity {
				return fmt.Errorf("%w: LOB historical page/length", ErrCorrupt)
			}
			v = oldVersion
			historyPrev, history = history, lobAddr(old[6:])
		}
		if history != lobNull || historyPrev != historyLast {
			return fmt.Errorf("%w: LOB history endpoint", ErrCorrupt)
		}
		number, n := be.Uint32(e[48:]), int(be.Uint16(e[52:]))
		if pages[number] || number == 0 || n == 0 || uint64(n) > uint64(f.Length)-total {
			return fmt.Errorf("%w: repeated LOB data page or invalid chunk length", ErrCorrupt)
		}
		pages[number] = true
		page, start, lengthOffset := first, 696, 54
		creator := first[58:64]
		if number != f.FirstPage {
			page, err = load(number, 23)
			if err != nil {
				return err
			}
			start, lengthOffset = 49, 39
			creator = page[43:49]
		}
		// Full-block replacement does not write the DATA transaction field.
		// Only insertion-version entries have the creator equality invariant.
		if be.Uint32(e[56:]) == 1 && !bytes.Equal(creator, e[28:34]) {
			return fmt.Errorf("%w: LOB data creator transaction mismatch", ErrCorrupt)
		}
		if n > PageSize-8-start || uint32(n) != be.Uint32(page[lengthOffset:]) {
			return fmt.Errorf("%w: LOB chunk/page length mismatch", ErrCorrupt)
		}
		if err := emit(page[start:start+n], LOBChunk{IndexPage: current.page, IndexOffset: int(current.offset), PageNumber: number, Offset: start, Length: n}, false); err != nil {
			return err
		}
		total += uint64(n)
		previous, current = current, lobAddr(e[6:])
	}
	if current != lobNull || previous != last || total != uint64(f.Length) {
		return fmt.Errorf("%w: LOB list endpoint/total length mismatch", ErrCorrupt)
	}
	return scanCheck(r)
}
