# Bulk-import rules, taken from the documentation (use-cases ImportFacilityLayout;
# aggregate-design-canvas Site "Corrective Policies" (ensureSite reuses an
# Active site, rejects a non-Active one); in-repo docs/docs/ddd/invariants.md
# "Bulk import: atomic per row, never all-or-nothing" and ADR 0006).
#
# Every row creates the parents it names on first sight, never mutates an
# existing one, and then runs the same RegisterLocationSlot (and, with
# geometry, SetLocationGeometry) as a hand-made request. One bad row never
# aborts the rest, and exactly one FacilityLayoutImported summarises the call.

Feature: Importing a facility layout row by row
  The report and the events must agree: what was created is announced once,
  what was refused is counted, and re-running an import is visibly not a no-op.

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1200 kg and 2.4 m3
    And a POST to "/location-types" with the JSON body {"name": "DockDoor", "role": "Dock"} has been accepted

  @bdd
  Scenario: An empty import is refused rather than answered with a zero-row report
    When a POST request is sent to "/location-imports" with the JSON body []
    Then the response status is 400
    And the problem detail type is "empty-import"
    And exactly 0 "FacilityLayoutImported" domain events are published

  @bdd
  Scenario: A clean import announces each new parent once and each slot once
    When I import the following facility layout rows:
      | siteCode | areaCode | zoneCode | temperatureClass | aisleCode | sequenceHint | bay | level | position | locationType |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 01  | 01    | A        | PalletRack   |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 02  | 01    | A        | PalletRack   |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 03  | 01    | A        | PalletRack   |
    Then the import report says 3 submitted, 3 imported, 0 rejected
    And exactly 1 "SiteRegistered" domain events are published
    And exactly 1 "ZoneRegistered" domain events are published
    And exactly 1 "AisleRegistered" domain events are published
    And exactly 3 "LocationSlotRegistered" domain events are published
    And exactly 1 "FacilityLayoutImported" domain events are published
    And the last "FacilityLayoutImported" domain event has the field "rowsSubmitted" equal to "3"
    And the last "FacilityLayoutImported" domain event has the field "slotsImported" equal to "3"
    And the last "FacilityLayoutImported" domain event has the field "rowsRejected" equal to "0"

  @bdd
  Scenario: A mixed import summarises, announces and counts exactly what happened
    When I import the following facility layout rows:
      | siteCode | areaCode | zoneCode | temperatureClass | aisleCode | sequenceHint | bay | level | position | locationType |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 01  | 01    | A        | PalletRack   |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 02  | 01    | A        | Hovercraft   |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 03  | 01    | A        | PalletRack   |
    Then the import report says 3 submitted, 2 imported, 1 rejected
    And exactly 2 "LocationSlotRegistered" domain events are published
    And exactly 1 "FacilityLayoutImported" domain events are published
    And the last "FacilityLayoutImported" domain event has the field "rowsSubmitted" equal to "3"
    And the last "FacilityLayoutImported" domain event has the field "slotsImported" equal to "2"
    And the last "FacilityLayoutImported" domain event has the field "rowsRejected" equal to "1"
    And the slot registration counter shows 2 "accepted" outcomes
    And the slot registration counter shows 1 "rejected" outcome

  @bdd
  Scenario: A row that names an existing parent reuses it and never redefines it
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    When I import the following facility layout rows:
      | siteCode | areaCode | zoneCode | temperatureClass | aisleCode | sequenceHint | bay | level | position | locationType |
      | WH1      | STOR     | AMB      | Frozen           | A07       | 99           | 01  | 01    | A        | PalletRack   |
    Then the import report says 1 submitted, 1 imported, 0 rejected
    And exactly 1 "SiteRegistered" domain events are published
    And exactly 1 "ZoneRegistered" domain events are published
    And exactly 1 "AisleRegistered" domain events are published
    When a GET request is sent to "/zones/WH1-STOR-AMB"
    Then the response field "temperatureClass" is "Ambient"
    When a GET request is sent to "/zones/WH1-STOR-AMB/aisles/A07"
    Then the response field "sequenceHint" is 7

  @bdd
  Scenario: A row under a parent that is no longer Active is rejected and the others carry on
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Site "WH2"
    And the Zone "WH1-STOR-AMB" has been decommissioned
    And the Site "WH2" has been decommissioned
    When I import the following facility layout rows:
      | siteCode | areaCode | zoneCode | temperatureClass | aisleCode | sequenceHint | bay | level | position | locationType |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 01  | 01    | A        | PalletRack   |
      | WH1      | STOR     | FRZ      | Frozen           | A02       | 2            | 01  | 01    | A        | PalletRack   |
      | WH2      | STOR     | AMB      | Ambient          | A01       | 1            | 01  | 01    | A        | PalletRack   |
    Then the import report says 3 submitted, 1 imported, 2 rejected
    And import row 0 was rejected mentioning "zone is not active"
    And import row 1 succeeded
    And import row 2 was rejected mentioning "site is not active"
    And no LocationSlot "WH1-STOR-AMB-A07-01-01-A" exists
    And no LocationSlot "WH2-STOR-AMB-A01-01-01-A" exists
    And the LocationSlot "WH1-STOR-FRZ-A02-01-01-A" exists

  @bdd
  Scenario: A row refused at the slot step keeps the parents it already created
    When I import the following facility layout rows:
      | siteCode | areaCode | zoneCode | temperatureClass | aisleCode | sequenceHint | bay | level | position | locationType |
      | WH1      | STOR     | FRZ      | Frozen           | A02       | 2            | 01  | 01    | A        | Hovercraft   |
    Then the import report says 1 submitted, 0 imported, 1 rejected
    And import row 0 was rejected mentioning "location type not found"
    And the Site "WH1" exists
    And the Zone "WH1-STOR-FRZ" exists
    And no LocationSlot "WH1-STOR-FRZ-A02-01-01-A" exists
    When a GET request is sent to "/zones/WH1-STOR-FRZ/aisles/A02"
    Then the response status is 200

  @bdd
  Scenario: Running the same import again is not idempotent: every slot is a duplicate
    Given I import the following facility layout rows:
      | siteCode | areaCode | zoneCode | temperatureClass | aisleCode | sequenceHint | bay | level | position | locationType |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 01  | 01    | A        | PalletRack   |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 02  | 01    | A        | PalletRack   |
    When I import the following facility layout rows:
      | siteCode | areaCode | zoneCode | temperatureClass | aisleCode | sequenceHint | bay | level | position | locationType |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 01  | 01    | A        | PalletRack   |
      | WH1      | STOR     | AMB      | Ambient          | A07       | 7            | 02  | 01    | A        | PalletRack   |
    Then the import report says 2 submitted, 0 imported, 2 rejected
    And import row 0 was rejected mentioning "already exists"
    And import row 1 was rejected mentioning "already exists"
    And exactly 1 "SiteRegistered" domain events are published
    And exactly 2 "LocationSlotRegistered" domain events are published
    And exactly 2 "FacilityLayoutImported" domain events are published

  @bdd
  Scenario: A row with geometry is registered, positioned and announced
    When a POST request is sent to "/location-imports" with the JSON body [{"siteCode": "WH1", "areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "aisleCode": "A07", "sequenceHint": 7, "bay": "03", "level": "01", "position": "A", "locationType": "PalletRack", "geometry": {"position": {"xM": 12.5, "yM": 7.25, "zM": 1.5}, "dimensions": {"widthM": 1.2, "depthM": 0.9, "heightM": 2.4}}, "pickSequence": 5}]
    Then the response field "slotsImported" is 1
    And exactly 1 "LocationGeometryUpdated" domain events are published
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-03-01-A"
    Then the response field "position.xM" is 12.5
    And the response field "dimensions.heightM" is 2.4
    And the response field "pickSequence" is 5

  @bdd
  Scenario Outline: A row whose geometry is refused is reported, but its slot stays registered
    When a POST request is sent to "/location-imports" with the JSON body [{"siteCode": "WH1", "areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "aisleCode": "A07", "sequenceHint": 7, "bay": "03", "level": "01", "position": "A", "locationType": "PalletRack", "geometry": <geometry>}]
    Then the response field "rowsRejected" is 1
    And the response field "results.0.succeeded" is false
    And the response field "results.0.error" is "<error>"
    And the LocationSlot "WH1-STOR-AMB-A07-03-01-A" exists
    And exactly 0 "LocationGeometryUpdated" domain events are published

    Examples:
      | geometry                                                                                                          | error                                         |
      | {"position": {"xM": 1, "yM": 1, "zM": -1}, "dimensions": {"widthM": 1, "depthM": 1, "heightM": 1}}                | z coordinate must not be negative             |
      | {"position": {"xM": 1, "yM": 1, "zM": 0}, "dimensions": {"widthM": 1, "depthM": 0, "heightM": 1}}                 | width, depth, and height must all be greater than zero |

  @bdd
  Scenario: Capacity and functional attributes ride along on import rows under the same rules
    When a POST request is sent to "/location-imports" with the JSON body [{"siteCode": "WH1", "areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "aisleCode": "A07", "sequenceHint": 7, "bay": "01", "level": "01", "position": "A", "locationType": "PalletRack", "capacityOverride": {"maxWeightKg": 500, "maxVolumeM3": 1.5}}, {"siteCode": "WH1", "areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "aisleCode": "A07", "sequenceHint": 7, "bay": "02", "level": "01", "position": "A", "locationType": "PalletRack", "capacityOverride": {"maxWeightKg": 500}}, {"siteCode": "WH1", "areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "aisleCode": "A07", "sequenceHint": 7, "bay": "03", "level": "01", "position": "A", "locationType": "DockDoor", "dockFlow": "Outbound"}, {"siteCode": "WH1", "areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient", "aisleCode": "A07", "sequenceHint": 7, "bay": "04", "level": "01", "position": "A", "locationType": "PalletRack", "dockFlow": "Outbound"}]
    Then the response field "rowsSubmitted" is 4
    And the response field "slotsImported" is 2
    And the response field "results.1.error" is "capacity max volume must be greater than zero"
    And the response field "results.3.error" is "dockFlow and activities may only be set on a Dock or WorkCenter location"
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A"
    Then the response field "capacity.maxWeightKg" is 500
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-03-01-A"
    Then the response field "dockFlow" is "Outbound"
