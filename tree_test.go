package innodb

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func unzip(t testing.TB, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func treeFixture(t testing.TB, name string) ([]byte, Schema) {
	t.Helper()
	b := unzip(t, filepath.Join("testdata/trees", name+".ibd.gz"))
	j, err := os.ReadFile(filepath.Join("testdata/trees", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Schema
	if err = json.Unmarshal(j, &s); err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestTreeFixtures(t *testing.T) {
	b, err := os.ReadFile("testdata/trees/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
			Level        uint16 `json:"root_level"`
		}
	}
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b, s := treeFixture(t, c.Name)
			hash := sha256.Sum256(b)
			if hex.EncodeToString(hash[:]) != c.SHA256 {
				t.Fatal("SHA mismatch")
			}
			r, err := Read(bytes.NewReader(b), int64(len(b)), s)
			if err != nil {
				t.Fatal(err)
			}
			if r.Page.Level != c.Level || len(r.Records) != c.Rows {
				t.Fatalf("level/rows mismatch: %d/%d", r.Page.Level, len(r.Records))
			}
			want := unzip(t, filepath.Join("testdata/trees", c.Name+".expected.json.gz"))
			var expected [][]any
			decoder := json.NewDecoder(bytes.NewReader(want))
			decoder.UseNumber()
			if err = decoder.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			for i, rec := range r.Records {
				got, err := json.Marshal(rec.Values)
				if err != nil {
					t.Fatal(err)
				}
				var values []any
				d := json.NewDecoder(bytes.NewReader(got))
				d.UseNumber()
				if err = d.Decode(&values); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(values, expected[i]) {
					t.Fatalf("SQL mismatch at row %d", i)
				}
				page := b[int(rec.PageNumber)*PageSize : int(rec.PageNumber+1)*PageSize]
				if !bytes.Equal(rec.Transaction[:], page[rec.Offset+4:rec.Offset+10]) {
					t.Fatal("wrong record page provenance")
				}
			}
			// Independent sibling walk must enumerate exactly the same leaves as DFS.
			var leaves []uint32
			levels := map[uint16]int{}
			garbage := 0
			for _, p := range r.Pages {
				levels[p.Level]++
				if p.Level == 0 {
					leaves = append(leaves, p.Number)
				}
				if p.Garbage > 0 {
					garbage++
				}
			}
			var chain []uint32
			for n := leaves[0]; n != ^uint32(0); {
				if len(chain) > len(leaves) {
					t.Fatal("cyclic leaf chain")
				}
				chain = append(chain, n)
				n = be.Uint32(b[int(n)*PageSize+12 : int(n)*PageSize+16])
			}
			if !reflect.DeepEqual(leaves, chain) {
				t.Fatal("DFS differs from leaf chain")
			}
			t.Logf("rows=%d levels=%v pages=%d nodes=%d garbage_pages=%d", len(r.Records), levels, len(r.Pages), len(r.Nodes), garbage)
		})
	}
}

func TestCorruptTree(t *testing.T) {
	b, s := treeFixture(t, "ordered_rows")
	r, err := Read(bytes.NewReader(b), int64(len(b)), s)
	if err != nil {
		t.Fatal(err)
	}
	n0, n1 := r.Nodes[0], r.Nodes[1]
	root := int(s.RootPage) * PageSize
	child := int(n0.ChildPage) * PageSize
	cases := []struct {
		name   string
		mutate func([]byte)
	}{
		{"child-outside-file", func(b []byte) { be.PutUint32(b[root+n0.Offset+4:], ^uint32(0)) }},
		{"child-zero", func(b []byte) { be.PutUint32(b[root+n0.Offset+4:], 0) }},
		{"cycle-to-root", func(b []byte) { be.PutUint32(b[root+n0.Offset+4:], s.RootPage) }},
		{"duplicate-child", func(b []byte) { be.PutUint32(b[root+n1.Offset+4:], n0.ChildPage) }},
		{"wrong-child-level", func(b []byte) { be.PutUint16(b[child+64:], 1) }},
		{"wrong-child-space", func(b []byte) { be.PutUint32(b[child+34:], 999) }},
		{"wrong-child-index", func(b []byte) { be.PutUint64(b[child+66:], 999) }},
		{"missing-minimum", func(b []byte) { b[root+n0.Offset-5] &= ^byte(0x10) }},
		{"node-null-bitmap", func(b []byte) { b[root+n0.Start] = 1 }},
		{"extra-minimum", func(b []byte) { b[root+n1.Offset-5] |= 0x10 }},
		{"parent-range", func(b []byte) { be.PutUint32(b[root+n1.Offset:], uint32(n1.Key.(int32)+1)^0x80000000) }},
		{"leaf-next", func(b []byte) { be.PutUint32(b[child+12:], ^uint32(0)) }},
		{"leaf-prev", func(b []byte) { be.PutUint32(b[child+8:], n0.ChildPage) }},
		{"free-to-live", func(b []byte) { be.PutUint16(b[child+44:], uint16(r.Records[0].Offset)) }},
		{"free-cycle", func(b []byte) { o := int(be.Uint16(b[child+44:])); be.PutUint16(b[child+o-2:], 16384) }},
		{"record-cycle", func(b []byte) { be.PutUint16(b[root+n1.Offset-2:], uint16(65536+n0.Offset-n1.Offset)) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			copyb := append([]byte(nil), b...)
			c.mutate(copyb)
			resealTestPages(copyb)
			got, err := Read(bytes.NewReader(copyb), int64(len(copyb)), s)
			if !errors.Is(err, ErrCorrupt) || got != nil {
				t.Fatalf("want corruption and no result, got %v", err)
			}
		})
	}
}

func FuzzTree(f *testing.F) {
	b, s := treeFixture(f, "ordered_rows")
	f.Add(uint32(4*PageSize+12), []byte{0, 0, 0, 4})
	f.Fuzz(func(t *testing.T, offset uint32, change []byte) {
		data := append([]byte(nil), b...)
		copy(data[int(offset)%len(data):], change)
		resealTestPages(data)
		_, _ = Read(bytes.NewReader(data), int64(len(data)), s)
	})
}
