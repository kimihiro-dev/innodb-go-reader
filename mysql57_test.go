package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const mysql57Fixtures = "testdata/mysql5744"

func mysql57Fixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, mysql57Fixtures+"/"+name+".ibd.gz")
	raw, err := os.ReadFile(mysql57Fixtures + "/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var schema Schema
	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	return b, schema
}

// Existing fixture matrices keep SDI discovery for 8.0 and use the independently
// collected schema for the 5.7 corpus, whose auto-discovery refusal is tested above.
func mysql57Schema(t testing.TB, path string) *Schema {
	t.Helper()
	if dir := filepath.Base(filepath.Dir(path)); dir != "mysql5744" && dir != "mysql5744-secondary" {
		return nil
	}
	var schema Schema
	raw, err := os.ReadFile(strings.TrimSuffix(path, ".ibd.gz") + ".json")
	if err != nil || json.Unmarshal(raw, &schema) != nil {
		t.Fatal("schema", err)
	}
	return &schema
}

func mysql57Canonical(t testing.TB, raw []byte) string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mysql57Row(t testing.TB, values []any) string {
	t.Helper()
	values = append([]any{}, values...)
	for i, value := range values {
		switch v := value.(type) {
		case []byte:
			values[i] = strings.ToUpper(fmt.Sprintf("%x", v))
		case JSONValue:
			b, err := v.JSON()
			if err != nil {
				t.Fatal(err)
			}
			values[i] = json.RawMessage(b)
		case GeometryValue:
			values[i] = map[string]any{"srid": v.SRID, "wkb_hex": strings.ToUpper(fmt.Sprintf("%x", v.WKB))}
		}
	}
	b, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return mysql57Canonical(t, b)
}

func TestMySQL57Fixtures(t *testing.T) {
	raw, err := os.ReadFile(mysql57Fixtures + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Status string
		Cases  []struct {
			Name, SHA256 string
			Rows         int
			Unordered    bool
		}
	}
	if err = json.Unmarshal(raw, &manifest); err != nil || manifest.Status != "captured" || len(manifest.Cases) != 22 {
		t.Fatal("incomplete manifest", err)
	}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, schema := mysql57Fixture(t, c.Name)
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("fixture SHA256 mismatch")
			}
			var source struct {
				Environment struct {
					Version  string
					PageSize int `json:"page_size"`
					Checksum string
				}
			}
			sourceRaw, err := os.ReadFile(mysql57Fixtures + "/" + c.Name + ".source.json")
			if err != nil || json.Unmarshal(sourceRaw, &source) != nil || source.Environment.Version != "5.7.44" || source.Environment.PageSize != PageSize || source.Environment.Checksum != "strict_crc32" {
				t.Fatal("source version/configuration", err)
			}
			reader, size := bytes.NewReader(b), int64(len(b))
			if err := ValidateSchema(reader, size, schema); err != nil {
				t.Fatal("schema/root preflight", err)
			}
			if _, err := ReadAuto(reader, size); !errors.Is(err, ErrUnsupported) {
				t.Fatal("5.7 must not use SDI auto discovery", err)
			}
			result, err := ReadMaterialized(reader, size, schema)
			if err != nil {
				t.Fatal(err)
			}
			if len(schema.VirtualColumns) > 0 {
				if _, err = Read(reader, size, schema); !errors.Is(err, ErrUnsupported) {
					t.Fatal("VIRTUAL full rows", err)
				}
			}
			got := []string{}
			for _, record := range result.Result.Records {
				got = append(got, mysql57Row(t, record.Values))
				for _, external := range record.External {
					if external.Format != "BLOB" || external.HeaderOffset != 38 {
						t.Fatal("5.7 requires legacy BLOB", external)
					}
					var stream []byte
					report, err := StreamLOB(context.Background(), reader, size, external, ScanOptions{}, func(block LOBBlock) error {
						stream = append(stream, block.Data...)
						return nil
					})
					if err != nil || !report.Complete || uint64(len(stream)) != report.Bytes {
						t.Fatal("LOB stream", err, report)
					}
					var want []byte
					switch v := record.Values[external.Column].(type) {
					case []byte:
						want = v
					case string:
						want = record.TextBytes[external.Column]
					case JSONValue:
						decoded, err := DecodeJSON(stream)
						if err != nil || !reflect.DeepEqual(decoded, v) {
							t.Fatal("streamed binary JSON", err)
						}
						want = stream
					default:
						t.Fatalf("unexpected external type %T", v)
					}
					if !bytes.Equal(stream, want) {
						t.Fatal("LOB stream differs from retained source bytes")
					}
				}
			}
			var expected []json.RawMessage
			if err = json.Unmarshal(unzip(t, mysql57Fixtures+"/"+c.Name+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			want := []string{}
			for _, row := range expected {
				want = append(want, mysql57Canonical(t, row))
			}
			if c.Unordered {
				sort.Strings(got)
				sort.Strings(want)
			}
			if len(got) != c.Rows || !reflect.DeepEqual(got, want) {
				t.Fatalf("independent SQL rows differ\ngot %v\nwant %v", got, want)
			}
			var scanned []string
			scan, err := ScanMaterialized(context.Background(), reader, size, schema, ScanOptions{}, func(event ScanEvent) error {
				if event.Record != nil {
					scanned = append(scanned, mysql57Row(t, event.Record.Values))
				}
				return nil
			})
			if c.Unordered {
				sort.Strings(scanned)
			}
			if err != nil || !scan.Complete || scan.Records != uint64(c.Rows) || !reflect.DeepEqual(scanned, want) && len(want) > 0 {
				t.Fatal("Scan/SQL comparison", err, scan)
			}
			if strings.HasPrefix(c.Name, "tree_") && (result.Result.Page.Level < 2 || len(result.Result.Nodes) == 0) {
				t.Fatal("fixture must exercise a three-level tree")
			}
		})
	}
}

