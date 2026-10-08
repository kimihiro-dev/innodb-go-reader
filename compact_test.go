package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

func compactFixture(t testing.TB, directory, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, directory+"/"+name+".ibd.gz")
	raw, err := os.ReadFile(directory + "/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func compactCanonical(t testing.TB, values []any, s Schema) string {
	t.Helper()
	values = append([]any{}, values...)
	for i, v := range values {
		if v == nil {
			continue
		}
		switch x := v.(type) {
		case []byte:
			values[i] = strings.ToUpper(fmt.Sprintf("%x", x))
		case JSONValue:
			text, err := x.JSON()
			if err != nil {
				t.Fatal(err)
			}
			values[i] = json.RawMessage(text)
		case string:
			if s.Columns[i].Type == "JSON" {
				values[i] = json.RawMessage(x)
			}
		}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(jsonTextValue(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCompactFixtures(t *testing.T) {
	total, cases := 0, 0
	for _, dir := range []string{"testdata/compact", "testdata/compact-legacy"} {
		raw, err := os.ReadFile(dir + "/manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Writer string
			Cases  []struct {
				Name, SHA256, Pair, Format string
				Rows                       int
				Unordered                  bool
			}
		}
		if err = json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		if len(manifest.Cases) != 22 {
			t.Fatal("case count")
		}
		pairs := map[string][]string{}
		results := map[string]*MaterializedResult{}
		for _, c := range manifest.Cases {
			t.Run(dir+"/"+c.Name, func(t *testing.T) {
				b, s := compactFixture(t, dir, c.Name)
				r := bytes.NewReader(b)
				size := int64(len(b))
				if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
					t.Fatal("SHA256")
				}
				m, err := InspectTable(r, size)
				if err != nil {
					t.Fatal(err)
				}
				if m.MaterializedSchema == nil || !reflect.DeepEqual(s, *m.MaterializedSchema) {
					t.Fatalf("schema %v %v", m.Issues, err)
				}
				got, err := ReadMaterializedAuto(r, size)
				if err != nil {
					t.Fatal(err)
				}
				manual, err := ReadMaterialized(r, size, s)
				if err != nil || !reflect.DeepEqual(got, manual) {
					t.Fatal("manual/auto", err)
				}
				results[c.Name] = got
				full, err := ReadAuto(r, size)
				if len(s.VirtualColumns) > 0 {
					if full != nil || !errors.Is(err, ErrUnsupported) {
						t.Fatal("virtual full-row contract", err)
					}
				} else if err != nil || !reflect.DeepEqual(full, got.Result) {
					t.Fatal("strict read", err)
				}
				var expected []json.RawMessage
				if err = json.Unmarshal(unzip(t, dir+"/"+c.Name+".expected.json.gz"), &expected); err != nil {
					t.Fatal(err)
				}
				if len(expected) != c.Rows || len(got.Result.Records) != c.Rows {
					t.Fatal("row count")
				}
				actualRows := []string{}
				wantedRows := []string{}
				actualSet, wantedSet := map[string]int{}, map[string]int{}
				for _, raw := range expected {
					var row []any
					d := json.NewDecoder(bytes.NewReader(raw))
					d.UseNumber()
					if err = d.Decode(&row); err != nil {
						t.Fatal(err)
					}
					v := compactCanonical(t, row, s)
					wantedRows = append(wantedRows, v)
					wantedSet[v]++
				}
				for i, row := range got.Result.Records {
					v := compactCanonical(t, row.Values, s)
					actualRows = append(actualRows, v)
					actualSet[v]++
					if !c.Unordered && v != wantedRows[i] {
						t.Fatalf("SQL row %d", i)
					}
					for _, f := range row.External {
						ref := int(row.PageNumber)*PageSize + f.Offset
						if !bytes.Equal(f.Reference[:], b[ref:ref+20]) {
							t.Fatal("reference offset")
						}
						wantPrefix := 0
						if c.Format == "COMPACT" {
							wantPrefix = 768
						}
						if len(f.Prefix) != wantPrefix || !bytes.Equal(f.Prefix, b[ref-wantPrefix:ref]) {
							t.Fatal("local prefix provenance")
						}
						joined := append([]byte{}, f.Prefix...)
						sum := 0
						for _, chunk := range f.Chunks {
							start := int(chunk.PageNumber)*PageSize + chunk.Offset
							joined = append(joined, b[start:start+chunk.Length]...)
							sum += chunk.Length
							if f.Format == "BLOB" && (chunk.Offset != 46 || chunk.IndexPage != 0 || chunk.IndexOffset != 0) {
								t.Fatal("old BLOB has fictitious index")
							}
						}
						if sum != int(f.Length) {
							t.Fatal("suffix length")
						}
						value, err := variableValue(s.Columns[f.Column], joined)
						if err != nil || !reflect.DeepEqual(value, row.Values[f.Column]) {
							t.Fatal("complete prefix/chunk value", err)
						}
						if f.Format == "BLOB" {
							if f.Version != 0 || f.HeaderOffset != 38 || be.Uint16(b[int(f.FirstPage)*PageSize+24:]) != 10 {
								t.Fatal("old header interpretation")
							}
						} else if f.Version == 0 || f.HeaderOffset != 0 || be.Uint16(b[int(f.FirstPage)*PageSize+24:]) != 24 {
							t.Fatal("new LOB interpretation")
						}
					}
				}
				if !reflect.DeepEqual(actualSet, wantedSet) {
					t.Fatal("SQL multiset")
				}
				if c.Unordered {
					sort.Strings(actualRows)
				}
				if old, ok := pairs[c.Pair]; ok {
					if !reflect.DeepEqual(old, actualRows) {
						t.Fatal("COMPACT/DYNAMIC pair values")
					}
				} else {
					pairs[c.Pair] = actualRows
				}
				sdi, err := ReadSDI(r, size)
				if err != nil {
					t.Fatal(err)
				}
				var official []json.RawMessage
				if err = json.Unmarshal(unzip(t, dir+"/"+c.Name+".sdi.json.gz"), &official); err != nil {
					t.Fatal(err)
				}
				if len(official) != len(sdi.Records)+1 {
					t.Fatal("SDI count")
				}
				for i, x := range sdi.Records {
					var want struct {
						Type   uint32
						ID     uint64
						Object json.RawMessage
					}
					if err = json.Unmarshal(official[i+1], &want); err != nil {
						t.Fatal(err)
					}
					if x.Key != (SDIKey{want.Type, want.ID}) || !reflect.DeepEqual(jsonTextValue(t, x.JSON), jsonTextValue(t, want.Object)) {
						t.Fatal("official SDI")
					}
				}
				cases++
				total += c.Rows
			})
		}
		if t.Failed() {
			return
		}
		first := results["lesson_compact_initial"].Result.Records[0].External[0]
		if utf8.Valid(first.Prefix) {
			t.Fatal("fixture failed to split UTF-8 at prefix")
		}
		old := dir == "testdata/compact-legacy"
		if (first.Format == "BLOB") != old {
			t.Fatal("writer path not exercised")
		}
		mixed := results["lesson_compact_mixed_formats"].Result.Records[0].External
		if old && (mixed[0].Format != "" || mixed[1].Format != "BLOB") {
			t.Fatal("mixed old/new formats absent")
		}
		bounds := results["bounds_compact_initial"].Result.Records
		if len(bounds[0].External) != 0 || len(bounds[8].External) != 1 {
			t.Fatal("inline/external boundary absent")
		}
		if old && (len(bounds[9].External[0].Chunks) != 1 || len(bounds[10].External[0].Chunks) != 2 || len(bounds[11].External[0].Chunks) != 2 || len(bounds[12].External[0].Chunks) != 3) {
			t.Fatal("old page-capacity boundaries")
		}
		if results["tree_compact_instant"].Result.Page.Level == 0 || len(results["tree_compact_instant"].Result.Records[0].DefaultColumns) != 1 {
			t.Fatal("INSTANT tree")
		}
		if results["hidden_compact_initial"].Result.Records[0].RowID == nil || results["composite_compact_initial"].Result.Page.Level == 0 {
			t.Fatal("clustered identities/tree")
		}
	}
	if cases != 44 || total != 7732 {
		t.Fatal("matrix", cases, total)
	}
	t.Logf("%d snapshots, %d SQL rows", cases, total)
}

func TestCompactCorruption(t *testing.T) {
	original, s := compactFixture(t, "testdata/compact-legacy", "lesson_compact_initial")
	got, err := Read(bytes.NewReader(original), int64(len(original)), s)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Records[0]
	field := row.External[0]
	page := int(field.FirstPage) * PageSize
	ref := int(row.PageNumber)*PageSize + field.Offset
	cases := []struct {
		name        string
		mutate      func([]byte)
		unsupported bool
	}{
		{"cycle", func(b []byte) { be.PutUint32(b[page+42:], field.FirstPage) }, false},
		{"zero next", func(b []byte) { be.PutUint32(b[page+42:], 0) }, false},
		{"outside file", func(b []byte) { be.PutUint32(b[page+42:], uint32(len(b)/PageSize)) }, false},
		{"early end", func(b []byte) { be.PutUint32(b[page+42:], ^uint32(0)) }, false},
		{"zero part", func(b []byte) { be.PutUint32(b[page+38:], 0) }, false},
		{"oversize part", func(b []byte) { be.PutUint32(b[page+38:], PageSize) }, false},
		{"space", func(b []byte) { be.PutUint32(b[page+34:], field.SpaceID+1) }, false},
		{"type", func(b []byte) { be.PutUint16(b[page+24:], 23) }, true},
		{"header offset", func(b []byte) { be.PutUint32(b[ref+8:], 39) }, true},
		{"modified", func(b []byte) { b[ref+12] |= 0x20 }, true},
		{"invalid prefix", func(b []byte) { b[ref-768] = 0xff }, false},
		{"short suffix", func(b []byte) { be.PutUint32(b[ref+16:], 1) }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := append([]byte(nil), original...)
			c.mutate(b)
			resealTestPages(b)
			result, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			want := ErrCorrupt
			if c.unsupported {
				want = ErrUnsupported
			}
			if result != nil || !errors.Is(err, want) {
				t.Fatalf("result=%v error=%v", result != nil, err)
			}
		})
	}
	b := append([]byte(nil), original...)
	b[page+46] ^= 1
	if result, err := ReadAuto(bytes.NewReader(b), int64(len(b))); result != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("CRC", err)
	}
	b = append([]byte(nil), original...)
	be.PutUint32(b[page+12:], field.FirstPage)
	resealTestPages(b)
	if result, err := ReadAuto(bytes.NewReader(b), int64(len(b))); err != nil || !reflect.DeepEqual(result.Records, got.Records) {
		t.Fatal("FIL_NEXT must not replace BLOB header next", err)
	}
	wrong := s
	wrong.RowFormat = "DYNAMIC"
	if result, err := Read(bytes.NewReader(original), int64(len(original)), wrong); result != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("format mismatch", err)
	}
	f := ExternalField{Length: MaxLOBValueBytes - 767, Prefix: make([]byte, 768)}
	if _, err := readExternal(bytes.NewReader(nil), 0, &f); !errors.Is(err, ErrUnsupported) {
		t.Fatal("full value budget", err)
	}
	local := append(append([]byte(nil), field.Prefix...), field.Reference[:]...)
	parsed, err := parseCompactExternal(local, uint64(field.Length)+768, field.SpaceID)
	if err != nil {
		t.Fatal(err)
	}
	local[0] ^= 1
	if parsed.Prefix[0] == local[0] {
		t.Fatal("prefix aliases record")
	}
	if _, err := parseCompactExternal(local, uint64(field.Length)+767, field.SpaceID); !errors.Is(err, ErrCorrupt) {
		t.Fatal("column capacity", err)
	}
	if _, err := parseCompactExternal(local[:787], 1<<32, field.SpaceID); !errors.Is(err, ErrCorrupt) {
		t.Fatal("local length", err)
	}
}

