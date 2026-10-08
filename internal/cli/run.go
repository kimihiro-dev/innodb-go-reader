// Package cli implements the offline command without global flags or process exits.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	innodb "innodb-go-reader"
	"innodb-go-reader/rowio"
	"innodb-go-reader/visual"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const usage = "usage: innodb-reader <metadata|page|export|query|check|space|visualize> [flags] file.ibd\nPartition metadata/export/check: --manifest files.json instead of file.ibd.\nUse <command> --help for flags. Flags must precede file.ibd.\n"

type summary struct {
	Command        string `json:"command"`
	Scope          string `json:"scope"`
	Complete       bool   `json:"complete"`
	PreflightReads uint64 `json:"preflight_reads,omitempty"`
	Report         any    `json:"report,omitempty"`
	Error          string `json:"error,omitempty"`
}
type config struct {
	visual                                                       visual.Options
	rawPages                                                     string
	command, path, output, format, index, query, scope, manifest string
	overwrite, physical, materialized                            bool
	number                                                       uint64
	scan                                                         innodb.ScanOptions
	space                                                        innodb.SpaceOptions
}

func parse(args []string, stderr io.Writer) (config, bool, error) {
	var c config
	if len(args) == 0 {
		return c, false, fmt.Errorf("command required")
	}
	if args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(contextWriter{context.Background(), stderr}, usage)
		return c, true, err
	}
	c.command = args[0]
	f := flag.NewFlagSet(c.command, flag.ContinueOnError)
	diagnostics := &diagnosticWriter{w: stderr}
	f.SetOutput(diagnostics)
	f.Usage = func() { fmt.Fprint(diagnostics, usage); f.PrintDefaults() }
	f.StringVar(&c.output, "output", "", "atomically publish to file (default stdout)")
	f.BoolVar(&c.overwrite, "overwrite", false, "replace an existing output file")
	if c.command == "metadata" || c.command == "export" || c.command == "check" {
		f.StringVar(&c.manifest, "manifest", "", "complete partition file manifest; replaces file.ibd")
	}
	scanFlags := func() {
		f.BoolVar(&c.materialized, "materialized", false, "explicitly read stored columns when VIRTUAL columns exist")
		f.IntVar(&c.scan.CachePages, "cache-pages", 64, "cached pages; -1 disables")
		f.Uint64Var(&c.scan.MaxPageReads, "max-page-reads", 1000000, "page request budget including preflight")
		f.Uint64Var(&c.scan.MaxEntries, "max-entries", 1000000, "cumulative scan traversal budget")
		f.Uint64Var(&c.scan.MaxRows, "max-rows", 1000000, "delivered row budget")
		f.Uint64Var(&c.scan.MaxRowBytes, "max-row-bytes", 64<<20, "source bytes per row")
	}
	spaceFlags := func() {
		defaultPages := uint64(1000000)
		if c.command == "visualize" {
			defaultPages = 100000
		}
		f.Uint64Var(&c.space.MaxPages, "max-pages", defaultPages, "input file page budget")
		f.Uint64Var(&c.space.MaxEntries, "space-max-entries", 10000000, "space structure traversal budget")
	}
	switch c.command {
	case "export", "query":
		scanFlags()
		f.StringVar(&c.format, "format", "jsonl", "jsonl or csv (typed lossless cells)")
		f.BoolVar(&c.physical, "physical", false, "include record provenance")
		if c.command == "query" {
			f.StringVar(&c.query, "query", "", "required range JSON file")
			f.StringVar(&c.index, "index", "", "secondary index name; otherwise clustered")
		}
	case "metadata":
		f.Uint64Var(&c.scan.MaxPageReads, "max-page-reads", 1000000, "metadata page request budget")
	case "visualize":
		scanFlags()
		spaceFlags()
		f.StringVar(&c.visual.Index, "index", "", "exact index name for complete tree/detail validation")
		f.StringVar(&c.rawPages, "pages", "", "comma-separated page numbers to embed (at most 256)")
		f.StringVar(&c.visual.ManualDir, "manual-dir", "", "local format manual directory")
		f.Uint64Var(&c.visual.MaxReadCalls, "max-read-calls", 1000000, "cumulative underlying source reads")
		f.Uint64Var(&c.visual.MaxDetails, "max-details", 10000, "selected record detail budget")
		f.Uint64Var(&c.visual.MaxReportBytes, "max-report-bytes", 64<<20, "embedded JSON byte budget")
	case "space":
		spaceFlags()
	case "page":
		spaceFlags()
		f.Uint64Var(&c.number, "number", 0, "required zero-based page number; checks whole space")
	case "check":
		scanFlags()
		spaceFlags()
		f.StringVar(&c.scope, "scope", "rows", "rows, space, or secondary")
		f.StringVar(&c.index, "index", "", "required for secondary scope")
	default:
		return c, false, fmt.Errorf("unknown command %q", c.command)
	}
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return c, true, diagnostics.err
		}
		return c, false, err
	}
	if c.manifest != "" {
		if f.NArg() != 0 {
			return c, false, fmt.Errorf("--manifest and file.ibd are mutually exclusive")
		}
		if c.command == "check" && c.scope != "rows" {
			return c, false, fmt.Errorf("partition check supports only rows scope")
		}
	} else {
		if f.NArg() != 1 {
			return c, false, fmt.Errorf("one input ibd or --manifest is required")
		}
		c.path = f.Arg(0)
	}
	if c.command == "visualize" {
		if c.visual.Index == "" {
			invalid := ""
			f.Visit(func(v *flag.Flag) {
				switch v.Name {
				case "cache-pages", "max-page-reads", "max-entries", "max-rows", "max-row-bytes":
					invalid = v.Name
				}
			})
			if invalid != "" {
				return c, false, fmt.Errorf("--%s requires --index", invalid)
			}
		}
		if c.materialized && c.visual.Index == "" {
			return c, false, fmt.Errorf("--materialized requires --index")
		}
		if c.rawPages != "" {
			parts := strings.Split(c.rawPages, ",")
			if len(parts) > 256 {
				return c, false, fmt.Errorf("at most 256 selected pages")
			}
			seen := map[uint32]bool{}
			for _, raw := range parts {
				n, err := strconv.ParseUint(raw, 10, 32)
				if err != nil || seen[uint32(n)] {
					return c, false, fmt.Errorf("invalid/duplicate --pages")
				}
				seen[uint32(n)] = true
				c.visual.Pages = append(c.visual.Pages, uint32(n))
			}
		}
		if c.visual.ManualDir != "" {
			var err error
			c.visual.ManualDir, err = filepath.Abs(c.visual.ManualDir)
			if err != nil {
				return c, false, err
			}
		}
	}
	if c.overwrite && c.output == "" {
		return c, false, fmt.Errorf("--overwrite requires --output")
	}
	if (c.command == "export" || c.command == "query") && c.format != "jsonl" && c.format != "csv" {
		return c, false, fmt.Errorf("format must be jsonl or csv")
	}
	if c.command == "query" && c.query == "" {
		return c, false, fmt.Errorf("--query is required")
	}
	if c.scan.CachePages < -1 {
		return c, false, fmt.Errorf("invalid cache-pages")
	}
	if c.command == "page" {
		present := false
		f.Visit(func(v *flag.Flag) {
			if v.Name == "number" {
				present = true
			}
		})
		if !present {
			return c, false, fmt.Errorf("--number is required")
		}
	}
	if c.command == "check" {
		if c.scope != "rows" && c.scope != "space" && c.scope != "secondary" {
			return c, false, fmt.Errorf("unknown check scope")
		}
		if (c.scope == "secondary") != (c.index != "") {
			return c, false, fmt.Errorf("--index is required only for secondary scope")
		}
		invalid := ""
		f.Visit(func(v *flag.Flag) {
			spaceFlag := v.Name == "max-pages" || v.Name == "space-max-entries"
			rowFlag := v.Name == "materialized" || v.Name == "cache-pages" || v.Name == "max-page-reads" || v.Name == "max-entries" || v.Name == "max-rows" || v.Name == "max-row-bytes"
			if c.scope == "space" && rowFlag || c.scope != "space" && spaceFlag || c.scope == "secondary" && v.Name == "materialized" {
				invalid = v.Name
			}
		})
		if invalid != "" {
			return c, false, fmt.Errorf("--%s does not apply to %s scope", invalid, c.scope)
		}
	}
	return c, false, nil
}

