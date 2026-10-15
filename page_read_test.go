package innodb

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

type localPageReader struct {
	data  []byte
	pages []uint32
}

func (r *localPageReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) != PageSize || off%PageSize != 0 {
		return 0, errors.New("expected one aligned page")
	}
	r.pages = append(r.pages, uint32(off/PageSize))
	return bytes.NewReader(r.data).ReadAt(p, off)
}

func TestReadLocalLesson(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	r := &localPageReader{data: b}
	p, err := ReadClusteredPage(r, int64(len(b)), 4, s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.pages, []uint32{0, 4}) || p.Page.IndexID != 249 || p.Page.Level != 0 || len(p.Nodes) != 0 || len(p.DeletedRecords) != 0 {
		t.Fatal("local scope", r.pages, p.Page)
	}
	var values [][]any
	var offsets []int
	for _, record := range p.Records {
		values = append(values, record.Values)
		offsets = append(offsets, record.Offset)
	}
	if !reflect.DeepEqual(offsets, []int{155, 222, 127, 189}) || p.Records[0].Start != 148 || p.Records[0].End != 182 {
		t.Fatal("independent documented byte positions", offsets)
	}
	want, err := os.ReadFile("testdata/mysql8045/lesson_rows.expected.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(values)
	if err != nil || !reflect.DeepEqual(jsonTextValue(t, got), jsonTextValue(t, want)) {
		t.Fatal("independent SQL values", string(got), err)
	}
	b[4*PageSize+176] ^= 1
	if p.Records[0].Values[2] != "InnoDB" || string(p.Records[0].TextBytes[2]) != "InnoDB" {
		t.Fatal("local text borrows input bytes")
	}
	b[4*PageSize+176] ^= 1
	// SDI is not a dependency of the explicit local reader. Do not reseal CRC.
	b[3*PageSize+200] ^= 1
	if _, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), 4, s); err != nil {
		t.Fatal("unrelated SDI damage", err)
	}
}

func TestReadPhysicalPage(t *testing.T) {
	b, _ := fixture(t, "lesson_rows")
	r := &localPageReader{data: b}
	p, err := ReadPage(r, int64(len(b)), 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.pages, []uint32{4}) || p.Header.Number != 4 || p.Header.SpaceID != 40 || p.Header.Type != 17855 || p.Header.Checksum != 0xbcf5b404 || !bytes.Equal(p.Raw, b[4*PageSize:5*PageSize]) {
		t.Fatal("independent header/CRC bytes", r.pages, p.Header)
	}
	p.Raw[200] ^= 1
	if bytes.Equal(p.Raw, b[4*PageSize:5*PageSize]) {
		t.Fatal("raw page aliases input")
	}
	// Raw access preserves damage; verified access rejects it.
	b[4*PageSize+200] ^= 1
	if raw, err := ReadRawPage(bytes.NewReader(b), int64(len(b)), 4); err != nil || !bytes.Equal(raw, b[4*PageSize:5*PageSize]) {
		t.Fatal("raw damage inspection", err)
	}
	if p, err := ReadPage(bytes.NewReader(b), int64(len(b)), 4); p != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("checksum damage accepted", err)
	}
	if raw, err := ReadRawPage(bytes.NewReader(b), int64(len(b)), 6); err != nil || bytes.Count(raw, []byte{0}) != PageSize {
		t.Fatal("raw zero page", err)
	}
	if p, err := ReadPage(bytes.NewReader(b), int64(len(b)), 6); p != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("zero page verified", err)
	}
}

type localReadResult struct {
	data []byte
	n    int
	err  error
}

func (r localReadResult) ReadAt(p []byte, _ int64) (int, error) {
	copy(p, r.data)
	return r.n, r.err
}

