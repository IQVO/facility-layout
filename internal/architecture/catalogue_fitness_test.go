package architecture

// Event-catalogue fitness test (harness v3, round-3 follow-up). MANAGED by warehouse-harness-template
// (tools/migrate_v3.py): do not edit in a service repo.
//
// Rule: every full CloudEvents type string that this service's contract (apis/asyncapi.yaml) declares for ITSELF
// must also appear in a CloudEvents ADR under docs/docs/adr/ or docs/adr/ (the fleet type catalogue). "Declares for itself" means
// the service segment of com.warehouse.<subdomain>.<service>.<entity>.<Event> equals this module's name (last path
// element of go.mod), so types a service only CONSUMES are never required in its catalogue.
//
// Why this and not the context map: a survey of the 10 contexts showed the catalogue ADR tracks the contract
// exactly (10 of 10, 12 of 12 ...), whereas the context map describes flows BETWEEN contexts and does not list every
// event a service emits, so "the map must list every event" is not a convention this fleet follows.
//
// Limits (read these): it only sees types written out as full strings in the contract. A service that declares only
// the types it consumes (wes-work-planning) is checked against nothing and passes. It cannot tell whether a prose
// table elsewhere (an ADR's "publish" row, a context-map diagram) is stale; that stays a review item.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var cloudEventTypeRx = regexp.MustCompile(`com\.warehouse\.(?:wms|wes)\.([a-z][a-z-]*)\.[a-z][a-z-]*\.[A-Z][A-Za-z0-9]*`)

// moduleServiceName returns the last path element of the module line of go.mod ("" if unknown).
func moduleServiceName(goMod string) string {
	for _, l := range strings.Split(goMod, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "module" {
			return f[1][strings.LastIndex(f[1], "/")+1:]
		}
	}
	return ""
}

// missingFromCatalogue returns the types declared by service in contract that no catalogue text mentions.
func missingFromCatalogue(contract, service string, catalogue []string) []string {
	have := strings.Join(catalogue, "\n")
	seen := map[string]bool{}
	var missing []string
	for _, m := range cloudEventTypeRx.FindAllStringSubmatch(contract, -1) {
		if m[1] != service || seen[m[0]] {
			continue
		}
		seen[m[0]] = true
		if !strings.Contains(have, m[0]) {
			missing = append(missing, m[0])
		}
	}
	sort.Strings(missing)
	return missing
}

func TestEventCatalogueMatchesContract(t *testing.T) {
	root := filepath.Join("..", "..")
	contract, err := os.ReadFile(filepath.Join(root, "apis", "asyncapi.yaml"))
	if err != nil {
		t.Skip("no apis/asyncapi.yaml: nothing to compare") // a service with no event contract has no catalogue duty
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	service := moduleServiceName(string(goMod))
	if service == "" {
		t.Fatalf("%s", archViolation("contents", "go.mod has a module line", "cannot derive the service name from go.mod"))
	}
	// ADRs live in docs/docs/adr (repos with a Docusaurus site) or docs/adr (repos without one).
	var adrs []string
	for _, dir := range [][]string{{"docs", "docs", "adr"}, {"docs", "adr"}} {
		m, _ := filepath.Glob(filepath.Join(append(append([]string{root}, dir...), "*cloudevents*.md")...))
		adrs = append(adrs, m...)
	}
	var catalogue []string
	for _, a := range adrs {
		if b, err := os.ReadFile(a); err == nil {
			catalogue = append(catalogue, string(b))
		}
	}
	missing := missingFromCatalogue(string(contract), service, catalogue)
	if len(missing) > 0 {
		hint := "add each type to the CloudEvents ADR's type catalogue"
		if len(adrs) == 0 {
			hint = "this repo has no CloudEvents ADR (*cloudevents*.md under docs/docs/adr or docs/adr): write one that lists the types"
		}
		t.Errorf("%s", archViolation("contents", "the CloudEvents type catalogue ADR lists every type apis/asyncapi.yaml declares for this service",
			strings.Join(missing, ", ")+" declared in apis/asyncapi.yaml but absent from the CloudEvents ADR (docs/docs/adr or docs/adr, *cloudevents*.md): "+
				"other contexts read that ADR to learn what this service emits. FIX: "+hint))
	}
}

// The detector itself, so a refactor cannot quietly turn it into a test that always passes.
func TestEventCatalogueDetector(t *testing.T) {
	const contract = "type: com.warehouse.wes.my-svc.order.OrderPlaced\n" +
		"type: com.warehouse.wes.my-svc.order.OrderShipped\n" +
		"type: com.warehouse.wes.other-svc.task.TaskDone\n" // consumed from another service: not ours
	if got := missingFromCatalogue(contract, "my-svc", []string{"com.warehouse.wes.my-svc.order.OrderPlaced"}); len(got) != 1 ||
		!strings.HasSuffix(got[0], "OrderShipped") {
		t.Fatalf("want only OrderShipped missing, got %v", got)
	}
	if got := missingFromCatalogue(contract, "my-svc", nil); len(got) != 2 {
		t.Fatalf("with no catalogue both own types are missing (and the consumed one is not), got %v", got)
	}
	if got := missingFromCatalogue("none declared", "my-svc", nil); len(got) != 0 {
		t.Fatalf("a contract with no own types needs nothing, got %v", got)
	}
	if moduleServiceName("module github.com/acme/wms-thing\n\ngo 1.22\n") != "wms-thing" {
		t.Fatal("module name not derived from the last path element")
	}
}
