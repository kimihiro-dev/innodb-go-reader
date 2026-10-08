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

func enumFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/enums", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/enums", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestEnumFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/enums/manifest.json")
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
	if len(m.Cases) != 7 || m.Environment.SQLMode != "" {
		t.Fatal("contract")
	}
	total := 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := enumFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/enums", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err = d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			var ordinals [][]*uint16
			if err = json.Unmarshal(unzip(t, filepath.Join("testdata/enums", c.Name+".ordinals.json.gz")), &ordinals); err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows || len(want) != c.Rows || len(ordinals) != c.Rows {
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
					t.Fatalf("SQL label row %d", i)
				}
				k, count := 0, 0
				for col, def := range s.Columns {
					if def.Type != "ENUM" {
						continue
					}
					index, exists := rec.EnumIndexes[col]
					if k >= len(ordinals[i]) {
						t.Fatal("ordinal columns")
					}
					wantIndex := ordinals[i][k]
					k++
					if wantIndex == nil {
						if exists || rec.Values[col] != nil {
							t.Fatal("NULL ordinal")
						}
						continue
					}
					count++
					if !exists || index != *wantIndex {
						t.Fatal("SQL ordinal", i, col, index, wantIndex)
					}
					if _, ok := rec.Values[col].(string); !ok {
						t.Fatal("string contract")
					}
				}
				if len(rec.EnumIndexes) != count || k != len(ordinals[i]) {
					t.Fatal("unexpected ordinal entry")
				}
			}
			switch c.Name {
			case "enum_1", "enum_255", "enum_256", "enum_65535":
				for i, rec := range r.Records {
					length := 17
					if i != 0 {
						length += enumWidth(len(s.Columns[1].EnumValues))
					}
					if rec.End-rec.Offset != length {
						t.Fatal("width/NULL")
					}
				}
				if c.Name == "enum_255" || c.Name == "enum_256" {
					for index := 0; index <= len(s.Columns[1].EnumValues); index++ {
						if r.Records[index+1].EnumIndexes[1] != uint16(index) {
							t.Fatal("all ordinals")
						}
					}
				}
				if c.Name == "enum_65535" && r.Records[7].EnumIndexes[1] != 65535 {
					t.Fatal("max ordinal")
				}
			case "enum_labels":
				if r.Records[1].Values[1] != "" || r.Records[2].Values[1] != "" || r.Records[1].EnumIndexes[1] != 0 || r.Records[2].EnumIndexes[1] != 1 {
					t.Fatal("error zero versus empty member")
				}
				if r.Records[9].Values[1] != "2" || r.Records[9].EnumIndexes[1] != 4 {
					t.Fatal("numeric label")
				}
			case "enum_mixed":
				if len(r.Records[1].External) != 1 {
					t.Fatal("LOB")
				}
			case "enum_tree":
				if r.Page.Level != 1 || len(r.Pages) < 3 {
					t.Fatal("tree")
				}
			}
			total += c.Rows
		})
	}
	if total != 1140 {
		t.Fatal("total", total)
	}
}

func TestEnumContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for _, n := range []int{1, 255, 256, 65535} {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = Column{Name: "name", Type: "ENUM", EnumValues: make([]string, n), Nullable: true}
		if _, err := s.validate(); err != nil {
			t.Fatal(err)
		}
		for _, length := range []int{0, enumWidth(n) - 1, enumWidth(n) + 1} {
			if _, _, err := decodeEnum(make([]byte, length), s.Columns[2].EnumValues); !errors.Is(err, ErrCorrupt) {
				t.Fatal("length")
			}
		}
	}
	good := Column{Name: "name", Type: "ENUM", EnumValues: []string{"", "你好", "2"}, Nullable: true}
	for _, change := range []func(*Column){func(c *Column) { c.EnumValues = nil }, func(c *Column) { c.EnumValues = make([]string, 65536) }, func(c *Column) { c.EnumValues = []string{string([]byte{0xff})} }, func(c *Column) { c.Unsigned = true }, func(c *Column) { c.MaxChars = 1 }, func(c *Column) { c.MaxBytes = 1 }, func(c *Column) { c.FSP = 1 }, func(c *Column) { c.BitLength = 1 }, func(c *Column) { c.Precision = 1 }, func(c *Column) { c.Scale = 1 }, func(c *Column) { c.Type = "INT" }} {
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
	s.Columns[0] = Column{Name: "id", Type: "ENUM", EnumValues: []string{"a"}}
	if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatal("ENUM key")
	}
	dict := make([]string, 256)
	dict[255] = "第256项"
	b := []byte{1, 0}
	label, index, err := decodeEnum(b, dict)
	if err != nil || label != "第256项" || index != 256 || !bytes.Equal(b, []byte{1, 0}) {
		t.Fatal("big endian golden", err)
	}
	// Non-ENUM records should not gain enum metadata.
	b, s = fixture(t, "lesson_rows")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range r.Records {
		if rec.EnumIndexes != nil {
			t.Fatal("old API metadata")
		}
	}
}

func TestEnumDamage(t *testing.T) {
	for _, name := range []string{"enum_1", "enum_256"} {
		t.Run(name, func(t *testing.T) {
			b, s := enumFixture(t, name)
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			rec := r.Records[2]
			base := int(rec.PageNumber) * PageSize
			off := base + rec.Offset + 17
			bad := append([]byte(nil), b...)
			for i := 0; i < enumWidth(len(s.Columns[1].EnumValues)); i++ {
				bad[off+i] = 0xff
			}
			resealTestPages(bad)
			got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("ordinal range", err)
			}
			bad = append([]byte(nil), b...)
			be.PutUint16(bad[base+40:], uint16(rec.End-1))
			resealTestPages(bad)
			got, err = Read(bytes.NewReader(bad), int64(len(bad)), s)
			if got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatal("truncation", err)
			}
		})
	}
}

func FuzzEnum(f *testing.F) {
	b, s := enumFixture(f, "enum_mixed")
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
			t.Fatal(fmt.Sprint("partial result", err))
		}
	})
}
