package innodb

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	// MaxSDIObjectBytes bounds both compressed and uncompressed object sizes.
	MaxSDIObjectBytes = 16 * 1024 * 1024
	MaxSDITotalBytes  = 64 * 1024 * 1024
	MaxSDIRecords     = 4096
	// 16 KiB: XDES array offset + 256 descriptors + encryption metadata reserve.
	sdiHeaderOffset = 150 + 40*256 + 115
)

// SDIKey is ordered by unsigned Type, then unsigned ID.
type SDIKey struct {
	Type uint32
	ID   uint64
}

func (k SDIKey) less(b SDIKey) bool { return k.Type < b.Type || k.Type == b.Type && k.ID < b.ID }

// SDIBlobChunk locates compressed payload bytes, not decompressed JSON bytes.
type SDIBlobChunk struct {
	PageNumber     uint32
	Offset, Length int
}
type SDIExternal struct {
	Reference    [20]byte
	Offset       int // reference start within the SDI leaf page
	PrefixLength int
	Chunks       []SDIBlobChunk
}
type SDIRecord struct {
	Key                                  SDIKey
	PageNumber                           uint32
	Start, Offset, End                   int
	Header                               [5]byte
	Transaction                          [6]byte
	RollPointer                          [7]byte
	CompressedLength, UncompressedLength uint32
	JSON                                 json.RawMessage // original decompressed bytes; no reserialization
	External                             *SDIExternal
}
type SDIResult struct {
	SpaceID, RootPage, Version uint32
	IndexID                    uint64
	Pages                      []Page      // SDI tree pages, not page 0 or SDI_BLOB pages
	Records                    []SDIRecord // ordered by (type,id)
}

