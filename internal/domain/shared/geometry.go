package shared

import (
	"errors"
	"math"
)

var (
	// ErrInvalidZ is returned when a Point3D's z coordinate is negative —
	// height above floor level has no meaning below zero in this model.
	ErrInvalidZ = errors.New("z coordinate must not be negative")
	// ErrInvalidDimensions is returned when a Dimensions envelope has a
	// non-positive width, depth, or height.
	ErrInvalidDimensions = errors.New("width, depth, and height must all be greater than zero")
	// ErrSegmentEndpointsNotReal is returned when a Segment is built from a
	// zero point — a centreline anchored at the frame's origin placeholder
	// carries no location information.
	ErrSegmentEndpointsNotReal = errors.New("segment endpoints must both be real points")
	// ErrSegmentStartEndEqual is returned when a Segment's two endpoints
	// coincide — a centreline with no length carries no travel-distance
	// information.
	ErrSegmentStartEndEqual = errors.New("segment start and end must differ")
)

// Point3D is a position in the site's local coordinate frame, in metres,
// with the origin and axis orientation defined per-site by convention (this
// context does not know or care about real-world geodesy). X and Y may be
// any finite value (a site's origin is arbitrary); Z is height above floor
// level and must not be negative.
type Point3D struct {
	xM, yM, zM float64
	set        bool
}

// NewPoint3D validates and constructs a Point3D.
func NewPoint3D(xM, yM, zM float64) (Point3D, error) {
	if zM < 0 {
		return Point3D{}, ErrInvalidZ
	}
	return Point3D{xM: xM, yM: yM, zM: zM, set: true}, nil
}

// XM returns the X coordinate in metres.
func (p Point3D) XM() float64 { return p.xM }

// YM returns the Y coordinate in metres.
func (p Point3D) YM() float64 { return p.yM }

// ZM returns the Z coordinate (height above floor) in metres.
func (p Point3D) ZM() float64 { return p.zM }

// IsZero reports whether this is the zero Point3D, i.e. "no geometry
// supplied". A genuinely-supplied point at the literal origin (0,0,0) also
// reports IsZero()==false because it always carries the `set` marker —
// geometry.go's constructors are the only way to produce a "set" point.
func (p Point3D) IsZero() bool { return !p.set }

// Dimensions is a rectangular footprint's width (X), depth (Y), and height
// (Z), in metres. All three must be strictly positive — a slot or
// structure with zero volume is not real geometry.
type Dimensions struct {
	widthM, depthM, heightM float64
	set                     bool
}

// NewDimensions validates and constructs a Dimensions envelope.
func NewDimensions(widthM, depthM, heightM float64) (Dimensions, error) {
	if widthM <= 0 || depthM <= 0 || heightM <= 0 {
		return Dimensions{}, ErrInvalidDimensions
	}
	return Dimensions{widthM: widthM, depthM: depthM, heightM: heightM, set: true}, nil
}

// WidthM returns the X-axis extent in metres.
func (d Dimensions) WidthM() float64 { return d.widthM }

// DepthM returns the Y-axis extent in metres.
func (d Dimensions) DepthM() float64 { return d.depthM }

// HeightM returns the Z-axis extent in metres.
func (d Dimensions) HeightM() float64 { return d.heightM }

// IsZero reports whether this is the zero Dimensions, i.e. "no geometry
// supplied".
func (d Dimensions) IsZero() bool { return !d.set }

// Rect is a rectangular footprint: its origin corner (the minimum X/Y/Z
// point) plus its Dimensions extending in the positive X/Y/Z direction from
// there. Used for FixedStructure footprints.
type Rect struct {
	origin Point3D
	size   Dimensions
}

// NewRect validates and constructs a Rect. origin must not be the zero
// Point3D and size must not be the zero Dimensions — a footprint with no
// position or no extent is not a footprint.
func NewRect(origin Point3D, size Dimensions) (Rect, error) {
	if origin.IsZero() {
		return Rect{}, errors.New("rect origin must be a real point, use NewPoint3D even for (0,0,0)")
	}
	if size.IsZero() {
		return Rect{}, ErrInvalidDimensions
	}
	return Rect{origin: origin, size: size}, nil
}

// Origin returns the footprint's minimum-corner position.
func (r Rect) Origin() Point3D { return r.origin }

// Size returns the footprint's extent.
func (r Rect) Size() Dimensions { return r.size }

// IsZero reports whether this is the zero Rect.
func (r Rect) IsZero() bool { return r.origin.IsZero() && r.size.IsZero() }

// Segment is a straight-line centreline between two points, in the site's
// local coordinate frame — used as an Aisle's optional travel centreline
// (ADR-0017).
type Segment struct {
	start, end Point3D
	set        bool
}

// NewSegment validates and constructs a Segment. Both endpoints must be
// real (non-zero) points, and they must differ — a centreline with no
// length carries no travel-distance information.
func NewSegment(start, end Point3D) (Segment, error) {
	if start.IsZero() || end.IsZero() {
		return Segment{}, ErrSegmentEndpointsNotReal
	}
	if start.xM == end.xM && start.yM == end.yM && start.zM == end.zM {
		return Segment{}, ErrSegmentStartEndEqual
	}
	return Segment{start: start, end: end, set: true}, nil
}

// Start returns the segment's starting point.
func (s Segment) Start() Point3D { return s.start }

// End returns the segment's ending point.
func (s Segment) End() Point3D { return s.end }

// IsZero reports whether this is the zero Segment, i.e. "no centreline
// supplied".
func (s Segment) IsZero() bool { return !s.set }

// LengthM returns the segment's straight-line (Euclidean) length in
// metres. It is the zero Segment's zero-value 0 when unset — callers
// should check IsZero() first.
func (s Segment) LengthM() float64 {
	dx := s.end.xM - s.start.xM
	dy := s.end.yM - s.start.yM
	dz := s.end.zM - s.start.zM
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// DistanceToPoint returns the shortest Euclidean distance, in metres, from
// p to the closest point ON this segment (not the infinite line through
// it) — used by the travel graph to place a slot's real position relative
// to its aisle's centreline (ADR-0017). It is 0 for the zero Segment;
// callers should check IsZero() first.
func (s Segment) DistanceToPoint(p Point3D) float64 {
	if s.IsZero() {
		return 0
	}
	// Standard closest-point-on-segment projection: clamp the scalar
	// projection of (p - start) onto (end - start) to [0, 1] so the
	// closest point never falls outside the segment's two endpoints.
	ax, ay, az := s.end.xM-s.start.xM, s.end.yM-s.start.yM, s.end.zM-s.start.zM
	bx, by, bz := p.xM-s.start.xM, p.yM-s.start.yM, p.zM-s.start.zM

	lengthSquared := ax*ax + ay*ay + az*az
	if lengthSquared == 0 {
		// Degenerate segment (should be unreachable: NewSegment rejects
		// identical endpoints), fall back to distance from the start.
		return math.Sqrt(bx*bx + by*by + bz*bz)
	}

	t := (ax*bx + ay*by + az*bz) / lengthSquared
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}

	closestX := s.start.xM + t*ax
	closestY := s.start.yM + t*ay
	closestZ := s.start.zM + t*az

	dx := p.xM - closestX
	dy := p.yM - closestY
	dz := p.zM - closestZ
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}
