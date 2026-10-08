package innodb

import "fmt"

func (a *spaceAnalysis) list(base []byte, label string, visit func(lobAddress) error) error {
	count := be.Uint32(base)
	first, last := lobAddr(base[4:]), lobAddr(base[10:])
	if uint64(count) > a.options.MaxEntries-a.out.TraversalEntries {
		return fmt.Errorf("%w: space list length", ErrLimit)
	}
	if count == 0 {
		if first.page != ^uint32(0) || last.page != ^uint32(0) {
			return spaceError("%s empty endpoints", label)
		}
		return nil
	}
	previous := lobNull
	current := first
	for i := uint32(0); i < count; i++ {
		if err := a.step(1); err != nil {
			return err
		}
		if current.page == ^uint32(0) {
			return spaceError("%s short list", label)
		}
		if old, ok := a.claimed[current]; ok {
			return spaceError("%s overlaps %s", label, old)
		}
		a.claimed[current] = label
		raw := a.metadata[current.page]
		off := int(current.offset)
		if len(raw) != PageSize || off < 38 || off+12 > PageSize-8 {
			return spaceError("%s invalid node address", label)
		}
		prev, next := lobAddr(raw[off:]), lobAddr(raw[off+6:])
		if prev.page != previous.page || (prev.page != ^uint32(0) && prev.offset != previous.offset) {
			return spaceError("%s previous link", label)
		}
		if err := visit(current); err != nil {
			return err
		}
		previous, current = current, next
	}
	if current.page != ^uint32(0) || previous != last {
		return spaceError("%s end/length", label)
	}
	return nil
}
func (a *spaceAnalysis) claimPage(n uint32, id uint64) error {
	if uint64(n) >= a.out.FilePages || a.out.Pages[n].Allocation != "used" {
		return spaceError("segment %d references unallocated page %d", id, n)
	}
	p := &a.out.Pages[n]
	if p.SegmentID != 0 {
		return spaceError("page %d multiple segment owners", n)
	}
	p.SegmentID = id
	return nil
}
func (a *spaceAnalysis) allocations() error {
	fsp := a.metadata[0]
	fragmentUsed := uint32(0)
	for i, name := range []string{"space-free", "space-fragment", "space-full"} {
		err := a.list(fsp[62+i*16:], name, func(addr lobAddress) error {
			pos, ok := a.extent[addr]
			if !ok {
				return spaceError("space list points outside descriptors")
			}
			e := &a.out.Extents[pos]
			if e.State != uint32(i+1) {
				return spaceError("extent %d wrong space list", e.FirstPage)
			}
			e.List = name
			if i == 1 {
				fragmentUsed += e.Used
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if fragmentUsed != a.out.FragmentUsed {
		return spaceError("FSP fragment used count")
	}
	for i, name := range []string{"inode-full", "inode-free"} {
		err := a.list(fsp[118+i*16:], name, func(addr lobAddress) error {
			if addr.offset != 38 || a.out.Pages[addr.page].Type != 3 {
				return spaceError("inode page list type/address")
			}
			raw := a.metadata[addr.page]
			used := 0
			for off := 50; off+192 <= PageSize-10; off += 192 {
				if err := a.step(1); err != nil {
					return err
				}
				id := be.Uint64(raw[off:])
				if id == 0 {
					continue
				}
				used++
				if id >= a.out.NextSegmentID || be.Uint32(raw[off+60:]) != 97937874 {
					return spaceError("inode ID/magic")
				}
				if _, ok := a.segments[id]; ok {
					return spaceError("duplicate segment %d", id)
				}
				seg := SpaceSegment{ID: id, Page: addr.page, Offset: uint16(off)}
				for j := 0; j < 32; j++ {
					n := be.Uint32(raw[off+64+j*4:])
					if n != ^uint32(0) {
						seg.FragmentPages = append(seg.FragmentPages, n)
					}
				}
				a.segments[id] = len(a.out.Segments)
				a.inodes[lobAddress{addr.page, uint16(off)}] = id
				a.out.Segments = append(a.out.Segments, seg)
			}
			if (i == 0 && used != 85) || (i == 1 && used == 85) {
				return spaceError("inode full/free list classification")
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	for i := range a.out.Segments {
		seg := &a.out.Segments[i]
		raw := a.metadata[seg.Page]
		off := int(seg.Offset)
		partial := uint32(0)
		for j, name := range []string{"free", "partial", "full"} {
			err := a.list(raw[off+12+j*16:], fmt.Sprintf("segment-%d-%s", seg.ID, name), func(addr lobAddress) error {
				pos, ok := a.extent[addr]
				if !ok {
					return spaceError("segment extent address")
				}
				e := &a.out.Extents[pos]
				if (e.State != 4 && e.State != 5) || e.SegmentID != seg.ID {
					return spaceError("extent %d segment identity", e.FirstPage)
				}
				if (j == 0 && e.Used != 0) || (j == 1 && (e.Used == 0 || e.Used == 64)) || (j == 2 && e.Used != 64) {
					return spaceError("segment extent fullness")
				}
				e.List = fmt.Sprintf("segment-%d-%s", seg.ID, name)
				switch j {
				case 0:
					seg.FreeExtents = append(seg.FreeExtents, e.FirstPage)
				case 1:
					seg.PartialExtents = append(seg.PartialExtents, e.FirstPage)
					partial += e.Used
				case 2:
					seg.FullExtents = append(seg.FullExtents, e.FirstPage)
				}
				start := uint32(0)
				if e.State == 5 {
					start = 2
				}
				seg.ReservedPages += uint64(e.Present - start)
				for k := start; k < e.Present; k++ {
					if err := a.step(1); err != nil {
						return err
					}
					if e.FreeBits&(uint64(1)<<k) == 0 {
						if err := a.claimPage(e.FirstPage+k, seg.ID); err != nil {
							return err
						}
						seg.UsedPages++
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if partial != be.Uint32(raw[off+8:]) {
			return spaceError("segment %d partial used count", seg.ID)
		}
		for _, n := range seg.FragmentPages {
			if err := a.step(1); err != nil {
				return err
			}
			if n >= a.out.FreeLimit {
				return spaceError("fragment outside initialized space")
			}
			e := a.out.Extents[n/64]
			if e.State != 2 && e.State != 3 {
				return spaceError("segment fragment extent state")
			}
			if err := a.claimPage(n, seg.ID); err != nil {
				return err
			}
			seg.UsedPages++
			seg.ReservedPages++
		}
	}
	for _, e := range a.out.Extents {
		if e.List == "" {
			return spaceError("extent %d is not in an allocation list", e.FirstPage)
		}
	}
	for _, p := range a.out.Pages {
		if p.Allocation != "used" {
			continue
		}
		if p.Type == 3 {
			if _, ok := a.claimed[lobAddress{p.Number, 38}]; !ok {
				return spaceError("unlinked inode page")
			}
		}
		if p.SegmentID == 0 && p.Type != 3 && p.Type != 5 && p.Type != 8 && p.Type != 9 {
			return spaceError("used page %d has no segment", p.Number)
		}
		if p.SegmentID != 0 && (p.Type == 3 || p.Type == 5 || p.Type == 8 || p.Type == 9) {
			return spaceError("metadata page assigned to segment")
		}
	}
	return nil
}
