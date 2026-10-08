package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMetadataFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/sdi/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Cases []struct{ Source string } }
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, c := range manifest.Cases {
		t.Run(c.Source, func(t *testing.T) {
			var b []byte
			if strings.HasSuffix(c.Source, ".gz") {
				b = unzip(t, c.Source)
			} else {
				b, err = os.ReadFile(c.Source)
				if err != nil {
					t.Fatal(err)
				}
			}
			schemaPath := strings.TrimSuffix(strings.TrimSuffix(c.Source, ".gz"), ".ibd") + ".json"
			raw, err := os.ReadFile(schemaPath)
			if err != nil {
				t.Fatal(err)
			}
			var want Schema
			if err = json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			got, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Issues) != 0 || got.Schema == nil {
				t.Fatal("unsupported", got.Issues)
			}
			if !reflect.DeepEqual(*got.Schema, want) {
				t.Fatalf("schema mismatch\ngot %+v\nwant %+v", *got.Schema, want)
			}
			if !got.Indexes[0].RootVerified {
				t.Fatal("root not checked")
			}
			rows, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			manual, manualErr := Read(bytes.NewReader(b), int64(len(b)), want)
			if manualErr != nil {
				if rows != nil || !errors.Is(err, ErrUnsupported) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(rows, manual) {
				t.Fatal("automatic/manual rows mismatch", err)
			}
		})
	}
}

// SDI JSON mutation tests operate on decoded objects, independent of zlib/CRC.
func metadataLesson(t testing.TB) ([]byte, *SDIResult, map[string]any) {
	t.Helper()
	b, _ := fixture(t, "lesson_rows")
	sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	d := json.NewDecoder(bytes.NewReader(sdi.Records[0].JSON))
	d.UseNumber()
	if err = d.Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return b, sdi, envelope
}
func TestMetadataRejections(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		corrupt bool
	}{
		{"version", func(e map[string]any) { e["dd_version"] = 999 }, false},
		{"engine", func(e map[string]any) { e["dd_object"].(map[string]any)["engine"] = "Other" }, false},
		{"instant", func(e map[string]any) { e["dd_object"].(map[string]any)["se_private_data"] = "instant_col=3;" }, false},
		{"internal-hidden-generated", func(e map[string]any) {
			c := e["dd_object"].(map[string]any)["columns"].([]any)[1].(map[string]any)
			c["generation_expression"] = "id+1"
			c["hidden"] = 3
		}, false},
		{"json-invalid-collation-capacity", func(e map[string]any) {
			e["dd_object"].(map[string]any)["columns"].([]any)[1].(map[string]any)["type"] = 31
		}, false},
		{"charset", func(e map[string]any) {
			e["dd_object"].(map[string]any)["columns"].([]any)[2].(map[string]any)["collation_id"] = 28
		}, false},
		{"missing-nullable", func(e map[string]any) {
			delete(e["dd_object"].(map[string]any)["columns"].([]any)[1].(map[string]any), "is_nullable")
		}, true},
		{"root", func(e map[string]any) {
			e["dd_object"].(map[string]any)["indexes"].([]any)[0].(map[string]any)["se_private_data"] = "id=249;root=99;space_id=40;table_id=1102;"
		}, true},
		{"index-id", func(e map[string]any) {
			e["dd_object"].(map[string]any)["indexes"].([]any)[0].(map[string]any)["se_private_data"] = "id=999;root=4;space_id=40;table_id=1102;"
		}, true},
		{"column-opx", func(e map[string]any) {
			e["dd_object"].(map[string]any)["indexes"].([]any)[0].(map[string]any)["elements"].([]any)[0].(map[string]any)["column_opx"] = 999
		}, true},
		{"invalid-order", func(e map[string]any) {
			e["dd_object"].(map[string]any)["indexes"].([]any)[0].(map[string]any)["elements"].([]any)[0].(map[string]any)["order"] = 99
		}, false},
		{"physical-order", func(e map[string]any) {
			e["dd_object"].(map[string]any)["indexes"].([]any)[0].(map[string]any)["elements"].([]any)[3].(map[string]any)["column_opx"] = 2
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, sdi, e := metadataLesson(t)
			c.mutate(e)
			raw, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			sdi.Records[0].JSON = raw
			got, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), sdi)
			if c.corrupt {
				if got != nil || err == nil {
					t.Fatal("accepted corruption")
				}
			} else {
				if err != nil || got.Schema != nil || len(got.Issues) == 0 {
					t.Fatal("missing issue", err)
				}
			}
		})
	}
}

