package rowio

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	innodb "innodb-go-reader"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func header(n int, physical bool) Header {
	h := Header{Version: Version, Physical: physical}
	for i := 0; i < n; i++ {
		h.Columns = append(h.Columns, innodb.Column{Name: fmt.Sprint(i), Type: "VARCHAR", Nullable: true})
	}
	return h
}
func roundTrip(t *testing.T, format string, values []any) {
	t.Helper()
	var b bytes.Buffer
	e, err := NewEncoder(&b, format, header(len(values), true), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Write(Row{Values: values, Physical: json.RawMessage(`{"RowID":18446744073709551615}`)}); err != nil {
		t.Fatal(err)
	}
	if err = e.Finish(); err != nil {
		t.Fatal(err)
	}
	d, err := NewDecoder(&b, format, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Values, values) {
		t.Fatalf("round trip got %#v want %#v", r.Values, values)
	}
	for i, v := range values {
		switch x := v.(type) {
		case float32:
			if math.Float32bits(x) != math.Float32bits(r.Values[i].(float32)) {
				t.Fatal("float32 bits")
			}
		case float64:
			if math.Float64bits(x) != math.Float64bits(r.Values[i].(float64)) {
				t.Fatal("float64 bits")
			}
		}
	}
	if string(r.Physical) != `{"RowID":18446744073709551615}` {
		t.Fatal("physical integer")
	}
	if _, err = d.Next(); err != io.EOF || d.Complete() != (format == "jsonl") {
		t.Fatal("completion", err)
	}
}
func TestValues(t *testing.T) {
	values := []any{nil, "", []byte{}, []byte{0, 255, 10}, "a,\"b\"\r\n你好\x00", int8(-128), int16(-32768), int32(-2147483648), int64(math.MinInt64), uint8(255), uint16(65535), uint32(math.MaxUint32), uint64(math.MaxUint64), int(-1), uint(1), float32(math.Copysign(0, -1)), math.Copysign(0, -1), float32(math.SmallestNonzeroFloat32), math.MaxFloat64, "12345678901234567890.00000", true,
		innodb.JSONValue{Kind: "array", BinaryType: 2, Elements: []innodb.JSONValue{{Kind: "unsigned", BinaryType: 10, Value: uint64(math.MaxUint64)}, {Kind: "double", BinaryType: 11, Value: math.Copysign(0, -1)}, {Kind: "opaque", BinaryType: 15, Opaque: &innodb.JSONOpaque{FieldType: 250, Data: []byte{0, 255}}}, {Kind: "null", BinaryType: 4}}}}
	for _, format := range []string{"jsonl", "csv"} {
		t.Run(format, func(t *testing.T) { roundTrip(t, format, values) })
	}
}
func TestProtocolErrors(t *testing.T) {
	var b bytes.Buffer
	e, _ := NewEncoder(&b, "jsonl", header(1, false), Options{})
	_ = e.Write(Row{Values: []any{uint64(9)}})
	prefix := b.String()
	_ = e.Finish()
	full := b.String()
	bad := []string{prefix, full + "{}\n", strings.Replace(full, `"rows":"1"`, `"rows":"2"`, 1), strings.Replace(full, `"uint64"`, `"unknown"`, 1), strings.Replace(full, `"value":"9"`, `"value":9`, 1), strings.Replace(full, `"value":"9"`, `"value":"18446744073709551616"`, 1), strings.Replace(full, `"kind":"row"`, `"kind":"schema"`, 1), strings.Replace(full, `"type":"uint64"`, `"type":"null"`, 1), strings.Replace(full, `"type":"uint64"`, `"type":"string","extra":1`, 1), full[:len(full)-1]}
	for i, s := range bad {
		d, err := NewDecoder(strings.NewReader(s), "jsonl", Options{})
		if err == nil {
			for {
				_, err = d.Next()
				if err != nil {
					break
				}
			}
		}
		if err == nil || err == io.EOF {
			t.Errorf("accepted case %d", i)
		}
	}
	if _, err := NewDecoder(strings.NewReader(full), "jsonl", Options{MaxRecordBytes: 8}); err == nil {
		t.Fatal("header limit")
	}
	for _, v := range []any{math.Inf(1), math.NaN(), complex(1, 2), string([]byte{255})} {
		var buf bytes.Buffer
		e, _ := NewEncoder(&buf, "jsonl", header(1, false), Options{})
		if e.Write(Row{Values: []any{v}}) == nil || e.Finish() == nil {
			t.Fatal("accepted bad/poisoned writer")
		}
	}
	var buf bytes.Buffer
	e, _ = NewEncoder(&buf, "jsonl", header(1, false), Options{})
	if e.Write(Row{}) == nil {
		t.Fatal("width")
	}
	d, _ := NewDecoder(strings.NewReader(full), "jsonl", Options{})
	h := d.Header()
	h.Columns[0].Name = "mutated"
	if d.Header().Columns[0].Name == "mutated" {
		t.Fatal("header ownership")
	}
}

type failWriter struct{ remaining int }

func (w *failWriter) Write(b []byte) (int, error) {
	if len(b) > w.remaining {
		return 0, io.ErrClosedPipe
	}
	w.remaining -= len(b)
	return len(b), nil
}
func TestWriteErrors(t *testing.T) {
	for _, f := range []string{"jsonl", "csv"} {
		w := &failWriter{remaining: 2000}
		e, err := NewEncoder(w, f, header(1, false), Options{})
		if err != nil {
			t.Fatal(err)
		}
		w.remaining = 0
		if !errors.Is(e.Write(Row{Values: []any{"x"}}), io.ErrClosedPipe) || !errors.Is(e.Finish(), io.ErrClosedPipe) {
			t.Fatal("writer error lost")
		}
	}
}

// All existing SQL-validated fixtures pass through both public codecs. Pipe the
// export so the test retains no second whole-table serialization in memory.
func TestAllFixtures(t *testing.T) {
	paths, err := filepath.Glob("../testdata/*/*.ibd*")
	if err != nil {
		t.Fatal(err)
	}
	success, rejected, total := 0, 0, 0
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			var r io.Reader = f
			if strings.HasSuffix(path, ".gz") {
				z, err := gzip.NewReader(f)
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				r = z
			}
			b, err := io.ReadAll(r)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			result, err := innodb.ReadMaterializedAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				rejected++
				return
			}
			for _, format := range []string{"jsonl", "csv"} {
				pr, pw := io.Pipe()
				done := make(chan error, 1)
				go func() {
					e, err := NewEncoder(pw, format, Header{Version: Version, Columns: result.Columns, VirtualColumns: result.VirtualColumns, Physical: true}, Options{})
					if err == nil {
						for _, rec := range result.Result.Records {
							physical := rec
							physical.Values = nil
							p, x := json.Marshal(physical)
							if x != nil {
								err = x
								break
							}
							if err = e.Write(Row{Values: rec.Values, Physical: p}); err != nil {
								break
							}
						}
					}
					if err == nil {
						err = e.Finish()
					}
					pw.CloseWithError(err)
					done <- err
				}()
				func() {
					defer pr.Close()
					d, err := NewDecoder(pr, format, Options{})
					if err != nil {
						t.Error(err)
						return
					}
					if !reflect.DeepEqual(d.Header().Columns, result.Columns) {
						t.Error("column metadata")
						return
					}
					for i, rec := range result.Result.Records {
						row, err := d.Next()
						if err != nil {
							t.Errorf("%s row %d: %v", format, i, err)
							return
						}
						if !reflect.DeepEqual(rec.Values, row.Values) {
							t.Errorf("%s row %d value/type mismatch", format, i)
							return
						}
						physical := rec
						physical.Values = nil
						want, _ := json.Marshal(physical)
						if !bytes.Equal(want, row.Physical) {
							t.Errorf("%s physical row %d", format, i)
							return
						}
					}
					if _, err = d.Next(); err != io.EOF || d.Complete() != (format == "jsonl") {
						t.Error("completion", err)
					}
				}()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}
			success++
			total += len(result.Result.Records)
		})
	}
	if success != 484 || rejected != 3 || total != 98811 {
		t.Fatalf("success=%d rejected=%d rows=%d", success, rejected, total)
	}
	t.Logf("%d fixtures, %d rows in each format, %d rejected", success, total, rejected)
}
func FuzzDecoder(f *testing.F) {
	var b bytes.Buffer
	e, _ := NewEncoder(&b, "jsonl", header(1, false), Options{})
	_ = e.Write(Row{Values: []any{uint64(math.MaxUint64)}})
	_ = e.Finish()
	f.Add(b.Bytes(), false)
	f.Add([]byte("bad\n"), true)
	f.Fuzz(func(t *testing.T, b []byte, csv bool) {
		if len(b) > 1<<20 {
			return
		}
		format := "jsonl"
		if csv {
			format = "csv"
		}
		d, err := NewDecoder(bytes.NewReader(b), format, Options{MaxRecordBytes: 1 << 20})
		if err != nil {
			return
		}
		for i := 0; i <= len(b); i++ {
			if _, err = d.Next(); err != nil {
				return
			}
		}
		t.Fatal("decoder did not terminate")
	})
}

