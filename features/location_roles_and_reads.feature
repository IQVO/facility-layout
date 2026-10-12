# Listing locations by functional role, and the single-resource reads.
#
# Derived from:
#   - apis/openapi.yaml GET /sites/{siteCode}/locations?role=: every slot at the
#     site whose LocationType role equals the requested LocationRole (Storage,
#     Dock, Yard, WorkCenter, Drop, Staging, QC, Consolidation, Shipping),
#     ordered zone, aisle, bay, level, position; an empty array when none;
#     404 unknown site, 422 unknown role
#   - docs/docs/adr/0016-functional-location-roles.md: a Dock slot carries a
#     dockFlow and a WorkCenter slot carries activities
#   - apis/openapi.yaml GET /zones/{zoneId}/aisles/{aisleCode},
#     /location-types/{name}, /placement-rules/{ruleId}: 200 with the resource,
#     404 when it does not exist

Feature: Locations by role and single-resource reads
  As a downstream context
  I want to find a site's functional locations and read single resources
  So that I need not walk the whole layout

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1000 kg and 2 m3
    And a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Zone "RCV"/"DOCK" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A01" in Zone "WH1-STOR-AMB" with sequence hint 1
    And a registered Aisle "D01" in Zone "WH1-RCV-DOCK" with sequence hint 1
    And a POST to "/location-types" with the JSON body {"name":"DockDoor","role":"Dock"} has been accepted
    And a POST to "/location-types" with the JSON body {"name":"PackStation","role":"WorkCenter"} has been accepted

  @bdd
  Scenario: Dock doors are listed by role in walk order with their flow
    Given a POST to "/locations" with the JSON body {"locationCode":"WH1-RCV-DOCK-D01-02-01-A","locationType":"DockDoor","dockFlow":"Outbound"} has been accepted
    And a POST to "/locations" with the JSON body {"locationCode":"WH1-RCV-DOCK-D01-01-01-A","locationType":"DockDoor","dockFlow":"Inbound"} has been accepted
    And a registered LocationSlot "WH1-STOR-AMB-A01-01-01-A" of type "PalletRack"
    When a GET request is sent to "/sites/WH1/locations?role=Dock"
    Then the response status is 200
    And the response list "" has 2 items
    And the "locationCode" of each item in "" are "WH1-RCV-DOCK-D01-01-01-A,WH1-RCV-DOCK-D01-02-01-A"
    And the "dockFlow" of each item in "" are "Inbound,Outbound"
    And the "role" of each item in "" are "Dock,Dock"

  @bdd
  Scenario: Storage slots are the default role
    Given a registered LocationSlot "WH1-STOR-AMB-A01-01-01-A" of type "PalletRack"
    And a POST to "/locations" with the JSON body {"locationCode":"WH1-RCV-DOCK-D01-01-01-A","locationType":"DockDoor","dockFlow":"Both"} has been accepted
    When a GET request is sent to "/sites/WH1/locations?role=Storage"
    Then the response status is 200
    And the "locationCode" of each item in "" are "WH1-STOR-AMB-A01-01-01-A"

  @bdd
  Scenario: A work center lists the activities it performs
    Given a POST to "/locations" with the JSON body {"locationCode":"WH1-STOR-AMB-A01-01-01-A","locationType":"PackStation","activities":["Pack","QC"]} has been accepted
    When a GET request is sent to "/sites/WH1/locations?role=WorkCenter"
    Then the response status is 200
    And the response list "" has 1 item
    And the response field "0.activities.0" is "Pack"
    And the response field "0.activities.1" is "QC"

  @bdd
  Scenario: A role nobody holds lists an empty array
    When a GET request is sent to "/sites/WH1/locations?role=Yard"
    Then the response status is 200
    And the response list "" has 0 items

  @bdd
  Scenario: Listing by role for an unknown site is not found
    When a GET request is sent to "/sites/NOPE/locations?role=Dock"
    Then the response status is 404
    And the problem detail type is "site-not-found"

  @bdd
  Scenario: An unknown role is refused
    When a GET request is sent to "/sites/WH1/locations?role=Spaceport"
    Then the response status is 422
    And the problem detail type is "unknown-location-role"

  @bdd
  Scenario: A dock slot must say which way goods flow and other roles must not
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-RCV-DOCK-D01-01-01-A","locationType":"DockDoor"}
    Then the response status is 422
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-STOR-AMB-A01-01-01-A","locationType":"PalletRack","dockFlow":"Inbound"}
    Then the response status is 422

  @bdd
  Scenario: A work center must name at least one activity
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-STOR-AMB-A01-01-01-A","locationType":"PackStation","activities":[]}
    Then the response status is 422

  @bdd
  Scenario: A single aisle is read by zone and code
    When a GET request is sent to "/zones/WH1-STOR-AMB/aisles/A01"
    Then the response status is 200
    And the response field "aisleId" is "WH1-STOR-AMB-A01"
    And the response field "zoneId" is "WH1-STOR-AMB"
    And the response field "direction" is "TwoWay"
    And the response field "status" is "Active"

  @bdd
  Scenario: A single aisle that does not exist is not found
    When a GET request is sent to "/zones/WH1-STOR-AMB/aisles/Z99"
    Then the response status is 404
    And the problem detail type is "aisle-not-found"

  @bdd
  Scenario: A single location type is read by name
    When a GET request is sent to "/location-types/DockDoor"
    Then the response status is 200
    And the response field "name" is "DockDoor"
    And the response field "role" is "Dock"

  @bdd
  Scenario: A location type that does not exist is not found
    When a GET request is sent to "/location-types/Ghost"
    Then the response status is 404

  @bdd
  Scenario: A single placement rule is read by id
    Given a PlacementRule "RULE-1" denying "PalletRack" where temperature class is "Frozen"
    When a GET request is sent to "/placement-rules/RULE-1"
    Then the response status is 200
    And the response field "ruleId" is "RULE-1"
    And the response field "effect" is "Deny"

  @bdd
  Scenario: A placement rule that does not exist is not found
    When a GET request is sent to "/placement-rules/RULE-NOPE"
    Then the response status is 404
