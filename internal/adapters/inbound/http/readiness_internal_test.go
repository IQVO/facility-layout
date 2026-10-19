package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countingDep is a dependency that counts its calls and fails on demand.
type countingDep struct {
	calls atomic.Int32
	fail  atomic.Bool
}

func (c *countingDep) dependency(name string) Dependency {
	return Dependency{Name: name, Check: func(context.Context) error {
		c.calls.Add(1)
		if c.fail.Load() {
			return errors.New("down")
		}
		return nil
	}}
}

func getPath(gate *Readiness, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	NewRouter(&Server{Readiness: gate}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
	return rec
}

func getReadyz(gate *Readiness) *httptest.ResponseRecorder { return getPath(gate, "/readyz") }

func TestReadinessRechecksOnceTheTTLExpires(t *testing.T) {
	probe := &countingDep{}
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	gate := NewReadiness(3*time.Second, time.Second, probe.dependency("postgres"))
	gate.now = func() time.Time { return now }
	gate.Failing(context.Background())
	now = now.Add(2 * time.Second)
	gate.Failing(context.Background())
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("checked %d times before the ttl, want 1", n)
	}
	probe.fail.Store(true)
	now = now.Add(time.Second) // exactly the ttl: the verdict is stale
	if failing := gate.Failing(context.Background()); len(failing) != 1 || failing[0] != "postgres" {
		t.Fatalf("after the ttl the check must run again, got %v", failing)
	}
	if n := probe.calls.Load(); n != 2 {
		t.Fatalf("checked %d times, want 2", n)
	}
}

// A dependency that hangs is cut off by the timeout and reported down.
func TestReadinessBoundsASlowDependency(t *testing.T) {
	slow := Dependency{Name: "kafka", Check: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	gate := NewReadiness(time.Second, 20*time.Millisecond, slow)
	start := time.Now()
	if failing := gate.Failing(context.Background()); len(failing) != 1 {
		t.Fatalf("failing = %v", failing)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the slow dependency was not cut off by the timeout")
	}
}

// A caller that hangs up must not poison the cached verdict.
func TestReadinessCheckIgnoresTheCallersCancellation(t *testing.T) {
	gate := NewReadiness(time.Hour, time.Second, Dependency{Name: "postgres", Check: func(ctx context.Context) error {
		return ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if failing := gate.Failing(ctx); failing != nil {
		t.Fatalf("a cancelled caller context must not fail the dependency, got %v", failing)
	}
}

func TestReadinessDefaultsApplyToNonPositiveValues(t *testing.T) {
	gate := NewReadiness(0, -1)
	if gate.ttl != DefaultReadinessTTL || gate.timeout != DefaultReadinessTimeout {
		t.Fatalf("ttl=%v timeout=%v, want the defaults %v / %v", gate.ttl, gate.timeout, DefaultReadinessTTL, DefaultReadinessTimeout)
	}
	if DefaultReadinessTTL != 3*time.Second || DefaultReadinessTimeout != 2*time.Second {
		t.Fatalf("fleet bounds changed: ttl=%v timeout=%v, want 3s / 2s", DefaultReadinessTTL, DefaultReadinessTimeout)
	}
}

// Draining wins over the dependency verdict and does not run the checks.
func TestDrainingDoesNotCheckDependencies(t *testing.T) {
	probe := &countingDep{}
	gate := NewReadiness(time.Second, time.Second, probe.dependency("postgres"))
	gate.SetNotReady()
	rec := getReadyz(gate)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not_ready") || probe.calls.Load() != 0 {
		t.Fatalf("draining = %d %s (checks=%d)", rec.Code, rec.Body.String(), probe.calls.Load())
	}
}

// A failed dependency is a 503 problem+json naming the dependency; healthy
// dependencies are 200 ready.
func TestReadyzReportsAFailedDependencyAsAProblem(t *testing.T) {
	pg, kafka := &countingDep{}, &countingDep{}
	kafka.fail.Store(true)
	gate := NewReadiness(time.Nanosecond, time.Second, pg.dependency("postgres"), kafka.dependency("kafka"))

	rec := getReadyz(gate)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with Kafka down = %d %s, want 503", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"dependency-unavailable", "not ready: kafka", "/readyz"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q: %s", want, body)
		}
	}
	if strings.Contains(body, "postgres") {
		t.Errorf("healthy postgres must not be named: %s", body)
	}

	kafka.fail.Store(false)
	pg.fail.Store(true)
	time.Sleep(2 * time.Millisecond) // past the 1ns ttl
	if rec := getReadyz(gate); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not ready: postgres") {
		t.Fatalf("readyz with Postgres down = %d %s", rec.Code, rec.Body)
	}

	pg.fail.Store(false)
	time.Sleep(2 * time.Millisecond)
	if rec := getReadyz(gate); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ready"`) {
		t.Fatalf("readyz with every dependency up = %d %s", rec.Code, rec.Body)
	}
}

// Liveness never depends on the dependencies.
func TestHealthzIgnoresDependencies(t *testing.T) {
	down := &countingDep{}
	down.fail.Store(true)
	rec := getPath(NewReadiness(0, 0, down.dependency("postgres")), "/healthz")
	if rec.Code != http.StatusOK || down.calls.Load() != 0 {
		t.Fatalf("healthz = %d (checks=%d), want 200 without checking", rec.Code, down.calls.Load())
	}
}
