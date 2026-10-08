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

func dateFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/dates", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/dates", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestDateFixtures(t *testing.T) {
	j, err := os.ReadFile("testdata/dates/manifest.json")
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
	if len(m.Cases) != 4 || m.Environment.SQLMode != "ALLOW_INVALID_DATES" {
		t.Fatal("fixture contract")
	}
	total := 0
	dates := []any{nil, "0000-00-00", "0000-01-01", "0001-01-01", "0999-12-31", "1000-01-01", "1900-02-28", "1900-02-29", "2000-02-29", "2024-02-29", "2023-02-31", "2024-00-15", "2024-05-00", "9999-12-31"}
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := dateFixture(t, c.Name)
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			d := json.NewDecoder(bytes.NewReader(unzip(t, filepath.Join("testdata/dates", c.Name+".expected.json.gz"))))
			d.UseNumber()
			if err = d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			if len(r.Records) != c.Rows || len(want) != c.Rows {
				t.Fatal("row count")
			}
			for i, rec := range r.Records {
				j, err := json.Marshal(rec.Values)
				if err != nil {
					t.Fatal(err)
				}
				var got []any
				d = json.NewDecoder(bytes.NewReader(j))
				d.UseNumber()
				if err = d.Decode(&got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want[i]) {
					t.Fatalf("SQL row %d mismatch", i)
				}
				for k, col := range s.Columns {
					if rec.Values[k] != nil {
						switch col.Type {
						case "DATE":
							if _, ok := rec.Values[k].(string); !ok {
								t.Fatal("DATE type")
							}
						case "YEAR":
							if _, ok := rec.Values[k].(uint16); !ok {
								t.Fatal("YEAR type")
							}
						}
					}
				}
				if c.Name == "date_values" {
					if i >= len(dates) || rec.Values[1] != dates[i] {
						t.Fatal("SQL normalized special input", i)
					}
				}
				if c.Name == "date_components" {
					expected := fmt.Sprintf("%04d-%02d-%02d", []int{1900, 2000, 2024, 9999}[i/(13*32)], (i/32)%13, i%32)
					if rec.Values[0] != expected {
						t.Fatal("component input changed", i, rec.Values[0])
					}
				}
				if c.Name == "year_values" && i < 256 {
					expected := uint16(0)
					if i != 0 {
						expected = uint16(i + 1900)
					}
					if rec.Values[1] != expected {
						t.Fatal("YEAR mapping", i)
					}
					off := int(rec.PageNumber)*PageSize + rec.Offset + 17
					if b[off] != byte(i) {
						t.Fatal("YEAR raw byte", i)
					}
				}
			}
			if c.Name == "date_values" && c.Rows != len(dates) {
				t.Fatal("date coverage")
			}
			if c.Name == "year_values" && c.Rows != 257 {
				t.Fatal("YEAR coverage")
			}
			if c.Name == "date_components" && (c.Rows != 1664 || r.Page.Level != 1 || len(r.Pages) < 3) {
				t.Fatal("missing component/tree coverage")
			}
			if c.Name == "date_mixed" && len(r.Records[1].External) != 1 {
				t.Fatal("missing mixed LOB")
			}
			total += c.Rows
		})
	}
	if total != 1939 {
		t.Fatal("total", total)
	}
}

func TestDateContracts(t *testing.T) {
	_, base := fixture(t, "lesson_rows")
	for _, kind := range []string{"DATE", "YEAR"} {
		good := Column{Name: "name", Type: kind, Nullable: true}
		s := base
		s.Columns = append([]Column(nil), base.Columns...)
		s.Columns[2] = good
		if _, err := s.validate(); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func(*Column){func(c *Column) { c.Unsigned = true }, func(c *Column) { c.Precision = 4 }, func(c *Column) { c.Scale = 1 }, func(c *Column) { c.MaxChars = 10 }, func(c *Column) { c.MaxBytes = 3 }} {
			s.Columns[2] = good
			change(&s.Columns[2])
			if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
				t.Fatal("accepted date attributes")
			}
		}
		s.Columns[2] = good
		s.Columns[2].Nullable = false
		s.PrimaryKey = "name"
		if _, err := s.validate(); err != nil {
			t.Fatal("date PK accepted")
		}
		for _, n := range []int{0, dateWidth(kind) - 1, dateWidth(kind) + 1} {
			if _, err := decodeDate(make([]byte, n), kind); !errors.Is(err, ErrCorrupt) {
				t.Fatal("date width")
			}
		}
	}
	for _, tc := range []struct {
		raw, want string
		bad       bool
	}{
		{"8fd05d", "2024-02-29", false}, {"800000", "0000-00-00", false}, {"800021", "0000-01-01", false},
		{"800221", "0001-01-01", false}, {"ce1f9f", "9999-12-31", false},
		{"000000", "", true}, {"ce2021", "", true}, {"8fd1a1", "", true},
	} {
		b, _ := hex.DecodeString(tc.raw)
		copyB := append([]byte(nil), b...)
		v, err := decodeDate(b, "DATE")
		if tc.bad {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatal("bad DATE accepted", tc.raw)
			}
		} else if err != nil || v != tc.want {
			t.Fatal(tc.raw, v, err)
		}
		if !bytes.Equal(b, copyB) {
			t.Fatal("input changed")
		}
	}
}

func TestDateDamage(t *testing.T) {
	b, s := dateFixture(t, "date_values")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	off := int(r.Records[1].PageNumber)*PageSize + r.Records[1].Offset + 17
	for _, raw := range []string{"000000", "ce2021", "8fd1a1"} {
		bad := append([]byte(nil), b...)
		v, _ := hex.DecodeString(raw)
		copy(bad[off:], v)
		resealTestPages(bad)
		got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
		if got != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("partial/invalid date table", err)
		}
	}
	bad := append([]byte(nil), b...)
	page := int(r.Records[1].PageNumber) * PageSize
	be.PutUint16(bad[page+40:], uint16(r.Records[1].Offset+18))
	resealTestPages(bad)
	got, err := Read(bytes.NewReader(bad), int64(len(bad)), s)
	if got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("truncated record accepted", err)
	}
}

func FuzzDate(f *testing.F) {
	b, s := dateFixture(f, "date_mixed")
	f.Add(uint32(4*PageSize+146), []byte{0, 0, 0})
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
