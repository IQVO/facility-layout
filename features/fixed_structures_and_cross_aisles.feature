# Fixed structures (obstacles) and cross-aisle connections on the map.
#
# Derived from:
#   - apis/openapi.yaml POST/GET /sites/{siteCode}/structures: a site-scoped
#     wall, column, office, conveyor or other obstacle (kind in Wall, Column,
#     Office, Conveyor, Other) with a footprint (origin + strictly positive
#     size) and a non-empty label; a UUID id is minted when id is omitted, null
#     or empty; 201 publishes FixedStructureRegistered; the list is ordered by
#     id; 404 unknown site, 409 duplicate id, 422 unknown kind / empty footprint
#   - apis/openapi.yaml POST/GET /zones/{zoneId}/cross-aisles: a connection
#     between two DISTINCT aisles of the zone at a bay ordinal; both aisles
#     must exist in the zone; the same pair at the same bay is a duplicate
#     whichever side is named "from"; 201 publishes CrossAisleRegistered; the
#     list is ordered by from-aisle, to-aisle, bay

Feature: Registering fixed structures and cross-aisles
  As a warehouse mapper
  I want to draw obstacles and aisle connections on the map
  So that routes and renderings reflect the physical building

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1000 kg and 2 m3
    And a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    And a registered Aisle "A08" in Zone "WH1-STOR-AMB" with sequence hint 8
    And a registered Aisle "A09" in Zone "WH1-STOR-AMB" with sequence hint 9

  @bdd
  Scenario: Registering a fixed structure with a caller-supplied id
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {"id":"STR-1","kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":40,"depthM":0.3,"heightM":6}},"label":"North wall"}
    Then the response status is 201
    And the response field "id" is "STR-1"
    And the response field "siteCode" is "WH1"
    And the response field "kind" is "Wall"
    And the response field "label" is "North wall"
    And the response field "size.widthM" is 40
    And exactly 1 "FixedStructureRegistered" domain event is published

  @bdd
  Scenario Outline: An id is minted when the caller gives none
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {<id>"kind":"Column","footprint":{"origin":{"xM":5,"yM":5,"zM":0},"size":{"widthM":0.5,"depthM":0.5,"heightM":6}},"label":"Column C4"}
    Then the response status is 201
    And the response field "kind" is "Column"
    And exactly 1 "FixedStructureRegistered" domain event is published

    Examples:
      | id          |
      |             |
      | "id":null,  |
      | "id":"",    |

  @bdd
  Scenario Outline: Every structure kind in the enum can be registered
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {"kind":"<kind>","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"A <kind>"}
    Then the response status is 201
    And the response field "kind" is "<kind>"

    Examples:
      | kind     |
      | Wall     |
      | Column   |
      | Office   |
      | Conveyor |
      | Other    |

  @bdd
  Scenario: A structure id cannot be registered twice
    Given a POST to "/sites/WH1/structures" with the JSON body {"id":"STR-1","kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"North wall"} has been accepted
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {"id":"STR-1","kind":"Column","footprint":{"origin":{"xM":9,"yM":9,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"Another"}
    Then the response status is 409
    And the problem detail type is "duplicate-fixed-structure"
    And exactly 1 "FixedStructureRegistered" domain event is published

  @bdd
  Scenario: Structures are listed by id and scoped to their site
    Given a registered Site "WH2"
    And a POST to "/sites/WH1/structures" with the JSON body {"id":"STR-B","kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"Second"} has been accepted
    And a POST to "/sites/WH1/structures" with the JSON body {"id":"STR-A","kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"First"} has been accepted
    And a POST to "/sites/WH2/structures" with the JSON body {"id":"STR-Z","kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"Elsewhere"} has been accepted
    When a GET request is sent to "/sites/WH1/structures"
    Then the response status is 200
    And the response list "" has 2 items
    And the "id" of each item in "" are "STR-A,STR-B"

  @bdd
  Scenario: A site with no structures lists an empty array
    When a GET request is sent to "/sites/WH1/structures"
    Then the response status is 200
    And the response list "" has 0 items

  @bdd
  Scenario: Structures cannot be registered or listed for an unknown site
    When a POST request is sent to "/sites/NOPE/structures" with the JSON body {"kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"Ghost"}
    Then the response status is 404
    And the problem detail type is "site-not-found"
    When a GET request is sent to "/sites/NOPE/structures"
    Then the response status is 404

  @bdd
  Scenario: An unknown structure kind is refused
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {"kind":"Moat","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"Moat"}
    Then the response status is 422
    And the problem detail type is "unknown-fixed-structure-kind"

  @bdd
  Scenario Outline: A structure footprint must have a real, positive size
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {"kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":<w>,"depthM":<d>,"heightM":<h>}},"label":"Wall"}
    Then the response status is 422
    And exactly 0 "FixedStructureRegistered" domain events are published

    Examples:
      | w | d | h |
      | 0 | 1 | 1 |
      | 1 | 0 | 1 |
      | 1 | 1 | 0 |
      | -2 | 1 | 1 |

  @bdd
  Scenario Outline: A structure needs a kind, a footprint and a label
    When a POST request is sent to "/sites/WH1/structures" with the JSON body <body>
    Then the response status is 400
    And exactly 0 "FixedStructureRegistered" domain events are published

    Examples:
      | body                                                                                                       |
      | {"kind":"Wall","label":"No footprint"}                                                                     |
      | {"kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}}}   |
      | {"kind":"Wall","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":""} |
      | {"kind":"Wall","footprint":                                                                                |

  @bdd
  Scenario: Registering a cross-aisle between two aisles of the zone
    When a POST request is sent to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"02"}
    Then the response status is 201
    And the response field "zoneId" is "WH1-STOR-AMB"
    And the response field "fromAisle" is "A07"
    And the response field "toAisle" is "A08"
    And the response field "atBay" is "02"
    And the response field "active" is true
    And exactly 1 "CrossAisleRegistered" domain event is published

  @bdd
  Scenario Outline: The same two aisles at the same bay are a duplicate whichever side is "from"
    Given a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"02"} has been accepted
    When a POST request is sent to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"<from>","toAisle":"<to>","atBay":"02"}
    Then the response status is 409
    And the problem detail type is "duplicate-cross-aisle"
    And exactly 1 "CrossAisleRegistered" domain event is published

    Examples:
      | from | to  |
      | A07  | A08 |
      | A08  | A07 |

  @bdd
  Scenario: The same two aisles at another bay are a different connection
    Given a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"02"} has been accepted
    When a POST request is sent to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"03"}
    Then the response status is 201

  @bdd
  Scenario: A cross-aisle must connect two distinct aisles
    When a POST request is sent to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A07","atBay":"02"}
    Then the response status is 422
    And the problem detail type is "cross-aisle-same-aisle"

  @bdd
  Scenario: A cross-aisle to an aisle that is not in the zone is refused
    When a POST request is sent to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"Z99","atBay":"02"}
    Then the response status is 422
    And the problem detail type is "cross-aisle-aisle-mismatch"
    And exactly 0 "CrossAisleRegistered" domain events are published

  @bdd
  Scenario: A cross-aisle in an unknown zone is not found
    When a POST request is sent to "/zones/WH9-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"02"}
    Then the response status is 404
    And the problem detail type is "zone-not-found"

  @bdd
  Scenario Outline: A cross-aisle needs both aisles and a bay
    When a POST request is sent to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body <body>
    Then the response status is 400
    And exactly 0 "CrossAisleRegistered" domain events are published

    Examples:
      | body                                                |
      | {"fromAisle":"A07","toAisle":"A08"}                 |
      | {"fromAisle":"A07","toAisle":"A08","atBay":""}      |
      | {"fromAisle":                                       |

  @bdd
  Scenario: Cross-aisles are listed by from-aisle, to-aisle and bay
    Given a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A08","toAisle":"A09","atBay":"01"} has been accepted
    And a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A09","atBay":"03"} has been accepted
    And a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"02"} has been accepted
    When a GET request is sent to "/zones/WH1-STOR-AMB/cross-aisles"
    Then the response status is 200
    And the response list "" has 3 items
    And the "fromAisle" of each item in "" are "A07,A07,A08"
    And the "toAisle" of each item in "" are "A08,A09,A09"

  @bdd
  Scenario: A zone with no cross-aisles lists an empty array and an unknown zone is not found
    When a GET request is sent to "/zones/WH1-STOR-AMB/cross-aisles"
    Then the response status is 200
    And the response list "" has 0 items
    When a GET request is sent to "/zones/WH9-STOR-AMB/cross-aisles"
    Then the response status is 404

  @bdd
  Scenario: A cross-aisle without a from-aisle or a to-aisle is a bad request
    # Validation of the request runs before the aisle-membership check, so a
    # missing aisle is a 400 and not a 422 cross-aisle-aisle-mismatch.
    When a POST request is sent to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"toAisle":"A08","atBay":"02"}
    Then the response status is 400
    And the problem detail type is "empty-cross-aisle-from-aisle"
    When a POST request is sent to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","atBay":"02"}
    Then the response status is 400
    And the problem detail type is "empty-cross-aisle-to-aisle"
    And exactly 0 "CrossAisleRegistered" domain events are published

  @bdd
  Scenario: A structure without a kind is a bad request, an unrecognised kind is unprocessable
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {"footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"No kind"}
    Then the response status is 400
    And the problem detail type is "empty-fixed-structure-kind"
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {"kind":"","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"Blank kind"}
    Then the response status is 400
    And the problem detail type is "empty-fixed-structure-kind"
    When a POST request is sent to "/sites/WH1/structures" with the JSON body {"kind":"Moat","footprint":{"origin":{"xM":0,"yM":0,"zM":0},"size":{"widthM":1,"depthM":1,"heightM":1}},"label":"Moat"}
    Then the response status is 422
    And the problem detail type is "unknown-fixed-structure-kind"
    And exactly 0 "FixedStructureRegistered" domain events are published
