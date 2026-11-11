package slot

import (
	"errors"

	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// ErrSpotKindNotAllowed is returned when a spot kind is set on a slot whose
// role is not Yard: only a yard spot has a kind (ADR-0045).
var ErrSpotKindNotAllowed = errors.New("spotKind may only be set on a Yard location")

// GLN returns the slot's GS1 Global Location Number, or "" when none was
// recorded (ADR-0045).
func (s *LocationSlot) GLN() shared.GLN { return s.gln }

// SpotKind returns the yard spot kind, or "" when none was recorded. It is
// never set on a slot whose role is not Yard (ADR-0045).
func (s *LocationSlot) SpotKind() shared.SpotKind { return s.spotKind }

// AssignGLN records the slot's GS1 Global Location Number. The GLN must
// already be valid (shared.ParseGLN). A Decommissioned slot's identity is
// frozen: ErrSlotDecommissioned. Whether the number is already held by
// another location is the use case's question, not the aggregate's.
func (s *LocationSlot) AssignGLN(gln shared.GLN) error {
	if s.status == shared.Decommissioned {
		return ErrSlotDecommissioned
	}
	if _, err := shared.ParseGLN(string(gln)); err != nil {
		return err
	}
	s.gln = gln
	return nil
}

// AssignSpotKind records the yard spot kind. Rejected on a Decommissioned
// slot (ErrSlotDecommissioned, checked first), for any value outside the
// enum (shared.ErrUnknownSpotKind), and on a slot whose role is not Yard
// (ErrSpotKindNotAllowed): a dock door or a storage bin has no spot kind.
func (s *LocationSlot) AssignSpotKind(kind shared.SpotKind) error {
	if s.status == shared.Decommissioned {
		return ErrSlotDecommissioned
	}
	if _, err := shared.ParseSpotKind(string(kind)); err != nil {
		return err
	}
	if s.role != placement.Yard {
		return ErrSpotKindNotAllowed
	}
	s.spotKind = kind
	return nil
}

// WithPersistedIdentity restores the GLN and spot kind read from storage onto
// a rehydrated slot without re-running the invariants, exactly like
// RehydrateLocationSlot itself. "" means none recorded. Persistence adapters
// only.
func (s *LocationSlot) WithPersistedIdentity(gln shared.GLN, kind shared.SpotKind) *LocationSlot {
	s.gln = gln
	s.spotKind = kind
	return s
}
