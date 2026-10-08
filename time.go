package innodb

import "fmt"

// fsp is validated by Schema.validate.
func timeWidth(fsp int) int { return 3 + (fsp+1)/2 }

// MySQL 8.0.45 my_time.cc: my_time_packed_from_binary. For negative
// fractional values the integer part is one lower and the fraction complemented.
func decodeTime(b []byte, fsp int) (string, error) {
	if len(b) != timeWidth(fsp) {
		return "", fmt.Errorf("%w: TIME byte length", ErrCorrupt)
	}
	hms := int64(uint32(b[0])<<16|uint32(b[1])<<8|uint32(b[2])) - 0x800000
	fraction := int64(0)
	for _, v := range b[3:] {
		fraction = fraction<<8 | int64(v)
	}
	if hms < 0 && fraction != 0 {
		hms++
		fraction -= int64(1) << uint((len(b)-3)*8)
	}
	negative := hms < 0 || fraction < 0
	if hms < 0 {
		hms = -hms
	}
	if fraction < 0 {
		fraction = -fraction
	}
	switch len(b) - 3 {
	case 1:
		fraction *= 10000
	case 2:
		fraction *= 100
	}
	hour, minute, second := hms>>12, (hms>>6)&63, hms&63
	unit := int64(1)
	for i := fsp; i < 6; i++ {
		unit *= 10
	}
	if hour > 838 || minute > 59 || second > 59 || fraction > 999999 || fraction%unit != 0 || hour == 838 && minute == 59 && second == 59 && fraction != 0 {
		return "", fmt.Errorf("%w: TIME component/fraction range or alignment", ErrCorrupt)
	}
	value := fmt.Sprintf("%02d:%02d:%02d", hour, minute, second)
	if fsp != 0 {
		value += fmt.Sprintf(".%0*d", fsp, fraction/unit)
	}
	if negative {
		value = "-" + value
	}
	return value, nil
}
