package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

func consistencyEvidence(b []byte, source string) *ExportSnapshotEvidence {
	return &ExportSnapshotEvidence{Source: source, SHA256: fmt.Sprintf("%x", sha256.Sum256(b)), ExportCompleted: true, CopiedWhileLocked: true, WritesCompleted: true}
}

func consistencyFixture(t testing.TB, name string) ([]byte, Schema, []SecondarySchema, int, int) {
	t.Helper()
	b := unzip(t, "testdata/secondary/"+name+".ibd.gz")
	m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
	if err != nil || m.MaterializedSchema == nil {
		t.Fatal(err)
	}
	var indexes []SecondarySchema
	var oracle []struct {
		Name string
		Rows []json.RawMessage
	}
	if err := json.Unmarshal(unzip(t, "testdata/secondary/"+name+".secondary.json.gz"), &oracle); err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, idx := range oracle {
		s, err := inspectSecondaryMetadata(m, idx.Name)
		if err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, *s)
		rows += len(idx.Rows)
	}
	var cluster struct{ Rows []json.RawMessage }
	if err := json.Unmarshal(unzip(t, "testdata/secondary/"+name+".expected.json.gz"), &cluster); err != nil {
		t.Fatal(err)
	}
	return b, *m.MaterializedSchema, indexes, len(cluster.Rows), rows
}

func TestConsistencySnapshots(t *testing.T) {
	var manifest struct {
		Cases []struct{ Name, SHA256 string }
	}
	raw, err := os.ReadFile("testdata/secondary/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, tc := range manifest.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			b, table, indexes, rows, entries := consistencyFixture(t, tc.Name)
			e := consistencyEvidence(b, "testdata/secondary/README.md + writer.sql.gz + manifest.json")
			// Compare the bound input to the original capture manifest, not a
			// digest invented by another parser path or rewritten evidence.
			if e.SHA256 != tc.SHA256 {
				t.Fatal("original capture SHA256")
			}
			p, err := CheckIndexConsistencyMaterialized(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e})
			if err != nil || !p.Complete || !p.Passed || p.Truncated || len(p.Issues) != 0 || p.Preconditions.State != CheckPassed || p.ActualSHA256 != tc.SHA256 {
				t.Fatal("trusted original snapshot", p, err)
			}
			var actual uint64
			for _, idx := range p.Indexes {
				actual += idx.SecondaryRecords
				if idx.State != CheckPassed || idx.ClusteredRows != uint64(rows) || idx.Matched != uint64(rows) || idx.Missing+idx.Extra+idx.Mismatched+idx.DuplicateLocators != 0 {
					t.Fatal("bidirectional counts", idx)
				}
			}
			if actual != uint64(entries) {
				t.Fatal("independent FORCE INDEX rows", actual, entries)
			}
			if tc.Name == "changes" && p.Indexes[0].SecondaryDeleted == 0 {
				t.Fatal("history evidence not exercised")
			}
			if tc.Name == "generated" {
				if len(p.VirtualColumns) == 0 {
					t.Fatal("separate VIRTUAL definitions")
				}
				if q, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e}); err == nil || q.Passed || !errors.Is(err, ErrUnsupported) {
					t.Fatal(q, err)
				}
			}
		})
	}
}

