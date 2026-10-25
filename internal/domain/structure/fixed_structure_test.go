package structure_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/structure"
)

func mustFootprint(t *testing.T) shared.Rect {
	t.Helper()
	origin, err := shared.NewPoint3D(10, 20, 0)
	if err != nil {
		t.Fatalf("origin: %v", err)
	}
	size, err := shared.NewDimensions(1, 1, 3)
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	rect, err := shared.NewRect(origin, size)
	if err != nil {
		t.Fatalf("rect: %v", err)
	}
	return rect
}

func TestParseKind(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"Wall", false}, {"Column", false}, {"Office", false},
		{"Conveyor", false}, {"Other", false}, {"Elevator", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := structure.ParseKind(tc.name)
			if tc.wantErr {
				if !errors.Is(err, structure.ErrUnknownKind) {
					t.Fatalf("expected ErrUnknownKind, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(k) != tc.name {
				t.Fatalf("expected kind %q, got %q", tc.name, k)
			}
		})
	}
}

func TestNewFixedStructure(t *testing.T) {
	footprint := mustFootprint(t)

	t.Run("constructs a valid structure", func(t *testing.T) {
		f, err := structure.NewFixedStructure("STR-1", "WH1", structure.Wall, footprint, "North wall")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if f.ID() != "STR-1" || f.SiteCode() != "WH1" || f.Kind() != structure.Wall || f.Label() != "North wall" {
			t.Fatalf("unexpected structure %+v", f)
		}
		if f.Footprint() != footprint {
			t.Fatalf("unexpected footprint %+v", f.Footprint())
		}
	})

	t.Run("rejects an empty id", func(t *testing.T) {
		_, err := structure.NewFixedStructure("", "WH1", structure.Wall, footprint, "North wall")
		if !errors.Is(err, structure.ErrEmptyID) {
			t.Fatalf("expected ErrEmptyID, got %v", err)
		}
	})

	t.Run("rejects an empty site code", func(t *testing.T) {
		_, err := structure.NewFixedStructure("STR-1", "", structure.Wall, footprint, "North wall")
		if !errors.Is(err, structure.ErrEmptySiteCode) {
			t.Fatalf("expected ErrEmptySiteCode, got %v", err)
		}
	})

	t.Run("rejects an unknown kind", func(t *testing.T) {
		_, err := structure.NewFixedStructure("STR-1", "WH1", "Elevator", footprint, "North wall")
		if !errors.Is(err, structure.ErrUnknownKind) {
			t.Fatalf("expected ErrUnknownKind, got %v", err)
		}
	})

	t.Run("rejects a zero footprint", func(t *testing.T) {
		_, err := structure.NewFixedStructure("STR-1", "WH1", structure.Wall, shared.Rect{}, "North wall")
		if !errors.Is(err, structure.ErrEmptyFootprint) {
			t.Fatalf("expected ErrEmptyFootprint, got %v", err)
		}
	})

	t.Run("rejects an empty label", func(t *testing.T) {
		_, err := structure.NewFixedStructure("STR-1", "WH1", structure.Wall, footprint, "")
		if !errors.Is(err, structure.ErrEmptyLabel) {
			t.Fatalf("expected ErrEmptyLabel, got %v", err)
		}
	})
}

func TestRehydrateFixedStructure(t *testing.T) {
	footprint := mustFootprint(t)
	f := structure.RehydrateFixedStructure("STR-1", "WH1", structure.Column, footprint, "Column B3")
	if f.ID() != "STR-1" || f.Kind() != structure.Column || f.Label() != "Column B3" {
		t.Fatalf("unexpected rehydrated structure %+v", f)
	}
}

func TestParseKind_EmptyIsItsOwnError(t *testing.T) {
	if _, err := structure.ParseKind(""); !errors.Is(err, structure.ErrEmptyKind) {
		t.Fatalf("expected ErrEmptyKind for an absent kind, got %v", err)
	}
	if _, err := structure.ParseKind("Elevator"); !errors.Is(err, structure.ErrUnknownKind) {
		t.Fatalf("expected ErrUnknownKind for an unrecognised kind, got %v", err)
	}
}
