// Package site holds the Site aggregate: a physical facility/building and
// the root of the location hierarchy. Everything else in this bounded
// context hangs off a Site.
package site

import (
	"errors"
	"fmt"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

var (
	// ErrEmptySiteCode is returned when a site code is empty.
	ErrEmptySiteCode = errors.New("site code must not be empty")
	// ErrInvalidSiteCode is returned when a site code contains a character
	// outside [A-Z0-9].
	ErrInvalidSiteCode = errors.New("site code must contain only uppercase letters and digits")
	// ErrEmptySiteName is returned when a site's human name is empty.
	ErrEmptySiteName = errors.New("site name must not be empty")
	// ErrAlreadyDecommissioned is returned when decommissioning a site that
	// is already decommissioned.
	ErrAlreadyDecommissioned = errors.New("site is already decommissioned")
	// ErrSiteDecommissioned is returned when a command would change a
	// decommissioned site's state (ADR-0035: a retired site's transfer
	// capability is frozen).
	ErrSiteDecommissioned = errors.New("site is decommissioned")
)

// TransferCapability is the value object naming which roles a site may
// play in an inter-warehouse transfer (ADR-0035): an origin donates stock,
// a destination receives it. The zero value is "capable of nothing", which
// is also how consumers treat a site they have never heard about.
type TransferCapability struct {
	originEnabled      bool
	destinationEnabled bool
}

// NewTransferCapability builds a full capability snapshot.
func NewTransferCapability(originEnabled, destinationEnabled bool) TransferCapability {
	return TransferCapability{originEnabled: originEnabled, destinationEnabled: destinationEnabled}
}

// OriginEnabled reports whether the site may donate stock to another site.
func (c TransferCapability) OriginEnabled() bool { return c.originEnabled }

// DestinationEnabled reports whether the site may receive stock from another site.
func (c TransferCapability) DestinationEnabled() bool { return c.destinationEnabled }

// Site is a physical facility/building. Its identity is its SiteCode, which
// is also the first segment of every LocationCode inside it. Uniqueness of
// the code is enforced at the application/repository layer: a single
// aggregate cannot see its siblings.
//
// The site also owns its transfer capability (ADR-0035) and the per-site,
// monotonically increasing revision of that capability: every change that
// is published as SiteCapabilityChanged carries the revision it produced,
// so consumers keep the newest snapshot (last-write-wins on the revision).
type Site struct {
	code string
	name string
	// role is the site's role in the fulfilment network (ADR-0037).
	role               Role
	status             shared.Status
	capability         TransferCapability
	capabilityRevision int64
	// version is optimistic-concurrency metadata (ADR-0025, extended to
	// Site by ADR-0035): the persisted row version this aggregate was
	// loaded at. Never read by business logic.
	version int
}

// NewSite validates and constructs an Active Site with an UNSPECIFIED role,
// no transfer capability and no published capability revision yet.
func NewSite(code, name string) (*Site, error) {
	return NewSiteWithRole(code, name, RoleUnspecified)
}

// NewSiteWithRole validates and constructs an Active Site with the given
// role (ADR-0037; UNSPECIFIED is accepted at registration), no transfer
// capability and no published capability revision yet. The role never
// implies a capability: that stays the explicit ADR-0035 command.
func NewSiteWithRole(code, name string, role Role) (*Site, error) {
	if err := validateSiteCode(code); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, ErrEmptySiteName
	}
	if !role.known() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidSiteRole, role)
	}
	return &Site{code: code, name: name, role: role, status: shared.Active, version: 1}, nil
}

// RehydrateSite rebuilds a Site from persisted state without re-running the
// registration invariants. Only persistence adapters may call it.
func RehydrateSite(code, name string, role Role, status shared.Status, capability TransferCapability, capabilityRevision int64, version int) *Site {
	return &Site{
		code:               code,
		name:               name,
		role:               role,
		status:             status,
		capability:         capability,
		capabilityRevision: capabilityRevision,
		version:            version,
	}
}

func validateSiteCode(code string) error {
	if code == "" {
		return ErrEmptySiteCode
	}
	for _, r := range code {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("%w: %q", ErrInvalidSiteCode, code)
	}
	return nil
}

// Code returns the site code, e.g. "WH1".
func (s *Site) Code() string { return s.code }

// Name returns the human-readable site name.
func (s *Site) Name() string { return s.name }

// Role returns the site's role in the fulfilment network (ADR-0037).
func (s *Site) Role() Role { return s.role }

// Status returns the site's lifecycle status.
func (s *Site) Status() shared.Status { return s.status }

// IsActive reports whether new structure may be registered against this site.
func (s *Site) IsActive() bool { return s.status == shared.Active }

// TransferCapability returns the site's current transfer capability.
func (s *Site) TransferCapability() TransferCapability { return s.capability }

// CapabilityRevision returns the revision of the last capability change; 0
// means no capability snapshot has ever been published for this site.
func (s *Site) CapabilityRevision() int64 { return s.capabilityRevision }

// Version returns the persisted row version this aggregate was loaded at
// (ADR-0025 optimistic concurrency); 1 for a freshly constructed site.
func (s *Site) Version() int { return s.version }

// ChangeTransferCapability replaces the site's transfer capability with a
// full snapshot (ADR-0035). It reports whether the change produced a new
// capability revision, which the caller must then publish as
// SiteCapabilityChanged:
//
//   - the first change is always a change, even when it restates the
//     default, because no snapshot has been published yet;
//   - an identical snapshot afterwards is an idempotent no-op (no revision,
//     no event), so a retried PUT never inflates the revision;
//   - any other snapshot advances the revision by exactly one.
//
// A decommissioned site's capability is frozen: ErrSiteDecommissioned.
func (s *Site) ChangeTransferCapability(c TransferCapability) (bool, error) {
	if !s.IsActive() {
		return false, ErrSiteDecommissioned
	}
	if s.capabilityRevision > 0 && s.capability == c {
		return false, nil
	}
	s.capability = c
	s.capabilityRevision++
	return true, nil
}

// ChangeRole sets the site's role (ADR-0037) and reports the role it
// replaced and whether anything changed; the caller publishes
// SiteRoleChanged only when it did:
//
//   - the target must be an assignable role: UNSPECIFIED or an unknown value
//     is ErrInvalidSiteRole;
//   - the current role is an idempotent no-op (no change, no event);
//   - a decommissioned site's role is frozen: ErrSiteDecommissioned.
//
// The transfer capability is never touched: a role change does not flip
// capability (capability stays the explicit ADR-0035 command).
func (s *Site) ChangeRole(r Role) (Role, bool, error) {
	previous := s.role
	if !s.IsActive() {
		return previous, false, ErrSiteDecommissioned
	}
	if !r.assignable() {
		return previous, false, fmt.Errorf("%w: %q cannot be assigned", ErrInvalidSiteRole, r)
	}
	if r == previous {
		return previous, false, nil
	}
	s.role = r
	return previous, true, nil
}

// Decommission permanently retires the site. One-way: a decommissioned site
// is never reactivated.
func (s *Site) Decommission() error {
	if s.status == shared.Decommissioned {
		return ErrAlreadyDecommissioned
	}
	s.status = shared.Decommissioned
	return nil
}