func TestWireBoundaries(t *testing.T) {
	for _, format := range []string{"jsonl", "csv"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			e, err := NewEncoder(&buf, format, header(1, false), Options{MaxRecordBytes: 512})
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Write(Row{Values: []any{strings.Repeat("x", 1024)}}); err == nil || e.Finish() == nil {
				t.Fatal("encoder row budget")
			}
			buf.Reset()
			e, _ = NewEncoder(&buf, format, header(1, false), Options{})
			_ = e.Write(Row{Values: []any{strings.Repeat("x", 1024)}})
			_ = e.Finish()
			d, err := NewDecoder(bytes.NewReader(buf.Bytes()), format, Options{MaxRecordBytes: 512})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.Next(); err == nil {
				t.Fatal("decoder row budget")
			}
			// Truncation at any byte after the header is rejected except at a complete
			// CSV record boundary (CSV has no in-band producer completion marker).
			buf.Reset()
			e, _ = NewEncoder(&buf, format, header(1, false), Options{})
			offset := buf.Len()
			_ = e.Write(Row{Values: []any{"x\n\"y"}})
			_ = e.Finish()
			wire := append([]byte{}, buf.Bytes()...)
			for i := offset; i < len(wire); i++ {
				d, err := NewDecoder(bytes.NewReader(wire[:i]), format, Options{})
				if err == nil {
					for {
						_, err = d.Next()
						if err != nil {
							break
						}
					}
				}
				if err == io.EOF && !(format == "csv" && i == offset) {
					t.Fatalf("accepted truncation %d", i)
				}
			}
			if format == "csv" {
				bad := append(append([]byte{}, wire[:offset]...), append([]byte("\n"), wire[offset:]...)...)
				d, err = NewDecoder(bytes.NewReader(bad), format, Options{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = d.Next(); err == nil || err == io.EOF {
					t.Fatal("blank record hid subsequent rows")
				}
			}
			if err = e.Finish(); err == nil {
				t.Fatal("double finish")
			}
			if err = e.Write(Row{Values: []any{"late"}}); err == nil {
				t.Fatal("late row")
			}
		})
	}
	for _, h := range []Header{{Version: 2, Columns: header(1, false).Columns}, {Version: 1}, {Version: 1, Columns: []innodb.Column{{Name: "x", Type: "INT"}, {Name: "x", Type: "INT"}}}} {
		if _, err := NewEncoder(io.Discard, "jsonl", h, Options{}); err == nil {
			t.Fatal("bad header")
		}
	}
	for _, format := range []string{"bad", ""} {
		if _, err := NewEncoder(io.Discard, format, header(1, false), Options{}); err == nil {
			t.Fatal("unknown format")
		}
		if _, err := NewDecoder(strings.NewReader(""), format, Options{}); err == nil {
			t.Fatal("decoder format")
		}
	}
	if _, err := NewEncoder(io.Discard, "csv", header(1, false), Options{MaxRecordBytes: -1}); err == nil {
		t.Fatal("negative budget")
	}
	for _, raw := range []json.RawMessage{nil, []byte(`null`), []byte(`[]`), []byte(`{"x":1} {}`)} {
		e, _ := NewEncoder(io.Discard, "csv", header(1, true), Options{})
		if e.Write(Row{Values: []any{nil}, Physical: raw}) == nil {
			t.Fatal("invalid physical object")
		}
	}
	e, _ := NewEncoder(io.Discard, "jsonl", header(1, false), Options{})
	if e.Write(Row{Values: []any{nil}, Physical: []byte(`{}`)}) == nil {
		t.Fatal("unexpected physical")
	}
	// JSON depth limits and malformed kind/tag combinations are checked before
	// recursion can exhaust the stack; arbitrary nested scalar values are rejected.
	v := innodb.JSONValue{Kind: "null", BinaryType: 4}
	for i := 0; i < 101; i++ {
		v = innodb.JSONValue{Kind: "array", BinaryType: 2, Elements: []innodb.JSONValue{v}}
	}
	for _, v := range []any{v, innodb.JSONValue{Kind: "integer", BinaryType: 9, Value: "1"}, innodb.JSONValue{Kind: "null", BinaryType: 99}, innodb.JSONValue{Kind: "string", BinaryType: 12, Value: innodb.JSONValue{Kind: "null", BinaryType: 4}}} {
		if _, err := encodeValue(v); err == nil {
			t.Fatal("bad JSON tree")
		}
	}
}

func TestInvalidMetadataText(t *testing.T) {
	for _, modify := range []func(*Header){func(h *Header) { h.Columns[0].Name = string([]byte{255}) }, func(h *Header) { h.Columns[0].EnumValues = []string{string([]byte{255})} }, func(h *Header) {
		h.VirtualColumns = []innodb.VirtualColumn{{Name: "v", Expression: string([]byte{255})}}
	}} {
		h := header(1, false)
		modify(&h)
		var out bytes.Buffer
		if _, err := NewEncoder(&out, "jsonl", h, Options{}); err == nil || out.Len() != 0 {
			t.Fatal("metadata silently replaced invalid UTF-8")
		}
	}
}
