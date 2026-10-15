package innodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ExportSnapshotEvidence is a trusted caller assertion, backed by independent
// capture records named in Source. SHA256 binds those records to the raw file.
// The checker does not authenticate Source or discover transaction/ibuf state.
type ExportSnapshotEvidence struct {
	Source, SHA256                                      string
	ExportCompleted, CopiedWhileLocked, WritesCompleted bool
}

type ConsistencyOptions struct {
	ScanOptions      ScanOptions
	Evidence         *ExportSnapshotEvidence
	MaxIssues        uint64 // default 1,000
	MaxBufferedBytes uint64 // default 64 MiB of retained encoded keys per index
}

// ConsistencyIssue identifies the selected index separately from the actual
// physical Problem.Location. Missing entries have only a clustered location.
type ConsistencyIssue struct {
	IndexID   uint64 `json:",string"`
	IndexName string
	Problem   *Problem
	Clustered *ProblemLocation `json:",omitempty"`
}

type IndexConsistency struct {
	IndexID                                       uint64 `json:",string"`
	Name                                          string
	State                                         CheckState
	Reason                                        string `json:",omitempty"`
	ClusteredRows, SecondaryRecords, Matched      uint64 `json:",string"`
	ClusteredDeleted, SecondaryDeleted            uint64 `json:",string"`
	Missing, Extra, Mismatched, DuplicateLocators uint64 `json:",string"`
}

type ConsistencyPreconditions struct {
	State       CheckState
	Reason      string `json:",omitempty"`
	HashedPages uint64 `json:",string"`
}

// ConsistencyReport certifies only selected indexes under trusted export
// evidence. Complete is execution completion, not Passed. Operational failures
// retain an incomplete report and error; findings are in Issues with nil error.
type ConsistencyReport struct {
	Complete, Passed, Truncated                           bool
	Preconditions                                         ConsistencyPreconditions
	Evidence                                              *ExportSnapshotEvidence
	ActualSHA256                                          string
	Indexes                                               []IndexConsistency
	Issues                                                []ConsistencyIssue
	VirtualColumns                                        []VirtualColumn
	Unverified                                            []string
	PeakBufferedBytes                                     uint64 `json:",string"`
	PageReads, CacheHits, PhysicalReads, TraversalEntries uint64 `json:",string"`
}

// CheckIndexConsistency compares current stored rows and explicitly selected
// secondary trees under trusted, file-bound export evidence. Consume Complete,
// Passed and Preconditions as well as error; nil error can accompany findings.
func CheckIndexConsistency(ctx context.Context, r io.ReaderAt, size int64, table Schema, indexes []SecondarySchema, options ConsistencyOptions) (ConsistencyReport, error) {
	return checkIndexConsistency(ctx, r, size, table, indexes, options, false)
}

// CheckIndexConsistencyMaterialized reports non-indexed VIRTUAL definitions
// separately without evaluating them; only stored index columns are compared.
func CheckIndexConsistencyMaterialized(ctx context.Context, r io.ReaderAt, size int64, table Schema, indexes []SecondarySchema, options ConsistencyOptions) (ConsistencyReport, error) {
	return checkIndexConsistency(ctx, r, size, table, indexes, options, true)
}

type consistencyCheck struct {
	reader  *scanReader
	size    int64
	table   Schema
	options ConsistencyOptions
	report  ConsistencyReport
}

