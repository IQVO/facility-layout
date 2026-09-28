package shared_test

import (
	"errors"
	"math"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func TestNewPoint3D(t *testing.T) {
	t.Run("accepts any finite x/y with non-negative z", func(t *testing.T) {
		p, err := shared.NewPoint3D(-5.5, 10.2, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.XM() != -5.5 || p.YM() != 10.2 || p.ZM() != 0 {
			t.Fatalf("unexpected point %+v", p)
		}
		if p.IsZero() {
			t.Fatal("a constructed point must not report IsZero")
		}
	})

	t.Run("rejects a negative z", func(t *testing.T) {
		_, err := shared.NewPoint3D(0, 0, -1)
		if !errors.Is(err, shared.ErrInvalidZ) {
			t.Fatalf("expected ErrInvalidZ, got %v", err)
		}
	})

	t.Run("the zero value reports IsZero", func(t *testing.T) {
		var p shared.Point3D
		if !p.IsZero() {
			t.Fatal("expected the zero Point3D to report IsZero")
		}
	})
}

func TestNewDimensions(t *testing.T) {
	t.Run("accepts strictly positive width/depth/height", func(t *testing.T) {
		d, err := shared.NewDimensions(1.2, 0.9, 2.4)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.WidthM() != 1.2 || d.DepthM() != 0.9 || d.HeightM() != 2.4 {
			t.Fatalf("unexpected dimensions %+v", d)
		}
		if d.IsZero() {
			t.Fatal("constructed dimensions must not report IsZero")
		}
	})

	t.Run("rejects zero or negative extents", func(t *testing.T) {
		tests := [][3]float64{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {-1, 1, 1}}
		for _, tc := range tests {
			if _, err := shared.NewDimensions(tc[0], tc[1], tc[2]); !errors.Is(err, shared.ErrInvalidDimensions) {
				t.Fatalf("dims %v: expected ErrInvalidDimensions, got %v", tc, err)
			}
		}
	})

	t.Run("the zero value reports IsZero", func(t *testing.T) {
		var d shared.Dimensions
		if !d.IsZero() {
			t.Fatal("expected the zero Dimensions to report IsZero")
		}
	})
}

func TestNewRect(t *testing.T) {
	origin := mustPoint(t, 1, 2, 0)
	size := mustDimensions(t, 3, 4, 2)

	t.Run("constructs from a real origin and real size", func(t *testing.T) {
		r, err := shared.NewRect(origin, size)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Origin() != origin || r.Size() != size {
			t.Fatalf("unexpected rect %+v", r)
		}
		if r.IsZero() {
			t.Fatal("a constructed rect must not report IsZero")
		}
	})

	t.Run("rejects a zero origin", func(t *testing.T) {
		if _, err := shared.NewRect(shared.Point3D{}, size); err == nil {
			t.Fatal("expected an error for a zero origin")
		}
	})

	t.Run("rejects zero size", func(t *testing.T) {
		if _, err := shared.NewRect(origin, shared.Dimensions{}); !errors.Is(err, shared.ErrInvalidDimensions) {
			t.Fatalf("expected ErrInvalidDimensions, got %v", err)
		}
	})
}

func TestNewSegment(t *testing.T) {
	start := mustPoint(t, 0, 0, 0)
	end := mustPoint(t, 3, 4, 0)

	t.Run("constructs from two distinct real points and computes length", func(t *testing.T) {
		testSegmentFromDistinctPoints(t, start, end)
	})

	t.Run("length is computed from the difference of non-zero endpoints", func(t *testing.T) {
		testSegmentLengthFromDeltas(t)
	})

	t.Run("rejects a zero endpoint", func(t *testing.T) {
		testSegmentRejectsZeroEndpoint(t, start, end)
	})

	t.Run("rejects identical start and end", func(t *testing.T) {
		if _, err := shared.NewSegment(start, start); err == nil {
			t.Fatal("expected an error for a zero-length segment")
		}
	})

	t.Run("the zero value reports IsZero", func(t *testing.T) {
		var s shared.Segment
		if !s.IsZero() {
			t.Fatal("expected the zero Segment to report IsZero")
		}
	})
}

func testSegmentFromDistinctPoints(t *testing.T, start, end shared.Point3D) {
	t.Helper()
	s, err := shared.NewSegment(start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Start() != start || s.End() != end {
		t.Fatalf("unexpected segment %+v", s)
	}
	if s.IsZero() {
		t.Fatal("a constructed segment must not report IsZero")
	}
	if math.Abs(s.LengthM()-5.0) > 1e-9 {
		t.Fatalf("expected a 3-4-5 triangle length of 5, got %v", s.LengthM())
	}
}

func testSegmentLengthFromDeltas(t *testing.T) {
	t.Helper()
	// A start point at the origin makes end-start and end+start
	// identical, which would let an arithmetic-operator mutant
	// survive undetected. Both endpoints here are non-zero on
	// every axis, AND all three deltas (dx, dy, dz) are distinct
	// non-zero values, so a mutant that swaps + for - between
	// terms, or * for / within a term, changes the result.
	off := mustPoint(t, 5, 5, 5)
	far, err := shared.NewPoint3D(6, 7, 7)
	if err != nil {
		t.Fatalf("far: %v", err)
	}
	s, err := shared.NewSegment(off, far)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// dx=1, dy=2, dz=2 -> sqrt(1+4+4) = 3.
	if math.Abs(s.LengthM()-3.0) > 1e-9 {
		t.Fatalf("expected length 3 from deltas (1,2,2), got %v", s.LengthM())
	}
}

func testSegmentRejectsZeroEndpoint(t *testing.T, start, end shared.Point3D) {
	t.Helper()
	if _, err := shared.NewSegment(shared.Point3D{}, end); err == nil {
		t.Fatal("expected an error for a zero start point")
	}
	if _, err := shared.NewSegment(start, shared.Point3D{}); err == nil {
		t.Fatal("expected an error for a zero end point")
	}
}

func TestSegmentDistanceToPoint(t *testing.T) {
	// A genuinely 3D, non-axis-aligned segment (direction (3,4,12), length
	// 13 — a Pythagorean quadruple) so every one of ax/ay/az and
	// bx/by/bz is non-zero and distinct for every test point below. An
	// axis-aligned segment (e.g. start=(0,0,0), end=(10,0,0)) would make
	// the y/z terms of the dot product and lengthSquared always zero,
	// letting an arithmetic-operator or sign-inversion mutant on those
	// terms survive undetected.
	start := mustPoint(t, 1, 2, 15)
	end := mustPoint(t, 4, 6, 27)
	s, err := shared.NewSegment(start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("a point exactly on the segment has zero distance", func(t *testing.T) {
		// closest point at t=0.5: start + 0.5*(3,4,12) = (2.5, 4, 21).
		p := mustPoint(t, 2.5, 4, 21)
		if got := s.DistanceToPoint(p); math.Abs(got) > 1e-9 {
			t.Fatalf("expected distance 0, got %v", got)
		}
	})

	t.Run("an interior point off the segment projects to a perpendicular distance", func(t *testing.T) {
		// (4,-3,0) is perpendicular to the direction (3,4,12): offsetting
		// the t=0.5 point by it keeps t=0.5 (no clamping) while adding a
		// hand-verified perpendicular distance of 5.
		p := mustPoint(t, 6.5, 1, 21)
		if got := s.DistanceToPoint(p); math.Abs(got-5.0) > 1e-9 {
			t.Fatalf("expected distance 5, got %v", got)
		}
	})

	t.Run("a point beyond the end clamps to the end", func(t *testing.T) {
		// end + direction = (7,10,39); the projection's t would be 2.0,
		// clamped to 1, so the closest point is end itself and the
		// distance is exactly one segment length (13).
		p := mustPoint(t, 7, 10, 39)
		if got := s.DistanceToPoint(p); math.Abs(got-13.0) > 1e-9 {
			t.Fatalf("expected distance 13, got %v", got)
		}
	})

	t.Run("a point before the start clamps to the start", func(t *testing.T) {
		// start - direction = (-2,-2,3); the projection's t would be
		// -1.0, clamped to 0, so the closest point is start itself and
		// the distance is exactly one segment length (13).
		p := mustPoint(t, -2, -2, 3)
		if got := s.DistanceToPoint(p); math.Abs(got-13.0) > 1e-9 {
			t.Fatalf("expected distance 13, got %v", got)
		}
	})

	t.Run("a point whose projection lands exactly at t=0 is not clamped", func(t *testing.T) {
		// A point offset from start by a vector perpendicular to the
		// segment direction (4,-3,0) projects to t=0 exactly (the
		// boundary between "before start" and "on segment"), with
		// distance 5 either way — pins the t < 0 boundary precisely.
		p := mustPoint(t, 5, -1, 15)
		if got := s.DistanceToPoint(p); math.Abs(got-5.0) > 1e-9 {
			t.Fatalf("expected distance 5, got %v", got)
		}
	})

	t.Run("a point whose projection lands exactly at t=1 is not clamped", func(t *testing.T) {
		// Same perpendicular offset applied at the end point: projects
		// to t=1 exactly — pins the t > 1 boundary precisely.
		p := mustPoint(t, 8, 3, 27)
		if got := s.DistanceToPoint(p); math.Abs(got-5.0) > 1e-9 {
			t.Fatalf("expected distance 5, got %v", got)
		}
	})

	t.Run("the zero segment returns zero", func(t *testing.T) {
		var zero shared.Segment
		if got := zero.DistanceToPoint(mustPoint(t, 100, 100, 100)); got != 0 {
			t.Fatalf("expected 0 for the zero segment, got %v", got)
		}
	})
}

func mustPoint(t *testing.T, x, y, z float64) shared.Point3D {
	t.Helper()
	p, err := shared.NewPoint3D(x, y, z)
	if err != nil {
		t.Fatalf("mustPoint: %v", err)
	}
	return p
}

func mustDimensions(t *testing.T, w, d, h float64) shared.Dimensions {
	t.Helper()
	dim, err := shared.NewDimensions(w, d, h)
	if err != nil {
		t.Fatalf("mustDimensions: %v", err)
	}
	return dim
}
