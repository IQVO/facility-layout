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

// ErrNaturalKeyTaken is returned by a repository writing a NEW aggregate
// when a stored row already holds the aggregate's natural key — the
// caller-chosen site code, zone/aisle id, location code, location type
// name, rule id, structure id or cross-aisle identity (ADR-0047). The
// insert refused to create over it, so the creation use case maps this to
// its per-entity duplicate refusal and the loser of a concurrent
// registration of the same code gets the documented duplicate-* problem
// (FINAL, ADR 0046) instead of silently overwriting the winner's row.
var ErrNaturalKeyTaken = errors.New("the natural key is already held by a stored row")

// ErrAutomatedStorageAisleTaken is returned by a persistence adapter when a
// slot save would give one aisle a second active AutomatedStorage location
// (ADR-0048): the database refuses the race the use case's own check cannot
// see.
var ErrAutomatedStorageAisleTaken = errors.New("this aisle already holds an active AutomatedStorage location")

// SiteRepo persists and retrieves Site aggregates. FindByCode returns
// (nil, nil) when the site does not exist — "not found" is an application
// concern, not a repository error.
type SiteRepo interface {
	Save(ctx context.Context, s *site.Site) error
	FindByCode(ctx context.Context, code string) (*site.Site, error)
	List(ctx context.Context) ([]*site.Site, error)
}

// SiteCapabilityHeartbeatSource lists the sites whose capability fact is due
// for a heartbeat (ADR-0041). It is a separate, narrow port so SiteRepo and
// every other Site reader stay unchanged.
type SiteCapabilityHeartbeatSource interface {
	// ListCapabilityHeartbeatDue returns, ordered by code and at most limit
	// of them, the codes of ACTIVE sites with a declared capability
	// (capability_revision > 0) whose last heartbeat is absent or at or
	// before notAfter. It is a pre-filter: the use case re-checks each site
	// on the aggregate, and the version guard settles any race.
	ListCapabilityHeartbeatDue(ctx context.Context, notAfter time.Time, limit int) ([]string, error)
}

// ZoneRepo persists and retrieves Zone aggregates, keyed by zone id
// (SITE-AREA-ZONE).
type ZoneRepo interface {
	// Create inserts a NEW zone; it refuses with ports.ErrNaturalKeyTaken
	// when a stored row already holds the id (ADR-0047) — the second
	// writer of a concurrent registration of the same zone loses.
	Create(ctx context.Context, z *zone.Zone) error
	Save(ctx context.Context, z *zone.Zone) error
	FindByID(ctx context.Context, id string) (*zone.Zone, error)
	ListBySite(ctx context.Context, siteCode string) ([]*zone.Zone, error)
}

// AisleRepo persists and retrieves Aisle aggregates, keyed by aisle id
// (SITE-AREA-ZONE-AISLE).
type AisleRepo interface {
	// Create inserts a NEW aisle; it refuses with ports.ErrNaturalKeyTaken
	// when a stored row already holds the id (ADR-0047). Save remains the
	// update path (SetAisleGeometry and the decommissioned-parent seeding).
	Create(ctx context.Context, a *aisle.Aisle) error
	Save(ctx context.Context, a *aisle.Aisle) error
	FindByID(ctx context.Context, id string) (*aisle.Aisle, error)
	ListByZone(ctx context.Context, zoneID string) ([]*aisle.Aisle, error)
}

// CrossAisleRepo persists and retrieves CrossAisle aggregates, scoped by
// zone (ADR-0017).
type CrossAisleRepo interface {
	// Create inserts a NEW cross-aisle; it refuses with
	// ports.ErrNaturalKeyTaken when a stored row already holds the
	// (zone, from, to, bay) identity (ADR-0047).
	Create(ctx context.Context, c *aisle.CrossAisle) error
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
	// Create inserts a NEW location type; it refuses with
	// ports.ErrNaturalKeyTaken when a stored row already holds the name
	// (ADR-0047). Location types are never edited or deleted (ADR 0046 J1).
	Create(ctx context.Context, t placement.LocationType) error
	Save(ctx context.Context, t placement.LocationType) error
	FindByName(ctx context.Context, name string) (*placement.LocationType, error)
	List(ctx context.Context) ([]placement.LocationType, error)
}

// PlacementRuleRepo persists and retrieves PlacementRules. List returns the
// full rule set; the use case narrows it to the zone under consideration.
type PlacementRuleRepo interface {
	// Create inserts a NEW placement rule; it refuses with
	// ports.ErrNaturalKeyTaken when a stored row already holds the id
	// (ADR-0047). Rules are never edited or deleted (ADR 0046 J1).
	Create(ctx context.Context, r placement.PlacementRule) error
	Save(ctx context.Context, r placement.PlacementRule) error
	FindByID(ctx context.Context, id string) (*placement.PlacementRule, error)
	List(ctx context.Context) ([]placement.PlacementRule, error)
}

// FixedStructureRepo persists and retrieves FixedStructure aggregates,
// keyed by their opaque id (ADR-0017).
type FixedStructureRepo interface {
	// Create inserts a NEW fixed structure; it refuses with
	// ports.ErrNaturalKeyTaken when a stored row already holds the
	// caller-chosen id (ADR-0047).
	Create(ctx context.Context, f *structure.FixedStructure) error
	Save(ctx context.Context, f *structure.FixedStructure) error
	FindByID(ctx context.Context, id string) (*structure.FixedStructure, error)
	ListBySite(ctx context.Context, siteCode string) ([]*structure.FixedStructure, error)
}

// GLNRegistry is the one place a GS1 Global Location Number is claimed, so
// that a GLN identifies at most one location across sites, zones and slots
// (ADR-0045). It is a separate, narrow port because the uniqueness spans
// three aggregates a single repository cannot see.
type GLNRegistry interface {
	// Claim records gln as held by owner (an opaque identity such as
	// "slot:WH1-YARD-Y-A01-01-01-A"). It reports true when owner now holds
	// the number, including when it already did (idempotent), and false when a
	// DIFFERENT owner holds it. Called inside the unit of work that saves the
	// owner, so a rollback releases the claim and two concurrent claims of the
	// same number cannot both succeed.
	Claim(ctx context.Context, gln shared.GLN, owner string) (bool, error)
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
