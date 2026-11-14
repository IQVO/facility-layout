# Lifecycle, clock and operational rules, taken from the documentation
# (aggregate-design-canvas State Transitions: "Decommissioned" sites, zones
# and aisles, "UnderMaintenance ... reachable only by rehydrating a persisted
# row"; use-cases ChangeSiteTransferCapability / ChangeSiteRole "site active";
# troubleshooting "heartbeat is not publishing" and "Pod Not Ready (/readyz
# 503) while /healthz is 200" and the REST slug table).
#
# No v1 command sets Decommissioned on a site or zone, or UnderMaintenance on
# anything, so these states are seeded directly; what the API then does with
# them is what the documentation specifies.

Feature: Lifecycle states, heartbeat clock and readiness
  A parent that is no longer Active takes no new children and no commands;
  UnderMaintenance is a legal state that still reads, still takes geometry and
  can still be retired; the capability heartbeat and /readyz behave as the
  runbook says they do.

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1200 kg and 2.4 m3
    And a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7

  # -------------------------------------------------- not-Active parents ----

  @bdd
  Scenario Outline: A decommissioned site takes no new structure and no command
    Given the Site "WH1" has been decommissioned
    When a <method> request is sent to "<path>" with the JSON body <body>
    Then the response status is 409
    And the problem detail type is "site-not-active"

    Examples:
      | method | path                          | body                                                                       |
      | POST   | /sites/WH1/zones              | {"areaCode": "PACK", "zoneCode": "AMB", "temperatureClass": "Ambient"}     |
      | POST   | /locations                    | {"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack"} |
      | PUT    | /sites/WH1/transfer-capability | {"transferOriginEnabled": true, "transferDestinationEnabled": true}        |
      | PUT    | /sites/WH1/role               | {"siteRole": "MICRO_FC"}                                                   |

  @bdd
  Scenario: A decommissioned site is still readable, with its status and no capability change
    Given the Site "WH1" has been decommissioned
    When a GET request is sent to "/sites/WH1"
    Then the response status is 200
    And the response field "status" is "Decommissioned"
    When a GET request is sent to "/sites/WH1/transfer-capability"
    Then the response field "capabilityRevision" is 0
    And exactly 0 "SiteCapabilityChanged" domain events are published
    And exactly 0 "SiteRoleChanged" domain events are published

  @bdd
  Scenario Outline: A decommissioned zone takes no new aisle and no new slot
    Given the Zone "WH1-STOR-AMB" has been decommissioned
    When a POST request is sent to "<path>" with the JSON body <body>
    Then the response status is 409
    And the problem detail type is "zone-not-active"

    Examples:
      | path                        | body                                                                       |
      | /zones/WH1-STOR-AMB/aisles  | {"aisleCode": "A08", "sequenceHint": 8, "direction": "TwoWay"}             |
      | /locations                  | {"locationCode": "WH1-STOR-AMB-A07-03-02-B", "locationType": "PalletRack"} |

  @bdd
  Scenario: A site that has been decommissioned is no longer heartbeated
    Given a PUT to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": true} has been accepted
    And the Site "WH1" has been decommissioned
    When 7 minutes pass
    And the site capability heartbeat runs
    Then exactly 1 "SiteCapabilityChanged" domain events are published

  @bdd
  Scenario: With the heartbeat interval at zero nothing is re-published however long it has been
    Given a PUT to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": false} has been accepted
    And the site capability heartbeat is disabled
    When 30 minutes pass
    And the site capability heartbeat runs
    Then exactly 1 "SiteCapabilityChanged" domain events are published

  # ------------------------------------------------------ UnderMaintenance ----

  @bdd
  Scenario: A slot under maintenance still takes geometry and stays under maintenance
    Given a registered LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    And the LocationSlot "WH1-STOR-AMB-A07-03-02-B" is under maintenance
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-03-02-B/geometry" with the JSON body {"position": {"xM": 12.5, "yM": 7.25, "zM": 1.5}, "dimensions": {"widthM": 1.2, "depthM": 0.9, "heightM": 2.4}}
    Then the response status is 200
    And the response field "status" is "UnderMaintenance"
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-03-02-B"
    Then the response field "status" is "UnderMaintenance"
    And the response field "position.xM" is 12.5

  @bdd
  Scenario: A slot under maintenance can still be decommissioned
    Given a registered LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    And the LocationSlot "WH1-STOR-AMB-A07-03-02-B" is under maintenance
    When I decommission the LocationSlot "WH1-STOR-AMB-A07-03-02-B"
    Then the response status is 204
    And exactly 1 "LocationSlotDecommissioned" domain events are published
    When I request the LocationSlot "WH1-STOR-AMB-A07-03-02-B"
    Then the LocationSlot response reports type "PalletRack" with status "Decommissioned"

  @bdd
  Scenario: An aisle under maintenance takes a centreline but no new slot
    Given the Aisle "WH1-STOR-AMB-A07" is under maintenance
    When a PUT request is sent to "/zones/WH1-STOR-AMB/aisles/A07/geometry" with the JSON body {"centreline": {"start": {"xM": 0, "yM": 0, "zM": 0}, "end": {"xM": 0, "yM": 24, "zM": 0}}}
    Then the response status is 200
    And the response field "status" is "UnderMaintenance"
    When I register the LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    Then the response status is 409
    And the problem detail type is "aisle-not-active"

  # ------------------------------------------------------ site role input ----

  @bdd
  Scenario Outline: A site role is one of the exact UPPER_SNAKE roles of the published table
    When a <method> request is sent to "<path>" with the JSON body <body>
    Then the response status is 422
    And the problem detail type is "invalid-site-role"
    And exactly 1 "SiteRegistered" domain events are published
    And exactly 0 "SiteRoleChanged" domain events are published

    Examples:
      | method | path            | body                                                                                    |
      | POST   | /sites          | {"siteCode": "WH2", "name": "Second Centre", "siteRole": "Non-Sortable Fulfillment Center"} |
      | PUT    | /sites/WH1/role | {"siteRole": "Non-Sortable Fulfillment Center"}                                         |
      | PUT    | /sites/WH1/role | {"siteRole": "non_sortable_fc"}                                                         |
      | PUT    | /sites/WH1/role | {}                                                                                      |

  @bdd
  Scenario: The published transfer-default table lists the 15 roles and never UNSPECIFIED
    When a GET request is sent to "/site-roles/transfer-defaults"
    Then the response status is 200
    And the response list "items" has 15 items
    And the "siteRole" of the items in "items" are all different
    And no item in "items" has the "siteRole" "UNSPECIFIED"
    And an item in "items" has the "siteRole" "NON_SORTABLE_FC"

  # ----------------------------------------------------------- read slugs ----

  @bdd
  Scenario Outline: Reading something that is not on the map names what is missing
    When a GET request is sent to "<path>"
    Then the response status is 404
    And the problem detail type is "<slug>"

    Examples:
      | path                                    | slug                    |
      | /sites/WH9                              | site-not-found          |
      | /sites/WH9/transfer-capability          | site-not-found          |
      | /zones/WH9-STOR-AMB                     | zone-not-found          |
      | /zones/WH9-STOR-AMB/aisles              | zone-not-found          |
      | /locations/WH1-STOR-AMB-A07-09-09-Z     | location-slot-not-found |

  @bdd
  Scenario Outline: The site layout is rendered as json or svg and nothing else
    Given a registered LocationSlot "WH1-STOR-AMB-A07-03-02-B" of type "PalletRack"
    When a GET request is sent to "/sites/WH1/layout?format=<format>"
    Then the response status is 400
    And the problem detail type is "invalid-layout-format"

    Examples:
      | format |
      | pdf    |
      | JSON   |

  # ------------------------------------------------------------ readiness ----

  @bdd
  Scenario: A draining pod stays alive but stops being ready
    When a GET request is sent to "/readyz"
    Then the response status is 200
    And the response field "status" is "ready"
    When the service is draining
    And a GET request is sent to "/readyz"
    Then the response status is 503
    And the response field "status" is "not_ready"
    When a GET request is sent to "/healthz"
    Then the response status is 200

  @bdd
  Scenario: An unreachable dependency makes the pod not ready until it recovers by itself
    Given the dependency "kafka" is unreachable
    When a GET request is sent to "/readyz"
    Then the response status is 503
    And the problem detail type is "dependency-unavailable"
    And the problem detail names the rule "kafka"
    When a GET request is sent to "/healthz"
    Then the response status is 200
    When the dependency "kafka" is reachable again
    And a GET request is sent to "/readyz"
    Then the response status is 200
    And the response field "status" is "ready"