// ReadSDI extracts raw SDI objects without a user schema. It requires a stable
// 16 KiB, unencrypted, uncompressed private DYNAMIC/COMPACT tablespace with CRC32C.
// It does not interpret the JSON as a table schema. Any error returns nil.
func ReadSDI(r io.ReaderAt, size int64) (*SDIResult, error) {
	fsp, err := readPage(r, size, 0)
	if err != nil {
		return nil, err
	}
	space := be.Uint32(fsp[34:38])
	if space == 0 {
		return nil, fmt.Errorf("%w: SDI requires a private tablespace", ErrUnsupported)
	}
	spaceSchema := Schema{SpaceID: space}
	// Non-atomic flags also occur in REDUNDANT; SDI discovery does not promise
	// that its user records can be decoded. InspectTable checks the DD format.
	if be.Uint32(fsp[54:58])&0x21 == 0 {
		spaceSchema.RowFormat = "COMPACT"
	}
	if err = checkTablespace(fsp, spaceSchema); err != nil {
		return nil, err
	}
	if be.Uint32(fsp[54:58])&0x4000 == 0 {
		return nil, fmt.Errorf("%w: SDI flag is absent", ErrUnsupported)
	}
	version, root := be.Uint32(fsp[sdiHeaderOffset:]), be.Uint32(fsp[sdiHeaderOffset+4:])
	if version != 1 {
		return nil, fmt.Errorf("%w: SDI physical version %d", ErrUnsupported, version)
	}
	if root == 0 || int64(root) >= size/PageSize {
		return nil, fmt.Errorf("%w: invalid SDI root %d", ErrCorrupt, root)
	}
	out := &SDIResult{SpaceID: space, RootPage: root, Version: version, Records: make([]SDIRecord, 0)}
	type task struct {
		page      uint32
		level     int
		low, high SDIKey
		bounded   bool
	}
	stack := []task{{page: root, level: -1}}
	seen := map[uint32]bool{}
	last := map[uint16]Page{}
	total := uint64(0)
	for len(stack) > 0 {
		t := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[t.page] {
			return nil, fmt.Errorf("%w: repeated SDI page %d", ErrCorrupt, t.page)
		}
		seen[t.page] = true
		b, err := readPage(r, size, t.page)
		if err != nil {
			return nil, err
		}
		if t.level < 0 {
			out.IndexID = be.Uint64(b[66:74])
		}
		p, err := parseIndexType(b, Schema{SpaceID: space, IndexID: out.IndexID}, 17853)
		if err != nil {
			return nil, err
		}
		if out.IndexID == 0 || int64(p.Level) >= size/PageSize || t.level >= 0 && int(p.Level) != t.level {
			return nil, fmt.Errorf("%w: SDI index ID/level", ErrCorrupt)
		}
		prev, exists := last[p.Level]
		if !exists && p.Previous != ^uint32(0) || exists && (prev.Next != p.Number || p.Previous != prev.Number) {
			return nil, fmt.Errorf("%w: SDI sibling chain page %d", ErrCorrupt, p.Number)
		}
		last[p.Level] = p
		entries, err := decodePageEntries(b, p, func(o, l int) (pageEntry, error) { return decodeSDIEntry(b, o, l, p.Level) }, func(a, b pageEntry) bool { return a.sdiKey.less(b.sdiKey) })
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 && (p.Level > 0 || t.level >= 0) {
			return nil, fmt.Errorf("%w: empty SDI child/internal page", ErrCorrupt)
		}
		out.Pages = append(out.Pages, p)
		for _, e := range entries {
			if !e.minimum && (e.sdiKey.less(t.low) || t.bounded && !e.sdiKey.less(t.high)) {
				return nil, fmt.Errorf("%w: SDI key outside parent range", ErrCorrupt)
			}
			if p.Level > 0 {
				continue
			}
			if len(out.Records) >= MaxSDIRecords {
				return nil, fmt.Errorf("%w: SDI record count limit", ErrUnsupported)
			}
			if n := len(out.Records); n > 0 && !out.Records[n-1].Key.less(e.sdiKey) {
				return nil, fmt.Errorf("%w: SDI cross-page key order", ErrCorrupt)
			}
			u, c := e.Values[0].(uint32), e.Values[1].(uint32)
			total += uint64(u)
			if total > MaxSDITotalBytes {
				return nil, fmt.Errorf("%w: SDI total byte limit", ErrUnsupported)
			}
			rec := SDIRecord{Key: e.sdiKey, PageNumber: p.Number, Start: e.Start, Offset: e.Offset, End: e.End, Header: e.Header, Transaction: e.Transaction, RollPointer: e.RollPointer, CompressedLength: c, UncompressedLength: u}
			compressed := e.Values[2].([]byte)
			if e.Values[3].(bool) {
				compressed, rec.External, err = readSDIBlob(r, size, space, compressed, e.Offset+33, c)
				if err != nil {
					return nil, fmt.Errorf("SDI page %d record %d: %w", p.Number, e.Offset, err)
				}
			}
			rec.JSON, err = inflateSDI(compressed, u)
			if err != nil {
				return nil, fmt.Errorf("SDI page %d record %d: %w", p.Number, e.Offset, err)
			}
			out.Records = append(out.Records, rec)
		}
		if p.Level > 0 {
			for i := len(entries) - 1; i >= 0; i-- {
				e := entries[i]
				lo, hi, bounded := t.low, t.high, t.bounded
				if !e.minimum {
					lo = e.sdiKey
				}
				if i+1 < len(entries) {
					hi = entries[i+1].sdiKey
					bounded = true
				}
				if e.child == 0 || bounded && !lo.less(hi) {
					return nil, fmt.Errorf("%w: SDI child/range", ErrCorrupt)
				}
				stack = append(stack, task{e.child, int(p.Level) - 1, lo, hi, bounded})
			}
		}
	}
	for _, p := range last {
		if p.Next != ^uint32(0) {
			return nil, fmt.Errorf("%w: unvisited SDI sibling", ErrCorrupt)
		}
	}
	return out, nil
}

func decodeSDIEntry(b []byte, o, limit int, level uint16) (pageEntry, error) {
	e := pageEntry{Record: Record{Start: o - 5, Offset: o, End: o + 16, NextOffset: nextRecord(b, o)}}
	copy(e.Header[:], b[o-5:o])
	e.HeapNumber = be.Uint16(e.Header[1:3]) >> 3
	status := be.Uint16(e.Header[1:3]) & 7
	flags := e.Header[0] & 0xf0
	if flags&^byte(0x10) != 0 {
		return e, fmt.Errorf("%w: SDI deleted/instant/version flags", ErrUnsupported)
	}
	if level > 0 {
		if status != 1 || e.End > limit {
			return e, fmt.Errorf("%w: SDI node layout", ErrCorrupt)
		}
		e.minimum = flags == 0x10
		e.child = be.Uint32(b[o+12 : o+16])
	} else {
		if status != 0 || flags != 0 || o+33 > limit {
			return e, fmt.Errorf("%w: SDI leaf layout", ErrCorrupt)
		}
		length, pos, external, err := readVariableLength(b, o-6, MaxSDIObjectBytes, true)
		if err != nil {
			return e, err
		}
		e.Start = pos + 1
		e.End = o + 33 + length
		if e.End > limit {
			return e, fmt.Errorf("%w: truncated SDI payload", ErrCorrupt)
		}
		copy(e.Transaction[:], b[o+12:o+18])
		copy(e.RollPointer[:], b[o+18:o+25])
		u, c := be.Uint32(b[o+25:]), be.Uint32(b[o+29:])
		if u == 0 || c == 0 {
			return e, fmt.Errorf("%w: empty SDI lengths", ErrCorrupt)
		}
		if u > MaxSDIObjectBytes || c > MaxSDIObjectBytes {
			return e, fmt.Errorf("%w: SDI object byte limit", ErrUnsupported)
		}
		if !external && uint32(length) != c {
			return e, fmt.Errorf("%w: SDI compressed length mismatch", ErrCorrupt)
		}
		e.Values = []any{u, c, b[o+33 : e.End], external}
	}
	e.sdiKey = SDIKey{be.Uint32(b[o : o+4]), be.Uint64(b[o+4 : o+12])}
	return e, nil
}