func TestReadLocalInputErrors(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	cause := errors.New("storage read failed")
	for _, c := range []struct {
		name   string
		reader io.ReaderAt
		size   int64
		page   uint32
		want   error
	}{
		{"nil", nil, int64(len(b)), 4, ErrUnsupported},
		{"short-file", bytes.NewReader(b), PageSize - 1, 0, ErrCorrupt},
		{"unaligned", bytes.NewReader(b), int64(len(b)) - 1, 0, ErrCorrupt},
		{"outside", bytes.NewReader(b), int64(len(b)), uint32(len(b) / PageSize), ErrCorrupt},
		{"short-read", localReadResult{n: PageSize - 1}, int64(len(b)), 4, io.ErrUnexpectedEOF},
		{"io", localReadResult{err: cause}, int64(len(b)), 4, cause},
	} {
		t.Run(c.name, func(t *testing.T) {
			if raw, err := ReadRawPage(c.reader, c.size, c.page); raw != nil || !errors.Is(err, c.want) {
				t.Fatal("raw", err)
			}
			if page, err := ReadPage(c.reader, c.size, c.page); page != nil || !errors.Is(err, c.want) {
				t.Fatal("verified", err)
			}
		})
	}
	fullEOF := localReadResult{data: b[4*PageSize : 5*PageSize], n: PageSize, err: io.EOF}
	if _, err := ReadPage(fullEOF, int64(len(b)), 4); err != nil {
		t.Fatal("ReaderAt full page with EOF", err)
	}
	if p, err := ReadClusteredPage(nil, int64(len(b)), 4, s); p != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("nil clustered reader", err)
	}
}

