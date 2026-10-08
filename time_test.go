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

func timeFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/times", name+".ibd.gz"))
	j, e := os.ReadFile(filepath.Join("testdata/times", name+".json"))
	if e != nil {
		t.Fatal(e)
	}
	var s Schema
	if e = json.Unmarshal(j, &s); e != nil {
		t.Fatal(e)
	}
	return b, s
}
func TestTimeFixtures(t *testing.T) {
	j, e := os.ReadFile("testdata/times/manifest.json")
	if e != nil {
		t.Fatal(e)
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
	if e = json.Unmarshal(j, &m); e != nil {
		t.Fatal(e)
	}
	if len(m.Cases) != 9 || m.Environment.SQLMode != "STRICT_TRANS_TABLES" {
		t.Fatal("manifest")
	}
	seen := map[int]bool{}
	total := 0
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := timeFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA")
			}
			r, e := Read(bytes.NewReader(b), int64(len(b)), s)
			if e != nil {
				t.Fatal(e)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/times", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if e = d.Decode(&want); e != nil {
				t.Fatal(e)
			}
			if len(r.Records) != c.Rows || len(want) != c.Rows {
				t.Fatal("row count")
			}
			for i, rec := range r.Records {
				raw, e := json.Marshal(rec.Values)
				if e != nil {
					t.Fatal(e)
				}
				var got []any
				d = json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if e = d.Decode(&got); e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(got, want[i]) {
					t.Fatalf("SQL row %d mismatch", i)
				}
				for k, col := range s.Columns {
					if col.Type == "TIME" && rec.Values[k] != nil {
						v, ok := rec.Values[k].(string)
						if !ok {
							t.Fatal("TIME output type")
						}
						parts := strings.Split(v, ".")
						if col.FSP == 0 {
							if len(parts) != 1 {
								t.Fatal("unexpected fraction")
							}
						} else if len(parts) != 2 || len(parts[1]) != col.FSP {
							t.Fatal("fsp lost")
						}
					}
				}
			}
			if strings.HasPrefix(c.Name, "time_fsp_") {
				fsp := s.Columns[1].FSP
				seen[fsp] = true
				if c.Rows != 31 {
					t.Fatal("boundary coverage")
				}
				suffix := ""
				if fsp > 0 {
					suffix = "." + strings.Repeat("0", fsp)
				}
				for i, base := range map[int]string{1: "00:00:00", 2: "00:00:00", 3: "00:00:01", 4: "-00:00:01", 7: "25:00:00", 8: "-25:00:00", 9: "838:59:59", 10: "-838:59:59"} {
					if r.Records[i].Values[1] != base+suffix {
						t.Fatal("endpoint", i)
					}
				}
				rounded := "00:01:00" + suffix
				if fsp == 6 {
					rounded = "00:00:59.999999"
				}
				if r.Records[29].Values[1] != rounded || r.Records[30].Values[1] != "-"+rounded {
					t.Fatal("signed rounding")
				}
				if fsp > 0 {
					tiny := "00:00:00." + strings.Repeat("0", fsp-1) + "1"
					if r.Records[11].Values[1] != tiny || r.Records[14].Values[1] != "-"+tiny {
						t.Fatal("subsecond sign")
					}
				}
				for i, rec := range r.Records {
					n := 17
					if i != 0 {
						n += timeWidth(fsp)
					}
					if rec.End-rec.Offset != n {
						t.Fatal("local width")
					}
				}
			}
			if c.Name == "time_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("tree")
			}
			if c.Name == "time_mixed" && len(r.Records[1].External) != 1 {
				t.Fatal("LOB")
			}
			total += c.Rows
		})
	}
	if total != 821 || len(seen) != 7 {
		t.Fatal("coverage", total)
	}
}
func TestTimeContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for fsp := 0; fsp <= 6; fsp++ {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = Column{Name: "name", Type: "TIME", FSP: fsp, Nullable: true}
		if _, e := s.validate(); e != nil {
			t.Fatal(e)
		}
		for _, n := range []int{0, timeWidth(fsp) - 1, timeWidth(fsp) + 1} {
			if _, e := decodeTime(make([]byte, n), fsp); !errors.Is(e, ErrCorrupt) {
				t.Fatal("width")
			}
		}
	}
	for _, change := range []func(*Column){func(c *Column) { c.FSP = -1 }, func(c *Column) { c.FSP = 7 }, func(c *Column) { c.Unsigned = true }, func(c *Column) { c.Precision = 6 }, func(c *Column) { c.Scale = 1 }, func(c *Column) { c.MaxChars = 10 }, func(c *Column) { c.MaxBytes = 6 }, func(c *Column) { c.Type = "YEAR" }} {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = Column{Name: "name", Type: "TIME", FSP: 6}
		change(&s.Columns[2])
		if _, e := s.validate(); !errors.Is(e, ErrUnsupported) {
			t.Fatal("attributes")
		}
	}
	s := base
	s.Columns = append([]Column(nil), base.Columns...)
	s.Columns[0] = Column{Name: "id", Type: "TIME"}
	if _, e := s.validate(); e != nil {
		t.Fatal("PK")
	}
	for _, tc := range []struct {
		fsp       int
		raw, want string
	}{
		{0, "800000", "00:00:00"}, {0, "7fffff", "-00:00:01"}, {0, "80c8b8", "12:34:56"},
		{1, "7ffffff6", "-00:00:00.1"}, {2, "7fffffff", "-00:00:00.01"}, {3, "7ffffffff6", "-00:00:00.001"}, {4, "7fffffffff", "-00:00:00.0001"}, {5, "7ffffffffff6", "-00:00:00.00001"}, {6, "7fffffffffff", "-00:00:00.000001"},
		{6, "7f3747fe1dc0", "-12:34:56.123456"},
	} {
		b, _ := hex.DecodeString(tc.raw)
		copyB := append([]byte(nil), b...)
		v, e := decodeTime(b, tc.fsp)
		if e != nil || v != tc.want {
			t.Fatal(tc.raw, v, e)
		}
		if !bytes.Equal(copyB, b) {
			t.Fatal("mutated input")
		}
	}
}
func TestTimeDamage(t *testing.T) {
	for fsp := 0; fsp <= 6; fsp++ {
		t.Run(fmt.Sprint(fsp), func(t *testing.T) {
			b, s := timeFixture(t, fmt.Sprintf("time_fsp_%d", fsp))
			r, e := Read(bytes.NewReader(b), int64(len(b)), s)
			if e != nil {
				t.Fatal(e)
			}
			off := int(r.Records[17].PageNumber)*PageSize + r.Records[17].Offset + 17
			type badValue struct {
				name      string
				hms, frac int64
				negative  bool
			}
			cases := []badValue{{"hour", 839 << 12, 0, false}, {"reserved", 1 << 22, 0, false}, {"minute", 60 << 6, 0, false}, {"second", 60, 0, false}, {"negative-hour", 839 << 12, 0, true}}
			if fsp > 0 {
				limit := int64(100)
				if fsp > 2 {
					limit = 10000
				}
				if fsp > 4 {
					limit = 1000000
				}
				cases = append(cases, badValue{"fraction", 0, limit, false}, badValue{"negative-fraction", 0, limit, true}, badValue{"endpoint", 838<<12 | 59<<6 | 59, 10, false}, badValue{"negative-endpoint", 838<<12 | 59<<6 | 59, 10, true})
				if fsp%2 == 1 {
					cases = append(cases, badValue{"alignment", 0, 1, false}, badValue{"negative-alignment", 0, 1, true})
				}
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					bad := append([]byte(nil), b...)
					hms, frac := tc.hms, tc.frac
					n := timeWidth(fsp) - 3
					if tc.negative {
						hms = -hms
						if frac != 0 {
							hms--
							frac = (int64(1) << uint(n*8)) - frac
						}
					}
					v := hms + 0x800000
					for i := 2; i >= 0; i-- {
						bad[off+i] = byte(v)
						v >>= 8
					}
					for i := n - 1; i >= 0; i-- {
						bad[off+3+i] = byte(frac)
						frac >>= 8
					}
					resealTestPages(bad)
					got, e := Read(bytes.NewReader(bad), int64(len(bad)), s)
					if got != nil || !errors.Is(e, ErrCorrupt) {
						t.Fatal("accepted corrupt TIME", e)
					}
				})
			}
			bad := append([]byte(nil), b...)
			page := int(r.Records[17].PageNumber) * PageSize
			be.PutUint16(bad[page+40:], uint16(r.Records[17].Offset+18))
			resealTestPages(bad)
			got, e := Read(bytes.NewReader(bad), int64(len(bad)), s)
			if got != nil || !errors.Is(e, ErrCorrupt) {
				t.Fatal("truncation")
			}
		})
	}
}
func FuzzTime(f *testing.F) {
	b, s := timeFixture(f, "time_mixed")
	f.Add(uint32(4*PageSize+146), []byte{0x7f, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, off uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		bad := append([]byte(nil), b...)
		copy(bad[int(off)%len(bad):], change)
		resealTestPages(bad)
		r, e := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if e != nil && r != nil {
			t.Fatal("partial table")
		}
	})
}
