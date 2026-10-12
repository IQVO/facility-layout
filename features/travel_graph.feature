# The per-zone travel graph and the shortest-distance query built on it.
#
# Derived from:
#   - apis/openapi.yaml GET /zones/{zoneId}/travel-graph and GET /distance, and
#     docs/docs/adr/0017-geometry-and-travel-graph.md: one waypoint per aisle+bay
#     that has a slot; one edge per pair of adjacent bays (both directions on a
#     two-way aisle, walk-order only on a one-way aisle); an active cross-aisle
#     adds a bidirectional edge pair at its bay between the two aisles
#   - an edge length comes from the aisle's centreline divided evenly across its
#     bay gaps when geometry is set (estimated=false), else from the zone's bay
#     pitch (default 1.2 m, estimated=true); a cross-aisle edge is always the
#     bay pitch and always estimated
#   - /distance: Dijkstra honouring one-way aisles; same-zone routes use the
#     graph, cross-zone routes are the straight-line distance between the two
#     slots' recorded positions (always estimated) and are refused 422 when
#     either position is missing; an unreachable pair is 422 no-route

Feature: Travel graph and travel distance
  As a warehouse planner
  I want walking distances derived from the map
  So that routes are honest about direction, geometry and estimation

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1000 kg and 2 m3
    And a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    And a registered Aisle "A08" in Zone "WH1-STOR-AMB" with sequence hint 8
    And a registered one-way Aisle "A09" in Zone "WH1-STOR-AMB" with sequence hint 9
    And bays "01,02,03" of Aisle "A07" in Zone "WH1-STOR-AMB" each have a LocationSlot of type "PalletRack"
    And bays "01,02,03" of Aisle "A08" in Zone "WH1-STOR-AMB" each have a LocationSlot of type "PalletRack"
    And bays "01,02,03" of Aisle "A09" in Zone "WH1-STOR-AMB" each have a LocationSlot of type "PalletRack"

  @bdd
  Scenario: A zone without geometry has an estimated edge per adjacent bay pair
    When a GET request is sent to "/zones/WH1-STOR-AMB/travel-graph"
    Then the response status is 200
    And the response list "nodes" has 9 items
    And the response list "edges" has 10 items
    And the travel edge from aisle "A07" bay "01" to aisle "A07" bay "02" is 1.2 m and estimated
    And the travel edge from aisle "A09" bay "01" to aisle "A09" bay "02" is 1.2 m and estimated

  @bdd
  Scenario: A one-way aisle contributes edges in walk order only
    When a GET request is sent to "/zones/WH1-STOR-AMB/travel-graph"
    Then the response status is 200
    # A07 and A08 give 2 gaps x 2 directions each, A09 gives 2 gaps x 1 direction
    And the travel edge from aisle "A09" bay "01" to aisle "A09" bay "02" is 1.2 m and estimated
    And the travel edge from aisle "A09" bay "02" to aisle "A09" bay "03" is 1.2 m and estimated
    And the response list "edges" has 10 items

  @bdd
  Scenario: A zone pitch override sets the estimated edge length
    Given a registered Zone "STOR"/"RSV" in Site "WH1" with bay pitch 2.5 m and level pitch 1 m
    And a registered Aisle "B01" in Zone "WH1-STOR-RSV" with sequence hint 1
    And bays "01,02" of Aisle "B01" in Zone "WH1-STOR-RSV" each have a LocationSlot of type "PalletRack"
    When a GET request is sent to "/zones/WH1-STOR-RSV/travel-graph"
    Then the response status is 200
    And the response list "edges" has 2 items
    And the travel edge from aisle "B01" bay "01" to aisle "B01" bay "02" is 2.5 m and estimated

  @bdd
  Scenario: A centreline divides evenly across the aisle's bay gaps and is not estimated
    Given a PUT to "/zones/WH1-STOR-AMB/aisles/A07/geometry" with the JSON body {"centreline":{"start":{"xM":0,"yM":0,"zM":0},"end":{"xM":0,"yM":8,"zM":0}}} has been accepted
    When a GET request is sent to "/zones/WH1-STOR-AMB/travel-graph"
    Then the response status is 200
    And the travel edge from aisle "A07" bay "01" to aisle "A07" bay "02" is 4 m and measured
    And the travel edge from aisle "A07" bay "03" to aisle "A07" bay "02" is 4 m and measured
    And the travel edge from aisle "A08" bay "01" to aisle "A08" bay "02" is 1.2 m and estimated

  @bdd
  Scenario: A cross-aisle adds one estimated edge in each direction at its bay
    Given a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"02"} has been accepted
    When a GET request is sent to "/zones/WH1-STOR-AMB/travel-graph"
    Then the response status is 200
    And the response list "nodes" has 9 items
    And the response list "edges" has 12 items
    And the travel edge from aisle "A07" bay "02" to aisle "A08" bay "02" is 1.2 m and estimated
    And the travel edge from aisle "A08" bay "02" to aisle "A07" bay "02" is 1.2 m and estimated

  @bdd
  Scenario: A cross-aisle at a bay one of its aisles has no slot at adds no edge
    Given a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"09"} has been accepted
    When a GET request is sent to "/zones/WH1-STOR-AMB/travel-graph"
    Then the response status is 200
    And the response list "edges" has 10 items

  @bdd
  Scenario: The travel graph of an unknown zone is not found
    When a GET request is sent to "/zones/WH9-STOR-AMB/travel-graph"
    Then the response status is 404
    And the problem detail type is "zone-not-found"

  @bdd
  Scenario: The distance along one aisle is the sum of its bay gaps, estimated without geometry
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-AMB-A07-03-01-A"
    Then the response status is 200
    And the response field "metresM" is 2.4
    And the response field "estimated" is true
    And the response list "route" has 3 items
    And the response field "route.0.bay" is "01"
    And the response field "route.2.bay" is "03"

  @bdd
  Scenario: The distance along a measured aisle is exact and not estimated
    Given a PUT to "/zones/WH1-STOR-AMB/aisles/A07/geometry" with the JSON body {"centreline":{"start":{"xM":0,"yM":0,"zM":0},"end":{"xM":0,"yM":8,"zM":0}}} has been accepted
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-AMB-A07-03-01-A"
    Then the response status is 200
    And the response field "metresM" is 8
    And the response field "estimated" is false

  @bdd
  Scenario: A location is zero metres from itself
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-02-01-A&to=WH1-STOR-AMB-A07-02-01-A"
    Then the response status is 200
    And the response field "metresM" is 0
    And the response field "estimated" is false
    And the response list "route" has 1 item

  @bdd
  Scenario: Two aisles with no cross-aisle between them have no route
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-AMB-A08-01-01-A"
    Then the response status is 422
    And the problem detail type is "no-route"

  @bdd
  Scenario: A cross-aisle makes the other aisle reachable
    Given a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"01"} has been accepted
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-AMB-A08-03-01-A"
    Then the response status is 200
    # one cross-aisle hop (1.2 m) plus two bay gaps (2.4 m)
    And the response field "metresM" is 3.6
    And the response field "estimated" is true
    And the response list "route" has 4 items

  @bdd
  Scenario: The shortest of two routes is chosen
    Given a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"01"} has been accepted
    And a POST to "/zones/WH1-STOR-AMB/cross-aisles" with the JSON body {"fromAisle":"A07","toAisle":"A08","atBay":"03"} has been accepted
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-03-01-A&to=WH1-STOR-AMB-A08-03-01-A"
    Then the response status is 200
    # crossing at bay 03 directly (1.2 m) beats walking to bay 01 and back
    And the response field "metresM" is 1.2
    And the response list "route" has 2 items

  @bdd
  Scenario: A one-way aisle can be walked forward but not backward
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A09-01-01-A&to=WH1-STOR-AMB-A09-03-01-A"
    Then the response status is 200
    And the response field "metresM" is 2.4
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A09-03-01-A&to=WH1-STOR-AMB-A09-01-01-A"
    Then the response status is 422
    And the problem detail type is "no-route"

  @bdd
  Scenario: A distance to a location that does not exist is not found
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-AMB-A07-09-01-A"
    Then the response status is 404

  @bdd
  Scenario Outline: A distance query needs two well-formed location codes
    When a GET request is sent to "/distance<query>"
    Then the response status is 400

    Examples:
      | query                                                       |
      |                                                             |
      | ?from=WH1-STOR-AMB-A07-01-01-A                              |
      | ?to=WH1-STOR-AMB-A07-01-01-A                                |
      | ?from=WH1-STOR&to=WH1-STOR-AMB-A07-01-01-A                  |
      | ?from=WH1-STOR-AMB-A07-01-01-A&to=not-a-code                |

  @bdd
  Scenario: Locations in different zones need recorded positions to be measured
    Given a registered Zone "STOR"/"CHL" in Site "WH1" with temperature class "Chilled"
    And a registered Aisle "C01" in Zone "WH1-STOR-CHL" with sequence hint 1
    And bays "01" of Aisle "C01" in Zone "WH1-STOR-CHL" each have a LocationSlot of type "PalletRack"
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-CHL-C01-01-01-A"
    Then the response status is 422
    And the problem detail type is "no-route-between-zones"

  @bdd
  Scenario: Locations in different zones with positions are a straight-line estimate
    Given a registered Zone "STOR"/"CHL" in Site "WH1" with temperature class "Chilled"
    And a registered Aisle "C01" in Zone "WH1-STOR-CHL" with sequence hint 1
    And bays "01" of Aisle "C01" in Zone "WH1-STOR-CHL" each have a LocationSlot of type "PalletRack"
    And a PUT to "/locations/WH1-STOR-AMB-A07-01-01-A/geometry" with the JSON body {"position":{"xM":0,"yM":0,"zM":0},"dimensions":{"widthM":1,"depthM":1,"heightM":1}} has been accepted
    And a PUT to "/locations/WH1-STOR-CHL-C01-01-01-A/geometry" with the JSON body {"position":{"xM":3,"yM":4,"zM":0},"dimensions":{"widthM":1,"depthM":1,"heightM":1}} has been accepted
    When a GET request is sent to "/distance?from=WH1-STOR-AMB-A07-01-01-A&to=WH1-STOR-CHL-C01-01-01-A"
    Then the response status is 200
    And the response field "metresM" is 5
    And the response field "estimated" is true
    And the response list "route" has 2 items
