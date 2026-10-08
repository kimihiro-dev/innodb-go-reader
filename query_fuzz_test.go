package innodb

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"
)

func FuzzQueryRanges(f *testing.F) {
	b := scanFixture(f, "testdata/trees/ordered_rows.ibd.gz")
	s := loadScanSchema(f, "testdata/trees/ordered_rows.json")
	full, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(int32(-100), int32(100), uint8(3), uint16(0))
	f.Add(int32(0), int32(1000), uint8(4), uint16(7))
	f.Fuzz(func(t *testing.T, low, high int32, flags uint8, limit uint16) {
		lc, uc, rev := flags&1 != 0, flags&2 != 0, flags&4 != 0
		q := KeyRange{Lower: &KeyBound{Key{low}, lc}, Upper: &KeyBound{Key{high}, uc}, Reverse: rev, Limit: uint64(limit)}
		expected := []Record{}
		for _, row := range full.Records {
			v := row.Values[0].(int32)
			if (v > low || lc && v == low) && (v < high || uc && v == high) {
				expected = append(expected, row)
			}
		}
		if rev {
			slices.Reverse(expected)
		}
		reached := limit != 0 && len(expected) >= int(limit)
		if reached {
			expected = expected[:int(limit)]
		}
		actual, p, err := queryRecords(context.Background(), bytes.NewReader(b), int64(len(b)), s, q, ScanOptions{})
		if err != nil || !p.Complete || p.LimitReached != reached || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("bounds %d %d flags %d limit %d: %v", low, high, flags, limit, err)
		}
	})
}

func FuzzQueryKeys(f *testing.F) {
	columns := []Column{{Type: "DECIMAL", Precision: 20, Scale: 6}, {Type: "DATE"}, {Type: "DATETIME", FSP: 6}, {Type: "TIME", FSP: 6}, {Type: "TIMESTAMP", FSP: 6}}
	f.Add(uint8(0), "-1234567890.123456")
	f.Add(uint8(2), "2020-01-01 00:00:00.000000")
	f.Add(uint8(3), "-00:00:00.000001")
	f.Fuzz(func(t *testing.T, kind uint8, text string) {
		if len(text) > 100 {
			return
		}
		c := columns[int(kind)%len(columns)]
		b, err := encodeQueryValue(text, c)
		if err != nil {
			return
		}
		decoded, err := decodeKeyValue(c, b)
		if err != nil || decoded != text {
			t.Fatal("accepted noncanonical key", text, decoded, err)
		}
	})
}
