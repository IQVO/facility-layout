package heartbeat_test

import (
	"bytes"
	"sync"
)

// lockedWriter serialises writes so the loop goroutine and the test can
// share one buffer under -race.
type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
