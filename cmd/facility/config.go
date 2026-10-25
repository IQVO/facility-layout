package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/postgres"
)

// defaultSiteCapabilityHeartbeatInterval is the SITE_CAPABILITY_HEARTBEAT_INTERVAL
// default (ADR 0041): half of network-inventory-planning's 10 minute
// freshness window for a site-capability fact.
const defaultSiteCapabilityHeartbeatInterval = 5 * time.Minute

// Boot errors of the mandatory configuration. Each names its variable
// (ADR 0039): the service runs on real Postgres and real Kafka, with no
// default and no degraded mode behind either.
var (
	errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres")
	errNoBrokers  = errors.New("KAFKA_BROKERS is required: this context runs on real Kafka")
)

// parseBrokers splits KAFKA_BROKERS (comma separated), trimming blanks. It
// returns nil when nothing is configured: there is no localhost default.
func parseBrokers(raw string) []string {
	var brokers []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			brokers = append(brokers, b)
		}
	}
	return brokers
}

// checkEventPublisher validates EVENT_PUBLISHER. The only publisher is the
// Kafka-backed transactional outbox: unset and "kafka" are accepted (the
// variable is kept because the chart and warehouse-infra set it), "log" and
// anything else is a boot error naming the variable.
func checkEventPublisher(raw string) error {
	if raw == "" || raw == "kafka" {
		return nil
	}
	if raw == "log" {
		return errors.New("EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka")
	}
	return fmt.Errorf("unknown EVENT_PUBLISHER %q: this context publishes to real Kafka (want kafka)", raw)
}

// loadBootConfig reads and validates the environment of cmd/facility. Every
// problem is reported at once, each naming its variable, so the process fails
// fast at boot, before it opens a connection or binds the listener.
func loadBootConfig() (publisherConfig, error) {
	cfg := publisherConfigFromEnv()
	var errs []error
	if cfg.databaseURL == "" {
		errs = append(errs, errNoDatabase)
	}
	if len(cfg.kafkaBrokers) == 0 {
		errs = append(errs, errNoBrokers)
	}
	if err := checkEventPublisher(os.Getenv("EVENT_PUBLISHER")); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return publisherConfig{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// publisherConfigFromEnv gathers the outbound-wiring environment in one
// place. It does not validate: loadBootConfig does.
func publisherConfigFromEnv() publisherConfig {
	databaseURL := os.Getenv("DATABASE_URL")
	return publisherConfig{
		databaseURL: databaseURL,
		// See publisherConfig.migrationsDatabaseURL's doc comment and ADR
		// 0023-migrations-direct-postgres-connection.md: falls back to
		// databaseURL (DATABASE_URL) when MIGRATIONS_DATABASE_URL is unset,
		// which is every environment that doesn't provision the split
		// (local dev, CI integration tests, a cluster whose Terraform
		// predates this fix).
		migrationsDatabaseURL: getenv("MIGRATIONS_DATABASE_URL", databaseURL),
		migrationsPath:        getenv("MIGRATIONS_PATH", "migrations"),
		kafkaBrokers:          parseBrokers(os.Getenv("KAFKA_BROKERS")),
		relayInterval:         durationEnv("OUTBOX_RELAY_INTERVAL", time.Second),
		// Housekeeping (ADR-0026): a zero/negative value disables the
		// respective knob (durationEnv falls back to the default on a
		// malformed value; an explicit "0s" parses to 0 and disables).
		housekeepingInterval: durationEnvRaw("HOUSEKEEPING_INTERVAL", postgres.DefaultSweepInterval),
		idempotencyKeyTTL:    durationEnvRaw("IDEMPOTENCY_KEY_TTL", postgres.DefaultIdempotencyKeyTTL),
		outboxRetention:      durationEnvRaw("OUTBOX_RETENTION", postgres.DefaultOutboxRetention),
		// Site capability heartbeat (ADR 0041): same convention as the
		// housekeeping knobs -- unset or malformed falls back to the
		// default, an explicit "0s"/"0" disables the loop.
		siteCapabilityHeartbeatInterval: durationEnvRaw("SITE_CAPABILITY_HEARTBEAT_INTERVAL", defaultSiteCapabilityHeartbeatInterval),
	}
}
