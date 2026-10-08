package innodb

import "fmt"

// count and dictionary are validated by Schema.validate.
func enumWidth(count int) int {
	if count <= 255 {
		return 1
	}
	return 2
}

func decodeEnum(b []byte, dictionary []string) (string, uint16, error) {
	if len(b) != enumWidth(len(dictionary)) {
		return "", 0, fmt.Errorf("%w: ENUM byte length", ErrCorrupt)
	}
	var index uint16
	for _, v := range b {
		index = index<<8 | uint16(v)
	}
	if int(index) > len(dictionary) {
		return "", 0, fmt.Errorf("%w: ENUM index %d exceeds dictionary", ErrCorrupt, index)
	}
	if index == 0 {
		return "", 0, nil
	}
	return dictionary[index-1], index, nil
}
