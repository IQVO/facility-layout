package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// TestNewDLQWriter_MatchesFleetSyncWriterConfig pins the dead-letter
// writer's full durability config (ADR-0020), mirroring the fleet's
// fulfillment-execution TestNewDeadLetterWriter_MatchesSyncWriterConfig:
// RequireAll (the kafka-go default RequireNone lets WriteMessages return
// nil before the broker stores the message — and Run commits the source
// offset right after a "successful" DLQ publish, so the poison message is
// silently LOST), the Hash balancer (the original message key still decides
// the .dlq partition, preserving per-key order), a prompt 10ms flush
// (kafka-go's 1s default caps dead-lettering at ~1 msg/s), and
// auto-creation of the rarely-existing "<topic>.dlq" topic.
func TestNewDLQWriter_MatchesFleetSyncWriterConfig(t *testing.T) {
	w := newDLQWriter([]string{"localhost:9092"}, "warehouse.x.analytics.dlq")
	t.Cleanup(func() { _ = w.Close() })

	if w.Topic != "warehouse.x.analytics.dlq" {
		t.Fatalf("DLQ topic = %q", w.Topic)
	}
	if !w.AllowAutoTopicCreation {
		t.Fatal("DLQ writer must set AllowAutoTopicCreation")
	}
	if w.BatchTimeout != 10*time.Millisecond {
		t.Fatalf("DLQ BatchTimeout = %v, want 10ms (kafka-go's 1s default caps dead-lettering at ~1 msg/s)", w.BatchTimeout)
	}
	if w.RequiredAcks != kafkago.RequireAll {
		t.Fatalf("DLQ RequiredAcks = %v, want RequireAll (the default RequireNone silently loses messages the broker never stored)", w.RequiredAcks)
	}
	if _, ok := w.Balancer.(*kafkago.Hash); !ok {
		t.Fatalf("DLQ Balancer = %T, want *kafkago.Hash so the original message key decides the partition", w.Balancer)
	}
}

type scriptedDLQWriter struct {
	errs  []error
	calls int
}

func (w *scriptedDLQWriter) WriteMessages(_ context.Context, _ ...kafkago.Message) error {
	w.calls++
	if len(w.errs) == 0 {
		return nil
	}
	err := w.errs[0]
	w.errs = w.errs[1:]
	return err
}

func TestWriteDLQ_RetriesOnlyWhileTheTopicIsBeingCreated(t *testing.T) {
	boom := errors.New("broker on fire")
	tests := []struct {
		name      string
		errs      []error
		wantErr   error
		wantCalls int
	}{
		{"first write succeeds", nil, nil, 1},
		{"unknown topic then success", []error{kafkago.UnknownTopicOrPartition, kafkago.LeaderNotAvailable}, nil, 3},
		{"per-message write errors that are all not-ready are retried", []error{kafkago.WriteErrors{kafkago.UnknownTopicOrPartition}}, nil, 2},
		{"other errors are returned immediately", []error{boom}, boom, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &scriptedDLQWriter{errs: tt.errs}
			err := writeDLQ(context.Background(), w, kafkago.Message{})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if w.calls != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", w.calls, tt.wantCalls)
			}
		})
	}
}

func TestWriteDLQ_GivesUpWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &scriptedDLQWriter{errs: []error{kafkago.UnknownTopicOrPartition, kafkago.UnknownTopicOrPartition}}
	if err := writeDLQ(ctx, w, kafkago.Message{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
