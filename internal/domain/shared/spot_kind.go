package shared

import "errors"

// ErrUnknownSpotKind is returned when a yard spot kind is not one of
// PARKING/HOLDING/REEFER_PLUG/HAZMAT/STAGING_LANE (ADR-0045).
var ErrUnknownSpotKind = errors.New("spot kind must be one of PARKING, HOLDING, REEFER_PLUG, HAZMAT, STAGING_LANE")

// SpotKind classifies a yard spot: what the spot is for, as yard-management
// will need to hold or stage a trailer on it. It is OPTIONAL and only
// meaningful on a LocationSlot whose role is Yard; the zero value ("") means
// none was ever recorded.
type SpotKind string

const (
	// SpotKindParking is an ordinary trailer parking spot.
	SpotKindParking SpotKind = "PARKING"
	// SpotKindHolding is a spot where a trailer waits for a dock door.
	SpotKindHolding SpotKind = "HOLDING"
	// SpotKindReeferPlug is a spot with shore power for a refrigerated trailer.
	SpotKindReeferPlug SpotKind = "REEFER_PLUG"
	// SpotKindHazmat is a spot cleared for hazardous-materials trailers.
	SpotKindHazmat SpotKind = "HAZMAT"
	// SpotKindStagingLane is a lane where loaded trailers are staged.
	SpotKindStagingLane SpotKind = "STAGING_LANE"
)

// ParseSpotKind validates and converts the string form. The empty string is
// NOT valid here: "absent" is represented by not calling the parser.
func ParseSpotKind(value string) (SpotKind, error) {
	switch SpotKind(value) {
	case SpotKindParking, SpotKindHolding, SpotKindReeferPlug, SpotKindHazmat, SpotKindStagingLane:
		return SpotKind(value), nil
	default:
		return "", ErrUnknownSpotKind
	}
}
