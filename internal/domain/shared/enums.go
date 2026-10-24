package shared

// TemperatureClass is the thermal behaviour of a Zone. It is not cosmetic:
// PlacementRules are keyed by it, and it is what stops ambient product
// being slotted into the frozen zone.
type TemperatureClass string

const (
	// Ambient is room temperature storage.
	Ambient TemperatureClass = "Ambient"
	// Chilled is refrigerated storage.
	Chilled TemperatureClass = "Chilled"
	// Frozen is sub-zero storage.
	Frozen TemperatureClass = "Frozen"
)

// ParseTemperatureClass validates and converts the string form.
func ParseTemperatureClass(value string) (TemperatureClass, error) {
	switch TemperatureClass(value) {
	case Ambient, Chilled, Frozen:
		return TemperatureClass(value), nil
	default:
		return "", ErrUnknownTemperatureClass
	}
}

// Direction is the traffic direction of an Aisle, an input to WES travel-path
// planning.
type Direction string

const (
	// OneWay means the aisle may only be traversed in its sequence direction.
	OneWay Direction = "OneWay"
	// TwoWay means the aisle may be traversed in either direction.
	TwoWay Direction = "TwoWay"
)

// ParseDirection validates and converts the string form.
func ParseDirection(value string) (Direction, error) {
	switch Direction(value) {
	case OneWay, TwoWay:
		return Direction(value), nil
	default:
		return "", ErrUnknownDirection
	}
}

// Status is the lifecycle state shared by every structural aggregate in
// this context (Site, Zone, Aisle, LocationSlot).
type Status string

const (
	// Active means the structure exists and is legal for storage/traversal.
	Active Status = "Active"
	// UnderMaintenance means the structure exists but is temporarily out of
	// service. v1 exposes no use case that sets it: it is a legal persisted
	// state (e.g. loaded from an external facility-management system) that
	// the read models render, and a slot in it can still be decommissioned.
	UnderMaintenance Status = "UnderMaintenance"
	// Decommissioned means the structure is permanently retired. This is a
	// one-way transition in v1: a decommissioned structure is never
	// reactivated, and re-registering its code is rejected.
	Decommissioned Status = "Decommissioned"
)

// ParseStatus validates and converts the string form. Used by the
// persistence adapters when rehydrating an aggregate.
func ParseStatus(value string) (Status, error) {
	switch Status(value) {
	case Active, UnderMaintenance, Decommissioned:
		return Status(value), nil
	default:
		return "", ErrUnknownStatus
	}
}

// HandlingUnit is the unit of handling a storage slot (a "bin" downstream)
// accepts: a single each, a whole case, or a whole pallet. It is OPTIONAL on
// a LocationSlot — the zero value ("") means no handling unit was ever
// recorded, which is every slot registered before ADR-0040 and keeps
// today's behaviour. Downstream inventory-storage refuses to stow a sealed
// case into a bin that is not CASE or PALLET.
type HandlingUnit string

const (
	// HandlingUnitEach is a bin that takes loose eaches.
	HandlingUnitEach HandlingUnit = "EACH"
	// HandlingUnitCase is a bin that takes whole cases.
	HandlingUnitCase HandlingUnit = "CASE"
	// HandlingUnitPallet is a bin that takes whole pallets.
	HandlingUnitPallet HandlingUnit = "PALLET"
)

// ParseHandlingUnit validates and converts the string form. The empty string
// is NOT valid here: "absent" is represented by not calling the parser.
func ParseHandlingUnit(value string) (HandlingUnit, error) {
	switch HandlingUnit(value) {
	case HandlingUnitEach, HandlingUnitCase, HandlingUnitPallet:
		return HandlingUnit(value), nil
	default:
		return "", ErrUnknownHandlingUnit
	}
}
