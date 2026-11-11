# GS1 location identity (gln) and yard spot kind (spotKind).
#
# Derived from:
#   - apis/openapi.yaml POST /sites, /sites/{siteCode}/zones, /locations: the
#     optional `gln` (13 digits, GS1 mod-10 check digit; 422 invalid-gln;
#     409 gln-already-assigned) and, on /locations, the optional `spotKind`
#     (PARKING|HOLDING|REEFER_PLUG|HAZMAT|STAGING_LANE; Yard locations only,
#     else 422 spot-kind-not-allowed)
#   - apis/asyncapi.yaml SiteRegistered, ZoneRegistered, LocationSlotRegistered:
#     the same fields, additive and omitted when none was recorded
#   - docs/docs/adr/0045-gs1-location-identity.md

Feature: GS1 location identity and yard spot kind
  As a trading partner or the yard system
  I want a location to carry its GS1 Global Location Number and a yard spot its kind
  So that both sides name the same place without a translation table

  Background:
    Given an empty warehouse map
    And a registered Site "WH1"
    And a registered Zone "YARD"/"NORTH" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "Y01" in Zone "WH1-YARD-NORTH" with sequence hint 1
    And a POST to "/location-types" with the JSON body {"name":"YardSpot","role":"Yard"} has been accepted
    And a POST to "/location-types" with the JSON body {"name":"DockDoor","role":"Dock"} has been accepted

  @bdd
  Scenario: A site registered with a valid GLN keeps it
    When a POST request is sent to "/sites" with the JSON body {"siteCode":"WH2","name":"Second","gln":"4006381333931"}
    Then the response status is 201
    And the response field "gln" is "4006381333931"
    And the last "SiteRegistered" domain event has the field "gln" equal to "4006381333931"

  @bdd
  Scenario: A zone registered with a valid GLN keeps it and the event carries it
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","gln":"5412345000013"}
    Then the response status is 201
    And the response field "gln" is "5412345000013"
    And the last "ZoneRegistered" domain event has the field "gln" equal to "5412345000013"

  @bdd
  Scenario: A yard spot with a valid GLN and a spot kind keeps both
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-A","locationType":"YardSpot","gln":"4012345000009","spotKind":"REEFER_PLUG"}
    Then the response status is 201
    And the response field "gln" is "4012345000009"
    And the response field "spotKind" is "REEFER_PLUG"
    And the last "LocationSlotRegistered" domain event has the field "gln" equal to "4012345000009"
    And the last "LocationSlotRegistered" domain event has the field "spotKind" equal to "REEFER_PLUG"

  @bdd
  Scenario: The GLN and spot kind are returned when the slot is read back
    Given a POST to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-A","locationType":"YardSpot","gln":"4012345000009","spotKind":"HOLDING"} has been accepted
    When a GET request is sent to "/locations/WH1-YARD-NORTH-Y01-01-01-A"
    Then the response status is 200
    And the response field "gln" is "4012345000009"
    And the response field "spotKind" is "HOLDING"

  @bdd
  Scenario: A dock door may carry a GLN
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-B","locationType":"DockDoor","dockFlow":"Inbound","gln":"9780201379624"}
    Then the response status is 201
    And the response field "gln" is "9780201379624"
    And the response has no field "spotKind"

  @bdd
  Scenario: A wrong GS1 check digit is refused as an invalid GLN
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-A","locationType":"YardSpot","gln":"4006381333932"}
    Then the response status is 422
    And the problem detail type is "invalid-gln"
    And exactly 0 "LocationSlotRegistered" domain events are published

  @bdd
  Scenario: A GLN that is not 13 digits is refused
    When a POST request is sent to "/sites" with the JSON body {"siteCode":"WH2","name":"Second","gln":"40063813339"}
    Then the response status is 422
    And the problem detail type is "invalid-gln"

  @bdd
  Scenario: The same GLN cannot identify two locations
    Given a POST to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-A","locationType":"YardSpot","gln":"4012345000009"} has been accepted
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-02-A","locationType":"YardSpot","gln":"4012345000009"}
    Then the response status is 409
    And the problem detail type is "gln-already-assigned"

  @bdd
  Scenario: A GLN held by a site cannot be taken by a zone
    Given a POST to "/sites" with the JSON body {"siteCode":"WH2","name":"Second","gln":"4006381333931"} has been accepted
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient","gln":"4006381333931"}
    Then the response status is 409
    And the problem detail type is "gln-already-assigned"

  @bdd
  Scenario: Every spot kind of the enum is accepted on a yard spot
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-A","locationType":"YardSpot","spotKind":"PARKING"}
    Then the response status is 201
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-02-A","locationType":"YardSpot","spotKind":"HAZMAT"}
    Then the response status is 201
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-03-A","locationType":"YardSpot","spotKind":"STAGING_LANE"}
    Then the response status is 201

  @bdd
  Scenario: A spot kind on a dock door is refused
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-B","locationType":"DockDoor","dockFlow":"Inbound","spotKind":"PARKING"}
    Then the response status is 422
    And the problem detail type is "spot-kind-not-allowed"

  @bdd
  Scenario: An unknown spot kind is refused
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-A","locationType":"YardSpot","spotKind":"TRAILER_BAY"}
    Then the response status is 422
    And the problem detail type is "unknown-spot-kind"

  @bdd
  Scenario: An old payload without the new fields still works and omits them everywhere
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-YARD-NORTH-Y01-01-01-A","locationType":"YardSpot"}
    Then the response status is 201
    And the response has no field "gln"
    And the response has no field "spotKind"
    And the last "LocationSlotRegistered" domain event has no field "gln"
    And the last "LocationSlotRegistered" domain event has no field "spotKind"

  @bdd
  Scenario: A site and a zone registered without a GLN omit it
    When a POST request is sent to "/sites" with the JSON body {"siteCode":"WH2","name":"Second"}
    Then the response status is 201
    And the response has no field "gln"
    And the last "SiteRegistered" domain event has no field "gln"
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode":"STOR","zoneCode":"AMB","temperatureClass":"Ambient"}
    Then the response status is 201
    And the response has no field "gln"
    And the last "ZoneRegistered" domain event has no field "gln"
