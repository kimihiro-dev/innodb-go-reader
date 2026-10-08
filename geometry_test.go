package innodb

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

// This is only a test projection for comparison with MySQL ST_AsGeoJSON;
// the public API intentionally does not assign GeoJSON axis semantics.
func geometrySQLCoordinates(g Geometry) any {
	point := func(p Coordinate) []float64 { return []float64{p.X, p.Y} }
	points := func(ps []Coordinate) [][]float64 {
		out := make([][]float64, 0, len(ps))
		for _, p := range ps {
			out = append(out, point(p))
		}
		return out
	}
	names := map[string]string{"POINT": "Point", "LINESTRING": "LineString", "POLYGON": "Polygon", "MULTIPOINT": "MultiPoint", "MULTILINESTRING": "MultiLineString", "MULTIPOLYGON": "MultiPolygon", "GEOMETRYCOLLECTION": "GeometryCollection"}
	out := map[string]any{"type": names[g.Type]}
	switch g.Type {
	case "POINT":
		out["coordinates"] = point(g.Points[0])
	case "LINESTRING":
		out["coordinates"] = points(g.Points)
	case "POLYGON":
		rings := make([]any, 0, len(g.Rings))
		for _, r := range g.Rings {
			rings = append(rings, points(r))
		}
		out["coordinates"] = rings
	default:
		children := make([]any, 0, len(g.Geometries))
		for _, c := range g.Geometries {
			v := geometrySQLCoordinates(c).(map[string]any)
			if g.Type == "GEOMETRYCOLLECTION" {
				children = append(children, v)
			} else {
				children = append(children, v["coordinates"])
			}
		}
		if g.Type == "GEOMETRYCOLLECTION" {
			out["geometries"] = children
		} else {
			out["coordinates"] = children
		}
	}
	return out
}
func TestGeometryFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/geometry/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			Name, SHA256 string
			Rows         int
		}
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	total, external, swapped := 0, 0, 0
	for _, c := range manifest.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b := unzip(t, "testdata/geometry/"+c.Name+".ibd.gz")
			if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA256 {
				t.Fatal("SHA256")
			}
			sdi, err := ReadSDI(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			var official []json.RawMessage
			if err = json.Unmarshal(unzip(t, "testdata/geometry/"+c.Name+".sdi.json.gz"), &official); err != nil {
				t.Fatal(err)
			}
			if len(official) != len(sdi.Records)+1 {
				t.Fatal("SDI count")
			}
			for i, r := range sdi.Records {
				var w struct {
					Type   uint32
					ID     uint64
					Object json.RawMessage
				}
				if err = json.Unmarshal(official[i+1], &w); err != nil {
					t.Fatal(err)
				}
				if r.Key != (SDIKey{w.Type, w.ID}) || !reflect.DeepEqual(jsonTextValue(t, r.JSON), jsonTextValue(t, w.Object)) {
					t.Fatal("official SDI")
				}
			}

			schemaBytes, err := os.ReadFile("testdata/geometry/" + c.Name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var schema Schema
			if err = json.Unmarshal(schemaBytes, &schema); err != nil {
				t.Fatal(err)
			}
			meta, err := InspectTable(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if meta.Schema == nil || !reflect.DeepEqual(*meta.Schema, schema) {
				t.Fatalf("schema: %+v want %+v", meta, schema)
			}
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			manual, err := Read(bytes.NewReader(b), int64(len(b)), schema)
			if err != nil || !reflect.DeepEqual(got, manual) {
				t.Fatal("manual/auto", err)
			}
			var expected []struct {
				ID                   int32
				SQLNull              bool `json:"sql_null"`
				Raw, WKB, Type, Note string
				SRID                 uint32
				Geo                  json.RawMessage
			}
			// default_wkb has an underscore; decode explicitly to retain the axis evidence.
			var axis []struct {
				Default string `json:"default_wkb"`
			}
			exp := unzip(t, "testdata/geometry/"+c.Name+".expected.json.gz")
			if err = json.Unmarshal(exp, &expected); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(exp, &axis); err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != c.Rows || len(expected) != c.Rows {
				t.Fatal("rows")
			}
			for i, r := range got.Records {
				want := expected[i]
				total++
				external += len(r.External)
				if r.Values[0] != want.ID || r.Values[2] != want.Note {
					t.Fatal("adjacent column", i)
				}
				if want.SQLNull {
					if r.Values[1] != nil {
						t.Fatal("NULL")
					}
					continue
				}
				v, ok := r.Values[1].(GeometryValue)
				if !ok {
					t.Fatalf("value: %T", r.Values[1])
				}
				if v.SRID != want.SRID || v.Geometry.Type != strings.ReplaceAll(strings.ToUpper(want.Type), "GEOMCOLLECTION", "GEOMETRYCOLLECTION") || !strings.EqualFold(hex.EncodeToString(v.WKB), want.WKB) {
					t.Fatal("WKB/SRID/type", i)
				}
				data := binary.LittleEndian.AppendUint32(nil, v.SRID)
				data = append(data, v.WKB...)
				if !strings.EqualFold(hex.EncodeToString(data), want.Raw) {
					t.Fatal("raw storage", i)
				}
				view, _ := json.Marshal(geometrySQLCoordinates(v.Geometry))
				if !reflect.DeepEqual(jsonTextValue(t, view), jsonTextValue(t, want.Geo)) {
					t.Fatalf("coordinates row %d: %s != %s", i, view, want.Geo)
				}
				if v.SRID == 4326 {
					if axis[i].Default == want.WKB {
						t.Fatal("expected SRS axis difference")
					}
					swapped++
				}
			}
			if c.Name == "geometry_tree" && len(got.Pages) < 2 {
				t.Fatal("tree must span pages")
			}
		})
	}
	if total != 628 || external != 1 || swapped != 3 {
		t.Fatal("coverage", total, external, swapped)
	}
	t.Logf("%d rows, %d external values, %d geographic axis comparisons", total, external, swapped)
}