func FuzzCompactLOB(f *testing.F) {
	original, s := compactFixture(f, "testdata/compact-legacy", "lesson_compact_initial")
	result, err := Read(bytes.NewReader(original), int64(len(original)), s)
	if err != nil {
		f.Fatal(err)
	}
	page := int(result.Records[0].External[0].FirstPage) * PageSize
	f.Add(uint16(38), []byte{0, 0, 0, 0})
	f.Add(uint16(42), []byte{255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, offset uint16, data []byte) {
		if len(data) > PageSize {
			return
		}
		b := append([]byte(nil), original...)
		start := int(offset) % PageSize
		copy(b[page+start:page+PageSize], data)
		resealTestPages(b)
		got, err := Read(bytes.NewReader(b), int64(len(b)), s)
		if err != nil && got != nil {
			t.Fatal("partial result")
		}
	})
}

func TestCompactKeyLimit(t *testing.T) {
	_, s := compactFixture(t, "testdata/compact", "composite_compact_initial")
	if _, err := s.validate(); err != nil {
		t.Fatal("767-byte member plus INT", err)
	}
	s.Columns[1].MaxBytes = 768
	if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatal("768-byte member", err)
	}
	s.RowFormat = "DYNAMIC"
	if _, err := s.validate(); err != nil {
		t.Fatal("DYNAMIC key regression", err)
	}
	s.RowFormat = "REDUNDANT"
	if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatal("REDUNDANT", err)
	}
}
