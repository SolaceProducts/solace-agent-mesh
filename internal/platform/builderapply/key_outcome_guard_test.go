// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type keyOutcome int

const (
	keyRead keyOutcome = iota + 1
	keySaved
	keyManaged
	keyRefused
	keyManagedNoted
)

var agentKeyOutcomes = map[string]keyOutcome{
	"agent_name": keyRead, "description": keyRead, "instruction": keyRead, "skills": keyRead,
	"tools": keyRead, "model_provider": keyRead, "agent_card": keyRead, "model": keyRefused,
	"display_name": keyManaged, "namespace": keyManaged, "session_service": keyManaged, "artifact_service": keyManaged,
	"agent_card_publishing": keyManaged, "agent_discovery": keyManaged, "skills_base_path": keyManaged, "skills_cache_path": keyManaged,
	"agent_type": keyManaged, "volumes": keyManagedNoted,
}

type schemaDocument struct {
	Properties map[string]map[string]any `yaml:"properties"`
	Defs       map[string]struct {
		Properties map[string]map[string]any `yaml:"properties"`
	} `yaml:"$defs"`
}

func loadSchemaDocument(t *testing.T, name string) schemaDocument {
	t.Helper()
	raw, err := schema.SchemaContent(name)
	if err != nil {
		t.Fatalf("SchemaContent(%q): %v", name, err)
	}
	var doc schemaDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s schema: %v", name, err)
	}
	return doc
}

var sampleOverrides = map[string]any{
	"agent_name":                 "probe_agent",
	"model":                      "openai/gpt-4o",
	"model_provider":             []any{"planning"},
	"agent_card":                 map[string]any{"description": "A probe agent for the key outcome guard."},
	"inter_agent_communication":  map[string]any{"allow_list": []any{"*"}},
	"input_schema":               map[string]any{"type": "object"},
	"output_schema":              map[string]any{"type": "object"},
	"mcp_intelligent_processing": map[string]any{"enabled": true},
	"volumes":                    []any{map[string]any{"name": "data", "path": "/data"}},
	"hil":                        map[string]any{"require_approval": true},
}

func sampleFor(key string, prop map[string]any) any {
	if override, ok := sampleOverrides[key]; ok {
		return override
	}
	if constant, ok := prop["const"]; ok {
		return constant
	}
	if def, ok := prop["default"]; ok {
		return def
	}
	switch prop["type"] {
	case "integer":
		return 1
	case "number":
		return 1.5
	case "boolean":
		return true
	case "object":
		return map[string]any{"k": "v"}
	case "array":
		return []any{}
	}
	return "sample text"
}

func probeAgentContent(t *testing.T, key string, sample any) string {
	t.Helper()
	appConfig := map[string]any{
		"agent_name":  "probe_agent",
		"instruction": "You are a probe agent.",
		"agent_card":  map[string]any{"description": "A probe agent for the key outcome guard."},
	}
	appConfig[key] = sample
	content, err := yaml.Marshal(map[string]any{"apps": []any{map[string]any{"name": "probe_agent", "app_config": appConfig}}})
	if err != nil {
		t.Fatalf("marshal probe for %s: %v", key, err)
	}
	return string(content)
}

func createdAdditional(t *testing.T, d desiredComponent) map[string]any {
	t.Helper()
	create, ok := d.create.(entplatform.CreateAgentRequest)
	if !ok {
		t.Fatalf("create is %T, want CreateAgentRequest", d.create)
	}
	var blob map[string]any
	if len(create.AdditionalConfigurations) == 0 {
		return blob
	}
	if err := json.Unmarshal(create.AdditionalConfigurations, &blob); err != nil {
		t.Fatalf("decode additionalConfigurations: %v", err)
	}
	return blob
}

