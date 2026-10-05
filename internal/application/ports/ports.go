// Package ports declares the outbound interfaces the application layer
// depends on. Adapters implement these; the application never imports an
// adapter package. This package contains interfaces plus the shared
// repository error sentinels the use cases surface to callers.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/slot"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// ErrConcurrentModification is returned by a versioned repository's Save
// when the row was modified by another writer between this caller's load
// and its Save (ADR-0025, optimistic concurrency): the aggregate's loaded
// version no longer matches the row's current version. The caller must
// re-fetch and re-apply its change; it is never safe to retry the same
// in-memory aggregate blindly.
var ErrConcurrentModification = errors.New("aggregate was concurrently modified; reload and retry")

// SiteRepo persists and retrieves Site aggregates. FindByCode returns
// (nil, nil) when the site does not exist — "not found" is an application
// concern, not a repository error.
type SiteRepo interface {
	Save(ctx context.Context, s *site.Site) error
	FindByCode(ctx context.Context, code string) (*site.Site, error)
	List(ctx context.Context) ([]*site.Site, error)
}

// ZoneRepo persists and retrieves Zone aggregates, keyed by zone id
// (SITE-AREA-ZONE).
type ZoneRepo interface {
	Save(ctx context.Context, z *zone.Zone) error
	FindByID(ctx context.Context, id string) (*zone.Zone, error)
	ListBySite(ctx context.Context, siteCode string) ([]*zone.Zone, error)
}

// AisleRepo persists and retrieves Aisle aggregates, keyed by aisle id
// (SITE-AREA-ZONE-AISLE).
type AisleRepo interface {
	Save(ctx context.Context, a *aisle.Aisle) error
	FindByID(ctx context.Context, id string) (*aisle.Aisle, error)
	ListByZone(ctx context.Context, zoneID string) ([]*aisle.Aisle, error)
}

// CrossAisleRepo persists and retrieves CrossAisle aggregates, scoped by
// zone (ADR-0017).
type CrossAisleRepo interface {
	Save(ctx context.Context, c *aisle.CrossAisle) error
	// FindByAisles returns the cross-aisle connecting fromAisle and
	// toAisle at atBay within zoneID, or (nil, nil) when none exists —
	// used to reject a duplicate registration regardless of which side
	// is named "from" (the connection is symmetric).
	FindByAisles(ctx context.Context, zoneID, fromAisle, toAisle, atBay string) (*aisle.CrossAisle, error)
	ListByZone(ctx context.Context, zoneID string) ([]*aisle.CrossAisle, error)
}

// SlotRepo persists and retrieves LocationSlot aggregates, keyed by their
// LocationCode (which is their identity).
type SlotRepo interface {
	Save(ctx context.Context, s *slot.LocationSlot) error
	FindByCode(ctx context.Context, code shared.LocationCode) (*slot.LocationSlot, error)
	ListByAisle(ctx context.Context, aisleID string) ([]*slot.LocationSlot, error)
	ListByZone(ctx context.Context, zoneID string) ([]*slot.LocationSlot, error)
}

// LocationTypeRepo persists and retrieves LocationType definitions.
type LocationTypeRepo interface {
	Save(ctx context.Context, t placement.LocationType) error
	FindByName(ctx context.Context, name string) (*placement.LocationType, error)
	List(ctx context.Context) ([]placement.LocationType, error)
}

// PlacementRuleRepo persists and retrieves PlacementRules. List returns the
// full rule set; the use case narrows it to the zone under consideration.
type PlacementRuleRepo interface {
	Save(ctx context.Context, r placement.PlacementRule) error
	FindByID(ctx context.Context, id string) (*placement.PlacementRule, error)
	List(ctx context.Context) ([]placement.PlacementRule, error)
}

// FixedStructureRepo persists and retrieves FixedStructure aggregates,
// keyed by their opaque id (ADR-0017).
type FixedStructureRepo interface {
	Save(ctx context.Context, f *structure.FixedStructure) error
	FindByID(ctx context.Context, id string) (*structure.FixedStructure, error)
	ListBySite(ctx context.Context, siteCode string) ([]*structure.FixedStructure, error)
}

// EventPublisher publishes domain events. Adapters may log them, buffer
// them, or forward them to a broker.
type EventPublisher interface {
	Publish(ctx context.Context, event shared.DomainEvent) error
}

// UnitOfWork brackets a use case's aggregate save(s) and the domain
// event(s) it raises so both commit or neither does (ADR-0018,
// transactional outbox).
//
// Execute runs fn inside one atomic scope. Every Repo.Save and
// EventPublisher.Publish made with the ctx handed to fn is bound to that
// same scope: if fn returns an error the scope is rolled back and nothing
// — neither the aggregate row nor the outbox row — is visible afterwards.
//
// Adapters with no transactional backing (the in-memory repos, the log
// publisher) never need a UnitOfWork: a nil value means "no transactional
// backing" and use cases run Save/Publish back to back, which is exactly
// the in-memory dev configuration.
type UnitOfWork interface {
	Execute(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock abstracts current time so use cases and tests are deterministic.
type Clock interface {
	Now() time.Time
}

// LocationMetrics records the business-level telemetry this bounded context
// owns: how many coded location slots were registered, and how many were
// turned away and why. Implemented by the telemetry adapter; a use case
// with no recorder wired simply records nothing.
type LocationMetrics interface {
	LocationSlotRegistered(ctx context.Context, outcome string)
}
