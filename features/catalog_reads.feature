# Catalog reads — the list endpoints and the denormalized classification
# read.
#
# Every scenario in this file is derived from the project documentation:
#   - apis/openapi.yaml:
#       listSites ("ordered by site code"),
#       listZones ("ordered by zone id"; "Answers 404 if the site itself is
#         unknown, rather than an empty list, so a caller can tell 'no zones
#         yet' apart from 'no such site'"),
#       listAisles ("ordered by SequenceHint then aisle code — that is, in
#         the order an associate would walk them"),
#       listLocationTypes ("ordered by name ... the vocabulary a rule
#         author picks from"),
#       listPlacementRules ("ordered by rule id ... the full rule set a
#         slot registration is evaluated against"),
#       getLocationClassification ("Resolves a LocationSlot's LocationCode
#         to its parent Zone ... and returns that Zone's hazmat and
#         temperatureClass attributes").
#   - docs/docs/api-reference/endpoints.md, Aisles section: "GET
#     /zones/{zoneId}/aisles returns aisles ordered by sequenceHint, not by
#     registration order. That ordering *is* the walk order."
#   - docs/docs/adr/0008-location-classification-read-endpoint.md: the
#     one-call {hazmat, temperatureClass} read for cross-context
#     placement validation.

Feature: Catalog reads
  The list endpoints are the vocabulary an operator, a rule author, or a
  frontend drills in by: every site, a site's zones, a zone's aisles in
  walk order, the location types, the placement rules. Each comes back in
  a documented deterministic order, and the classification read resolves a
  coded slot to its zone's hazmat/temperature attributes in one cheap call.

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1200 kg and 2.4 m3
    And a LocationType "Shelf" with capacity 60 kg and 0.4 m3

  # apis/openapi.yaml listSites: "Returns every Site on the warehouse map,
  # ordered by site code."
  @bdd
  Scenario: Listing sites returns them ordered by site code
    Given a registered Site "WH2"
    And a registered Site "WH1"
    When I list the sites
    Then the response status is 200
    And the listed sites are "WH1,WH2"

  # apis/openapi.yaml listZones: "Returns every Zone in the given Site,
  # ordered by zone id."
  @bdd
  Scenario: Listing a site's zones returns them ordered by zone id
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"FRZ" in Site "WH1" with temperature class "Frozen"
    And a registered Zone "RCV"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    When I list the zones of Site "WH1"
    Then the response status is 200
    And the listed zones are "WH1-RCV-AMB,WH1-STOR-AMB,WH1-STOR-FRZ"

  # apis/openapi.yaml listZones: "Answers 404 if the site itself is
  # unknown, rather than an empty list, so a caller can tell 'no zones
  # yet' apart from 'no such site'."
  @bdd
  Scenario: Listing an unknown site's zones is a 404, not an empty list
    When I list the zones of Site "WH9"
    Then the response status is 404
    And the problem detail type is "site-not-found"

  # apis/openapi.yaml listAisles + docs/docs/api-reference/endpoints.md
  # (Aisles): walk order is sequenceHint then aisle code — never
  # registration order.
  @bdd
  Scenario: Listing a zone's aisles returns them in walk order, not registration order
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A09" in Zone "WH1-STOR-AMB" with sequence hint 9
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    And a registered Aisle "A08" in Zone "WH1-STOR-AMB" with sequence hint 7
    When I list the aisles of Zone "WH1-STOR-AMB"
    Then the response status is 200
    And the listed aisles are "A07,A08,A09"

  # apis/openapi.yaml listLocationTypes: "Returns every LocationType,
  # ordered by name, with its default capacity envelope."
  @bdd
  Scenario: Listing location types returns them ordered by name
    Given a LocationType "Amnesty" with capacity 30 kg and 0.2 m3
    When I list the location types
    Then the response status is 200
    And the listed location types are "Amnesty,PalletRack,Shelf"

  # apis/openapi.yaml listPlacementRules: "Returns every PlacementRule,
  # ordered by rule id ... This is the full rule set a slot registration is
  # evaluated against."
  @bdd
  Scenario: Listing placement rules returns them ordered by rule id
    Given a registered Site "WH1"
    And a PlacementRule "RULE-ZZZ-NO-SHELF" denying "Shelf" where temperature class is "Frozen"
    And a PlacementRule "RULE-AAA-ALLOW-RACK" allowing "PalletRack" where zone code is "HAZ"
    When I list the placement rules
    Then the response status is 200
    And the listed placement rules are "RULE-AAA-ALLOW-RACK,RULE-ZZZ-NO-SHELF"

  # apis/openapi.yaml getLocationClassification +
  # docs/docs/adr/0008-location-classification-read-endpoint.md: resolves
  # the code to its parent Zone and returns exactly {hazmat,
  # temperatureClass} — nothing else about the slot or the zone.
  @bdd
  Scenario: A slot's classification resolves its zone's hazmat flag and temperature class
    Given a registered Site "WH1"
    And a registered Zone "STOR"/"HAZ" in Site "WH1" with temperature class "Frozen"
    And a registered Aisle "A01" in Zone "WH1-STOR-HAZ" with sequence hint 1
    And a registered LocationSlot "WH1-STOR-HAZ-A01-01-01-A" of type "PalletRack"
    When I request the classification of LocationSlot "WH1-STOR-HAZ-A01-01-01-A"
    Then the response status is 200
    And the classification reports hazmat true and temperature class "Frozen"
