package site

import "time"

// CapabilityHeartbeatAt returns when the last capability heartbeat was
// raised for this site, or the zero time when none has been (ADR-0041).
func (s *Site) CapabilityHeartbeatAt() time.Time { return s.capabilityHeartbeatAt }

// HeartbeatDue reports whether this site's capability fact is due for a
// heartbeat at now (ADR-0041): the site is active, a capability snapshot
// has been declared (revision above zero), and either no heartbeat was
// ever raised or the last one is at least interval old. An interval of zero
// or less is never due: the heartbeat is disabled.
func (s *Site) HeartbeatDue(now time.Time, interval time.Duration) bool {
	if interval <= 0 || !s.IsActive() || s.capabilityRevision <= 0 {
		return false
	}
	return s.capabilityHeartbeatAt.IsZero() || now.Sub(s.capabilityHeartbeatAt) >= interval
}

// HeartbeatTransferCapability re-asserts the site's CURRENT capability
// (ADR-0041) and reports whether a heartbeat is to be published. When the
// site is due (HeartbeatDue) it advances capabilityRevision by exactly one
// and stamps now as the heartbeat time, so a last-write-wins consumer that
// only accepts a strictly greater revision (network-inventory-planning)
// refreshes its watermark; the capability values are untouched. Otherwise
// nothing changes and it returns false.
//
// It is distinct from ChangeTransferCapability on purpose: a PUT of the
// value a site already holds stays an idempotent no-op (ADR-0035).
func (s *Site) HeartbeatTransferCapability(now time.Time, interval time.Duration) bool {
	if !s.HeartbeatDue(now, interval) {
		return false
	}
	s.capabilityRevision++
	s.capabilityHeartbeatAt = now
	return true
}
