package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func clusterFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, "testdata/cluster/"+name+".ibd.gz")
	raw, err := os.ReadFile("testdata/cluster/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}
func TestClusterFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/cluster/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
			Unordered    bool
		}
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := clusterFixture(t, c.Name)
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/cluster/"+c.Name+".sdi.json.gz"), &official); err != nil {
				t.Fatal(err)
			}
			if len(official) != len(sdi.Records)+1 {
				t.Fatal("SDI count")
			}
			for i, r := range sdi.Records {
				var w struct {
					Type   uint32
					ID     uint64
					Object json.RawMessage
				}
				if err = json.Unmarshal(official[i+1], &w); err != nil {
					t.Fatal(err)
				}
				if r.Key != (SDIKey{w.Type, w.ID}) || !reflect.DeepEqual(jsonTextValue(t, r.JSON), jsonTextValue(t, w.Object)) {
					t.Fatal("official SDI")
				}
			}

			meta, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if meta.Schema == nil || !reflect.DeepEqual(*meta.Schema, s) {
				t.Fatalf("schema %+v issues %+v", meta.Schema, meta.Issues)
			}
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			manual, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil || !reflect.DeepEqual(got, manual) {
				t.Fatal("manual/auto", err)
			}
			var expected []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/cluster/"+c.Name+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != c.Rows || len(expected) != c.Rows {
				t.Fatal("rows")
			}
			_, err = s.validate()
			if err != nil {
				t.Fatal(err)
			}

			wantRows, actualRows := map[string]int{}, map[string]int{}
			for _, raw := range expected {
				canonical, _ := json.Marshal(jsonTextValue(t, raw))
				wantRows[string(canonical)]++
			}
			var previous uint64
			for i, r := range got.Records {
				if s.hiddenRowID() {
					if r.RowID == nil || i > 0 && *r.RowID <= previous {
						t.Fatal("ROW_ID identity/order")
					}
					previous = *r.RowID
					offset := int(r.PageNumber)*PageSize + r.Offset
					if fmt.Sprintf("%012x", *r.RowID) != fmt.Sprintf("%x", b[offset:offset+6]) {
						t.Fatal("six-byte value")
					}
					if !bytes.Equal(b[offset+6:offset+12], r.Transaction[:]) || !bytes.Equal(b[offset+12:offset+19], r.RollPointer[:]) {
						t.Fatal("system offsets")
					}
				} else if r.RowID != nil {
					t.Fatal("unexpected ROW_ID")
				}
				if len(r.Values) != len(s.Columns) {
					t.Fatal("system column leaked")
				}
				values := append([]any{}, r.Values...)
				for j, c := range s.Columns {
					if c.isBinary() && values[j] != nil {
						values[j] = strings.ToUpper(fmt.Sprintf("%x", values[j].([]byte)))
					}
				}
				encoded, _ := json.Marshal(values)
				canonical, _ := json.Marshal(jsonTextValue(t, encoded))
				actualRows[string(canonical)]++
				if !c.Unordered && !reflect.DeepEqual(jsonTextValue(t, encoded), jsonTextValue(t, expected[i])) {
					t.Fatalf("row %d: %s != %s", i, encoded, expected[i])
				}
				total++
			}
			if c.Unordered && !reflect.DeepEqual(wantRows, actualRows) {
				t.Fatal("SQL multiset")
			}
			for _, node := range got.Nodes {
				if s.hiddenRowID() {
					value, ok := node.Key.(uint64)
					if !ok || node.End-node.Offset != 10 {
						t.Fatal("node layout")
					}
					start := int(node.PageNumber)*PageSize + node.Offset
					if fmt.Sprintf("%012x", value) != fmt.Sprintf("%x", b[start:start+6]) || be.Uint32(b[start+6:start+10]) != node.ChildPage {
						t.Fatal("node bytes")
					}
				}
			}
			var indexes []struct {
				Name    string
				IndexID uint64 `json:"index_id"`
				Root    uint32 `json:"root_page"`
				Space   uint32 `json:"space_id"`
				Type    uint32
			}
			if err = json.Unmarshal(unzip(t, "testdata/cluster/"+c.Name+".indexes.json.gz"), &indexes); err != nil {
				t.Fatal(err)
			}
			if len(indexes) != len(meta.Indexes) {
				t.Fatal("SQL index count")
			}
			for _, want := range indexes {
				found := false
				for _, idx := range meta.Indexes {
					if idx.ID == want.IndexID {
						found = true
						name := idx.Name
						if idx.Hidden {
							name = "GEN_CLUST_INDEX"
						}
						if name != want.Name || idx.RootPage != want.Root || idx.SpaceID != want.Space || !idx.RootVerified || idx.Clustered != (want.Type&1 != 0) {
							t.Fatal("SQL index identity", idx, want)
						}
					}
				}
				if !found {
					t.Fatal("missing SQL index")
				}
			}
			if strings.HasSuffix(c.Name, "_tree") && got.Page.Level < 1 {
				t.Fatal("missing non-leaf coverage")
			}
			if c.Name == "rowid_deep" && got.Page.Level < 2 {
				t.Fatalf("expected three levels, got %d", got.Page.Level)
			}
		})
	}
	t.Logf("%d real key rows", total)
}

