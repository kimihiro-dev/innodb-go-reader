package innodb

import "fmt"

func dateWidth(kind string) int {
	switch kind {
	case "DATE":
		return 3
	case "YEAR":
		return 1
	}
	return 0
}

// Metadata is validated by Schema.validate. DATE preserves stored components,
// including zero components and calendar-invalid dates allowed by MySQL modes.
// Sources: field.cc Field_newdate/Field_year and row0mysql.cc DATA_INT conversion.
func decodeDate(b []byte, kind string) (any, error) {
	if len(b) != dateWidth(kind) {
		return nil, fmt.Errorf("%w: DATE/YEAR byte length", ErrCorrupt)
	}
	if kind == "YEAR" {
		year := uint16(b[0])
		if year != 0 {
			year += 1900
		}
		return year, nil
	}
	packed := (uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])) ^ 0x800000
	year, month, day := packed>>9, (packed>>5)&15, packed&31
	if year > 9999 || month > 12 {
		return nil, fmt.Errorf("%w: DATE year/month or sign encoding", ErrCorrupt)
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day), nil
}
