package innodb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
)

// CheckState distinguishes failed input, missing prerequisites and interrupted work.
type CheckState string

const (
	CheckPassed       CheckState = "passed"
	CheckFailed       CheckState = "failed"
	CheckUnsupported  CheckState = "unsupported"
	CheckBlocked      CheckState = "blocked"
	CheckIncomplete   CheckState = "incomplete"
	CheckNotRequested CheckState = "not_requested"
)

// DiagnosticOptions shares scan budgets across every phase, including repeated
// verification. Secondary is the explicitly selected set, not an exhaustive list.
type DiagnosticOptions struct {
	ScanOptions         ScanOptions
	MaxPages, MaxIssues uint64 // defaults 1,000,000 pages and 1,000 problems
	Secondary           []SecondarySchema
	SkipLOB             bool // default false: validate current clustered LOB types too
}

// DiagnosticCheck describes one scope. Checked counts attempts/visited pages or
// references, not passes; a failed/incomplete scope never certifies its prefix.
type DiagnosticCheck struct {
	Layer, Scope string
	IndexID      *uint64 `json:",omitempty"`
	State        CheckState
	Checked      uint64
	Reason       string `json:",omitempty"`
}

// DiagnosticReport contains no importable rows. Complete means execution ended
// without operational interruption; Passed concerns only requested checks.
// Unverified remains outside that assertion, even when Passed is true.
type DiagnosticReport struct {
	Complete, Passed, Truncated                           bool
	Problems                                              []*Problem
	Checks                                                []DiagnosticCheck
	Unverified                                            []string
	FilePages, UsedPages, SkippedPages, RecordsChecked    uint64
	PageReads, CacheHits, PhysicalReads, TraversalEntries uint64
}

type diagnostic struct {
	reader      *scanReader
	size        int64
	options     DiagnosticOptions
	report      DiagnosticReport
	operational error
}

// Diagnose checks one stable file with trusted table/optional secondary schemas.
// Corrupt/unsupported findings are Problems, not a successful-looking table.
// I/O, cancellation and limits return a report and error with Complete=false.
func Diagnose(ctx context.Context, r io.ReaderAt, size int64, schema Schema, options DiagnosticOptions) (DiagnosticReport, error) {
	if options.MaxPages == 0 {
		options.MaxPages = 1_000_000
	}
	if options.MaxIssues == 0 {
		options.MaxIssues = 1_000
	}
	d := diagnostic{size: size, options: options}
	d.report.Unverified = []string{"transaction visibility and snapshot provenance", "secondary/clustered correspondence", "indexes without supplied definitions", "VIRTUAL expressions", "historical row/LOB values"}
	d.report.Checks = []DiagnosticCheck{
		{Layer: "input", Scope: "aligned addressable file geometry"},
		{Layer: "schema", Scope: "trusted definitions and selected roots; not SQL provenance"},
		{Layer: "pages", Scope: "FSP/XDES and all allocated used page envelopes/FIL identities"},
		{Layer: "layout", Scope: "allocated INDEX/SDI compact headers, chains and directory owners"},
		{Layer: "allocation", Scope: "complete FSP/XDES/INODE/segments and allocation-level index chains"},
	}
	for _, id := range append([]uint64{schema.IndexID}, diagnosticIndexIDs(options.Secondary)...) {
		d.report.Checks = append(d.report.Checks,
			DiagnosticCheck{Layer: "values", IndexID: diagnosticPtr(id), Scope: "all allocated pages of selected index; local values"},
			DiagnosticCheck{Layer: "tree", IndexID: diagnosticPtr(id), Scope: "selected complete tree navigation and allocated page coverage"})
	}
	lob := DiagnosticCheck{Layer: "lob", IndexID: diagnosticPtr(schema.IndexID), Scope: "current live clustered references and typed values"}
	if options.SkipLOB {
		lob.State = CheckNotRequested
	}
	d.report.Checks = append(d.report.Checks, lob)
	reader, err := newScanReader(ctx, r, options.ScanOptions)
	if err == nil {
		d.reader = reader
		err = d.run(schema)
		d.report.PageReads, d.report.CacheHits, d.report.PhysicalReads = reader.requests, reader.hits, reader.reads
		d.report.TraversalEntries = reader.entries
	}
	err = errors.Join(err, d.operational)
	d.report.Complete = err == nil
	d.report.Truncated = errors.Is(err, ErrLimit)
	d.report.Passed = err == nil
	for i := range d.report.Checks {
		c := &d.report.Checks[i]
		if c.State == "" {
			c.State, c.Reason = CheckBlocked, "prerequisite unavailable"
			if err != nil {
				c.State, c.Reason = CheckIncomplete, "execution interrupted before this check"
			}
		}
		if c.State != CheckPassed && c.State != CheckNotRequested {
			d.report.Passed = false
		}
	}
	return d.report, err
}

