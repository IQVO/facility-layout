package main

import (
	"strings"
	"testing"
)

// setBootEnv sets the whole environment loadBootConfig reads, so a case only
// has to say what it changes.
func setBootEnv(t *testing.T, overrides map[string]string) {
	t.Helper()
	env := map[string]string{
		"DATABASE_URL":    "postgres://u:p@localhost:5432/facility?sslmode=disable",
		"KAFKA_BROKERS":   "kafka-1:9092,kafka-2:9092",
		"EVENT_PUBLISHER": "",
	}
	for k, v := range overrides {
		env[k] = v
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// TestLoadBootConfigRejects pins the boot errors of the mandatory
// configuration: each one names its variable, and several problems are
// reported together.
func TestLoadBootConfigRejects(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
		wantNames []string
	}{
		{"no DATABASE_URL", map[string]string{"DATABASE_URL": ""}, []string{"DATABASE_URL"}},
		{"no KAFKA_BROKERS", map[string]string{"KAFKA_BROKERS": ""}, []string{"KAFKA_BROKERS"}},
		{"blank KAFKA_BROKERS entries", map[string]string{"KAFKA_BROKERS": " , "}, []string{"KAFKA_BROKERS"}},
		{"EVENT_PUBLISHER=log", map[string]string{"EVENT_PUBLISHER": "log"}, []string{"EVENT_PUBLISHER=log is not supported"}},
		{"unknown EVENT_PUBLISHER", map[string]string{"EVENT_PUBLISHER": "rabbit"}, []string{"EVENT_PUBLISHER", "rabbit"}},
		{"everything missing is reported at once", map[string]string{"DATABASE_URL": "", "KAFKA_BROKERS": "", "EVENT_PUBLISHER": "log"},
			[]string{"DATABASE_URL", "KAFKA_BROKERS", "EVENT_PUBLISHER=log"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBootEnv(t, tt.overrides)
			_, err := loadBootConfig()
			if err == nil {
				t.Fatal("loadBootConfig() = nil error, want a boot error")
			}
			for _, name := range tt.wantNames {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not mention %q", err, name)
				}
			}
		})
	}
}

func TestLoadBootConfigAccepts(t *testing.T) {
	for _, publisher := range []string{"", "kafka"} {
		t.Run("EVENT_PUBLISHER="+publisher, func(t *testing.T) {
			setBootEnv(t, map[string]string{"EVENT_PUBLISHER": publisher})
			cfg, err := loadBootConfig()
			if err != nil {
				t.Fatalf("loadBootConfig() = %v, want nil", err)
			}
			if got := strings.Join(cfg.kafkaBrokers, "|"); got != "kafka-1:9092|kafka-2:9092" {
				t.Errorf("kafkaBrokers = %q, want the two configured brokers", got)
			}
			if cfg.databaseURL == "" || cfg.migrationsDatabaseURL != cfg.databaseURL {
				t.Errorf("databaseURL = %q, migrationsDatabaseURL = %q, want the DATABASE_URL fallback", cfg.databaseURL, cfg.migrationsDatabaseURL)
			}
		})
	}
}

// TestBuildAdaptersRefusesWithoutBackends proves the adapter builder itself has
// no fallback runtime: an empty database or broker list is an error, not a
// degraded mode.
func TestBuildAdaptersRefusesWithoutBackends(t *testing.T) {
	tests := []struct {
		name string
		cfg  publisherConfig
		want error
	}{
		{"no database", publisherConfig{kafkaBrokers: []string{"k:9092"}}, errNoDatabase},
		{"no brokers", publisherConfig{databaseURL: "postgres://x"}, errNoBrokers},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, relay, sweeper, _, _, err := buildAdapters(tt.cfg, quietLogger())
			if err != tt.want {
				t.Fatalf("buildAdapters() error = %v, want %v", err, tt.want)
			}
			if relay != nil || sweeper != nil {
				t.Fatalf("buildAdapters() returned a relay/sweeper on error")
			}
		})
	}
}

func TestParseBrokers(t *testing.T) {
	if got := parseBrokers(""); got != nil {
		t.Errorf("parseBrokers(\"\") = %v, want nil (no localhost default)", got)
	}
	if got := strings.Join(parseBrokers(" a:1 , ,b:2"), "|"); got != "a:1|b:2" {
		t.Errorf("parseBrokers = %q, want a:1|b:2", got)
	}
}
