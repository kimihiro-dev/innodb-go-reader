package innodb

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MaxJSONDepth = 100
	MaxJSONNodes = 100000
)

// JSONValue preserves MySQL JSON types. SQL NULL remains nil in Record.Values;
// JSON null is JSONValue{Kind:"null", BinaryType:4}. Default JSON serialization
// exposes this typed representation; JSON() produces a conventional JSON view.
// Value is bool, int64, uint64, float64 or string for scalar kinds.
type JSONValue struct {
	Kind       string       `json:"kind"`
	BinaryType byte         `json:"binary_type"`
	Value      any          `json:"value,omitempty"`
	Members    []JSONMember `json:"members,omitempty"`
	Elements   []JSONValue  `json:"elements,omitempty"`
	Opaque     *JSONOpaque  `json:"opaque,omitempty"`
}

type JSONMember struct {
	Key   string    `json:"key"`
	Value JSONValue `json:"value"`
}

// JSONOpaque retains the original enum_field_types identifier and independent
// payload bytes even when a decoded decimal or temporal display is available.
type JSONOpaque struct {
	FieldType byte   `json:"field_type"`
	Data      []byte `json:"data"`
	Decoded   string `json:"decoded,omitempty"`
	Precision int    `json:"precision,omitempty"`
	Scale     int    `json:"scale,omitempty"`
}

// DecodeJSON decodes an initial (unfragmented) MySQL binary JSON document.
// It does not accept textual JSON or reconstruct partial-update history.
func DecodeJSON(data []byte) (JSONValue, error) {
	if uint64(len(data)) > uint64(MaxLOBValueBytes) {
		return JSONValue{}, fmt.Errorf("%w: JSON document exceeds 16 MiB", ErrUnsupported)
	}
	if len(data) == 0 {
		return JSONValue{}, fmt.Errorf("%w: empty binary JSON document", ErrUnsupported)
	}
	d := jsonBinaryDecoder{}
	value, n, err := d.value(data[0], data[1:], 1)
	if err == nil && n != len(data)-1 {
		err = fmt.Errorf("%w: trailing binary JSON bytes", ErrCorrupt)
	}
	if err != nil {
		return JSONValue{}, err
	}
	return value, nil
}

type jsonBinaryDecoder struct{ nodes int }

var jsonLE = binary.LittleEndian

