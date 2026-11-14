# Placement-rule policy, taken from the documentation (business-context
# "PlacementRules: the enforcement point"; aggregate-design-canvas LocationSlot
# and PlacementRule; in-repo docs/docs/ddd/invariants.md "Placement-rule
# evaluation"): Deny always wins, any matching Allow turns the zone into an
# allow-list, a predicate is an AND of the fields it sets, and a rule never
# reaches back to a slot that already exists.

Feature: Placement-rule policy
  Placement legality is decided once, at registration, so that every Active
  slot a consumer reads is already legal. The rules are evaluated in a fixed
  order and the refusal names the rule that caused it.

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1200 kg and 2.4 m3
    And a LocationType "Shelf" with capacity 60 kg and 0.4 m3
    And a LocationType "ToteWall" with capacity 30 kg and 0.2 m3
    And a registered Site "WH1"
    And a registered Zone "STOR"/"FRZ" in Site "WH1" with temperature class "Frozen"
    And a registered Aisle "A02" in Zone "WH1-STOR-FRZ" with sequence hint 2

  @bdd
  Scenario: Deny always wins over Allow, whichever rule was defined first
    Given a PlacementRule "R-ALLOW-RACK" allowing "PalletRack" where zone code is "FRZ"
    And a PlacementRule "R-DENY-RACK" denying "PalletRack" where temperature class is "Frozen"
    And a PlacementRule "R-DENY-SHELF" denying "Shelf" where temperature class is "Frozen"
    And a PlacementRule "R-ALLOW-SHELF" allowing "Shelf" where zone code is "FRZ"
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-01-A" of type "PalletRack"
    Then the response status is 422
    And the problem detail type is "placement-rule-violated"
    And the problem detail names the rule "R-DENY-RACK"
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-02-A" of type "Shelf"
    Then the response status is 422
    And the problem detail names the rule "R-DENY-SHELF"
    When I request the layout of Site "WH1"
    Then the layout reports 1 zones, 1 aisles and 0 slots

  @bdd
  Scenario: Several Allow rules together form one allow-list
    Given a PlacementRule "R-ALLOW-RACK" allowing "PalletRack" where zone code is "FRZ"
    And a PlacementRule "R-ALLOW-SHELF" allowing "Shelf" where zone code is "FRZ"
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-01-A" of type "PalletRack"
    Then the response status is 201
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-02-A" of type "Shelf"
    Then the response status is 201
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-03-A" of type "ToteWall"
    Then the response status is 422
    And the problem detail type is "placement-rule-violated"
    And the problem detail names the rule "R-ALLOW-RACK"
    And the problem detail names the rule "R-ALLOW-SHELF"

  @bdd
  Scenario: A rule constrains only the location type it names
    Given a PlacementRule "R-DENY-SHELF" denying "Shelf" where temperature class is "Frozen"
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-01-A" of type "PalletRack"
    Then the response status is 201
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-02-A" of type "Shelf"
    Then the response status is 422

  @bdd
  Scenario Outline: A deny rule does not apply unless every field of its predicate matches the zone
    Given a POST to "/placement-rules" with the JSON body {"ruleId": "R-DENY-RACK", "locationType": "PalletRack", "effect": "Deny", "zone": <predicate>} has been accepted
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-01-A" of type "PalletRack"
    Then the response status is 201

    Examples:
      | predicate                                            | why the Frozen, non-hazmat zone WH1-STOR-FRZ is outside it |
      | {"zoneCode": "FRZ", "temperatureClass": "Ambient"}   | zone code matches, temperature class does not              |
      | {"zoneCode": "AMB", "temperatureClass": "Frozen"}    | temperature class matches, zone code does not              |
      | {"temperatureClass": "Frozen", "hazmat": true}       | the zone is not hazmat                                     |
      | {"hazmat": true}                                     | the zone is not hazmat                                     |

  @bdd
  Scenario Outline: A deny rule applies when every field its predicate sets matches the zone
    Given a POST to "/placement-rules" with the JSON body {"ruleId": "R-DENY-RACK", "locationType": "PalletRack", "effect": "Deny", "zone": <predicate>} has been accepted
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-01-A" of type "PalletRack"
    Then the response status is 422
    And the problem detail type is "placement-rule-violated"
    And the problem detail names the rule "R-DENY-RACK"

    Examples:
      | predicate                                            | what matches                                        |
      | {"zoneCode": "FRZ", "temperatureClass": "Frozen"}    | zone code and temperature class                     |
      | {"temperatureClass": "Frozen", "hazmat": false}      | an explicit hazmat=false is a constraint, not unset |
      | {"hazmat": false}                                    | hazmat=false alone is a real predicate              |
      | {"zoneCode": "FRZ"}                                  | zone code alone                                     |

  @bdd
  Scenario: A hazmat predicate matches the zone's hazmat flag whichever way it is set
    Given a registered Zone "STOR"/"HAZ" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "B01" in Zone "WH1-STOR-HAZ" with sequence hint 1
    And a POST to "/placement-rules" with the JSON body {"ruleId": "R-NO-TOTE-OFF-HAZMAT", "locationType": "ToteWall", "effect": "Deny", "zone": {"hazmat": false}} has been accepted
    And a POST to "/placement-rules" with the JSON body {"ruleId": "R-NO-SHELF-ON-HAZMAT", "locationType": "Shelf", "effect": "Deny", "zone": {"hazmat": true}} has been accepted
    When I register the LocationSlot "WH1-STOR-HAZ-B01-01-01-A" of type "ToteWall"
    Then the response status is 201
    When I register the LocationSlot "WH1-STOR-HAZ-B01-01-02-A" of type "Shelf"
    Then the response status is 422
    And the problem detail names the rule "R-NO-SHELF-ON-HAZMAT"
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-01-A" of type "ToteWall"
    Then the response status is 422
    And the problem detail names the rule "R-NO-TOTE-OFF-HAZMAT"
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-02-A" of type "Shelf"
    Then the response status is 201

  @bdd
  Scenario: A new rule never invalidates a slot that already exists
    Given a registered LocationSlot "WH1-STOR-FRZ-A02-01-01-A" of type "Shelf"
    When a PlacementRule "R-DENY-SHELF" denying "Shelf" where temperature class is "Frozen"
    Then the LocationSlot "WH1-STOR-FRZ-A02-01-01-A" exists
    When I request the LocationSlot "WH1-STOR-FRZ-A02-01-01-A"
    Then the LocationSlot response reports type "Shelf" with status "Active"
    When I register the LocationSlot "WH1-STOR-FRZ-A02-01-02-A" of type "Shelf"
    Then the response status is 422
    And the problem detail type is "placement-rule-violated"

  @bdd
  Scenario Outline: A rule definition is refused when it cannot be evaluated
    When a POST request is sent to "/placement-rules" with the JSON body <body>
    Then the response status is <status>
    And the problem detail type is "<slug>"
    And exactly 0 "PlacementRuleDefined" domain events are published

    Examples:
      | body                                                                                           | status | slug                      |
      | {"ruleId": "", "locationType": "Shelf", "effect": "Deny", "zone": {"zoneCode": "FRZ"}}         | 400    | empty-placement-rule-id   |
      | {"ruleId": "R-1", "locationType": "Shelf", "effect": "deny", "zone": {"zoneCode": "FRZ"}}      | 422    | unknown-placement-effect  |
      | {"ruleId": "R-1", "locationType": "Shelf", "effect": "", "zone": {"zoneCode": "FRZ"}}          | 422    | unknown-placement-effect  |
      | {"ruleId": "R-1", "locationType": "Shelf", "effect": "Deny", "zone": {"temperatureClass": "Cold"}} | 422 | unknown-temperature-class |

  @bdd
  Scenario: A duplicate rule id is refused and the first rule stands
    Given a PlacementRule "R-1" denying "Shelf" where temperature class is "Frozen"
    When a POST request is sent to "/placement-rules" with the JSON body {"ruleId": "R-1", "locationType": "PalletRack", "effect": "Allow", "zone": {"zoneCode": "AMB"}}
    Then the response status is 409
    And the problem detail type is "duplicate-placement-rule"
    When a GET request is sent to "/placement-rules/R-1"
    Then the response field "effect" is "Deny"
    And the response field "locationType" is "Shelf"
    And exactly 1 "PlacementRuleDefined" domain events are published

  @bdd
  Scenario: A rule states itself in words on the event and on the read
    When a POST request is sent to "/placement-rules" with the JSON body {"ruleId": "R-DENY-SHELF", "locationType": "Shelf", "effect": "Deny", "zone": {"zoneCode": "FRZ", "temperatureClass": "Frozen", "hazmat": false}}
    Then the response status is 201
    And the response has a Location header pointing at "/placement-rules/R-DENY-SHELF"
    And the last "PlacementRuleDefined" domain event has the field "predicate" equal to "zoneCode=FRZ,temperatureClass=Frozen,hazmat=false"
    When a GET request is sent to "/placement-rules/R-DENY-SHELF"
    Then the response field "description" is "R-DENY-SHELF: Deny Shelf where zoneCode=FRZ,temperatureClass=Frozen,hazmat=false"
