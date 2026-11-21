package shared

import "time"

// The Site aggregate's domain events. They live apart from events.go so that
// file stays inside the repo's file-length budget; the shared base, the
// DomainEvent interface and newBase are defined there.

// SiteRegistered: a physical facility was added to the warehouse map.
// SiteRole is additive (ADR-0037): the site's role in the fulfilment
// network, UNSPECIFIED when the registration did not state one.
type SiteRegistered struct {
	base
	SiteCode string
	SiteName string
	SiteRole string
	// GLN is OPTIONAL (ADR-0045): "" when the site has none, which keeps the
	// wire payload byte-identical to before.
	GLN GLN
}

// NewSiteRegistered builds a SiteRegistered event.
func NewSiteRegistered(occurredAt time.Time, siteCode, siteName, siteRole string) SiteRegistered {
	return SiteRegistered{base: newBase("site", "SiteRegistered", occurredAt), SiteCode: siteCode, SiteName: siteName, SiteRole: siteRole}
}

// WithGLN returns a copy of the event carrying the site's optional GS1 Global
// Location Number (ADR-0045). Kept out of the constructor so the existing
// constructor signature, and every caller of it, is unchanged.
func (e SiteRegistered) WithGLN(gln GLN) SiteRegistered {
	e.GLN = gln
	return e
}

// SiteRoleChanged: an operator changed a site's role in the fulfilment
// network (ADR-0037). It carries the new and the replaced role; it never
// implies a transfer-capability change (that is SiteCapabilityChanged,
// raised only by the explicit ADR-0035 command).
type SiteRoleChanged struct {
	base
	SiteCode         string
	SiteRole         string
	PreviousSiteRole string
}

// NewSiteRoleChanged builds a SiteRoleChanged event.
func NewSiteRoleChanged(occurredAt time.Time, siteCode, siteRole, previousSiteRole string) SiteRoleChanged {
	return SiteRoleChanged{
		base:             newBase("site", "SiteRoleChanged", occurredAt),
		SiteCode:         siteCode,
		SiteRole:         siteRole,
		PreviousSiteRole: previousSiteRole,
	}
}

// SiteCapabilityChanged: a site's transfer capability state changed. The
// payload is a complete, PII-free last-write-wins snapshot; consumers use
// CapabilityRevision to retain the newest state and never infer it from
// physical layout topology.
type SiteCapabilityChanged struct {
	base
	SiteCode                   string
	TransferOriginEnabled      bool
	TransferDestinationEnabled bool
	CapabilityRevision         int64
}

// NewSiteCapabilityChanged builds a full last-write-wins capability snapshot.
func NewSiteCapabilityChanged(occurredAt time.Time, siteCode string, transferOriginEnabled, transferDestinationEnabled bool, capabilityRevision int64) SiteCapabilityChanged {
	return SiteCapabilityChanged{
		base:                       newBase("site", "SiteCapabilityChanged", occurredAt),
		SiteCode:                   siteCode,
		TransferOriginEnabled:      transferOriginEnabled,
		TransferDestinationEnabled: transferDestinationEnabled,
		CapabilityRevision:         capabilityRevision,
	}
}