func TestClusterContracts(t *testing.T) {
	_, base := clusterFixture(t, "rowid_lesson")
	for _, change := range []func(*Schema){
		func(s *Schema) { s.PrimaryKey = "n" }, func(s *Schema) { s.PrimaryKeys = []string{"n"} },
		func(s *Schema) { s.ClusteredKey = nil }, func(s *Schema) { s.ClusteredKey.Name = "" },
		func(s *Schema) { s.ClusteredKey.Columns = []string{"n"} }, func(s *Schema) { s.ClusteredKey.HiddenRowID = false },
		func(s *Schema) { s.Columns[0].Descending = true }, func(s *Schema) { s.Columns[1].Collation = "utf8mb4_bin" },
	} {
		s := base
		c := *base.ClusteredKey
		s.ClusteredKey = &c
		s.Columns = append([]Column{}, base.Columns...)
		change(&s)
		if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal("accepted schema", s, err)
		}
	}
	b, s := clusterFixture(t, "rowid_lesson")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic legal 48-bit endpoints, not values claimed from the SQL instance.
	values := []uint64{0, 1, 1 << 47, (1 << 48) - 1}
	for i, row := range r.Records {
		pos := int(row.PageNumber)*PageSize + row.Offset
		for j := 0; j < 6; j++ {
			b[pos+j] = byte(values[i] >> uint(8*(5-j)))
		}
	}
	resealTestPages(b)
	r, err = Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range r.Records {
		if *row.RowID != values[i] {
			t.Fatal("48-bit boundary")
		}
	}
}
func TestClusterDamage(t *testing.T) {
	for _, kind := range []string{"duplicate", "reverse", "node-range", "child", "bitmap", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			name := "rowid_lesson"
			if kind == "node-range" || kind == "child" || kind == "bitmap" {
				name = "rowid_deep"
			}
			b, s := clusterFixture(t, name)
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			first := int(r.Records[0].PageNumber)*PageSize + r.Records[0].Offset
			switch kind {
			case "duplicate":
				next := int(r.Records[1].PageNumber)*PageSize + r.Records[1].Offset
				copy(b[next:next+6], b[first:first+6])
			case "reverse":
				for i := 0; i < 6; i++ {
					b[first+i] = 0xff
				}
			case "node-range":
				for _, n := range r.Nodes {
					if !n.Minimum {
						start := int(n.PageNumber)*PageSize + n.Offset
						for i := 0; i < 6; i++ {
							b[start+i] = 0xff
						}
						break
					}
				}
			case "child":
				n := r.Nodes[0]
				be.PutUint32(b[int(n.PageNumber)*PageSize+n.End-4:], 0)
			case "bitmap":
				n := r.Nodes[0]
				b[int(n.PageNumber)*PageSize+n.Offset-6] = 1
			case "truncated":
				b = b[:len(b)-1]
			}
			if kind != "truncated" {
				resealTestPages(b)
			}
			got, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err == nil || got != nil {
				t.Fatal("accepted damage", kind)
			}
		})
	}
}
func TestClusterMetadataDamage(t *testing.T) {
	for _, kind := range []string{"rowid-length", "rowid-null", "rowid-unsigned", "hidden-flag", "field-order", "field-length", "direction", "kind", "missing-rowid", "wrong-cluster"} {
		t.Run(kind, func(t *testing.T) {
			b, _ := clusterFixture(t, "rowid_lesson")
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			d := json.NewDecoder(bytes.NewReader(sdi.Records[0].JSON))
			d.UseNumber()
			if err = d.Decode(&doc); err != nil {
				t.Fatal(err)
			}
			table := doc["dd_object"].(map[string]any)
			cols := table["columns"].([]any)
			idx := table["indexes"].([]any)[0].(map[string]any)
			fields := idx["elements"].([]any)
			switch kind {
			case "rowid-length":
				cols[2].(map[string]any)["char_length"] = 3
			case "rowid-unsigned":
				cols[2].(map[string]any)["is_unsigned"] = true
			case "rowid-null":
				cols[2].(map[string]any)["is_nullable"] = true
			case "hidden-flag":
				idx["hidden"] = false
			case "field-order":
				fields[0].(map[string]any)["column_opx"] = 3
			case "field-length":
				fields[0].(map[string]any)["length"] = 6
			case "direction":
				fields[0].(map[string]any)["order"] = 3
			case "kind":
				idx["type"] = 3
			case "missing-rowid":
				cols[2].(map[string]any)["name"] = "unexpected"
			case "wrong-cluster":
				idx["name"] = "GEN_CLUST_INDEX"
			}
			sdi.Records[0].JSON, _ = json.Marshal(doc)
			got, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), sdi)
			if err == nil && (got.Schema != nil || len(got.Issues) == 0) {
				t.Fatal("accepted bad metadata")
			}
		})
	}
}
func FuzzRowID(f *testing.F) {
	f.Add(uint64(0), uint64((1<<48)-1))
	f.Fuzz(func(t *testing.T, a, b uint64) {
		a &= (1 << 48) - 1
		b &= (1 << 48) - 1
		var x, y [8]byte
		be.PutUint64(x[:], a)
		be.PutUint64(y[:], b)
		if rowIDValue(x[2:]) != a {
			t.Fatal("decode")
		}
		s := Schema{ClusteredKey: &ClusteredKey{HiddenRowID: true}}
		cmp := compareIndexKey(indexKey{x[2:]}, indexKey{y[2:]}, s, nil)
		if (cmp < 0) != (a < b) || (cmp == 0) != (a == b) || (cmp > 0) != (a > b) {
			t.Fatal("order")
		}
	})
}

