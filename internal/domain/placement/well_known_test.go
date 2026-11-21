package placement_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func capacity(t *testing.T, w, v float64) shared.Capacity {
	t.Helper()
	c, err := shared.NewCapacity(w, v)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCanonicalRoles(t *testing.T) {
	cases := map[string]placement.LocationRole{
		placement.AutomatedStorage: placement.Storage,
		placement.AsrsPort:         placement.Drop,
		placement.HandoffPoint:     placement.Drop,
	}
	for name, want := range cases {
		if got, ok := placement.CanonicalRole(name); !ok || got != want {
			t.Fatalf("CanonicalRole(%q) = %q %v, want %q", name, got, ok, want)
		}
	}
	for _, free := range []string{placement.PalletRack, placement.Staging, "WorkStation", "automatedstorage", ""} {
		if _, ok := placement.CanonicalRole(free); ok {
			t.Fatalf("%q must stay a free name", free)
		}
	}
	if placement.AutomatedStorage != "AutomatedStorage" || placement.AsrsPort != "AsrsPort" || placement.HandoffPoint != "HandoffPoint" {
		t.Fatal("the wire names are part of the contract (ADR 0048)")
	}
}

func TestNewLocationTypeAcceptsTheCanonicalRole(t *testing.T) {
	if _, err := placement.NewLocationType(placement.AutomatedStorage, placement.Storage, capacity(t, 50000, 400)); err != nil {
		t.Fatal(err)
	}
	if _, err := placement.NewLocationType(placement.AsrsPort, placement.Drop, capacity(t, 30, 0.1)); err != nil {
		t.Fatal(err)
	}
	if _, err := placement.NewLocationType(placement.HandoffPoint, placement.Drop, capacity(t, 30, 0.1)); err != nil {
		t.Fatal(err)
	}
}

func TestNewLocationTypeRefusesAnotherRoleForAWellKnownName(t *testing.T) {
	cases := []struct {
		name string
		role placement.LocationRole
	}{
		{placement.AutomatedStorage, placement.Dock},
		{placement.AutomatedStorage, placement.Drop},
		{placement.AsrsPort, placement.Storage},
		{placement.HandoffPoint, placement.WorkCenter},
	}
	for _, c := range cases {
		if _, err := placement.NewLocationType(c.name, c.role, capacity(t, 30, 0.1)); !errors.Is(err, placement.ErrLocationTypeRoleMismatch) {
			t.Fatalf("%s as %s: err = %v", c.name, c.role, err)
		}
	}
}

func TestNewLocationTypeLeavesOtherNamesFree(t *testing.T) {
	if _, err := placement.NewLocationType(placement.PalletRack, placement.Dock, shared.Capacity{}); err != nil {
		t.Fatalf("existing behaviour changed: %v", err)
	}
}
