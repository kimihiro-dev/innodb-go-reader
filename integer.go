package innodb

// integerWidth returns the physical byte width; MEDIUMINT has no native Go type.
func integerWidth(kind string) int {
	switch kind {
	case "TINYINT":
		return 1
	case "SMALLINT":
		return 2
	case "MEDIUMINT":
		return 3
	case "INT":
		return 4
	case "BIGINT":
		return 8
	}
	return 0
}

// decodeInteger receives an already bounds-checked field. InnoDB stores signed
// integers with the sign bit inverted, in big-endian order (mach_read_int_type).
func decodeInteger(b []byte, unsigned bool) any {
	var u uint64
	for _, v := range b {
		u = u<<8 | uint64(v)
	}
	if unsigned {
		switch len(b) {
		case 1:
			return uint8(u)
		case 2:
			return uint16(u)
		case 3, 4:
			return uint32(u)
		default:
			return u
		}
	}
	bits := uint(len(b) * 8)
	u ^= uint64(1) << (bits - 1)
	v := int64(u<<(64-bits)) >> (64 - bits)
	switch len(b) {
	case 1:
		return int8(v)
	case 2:
		return int16(v)
	case 3, 4:
		return int32(v)
	default:
		return v
	}
}
