package rowio

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"io"
	"math"
	"reflect"
	"strconv"
	"unicode/utf8"
)

type cell struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value,omitempty"`
}

func strict(b []byte, v any) error {
	if !utf8.Valid(b) {
		return fmt.Errorf("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

func encodeValue(v any) (cell, error) {
	c := cell{Type: "null"}
	if v == nil {
		return c, nil
	}
	var payload any
	switch x := v.(type) {
	case string:
		if !utf8.ValidString(x) {
			return c, fmt.Errorf("invalid UTF-8 string")
		}
		c.Type = "string"
		payload = x
	case bool:
		c.Type = "bool"
		payload = x
	case []byte:
		c.Type = "binary"
		payload = base64.StdEncoding.EncodeToString(x)
	case innodb.JSONValue:
		c.Type = "json"
		n := 0
		tree, err := encodeTree(x, 1, &n)
		if err != nil {
			return c, err
		}
		payload = tree
	case innodb.GeometryValue:
		c.Type = "geometry"
		data := make([]byte, 4+len(x.WKB))
		binary.LittleEndian.PutUint32(data, x.SRID)
		copy(data[4:], x.WKB)
		checked, err := innodb.DecodeGeometry(data)
		if err != nil {
			return c, err
		}
		want, _ := json.Marshal(checked.Geometry)
		got, err := json.Marshal(x.Geometry)
		if err != nil {
			return c, err
		}
		if !bytes.Equal(want, got) {
			return c, fmt.Errorf("geometry tree disagrees with WKB")
		}
		payload = geometryWire{x.SRID, x.WKB}
	default:
		r := reflect.ValueOf(v)
		c.Type = r.Kind().String()
		switch r.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			payload = strconv.FormatInt(r.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			payload = strconv.FormatUint(r.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			f := r.Float()
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return c, fmt.Errorf("non-finite float")
			}
			payload = strconv.FormatFloat(f, 'g', -1, r.Type().Bits())
		default:
			return c, fmt.Errorf("unsupported value %T", v)
		}
	}
	b, err := json.Marshal(payload)
	c.Value = b
	return c, err
}

type geometryWire struct {
	SRID uint32 `json:"srid"`
	WKB  []byte `json:"wkb"`
}

func decodeValue(c cell) (any, error) {
	if c.Type == "null" {
		if len(c.Value) != 0 {
			return nil, fmt.Errorf("null has payload")
		}
		return nil, nil
	}
	if len(c.Value) == 0 || bytes.Equal(c.Value, []byte("null")) {
		return nil, fmt.Errorf("missing %s payload", c.Type)
	}
	switch c.Type {
	case "bool":
		var v bool
		err := strict(c.Value, &v)
		return v, err
	case "json":
		var v treeWire
		if err := strict(c.Value, &v); err != nil {
			return nil, err
		}
		n := 0
		return decodeTree(v, 1, &n)
	case "geometry":
		var v geometryWire
		if err := strict(c.Value, &v); err != nil {
			return nil, err
		}
		b := make([]byte, 4+len(v.WKB))
		binary.LittleEndian.PutUint32(b, v.SRID)
		copy(b[4:], v.WKB)
		return innodb.DecodeGeometry(b)
	}
	var s string
	if err := strict(c.Value, &s); err != nil {
		return nil, err
	}
	switch c.Type {
	case "string":
		return s, nil
	case "binary":
		return base64.StdEncoding.Strict().DecodeString(s)
	case "float32", "float64":
		bits := 64
		if c.Type == "float32" {
			bits = 32
		}
		v, err := strconv.ParseFloat(s, bits)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("invalid %s", c.Type)
		}
		if bits == 32 {
			return float32(v), nil
		}
		return v, nil
	}
	types := map[string]reflect.Type{"int": reflect.TypeOf(int(0)), "int8": reflect.TypeOf(int8(0)), "int16": reflect.TypeOf(int16(0)), "int32": reflect.TypeOf(int32(0)), "int64": reflect.TypeOf(int64(0)), "uint": reflect.TypeOf(uint(0)), "uint8": reflect.TypeOf(uint8(0)), "uint16": reflect.TypeOf(uint16(0)), "uint32": reflect.TypeOf(uint32(0)), "uint64": reflect.TypeOf(uint64(0))}
	t, ok := types[c.Type]
	if !ok {
		return nil, fmt.Errorf("unknown value type %q", c.Type)
	}
	v := reflect.New(t).Elem()
	if t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64 {
		n, err := strconv.ParseUint(s, 10, t.Bits())
		if err != nil {
			return nil, err
		}
		v.SetUint(n)
	} else {
		n, err := strconv.ParseInt(s, 10, t.Bits())
		if err != nil {
			return nil, err
		}
		v.SetInt(n)
	}
	return v.Interface(), nil
}

type treeWire struct {
	Kind       string             `json:"kind"`
	BinaryType byte               `json:"binary_type"`
	Scalar     *cell              `json:"scalar,omitempty"`
	Members    []memberWire       `json:"members,omitempty"`
	Elements   []treeWire         `json:"elements,omitempty"`
	Opaque     *innodb.JSONOpaque `json:"opaque,omitempty"`
}
type memberWire struct {
	Key   string   `json:"key"`
	Value treeWire `json:"value"`
}

func treeLimit(depth int, n *int) error {
	*n++
	if depth > innodb.MaxJSONDepth || *n > innodb.MaxJSONNodes {
		return fmt.Errorf("JSON tree limit")
	}
	return nil
}
func encodeTree(v innodb.JSONValue, depth int, n *int) (treeWire, error) {
	w := treeWire{Kind: v.Kind, BinaryType: v.BinaryType, Opaque: v.Opaque}
	if err := treeLimit(depth, n); err != nil {
		return w, err
	}
	if v.Value != nil {
		switch v.Value.(type) {
		case bool, int64, uint64, float64, string:
		default:
			return w, fmt.Errorf("invalid JSON scalar %T", v.Value)
		}
		c, err := encodeValue(v.Value)
		if err != nil {
			return w, err
		}
		w.Scalar = &c
	}
	for _, m := range v.Members {
		if !utf8.ValidString(m.Key) {
			return w, fmt.Errorf("invalid JSON key")
		}
		c, err := encodeTree(m.Value, depth+1, n)
		if err != nil {
			return w, err
		}
		w.Members = append(w.Members, memberWire{m.Key, c})
	}
	for _, e := range v.Elements {
		c, err := encodeTree(e, depth+1, n)
		if err != nil {
			return w, err
		}
		w.Elements = append(w.Elements, c)
	}
	if err := validateTree(w); err != nil {
		return w, err
	}
	return w, nil
}
func validateTree(w treeWire) error {
	expected := ""
	switch w.Kind {
	case "object", "array", "null", "opaque", "decimal", "date", "time", "datetime", "timestamp":
	case "boolean":
		expected = "bool"
	case "integer":
		expected = "int64"
	case "unsigned":
		expected = "uint64"
	case "double":
		expected = "float64"
	case "string":
		expected = "string"
	default:
		return fmt.Errorf("unknown JSON kind %q", w.Kind)
	}
	if (expected == "") != (w.Scalar == nil) || w.Scalar != nil && w.Scalar.Type != expected {
		return fmt.Errorf("JSON scalar kind mismatch")
	}
	if len(w.Members) > 0 && w.Kind != "object" || len(w.Elements) > 0 && w.Kind != "array" {
		return fmt.Errorf("JSON container kind mismatch")
	}

	validTag := false
	switch w.Kind {
	case "object":
		validTag = w.BinaryType == 0 || w.BinaryType == 1
	case "array":
		validTag = w.BinaryType == 2 || w.BinaryType == 3
	case "null", "boolean":
		validTag = w.BinaryType == 4
	case "integer":
		validTag = w.BinaryType == 5 || w.BinaryType == 7 || w.BinaryType == 9
	case "unsigned":
		validTag = w.BinaryType == 6 || w.BinaryType == 8 || w.BinaryType == 10
	case "double":
		validTag = w.BinaryType == 11
	case "string":
		validTag = w.BinaryType == 12
	default:
		validTag = w.BinaryType == 15
	}
	if !validTag || (w.BinaryType == 15) != (w.Opaque != nil) {
		return fmt.Errorf("JSON binary type mismatch")
	}
	names := map[string]bool{}
	for _, m := range w.Members {
		if names[m.Key] {
			return fmt.Errorf("duplicate JSON member")
		}
		names[m.Key] = true
	}
	if w.Opaque != nil && !utf8.ValidString(w.Opaque.Decoded) {
		return fmt.Errorf("invalid opaque display")
	}
	return nil
}
func decodeTree(w treeWire, depth int, n *int) (innodb.JSONValue, error) {
	v := innodb.JSONValue{Kind: w.Kind, BinaryType: w.BinaryType, Opaque: w.Opaque}
	if w.Kind == "object" {
		v.Members = []innodb.JSONMember{}
	}
	if w.Kind == "array" {
		v.Elements = []innodb.JSONValue{}
	}
	if err := treeLimit(depth, n); err != nil {
		return v, err
	}
	if err := validateTree(w); err != nil {
		return v, err
	}
	if w.Scalar != nil {
		x, err := decodeValue(*w.Scalar)
		if err != nil {
			return v, err
		}
		v.Value = x
	}
	for _, m := range w.Members {
		x, err := decodeTree(m.Value, depth+1, n)
		if err != nil {
			return v, err
		}
		v.Members = append(v.Members, innodb.JSONMember{Key: m.Key, Value: x})
	}
	for _, e := range w.Elements {
		x, err := decodeTree(e, depth+1, n)
		if err != nil {
			return v, err
		}
		v.Elements = append(v.Elements, x)
	}
	return v, nil
}
