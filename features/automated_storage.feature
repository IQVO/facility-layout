Feature: One AS/RS is one logical location (ADR 0048)

  Background:
    Given a site "WH1" with zone "AUTO" "ASRS" and aisles "ASRS1", "ASRS2"
    And the location type "AutomatedStorage" with role "Storage"
    And the location type "AsrsPort" with role "Drop"

  Scenario: An AS/RS, its ports, and nothing more in its aisle
    When I register "WH1-AUTO-ASRS-ASRS1-00-00-S" as "AutomatedStorage"
    And I register "WH1-AUTO-ASRS-ASRS1-01-00-IN1" as "AsrsPort"
    And I register "WH1-AUTO-ASRS-ASRS1-01-00-OUT1" as "AsrsPort"
    Then listing role "Storage" for site "WH1" returns exactly "WH1-AUTO-ASRS-ASRS1-00-00-S"
    And listing role "Drop" for site "WH1" returns exactly "WH1-AUTO-ASRS-ASRS1-01-00-IN1, WH1-AUTO-ASRS-ASRS1-01-00-OUT1"
    When I register "WH1-AUTO-ASRS-ASRS1-00-00-T" as "AutomatedStorage"
    Then the registration is refused with 409 "automated-storage-already-in-aisle"

  Scenario: A port needs its AS/RS
    When I register "WH1-AUTO-ASRS-ASRS2-01-00-IN1" as "AsrsPort"
    Then the registration is refused with 422 "asrs-port-without-automated-storage"

  Scenario: A well-known type keeps its role
    When I register the location type "HandoffPoint" with role "Storage"
    Then the registration is refused with 422 "location-type-role-mismatch"
