package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/events"
	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
	"github.com/claudioed/facility-layout/internal/application/ports"
	"github.com/claudioed/facility-layout/internal/application/usecases"
	"github.com/claudioed/facility-layout/internal/domain/placement"
	"github.com/claudioed/facility-layout/internal/domain/shared"
	"github.com/claudioed/facility-layout/internal/domain/site"
)

var base = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

// harness builds Deps over in-memory repos, seeding the map through the REAL
// write use cases with a fixed clock -- the same approach as the MCP adapter's
// harness. The write use cases exist ONLY in the test: Deps carries reads.
type harness struct {
	t    *testing.T
	deps Deps

	sites *memory.SiteRepo

	registerSite         *usecases.RegisterSite
	registerZone         *usecases.RegisterZone
	registerAisle        *usecases.RegisterAisle
	registerLocationType *usecases.RegisterLocationType
	registerSlot         *usecases.RegisterLocationSlot
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	sites := memory.NewSiteRepo()
	zones := memory.NewZoneRepo()
	aisles := memory.NewAisleRepo()
	slots := memory.NewSlotRepo()
	structures := memory.NewFixedStructureRepo()
	locationTypes := memory.NewLocationTypeRepo()
	rules := memory.NewPlacementRuleRepo()
	crossAisles := memory.NewCrossAisleRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(base)

	return &harness{
		t:     t,
		sites: sites,
		deps: Deps{
			GetSiteLayout:          &usecases.GetSiteLayout{Sites: sites, Zones: zones, Aisles: aisles, Slots: slots, Structures: structures},
			EstimateTravelDistance: &usecases.EstimateTravelDistance{Zones: zones, Aisles: aisles, Slots: slots, CrossAisles: crossAisles},
		},
		registerSite:         &usecases.RegisterSite{Sites: sites, Events: publisher, Clock: clock},
		registerZone:         &usecases.RegisterZone{Sites: sites, Zones: zones, Events: publisher, Clock: clock},
		registerAisle:        &usecases.RegisterAisle{Zones: zones, Aisles: aisles, Events: publisher, Clock: clock},
		registerLocationType: &usecases.RegisterLocationType{LocationTypes: locationTypes, Events: publisher, Clock: clock},
		registerSlot: &usecases.RegisterLocationSlot{
			Sites: sites, Zones: zones, Aisles: aisles, Slots: slots,
			LocationTypes: locationTypes, Rules: rules, Events: publisher, Clock: clock,
		},
	}
}

func (h *harness) ctx() context.Context { return context.Background() }

