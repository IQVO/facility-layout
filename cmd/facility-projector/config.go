package main

import (
	"errors"
	"strings"
)

// Boot errors of the projector's mandatory configuration. Each names its
// variable (ADR 0039): the projector writes only to the analytical database and
// consumes only from real Kafka, with no default behind either.
var (
	errMissingAnalyticsURL = errors.New("ANALYTICS_DATABASE_URL is required: the projector writes only to the analytical database")
	errMissingBrokers      = errors.New("KAFKA_BROKERS is required: the projector consumes the analytics topic from real Kafka")
	errMissingGroup        = errors.New("ANALYTICS_CONSUMER_GROUP is required: a stable Kafka group id for the projector; there is no default")
)

// config is the validated mandatory environment of the projector.
type config struct {
	analyticsURL string
	brokers      []string
	group        string
}

// parseBrokers splits KAFKA_BROKERS (comma separated), trimming blanks; nil
// when nothing is configured (there is no localhost default).
func parseBrokers(raw string) []string {
	var brokers []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			brokers = append(brokers, b)
		}
	}
	return brokers
}

// loadConfig reads and validates the mandatory environment through getenv
// (os.Getenv in main). Every problem is reported at once, each naming its
// variable, so the process fails fast at boot, before it opens a connection or
// binds the admin listener.
func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		analyticsURL: getenv("ANALYTICS_DATABASE_URL"),
		brokers:      parseBrokers(getenv("KAFKA_BROKERS")),
		group:        strings.TrimSpace(getenv("ANALYTICS_CONSUMER_GROUP")),
	}
	var errs []error
	if c.analyticsURL == "" {
		errs = append(errs, errMissingAnalyticsURL)
	}
	if len(c.brokers) == 0 {
		errs = append(errs, errMissingBrokers)
	}
	if c.group == "" {
		errs = append(errs, errMissingGroup)
	}
	if err := errors.Join(errs...); err != nil {
		return config{}, err
	}
	return c, nil
}
