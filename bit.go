package innodb

import "fmt"

// bitLength is validated by Schema.validate.
func bitWidth(bitLength int) int { return (bitLength + 7) / 8 }

// InnoDB stores BIT as fixed binary, without an integer sign-bit flip.
func decodeBit(b []byte, bitLength int) (uint64, error) {
	if len(b) != bitWidth(bitLength) {
		return 0, fmt.Errorf("%w: BIT byte length", ErrCorrupt)
	}
	var value uint64
	for _, v := range b {
		value = value<<8 | uint64(v)
	}
	if bitLength < 64 && value>>bitLength != 0 {
		return 0, fmt.Errorf("%w: BIT unused high bits", ErrCorrupt)
	}
	return value, nil
}
