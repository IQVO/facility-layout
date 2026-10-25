# Declaring the handling unit a slot accepts.
#
# Derived from:
#   - apis/openapi.yaml POST /locations: optional handlingUnit (EACH|CASE|PALLET)
#   - apis/openapi.yaml PUT /locations/{locationCode}/handling-unit: sets or
#     changes it on an existing slot; the slot must exist (404), must not be
#     Decommissioned (409) and the value must be EACH, CASE or PALLET (422);
#     publishes LocationSlotHandlingUnitDeclared
#   - docs/docs/adr/0040-slot-handling-unit.md: absent = none recorded =
#     behaviour before the field existed (the field is omitted everywhere)

Feature: Declaring a slot's handling unit
  As a warehouse mapper
  I want to say whether a bin takes eaches, cases or pallets
  So that inventory-storage can stow a sealed case only where one fits

  Background:
    Given an empty warehouse map
    And a LocationType "PalletRack" with capacity 1000 kg and 2 m3
    And a registered Site "WH1"
    And a registered Zone "STOR"/"AMB" in Site "WH1" with temperature class "Ambient"
    And a registered Aisle "A07" in Zone "WH1-STOR-AMB" with sequence hint 7
    And bays "01" of Aisle "A07" in Zone "WH1-STOR-AMB" each have a LocationSlot of type "PalletRack"

  @bdd
  Scenario: A slot registered without a handling unit reports none
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A"
    Then the response status is 200
    And the response has no field "handlingUnit"

  @bdd
  Scenario: A handling unit can be given at registration
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-STOR-AMB-A07-02-01-A","locationType":"PalletRack","handlingUnit":"PALLET"}
    Then the response status is 201
    And the response field "handlingUnit" is "PALLET"

  @bdd
  Scenario: An unknown handling unit at registration is refused
    When a POST request is sent to "/locations" with the JSON body {"locationCode":"WH1-STOR-AMB-A07-02-01-A","locationType":"PalletRack","handlingUnit":"TRUCK"}
    Then the response status is 422
    And the problem detail type is "unknown-handling-unit"

  @bdd
  Scenario: Declaring a handling unit echoes it and announces it
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/handling-unit" with the JSON body {"handlingUnit":"CASE"}
    Then the response status is 200
    And the response field "locationCode" is "WH1-STOR-AMB-A07-01-01-A"
    And the response field "handlingUnit" is "CASE"
    And exactly 1 "LocationSlotHandlingUnitDeclared" domain event is published

  @bdd
  Scenario: The declared handling unit is visible when the slot is read back
    Given a PUT to "/locations/WH1-STOR-AMB-A07-01-01-A/handling-unit" with the JSON body {"handlingUnit":"EACH"} has been accepted
    When a GET request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A"
    Then the response status is 200
    And the response field "handlingUnit" is "EACH"

  @bdd
  Scenario: The handling unit can be changed and each change is announced
    Given a PUT to "/locations/WH1-STOR-AMB-A07-01-01-A/handling-unit" with the JSON body {"handlingUnit":"EACH"} has been accepted
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/handling-unit" with the JSON body {"handlingUnit":"PALLET"}
    Then the response status is 200
    And the response field "handlingUnit" is "PALLET"
    And exactly 2 "LocationSlotHandlingUnitDeclared" domain events are published

  @bdd
  Scenario Outline: Only EACH, CASE and PALLET are handling units
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/handling-unit" with the JSON body <body>
    Then the response status is 422
    And the problem detail type is "unknown-handling-unit"
    And exactly 0 "LocationSlotHandlingUnitDeclared" domain events are published

    Examples:
      | body                      |
      | {"handlingUnit":"TRUCK"}  |
      | {"handlingUnit":"case"}   |
      | {"handlingUnit":""}       |
      | {}                        |

  @bdd
  Scenario: A handling unit for a slot that does not exist is not found
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-09-01-A/handling-unit" with the JSON body {"handlingUnit":"CASE"}
    Then the response status is 404
    And exactly 0 "LocationSlotHandlingUnitDeclared" domain events are published

  @bdd
  Scenario: A decommissioned slot refuses a new handling unit
    Given I decommission the LocationSlot "WH1-STOR-AMB-A07-01-01-A"
    When a PUT request is sent to "/locations/WH1-STOR-AMB-A07-01-01-A/handling-unit" with the JSON body {"handlingUnit":"CASE"}
    Then the response status is 409
    And the problem detail type is "already-decommissioned"
    And exactly 0 "LocationSlotHandlingUnitDeclared" domain events are published
