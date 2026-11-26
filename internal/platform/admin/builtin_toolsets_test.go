// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestBuiltinToolsetCatalog_ValidEverywhere(t *testing.T) {
	t.Parallel()
	aiIDs := make(map[string]bool, len(builtinToolsets))
	for _, ts := range builtinToolsets {
		aiIDs[ts.ID] = true
	}
	for _, e := range builtinToolsetCatalog {
		if !schema.IsValidBuiltinGroup(e.ID) {
			t.Errorf("builtin toolset %q is in the builder catalog but not a valid built-in group in pkg/schema — agent create/config-apply will reject it", e.ID)
		}
		if !aiIDs[e.ID] {
			t.Errorf("builtin toolset %q is in the builder catalog but missing from the AI assistant builtinToolsets list", e.ID)
		}
	}
}

func TestBuiltinToolsetTools_DerivedFromRuntime(t *testing.T) {
	t.Parallel()
	for _, e := range builtinToolsetCatalog {
		want := toolinit.LookupBuiltinGroup(e.ID, toolinit.Options{})
		got, ok := builtinToolsetTools[e.ID]
		if !ok {
			t.Errorf("builtinToolsetTools missing entry for %q", e.ID)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("builtinToolsetTools[%q] = %v, want %v (runtime catalog)", e.ID, got, want)
		}
	}
}

func TestBuiltinToolsetTools_DriftFixed(t *testing.T) {
	t.Parallel()
	if !slices.Contains(builtinToolsetTools["builtin_artifact_tools"], "append_to_artifact") {
		t.Error("builtin_artifact_tools should include append_to_artifact (runtime loads it)")
	}
	if !slices.Contains(builtinToolsetTools["builtin_image_tools"], "generate_image_with_gemini") {
		t.Error("builtin_image_tools should include generate_image_with_gemini (runtime loads it)")
	}
}

func TestBuiltinToolsetTools_CoversAdminObservability(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"observability_analytics", "observability_datadog", "observability_platform_read"} {
		if slices.ContainsFunc(builtinToolsetCatalog, func(e builtinToolsetEntry) bool { return e.ID == id }) {
			t.Errorf("precondition: %q is expected to be ABSENT from the builder catalog", id)
		}
		if got, ok := builtinToolsetTools[id]; !ok || len(got) == 0 {
			t.Errorf("builtinToolsetTools missing %q — its toolsetConfigs would be rejected with a 422", id)
		}
	}
	for id := range schema.ValidBuiltinGroups {
		if _, ok := builtinToolsetTools[id]; !ok {
			t.Errorf("builtinToolsetTools missing valid built-in group %q", id)
		}
	}
	if len(builtinToolsetTools) != len(schema.ValidBuiltinGroups) {
		t.Errorf("builtinToolsetTools has %d entries, schema.ValidBuiltinGroups has %d", len(builtinToolsetTools), len(schema.ValidBuiltinGroups))
	}
}

func TestBuiltinToolsetTools_STRBackedGroups(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"builtin_document_tools": {"pptx_create_presentation", "pdf_ops", "render_document_to_images", "html_to_pdf"},
		"builtin_diagram_tools":  {"mermaid_diagram_generator"},
		"builtin_media_tools":    {"ffmpeg", "ffprobe", "image_magick"},
	}
	for id, want := range cases {
		if got := builtinToolsetTools[id]; !slices.Equal(got, want) {
			t.Errorf("builtinToolsetTools[%q] = %v, want %v", id, got, want)
		}
	}
}

func TestBuiltinToolsetResponses_MatchCatalog(t *testing.T) {
	t.Parallel()
	if len(builtinToolsetResponses) != len(builtinToolsetCatalog) {
		t.Fatalf("builtinToolsetResponses has %d entries, catalog has %d", len(builtinToolsetResponses), len(builtinToolsetCatalog))
	}
	for i, e := range builtinToolsetCatalog {
		if builtinToolsetResponses[i].ID != e.ID {
			t.Errorf("response[%d].ID = %q, want %q", i, builtinToolsetResponses[i].ID, e.ID)
		}
		if builtinToolsetResponses[i].Type != "builtin" {
			t.Errorf("response[%d].Type = %q, want builtin", i, builtinToolsetResponses[i].Type)
		}
	}
}

