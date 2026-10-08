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
	"time"
)

func timestampFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/timestamps", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/timestamps", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestTimestampFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/timestamps/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Environment struct {
			SQLMode  string `json:"sql_mode"`
			TimeZone string `json:"time_zone"`
		}
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err = json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Cases) != 10 || m.Environment.SQLMode != "STRICT_TRANS_TABLES" || m.Environment.TimeZone != "+00:00" {
		t.Fatal("fixture contract")
	}
	total := 0
	seen := map[int]bool{}
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := timestampFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/timestamps", c.Name+".expected.json.gz"))))
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
					if col.Type == "TIMESTAMP" && rec.Values[k] != nil {
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
			if strings.HasPrefix(c.Name, "timestamp_fsp_") {
				fsp := s.Columns[1].FSP
				seen[fsp] = true
				if c.Rows != 12 {
					t.Fatal("boundary rows")
				}
				suffix := ""
				if fsp != 0 {
					suffix = "." + strings.Repeat("0", fsp)
				}
				for i, base := range map[int]string{1: "0000-00-00 00:00:00", 2: "1970-01-01 00:00:01", 3: "2038-01-19 03:14:07"} {
					if r.Records[i].Values[1] != base+suffix {
						t.Fatal("boundary", i)
					}
				}
				rounded := "2024-03-01 00:00:00" + suffix
				if fsp == 6 {
					rounded = "2024-02-29 23:59:59.999999"
				}
				if r.Records[10].Values[1] != rounded {
					t.Fatal("stored rounding boundary")
				}
				end := "2038-01-19 03:14:07"
				if fsp != 0 {
					end += "." + strings.Repeat("9", fsp)
				}
				if r.Records[11].Values[1] != end {
					t.Fatal("maximum endpoint")
				}
				for i, rec := range r.Records {
					length := 17
					if i != 0 {
						length += timestampWidth(fsp)
					}
					if rec.End-rec.Offset != length {
						t.Fatal("fixed width or NULL")
					}
				}
			}
			if c.Name == "timestamp_tree" && (r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("tree coverage")
			}
			if c.Name == "timestamp_mixed" && len(r.Records[1].External) != 1 {
				t.Fatal("LOB coverage")
			}
			total += c.Rows
		})
	}
	if total != 691 || len(seen) != 7 {
		t.Fatal("coverage", total, seen)
	}
}

func TestTimestampContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for fsp := 0; fsp <= 6; fsp++ {
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = Column{Name: "name", Type: "TIMESTAMP", FSP: fsp, Nullable: true}
		if _, err := s.validate(); err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{0, timestampWidth(fsp) - 1, timestampWidth(fsp) + 1} {
			if _, err := decodeTimestamp(make([]byte, n), fsp); !errors.Is(err, ErrCorrupt) {
				t.Fatal("length accepted")
			}
		}
	}
	good := Column{Name: "name", Type: "TIMESTAMP", FSP: 6, Nullable: true}
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
	s.Columns[0] = Column{Name: "id", Type: "TIMESTAMP"}
	if _, err := s.validate(); err != nil {
		t.Fatal("TIMESTAMP PK")
	}
	raw := []string{"65e079f0", "65e079f00a", "65e079f00c", "65e079f004ce", "65e079f004d2", "65e079f001e23a", "65e079f001e240"}
	for fsp, text := range raw {
		b, _ := hex.DecodeString(text)
		original := append([]byte(nil), b...)
		v, err := decodeTimestamp(b, fsp)
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

func TestTimestampDamage(t *testing.T) {
	for fsp := 0; fsp <= 6; fsp++ {
		t.Run(fmt.Sprint(fsp), func(t *testing.T) {
			b, s := timestampFixture(t, fmt.Sprintf("timestamp_fsp_%d", fsp))
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			off := int(r.Records[7].PageNumber)*PageSize + r.Records[7].Offset + 17
			cases := []struct {
				name   string
				change func([]byte)
			}{
				{"seconds-range", func(b []byte) { be.PutUint32(b[off:], 0x80000000) }},
				{"seconds-max", func(b []byte) { be.PutUint32(b[off:], 0xffffffff) }},
				{"truncated-heap", func(b []byte) {
					page := int(r.Records[7].PageNumber) * PageSize
					be.PutUint16(b[page+40:], uint16(r.Records[7].Offset+18))
				}},
			}
			if fsp > 0 {
				cases = append(cases, struct {
					name   string
					change func([]byte)
				}{"zero-fraction", func(b []byte) { be.PutUint32(b[off:], 0) }})
				cases = append(cases, struct {
					name   string
					change func([]byte)
				}{"fraction-range", func(b []byte) {
					for i := 4; i < timestampWidth(fsp); i++ {
						b[off+i] = 0xff
					}
				}})
				if fsp%2 == 1 {
					cases = append(cases, struct {
						name   string
						change func([]byte)
					}{"fraction-alignment", func(b []byte) {
						for i := 4; i < timestampWidth(fsp); i++ {
							b[off+i] = 0
						}
						b[off+timestampWidth(fsp)-1] = 1
					}})
				}
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					bad := append([]byte(nil), b...)
					tc.change(bad)
					resealTestPages(bad)
					got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
					if got != nil || !errors.Is(err, ErrCorrupt) {
						t.Fatal("accepted bad timestamp", err)
					}
				})
			}
		})
	}
}

func FuzzTimestamp(f *testing.F) {
	b, s := timestampFixture(f, "timestamp_mixed")
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

func TestTimestampZones(t *testing.T) {
	b, s := timestampFixture(t, "timestamp_zones")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	var displays map[string][][]any
	d := json.NewDecoder(bytes.NewReader(unzip(t, "testdata/timestamps/timestamp_zones.zones.json.gz")))
	d.UseNumber()
	if err = d.Decode(&displays); err != nil {
		t.Fatal(err)
	}
	if len(displays) != 3 {
		t.Fatal("zone coverage")
	}
	for zone, offset := range map[string]int{"+00:00": 0, "+08:00": 8 * 3600, "-05:30": -19800} {
		rows := displays[zone]
		if len(rows) != 3 {
			t.Fatal("rows")
		}
		for i, rec := range r.Records {
			stamp, err := time.Parse("2006-01-02 15:04:05.000000", rec.Values[1].(string))
			if err != nil {
				t.Fatal(err)
			}
			want := stamp.Add(time.Duration(offset) * time.Second).Format("2006-01-02 15:04:05.000000")
			if rows[i][1] != want || rows[i][2] != "2024-02-29 12:34:56.123456" || rec.Values[2] != rows[i][2] {
				t.Fatal("SQL timezone mismatch", zone, i, rows[i])
			}
		}
	}
	for i, want := range []string{"2024-02-29 12:34:56.123456", "2024-02-29 04:34:56.123456", "2024-02-29 18:04:56.123456"} {
		if r.Records[i].Values[1] != want {
			t.Fatal("insert timezone", i)
		}
	}
}
