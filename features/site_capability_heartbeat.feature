Feature: Site capability heartbeat
  SiteCapabilityChanged is raised only when a capability CHANGES (ADR-0035),
  yet network-inventory-planning treats a capability fact older than its
  freshness window as stale. So every 5 minutes (ADR-0041) the service
  re-publishes the CURRENT capability of every site that has declared one,
  with a fresh time and the revision advanced by one, through the outbox.
  A PUT of the value a site already holds stays a no-op.

  Background:
    Given an empty warehouse map
    And a registered Site "WH1"

  @bdd
  Scenario: A site that never declared a capability is not heartbeated
    When the site capability heartbeat runs
    Then exactly 0 "SiteCapabilityChanged" domain events are published

  @bdd
  Scenario: A declared capability is re-published with a fresh time and the next revision
    Given a PUT to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": false} has been accepted
    When 7 minutes pass
    And the site capability heartbeat runs
    Then exactly 2 "SiteCapabilityChanged" domain events are published
    And the last SiteCapabilityChanged for Site "WH1" is origin true, destination false at revision 2
    And the last SiteCapabilityChanged carries a time 7 minutes after the start

  @bdd
  Scenario: The heartbeat is not repeated before an interval has passed
    Given a PUT to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": true} has been accepted
    When the site capability heartbeat runs
    And 4 minutes pass
    And the site capability heartbeat runs
    Then exactly 2 "SiteCapabilityChanged" domain events are published
    When 1 minute passes
    And the site capability heartbeat runs
    Then exactly 3 "SiteCapabilityChanged" domain events are published
    And the last SiteCapabilityChanged for Site "WH1" is origin true, destination true at revision 3

  @bdd
  Scenario: A heartbeat does not change what a repeated PUT does
    Given a PUT to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": true} has been accepted
    And the site capability heartbeat runs
    When a PUT request is sent to "/sites/WH1/transfer-capability" with the JSON body {"transferOriginEnabled": true, "transferDestinationEnabled": true}
    Then the response status is 200
    And the response field "capabilityRevision" is 2
    And exactly 2 "SiteCapabilityChanged" domain events are published
