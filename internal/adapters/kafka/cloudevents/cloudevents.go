// Package cloudevents is this service's single home for the fleet-mandatory
// CloudEvents 1.0 envelope (see the "CloudEvents 1.0 as the mandatory event
// envelope" ADR). Every Kafka publisher and consumer in the service goes
// through New / Decode / ContentTypeHeader — nothing else hand-builds an
// envelope.
//
// REFERENCE IMPLEMENTATION shared across the fleet. Copy into
// internal/adapters/kafka/cloudevents/cloudevents.go and change ONLY the
// four constants marked "per repo".
package cloudevents

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"
)

const (
	// Per repo.
	Repo      = "facility-layout"
	Subdomain = "wms"
	Context   = "facility-layout" // bounded-context segment of `type`
	Source    = "/warehouse/" + Repo

	SpecVersion     = ce.CloudEventsVersionV1 // "1.0"
	DataContentType = "application/json"
	// MediaType is the Kafka `content-type` header value for structured mode.
	MediaType = "application/cloudevents+json; charset=UTF-8"

	StreamEvents    = "events"
	StreamAnalytics = "analytics"
)

// ErrNotCloudEvent marks a message that is not a valid CloudEvents 1.0 event.
// It is deterministic: consumers DLQ/skip it, never retry it.
var ErrNotCloudEvent = errors.New("cloudevents: message is not a valid CloudEvents 1.0 event")

// Type builds com.warehouse.<subdomain>.<context>.<entity>.<EventName>.
func Type(entity, eventName string) string {
	return fmt.Sprintf("com.warehouse.%s.%s.%s.%s", Subdomain, Context, entity, eventName)
}

// DataSchema builds urn:warehouse:<repo>:<stream>:<EventName>:v<version>.
func DataSchema(stream, eventName string, version int) string {
	return fmt.Sprintf("urn:warehouse:%s:%s:%s:v%d", Repo, stream, eventName, version)
}

// Spec describes one event occurrence to encode.
type Spec struct {
	ID        string    // UUID, minted once per occurrence, stable across outbox redelivery
	Entity    string    // aggregate segment of `type`, e.g. "order"
	EventName string    // PascalCase domain event name, e.g. "OrderAllocated"
	Subject   string    // aggregate instance id
	Time      time.Time // domain occurred-at
	Stream    string    // StreamEvents | StreamAnalytics
	Version   int       // dataschema version, >= 1
	Data      any       // payload, marshalled as JSON
}

// New builds and validates the CloudEvent and returns its structured-mode
// JSON bytes (the Kafka message value).
func New(s Spec) ([]byte, error) {
	if s.Subject == "" {
		return nil, fmt.Errorf("cloudevents: %s: empty subject", s.EventName)
	}
	if s.Version < 1 {
		s.Version = 1
	}
	e := ce.New(SpecVersion)
	e.SetID(s.ID)
	e.SetSource(Source)
	e.SetType(Type(s.Entity, s.EventName))
	e.SetSubject(s.Subject)
	e.SetTime(s.Time.UTC())
	e.SetDataSchema(DataSchema(s.Stream, s.EventName, s.Version))
	if err := e.SetData(DataContentType, s.Data); err != nil {
		return nil, fmt.Errorf("cloudevents: set data: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, fmt.Errorf("cloudevents: invalid event: %w", err)
	}
	return json.Marshal(e)
}

// ContentTypeHeader is the Kafka header every produced message carries.
func ContentTypeHeader() kafkago.Header {
	return kafkago.Header{Key: "content-type", Value: []byte(MediaType)}
}

// Decode parses and validates a structured-mode message value. Anything that
// is not a valid CloudEvents 1.0 event (including the retired flat envelope)
// yields an error wrapping ErrNotCloudEvent.
func Decode(value []byte) (ce.Event, error) {
	var e ce.Event
	if err := json.Unmarshal(value, &e); err != nil {
		return ce.Event{}, fmt.Errorf("%w: %v", ErrNotCloudEvent, err)
	}
	if e.SpecVersion() != SpecVersion {
		return ce.Event{}, fmt.Errorf("%w: specversion %q", ErrNotCloudEvent, e.SpecVersion())
	}
	if err := e.Validate(); err != nil {
		return ce.Event{}, fmt.Errorf("%w: %v", ErrNotCloudEvent, err)
	}
	return e, nil
}
