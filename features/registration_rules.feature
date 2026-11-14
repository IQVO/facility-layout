# Registration rules of the structural aggregates, taken from the documentation
# (warehouse-docs docs/contexts/facility-layout: aggregate-design-canvas
# "Enforced Invariants" / "State Transitions" / "Corrective Policies", use-cases
# "Invariants checked (-> problem slug)" and the REST problem table in
# troubleshooting; in-repo docs/docs/ddd/invariants.md).
#
# Each scenario pins ONE documented rule on Site, Zone, Aisle or LocationType
# with concrete codes and asserts the outcome (status, slug, state read back,
# events published), never only a 2xx.

Feature: Registration rules of Site, Zone, Aisle and LocationType
  The warehouse map only accepts identities that are real: upper-case codes
  that are never normalised, unique within their parent, with capacities and
  pitches that are strictly positive where they are set.

  Background:
    Given an empty warehouse map

  # ------------------------------------------------------------------ Site --

  @bdd
  Scenario Outline: A site code is an identity, so it is refused rather than normalised
    When a POST request is sent to "/sites" with the JSON body {"siteCode": "<code>", "name": "Fulfilment Centre One"}
    Then the response status is 400
    And the problem detail type is "invalid-site-code"
    And exactly 0 "SiteRegistered" domain events are published

    Examples:
      | code | why                          |
      | wh1  | lowercase is not upper-cased |
      | Wh1  | mixed case                   |
      | WH-1 | the hyphen is the separator  |
      | WH 1 | whitespace                   |
      | WH1! | punctuation                  |
      |      | empty                        |

  @bdd
  Scenario Outline: A site needs a name
    When a POST request is sent to "/sites" with the JSON body <body>
    Then the response status is 400
    And the problem detail type is "empty-site-name"
    And exactly 0 "SiteRegistered" domain events are published

    Examples:
      | body                                  |
      | {"siteCode": "WH1", "name": ""}       |
      | {"siteCode": "WH1"}                   |

  @bdd
  Scenario: A duplicate site code is refused, not merged
    Given a POST to "/sites" with the JSON body {"siteCode": "WH1", "name": "Fulfilment Centre One"} has been accepted
    When a POST request is sent to "/sites" with the JSON body {"siteCode": "WH1", "name": "Renamed Centre"}
    Then the response status is 409
    And the problem detail type is "duplicate-site-code"
    When a GET request is sent to "/sites/WH1"
    Then the response field "name" is "Fulfilment Centre One"
    And exactly 1 "SiteRegistered" domain events are published

  @bdd
  Scenario: A registered site, zone and aisle all start Active
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    When a GET request is sent to "/sites/WH1"
    Then the response field "status" is "Active"
    When a GET request is sent to "/zones/WH1-STOR-AMB"
    Then the response field "status" is "Active"
    When a GET request is sent to "/zones/WH1-STOR-AMB/aisles/A07"
    Then the response field "status" is "Active"

  # ------------------------------------------------------------------ Zone --

  @bdd
  Scenario Outline: Ambient, Chilled and Frozen are the temperature classes a zone accepts
    Given a registered Site "WH1"
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "<class>"}
    Then the response status is 201
    And the response field "temperatureClass" is "<class>"
    And exactly 1 "ZoneRegistered" domain events are published

    Examples:
      | class   |
      | Ambient |
      | Chilled |
      | Frozen  |

  @bdd
  Scenario Outline: Any other temperature class is refused, spelt exactly
    Given a registered Site "WH1"
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "<class>"}
    Then the response status is 422
    And the problem detail type is "unknown-temperature-class"
    And exactly 0 "ZoneRegistered" domain events are published

    Examples:
      | class   |
      | Cold    |
      | ambient |
      |         |

  @bdd
  Scenario Outline: Area and zone codes are upper-case alphanumerics and never empty
    Given a registered Site "WH1"
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode": "<area>", "zoneCode": "<zone>", "temperatureClass": "Ambient"}
    Then the response status is 400
    And the problem detail type is "invalid-zone-code"
    And exactly 0 "ZoneRegistered" domain events are published

    Examples:
      | area  | zone | why                  |
      | stor  | AMB  | lowercase area       |
      | STOR  | amb  | lowercase zone       |
      | ST-OR | AMB  | hyphen in the area   |
      |       | AMB  | empty area           |
      | STOR  |      | empty zone           |

  @bdd
  Scenario: A duplicate zone is refused even when its temperature class differs
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Frozen"}
    Then the response status is 409
    And the problem detail type is "duplicate-zone"
    When a GET request is sent to "/zones/WH1-STOR-AMB"
    Then the response field "temperatureClass" is "Ambient"
    And exactly 1 "ZoneRegistered" domain events are published

  @bdd
  Scenario: A zone is identified by site, area and zone together
    Given a registered Site "WH1"
    And a registered Site "WH2"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode": "PACK", "zoneCode": "AMB", "temperatureClass": "Ambient"}
    Then the response status is 201
    When a POST request is sent to "/sites/WH2/zones" with the JSON body {"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient"}
    Then the response status is 201
    When I list the zones of Site "WH1"
    Then the listed zones are "WH1-PACK-AMB,WH1-STOR-AMB"

  @bdd
  Scenario Outline: A zone's bay and level pitch are both set and both positive, or neither is
    Given a registered Site "WH1"
    When a POST request is sent to "/sites/WH1/zones" with the JSON body {"areaCode": "STOR", "zoneCode": "AMB", "temperatureClass": "Ambient"<pitch>}
    Then the response status is 422
    And the problem detail type is "invalid-pitch"
    And exactly 0 "ZoneRegistered" domain events are published

    Examples:
      | pitch                                     | why                      |
      | , "bayPitchM": 1.2                        | level pitch missing      |
      | , "levelPitchM": 1.5                      | bay pitch missing        |
      | , "bayPitchM": 0, "levelPitchM": 1.5      | bay pitch at zero        |
      | , "bayPitchM": 1.2, "levelPitchM": 0      | level pitch at zero      |
      | , "bayPitchM": -0.5, "levelPitchM": 1.5   | negative bay pitch       |

  @bdd
  Scenario: A zone without a pitch falls back to 1.2 m between bays and 1.5 m between levels
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When a GET request is sent to "/zones/WH1-STOR-AMB"
    Then the response field "bayPitchM" is 1.2
    And the response field "levelPitchM" is 1.5

  # ----------------------------------------------------------------- Aisle --

  @bdd
  Scenario Outline: A sequence hint of zero or more is a walk-order position
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When a POST request is sent to "/zones/WH1-STOR-AMB/aisles" with the JSON body {"aisleCode": "A07", "sequenceHint": <hint>, "direction": "TwoWay"}
    Then the response status is 201
    And the response field "sequenceHint" is <hint>
    And exactly 1 "AisleRegistered" domain events are published

    Examples:
      | hint |
      | 0    |
      | 1    |

  @bdd
  Scenario: A negative sequence hint is refused
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When a POST request is sent to "/zones/WH1-STOR-AMB/aisles" with the JSON body {"aisleCode": "A07", "sequenceHint": -1, "direction": "TwoWay"}
    Then the response status is 422
    And the problem detail type is "negative-sequence-hint"
    And exactly 0 "AisleRegistered" domain events are published

  @bdd
  Scenario: A missing sequence hint is a bad request, not a silent zero
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When a POST request is sent to "/zones/WH1-STOR-AMB/aisles" with the JSON body {"aisleCode": "A07", "direction": "TwoWay"}
    Then the response status is 400
    And the problem detail type is "missing-sequence-hint"

  @bdd
  Scenario Outline: An aisle runs OneWay or TwoWay and nothing else
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When a POST request is sent to "/zones/WH1-STOR-AMB/aisles" with the JSON body {"aisleCode": "A07", "sequenceHint": 7, "direction": "<direction>"}
    Then the response status is 422
    And the problem detail type is "unknown-direction"

    Examples:
      | direction |
      | Sideways  |
      | oneway    |
      |           |

  @bdd
  Scenario Outline: An aisle code is upper-case alphanumeric and never empty
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When a POST request is sent to "/zones/WH1-STOR-AMB/aisles" with the JSON body {"aisleCode": "<code>", "sequenceHint": 7, "direction": "TwoWay"}
    Then the response status is 400
    And the problem detail type is "invalid-aisle-code"

    Examples:
      | code |
      | a07  |
      | A-07 |
      |      |

  @bdd
  Scenario: An aisle code is unique within its zone only
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Zone "STOR"/"CHL" in Site "WH1" with temperature class "Chilled"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    When I register the Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 9 and direction "OneWay"
    Then the response status is 409
    And the problem detail type is "duplicate-aisle"
    When I register the Aisle "A07" in Zone "WH1-STOR-CHL" with sequence hint 7 and direction "TwoWay"
    Then the response status is 201
    And exactly 2 "AisleRegistered" domain events are published

  @bdd
  Scenario: An aisle cannot be registered in a zone that does not exist
    When I register the Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7 and direction "TwoWay"
    Then the response status is 404
    And the problem detail type is "zone-not-found"
    And exactly 0 "AisleRegistered" domain events are published

  # ---------------------------------------------------------- LocationType --

  @bdd
  Scenario Outline: Roles that hold stock at rest must carry a capacity
    When a POST request is sent to "/location-types" with the JSON body {"name": "Bay<role>", "role": "<role>"}
    Then the response status is 422
    And the problem detail type is "invalid-max-weight"
    And exactly 0 "LocationTypeRegistered" domain events are published

    Examples:
      | role          |
      | Storage       |
      | Staging       |
      | Drop          |
      | Consolidation |

  @bdd
  Scenario Outline: Roles that do not hold stock at rest need no capacity
    When a POST request is sent to "/location-types" with the JSON body {"name": "Bay<role>", "role": "<role>"}
    Then the response status is 201
    And the response field "role" is "<role>"
    And the last "LocationTypeRegistered" domain event has no field "maxWeightKg"
    And the last "LocationTypeRegistered" domain event has no field "maxVolumeM3"

    Examples:
      | role       |
      | Dock       |
      | Yard       |
      | WorkCenter |
      | QC         |
      | Shipping   |

  @bdd
  Scenario Outline: A capacity that is given must be strictly positive in weight and volume
    When a POST request is sent to "/location-types" with the JSON body {"name": "PalletRack", "defaultCapacity": {"maxWeightKg": <weight>, "maxVolumeM3": <volume>}}
    Then the response status is <status>
    And the problem detail type is "<slug>"

    Examples:
      | weight | volume | status | slug               |
      | 0      | 2.4    | 422    | invalid-max-weight |
      | -5     | 2.4    | 422    | invalid-max-weight |
      | 1200   | 0      | 422    | invalid-max-volume |
      | 1200   | -1     | 422    | invalid-max-volume |

  @bdd
  Scenario Outline: A location role is one of the nine known roles, spelt exactly
    When a POST request is sent to "/location-types" with the JSON body {"name": "Mystery", "role": "<role>", "defaultCapacity": {"maxWeightKg": 100, "maxVolumeM3": 1}}
    Then the response status is 422
    And the problem detail type is "unknown-location-role"

    Examples:
      | role       |
      | Warehouse  |
      | storage    |
      | Work Center|

  @bdd
  Scenario: A location type registered without a role is a Storage type
    When a POST request is sent to "/location-types" with the JSON body {"name": "PalletRack", "defaultCapacity": {"maxWeightKg": 1200, "maxVolumeM3": 2.4}}
    Then the response status is 201
    When a GET request is sent to "/location-types/PalletRack"
    Then the response field "role" is "Storage"

  @bdd
  Scenario: A location type needs a name
    When a POST request is sent to "/location-types" with the JSON body {"name": "", "defaultCapacity": {"maxWeightKg": 1200, "maxVolumeM3": 2.4}}
    Then the response status is 400
    And the problem detail type is "invalid-location-type"

  @bdd
  Scenario: A duplicate location type name is refused and the first definition stands
    Given a LocationType "PalletRack" with capacity 1200 kg and 2.4 m3
    When a POST request is sent to "/location-types" with the JSON body {"name": "PalletRack", "defaultCapacity": {"maxWeightKg": 999, "maxVolumeM3": 9}}
    Then the response status is 409
    And the problem detail type is "duplicate-location-type"
    When a GET request is sent to "/location-types/PalletRack"
    Then the response field "defaultCapacity.maxWeightKg" is 1200
    And exactly 1 "LocationTypeRegistered" domain events are published
