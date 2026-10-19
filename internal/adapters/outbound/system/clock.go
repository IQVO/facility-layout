// Package system holds the production implementations of the host-provided
// ports (the wall clock). They live apart from internal/adapters/outbound/memory
// so a cmd/** binary never imports the in-memory test doubles
// (internal/architecture TestRuntimeBinaryHasNoInMemoryAdapters).
package system

import "time"

// Clock implements ports.Clock using wall-clock time.
type Clock struct{}

// Now returns the current wall-clock time.
func (Clock) Now() time.Time { return time.Now() }