func hasToolset(rs []ToolsetResponse, id string) bool {
	return slices.ContainsFunc(rs, func(r ToolsetResponse) bool { return r.ID == id })
}

func TestVisibleBuiltinToolsets_GatesCodeExecution(t *testing.T) {
	t.Parallel()

	nilReg := visibleBuiltinToolsets(nil)
	if hasToolset(nilReg, "builtin_code_execution") {
		t.Error("nil registry: builtin_code_execution should be hidden")
	}
	if !hasToolset(nilReg, "builtin_artifact_tools") {
		t.Error("nil registry: builtin_artifact_tools should still be visible")
	}

	off := features.NewRegistry()
	off.Register(features.Definition{Key: pythonCodeExecutionFeatureKey, Default: false})
	if hasToolset(visibleBuiltinToolsets(off), "builtin_code_execution") {
		t.Error("flag off: builtin_code_execution should be hidden")
	}

	on := features.NewRegistry()
	on.Register(features.Definition{Key: pythonCodeExecutionFeatureKey, Default: true})
	got := visibleBuiltinToolsets(on)
	if !hasToolset(got, "builtin_code_execution") {
		t.Error("flag on: builtin_code_execution should be visible")
	}
	if want := len(nilReg) + 1; len(got) != want {
		t.Errorf("flag on: got %d toolsets, want %d (baseline + 1 gated)", len(got), want)
	}
}

func TestVisibleBuiltinToolsets_GatesVolumeTools(t *testing.T) {
	t.Parallel()

	if hasToolset(visibleBuiltinToolsets(nil), "builtin_volume_tools") {
		t.Error("nil registry: builtin_volume_tools should be hidden")
	}
	off := features.NewRegistry()
	off.Register(features.Definition{Key: volumesFeatureKey, Default: false})
	if hasToolset(visibleBuiltinToolsets(off), "builtin_volume_tools") {
		t.Error("flag off: builtin_volume_tools should be hidden")
	}

	on := features.NewRegistry()
	on.Register(features.Definition{Key: volumesFeatureKey, Default: true})
	if !hasToolset(visibleBuiltinToolsets(on), "builtin_volume_tools") {
		t.Error("flag on: builtin_volume_tools should be visible")
	}

	if got := builtinToolsetTools["builtin_volume_tools"]; len(got) != 11 {
		t.Errorf("builtin_volume_tools should resolve to 11 volume_fs tools, got %d: %v", len(got), got)
	}
}

func hasToolsetDef(ds []toolsetDefinition, id string) bool {
	return slices.ContainsFunc(ds, func(d toolsetDefinition) bool { return d.ID == id })
}

func TestAIAssistantToolsets_GatesByFlag(t *testing.T) {
	t.Parallel()

	nilSet := aiAssistantToolsets(nil)
	if hasToolsetDef(nilSet, "builtin_code_execution") {
		t.Error("nil registry: code_execution must be excluded from the assistant set")
	}
	if hasToolsetDef(nilSet, "builtin_volume_tools") {
		t.Error("nil registry: volume tools must be excluded from the assistant set")
	}
	if !hasToolsetDef(nilSet, "builtin_artifact_tools") {
		t.Error("nil registry: ungated artifact tools should still be present")
	}

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: volumesFeatureKey, Default: true})
	reg.Register(features.Definition{Key: pythonCodeExecutionFeatureKey, Default: false})
	set := aiAssistantToolsets(reg)
	if !hasToolsetDef(set, "builtin_volume_tools") {
		t.Error("volumes on: volume tools should be in the assistant set")
	}
	if hasToolsetDef(set, "builtin_code_execution") {
		t.Error("python off: code_execution should remain excluded")
	}
}

func TestValidateAndDefaultToolsets_DropsGatedWhenOff(t *testing.T) {
	t.Parallel()

	off := validateAndDefaultToolsets(
		[]string{"builtin_volume_tools", "builtin_web_request_tools"},
		aiAssistantToolsets(nil),
	)
	if slices.Contains(off, "builtin_volume_tools") {
		t.Error("flag off: gated builtin_volume_tools must be dropped from the result")
	}

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: volumesFeatureKey, Default: true})
	on := validateAndDefaultToolsets([]string{"builtin_volume_tools"}, aiAssistantToolsets(reg))
	if !slices.Contains(on, "builtin_volume_tools") {
		t.Error("flag on: builtin_volume_tools should be kept in the result")
	}
}