func TestClusterRejectsUnsupportedChosenKey(t *testing.T) {
	b, _ := clusterFixture(t, "unique_multiple")
	sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(sdi.Records[0].JSON))
	d.UseNumber()
	if err = d.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	// A supported second unique index must not replace an invalid actual cluster.
	doc["dd_object"].(map[string]any)["columns"].([]any)[1].(map[string]any)["is_nullable"] = true
	sdi.Records[0].JSON, _ = json.Marshal(doc)
	got, err := inspectSDITable(bytes.NewReader(b), int64(len(b)), sdi)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != nil || len(got.Issues) == 0 {
		t.Fatal("silently selected another unique index")
	}
}
func TestRowIDTruncation(t *testing.T) {
	b, s := clusterFixture(t, "rowid_lesson")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	row := r.Records[0]
	page := b[int(row.PageNumber)*PageSize : int(row.PageNumber+1)*PageSize]
	for _, n := range []int{0, 5, 6, 18} {
		if _, err := decodeRecord(page, row.Offset, row.Offset+n, s, nil); !errors.Is(err, ErrCorrupt) {
			t.Fatal("truncated ROW_ID/system", n, err)
		}
	}
	b, s = clusterFixture(t, "rowid_deep")
	r, err = Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	node := r.Nodes[0]
	page = b[int(node.PageNumber)*PageSize : int(node.PageNumber+1)*PageSize]
	for _, n := range []int{0, 5, 6, 9} {
		if _, err := decodeNode(page, node.Offset, node.Offset+n, s, nil); !errors.Is(err, ErrCorrupt) {
			t.Fatal("truncated ROW_ID/child", n, err)
		}
	}
}
func FuzzHiddenPage(f *testing.F) {
	b, s := clusterFixture(f, "rowid_lesson")
	page := b[int(s.RootPage)*PageSize : int(s.RootPage+1)*PageSize]
	p, err := parseIndex(page, s)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(page)
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) != PageSize {
			return
		}
		got, err := decodePage(b, p, s, nil)
		if err != nil && got != nil {
			t.Fatal("partial page")
		}
	})
}
