package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/facility-layout/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card).
const (
	skillSiteLayout     = "site_layout"
	skillTravelDistance = "travel_distance"
)

// Problem slugs of the adapter's own argument checks. Each is a slug the REST
// adapter reports for the same condition, so the status and title come from
// the shared table (TestArgumentSlugsExistInTheRESTTable pins that).
const (
	slugInvalidSiteCode     = "invalid-site-code"
	slugMissingLocationCode = "missing-location-code"
)

// skillRequest is the wire contract of every skill call: the first meaningful
// message part carries (as a Data part, or a Text part holding the same JSON)
// the skill id and its arguments. Strict on purpose -- an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored. (Go's
// encoding/json matches field names case-insensitively, so "SITECODE" still
// reaches siteCode; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// siteLayoutArgs and travelDistanceArgs are the per-skill argument shapes.
type siteLayoutArgs struct {
	SiteCode string `json:"siteCode"`
}

type travelDistanceArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR-0043).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillSiteLayout:
		data, problem := deps.siteLayout(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{SiteLayout: &data, summary: summarizeSiteLayout(data)}, nil
	case skillTravelDistance:
		data, problem := deps.travelDistance(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{TravelDistance: &data, summary: summarizeTravelDistance(data)}, nil
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first
// meaningful part. A Data part (or a Text part holding the same JSON object)
// is accepted; anything else is a request-shape problem, answered -- not
// raised -- because a malformed delegation is a business answer for the
// calling agent, not a transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// siteLayout is the site map read skill: one site's drawable structure from
// this context's own state, through the SAME GetSiteLayout read model the
// REST and MCP surfaces use.
func (d Deps) siteLayout(ctx context.Context, rawArgs json.RawMessage) (siteLayoutDTO, *problem) {
	var args siteLayoutArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return siteLayoutDTO{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	if strings.TrimSpace(args.SiteCode) == "" {
		return siteLayoutDTO{}, problemForSlug(slugInvalidSiteCode, "siteCode is required")
	}
	layout, err := d.GetSiteLayout.Execute(ctx, args.SiteCode)
	if err != nil {
		return siteLayoutDTO{}, problemFromUseCase(err)
	}
	return toSiteLayoutDTO(layout), nil
}

// travelDistance is the travel distance read skill: the shortest map distance
// between two coded locations, through the SAME EstimateTravelDistance read
// model the REST and MCP surfaces use. It reports map topology only.
func (d Deps) travelDistance(ctx context.Context, rawArgs json.RawMessage) (travelDistanceDTO, *problem) {
	var args travelDistanceArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return travelDistanceDTO{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	if strings.TrimSpace(args.From) == "" {
		return travelDistanceDTO{}, problemForSlug(slugMissingLocationCode, "from is required")
	}
	if strings.TrimSpace(args.To) == "" {
		return travelDistanceDTO{}, problemForSlug(slugMissingLocationCode, "to is required")
	}
	from, err := shared.ParseLocationCode(args.From)
	if err != nil {
		return travelDistanceDTO{}, problemFromUseCase(err)
	}
	to, err := shared.ParseLocationCode(args.To)
	if err != nil {
		return travelDistanceDTO{}, problemFromUseCase(err)
	}
	distance, err := d.EstimateTravelDistance.Execute(ctx, from, to)
	if err != nil {
		return travelDistanceDTO{}, problemFromUseCase(err)
	}
	return toTravelDistanceDTO(from.String(), to.String(), distance), nil
}
