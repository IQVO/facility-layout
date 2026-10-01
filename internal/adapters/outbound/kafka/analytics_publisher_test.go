package kafka_test

import (
	"context"
	"errors"
	"testing"
	"time"

	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// TestAnalyticsPublisher_GoldenCloudEventPerType asserts the exact CloudEvent
// on the analytics topic for every event type: same `type` as the
// integration topic, `dataschema` naming the analytics stream, no
// schema_version field, and the content-type header.
func TestAnalyticsPublisher_GoldenCloudEventPerType(t *testing.T) {
	for _, tc := range goldenCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeWriter{}
			p := outboundkafka.NewAnalyticsPublisher(nil, func() string { return goldenID })
			p.Writer = w
			if err := p.Publish(context.Background(), tc.event); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if len(w.msgs) != 1 {
				t.Fatalf("expected 1 message, got %d", len(w.msgs))
			}
			assertGolden(t, w.msgs[0], tc, "analytics")
		})
	}
}

func TestAnalyticsPublisher_EncodeTargetsAnalyticsTopic(t *testing.T) {
	enc, err := (&outboundkafka.AnalyticsPublisher{}).Encode(context.Background(), shared.NewSiteRegistered(time.Now(), "WH1", "Main"), "id-1")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if enc.Topic != outboundkafka.AnalyticsTopic || string(enc.Key) != "WH1" {
		t.Errorf("topic = %q key = %q", enc.Topic, enc.Key)
	}
}

func TestAnalyticsPublisher_PropagatesWriteError(t *testing.T) {
	boom := errors.New("broker down")
	p := outboundkafka.NewAnalyticsPublisher(nil, func() string { return "evt" })
	p.Writer = &fakeWriter{err: boom}

	err := p.Publish(context.Background(), shared.NewSiteRegistered(time.Now(), "WH1", "Main"))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped broker down", err)
	}
}