func diagnosticIndexIDs(indexes []SecondarySchema) []uint64 {
	ids := make([]uint64, len(indexes))
	for i, index := range indexes {
		ids[i] = index.IndexID
	}
	return ids
}

func (d *diagnostic) check(layer string, id uint64) *DiagnosticCheck {
	for i := range d.report.Checks {
		c := &d.report.Checks[i]
		if c.Layer == layer && (c.IndexID == nil || *c.IndexID == id) {
			return c
		}
	}
	panic("missing diagnostic check")
}

func diagnosticBlock(c *DiagnosticCheck, reason string) {
	if c.State == "" || c.State == CheckPassed {
		c.State, c.Reason = CheckBlocked, reason
	}
}

func (d *diagnostic) fail(c *DiagnosticCheck, err error, code ProblemCode, location ProblemLocation) error {
	if diagnosticControl(err) {
		c.State, c.Reason = CheckIncomplete, err.Error()
		return err
	}
	wrapped := problemAt(err, code, location, c.Scope, err.Error())
	var p *Problem
	errors.As(wrapped, &p)
	state := CheckFailed
	if p.Category == "unsupported" {
		state = CheckUnsupported
	} else if p.Category == "io" {
		state = CheckIncomplete
		d.operational = errors.Join(d.operational, wrapped)
	}
	if c.State != CheckFailed {
		c.State = state
	}
	for _, old := range d.report.Problems {
		if old.Code == p.Code && reflect.DeepEqual(old.Location, p.Location) {
			return nil
		}
	}
	if uint64(len(d.report.Problems)) >= d.options.MaxIssues {
		c.State, c.Reason = CheckIncomplete, "problem retention budget exhausted"
		return fmt.Errorf("%w: diagnostic problems", ErrLimit)
	}
	d.report.Problems = append(d.report.Problems, p)
	return nil
}