func TestConsistencyEvidence(t *testing.T) {
	b, table, indexes, _, _ := consistencyFixture(t, "tiny")
	for _, change := range []func(*ExportSnapshotEvidence){
		func(e *ExportSnapshotEvidence) { e.Source = "" },
		func(e *ExportSnapshotEvidence) { e.ExportCompleted = false },
		func(e *ExportSnapshotEvidence) { e.CopiedWhileLocked = false },
		func(e *ExportSnapshotEvidence) { e.WritesCompleted = false },
		func(e *ExportSnapshotEvidence) { e.SHA256 = "bad" },
		func(e *ExportSnapshotEvidence) { e.SHA256 = fmt.Sprintf("%064d", 0) },
	} {
		e := consistencyEvidence(b, "capture logs")
		change(e)
		p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e})
		if err != nil || !p.Complete || p.Passed || p.Preconditions.State != CheckBlocked || len(p.Issues) != 0 || p.Indexes[0].State != CheckBlocked || p.Indexes[0].ClusteredRows != 0 {
			t.Fatal("unknown preconditions are not corruption/pass", p, err)
		}
	}
	r := &localPageReader{data: b}
	p, err := CheckIndexConsistency(t.Context(), r, int64(len(b)), table, indexes, ConsistencyOptions{})
	if err != nil || p.Passed || len(r.pages) != 0 {
		t.Fatal("missing evidence must not inspect index bytes", p, r.pages, err)
	}
}

func TestConsistencyMismatch(t *testing.T) {
	b, table, indexes, _, _ := consistencyFixture(t, "tiny")
	index := indexes[0]
	local, err := TraceSecondaryPage(bytes.NewReader(b), int64(len(b)), index.RootPage, index)
	if err != nil {
		t.Fatal(err)
	}
	// Each group of equal n has different id locators. Change one n without
	// changing order: select n=0 and move its first entry into the NULL group
	// would alter bitmap/layout, so instead alter the clustered n of its row.
	entry := local.Records[len(local.Records)/2]
	cluster, err := TraceClusteredPage(bytes.NewReader(b), int64(len(b)), table.RootPage, table)
	if err != nil {
		t.Fatal(err)
	}
	var origin int
	for _, row := range cluster.Records {
		if row.Values[0] == entry.ClusteredKey[0] {
			origin = row.Offset
		}
	}
	if origin == 0 {
		t.Fatal("row locator")
	}
	for _, source := range cluster.Sources {
		if source.Offset != origin {
			continue
		}
		for _, f := range source.Fields {
			if f.Column == 1 && f.Data != nil {
				b[int(table.RootPage)*PageSize+f.Data.Start] ^= 1
			}
		}
	}
	resealTestPages(b) // Synthetic field mismatch with otherwise valid trees.
	p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: consistencyEvidence(b, "synthetic mismatch; based on tiny capture")})
	if err != nil || !p.Complete || p.Passed || len(p.Issues) != 1 || p.Indexes[0].Mismatched != 1 || p.Indexes[0].Missing != 0 || p.Indexes[0].ClusteredRows != p.Indexes[0].SecondaryRecords {
		t.Fatal("same row count is not content equality", p, err)
	}
	issue := p.Issues[0]
	if issue.Problem.Code != ProblemIndexMismatch || issue.Clustered == nil || *issue.Clustered.RecordOffset != origin || issue.Problem.Location.Column == nil || *issue.Problem.Location.Column != 1 || issue.Problem.Location.Bytes == nil || !errors.Is(issue.Problem, ErrCorrupt) {
		t.Fatal("mismatch provenance", issue)
	}
}

func TestConsistencyMissingExtra(t *testing.T) {
	b, table, indexes, _, _ := consistencyFixture(t, "tiny")
	index := indexes[0]
	full, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), index)
	if err != nil {
		t.Fatal(err)
	}
	entry := full.Records[len(full.Records)/2]
	for _, side := range []string{"secondary", "clustered"} {
		t.Run(side, func(t *testing.T) {
			x := bytes.Clone(b)
			page, origin := entry.PageNumber, entry.Offset
			code := ProblemIndexMissing
			if side == "clustered" {
				code = ProblemIndexExtra
				selection, _ := prepareQuery(PointKey(entry.ClusteredKey), table, []int{0})
				selection.deferValues = true
				if err := walkSelectedTree(bytes.NewReader(x), int64(len(x)), table, func(e ScanEvent) error {
					if e.Record != nil {
						page, origin = e.Record.PageNumber, e.Record.Offset
					}
					return nil
				}, selection); err != nil {
					t.Fatal(err)
				}
			}
			x[int(page)*PageSize+origin-5] |= 0x20 // Synthetic delete mark; chain unchanged.
			resealTestPages(x)
			p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(x), int64(len(x)), table, indexes, ConsistencyOptions{Evidence: consistencyEvidence(x, "synthetic delete-mark; based on tiny")})
			if err != nil || !p.Complete || p.Passed || len(p.Issues) != 1 || p.Issues[0].Problem.Code != code {
				t.Fatal(p, err)
			}
		})
	}
}

