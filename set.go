package innodb

import (
	"fmt"
	"strings"
)

// count and dictionary are validated by Schema.validate.
func setWidth(count int) int {
	if count > 32 {
		return 8
	}
	return (count + 7) / 8
}

func decodeSet(b []byte, dictionary []string) (string, uint64, error) {
	if len(b) != setWidth(len(dictionary)) {
		return "", 0, fmt.Errorf("%w: SET byte length", ErrCorrupt)
	}
	var mask uint64
	for _, v := range b {
		mask = mask<<8 | uint64(v)
	}
	if len(dictionary) < 64 && mask>>len(dictionary) != 0 {
		return "", 0, fmt.Errorf("%w: SET bits outside dictionary", ErrCorrupt)
	}
	var value strings.Builder
	for i, label := range dictionary {
		if mask&(uint64(1)<<i) == 0 {
			continue
		}
		// MySQL Field_set::val_str tests the output length, not the number of bits.
		if value.Len() != 0 {
			value.WriteByte(',')
		}
		value.WriteString(label)
	}
	return value.String(), mask, nil
}
