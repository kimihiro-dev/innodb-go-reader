package innodb

import (
	"encoding/binary"
	"fmt"
	"math"
)

func floatWidth(kind string) int {
	switch kind {
	case "FLOAT":
		return 4
	case "DOUBLE":
		return 8
	}
	return 0
}

// MySQL 8.0.45 mach0data.ic: mach_float_read / mach_double_read.
// Floats are little-endian IEEE 754; the integer sign transform does not apply.
// Column metadata is validated before this function is called.
func decodeFloat(b []byte, c Column) (any, error) {
	if len(b) != floatWidth(c.Type) {
		return nil, fmt.Errorf("%w: floating-point byte length", ErrCorrupt)
	}
	var value float64
	if c.Type == "FLOAT" {
		value = float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	} else {
		value = math.Float64frombits(binary.LittleEndian.Uint64(b))
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, fmt.Errorf("%w: nonfinite floating-point value", ErrCorrupt)
	}
	if c.Unsigned && value < 0 {
		return nil, fmt.Errorf("%w: negative UNSIGNED floating-point value", ErrCorrupt)
	}
	if c.Type == "FLOAT" {
		return float32(value), nil
	}
	return value, nil
}
