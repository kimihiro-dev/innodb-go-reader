// Package rowio streams versioned, typed JSONL and CSV exports. CSV EOF does not
// certify producer completion; use its exit status or atomically published file.
package rowio

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	innodb "innodb-go-reader"
	"io"
	"strconv"
	"unicode/utf8"
)

const Version = 1
const DefaultMaxRecordBytes = 128 << 20

type Header struct {
	Version        int                    `json:"version"`
	Columns        []innodb.Column        `json:"columns"`
	VirtualColumns []innodb.VirtualColumn `json:"virtual_columns,omitempty"`
	Physical       bool                   `json:"physical"`
}
type Row struct {
	Values []any
	// Physical is an opaque JSON object. Decode it into the corresponding library
	// struct, or use json.Decoder.UseNumber for an untyped view.
	Physical json.RawMessage
}

// Options bounds one wire record, including the schema. Zero selects 128 MiB.
type Options struct{ MaxRecordBytes int }

type envelope struct {
	Kind     string          `json:"kind"`
	Header   *Header         `json:"schema,omitempty"`
	Values   []cell          `json:"values,omitempty"`
	Physical json.RawMessage `json:"physical,omitempty"`
	Rows     string          `json:"rows,omitempty"`
}

func limit(o Options) (int, error) {
	if o.MaxRecordBytes < 0 {
		return 0, fmt.Errorf("negative record limit")
	}
	if o.MaxRecordBytes == 0 {
		return DefaultMaxRecordBytes, nil
	}
	return o.MaxRecordBytes, nil
}
func validateHeader(h Header) error {
	if h.Version != Version || len(h.Columns) == 0 {
		return fmt.Errorf("unsupported version or empty columns")
	}
	names := map[string]bool{}
	for _, c := range h.Columns {
		if c.Name == "" || c.Type == "" || names[c.Name] {
			return fmt.Errorf("invalid/duplicate column %q", c.Name)
		}
		names[c.Name] = true
		texts := []string{c.Name, c.Type, c.GenerationExpression, c.Collation, c.Charset}
		texts = append(texts, c.EnumValues...)
		texts = append(texts, c.SetValues...)
		for _, text := range texts {
			if !utf8.ValidString(text) {
				return fmt.Errorf("invalid UTF-8 column metadata")
			}
		}
	}
	for _, v := range h.VirtualColumns {
		if !utf8.ValidString(v.Name) || !utf8.ValidString(v.Expression) {
			return fmt.Errorf("invalid UTF-8 virtual metadata")
		}
	}
	return nil
}
func validatePhysical(b json.RawMessage, enabled bool) error {
	if !enabled && len(b) != 0 {
		return fmt.Errorf("physical metadata disabled")
	}
	if enabled {
		var obj map[string]json.RawMessage
		if err := strict(b, &obj); err != nil {
			return err
		}
		if obj == nil {
			return fmt.Errorf("physical object required")
		}
	}
	return nil
}

// Encoder writes its schema immediately. Any error poisons it; Finish is required
// only after the producer completes successfully. It never closes the writer.
type Encoder struct {
	w        io.Writer
	format   string
	header   Header
	max      int
	rows     uint64
	err      error
	finished bool
}