func TestReadLocalClusteredSQL(t *testing.T) {
	for _, c := range []struct {
		dir, name string
		auto      bool
	}{
		{"testdata/trees", "deep_rows", false},
		{"testdata/compact", "lesson_compact_initial", false},
		{"testdata/compact", "lesson_compact_null_deleted", false},
		{"testdata/compact", "tree_compact_instant", false},
		{"testdata/cluster", "rowid_lesson", false},
		{"testdata/cluster", "unique_single", false},
		{"testdata/cluster", "unique_text_tree", false},
		{"testdata/chars", "char_widths_1", false},
		{"testdata/generated", "mixed_instant", false},
		{"testdata/instant", "lesson_drop", false},
		{"testdata/instant", "hidden_drop", false},
		{"testdata/instant", "tree_drop", false},
		{"testdata/mysql5744", "scalar_dynamic_initial", false},
		{"testdata/mysql5744", "lob_compact_initial", false},
		{"testdata/mysql5744", "empty_dynamic_initial", false},
		{"testdata/mysql80-patches/8.0.28", "instant_compact_mixed", true},
	} {
		t.Run(c.dir+"/"+c.name, func(t *testing.T) {
			var b []byte
			var s Schema
			base := c.dir + "/" + c.name
			if c.auto {
				b = unzip(t, base+".ibd.gz")
				m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
				if err != nil || m.MaterializedSchema == nil {
					t.Fatal("metadata", err)
				}
				s = *m.MaterializedSchema
			} else {
				b, s = compactFixture(t, c.dir, c.name)
			}
			size := int64(len(b))
			full, err := ReadMaterialized(bytes.NewReader(b), size, s)
			if err != nil {
				t.Fatal(err)
			}
			actual, wanted := map[string]int{}, map[string]int{}
			var expected []json.RawMessage
			if err := json.Unmarshal(unzip(t, base+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			for _, row := range expected {
				if c.dir == "testdata/compact" {
					var values []any
					d := json.NewDecoder(bytes.NewReader(row))
					d.UseNumber()
					if err := d.Decode(&values); err != nil {
						t.Fatal(err)
					}
					wanted[compactCanonical(t, values, s)]++
				} else {
					wanted[mysql57Canonical(t, row)]++
				}
			}
			deleted, nodes := 0, 0
			for _, page := range full.Result.Pages {
				r := &localPageReader{data: b}
				local, err := ReadClusteredPage(r, size, page.Number, s)
				if err != nil || !reflect.DeepEqual(r.pages, []uint32{0, page.Number}) {
					t.Fatal("local I/O", r.pages, err)
				}
				if !reflect.DeepEqual(local.Page, page) {
					t.Fatal("page regression")
				}
				traced, err := TraceClusteredPage(bytes.NewReader(b), size, page.Number, s)
				if err != nil {
					t.Fatal(err)
				}
				checkClusteredSources(t, b, traced, s)
				traced.Sources, traced.VirtualColumns = nil, nil
				if !reflect.DeepEqual(traced, local) {
					t.Fatal("tracing changed local result")
				}
				local = traced // The independent SQL comparison below also covers tracing.
				deleted += len(local.DeletedRecords)
				nodes += len(local.Nodes)
				for _, record := range local.Records {
					values := append([]any{}, record.Values...)
					for _, ref := range record.External {
						value, ok := values[ref.Column].(ExternalField)
						if !ok || !reflect.DeepEqual(value, ref) || len(ref.Chunks) != 0 {
							t.Fatal("unresolved external value represented as NULL")
						}
						off := int(page.Number)*PageSize + ref.Offset
						if !bytes.Equal(ref.Reference[:], b[off:off+20]) {
							t.Fatal("reference source bytes")
						}
						if s.RowFormat == "COMPACT" && len(ref.Prefix) != 768 {
							t.Fatal("local COMPACT prefix")
						}
						var raw []byte
						report, err := StreamLOB(t.Context(), bytes.NewReader(b), size, ref, ScanOptions{}, func(block LOBBlock) error {
							raw = append(raw, block.Data...)
							return nil
						})
						if err != nil || !report.Complete {
							t.Fatal("public reference consumption", err)
						}
						values[ref.Column], err = variableValue(s.Columns[ref.Column], raw)
						if err != nil {
							t.Fatal(err)
						}
						if s.Columns[ref.Column].Type == "CHAR" {
							values[ref.Column] = strings.TrimRight(values[ref.Column].(string), " ")
						}
					}
					if c.dir == "testdata/compact" {
						actual[compactCanonical(t, values, s)]++
						continue
					}
					actual[mysql57Row(t, values)]++
				}
			}
			if !reflect.DeepEqual(actual, wanted) || deleted != len(full.Result.DeletedRecords) || nodes != len(full.Result.Nodes) {
				t.Fatalf("independent SQL values or structural regression: rows %d/%d, deleted %d/%d, nodes %d/%d", len(actual), len(wanted), deleted, len(full.Result.DeletedRecords), nodes, len(full.Result.Nodes))
			}
		})
	}
}

func TestReadLocalSecondarySQL(t *testing.T) {
	for _, name := range []string{"tiny", "empty", "deep", "compact", "rowid", "unique_cluster", "changes", "overlap"} {
		t.Run(name, func(t *testing.T) {
			base := "testdata/secondary/" + name
			b := unzip(t, base+".ibd.gz")
			size := int64(len(b))
			var expected []struct {
				Name string
				Rows [][]any
			}
			d := json.NewDecoder(bytes.NewReader(unzip(t, base+".secondary.json.gz")))
			d.UseNumber()
			if err := d.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			for _, index := range expected {
				full, err := ReadSecondaryAuto(bytes.NewReader(b), size, index.Name)
				if err != nil {
					t.Fatal(err)
				}
				var records, deleted []SecondaryRecord
				var nodes []SecondaryNode
				for _, page := range full.Pages {
					r := &localPageReader{data: b}
					local, err := ReadSecondaryPage(r, size, page.Number, full.Schema)
					if err != nil || !reflect.DeepEqual(r.pages, []uint32{0, page.Number}) {
						t.Fatal("local I/O", r.pages, err)
					}
					traced, err := TraceSecondaryPage(bytes.NewReader(b), size, page.Number, full.Schema)
					if err != nil {
						t.Fatal(err)
					}
					checkSecondarySources(t, b, traced, full.Schema)
					traced.Sources = nil
					if !reflect.DeepEqual(traced, local) {
						t.Fatal("tracing changed secondary result")
					}
					local = traced
					records = append(records, local.Records...)
					deleted = append(deleted, local.DeletedRecords...)
					nodes = append(nodes, local.Nodes...)
				}
				if len(records) != len(index.Rows) || !reflect.DeepEqual(deleted, full.DeletedRecords) || !reflect.DeepEqual(nodes, full.Nodes) {
					t.Fatal("rows/deleted/nodes", index.Name)
				}
				for i, record := range records {
					values := secondarySQLValues(record.Values)
					if record.RowID != nil {
						// SQL cannot expose DB_ROW_ID; full physical fields are compared separately.
						values = values[:full.Schema.UserFields]
					}
					if !reflect.DeepEqual(record, full.Records[i]) || secondaryJSON(values) != secondaryJSON(index.Rows[i]) {
						t.Fatal("independent SQL projection or record regression", index.Name, i)
					}
				}
			}
		})
	}
}

func TestReadLocalDamage(t *testing.T) {
	b, s := fixture(t, "lesson_rows")
	for _, c := range []struct {
		name   string
		mutate func([]byte)
		reseal bool
		want   error
	}{
		{"checksum", func(x []byte) { x[4*PageSize+200] ^= 1 }, false, ErrCorrupt},
		{"lsn", func(x []byte) { x[5*PageSize-1] ^= 1 }, false, ErrCorrupt},
		{"page-zero-checksum", func(x []byte) { x[200] ^= 1 }, false, ErrCorrupt},
		{"number", func(x []byte) { be.PutUint32(x[4*PageSize+4:], 99) }, true, ErrCorrupt},
		{"space", func(x []byte) { be.PutUint32(x[4*PageSize+34:], 99) }, true, ErrCorrupt},
		{"index", func(x []byte) { be.PutUint64(x[4*PageSize+66:], 99) }, true, ErrCorrupt},
		{"unknown-type", func(x []byte) { be.PutUint16(x[4*PageSize+24:], 65535) }, true, ErrUnsupported},
		{"directory", func(x []byte) { x[5*PageSize-10] ^= 1 }, true, ErrCorrupt},
		{"record-chain", func(x []byte) { be.PutUint16(x[4*PageSize+153:], 0) }, true, ErrCorrupt},
		{"level", func(x []byte) { be.PutUint16(x[4*PageSize+64:], 99) }, true, ErrCorrupt},
		{"encrypted-flags", func(x []byte) { be.PutUint32(x[54:], 0x2021) }, true, ErrUnsupported},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := append([]byte{}, b...)
			c.mutate(x)
			if c.reseal {
				// Synthetic envelope for structural checks, never a repaired fixture.
				resealTestPages(x)
			}
			if p, err := ReadClusteredPage(bytes.NewReader(x), int64(len(x)), 4, s); p != nil || !errors.Is(err, c.want) {
				t.Fatal(err)
			}
			if c.name == "unknown-type" {
				p, err := ReadPage(bytes.NewReader(x), int64(len(x)), 4)
				if err != nil || p.Header.Type != 65535 {
					t.Fatal("unknown type common header", err)
				}
			}
		})
	}
	r := &localPageReader{data: b}
	bad := s
	bad.RootPage = 0
	if p, err := ReadClusteredPage(r, int64(len(b)), 4, bad); p != nil || !errors.Is(err, ErrUnsupported) || len(r.pages) != 0 {
		t.Fatal("schema validation before I/O", err)
	}
	b, index := secondaryFixture(t, "tiny", "n_idx")
	if p, err := ReadSecondaryPage(nil, int64(len(b)), index.RootPage, index); p != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("nil secondary reader", err)
	}
	index.Unique = true
	if p, err := ReadSecondaryPage(bytes.NewReader(b), int64(len(b)), index.RootPage, index); p != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("local unique violation", err)
	}
	index.Unique = false
	b[int(index.RootPage)*PageSize+200] ^= 1
	if p, err := ReadSecondaryPage(bytes.NewReader(b), int64(len(b)), index.RootPage, index); p != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("secondary checksum", err)
	}
}

