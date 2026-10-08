package innodb

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Key lists complete key-column values in index definition order, even for a
// single-column key. Integer inputs must be Go integers or exact json.Number;
// decimal/temporal inputs use Read's canonical strings, binary inputs []byte.
type Key []any

func queryInteger(value any) (string, bool) {
	if n, ok := value.(json.Number); ok {
		return string(n), true
	}
	if value == nil {
		return "", false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), true
	}
	return "", false
}
func queryUnsigned(value any, bits int) (uint64, error) {
	text, ok := queryInteger(value)
	if !ok {
		return 0, fmt.Errorf("integer required")
	}
	return strconv.ParseUint(text, 10, bits)
}
func queryBytes(n uint64, width int) []byte {
	b := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		b[i] = byte(n)
		n >>= 8
	}
	return b
}
func encodeQueryKey(key Key, s Schema, pk []int) (indexKey, error) {
	count := len(pk)
	if s.hiddenRowID() {
		count = 1
	}
	if len(key) == 0 || len(key) > count {
		return nil, fmt.Errorf("%w: query key member count", ErrUnsupported)
	}
	out := make(indexKey, len(key))
	for i, value := range key {
		if s.hiddenRowID() {
			n, err := queryUnsigned(value, 48)
			if err != nil {
				return nil, fmt.Errorf("%w: ROW_ID: %v", ErrUnsupported, err)
			}
			out[i] = queryBytes(n, 6)
			continue
		}
		b, err := encodeQueryValue(value, s.Columns[pk[i]])
		if err != nil {
			return nil, fmt.Errorf("%w: query column %q: %v", ErrUnsupported, s.Columns[pk[i]].Name, err)
		}
		out[i] = b
	}
	return out, nil
}
func encodeQueryValue(value any, c Column) ([]byte, error) {
	bad := func() ([]byte, error) { return nil, fmt.Errorf("invalid or noncanonical %s key", c.Type) }
	if width := integerWidth(c.Type); width != 0 {
		text, ok := queryInteger(value)
		if !ok {
			return bad()
		}
		if c.Unsigned {
			n, err := strconv.ParseUint(text, 10, width*8)
			if err != nil {
				return bad()
			}
			return queryBytes(n, width), nil
		}
		n, err := strconv.ParseInt(text, 10, width*8)
		if err != nil {
			return bad()
		}
		b := queryBytes(uint64(n), width)
		b[0] ^= 0x80
		return b, nil
	}
	if c.Type == "YEAR" || c.Type == "BIT" {
		bits := 64
		if c.Type == "BIT" {
			bits = c.BitLength
		}
		n, err := queryUnsigned(value, bits)
		if err != nil {
			return bad()
		}
		if c.Type == "YEAR" {
			if n != 0 && (n < 1901 || n > 2155) {
				return bad()
			}
			if n > 0 {
				n -= 1900
			}
		}
		return queryBytes(n, c.fixedWidth()), nil
	}
	if c.Type == "BINARY" || c.Type == "VARBINARY" {
		b, ok := value.([]byte)
		if !ok || len(b) > c.MaxBytes || c.Type == "BINARY" && len(b) != c.MaxBytes {
			return bad()
		}
		return append([]byte{}, b...), nil
	}
	text, ok := value.(string)
	if !ok {
		return bad()
	}
	if c.Type == "CHAR" || c.Type == "VARCHAR" {
		if !utf8.ValidString(text) || utf8.RuneCountInString(text) > c.MaxChars {
			return bad()
		}
		b := make([]byte, 0)
		switch c.charsetName() {
		case "utf8mb4", "utf8mb3":
			b = []byte(text)
			if _, err := decodeText(c.charsetName(), b); err != nil {
				return bad()
			}
		case "ascii", "latin1":
			for _, r := range text {
				found := false
				max := 256
				if c.charsetName() == "ascii" {
					max = 128
				}
				for x := 0; x < max; x++ {
					candidate := rune(x)
					if x >= 128 && x <= 159 {
						candidate = mysqlLatin1High[x-128]
					}
					if candidate == r {
						b = append(b, byte(x))
						found = true
						break
					}
				}
				if !found {
					return bad()
				}
			}
		default:
			return bad()
		}
		return b, nil
	}
	var b []byte
	switch c.Type {
	case "DECIMAL":
		neg := strings.HasPrefix(text, "-")
		digits := text
		if neg {
			digits = digits[1:]
		}
		parts := strings.Split(digits, ".")
		integer := parts[0]
		fraction := ""
		if c.Scale > 0 {
			if len(parts) != 2 || len(parts[1]) != c.Scale {
				return bad()
			}
			fraction = parts[1]
		} else if len(parts) != 1 {
			return bad()
		}
		if integer == "" || (len(integer) > 1 && integer[0] == '0') {
			return bad()
		}
		intDigits := c.Precision - c.Scale
		if intDigits == 0 {
			if integer != "0" {
				return bad()
			}
			integer = ""
		} else if len(integer) > intDigits {
			return bad()
		}
		integer = strings.Repeat("0", intDigits-len(integer)) + integer
		group := func(s string) bool {
			if s == "" {
				return true
			}
			for _, x := range s {
				if x < '0' || x > '9' {
					return false
				}
			}
			n, err := strconv.ParseUint(s, 10, 32)
			if err != nil {
				return false
			}
			b = append(b, queryBytes(n, decimalGroupBytes[len(s)])...)
			return true
		}
		rem := intDigits % 9
		if rem > 0 && !group(integer[:rem]) {
			return bad()
		}
		for pos := rem; pos < intDigits; pos += 9 {
			if !group(integer[pos : pos+9]) {
				return bad()
			}
		}
		for pos := 0; pos < len(fraction); {
			end := pos + 9
			if end > len(fraction) {
				end = len(fraction)
			}
			if !group(fraction[pos:end]) {
				return bad()
			}
			pos = end
		}
		if neg {
			for i := range b {
				b[i] ^= 0xff
			}
		}
		b[0] ^= 0x80
	case "DATE":
		var y, m, d int
		if len(text) != 10 {
			return bad()
		}
		if _, err := fmt.Sscanf(text, "%d-%d-%d", &y, &m, &d); err != nil || y < 0 || y > 9999 || m < 0 || m > 12 || d < 0 || d > 31 {
			return bad()
		}
		b = queryBytes(uint64(y<<9|m<<5|d)^0x800000, 3)
	case "DATETIME", "TIME", "TIMESTAMP":
		base := text
		frac := uint64(0)
		fractionBytes := (c.FSP + 1) / 2
		if c.FSP > 0 {
			split := len(text) - c.FSP - 1
			if split < 0 || text[split] != '.' {
				return bad()
			}
			digits := text[split+1:]
			for _, x := range digits {
				if x < '0' || x > '9' {
					return bad()
				}
			}
			var err error
			frac, err = strconv.ParseUint(digits, 10, 32)
			if err != nil {
				return bad()
			}
			if c.FSP%2 == 1 {
				frac *= 10
			}
			base = text[:split]
		}
		switch c.Type {
		case "DATETIME":
			var y, m, d, h, mi, se int
			if len(base) != 19 {
				return bad()
			}
			if _, err := fmt.Sscanf(base, "%d-%d-%d %d:%d:%d", &y, &m, &d, &h, &mi, &se); err != nil || y < 0 || y > 9999 || m < 0 || m > 12 || d < 0 || d > 31 || h < 0 || h > 23 || mi < 0 || mi > 59 || se < 0 || se > 59 {
				return bad()
			}
			packed := uint64((y*13+m)<<22|d<<17|h<<12|mi<<6|se) + 0x8000000000
			b = queryBytes(packed, 5)
		case "TIME":
			neg := strings.HasPrefix(base, "-")
			if neg {
				base = base[1:]
			}
			var h, mi, se int
			if _, err := fmt.Sscanf(base, "%d:%d:%d", &h, &mi, &se); err != nil || h < 0 || h > 838 || mi < 0 || mi > 59 || se < 0 || se > 59 {
				return bad()
			}
			packed := int64(h<<12 | mi<<6 | se)
			if neg {
				packed = -packed
				if frac != 0 {
					packed--
					frac = (uint64(1) << uint(fractionBytes*8)) - frac
				}
			}
			b = queryBytes(uint64(packed+0x800000), 3)
		case "TIMESTAMP":
			seconds := int64(0)
			if base != "0000-00-00 00:00:00" {
				tm, err := time.Parse("2006-01-02 15:04:05", base)
				if err != nil {
					return bad()
				}
				seconds = tm.Unix()
				if seconds < 1 || seconds > 2147483647 {
					return bad()
				}
			}
			b = queryBytes(uint64(seconds), 4)
		}
		b = append(b, queryBytes(frac, fractionBytes)...)
	default:
		return bad()
	}
	// The existing decoder enforces temporal/decimal edge cases; round-trip rejects
	// normalization, trailing input, negative zero and noncanonical spellings.
	decoded, err := decodeKeyValue(c, b)
	if err != nil || decoded != text {
		return bad()
	}
	return b, nil
}
