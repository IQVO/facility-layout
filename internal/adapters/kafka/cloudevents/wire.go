package cloudevents

import (
	"fmt"
	"time"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// This file owns the JSON wire shape of every facility-layout domain event.
// The domain events (internal/domain/shared/events.go) carry no serialisation
// tags: what goes into the CloudEvent `data` (and the log publisher's
// `payload`) is defined here, as adapter DTOs plus explicit mapping from each
// domain event. The field names are the Published Language that downstream
// Conformists (inventory-storage, wes-work-planning, ...) decode; they are
// pinned byte-for-byte by the golden files under
// internal/adapters/outbound/kafka/testdata/wire, so a rename here fails CI
// instead of silently breaking a consumer. Field order is the wire order.

// wireEnvelope is the common prefix of every event payload.
type wireEnvelope struct {
	EventName  string    `json:"eventName"`
	EventType  string    `json:"eventType"`
	OccurredAt time.Time `json:"occurredAt"`
}

func envelopeOf(e shared.DomainEvent) wireEnvelope {
	return wireEnvelope{EventName: e.EventName(), EventType: e.EventType(), OccurredAt: e.OccurredAt()}
}

type siteRegisteredData struct {
	wireEnvelope
	SiteCode string `json:"siteCode"`
	SiteName string `json:"siteName"`
}

type siteCapabilityChangedData struct {
	wireEnvelope
	SiteCode                   string `json:"site_code"`
	TransferOriginEnabled      bool   `json:"transfer_origin_enabled"`
	TransferDestinationEnabled bool   `json:"transfer_destination_enabled"`
	CapabilityRevision         int64  `json:"capability_revision"`
}

type zoneRegisteredData struct {
	wireEnvelope
	ZoneID           string `json:"zoneId"`
	SiteCode         string `json:"siteCode"`
	AreaCode         string `json:"areaCode"`
	ZoneCode         string `json:"zoneCode"`
	TemperatureClass string `json:"temperatureClass"`
	Hazmat           bool   `json:"hazmat"`
}

type aisleRegisteredData struct {
	wireEnvelope
	AisleID      string `json:"aisleId"`
	ZoneID       string `json:"zoneId"`
	AisleCode    string `json:"aisleCode"`
	SequenceHint int    `json:"sequenceHint"`
	Direction    string `json:"direction"`
}

type locationTypeRegisteredData struct {
	wireEnvelope
	LocationType string  `json:"locationType"`
	Role         string  `json:"role"`
	MaxWeightKg  float64 `json:"maxWeightKg,omitempty"`
	MaxVolumeM3  float64 `json:"maxVolumeM3,omitempty"`
}

type placementRuleDefinedData struct {
	wireEnvelope
	RuleID       string `json:"ruleId"`
	LocationType string `json:"locationType"`
	Effect       string `json:"effect"`
	Predicate    string `json:"predicate"`
}

type locationSlotRegisteredData struct {
	wireEnvelope
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

type locationSlotDecommissionedData struct {
	wireEnvelope
	LocationCode string `json:"locationCode"`
}

type facilityLayoutImportedData struct {
	wireEnvelope
	RowsSubmitted int `json:"rowsSubmitted"`
	SlotsImported int `json:"slotsImported"`
	RowsRejected  int `json:"rowsRejected"`
}

type locationGeometryUpdatedData struct {
	wireEnvelope
	LocationCode string  `json:"locationCode"`
	XM           float64 `json:"xM"`
	YM           float64 `json:"yM"`
	ZM           float64 `json:"zM"`
	WidthM       float64 `json:"widthM"`
	DepthM       float64 `json:"depthM"`
	HeightM      float64 `json:"heightM"`
	PickSequence *int    `json:"pickSequence,omitempty"`
}

type aisleGeometryUpdatedData struct {
	wireEnvelope
	AisleID string  `json:"aisleId"`
	StartXM float64 `json:"startXM"`
	StartYM float64 `json:"startYM"`
	StartZM float64 `json:"startZM"`
	EndXM   float64 `json:"endXM"`
	EndYM   float64 `json:"endYM"`
	EndZM   float64 `json:"endZM"`
	LengthM float64 `json:"lengthM"`
}

type fixedStructureRegisteredData struct {
	wireEnvelope
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

type crossAisleRegisteredData struct {
	wireEnvelope
	ZoneID    string `json:"zoneId"`
	FromAisle string `json:"fromAisle"`
	ToAisle   string `json:"toAisle"`
	AtBay     string `json:"atBay"`
}

// WireData maps a domain event to its JSON wire DTO: the value marshalled
// as the CloudEvent `data` (and as the log publisher's payload). An event
// type with no DTO is an error rather than a silent fallback to reflection
// over domain fields, so adding a domain event without defining its wire
// shape fails loudly.
func WireData(event shared.DomainEvent) (any, error) {
	env := envelopeOf(event)
	switch e := event.(type) {
	case shared.SiteRegistered:
		return siteRegisteredData{env, e.SiteCode, e.SiteName}, nil
	case shared.SiteCapabilityChanged:
		return siteCapabilityChangedData{env, e.SiteCode, e.TransferOriginEnabled, e.TransferDestinationEnabled, e.CapabilityRevision}, nil
	case shared.ZoneRegistered:
		return zoneRegisteredData{env, e.ZoneID, e.SiteCode, e.AreaCode, e.ZoneCode, string(e.TemperatureClass), e.Hazmat}, nil
	case shared.AisleRegistered:
		return aisleRegisteredData{env, e.AisleID, e.ZoneID, e.AisleCode, e.SequenceHint, string(e.Direction)}, nil
	case shared.LocationTypeRegistered:
		return locationTypeRegisteredData{env, e.LocationType, e.Role, e.MaxWeightKg, e.MaxVolumeM3}, nil
	case shared.PlacementRuleDefined:
		return placementRuleDefinedData{env, e.RuleID, e.LocationType, e.Effect, e.Predicate}, nil
	case shared.LocationSlotRegistered:
		return locationSlotRegisteredData{env, e.LocationCode, e.AisleID, e.ZoneID, e.LocationType, e.Role, e.DockFlow, e.Activities, e.MaxWeightKg, e.MaxVolumeM3}, nil
	case shared.LocationSlotDecommissioned:
		return locationSlotDecommissionedData{env, e.LocationCode}, nil
	case shared.FacilityLayoutImported:
		return facilityLayoutImportedData{env, e.RowsSubmitted, e.SlotsImported, e.RowsRejected}, nil
	case shared.LocationGeometryUpdated:
		return locationGeometryUpdatedData{env, e.LocationCode, e.XM, e.YM, e.ZM, e.WidthM, e.DepthM, e.HeightM, e.PickSequence}, nil
	case shared.AisleGeometryUpdated:
		return aisleGeometryUpdatedData{env, e.AisleID, e.StartXM, e.StartYM, e.StartZM, e.EndXM, e.EndYM, e.EndZM, e.LengthM}, nil
	case shared.FixedStructureRegistered:
		return fixedStructureRegisteredData{env, e.StructureID, e.SiteCode, e.Kind, e.XM, e.YM, e.ZM, e.WidthM, e.DepthM, e.HeightM, e.Label}, nil
	case shared.CrossAisleRegistered:
		return crossAisleRegisteredData{env, e.ZoneID, e.FromAisle, e.ToAisle, e.AtBay}, nil
	default:
		return nil, fmt.Errorf("cloudevents: no wire DTO for domain event %T", event)
	}
}
