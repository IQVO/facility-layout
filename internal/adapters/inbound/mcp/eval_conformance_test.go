// E2 — over-the-wire protocol conformance evals.
//
// server_test.go already proves the happy paths (list+call, resource read,
// prompt get). These evals pin the protocol behaviors a model host relies
// on when things go WRONG or get exotic: the initialize handshake and its
// negotiation results, error-shape guarantees for unknown tools / bad
// argument types / unknown extra arguments / unknown resources and prompts,
// discovery of resource templates and prompts, and session lifecycle after
// close. All against the real Streamable HTTP handler — never in-process
// values.
package mcp_test

import (
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestEvalConformance_InitializeHandshake pins what a host learns at
// connect time: a negotiated protocol result carrying this server's name
// and non-empty usage instructions.
func TestEvalConformance_InitializeHandshake(t *testing.T) {
	h := newEvalHarness(t)

	init := h.session.InitializeResult()
	if init == nil {
		t.Fatal("no initialize result on the session")
	}
	if init.ServerInfo.Name != "facility-layout-mcp" {
		t.Errorf("initialize result server name = %q, want facility-layout-mcp", init.ServerInfo.Name)
	}
	if strings.TrimSpace(init.Instructions) == "" {
		t.Error("initialize result carries no instructions — hosts surface these to the model; an empty string wastes the connect handshake")
	}
}

// TestEvalConformance_UnknownToolIsAProtocolError pins that calling a tool
// the server never advertised is rejected at the protocol layer (a
// JSON-RPC error), not silently executed or returned as a tool result.
func TestEvalConformance_UnknownToolIsAProtocolError(t *testing.T) {
	h := newEvalHarness(t)

	if err := h.callTool(t.Context(), "definitely_not_a_tool", map[string]any{}); err == nil {
		t.Fatal("calling an unknown tool must be a protocol error, got success")
	}
}

// TestEvalConformance_WrongTypedArgumentIsRejected pins that a model
// sending a string parameter as a number is rejected by the typed tool
// schema — either as a protocol error or a tool-level error result, never
// as a success that silently coerces.
func TestEvalConformance_WrongTypedArgumentIsRejected(t *testing.T) {
	h := newEvalHarness(t)

	res, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "get_site_layout",
		Arguments: map[string]any{"siteCode": 42},
	})
	switch {
	case err != nil:
		// Protocol-level rejection: acceptable.
	case res != nil && res.IsError:
		// Tool-level rejection: acceptable.
	default:
		t.Fatalf("wrong-typed siteCode argument must not succeed: err=%v res=%+v", err, res)
	}
}

// TestEvalConformance_ExtraArgumentsAreRejected pins the strict side of
// argument handling: the typed tool schemas disallow undeclared properties
// (the SDK default, additionalProperties: false), so stray model chatter
// forwarded by a host is rejected loudly rather than silently ignored.
func TestEvalConformance_ExtraArgumentsAreRejected(t *testing.T) {
	h := newEvalHarness(t)

	res, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "get_site_layout",
		Arguments: map[string]any{
			"siteCode":      "WH1",
			"model_chatter": "I think this site might be WH1",
			"step":          2,
		},
	})
	switch {
	case err != nil:
		// Protocol-level rejection: acceptable.
	case res != nil && res.IsError:
		// Tool-level rejection: acceptable.
	default:
		t.Fatalf("unknown extra arguments must not succeed: err=%v res=%+v", err, res)
	}
}

// TestEvalConformance_ResourceTemplateDiscovery pins that the site-layout
// resource template is discoverable via the protocol's listing calls, so a
// host can surface it without out-of-band knowledge.
func TestEvalConformance_ResourceTemplateDiscovery(t *testing.T) {
	h := newEvalHarness(t)

	templates, err := h.session.ListResourceTemplates(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	for _, tpl := range templates.ResourceTemplates {
		if strings.Contains(tpl.URITemplate, "{siteCode}") && tpl.URITemplate != "" {
			return
		}
	}
	t.Fatalf("site-layout template not discoverable: %+v", templates.ResourceTemplates)
}

// TestEvalConformance_UnknownResourceIsRejected pins that reading a URI
// outside the template is a protocol error.
func TestEvalConformance_UnknownResourceIsRejected(t *testing.T) {
	h := newEvalHarness(t)

	_, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: "map://not-a-layout"})
	if err == nil {
		t.Fatal("reading an unknown resource URI must be rejected")
	}
}

// TestEvalConformance_UnknownPromptIsRejected pins that getting a prompt
// the server does not have is a protocol error.
func TestEvalConformance_UnknownPromptIsRejected(t *testing.T) {
	h := newEvalHarness(t)

	_, err := h.session.GetPrompt(t.Context(), &sdk.GetPromptParams{Name: "no_such_prompt"})
	if err == nil {
		t.Fatal("getting an unknown prompt must be rejected")
	}
}

// TestEvalConformance_PromptIsListable pins prompt discovery: the
// explore_layout SOP is advertised via prompts/list.
func TestEvalConformance_PromptIsListable(t *testing.T) {
	h := newEvalHarness(t)

	prompts, err := h.session.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	for _, p := range prompts.Prompts {
		if p.Name == "explore_layout" {
			return
		}
	}
	t.Fatalf("explore_layout prompt not advertised: %+v", prompts.Prompts)
}

// TestEvalConformance_SessionCloseEndsCalls pins that after the client
// closes the session, further calls fail loudly instead of silently
// no-op'ing.
func TestEvalConformance_SessionCloseEndsCalls(t *testing.T) {
	h := newEvalHarness(t)

	if err := h.session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	if _, err := h.session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "list_sites",
		Arguments: map[string]any{},
	}); err == nil {
		t.Fatal("calling a tool on a closed session must fail")
	}
}
