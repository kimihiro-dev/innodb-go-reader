package innodb

import (
	"fmt"
	"io"
)

const compactPrefixBytes = 768

func parseCompactExternal(value []byte, maximum uint64, space uint32) (ExternalField, error) {
	var f ExternalField
	if len(value) != compactPrefixBytes+20 {
		return f, fmt.Errorf("%w: COMPACT external field requires 768-byte prefix and 20-byte reference", ErrCorrupt)
	}
	// Length in the reference counts only the off-page suffix. The common
	// reference parser validates flags, identity, and length before subtraction.
	f, err := parseExternal(value[compactPrefixBytes:], maximum, space)
	if err != nil {
		return f, err
	}
	if uint64(f.Length)+compactPrefixBytes > maximum {
		return f, fmt.Errorf("%w: COMPACT prefix and suffix exceed column capacity", ErrCorrupt)
	}
	f.Prefix = append([]byte(nil), value[:compactPrefixBytes]...)
	return f, nil
}

// walkLegacyExternal visits only the suffix; walkExternal emits the prefix.
// Old BLOB pages contain length/next, not versioned index entries.
func walkLegacyExternal(r io.ReaderAt, size int64, f *ExternalField, emit func([]byte, LOBChunk, bool) error) error {
	total := uint64(f.Length)
	if int64(f.Length) > size || f.Length == 0 {
		return fmt.Errorf("%w: BLOB suffix length exceeds file or is zero", ErrCorrupt)
	}
	var consumed uint64
	seen := map[uint32]bool{}
	number := f.FirstPage
	for number != ^uint32(0) {
		if number == 0 || seen[number] || int64(len(seen)) >= size/PageSize || consumed >= total {
			return fmt.Errorf("%w: BLOB repeated/excess page %d", ErrCorrupt, number)
		}
		if err := scanEntries(r, 1); err != nil {
			return err
		}
		seen[number] = true
		b, err := readPage(r, size, number)
		if err != nil {
			return err
		}
		if be.Uint32(b[34:]) != f.SpaceID {
			return fmt.Errorf("%w: BLOB page %d space mismatch", ErrCorrupt, number)
		}
		if be.Uint16(b[24:]) != 10 {
			return fmt.Errorf("%w: BLOB page %d type %d, expected 10", ErrUnsupported, number, be.Uint16(b[24:]))
		}
		const start = 46 // FIL_PAGE_DATA (38) + length/next (8)
		n := be.Uint32(b[38:42])
		if n == 0 || n > PageSize-8-start || uint64(n) > total-consumed {
			return fmt.Errorf("%w: BLOB page %d part length %d", ErrCorrupt, number, n)
		}
		if err := emit(b[start:start+int(n)], LOBChunk{PageNumber: number, Offset: start, Length: int(n)}, false); err != nil {
			return err
		}
		consumed += uint64(n)
		number = be.Uint32(b[42:46])
	}
	if consumed != total {
		return fmt.Errorf("%w: BLOB chain ended before complete suffix", ErrCorrupt)
	}
	return scanCheck(r)
}
