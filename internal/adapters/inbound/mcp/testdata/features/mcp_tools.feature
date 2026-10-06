# MCP behavioral evals for the facility-layout tool surface.
#
# Each scenario drives tools/call over the REAL Streamable HTTP handler
# with a connected SDK client — exactly the call a model host makes — and
# pins the structured result. Arguments are deliberately model-realistic:
# extra keys, wrong types, unknown ids, malformed codes.
#
# facility-layout is a read-only Open Host Service: every tool is a read,
# so there is no write tool and no domain-event/side-effect scenario to
# pin — the absence of a write tool is itself the contract (charter §4).
#
# Derived from the tool contracts documented in:
#   - internal/adapters/inbound/mcp/tools.go (tool descriptions and
#     semantics: walk order, seven-segment location codes, cross-zone
#     estimate-or-refusal) and report_tool.go (conditionally-registered report tool,
#     outside this suite's default surface)
#   - docs/docs/mcp/governance-charter.md (§2 tool curation: intent-level
#     tools; §4 read-only context, no write tool)
#   - apis/openapi.yaml read models the tools serve: GET /sites,
#     GET /sites/{siteCode}/layout, GET /zones/{zoneId}/grid,
#     GET /sites/{siteCode}/locations, GET /zones/{zoneId}/travel-graph,
#     GET /distance.