// seedDrawableSite builds a small WH1: two zones, aisles registered out of
// walk order (A09 before A07), and a handful of slots.
func (h *harness) seedDrawableSite() {
	h.t.Helper()
	if _, err := h.registerSite.Execute(h.ctx(), "WH1", "Fulfilment Centre One", site.RoleUnspecified); err != nil {
		h.t.Fatalf("seeding site: %v", err)
	}
	capacity, err := shared.NewCapacity(1200, 2.4)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.registerLocationType.Execute(h.ctx(), placement.PalletRack, placement.Storage, capacity); err != nil {
		h.t.Fatalf("seeding location type: %v", err)
	}
	for _, z := range [][2]string{{"STOR", "AMB"}, {"RCV", "AMB"}} {
		if _, err := h.registerZone.Execute(h.ctx(), "WH1", z[0], z[1], shared.Ambient, false, nil, nil); err != nil {
			h.t.Fatalf("seeding zone: %v", err)
		}
	}
	for _, a := range []struct {
		zone, code string
		hint       int
		dir        shared.Direction
	}{
		{"WH1-STOR-AMB", "A09", 9, shared.OneWay},
		{"WH1-STOR-AMB", "A07", 7, shared.TwoWay},
		{"WH1-RCV-AMB", "D01", 1, shared.TwoWay},
	} {
		if _, err := h.registerAisle.Execute(h.ctx(), a.zone, a.code, a.hint, a.dir); err != nil {
			h.t.Fatalf("seeding aisle: %v", err)
		}
	}
	for _, raw := range []string{
		"WH1-STOR-AMB-A07-01-01-A",
		"WH1-STOR-AMB-A07-02-01-A",
		"WH1-STOR-AMB-A09-01-01-A",
		"WH1-RCV-AMB-D01-01-01-A",
	} {
		code, err := shared.ParseLocationCode(raw)
		if err != nil {
			h.t.Fatal(err)
		}
		if _, err := h.registerSlot.Execute(h.ctx(), code, placement.PalletRack, shared.Capacity{}, "", nil); err != nil {
			h.t.Fatalf("seeding slot %q: %v", raw, err)
		}
	}
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		SiteLayout *struct {
			Site struct {
				Code string `json:"code"`
				Name string `json:"name"`
			} `json:"site"`
			Zones []struct {
				ZoneID string `json:"zoneId"`
				Aisles []struct {
					AisleCode string   `json:"aisleCode"`
					SlotCodes []string `json:"slotCodes"`
				} `json:"aisles"`
			} `json:"zones"`
		} `json:"siteLayout"`
		TravelDistance *struct {
			From      string  `json:"from"`
			To        string  `json:"to"`
			MetresM   float64 `json:"metresM"`
			Estimated bool    `json:"estimated"`
			Route     []struct {
				AisleID string `json:"aisleId"`
				Bay     string `json:"bay"`
			} `json:"route"`
		} `json:"travelDistance"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wire struct {
	client *a2aclient.Client
	url    string
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client, url: srv.URL}
}

// answerOf is what one request returned: the decoded envelope, the one-line
// text part, and the raw reply message (to scan every part).
type answerOf struct {
	env  wireEnvelope
	text string
	msg  *a2a.Message
}

// call sends one message part and returns the decoded answer.
func (w *wire) call(t *testing.T, part *a2a.Part) answerOf {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return answerOf{env: env, text: string(text), msg: msg}
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) answerOf {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, a answerOf, status int, slug string) {
	t.Helper()
	if a.env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, a.env.Data)
	}
	if a.env.Problem.Status != status || a.env.Problem.Type != problemTypeBase+"/"+slug {
		t.Fatalf("problem = %+v, want %d %s", a.env.Problem, status, slug)
	}
}

func TestWireSiteLayoutReadsTheMapInWalkOrder(t *testing.T) {
	h := newHarness(t)
	h.seedDrawableSite()
	w := newWire(t, h.deps)

	a := w.skill(t, skillSiteLayout, map[string]any{"siteCode": "WH1"})

	if a.env.Skill != skillSiteLayout || a.env.Problem != nil || a.env.Data.SiteLayout == nil {
		t.Fatalf("envelope = %+v", a.env)
	}
	l := a.env.Data.SiteLayout
	if l.Site.Code != "WH1" || l.Site.Name != "Fulfilment Centre One" {
		t.Errorf("site = %+v", l.Site)
	}
	if len(l.Zones) != 2 || l.Zones[0].ZoneID != "WH1-RCV-AMB" || l.Zones[1].ZoneID != "WH1-STOR-AMB" {
		t.Fatalf("zones = %+v, want RCV then STOR", l.Zones)
	}
	stor := l.Zones[1]
	if len(stor.Aisles) != 2 || stor.Aisles[0].AisleCode != "A07" || stor.Aisles[1].AisleCode != "A09" {
		t.Fatalf("STOR aisles = %+v, want A07 before A09 (walk order, not registration order)", stor.Aisles)
	}
	if got := stor.Aisles[0].SlotCodes; len(got) != 2 || got[0] != "WH1-STOR-AMB-A07-01-01-A" {
		t.Errorf("A07 slots = %v", got)
	}
	if want := "WH1 (Fulfilment Centre One): 2 zones, 3 aisles, 4 slots, 0 fixed structures"; a.text != want {
		t.Errorf("summary = %q, want %q", a.text, want)
	}
}

func TestWireSiteLayoutAcceptsATextPartHoldingTheSameJSON(t *testing.T) {
	h := newHarness(t)
	h.seedDrawableSite()
	w := newWire(t, h.deps)

	a := w.call(t, a2a.NewTextPart(`{"skill":"site_layout","arguments":{"siteCode":"WH1"}}`))
	if a.env.Problem != nil || a.env.Data.SiteLayout == nil {
		t.Fatalf("envelope = %+v", a.env)
	}
}

func TestWireReplyIsBoundToTheCallersContext(t *testing.T) {
	h := newHarness(t)
	h.seedDrawableSite()
	w := newWire(t, h.deps)

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skillSiteLayout, "arguments": map[string]any{"siteCode": "WH1"}}))
	msg.ContextID = "ctx-42"
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || reply.ContextID != "ctx-42" {
		t.Fatalf("reply = %#v, want a message in context ctx-42", resp)
	}
}

func TestWireTravelDistanceWithinAZone(t *testing.T) {
	h := newHarness(t)
	h.seedDrawableSite()
	w := newWire(t, h.deps)

	a := w.skill(t, skillTravelDistance, map[string]any{"from": "WH1-STOR-AMB-A07-01-01-A", "to": "WH1-STOR-AMB-A07-02-01-A"})

	d := a.env.Data.TravelDistance
	if a.env.Problem != nil || d == nil {
		t.Fatalf("envelope = %+v", a.env)
	}
	if d.MetresM != 1.2 || !d.Estimated || len(d.Route) != 2 {
		t.Errorf("distance = %+v, want 1.2 m, estimated, 2 waypoints (no aisle geometry was ever set)", d)
	}
	if want := "WH1-STOR-AMB-A07-01-01-A -> WH1-STOR-AMB-A07-02-01-A: 1.20 m (estimated)"; a.text != want {
		t.Errorf("summary = %q, want %q", a.text, want)
	}
}

// TestWireProblemsKeepTheRESTClassification: domain rejections are ANSWERED
// in-band with the status REST would use, never raised as JSON-RPC errors.
func TestWireProblemsKeepTheRESTClassification(t *testing.T) {
	h := newHarness(t)
	h.seedDrawableSite()
	w := newWire(t, h.deps)

	cases := []struct {
		name   string
		skill  string
		args   map[string]any
		status int
		slug   string
	}{
		{"unknown site is a 404", skillSiteLayout, map[string]any{"siteCode": "NOPE"}, 404, "site-not-found"},
		{"unknown slot is a 404", skillTravelDistance, map[string]any{"from": "WH1-STOR-AMB-A07-01-01-A", "to": "WH1-STOR-AMB-A07-09-09-Z"}, 404, "location-slot-not-found"},
		{"malformed code is a 400", skillTravelDistance, map[string]any{"from": "not-a-code", "to": "WH1-STOR-AMB-A07-02-01-A"}, 400, "malformed-location-code"},
		{"different zones without geometry is a 422", skillTravelDistance, map[string]any{"from": "WH1-STOR-AMB-A07-01-01-A", "to": "WH1-RCV-AMB-D01-01-01-A"}, 422, "no-route-between-zones"},
		{"no connecting cross-aisle is a 422", skillTravelDistance, map[string]any{"from": "WH1-STOR-AMB-A07-01-01-A", "to": "WH1-STOR-AMB-A09-01-01-A"}, 422, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := w.skill(t, tc.skill, tc.args)
			if tc.slug == "" {
				if a.env.Problem == nil || a.env.Problem.Status != tc.status {
					t.Fatalf("problem = %+v, want a %d", a.env.Problem, tc.status)
				}
			} else {
				wantProblem(t, a, tc.status, tc.slug)
			}
			if a.env.Skill != tc.skill {
				t.Errorf("envelope skill = %q, want %q", a.env.Skill, tc.skill)
			}
			if !strings.Contains(a.text, a.env.Problem.Detail) {
				t.Errorf("text summary %q lacks the problem detail %q", a.text, a.env.Problem.Detail)
			}
		})
	}
}

// TestWireArgumentValidation is the strict-input table: every malformed
// delegation is a 400 problem answered in-band.
func TestWireArgumentValidation(t *testing.T) {
	h := newHarness(t)
	h.seedDrawableSite()
	w := newWire(t, h.deps)

	cases := []struct {
		name string
		part *a2a.Part
		slug string
		want string // substring of the detail
	}{
		{"unknown skill", a2a.NewDataPart(map[string]any{"skill": "launch_rocket", "arguments": map[string]any{}}), "unknown-skill", "launch_rocket"},
		{"no skill field", a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}), "malformed-skill-request", `no "skill" field`},
		{"unknown request field", a2a.NewDataPart(map[string]any{"skill": skillSiteLayout, "arguments": map[string]any{"siteCode": "WH1"}, "extra": 1}), "malformed-skill-request", "extra"},
		{"trailing data", a2a.NewTextPart(`{"skill":"site_layout","arguments":{"siteCode":"WH1"}} {"x":1}`), "malformed-skill-request", "unexpected data"},
		{"text that is not json", a2a.NewTextPart("how many aisles does WH1 have?"), "malformed-skill-request", "not valid"},
		{"arguments not an object", a2a.NewDataPart(map[string]any{"skill": skillSiteLayout, "arguments": []any{"WH1"}}), "invalid-argument", "arguments"},
		{"site_layout: siteCode missing", a2a.NewDataPart(map[string]any{"skill": skillSiteLayout}), "invalid-site-code", "siteCode is required"},
		{"site_layout: siteCode blank", a2a.NewDataPart(map[string]any{"skill": skillSiteLayout, "arguments": map[string]any{"siteCode": "  "}}), "invalid-site-code", "siteCode is required"},
		{"site_layout: unknown argument", a2a.NewDataPart(map[string]any{"skill": skillSiteLayout, "arguments": map[string]any{"siteCode": "WH1", "zoneId": "WH1-STOR-AMB"}}), "invalid-argument", "zoneId"},
		{"site_layout: misspelled argument", a2a.NewDataPart(map[string]any{"skill": skillSiteLayout, "arguments": map[string]any{"site": "WH1"}}), "invalid-argument", "site"},
		{"site_layout: wrong type", a2a.NewDataPart(map[string]any{"skill": skillSiteLayout, "arguments": map[string]any{"siteCode": 7}}), "invalid-argument", "siteCode"},
		{"travel_distance: from missing", a2a.NewDataPart(map[string]any{"skill": skillTravelDistance, "arguments": map[string]any{"to": "WH1-STOR-AMB-A07-02-01-A"}}), "missing-location-code", "from is required"},
		{"travel_distance: to blank", a2a.NewDataPart(map[string]any{"skill": skillTravelDistance, "arguments": map[string]any{"from": "WH1-STOR-AMB-A07-02-01-A", "to": " "}}), "missing-location-code", "to is required"},
		{"travel_distance: unknown argument", a2a.NewDataPart(map[string]any{"skill": skillTravelDistance, "arguments": map[string]any{"from": "a", "to": "b", "mode": "walking"}}), "invalid-argument", "mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := w.call(t, tc.part)
			wantProblem(t, a, 400, tc.slug)
			if !strings.Contains(a.env.Problem.Detail, tc.want) {
				t.Errorf("detail = %q, want it to contain %q", a.env.Problem.Detail, tc.want)
			}
			if a.env.Data.SiteLayout != nil || a.env.Data.TravelDistance != nil {
				t.Errorf("a problem answer must carry no data: %+v", a.env.Data)
			}
		})
	}
}

// failingSites makes the first repository read of a skill fail with an
// infrastructure error, to prove the cause never reaches the caller.
type failingSites struct{ ports.SiteRepo }

func (failingSites) FindByCode(context.Context, string) (*site.Site, error) {
	return nil, errors.New("dial tcp 10.0.0.7:5432: connect: password authentication failed for user facility")
}

func TestWireInfrastructureFailureIsAnInternalProblemThatLeaksNothing(t *testing.T) {
	h := newHarness(t)
	h.deps.GetSiteLayout = &usecases.GetSiteLayout{Sites: failingSites{}}
	w := newWire(t, h.deps)

	a := w.skill(t, skillSiteLayout, map[string]any{"siteCode": "WH1"})

	wantProblem(t, a, http.StatusInternalServerError, "internal-error")
	raw, _ := json.Marshal(a.msg)
	for _, secret := range []string{"10.0.0.7", "password", "dial tcp"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the answer leaks %q: %s", secret, raw)
		}
	}
}

// TestWireNeverRaisesJSONRPCErrorsForSkillFailures: whatever is wrong with the
// delegation, the HTTP answer is a JSON-RPC RESULT carrying the problem.
func TestWireNeverRaisesJSONRPCErrorsForSkillFailures(t *testing.T) {
	h := newHarness(t)
	w := newWire(t, h.deps)

	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"site_layout","arguments":{"siteCode":"NOPE"}}}]}}}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, w.url+"/", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", string(a2a.Version))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatal(err)
	}
	if len(rpc.Error) != 0 || len(rpc.Result) == 0 || !strings.Contains(string(rpc.Result), "site-not-found") {
		t.Fatalf("want a JSON-RPC result carrying the site-not-found problem, got result=%s error=%s", rpc.Result, rpc.Error)
	}
}

// TestNoAnswerMentionsADeprecatedAlias: this context has no deprecated key
// alias today, but the A2A surface is new and must not teach one. The scan
// covers every part of representative answers, success and problem alike.
func TestNoAnswerMentionsADeprecatedAlias(t *testing.T) {
	h := newHarness(t)
	h.seedDrawableSite()
	w := newWire(t, h.deps)

	answers := []answerOf{
		w.skill(t, skillSiteLayout, map[string]any{"siteCode": "WH1"}),
		w.skill(t, skillSiteLayout, map[string]any{"siteCode": "NOPE"}),
		w.skill(t, skillSiteLayout, map[string]any{}),
		w.skill(t, skillTravelDistance, map[string]any{"from": "x", "to": "y"}),
	}
	for _, a := range answers {
		raw, _ := json.Marshal(a.msg)
		for _, alias := range []string{"alias", "deprecated", "buildingId"} {
			if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(alias)) {
				t.Errorf("an answer mentions %q: %s", alias, raw)
			}
		}
	}
}
