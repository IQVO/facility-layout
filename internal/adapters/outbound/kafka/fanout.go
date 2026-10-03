package kafka

import (
	"context"
	"fmt"

	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// EncodeSender is a fixed-topic publisher that can encode an event under a
// caller-supplied CloudEvents id and send the result: Publisher and
// AnalyticsPublisher both satisfy it.
type EncodeSender interface {
	Encoder
	Send(ctx context.Context, enc Encoded) error
}

// FanOut publishes one domain event to several topics directly (the
// EVENT_PUBLISHER=kafka path with no Postgres outbox). It mints the
// CloudEvents `id` ONCE per domain event and encodes every target under
// that same id, exactly as postgres.OutboxPublisher does, so the
// integration and analytics copies of one occurrence share their id
// (ADR-0024). A failure on any target aborts and is returned.
type FanOut struct {
	NewId   func() string
	Targets []EncodeSender
}

// Publish encodes event once per target under one shared id and sends it.
func (f FanOut) Publish(ctx context.Context, event shared.DomainEvent) error {
	id := f.NewId()
	for _, t := range f.Targets {
		enc, err := t.Encode(ctx, event, id)
		if err != nil {
			return err
		}
		if err := t.Send(ctx, enc); err != nil {
			return fmt.Errorf("kafka: publish %s on %s: %w", event.EventName(), enc.Topic, err)
		}
	}
	return nil
}

// Compile-time assertion that FanOut satisfies the outbound port.
var _ ports.EventPublisher = FanOut{}
