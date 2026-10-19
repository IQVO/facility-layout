package system

import (
	"testing"
	"time"
)

func TestClockNowIsWallClock(t *testing.T) {
	before := time.Now()
	got := Clock{}.Now()
	if got.Before(before) || time.Since(got) > time.Minute {
		t.Fatalf("Clock.Now() = %v, want the current wall-clock time (>= %v)", got, before)
	}
}
