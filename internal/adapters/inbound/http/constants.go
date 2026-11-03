package http

// String literals shared across several files of this adapter: the JSON
// status field of the health/readyz payloads (also the access-log status
// key) and the slug reported for an unparseable request body.
const (
	statusField              = "status"
	slugMalformedRequestBody = "malformed-request-body"

	// Slugs reused by several catalog rows (the same slugs the MCP and A2A
	// adapters report for these conditions, ADR-0033/0043).
	slugAlreadyDecommissioned = "already-decommissioned"
	slugMalformedLocationCode = "malformed-location-code"
	slugInvalidSiteCode       = "invalid-site-code"
	slugInvalidZoneCode       = "invalid-zone-code"
	slugInvalidAisleCode      = "invalid-aisle-code"
	slugInvalidLocationType   = "invalid-location-type"
)
