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
	"unicode/utf8"
)

func updatedLOBFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, "testdata/lob_updates/"+name+".ibd.gz")
	raw, err := os.ReadFile("testdata/lob_updates/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestUpdatedLOBFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/lob_updates/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
			Physical     struct{ DeleteMarked int }
		}
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	total, historyMax, allocationMax, inherited, moved, split := 0, 0, 0, false, false, false
	results := map[string]*Result{}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := updatedLOBFixture(t, c.Name)
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			meta, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil || meta.Schema == nil || !reflect.DeepEqual(s, *meta.Schema) {
				t.Fatal("schema", err)
			}
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			manual, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil || !reflect.DeepEqual(got, manual) {
				t.Fatal("manual/auto", err)
			}
			results[c.Name] = got
			var expected []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/lob_updates/"+c.Name+".expected.json.gz"), &expected); err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != c.Rows || len(expected) != c.Rows || len(got.DeletedRecords) != c.Physical.DeleteMarked {
				t.Fatal("counts")
			}
			for i, r := range got.Records {
				values := append([]any{}, r.Values...)
				for j, v := range values {
					switch x := v.(type) {
					case []byte:
						values[j] = strings.ToUpper(fmt.Sprintf("%x", x))
					case JSONValue:
						values[j], err = x.JSON()
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				encoded, err := json.Marshal(values)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(jsonTextValue(t, encoded), jsonTextValue(t, expected[i])) {
					t.Fatalf("row %d SQL mismatch", i)
				}
				total++
				for _, f := range r.External {
					first := b[int(f.FirstPage)*PageSize:]
					allocations := 0
					for n := be.Uint32(first[12:]); n != ^uint32(0); n = be.Uint32(b[int(n)*PageSize+12:]) {
						allocations++
					}
					if allocations > allocationMax {
						allocationMax = allocations
					}
					if f.Reference[12]&0xc0 != 0 {
						inherited = true
					}
					if f.Chunks[0].PageNumber != f.FirstPage {
						moved = true
					}
					joined := []byte{}
					for _, chunk := range f.Chunks {
						e := b[int(chunk.IndexPage)*PageSize+chunk.IndexOffset:]
						count := int(be.Uint32(e[12:]))
						if c.Name == "documents_history_purged" && count != 0 {
							t.Fatal("history not purged")
						}
						if count > historyMax {
							historyMax = count
						}
						start := int(chunk.PageNumber)*PageSize + chunk.Offset
						part := b[start : start+chunk.Length]
						joined = append(joined, part...)
						if s.Columns[f.Column].Type == "LONGTEXT" && !utf8.Valid(part) {
							split = true
						}
					}
					if len(joined) != int(f.Length) {
						t.Fatal("chunk provenance length")
					}
					if want, ok := r.TextBytes[f.Column]; ok && !bytes.Equal(joined, want) {
						t.Fatal("text provenance")
					}
					if want, ok := r.Values[f.Column].([]byte); ok && !bytes.Equal(joined, want) {
						t.Fatal("binary provenance")
					}
					if want, ok := r.Values[f.Column].(JSONValue); ok {
						v, err := DecodeJSON(joined)
						if err != nil || !reflect.DeepEqual(v, want) {
							t.Fatal("JSON provenance", err)
						}
					}
				}
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/lob_updates/"+c.Name+".sdi.json.gz"), &official); err != nil {
				t.Fatal(err)
			}
			if len(official) != len(sdi.Records)+1 {
				t.Fatal("SDI count")
			}
			for i, r := range sdi.Records {
				var want struct {
					Type   uint32
					ID     uint64
					Object json.RawMessage
				}
				if err = json.Unmarshal(official[i+1], &want); err != nil {
					t.Fatal(err)
				}
				if r.Key != (SDIKey{want.Type, want.ID}) || !reflect.DeepEqual(jsonTextValue(t, r.JSON), jsonTextValue(t, want.Object)) {
					t.Fatal("official SDI")
				}
			}
		})
	}
	if t.Failed() {
		return
	}
	// These assertions prove the fixture actually exercised the intended paths.
	a := results["documents_before"].Records[0].External[0]
	b := results["documents_small"].Records[0].External[0]
	if a.Version != 1 || b.Version != 1 || !reflect.DeepEqual(a.Chunks, b.Chunks) {
		t.Fatal("small change should reuse version and pages")
	}
	a = results["documents_large"].Records[0].External[0]
	if a.Version <= b.Version || a.FirstPage != b.FirstPage || reflect.DeepEqual(a.Chunks, b.Chunks) {
		t.Fatal("large change must replace chunks")
	}
	if historyMax < 13 || allocationMax < 2 || !inherited || !moved || !split {
		t.Fatalf("matrix history=%d allocations=%d inherited=%t moved=%t split=%t", historyMax, allocationMax, inherited, moved, split)
	}
	if !reflect.DeepEqual(results["documents_reuse"].Records, results["documents_history_purged"].Records) {
		t.Fatal("purge changed current values or provenance")
	}
	t.Logf("%d snapshots, %d rows; max history %d, allocation pages %d", len(manifest.Cases), total, historyMax, allocationMax)
}

