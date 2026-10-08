package innodb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// SecondaryQuery applies Range to complete values of the declared secondary
// columns. Columns=nil selects all stored columns; otherwise names are ordered,
// distinct stored columns. Prefix indexes are only candidate filters.
type SecondaryQuery struct {
	Range   KeyRange
	Columns []string
}

// ProjectedRow contains only the columns named in the report, in that order.
// Covered rows have no clustered-page provenance and do not certify that tree.
type ProjectedRow struct {
	Values          []any
	Covered         bool
	SecondaryPage   uint32
	SecondaryOffset int
	ClusteredPage   *uint32
	ClusteredOffset *int
	ClusteredKey    Key
	RowID           *uint64
}
type SecondaryQueryReport struct {
	Columns                                                                                         []Column
	VirtualColumns                                                                                  []VirtualColumn
	Complete, LimitReached                                                                          bool
	SecondaryPages, ClusteredPages, Candidates, Filtered, Lookups, Covered, Records, DeletedRecords uint64
	PageReads, CacheHits, PhysicalReads, TraversalEntries                                           uint64
}

func QuerySecondary(ctx context.Context, r io.ReaderAt, size int64, table Schema, index SecondarySchema, q SecondaryQuery, options ScanOptions, yield func(ProjectedRow) error) (SecondaryQueryReport, error) {
	return querySecondary(ctx, r, size, &table, &index, "", q, options, yield, false)
}
func QuerySecondaryAuto(ctx context.Context, r io.ReaderAt, size int64, index string, q SecondaryQuery, options ScanOptions, yield func(ProjectedRow) error) (SecondaryQueryReport, error) {
	return querySecondary(ctx, r, size, nil, nil, index, q, options, yield, false)
}
func QuerySecondaryMaterialized(ctx context.Context, r io.ReaderAt, size int64, table Schema, index SecondarySchema, q SecondaryQuery, options ScanOptions, yield func(ProjectedRow) error) (SecondaryQueryReport, error) {
	return querySecondary(ctx, r, size, &table, &index, "", q, options, yield, true)
}
func QuerySecondaryMaterializedAuto(ctx context.Context, r io.ReaderAt, size int64, index string, q SecondaryQuery, options ScanOptions, yield func(ProjectedRow) error) (SecondaryQueryReport, error) {
	return querySecondary(ctx, r, size, nil, nil, index, q, options, yield, true)
}

var errSecondaryQueryLimit = errors.New("secondary query limit satisfied")

