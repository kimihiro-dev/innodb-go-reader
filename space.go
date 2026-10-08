package innodb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
)

// SpaceOptions bounds the atomic, schema-independent allocation analysis.
type SpaceOptions struct{ MaxPages, MaxEntries uint64 }

// SpacePage distinguishes live allocation from untrusted contents of free pages.
type SpacePage struct {
	Number                                      uint32
	Allocation                                  string // used, free, uninitialized, or file-tail
	Zero                                        bool
	Type                                        uint16
	HeaderNumber, HeaderSpaceID, Previous, Next uint32
	LSN                                         uint64
	ChecksumVerified                            bool
	SegmentID                                   uint64
	Index                                       *SpaceIndexPage `json:",omitempty"`
	Bitmap                                      *SpaceBitmap    `json:",omitempty"`
	Raw                                         []byte          `json:",omitempty"` // Unknown used page only; independently owned.
}
type SpaceBitmap struct {
	FreeClass              uint8
	Buffered, ChangeBuffer bool
}

// SpaceIndexPage measures local compact-format layout, not SQL live-row bytes.
type SpaceIndexPage struct {
	DeletedRecords, FreeRecords                                                   uint16
	IndexID                                                                       uint64
	Level, Records, HeapCount, Slots                                              uint16
	HeapBytes, GarbageBytes, LayoutUsedBytes, ContiguousFreeBytes, DirectoryBytes uint32
	LeafSegment, TopSegment                                                       uint64
}
type SpaceExtent struct {
	FirstPage, DescriptorPage uint32
	Offset                    uint16
	State                     uint32
	SegmentID                 uint64
	Used, Present             uint32
	FreeBits, CleanBits       uint64
	List                      string
}
type SpaceSegment struct {
	ID                                       uint64
	Page                                     uint32
	Offset                                   uint16
	FragmentPages                            []uint32
	FreeExtents, PartialExtents, FullExtents []uint32
	UsedPages, ReservedPages                 uint64
	IndexID                                  uint64
	Role                                     string
}
type SpaceIndex struct {
	ID                                          uint64
	Root                                        uint32
	LeafSegment, TopSegment                     uint64
	Pages, LeafPages, PhysicalRecords, LOBPages uint64
	Height                                      uint32
}
type SpaceReport struct {
	SpaceID, Flags, SizePages, FreeLimit, FragmentUsed                                                         uint32
	NextSegmentID                                                                                              uint64
	FilePages, UsedPages, FreePages, UninitializedPages, TailPages, ZeroPages, PhysicalReads, TraversalEntries uint64
	Pages                                                                                                      []SpacePage
	Extents                                                                                                    []SpaceExtent
	Segments                                                                                                   []SpaceSegment
	Indexes                                                                                                    []SpaceIndex
	UsedPageTypes                                                                                              map[uint16]uint64
}
type spaceAnalysis struct {
	ctx      context.Context
	r        io.ReaderAt
	size     int64
	options  SpaceOptions
	out      *SpaceReport
	metadata map[uint32][]byte
	extent   map[lobAddress]int
	segments map[uint64]int
	inodes   map[lobAddress]uint64
	claimed  map[lobAddress]string
}

