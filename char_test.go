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
	"strings"
	"testing"
)

func charFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/chars", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/chars", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}
func TestCharFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/chars/manifest.json")
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
	if len(m.Cases) != 35 {
		t.Fatal("cases")
	}
	seen := map[int]bool{}
	total := 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := charFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/chars", c.Name+".expected.json.gz"))))
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
				count := 0
				for k, col := range s.Columns {
					if col.Type != "CHAR" {
						continue
					}
					seen[col.MaxChars] = true
					stored, ok := rec.CharStorage[k]
					if want[i][k] == nil {
						if ok {
							t.Fatal("NULL storage")
						}
						continue
					}
					count++
					v := want[i][k].(string)
					// Independent SQL display plus the documented minimum byte reserve.
					if len(v) < col.MaxChars {
						v += strings.Repeat(" ", col.MaxChars-len(v))
					}
					if !ok || stored != v {
						t.Fatal("physical padding", i, k, len(stored), len(v))
					}
				}
				if count != len(rec.CharStorage) {
					t.Fatal("map keys")
				}
			}
			if c.Name == "char_mixed" {
				if len(r.Records[1].External) != 1 {
					t.Fatal("LOB")
				}
				if r.Records[0].Values[10] != " a  " {
					t.Fatal("VARCHAR trailing spaces")
				}
			}
			if c.Name == "char_external" {
				if len(r.Records[0].External) == 0 {
					t.Fatal("external CHAR coverage")
				}
				for _, e := range r.Records[0].External {
					if s.Columns[e.Column].Type != "CHAR" {
						t.Fatal("external type")
					}
				}
			}
			if c.Name == "char_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("tree")
			}
			total += c.Rows
		})
	}
	if total != 926 || len(seen) != 255 {
		t.Fatal("coverage", total, len(seen))
	}
}
func TestCharContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	good := Column{Name: "name", Type: "CHAR", MaxChars: 5, Nullable: true}
	for n := 0; n <= 255; n++ {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = good
		s.Columns[2].MaxChars = n
		if _, err := s.validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*Column){func(c *Column) { c.MaxChars = -1 }, func(c *Column) { c.MaxChars = 256 }, func(c *Column) { c.MaxBytes = 1 }, func(c *Column) { c.Unsigned = true }, func(c *Column) { c.FSP = 1 }, func(c *Column) { c.Precision = 1 }, func(c *Column) { c.Scale = 1 }, func(c *Column) { c.BitLength = 1 }, func(c *Column) { c.EnumValues = []string{"a"} }, func(c *Column) { c.SetValues = []string{"a"} }} {
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
	s.Columns[0] = Column{Name: "id", Type: "CHAR", MaxChars: 5}
	if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatal("CHAR key")
	}
	for _, v := range [][]byte{[]byte("abcd"), []byte("界界界界界界"), []byte("界界 "), {0xff, 0x20, 0x20, 0x20, 0x20}} {
		if _, err := variableValue(good, v); !errors.Is(err, ErrCorrupt) {
			t.Fatal("bad CHAR", v, err)
		}
	}
	for _, v := range []string{"a    ", "界  ", "界界", "\u00a0\u00a0\u00a0"} {
		got, err := variableValue(good, []byte(v))
		if err != nil || got != v {
			t.Fatal("valid stored", v, err)
		}
	}
	b, s := fixture(t, "lesson_rows")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range r.Records {
		if rec.CharStorage != nil {
			t.Fatal("old metadata")
		}
	}
}
func TestCharDamage(t *testing.T) {
	b, s := charFixture(t, "char_mixed")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[1]
	base := int(rec.PageNumber) * PageSize
	off := base + rec.Offset + 17
	// c0 is CHAR(1), with a three-byte Chinese character. Two ASCII spaces
	// after a one-byte letter are noncanonical and exceed its character limit.
	for _, v := range [][]byte{{0xff, 0xff, 0xff}, []byte("a  ")} {
		bad := append([]byte(nil), b...)
		copy(bad[off:], v)
		resealTestPages(bad)
		got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if got != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("bad physical CHAR", err)
		}
	}
	bad := append([]byte(nil), b...)
	be.PutUint16(bad[base+40:], uint16(rec.End-1))
	resealTestPages(bad)
	got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
	if got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("truncated", err)
	}
}
func FuzzChar(f *testing.F) {
	b, s := charFixture(f, "char_external")
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