func TestConsistencyControl(t *testing.T) {
	b, table, indexes, _, _ := consistencyFixture(t, "tiny")
	e := consistencyEvidence(b, "trusted tiny capture")
	for _, o := range []ConsistencyOptions{
		{ScanOptions: ScanOptions{MaxPageReads: 1}}, {ScanOptions: ScanOptions{MaxEntries: 1}},
		{ScanOptions: ScanOptions{MaxRows: 1}}, {ScanOptions: ScanOptions{MaxRowBytes: 1}}, {MaxBufferedBytes: 1},
	} {
		o.Evidence = e
		p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, o)
		if !errors.Is(err, ErrLimit) || p.Complete || p.Passed || !p.Truncated {
			t.Fatal("shared limit", p, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p, err := CheckIndexConsistency(ctx, bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e})
	if !errors.Is(err, context.Canceled) || p.Complete || p.Passed {
		t.Fatal(p, err)
	}
	cause := errors.New("snapshot read cause")
	p, err = CheckIndexConsistency(t.Context(), &diagnosticIOReader{data: b, fail: map[uint32]error{4: cause}}, int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e})
	if !errors.Is(err, cause) || p.Complete || p.Passed {
		t.Fatal(p, err)
	}
	p, err = CheckIndexConsistency(t.Context(), bytes.NewReader(b[:PageSize]), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e})
	if !errors.Is(err, io.EOF) || p.Complete || p.Passed {
		t.Fatal(p, err)
	}
	x := bytes.Clone(b)
	x[int(indexes[0].RootPage)*PageSize+200] ^= 1 // CRC damage: no reseal.
	p, err = CheckIndexConsistency(t.Context(), bytes.NewReader(x), int64(len(x)), table, indexes, ConsistencyOptions{Evidence: consistencyEvidence(x, "synthetic CRC fault")})
	if err != nil || !p.Complete || p.Passed || len(p.Issues) != 1 || p.Issues[0].Problem.Code != ProblemChecksum {
		t.Fatal(p, err)
	}
}

func TestConsistencyLocator(t *testing.T) {
	columns := []Column{{Type: "VARCHAR"}, {Type: "VARBINARY"}}
	key := func(a, b []byte) string { return consistencyLocator(indexKey{a, b}, columns) }
	if key([]byte("x"), []byte{}) != key([]byte("x  "), []byte{}) || key(nil, []byte{}) == key([]byte{}, []byte{}) || key([]byte("ab"), []byte("c")) == key([]byte("a"), []byte("bc")) {
		t.Fatal("PAD SPACE/NULL/member boundaries")
	}
}

func TestConsistencyHistoricalRevivalAndNavigation(t *testing.T) {
	b, table, indexes, _, _ := consistencyFixture(t, "changes")
	index := indexes[0]
	full, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), index)
	if err != nil || len(full.DeletedRecords) == 0 {
		t.Fatal("need retained history", err)
	}
	old := full.DeletedRecords[0]
	b[int(old.PageNumber)*PageSize+old.Offset-5] &^= 0x20
	resealTestPages(b) // Synthetic revival of a historical secondary entry.
	p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, []SecondarySchema{index}, ConsistencyOptions{Evidence: consistencyEvidence(b, "synthetic historical entry revival")})
	if err != nil || !p.Complete || p.Passed || len(p.Issues) == 0 || p.Indexes[0].SecondaryRecords != p.Indexes[0].ClusteredRows+1 || p.Indexes[0].Extra+p.Indexes[0].DuplicateLocators == 0 {
		t.Fatal("revived history cannot certify current entries", p, err)
	}
	b, table, indexes, _, _ = consistencyFixture(t, "deep")
	full, err = ReadSecondary(bytes.NewReader(b), int64(len(b)), indexes[0])
	if err != nil {
		t.Fatal(err)
	}
	node := full.Nodes[0]
	be.PutUint32(b[int(node.PageNumber)*PageSize+node.End-4:], 0)
	resealTestPages(b) // Synthetic zero child under valid CRCs.
	p, err = CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: consistencyEvidence(b, "synthetic zero child")})
	if err != nil || !p.Complete || p.Passed || len(p.Issues) != 1 || p.Indexes[0].Missing != 0 || p.Indexes[0].State != CheckFailed {
		t.Fatal("navigation failure blocks absence inference", p, err)
	}
}