// Run returns 0 on success, 2 for usage, 1 for runtime/I/O failure and 130 for
// cancellation. A successful API report does not override an output failure.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c, help, err := parse(args, stderr)
	if help {
		if err != nil {
			return 1
		}
		return 0
	}
	s := summary{Command: c.command, Scope: c.scope}
	code := 0
	if err != nil {
		code = 2
	} else {
		if c.scope == "" {
			switch c.command {
			case "export":
				s.Scope = "current stored rows"
			case "query":
				s.Scope = "requested range/limit, visited paths"
			case "metadata":
				s.Scope = "metadata discovery"
			case "visualize":
				s.Scope = "space; explicitly selected index and page details"
			default:
				s.Scope = "whole space allocation and supported page structures"
			}
		}
		var q innodb.SecondaryQuery
		if c.command == "query" {
			q, err = readQuery(c.query, c.index != "")
			if err != nil {
				code = 2
				var pathError *os.PathError
				if errors.As(err, &pathError) {
					code = 1
				}
			}
		}
		if err == nil {
			if c.manifest != "" {
				s.Scope = "complete partition collection, definition order"
				err = executePartitions(ctx, c, stdout, &s)
				var formatErr *manifestError
				if errors.As(err, &formatErr) {
					code = 2
				}
			} else {
				err = execute(ctx, c, q, stdout, &s)
			}
		}
		if err != nil && code == 0 {
			code = 1
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				code = 130
			}
		}
	}
	s.Complete = err == nil
	if err != nil {
		s.Error = err.Error()
	}
	if e := json.NewEncoder(contextWriter{context.Background(), stderr}).Encode(s); e != nil && code == 0 {
		return 1
	}
	return code
}

