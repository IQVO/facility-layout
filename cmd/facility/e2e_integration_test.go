//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The end-to-end proof that the shipped binary is really wired (ADR 0039): the
// test boots the REAL entry point (run, in-process) against testcontainers
// Postgres and Kafka with nothing but the environment a deployment sets, and
// drives it only through REST. It reads the effects from Postgres and from the
// two produced topics (integration and analytics), replays a write to watch the
// idempotency, restarts the service on the same state, and finally takes the
// dependencies away to watch /readyz follow them.

const (
	topicEvents    = "warehouse.facility.events"
	topicAnalytics = "warehouse.facility.analytics"

	source     = "/warehouse/facility-layout"
	typePrefix = "com.warehouse.wms.facility-layout."
)

// containerStartTimeout bounds a testcontainers start.
const containerStartTimeout = 120 * time.Second

// The five occurrences the write phase produces, in order: the CloudEvents
// type entity segment, the event name and the aggregate key (= subject).
var wantOccurrences = []struct{ entity, name, key string }{
	{"site", "SiteRegistered", "WH1"},
	{"zone", "ZoneRegistered", "WH1-STOR-FRZ"},
	{"aisle", "AisleRegistered", "WH1-STOR-FRZ-A07"},
	{"locationtype", "LocationTypeRegistered", "PalletRack"},
	{"locationslot", "LocationSlotRegistered", "WH1-STOR-FRZ-A07-01-01-A"},
}

func startPostgres(t *testing.T) (*tcpostgres.PostgresContainer, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("facility_e2e"), tcpostgres.WithUsername("e2e"), tcpostgres.WithPassword("e2e"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}
	return c, url
}

func startKafka(t *testing.T) (*tckafka.KafkaContainer, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID(fmt.Sprintf("facility-layout-e2e-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatalf("start kafka: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	return c, brokers
}

// freeAddr reserves a loopback port for the service's listener.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// topicExists asks the broker for the topic through a metadata request that
// cannot create it (kafka-go's Client.Metadata never auto-creates).
func topicExists(t *testing.T, brokers []string, topic string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := (&kafkago.Client{Addr: kafkago.TCP(brokers...)}).Metadata(ctx, &kafkago.MetadataRequest{Topics: []string{topic}})
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	for _, tp := range resp.Topics {
		if tp.Name == topic && tp.Error == nil {
			return true
		}
	}
	return false
}

// createTopic creates a one-partition topic and waits for its leader.
func createTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
	}
	t.Fatalf("topic %s never got a leader", topic)
}

// consume reads exactly n messages of the one-partition topic from the start,
// bounded by a deadline.
func consume(t *testing.T, brokers []string, topic string, n int) []kafkago.Message {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	var out []kafkago.Message
	for len(out) < n {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read %s: got %d of %d messages: %v", topic, len(out), n, err)
		}
		out = append(out, m)
	}
	return out
}

// noMoreMessages fails if the topic holds more than want messages after a
// settling period (nothing was duplicated).
func noMoreMessages(t *testing.T, brokers []string, topic string, want int) {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer r.Close()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := 0
	for {
		if _, err := r.ReadMessage(ctx); err != nil {
			break
		}
		got++
	}
	if got != want {
		t.Fatalf("%s holds %d messages, want exactly %d", topic, got, want)
	}
}

// service is one running instance of the real entry point.
type service struct {
	t      *testing.T
	base   string
	cancel context.CancelFunc
	done   chan error
}

// env is the environment a deployment sets.
type env struct {
	dbURL   string
	brokers []string
	addr    string
}

func newEnv(t *testing.T, dbURL string, brokers []string) env {
	t.Helper()
	return env{dbURL: dbURL, brokers: brokers, addr: freeAddr(t)}
}

