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
	"strings"
	"testing"
)

func decimalFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/decimal", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/decimal", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestDecimalFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/decimal/manifest.json")
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
	if len(m.Cases) != 19 {
		t.Fatal("fixture count")
	}
	pairs := map[[2]int]bool{}
	total := 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := decimalFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows {
				t.Fatal("row count")
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/decimal", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err = d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			if len(want) != c.Rows {
				t.Fatal("SQL row count")
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
					t.Fatalf("SQL mismatch row %d", i)
				}
				for k, col := range s.Columns {
					if col.Type == "DECIMAL" && rec.Values[k] != nil {
						v, ok := rec.Values[k].(string)
						if !ok {
							t.Fatal("decimal is not a string")
						}
						if col.Scale > 0 && (len(v) < col.Scale+2 || v[len(v)-col.Scale-1] != '.') {
							t.Fatal("scale lost")
						}
					}
				}
			}
			if strings.HasPrefix(c.Name, "decimal_matrix_") {
				for _, col := range s.Columns {
					if col.Type == "DECIMAL" {
						pairs[[2]int{col.Precision, col.Scale}] = true
					}
				}
			}
			if c.Name == "decimal_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("missing tree coverage")
			}
			if c.Name == "decimal_lesson" && len(r.Records[1].External) != 1 {
				t.Fatal("missing mixed LOB")
			}
			total += len(r.Records)
		})
	}
	for p := 1; p <= 65; p++ {
		for s := 0; s <= p && s <= 30; s++ {
			if !pairs[[2]int{p, s}] {
				t.Fatalf("missing (%d,%d)", p, s)
			}
		}
	}
	if len(pairs) != 1580 || total != 704 {
		t.Fatal("coverage count", len(pairs), total)
	}
}

func TestDecimalContracts(t *testing.T) {
	_, s := fixture(t, "lesson_rows")
	good := Column{Name: "name", Type: "DECIMAL", Precision: 65, Scale: 30, Nullable: true}
	s.Columns[2] = good
	if _, err := s.validate(); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(*Column){
		func(c *Column) { c.Precision = 0 }, func(c *Column) { c.Precision = 66 }, func(c *Column) { c.Precision = -1 },
		func(c *Column) { c.Scale = -1 }, func(c *Column) { c.Scale = 31 }, func(c *Column) { c.Precision = 2; c.Scale = 3 },
		func(c *Column) { c.MaxChars = 1 }, func(c *Column) { c.MaxBytes = 1 },
		func(c *Column) { c.Type = "INT" }, func(c *Column) { c.Type = "VARCHAR"; c.MaxChars = 20 },
	} {
		c := good
		alter(&c)
		s.Columns[2] = c
		if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal("bad schema", c, err)
		}
	}
	s.Columns[2] = good
	s.PrimaryKey = "name"
	s.Columns[2].Nullable = false
	if _, err := s.validate(); err != nil {
		t.Fatal("decimal PK accepted")
	}
	for _, tc := range []struct {
		p, s          int
		raw, want     string
		unsigned, bad bool
	}{
		{14, 4, "810dfb38d204d2", "1234567890.1234", false, false},
		{14, 4, "7ef204c72dfb2d", "-1234567890.1234", false, false},
		{2, 2, "81", "0.01", false, false}, {2, 2, "7f", "0.00", false, false},
		{3, 2, "8000", "0.00", false, false}, {3, 2, "7fff", "0.00", true, false},
		{2, 2, "7e", "", true, true}, {1, 0, "8a", "", false, true},
		{9, 0, "bb9aca00", "", false, true}, {2, 2, "e4", "", false, true},
		{14, 4, "810dfb38d22710", "", false, true},
		{14, 4, "810dfb38d2", "", false, true}, {1, 0, "8000", "", false, true},
	} {
		t.Run(fmt.Sprintf("%d_%d_%s", tc.p, tc.s, tc.raw), func(t *testing.T) {
			b, _ := hex.DecodeString(tc.raw)
			original := append([]byte(nil), b...)
			v, err := decodeDecimal(b, Column{Type: "DECIMAL", Precision: tc.p, Scale: tc.s, Unsigned: tc.unsigned})
			if tc.bad {
				if !errors.Is(err, ErrCorrupt) {
					t.Fatal("accepted invalid decimal", v, err)
				}
			} else if err != nil || v != tc.want {
				t.Fatal(v, err)
			}
			if !bytes.Equal(b, original) {
				t.Fatal("mutated input")
			}
		})
	}
}

func TestDecimalRecordDamage(t *testing.T) {
	b, s := decimalFixture(t, "decimal_lesson")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	off := int(r.Records[0].PageNumber)*PageSize + r.Records[0].Offset + 4 + 13
	for _, change := range []struct {
		name   string
		mutate func([]byte, *Schema)
	}{
		{"partial-integer-overflow", func(b []byte, s *Schema) { b[off] = 0x8a }},
		{"full-group-overflow", func(b []byte, s *Schema) { be.PutUint32(b[off+1:], 1000000000) }},
		{"fraction-overflow", func(b []byte, s *Schema) { be.PutUint16(b[off+5:], 10000) }},
		{"negative-unsigned", func(b []byte, s *Schema) { s.Columns[0].Unsigned = true }},
		{"heap-truncation", func(b []byte, s *Schema) {
			page := int(r.Records[0].PageNumber) * PageSize
			be.PutUint16(b[page+40:], uint16(r.Records[0].Offset+18))
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			bad := append([]byte(nil), b...)
			sc := s
			sc.Columns = append([]Column(nil), s.Columns...)
			change.mutate(bad, &sc)
			resealTestPages(bad)
			got, err := Read(bytes.NewReader(bad), int64(len(bad)), sc)
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("accepted damaged decimal", err)
			}
		})
	}
}

func FuzzDecimal(f *testing.F) {
	b, s := decimalFixture(f, "decimal_lesson")
	f.Add(uint32(4*PageSize+160), []byte{0xff})
	f.Fuzz(func(t *testing.T, offset uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(offset)%len(bad):], change)
		resealTestPages(bad)
		got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if err != nil && got != nil {
			t.Fatal("partial result")
		}
	})
}