type budgetReader struct {
	ctx        context.Context
	r          io.ReaderAt
	reads, max uint64
}

func (r *budgetReader) ReadAt(b []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.reads >= r.max {
		return 0, fmt.Errorf("%w: CLI page reads", innodb.ErrLimit)
	}
	r.reads++
	return r.r.ReadAt(b, off)
}
func execute(ctx context.Context, c config, q innodb.SecondaryQuery, stdout io.Writer, s *summary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(c.path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("input must be a regular stable snapshot")
	}
	max := c.scan.MaxPageReads
	if max == 0 {
		max = 1000000
	}
	r := &budgetReader{ctx: ctx, r: f, max: max}
	return output(ctx, c.output, c.overwrite, []string{c.path, c.query}, stdout, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		switch c.command {
		case "visualize":
			o := c.visual
			o.Title = filepath.Base(c.path)
			o.Space = c.space
			o.Scan = c.scan
			o.Materialized = c.materialized
			report, err := visual.Build(ctx, f, st.Size(), o)
			if err != nil {
				return err
			}
			s.Report = struct {
				Pages, ReadCalls  uint64
				Index             string
				RawPages, Details int
			}{report.Space.FilePages, report.ReadCalls, report.Index, len(report.RawPages), len(report.Details)}
			return visual.WriteHTML(ctx, w, report)
		case "metadata":
			m, err := innodb.InspectTable(r, st.Size())
			s.PreflightReads = r.reads
			if err != nil {
				return err
			}
			return enc.Encode(m)
		case "space", "page":
			report, err := innodb.AnalyzeSpace(ctx, f, st.Size(), c.space)
			if err != nil {
				return err
			}
			if c.command == "page" {
				if c.number >= uint64(len(report.Pages)) {
					return fmt.Errorf("page outside file")
				}
				return enc.Encode(report.Pages[c.number])
			}
			return enc.Encode(report)
		case "check":
			switch c.scope {
			case "space":
				report, err := innodb.AnalyzeSpace(ctx, f, st.Size(), c.space)
				if err != nil {
					return err
				}
				return enc.Encode(report)
			case "secondary":
				report, err := innodb.ScanSecondaryAuto(ctx, r, st.Size(), c.index, c.scan, func(innodb.SecondaryEvent) error { return nil })
				s.Report = report
				if err != nil {
					return err
				}
				return enc.Encode(report)
			default:
				scan := innodb.ScanAuto
				if c.materialized {
					scan = innodb.ScanMaterializedAuto
				}
				report, err := scan(ctx, r, st.Size(), c.scan, func(innodb.ScanEvent) error { return nil })
				s.Report = report
				if err != nil {
					return err
				}
				return enc.Encode(report)
			}
		}
		// Schema precedes rows. Charge its uncached reads before starting the API's
		// shared cache/request budget; SDI retains its independent capacity limits.
		meta, err := innodb.InspectTable(r, st.Size())
		s.PreflightReads = r.reads
		if err != nil {
			return err
		}
		schema := meta.Schema
		if c.materialized {
			schema = meta.MaterializedSchema
		}
		if schema == nil {
			return fmt.Errorf("%w: %v", innodb.ErrUnsupported, meta.Issues)
		}
		columns := schema.Columns
		if c.index != "" && q.Columns != nil {
			if len(q.Columns) == 0 {
				return fmt.Errorf("empty projection")
			}
			columns = nil
			seen := map[string]bool{}
			for _, name := range q.Columns {
				found := false
				for _, col := range schema.Columns {
					if col.Name == name && !seen[name] {
						columns = append(columns, col)
						found = true
						seen[name] = true
						break
					}
				}
				if !found {
					return fmt.Errorf("unknown or duplicate projection %q", name)
				}
			}
		}
		if r.reads >= max {
			return fmt.Errorf("%w: preflight exhausted page budget", innodb.ErrLimit)
		}
		c.scan.MaxPageReads = max - r.reads
		rows, err := rowio.NewEncoder(w, c.format, rowio.Header{Version: rowio.Version, Columns: columns, VirtualColumns: schema.VirtualColumns, Physical: c.physical}, rowio.Options{})
		if err != nil {
			return err
		}
		emit := func(values []any, physical any) error {
			row := rowio.Row{Values: values}
			if c.physical {
				row.Physical, err = physicalJSON(physical)
				if err != nil {
					return err
				}
			}
			return rows.Write(row)
		}
		yield := func(e innodb.ScanEvent) error {
			if e.Record == nil {
				return nil
			}
			copy := *e.Record
			copy.Values = nil
			return emit(e.Record.Values, copy)
		}
		if c.command == "export" {
			scan := innodb.Scan
			if c.materialized {
				scan = innodb.ScanMaterialized
			}
			report, e := scan(ctx, r, st.Size(), *schema, c.scan, yield)
			s.Report = report
			err = e
		} else if c.index == "" {
			query := innodb.Query
			if c.materialized {
				query = innodb.QueryMaterialized
			}
			report, e := query(ctx, r, st.Size(), *schema, q.Range, c.scan, yield)
			s.Report = report
			err = e
		} else {
			query := innodb.QuerySecondaryAuto
			if c.materialized {
				query = innodb.QuerySecondaryMaterializedAuto
			}
			report, e := query(ctx, r, st.Size(), c.index, q, c.scan, func(p innodb.ProjectedRow) error { copy := p; copy.Values = nil; return emit(p.Values, copy) })
			s.Report = report
			err = e
		}
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return rows.Finish()
	})
}

// Keep provenance separate from the selected values; never emit a Values:null
// placeholder that could be mistaken for an absent projection.
func physicalJSON(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	delete(fields, "Values")
	return json.Marshal(fields)
}

// flag.Usage cannot return errors, so retain failures while rendering help.
type diagnosticWriter struct {
	w   io.Writer
	err error
}

func (d *diagnosticWriter) Write(b []byte) (int, error) {
	n, err := (contextWriter{context.Background(), d.w}).Write(b)
	if err != nil {
		d.err = err
	}
	return n, err
}