func (e env) apply(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", e.dbURL)
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("MIGRATIONS_PATH", "../../migrations")
	t.Setenv("KAFKA_BROKERS", strings.Join(e.brokers, ","))
	t.Setenv("EVENT_PUBLISHER", "kafka")
	t.Setenv("HTTP_ADDR", e.addr)
	t.Setenv("OUTBOX_RELAY_INTERVAL", "1s")
	t.Setenv("LOG_LEVEL", "warn")
}

// start boots run in-process and waits until the listener answers /healthz.
func (e env) start(t *testing.T) *service {
	t.Helper()
	e.apply(t)
	ctx, cancel := context.WithCancel(context.Background())
	s := &service{t: t, base: "http://" + e.addr, cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- run(ctx) }()
	t.Cleanup(s.stopQuietly)
	waitFor(t, 90*time.Second, "the service to listen on "+e.addr, func() bool {
		select {
		case err := <-s.done:
			t.Fatalf("run returned while booting: %v", err)
		default:
		}
		status, _ := s.try(http.MethodGet, "/healthz", "", nil)
		return status == http.StatusOK
	})
	return s
}

// stop cancels run and requires a clean shutdown within a bound.
func (s *service) stop() {
	s.t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil {
			s.t.Fatalf("shutdown returned %v", err)
		}
	case <-time.After(60 * time.Second):
		s.t.Fatal("run did not return after the shutdown")
	}
	s.done <- nil // let the cleanup find it stopped
}

func (s *service) stopQuietly() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(60 * time.Second):
	}
}