func (d *diagnostic) run(schema Schema) error {
	input, definitions := d.check("input", 0), d.check("schema", 0)
	pages, layout, allocation := d.check("pages", 0), d.check("layout", 0), d.check("allocation", 0)
	input.Checked = 1
	if d.size < PageSize || d.size%PageSize != 0 || uint64(d.size/PageSize) > uint64(^uint32(0)) {
		return d.fail(input, newProblem(fmt.Errorf("%w: diagnostic file size %d", ErrCorrupt, d.size), ProblemGeometry, ProblemLocation{}, "aligned addressable 16 KiB file", fmt.Sprint(d.size)), ProblemGeometry, ProblemLocation{})
	}
	d.report.FilePages = uint64(d.size / PageSize)
	if d.report.FilePages > d.options.MaxPages {
		return d.fail(input, fmt.Errorf("%w: diagnostic file pages", ErrLimit), ProblemGeometry, ProblemLocation{})
	}
	input.State, definitions.State = CheckPassed, CheckPassed
	definitions.Checked++
	schemaErr := validateSecondaryDefinitions(schema, d.options.Secondary)
	if schemaErr != nil {
		if err := d.fail(definitions, newProblem(schemaErr, ProblemSchema, ProblemLocation{}, "valid trusted definitions", schemaErr.Error()), ProblemSchema, ProblemLocation{}); err != nil {
			return err
		}
	}
	a := &spaceAnalysis{ctx: d.reader.ctx, r: d.reader, size: d.size, options: SpaceOptions{MaxPages: d.options.MaxPages, MaxEntries: d.reader.options.MaxEntries}, out: &SpaceReport{FilePages: d.report.FilePages}, metadata: map[uint32][]byte{}, extent: map[lobAddress]int{}}
	pages.State, layout.State = CheckPassed, CheckPassed
	pages.Checked++
	b, err := a.read(0)
	if err == nil {
		err = a.descriptors(b)
	}
	pages.Checked = a.out.PhysicalReads
	if err != nil {
		var p *Problem
		c := allocation
		if errors.As(err, &p) && (p.Code == ProblemChecksum || p.Code == ProblemLSN || p.Code == ProblemZeroPage || p.Code == ProblemPageRead || p.Code == ProblemPageIdentity) {
			c = pages
		}
		if c != pages {
			diagnosticBlock(pages, "valid FSP/XDES descriptors required to identify used pages")
		}
		diagnosticBlock(layout, "valid FSP/XDES descriptors required")
		return d.fail(c, err, ProblemAllocation, ProblemLocation{})
	}
	for n := uint32(0); uint64(n) < d.report.FilePages; n++ {
		if n >= a.out.SizePages || n >= a.out.FreeLimit || a.out.Extents[n/64].FreeBits&(uint64(1)<<(n%64)) != 0 {
			d.report.SkippedPages++
			continue
		}
		d.report.UsedPages++
		pages.Checked++
		if err := scanEntries(d.reader, 1); err != nil {
			return d.fail(pages, err, ProblemPageRead, ProblemLocation{Page: &n})
		}
		raw, err := readPage(d.reader, d.size, n)
		if err == nil {
			err = a.verified(raw, n)
		}
		if err != nil {
			diagnosticBlock(layout, "some used page envelopes unavailable")
			if err := d.fail(pages, err, ProblemPageIdentity, ProblemLocation{Page: &n}); err != nil {
				return err
			}
			continue
		}
		kind := be.Uint16(raw[24:])
		if kind != 17855 && kind != 17853 {
			continue
		}
		layout.Checked++
		p, err := parseIndexTypeMinimum(raw, Schema{SpaceID: a.out.SpaceID, IndexID: be.Uint64(raw[66:])}, kind, 6)
		if err == nil {
			_, _, err = a.indexLayout(raw, p)
		}
		if err != nil {
			if err := d.fail(layout, err, ProblemRecordLayout, ProblemLocation{Page: &n}); err != nil {
				return err
			}
		}
	}
	if pages.State != CheckPassed || layout.State != CheckPassed {
		diagnosticBlock(allocation, "used page envelopes/local layouts did not pass")
		return nil
	}
	allocation.Checked++
	space, err := AnalyzeSpace(d.reader.ctx, d.reader, d.size, a.options)
	if err != nil {
		return d.fail(allocation, err, ProblemAllocation, ProblemLocation{})
	}
	allocation.State = CheckPassed
	if schemaErr != nil {
		return nil
	}
	if err := d.index(space, schema, nil); err != nil {
		return err
	}
	for i := range d.options.Secondary {
		if err := d.index(space, schema, &d.options.Secondary[i]); err != nil {
			return err
		}
	}
	return d.reader.ctx.Err()
}