func jsonLength(b []byte) (int, int, error) {
	var n uint64
	for i := 0; i < 5 && i < len(b); i++ {
		n |= uint64(b[i]&127) << uint(7*i)
		if b[i]&128 == 0 {
			if n > math.MaxUint32 || n > uint64(math.MaxInt) {
				break
			}
			return int(n), i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("%w: JSON variable length", ErrCorrupt)
}

func (d *jsonBinaryDecoder) value(tag byte, b []byte, depth int) (JSONValue, int, error) {
	v := JSONValue{BinaryType: tag}
	d.nodes++
	if depth > MaxJSONDepth || d.nodes > MaxJSONNodes {
		return v, 0, fmt.Errorf("%w: JSON depth/node limit", ErrUnsupported)
	}
	bad := func() (JSONValue, int, error) {
		return v, 0, fmt.Errorf("%w: binary JSON tag %02x length/value", ErrCorrupt, tag)
	}
	switch tag {
	case 0, 1, 2, 3:
		return d.container(tag, b, depth)
	case 4:
		if len(b) < 1 {
			return bad()
		}
		switch b[0] {
		case 0:
			v.Kind = "null"
		case 1:
			v.Kind = "boolean"
			v.Value = true
		case 2:
			v.Kind = "boolean"
			v.Value = false
		default:
			return bad()
		}
		return v, 1, nil
	case 5, 6, 7, 8, 9, 10:
		width := 2
		if tag >= 7 {
			width = 4
		}
		if tag >= 9 {
			width = 8
		}
		if len(b) < width {
			return bad()
		}
		var n uint64
		switch width {
		case 2:
			n = uint64(jsonLE.Uint16(b))
		case 4:
			n = uint64(jsonLE.Uint32(b))
		case 8:
			n = jsonLE.Uint64(b)
		}
		if tag%2 == 0 {
			v.Kind = "unsigned"
			v.Value = n
		} else {
			v.Kind = "integer"
			switch width {
			case 2:
				v.Value = int64(int16(n))
			case 4:
				v.Value = int64(int32(n))
			case 8:
				v.Value = int64(n)
			}
		}
		return v, width, nil
	case 11:
		if len(b) < 8 {
			return bad()
		}
		n := math.Float64frombits(jsonLE.Uint64(b))
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return bad()
		}
		v.Kind = "double"
		v.Value = n
		return v, 8, nil
	case 12, 15:
		pos := 0
		if tag == 15 {
			if len(b) < 1 {
				return bad()
			}
			pos = 1
		}
		n, prefix, err := jsonLength(b[pos:])
		if err != nil {
			return v, 0, err
		}
		pos += prefix
		if n > len(b)-pos {
			return bad()
		}
		payload := b[pos : pos+n]
		if tag == 12 {
			if !utf8.Valid(payload) {
				return bad()
			}
			v.Kind = "string"
			v.Value = string(payload)
		} else {
			v.Kind = "opaque"
			o := &JSONOpaque{FieldType: b[0], Data: append([]byte{}, payload...)}
			v.Opaque = o
			switch o.FieldType {
			case 253:
				if !utf8.Valid(payload) {
					return bad()
				}
				o.Decoded = string(payload)
			case 246:
				if len(payload) < 3 {
					return bad()
				}
				o.Precision, o.Scale = int(payload[0]), int(payload[1])
				if o.Precision < 1 || o.Precision > 81 || o.Scale > o.Precision || (o.Precision-o.Scale+8)/9+(o.Scale+8)/9 > 9 {
					return bad()
				}
				o.Decoded, err = decodeDecimal(payload[2:], Column{Type: "DECIMAL", Precision: o.Precision, Scale: o.Scale})
				v.Kind = "decimal"
			case 7, 10, 11, 12:
				o.Decoded, err = jsonPackedTime(o.FieldType, payload)
				v.Kind = map[byte]string{7: "timestamp", 10: "date", 11: "time", 12: "datetime"}[o.FieldType]
			}
			if err != nil {
				return v, 0, err
			}
		}
		return v, pos + n, nil
	}
	return bad()
}

func (d *jsonBinaryDecoder) container(tag byte, b []byte, depth int) (JSONValue, int, error) {
	v := JSONValue{BinaryType: tag, Kind: "array"}
	object := tag < 2
	large := tag&1 != 0
	w := 2
	if large {
		w = 4
	}
	if object {
		v.Kind = "object"
	}
	bad := func(s string) (JSONValue, int, error) {
		return v, 0, fmt.Errorf("%w: JSON container %s", ErrCorrupt, s)
	}
	if len(b) < 2*w {
		return bad("header")
	}
	read := func(b []byte) int {
		if w == 2 {
			return int(jsonLE.Uint16(b))
		}
		n := jsonLE.Uint32(b)
		if uint64(n) > uint64(math.MaxInt) {
			return math.MaxInt
		}
		return int(n)
	}
	count, size := read(b), read(b[w:])
	entry := w + 1
	per := entry
	if object {
		per += w + 2
	}
	if size > len(b) || size < 2*w || count > (size-2*w)/per {
		return bad("count/size")
	}
	if count > MaxJSONNodes-d.nodes {
		return v, 0, fmt.Errorf("%w: JSON node limit", ErrUnsupported)
	}
	b = b[:size]
	headerEnd := 2*w + count*per
	type span struct{ start, end int }
	spans := make([]span, 0, count*2)
	valueBase := 2 * w
	keys := []string(nil)
	if object {
		keys = make([]string, count)
		valueBase += count * (w + 2)
		for i := 0; i < count; i++ {
			e := b[2*w+i*(w+2):]
			off, n := read(e), int(jsonLE.Uint16(e[w:]))
			if off < headerEnd || off > size || n > size-off {
				return bad("key offset/length")
			}
			key := b[off : off+n]
			if !utf8.Valid(key) {
				return bad("key UTF-8")
			}
			keys[i] = string(key)
			if i > 0 && (len(keys[i-1]) > n || len(keys[i-1]) == n && keys[i-1] >= keys[i]) {
				return bad("key order/duplicate")
			}
			spans = append(spans, span{off, off + n})
		}
		v.Members = make([]JSONMember, 0, count)
	} else {
		v.Elements = make([]JSONValue, 0, count)
	}
	for i := 0; i < count; i++ {
		e := b[valueBase+i*entry : valueBase+(i+1)*entry]
		t := e[0]
		inline := t == 4 || t == 5 || t == 6 || large && (t == 7 || t == 8)
		payload := e[1:]
		off := 0
		if !inline {
			off = read(e[1:])
			if off < headerEnd || off >= size {
				return bad("value offset/overlap")
			}
			payload = b[off:]
		}
		child, n, err := d.value(t, payload, depth+1)
		if err != nil {
			return v, 0, err
		}
		if !inline {
			spans = append(spans, span{off, off + n})
		}
		if object {
			v.Members = append(v.Members, JSONMember{keys[i], child})
		} else {
			v.Elements = append(v.Elements, child)
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	end := headerEnd
	for _, s := range spans {
		if s.start == s.end {
			continue
		} // empty object keys occupy no bytes
		if s.start < end {
			return bad("overlapping key/value payloads")
		}
		end = s.end
	}
	return v, size, nil
}

// JSON temporal payloads are signed little-endian packed numbers, not the
// big-endian DATE/TIME2/DATETIME2 encodings used by ordinary table columns.
func jsonPackedTime(tag byte, b []byte) (string, error) {
	bad := func() (string, error) { return "", fmt.Errorf("%w: JSON packed temporal", ErrCorrupt) }
	if len(b) != 8 {
		return bad()
	}
	n := int64(jsonLE.Uint64(b))
	negative := n < 0
	if n == math.MinInt64 {
		return bad()
	}
	if negative {
		n = -n
	}
	fraction := n & 0xffffff
	integer := n >> 24
	if fraction > 999999 {
		return bad()
	}
	second, minute := integer&63, (integer>>6)&63
	if second > 59 || minute > 59 {
		return bad()
	}
	if tag == 11 {
		hour := integer >> 12
		if hour > 838 || hour == 838 && minute == 59 && second == 59 && fraction != 0 {
			return bad()
		}
		sign := ""
		if negative {
			sign = "-"
		}
		return fmt.Sprintf("%s%02d:%02d:%02d.%06d", sign, hour, minute, second, fraction), nil
	}
	if negative {
		return bad()
	}
	hour := (integer >> 12) & 31
	ymd := integer >> 17
	day := ymd & 31
	ym := ymd >> 5
	month, year := ym%13, ym/13
	if year > 9999 || hour > 23 {
		return bad()
	}
	date := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	if tag == 10 {
		if hour != 0 || minute != 0 || second != 0 || fraction != 0 {
			return bad()
		}
		return date, nil
	}
	return fmt.Sprintf("%s %02d:%02d:%02d.%06d", date, hour, minute, second, fraction), nil
}

// JSON returns a conventional JSON view. Decimal precision/scale, opaque type
// identity and temporal types remain available in the typed tree, not this view.
func (v JSONValue) JSON() (json.RawMessage, error) {
	var b bytes.Buffer
	nodes := 0
	var write func(JSONValue, int) error
	write = func(v JSONValue, depth int) error {
		nodes++
		if depth > MaxJSONDepth || nodes > MaxJSONNodes {
			return fmt.Errorf("%w: JSON display limit", ErrUnsupported)
		}
		switch v.Kind {
		case "null":
			b.WriteString("null")
		case "object":
			b.WriteByte('{')
			for i, m := range v.Members {
				if i > 0 {
					b.WriteByte(',')
				}
				key, err := json.Marshal(m.Key)
				if err != nil {
					return err
				}
				b.Write(key)
				b.WriteByte(':')
				if err := write(m.Value, depth+1); err != nil {
					return err
				}
			}
			b.WriteByte('}')
		case "array":
			b.WriteByte('[')
			for i, e := range v.Elements {
				if i > 0 {
					b.WriteByte(',')
				}
				if err := write(e, depth+1); err != nil {
					return err
				}
			}
			b.WriteByte(']')
		case "boolean", "integer", "unsigned", "double", "string":
			data, err := json.Marshal(v.Value)
			if err != nil {
				return err
			}
			if v.Kind == "double" && !bytes.ContainsAny(data, ".eE") {
				data = append(data, '.', '0')
			}
			b.Write(data)
		case "decimal":
			if v.Opaque == nil {
				return fmt.Errorf("%w: missing JSON opaque data", ErrCorrupt)
			}
			// Validate even caller-modified display values before emitting raw numbers.
			if !json.Valid([]byte(v.Opaque.Decoded)) {
				return fmt.Errorf("%w: invalid JSON decimal display", ErrCorrupt)
			}
			if first := v.Opaque.Decoded[0]; first != '-' && (first < '0' || first > '9') {
				return fmt.Errorf("%w: invalid JSON decimal display", ErrCorrupt)
			}
			b.WriteString(v.Opaque.Decoded)
		case "date", "time", "datetime", "timestamp", "opaque":
			if v.Opaque == nil {
				return fmt.Errorf("%w: missing JSON opaque data", ErrCorrupt)
			}
			text := v.Opaque.Decoded
			if v.Kind == "opaque" && v.Opaque.FieldType != 253 {
				raw := base64.StdEncoding.EncodeToString(v.Opaque.Data)
				var lines []string
				for len(raw) > 76 {
					lines = append(lines, raw[:76])
					raw = raw[76:]
				}
				lines = append(lines, raw)
				text = fmt.Sprintf("base64:type%d:%s", v.Opaque.FieldType, strings.Join(lines, "\n"))
			}
			data, err := json.Marshal(text)
			if err != nil {
				return err
			}
			b.Write(data)
		default:
			return fmt.Errorf("%w: JSON kind %q", ErrUnsupported, v.Kind)
		}
		return nil
	}
	if err := write(v, 1); err != nil {
		return nil, err
	}
	return json.RawMessage(b.Bytes()), nil
}
