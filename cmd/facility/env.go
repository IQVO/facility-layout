package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// durationEnv parses key as a time.Duration, falling back on absence or a
// malformed value (the relay interval is a tuning knob, not a contract).
func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// durationEnvRaw is durationEnv for knobs where ZERO is meaningful (it
// disables the feature): only absence or a malformed value falls back to
// the default, an explicit "0s"/"0" is preserved as-is.
func durationEnvRaw(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

// outboxMaxAttempts parses OUTBOX_MAX_ATTEMPTS (ADR 0036): how many failed
// sends exclude an outbox row from the relay's claims. Unset means
// postgres.DefaultOutboxMaxAttempts (20); 0 means unlimited (the pre-ADR
// behaviour, where one poison row blocks the relay forever).
func outboxMaxAttempts(logger *slog.Logger) int {
	return intEnv(logger, "OUTBOX_MAX_ATTEMPTS", postgres.DefaultOutboxMaxAttempts)
}

// intEnv parses a non-negative integer env var. Unset yields fallback; an
// invalid or negative value logs a warning and yields fallback, so a typo
// cannot silently remove a safety limit. "0" is valid and its meaning is the
// caller's.
func intEnv(logger *slog.Logger, key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		logger.Warn("invalid integer env var, using default", "key", key, "value", raw, "default", fallback, "error", err)
		return fallback
	}
	return n
}

// uuidLike mints the CloudEvents `id` (UUID v4) — once per domain event —
// stamped on each published event (ADR-0024).
func uuidLike() string {
	return uuid.NewString()
}
