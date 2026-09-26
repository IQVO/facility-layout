# Error contract — RFC 7807 problem details on documented failure paths.
#
# Every scenario in this file is derived from the project documentation:
#   - docs/docs/api-reference/conventions.md: the "Status codes" table
#     (400 malformed input / 404 does not exist / 409 state conflict / 422
#     semantically invalid) and "The problem type catalogue".
#   - apis/openapi.yaml:
#       getLocationSlot ("A code that is not seven [A-Z0-9] segments is a
#         400, not a 404 — it could never identify a slot"),
#       RegisterLocationSlotRequest.locationType ("An already-registered
#         LocationType name"),
#       definePlacementRule ("The referenced LocationType must already
#         exist"; 404 response),
#       ZonePredicate ("unset fields are wildcards; at least one must be
#         set"),
#       decommissionLocationSlot ("The transition is one-way in v1 ...
#         calling it twice is a 409"),
#       getLocationClassification ("An unknown location code ... is a 404"),
#       getZoneGrid (404 response for an unknown zone).
#   - docs/docs/adr/0005-one-way-decommission.md: decommission is one-way.
#   - docs/docs/adr/0008-location-classification-read-endpoint.md: "An
#     unknown slot is 404 location-slot-not-found."

Feature: Error contract
  Every error response is RFC 7807 application/problem+json with a stable
  type slug, and the status split is the documented one: 400 for input
  that could never identify a resource, 404 for a name that does not
  resolve, 409 for a state conflict the world is in, 422 for a request
  that is semantically invalid on its face.

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1200 kg and 2.4 m3
    And a LocationType "Shelf" with capacity 60 kg and 0.4 m3

  # apis/openapi.yaml getLocationSlot: "A code that is not seven [A-Z0-9]
  # segments is a 400, not a 404 — it could never identify a slot."
  # conventions.md status table: 400 = "a location code that is not seven
  # [A-Z0-9] segments".
  @bdd
  Scenario: Reading a slot with a malformed location code is a 400, not a 404
    When I request the LocationSlot "WH1-STOR-AMB"
    Then the response status is 400
    And the problem detail type is "malformed-location-code"

  # apis/openapi.yaml RegisterLocationSlotRequest.locationType: "An
  # already-registered LocationType name"; conventions.md catalogue:
  # location-type-not-found, 404.
  @bdd
  Scenario: Registering a slot whose location type does not exist is a 404
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    When I register the LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "Hovercraft"
    Then the response status is 404
    And the problem detail type is "location-type-not-found"
    And no LocationSlot "WH1-STOR-AMB-A07-03-02-B" exists

  # apis/openapi.yaml definePlacementRule: "The referenced LocationType
  # must already exist." (404 response.)
  @bdd
  Scenario: Defining a placement rule that references an unknown location type is a 404
    When I define the PlacementRule "RULE-NOPE" denying "Hovercraft" where temperature class is "Frozen"
    Then the response status is 404
    And the problem detail type is "location-type-not-found"

  # apis/openapi.yaml ZonePredicate: "unset fields are wildcards; at least
  # one must be set"; conventions.md catalogue: empty-zone-predicate, 422.
  @bdd
  Scenario: A placement rule whose zone predicate constrains nothing is rejected
    When I define the PlacementRule "RULE-EMPTY" denying "Shelf" with an empty zone predicate
    Then the response status is 422
    And the problem detail type is "empty-zone-predicate"

  # apis/openapi.yaml decommissionLocationSlot: "The transition is one-way
  # in v1: there is no reactivation use case, calling it twice is a 409";
  # docs/docs/adr/0005-one-way-decommission.md.
  @bdd
  Scenario: Decommissioning a slot twice is a 409
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    And a registered LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    When I decommission the LocationSlot "WH1-STOR-AMB-A07-03-02-B"
    Then the response status is 204
    When I decommission the LocationSlot "WH1-STOR-AMB-A07-03-02-B"
    Then the response status is 409
    And the problem detail type is "already-decommissioned"

  # docs/docs/adr/0008-location-classification-read-endpoint.md: "An
  # unknown slot is 404 location-slot-not-found."
  @bdd
  Scenario: The classification of an unknown slot is a 404
    When I request the classification of LocationSlot "WH1-STOR-AMB-A07-03-02-B"
    Then the response status is 404
    And the problem detail type is "location-slot-not-found"

  # apis/openapi.yaml getZoneGrid responses: 404 when the named zone does
  # not exist.
  @bdd
  Scenario: Requesting the grid of an unknown zone is a 404
    When I request the grid of Zone "WH9-STOR-AMB"
    Then the response status is 404
    And the problem detail type is "zone-not-found"
