package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func bitFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/bits", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/bits", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestBitFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/bits/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err = json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Cases) != 3 {
		t.Fatal("cases")
	}
	total := 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := bitFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/bits", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err = d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows || len(want) != c.Rows {
				t.Fatal("rows")
			}
			for i, rec := range r.Records {
				raw, err := json.Marshal(rec.Values)
				if err != nil {
					t.Fatal(err)
				}
				var got []any
				d = json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if err = d.Decode(&got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want[i]) {
					t.Fatalf("SQL row %d", i)
				}
				for k, col := range s.Columns {
					if col.Type == "BIT" && rec.Values[k] != nil {
						if _, ok := rec.Values[k].(uint64); !ok {
							t.Fatal("uint64 contract")
						}
					}
				}
			}
			if c.Name == "bit_widths" {
				if len(s.Columns) != 65 || c.Rows != 69 {
					t.Fatal("width coverage")
				}
				for n := 1; n <= 64; n++ {
					if s.Columns[n].BitLength != n || r.Records[0].Values[n] != nil || r.Records[1].Values[n] != uint64(0) {
						t.Fatal("NULL/zero", n)
					}
					mask := ^uint64(0) >> (64 - n)
					if r.Records[2].Values[n] != mask {
						t.Fatal("max", n)
					}
					for k := 0; k < 64; k++ {
						if r.Records[k+5].Values[n] != (uint64(1)<<k)&mask {
							t.Fatal("walking bit", n, k)
						}
					}
				}
				for i, rec := range r.Records {
					length := 17
					if i != 0 {
						for n := 1; n <= 64; n++ {
							length += bitWidth(n)
						}
					}
					if rec.End-rec.Offset != length {
						t.Fatal("fixed width")
					}
				}
			}
			if c.Name == "bit_mixed" && len(r.Records[1].External) != 1 {
				t.Fatal("LOB")
			}
			if c.Name == "bit_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("tree")
			}
			total += c.Rows
		})
	}
	if total != 673 {
		t.Fatal("total", total)
	}
}

func TestBitContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for n := 1; n <= 64; n++ {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = Column{Name: "name", Type: "BIT", BitLength: n, Nullable: true}
		if _, err := s.validate(); err != nil {
			t.Fatal(err)
		}
		for _, length := range []int{0, bitWidth(n) - 1, bitWidth(n) + 1} {
			if _, err := decodeBit(make([]byte, length), n); !errors.Is(err, ErrCorrupt) {
				t.Fatal("length")
			}
		}
		if n%8 != 0 {
			b := make([]byte, bitWidth(n))
			b[0] = 1 << uint(n%8)
			if _, err := decodeBit(b, n); !errors.Is(err, ErrCorrupt) {
				t.Fatal("padding", n)
			}
		}
	}
	good := Column{Name: "name", Type: "BIT", BitLength: 9, Nullable: true}
	for _, change := range []func(*Column){func(c *Column) { c.BitLength = 0 }, func(c *Column) { c.BitLength = -1 }, func(c *Column) { c.BitLength = 65 }, func(c *Column) { c.Unsigned = true }, func(c *Column) { c.FSP = 1 }, func(c *Column) { c.Precision = 1 }, func(c *Column) { c.Scale = 1 }, func(c *Column) { c.MaxChars = 1 }, func(c *Column) { c.MaxBytes = 1 }, func(c *Column) { c.Type = "INT" }} {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = good
		change(&s.Columns[2])
		if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal("attributes")
		}
	}
	s := base
	s.Columns = append([]Column(nil), base.Columns...)
	s.Columns[0] = Column{Name: "id", Type: "BIT", BitLength: 64}
	if _, err := s.validate(); err != nil {
		t.Fatal("BIT primary key")
	}
	for _, tc := range []struct {
		raw  string
		n    int
		want uint64
	}{{"01", 1, 1}, {"0100", 9, 256}, {"0123456789abcdef", 64, 0x0123456789abcdef}, {"ffffffffffffffff", 64, ^uint64(0)}} {
		b, _ := hex.DecodeString(tc.raw)
		old := append([]byte(nil), b...)
		got, err := decodeBit(b, tc.n)
		if err != nil || got != tc.want || !bytes.Equal(b, old) {
			t.Fatal("golden", tc, err)
		}
	}
}

func TestBitDamage(t *testing.T) {
	b, s := bitFixture(t, "bit_widths")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[2]
	off := int(rec.PageNumber)*PageSize + rec.Offset + 17
	for n := 1; n <= 64; n++ {
		if n%8 != 0 {
			t.Run(fmt.Sprint(n), func(t *testing.T) {
				bad := append([]byte(nil), b...)
				bad[off] |= 1 << uint(n%8)
				resealTestPages(bad)
				got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
				if got != nil || !errors.Is(err, ErrCorrupt) {
					t.Fatal("high padding", err)
				}
			})
		}
		off += bitWidth(n)
	}
	bad := append([]byte(nil), b...)
	be.PutUint16(bad[int(rec.PageNumber)*PageSize+40:], uint16(rec.End-1))
	resealTestPages(bad)
	got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
	if got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("truncated heap", err)
	}
}

func FuzzBit(f *testing.F) {
	b, s := bitFixture(f, "bit_mixed")
	f.Add(uint32(4*PageSize+146), []byte{255})
	f.Fuzz(func(t *testing.T, off uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(off)%len(bad):], change)
		resealTestPages(bad)
		r, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if err != nil && r != nil {
			t.Fatal("partial result")
		}
	})
}