func TestConsistencyVersions(t *testing.T) {
	for _, dir := range []string{"testdata/mysql5744-secondary", "testdata/mysql57-patches/5.7.12/secondary", "testdata/mysql80-patches/8.0.28", "testdata/mysql80-patches/8.0.30"} {
		var manifest struct {
			Cases []struct {
				Name, SHA256 string
				Rows         int
			}
		}
		raw, err := os.ReadFile(dir + "/manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		for _, tc := range manifest.Cases {
			// The 5.7 secondary corpus is small and explicitly defined. For
			// 8.0 select both formats; generated/INSTANT is in the base corpus.
			is57 := bytes.Contains([]byte(dir), []byte("mysql57"))
			if !is57 && tc.Name != "secondary_dynamic_current" && tc.Name != "secondary_compact_current" {
				continue
			}
			t.Run(dir+"/"+tc.Name, func(t *testing.T) {
				base := dir + "/" + tc.Name
				b := unzip(t, base+".ibd.gz")
				var table Schema
				var indexes []SecondarySchema
				if is57 {
					table = loadScanSchema(t, base+".json")
					raw, err := os.ReadFile(base + ".indexes.json")
					if err != nil || json.Unmarshal(raw, &indexes) != nil {
						t.Fatal(err)
					}
				} else {
					m, err := InspectTable(bytes.NewReader(b), int64(len(b)))
					if err != nil || m.MaterializedSchema == nil {
						t.Fatal(err)
					}
					table = *m.MaterializedSchema
					for _, idx := range m.Indexes {
						if idx.Clustered || idx.Hidden {
							continue
						}
						s, err := inspectSecondaryMetadata(m, idx.Name)
						if err != nil {
							t.Fatal(err)
						}
						indexes = append(indexes, *s)
					}
				}
				e := consistencyEvidence(b, dir+"/manifest.json and writer capture SQL")
				if e.SHA256 != tc.SHA256 {
					t.Fatal("original file digest")
				}
				p, err := CheckIndexConsistencyMaterialized(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e})
				if err != nil || !p.Complete || !p.Passed {
					t.Fatal(p, err)
				}
				for _, idx := range p.Indexes {
					if idx.ClusteredRows != uint64(tc.Rows) || idx.SecondaryRecords != uint64(tc.Rows) || idx.Matched != uint64(tc.Rows) {
						t.Fatal("original independent SQL row count", idx, tc.Rows)
					}
				}
			})
		}
	}
}

