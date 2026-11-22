package postgres

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
)

// TestRelayFailureLogsNoEventDerivedLineBreaks: the send-failed and
// attempt-cap logs of the outbox relay must carry no line break from the
// row's event type and key or from the send error.
func TestRelayFailureLogsNoEventDerivedLineBreaks(t *testing.T) {
	rec := newCRLFRecorder()
	r := &OutboxRelay{logger: slog.New(rec), maxAttempts: 3}
	row := pendingRow{id: 1, enc: outboundkafka.Encoded{Topic: "logsafe.topic", EventType: "logsafe.Evil" + crlfForged, Key: []byte("key" + crlfForged)}}
	sendErr := errors.New("broker refused" + crlfForged)
	r.logCapReached(context.Background(), row, 1, sendErr)
	r.logCapReached(context.Background(), row, 3, sendErr)
	rec.assertNoLineBreaks(t, 1)
}