func (d *diagnostic) index(space *SpaceReport, schema Schema, secondary *SecondarySchema) error {
	id, root, minimum := schema.IndexID, schema.RootPage, 10
	var columns []Column
	if secondary != nil {
		id, root, minimum = secondary.IndexID, secondary.RootPage, 6
		columns, _ = secondary.validate()
	}
	values, tree := d.check("values", id), d.check("tree", id)
	values.Scope = "allocated clustered pages; keys and non-delete-mark local columns"
	if secondary != nil {
		values.Scope = "allocated secondary pages; physical fields/prefixes, no table lookup"
	}
	lob := d.check("lob", schema.IndexID)
	d.check("schema", 0).Checked++
	var err error
	if uint64(root) >= uint64(len(space.Pages)) || space.Pages[root].Allocation != "used" {
		actual := "outside file"
		if uint64(root) < uint64(len(space.Pages)) {
			actual = space.Pages[root].Allocation
		}
		err = newProblem(fmt.Errorf("%w: selected root %d is not allocated", ErrCorrupt, root), ProblemCoverage, ProblemLocation{Page: &root, IndexID: &id}, "allocated used root", actual)
	} else if secondary == nil {
		err = ValidateSchema(d.reader, d.size, schema)
	} else {
		err = ValidateSecondarySchemas(d.reader, d.size, schema, []SecondarySchema{*secondary})
	}
	if err != nil {
		diagnosticBlock(values, "selected root precheck failed")
		diagnosticBlock(tree, "selected root precheck failed")
		if secondary == nil {
			diagnosticBlock(lob, "selected root precheck failed")
		}
		return d.fail(d.check("schema", 0), err, ProblemSchema, ProblemLocation{IndexID: &id})
	}
	values.State = CheckPassed
	if secondary == nil && !d.options.SkipLOB {
		lob.State = CheckPassed
	}
	pk, _ := schema.validate()
	allocated := map[uint32]bool{}
	for _, page := range space.Pages {
		if page.Allocation != "used" || page.Type != 17855 || page.Index == nil || page.Index.IndexID != id {
			continue
		}
		allocated[page.Number] = true
		values.Checked++
		if err := scanEntries(d.reader, uint64(page.Index.Records)+1); err != nil {
			return d.fail(values, err, ProblemRecordLayout, ProblemLocation{Page: &page.Number, IndexID: &id})
		}
		raw, err := readPage(d.reader, d.size, page.Number)
		var entries []pageEntry
		if err == nil {
			p, e := parseIndexMinimum(raw, Schema{SpaceID: schema.SpaceID, IndexID: id}, minimum)
			err = e
			if err == nil {
				if secondary == nil {
					entries, err = decodePage(raw, p, schema, pk)
				} else {
					entries, err = decodePageEntries(raw, p, func(origin, limit int) (pageEntry, error) {
						return decodeSecondary(raw, origin, limit, columns, secondary.Fields, p.Level > 0)
					}, func(a, b pageEntry) bool { return compareSecondary(a.order, b.order, columns) < 0 })
				}
			}
		}
		if err != nil {
			if secondary == nil {
				diagnosticBlock(lob, "some local records could not be decoded")
			}
			if err := d.fail(values, err, ProblemRecordLayout, ProblemLocation{Page: &page.Number, IndexID: &id}); err != nil {
				return err
			}
			continue
		}
		if page.Index.Level != 0 {
			continue
		}
		for _, entry := range entries {
			if entry.deleteMarked {
				continue
			}
			budgetRecord := entry.Record
			if d.options.SkipLOB {
				budgetRecord.External = nil
			}
			if err := scanRow(d.reader, budgetRecord, schema); err != nil {
				return d.fail(values, err, ProblemFieldValue, ProblemLocation{Page: &page.Number, IndexID: &id, RecordPage: &page.Number, RecordOffset: &entry.Offset})
			}
			d.report.RecordsChecked++
			if secondary != nil || d.options.SkipLOB {
				continue
			}
			for _, field := range entry.External {
				lob.Checked++
				location := externalProblemLocation(entry.Record, schema, field)
				data, err := readExternal(d.reader, d.size, &field)
				if err != nil {
					if err := d.fail(lob, err, ProblemLOBChain, location); err != nil {
						return err
					}
					continue
				}
				if _, err := variableValue(schema.Columns[field.Column], data); err != nil {
					if err := d.fail(lob, newProblem(err, ProblemLOBValue, location, schema.Columns[field.Column].Type, byteObservation(data)), ProblemLOBValue, location); err != nil {
						return err
					}
				}
			}
		}
	}
	if values.State != CheckPassed {
		diagnosticBlock(tree, "selected index local values/layouts did not pass")
		return nil
	}
	visited := map[uint32]bool{}
	visit := func(p *Page) error {
		if p != nil {
			visited[p.Number] = true
			tree.Checked++
			if !allocated[p.Number] {
				return newProblem(fmt.Errorf("%w: visited page %d is not allocated to selected index", ErrCorrupt, p.Number), ProblemCoverage, ProblemLocation{Page: &p.Number, IndexID: &id}, "used page of selected index", "unallocated or different index")
			}
		}
		return nil
	}
	schema.VirtualColumns = nil
	if secondary == nil {
		selection, _ := prepareQuery(KeyRange{}, schema, pk)
		selection.deferValues, selection.fullScan = true, true
		err = walkSelectedTree(d.reader, d.size, schema, func(e ScanEvent) error { return visit(e.Page) }, selection)
	} else {
		selection, _ := prepareSecondaryRange(KeyRange{}, *secondary)
		selection.fullScan = true
		err = walkSelectedSecondary(d.reader, d.size, *secondary, func(e SecondaryEvent) error { return visit(e.Page) }, selection)
	}
	if err != nil {
		return d.fail(tree, err, ProblemNavigation, ProblemLocation{IndexID: &id})
	}
	tree.State = CheckPassed
	for _, page := range space.Pages {
		if allocated[page.Number] && !visited[page.Number] {
			err := newProblem(fmt.Errorf("%w: allocated index page %d unreachable from root %d", ErrCorrupt, page.Number, root), ProblemCoverage, ProblemLocation{Page: &page.Number, IndexID: &id}, "reachable from selected root", "allocated but unvisited")
			if err := d.fail(tree, err, ProblemCoverage, err.Location); err != nil {
				return err
			}
		}
	}
	return nil
}
