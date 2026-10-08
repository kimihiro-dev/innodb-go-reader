package innodb

// indexLayout validates compact headers, physical chains and directory owners.
// It deliberately does not decode keys, field lengths or parent child pointers.
func (a *spaceAnalysis) indexLayout(b []byte, p Page) (deleted, free uint16, err error) {
	if string(b[99:107]) != "infimum\x00" || string(b[112:120]) != "supremum" || be.Uint16(b[95:97]) != 2 || be.Uint16(b[108:110]) != 11 || b[94] != 1 || b[107]&0xf0 != 0 || nextRecord(b, supremum) != 0 {
		return 0, 0, spaceError("index system records")
	}
	seen := map[int]bool{}
	heaps := map[uint16]bool{}
	headerBytes := map[int]bool{}
	check := func(origin int) error {
		if err := a.step(1); err != nil {
			return err
		}
		if origin < dataStart+5 || origin >= int(p.HeapTop) || seen[origin] {
			return spaceError("index physical record chain")
		}
		seen[origin] = true
		heap := be.Uint16(b[origin-4:]) >> 3
		if heap < 2 || heap >= p.HeapCount || heaps[heap] {
			return spaceError("index heap identity")
		}
		heaps[heap] = true
		for i := origin - 5; i < origin; i++ {
			if headerBytes[i] {
				return spaceError("index overlapping headers")
			}
			headerBytes[i] = true
		}
		return nil
	}
	slot, owned, count := 1, 0, 0
	for origin := nextRecord(b, infimum); ; origin = nextRecord(b, origin) {
		owned++
		if origin == supremum {
			if slot != len(p.Slots)-1 || int(b[107]&15) != owned {
				return 0, 0, spaceError("index supremum ownership")
			}
			break
		}
		if err = check(origin); err != nil {
			return 0, 0, err
		}
		count++
		status := be.Uint16(b[origin-4:]) & 7
		want := uint16(0)
		if p.Level > 0 {
			want = 1
		}
		if status != want {
			return 0, 0, spaceError("index record status")
		}
		if p.Level > 0 && (b[origin-5]&0x10 != 0) != (count == 1 && p.Previous == ^uint32(0)) {
			return 0, 0, spaceError("index minimum flag")
		}
		if b[origin-5]&0x20 != 0 {
			deleted++
		}
		if origin == int(p.Slots[slot]) {
			if int(b[origin-5]&15) != owned {
				return 0, 0, spaceError("index directory ownership")
			}
			slot++
			owned = 0
		} else if b[origin-5]&15 != 0 {
			return 0, 0, spaceError("unexpected index owner")
		}
	}
	if count != int(p.Records) {
		return 0, 0, spaceError("index physical record count")
	}
	for origin := int(p.Free); origin != 0; origin = nextRecord(b, origin) {
		if err = check(origin); err != nil {
			return 0, 0, err
		}
		free++
	}
	if len(heaps)+2 != int(p.HeapCount) {
		return 0, 0, spaceError("index missing heap records")
	}
	return deleted, free, nil
}
