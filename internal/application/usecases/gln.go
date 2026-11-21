package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// errGLNRegistryMissing is returned when a GLN is supplied but the use case
// was built without a GLNRegistry: a wiring bug, never a caller error.
var errGLNRegistryMissing = errors.New("usecases: a GLN was supplied but no GLNRegistry is wired")

// parseOptionalGLN converts the raw request string: "" means none recorded;
// any other value must be a valid GS1 GLN (shared.ErrInvalidGLN).
func parseOptionalGLN(raw string) (shared.GLN, error) {
	if raw == "" {
		return "", nil
	}
	return shared.ParseGLN(raw)
}

// claimGLN claims gln for owner through the registry, or refuses with
// ErrGLNAlreadyAssigned when another location holds it. A zero gln is a
// no-op. Callers run it inside the unit of work that saves the owner.
func claimGLN(ctx context.Context, registry ports.GLNRegistry, gln shared.GLN, owner string) error {
	if gln.IsZero() {
		return nil
	}
	if registry == nil {
		return errGLNRegistryMissing
	}
	claimed, err := registry.Claim(ctx, gln, owner)
	if err != nil {
		return err
	}
	if !claimed {
		return ErrGLNAlreadyAssigned
	}
	return nil
}

// The owner identities a GLN is claimed under. They are opaque to the
// registry; the prefix keeps a site, a zone and a slot with look-alike codes
// distinct.
func siteGLNOwner(code string) string { return "site:" + code }
func zoneGLNOwner(id string) string   { return "zone:" + id }
func slotGLNOwner(code string) string { return "slot:" + code }
