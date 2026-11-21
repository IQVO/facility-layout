package placement

import "errors"

// The well-known LocationTypes of the WCS tier (ADR-0048). Like PalletRack
// they are names, not an enum, but each carries a canonical role that
// NewLocationType enforces, so an AS/RS can never be registered as a dock.
const (
	// AutomatedStorage is one AS/RS as ONE logical location (WCS seam
	// decision 2): role Storage; its rack positions live in asrs-control.
	AutomatedStorage = "AutomatedStorage"
	// AsrsPort is an AS/RS input or output port, where containers wait
	// between the AS/RS and a robot: role Drop.
	AsrsPort = "AsrsPort"
	// HandoffPoint is where a robot hands a container to people or another
	// process (a dock lane, a forward area): role Drop.
	HandoffPoint = "HandoffPoint"
)

// ErrLocationTypeRoleMismatch is returned when a well-known LocationType name
// is registered with a role other than its canonical one.
var ErrLocationTypeRoleMismatch = errors.New("a well-known location type must carry its canonical role: AutomatedStorage is Storage, AsrsPort and HandoffPoint are Drop")

var canonicalRoles = map[string]LocationRole{
	AutomatedStorage: Storage,
	AsrsPort:         Drop,
	HandoffPoint:     Drop,
}

// CanonicalRole returns the role a well-known LocationType name must carry,
// and false for every other (free) name.
func CanonicalRole(name string) (LocationRole, bool) {
	role, ok := canonicalRoles[name]
	return role, ok
}