func TestConsistencyWrongLocator(t *testing.T) {
	b, table, indexes, _, _ := consistencyFixture(t, "tiny")
	full, err := ReadSecondary(bytes.NewReader(b), int64(len(b)), indexes[0])
	if err != nil {
		t.Fatal(err)
	}
	entry := full.Records[len(full.Records)-1]
	for _, id := range []byte{127, 90} {
		x := bytes.Clone(b)
		x[int(entry.PageNumber)*PageSize+entry.End-1] = id ^ 0x80 // Signed TINYINT locator, keeps physical order.
		resealTestPages(x)
		p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(x), int64(len(x)), table, indexes, ConsistencyOptions{Evidence: consistencyEvidence(x, "synthetic wrong TINYINT locator")})
		if err != nil || !p.Complete || p.Passed {
			t.Fatal(p, err)
		}
		counts := map[ProblemCode]int{}
		for _, issue := range p.Issues {
			counts[issue.Problem.Code]++
		}
		if counts[ProblemIndexMissing] != 1 {
			t.Fatal("original row must be missing", p)
		}
		if id == 127 && (counts[ProblemIndexExtra] != 1 || len(p.Issues) != 2) {
			t.Fatal("unknown locator", p)
		}
		if id == 90 && (counts[ProblemIndexLocator] != 1 || counts[ProblemIndexMismatch] != 1 || len(p.Issues) != 3) {
			t.Fatal("duplicate locator and wrong value", p)
		}
	}
}

func TestConsistencyUniqueAndIssues(t *testing.T) {
	b, table, indexes, _, _ := consistencyFixture(t, "unique_cluster")
	var index SecondarySchema
	for _, s := range indexes {
		if s.Name == "u_idx" {
			index = s
		}
	}
	local, err := TraceSecondaryPage(bytes.NewReader(b), int64(len(b)), index.RootPage, index)
	if err != nil {
		t.Fatal(err)
	}
	var previous *SecondaryRecord
	var target SecondaryRecord
	for i := range local.Records {
		r := &local.Records[i]
		if r.Values[0] == nil {
			continue
		}
		if previous != nil {
			target = *r
			break
		}
		previous = r
	}
	for _, source := range local.Sources {
		if source.Offset != target.Offset {
			continue
		}
		for _, field := range source.Fields {
			if field.Position == 0 {
				copy(b[int(index.RootPage)*PageSize+field.Data.Start:], previous.FieldBytes[0])
			}
		}
	}
	resealTestPages(b) // Two live equal non-NULL unique keys, increasing locators.
	p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, []SecondarySchema{index}, ConsistencyOptions{Evidence: consistencyEvidence(b, "synthetic duplicate unique key")})
	if err != nil || !p.Complete || p.Passed || len(p.Issues) != 1 || p.Issues[0].Problem.Code != ProblemIndexUnique || p.Indexes[0].Missing != 0 {
		t.Fatal("unique contradiction must block missing inference", p, err)
	}
	b, table, indexes, _, _ = consistencyFixture(t, "tiny")
	local, err = TraceSecondaryPage(bytes.NewReader(b), int64(len(b)), indexes[0].RootPage, indexes[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range local.Records[:2] {
		b[int(r.PageNumber)*PageSize+r.Offset-5] |= 0x20
	}
	resealTestPages(b)
	p, err = CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: consistencyEvidence(b, "synthetic two missing entries"), MaxIssues: 1})
	if !errors.Is(err, ErrLimit) || p.Complete || p.Passed || !p.Truncated || len(p.Issues) != 1 || p.Indexes[0].State != CheckIncomplete {
		t.Fatal("issue cap", p, err)
	}
}

func TestConsistencyUnselectedLOB(t *testing.T) {
	for _, name := range []string{"prefixes", "huge"} {
		b, table, index := secondaryQueryFixture(t, "secondary_query", name, "s")
		p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, []SecondarySchema{index}, ConsistencyOptions{Evidence: consistencyEvidence(b, "secondary_query/manifest.json and writer.sql.gz")})
		if err != nil || !p.Complete || !p.Passed {
			t.Fatal("unselected LOB must not be typed", p, err)
		}
		if name == "huge" {
			local, err := ReadClusteredPage(bytes.NewReader(b), int64(len(b)), table.RootPage, table)
			if err != nil {
				t.Fatal(err)
			}
			page := local.Records[0].External[0].FirstPage
			b[int(page)*PageSize+200] ^= 1 // Genuine CRC damage in an unselected LOB.
			p, err = CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, []SecondarySchema{index}, ConsistencyOptions{Evidence: consistencyEvidence(b, "synthetic unselected LOB fault")})
			if err != nil || !p.Passed {
				t.Fatal("hashing bytes does not certify unselected LOB CRC", p, err)
			}
		}
	}
}