func TestReadLocalChildAndScope(t *testing.T) {
	b, s := treeFixture(t, "deep_rows")
	full, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	root, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
	if err != nil || len(root.Nodes) == 0 || len(root.Records) != 0 {
		t.Fatal("internal clustered page", err)
	}
	for _, child := range []uint32{0, s.RootPage, uint32(len(b) / PageSize)} {
		x := append([]byte{}, b...)
		n := root.Nodes[0]
		be.PutUint32(x[int(s.RootPage)*PageSize+n.End-4:], child)
		resealTestPages(x) // Synthetic structural damage.
		if p, err := ReadClusteredPage(bytes.NewReader(x), int64(len(x)), s.RootPage, s); p != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("invalid clustered child", child, err)
		}
	}
	leaf := full.Records[0].PageNumber
	for page := 1; page < len(b)/PageSize; page++ {
		if uint32(page) != leaf {
			b[page*PageSize+200] ^= 1 // No resealing: root/SDI/other leaves are corrupt.
		}
	}
	r := &localPageReader{data: b}
	local, err := ReadClusteredPage(r, int64(len(b)), leaf, s)
	if err != nil || len(local.Records) == 0 || !reflect.DeepEqual(r.pages, []uint32{0, leaf}) {
		t.Fatal("local leaf depends on unrelated pages", err, r.pages)
	}
	if p, err := Read(bytes.NewReader(b), int64(len(b)), s); p != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("full reader missed root damage", err)
	}
	b, index := secondaryFixture(t, "deep", "b_idx")
	whole, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), index)
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range []uint32{0, index.RootPage, uint32(len(b) / PageSize)} {
		x := append([]byte{}, b...)
		n := whole.Nodes[0]
		be.PutUint32(x[int(index.RootPage)*PageSize+n.End-4:], child)
		resealTestPages(x)
		if p, err := ReadSecondaryPage(bytes.NewReader(x), int64(len(x)), index.RootPage, index); p != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("invalid secondary child", child, err)
		}
	}
	leaf = whole.Records[0].PageNumber
	b[int(index.RootPage)*PageSize+200] ^= 1
	r = &localPageReader{data: b}
	if p, err := ReadSecondaryPage(r, int64(len(b)), leaf, index); err != nil || len(p.Records) == 0 || !reflect.DeepEqual(r.pages, []uint32{0, leaf}) {
		t.Fatal("secondary leaf depends on root", err)
	}
}

