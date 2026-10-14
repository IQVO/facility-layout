Feature: Site role
  A site's role says what kind of node it is in the fulfilment network: a
  Non-Sortable Fulfillment Center, a Delivery Station, an Inbound Cross-Dock.
  facility-layout owns it as explicit site master data (ADR-0037): it is set
  at registration (UNSPECIFIED when nobody states it), changed by command,
  and every real change is published as SiteRoleChanged. A role never flips
  the site's transfer capability: that stays the explicit ADR-0035 command,
  and the role -> default capability table is published read-only so a seed
  applies it once.

  Background:
    Given an empty warehouse map

  @bdd
  Scenario: A site registered without a role is UNSPECIFIED
    When a POST request is sent to "/sites" with the JSON body {"siteCode": "WH1", "name": "Fulfilment Centre One"}
    Then the response status is 201
    And the response field "siteRole" is "UNSPECIFIED"
    And the last SiteRegistered for Site "WH1" carries role "UNSPECIFIED"

  @bdd
  Scenario: A site registered with a role carries it on SiteRegistered
    When a POST request is sent to "/sites" with the JSON body {"siteCode": "ABE8", "name": "ABE8 - Non-Sortable Fulfillment Center (Breinigsville, PA)", "siteRole": "NON_SORTABLE_FC"}
    Then the response status is 201
    And the response field "siteRole" is "NON_SORTABLE_FC"
    And the last SiteRegistered for Site "ABE8" carries role "NON_SORTABLE_FC"

  @bdd
  Scenario: Changing the role publishes SiteRoleChanged and leaves the capability alone
    Given a registered Site "WH1"
    When a PUT request is sent to "/sites/WH1/role" with the JSON body {"siteRole": "ROBOTICS_SORTABLE_FC"}
    Then the response status is 200
    And the response field "siteRole" is "ROBOTICS_SORTABLE_FC"
    And exactly 1 "SiteRoleChanged" domain event is published
    And the last SiteRoleChanged for Site "WH1" is "ROBOTICS_SORTABLE_FC" replacing "UNSPECIFIED"
    And exactly 0 "SiteCapabilityChanged" domain events are published

  @bdd
  Scenario: Repeating the current role changes nothing and publishes nothing
    Given a registered Site "WH1"
    And a PUT to "/sites/WH1/role" with the JSON body {"siteRole": "MICRO_FC"} has been accepted
    When a PUT request is sent to "/sites/WH1/role" with the JSON body {"siteRole": "MICRO_FC"}
    Then the response status is 200
    And exactly 1 "SiteRoleChanged" domain event is published

  @bdd
  Scenario: UNSPECIFIED cannot be assigned by command
    Given a registered Site "WH1"
    When a PUT request is sent to "/sites/WH1/role" with the JSON body {"siteRole": "UNSPECIFIED"}
    Then the response status is 422
    And the problem detail type is "invalid-site-role"
    And exactly 0 "SiteRoleChanged" domain events are published

  @bdd
  Scenario: The transfer defaults table is published read-only
    When a GET request is sent to "/site-roles/transfer-defaults"
    Then the response status is 200
    And the transfer default for "MICRO_FC" is origin false, destination true
    And the transfer default for "INBOUND_CROSS_DOCK" is origin true, destination false
    And the transfer default for "FRESH_GROCERY_FC" is origin false, destination false
    And the transfer default for "SIMULATED_FC" is origin true, destination true
