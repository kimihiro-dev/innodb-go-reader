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

func datetimeFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/datetimes", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/datetimes", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestDatetimeFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/datetimes/manifest.json")
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
	if len(m.Cases) != 9 || m.Environment.SQLMode != "ALLOW_INVALID_DATES" {
		t.Fatal("fixture contract")
	}
	total := 0
	seen := map[int]bool{}
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := datetimeFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/datetimes", c.Name+".expected.json.gz"))))
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
					t.Fatalf("SQL row %d mismatch", i)
				}
				for k, col := range s.Columns {
					if col.Type == "DATETIME" && rec.Values[k] != nil {
						v, ok := rec.Values[k].(string)
						n := 19
						if col.FSP != 0 {
							n += 1 + col.FSP
						}
						if !ok || len(v) != n {
							t.Fatal("output precision")
						}
					}
				}
			}
			if strings.HasPrefix(c.Name, "datetime_fsp_") {
				fsp := s.Columns[1].FSP
				seen[fsp] = true
				if c.Rows != 16 {
					t.Fatal("boundary rows")
				}
				suffix := ""
				if fsp != 0 {
					suffix = "." + strings.Repeat("0", fsp)
				}
				for i, base := range map[int]string{1: "0000-00-00 00:00:00", 2: "0001-01-01 00:00:00", 4: "1900-02-29 01:02:03", 5: "2024-00-15 12:34:56", 6: "2024-05-00 12:34:56"} {
					if r.Records[i].Values[1] != base+suffix {
						t.Fatal("special input normalized")
					}
				}
				rounded := "2024-03-01 00:00:00" + suffix
				if fsp == 6 {
					rounded = "2024-02-29 23:59:59.999999"
				}
				if r.Records[14].Values[1] != rounded {
					t.Fatal("stored rounding boundary")
				}
				end := "9999-12-31 23:59:59"
				if fsp != 0 {
					end += "." + strings.Repeat("9", fsp)
				}
				if r.Records[15].Values[1] != end {
					t.Fatal("maximum endpoint")
				}
				for i, rec := range r.Records {
					length := 17
					if i != 0 {
						length += datetimeWidth(fsp)
					}
					if rec.End-rec.Offset != length {
						t.Fatal("fixed width or NULL")
					}
				}
			}
			if c.Name == "datetime_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("tree coverage")
			}
			if c.Name == "datetime_mixed" && len(r.Records[1].External) != 1 {
				t.Fatal("LOB coverage")
			}
			total += c.Rows
		})
	}
	if total != 716 || len(seen) != 7 {
		t.Fatal("coverage", total, seen)
	}
}

func TestDatetimeContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for fsp := 0; fsp <= 6; fsp++ {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = Column{Name: "name", Type: "DATETIME", FSP: fsp, Nullable: true}
		if _, err := s.validate(); err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{0, datetimeWidth(fsp) - 1, datetimeWidth(fsp) + 1} {
			if _, err := decodeDatetime(make([]byte, n), fsp); !errors.Is(err, ErrCorrupt) {
				t.Fatal("length accepted")
			}
		}
	}
	good := Column{Name: "name", Type: "DATETIME", FSP: 6, Nullable: true}
	for _, change := range []func(*Column){func(c *Column) { c.FSP = -1 }, func(c *Column) { c.FSP = 7 }, func(c *Column) { c.Unsigned = true }, func(c *Column) { c.Precision = 6 }, func(c *Column) { c.Scale = 1 }, func(c *Column) { c.MaxChars = 26 }, func(c *Column) { c.MaxBytes = 8 }, func(c *Column) { c.Type = "DATE" }, func(c *Column) { c.Type = "TIMESTAMP_OLD" }, func(c *Column) { c.Type = "DECIMAL"; c.Precision = 10 }} {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = good
		change(&s.Columns[2])
		if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal("bad attributes")
		}
	}
	s := base
	s.Columns = append([]Column(nil), base.Columns...)
	s.Columns[0] = Column{Name: "id", Type: "DATETIME"}
	if _, err := s.validate(); err != nil {
		t.Fatal("DATETIME PK")
	}
	raw := []string{"99b2bac8b8", "99b2bac8b80a", "99b2bac8b80c", "99b2bac8b804ce", "99b2bac8b804d2", "99b2bac8b801e23a", "99b2bac8b801e240"}
	for fsp, text := range raw {
		b, _ := hex.DecodeString(text)
		original := append([]byte(nil), b...)
		v, err := decodeDatetime(b, fsp)
		want := "2024-02-29 12:34:56"
		if fsp != 0 {
			want += "." + "123456"[:fsp]
		}
		if err != nil || v != want {
			t.Fatal("golden", fsp, v, err)
		}
		if !bytes.Equal(b, original) {
			t.Fatal("mutated input")
		}
	}
}

func TestDatetimeDamage(t *testing.T) {
	for fsp := 0; fsp <= 6; fsp++ {
		t.Run(fmt.Sprint(fsp), func(t *testing.T) {
			b, s := datetimeFixture(t, fmt.Sprintf("datetime_fsp_%d", fsp))
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			off := int(r.Records[12].PageNumber)*PageSize + r.Records[12].Offset + 17
			cases := []struct {
				name   string
				change func([]byte)
			}{
				{"negative", func(b []byte) { b[off] &= 0x7f }},
			}
			put := func(b []byte, u uint64) {
				for i := 4; i >= 0; i-- {
					b[off+i] = byte(u)
					u >>= 8
				}
			}
			packed := uint64(0x99b2bac8b8)
			for _, test := range []struct {
				name string
				bits uint64
			}{{"year", 0x8000000000 + (10000*13)<<22}, {"hour", packed&^(31<<12) | 24<<12}, {"minute", packed&^(63<<6) | 60<<6}, {"second", packed&^63 | 60}} {
				test := test
				cases = append(cases, struct {
					name   string
					change func([]byte)
				}{test.name, func(b []byte) { put(b, test.bits) }})
			}
			if fsp > 0 {
				cases = append(cases, struct {
					name   string
					change func([]byte)
				}{"fraction-range", func(b []byte) {
					for i := 5; i < datetimeWidth(fsp); i++ {
						b[off+i] = 0xff
					}
				}})
				if fsp%2 == 1 {
					cases = append(cases, struct {
						name   string
						change func([]byte)
					}{"fraction-alignment", func(b []byte) {
						for i := 5; i < datetimeWidth(fsp); i++ {
							b[off+i] = 0
						}
						b[off+datetimeWidth(fsp)-1] = 1
					}})
				}
			}
			cases = append(cases, struct {
				name   string
				change func([]byte)
			}{"truncated-heap", func(b []byte) {
				page := int(r.Records[12].PageNumber) * PageSize
				be.PutUint16(b[page+40:], uint16(r.Records[12].Offset+18))
			}})
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					bad := append([]byte(nil), b...)
					tc.change(bad)
					resealTestPages(bad)
					got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
					if got != nil || !errors.Is(err, ErrCorrupt) {
						t.Fatal("accepted bad datetime", err)
					}
				})
			}
		})
	}
}

func FuzzDatetime(f *testing.F) {
	b, s := datetimeFixture(f, "datetime_mixed")
	f.Add(uint32(4*PageSize+146), []byte{0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, off uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(off)%len(bad):], change)
		resealTestPages(bad)
		r, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if err != nil && r != nil {
			t.Fatal("partial table")
		}
	})
}