func TestMetadataProperties(t *testing.T) {
	for _, s := range []string{"id=1;id=2;", "id=x;broken", "=1;", "a=b=c;", "a=bad\\x;"} {
		if _, err := metadataProperties(s); !errors.Is(err, ErrCorrupt) {
			t.Fatal(s, err)
		}
	}
	p, err := metadataProperties(`a=x\;y\=z\\;id=18446744073709551615;`)
	if err != nil || p["a"] != `x;y=z\` {
		t.Fatal(p, err)
	}
	n, err := propertyUint(p, "id", 64)
	if err != nil || n != ^uint64(0) {
		t.Fatal(err)
	}
	for _, p := range []map[string]string{{}, {"id": "-1"}, {"id": "18446744073709551616"}} {
		if _, err := propertyUint(p, "id", 64); err == nil {
			t.Fatal("invalid number")
		}
	}
	var out struct {
		N uint32 `json:"n"`
	}
	for _, raw := range []string{`{"n":1,"n":2}`, `{"n":null}`, `{}`, `{"n":-1}`} {
		if err := decodeMetadata([]byte(raw), &out, "n"); !errors.Is(err, ErrCorrupt) {
			t.Fatal(err)
		}
	}
	if err := decodeMetadata([]byte(`{"n":1,"N":2}`), &out, "n"); err != nil || out.N != 1 {
		t.Fatal("noncanonical field overrode canonical value", err)
	}
}

func TestMetadataDictionary(t *testing.T) {
	for _, raw := range []string{`{"index":1,"name":"%%%"}`, `{"index":1,"name":"/w=="}`, `{"index":2,"name":"YQ=="}`} {
		c := ddColumn{Name: "choice", Type: 22, Collation: 255, Options: "interval_count=1;", Elements: []json.RawMessage{json.RawMessage(raw)}}
		if _, _, err := metadataColumn(c); !errors.Is(err, ErrCorrupt) {
			t.Fatal("accepted corrupt dictionary", err)
		}
	}
}

// Synthetic secondary root: only root identity is tested, not secondary records.
func TestMetadataSecondaryReport(t *testing.T) {
	b, sdi, e := metadataLesson(t)
	page := append([]byte(nil), b[4*PageSize:5*PageSize]...)
	root := uint32(len(b) / PageSize)
	be.PutUint32(page[4:], root)
	be.PutUint64(page[66:], 999)
	b = append(b, page...)
	resealTestPages(b)
	table := e["dd_object"].(map[string]any)
	indexes := table["indexes"].([]any)
	copyJSON, _ := json.Marshal(indexes[0])
	var secondary map[string]any
	json.Unmarshal(copyJSON, &secondary)
	secondary["name"], secondary["type"], secondary["ordinal_position"] = "idx_score", 3, 2
	secondary["se_private_data"] = fmt.Sprintf("id=999;root=%d;space_id=40;table_id=1102;", root)
	secondary["elements"] = []any{
		map[string]any{"ordinal_position": 1, "column_opx": 1, "order": 2, "length": 4, "hidden": false},
		map[string]any{"ordinal_position": 2, "column_opx": 0, "order": 2, "length": 4, "hidden": true},
	}
	table["indexes"] = append(indexes, secondary)
	sdi.Records[0].JSON, _ = json.Marshal(e)
	got, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), sdi)
	if err != nil || got.Schema == nil || len(got.Issues) != 0 || len(got.Indexes) != 2 || !got.Indexes[1].RootVerified || got.Indexes[1].Clustered {
		t.Fatal("secondary discovery contract", err)
	}
}

// Shipped snapshots run offline by default; override the directory to verify a new capture.
func TestMetadataLifecycle(t *testing.T) {
	dir := os.Getenv("INNODB_METADATA_FIXTURES")
	if dir == "" {
		dir = "testdata/metadata"
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Readable     bool
		}
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || len(manifest.Cases) != 5 {
		t.Fatal("incomplete lifecycle manifest", err)
	}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(dir, c.Name+".ibd"))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("fixture checksum")
			}
			// Compare SDI against the independent official ibd2sdi output.
			raw, err := os.ReadFile(filepath.Join(dir, c.Name+".sdi.json"))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err := json.Unmarshal(raw, &official); err != nil {
				t.Fatal(err)
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if len(official) != 3 || len(sdi.Records) != 2 {
				t.Fatal("SDI object count")
			}
			decode := func(raw []byte) any {
				var value any
				d := json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if err := d.Decode(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			for i, record := range sdi.Records {
				var want struct {
					Type   uint32
					ID     uint64
					Object json.RawMessage
				}
				if err := json.Unmarshal(official[i+1], &want); err != nil {
					t.Fatal(err)
				}
				if record.Key != (SDIKey{want.Type, want.ID}) || !reflect.DeepEqual(decode(record.JSON), decode(want.Object)) {
					t.Fatal("official SDI mismatch")
				}
			}
			raw, err = os.ReadFile(filepath.Join(dir, c.Name+".indexes.json"))
			if err != nil {
				t.Fatal(err)
			}
			var indexes []struct {
				Name  string
				ID    uint64 `json:"index_id"`
				Root  uint32 `json:"root_page"`
				Space uint32 `json:"space_id"`
				Table uint64 `json:"table_id"`
			}
			if err := json.Unmarshal(raw, &indexes); err != nil {
				t.Fatal(err)
			}
			m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if len(indexes) != len(m.Indexes) {
				t.Fatal("index count")
			}
			for _, want := range indexes {
				found := false
				for _, got := range m.Indexes {
					if got.Name == want.Name {
						found = true
						if !got.RootVerified || got.ID != want.ID || got.RootPage != want.Root || got.SpaceID != want.Space || m.TableID != want.Table {
							t.Fatal("SQL index identity mismatch")
						}
					}
				}
				if !found {
					t.Fatal("missing SQL index", want.Name)
				}
			}
			rows, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			// Stage 26 admits clustered reads; historical manifest flags remain immutable.

			if err != nil {
				t.Fatal(err)
			}
			// Expected columns come from the fixture SQL, independently of SDI.
			wantColumns := []Column{{Name: "id", Type: "INT"}, {Name: "score", Type: "INT", Nullable: true}, {Name: "name", Type: "VARCHAR", Nullable: true, MaxChars: 32}}
			if m.Schema == nil || m.Schema.PrimaryKey != "id" || !reflect.DeepEqual(m.Schema.Columns, wantColumns) {
				t.Fatal("SQL schema mismatch")
			}
			raw, err = os.ReadFile(filepath.Join(dir, c.Name+".expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want [][]any
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			values := make([][]any, len(rows.Records))
			for i, row := range rows.Records {
				values[i] = row.Values
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			var got [][]any
			// This fixture contains only 32-bit integers.
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("SQL rows mismatch")
			}
		})
	}
}

func TestMetadataMovedRoot(t *testing.T) {
	b, sdi, e := metadataLesson(t)
	page := append([]byte(nil), b[4*PageSize:5*PageSize]...)
	root := len(b) / PageSize
	be.PutUint32(page[4:], uint32(root))
	b = append(b, page...)
	resealTestPages(b)
	idx := e["dd_object"].(map[string]any)["indexes"].([]any)[0].(map[string]any)
	idx["se_private_data"] = "id=249;root=7;space_id=40;table_id=1102;"
	raw, _ := json.Marshal(e)
	sdi.Records[0].JSON = raw
	got, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), sdi)
	if err != nil || got.Schema == nil || got.Schema.RootPage != 7 {
		t.Fatal(err)
	}
	if _, err := Read(bytes.NewReader(b), int64(len(b)), *got.Schema); err != nil {
		t.Fatal(err)
	}
}

func TestReadAutoRejectedMetadata(t *testing.T) {
	// Isolate the table object in a synthetic leaf so changing zlib size does not
	// move another record. The complete public ReadAuto path still reads SDI.
	b, original := sdiTwoLeaf(t)
	var e map[string]any
	json.Unmarshal(original.Records[0].JSON, &e)
	e["dd_version"] = 999
	raw, _ := json.Marshal(e)
	payload := compressSDITest(t, raw)
	p := b[7*PageSize : 8*PageSize]
	origin := 127
	p[origin-7] = byte(len(payload))
	p[origin-6] = 0x80 | byte(len(payload)>>8)
	be.PutUint32(p[origin+25:], uint32(len(raw)))
	be.PutUint32(p[origin+29:], uint32(len(payload)))
	copy(p[origin+33:], payload)
	be.PutUint16(p[40:], uint16(origin+33+len(payload)))
	resealTestPages(b)
	if got, err := ReadAuto(bytes.NewReader(b), int64(len(b))); got != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("unsupported metadata returned rows", err)
	}
	b[7*PageSize+500] ^= 1
	if got, err := ReadAuto(bytes.NewReader(b), int64(len(b))); got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("corrupt SDI returned rows", err)
	}
}

func FuzzMetadata(f *testing.F) {
	b, sdi, _ := metadataLesson(f)
	original := append([]byte(nil), sdi.Records[0].JSON...)
	f.Add(uint16(100), []byte("null"))
	f.Fuzz(func(t *testing.T, off uint16, changes []byte) {
		if len(changes) > 1024 {
			return
		}
		bad := append([]byte(nil), original...)
		copy(bad[int(off)%len(bad):], changes)
		copySDI := *sdi
		copySDI.Records = append([]SDIRecord(nil), sdi.Records...)
		copySDI.Records[0].JSON = bad
		got, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), &copySDI)
		if err != nil && got != nil {
			t.Fatal("partial report")
		}
		if got != nil && len(got.Issues) > 0 && got.Schema != nil {
			t.Fatal("unsafe schema")
		}
	})
}