func TestMySQL57Damage(t *testing.T) {
	b, schema := mysql57Fixture(t, "lob_compact_initial")
	result, err := Read(bytes.NewReader(b), int64(len(b)), schema)
	if err != nil {
		t.Fatal(err)
	}
	source := result.Records[0].External[0]
	for _, test := range []struct {
		name   string
		change func([]byte)
		want   error
	}{
		{"cycle", func(p []byte) { be.PutUint32(p[42:46], source.FirstPage) }, ErrCorrupt},
		{"empty-part", func(p []byte) { be.PutUint32(p[38:42], 0) }, ErrCorrupt},
		{"wrong-space", func(p []byte) { be.PutUint32(p[34:38], schema.SpaceID+1) }, ErrCorrupt},
		{"wrong-type", func(p []byte) { be.PutUint16(p[24:26], 11) }, ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := append([]byte{}, b...)
			page := copy[int(source.FirstPage)*PageSize : int(source.FirstPage+1)*PageSize]
			test.change(page)
			resealTestPages(copy) // Synthetic structural damage; original asset remains untouched.
			reader, size := bytes.NewReader(copy), int64(len(copy))
			if got, err := Read(reader, size, schema); got != nil || !errors.Is(err, test.want) {
				t.Fatal("atomic read", err)
			}
			report, err := StreamLOB(context.Background(), reader, size, source, ScanOptions{}, func(LOBBlock) error { return nil })
			if report.Complete || !errors.Is(err, test.want) {
				t.Fatal("LOB completion", err, report)
			}
		})
	}
	for _, change := range []func(*Schema){
		func(s *Schema) { s.SpaceID++ },
		func(s *Schema) { s.IndexID++ },
		func(s *Schema) { s.RootPage = uint32(len(b) / PageSize) },
	} {
		bad := schema
		change(&bad)
		if err := ValidateSchema(bytes.NewReader(b), int64(len(b)), bad); !errors.Is(err, ErrCorrupt) {
			t.Fatal("schema identity mismatch", err)
		}
	}
	damaged := append([]byte{}, b...)
	damaged[int(schema.RootPage)*PageSize+120] ^= 1
	if err := ValidateSchema(bytes.NewReader(damaged), int64(len(damaged)), schema); !errors.Is(err, ErrCorrupt) {
		t.Fatal("CRC damage without resealing", err)
	}
}
