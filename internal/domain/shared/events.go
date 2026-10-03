package shared

import "time"

// eventTypePrefix is this bounded context's CloudEvents `type` namespace.
// The Kafka adapters put EventType() verbatim into the CloudEvents 1.0
// `type` attribute (ADR-0024), so these strings are the wire contract.
// The convention is identical to the other four warehouse-systems services:
//
//	com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>
//
// reverse-DNS, lowercase except the final PascalCase event name, and the
// entity segment carries no hyphen even for multi-word aggregate names.
// This service's subdomain segment is `wms`: bin-accurate location is
// WMS-tier in the domain reference, and this service is the generalized,
// multi-consumer version of that concern.
const eventTypePrefix = "com.warehouse.wms.facility-layout."

// DomainEvent is a past-tense fact published by an aggregate. Adapters
// (outbound/kafka as CloudEvents 1.0, outbound/events for log/test)
// serialize and publish these; the domain never depends
// on the publishing mechanism. EventType is this context's Published
// Language: downstream Conformists key off it.
type DomainEvent interface {
	EventName() string
	EventType() string
	OccurredAt() time.Time
}

type base struct {
	Name string    `json:"eventName"`
	Type string    `json:"eventType"`
	At   time.Time `json:"occurredAt"`
}

func (b base) EventName() string     { return b.Name }
func (b base) EventType() string     { return b.Type }
func (b base) OccurredAt() time.Time { return b.At }

func newBase(entity, name string, occurredAt time.Time) base {
	return base{Name: name, Type: eventTypePrefix + entity + "." + name, At: occurredAt}
}

// SiteRegistered: a physical facility was added to the warehouse map.
type SiteRegistered struct {
	base
	SiteCode string `json:"siteCode"`
	SiteName string `json:"siteName"`
}

// NewSiteRegistered builds a SiteRegistered event.
func NewSiteRegistered(occurredAt time.Time, siteCode, siteName string) SiteRegistered {
	return SiteRegistered{base: newBase("site", "SiteRegistered", occurredAt), SiteCode: siteCode, SiteName: siteName}
}

// ZoneRegistered: a behavioral zone was added inside a Site's area.
type ZoneRegistered struct {
	base
	ZoneID           string           `json:"zoneId"`
	SiteCode         string           `json:"siteCode"`
	AreaCode         string           `json:"areaCode"`
	ZoneCode         string           `json:"zoneCode"`
	TemperatureClass TemperatureClass `json:"temperatureClass"`
	Hazmat           bool             `json:"hazmat"`
}

// NewZoneRegistered builds a ZoneRegistered event.
func NewZoneRegistered(occurredAt time.Time, zoneID, siteCode, areaCode, zoneCode string, temperatureClass TemperatureClass, hazmat bool) ZoneRegistered {
	return ZoneRegistered{
		base:             newBase("zone", "ZoneRegistered", occurredAt),
		ZoneID:           zoneID,
		SiteCode:         siteCode,
		AreaCode:         areaCode,
		ZoneCode:         zoneCode,
		TemperatureClass: temperatureClass,
		Hazmat:           hazmat,
	}
}

// AisleRegistered: a physical corridor was added inside a Zone.
type AisleRegistered struct {
	base
	AisleID      string    `json:"aisleId"`
	ZoneID       string    `json:"zoneId"`
	AisleCode    string    `json:"aisleCode"`
	SequenceHint int       `json:"sequenceHint"`
	Direction    Direction `json:"direction"`
}

// NewAisleRegistered builds an AisleRegistered event.
func NewAisleRegistered(occurredAt time.Time, aisleID, zoneID, aisleCode string, sequenceHint int, direction Direction) AisleRegistered {
	return AisleRegistered{
		base:         newBase("aisle", "AisleRegistered", occurredAt),
		AisleID:      aisleID,
		ZoneID:       zoneID,
		AisleCode:    aisleCode,
		SequenceHint: sequenceHint,
		Direction:    direction,
	}
}

// LocationTypeRegistered: a reusable slot shape/kind was defined.
type LocationTypeRegistered struct {
	base
	LocationType string  `json:"locationType"`
	Role         string  `json:"role"`
	MaxWeightKg  float64 `json:"maxWeightKg,omitempty"`
	MaxVolumeM3  float64 `json:"maxVolumeM3,omitempty"`
}

