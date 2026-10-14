package site

import (
	"errors"
	"fmt"
)

// ErrInvalidSiteRole is returned when a site role is not one of the known
// roles, or when a command tries to assign UNSPECIFIED (ADR-0037).
var ErrInvalidSiteRole = errors.New("site role must be one of the known site roles")

// Role is the site's role in the fulfilment network (ADR-0037): what kind of
// node the building is. It is master data an operator states explicitly;
// nothing derives it from the display name at run time. The values are
// stable UPPER_SNAKE strings: they are the REST, MCP and event wire values.
type Role string

// The known site roles. RoleUnspecified is the role of a site nobody has
// classified yet (the registration default, and the backfill result for a
// name that matched no known pattern); it cannot be assigned by command.
const (
	RoleUnspecified              Role = "UNSPECIFIED"
	RoleRoboticsSortableFC       Role = "ROBOTICS_SORTABLE_FC"
	RoleNonSortableFC            Role = "NON_SORTABLE_FC"
	RoleTraditionalSortableFC    Role = "TRADITIONAL_SORTABLE_FC"
	RoleSubSameDayFC             Role = "SUB_SAME_DAY_FC"
	RoleFreshGroceryFC           Role = "FRESH_GROCERY_FC"
	RoleXLFC                     Role = "XL_FC"
	RoleSoftLinesSortableFC      Role = "SOFT_LINES_SORTABLE_FC"
	RoleMicroFC                  Role = "MICRO_FC"
	RoleSimulatedFC              Role = "SIMULATED_FC"
	RoleInboundCrossDock         Role = "INBOUND_CROSS_DOCK"
	RoleNationalInboundCrossDock Role = "NATIONAL_INBOUND_CROSS_DOCK"
	RoleReturnsCenter            Role = "RETURNS_CENTER"
	RoleSortationCenter          Role = "SORTATION_CENTER"
	RoleDeliveryStation          Role = "DELIVERY_STATION"
	RoleAirHubGateway            Role = "AIR_HUB_GATEWAY"
)

// RoleTransferDefault is one row of the role -> default transfer capability
// table (ADR-0037, final-gaps decision E2).
type RoleTransferDefault struct {
	Role       Role
	Capability TransferCapability
}

// roleTable is the single source of truth for the known roles (in display
// order) and the transfer capability the ecosystem needs for each:
//
//   - origin AND destination: every FC that stores ambient inventory, plus
//     the Simulated FC;
//   - destination only: Micro-Fulfillment Centers (replenished, never a
//     source);
//   - origin only: (National) Inbound Cross-Docks and Returns Centers (they
//     push stock into FCs);
//   - neither: Fresh Grocery FCs (perishable, no rebalancing), Sortation
//     Centers, Delivery Stations and Air Hub Gateways (no storage).
//
// It is a DEFAULT, never applied by a role change: capability stays the
// explicit ADR-0035 command.
var roleTable = []RoleTransferDefault{
	{RoleRoboticsSortableFC, NewTransferCapability(true, true)},
	{RoleNonSortableFC, NewTransferCapability(true, true)},
	{RoleTraditionalSortableFC, NewTransferCapability(true, true)},
	{RoleSubSameDayFC, NewTransferCapability(true, true)},
	{RoleFreshGroceryFC, NewTransferCapability(false, false)},
	{RoleXLFC, NewTransferCapability(true, true)},
	{RoleSoftLinesSortableFC, NewTransferCapability(true, true)},
	{RoleMicroFC, NewTransferCapability(false, true)},
	{RoleSimulatedFC, NewTransferCapability(true, true)},
	{RoleInboundCrossDock, NewTransferCapability(true, false)},
	{RoleNationalInboundCrossDock, NewTransferCapability(true, false)},
	{RoleReturnsCenter, NewTransferCapability(true, false)},
	{RoleSortationCenter, NewTransferCapability(false, false)},
	{RoleDeliveryStation, NewTransferCapability(false, false)},
	{RoleAirHubGateway, NewTransferCapability(false, false)},
}

// TransferDefaults returns a copy of the role -> default transfer capability
// table, one row per assignable role, in display order. UNSPECIFIED has no
// row: an unclassified site has no default and its capability is left as is.
func TransferDefaults() []RoleTransferDefault {
	out := make([]RoleTransferDefault, len(roleTable))
	copy(out, roleTable)
	return out
}

// Roles returns every known role value: UNSPECIFIED first, then the
// assignable roles in display order.
func Roles() []Role {
	out := make([]Role, 0, len(roleTable)+1)
	out = append(out, RoleUnspecified)
	for _, d := range roleTable {
		out = append(out, d.Role)
	}
	return out
}

// DefaultTransferCapability returns the role's default transfer capability
// and true, or the zero capability and false for UNSPECIFIED or an unknown
// role.
func (r Role) DefaultTransferCapability() (TransferCapability, bool) {
	for _, d := range roleTable {
		if d.Role == r {
			return d.Capability, true
		}
	}
	return TransferCapability{}, false
}

// assignable reports whether r may be set on a site by command: one of the
// table's roles, never UNSPECIFIED.
func (r Role) assignable() bool {
	_, ok := r.DefaultTransferCapability()
	return ok
}

// known reports whether r is UNSPECIFIED or an assignable role.
func (r Role) known() bool {
	if r == RoleUnspecified {
		return true
	}
	return r.assignable()
}

// ParseRole maps a wire value to a Role. Matching is exact (case and
// spelling): anything that is not UNSPECIFIED or a table role is
// ErrInvalidSiteRole.
func ParseRole(raw string) (Role, error) {
	r := Role(raw)
	if !r.known() {
		return "", fmt.Errorf("%w: %q", ErrInvalidSiteRole, raw)
	}
	return r, nil
}
