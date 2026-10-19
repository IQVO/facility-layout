package main

import (
	"strings"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfigRejects(t *testing.T) {
	full := map[string]string{
		"ANALYTICS_DATABASE_URL":   "postgres://x",
		"KAFKA_BROKERS":            "k1:9092, k2:9092",
		"ANALYTICS_CONSUMER_GROUP": "facility-analytics",
	}
	without := func(key string) map[string]string {
		m := map[string]string{}
		for k, v := range full {
			if k != key {
				m[k] = v
			}
		}
		return m
	}

	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"missing ANALYTICS_DATABASE_URL", without("ANALYTICS_DATABASE_URL"), []string{"ANALYTICS_DATABASE_URL"}},
		{"missing KAFKA_BROKERS", without("KAFKA_BROKERS"), []string{"KAFKA_BROKERS"}},
		{"blank KAFKA_BROKERS", map[string]string{"ANALYTICS_DATABASE_URL": "postgres://x", "KAFKA_BROKERS": " , ", "ANALYTICS_CONSUMER_GROUP": "g"}, []string{"KAFKA_BROKERS"}},
		{"missing ANALYTICS_CONSUMER_GROUP", without("ANALYTICS_CONSUMER_GROUP"), []string{"ANALYTICS_CONSUMER_GROUP"}},
		{"all reported at once", map[string]string{}, []string{"ANALYTICS_DATABASE_URL", "KAFKA_BROKERS", "ANALYTICS_CONSUMER_GROUP"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(envFrom(tt.env))
			if err == nil {
				t.Fatal("loadConfig() = nil error, want a boot error")
			}
			for _, name := range tt.want {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name %q", err, name)
				}
			}
		})
	}
}

func TestLoadConfigComplete(t *testing.T) {
	c, err := loadConfig(envFrom(map[string]string{
		"ANALYTICS_DATABASE_URL":   "postgres://x",
		"KAFKA_BROKERS":            "k1:9092, k2:9092",
		"ANALYTICS_CONSUMER_GROUP": "facility-analytics",
	}))
	if err != nil {
		t.Fatalf("loadConfig() = %v", err)
	}
	if strings.Join(c.brokers, "|") != "k1:9092|k2:9092" || c.group != "facility-analytics" || c.analyticsURL != "postgres://x" {
		t.Errorf("config = %+v", c)
	}
}