func readSDIBlob(r io.ReaderAt, size int64, space uint32, local []byte, offset int, compressed uint32) ([]byte, *SDIExternal, error) {
	if len(local) != 20 && len(local) != 788 {
		return nil, nil, fmt.Errorf("%w: SDI external prefix length", ErrUnsupported)
	}
	prefix := len(local) - 20
	ref := local[prefix:]
	ext := &SDIExternal{Offset: offset + prefix, PrefixLength: prefix}
	copy(ext.Reference[:], ref)
	remaining := be.Uint64(ref[12:20])
	if be.Uint32(ref[:4]) != space || be.Uint32(ref[8:12]) != 38 || remaining == 0 || remaining > uint64(compressed) || remaining+uint64(prefix) != uint64(compressed) {
		return nil, nil, fmt.Errorf("%w: SDI external reference", ErrCorrupt)
	}
	data := make([]byte, 0, int(compressed))
	data = append(data, local[:prefix]...)
	seen := map[uint32]bool{}
	for page := be.Uint32(ref[4:8]); page != ^uint32(0); {
		if page == 0 || seen[page] || remaining == 0 {
			return nil, nil, fmt.Errorf("%w: SDI BLOB cycle/extra page", ErrCorrupt)
		}
		seen[page] = true
		b, err := readPage(r, size, page)
		if err != nil {
			return nil, nil, err
		}
		if be.Uint16(b[24:26]) != 18 {
			return nil, nil, fmt.Errorf("%w: expected SDI_BLOB page %d", ErrUnsupported, page)
		}
		if be.Uint32(b[34:38]) != space {
			return nil, nil, fmt.Errorf("%w: SDI BLOB space", ErrCorrupt)
		}
		length := be.Uint32(b[38:42])
		next := be.Uint32(b[42:46])
		if length == 0 || length > PageSize-8-46 || uint64(length) > remaining {
			return nil, nil, fmt.Errorf("%w: SDI BLOB part length", ErrCorrupt)
		}
		data = append(data, b[46:46+int(length)]...)
		remaining -= uint64(length)
		ext.Chunks = append(ext.Chunks, SDIBlobChunk{page, 46, int(length)})
		page = next
	}
	if remaining != 0 {
		return nil, nil, fmt.Errorf("%w: truncated SDI BLOB chain", ErrCorrupt)
	}
	return data, ext, nil
}

func inflateSDI(compressed []byte, length uint32) (json.RawMessage, error) {
	if length > MaxSDIObjectBytes || len(compressed) > MaxSDIObjectBytes {
		return nil, fmt.Errorf("%w: SDI object byte limit", ErrUnsupported)
	}
	input := bytes.NewReader(compressed)
	z, err := zlib.NewReader(input)
	if err != nil {
		return nil, fmt.Errorf("%w: SDI zlib header: %v", ErrCorrupt, err)
	}
	data, err := io.ReadAll(io.LimitReader(z, int64(length)+1))
	closeErr := z.Close()
	if err != nil || closeErr != nil || len(data) != int(length) || input.Len() != 0 {
		return nil, fmt.Errorf("%w: SDI zlib stream/length/trailing bytes", ErrCorrupt)
	}
	trimmed := bytes.TrimSpace(data)
	if !utf8.Valid(data) || !json.Valid(data) || len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: SDI payload must be a UTF-8 JSON object", ErrCorrupt)
	}
	return json.RawMessage(data), nil
}
