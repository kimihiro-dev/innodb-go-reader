package innodb

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	MaxGeometryDepth  = 100
	MaxGeometryNodes  = 100000
	MaxGeometryPoints = 1000000
)

// GeometryValue retains the on-disk SRID, independent original WKB bytes, and
// decoded two-dimensional coordinates. No SRS lookup or axis conversion occurs.
type GeometryValue struct {
	SRID     uint32   `json:"srid"`
	WKB      []byte   `json:"wkb"`
	Geometry Geometry `json:"geometry"`
}

type Coordinate struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Geometry stores a Point as one Points entry, a LineString as Points, a
// Polygon as Rings, and Multi*/GeometryCollection as Geometries. ByteOrder is
// the original WKB marker: 0 big-endian, 1 little-endian.
type Geometry struct {
	Type       string         `json:"type"`
	ByteOrder  byte           `json:"byte_order"`
	Points     []Coordinate   `json:"points,omitempty"`
	Rings      [][]Coordinate `json:"rings,omitempty"`
	Geometries []Geometry     `json:"geometries,omitempty"`
}

var geometryTypes = [...]string{"GEOMETRY", "POINT", "LINESTRING", "POLYGON", "MULTIPOINT", "MULTILINESTRING", "MULTIPOLYGON", "GEOMETRYCOLLECTION"}

func geometryType(t string) bool {
	for _, name := range geometryTypes {
		if t == name {
			return true
		}
	}
	return false
}

// DecodeGeometry reads MySQL's four-byte little-endian SRID followed by WKB.
// It validates structure, not topological validity or geographic coordinate ranges.
func DecodeGeometry(data []byte) (GeometryValue, error) {
	if uint64(len(data)) > uint64(MaxLOBValueBytes) {
		return GeometryValue{}, fmt.Errorf("%w: geometry exceeds 16 MiB", ErrUnsupported)
	}
	if len(data) < 9 {
		return GeometryValue{}, fmt.Errorf("%w: geometry header", ErrCorrupt)
	}
	d := geometryDecoder{data: data[4:]}
	root, err := d.geometry(1)
	if err == nil && d.pos != len(d.data) {
		err = fmt.Errorf("%w: geometry trailing bytes", ErrCorrupt)
	}
	if err != nil {
		return GeometryValue{}, err
	}
	return GeometryValue{binary.LittleEndian.Uint32(data), append([]byte{}, data[4:]...), root}, nil
}

type geometryDecoder struct {
	data               []byte
	pos, nodes, points int
}

