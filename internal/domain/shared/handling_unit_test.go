package shared_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func TestParseHandlingUnit(t *testing.T) {
	tests := []struct {
		raw     string
		want    shared.HandlingUnit
		wantErr error
	}{
		{"EACH", shared.HandlingUnitEach, nil},
		{"CASE", shared.HandlingUnitCase, nil},
		{"PALLET", shared.HandlingUnitPallet, nil},
		{"", "", shared.ErrUnknownHandlingUnit},
		{"case", "", shared.ErrUnknownHandlingUnit},
		{"INNER", "", shared.ErrUnknownHandlingUnit},
		{"PALLETS", "", shared.ErrUnknownHandlingUnit},
		{" CASE", "", shared.ErrUnknownHandlingUnit},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := shared.ParseHandlingUnit(tc.raw)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLocationSlotRegisteredHandlingUnitIsOptional(t *testing.T) {
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	code, _ := shared.ParseLocationCode("WH1-STOR-AMB-A07-03-02-B")
	plain := shared.NewLocationSlotRegistered(at, code, "Shelf", "Storage", "", nil, shared.Capacity{})
	if plain.HandlingUnit != "" {
		t.Fatalf("constructor must leave the handling unit absent, got %q", plain.HandlingUnit)
	}
	with := plain.WithHandlingUnit(shared.HandlingUnitCase)
	if with.HandlingUnit != shared.HandlingUnitCase {
		t.Fatalf("WithHandlingUnit = %q", with.HandlingUnit)
	}
	if plain.HandlingUnit != "" {
		t.Fatal("WithHandlingUnit must not mutate the receiver")
	}
}

func TestLocationSlotHandlingUnitDeclaredCarriesCodeUnitAndInstant(t *testing.T) {
	at := time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC)
	code, _ := shared.ParseLocationCode("WH1-STOR-AMB-A07-03-02-B")
	e := shared.NewLocationSlotHandlingUnitDeclared(at, code, shared.HandlingUnitPallet)
	if e.EventName() != "LocationSlotHandlingUnitDeclared" ||
		e.EventType() != "com.warehouse.wms.facility-layout.locationslot.LocationSlotHandlingUnitDeclared" {
		t.Fatalf("name/type = %q / %q", e.EventName(), e.EventType())
	}
	if e.LocationCode != code.String() || e.HandlingUnit != shared.HandlingUnitPallet {
		t.Fatalf("event = %+v", e)
	}
	if !e.DeclaredAt.Equal(at) || !e.OccurredAt().Equal(at) {
		t.Fatalf("declaredAt=%v occurredAt=%v want %v", e.DeclaredAt, e.OccurredAt(), at)
	}
}
