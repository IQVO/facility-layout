package slot

import (
	"errors"
	"sort"
	"strings"

	"github.com/claudioed/facility-layout/internal/domain/placement"
)

// ErrUnknownDockFlow is returned when a DockFlow string is not one of the
// known values.
var ErrUnknownDockFlow = errors.New("dock flow must be one of Inbound, Outbound, Both")

// ErrUnknownActivity is returned when an Activity string is not one of the
// known values.
var ErrUnknownActivity = errors.New("activity must be one of Pack, Sort, QC, VAS, Deconsolidate, Receive, Kit, Pick")

// ErrDockFlowRequired is returned when a Dock-role slot is registered with
// no DockFlow.
var ErrDockFlowRequired = errors.New("a Dock location requires a dockFlow")

// ErrWorkCenterActivitiesRequired is returned when a WorkCenter-role slot
// is registered with no activities.
var ErrWorkCenterActivitiesRequired = errors.New("a WorkCenter location requires at least one activity")

// ErrFunctionalAttributesNotAllowed is returned when a slot whose role is
// neither Dock nor WorkCenter is registered with a DockFlow or an Activity
// set — those attributes are meaningless outside their owning role.
var ErrFunctionalAttributesNotAllowed = errors.New("dockFlow and activities may only be set on a Dock or WorkCenter location")

// DockFlow is the direction of load a Dock-role location handles.
type DockFlow string

const (
	// Inbound is a receiving-only dock door.
	Inbound DockFlow = "Inbound"
	// Outbound is a shipping-only dock door.
	Outbound DockFlow = "Outbound"
	// Both handles both inbound and outbound loads.
	Both DockFlow = "Both"
)

// ParseDockFlow validates and converts the string form.
func ParseDockFlow(value string) (DockFlow, error) {
	switch DockFlow(value) {
	case Inbound, Outbound, Both:
		return DockFlow(value), nil
	default:
		return "", ErrUnknownDockFlow
	}
}

// Activity is one process a WorkCenter-role location performs (SAP EWM's
// Work Center: "deconsolidation, inspection, packing or value added
// service processing").
type Activity string

const (
	// Pack is cartonization/packing.
	Pack Activity = "Pack"
	// Sort is package/order sortation.
	Sort Activity = "Sort"
	// ActivityQC is inspection ("identification/pick point" in SAP terms).
	// Named ActivityQC, not QC, because QC is already placement.QC's
	// LocationRole constant; the role and the activity are independent
	// concepts that happen to share a word.
	ActivityQC Activity = "QC"
	// VAS is value-added-service processing (labeling, kitting add-ons).
	VAS Activity = "VAS"
	// Deconsolidate is breaking a mixed load into its constituent units.
	Deconsolidate Activity = "Deconsolidate"
	// Receive is inbound receiving processing.
	Receive Activity = "Receive"
	// Kit is kitting (assembling several SKUs into one sellable unit).
	Kit Activity = "Kit"
	// Pick is goods-to-person picking: a container is brought to the station
	// and the picker takes eaches out of it (ADR-0048).
	Pick Activity = "Pick"
)

// ParseActivity validates and converts the string form.
func ParseActivity(value string) (Activity, error) {
	switch Activity(value) {
	case Pack, Sort, ActivityQC, VAS, Deconsolidate, Receive, Kit, Pick:
		return Activity(value), nil
	default:
		return "", ErrUnknownActivity
	}
}

// FunctionalAttributes are the role-conditional facts a LocationSlot whose
// role is Dock or WorkCenter carries. Every other role's slot must have
// the zero FunctionalAttributes (ADR-0016).
type FunctionalAttributes struct {
	dockFlow   DockFlow
	activities []Activity
}

// NewFunctionalAttributes validates and constructs FunctionalAttributes for
// a slot with the given role. dockFlow is used (and required) only when
// role is Dock; activities is used (and required, non-empty, unique) only
// when role is WorkCenter. Passing either for any other role is rejected.
func NewFunctionalAttributes(role placement.LocationRole, dockFlow DockFlow, activities []Activity) (FunctionalAttributes, error) {
	switch role {
	case placement.Dock:
		if dockFlow == "" {
			return FunctionalAttributes{}, ErrDockFlowRequired
		}
		if _, err := ParseDockFlow(string(dockFlow)); err != nil {
			return FunctionalAttributes{}, err
		}
		if len(activities) != 0 {
			return FunctionalAttributes{}, ErrFunctionalAttributesNotAllowed
		}
		return FunctionalAttributes{dockFlow: dockFlow}, nil

	case placement.WorkCenter:
		if dockFlow != "" {
			return FunctionalAttributes{}, ErrFunctionalAttributesNotAllowed
		}
		if len(activities) == 0 {
			return FunctionalAttributes{}, ErrWorkCenterActivitiesRequired
		}
		deduped, err := dedupeActivities(activities)
		if err != nil {
			return FunctionalAttributes{}, err
		}
		return FunctionalAttributes{activities: deduped}, nil

	default:
		if dockFlow != "" || len(activities) != 0 {
			return FunctionalAttributes{}, ErrFunctionalAttributesNotAllowed
		}
		return FunctionalAttributes{}, nil
	}
}

// dedupeActivities validates every activity and rejects a duplicate,
// returning them sorted so equal activity sets always compare and print
// identically regardless of caller order.
func dedupeActivities(activities []Activity) ([]Activity, error) {
	seen := make(map[Activity]bool, len(activities))
	out := make([]Activity, 0, len(activities))
	for _, a := range activities {
		valid, err := ParseActivity(string(a))
		if err != nil {
			return nil, err
		}
		if seen[valid] {
			continue
		}
		seen[valid] = true
		out = append(out, valid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// DockFlow returns the slot's dock flow, or "" when the slot's role is not
// Dock.
func (a FunctionalAttributes) DockFlow() DockFlow { return a.dockFlow }

// Activities returns the slot's activity set, or nil when the slot's role
// is not WorkCenter.
func (a FunctionalAttributes) Activities() []Activity {
	if len(a.activities) == 0 {
		return nil
	}
	out := make([]Activity, len(a.activities))
	copy(out, a.activities)
	return out
}

// IsZero reports whether this is the zero FunctionalAttributes — no dock
// flow and no activities, the shape every non-Dock, non-WorkCenter slot
// must have.
func (a FunctionalAttributes) IsZero() bool {
	return a.dockFlow == "" && len(a.activities) == 0
}

// String renders the attributes for event payloads and logs.
func (a FunctionalAttributes) String() string {
	if a.dockFlow != "" {
		return "dockFlow=" + string(a.dockFlow)
	}
	if len(a.activities) == 0 {
		return ""
	}
	names := make([]string, 0, len(a.activities))
	for _, act := range a.activities {
		names = append(names, string(act))
	}
	return "activities=" + strings.Join(names, ",")
}
