package heartbeat_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/inbound/heartbeat"
	"github.com/claudioed/facility-layout/internal/application/usecases"
)

type countingPublisher struct {
	calls atomic.Int32
	res   usecases.HeartbeatResult
	err   error
}

func (p *countingPublisher) Execute(context.Context) (usecases.HeartbeatResult, error) {
	p.calls.Add(1)
	return p.res, p.err
}

func TestTickEveryIsAFifthOfTheInterval(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		5 * time.Minute:     time.Minute,
		10 * time.Second:    2 * time.Second,
		5 * time.Nanosecond: time.Nanosecond,
		4 * time.Nanosecond: 4 * time.Nanosecond, // a fifth rounds to zero: tick on the interval
		time.Nanosecond:     time.Nanosecond,
	}
	for interval, want := range cases {
		if got := heartbeat.TickEvery(interval); got != want {
			t.Errorf("TickEvery(%v) = %v, want %v", interval, got, want)
		}
	}
}

func TestRunPassesImmediatelyThenOnEveryTick(t *testing.T) {
	p := &countingPublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (&heartbeat.Loop{Publisher: p, Interval: 50 * time.Millisecond}).Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for p.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run must return nil on cancel, got %v", err)
	}
	if p.calls.Load() < 3 {
		t.Fatalf("want an immediate pass plus ticks, got %d passes", p.calls.Load())
	}
}

func TestRunWithAZeroOrNegativeIntervalIsDisabled(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		p := &countingPublisher{}
		if err := (&heartbeat.Loop{Publisher: p, Interval: interval}).Run(context.Background()); err != nil {
			t.Fatalf("interval %v: %v", interval, err)
		}
		if p.calls.Load() != 0 {
			t.Fatalf("interval %v must never run a pass, ran %d", interval, p.calls.Load())
		}
	}
}

func TestRunLogsAFailedPassAndKeepsGoing(t *testing.T) {
	var buf bytes.Buffer
	p := &countingPublisher{err: errors.New("boom")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (&heartbeat.Loop{Publisher: p, Interval: 25 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(&lockedWriter{w: &buf}, nil))}).Run(ctx)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for p.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a failed pass must never end the loop with an error: %v", err)
	}
	if p.calls.Load() < 2 {
		t.Fatalf("the loop must retry after a failed pass, got %d", p.calls.Load())
	}
	if !strings.Contains(buf.String(), "site capability heartbeat failed") {
		t.Fatalf("failure not logged: %q", buf.String())
	}
}
