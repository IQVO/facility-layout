package site_test

import (
	"errors"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

func TestNewSite(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		siteName string
		wantErr  error
	}{
		{name: "a real facility", code: "WH1", siteName: "Fulfilment Centre One"},
		{name: "empty code", code: "", siteName: "Fulfilment Centre One", wantErr: site.ErrEmptySiteCode},
		{name: "lowercase code", code: "wh1", siteName: "Fulfilment Centre One", wantErr: site.ErrInvalidSiteCode},
		{name: "hyphenated code", code: "WH-1", siteName: "Fulfilment Centre One", wantErr: site.ErrInvalidSiteCode},
		{name: "empty name", code: "WH1", siteName: "", wantErr: site.ErrEmptySiteName},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := site.NewSite(tc.code, tc.siteName)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s.Code() != tc.code || s.Name() != tc.siteName {
				t.Fatalf("unexpected site %q/%q", s.Code(), s.Name())
			}
			if s.Status() != shared.Active || !s.IsActive() {
				t.Fatalf("a newly registered site must be Active, got %q", s.Status())
			}
		})
	}
}

func TestSiteDecommissionIsOneWay(t *testing.T) {
	s, err := site.NewSite("WH1", "Fulfilment Centre One")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := s.Decommission(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Status() != shared.Decommissioned || s.IsActive() {
		t.Fatalf("expected a Decommissioned site, got %q", s.Status())
	}
	if err := s.Decommission(); !errors.Is(err, site.ErrAlreadyDecommissioned) {
		t.Fatalf("expected ErrAlreadyDecommissioned, got %v", err)
	}
}

func TestRehydrateSitePreservesPersistedStatus(t *testing.T) {
	s := site.RehydrateSite("WH2", "Fulfilment Centre Two", shared.Decommissioned, site.TransferCapability{}, 0, 1)
	if s.Code() != "WH2" || s.Name() != "Fulfilment Centre Two" {
		t.Fatalf("unexpected rehydrated site %q/%q", s.Code(), s.Name())
	}
	if s.IsActive() {
		t.Fatal("a rehydrated Decommissioned site must not be Active")
	}
}