// try performs a request and returns -1 when the connection fails.
func (s *service) try(method, path, body string, headers map[string]string) (int, string) {
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		return -1, err.Error()
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return -1, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (s *service) call(method, path, body string, want int) string {
	s.t.Helper()
	return s.callWith(method, path, body, nil, want)
}

func (s *service) callWith(method, path, body string, headers map[string]string, want int) string {
	s.t.Helper()
	status, raw := s.try(method, path, body, headers)
	if status != want {
		s.t.Fatalf("%s %s = %d %s, want %d", method, path, status, raw, want)
	}
	return raw
}

// create POSTs a resource-creation request under an Idempotency-Key.
func (s *service) create(path, body, key string) string {
	s.t.Helper()
	return s.callWith(http.MethodPost, path, body, map[string]string{"Idempotency-Key": key}, http.StatusCreated)
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// pgView reads the service's own database.
type pgView struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func newPGView(t *testing.T, url string) pgView {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pgView{t: t, pool: pool}
}

func (v pgView) count(query string, args ...any) int {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	if err := v.pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		v.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (v pgView) unpublished() int {
	return v.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`)
}

// The CloudEvents context attributes of a published message, plus its data.
type envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            string          `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	DataSchema      string          `json:"dataschema"`
	Data            json.RawMessage `json:"data"`
}

func decodeEnvelope(t *testing.T, m kafkago.Message) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(m.Value, &e); err != nil {
		t.Fatalf("not a CloudEvent: %v: %s", err, m.Value)
	}
	return e
}

// assertEnvelope checks every context attribute of a message of this service.
func assertEnvelope(t *testing.T, m kafkago.Message, entity, eventName, stream, key string) envelope {
	t.Helper()
	e := decodeEnvelope(t, m)
	wantType := typePrefix + entity + "." + eventName
	schema := fmt.Sprintf("urn:warehouse:facility-layout:%s:%s:v1", stream, eventName)
	if e.SpecVersion != "1.0" || e.Type != wantType || e.Source != source ||
		e.DataSchema != schema || e.DataContentType != "application/json" || e.Subject != key || e.ID == "" {
		t.Fatalf("envelope = %+v, want 1.0 %s from %s (%s) about %s", e, wantType, source, schema, key)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
		t.Fatalf("time %q is not RFC 3339: %v", e.Time, err)
	}
	if string(m.Key) != key {
		t.Fatalf("kafka key = %q, want the aggregate key %q", m.Key, key)
	}
	var ct string
	for _, h := range m.Headers {
		if h.Key == "content-type" {
			ct = string(h.Value)
		}
	}
	if ct != "application/cloudevents+json; charset=UTF-8" {
		t.Fatalf("content-type header = %q", ct)
	}
	return e
}

func readyz(s *service) (int, string) { return s.try(http.MethodGet, "/readyz", "", nil) }

func waitReadyzStatus(t *testing.T, s *service, want int, what string) string {
	t.Helper()
	var body string
	waitFor(t, 60*time.Second, what, func() bool {
		var status int
		status, body = readyz(s)
		return status == want
	})
	return body
}

// Phase 1: the topics do not exist yet (a fresh cluster before anything
// published). Readiness needs Postgres plus a broker answering metadata, NOT
// topics, so the service is ready at once, and it creates no topic itself.
func phaseTopicsMissing(t *testing.T, s *service, brokers []string) {
	t.Helper()
	body := waitReadyzStatus(t, s, http.StatusOK, "/readyz to be 200 although no topic exists")
	if !strings.Contains(body, "ready") {
		t.Fatalf("readyz = %s", body)
	}
	for _, topic := range []string{topicEvents, topicAnalytics} {
		if topicExists(t, brokers, topic) {
			t.Fatalf("topic %s exists: the service must never create topics itself", topic)
		}
	}
	// The topics appear as the cluster broker would create them on first write.
	createTopic(t, brokers, topicEvents)
	createTopic(t, brokers, topicAnalytics)
	if status, body := readyz(s); status != http.StatusOK {
		t.Fatalf("readyz after the topics appeared = %d %s, want 200", status, body)
	}
}

// Phase 2: the write API, driven only through REST. Five occurrences.
func phaseWrites(t *testing.T, s *service) {
	t.Helper()
	s.create("/sites", `{"siteCode":"WH1","name":"Fulfilment Centre One"}`, "e2e-site-wh1")
	s.create("/sites/WH1/zones", `{"areaCode":"STOR","zoneCode":"FRZ","temperatureClass":"Frozen","hazmat":false}`, "e2e-zone-frz")
	s.create("/zones/WH1-STOR-FRZ/aisles", `{"aisleCode":"A07","sequenceHint":7,"direction":"TwoWay"}`, "e2e-aisle-a07")
	s.create("/location-types", `{"name":"PalletRack","defaultCapacity":{"maxWeightKg":1200,"maxVolumeM3":2.4}}`, "e2e-type-palletrack")
	s.create("/locations", `{"locationCode":"WH1-STOR-FRZ-A07-01-01-A","locationType":"PalletRack"}`, "e2e-slot-1")

	var slot struct {
		LocationCode string `json:"locationCode"`
		LocationType string `json:"locationType"`
	}
	if err := json.Unmarshal([]byte(s.call(http.MethodGet, "/locations/WH1-STOR-FRZ-A07-01-01-A", "", http.StatusOK)), &slot); err != nil {
		t.Fatal(err)
	}
	if slot.LocationCode != "WH1-STOR-FRZ-A07-01-01-A" || slot.LocationType != "PalletRack" {
		t.Fatalf("GET /locations/... = %+v", slot)
	}
	var sites []struct {
		SiteCode string `json:"siteCode"`
	}
	if err := json.Unmarshal([]byte(s.call(http.MethodGet, "/sites", "", http.StatusOK)), &sites); err != nil || len(sites) != 1 || sites[0].SiteCode != "WH1" {
		t.Fatalf("GET /sites = %+v (%v)", sites, err)
	}
}

// Phase 3: the rows are in Postgres, and the relay drained every one of them.
func phaseRows(t *testing.T, db pgView) {
	t.Helper()
	for _, q := range []string{
		`SELECT count(*) FROM sites WHERE code = 'WH1'`,
		`SELECT count(*) FROM zones WHERE id = 'WH1-STOR-FRZ' AND temperature_class = 'Frozen'`,
		`SELECT count(*) FROM aisles WHERE id = 'WH1-STOR-FRZ-A07'`,
		`SELECT count(*) FROM location_types WHERE name = 'PalletRack'`,
		`SELECT count(*) FROM location_slots WHERE code = 'WH1-STOR-FRZ-A07-01-01-A'`,
	} {
		if n := db.count(q); n != 1 {
			t.Fatalf("%s = %d, want 1", q, n)
		}
	}
	// Five occurrences, each written twice in the same transaction: the
	// integration topic and the analytics topic.
	if n := db.count(`SELECT count(*) FROM outbox_events`); n != 10 {
		t.Fatalf("outbox rows = %d, want 10", n)
	}
	waitFor(t, 60*time.Second, "the relay to publish every outbox row", func() bool { return db.unpublished() == 0 })
	for _, topic := range []string{topicEvents, topicAnalytics} {
		if n := db.count(`SELECT count(*) FROM outbox_events WHERE topic = $1`, topic); n != 5 {
			t.Fatalf("outbox rows for %s = %d, want 5", topic, n)
		}
	}
}

// Phase 4: the CloudEvents on both output topics, byte-exact envelope.
func phaseTopics(t *testing.T, brokers []string) {
	t.Helper()
	for _, tc := range []struct{ topic, stream string }{{topicEvents, "events"}, {topicAnalytics, "analytics"}} {
		msgs := consume(t, brokers, tc.topic, len(wantOccurrences))
		for i, m := range msgs {
			w := wantOccurrences[i]
			assertEnvelope(t, m, w.entity, w.name, tc.stream, w.key)
		}
	}
	// One occurrence, one CloudEvents id on both topics.
	ev, an := consume(t, brokers, topicEvents, 5), consume(t, brokers, topicAnalytics, 5)
	for i := range ev {
		if decodeEnvelope(t, ev[i]).ID != decodeEnvelope(t, an[i]).ID {
			t.Fatalf("occurrence %d has different ids on the two topics", i)
		}
	}
	var site struct {
		SiteCode string `json:"siteCode"`
		SiteName string `json:"siteName"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, ev[0]).Data, &site); err != nil || site.SiteCode != "WH1" || site.SiteName != "Fulfilment Centre One" {
		t.Fatalf("SiteRegistered data = %+v (%v)", site, err)
	}
	var slot struct {
		LocationCode string  `json:"locationCode"`
		AisleID      string  `json:"aisleId"`
		ZoneID       string  `json:"zoneId"`
		LocationType string  `json:"locationType"`
		MaxWeightKg  float64 `json:"maxWeightKg"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, ev[4]).Data, &slot); err != nil ||
		slot.LocationCode != "WH1-STOR-FRZ-A07-01-01-A" || slot.AisleID != "WH1-STOR-FRZ-A07" || slot.ZoneID != "WH1-STOR-FRZ" ||
		slot.LocationType != "PalletRack" || slot.MaxWeightKg != 1200 {
		t.Fatalf("LocationSlotRegistered data = %+v (%v)", slot, err)
	}
}

// Phase 5: replaying a creation under the same Idempotency-Key returns the
// stored response and adds no row and no event; the same site under a fresh key
// is a conflict; a creation without a key is refused.
func phaseReplay(t *testing.T, s *service, db pgView, brokers []string) {
	t.Helper()
	body := `{"siteCode":"WH1","name":"Fulfilment Centre One"}`
	s.create("/sites", body, "e2e-site-wh1")
	if body := s.callWith(http.MethodPost, "/sites", body, map[string]string{"Idempotency-Key": "e2e-site-wh1-again"}, http.StatusConflict); !strings.Contains(body, "duplicate-site-code") {
		t.Fatalf("a second WH1 under a new key = %s, want duplicate-site-code", body)
	}
	if body := s.call(http.MethodPost, "/sites", body, http.StatusBadRequest); !strings.Contains(body, "idempotency-key-required") {
		t.Fatalf("a creation without a key = %s, want idempotency-key-required", body)
	}
	if n := db.count(`SELECT count(*) FROM sites`); n != 1 {
		t.Fatalf("sites rows after the replay = %d, want 1", n)
	}
	if n := db.count(`SELECT count(*) FROM outbox_events`); n != 10 {
		t.Fatalf("outbox rows after the replay = %d, want still 10", n)
	}
	noMoreMessages(t, brokers, topicEvents, 5)
}

// Phase 6: restart the real entry point in-process on the same database and
// broker: nothing is lost, nothing is published twice.
func phaseRestart(t *testing.T, e env, s *service, db pgView, brokers []string) *service {
	t.Helper()
	s.stop()
	s2 := e.start(t)
	var site struct {
		SiteCode string `json:"siteCode"`
	}
	if err := json.Unmarshal([]byte(s2.call(http.MethodGet, "/sites/WH1", "", http.StatusOK)), &site); err != nil || site.SiteCode != "WH1" {
		t.Fatalf("after the restart WH1 = %+v (%v)", site, err)
	}
	s2.create("/sites", `{"siteCode":"WH2","name":"Overflow Site"}`, "e2e-site-wh2")
	waitFor(t, 60*time.Second, "the relay to publish WH2 after the restart", func() bool { return db.unpublished() == 0 })
	msgs := consume(t, brokers, topicEvents, 6)
	ids := map[string]bool{}
	for _, m := range msgs {
		ids[decodeEnvelope(t, m).ID] = true
	}
	if len(ids) != 6 {
		t.Fatalf("%d distinct CloudEvents ids over 6 messages: an event was published twice", len(ids))
	}
	noMoreMessages(t, brokers, topicEvents, 6)
	assertEnvelope(t, msgs[5], "site", "SiteRegistered", "events", "WH2")
	return s2
}

// Phase 7: /readyz follows the dependencies. Postgres goes first (Kafka still
// up): only postgres is named. Then Kafka goes: kafka is named too. /healthz
// stays liveness and answers 200 throughout.
func phaseDependenciesGoAway(t *testing.T, s *service, pg *tcpostgres.PostgresContainer, kc *tckafka.KafkaContainer) {
	t.Helper()
	stopTimeout := 10 * time.Second
	if err := pg.Stop(context.Background(), &stopTimeout); err != nil {
		t.Fatalf("stop postgres: %v", err)
	}
	body := waitReadyzStatus(t, s, http.StatusServiceUnavailable, "/readyz to turn 503 once Postgres is gone")
	if !strings.Contains(body, "dependency-unavailable") || !strings.Contains(body, "postgres") || strings.Contains(body, "kafka") {
		t.Fatalf("readyz with Postgres down = %s, want dependency-unavailable naming only postgres", body)
	}
	if err := kc.Stop(context.Background(), &stopTimeout); err != nil {
		t.Fatalf("stop kafka: %v", err)
	}
	waitFor(t, 60*time.Second, "/readyz to name kafka as well", func() bool {
		_, body := readyz(s)
		return strings.Contains(body, "postgres") && strings.Contains(body, "kafka")
	})
	if status, body := s.try(http.MethodGet, "/healthz", "", nil); status != http.StatusOK {
		t.Fatalf("healthz with every dependency down = %d %s, want 200 (liveness only)", status, body)
	}
}

func TestRunOnRealPostgresAndKafka(t *testing.T) {
	pg, dbURL := startPostgres(t)
	kc, brokers := startKafka(t)
	e := newEnv(t, dbURL, brokers)
	db := newPGView(t, dbURL)

	s := e.start(t)
	phaseTopicsMissing(t, s, brokers)
	phaseWrites(t, s)
	phaseRows(t, db)
	phaseTopics(t, brokers)
	phaseReplay(t, s, db, brokers)
	s = phaseRestart(t, e, s, db, brokers)
	phaseDependenciesGoAway(t, s, pg, kc)
}
