package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func changeFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, "testdata/changes/"+name+".ibd.gz")
	raw, err := os.ReadFile("testdata/changes/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}
func TestChangeFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/changes/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Table, Phase string
			Physical     struct {
				DeleteMarked, Pages, FreePages, GarbageBytes int
				RootLevel                                    uint16
			}
			Rows      int
			Unordered bool
		}
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	total := 0
	results := map[string]*Result{}
	schemas := map[string]Schema{}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := changeFixture(t, c.Name)
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/changes/"+c.Name+".sdi.json.gz"), &official); err != nil {
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
			results[c.Name] = got
			schemas[c.Name] = s
			manual, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil || !reflect.DeepEqual(got, manual) {
				t.Fatal("manual/auto", err)
			}
			var expected []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/changes/"+c.Name+".expected.json.gz"), &expected); err != nil {
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
			if err = json.Unmarshal(unzip(t, "testdata/changes/"+c.Name+".indexes.json.gz"), &indexes); err != nil {
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
			if c.Name == "tree_purged" && (got.Page.Level != 0 || len(got.Pages) != 1) {
				t.Fatal("missing non-leaf coverage")
			}
			if c.Name == "tree_before" && got.Page.Level < 2 {
				t.Fatalf("expected three levels, got %d", got.Page.Level)
			}
			physicalRows := 0
			for _, page := range got.Pages {
				if page.Level == 0 {
					physicalRows += int(page.Records)
				}
			}
			if physicalRows != len(got.Records)+len(got.DeletedRecords) {
				t.Fatal("linked record count")
			}
			if len(got.DeletedRecords) != c.Physical.DeleteMarked || len(got.Pages) != c.Physical.Pages || got.Page.Level != c.Physical.RootLevel {
				t.Fatal("capture metrics")
			}
			if c.Phase != "marked" && len(got.DeletedRecords) != 0 {
				t.Fatal("unexpected retained deletion")
			}
			for _, r := range got.DeletedRecords {
				start := int(r.PageNumber)*PageSize + r.Start
				if !bytes.Equal(r.Raw, b[start:start+len(r.Raw)]) || len(r.Raw) != r.End-r.Start || r.Header[0]&0xf0 != 0x20 {
					t.Fatal("local deletion evidence")
				}
				origin := r.Offset - r.Start
				if !bytes.Equal(r.Raw[origin-5:origin], r.Header[:]) {
					t.Fatal("raw header")
				}
			}

		})
	}
	// SQL-checked before/current rows independently identify removed keys.
	for _, table := range []string{"lesson", "empty", "hidden", "unique", "tree"} {
		before, marked := results[table+"_before"], results[table+"_marked"]
		pk, err := schemas[table+"_before"].validate()
		if err != nil {
			t.Fatal(err)
		}
		removed := map[string]bool{}
		keyString := func(v any) string { b, _ := json.Marshal(v); return string(b) }
		for _, r := range before.Records {
			removed[keyString(nodeKey(r, pk))] = true
		}
		for _, r := range marked.Records {
			delete(removed, keyString(nodeKey(r, pk)))
		}
		if len(removed) != len(marked.DeletedRecords) {
			t.Fatal(table, "deleted key count")
		}
		for _, r := range marked.DeletedRecords {
			key := keyString(r.Key)
			if !removed[key] {
				t.Fatal(table, "unexpected deleted key", key)
			}
			delete(removed, key)
		}
	}

	t.Logf("%d real key rows", total)
}

func TestDeletedRecordContracts(t *testing.T) {
	b, s := changeFixture(t, "lesson_marked")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.DeletedRecords) != 2 {
		t.Fatal("delete marks")
	}
	// The deletion summary has no Values field: local evidence is not SQL history.
	if _, ok := reflect.TypeOf(DeletedRecord{}).FieldByName("Values"); ok {
		t.Fatal("partial SQL values exposed")
	}
	for _, kind := range []string{"duplicate", "wrong-key", "instant", "version", "minimum", "bad-length", "bad-ownership"} {
		t.Run(kind, func(t *testing.T) {
			data := append([]byte{}, b...)
			d := r.DeletedRecords[0]
			p := data[int(d.PageNumber)*PageSize : int(d.PageNumber+1)*PageSize]
			switch kind {
			case "duplicate":
				other := r.DeletedRecords[1]
				copy(p[d.Offset:d.Offset+4], data[int(other.PageNumber)*PageSize+other.Offset:int(other.PageNumber)*PageSize+other.Offset+4])
			case "wrong-key":
				be.PutUint32(p[d.Offset:], 0xffffffff)
			case "instant":
				p[d.Offset-5] |= 0x80
			case "version":
				p[d.Offset-5] |= 0x40
			case "minimum":
				p[d.Offset-5] |= 0x10
			case "bad-length":
				p[d.Offset-7] = 0x83
				p[d.Offset-8] = 0xff
			case "bad-ownership":
				p[d.Offset-5] = (p[d.Offset-5] & 0xf0) | 15
			}
			resealTestPages(data)
			got, err := Read(bytes.NewReader(data), int64(len(data)), s)
			if err == nil || got != nil {
				t.Fatal("accepted corrupt deleted record", kind)
			}
		})
	}
}

func TestDeletedLOBIsLocalEvidence(t *testing.T) {
	b, s := clusterFixture(t, "rowid_lob")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	first := r.Records[0]
	field := first.External[0]
	b[int(first.PageNumber)*PageSize+first.Offset-5] |= 0x20
	// Make its LOB page unusable. Other rows reference independent LOBs.
	be.PutUint16(b[int(field.FirstPage)*PageSize+24:], 0)
	resealTestPages(b)
	r, err = Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil || r == nil || len(r.Records) != 2 || len(r.DeletedRecords) != 1 {
		t.Fatal("followed deleted external reference", err)
	}
	if r.DeletedRecords[0].End != first.End {
		t.Fatal("local reference boundary")
	}
}

func TestDeletedBinaryKeyOwnership(t *testing.T) {
	b, s := changeFixture(t, "tree_marked")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	d := &r.DeletedRecords[0]
	key := d.Key.([]any)[0].([]byte)
	before := append([]byte{}, key...)
	d.Raw[d.Offset-d.Start] ^= 0xff
	if !bytes.Equal(before, key) {
		t.Fatal("key aliases raw")
	}
	// Changing the result cannot alter the supplied snapshot.
	if !bytes.Equal(before, b[int(d.PageNumber)*PageSize+d.Offset:int(d.PageNumber)*PageSize+d.Offset+len(before)]) {
		t.Fatal("snapshot modified")
	}
}

func FuzzChangedPage(f *testing.F) {
	b, s := changeFixture(f, "lesson_marked")
	page := b[int(s.RootPage)*PageSize : int(s.RootPage+1)*PageSize]
	p, err := parseIndex(page, s)
	if err != nil {
		f.Fatal(err)
	}
	pk, err := s.validate()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(page)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) != PageSize {
			return
		}
		got, err := decodePage(data, p, s, pk)
		if err != nil && got != nil {
			t.Fatal("partial page")
		}
	})
}
