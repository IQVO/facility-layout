package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// BrokerLister asks ONE live broker for the cluster's broker list: a real
// metadata round trip, never an answer from a cache.
type BrokerLister interface {
	Brokers(ctx context.Context) ([]kafkago.Broker, error)
}

// BrokerCheck reports whether Kafka is reachable: a broker answers a metadata
// request. That is all it checks. It deliberately does NOT look at topics: on
// the cluster the broker auto-creates every business topic on first write (the
// fleet convention, warehouse-infra terraform/kafka.tf), so the service's own
// output topics do not exist until it first publishes, and a not-ready pod
// receives no traffic and so could never be driven to publish.
//
// The request names NO topics (kafka-go's Conn.Brokers sends an empty, non-nil
// topic list: zero topics, not the null list that means "all topics"), so the
// answer is small and asking never creates a topic.
//
// It dials a FRESH connection on every call on purpose. kafka-go's
// Client.Metadata is served from the Transport's cached cluster layout once
// one metadata response was seen ("prioritize a previously known state of the
// cluster to reduce the impact of transient failures"): a check built on it
// keeps answering ready after the broker is gone.
type BrokerCheck struct {
	lister BrokerLister
}

// NewBrokerCheck builds the check over brokers. It does not dial.
func NewBrokerCheck(brokers []string) *BrokerCheck {
	return NewBrokerCheckWithLister(dialLister{brokers: brokers, dialer: &kafkago.Dialer{Timeout: dialTimeout}})
}

// NewBrokerCheckWithLister builds the check over an explicit lister (a fake in
// tests).
func NewBrokerCheckWithLister(l BrokerLister) *BrokerCheck {
	return &BrokerCheck{lister: l}
}

// Check returns nil when a broker answered a topic-less metadata request;
// otherwise an error. ctx bounds the round trip.
func (b *BrokerCheck) Check(ctx context.Context) error {
	brokers, err := b.lister.Brokers(ctx)
	if err != nil {
		return fmt.Errorf("kafka metadata: %w", err)
	}
	if len(brokers) == 0 {
		return errors.New("kafka metadata: the broker answered without listing any broker")
	}
	return nil
}

// dialTimeout bounds one dial of the check; the readiness gate bounds the
// whole round (inboundhttp.DefaultReadinessTimeout) on top of it.
const dialTimeout = 2 * time.Second

// dialLister dials the configured addresses in order and asks the first one
// that answers.
type dialLister struct {
	brokers []string
	dialer  *kafkago.Dialer
}

func (d dialLister) Brokers(ctx context.Context) ([]kafkago.Broker, error) {
	if len(d.brokers) == 0 {
		return nil, errors.New("no broker address configured")
	}
	var lastErr error
	for _, addr := range d.brokers {
		brokers, err := d.ask(ctx, addr)
		if err == nil {
			return brokers, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// ask dials addr, sends one empty-topic metadata request and closes.
func (d dialLister) ask(ctx context.Context, addr string) ([]kafkago.Broker, error) {
	conn, err := d.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("deadline %s: %w", addr, err)
		}
	}
	brokers, err := conn.Brokers()
	if err != nil {
		return nil, fmt.Errorf("ask %s: %w", addr, err)
	}
	return brokers, nil
}