// NewLocationTypeRegistered builds a LocationTypeRegistered event. Role is
// additive (ADR-0016): every LocationType registered before this field
// existed is "Storage". MaxWeightKg/MaxVolumeM3 are omitted when the type's
// role does not require a capacity envelope.
func NewLocationTypeRegistered(occurredAt time.Time, locationType string, role string, capacity Capacity) LocationTypeRegistered {
	return LocationTypeRegistered{
		base:         newBase("locationtype", "LocationTypeRegistered", occurredAt),
		LocationType: locationType,
		Role:         role,
		MaxWeightKg:  capacity.MaxWeightKg(),
		MaxVolumeM3:  capacity.MaxVolumeM3(),
	}
}

// PlacementRuleDefined: a rule constraining which LocationTypes are legal
// in which Zones was declared.
type PlacementRuleDefined struct {
	base
	RuleID       string `json:"ruleId"`
	LocationType string `json:"locationType"`
	Effect       string `json:"effect"`
	Predicate    string `json:"predicate"`
}

// NewPlacementRuleDefined builds a PlacementRuleDefined event.
func NewPlacementRuleDefined(occurredAt time.Time, ruleID, locationType, effect, predicate string) PlacementRuleDefined {
	return PlacementRuleDefined{
		base:         newBase("placementrule", "PlacementRuleDefined", occurredAt),
		RuleID:       ruleID,
		LocationType: locationType,
		Effect:       effect,
		Predicate:    predicate,
	}
}

// LocationSlotRegistered: a coded leaf slot now exists on the warehouse map.
type LocationSlotRegistered struct {
	base
	LocationCode string   `json:"locationCode"`
	AisleID      string   `json:"aisleId"`
	ZoneID       string   `json:"zoneId"`
	LocationType string   `json:"locationType"`
	Role         string   `json:"role"`
	DockFlow     string   `json:"dockFlow,omitempty"`
	Activities   []string `json:"activities,omitempty"`
	MaxWeightKg  float64  `json:"maxWeightKg,omitempty"`
	MaxVolumeM3  float64  `json:"maxVolumeM3,omitempty"`
}

// NewLocationSlotRegistered builds a LocationSlotRegistered event. Role,
// dockFlow, and activities are additive fields (ADR-0016): every slot
// registered before they existed is "Storage" with neither dockFlow nor
// activities set.
func NewLocationSlotRegistered(occurredAt time.Time, code LocationCode, locationType string, role string, dockFlow string, activities []string, capacity Capacity) LocationSlotRegistered {
	return LocationSlotRegistered{
		base:         newBase("locationslot", "LocationSlotRegistered", occurredAt),
		LocationCode: code.String(),
		AisleID:      code.AisleID(),
		ZoneID:       code.ZoneID(),
		LocationType: locationType,
		Role:         role,
		DockFlow:     dockFlow,
		Activities:   activities,
		MaxWeightKg:  capacity.MaxWeightKg(),
		MaxVolumeM3:  capacity.MaxVolumeM3(),
	}
}

// LocationSlotDecommissioned: a coded slot was permanently retired.
type LocationSlotDecommissioned struct {
	base
	LocationCode string `json:"locationCode"`
}

// NewLocationSlotDecommissioned builds a LocationSlotDecommissioned event.
func NewLocationSlotDecommissioned(occurredAt time.Time, code LocationCode) LocationSlotDecommissioned {
	return LocationSlotDecommissioned{
		base:         newBase("locationslot", "LocationSlotDecommissioned", occurredAt),
		LocationCode: code.String(),
	}
}

// FacilityLayoutImported: a bulk layout import completed. Emitted once per
// import call; the individual LocationSlotRegistered events still fire
// per-slot within that same import.
type FacilityLayoutImported struct {
	base
	RowsSubmitted int `json:"rowsSubmitted"`
	SlotsImported int `json:"slotsImported"`
	RowsRejected  int `json:"rowsRejected"`
}

// NewFacilityLayoutImported builds a FacilityLayoutImported event.
func NewFacilityLayoutImported(occurredAt time.Time, submitted, imported, rejected int) FacilityLayoutImported {
	return FacilityLayoutImported{
		base:          newBase("locationslot", "FacilityLayoutImported", occurredAt),
		RowsSubmitted: submitted,
		SlotsImported: imported,
		RowsRejected:  rejected,
	}
}

