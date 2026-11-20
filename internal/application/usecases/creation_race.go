package usecases

import (
	"errors"

	"github.com/claudioed/facility-layout/internal/application/ports"
)

// lostCreationRace maps the repositories' lost-creation sentinel
// (ports.ErrNaturalKeyTaken: another writer committed this natural key
// first, ADR 0047) to the use case's documented duplicate refusal, so the
// loser of a concurrent registration of the same code gets the FINAL
// duplicate-* problem (ADR 0046) — stored and replayed under its own key —
// instead of overwriting the winner's row. Any other error passes through.
func lostCreationRace(err, duplicate error) error {
	if errors.Is(err, ports.ErrNaturalKeyTaken) {
		return duplicate
	}
	return err
}
