Feature: Site transfer capability
  A site's transfer capability says whether it may donate stock to another
  site (origin) and whether it may receive stock from one (destination).
  facility-layout owns that site master data (ADR-0035): every real change
  is published as SiteCapabilityChanged, a full snapshot carrying a per-site
  revision that only ever goes up, so network-inventory-planning keeps the
  newest state no matter how Kafka redelivers.

  Background:
    Given an empty warehouse map
    And a registered Site "WH1"

  @bdd
  Scenario: A registered site is capable of nothing until an operator says otherwise
    When a GET request is sent to "/sites/WH1/transfer-capability"
    Then the response status is 200
    And the response field "transferOriginEnabled" is false
    And the response field "transferDestinationEnabled" is false
    And the response field "capabilityRevision" is 0
    And exactly 0 "SiteCapabilityChanged" domain events are published

  @bdd
  Scenario: Enabling a site as a transfer origin publishes revision 1
    When a PUT request is sent to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": false}
    Then the response status is 200
    And the response field "siteCode" is "WH1"
    And the response field "transferOriginEnabled" is true
    And the response field "capabilityRevision" is 1
    And exactly 1 "SiteCapabilityChanged" domain event is published
    And the last SiteCapabilityChanged for Site "WH1" is origin true, destination false at revision 1

  @bdd
  Scenario: Repeating the same snapshot changes nothing and publishes nothing
    Given a PUT to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": true} has been accepted
    When a PUT request is sent to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": true}
    Then the response status is 200
    And the response field "capabilityRevision" is 1
    And exactly 1 "SiteCapabilityChanged" domain event is published

  @bdd
  Scenario: Every real change advances the revision by exactly one
    Given a PUT to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": true} has been accepted
    When a PUT request is sent to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": false, "transferDestinationEnabled": true}
    Then the response status is 200
    And the response field "capabilityRevision" is 2
    And exactly 2 "SiteCapabilityChanged" domain events are published
    And the last SiteCapabilityChanged for Site "WH1" is origin false, destination true at revision 2

  @bdd
  Scenario: The capability of an unknown site cannot be changed
    When a PUT request is sent to "/sites/WH9/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": true}
    Then the response status is 404
    And the problem detail type is "site-not-found"
    And exactly 0 "SiteCapabilityChanged" domain events are published

  @bdd
  Scenario: A partial snapshot is rejected instead of silently switching a capability off
    When a PUT request is sent to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true}
    Then the response status is 400
    And the problem detail type is "missing-transfer-capability-field"
    And exactly 0 "SiteCapabilityChanged" domain events are published