func TestReadLocalUnresolvedLOB(t *testing.T) {
	for _, name := range []string{"lesson_compact_initial", "lesson_dynamic_initial"} {
		b, s := compactFixture(t, "testdata/compact", name)
		p, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), s.RootPage, s)
		if err != nil {
			t.Fatal(err)
		}
		var ref *ExternalField
		for _, record := range p.Records {
			if len(record.External) > 0 {
				v := record.Values[record.External[0].Column].(ExternalField)
				ref = &v
				break
			}
		}
		if ref == nil {
			t.Fatal("missing external fixture")
		}
		b[int(ref.FirstPage)*PageSize+200] ^= 1 // Preserve bad checksum.
		r := &localPageReader{data: b}
		if _, err := ReadClusteredPage(r, int64(len(b)), s.RootPage, s); err != nil || !reflect.DeepEqual(r.pages, []uint32{0, s.RootPage}) {
			t.Fatal("unresolved LOB was read", err)
		}
		if p, err := Read(bytes.NewReader(b), int64(len(b)), s); p != nil || !errors.Is(err, ErrCorrupt) {
			t.Fatal("complete read missed damaged LOB", err)
		}
	}
	b := unzip(t, "testdata/secondary_query/huge.ibd.gz")
	m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
	if err != nil || m.MaterializedSchema == nil {
		t.Fatal("huge metadata", err)
	}
	s := *m.MaterializedSchema
	r := &localPageReader{data: b}
	p, err := ReadClusteredPage(r, int64(len(b)), s.RootPage, s)
	if err != nil || len(p.Records) != 2 || !reflect.DeepEqual(r.pages, []uint32{0, s.RootPage}) {
		t.Fatal("huge value should remain a local reference", err)
	}
	found := false
	for _, record := range p.Records {
		for _, field := range record.External {
			found = found || field.Length == MaxLOBValueBytes+1
		}
	}
	if !found {
		t.Fatal("independent huge fixture length missing")
	}
}