func (d *geometryDecoder) take(n int) ([]byte, error) {
	if n < 0 || n > len(d.data)-d.pos {
		return nil, fmt.Errorf("%w: truncated WKB at offset %d", ErrCorrupt, d.pos)
	}
	b := d.data[d.pos : d.pos+n]
	d.pos += n
	return b, nil
}
func (d *geometryDecoder) count(order binary.ByteOrder, minBytes int) (int, error) {
	b, err := d.take(4)
	if err != nil {
		return 0, err
	}
	n := uint64(order.Uint32(b))
	if n > uint64((len(d.data)-d.pos)/minBytes) {
		return 0, fmt.Errorf("%w: WKB count exceeds available bytes", ErrCorrupt)
	}
	return int(n), nil
}
func (d *geometryDecoder) coordinates(order binary.ByteOrder, n int) ([]Coordinate, error) {
	if n > MaxGeometryPoints-d.points {
		return nil, fmt.Errorf("%w: geometry coordinate limit", ErrUnsupported)
	}
	d.points += n
	if n > (len(d.data)-d.pos)/16 {
		return nil, fmt.Errorf("%w: WKB coordinate count", ErrCorrupt)
	}
	points := make([]Coordinate, n)
	for i := range points {
		b, _ := d.take(16)
		x, y := math.Float64frombits(order.Uint64(b)), math.Float64frombits(order.Uint64(b[8:]))
		if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
			return nil, fmt.Errorf("%w: non-finite geometry coordinate", ErrCorrupt)
		}
		points[i] = Coordinate{x, y}
	}
	return points, nil
}
func (d *geometryDecoder) geometry(depth int) (Geometry, error) {
	var g Geometry
	d.nodes++
	if depth > MaxGeometryDepth || d.nodes > MaxGeometryNodes {
		return g, fmt.Errorf("%w: geometry depth/node limit", ErrUnsupported)
	}
	header, err := d.take(5)
	if err != nil {
		return g, err
	}
	var order binary.ByteOrder
	switch header[0] {
	case 0:
		order = binary.BigEndian
	case 1:
		order = binary.LittleEndian
	default:
		return g, fmt.Errorf("%w: WKB byte order", ErrCorrupt)
	}
	typ := order.Uint32(header[1:])
	if typ < 1 || typ > 7 {
		return g, fmt.Errorf("%w: WKB type %d (only 2D standard types 1..7)", ErrUnsupported, typ)
	}
	g.Type, g.ByteOrder = geometryTypes[typ], header[0]
	switch typ {
	case 1:
		g.Points, err = d.coordinates(order, 1)
	case 2:
		var n int
		n, err = d.count(order, 16)
		if err == nil {
			if n < 2 {
				err = fmt.Errorf("%w: LineString requires two points", ErrCorrupt)
			} else {
				g.Points, err = d.coordinates(order, n)
			}
		}
	case 3:
		var n int
		n, err = d.count(order, 4)
		if err != nil {
			break
		}
		if n == 0 {
			err = fmt.Errorf("%w: Polygon requires a ring", ErrCorrupt)
			break
		}
		// Each ring needs at least four coordinate pairs; bound allocation first.
		if n > (MaxGeometryPoints-d.points)/4 {
			err = fmt.Errorf("%w: geometry ring budget", ErrUnsupported)
			break
		}
		g.Rings = make([][]Coordinate, 0, n)
		for i := 0; i < n; i++ {
			var count int
			count, err = d.count(order, 16)
			if err != nil {
				break
			}
			if count < 4 {
				err = fmt.Errorf("%w: polygon ring requires four points", ErrCorrupt)
				break
			}
			var ring []Coordinate
			ring, err = d.coordinates(order, count)
			if err != nil {
				break
			}
			if ring[0] != ring[len(ring)-1] {
				err = fmt.Errorf("%w: unclosed polygon ring", ErrCorrupt)
				break
			}
			g.Rings = append(g.Rings, ring)
		}
	default:
		var n int
		n, err = d.count(order, 5)
		if err != nil {
			break
		}
		if typ != 7 && n == 0 {
			err = fmt.Errorf("%w: empty Multi geometry", ErrCorrupt)
			break
		}
		if n > MaxGeometryNodes-d.nodes {
			err = fmt.Errorf("%w: geometry node budget", ErrUnsupported)
			break
		}
		g.Geometries = make([]Geometry, 0, n)
		for i := 0; i < n; i++ {
			var child Geometry
			child, err = d.geometry(depth + 1)
			if err != nil {
				break
			}
			if typ != 7 && child.Type != geometryTypes[typ-3] {
				err = fmt.Errorf("%w: wrong Multi geometry child type", ErrCorrupt)
				break
			}
			g.Geometries = append(g.Geometries, child)
		}
	}
	return g, err
}

func geometryValue(c Column, data []byte) (GeometryValue, error) {
	v, err := DecodeGeometry(data)
	if err != nil {
		return GeometryValue{}, err
	}
	if c.Type != "GEOMETRY" && v.Geometry.Type != c.Type {
		return GeometryValue{}, fmt.Errorf("%w: geometry differs from declared %s", ErrCorrupt, c.Type)
	}
	if c.SRID != nil && v.SRID != *c.SRID {
		return GeometryValue{}, fmt.Errorf("%w: geometry SRID differs from column", ErrCorrupt)
	}
	return v, nil
}
