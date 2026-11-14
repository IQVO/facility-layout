# Slot registration and decommission rules, taken from the documentation
# (aggregate-design-canvas LocationSlot invariants / transitions / corrective
# policies; use-cases RegisterLocationSlot and DecommissionLocationSlot; the
# check-order flow in in-repo docs/docs/ddd/invariants.md; troubleshooting
# "outcome=rejected_by_placement_rule").

Feature: Registering and decommissioning a location slot
  A slot is a chain-of-custody check, not a bare insert. The checks run in a
  documented order, so the same bad request always earns the same answer.

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1200 kg and 2.4 m3
    And a LocationType "Shelf" with capacity 60 kg and 0.4 m3
    And a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7

  # ------------------------------------------------------------ the code ----

  @bdd
  Scenario Outline: A location code is seven upper-case alphanumeric segments, never normalised
    When a POST request is sent to "/locations" with the JSON body {"locationCode": "<code>", "locationType": "PalletRack"}
    Then the response status is 400
    And the problem detail type is "malformed-location-code"
    And exactly 0 "LocationSlotRegistered" domain events are published
    When I request the layout of Site "WH1"
    Then the layout reports 1 zones, 1 aisles and 0 slots

    Examples:
      | code                        | what is wrong               |
      | wh1-stor-amb-a07-03-02-b    | lowercase                   |
      | WH1-STOR-AMB-A07-03-02      | six segments                |
      | WH1-STOR-AMB-A07-03-02-B-X  | eight segments              |
      | WH1-STOR-AMB-A07--02-B      | an empty segment            |
      | WH1-STOR-AMB-A07-03-02-B!   | punctuation in a segment    |
      |                             | no code at all              |

  @bdd
  Scenario: The zone and aisle ids of a slot fall out of its code
    Given a registered LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-03-02-B"
    Then the response field "zoneId" is "WH1-STOR-AMB"
    And the response field "aisleId" is "WH1-STOR-AMB-A07"
    And the response field "coordinates.site" is "WH1"
    And the response field "coordinates.bay" is "03"
    And the response field "coordinates.level" is "02"
    And the response field "coordinates.position" is "B"

  # --------------------------------------------------------- check order ----

  @bdd
  Scenario Outline: A slot whose site or zone does not exist is not found, naming the missing link
    When a POST request is sent to "/locations" with the JSON body {"locationCode": "<code>", "locationType": "PalletRack"}
    Then the response status is 404
    And the problem detail type is "<slug>"
    And exactly 0 "LocationSlotRegistered" domain events are published

    Examples:
      | code                     | slug           |
      | WH9-STOR-AMB-A07-03-02-B | site-not-found |
      | WH1-STOR-FRZ-A07-03-02-B | zone-not-found |

  @bdd
  Scenario: A duplicate code is answered before the location type is even looked up
    Given a registered LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    When I register the LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "Hovercraft"
    Then the response status is 409
    And the problem detail type is "duplicate-location-code"

  @bdd
  Scenario: The chain of custody is walked before the location type is looked up
    When I register the LocationSlot "WH1-STOR-AMB-A99-03-02-B" of type "Hovercraft"
    Then the response status is 404
    And the problem detail type is "aisle-not-found"

  @bdd
  Scenario: A dock flow on a storage slot is refused before any placement rule is read
    Given a PlacementRule "R-AMB-NO-SHELF" denying "Shelf" where temperature class is "Ambient"
    When a POST request is sent to "/locations" with the JSON body {"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "Shelf", "dockFlow": "Inbound"}
    Then the response status is 422
    And the problem detail type is "functional-attributes-not-allowed"

  # ------------------------------------------------------------ capacity ----

  @bdd
  Scenario: Without an override a slot takes its type's default capacity envelope
    When I register the LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    Then the response status is 201
    And the response field "capacity.maxWeightKg" is 1200
    And the response field "capacity.maxVolumeM3" is 2.4
    And the last "LocationSlotRegistered" domain event has the field "maxWeightKg" equal to "1200"

  @bdd
  Scenario: A capacity override replaces the type's default for that slot only
    When a POST request is sent to "/locations" with the JSON body {"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack", "capacityOverride": {"maxWeightKg": 500, "maxVolumeM3": 1.5}}
    Then the response status is 201
    And the response field "capacity.maxWeightKg" is 500
    And the response field "capacity.maxVolumeM3" is 1.5
    When I register the LocationSlot "WH1-STOR-AMB-A07-03-03-B" of type "PalletRack"
    Then the response field "capacity.maxWeightKg" is 1200

  @bdd
  Scenario Outline: A capacity override must be strictly positive in weight and volume
    When a POST request is sent to "/locations" with the JSON body {"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack", "capacityOverride": {"maxWeightKg": <weight>, "maxVolumeM3": <volume>}}
    Then the response status is 422
    And the problem detail type is "<slug>"
    And no LocationSlot "WH1-STOR-AMB-A07-03-02-B" exists

    Examples:
      | weight | volume | slug               |
      | 0      | 1.5    | invalid-max-weight |
      | -1     | 1.5    | invalid-max-weight |
      | 500    | 0      | invalid-max-volume |

  # ------------------------------------------------- functional attributes ----

  @bdd
  Scenario: A dock door needs no capacity and publishes none
    Given a POST to "/location-types" with the JSON body {"name": "DockDoor", "role": "Dock"} has been accepted
    And a registered Zone "RCV"/"DOCK" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "D01" in Zone "WH1-RCV-DOCK" with sequence hint 1
    When a POST request is sent to "/locations" with the JSON body {"locationCode": "WH1-RCV-DOCK-D01-01-01-A", "locationType": "DockDoor", "dockFlow": "Inbound"}
    Then the response status is 201
    And the response field "capacity.maxWeightKg" is 0
    And the last "LocationSlotRegistered" domain event has the field "dockFlow" equal to "Inbound"
    And the last "LocationSlotRegistered" domain event has the field "role" equal to "Dock"
    And the last "LocationSlotRegistered" domain event has no field "maxWeightKg"

  @bdd
  Scenario Outline: A dock flow and a work-center activity must be ones the vocabulary knows
    Given a POST to "/location-types" with the JSON body {"name": "DockDoor", "role": "Dock"} has been accepted
    And a POST to "/location-types" with the JSON body {"name": "PackStation", "role": "WorkCenter"} has been accepted
    When a POST request is sent to "/locations" with the JSON body {"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "<type>", <attribute>}
    Then the response status is 422
    And the problem detail type is "<slug>"

    Examples:
      | type        | attribute                   | slug            |
      | DockDoor    | "dockFlow": "Sideways"      | unknown-dock-flow |
      | DockDoor    | "dockFlow": "inbound"       | unknown-dock-flow |
      | PackStation | "activities": ["Juggle"]    | unknown-activity  |
      | PackStation | "activities": ["Pack", "pack"] | unknown-activity |

  @bdd
  Scenario: Repeated work-center activities are de-duplicated and kept sorted
    Given a POST to "/location-types" with the JSON body {"name": "PackStation", "role": "WorkCenter"} has been accepted
    When a POST request is sent to "/locations" with the JSON body {"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PackStation", "activities": ["Sort", "Pack", "Sort"]}
    Then the response status is 201
    And the response list "activities" has 2 items
    And the response field "activities.0" is "Pack"
    And the response field "activities.1" is "Sort"

  # ------------------------------------------------------- decommissioning ----

  @bdd
  Scenario: Decommissioning retires the slot, announces it once and keeps it readable
    Given a registered LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    When I decommission the LocationSlot "WH1-STOR-AMB-A07-03-02-B"
    Then the response status is 204
    And exactly 1 "LocationSlotDecommissioned" domain events are published
    When I request the LocationSlot "WH1-STOR-AMB-A07-03-02-B"
    Then the LocationSlot response reports type "PalletRack" with status "Decommissioned"
    When I decommission the LocationSlot "WH1-STOR-AMB-A07-03-02-B"
    Then the response status is 409
    And exactly 1 "LocationSlotDecommissioned" domain events are published

  @bdd
  Scenario Outline: Decommissioning needs a slot that exists and a code that parses
    When a POST request is sent to "/locations/<code>/decommissionings"
    Then the response status is <status>
    And the problem detail type is "<slug>"
    And exactly 0 "LocationSlotDecommissioned" domain events are published

    Examples:
      | code                     | status | slug                    |
      | WH1-STOR-AMB-A07-09-09-Z | 404    | location-slot-not-found |
      | wh1-stor-amb-a07-09-09-z | 400    | malformed-location-code |

  # -------------------------------------------------------------- metrics ----

  @bdd
  Scenario: Every registration attempt is counted under its outcome
    Given a PlacementRule "R-AMB-NO-SHELF" denying "Shelf" where temperature class is "Ambient"
    When I register the LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    And I register the LocationSlot "WH1-STOR-AMB-A07-03-03-B" of type "Shelf"
    And I register the LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    Then the slot registration counter shows 1 "accepted" outcome
    And the slot registration counter shows 1 "rejected_by_placement_rule" outcome
    And the slot registration counter shows 1 "rejected" outcome
    And 3 slot registration attempts have been counted in all