// LocationGeometryUpdated: a coded slot's physical position/footprint was
// set or changed (ADR-0017).
type LocationGeometryUpdated struct {
	base
	LocationCode string  `json:"locationCode"`
	XM           float64 `json:"xM"`
	YM           float64 `json:"yM"`
	ZM           float64 `json:"zM"`
	WidthM       float64 `json:"widthM"`
	DepthM       float64 `json:"depthM"`
	HeightM      float64 `json:"heightM"`
	PickSequence *int    `json:"pickSequence,omitempty"`
}

// NewLocationGeometryUpdated builds a LocationGeometryUpdated event.
func NewLocationGeometryUpdated(occurredAt time.Time, code LocationCode, position Point3D, dimensions Dimensions, pickSequence *int) LocationGeometryUpdated {
	return LocationGeometryUpdated{
		base:         newBase("locationslot", "LocationGeometryUpdated", occurredAt),
		LocationCode: code.String(),
		XM:           position.XM(),
		YM:           position.YM(),
		ZM:           position.ZM(),
		WidthM:       dimensions.WidthM(),
		DepthM:       dimensions.DepthM(),
		HeightM:      dimensions.HeightM(),
		PickSequence: pickSequence,
	}
}

// AisleGeometryUpdated: an aisle's travel centreline was set or changed
// (ADR-0017).
type AisleGeometryUpdated struct {
	base
	AisleID string  `json:"aisleId"`
	StartXM float64 `json:"startXM"`
	StartYM float64 `json:"startYM"`
	StartZM float64 `json:"startZM"`
	EndXM   float64 `json:"endXM"`
	EndYM   float64 `json:"endYM"`
	EndZM   float64 `json:"endZM"`
	LengthM float64 `json:"lengthM"`
}

// NewAisleGeometryUpdated builds an AisleGeometryUpdated event.
func NewAisleGeometryUpdated(occurredAt time.Time, aisleID string, centreline Segment) AisleGeometryUpdated {
	return AisleGeometryUpdated{
		base:    newBase("aisle", "AisleGeometryUpdated", occurredAt),
		AisleID: aisleID,
		StartXM: centreline.Start().XM(),
		StartYM: centreline.Start().YM(),
		StartZM: centreline.Start().ZM(),
		EndXM:   centreline.End().XM(),
		EndYM:   centreline.End().YM(),
		EndZM:   centreline.End().ZM(),
		LengthM: centreline.LengthM(),
	}
}

// FixedStructureRegistered: a site-scoped physical obstacle (wall, column,
// office, conveyor, or other) was added to the warehouse map (ADR-0017).
type FixedStructureRegistered struct {
	base
	StructureID string  `json:"structureId"`
	SiteCode    string  `json:"siteCode"`
	Kind        string  `json:"kind"`
	XM          float64 `json:"xM"`
	YM          float64 `json:"yM"`
	ZM          float64 `json:"zM"`
	WidthM      float64 `json:"widthM"`
	DepthM      float64 `json:"depthM"`
	HeightM     float64 `json:"heightM"`
	Label       string  `json:"label"`
}

// NewFixedStructureRegistered builds a FixedStructureRegistered event.
func NewFixedStructureRegistered(occurredAt time.Time, structureID, siteCode, kind string, footprint Rect, label string) FixedStructureRegistered {
	return FixedStructureRegistered{
		base:        newBase("structure", "FixedStructureRegistered", occurredAt),
		StructureID: structureID,
		SiteCode:    siteCode,
		Kind:        kind,
		XM:          footprint.Origin().XM(),
		YM:          footprint.Origin().YM(),
		ZM:          footprint.Origin().ZM(),
		WidthM:      footprint.Size().WidthM(),
		DepthM:      footprint.Size().DepthM(),
		HeightM:     footprint.Size().HeightM(),
		Label:       label,
	}
}

// CrossAisleRegistered: a zone-scoped connection between two of its aisles
// at a bay ordinal was added to the travel graph (ADR-0017).
type CrossAisleRegistered struct {
	base
	ZoneID    string `json:"zoneId"`
	FromAisle string `json:"fromAisle"`
	ToAisle   string `json:"toAisle"`
	AtBay     string `json:"atBay"`
}

// NewCrossAisleRegistered builds a CrossAisleRegistered event.
func NewCrossAisleRegistered(occurredAt time.Time, zoneID, fromAisle, toAisle, atBay string) CrossAisleRegistered {
	return CrossAisleRegistered{
		base:      newBase("crossaisle", "CrossAisleRegistered", occurredAt),
		ZoneID:    zoneID,
		FromAisle: fromAisle,
		ToAisle:   toAisle,
		AtBay:     atBay,
	}
}
