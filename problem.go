package innodb

import (
	"context"
	"errors"
	"fmt"
)

// ProblemCode identifies a validation boundary, independently of error wording.
type ProblemCode string

const (
	ProblemGeometry      ProblemCode = "input.geometry"
	ProblemSchema        ProblemCode = "schema.definition"
	ProblemPageRead      ProblemCode = "page.read"
	ProblemZeroPage      ProblemCode = "page.zero"
	ProblemChecksum      ProblemCode = "page.checksum"
	ProblemLSN           ProblemCode = "page.lsn"
	ProblemPageNumber    ProblemCode = "page.number"
	ProblemPageIdentity  ProblemCode = "page.identity"
	ProblemFormat        ProblemCode = "tablespace.format"
	ProblemIndexIdentity ProblemCode = "index.identity"
	ProblemIndexHeader   ProblemCode = "index.header"
	ProblemRecordLayout  ProblemCode = "record.layout"
	ProblemFieldLayout   ProblemCode = "field.layout"
	ProblemFieldValue    ProblemCode = "field.value"
	ProblemAllocation    ProblemCode = "allocation.layout"
	ProblemNavigation    ProblemCode = "tree.navigation"
	ProblemCoverage      ProblemCode = "tree.coverage"
	ProblemLOBReference  ProblemCode = "lob.reference"
	ProblemLOBChain      ProblemCode = "lob.chain"
	ProblemLOBValue      ProblemCode = "lob.value"
	ProblemIndexMissing  ProblemCode = "index.missing"
	ProblemIndexExtra    ProblemCode = "index.extra"
	ProblemIndexMismatch ProblemCode = "index.mismatch"
	ProblemIndexLocator  ProblemCode = "index.duplicate_locator"
	ProblemIndexUnique   ProblemCode = "index.unique"
)

// ProblemLocation contains only known coordinates. Bytes is relative to Page;
// RecordOffset belongs to RecordPage, which may differ from a failing LOB Page.
// Column refers to the current table schema, never an INSTANT physical ordinal.
type ProblemLocation struct {
	Page         *uint32   `json:",omitempty"`
	IndexID      *uint64   `json:",omitempty"`
	RecordPage   *uint32   `json:",omitempty"`
	RecordOffset *int      `json:",omitempty"`
	Column       *int      `json:",omitempty"`
	ColumnName   string    `json:",omitempty"`
	Bytes        *ByteSpan `json:",omitempty"`
}

// Problem retains its cause for errors.Is/As. JSON carries a bounded observation
// and message, not the Go error object; it is diagnostic evidence, not row data.
type Problem struct {
	Code             ProblemCode
	Category         string // corrupt, unsupported, or io
	Location         ProblemLocation
	Expected, Actual string
	Message          string
	Cause            error `json:"-"`
}

func (p *Problem) Error() string { return p.Message }
func (p *Problem) Unwrap() error { return p.Cause }

func diagnosticPtr[T any](v T) *T { return &v }

func diagnosticControl(err error) bool {
	return errors.Is(err, ErrLimit) || errors.Is(err, ErrStopped) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func newProblem(err error, code ProblemCode, location ProblemLocation, expected, actual string) *Problem {
	category := "io"
	if errors.Is(err, ErrCorrupt) {
		category = "corrupt"
	} else if errors.Is(err, ErrUnsupported) {
		category = "unsupported"
	}
	return &Problem{Code: code, Category: category, Location: location, Expected: expected, Actual: actual, Message: err.Error(), Cause: err}
}

// Keep a specific inner problem and fill only missing context. Never attach a
// record's byte span to a different physical page reported by a nested LOB read.
func problemAt(err error, code ProblemCode, location ProblemLocation, expected, actual string) error {
	if diagnosticControl(err) {
		return err
	}
	var inner *Problem
	if !errors.As(err, &inner) {
		return newProblem(err, code, location, expected, actual)
	}
	p := *inner
	p.Message, p.Cause = err.Error(), err
	s := &p.Location
	if s.Bytes == nil && (s.Page == nil || location.Page == nil || *s.Page == *location.Page) {
		s.Bytes = location.Bytes
	}
	if s.Page == nil {
		s.Page = location.Page
	}
	if s.IndexID == nil {
		s.IndexID = location.IndexID
	}
	if s.RecordPage == nil {
		s.RecordPage = location.RecordPage
	}
	if s.RecordOffset == nil {
		s.RecordOffset = location.RecordOffset
	}
	if s.Column == nil {
		s.Column = location.Column
	}
	if s.ColumnName == "" {
		s.ColumnName = location.ColumnName
	}
	return &p
}

func byteObservation(b []byte) string {
	n := min(len(b), 64)
	return fmt.Sprintf("%x (%d bytes; showing %d)", b[:n], len(b), n)
}

func remapProblemColumn(err error, columns []int) error {
	var p *Problem
	if !errors.As(err, &p) || p.Location.Column == nil {
		return err
	}
	result := *p
	result.Cause, result.Message = err, err.Error()
	column := columns[*p.Location.Column]
	result.Location.Column = nil
	if column >= 0 {
		result.Location.Column = diagnosticPtr(column)
	}
	return &result
}

func navigationProblem(err error, page uint32, index uint64) error {
	return problemAt(err, ProblemNavigation, ProblemLocation{Page: &page, IndexID: &index}, "consistent child ranges, levels and sibling navigation", err.Error())
}

func externalProblemLocation(r Record, s Schema, f ExternalField) ProblemLocation {
	return ProblemLocation{Page: diagnosticPtr(r.PageNumber), IndexID: diagnosticPtr(s.IndexID), RecordPage: diagnosticPtr(r.PageNumber), RecordOffset: diagnosticPtr(r.Offset), Column: diagnosticPtr(f.Column), ColumnName: s.Columns[f.Column].Name, Bytes: &ByteSpan{f.Offset, f.Offset + 20}}
}