func TestReadLocalSecondary57SQL(t *testing.T) {
	for _, name := range []string{"ordinary_dynamic", "ordinary_compact", "hidden_compact"} {
		t.Run(name, func(t *testing.T) {
			b, _, indexes := mysql57SecondaryFixture(t, name)
			var sql []struct {
				Index   string
				Entries []json.RawMessage
			}
			if err := json.Unmarshal(unzip(t, mysql57SecondaryFixtures+"/"+name+".secondary.json.gz"), &sql); err != nil {
				t.Fatal(err)
			}
			for i, index := range indexes {
				full, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), index)
				if err != nil {
					t.Fatal(err)
				}
				got := []string{}
				for _, page := range full.Pages {
					r := &localPageReader{data: b}
					p, err := ReadSecondaryPage(r, int64(len(b)), page.Number, index)
					if err != nil || !reflect.DeepEqual(r.pages, []uint32{0, page.Number}) {
						t.Fatal("5.7 local scope", err)
					}
					traced, err := TraceSecondaryPage(bytes.NewReader(b), int64(len(b)), page.Number, index)
					if err != nil {
						t.Fatal(err)
					}
					checkSecondarySources(t, b, traced, index)
					traced.Sources = nil
					if !reflect.DeepEqual(traced, p) {
						t.Fatal("tracing changed 5.7 secondary result")
					}
					p = traced
					for _, record := range p.Records {
						values := []any{}
						for j, value := range record.Values {
							if !index.Fields[j].RowID {
								values = append(values, value)
							}
						}
						got = append(got, mysql57Row(t, values))
					}
				}
				if sql[i].Index != index.Name {
					t.Fatal("independent index identity")
				}
				mysql57EqualRows(t, got, sql[i].Entries, false)
			}
		})
	}
}

func FuzzReadLocalPage(f *testing.F) {
	b, s := fixture(f, "lesson_rows")
	secondary, index := secondaryFixture(f, "tiny", "n_idx")
	f.Add(false, uint16(40), byte(255))
	f.Add(false, uint16(153), byte(1))
	f.Add(true, uint16(38), byte(255))
	f.Fuzz(func(t *testing.T, useSecondary bool, offset uint16, value byte) {
		data, page := b, s.RootPage
		if useSecondary {
			data, page = secondary, index.RootPage
		}
		x := append([]byte{}, data...)
		x[int(page)*PageSize+int(offset)%PageSize] ^= value
		resealTestPages(x) // Fuzz local structure with a valid synthetic envelope.
		if useSecondary {
			if p, err := ReadSecondaryPage(bytes.NewReader(x), int64(len(x)), page, index); err != nil && p != nil {
				t.Fatal("partial secondary page")
			}
		} else if p, err := ReadClusteredPage(bytes.NewReader(x), int64(len(x)), page, s); err != nil && p != nil {
			t.Fatal("partial clustered page")
		}
	})
}
