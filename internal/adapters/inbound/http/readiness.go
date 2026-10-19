package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Dependency is one thing /readyz needs to be true: a name for the response
// and the check itself (Postgres ping, Kafka broker metadata).
type Dependency struct {
	Name  string
	Check func(ctx context.Context) error
}

// Readiness is a process-wide, thread-safe readiness gate (ADR-0020 §graceful
// shutdown, mirroring order-management's ADR-0025 §graceful shutdown, grown by
// ADR-0039): GET /readyz reflects it, separately from /healthz (liveness --
// "is the process alive", never flipped by shutdown) so a Kubernetes
// readinessProbe can be pointed at /readyz and a livenessProbe/startupProbe
// can stay pointed at /healthz (see charts/facility-layout/values.yaml).
//
// It has two parts. The drain gate: graceful shutdown flips it FIRST
// (SetNotReady), so a readinessProbe stops routing new traffic before the
// listener closes. The dependency checks (NewReadiness): the process is not
// ready while Postgres or Kafka is not reachable; their verdict is cached for
// a few seconds so probes cannot hammer either of them.
//
// The zero value is READY with no dependency to check (a unit-test fixture;
// every cmd/ binary builds the real gate with NewReadiness).
type Readiness struct {
	notReady atomic.Bool

	deps    []Dependency
	ttl     time.Duration
	timeout time.Duration
	now     func() time.Time

	mu        sync.Mutex
	checkedAt time.Time
	failing   []string
}

// Defaults of the dependency checks.
const (
	// DefaultReadinessTTL is how long a dependency verdict is reused.
	DefaultReadinessTTL = 3 * time.Second
	// DefaultReadinessTimeout bounds one round of dependency checks.
	DefaultReadinessTimeout = 2 * time.Second
)

// NewReadiness builds a gate that also requires every dependency to pass. A
// verdict (pass or fail) is reused for ttl; one round of checks is bounded by
// timeout. Non-positive values fall back to the defaults.
func NewReadiness(ttl, timeout time.Duration, deps ...Dependency) *Readiness {
	if ttl <= 0 {
		ttl = DefaultReadinessTTL
	}
	if timeout <= 0 {
		timeout = DefaultReadinessTimeout
	}
	return &Readiness{deps: deps, ttl: ttl, timeout: timeout, now: time.Now}
}

// SetNotReady flips the drain gate to not-ready. This is the FIRST step of
// graceful shutdown (ADR-0020): flip readiness before touching the HTTP
// server or any consumer/relay, so a Kubernetes readinessProbe can observe
// the flip and stop routing new traffic during the drain window that
// follows, before Shutdown ever closes a listener.
func (g *Readiness) SetNotReady() {
	if g == nil {
		return
	}
	g.notReady.Store(true)
}

// Ready reports whether the drain gate currently says ready (nil = ready).
func (g *Readiness) Ready() bool {
	if g == nil {
		return true
	}
	return !g.notReady.Load()
}

// Failing returns the names of the dependencies that are not reachable, nil
// when all are (or there is none). Within the ttl it answers from the cache.
// The check runs on a context detached from the caller's cancellation, so a
// client that hangs up cannot poison the cached verdict.
func (g *Readiness) Failing(ctx context.Context) []string {
	if g == nil || len(g.deps) == 0 {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.checkedAt.IsZero() && g.now().Sub(g.checkedAt) < g.ttl {
		return append([]string(nil), g.failing...)
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), g.timeout)
	defer cancel()
	var failing []string
	for _, d := range g.deps {
		if err := d.Check(checkCtx); err != nil {
			slog.WarnContext(ctx, "readiness: dependency is not ready", "dependency", d.Name, "error", err)
			failing = append(failing, d.Name)
		}
	}
	g.failing, g.checkedAt = failing, g.now()
	return append([]string(nil), failing...)
}

// dependencyUnavailable is the 503 problem of a failed dependency check.
var dependencyUnavailable = problemInfo{"dependency-unavailable", "A runtime dependency is unavailable"}

// HandleReadyz serves 200 {"status":"ready"} while ready; 503
// {"status":"not_ready"} once SetNotReady was called (draining); 503
// problem+json (dependency-unavailable, naming the dependencies) while
// Postgres or Kafka is not reachable. A nil Readiness always reports ready.
func HandleReadyz(g *Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.Ready() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		if failing := g.Failing(r.Context()); len(failing) > 0 {
			writeProblem(w, http.StatusServiceUnavailable, dependencyUnavailable, "not ready: "+strings.Join(failing, ", "), r.URL.Path)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