func TestBuiltinToolsetAliasTargets_InCatalog(t *testing.T) {
	t.Parallel()
	catalogIDs := make(map[string]bool, len(builtinToolsetCatalog))
	for _, e := range builtinToolsetCatalog {
		catalogIDs[e.ID] = true
	}
	for name := range schema.ValidBuiltinGroups {
		target := schema.CanonicalBuiltinGroup(name)
		if target == name {
			continue
		}
		if !catalogIDs[target] {
			t.Errorf("alias %q canonicalizes to %q, which is not in builtinToolsetCatalog — the agent's selection would still be invisible", name, target)
		}
	}
}

func TestBuiltinToolsetResponses_CarryToolNames(t *testing.T) {
	t.Parallel()
	for _, resp := range visibleBuiltinToolsets(nil) {
		want := toolinit.LookupBuiltinGroup(resp.ID, toolinit.Options{})
		if len(want) == 0 {
			t.Errorf("toolset %q registers no tools at runtime", resp.ID)
			continue
		}
		got := make([]string, 0, len(resp.Tools))
		for _, tool := range resp.Tools {
			got = append(got, tool.ToolName)
		}
		if !slices.Equal(got, want) {
			t.Errorf("toolset %q advertises tools %v, but the runtime loads %v", resp.ID, got, want)
		}
	}
}

func TestBuiltinToolsetResponses_AdvertiseAliases(t *testing.T) {
	t.Parallel()
	byID := make(map[string][]string)
	for _, resp := range visibleBuiltinToolsets(nil) {
		byID[resp.ID] = resp.Aliases
	}
	if got := byID["builtin_web_request_tools"]; !slices.Contains(got, "web_tools") {
		t.Errorf("builtin_web_request_tools aliases = %v, want to include web_tools", got)
	}
	for name := range schema.ValidBuiltinGroups {
		target := schema.CanonicalBuiltinGroup(name)
		if target == name {
			continue
		}
		advertised, listed := byID[target]
		if !listed {
			continue
		}
		if !slices.Contains(advertised, name) {
			t.Errorf("toolset %q does not advertise its alias %q, so clients cannot recognize the two as one toolset", target, name)
		}
	}
}

func TestBuiltinToolsetResponses_WebRequestReachable(t *testing.T) {
	t.Parallel()
	for _, resp := range visibleBuiltinToolsets(nil) {
		for _, tool := range resp.Tools {
			if tool.ToolName == "web_request" {
				if resp.ID != "builtin_web_request_tools" {
					t.Errorf("web_request surfaced under unexpected toolset %q", resp.ID)
				}
				return
			}
		}
	}
	t.Fatal("no built-in toolset advertises web_request; the builder cannot bind it")
}

func TestFeatureGatedToolsetsCarryToolNames(t *testing.T) {
	t.Parallel()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: pythonCodeExecutionFeatureKey, Default: true})
	reg.Register(features.Definition{Key: volumesFeatureKey, Default: true})
	var sawGated bool
	for _, resp := range visibleBuiltinToolsets(reg) {
		if resp.ID != "builtin_code_execution" && resp.ID != "builtin_volume_tools" {
			continue
		}
		sawGated = true
		if len(resp.Tools) == 0 {
			t.Errorf("flag-gated toolset %q carries no tool names", resp.ID)
		}
	}
	if !sawGated {
		t.Fatal("expected the flag-gated toolsets to be visible with both flags on")
	}
}

func TestBuiltinGroupAliases_ResolveToSameTools(t *testing.T) {
	t.Parallel()
	for name := range schema.ValidBuiltinGroups {
		target := schema.CanonicalBuiltinGroup(name)
		if target == name {
			continue
		}
		aliasTools := toolinit.LookupBuiltinGroup(name, toolinit.Options{})
		targetTools := toolinit.LookupBuiltinGroup(target, toolinit.Options{})
		if len(aliasTools) == 0 {
			t.Errorf("alias %q resolves to no tools", name)
		}
		if !slices.Equal(aliasTools, targetTools) {
			t.Errorf("canonicalizing %q to %q changes the agent's tools: %v vs %v", name, target, aliasTools, targetTools)
		}
	}
}
