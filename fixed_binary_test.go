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

func fixedBinaryFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/fixed_binary", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/fixed_binary", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestFixedBinaryFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/fixed_binary/manifest.json")
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
	if len(m.Cases) != 18 {
		t.Fatal("cases")
	}
	seen := map[int]bool{}
	total := 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := fixedBinaryFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/fixed_binary", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err = d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows || len(want) != c.Rows {
				t.Fatal("rows")
			}
			for i, rec := range r.Records {
				vals := append([]any(nil), rec.Values...)
				for k, col := range s.Columns {
					if col.isBinary() && vals[k] != nil {
						v, ok := vals[k].([]byte)
						if !ok {
							t.Fatal("binary type")
						}
						if col.Type == "BINARY" && len(v) != col.MaxBytes {
							t.Fatal("fixed length")
						}
						vals[k] = strings.ToUpper(hex.EncodeToString(v))
					}
				}
				raw, err := json.Marshal(vals)
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
			}
			if strings.HasPrefix(c.Name, "binary_widths_") {
				if c.Rows != 6 {
					t.Fatal("width rows")
				}
				width := 17
				for k, col := range s.Columns {
					if col.Type != "BINARY" {
						continue
					}
					seen[col.MaxBytes] = true
					width += col.MaxBytes
					if r.Records[0].Values[k] != nil {
						t.Fatal("NULL")
					}
					if !bytes.Equal(r.Records[1].Values[k].([]byte), make([]byte, col.MaxBytes)) {
						t.Fatal("empty padding")
					}
					want := make([]byte, col.MaxBytes)
					want[0] = 'a'
					if !bytes.Equal(r.Records[2].Values[k].([]byte), want) {
						t.Fatal("short padding")
					}
					if !bytes.Equal(r.Records[4].Values[k].([]byte), bytes.Repeat([]byte{' '}, col.MaxBytes)) {
						t.Fatal("trailing spaces")
					}
				}
				for i, rec := range r.Records {
					n := width
					if i == 0 {
						n = 17
					}
					if rec.End-rec.Offset != n {
						t.Fatal("record width")
					}
				}
			}
			if c.Name == "binary_mixed" {
				if len(r.Records[1].External) != 1 {
					t.Fatal("LOB")
				}
				v := r.Records[0].Values[10].([]byte)
				if v == nil || len(v) != 0 {
					t.Fatal("empty VARBINARY")
				}
				if r.Records[2].Values[10] != nil {
					t.Fatal("NULL VARBINARY")
				}
				original := append([]byte(nil), b...)
				v = r.Records[0].Values[0].([]byte)
				v[0] ^= 0xff
				if !bytes.Equal(b, original) {
					t.Fatal("output aliases input")
				}
			}
			if c.Name == "binary_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("tree")
			}
			total += c.Rows
		})
	}
	if total != 700 || len(seen) != 255 {
		t.Fatal("coverage", total, len(seen))
	}
}

func TestFixedBinaryContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for n := 1; n <= 255; n++ {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = Column{Name: "name", Type: "BINARY", MaxBytes: n, Nullable: true}
		if _, err := s.validate(); err != nil {
			t.Fatal(err)
		}
		if s.Columns[2].variableMaxBytes() != 0 {
			t.Fatal("consumes length array")
		}
	}
	good := Column{Name: "name", Type: "BINARY", MaxBytes: 3, Nullable: true}
	for _, change := range []func(*Column){func(c *Column) { c.MaxBytes = -1 }, func(c *Column) { c.MaxBytes = 256 }, func(c *Column) { c.Unsigned = true }, func(c *Column) { c.MaxChars = 1 }, func(c *Column) { c.FSP = 1 }, func(c *Column) { c.BitLength = 1 }, func(c *Column) { c.Precision = 1 }, func(c *Column) { c.Scale = 1 }} {
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
	s.Columns[0] = Column{Name: "id", Type: "BINARY", MaxBytes: 4}
	if _, err := s.validate(); err != nil {
		t.Fatal("BINARY key")
	}
}

func TestFixedBinaryDamage(t *testing.T) {
	b, s := fixedBinaryFixture(t, "binary_mixed")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[1]
	base := int(rec.PageNumber) * PageSize
	for _, end := range []int{rec.Offset + 17, rec.Offset + 18, rec.End - 1} {
		t.Run(fmt.Sprint(end), func(t *testing.T) {
			bad := append([]byte(nil), b...)
			be.PutUint16(bad[base+40:], uint16(end))
			resealTestPages(bad)
			got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("heap truncation", err)
			}
		})
	}
	// Every possible byte value is legal: changes inside a fixed binary value
	// must survive without text validation or trimming.
	off := base + rec.Offset + 17
	for _, v := range []byte{0, 0x20, 0x80, 0xff} {
		bad := append([]byte(nil), b...)
		bad[off] = v
		resealTestPages(bad)
		got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if err != nil || got.Records[1].Values[0].([]byte)[0] != v {
			t.Fatal("valid arbitrary byte", err)
		}
	}
}

func FuzzFixedBinary(f *testing.F) {
	b, s := fixedBinaryFixture(f, "binary_mixed")
	f.Add(uint32(4*PageSize+146), []byte{0xff})
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