func TestEveryAgentSchemaKeyHasAnOutcome(t *testing.T) {
	t.Parallel()
	doc := loadSchemaDocument(t, "agent")
	byRuntime := runtimeIndex(extraConfigKeys)

	for key := range agentKeyOutcomes {
		if _, inSchema := doc.Properties[key]; !inSchema {
			t.Errorf("agentKeyOutcomes has %s, which agent.schema.yaml does not declare", key)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(doc.Properties)) {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			want, declared := agentKeyOutcomes[key]
			canonical, registered := byRuntime[key]
			switch {
			case declared:
			case registered:
				want = keySaved
			default:
				t.Fatalf("%s has no declared outcome: add it to agentKeyOutcomes, or register it in extraConfigKeys", key)
			}

			content := probeAgentContent(t, key, sampleFor(key, doc.Properties[key]))
			got, err := desiredAgent("probe_agent", content, newToolsetCatalog())
			if want == keyRefused {
				if err == nil {
					t.Errorf("%s is declared refused but desiredAgent accepted it", key)
				}
				return
			}
			if err != nil {
				t.Fatalf("desiredAgent with %s: %v", key, err)
			}
			stored := createdAdditional(t, got)
			droppedWarning := "agent config key " + key + " "
			switch want {
			case keySaved:
				for _, w := range got.warnings {
					if strings.Contains(w, droppedWarning) {
						t.Errorf("%s is declared saved but warned: %s", key, w)
					}
				}
				if _, present := stored[canonical]; !present {
					t.Errorf("%s is declared saved but %s is not in the stored blob %v", key, canonical, stored)
				}
			case keyManagedNoted:
				var noted []string
				for _, w := range got.warnings {
					if strings.Contains(w, key+" is set with `sam config apply`") {
						noted = append(noted, w)
					}
				}
				if len(noted) != 1 {
					t.Errorf("%s is declared managed with a notice, want exactly one warning naming it, got %q", key, got.warnings)
				}
				if _, present := stored[canonical]; present {
					t.Errorf("%s is declared managed but %s reached the stored blob", key, canonical)
				}
			case keyRead, keyManaged:
				if len(got.warnings) > 0 {
					t.Errorf("%s is declared %s but warned: %q", key, outcomeName(want), got.warnings)
				}
				if _, present := stored[canonical]; registered && present {
					t.Errorf("%s is declared %s but %s reached the stored blob", key, outcomeName(want), canonical)
				}
			}
		})
	}
}

func outcomeName(o keyOutcome) string {
	return map[keyOutcome]string{keyRead: "read", keySaved: "saved", keyManaged: "managed", keyRefused: "refused", keyManagedNoted: "managed with a notice"}[o]
}

func TestEveryBuiltinEntryKeyHasAnOutcome(t *testing.T) {
	t.Parallel()
	doc := loadSchemaDocument(t, "tool")
	type entryShape struct {
		def, toolType, nameKey, nameValue string
	}
	shapes := []entryShape{
		{def: "builtin", toolType: "builtin", nameKey: "tool_name", nameValue: "web_request"},
		{def: "builtin-group", toolType: "builtin-group", nameKey: "group_name", nameValue: "artifact_management"},
	}
	for _, shape := range shapes {
		props := doc.Defs[shape.def].Properties
		if len(props) == 0 {
			t.Fatalf("tool schema $defs.%s has no properties", shape.def)
		}
		for _, key := range slices.Sorted(maps.Keys(props)) {
			t.Run(shape.def+"/"+key, func(t *testing.T) {
				t.Parallel()
				outcome, declared := builtinEntryKeyOutcomes[key]
				if !declared {
					t.Fatalf("%s has no outcome in builtinEntryKeyOutcomes", key)
				}
				entry := map[string]any{"tool_type": shape.toolType, shape.nameKey: shape.nameValue}
				if key != shape.nameKey {
					entry[key] = sampleFor(key, props[key])
				}
				appConfig := map[string]any{
					"agent_name":  "probe_agent",
					"instruction": "You are a probe agent.",
					"agent_card":  map[string]any{"description": "A probe agent for the key outcome guard."},
					"tools":       []any{entry},
				}
				content, err := yaml.Marshal(map[string]any{"apps": []any{map[string]any{"name": "probe_agent", "app_config": appConfig}}})
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				got, err := desiredAgent("probe_agent", string(content), newToolsetCatalog())
				switch outcome {
				case entryKeyRefused:
					if err == nil || !strings.Contains(err.Error(), key) {
						t.Errorf("%s is declared refused, error = %v, want one naming it", key, err)
					}
				case entryKeyUnstored:
					if err != nil {
						t.Fatalf("desiredAgent: %v", err)
					}
					marker := fmt.Sprintf(": %s ", key)
					var matching []string
					for _, w := range got.warnings {
						if strings.Contains(w, marker) {
							matching = append(matching, w)
						}
					}
					if len(matching) != 1 {
						t.Errorf("%s is declared unstored, want exactly one warning containing %q, got %q", key, marker, got.warnings)
					}
				case entryKeyRead:
					if err != nil {
						t.Fatalf("desiredAgent: %v", err)
					}
					if len(got.warnings) > 0 {
						t.Errorf("%s is declared read but warned: %q", key, got.warnings)
					}
				}
			})
		}
	}
}