func validateSecondaryTable(table Schema, index SecondarySchema) error {
	pk, err := table.validate()
	if err != nil {
		return err
	}
	if _, err = index.validate(); err != nil {
		return err
	}
	if table.SpaceID != index.SpaceID || table.IndexID == index.IndexID || table.RootPage == index.RootPage || (table.RowFormat == "COMPACT") != (index.RowFormat == "COMPACT") {
		return fmt.Errorf("%w: secondary/table identities", ErrUnsupported)
	}
	for _, f := range index.Fields {
		if f.RowID {
			if !table.hiddenRowID() {
				return fmt.Errorf("%w: unexpected ROW_ID", ErrUnsupported)
			}
			continue
		}
		if f.Column < 0 || f.Column >= len(table.Columns) {
			return fmt.Errorf("%w: secondary source column", ErrUnsupported)
		}
		a, b := f.Definition, table.Columns[f.Column]
		a.Descending = false
		b.Descending = false
		a.Collation = ""
		b.Collation = ""
		if !reflect.DeepEqual(a, b) {
			return fmt.Errorf("%w: secondary column definition mismatch", ErrUnsupported)
		}
	}
	if table.hiddenRowID() {
		if len(index.ClusteredFields) != 1 || !index.Fields[index.ClusteredFields[0]].RowID {
			return fmt.Errorf("%w: ROW_ID locator", ErrUnsupported)
		}
	} else {
		if len(pk) != len(index.ClusteredFields) {
			return fmt.Errorf("%w: clustered locator count", ErrUnsupported)
		}
		for i, col := range pk {
			if index.Fields[index.ClusteredFields[i]].Column != col {
				return fmt.Errorf("%w: clustered locator order", ErrUnsupported)
			}
		}
	}
	return nil
}
func querySecondary(ctx context.Context, r io.ReaderAt, size int64, table *Schema, index *SecondarySchema, name string, q SecondaryQuery, options ScanOptions, yield func(ProjectedRow) error, materialized bool) (report SecondaryQueryReport, err error) {
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
	if table == nil {
		meta, e := InspectTable(reader, size)
		if e != nil {
			return report, e
		}
		table = meta.Schema
		if materialized {
			table = meta.MaterializedSchema
		}
		if table == nil {
			return report, fmt.Errorf("%w: %s", ErrUnsupported, strings.Join(meta.Issues, "; "))
		}
		index, err = inspectSecondaryMetadata(meta, name)
		if err != nil {
			return report, err
		}
	}
	if err = validateSecondaryTable(*table, *index); err != nil {
		return report, err
	}
	if !materialized && len(table.VirtualColumns) > 0 {
		return report, fmt.Errorf("%w: VIRTUAL requires QuerySecondaryMaterialized", ErrUnsupported)
	}
	s := *table
	report.VirtualColumns = s.VirtualColumns
	s.VirtualColumns = nil
	projection := []int{}
	if q.Columns == nil {
		for i := range s.Columns {
			projection = append(projection, i)
		}
	} else {
		if len(q.Columns) == 0 {
			return report, fmt.Errorf("%w: empty projection", ErrUnsupported)
		}
		used := map[int]bool{}
		for _, name := range q.Columns {
			found := -1
			for i, c := range s.Columns {
				if c.Name == name {
					found = i
					break
				}
			}
			if found < 0 || used[found] {
				return report, fmt.Errorf("%w: unknown/duplicate/unmaterialized projection %q", ErrUnsupported, name)
			}
			used[found] = true
			projection = append(projection, found)
		}
	}
	for _, i := range projection {
		report.Columns = append(report.Columns, s.Columns[i])
	}
	selection, err := prepareSecondaryRange(q.Range, *index)
	if err != nil {
		return report, err
	}
	if selection.empty {
		err = ctx.Err()
		report.Complete = err == nil
		return report, err
	}
	fullFields := map[int]int{}
	for i, f := range index.Fields {
		if f.PrefixBytes == 0 && !f.RowID {
			fullFields[f.Column] = i
		}
	}
	predicateCount := 0
	if selection.exactLower != nil {
		predicateCount = len(selection.exactLower.key)
	}
	if selection.exactUpper != nil && len(selection.exactUpper.key) > predicateCount {
		predicateCount = len(selection.exactUpper.key)
	}
	needed := map[int]bool{}
	for _, i := range projection {
		needed[i] = true
	}
	for _, f := range index.Fields[:predicateCount] {
		needed[f.Column] = true
	}
	covered := true
	for col := range needed {
		if _, ok := fullFields[col]; !ok {
			covered = false
		}
	}
	predicateCovered := true
	for _, f := range index.Fields[:predicateCount] {
		if _, ok := fullFields[f.Column]; !ok {
			predicateCovered = false
		}
	}
	matches := func(values []any) (bool, error) {
		key := make(indexKey, predicateCount)
		for i, f := range index.Fields[:predicateCount] {
			if values[f.Column] != nil {
				var e error
				key[i], e = encodeQueryValue(values[f.Column], f.Definition)
				if e != nil {
					return false, e
				}
			}
		}
		return selection.matches(key), nil
	}
	pk, _ := s.validate()
	err = walkSelectedSecondary(reader, size, *index, func(event SecondaryEvent) error {
		if event.Page != nil {
			report.SecondaryPages++
			return nil
		}
		if event.DeletedRecord != nil {
			report.DeletedRecords++
			return nil
		}
		if event.Record == nil {
			return nil
		}
		entry := event.Record
		report.Candidates++
		sourceBytes := uint64(entry.End - entry.Start)
		checkBytes := func() error {
			if sourceBytes > reader.options.MaxRowBytes {
				return fmt.Errorf("%w: projected source bytes", ErrLimit)
			}
			return ctx.Err()
		}
		if e := checkBytes(); e != nil {
			return e
		}
		values := make([]any, len(s.Columns))
		for col, field := range fullFields {
			values[col] = entry.Values[field]
		}
		if predicateCovered {
			ok, e := matches(values)
			if e != nil {
				return e
			}
			if !ok {
				report.Filtered++
				return nil
			}
		}
		var cluster *Record
		if !covered {
			report.Lookups++
			selectKey, e := prepareQuery(PointKey(entry.ClusteredKey), s, pk)
			if e != nil {
				return e
			}
			selectKey.deferValues = true
			e = walkSelectedTree(reader, size, s, func(e ScanEvent) error {
				if e.Page != nil {
					report.ClusteredPages++
				}
				if e.Record != nil {
					if cluster != nil {
						return fmt.Errorf("%w: duplicate clustered lookup", ErrCorrupt)
					}
					cluster = e.Record
				}
				return nil
			}, selectKey)
			if e != nil {
				return e
			}
			if cluster == nil {
				return fmt.Errorf("%w: secondary locator has no current clustered row", ErrCorrupt)
			}
			sourceBytes += uint64(cluster.End - cluster.Start)
			if s.Instant != nil {
				for _, col := range cluster.DefaultColumns {
					for _, f := range s.Instant.Fields {
						if f.Column == col && f.Default != nil {
							sourceBytes += uint64(len(f.Default.Data))
						}
					}
				}
			}
			if e = checkBytes(); e != nil {
				return e
			}
			values = cluster.Values
		}
		resolved := map[int]bool{}
		resolve := func(columns map[int]bool) error {
			if cluster == nil {
				return nil
			}
			for i := range cluster.External {
				field := &cluster.External[i]
				if !columns[field.Column] || resolved[field.Column] {
					continue
				}
				sourceBytes += uint64(field.Length)
				if e := checkBytes(); e != nil {
					return e
				}
				data, e := readExternal(reader, size, field)
				if e != nil {
					return e
				}
				value, e := variableValue(s.Columns[field.Column], data)
				if e != nil {
					return e
				}
				if s.Columns[field.Column].Type == "CHAR" {
					value = strings.TrimRight(value.(string), " ")
				}
				values[field.Column] = value
				resolved[field.Column] = true
			}
			return nil
		}
		// Validate all index columns against the located row before trusting projection.
		if cluster != nil {
			indexColumns := map[int]bool{}
			for _, f := range index.Fields {
				if !f.RowID {
					indexColumns[f.Column] = true
				}
			}
			if e := resolve(indexColumns); e != nil {
				return e
			}
			for i, f := range index.Fields {
				var raw []byte
				if f.RowID {
					if cluster.RowID == nil || entry.RowID == nil || *cluster.RowID != *entry.RowID {
						return fmt.Errorf("%w: ROW_ID lookup mismatch", ErrCorrupt)
					}
					continue
				}
				if values[f.Column] != nil {
					raw, err = encodeQueryValue(values[f.Column], f.Definition)
					if err != nil {
						return err
					}
					raw = secondaryPrefix(raw, f)
				}
				if compareSecondary(indexKey{raw}, indexKey{entry.FieldBytes[i]}, []Column{f.Definition}) != 0 {
					return fmt.Errorf("%w: secondary/clustered value mismatch", ErrCorrupt)
				}
			}
		}
		if !predicateCovered {
			ok, e := matches(values)
			if e != nil {
				return e
			}
			if !ok {
				report.Filtered++
				return nil
			}
		}

		if reader.rows >= reader.options.MaxRows {
			return fmt.Errorf("%w: projected rows", ErrLimit)
		}
		selected := map[int]bool{}
		for _, i := range projection {
			selected[i] = true
		}
		if e := resolve(selected); e != nil {
			return e
		}
		reader.rows++
		row := ProjectedRow{Values: make([]any, len(projection)), Covered: covered, SecondaryPage: entry.PageNumber, SecondaryOffset: entry.Offset, ClusteredKey: entry.ClusteredKey, RowID: entry.RowID}
		for i, col := range projection {
			row.Values[i] = values[col]
		}
		if cluster != nil {
			page, offset := cluster.PageNumber, cluster.Offset
			row.ClusteredPage = &page
			row.ClusteredOffset = &offset
		}
		report.Records++
		if covered {
			report.Covered++
		}
		if e := yield(row); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if q.Range.Limit != 0 && report.Records == q.Range.Limit {
			report.LimitReached = true
			return errSecondaryQueryLimit
		}
		return nil
	}, selection)
	if err == errSecondaryQueryLimit {
		err = ctx.Err()
	}
	report.Complete = err == nil
	return report, err
}
