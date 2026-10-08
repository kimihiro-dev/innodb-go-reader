package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpaceAssets(t *testing.T) {
	count := 0
	err := filepath.WalkDir("testdata", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (!strings.HasSuffix(path, ".ibd.gz") && !strings.HasSuffix(path, ".ibd")) {
			return nil
		}
		t.Run(path, func(t *testing.T) {
			b := scanFixture(t, path)
			r, err := AnalyzeSpace(context.Background(), bytes.NewReader(b), int64(len(b)), SpaceOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if r.FilePages != r.UsedPages+r.FreePages+r.UninitializedPages+r.TailPages {
				t.Fatal("page accounting")
			}
			count++
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 487 {
		t.Fatal("assets", count)
	}
	t.Log("assets", count)
}

func TestSpaceSnapshots(t *testing.T) {
	var m struct {
		Status string
		Cases  []struct {
			Name, SHA256 string
			Bytes        int64
		}
	}
	raw, err := os.ReadFile("testdata/space/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &m); err != nil || m.Status != "captured" {
		t.Fatal(err, m.Status)
	}
	for _, c := range m.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b := scanFixture(t, "testdata/space/"+c.Name+".space.gz")
			if int64(len(b)) != c.Bytes || fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("fixture identity")
			}
			r, err := AnalyzeSpace(context.Background(), bytes.NewReader(b), int64(len(b)), SpaceOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var sql struct {
				Table   []struct{ Space uint32 }
				Indexes []struct {
					ID          uint64
					Page, Space uint32
				}
				Rows uint64
			}
			if err = json.Unmarshal(scanFixture(t, "testdata/space/"+c.Name+".sql-metadata.json.gz"), &sql); err != nil {
				t.Fatal(err)
			}
			if len(sql.Table) != 1 || r.SpaceID != sql.Table[0].Space {
				t.Fatal("table space")
			}
			for _, si := range sql.Indexes {
				found := false
				for _, idx := range r.Indexes {
					if idx.ID == si.ID {
						found = true
						if idx.Root != si.Page || r.SpaceID != si.Space {
							t.Fatal("SQL index identity")
						}
						if c.Name != "deleted" && idx.PhysicalRecords != sql.Rows {
							t.Fatal("SQL row count", idx)
						}
					}
				}
				if !found {
					t.Fatal("missing index", si)
				}
			}
			leased := 0
			for _, e := range r.Extents {
				if e.State == 5 {
					leased++
				}
			}
			if c.Name == "grown" && (r.FilePages <= 16384 || r.Pages[16384].Type != 9 || r.Pages[16385].Type != 5 || leased == 0) {
				t.Fatal("second descriptor/leased extent", leased)
			}
			if r.FilePages != r.UsedPages+r.FreePages+r.UninitializedPages+r.TailPages {
				t.Fatal("page totals")
			}
			t.Logf("file=%d used=%d free=%d uninitialized=%d zero=%d extents=%d segments=%d leased=%d indexes=%d", r.FilePages, r.UsedPages, r.FreePages, r.UninitializedPages, r.ZeroPages, len(r.Extents), len(r.Segments), leased, len(r.Indexes))
		})
	}
}
