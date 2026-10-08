package innodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestQueryKeyInvalidValues(t *testing.T) {
	cases := []struct {
		column Column
		bad    []any
	}{
		{Column{Type: "TINYINT"}, []any{128, -129, 1.0, nil, "1", json.Number("1e0")}},
		{Column{Type: "INT", Unsigned: true}, []any{-1, uint64(1) << 32}},
		{Column{Type: "YEAR"}, []any{1900, 2156, -1, "2020"}},
		{Column{Type: "BIT", BitLength: 3}, []any{8, -1, "7"}},
		{Column{Type: "BINARY", MaxBytes: 2}, []any{[]byte{1}, []byte{1, 2, 3}, "AA=="}},
		{Column{Type: "VARBINARY", MaxBytes: 2}, []any{[]byte{1, 2, 3}, "abc"}},
		{Column{Type: "VARCHAR", MaxChars: 2}, []any{"abc", string([]byte{0xff}), nil}},
		{Column{Type: "VARCHAR", MaxChars: 2, Charset: "ascii"}, []any{"€"}},
		{Column{Type: "VARCHAR", MaxChars: 2, Charset: "latin1"}, []any{"界"}},
		{Column{Type: "VARCHAR", MaxChars: 2, Charset: "utf8mb3"}, []any{"😀"}},
		{Column{Type: "DECIMAL", Precision: 4, Scale: 2}, []any{"1.2", "01.20", "100.00", "-0.00", "a.00", "1.2x", 1.2}},
		{Column{Type: "DECIMAL", Precision: 2, Scale: 2}, []any{"1.00", ".10"}},
		{Column{Type: "DECIMAL", Precision: 4}, []any{"1.0", "10000", "-0001"}},
		{Column{Type: "DECIMAL", Precision: 4, Scale: 2, Unsigned: true}, []any{"-1.00"}},
		{Column{Type: "DATE"}, []any{"2020-13-01", "2020-01-32", "2020-1-01", "-001-01-01", "2020-01-01x"}},
		{Column{Type: "DATETIME", FSP: 3}, []any{"2020-01-01 24:00:00.000", "2020-01-01 01:00:00.00", "2020-01-01 01:00:00.00x"}},
		{Column{Type: "TIME", FSP: 1}, []any{"839:00:00.0", "838:59:59.1", "-00:00:00.0", "01:60:00.0", "1:00:00.0"}},
		{Column{Type: "TIMESTAMP", FSP: 2}, []any{"0000-00-00 00:00:00.01", "1970-01-01 00:00:00.00", "2038-01-19 03:14:08.00", "2020-02-31 00:00:00.00"}},
	}
	for _, c := range cases {
		for _, v := range c.bad {
			if b, err := encodeQueryValue(v, c.column); err == nil {
				t.Fatalf("accepted %s %v -> %x", c.column.Type, v, b)
			}
		}
	}
}

func TestQueryKeyBoundaries(t *testing.T) {
	cases := []struct {
		column Column
		value  any
	}{
		{Column{Type: "BIGINT"}, int64(-9223372036854775808)},
		{Column{Type: "BIGINT", Unsigned: true}, uint64(18446744073709551615)},
		{Column{Type: "BIT", BitLength: 64}, uint64(18446744073709551615)},
		{Column{Type: "YEAR"}, uint16(0)}, {Column{Type: "YEAR"}, uint16(2155)},
		{Column{Type: "DECIMAL", Precision: 2, Scale: 2}, "0.99"},
		{Column{Type: "DECIMAL", Precision: 20, Scale: 10}, "-1234567890.1234567890"},
		{Column{Type: "DATE"}, "2020-02-31"},
		{Column{Type: "DATETIME", FSP: 6}, "0000-00-00 00:00:00.000000"},
		{Column{Type: "TIME", FSP: 5}, "-00:00:00.00001"},
		{Column{Type: "TIME", FSP: 6}, "-838:59:59.000000"},
		{Column{Type: "TIMESTAMP", FSP: 1}, "2038-01-19 03:14:07.9"},
		{Column{Type: "TIMESTAMP", FSP: 6}, "0000-00-00 00:00:00.000000"},
	}
	for _, c := range cases {
		b, err := encodeQueryValue(c.value, c.column)
		if err != nil {
			t.Fatal(c, err)
		}
		decoded, err := decodeKeyValue(c.column, b)
		if err != nil || decoded != c.value {
			t.Fatal(c, decoded, err)
		}
	}
	c := Column{Type: "VARCHAR", Charset: "latin1", MaxChars: 300}
	raw := make([]byte, 256)
	for i := range raw {
		raw[i] = byte(i)
	}
	text, err := decodeText("latin1", raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeQueryValue(text, c)
	if err != nil || !bytes.Equal(encoded, raw) {
		t.Fatal("latin1 reverse map", err)
	}
}

func TestQueryMutationAndRowID(t *testing.T) {
	b, s := compactFixture(t, "testdata/compact", "composite_compact_initial")
	r := bytes.NewReader(b)
	size := int64(len(b))
	full, err := Read(r, size, s)
	if err != nil {
		t.Fatal(err)
	}
	key := recordQueryKey(full.Records[0], s)
	q := PointKey(key)
	records := 0
	p, err := Query(context.Background(), r, size, s, q, ScanOptions{}, func(e ScanEvent) error {
		if e.Page != nil {
			key[0].([]byte)[0] ^= 0xff
			e.Page.Slots[0] = 0
		}
		if e.Node != nil {
			e.Node.ChildPage = 0
			for _, v := range e.Node.Key.([]any) {
				if b, ok := v.([]byte); ok {
					for i := range b {
						b[i] = 0
					}
				}
			}
		}
		if e.Record != nil {
			records++
			e.Record.Values[0] = nil
		}
		return nil
	})
	if err != nil || !p.Complete || records != 1 {
		t.Fatal("input/callback ownership", p, err)
	}
	b, s = compactFixture(t, "testdata/compact", "hidden_compact_initial")
	r = bytes.NewReader(b)
	size = int64(len(b))
	for _, key := range []Key{{-1}, {uint64(1) << 48}, {1.0}} {
		p, err := Query(context.Background(), r, size, s, PointKey(key), ScanOptions{}, func(ScanEvent) error { return nil })
		if p.Complete || !errors.Is(err, ErrUnsupported) {
			t.Fatal("ROW_ID range", p, err)
		}
	}
}