func NewEncoder(w io.Writer, format string, h Header, o Options) (*Encoder, error) {
	max, err := limit(o)
	if err != nil {
		return nil, err
	}
	if format != "jsonl" && format != "csv" {
		return nil, fmt.Errorf("unknown format %q", format)
	}
	if err = validateHeader(h); err != nil {
		return nil, err
	}
	e := &Encoder{w: w, format: format, header: h, max: max}
	b, err := json.Marshal(envelope{Kind: "schema", Header: &h})
	if err == nil {
		err = e.write([][]byte{b})
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}
func (e *Encoder) write(fields [][]byte) error {
	var b []byte
	if e.format == "jsonl" {
		b = append(fields[0], '\n')
	} else {
		var buf bytes.Buffer
		c := csv.NewWriter(&buf)
		s := make([]string, len(fields))
		for i, f := range fields {
			s[i] = string(f)
		}
		if err := c.Write(s); err != nil {
			return err
		}
		c.Flush()
		if err := c.Error(); err != nil {
			return err
		}
		b = buf.Bytes()
	}
	if len(b) > e.max {
		return fmt.Errorf("wire record exceeds %d bytes", e.max)
	}
	n, err := e.w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}
func (e *Encoder) Write(row Row) (err error) {
	if e.err != nil {
		return e.err
	}
	if e.finished {
		return fmt.Errorf("encoder finished")
	}
	defer func() {
		if err != nil {
			e.err = err
		}
	}()
	if len(row.Values) != len(e.header.Columns) {
		return fmt.Errorf("row width mismatch")
	}
	if err = validatePhysical(row.Physical, e.header.Physical); err != nil {
		return err
	}
	cells := make([]cell, len(row.Values))
	for i, v := range row.Values {
		cells[i], err = encodeValue(v)
		if err != nil {
			return fmt.Errorf("column %d: %w", i, err)
		}
	}
	var fields [][]byte
	if e.format == "jsonl" {
		b, x := json.Marshal(envelope{Kind: "row", Values: cells, Physical: row.Physical})
		if x != nil {
			return x
		}
		fields = [][]byte{b}
	} else {
		for _, c := range cells {
			b, x := json.Marshal(c)
			if x != nil {
				return x
			}
			fields = append(fields, b)
		}
		if e.header.Physical {
			var b bytes.Buffer
			if err = json.Compact(&b, row.Physical); err != nil {
				return err
			}
			fields = append(fields, b.Bytes())
		}
	}
	if err = e.write(fields); err == nil {
		e.rows++
	}
	return err
}
func (e *Encoder) Finish() error {
	if e.err != nil {
		return e.err
	}
	if e.finished {
		return fmt.Errorf("encoder finished")
	}
	e.finished = true
	if e.format == "jsonl" {
		b, err := json.Marshal(envelope{Kind: "end", Rows: strconv.FormatUint(e.rows, 10)})
		if err == nil {
			err = e.write([][]byte{b})
		}
		e.err = err
	}
	return e.err
}

// Decoder yields independent rows. Errors are terminal. Complete is true only
// after a JSONL end marker, count and trailing EOF have all been verified.
type Decoder struct {
	r              *bufio.Reader
	format         string
	header         Header
	max            int
	rows           uint64
	done, complete bool
	err            error
}

func NewDecoder(r io.Reader, format string, o Options) (*Decoder, error) {
	max, err := limit(o)
	if err != nil {
		return nil, err
	}
	if format != "jsonl" && format != "csv" {
		return nil, fmt.Errorf("unknown format %q", format)
	}
	d := &Decoder{r: bufio.NewReader(r), format: format, max: max}
	fields, err := d.read()
	if err != nil {
		return nil, err
	}
	if len(fields) != 1 {
		return nil, fmt.Errorf("schema record width")
	}
	var e envelope
	if err = strict(fields[0], &e); err != nil {
		return nil, err
	}
	if e.Kind != "schema" || e.Header == nil || e.Values != nil || len(e.Physical) > 0 || e.Rows != "" {
		return nil, fmt.Errorf("expected schema record")
	}
	if err = validateHeader(*e.Header); err != nil {
		return nil, err
	}
	d.header = *e.Header
	return d, nil
}

// Header returns a deep copy; changing it cannot alter decoding constraints.
func (d *Decoder) Header() Header {
	b, _ := json.Marshal(d.header)
	var h Header
	_ = json.Unmarshal(b, &h)
	return h
}
func (d *Decoder) Complete() bool { return d.complete }
func (d *Decoder) read() ([][]byte, error) {
	var line []byte
	for {
		part, err := d.r.ReadSlice('\n')
		if len(part) > d.max-len(line) {
			return nil, fmt.Errorf("wire record exceeds %d bytes", d.max)
		}
		line = append(line, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(line) == 0 {
			return nil, io.EOF
		}
		// A terminal newline is required: a byte-truncated record is never accepted.
		if line[len(line)-1] != '\n' {
			return nil, io.ErrUnexpectedEOF
		}
		break
	}
	if d.format == "jsonl" {
		return [][]byte{line}, nil
	}
	c := csv.NewReader(bytes.NewReader(line))
	c.FieldsPerRecord = -1
	fields, err := c.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("blank CSV record")
		}
		return nil, err
	}
	if _, err = c.Read(); err != io.EOF {
		return nil, fmt.Errorf("one CSV record per physical line required")
	}
	out := make([][]byte, len(fields))
	for i, s := range fields {
		out[i] = []byte(s)
	}
	return out, nil
}
func (d *Decoder) Next() (row Row, err error) {
	if d.err != nil {
		return row, d.err
	}
	if d.done {
		return row, io.EOF
	}
	defer func() {
		if err != nil && err != io.EOF {
			d.err = err
		}
	}()
	fields, err := d.read()
	if err == io.EOF {
		d.done = true
		if d.format == "jsonl" {
			return row, fmt.Errorf("missing completion record")
		}
		return row, io.EOF
	}
	if err != nil {
		return row, err
	}
	var cells []cell
	if d.format == "jsonl" {
		var e envelope
		if err = strict(fields[0], &e); err != nil {
			return row, err
		}
		if e.Kind == "end" {
			if e.Header != nil || e.Values != nil || len(e.Physical) > 0 || e.Rows != strconv.FormatUint(d.rows, 10) {
				return row, fmt.Errorf("invalid completion record")
			}
			if _, err = d.read(); err != io.EOF {
				if err != nil {
					return row, err
				}
				return row, fmt.Errorf("trailing records")
			}
			d.done = true
			d.complete = true
			return row, io.EOF
		}
		if e.Kind != "row" || e.Header != nil || e.Rows != "" {
			return row, fmt.Errorf("expected row")
		}
		cells = e.Values
		row.Physical = e.Physical
	} else {
		width := len(d.header.Columns)
		if d.header.Physical {
			width++
		}
		if len(fields) != width {
			return row, fmt.Errorf("CSV width mismatch")
		}
		for _, f := range fields[:len(d.header.Columns)] {
			var c cell
			if err = strict(f, &c); err != nil {
				return row, err
			}
			cells = append(cells, c)
		}
		if d.header.Physical {
			row.Physical = fields[len(fields)-1]
		}
	}
	if len(cells) != len(d.header.Columns) {
		return row, fmt.Errorf("row width mismatch")
	}
	if err = validatePhysical(row.Physical, d.header.Physical); err != nil {
		return row, err
	}
	row.Values = make([]any, len(cells))
	for i, c := range cells {
		row.Values[i], err = decodeValue(c)
		if err != nil {
			return Row{}, fmt.Errorf("column %d: %w", i, err)
		}
	}
	d.rows++
	return row, nil
}
