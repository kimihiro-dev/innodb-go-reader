package innodb

import "bytes"

type indexKey [][]byte

// Compare physical encodings in index order. PAD SPACE compares missing bytes
// as 0x20; stripping spaces would incorrectly order a prefix before a NUL suffix.
func compareIndexKey(a, b indexKey, s Schema, pk []int) int {
	if s.hiddenRowID() {
		return bytes.Compare(a[0], b[0])
	}
	for i, column := range pk {
		c := s.Columns[column]
		cmp := bytes.Compare(a[i], b[i])
		if c.Type == "CHAR" || c.Type == "VARCHAR" {
			cmp = comparePadded(a[i], b[i])
		}
		if c.Descending {
			cmp = -cmp
		}
		if cmp != 0 {
			return cmp
		}
	}
	return 0
}
func comparePadded(a, b []byte) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := byte(0x20), byte(0x20)
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
func (c Column) keyWidth() int {
	if c.Type == "CHAR" || c.Type == "VARCHAR" {
		if c.Collation != c.charsetName()+"_bin" {
			return 0
		}
		return int(c.variableMaxBytes())
	}
	if c.Collation != "" {
		return 0
	}
	if c.Type == "VARBINARY" {
		return c.MaxBytes
	}
	if integerWidth(c.Type) != 0 || dateWidth(c.Type) != 0 {
		return c.fixedWidth()
	}
	switch c.Type {
	case "BINARY", "DECIMAL", "BIT", "DATETIME", "TIME", "TIMESTAMP":
		return c.fixedWidth()
	}
	return 0
}
func (c Column) fixedWidth() int {
	if n := integerWidth(c.Type); n != 0 {
		return n
	}
	if n := floatWidth(c.Type); n != 0 {
		return n
	}
	if n := dateWidth(c.Type); n != 0 {
		return n
	}
	switch c.Type {
	case "BINARY":
		return c.MaxBytes
	case "CHAR":
		if !c.isVariable() {
			return c.MaxChars
		}
	case "DECIMAL":
		return decimalWidth(c.Precision, c.Scale)
	case "BIT":
		return bitWidth(c.BitLength)
	case "DATETIME":
		return datetimeWidth(c.FSP)
	case "TIME":
		return timeWidth(c.FSP)
	case "TIMESTAMP":
		return timestampWidth(c.FSP)
	case "ENUM":
		return enumWidth(len(c.EnumValues))
	case "SET":
		return setWidth(len(c.SetValues))
	}
	return 0
}
func decodeKeyValue(c Column, b []byte) (any, error) {
	if integerWidth(c.Type) != 0 {
		return decodeInteger(b, c.Unsigned), nil
	}
	if dateWidth(c.Type) != 0 {
		return decodeDate(b, c.Type)
	}
	switch c.Type {
	case "DECIMAL":
		return decodeDecimal(b, c)
	case "BIT":
		return decodeBit(b, c.BitLength)
	case "DATETIME":
		return decodeDatetime(b, c.FSP)
	case "TIME":
		return decodeTime(b, c.FSP)
	case "TIMESTAMP":
		return decodeTimestamp(b, c.FSP)
	}
	return variableValue(c, b)
}
func exposedKey(values []any, columns []int) any {
	if len(columns) == 1 {
		return values[columns[0]]
	}
	key := make([]any, len(columns))
	for i, column := range columns {
		key[i] = values[column]
	}
	return key
}

// ROW_ID is an unsigned six-byte system field, not a SQL integer column.
func rowIDValue(b []byte) uint64 {
	var value uint64
	for _, x := range b {
		value = value<<8 | uint64(x)
	}
	return value
}