func spaceError(format string, args ...any) error {
	return fmt.Errorf("%w: space: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}
func (a *spaceAnalysis) step(n uint64) error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if n > a.options.MaxEntries-a.out.TraversalEntries {
		return fmt.Errorf("%w: space traversal entries", ErrLimit)
	}
	a.out.TraversalEntries += n
	return nil
}
func (a *spaceAnalysis) read(n uint32) ([]byte, error) {
	if err := a.step(1); err != nil {
		return nil, err
	}
	b := make([]byte, PageSize)
	got, err := a.r.ReadAt(b, int64(n)*PageSize)
	a.out.PhysicalReads++
	if err != nil {
		return nil, fmt.Errorf("space page %d: %w", n, err)
	}
	if got != PageSize {
		return nil, io.ErrUnexpectedEOF
	}
	return b, nil
}
func (a *spaceAnalysis) verified(b []byte, n uint32) error {
	if err := verifyPageChecksum(b); err != nil {
		return fmt.Errorf("space page %d: %w", n, err)
	}
	if be.Uint32(b[4:]) != n || be.Uint32(b[34:]) != a.out.SpaceID {
		return spaceError("page %d FIL identity", n)
	}
	return nil
}

// AnalyzeSpace validates allocation and returns no report on any error. Input
// must be a stable private, unencrypted 16 KiB DYNAMIC/COMPACT tablespace.
func AnalyzeSpace(ctx context.Context, r io.ReaderAt, size int64, options SpaceOptions) (*SpaceReport, error) {
	if size < PageSize || size%PageSize != 0 || uint64(size/PageSize) > uint64(^uint32(0)) {
		return nil, spaceError("unaligned or unaddressable file size")
	}
	if options.MaxPages == 0 {
		options.MaxPages = 1_000_000
	}
	if options.MaxEntries == 0 {
		options.MaxEntries = 10_000_000
	}
	if uint64(size/PageSize) > options.MaxPages {
		return nil, fmt.Errorf("%w: space file pages", ErrLimit)
	}
	out := &SpaceReport{FilePages: uint64(size / PageSize), UsedPageTypes: map[uint16]uint64{}}
	a := &spaceAnalysis{ctx: ctx, r: r, size: size, options: options, out: out, metadata: map[uint32][]byte{}, extent: map[lobAddress]int{}, segments: map[uint64]int{}, inodes: map[lobAddress]uint64{}, claimed: map[lobAddress]string{}}
	b, err := a.read(0)
	if err != nil {
		return nil, err
	}
	out.SpaceID = be.Uint32(b[34:])
	out.Flags = be.Uint32(b[54:])
	if err = a.verified(b, 0); err != nil {
		return nil, err
	}
	if out.SpaceID == 0 {
		return nil, fmt.Errorf("%w: space analysis requires a private tablespace", ErrUnsupported)
	}
	s := Schema{SpaceID: out.SpaceID}
	if out.Flags&0x21 == 0 {
		s.RowFormat = "COMPACT"
	}
	if err = checkTablespace(b, s); err != nil {
		return nil, err
	}
	out.SizePages = be.Uint32(b[46:])
	out.FreeLimit = be.Uint32(b[50:])
	out.FragmentUsed = be.Uint32(b[58:])
	out.NextSegmentID = be.Uint64(b[110:])
	if out.SizePages < 3 || uint64(out.SizePages) > out.FilePages || out.FreeLimit < 64 || out.FreeLimit%64 != 0 || uint64(out.FreeLimit) > ((uint64(out.SizePages)+63)/64)*64 || out.NextSegmentID == 0 {
		return nil, spaceError("FSP size/free limit/segment ID")
	}
	a.metadata[0] = b
	for n := uint32(0); n < out.FreeLimit; n += PageSize {
		raw := b
		if n != 0 {
			raw, err = a.read(n)
			if err != nil {
				return nil, err
			}
			if err = a.verified(raw, n); err != nil {
				return nil, err
			}
			if be.Uint16(raw[24:]) != 9 {
				return nil, spaceError("descriptor page %d type", n)
			}
			a.metadata[n] = raw
		}
		for first := n; first < n+PageSize && first < out.FreeLimit; first += 64 {
			off := 150 + int((first-n)/64)*40
			e := SpaceExtent{FirstPage: first, DescriptorPage: n, Offset: uint16(off), SegmentID: be.Uint64(raw[off:]), State: be.Uint32(raw[off+20:])}
			if e.State < 1 || e.State > 5 {
				return nil, spaceError("extent %d state %d", first, e.State)
			}
			for j := uint32(0); j < 64; j++ {
				bits := (raw[off+24+int(j/4)] >> ((j % 4) * 2)) & 3
				if bits&1 != 0 {
					e.FreeBits |= uint64(1) << j
				} else {
					e.Used++
					if first+j >= out.SizePages {
						return nil, spaceError("allocated page beyond FSP_SIZE")
					}
				}
				if bits&2 != 0 {
					e.CleanBits |= uint64(1) << j
				}
				if first+j < out.SizePages {
					e.Present++
				}
			}
			if (e.State == 1 && e.Used != 0) || (e.State == 2 && (e.Used == 0 || e.Used == 64)) || (e.State == 3 && e.Used != 64) {
				return nil, spaceError("extent %d state/bitmap", first)
			}
			if first%PageSize == 0 && (e.FreeBits&3 != 0 || (e.State != 2 && e.State != 3 && e.State != 5)) {
				return nil, spaceError("descriptor extent reserved pages")
			}
			if e.State == 5 && (first%PageSize != 0 || e.Used < 2) {
				return nil, spaceError("leased fragment extent")
			}
			a.extent[lobAddress{n, uint16(off + 8)}] = len(out.Extents)
			out.Extents = append(out.Extents, e)
		}
	}
	out.Pages = make([]SpacePage, out.FilePages)
	for n := uint32(0); uint64(n) < out.FilePages; n++ {
		raw, ok := a.metadata[n]
		if !ok {
			raw, err = a.read(n)
			if err != nil {
				return nil, err
			}
		}
		p := SpacePage{Number: n, Type: be.Uint16(raw[24:]), HeaderNumber: be.Uint32(raw[4:]), HeaderSpaceID: be.Uint32(raw[34:]), Previous: be.Uint32(raw[8:]), Next: be.Uint32(raw[12:]), LSN: be.Uint64(raw[16:]), Zero: bytes.Count(raw, []byte{0}) == PageSize}
		if p.Zero {
			out.ZeroPages++
		}
		switch {
		case n >= out.SizePages:
			p.Allocation = "file-tail"
			out.TailPages++
		case n >= out.FreeLimit:
			p.Allocation = "uninitialized"
			out.UninitializedPages++
		case out.Extents[n/64].FreeBits&(uint64(1)<<(n%64)) != 0:
			p.Allocation = "free"
			out.FreePages++
		default:
			p.Allocation = "used"
			out.UsedPages++
			out.UsedPageTypes[p.Type]++
			if err = a.verified(raw, n); err != nil {
				return nil, err
			}
			p.ChecksumVerified = true
			switch p.Type {
			case 8, 9:
				if (p.Type == 8 && n != 0) || (p.Type == 9 && (n == 0 || n%PageSize != 0)) {
					return nil, spaceError("descriptor position %d", n)
				}
			case 3:
				a.metadata[n] = raw
			case 5:
				if n%PageSize != 1 {
					return nil, spaceError("bitmap position %d", n)
				}
				a.metadata[n] = raw
			case 17855, 17853:
				index, err := parseIndexTypeMinimum(raw, Schema{SpaceID: out.SpaceID, IndexID: be.Uint64(raw[66:])}, p.Type, 6)
				if err != nil {
					return nil, err
				}
				if index.IndexID == 0 {
					return nil, spaceError("zero index ID on page %d", n)
				}
				deleted, free, err := a.indexLayout(raw, index)
				if err != nil {
					return nil, fmt.Errorf("page %d: %w", n, err)
				}
				d := uint32(len(index.Slots) * 2)
				h := uint32(index.HeapTop) - dataStart
				p.Index = &SpaceIndexPage{DeletedRecords: deleted, FreeRecords: free, IndexID: index.IndexID, Level: index.Level, Records: index.Records, HeapCount: index.HeapCount, Slots: uint16(len(index.Slots)), HeapBytes: h, GarbageBytes: uint32(index.Garbage), LayoutUsedBytes: h - uint32(index.Garbage), ContiguousFreeBytes: PageSize - 8 - d - uint32(index.HeapTop), DirectoryBytes: d}
				// Only root pages contain persistent FSEG headers; retain their 20 bytes.
				if !bytes.Equal(raw[74:94], make([]byte, 20)) {
					a.metadata[n] = append([]byte(nil), raw[:94]...)
				}
			case 10, 18, 22, 23, 24: // Known LOB/SDI payload types; allocation scope only.
			default:
				p.Raw = raw
			}
		}
		out.Pages[n] = p
	}
	if err = a.allocations(); err != nil {
		return nil, err
	}
	if err = a.indexes(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (a *spaceAnalysis) indexes() error {
	roots := map[uint64]int{}
	for n := range a.out.Pages {
		p := &a.out.Pages[n]
		if p.Allocation != "used" {
			continue
		}
		if n%PageSize == 0 && p.Type != 8 && p.Type != 9 {
			return spaceError("missing descriptor")
		}
		if n%PageSize == 1 && p.Type != 5 {
			return spaceError("missing bitmap")
		}
		bitmap := a.metadata[uint32(n/PageSize*PageSize+1)]
		if len(bitmap) != PageSize {
			return spaceError("missing bitmap for page %d", n)
		}
		nibble := (bitmap[94+(n%PageSize)/2] >> ((n % 2) * 4)) & 15
		p.Bitmap = &SpaceBitmap{FreeClass: (nibble&1)*2 + (nibble>>1)&1, Buffered: nibble&4 != 0, ChangeBuffer: nibble&8 != 0}
		if p.Index == nil {
			continue
		}
		raw := a.metadata[uint32(n)]
		if len(raw) == 0 {
			continue
		}
		ids := [2]uint64{}
		for j := 0; j < 2; j++ {
			o := 74 + j*10
			if be.Uint32(raw[o:]) != a.out.SpaceID {
				return spaceError("root segment space")
			}
			address := lobAddress{be.Uint32(raw[o+4:]), be.Uint16(raw[o+8:])}
			id, ok := a.inodes[address]
			if !ok {
				return spaceError("root %d missing inode", n)
			}
			ids[j] = id
		}
		if ids[0] == ids[1] {
			return spaceError("root leaf/top segment overlap")
		}
		if _, ok := roots[p.Index.IndexID]; ok {
			return spaceError("duplicate index root")
		}
		for j, id := range ids {
			seg := &a.out.Segments[a.segments[id]]
			if seg.Role != "" {
				return spaceError("segment attached to multiple roots")
			}
			seg.IndexID = p.Index.IndexID
			seg.Role = []string{"leaf", "top"}[j]
		}
		p.Index.LeafSegment, p.Index.TopSegment = ids[0], ids[1]
		roots[p.Index.IndexID] = len(a.out.Indexes)
		a.out.Indexes = append(a.out.Indexes, SpaceIndex{ID: p.Index.IndexID, Root: uint32(n), LeafSegment: ids[0], TopSegment: ids[1], Height: uint32(p.Index.Level) + 1})
	}
	for n := range a.out.Pages {
		p := &a.out.Pages[n]
		if p.Allocation != "used" {
			continue
		}
		if p.Index != nil {
			pos, ok := roots[p.Index.IndexID]
			if !ok {
				return spaceError("index page %d has no root", n)
			}
			i := &a.out.Indexes[pos]
			i.Pages++
			if p.Index.Level == 0 {
				i.LeafPages++
				i.PhysicalRecords += uint64(p.Index.Records)
			}
			want := i.TopSegment
			if uint32(n) != i.Root && p.Index.Level == 0 {
				want = i.LeafSegment
			}
			if p.SegmentID != want || uint32(p.Index.Level) >= i.Height {
				return spaceError("index page %d segment/level", n)
			}
			for j, sibling := range []uint32{p.Previous, p.Next} {
				if sibling == ^uint32(0) {
					continue
				}
				if uint64(sibling) >= a.out.FilePages {
					return spaceError("index sibling outside file")
				}
				q := a.out.Pages[sibling]
				if q.Allocation != "used" || q.Index == nil || q.Index.IndexID != i.ID || q.Index.Level != p.Index.Level {
					return spaceError("index sibling identity")
				}
				back := q.Next
				if j == 1 {
					back = q.Previous
				}
				if back != uint32(n) {
					return spaceError("index sibling link")
				}
			}
		} else if p.SegmentID != 0 && (p.Type == 10 || p.Type == 18 || p.Type == 22 || p.Type == 23 || p.Type == 24) {
			seg := a.out.Segments[a.segments[p.SegmentID]]
			if pos, ok := roots[seg.IndexID]; ok {
				a.out.Indexes[pos].LOBPages++
			}
		}
	}
	// Every observed level must be one complete acyclic sibling chain. This is
	// allocation-level validation, independent of schema-dependent child keys.
	type levelKey struct {
		index uint64
		level uint16
	}
	groups := map[levelKey][]uint32{}
	for _, p := range a.out.Pages {
		if p.Allocation == "used" && p.Index != nil {
			k := levelKey{p.Index.IndexID, p.Index.Level}
			groups[k] = append(groups[k], p.Number)
		}
	}
	for _, idx := range a.out.Indexes {
		if idx.Height > uint32(idx.Pages) {
			return spaceError("index height exceeds page count")
		}
		for level := uint32(0); level < idx.Height; level++ {
			pages := groups[levelKey{idx.ID, uint16(level)}]
			if len(pages) == 0 {
				return spaceError("missing index level")
			}
			if level+1 == idx.Height && (len(pages) != 1 || pages[0] != idx.Root) {
				return spaceError("index root level")
			}
			head := uint32(0)
			heads := 0
			for _, n := range pages {
				if a.out.Pages[n].Previous == ^uint32(0) {
					head = n
					heads++
				}
			}
			if heads != 1 {
				return spaceError("index level chain heads")
			}
			count := 0
			n := head
			for n != ^uint32(0) {
				if err := a.step(1); err != nil {
					return err
				}
				count++
				if count > len(pages) {
					return spaceError("index sibling cycle")
				}
				n = a.out.Pages[n].Next
			}
			if count != len(pages) {
				return spaceError("disconnected index level")
			}
		}
	}

	sort.Slice(a.out.Indexes, func(i, j int) bool { return a.out.Indexes[i].ID < a.out.Indexes[j].ID })
	return nil
}
