package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// identityMessages maps each AsyncAPI message that carries the ADR-0045
// optional location identity to the optional properties it must declare.
var identityMessages = map[string][]string{
	"siteRegistered":         {"gln"},
	"zoneRegistered":         {"gln"},
	"locationSlotRegistered": {"gln", "spotKind"},
}

// goldenForExample pins each AsyncAPI example to the byte-exact wire golden it
// was copied from, so an example can never drift from what the publisher emits.
var goldenForExample = map[string]string{
	"siteWithGln":               "SiteRegistered_gln",
	"siteWithoutGln":            "SiteRegistered",
	"zoneWithGln":               "ZoneRegistered_gln",
	"zoneWithoutGln":            "ZoneRegistered",
	"yardSpot":                  "LocationSlotRegistered_yardSpot",
	"dockDoorWithGln":           "LocationSlotRegistered_dockGln",
	"storageBinWithoutIdentity": "LocationSlotRegistered",
}

func asyncMessages(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../apis/asyncapi.yaml")
	if err != nil {
		t.Fatalf("read AsyncAPI contract: %v", err)
	}
	var doc struct {
		Components struct {
			Messages map[string]map[string]any `yaml:"messages"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse AsyncAPI contract: %v", err)
	}
	return doc.Components.Messages
}

// dataSchema digs the event's `data` object schema out of a message payload.
func dataSchema(t *testing.T, msg map[string]any) (props map[string]any, required []any) {
	t.Helper()
	payload := msg["payload"].(map[string]any)["allOf"].([]any)
	data := payload[1].(map[string]any)["properties"].(map[string]any)["data"].(map[string]any)["allOf"].([]any)
	object := data[1].(map[string]any)
	required, _ = object["required"].([]any)
	return object["properties"].(map[string]any), required
}

// TestIdentityContractIsPublished pins the ADR-0045 additive contract: gln
// (and spotKind on slots) are declared on the registered events as OPTIONAL,
// nullable properties; they never become required.
func TestIdentityContractIsPublished(t *testing.T) {
	messages := asyncMessages(t)
	for name, fields := range identityMessages {
		msg, ok := messages[name]
		if !ok {
			t.Fatalf("AsyncAPI message %s missing", name)
		}
		props, required := dataSchema(t, msg)
		for _, field := range fields {
			prop, declared := props[field].(map[string]any)
			if !declared {
				t.Errorf("%s: property %q not declared", name, field)
				continue
			}
			if types, _ := prop["type"].([]any); len(types) != 2 || types[1] != "null" {
				t.Errorf("%s.%s: want a nullable type [string, 'null'], got %v", name, field, prop["type"])
			}
			for _, r := range required {
				if r == field {
					t.Errorf("%s.%s must stay optional (additive contract), but it is required", name, field)
				}
			}
		}
	}
}

// TestIdentityExamplesMatchTheWireGoldens checks every AsyncAPI example of the
// three messages equals the golden CloudEvent the publisher produces.
func TestIdentityExamplesMatchTheWireGoldens(t *testing.T) {
	messages := asyncMessages(t)
	seen := 0
	for name := range identityMessages {
		examples, _ := messages[name]["examples"].([]any)
		if len(examples) == 0 {
			t.Errorf("%s: no examples", name)
		}
		for _, e := range examples {
			example := e.(map[string]any)
			exName := example["name"].(string)
			file, known := goldenForExample[exName]
			if !known {
				t.Errorf("%s: example %q has no pinned golden", name, exName)
				continue
			}
			golden, err := os.ReadFile(filepath.Join("..", "adapters", "outbound", "kafka", "testdata", "wire", "events", file+".json"))
			if err != nil {
				t.Fatalf("read golden %s: %v", file, err)
			}
			var want any
			if err := json.Unmarshal(golden, &want); err != nil {
				t.Fatalf("parse golden %s: %v", file, err)
			}
			got := normalizeJSON(t, example["payload"])
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s example %q differs from golden %s\n got: %v\nwant: %v", name, exName, file, got, want)
			}
			seen++
		}
	}
	if seen != len(goldenForExample) {
		t.Errorf("checked %d examples, expected %d", seen, len(goldenForExample))
	}
}

// normalizeJSON round-trips a YAML-decoded value through JSON so map and
// number types compare equal to a decoded golden file.
func normalizeJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal example: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal example: %v", err)
	}
	return out
}