func TestUpdatedLOBFlags(t *testing.T) {
	original, s := lobFixture(t, "lob_text")
	got, err := Read(bytes.NewReader(original), int64(len(original)), s)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Records[1]
	f := r.External[0]
	ref := int(r.PageNumber)*PageSize + f.Offset
	first := int(f.FirstPage) * PageSize
	for _, flags := range []byte{0x40, 0x80, 0xc0} {
		t.Run(fmt.Sprintf("%x", flags), func(t *testing.T) {
			b := append([]byte(nil), original...)
			b[ref+12] = flags
			b[first+39] = 1
			be.PutUint32(b[first+50:], 99)
			resealTestPages(b)
			v, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil || !reflect.DeepEqual(v.Records[1].Values, r.Values) {
				t.Fatal("lifecycle flags", err)
			}
		})
	}
}

func TestUpdatedLOBDamage(t *testing.T) {
	original, s := updatedLOBFixture(t, "documents_repeated")
	got, err := Read(bytes.NewReader(original), int64(len(original)), s)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Records[1]
	f := r.External[0]
	first := int(f.FirstPage) * PageSize
	ref := int(r.PageNumber)*PageSize + f.Offset
	c := f.Chunks[0]
	entry := int(c.IndexPage)*PageSize + c.IndexOffset
	addr := lobAddr(original[entry+16:])
	old := int(addr.page)*PageSize + int(addr.offset)
	tests := []struct {
		name   string
		mutate func([]byte)
		want   error
	}{
		{"old-reference", func(b []byte) { be.PutUint32(b[ref+8:], f.Version-1) }, ErrUnsupported},
		{"zero-reference", func(b []byte) { be.PutUint32(b[ref+8:], 0) }, ErrCorrupt},
		{"zero-first-version", func(b []byte) { be.PutUint32(b[first+40:], 0) }, ErrCorrupt},
		{"history-count", func(b []byte) { be.PutUint32(b[entry+12:], ^uint32(0)) }, ErrCorrupt},
		{"history-prev", func(b []byte) { be.PutUint32(b[old:], f.FirstPage) }, ErrCorrupt},
		{"history-cycle", func(b []byte) { copy(b[old+6:old+12], b[entry+16:entry+22]) }, ErrCorrupt},
		{"history-version", func(b []byte) { be.PutUint32(b[old+56:], f.Version+1) }, ErrCorrupt},
		{"history-zero-version", func(b []byte) { be.PutUint32(b[old+56:], 0) }, ErrCorrupt},
		{"history-page", func(b []byte) { be.PutUint32(b[old+48:], uint32(len(b)/PageSize)) }, ErrCorrupt},
		{"history-length", func(b []byte) { be.PutUint16(b[old+52:], 0) }, ErrCorrupt},
		{"nested-history", func(b []byte) { be.PutUint32(b[old+12:], 1) }, ErrCorrupt},
		{"history-tail", func(b []byte) { be.PutUint16(b[entry+26:], 97) }, ErrCorrupt},
		{"active-in-history", func(b []byte) {
			be.PutUint32(b[entry+16:], c.IndexPage)
			be.PutUint16(b[entry+20:], uint16(c.IndexOffset))
		}, ErrCorrupt},
		{"allocated-cycle", func(b []byte) { be.PutUint32(b[first+12:], f.FirstPage) }, ErrCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := append([]byte(nil), original...)
			tt.mutate(b)
			resealTestPages(b)
			v, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if v != nil || !errors.Is(err, tt.want) {
				t.Fatalf("result=%t err=%v", v != nil, err)
			}
		})
	}
}

func FuzzUpdatedLOB(f *testing.F) {
	original, s := updatedLOBFixture(f, "documents_large")
	f.Add(uint32(5*PageSize+64), []byte{255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, off uint32, change []byte) {
		if len(change) > PageSize {
			return
		}
		b := append([]byte(nil), original...)
		copy(b[int(off)%len(b):], change)
		resealTestPages(b)
		v, err := Read(bytes.NewReader(b), int64(len(b)), s)
		if v != nil && err != nil {
			t.Fatal("partial result")
		}
	})
}
