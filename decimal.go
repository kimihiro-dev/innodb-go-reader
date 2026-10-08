package innodb

import (
	"fmt"
	"strconv"
	"strings"
)

// MySQL 8.0.45 strings/decimal.cc: decimal_bin_size and bin2decimal.
var decimalGroupBytes = [...]int{0, 1, 1, 2, 2, 3, 3, 4, 4, 4}

// precision/scale have been checked by Schema.validate.
func decimalWidth(precision, scale int) int {
	integer := precision - scale
	return integer/9*4 + decimalGroupBytes[integer%9] + scale/9*4 + decimalGroupBytes[scale%9]
}

// decodeDecimal returns exact decimal notation retaining the declared scale.
// No floating-point arithmetic or caller-owned byte mutation is used.
func decodeDecimal(b []byte, c Column) (string, error) {
	if len(b) != decimalWidth(c.Precision, c.Scale) {
		return "", fmt.Errorf("%w: DECIMAL byte length", ErrCorrupt)
	}
	negative := b[0]&0x80 == 0
	mask := byte(0)
	if negative {
		mask = 0xff
	}
	pos := 0
	nonzero := false
	group := func(digits int) (string, error) {
		var value uint32
		for j := 0; j < decimalGroupBytes[digits]; j++ {
			v := b[pos] ^ mask
			if pos == 0 {
				v ^= 0x80
			}
			value = value<<8 | uint32(v)
			pos++
		}
		bound := uint32(1)
		for j := 0; j < digits; j++ {
			bound *= 10
		}
		if value >= bound {
			return "", fmt.Errorf("%w: DECIMAL group exceeds %d digits", ErrCorrupt, digits)
		}
		nonzero = nonzero || value != 0
		text := strconv.FormatUint(uint64(value), 10)
		return strings.Repeat("0", digits-len(text)) + text, nil
	}
	var integer, fraction strings.Builder
	intDigits := c.Precision - c.Scale
	if rem := intDigits % 9; rem != 0 {
		v, err := group(rem)
		if err != nil {
			return "", err
		}
		integer.WriteString(v)
	}
	for i := 0; i < intDigits/9; i++ {
		v, err := group(9)
		if err != nil {
			return "", err
		}
		integer.WriteString(v)
	}
	for i := 0; i < c.Scale/9; i++ {
		v, err := group(9)
		if err != nil {
			return "", err
		}
		fraction.WriteString(v)
	}
	if rem := c.Scale % 9; rem != 0 {
		v, err := group(rem)
		if err != nil {
			return "", err
		}
		fraction.WriteString(v)
	}
	if negative && nonzero && c.Unsigned {
		return "", fmt.Errorf("%w: negative UNSIGNED DECIMAL", ErrCorrupt)
	}
	text := strings.TrimLeft(integer.String(), "0")
	if text == "" {
		text = "0"
	}
	if c.Scale != 0 {
		text += "." + fraction.String()
	}
	if negative && nonzero {
		text = "-" + text
	}
	return text, nil
}