func checkIndexConsistency(ctx context.Context, r io.ReaderAt, size int64, table Schema, indexes []SecondarySchema, options ConsistencyOptions, materialized bool) (report ConsistencyReport, err error) {
	if options.MaxIssues == 0 {
		options.MaxIssues = 1000
	}
	if options.MaxBufferedBytes == 0 {
		options.MaxBufferedBytes = 64 << 20
	}
	c := consistencyCheck{size: size, table: table, options: options}
	c.report.Unverified = []string{"authenticity of caller capture evidence and schema provenance", "SQL ReadView/transaction history", "FSP allocation and indexes without supplied definitions", "unselected LOB values and VIRTUAL expressions"}
	if options.Evidence != nil {
		evidence := *options.Evidence
		c.report.Evidence = &evidence
	}
	for _, s := range indexes {
		c.report.Indexes = append(c.report.Indexes, IndexConsistency{IndexID: s.IndexID, Name: s.Name})
	}
	defer func() {
		if c.reader != nil {
			c.report.PageReads, c.report.CacheHits, c.report.PhysicalReads = c.reader.requests, c.reader.hits, c.reader.reads
			c.report.TraversalEntries = c.reader.entries
		}
		c.report.Complete, c.report.Truncated = err == nil, errors.Is(err, ErrLimit)
		pending, reason := CheckBlocked, "prerequisite unavailable"
		if err != nil {
			pending, reason = CheckIncomplete, "execution interrupted"
		}
		if c.report.Preconditions.State == "" {
			c.report.Preconditions.State, c.report.Preconditions.Reason = pending, reason
		}
		c.report.Passed = err == nil && c.report.Preconditions.State == CheckPassed && len(c.report.Indexes) > 0
		for i := range c.report.Indexes {
			check := &c.report.Indexes[i]
			if check.State == "" {
				check.State, check.Reason = pending, reason
			}
			c.report.Passed = c.report.Passed && check.State == CheckPassed
		}
		report = c.report
	}()
	c.reader, err = newScanReader(ctx, r, options.ScanOptions)
	if err != nil {
		return
	}
	if len(indexes) == 0 {
		err = fmt.Errorf("%w: at least one selected secondary index required", ErrUnsupported)
		return
	}
	if err = validateSecondaryDefinitions(table, indexes); err != nil {
		return
	}
	if !materialized && len(table.VirtualColumns) != 0 {
		err = fmt.Errorf("%w: VIRTUAL requires CheckIndexConsistencyMaterialized", ErrUnsupported)
		return
	}
	c.report.VirtualColumns = append([]VirtualColumn(nil), table.VirtualColumns...)
	c.table.VirtualColumns = nil
	var ready bool
	ready, err = c.evidence()
	if err != nil || !ready {
		return
	}
	for i, index := range indexes {
		check := &c.report.Indexes[i]
		err = c.index(index, check)
		if err == nil {
			if check.State == "" {
				check.State = CheckPassed
			}
			continue
		}
		if diagnosticControl(err) || !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupported) {
			check.State, check.Reason = CheckIncomplete, err.Error()
			return
		}
		check.State, check.Reason = CheckFailed, "tree/value validation stopped; missing-entry inference blocked"
		if errors.Is(err, ErrUnsupported) {
			check.State = CheckUnsupported
		}
		err = c.issue(index, err, ProblemRecordLayout, ProblemLocation{}, nil, "valid complete selected trees and indexed values", "validation stopped")
		if err != nil {
			check.State = CheckIncomplete
			return
		}
	}
	err = ctx.Err()
	return
}

func (c *consistencyCheck) evidence() (bool, error) {
	p := &c.report.Preconditions
	e := c.report.Evidence
	if e == nil || strings.TrimSpace(e.Source) == "" || !e.ExportCompleted || !e.CopiedWhileLocked || !e.WritesCompleted {
		p.State, p.Reason = CheckBlocked, "independent completed export/locked-copy/write evidence required; ibuf/transaction state not inferred"
		return false, nil
	}
	want, err := hex.DecodeString(e.SHA256)
	if err != nil || len(want) != sha256.Size {
		p.State, p.Reason = CheckBlocked, "capture evidence requires a 32-byte SHA256 digest"
		return false, nil
	}
	if c.size < PageSize || c.size%PageSize != 0 || uint64(c.size/PageSize) > uint64(^uint32(0)) {
		return false, fmt.Errorf("%w: consistency file geometry", ErrCorrupt)
	}
	hash := sha256.New()
	for n := uint32(0); int64(n) < c.size/PageSize; n++ {
		raw, err := ReadRawPage(c.reader, c.size, n)
		if err != nil {
			return false, err
		}
		hash.Write(raw)
		p.HashedPages++
	}
	c.report.ActualSHA256 = hex.EncodeToString(hash.Sum(nil))
	if !bytes.Equal(want, hash.Sum(nil)) {
		p.State, p.Reason = CheckBlocked, "input SHA256 differs from capture evidence; correspondence not certified"
		return false, nil
	}
	p.State = CheckPassed
	return true, nil
}

type consistencyExpected struct {
	key      indexKey
	location ProblemLocation
	seen     bool
}

// Length-prefix members distinguish NULL, empty and tuple boundaries. Trimming
// only PAD SPACE character members implements the existing comparator's equality.
func consistencyLocator(key indexKey, columns []Column) string {
	var result []byte
	for i, raw := range key {
		if raw == nil {
			result = binary.AppendUvarint(result, 0)
			continue
		}
		if columns[i].Type == "CHAR" || columns[i].Type == "VARCHAR" {
			raw = bytes.TrimRight(raw, " ")
		}
		result = binary.AppendUvarint(result, uint64(len(raw))+1)
		result = append(result, raw...)
	}
	return string(result)
}