func wkbHeader(typ uint32, marker byte) []byte {
	b := []byte{marker}
	var o binary.AppendByteOrder = binary.LittleEndian
	if marker == 0 {
		o = binary.BigEndian
	}
	return o.AppendUint32(b, typ)
}
func wkbPoint(x, y float64, marker byte) []byte {
	b := wkbHeader(1, marker)
	var o binary.AppendByteOrder = binary.LittleEndian
	if marker == 0 {
		o = binary.BigEndian
	}
	b = o.AppendUint64(b, math.Float64bits(x))
	return o.AppendUint64(b, math.Float64bits(y))
}
func wkbGroup(typ uint32, children ...[]byte) []byte {
	b := binary.LittleEndian.AppendUint32(wkbHeader(typ, 1), uint32(len(children)))
	for _, c := range children {
		b = append(b, c...)
	}
	return b
}
func storedGeometry(wkb []byte) []byte { return append([]byte{0, 0, 0, 0}, wkb...) }
func TestGeometryEncoding(t *testing.T) {
	for _, order := range []byte{0, 1} {
		b := storedGeometry(wkbPoint(math.Copysign(0, -1), math.SmallestNonzeroFloat64, order))
		v, err := DecodeGeometry(b)
		if err != nil {
			t.Fatal(err)
		}
		if v.Geometry.ByteOrder != order || !math.Signbit(v.Geometry.Points[0].X) || v.Geometry.Points[0].Y != math.SmallestNonzeroFloat64 {
			t.Fatal("bits")
		}
		b[4] = 7
		if v.WKB[0] != order {
			t.Fatal("alias")
		}
	}
	mixed := storedGeometry(wkbGroup(7, wkbPoint(1, 2, 0), wkbGroup(4, wkbPoint(3, 4, 1))))
	v, err := DecodeGeometry(mixed)
	if err != nil || len(v.Geometry.Geometries) != 2 {
		t.Fatal(err)
	}
	for i := 0; i < len(mixed); i++ {
		if _, err := DecodeGeometry(mixed[:i]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("truncation %d: %v", i, err)
		}
	}
	zero, one := uint32(0), uint32(1)
	for _, c := range []Column{{Type: "GEOMETRY"}, {Type: "POINT", SRID: &zero}} {
		if _, err := geometryValue(c, storedGeometry(wkbPoint(1, 2, 1))); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []Column{{Type: "LINESTRING"}, {Type: "POINT", SRID: &one}} {
		if _, err := geometryValue(c, storedGeometry(wkbPoint(1, 2, 1))); !errors.Is(err, ErrCorrupt) {
			t.Fatal(err)
		}
	}
}
func TestGeometryDamage(t *testing.T) {
	badOrder := wkbPoint(1, 2, 1)
	badOrder[0] = 2
	shortLine := binary.LittleEndian.AppendUint32(wkbHeader(2, 1), 1)
	shortLine = append(shortLine, wkbPoint(0, 0, 1)[5:]...)
	polygon := binary.LittleEndian.AppendUint32(wkbHeader(3, 1), 1)
	polygon = binary.LittleEndian.AppendUint32(polygon, 4)
	for _, p := range []Coordinate{{0, 0}, {1, 0}, {1, 1}, {2, 2}} {
		polygon = append(polygon, wkbPoint(p.X, p.Y, 1)[5:]...)
	}
	for name, b := range map[string][]byte{"byte_order": badOrder, "nan": wkbPoint(math.NaN(), 0, 1), "inf": wkbPoint(0, math.Inf(1), 1), "one_point_line": shortLine, "unclosed": polygon, "empty_polygon": wkbGroup(3), "empty_multi": wkbGroup(4), "wrong_child": wkbGroup(4, wkbGroup(7)), "count": binary.LittleEndian.AppendUint32(wkbHeader(7, 1), math.MaxUint32), "trailing": append(wkbPoint(1, 2, 1), 0)} {
		t.Run(name, func(t *testing.T) {
			v, err := DecodeGeometry(storedGeometry(b))
			if !errors.Is(err, ErrCorrupt) || !reflect.DeepEqual(v, GeometryValue{}) {
				t.Fatal(v, err)
			}
		})
	}
	for _, typ := range []uint32{0, 8, 1001, 2001, 3001, 0x20000001} {
		if _, err := DecodeGeometry(storedGeometry(wkbHeader(typ, 1))); !errors.Is(err, ErrUnsupported) {
			t.Fatal(typ, err)
		}
	}
}
func TestGeometryLimits(t *testing.T) {
	b := wkbPoint(0, 0, 1)
	for i := 1; i < MaxGeometryDepth; i++ {
		b = wkbGroup(7, b)
	}
	if _, err := DecodeGeometry(storedGeometry(b)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeGeometry(storedGeometry(wkbGroup(7, b))); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	for _, n := range []int{MaxGeometryNodes - 1, MaxGeometryNodes} {
		b := binary.LittleEndian.AppendUint32(wkbHeader(7, 1), uint32(n))
		for i := 0; i < n; i++ {
			b = append(b, wkbGroup(7)...)
		}
		_, err := DecodeGeometry(storedGeometry(b))
		if n == MaxGeometryNodes-1 && err != nil || n == MaxGeometryNodes && !errors.Is(err, ErrUnsupported) {
			t.Fatal(n, err)
		}
	}
	for _, n := range []int{MaxGeometryPoints, MaxGeometryPoints + 1} {
		b := binary.LittleEndian.AppendUint32(wkbHeader(2, 1), uint32(n))
		b = append(b, make([]byte, n*16)...)
		_, err := DecodeGeometry(storedGeometry(b))
		if n == MaxGeometryPoints && err != nil || n > MaxGeometryPoints && !errors.Is(err, ErrUnsupported) {
			t.Fatal(n, err)
		}
	}
	if _, err := DecodeGeometry(make([]byte, int(MaxLOBValueBytes)+1)); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
func TestGeometryReadDamage(t *testing.T) {
	for _, name := range []string{"geometry_point", "geometry_large"} {
		t.Run(name, func(t *testing.T) {
			b := unzip(t, "testdata/geometry/"+name+".ibd.gz")
			r, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			rec := r.Records[len(r.Records)-1]
			offset := int(rec.PageNumber)*PageSize + rec.Offset + 17
			if len(rec.External) > 0 {
				c := rec.External[0].Chunks[0]
				offset = int(c.PageNumber)*PageSize + c.Offset
			}
			b[offset+4] = 2
			resealTestPages(b)
			got, err := ReadAuto(bytes.NewReader(b), int64(len(b)))
			if !errors.Is(err, ErrCorrupt) || got != nil {
				t.Fatal("partial or missed damage", err)
			}
		})
	}
}
func FuzzGeometry(f *testing.F) {
	for _, b := range [][]byte{wkbPoint(1, 2, 1), wkbGroup(7), wkbGroup(4, wkbPoint(3, 4, 0))} {
		f.Add(storedGeometry(b))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		v, err := DecodeGeometry(b)
		if err != nil {
			if !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupported) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(v, GeometryValue{}) {
				t.Fatal("partial")
			}
		} else if !bytes.Equal(v.WKB, b[4:]) {
			t.Fatal("bytes")
		}
	})
}

func TestGeometrySchema(t *testing.T) {
	zero := uint32(0)
	for _, c := range []Column{{Name: "g", Type: "INT", SRID: &zero}, {Name: "g", Type: "POINT", Unsigned: true}, {Name: "g", Type: "POINT", MaxBytes: 25}, {Name: "g", Type: "POINT", MaxChars: 1}} {
		s := Schema{Columns: []Column{{Name: "id", Type: "INT"}, c}, PrimaryKey: "id", RootPage: 4, SpaceID: 1, IndexID: 1}
		if _, err := s.validate(); !errors.Is(err, ErrUnsupported) {
			t.Fatal(c, err)
		}
	}
	for _, options := range []string{"", "geom_type=x;", "geom_type=1;geom_type=2;"} {
		if _, _, err := metadataColumn(ddColumn{Type: 30, Collation: 63, Length: 4294967295, Options: options}); !errors.Is(err, ErrCorrupt) {
			t.Fatal(options, err)
		}
	}
	for _, c := range []ddColumn{{Type: 30, Collation: 255, Length: 4294967295, Options: "geom_type=1;"}, {Type: 30, Collation: 63, Length: 25, Options: "geom_type=1;"}, {Type: 30, Collation: 63, Length: 4294967295, Options: "geom_type=8;"}} {
		if _, issue, err := metadataColumn(c); err != nil || issue == "" {
			t.Fatal(c, issue, err)
		}
	}
}
