package usecases

import (
	"context"

	"github.com/claudioed/facility-layout/internal/application/ports"
)

// atomically runs fn inside uow when one is wired, or directly otherwise.
// Keeping this in one place means every use case treats a nil UnitOfWork
// identically instead of each re-deciding the fallback (ADR-0018,
// transactional outbox).
func atomically(ctx context.Context, uow ports.UnitOfWork, fn func(ctx context.Context) error) error {
	if uow == nil {
		return fn(ctx)
	}
	return uow.Execute(ctx, fn)
}