Feature: MCP tool behavioral evals
  The facility-layout MCP tools expose this bounded context to AI agents:
  the warehouse map (sites, nested layouts, zone grids, functional
  locations) and the zone travel graph. An agent relying on them must get
  the same semantics the REST API guarantees, through the schema-decoded
  argument path a model host actually uses.

  Background:
    Given the MCP server is running with the canonical eval state (WH1: zones STOR-AMB, RCV-AMB, DOCK-OB; docks registered)

  Scenario: Every registered site is listed
    When I call the tool "list_sites" with no arguments
    Then the tool call succeeds
    And the structured result field "sites" is a list with 1 entries

  Scenario: A no-argument tool still rejects stray arguments
    list_sites declares no parameters: a host forwarding an argument the
    tool never asked for gets a schema validation error, not a silent
    ignore.
    When I call the tool "list_sites" with arguments
      | siteCode | WH1 |
    Then the tool call reports a problem mentioning "siteCode"

  Scenario: A site layout nests every zone under the site header
    When I call the tool "get_site_layout" with argument "siteCode" = "WH1"
    Then the tool call succeeds
    And the structured result field "site.code" is "WH1"
    And the structured result field "site.name" is "Fulfilment Centre One"
    And the structured result field "zones" is a list with 3 entries

  Scenario: An unknown site is a clean tool error
    When I call the tool "get_site_layout" with argument "siteCode" = "GHOST"
    Then the tool call reports a problem mentioning "site not found"

  Scenario: Model chatter in the arguments is rejected, not ignored
    The typed tool schemas are strict (additionalProperties: false, the
    SDK default): a host forwarding stray model-generated keys gets a
    clean schema validation error rather than a silent ignore.
    When I call the tool "get_site_layout" with arguments
      | siteCode      | WH1                    |
      | model_chatter | probably warehouse one |
      | step          | 2                      |
    Then the tool call reports a problem mentioning "model_chatter"

  Scenario: A wrong-typed argument is rejected without coercion
    When I call the tool "get_site_layout" with argument "siteCode" = 42
    Then the tool call does not succeed silently

  Scenario: A zone grid is one column per (aisle, bay) in walk order
    When I call the tool "get_zone_grid" with argument "zoneId" = "WH1-STOR-AMB"
    Then the tool call succeeds
    And the structured result field "zoneId" is "WH1-STOR-AMB"
    And the structured result field "columns" is a list with 4 entries

  Scenario: An unknown zone grid is a clean tool error
    When I call the tool "get_zone_grid" with argument "zoneId" = "WH1-NOPE-XXX"
    Then the tool call reports a problem mentioning "zone not found"

  Scenario: Functional locations list this site's dock doors
    When I call the tool "list_functional_locations" with arguments
      | siteCode | WH1  |
      | role     | Dock |
    Then the tool call succeeds
    And the structured result field "locations" is a list with 2 entries

  Scenario: A role with no matching locations is an empty list, not an error
    When I call the tool "list_functional_locations" with arguments
      | siteCode | WH1  |
      | role     | Yard |
    Then the tool call succeeds
    And the structured result field "locations" is a list with 0 entries

  Scenario: An unknown functional role is a clean tool error
    When I call the tool "list_functional_locations" with arguments
      | siteCode | WH1       |
      | role     | Warehouse |
    Then the tool call reports a problem mentioning "location role"

  Scenario: An unknown site in a functional-location search is a clean tool error
    When I call the tool "list_functional_locations" with arguments
      | siteCode | NOPE |
      | role     | Dock |
    Then the tool call reports a problem mentioning "site not found"

  Scenario: A zone travel graph lists a waypoint per aisle/bay
    When I call the tool "get_zone_travel_graph" with argument "zoneId" = "WH1-STOR-AMB"
    Then the tool call succeeds
    And the structured result field "nodes" is a list with 4 entries
    And the structured result field "edges" is a list with 4 entries

  Scenario: An unknown zone on the travel graph is a clean tool error
    When I call the tool "get_zone_travel_graph" with argument "zoneId" = "WH1-NOPE-XXX"
    Then the tool call reports a problem mentioning "zone not found"

  Scenario: Travel distance within one aisle is the bay-gap at the zone's pitch
    When I call the tool "estimate_travel_distance" with arguments
      | from | WH1-STOR-AMB-A07-01-01-A |
      | to   | WH1-STOR-AMB-A07-02-01-A |
    Then the tool call succeeds
    And the structured result field "metresM" is 1.2
    And the structured result field "estimated" is true
    And the structured result field "route" is a list with 2 entries

  Scenario: Travel between zones without position geometry is refused, not guessed
    The canonical eval state records no slot position geometry. With the
    travel graph per-zone, the tool refuses rather than inventing a
    cross-zone route; when BOTH slots carry geometry it instead returns a
    flagged straight-line estimate (ADR-0017, covered by the
    EstimateTravelDistance use case tests).
    When I call the tool "estimate_travel_distance" with arguments
      | from | WH1-STOR-AMB-A07-01-01-A |
      | to   | WH1-RCV-AMB-D01-01-01-A  |
    Then the tool call reports a problem mentioning "different zones"

  Scenario: A malformed location code is rejected before any graph work
    When I call the tool "estimate_travel_distance" with arguments
      | from | not-a-code                |
      | to   | WH1-STOR-AMB-A07-02-01-A |
    Then the tool call reports a problem mentioning "7 hyphen-joined segments"

  Scenario: A wrong-typed travel argument is rejected without coercion
    When I call the tool "estimate_travel_distance" with argument "from" = 7
    Then the tool call does not succeed silently

  # Fleet tool-error convention (ADR-0033): a tool error reads
  # "<slug>: detail", the slug being the REST problem slug for the same
  # condition, so a caller can tell a rejection from an internal failure.

  Scenario: An unknown site error carries the REST slug
    When I call the tool "get_site_layout" with argument "siteCode" = "GHOST"
    Then the tool call fails with the error slug "site-not-found"

  Scenario: An unknown zone error carries the REST slug
    When I call the tool "get_zone_grid" with argument "zoneId" = "WH1-NOPE-XXX"
    Then the tool call fails with the error slug "zone-not-found"

  Scenario: An unknown functional role error carries the REST slug
    When I call the tool "list_functional_locations" with arguments
      | siteCode | WH1       |
      | role     | Warehouse |
    Then the tool call fails with the error slug "unknown-location-role"

  Scenario: A malformed location code error carries the REST slug
    When I call the tool "estimate_travel_distance" with arguments
      | from | not-a-code                |
      | to   | WH1-STOR-AMB-A07-02-01-A |
    Then the tool call fails with the error slug "malformed-location-code"

  Scenario: A blank location code is a missing-location-code error
    When I call the tool "estimate_travel_distance" with arguments
      | from |                          |
      | to   | WH1-STOR-AMB-A07-02-01-A |
    Then the tool call fails with the error slug "missing-location-code"

  Scenario: A cross-zone refusal carries the REST slug
    When I call the tool "estimate_travel_distance" with arguments
      | from | WH1-STOR-AMB-A07-01-01-A |
      | to   | WH1-RCV-AMB-D01-01-01-A  |
    Then the tool call fails with the error slug "no-route-between-zones"
