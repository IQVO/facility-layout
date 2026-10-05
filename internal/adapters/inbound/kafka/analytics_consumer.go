// Package kafka contains the inbound Kafka adapters. Alongside the OLTP
// consumer, analytics_consumer.go consumes the analytics topic and projects the
// facility-layout "Layout Catalog Growth & Change" read model.
//
// Consistent with the ADR-0009 integration publisher and the ADR-0010 analytics
// publisher, the analytics pipeline is trace-free: facility-layout has no
// observability/OTel package, so this consumer opens no spans and reads no trace
// headers.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/facility-layout/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/facility-layout/internal/analytics/report"
)

// AnalyticsConsumerGroup is the Kafka consumer group the analytics projector
// reads under. It is distinct from any OLTP consumer group so the two pipelines
// track their offsets independently.
const AnalyticsConsumerGroup = "facility-analytics"

// dlqTopicSuffix names the dead-letter topic this consumer publishes a poison
// message to, relative to its OWN source topic (never a fixed constant) — see
// ADR-0020's DLQ section, which copies order-management's ADR-0025 §DLQ
// pattern verbatim for this consumer. NewAnalyticsConsumer's isolated test
// topics each get their own matching "<topic>.dlq" the same way.
const dlqTopicSuffix = ".dlq"

// maxHandlerAttempts bounds HandleMessage's in-process retry (ADR-0020 §DLQ)
// before a message is dead-lettered: 1 initial attempt plus up to 2 retries.
const maxHandlerAttempts = 3

const (
	retryInitialInterval = 100 * time.Millisecond
	retryMaxInterval     = 2 * time.Second
)

// ProcessedEvents is the consumer's idempotency gate. IsProcessed is a
// read-only pre-check: HandleMessage consults it BEFORE applying, so a
// genuine at-least-once redelivery of an event that was already fully
// applied is skipped without re-running Apply. MarkProcessed then records an
// event id as applied — HandleMessage calls it only AFTER Apply has
// succeeded (see HandleMessage's doc comment for why apply-then-claim, not
// claim-then-apply, is required for ADR-0020 §DLQ's in-process retry to
// actually retry). It is declared here (rather than in application/ports)
// because it is an analytics-only concern the OLTP layers never touch; the
// analyticsstore ConsumedEventsRepo implements it.
type ProcessedEvents interface {
	// IsProcessed reports whether eventId has already been recorded by a
	// prior, successful MarkProcessed call.
	IsProcessed(ctx context.Context, eventId string) (bool, error)
	// MarkProcessed records eventId as applied. Its bool return is kept
	// for callers that still want "was this the first recording" (true
	// under normal use, since HandleMessage always checks IsProcessed
	// first); ON CONFLICT DO NOTHING makes a duplicate call harmless
	// either way.
	MarkProcessed(ctx context.Context, eventId string) (bool, error)
}

// analyticsData is the union of fields the projecting event payloads carry (the
// domain events' own camelCase JSON, carried verbatim as the CloudEvent data).
// Each event type populates the subset it needs.
type analyticsData struct {
	SiteCode      string `json:"siteCode"`
	ZoneID        string `json:"zoneId"`
	AisleID       string `json:"aisleId"`
	LocationCode  string `json:"locationCode"`
	LocationType  string `json:"locationType"`
	RuleID        string `json:"ruleId"`
	RowsSubmitted int    `json:"rowsSubmitted"`
	SlotsImported int    `json:"slotsImported"`
	RowsRejected  int    `json:"rowsRejected"`
}

// AnalyticsConsumer reads analytics events off the analytics topic and applies
// each to the catalog-growth ProjectionStore, exactly once per CloudEvents id
// despite Kafka's at-least-once delivery.
type AnalyticsConsumer struct {
	Reader     *kafkago.Reader
	Projection report.ProjectionStore
	Processed  ProcessedEvents
	Logger     *slog.Logger
	// dlqWriter publishes a poison message (ADR-0020 §DLQ, mirroring
	// order-management's ADR-0025 §DLQ verbatim) to topic+dlqTopicSuffix
	// after maxHandlerAttempts in-process retries of HandleMessage all
	// fail. nil in a zero-value struct built directly by unit tests that
	// exercise HandleMessage in isolation (they never reach Run's DLQ
	// path) — dlqPublish itself guards against a nil writer so those
	// tests keep compiling/passing unchanged.
	dlqWriter *kafkago.Writer
}

