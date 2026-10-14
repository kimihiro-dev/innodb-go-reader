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
	"strings"
	"testing"
)

func TestMySQL80PatchFixtures(t *testing.T) {
	versions, err := filepath.Glob("testdata/mysql80-patches/*/manifest.json")
	if err != nil || len(versions) != 7 {
		t.Fatal("missing patch captures", err)
	}
	rows, fixtures, indexes := 0, 0, 0
	for _, path := range versions {
		dir := filepath.Dir(path)
		version := filepath.Base(dir)
		var manifest struct {
			Status      string
			Environment struct {
				Version  string
				PageSize int `json:"page_size"`
				Checksum string
			}
			Cases []struct {
				Name, SHA256       string
				Rows               int
				Unordered, Virtual bool
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Status != "captured" || strings.Split(manifest.Environment.Version, "-")[0] != version || manifest.Environment.PageSize != PageSize || manifest.Environment.Checksum != "strict_crc32" {
			t.Fatal("independent capture identity", path, err)
		}
		for _, c := range manifest.Cases {
			t.Run(version+"/"+c.Name, func(t *testing.T) {
				b := unzip(t, filepath.Join(dir, c.Name+".ibd.gz"))
				if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
					t.Fatal("snapshot SHA256")
				}
				r, size := bytes.NewReader(b), int64(len(b))
				m, err := InspectTable(r, size)
				if err != nil {
					t.Fatal(err)
				}
				if m.MaterializedSchema == nil {
					t.Fatal("metadata unsupported", m.Issues)
				}
				s := *m.MaterializedSchema
				// Compare raw SDI objects with the matching server's official ibd2sdi.
				var official []json.RawMessage
				if err := json.Unmarshal(unzip(t, filepath.Join(dir, c.Name+".sdi.json.gz")), &official); err != nil {
					t.Fatal(err)
				}
				sdi, err := ReadSDI(r, size)
				if err != nil {
					t.Fatal(err)
				}
				for _, rec := range sdi.Records {
					found := false
					for _, raw := range official {
						var obj struct {
							Object json.RawMessage `json:"object"`
						}
						_ = json.Unmarshal(raw, &obj)
						if obj.Object != nil && mysql57Canonical(t, obj.Object) == mysql57Canonical(t, rec.JSON) {
							found = true
						}
					}
					if !found {
						t.Fatal("official SDI mismatch")
					}
				}
				result, err := ReadMaterializedAuto(r, size)
				if err != nil {
					t.Fatal(err)
				}
				var expected []json.RawMessage
				if err := json.Unmarshal(unzip(t, filepath.Join(dir, c.Name+".expected.json.gz")), &expected); err != nil {
					t.Fatal(err)
				}
				got := []string{}
				for _, rec := range result.Result.Records {
					got = append(got, mysql57Row(t, rec.Values))
					for _, ext := range rec.External {
						var data []byte
						report, err := StreamLOB(t.Context(), r, size, ext, ScanOptions{}, func(block LOBBlock) error { data = append(data, block.Data...); return nil })
						if err != nil || !report.Complete || report.Bytes != uint64(len(data)) {
							t.Fatal("stream LOB", err)
						}
					}
				}
				mysql57EqualRows(t, got, expected, c.Unordered)
				if len(got) != c.Rows {
					t.Fatal("row count")
				}
				got = nil
				scan, err := ScanMaterializedAuto(t.Context(), r, size, ScanOptions{}, func(e ScanEvent) error {
					if e.Record != nil {
						got = append(got, mysql57Row(t, e.Record.Values))
					}
					return nil
				})
				if err != nil || !scan.Complete {
					t.Fatal("auto scan", err)
				}
				mysql57EqualRows(t, got, expected, c.Unordered)
				if strings.HasPrefix(c.Name, "tree_") && result.Result.Page.Level == 0 {
					t.Fatal("multi-level tree required")
				}
				if !s.hiddenRowID() {
					got = nil
					_, err = QueryMaterialized(t.Context(), r, size, s, KeyRange{}, ScanOptions{}, func(e ScanEvent) error {
						if e.Record != nil {
							got = append(got, mysql57Row(t, e.Record.Values))
						}
						return nil
					})
					if err != nil {
						t.Fatal("cluster query", err)
					}
					mysql57EqualRows(t, got, expected, false)
					if len(result.Result.Records) > 0 {
						got = nil
						_, err = QueryMaterializedAuto(t.Context(), r, size, PointKey(recordQueryKey(result.Result.Records[0], s)), ScanOptions{}, func(e ScanEvent) error {
							if e.Record != nil {
								got = append(got, mysql57Row(t, e.Record.Values))
							}
							return nil
						})
						if err != nil {
							t.Fatal("auto point query", err)
						}
						mysql57EqualRows(t, got, expected[:1], false)
					}
				}
				var sec []struct {
					Index         string
					Entries, Rows []json.RawMessage
				}
				if err := json.Unmarshal(unzip(t, filepath.Join(dir, c.Name+".secondary.json.gz")), &sec); err != nil {
					t.Fatal(err)
				}
				for _, e := range sec {
					idx, err := InspectSecondary(r, size, e.Index)
					if err != nil {
						t.Fatal(err)
					}
					got = nil
					_, err = ScanSecondary(context.Background(), r, size, *idx, ScanOptions{}, func(e SecondaryEvent) error {
						if e.Record != nil {
							got = append(got, mysql57Row(t, e.Record.Values))
						}
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
					mysql57EqualRows(t, got, e.Entries, false)
					got = nil
					_, err = QuerySecondaryMaterialized(t.Context(), r, size, s, *idx, SecondaryQuery{}, ScanOptions{}, func(e ProjectedRow) error { got = append(got, mysql57Row(t, e.Values)); return nil })
					if err != nil {
						t.Fatal(err)
					}
					mysql57EqualRows(t, got, e.Rows, false)
					var projected []json.RawMessage
					for _, raw := range e.Rows {
						var cells []json.RawMessage
						if err := json.Unmarshal(raw, &cells); err != nil {
							t.Fatal(err)
						}
						row, _ := json.Marshal([]json.RawMessage{cells[1], cells[0]})
						projected = append(projected, row)
					}
					got = nil
					report, err := QuerySecondaryMaterializedAuto(t.Context(), r, size, e.Index, SecondaryQuery{Columns: []string{"n", "id"}}, ScanOptions{}, func(e ProjectedRow) error { got = append(got, mysql57Row(t, e.Values)); return nil })
					if err != nil || !report.Complete || report.Lookups != 0 {
						t.Fatal("covered query", err, report)
					}
					mysql57EqualRows(t, got, projected, false)
					indexes++
				}
				rows += c.Rows
				fixtures++
			})
		}
	}
	if fixtures != 217 || rows != 13581 || indexes != 28 {
		t.Fatal("incomplete patch coverage", fixtures, rows, indexes)
	}
	t.Logf("SQL patch coverage: %d fixtures, %d rows, %d secondary indexes", fixtures, rows, indexes)
}

func TestLegacyInstantDamage(t *testing.T) {
	b := unzip(t, "testdata/mysql80-patches/8.0.28/instant_dynamic_mixed.ibd.gz")
	r, size := bytes.NewReader(b), int64(len(b))
	m, err := InspectTable(r, size)
	if err != nil || m.Schema == nil || m.Schema.LegacyInstant == nil {
		t.Fatal(err)
	}
	s := *m.Schema
	result, err := Read(r, size, s)
	if err != nil {
		t.Fatal(err)
	}
	var origin int
	for _, rec := range result.Records {
		if rec.Header[0]&0x80 != 0 {
			origin = int(rec.PageNumber)*PageSize + rec.Offset
			break
		}
	}
	if origin == 0 {
		t.Fatal("missing count-bearing record")
	}
	first := result.Records[0]
	bytesWithDefaults := uint64(first.End - first.Start)
	for _, column := range first.DefaultColumns {
		bytesWithDefaults += uint64(len(s.LegacyInstant.Defaults[column-s.LegacyInstant.Columns].Data))
	}
	if len(first.DefaultColumns) == 0 {
		t.Fatal("expected legacy synthesized values")
	}
	delivered := 0
	scan, err := Scan(t.Context(), r, size, s, ScanOptions{MaxRowBytes: bytesWithDefaults - 1}, func(e ScanEvent) error {
		if e.Record != nil {
			delivered++
		}
		return nil
	})
	if !errors.Is(err, ErrLimit) || scan.Complete || delivered != 0 {
		t.Fatal("legacy default byte budget", err, scan)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if report, err := ScanMaterializedAuto(ctx, r, size, ScanOptions{}, func(ScanEvent) error { return nil }); !errors.Is(err, context.Canceled) || report.Complete {
		t.Fatal("cancel", err)
	}
	for _, c := range []struct {
		name   string
		mutate func([]byte)
		want   error
	}{
		{"count-small", func(b []byte) { b[origin-6] = 1 }, ErrCorrupt},
		{"count-large", func(b []byte) { b[origin-6] = 127 }, ErrCorrupt},
		{"two-byte-noncanonical", func(b []byte) { b[origin-6] = 0x80; b[origin-7] = 5 }, ErrCorrupt},
		{"mixed-flags", func(b []byte) { b[origin-5] |= 0x40 }, ErrUnsupported},
		{"min-flag", func(b []byte) { b[origin-5] |= 0x10 }, ErrUnsupported},
	} {
		t.Run(c.name, func(t *testing.T) {
			bad := append([]byte(nil), b...)
			c.mutate(bad)
			resealTestPages(bad)
			got, err := Read(bytes.NewReader(bad), size, s)
			if got != nil || !errors.Is(err, c.want) {
				t.Fatal(err)
			}
		})
	}
	for _, c := range []struct {
		name   string
		mutate func(*LegacyInstantLayout)
	}{
		{"original-count", func(x *LegacyInstantLayout) { x.Columns = len(s.Columns) }},
		{"default-count", func(x *LegacyInstantLayout) { x.Defaults = nil }},
		{"null-not-null", func(x *LegacyInstantLayout) { x.Defaults[0] = InstantDefault{Null: true} }},
		{"default-width", func(x *LegacyInstantLayout) { x.Defaults[0] = InstantDefault{Data: []byte{1}} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			copy := *s.LegacyInstant
			copy.Defaults = append([]InstantDefault(nil), copy.Defaults...)
			c.mutate(&copy)
			bad := s
			bad.LegacyInstant = &copy
			if err := ValidateSchema(r, size, bad); !errors.Is(err, ErrUnsupported) {
				t.Fatal(err)
			}
		})
	}
}

func TestMySQL8011ZeroLOBEntryBoundary(t *testing.T) {
	b := unzip(t, "testdata/mysql80-patches/8.0.11/lob_dynamic_insert.ibd.gz")
	r, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
	if err != nil || len(r.Records[0].External) == 0 {
		t.Fatal(err)
	}
	f := r.Records[0].External[0]
	first := int(f.FirstPage) * PageSize
	page, offset := be.Uint32(b[first+68:]), be.Uint16(b[first+72:])
	if be.Uint32(b[int(page)*PageSize+int(offset)+56:]) != 0 {
		t.Fatal("expected original 8.0.11 zero entry")
	}
	be.PutUint32(b[8:], 80012)
	resealTestPages(b)
	if got, err := ReadAuto(bytes.NewReader(b), int64(len(b))); got != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("zero entry accepted for different writer", err)
	}
}

func TestPatchSDIVersions(t *testing.T) {
	for _, e := range []ddEnvelope{{MySQL: 80011, DD: 80011, Version: 1}, {MySQL: 80012, DD: 80012, Version: 1}, {MySQL: 80016, DD: 80016, Version: 80016}, {MySQL: 80019, DD: 80017, Version: 80019}, {MySQL: 80023, DD: 80023, Version: 80019}, {MySQL: 80028, DD: 80023, Version: 80019}, {MySQL: 80030, DD: 80023, Version: 80019}, {MySQL: 80045, DD: 80023, Version: 80019}} {
		if !supportedSDIVersion(e) {
			t.Fatal(e)
		}
		e.DD++
		if supportedSDIVersion(e) {
			t.Fatal("invalid tuple accepted")
		}
	}
	for _, v := range []uint32{80013, 80017, 80029, 80031, 80044, 80400} {
		if supportedSDIVersion(ddEnvelope{MySQL: v, DD: 80023, Version: 80019}) {
			t.Fatal("untested patch", v)
		}
	}
}

func TestPatchMetadataBoundaries(t *testing.T) {
	for _, c := range []struct {
		name, version, fixture, kind string
		mutate                       func(map[string]any)
	}{
		{"empty-table-encryption", "8.0.11", "scalar_dynamic_current", "Table", func(o map[string]any) { o["options"] = o["options"].(string) + "encrypt_type=;" }},
		{"empty-space-encryption", "8.0.11", "scalar_dynamic_current", "Tablespace", func(o map[string]any) { o["options"] = "encryption=;" }},
		{"empty-state", "8.0.11", "scalar_dynamic_current", "Tablespace", func(o map[string]any) { o["se_private_data"] = o["se_private_data"].(string) + "state=;" }},
		{"legacy-system-modern-position", "8.0.28", "instant_dynamic_mixed", "Table", func(o map[string]any) {
			cols := o["columns"].([]any)
			last := cols[len(cols)-1].(map[string]any)
			last["se_private_data"] = last["se_private_data"].(string) + "physical_pos=2;"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := unzip(t, filepath.Join("testdata/mysql80-patches", c.version, c.fixture+".ibd.gz"))
			r := bytes.NewReader(b)
			sdi, err := ReadSDI(r, int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			for i := range sdi.Records {
				var e map[string]any
				if err := json.Unmarshal(sdi.Records[i].JSON, &e); err != nil {
					t.Fatal(err)
				}
				if e["dd_object_type"] == c.kind {
					c.mutate(e["dd_object"].(map[string]any))
					raw, err := json.Marshal(e)
					if err != nil {
						t.Fatal(err)
					}
					sdi.Records[i].JSON = raw
				}
			}
			m, err := inspectSDITable(r, int64(len(b)), sdi)
			if err != nil || m.MaterializedSchema != nil || len(m.Issues) == 0 {
				t.Fatal("unsupported metadata accepted", err, m)
			}
		})
	}
}

func TestSDIDeletedObject(t *testing.T) {
	b, _ := fixture(t, "lesson_rows")
	r, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	rec := r.Records[0]
	base := int(rec.PageNumber) * PageSize
	b[base+rec.Offset-5] |= 0x20
	resealTestPages(b)
	r, err = ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil || len(r.DeletedRecords) != 1 || len(r.Records) != 1 {
		t.Fatal(err)
	}
	if !bytes.Equal(r.DeletedRecords[0].JSON, rec.JSON) {
		t.Fatal("deleted payload lost")
	}
	if result, err := ReadAuto(bytes.NewReader(b), int64(len(b))); result != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("deleted table discovery", err)
	}
	b[base+rec.Offset+33] = 0
	resealTestPages(b)
	if r, err := ReadSDI(bytes.NewReader(b), int64(len(b))); r != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("deleted payload corruption skipped", err)
	}
}
