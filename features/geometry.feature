# Recording physical geometry on locations and aisles.
#
# Derived from:
#   - apis/openapi.yaml PUT /locations/{locationCode}/geometry: records a slot's
#     position (xM, yM, zM >= 0), footprint dimensions (all strictly positive)
#     and an optional explicit pickSequence (>= 0, JSON null = omitted); the
#     slot must exist (404) and not be Decommissioned (409); publishes
#     LocationGeometryUpdated
#   - apis/openapi.yaml PUT /zones/{zoneId}/aisles/{aisleCode}/geometry: records
#     a straight-line centreline between two DISTINCT real points; the aisle
#     must exist (404) and not be Decommissioned (409); publishes
#     AisleGeometryUpdated
#   - 400 for a missing required member or an unreadable body, 422 for a value
#     the domain refuses

Feature: Recording location and aisle geometry
  As a warehouse mapper
  I want to attach real positions and centrelines to the map
  So that travel distances can be measured instead of estimated

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1000 kg and 2 m3
    And a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    And bays "01" of Aisle "A07" in Zone "WH1-STOR-AMB" each have a LocationSlot of type "PalletRack"

  @bdd
  Scenario: Setting a slot's geometry echoes it and announces the change
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":12.5,"yM":7.25,"zM":1.5},"dimensions":{"widthM":1.2,"depthM":0.9,"heightM":2.4},"pickSequence":7}
    Then the response status is 200
    And the response field "locationCode" is "WH1-STOR-AMB-A07-01-01-A"
    And the response field "position.xM" is 12.5
    And the response field "position.yM" is 7.25
    And the response field "position.zM" is 1.5
    And the response field "dimensions.widthM" is 1.2
    And the response field "dimensions.depthM" is 0.9
    And the response field "dimensions.heightM" is 2.4
    And the response field "pickSequence" is 7
    And exactly 1 "LocationGeometryUpdated" domain event is published

  @bdd
  Scenario: A slot's geometry is visible when the slot is read back
    Given a PUT to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":1,"yM":2,"zM":0},"dimensions":{"widthM":1,"depthM":1,"heightM":1}} has been accepted
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A"
    Then the response status is 200
    And the response field "position.xM" is 1
    And the response field "position.yM" is 2
    And the response field "dimensions.heightM" is 1

  @bdd
  Scenario: A slot without geometry reports neither position nor dimensions
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A"
    Then the response status is 200
    And the response has no field "position"
    And the response has no field "dimensions"

  @bdd
  Scenario: The pick sequence is optional and a JSON null counts as omitted
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":0,"yM":0,"zM":0},"dimensions":{"widthM":1,"depthM":1,"heightM":1},"pickSequence":null}
    Then the response status is 200
    And the response has no field "pickSequence"

  @bdd
  Scenario: Geometry can be replaced and each change is announced
    Given a PUT to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":1,"yM":1,"zM":0},"dimensions":{"widthM":1,"depthM":1,"heightM":1}} has been accepted
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":9,"yM":9,"zM":0},"dimensions":{"widthM":2,"depthM":2,"heightM":2}}
    Then the response status is 200
    And the response field "position.xM" is 9
    And exactly 2 "LocationGeometryUpdated" domain events are published

  @bdd
  Scenario: Geometry for a slot that does not exist is not found
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-09-01-A/geometry" with the JSON body {"position":{"xM":0,"yM":0,"zM":0},"dimensions":{"widthM":1,"depthM":1,"heightM":1}}
    Then the response status is 404
    And exactly 0 "LocationGeometryUpdated" domain events are published

  @bdd
  Scenario: A decommissioned slot refuses new geometry
    Given I decommission the LocationSlot "WH1-STOR-AMB-A07-01-01-A"
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":0,"yM":0,"zM":0},"dimensions":{"widthM":1,"depthM":1,"heightM":1}}
    Then the response status is 409
    And the problem detail type is "already-decommissioned"
    And exactly 0 "LocationGeometryUpdated" domain events are published

  @bdd
  Scenario Outline: A footprint dimension must be strictly positive
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":0,"yM":0,"zM":0},"dimensions":{"widthM":<w>,"depthM":<d>,"heightM":<h>}}
    Then the response status is 422
    And the problem detail type is "invalid-dimensions"
    And exactly 0 "LocationGeometryUpdated" domain events are published

    Examples:
      | w   | d   | h   |
      | 0   | 1   | 1   |
      | 1   | 0   | 1   |
      | 1   | 1   | 0   |
      | -1  | 1   | 1   |

  @bdd
  Scenario: A position below floor level is refused
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":0,"yM":0,"zM":-0.5},"dimensions":{"widthM":1,"depthM":1,"heightM":1}}
    Then the response status is 422
    And exactly 0 "LocationGeometryUpdated" domain events are published

  @bdd
  Scenario: A negative pick sequence is refused
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":0,"yM":0,"zM":0},"dimensions":{"widthM":1,"depthM":1,"heightM":1},"pickSequence":-1}
    Then the response status is 422
    And the problem detail type is "negative-pick-sequence"

  @bdd
  Scenario Outline: A geometry request needs both a position and dimensions
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body <body>
    Then the response status is 400

    Examples:
      | body                                                          |
      | {}                                                            |
      | {"dimensions":{"widthM":1,"depthM":1,"heightM":1}}            |
      | {"position":{"xM":0,"yM":0,"zM":0}}                           |
      | {"position":{"xM":0,"yM":0,"zM":0},"dimensions":              |

  @bdd
  Scenario: Setting an aisle's centreline echoes it and announces the change
    When a PUT request is sent to "/zones/WH1-STOR-AMB/aisles/A07/geometry" with the JSON body {"centreline":{"start":{"xM":0,"yM":0,"zM":0},"end":{"xM":0,"yM":24,"zM":0}}}
    Then the response status is 200
    And the response field "aisleCode" is "A07"
    And the response field "centreline.start.xM" is 0
    And the response field "centreline.end.yM" is 24
    And exactly 1 "AisleGeometryUpdated" domain event is published

  @bdd
  Scenario: An aisle without geometry reports no centreline
    When a GET request is sent to "/zones/WH1-STOR-AMB/aisles/A07"
    Then the response status is 200
    And the response has no field "centreline"

  @bdd
  Scenario: An aisle's centreline is visible when the aisle is read back
    Given a PUT to "/zones/WH1-STOR-AMB/aisles/A07/geometry" with the JSON body {"centreline":{"start":{"xM":0,"yM":0,"zM":0},"end":{"xM":5,"yM":0,"zM":0}}} has been accepted
    When a GET request is sent to "/zones/WH1-STOR-AMB/aisles/A07"
    Then the response status is 200
    And the response field "centreline.end.xM" is 5

  @bdd
  Scenario: A centreline whose ends coincide is refused
    When a PUT request is sent to "/zones/WH1-STOR-AMB/aisles/A07/geometry" with the JSON body {"centreline":{"start":{"xM":3,"yM":3,"zM":0},"end":{"xM":3,"yM":3,"zM":0}}}
    Then the response status is 422
    And the problem detail type is "segment-start-end-equal"
    And exactly 0 "AisleGeometryUpdated" domain events are published

  @bdd
  Scenario: Geometry for an aisle that does not exist is not found
    When a PUT request is sent to "/zones/WH1-STOR-AMB/aisles/Z99/geometry" with the JSON body {"centreline":{"start":{"xM":0,"yM":0,"zM":0},"end":{"xM":1,"yM":0,"zM":0}}}
    Then the response status is 404
    And the problem detail type is "aisle-not-found"

  @bdd
  Scenario: A decommissioned aisle refuses a centreline
    Given the Aisle "WH1-STOR-AMB-A07" has been decommissioned
    When a PUT request is sent to "/zones/WH1-STOR-AMB/aisles/A07/geometry" with the JSON body {"centreline":{"start":{"xM":0,"yM":0,"zM":0},"end":{"xM":1,"yM":0,"zM":0}}}
    Then the response status is 409

  @bdd
  Scenario Outline: An aisle geometry request needs a readable centreline
    When a PUT request is sent to "/zones/WH1-STOR-AMB/aisles/A07/geometry" with the JSON body <body>
    Then the response status is 400

    Examples:
      | body                                                |
      | {}                                                  |
      | {"centreline":                                      |
