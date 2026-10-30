// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as a third driving adapter over the
// same application-layer read use cases the HTTP and MCP adapters use. It is
// built on the official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC
// 2.0 over HTTP, with the Agent Card served at the well-known path.
//
// Per ADR-0043, A2A is the DELEGATION channel between bounded contexts: an
// agent client sends a goal-shaped skill request as a structured message part
// and this agent answers from its own state through its own use cases. It is
// not a data-sync channel -- facts between contexts still travel exclusively
// as CloudEvents on Kafka (ADR-0024).
//
// facility-layout is a read-only Open Host Service: its warehouse map is
// consumed, never mutated, by the rest of the estate, and its MCP surface
// offers no write (ADR-0007). This agent therefore offers no write skill
// either: both skills are reads over the map, and no use case that changes
// state is wired into Deps.
//
// Like the MCP adapter, this package depends inward on the application layer
// (use cases) and the domain only -- never on an outbound adapter. The
// composition root (cmd/a2a) wires concrete repositories into the use cases.
// Domain structs never leak across the message boundary.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/facility-layout/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root.
// It carries the same read use cases the HTTP and MCP adapters use; the
// adapter never constructs an outbound adapter itself. There is deliberately
// no write use case here (see the package comment).
type Deps struct {
	// GetSiteLayout is the existing read-model use case behind the
	// site_layout skill (and the MCP get_site_layout tool), reused unchanged.
	GetSiteLayout *usecases.GetSiteLayout
	// EstimateTravelDistance is the existing read-model use case behind the
	// travel_distance skill (and the MCP estimate_travel_distance tool),
	// reused unchanged.
	EstimateTravelDistance *usecases.EstimateTravelDistance
}

// Card builds this agent's Agent Card (ADR-0043). The surface is two curated,
// goal-shaped read skills -- not one skill per endpoint, the same curation
// discipline ADR-0007 applied to MCP tools. site_layout answers "what is the
// shape of this site"; travel_distance answers "how far is it between these
// two coded locations". The card declares no security schemes on purpose:
// this surface is unauthenticated by the same fleet-wide decision as REST and
// MCP (ADR-0015); schemes are added here, not in a redesign, if that decision
// is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "facility-layout",
		Description: "Facility Layout agent for the warehouse map: answers what a site physically looks like (zones, aisles, coded storage slots and fixed structures) and how far apart two coded locations are on the map. Read-only: it never changes the map, which is edited only through this context's REST API, and it reports map topology only -- never occupancy, stock, travel time or congestion.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/facility-layout",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillSiteLayout,
				Name:        "Layout of a site",
				Description: "Read: one site's full drawable structure as a compact nested map -- zones, then aisles in walk order, then slot codes, with each zone's temperature class and hazmat flag, each aisle's direction and sequence hint, the site's fixed structures, and the site header (name, role in the fulfilment network, transfer capability). Identify the site with siteCode (the canonical Site code, e.g. WH1). Answers a 404 problem when the site does not exist. Changes nothing.",
				Tags:        []string{"facility", "layout", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"site_layout","arguments":{"siteCode":"WH1"}}`,
				},
			},
			{
				ID:          skillTravelDistance,
				Name:        "Travel distance between two locations",
				Description: "Read: the shortest travel distance, in metres, between two coded locations (seven-segment codes, e.g. WH1-STOR-AMB-A07-01-01-A). Within one zone it routes over the zone's travel graph honouring one-way aisles and cross-aisles; the result is flagged estimated when any leg used a bay-pitch fallback instead of real geometry. Across zones it answers the straight-line distance, always flagged estimated, only when both locations have recorded position geometry, otherwise a 422 problem (it does not guess). Reports map TOPOLOGY only, never travel time or congestion. Changes nothing.",
				Tags:        []string{"facility", "travel", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"travel_distance","arguments":{"from":"WH1-STOR-AMB-A07-01-01-A","to":"WH1-STOR-AMB-A07-02-01-A"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are synchronous request/response reads, so
// there is no long-running Task lifecycle to track in this first A2A
// surface. The response carries the caller's contextId (and taskId when the
// client referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler -- the only transport this
// agent ships (ADR-0043), mirroring ADR-0007's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