func TestConsistencyCumulativeAndInputs(t *testing.T) {
	b, table, indexes, _, _ := consistencyFixture(t, "overlap")
	e := consistencyEvidence(b, "original overlap export")
	base, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e})
	if err != nil || !base.Passed {
		t.Fatal(base, err)
	}
	for _, o := range []ConsistencyOptions{
		{ScanOptions: ScanOptions{MaxPageReads: base.PageReads - 1}}, {ScanOptions: ScanOptions{MaxEntries: base.TraversalEntries - 1}},
		{ScanOptions: ScanOptions{MaxRows: 2 * base.Indexes[0].ClusteredRows}}, {MaxBufferedBytes: base.PeakBufferedBytes - 1},
	} {
		o.Evidence = e
		p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, o)
		if !errors.Is(err, ErrLimit) || p.Complete || !p.Truncated {
			t.Fatal("cumulative cap", p, err)
		}
	}
	for _, defs := range [][]SecondarySchema{nil, {indexes[0], indexes[0]}} {
		p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, defs, ConsistencyOptions{Evidence: e})
		if !errors.Is(err, ErrUnsupported) || p.Complete || p.Passed {
			t.Fatal("invalid selection", p, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	r := &diagnosticIOReader{data: b, cancel: cancel}
	p, err := CheckIndexConsistency(ctx, r, int64(len(b)), table, indexes, ConsistencyOptions{Evidence: e})
	if !errors.Is(err, context.Canceled) || p.Complete || p.Passed {
		t.Fatal("mid-read cancellation", p, err)
	}
	// A structural fault in one index must not report absent rows from its
	// truncated walk, and must not prevent checking an independent index.
	b[int(indexes[0].RootPage)*PageSize+200] ^= 1
	p, err = CheckIndexConsistency(t.Context(), bytes.NewReader(b), int64(len(b)), table, indexes, ConsistencyOptions{Evidence: consistencyEvidence(b, "synthetic one-index CRC fault")})
	if err != nil || !p.Complete || p.Passed || p.Indexes[0].State != CheckFailed || p.Indexes[0].Missing != 0 || p.Indexes[1].State != CheckPassed {
		t.Fatal("independent scope", p, err)
	}
}

func FuzzConsistency(f *testing.F) {
	b, table, indexes, _, _ := consistencyFixture(f, "tiny")
	f.Add(uint32(4*PageSize+150), byte(1), true)
	f.Add(uint32(0), byte(255), false)
	f.Fuzz(func(t *testing.T, offset uint32, mask byte, reseal bool) {
		x := bytes.Clone(b)
		x[int(offset)%len(x)] ^= mask
		if reseal {
			resealTestPages(x)
		}
		p, err := CheckIndexConsistency(t.Context(), bytes.NewReader(x), int64(len(x)), table, indexes, ConsistencyOptions{Evidence: consistencyEvidence(x, "synthetic fuzz copy"), ScanOptions: ScanOptions{MaxPageReads: 100, MaxEntries: 2000, MaxRows: 500}, MaxIssues: 10, MaxBufferedBytes: 100000})
		if p.Passed && (!p.Complete || err != nil || len(p.Issues) != 0 || p.Preconditions.State != CheckPassed) || len(p.Issues) > 10 || p.PeakBufferedBytes > 100000 || errors.Is(err, ErrLimit) && (!p.Truncated || p.Complete) {
			t.Fatal("completion/budget invariants", p, err)
		}
		for _, issue := range p.Issues {
			loc := issue.Problem.Location
			if loc.Bytes != nil && (loc.Page == nil || loc.Bytes.Start < 0 || loc.Bytes.End > PageSize || loc.Bytes.Start > loc.Bytes.End) {
				t.Fatal("source span", issue)
			}
		}
	})
}
