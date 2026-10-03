package kafka_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/facility-layout/internal/adapters/outbound/kafka"
	"github.com/claudioed/facility-layout/internal/domain/shared"
)

func TestFanOut_MintsOneIDSharedAcrossTopics(t *testing.T) {
	iw, aw := &fakeWriter{}, &fakeWriter{}
	ip := &outboundkafka.Publisher{Writer: iw}
	ap := &outboundkafka.AnalyticsPublisher{Writer: aw}
	calls := 0
	f := outboundkafka.FanOut{NewId: func() string { calls++; return "shared-id" }, Targets: []outboundkafka.EncodeSender{ip, ap}}

	if err := f.Publish(context.Background(), shared.NewSiteRegistered(time.Now(), "WH1", "Main")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if calls != 1 || len(iw.msgs) != 1 || len(aw.msgs) != 1 {
		t.Fatalf("calls=%d integration=%d analytics=%d", calls, len(iw.msgs), len(aw.msgs))
	}
	for name, w := range map[string]*fakeWriter{"integration": iw, "analytics": aw} {
		evt, err := cloudevents.Decode(w.msgs[0].Value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if evt.ID() != "shared-id" {
			t.Errorf("%s id = %q", name, evt.ID())
		}
	}
}

func TestFanOut_StopsAtFirstError(t *testing.T) {
	boom := errors.New("down")
	aw := &fakeWriter{}
	f := outboundkafka.FanOut{NewId: func() string { return "x" }, Targets: []outboundkafka.EncodeSender{
		&outboundkafka.Publisher{Writer: &fakeWriter{err: boom}},
		&outboundkafka.AnalyticsPublisher{Writer: aw},
	}}
	if err := f.Publish(context.Background(), shared.NewSiteRegistered(time.Now(), "WH1", "Main")); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if len(aw.msgs) != 0 {
		t.Fatal("second target must not be reached")
	}
}
