// Package events provides non-broker outbound EventPublisher implementations
// (log, in-memory buffer). The Kafka publishers live in outbound/kafka and
// emit every event as a CloudEvents 1.0 event (ADR-0024); nothing here writes
// to Kafka or builds an envelope. Every domain event carries its CloudEvents
// `type` (EventType), which these adapters only log/record.
package events

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// LogPublisher publishes domain events by logging them as JSON. Useful for
// local development and as a default when no broker is configured.
type LogPublisher struct {
	logger *slog.Logger
}

// NewLogPublisher builds a LogPublisher writing to logger.
func NewLogPublisher(logger *slog.Logger) *LogPublisher {
	return &LogPublisher{logger: logger}
}

// Publish logs the event as JSON, tagged with its CloudEvents type. The
// "event_type" key is a log attribute name (see LOGGING.md), not a Kafka
// envelope field. It
// logs with ctx so the line is correlated with the span the use case that
// raised the event is running in.
func (p *LogPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	p.logger.InfoContext(ctx, "domain event published",
		"event_name", event.EventName(),
		"event_type", event.EventType(),
		"payload", json.RawMessage(payload),
	)
	return nil
}
