package innodb

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
)

// ClusteredQuery selects a clustered range and an ordered projection. Columns=nil
// selects all stored columns; a non-nil list must contain distinct stored names.
type ClusteredQuery struct {
	Range   KeyRange
	Columns []string
}

// ProjectedRecord is distinct from a complete Record. Values contains only the
// report's Columns; all column-indexed metadata uses that same projected order.
// Unselected columns have no slot. External contains only selected, resolved LOBs.
// Physical record bounds/system fields remain those of the original whole record.
type ProjectedRecord Record

// ProjectedEvent contains exactly one non-nil field, in traversal order. Nodes
// and deleted records remain navigation/evidence, independent of selected columns.
type ProjectedEvent struct {
	Page          *Page
	Node          *NodePointer
	Record        *ProjectedRecord
	DeletedRecord *DeletedRecord
}

// ScanProjected scans the complete clustered tree, materializing selected values.
// It does not certify unselected off-page values. Callback prefixes survive errors.
func ScanProjected(ctx context.Context, r io.ReaderAt, size int64, schema Schema, columns []string, options ScanOptions, yield func(ProjectedEvent) error) (ScanReport, error) {
	p, err := projectRows(ctx, r, size, &schema, ClusteredQuery{Columns: columns}, options, yield, false, true)
	return p.ScanReport, err
}

func ScanProjectedAuto(ctx context.Context, r io.ReaderAt, size int64, columns []string, options ScanOptions, yield func(ProjectedEvent) error) (ScanReport, error) {
	p, err := projectRows(ctx, r, size, nil, ClusteredQuery{Columns: columns}, options, yield, false, true)
	return p.ScanReport, err
}

// ScanProjectedMaterialized separately reports VIRTUAL definitions, never values.
func ScanProjectedMaterialized(ctx context.Context, r io.ReaderAt, size int64, schema Schema, columns []string, options ScanOptions, yield func(ProjectedEvent) error) (ScanReport, error) {
	p, err := projectRows(ctx, r, size, &schema, ClusteredQuery{Columns: columns}, options, yield, true, true)
	return p.ScanReport, err
}

func ScanProjectedMaterializedAuto(ctx context.Context, r io.ReaderAt, size int64, columns []string, options ScanOptions, yield func(ProjectedEvent) error) (ScanReport, error) {
	p, err := projectRows(ctx, r, size, nil, ClusteredQuery{Columns: columns}, options, yield, true, true)
	return p.ScanReport, err
}

// QueryProjected visits a clustered range/limit and materializes selected columns.
// Complete certifies only the requested projection on the visited paths.
func QueryProjected(ctx context.Context, r io.ReaderAt, size int64, schema Schema, query ClusteredQuery, options ScanOptions, yield func(ProjectedEvent) error) (QueryReport, error) {
	return projectRows(ctx, r, size, &schema, query, options, yield, false, false)
}

func QueryProjectedAuto(ctx context.Context, r io.ReaderAt, size int64, query ClusteredQuery, options ScanOptions, yield func(ProjectedEvent) error) (QueryReport, error) {
	return projectRows(ctx, r, size, nil, query, options, yield, false, false)
}

// QueryProjectedMaterialized selects stored values and reports VIRTUAL separately.
func QueryProjectedMaterialized(ctx context.Context, r io.ReaderAt, size int64, schema Schema, query ClusteredQuery, options ScanOptions, yield func(ProjectedEvent) error) (QueryReport, error) {
	return projectRows(ctx, r, size, &schema, query, options, yield, true, false)
}

func QueryProjectedMaterializedAuto(ctx context.Context, r io.ReaderAt, size int64, query ClusteredQuery, options ScanOptions, yield func(ProjectedEvent) error) (QueryReport, error) {
	return projectRows(ctx, r, size, nil, query, options, yield, true, false)
}

// Shared with secondary projection: nil means all; an explicit empty list is invalid.
func prepareProjection(s Schema, columns []string) ([]int, error) {
	projection := []int{}
	if columns == nil {
		for i := range s.Columns {
			projection = append(projection, i)
		}
		return projection, nil
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: empty projection", ErrUnsupported)
	}
	used := map[int]bool{}
	for _, name := range columns {
		found := -1
		for i, c := range s.Columns {
			if c.Name == name {
				found = i
				break
			}
		}
		if found < 0 || used[found] {
			return nil, fmt.Errorf("%w: unknown/duplicate/unmaterialized projection %q", ErrUnsupported, name)
		}
		used[found] = true
		projection = append(projection, found)
	}
	return projection, nil
}