func (c *consistencyCheck) index(index SecondarySchema, check *IndexConsistency) error {
	pk, _ := c.table.validate()
	locatorColumns := []Column{}
	if c.table.hiddenRowID() {
		locatorColumns = append(locatorColumns, Column{Type: "BINARY"})
	} else {
		for _, i := range pk {
			locatorColumns = append(locatorColumns, c.table.Columns[i])
		}
	}
	selected := map[int]bool{}
	for _, f := range index.Fields {
		if !f.RowID {
			selected[f.Column] = true
		}
	}
	expected := map[string]*consistencyExpected{}
	ordered := []*consistencyExpected{}
	var buffered uint64
	selection, _ := prepareQuery(KeyRange{}, c.table, pk)
	selection.deferValues, selection.fullScan = true, true
	err := walkSelectedTree(c.reader, c.size, c.table, func(e ScanEvent) error {
		if e.DeletedRecord != nil {
			check.ClusteredDeleted++
		}
		if e.Record == nil {
			return nil
		}
		r, err := materializeProjection(c.reader, c.size, *e.Record, c.table, selected)
		if err != nil {
			return err
		}
		check.ClusteredRows++
		key := make(indexKey, len(index.Fields))
		for i, f := range index.Fields {
			if f.RowID {
				key[i] = append([]byte{}, r.key[0]...)
				continue
			}
			if r.Values[f.Column] != nil {
				key[i], err = encodeQueryValue(r.Values[f.Column], f.Definition)
				if err != nil {
					return err
				}
				key[i] = append([]byte{}, secondaryPrefix(key[i], f)...)
			}
		}
		locator := consistencyLocator(r.key, locatorColumns)
		n := uint64(len(locator))
		for _, raw := range key {
			n += uint64(len(raw))
		}
		if n > c.options.MaxBufferedBytes-buffered {
			return fmt.Errorf("%w: consistency retained key bytes", ErrLimit)
		}
		buffered += n
		c.report.PeakBufferedBytes = max(c.report.PeakBufferedBytes, buffered)
		entry := &consistencyExpected{key: key, location: ProblemLocation{Page: diagnosticPtr(r.PageNumber), IndexID: diagnosticPtr(c.table.IndexID), RecordPage: diagnosticPtr(r.PageNumber), RecordOffset: diagnosticPtr(r.Offset)}}
		expected[locator] = entry
		ordered = append(ordered, entry)
		return nil
	}, selection)
	if err != nil {
		return err
	}
	err = walkSecondary(c.reader, c.size, index, func(e SecondaryEvent) error {
		if e.DeletedRecord != nil {
			check.SecondaryDeleted++
		}
		if e.Record == nil {
			return nil
		}
		r := e.Record
		check.SecondaryRecords++
		locator := make(indexKey, len(index.ClusteredFields))
		for i, pos := range index.ClusteredFields {
			locator[i] = r.FieldBytes[pos]
		}
		entry := expected[consistencyLocator(locator, locatorColumns)]
		location := ProblemLocation{Page: &r.PageNumber, IndexID: diagnosticPtr(index.IndexID), RecordPage: &r.PageNumber, RecordOffset: &r.Offset}
		if entry == nil {
			check.Extra++
			check.State = CheckFailed
			return c.issue(index, fmt.Errorf("%w: secondary locator has no current clustered row", ErrCorrupt), ProblemIndexExtra, location, nil, "one current clustered row", "none")
		}
		if entry.seen {
			check.DuplicateLocators++
			check.State = CheckFailed
			if err := c.issue(index, fmt.Errorf("%w: multiple live entries for clustered locator", ErrCorrupt), ProblemIndexLocator, location, &entry.location, "one live entry per clustered row", "multiple"); err != nil {
				return err
			}
		}
		entry.seen = true
		matched, offset := true, r.Offset
		for i, f := range index.Fields {
			raw := r.FieldBytes[i]
			if compareSecondary(indexKey{entry.key[i]}, indexKey{raw}, []Column{f.Definition}) != 0 {
				matched = false
				where := location
				if raw != nil {
					where.Bytes = &ByteSpan{offset, offset + len(raw)}
				}
				if !f.RowID {
					where.Column, where.ColumnName = diagnosticPtr(f.Column), f.Definition.Name
				}
				if err := c.issue(index, fmt.Errorf("%w: secondary/clustered value mismatch", ErrCorrupt), ProblemIndexMismatch, where, &entry.location, consistencyObservation(entry.key[i]), consistencyObservation(raw)); err != nil {
					return err
				}
			}
			offset += len(raw)
		}
		if matched {
			check.Matched++
		} else {
			check.Mismatched++
			check.State = CheckFailed
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, entry := range ordered {
		if err := c.reader.ctx.Err(); err != nil {
			return err
		}
		if !entry.seen {
			check.Missing++
			check.State = CheckFailed
			if err := c.issue(index, fmt.Errorf("%w: clustered row lacks a live secondary entry", ErrCorrupt), ProblemIndexMissing, entry.location, &entry.location, "one live matching secondary entry", "none"); err != nil {
				return err
			}
		}
	}
	return nil
}

func consistencyObservation(raw []byte) string {
	if raw == nil {
		return "SQL NULL"
	}
	return byteObservation(raw)
}

func (c *consistencyCheck) issue(index SecondarySchema, err error, code ProblemCode, location ProblemLocation, clustered *ProblemLocation, expected, actual string) error {
	if uint64(len(c.report.Issues)) >= c.options.MaxIssues {
		return fmt.Errorf("%w: consistency issues", ErrLimit)
	}
	var problem *Problem
	errors.As(problemAt(err, code, location, expected, actual), &problem)
	if clustered != nil {
		copy := *clustered
		clustered = &copy // Do not retain an expected entry's encoded keys through an interior pointer.
	}
	c.report.Issues = append(c.report.Issues, ConsistencyIssue{IndexID: index.IndexID, IndexName: index.Name, Problem: problem, Clustered: clustered})
	return nil
}
