package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

type fakeLister struct {
	brokers []kafkago.Broker
	err     error
	calls   int
}

func (f *fakeLister) Brokers(context.Context) ([]kafkago.Broker, error) {
	f.calls++
	return f.brokers, f.err
}

func oneBroker() []kafkago.Broker { return []kafkago.Broker{{ID: 1, Host: "b", Port: 9092}} }

func TestBrokerCheckPassesWhenABrokerAnswers(t *testing.T) {
	if err := NewBrokerCheckWithLister(&fakeLister{brokers: oneBroker()}).Check(context.Background()); err != nil {
		t.Fatalf("check = %v", err)
	}
}

func TestBrokerCheckFailsWhenTheBrokerIsUnreachable(t *testing.T) {
	boom := errors.New("connection refused")
	err := NewBrokerCheckWithLister(&fakeLister{err: boom}).Check(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("check = %v, want the cause wrapped", err)
	}
}

func TestBrokerCheckFailsWhenTheAnswerListsNoBroker(t *testing.T) {
	for name, brokers := range map[string][]kafkago.Broker{"nil list": nil, "empty list": {}} {
		if err := NewBrokerCheckWithLister(&fakeLister{brokers: brokers}).Check(context.Background()); err == nil {
			t.Errorf("%s: a metadata answer without a broker must fail the check", name)
		}
	}
}

// The check asks the lister on EVERY call: there is no cache behind it (the
// readiness gate caches the verdict, not this).
func TestBrokerCheckAsksTheLiveBrokerEveryTime(t *testing.T) {
	f := &fakeLister{brokers: oneBroker()}
	c := NewBrokerCheckWithLister(f)
	for range 3 {
		if err := c.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	f.err = errors.New("gone")
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("a broker that went away after earlier successes must fail the check")
	}
	if f.calls != 4 {
		t.Fatalf("lister asked %d times, want 4", f.calls)
	}
}

// A broker address nobody listens on (the real dialer, no fake) is not
// ready, and fails fast instead of hanging.
func TestBrokerCheckAgainstADeadAddressIsNotReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := NewBrokerCheck([]string{"127.0.0.1:1"}).Check(ctx); err == nil {
		t.Fatal("a dead broker address must fail the check")
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("a dead address must fail fast")
	}
}

func TestBrokerCheckWithNoAddressFails(t *testing.T) {
	if err := NewBrokerCheck(nil).Check(context.Background()); err == nil {
		t.Fatal("no broker address must fail the check")
	}
}

func TestNewBrokerCheckDoesNotDial(t *testing.T) {
	if NewBrokerCheck([]string{"127.0.0.1:1"}) == nil {
		t.Fatal("nil check")
	}
}