func projectRows(ctx context.Context, r io.ReaderAt, size int64, schema *Schema, q ClusteredQuery, options ScanOptions, yield func(ProjectedEvent) error, materialized, fullScan bool) (report QueryReport, err error) {
	if yield == nil {
		return report, fmt.Errorf("%w: nil projection callback", ErrUnsupported)
	}
	reader, err := newScanReader(ctx, r, options)
	if err != nil {
		return report, err
	}
	defer func() {
		report.PageReads = reader.requests
		report.CacheHits = reader.hits
		report.PhysicalReads = reader.reads
		report.TraversalEntries = reader.entries
	}()
	if schema == nil {
		m, e := InspectTable(reader, size)
		if e != nil {
			return report, e
		}
		schema = m.Schema
		if materialized {
			schema = m.MaterializedSchema
		}
		if schema == nil {
			return report, fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(m.Issues, "; "))
		}
	}
	s := *schema
	pk, err := s.validate()
	if err != nil {
		return report, err
	}
	if !materialized && len(s.VirtualColumns) > 0 {
		return report, fmt.Errorf("%w: VIRTUAL requires a Materialized projection entry point", ErrUnsupported)
	}
	projection, err := prepareProjection(s, q.Columns)
	if err != nil {
		return report, err
	}
	selected := map[int]bool{}
	for _, col := range projection {
		report.Columns = append(report.Columns, s.Columns[col])
		selected[col] = true
	}
	report.VirtualColumns = s.VirtualColumns
	s.VirtualColumns = nil
	selection, err := prepareQuery(q.Range, s, pk)
	if err != nil {
		return report, err
	}
	selection.deferValues, selection.fullScan = true, fullScan
	if selection.empty {
		err = ctx.Err()
		report.Complete = err == nil
		return report, err
	}
	err = walkSelectedTree(reader, size, s, func(e ScanEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := ProjectedEvent{Page: e.Page, Node: e.Node, DeletedRecord: e.DeletedRecord}
		switch {
		case e.Page != nil:
			report.Pages++
		case e.Node != nil:
			report.Nodes++
		case e.DeletedRecord != nil:
			report.DeletedRecords++
		case e.Record != nil:
			record, err := materializeProjection(reader, size, *e.Record, s, selected)
			if err != nil {
				return err
			}
			projected := projectRecord(record, projection)
			event.Record = &projected
			report.Records++
		}
		if err := yield(event); err != nil {
			return err
		}
		return ctx.Err()
	}, selection)
	report.Complete = err == nil
	report.LimitReached = selection.limitReached
	return report, err
}

func materializeProjection(r io.ReaderAt, size int64, record Record, s Schema, selected map[int]bool) (Record, error) {
	external, defaults := record.External, record.DefaultColumns
	record.External, record.DefaultColumns = nil, nil
	for _, f := range external {
		if selected[f.Column] {
			record.External = append(record.External, f)
		}
	}
	for _, col := range defaults {
		if selected[col] {
			record.DefaultColumns = append(record.DefaultColumns, col)
		}
	}
	if err := scanRow(r, record, s); err != nil {
		return Record{}, err
	}
	for i := range record.External {
		field := &record.External[i]
		data, err := readExternal(r, size, field)
		if err != nil {
			return Record{}, problemAt(fmt.Errorf("page %d record %d column %q: %w", record.PageNumber, record.Offset, s.Columns[field.Column].Name, err), ProblemLOBChain, externalProblemLocation(record, s, *field), "valid current LOB chain", err.Error())
		}
		value, err := variableValue(s.Columns[field.Column], data)
		if err != nil {
			return Record{}, err
		}
		if s.Columns[field.Column].isText() {
			record.retainTextBytes(field.Column, data)
		}
		if s.Columns[field.Column].Type == "CHAR" {
			if record.CharStorage == nil {
				record.CharStorage = map[int]string{}
			}
			record.CharStorage[field.Column] = value.(string)
			value = strings.TrimRight(value.(string), " ")
		}
		record.Values[field.Column] = value
	}
	return record, nil
}

func projectRecord(r Record, columns []int) ProjectedRecord {
	result := ProjectedRecord(r)
	result.Values = make([]any, len(columns))
	result.TextBytes = projectColumnMap(r.TextBytes, columns)
	result.CharStorage = projectColumnMap(r.CharStorage, columns)
	result.EnumIndexes = projectColumnMap(r.EnumIndexes, columns)
	result.SetMasks = projectColumnMap(r.SetMasks, columns)
	result.DefaultColumns, result.External = nil, nil
	for i, col := range columns {
		result.Values[i] = r.Values[col]
		if slices.Contains(r.DefaultColumns, col) {
			result.DefaultColumns = append(result.DefaultColumns, i)
		}
		for _, f := range r.External {
			if f.Column == col {
				f.Column = i
				result.External = append(result.External, f)
			}
		}
	}
	return result
}

func projectColumnMap[V any](source map[int]V, columns []int) map[int]V {
	var result map[int]V
	for i, col := range columns {
		if v, ok := source[col]; ok {
			if result == nil {
				result = map[int]V{}
			}
			result[i] = v
		}
	}
	return result
}
