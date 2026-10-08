package innodb

import (
	"fmt"
	"time"
)

// fsp is validated by Schema.validate.
func timestampWidth(fsp int) int { return 4 + (fsp+1)/2 }

// MySQL 8.0.45 my_time.cc: my_timestamp_from_binary. Normal timestamps are
// formatted in UTC; MySQL's all-zero sentinel is not the Unix epoch.
func decodeTimestamp(b []byte, fsp int) (string, error) {
	if len(b) != timestampWidth(fsp) {
		return "", fmt.Errorf("%w: TIMESTAMP byte length", ErrCorrupt)
	}
	seconds := be.Uint32(b)
	micro := uint32(0)
	for _, v := range b[4:] {
		micro = micro<<8 | uint32(v)
	}
	switch len(b) - 4 {
	case 1:
		micro *= 10000
	case 2:
		micro *= 100
	}
	unit := uint32(1)
	for i := fsp; i < 6; i++ {
		unit *= 10
	}
	if seconds > 2147483647 || micro > 999999 || micro%unit != 0 || seconds == 0 && micro != 0 {
		return "", fmt.Errorf("%w: TIMESTAMP seconds/fraction range or alignment", ErrCorrupt)
	}
	value := "0000-00-00 00:00:00"
	if seconds != 0 {
		value = time.Unix(int64(seconds), 0).UTC().Format("2006-01-02 15:04:05")
	}
	if fsp != 0 {
		value += fmt.Sprintf(".%0*d", fsp, micro/unit)
	}
	return value, nil
}
