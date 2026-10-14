package visual

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	innodb "innodb-go-reader"
	"strings"
	"testing"
)

func TestMySQL57ExplicitIndexes(t *testing.T) {
	var manifest struct {
		Cases []struct {
			Name string
			Rows int
		}
	}
	if err := json.Unmarshal(fixture(t, "mysql5744-secondary/manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b := fixture(t, "mysql5744-secondary/"+c.Name+".ibd.gz")
			var schema innodb.Schema
			var indexes []innodb.SecondarySchema
			if err := json.Unmarshal(fixture(t, "mysql5744-secondary/"+c.Name+".json"), &schema); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(fixture(t, "mysql5744-secondary/"+c.Name+".indexes.json"), &indexes); err != nil {
				t.Fatal(err)
			}
			cluster := "PRIMARY"
			if schema.ClusteredKey != nil {
				cluster = schema.ClusteredKey.Name
			}
			names := []string{cluster}
			for _, index := range indexes {
				names = append(names, index.Name)
			}
			for _, name := range names {
				o := Options{Schema: &schema, Indexes: indexes, Index: name, Pages: []uint32{schema.RootPage}, Materialized: name == cluster && len(schema.VirtualColumns) > 0}
				root := schema.RootPage
				for _, index := range indexes {
					if index.Name == name {
						root = index.RootPage
					}
				}
				if root != schema.RootPage {
					o.Pages = append(o.Pages, root)
				}
				report, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), o)
				if err != nil || report == nil {
					t.Fatal(name, err)
				}
				if report.MetadataSource != "explicit_schema" || report.IndexRoot == nil || *report.IndexRoot != root || len(report.TreePages) == 0 || report.TreePages[0].Number != root {
					t.Fatal("tree identity", name)
				}
				if strings.HasPrefix(c.Name, "tree_") && name == "secondary" && (report.TreePages[0].Level != 2 || len(report.Edges) == 0) {
					t.Fatal("three-layer tree")
				}
				if c.Rows > 0 && len(report.TreePages) == 1 && len(report.Details) == 0 {
					t.Fatal("selected record details")
				}
				if name != cluster {
					s := report.ScanReport.(innodb.SecondaryScanReport)
					if !s.Complete || s.Records != uint64(c.Rows) || len(report.Columns) != 0 || len(report.SecondaryFields) == 0 {
						t.Fatal("physical secondary layer", s)
					}
				}
				var html bytes.Buffer
				if err := WriteHTML(t.Context(), &html, report); err != nil {
					t.Fatal(err)
				}
				payload := embedded(t, html.String())
				if payload["MetadataSource"] != "explicit_schema" {
					t.Fatal("provenance")
				}
				if failed, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), Options{Schema: &schema, Indexes: indexes, Index: name, MaxReadCalls: report.ReadCalls - 1, Materialized: o.Materialized, Pages: o.Pages}); err == nil || failed != nil {
					t.Fatal("read budget", err)
				}
			}
		})
	}
}
func TestMySQL57ExplicitVisualFailures(t *testing.T) {
	b := fixture(t, "mysql5744-secondary/generated_dynamic.ibd.gz")
	var schema innodb.Schema
	var indexes []innodb.SecondarySchema
	_ = json.Unmarshal(fixture(t, "mysql5744-secondary/generated_dynamic.json"), &schema)
	_ = json.Unmarshal(fixture(t, "mysql5744-secondary/generated_dynamic.indexes.json"), &indexes)
	for _, o := range []Options{
		{Schema: &schema}, {Indexes: indexes, Index: "secondary"}, {Schema: &schema, Index: "missing"},
		{Schema: &schema, Index: "PRIMARY"}, {Schema: &schema, Indexes: indexes, Index: "secondary", Materialized: true},
		{Schema: &schema, Indexes: indexes, Index: "secondary", Scan: innodb.ScanOptions{MaxPageReads: 4}},
	} {
		if report, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), o); err == nil || report != nil {
			t.Fatal("accepted", o)
		}
	}
	if report, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), Options{Schema: &schema, Indexes: indexes, Index: "secondary"}); err != nil || report == nil {
		t.Fatal("VIRTUAL physical index", err)
	}
	space, err := Build(t.Context(), bytes.NewReader(b), int64(len(b)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	reader := &sourceReader{ctx: t.Context(), source: bytes.NewReader(b), limit: 1000000}
	report, err := Build(t.Context(), reader, int64(len(b)), Options{Schema: &schema, Indexes: indexes, Index: "secondary", Scan: innodb.ScanOptions{MaxPageReads: 2}})
	if report != nil || !errors.Is(err, innodb.ErrLimit) || reader.calls != space.ReadCalls+2 {
		t.Fatal("preflight must stop before exceeding scan reads", err, reader.calls, space.ReadCalls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if report, err := Build(ctx, bytes.NewReader(b), int64(len(b)), Options{Schema: &schema, Indexes: indexes, Index: "secondary"}); !errors.Is(err, context.Canceled) || report != nil {
		t.Fatal("cancel", err)
	}
}
