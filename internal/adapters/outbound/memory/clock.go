// Package memory provides thread-safe, in-memory implementations of every
// outbound port. They are TEST DOUBLES for the unit and BDD tests: no cmd/**
// binary imports this package (internal/architecture
// TestRuntimeBinaryHasNoInMemoryAdapters); the shipped binaries run on real
// Postgres and real Kafka only (ADR 0039).
package memory

import "time"

// FixedClock implements ports.Clock with a fixed, settable time, for tests.
type FixedClock struct {
	t time.Time
}

// NewFixedClock builds a FixedClock pinned to t.
func NewFixedClock(t time.Time) *FixedClock {
	return &FixedClock{t: t}
}

// Now returns the pinned time.
func (c *FixedClock) Now() time.Time { return c.t }

// Advance moves the pinned time forward by d.
func (c *FixedClock) Advance(d time.Duration) {
	c.t = c.t.Add(d)
}
