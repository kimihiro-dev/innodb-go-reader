package innodb

import "fmt"

// readVariableLength consumes metadata backwards, starting nearest the NULL
// bitmap. A short declared maximum uses all 8 bits except for DATA_BLOB
// types, which use the big-column encoding even when their maximum is 255.
// MySQL 8.0.45 rem0rec.cc: rec_convert_dtuple_to_rec_comp, lines 891-933.
func readVariableLength(b []byte, pos int, maximum uint64, blob bool) (length, next int, external bool, err error) {
	if pos < dataStart {
		return 0, pos, false, fmt.Errorf("%w: missing variable length byte", ErrCorrupt)
	}
	first := b[pos]
	pos--
	length = int(first)
	if (maximum > 255 || blob) && first&0x80 != 0 {
		if pos < dataStart {
			return 0, pos, false, fmt.Errorf("%w: missing second variable length byte", ErrCorrupt)
		}
		length = int(first&0x3f)<<8 | int(b[pos])
		pos--
		if first&0x40 != 0 {
			return length, pos, true, nil
		}
		if length < 128 {
			return 0, pos, false, fmt.Errorf("%w: noncanonical two-byte length %d", ErrCorrupt, length)
		}
	}
	if uint64(length) > maximum {
		return 0, pos, false, fmt.Errorf("%w: variable byte length %d exceeds schema maximum %d", ErrCorrupt, length, maximum)
	}
	return length, pos, false, nil
}