// ConsumerOption customises an AnalyticsConsumer beyond its required
// dependencies.
type ConsumerOption func(*AnalyticsConsumer)

// WithDLQWriter overrides the consumer's dead-letter writer. The composition
// root uses this to inject the fleet-shared durable writer built by
// outbound/kafka.NewDeadLetterWriter (RequireAll + Hash + 10ms — a DLQ
// publish that reports success before the broker stores the message, after
// which the source offset is committed, is a silently lost poison message).
// The inbound package cannot import the outbound one (arch-go fitness rule),
// so the default fallback writer below re-states the same pinned config.
func WithDLQWriter(w *kafkago.Writer) ConsumerOption {
	return func(c *AnalyticsConsumer) { c.dlqWriter = w }
}

// NewAnalyticsConsumer constructs an AnalyticsConsumer reading topic from
// brokers under AnalyticsConsumerGroup, with a dead-letter writer targeting
// topic+".dlq" (ADR-0020 §DLQ).
func NewAnalyticsConsumer(brokers []string, topic string, projection report.ProjectionStore, processed ProcessedEvents, logger *slog.Logger, opts ...ConsumerOption) *AnalyticsConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: AnalyticsConsumerGroup,
		// Start a brand-new consumer group at the EARLIEST offset. The analytics
		// projection must see the full history of the topic (it is a replayable
		// read model, not a live integration reaction), so a fresh projector — or
		// a backfill into a new group — reads from the beginning rather than
		// kafka-go's default of the latest offset, which would silently drop
		// every event produced before the group first committed an offset. Once
		// the group has committed offsets, those take precedence and this only
		// affects the first join.
		StartOffset: kafkago.FirstOffset,
	})
	c := &AnalyticsConsumer{
		Reader:     reader,
		Projection: projection,
		Processed:  processed,
		Logger:     logger,
		dlqWriter:  newDLQWriter(brokers, topic+dlqTopicSuffix),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// newDLQWriter builds the dead-letter writer fallback for dlqTopic. It sets
// AllowAutoTopicCreation, the fleet convention for every writer
// (warehouse-infra/terraform/kafka.tf leaves topic creation to the
// producing writer): "<topic>.dlq" is only ever written on the rare
// poison-message path, so it usually does not exist yet when it is first
// needed. Without the flag that first dead-letter write fails with
// "[3] Unknown Topic Or Partition", the offset is (correctly) not
// committed, and Run aborts, stopping the projector on the very message
// the DLQ exists to route around.
//
// Durability (ADR-0020): RequiredAcks RequireAll — the kafka-go default
// RequireNone lets a DLQ publish report success before the broker stores
// the message, after which the source offset IS committed: the poison
// message is lost. BatchTimeout 10ms (a DLQ write is a synchronous single
// message; the 1s default caps dead-lettering at ~1 msg/s/partition) and
// the Hash balancer (the original message key still decides the partition,
// preserving per-key order on the .dlq topic) mirror the fleet writer
// config in outbound/kafka/writer_config.go, whose NewDeadLetterWriter the
// composition root injects in production. This package cannot import that
// one (arch fitness rule: inbound never depends on outbound), so the config
// is re-stated here and pinned by TestNewDLQWriter_MatchesFleetSyncWriterConfig.
func newDLQWriter(brokers []string, dlqTopic string) *kafkago.Writer {
	return &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Topic:                  dlqTopic,
		Balancer:               &kafkago.Hash{},
		RequiredAcks:           kafkago.RequireAll,
		AllowAutoTopicCreation: true,
		// BatchTimeout: a DLQ write is a synchronous single message; with
		// kafka-go's 1s default the writer holds every write for a full second
		// waiting to fill a batch, capping dead-lettering at ~1 msg/s/partition
		// (observed live: a backlog of legacy messages took hours to drain while
		// the consumer processed nothing else).
		BatchTimeout: dlqBatchTimeout,
	}
}

// Run fetches and handles messages until ctx is cancelled or the reader
// returns a fatal error. Unlike a plain ReadMessage loop (which commits the
// offset BEFORE the handler ever runs when a GroupID is configured), Run
// explicitly fetches, retries the handler in-process, and only commits after
// either a success or a dead-letter publish (ADR-0020 §DLQ) — so a message
// whose handler keeps failing is retried up to maxHandlerAttempts times and
// then dead-lettered, rather than being silently dropped or wedging the
// partition.
func (c *AnalyticsConsumer) Run(ctx context.Context) error {
	for {
		msg, err := c.Reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := c.handleMessage(ctx, msg); err != nil {
			return err
		}
	}
}

