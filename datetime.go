package innodb

import "fmt"

// fsp has been validated by Schema.validate.
func datetimeWidth(fsp int) int { return 5 + (fsp+1)/2 }

// MySQL 8.0.45 my_time.cc: my_datetime_packed_from_binary and
// TIME_from_longlong_datetime_packed. DATETIME2 is DATA_FIXBINARY in InnoDB.
func decodeDatetime(b []byte, fsp int) (string, error) {
	if len(b) != datetimeWidth(fsp) {
		return "", fmt.Errorf("%w: DATETIME byte length", ErrCorrupt)
	}
	var packed uint64
	for _, v := range b[:5] {
		packed = packed<<8 | uint64(v)
	}
	if packed < 0x8000000000 {
		return "", fmt.Errorf("%w: negative DATETIME encoding", ErrCorrupt)
	}
	packed -= 0x8000000000
	ym := packed >> 22
	year, month, day := ym/13, ym%13, (packed>>17)&31
	hour, minute, second := (packed>>12)&31, (packed>>6)&63, packed&63
	if year > 9999 || hour > 23 || minute > 59 || second > 59 {
		return "", fmt.Errorf("%w: DATETIME component range", ErrCorrupt)
	}
	micro := uint32(0)
	for _, v := range b[5:] {
		micro = micro<<8 | uint32(v)
	}
	switch len(b) - 5 {
	case 1:
		micro *= 10000
	case 2:
		micro *= 100
	}
	unit := uint32(1)
	for i := fsp; i < 6; i++ {
		unit *= 10
	}
	if micro > 999999 || micro%unit != 0 {
		return "", fmt.Errorf("%w: DATETIME fractional seconds range/alignment", ErrCorrupt)
	}
	value := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", year, month, day, hour, minute, second)
	if fsp != 0 {
		value += fmt.Sprintf(".%0*d", fsp, micro/unit)
	}
	return value, nil
}
