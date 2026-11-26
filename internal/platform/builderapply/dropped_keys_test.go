// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"reflect"
	"strings"
	"testing"
)

func TestDroppedAgentKeys_UnrecognisedKeyIsReported(t *testing.T) {
	t.Parallel()
	_, _, warnings, err := agentSpecFromAppConfig("probe", map[string]any{
		"agent_name":        "probe",
		"instruction":       "do the thing",
		"totally_bogus_key": 42,
	}, newToolsetCatalog())
	if err != nil {
		t.Fatalf("conversion: %v", err)
	}
	if !warns(warnings, "totally_bogus_key") {
		t.Errorf("warnings = %v, want the unrecognised key named", warnings)
	}
	for _, saved := range []string{"agent_name", "instruction"} {
		if warns(warnings, saved) {
			t.Errorf("warnings = %v, want nothing said about %s, which was saved", warnings, saved)
		}
	}
}

func TestDroppedAgentKeys_RealKeysThePlatformModelsElsewhereAreReported(t *testing.T) {
	t.Parallel()
	_, _, warnings, err := agentSpecFromAppConfig("probe",
		map[string]any{"agent_name": "probe", "model": "x"}, newToolsetCatalog())
	if err != nil {
		t.Fatalf("conversion: %v", err)
	}
	if !warns(warnings, "model") {
		t.Errorf("warnings = %v, want the model key reported as unsaved", warnings)
	}
}

func TestDroppedAgentKeys_RegistryKeysAreSavedNotWarned(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{"agent_name": "probe"}
	wantStored := map[string]any{}
	for key, value := range valueOtherThanTheDefault {
		appConfig[key] = value
		wantStored[runtimeIndex(extraConfigKeys)[key]] = value
	}
	spec, _, warnings, err := agentSpecFromAppConfig("probe", appConfig, newToolsetCatalog())
	if err != nil {
		t.Fatalf("conversion: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for registry keys", warnings)
	}
	stored := extraConfigOf(spec)
	for key, want := range wantStored {
		if !reflect.DeepEqual(normalizeForCompare(stored[key]), normalizeForCompare(want)) {
			t.Errorf("%s = %v, want %v saved on the spec", key, stored[key], want)
		}
	}
}

func TestDroppedAgentKeys_InfrastructureIsSilent(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{"agent_name": "probe", "instruction": "do the thing"}
	for _, key := range agentKeysManagedElsewhere {
		appConfig[key] = "x"
	}
	_, _, warnings, err := agentSpecFromAppConfig("probe", appConfig, newToolsetCatalog())
	if err != nil {
		t.Fatalf("conversion: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want silence for platform-managed keys", warnings)
	}
}

func TestDroppedAgentKeys_EveryKeyTheConverterReadsStaysSilent(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{
		"agent_name":     "probe_agent",
		"description":    "a described agent",
		"instruction":    "a distinctive system prompt",
		"model_provider": []any{"planning"},
		"input_modes":    []any{"text"},
		"output_modes":   []any{"text"},
		"skills":         []any{},
		"tools": []any{map[string]any{
			"tool_type": "builtin-group", "group_name": "artifact_management",
		}},
		"toolsets": []any{},
		"agent_card": map[string]any{
			"description":        "a described agent",
			"skills":             []any{},
			"defaultInputModes":  []any{"text"},
			"defaultOutputModes": []any{"text"},
		},
	}
	_, _, warnings, err := agentSpecFromAppConfig("probe_agent", appConfig, newToolsetCatalog())
	if err != nil {
		t.Fatalf("conversion: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want silence — every key here is one the converter reads", warnings)
	}
}

func warns(warnings []string, key string) bool {
	for _, w := range warnings {
		if strings.Contains(w, key) {
			return true
		}
	}
	return false
}