// handleMessage retries HandleMessage(msg.Value) up to maxHandlerAttempts
// times with jittered exponential backoff; once all attempts are exhausted
// it dead-letters the raw message (ADR-0020 §DLQ) and commits the offset
// anyway — one poison message must never permanently block every event
// behind it on this partition. Only a commit failure or a DLQ publish
// failure aborts the consume loop.
//
// A value that is not a valid CloudEvents 1.0 event (ErrNotCloudEvent,
// e.g. a retired flat-envelope message) is deterministic poison: it skips
// the retries and is dead-lettered immediately (ADR-0024).
func (c *AnalyticsConsumer) handleMessage(ctx context.Context, msg kafkago.Message) error {
	err := c.handleWithRetry(ctx, msg.Value)
	if err == nil {
		return c.commit(ctx, msg)
	}
	if errors.Is(err, cloudevents.ErrNotCloudEvent) {
		c.Logger.WarnContext(ctx, "analytics: message is not a valid CloudEvent, sending to dead-letter topic",
			"topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset, "error", err)
		if dlqErr := c.dlqPublish(ctx, msg, err); dlqErr != nil {
			return fmt.Errorf("analytics: publish to dead-letter topic: %w", dlqErr)
		}
		return c.commit(ctx, msg)
	}

	c.Logger.ErrorContext(ctx, "analytics: exhausted retries, sending to dead-letter topic",
		"topic", c.Reader.Config().Topic, "dlq_topic", c.Reader.Config().Topic+dlqTopicSuffix,
		"attempts", maxHandlerAttempts, "error", err)
	if dlqErr := c.dlqPublish(ctx, msg, err); dlqErr != nil {
		return fmt.Errorf("analytics: publish to dead-letter topic: %w", dlqErr)
	}
	return c.commit(ctx, msg)
}

// handleWithRetry retries HandleMessage with jittered exponential backoff
// (ADR-0020 §DLQ), bounded by ctx's own deadline/cancellation.
func (c *AnalyticsConsumer) handleWithRetry(ctx context.Context, raw []byte) error {
	policy := backoff.NewExponentialBackOff(
		backoff.WithInitialInterval(retryInitialInterval),
		backoff.WithMaxInterval(retryMaxInterval),
	)
	bounded := backoff.WithContext(backoff.WithMaxRetries(policy, maxHandlerAttempts-1), ctx)

	return backoff.Retry(func() error {
		err := c.HandleMessage(ctx, raw)
		if errors.Is(err, cloudevents.ErrNotCloudEvent) {
			return backoff.Permanent(err)
		}
		return err
	}, bounded)
}

// dlqPublish writes the raw, unmodified message payload plus error context
// (as headers, so the raw body stays byte-identical for a manual replay
// tool) to the dead-letter topic. A nil dlqWriter (the zero-value
// AnalyticsConsumer some unit tests construct directly, which never
// exercises this path) is a documented no-op rather than a nil-pointer
// panic.
func (c *AnalyticsConsumer) dlqPublish(ctx context.Context, msg kafkago.Message, cause error) error {
	if c.dlqWriter == nil {
		return nil
	}
	headers := append([]kafkago.Header{}, msg.Headers...)
	headers = append(headers,
		kafkago.Header{Key: "x-dlq-source-topic", Value: []byte(c.Reader.Config().Topic)},
		kafkago.Header{Key: "x-dlq-error", Value: []byte(cause.Error())},
		kafkago.Header{Key: "x-dlq-failed-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	)
	return writeDLQ(ctx, c.dlqWriter, kafkago.Message{
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: headers,
	})
}

// commit acknowledges msg so it is never redelivered. Only a commit failure
// itself aborts the consume loop.
func (c *AnalyticsConsumer) commit(ctx context.Context, msg kafkago.Message) error {
	return c.Reader.CommitMessages(ctx, msg)
}

// Close releases the underlying Kafka reader and, if configured, the DLQ
// writer.
func (c *AnalyticsConsumer) Close() error {
	readerErr := c.Reader.Close()
	if c.dlqWriter == nil {
		return readerErr
	}
	return errors.Join(readerErr, c.dlqWriter.Close())
}

// HandleMessage decodes raw as a CloudEvents 1.0 event (cloudevents.Decode,
// which validates) and applies the matching projection method for its FULL
// `type`. A value that is not a valid CloudEvent (including the retired flat
// envelope) returns an error wrapping cloudevents.ErrNotCloudEvent, which
// Run dead-letters without retrying -- it is never parsed as a legacy shape.
// Types outside the projection contract are ignored (and not marked
// processed). For a projecting event it first consults
// ProcessedEvents.IsProcessed with the CloudEvents `id` (a read-only check)
// to skip a genuine redelivery of an event already fully applied, then
// applies, then -- only once Apply has actually succeeded -- calls
// MarkProcessed. It is exported separately from Run so tests can feed raw
// events without a live broker.
//
// Note on ordering (apply-then-claim is deliberate, not incidental):
// PostgresProjection.Apply* is already idempotent per event id (it claims
// analytics_processed_events inside the SAME transaction as its effect), so
// calling it more than once for the same id on a retry is always safe. The
// OLDER claim-BEFORE-apply order (MarkProcessed, then Apply) had exactly the
// bug ADR-0020 §DLQ exists to prevent: if Apply then failed, the in-process
// retry's next attempt saw the event already marked processed and returned
// nil WITHOUT ever calling Apply again -- silently treating a still-failing
// poison message as handled instead of genuinely retrying it and eventually
// dead-lettering it. Checking IsProcessed (read-only, no side effect) up
// front instead gives the same at-least-once-redelivery short-circuit
// without creating that false-claim race.
func (c *AnalyticsConsumer) HandleMessage(ctx context.Context, raw []byte) error {
	evt, err := cloudevents.Decode(raw)
	if err != nil {
		return fmt.Errorf("analytics: %w", err)
	}

	// The report is derived from the catalog-change event set. Any other
	// type is acknowledged without touching the read model or the
	// processed set, so a later contract change could still reprocess it.
	if !isCatalogChangeEvent(evt.Type()) {
		return nil
	}

	alreadyProcessed, err := c.Processed.IsProcessed(ctx, evt.ID())
	if err != nil {
		return fmt.Errorf("analytics: check processed: %w", err)
	}
	if alreadyProcessed {
		return nil
	}

	var data analyticsData
	if err := evt.DataAs(&data); err != nil {
		return fmt.Errorf("analytics: decode data: %w", err)
	}

	if err := c.applyCatalogChange(ctx, evt, data); err != nil {
		return err
	}

	// Record consumption only now that Apply has actually succeeded. A
	// MarkProcessed failure here is reported (so at-least-once delivery
	// can redeliver and re-run this now-safe-to-repeat apply) rather than
	// silently swallowed.
	if _, err := c.Processed.MarkProcessed(ctx, evt.ID()); err != nil {
		return fmt.Errorf("analytics: mark processed: %w", err)
	}
	return nil
}

// The full CloudEvents `type` strings this consumer projects (ADR-0024).
// Dispatch is on the exact string, never a suffix or a short name.
var (
	typeSiteRegistered             = cloudevents.Type("site", "SiteRegistered")
	typeZoneRegistered             = cloudevents.Type("zone", "ZoneRegistered")
	typeAisleRegistered            = cloudevents.Type("aisle", "AisleRegistered")
	typeLocationTypeRegistered     = cloudevents.Type("locationtype", "LocationTypeRegistered")
	typePlacementRuleDefined       = cloudevents.Type("placementrule", "PlacementRuleDefined")
	typeLocationSlotRegistered     = cloudevents.Type("locationslot", "LocationSlotRegistered")
	typeLocationSlotDecommissioned = cloudevents.Type("locationslot", "LocationSlotDecommissioned")
	typeFacilityLayoutImported     = cloudevents.Type("locationslot", "FacilityLayoutImported")
)

// isCatalogChangeEvent reports whether the full CloudEvents type belongs to
// the catalog-change event set the Layout Catalog Growth & Change report
// derives from.
func isCatalogChangeEvent(eventType string) bool {
	switch eventType {
	case typeSiteRegistered, typeZoneRegistered, typeAisleRegistered,
		typeLocationTypeRegistered, typePlacementRuleDefined,
		typeLocationSlotRegistered, typeLocationSlotDecommissioned,
		typeFacilityLayoutImported:
		return true
	default:
		return false
	}
}

// applyCatalogChange routes one decoded, deduped CloudEvent to its projection
// method, using the event's `id` and `time` attributes. Scope follows the
// event: site-scoped events carry the site code, slot events the zone derived
// from the location code, and catalog-wide definitions (location types,
// placement rules, imports) land in the empty catalog-wide scope.
func (c *AnalyticsConsumer) applyCatalogChange(ctx context.Context, evt ce.Event, data analyticsData) error {
	id, at := evt.ID(), evt.Time()
	switch evt.Type() {
	case typeSiteRegistered:
		return c.Projection.ApplySiteRegistered(ctx, id, data.SiteCode, at)
	case typeZoneRegistered:
		// A zone is a growth of its site, so it is scoped to the site code.
		return c.Projection.ApplyZoneRegistered(ctx, id, data.SiteCode, at)
	case typeAisleRegistered:
		return c.Projection.ApplyAisleRegistered(ctx, id, data.ZoneID, at)
	case typeLocationTypeRegistered:
		// A location type is a catalog-wide definition, not scoped to a site or
		// zone: it lands in the empty catalog-wide scope.
		return c.Projection.ApplyLocationTypeRegistered(ctx, id, "", at)
	case typePlacementRuleDefined:
		return c.Projection.ApplyPlacementRuleDefined(ctx, id, "", at)
	case typeLocationSlotRegistered:
		return c.Projection.ApplyLocationSlotRegistered(ctx, id, zoneOf(data.LocationCode), at)
	case typeLocationSlotDecommissioned:
		return c.Projection.ApplyLocationSlotDecommissioned(ctx, id, zoneOf(data.LocationCode), at)
	case typeFacilityLayoutImported:
		return c.Projection.ApplyFacilityLayoutImported(ctx, id, "", data.RowsSubmitted, data.SlotsImported, data.RowsRejected, at)
	default:
		return nil
	}
}

// zoneOf derives the zone id (SITE-AREA-ZONE) from a full location code by
// keeping its first three hyphen-joined segments. This mirrors the domain's
// LocationCode.ZoneID() without importing the domain: the consumer stays free of
// any OLTP dependency. An unexpectedly short code is returned as-is.
func zoneOf(locationCode string) string {
	parts := strings.Split(locationCode, "-")
	if len(parts) < 3 {
		return locationCode
	}
	return strings.Join(parts[:3], "-")
}

// dlqTopicReadyAttempts / dlqTopicReadyBackoff bound how long a DLQ publish
// waits for an auto-created "<topic>.dlq" to become writable.
const (
	dlqTopicReadyAttempts = 40
	dlqTopicReadyBackoff  = 250 * time.Millisecond
)

// dlqMessageWriter is the slice of *kafkago.Writer writeDLQ needs.
type dlqMessageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// writeDLQ publishes msg to the dead-letter topic, retrying (bounded) while
// the topic is still being auto-created. AllowAutoTopicCreation alone is not
// enough: the first write races partition leader election and the broker
// answers UnknownTopicOrPartition / LeaderNotAvailable for a few hundred
// milliseconds. Any other error -- or exhausting the budget -- is returned,
// so the caller still refuses to commit the offset (no message loss).
func writeDLQ(ctx context.Context, w dlqMessageWriter, msg kafkago.Message) error {
	var err error
	for attempt := 0; attempt < dlqTopicReadyAttempts; attempt++ {
		if err = w.WriteMessages(ctx, msg); err == nil || !isTopicNotReady(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(dlqTopicReadyBackoff):
		}
	}
	return err
}

// isTopicNotReady reports whether err only means the (auto-created) topic
// has no leader yet.
func isTopicNotReady(err error) bool {
	var werrs kafkago.WriteErrors
	if errors.As(err, &werrs) {
		for _, e := range werrs {
			if e != nil && !isTopicNotReady(e) {
				return false
			}
		}
		return werrs.Count() > 0
	}
	return errors.Is(err, kafkago.UnknownTopicOrPartition) || errors.Is(err, kafkago.LeaderNotAvailable)
}

// dlqBatchTimeout flushes a dead-letter write almost immediately.
const dlqBatchTimeout = 10 * time.Millisecond
