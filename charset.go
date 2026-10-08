package innodb

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// charsetName resolves the historical MySQL utf8 alias. An omitted charset
// retains the original API's utf8mb4 default.
func (c Column) charsetName() string {
	switch c.Charset {
	case "":
		return "utf8mb4"
	case "utf8":
		return "utf8mb3"
	default:
		return c.Charset
	}
}
func (c Column) charsetWidth() int {
	switch c.charsetName() {
	case "utf8mb4":
		return 4
	case "utf8mb3":
		return 3
	case "ascii", "latin1":
		return 1
	}
	return 0
}
func (c Column) isText() bool {
	switch c.Type {
	case "CHAR", "VARCHAR", "TINYTEXT", "TEXT", "MEDIUMTEXT", "LONGTEXT":
		return true
	}
	return false
}

// Zero-width CHAR/BINARY have fixed_len=0 in InnoDB and consume a length byte.
func (c Column) isVariable() bool {
	switch c.Type {
	case "CHAR":
		return c.MaxChars == 0 || c.charsetWidth() > 1
	case "BINARY":
		return c.MaxBytes == 0
	case "VARCHAR", "VARBINARY":
		return true
	}
	return c.lobTypeMaxBytes() != 0
}

// MySQL strings/ctype-latin1.cc:138 maps 0x80..0x9f to CP1252, retaining
// U+0081/U+008D/U+008F/U+0090/U+009D for its five undefined positions.
var mysqlLatin1High = [...]rune{
	0x20ac, 0x81, 0x201a, 0x192, 0x201e, 0x2026, 0x2020, 0x2021,
	0x2c6, 0x2030, 0x160, 0x2039, 0x152, 0x8d, 0x17d, 0x8f,
	0x90, 0x2018, 0x2019, 0x201c, 0x201d, 0x2022, 0x2013, 0x2014,
	0x2dc, 0x2122, 0x161, 0x203a, 0x153, 0x9d, 0x17e, 0x178,
}

func decodeText(charset string, data []byte) (string, error) {
	switch charset {
	case "utf8mb4", "utf8mb3":
		if !utf8.Valid(data) {
			return "", fmt.Errorf("%w: invalid %s sequence", ErrCorrupt, charset)
		}
		if charset == "utf8mb3" {
			for _, b := range data {
				if b >= 0xf0 {
					return "", fmt.Errorf("%w: four-byte character in utf8mb3", ErrCorrupt)
				}
			}
		}
		return string(data), nil
	case "ascii":
		for _, b := range data {
			if b > 127 {
				return "", fmt.Errorf("%w: non-ASCII byte", ErrCorrupt)
			}
		}
		return string(data), nil
	case "latin1":
		var text strings.Builder
		text.Grow(len(data))
		for _, b := range data {
			r := rune(b)
			if b >= 0x80 && b <= 0x9f {
				r = mysqlLatin1High[b-0x80]
			}
			text.WriteRune(r)
		}
		return text.String(), nil
	default:
		return "", fmt.Errorf("%w: charset %q", ErrUnsupported, charset)
	}
}

// Only collations covered by real fixtures are mapped. This identifies byte
// encoding, not comparison weights or PAD SPACE semantics for index keys.
func metadataCharset(collation uint64) (string, bool) {
	switch collation {
	case 255, 45, 46:
		return "", true
	case 33, 83:
		return "utf8mb3", true
	case 11, 65:
		return "ascii", true
	case 8, 47:
		return "latin1", true
	default:
		return "", false
	}
}

// Dictionary labels are already UTF-8 in the public schema, but must be
// representable in the declared on-disk character set.
func textRepresentable(charset, label string) bool {
	if !utf8.ValidString(label) {
		return false
	}
	for _, r := range label {
		switch charset {
		case "utf8mb3":
			if r > 0xffff {
				return false
			}
		case "ascii":
			if r > 127 {
				return false
			}
		case "latin1":
			if r < 128 || r >= 160 && r <= 255 {
				continue
			}
			found := false
			for _, mapped := range mysqlLatin1High {
				if r == mapped {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}
