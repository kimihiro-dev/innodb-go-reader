package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func setFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/sets", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/sets", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}
func TestSetFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/sets/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Environment struct {
			SQLMode string `json:"sql_mode"`
		}
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err = json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Cases) != 4 || m.Environment.SQLMode != "STRICT_TRANS_TABLES" {
		t.Fatal("contract")
	}
	total := 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := setFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/sets", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err = d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			var masks [][]*uint64
			if err = json.Unmarshal(unzip(t, filepath.Join("testdata/sets", c.Name+".masks.json.gz")), &masks); err != nil {
				t.Fatal(err)
			}
			var ordinals [][]*uint16
			if err = json.Unmarshal(unzip(t, filepath.Join("testdata/sets", c.Name+".ordinals.json.gz")), &ordinals); err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows || len(want) != c.Rows || len(masks) != c.Rows || len(ordinals) != c.Rows {
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
					t.Fatalf("SQL row %d: %v != %v", i, got, want[i])
				}
				k, count, e, ec := 0, 0, 0, 0
				for col, def := range s.Columns {
					if def.Type == "ENUM" {
						v, ok := rec.EnumIndexes[col]
						w := ordinals[i][e]
						e++
						if w == nil {
							if ok {
								t.Fatal("NULL enum")
							}
						} else {
							ec++
							if !ok || v != *w {
								t.Fatal("enum ordinal")
							}
						}
					}
					if def.Type != "SET" {
						continue
					}
					v, ok := rec.SetMasks[col]
					w := masks[i][k]
					k++
					if w == nil {
						if ok || rec.Values[col] != nil {
							t.Fatal("NULL mask")
						}
						continue
					}
					count++
					if !ok || v != *w {
						t.Fatal("SQL mask", i, col, v, w)
					}
				}
				if len(rec.SetMasks) != count || k != len(masks[i]) || len(rec.EnumIndexes) != ec || e != len(ordinals[i]) {
					t.Fatal("metadata keys")
				}
			}
			if c.Name == "set_widths" {
				if c.Rows != 69 || len(s.Columns) != 65 {
					t.Fatal("width coverage")
				}
				width := 17
				for n := 1; n <= 64; n++ {
					if len(s.Columns[n].SetValues) != n {
						t.Fatal("dictionary")
					}
					width += setWidth(n)
					max := ^uint64(0) >> (64 - n)
					if r.Records[2].SetMasks[n] != max {
						t.Fatal("all bits", n)
					}
					for k := 0; k < 64; k++ {
						if r.Records[k+5].SetMasks[n] != (uint64(1)<<k)&max {
							t.Fatal("walking bit", n, k)
						}
					}
				}
				for i, rec := range r.Records {
					n := width
					if i == 0 {
						n = 17
					}
					if rec.End-rec.Offset != n {
						t.Fatal("fixed width", i)
					}
				}
			}
			if c.Name == "set_labels" {
				if r.Records[1].Values[1] != "" || r.Records[2].Values[1] != "" || r.Records[1].SetMasks[1] != 0 || r.Records[2].SetMasks[1] != 1 {
					t.Fatal("empty member versus empty set")
				}
				if r.Records[8].Values[1] != "a,b" || r.Records[8].Values[2] != "a,,b" || r.Records[8].Values[3] != "a,b," {
					t.Fatal("empty member separators")
				}
			}
			if c.Name == "set_mixed" && len(r.Records[1].External) != 1 {
				t.Fatal("LOB")
			}
			if c.Name == "set_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("tree")
			}
			total += c.Rows
		})
	}
	if total != 685 {
		t.Fatal("total", total)
	}
}
func TestSetContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for n := 1; n <= 64; n++ {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = Column{Name: "name", Type: "SET", SetValues: make([]string, n), Nullable: true}
		if _, err := s.validate(); err != nil {
			t.Fatal(err)
		}
		for _, length := range []int{0, setWidth(n) - 1, setWidth(n) + 1} {
			if _, _, err := decodeSet(make([]byte, length), s.Columns[2].SetValues); !errors.Is(err, ErrCorrupt) {
				t.Fatal("length")
			}
		}
	}
	good := Column{Name: "name", Type: "SET", SetValues: []string{"", "你好", "2"}, Nullable: true}
	for _, change := range []func(*Column){func(c *Column) { c.SetValues = nil }, func(c *Column) { c.SetValues = make([]string, 65) }, func(c *Column) { c.SetValues = []string{string([]byte{0xff})} }, func(c *Column) { c.SetValues = []string{"a,b"} }, func(c *Column) { c.Unsigned = true }, func(c *Column) { c.MaxChars = 1 }, func(c *Column) { c.MaxBytes = 1 }, func(c *Column) { c.FSP = 1 }, func(c *Column) { c.BitLength = 1 }, func(c *Column) { c.Precision = 1 }, func(c *Column) { c.Scale = 1 }, func(c *Column) { c.EnumValues = []string{"a"} }, func(c *Column) { c.Type = "INT" }} {
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
	s.Columns[0] = Column{Name: "id", Type: "SET", SetValues: []string{"a"}}
	if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatal("SET key")
	}
	dict := make([]string, 64)
	dict[0] = "first"
	dict[63] = "last"
	b := []byte{0x80, 0, 0, 0, 0, 0, 0, 1}
	old := append([]byte(nil), b...)
	v, mask, err := decodeSet(b, dict)
	if err != nil || v != "first,last" || mask != uint64(1)<<63|1 || !bytes.Equal(b, old) {
		t.Fatal("golden", err)
	}
	b, s = fixture(t, "lesson_rows")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range r.Records {
		if rec.SetMasks != nil {
			t.Fatal("old metadata")
		}
	}
}
func TestSetDamage(t *testing.T) {
	b, s := setFixture(t, "set_widths")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[2]
	base := int(rec.PageNumber) * PageSize
	off := base + rec.Offset + 17
	for n := 1; n <= 64; n++ {
		width := setWidth(n)
		if width*8 > n {
			bad := append([]byte(nil), b...)
			bad[off+width-1-n/8] |= 1 << uint(n%8)
			resealTestPages(bad)
			got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("unused bit", n, err)
			}
		}
		off += width
	}
	bad := append([]byte(nil), b...)
	be.PutUint16(bad[base+40:], uint16(rec.End-1))
	resealTestPages(bad)
	got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
	if got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("truncation", err)
	}
}
func FuzzSet(f *testing.F) {
	b, s := setFixture(f, "set_mixed")
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
