// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const (
	researchToolset = "builtin_research_tools"
	webTools        = "        - tool_type: builtin-group\n          group_name: web_tools\n"
	researchGroup   = "        - tool_type: builtin-group\n          group_name: research\n"
)

func groupWithHIL(group, hil string) string {
	return "        - tool_type: builtin-group\n          group_name: " + group + "\n          hil: " + hil + "\n"
}

func warningsContaining(warnings []string, fragment string) []string {
	var out []string
	for _, w := range warnings {
		if strings.Contains(w, fragment) {
			out = append(out, w)
		}
	}
	return out
}

func gateFor(t *testing.T, a entplatform.AgentResponse, toolset, tool string) any {
	t.Helper()
	gates, _ := toolsetConfigValues(a, toolset)["hil"].(map[string]any)
	return gates[tool]
}

func TestApply_TwoGroupsSharingAMemberResolveWithoutRegardToOrder(t *testing.T) {
	t.Parallel()
	gate := map[string]any{"require_approval": true}
	for _, tc := range []struct {
		name, web, research string
	}{
		{"gate then open", "{require_approval: true}", "{require_approval: false}"},
		{"gate then null", "{require_approval: true}", "null"},
	} {
		for _, order := range []string{"web first", "research first"} {
			t.Run(tc.name+"/"+order, func(t *testing.T) {
				t.Parallel()
				svc, ctx := newTestService(t), t.Context()
				web, research := groupWithHIL("web_tools", tc.web), groupWithHIL("research", tc.research)
				tools := web + research
				if order == "research first" {
					tools = research + web
				}
				resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("shared_groups", tools)}}, testActor)
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				if got := gateFor(t, agentNamed(ctx, t, svc, "shared_groups"), researchToolset, "web_search_google"); !reflect.DeepEqual(got, any(gate)) {
					t.Errorf("web_search_google gate = %v, want %v whichever group is written first", got, gate)
				}
				warnings := warningsContaining(opFor(t, resp, kindAgent, "shared_groups").Warnings, "state different hil for")
				if len(warnings) != 1 || !strings.Contains(warnings[0], "web_search_google") {
					t.Errorf("warnings = %q, want one naming web_search_google", warnings)
				}
			})
		}
	}
}

func TestResolveStatedSettings_Layers(t *testing.T) {
	t.Parallel()
	catalog := toolsetCatalog{toolNamesByToolsetID: map[string][]string{
		builtinWebRequestToolset: {"web_request", "web_search_google"},
		researchToolset:          {"web_search_google"},
	}}
	gate := map[string]any{"require_approval": true}
	open := map[string]any{"require_approval": false}
	quiet := map[string]any{"show_args": false}
	web := func(value any) toolSetting {
		return toolSetting{index: 0, kind: hilSetting, group: "web_tools", value: value}
	}
	research := func(value any) toolSetting {
		return toolSetting{index: 1, kind: hilSetting, group: "research", value: value}
	}
	valueOf := func(t *testing.T, got []toolSetting, tool string) any {
		t.Helper()
		for _, s := range got {
			if s.tool == tool && s.kind == hilSetting {
				return s.value
			}
		}
		t.Fatalf("no hil resolved for %s in %v", tool, got)
		return nil
	}

	t.Run("a gating group beats an open or null one, in either order", func(t *testing.T) {
		for _, stated := range [][]toolSetting{{web(gate), research(open)}, {research(open), web(gate)}, {web(gate), research(nil)}, {research(nil), web(gate)}} {
			got, warnings := resolveStatedSettings(stated, nil, catalog)
			if v := valueOf(t, got, "web_search_google"); !reflect.DeepEqual(v, any(gate)) {
				t.Errorf("%v: web_search_google = %v, want the gate", stated, v)
			}
			if len(warnings) != 1 {
				t.Errorf("%v: warnings = %q, want one for the disagreement", stated, warnings)
			}
		}
	})
	t.Run("two groups that agree do not warn", func(t *testing.T) {
		if _, warnings := resolveStatedSettings([]toolSetting{web(gate), research(map[string]any{"require_approval": true})}, nil, catalog); len(warnings) != 0 {
			t.Errorf("warnings = %q, want none", warnings)
		}
	})
	t.Run("a tool entry merges over its group field by field", func(t *testing.T) {
		own := toolSetting{tool: "web_request", kind: hilSetting, value: quiet}
		for _, stated := range [][]toolSetting{{web(gate), own}, {own, web(gate)}} {
			got, _ := resolveStatedSettings(stated, nil, catalog)
			want := map[string]any{"require_approval": true, "show_args": false}
			if v := valueOf(t, got, "web_request"); !reflect.DeepEqual(v, any(want)) {
				t.Errorf("web_request = %v, want %v", v, want)
			}
		}
	})
	t.Run("a tool entry's own require_approval_when replaces the group's rules", func(t *testing.T) {
		groupRules := map[string]any{"require_approval_when": []any{map[string]any{"arg": "url", "eq": "a"}}}
		ownRules := map[string]any{"require_approval_when": []any{map[string]any{"arg": "url", "eq": "b"}}}
		got, _ := resolveStatedSettings([]toolSetting{web(groupRules), {tool: "web_request", kind: hilSetting, value: ownRules}}, nil, catalog)
		if v := valueOf(t, got, "web_request"); !reflect.DeepEqual(v, any(ownRules)) {
			t.Errorf("web_request = %v, want %v", v, ownRules)
		}
	})
	t.Run("a group's tools map is laid over its defaults for that member only", func(t *testing.T) {
		grouped := toolSetting{index: 0, kind: hilSetting, group: "web_tools", value: map[string]any{"show_args": false},
			memberOverrides: map[string]any{"web_request": map[string]any{"require_approval": true}}}
		got, _ := resolveStatedSettings([]toolSetting{grouped}, nil, catalog)
		if v, want := valueOf(t, got, "web_request"), any(map[string]any{"show_args": false, "require_approval": true}); !reflect.DeepEqual(v, want) {
			t.Errorf("web_request = %v, want %v", v, want)
		}
		if v, want := valueOf(t, got, "web_search_google"), any(map[string]any{"show_args": false}); !reflect.DeepEqual(v, want) {
			t.Errorf("web_search_google = %v, want only the defaults %v", v, want)
		}
	})
	t.Run("a group with only a tools map states no default for the other members", func(t *testing.T) {
		grouped := toolSetting{index: 0, kind: hilSetting, group: "web_tools", value: map[string]any{}, noDefault: true,
			memberOverrides: map[string]any{"web_request": gate}}
		got, _ := resolveStatedSettings([]toolSetting{grouped}, nil, catalog)
		if len(got) != 1 || got[0].tool != "web_request" {
			t.Errorf("resolved = %v, want only web_request", got)
		}
	})
}

func TestStatedBuiltinToolSettings_EmptyHILIsARemoval(t *testing.T) {
	t.Parallel()
	stated, _ := statedBuiltinToolSettings(map[string]any{"tools": []any{
		map[string]any{"tool_type": "builtin", "tool_name": "web_request", "hil": map[string]any{}},
		map[string]any{"tool_type": "builtin-group", "group_name": "web_tools", "hil": map[string]any{}},
	}})
	if len(stated) != 2 || stated[0].value != nil || stated[1].value != nil {
		t.Errorf("stated = %+v, want both empty blocks read as null", stated)
	}
}

func TestApply_EmptyHILStoresNothing(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("empty_hil",
		"        - tool_type: builtin\n          tool_name: web_request\n          hil: {}\n")}}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := toolsetConfigValues(agentNamed(ctx, t, svc, "empty_hil"), builtinWebRequestToolset); got != nil {
		t.Errorf("configValues = %v, want nothing stored for hil: {}", got)
	}
}

func TestApply_RemovingOrWeakeningAStoredGateIsReported(t *testing.T) {
	t.Parallel()
	const removed = "the approval gate for built-in tool web_request (toolset builtin_web_request_tools) was removed: the hil this build states no longer requires approval, so the tool now runs without asking"
	gated := "        - tool_type: builtin\n          tool_name: web_request\n          hil: {require_approval: true}\n"
	const weakened = "the approval gate for built-in tool web_request (toolset builtin_web_request_tools) was weakened: it asked on every call and now asks only when its rules match"
	for _, tc := range []struct{ name, hil string }{
		{"an unconditional gate that drops to rules only", "{require_approval: false, require_approval_when: [{arg: url, eq: x}]}"},
		{"an unconditional gate restated as rules only with require_approval omitted", "{require_approval_when: [{arg: url, eq: x}]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, ctx := newTestService(t), t.Context()
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("gate_weaker", gated)}}, testActor); err != nil {
				t.Fatalf("seed: %v", err)
			}
			rulesOnly := "        - tool_type: builtin\n          tool_name: web_request\n          hil: " + tc.hil + "\n"
			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("gate_weaker", rulesOnly)}}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := warningsContaining(opFor(t, resp, kindAgent, "gate_weaker").Warnings, "approval gate"); !slices.Equal(got, []string{weakened}) {
				t.Errorf("warnings = %q, want [%q]", got, weakened)
			}
		})
	}
	for _, tc := range []struct {
		name, tools string
		wantWarning bool
	}{
		{"hil null", "        - tool_type: builtin\n          tool_name: web_request\n          hil: null\n", true},
		{"hil empty", "        - tool_type: builtin\n          tool_name: web_request\n          hil: {}\n", true},
		{"hil that no longer gates", "        - tool_type: builtin\n          tool_name: web_request\n          hil: {show_args: false}\n", true},
		{"a group block without require_approval", webTools + "          hil: {show_args: false}\n", true},
		{"a group null", groupWithHIL("web_tools", "null"), true},
		{"hil left out", "        - tool_type: builtin\n          tool_name: web_request\n", false},
		{"hil still gating", "        - tool_type: builtin\n          tool_name: web_request\n          hil: {require_approval: true, approval_message: \"Again?\"}\n", false},
		{"hil gating by rules", "        - tool_type: builtin\n          tool_name: web_request\n          hil: {require_approval: true, require_approval_when: [{arg: url, eq: x}]}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, ctx := newTestService(t), t.Context()
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("gate_loss", gated)}}, testActor); err != nil {
				t.Fatalf("seed: %v", err)
			}
			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("gate_loss", tc.tools)}}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got := warningsContaining(opFor(t, resp, kindAgent, "gate_loss").Warnings, "approval gate")
			switch {
			case tc.wantWarning && !slices.Equal(got, []string{removed}):
				t.Errorf("warnings = %q, want exactly [%q]", got, removed)
			case !tc.wantWarning && len(got) != 0:
				t.Errorf("warnings = %q, want no gate warning", got)
			}
		})
	}
}

func TestApply_PlatformManagedSettingsAreKeptAndReportedOnce(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(extraKeys, tools string) BuildOperationResult {
		t.Helper()
		c := builtinSettingsAgent("managed_settings", tools)
		c.Content = strings.Replace(c.Content, "      tools:\n", extraKeys+"      tools:\n", 1)
		resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{c}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, "managed_settings")
	}
	const fetch = "        - tool_type: builtin\n          tool_name: web_request\n"

	op := apply("      volumes: [{name: scratch, provision: prompt_user}]\n      networks: [{name: out, allowlist: [example.com]}]\n",
		fetch+"          tool_config: {timeout: 30, allow_loopback: true}\n")
	const want = "volumes, networks, tool_config.allow_loopback are set with `sam config apply`, not by the Builder, so they were not saved and any stored value was kept"
	if got := warningsContaining(op.Warnings, "set with `sam config apply`, not by the Builder"); !slices.Equal(got, []string{want}) {
		t.Errorf("warnings = %q, want exactly [%q]", got, want)
	}
	created := agentNamed(ctx, t, svc, "managed_settings")
	if got, wantConfig := toolsetConfigValues(created, builtinWebRequestToolset), map[string]any{"web_request": map[string]any{"timeout": float64(30)}}; !reflect.DeepEqual(got, wantConfig) {
		t.Errorf("created tool_config = %v, want allow_loopback dropped: %v", got, wantConfig)
	}
	if blob := storedAdditional(ctx, t, svc, "managed_settings"); blob["volumes"] != nil || blob["networks"] != nil {
		t.Errorf("stored blob = %v, want no volumes or networks from the Builder", blob)
	}

	seedToolsetConfigs(t, svc, "managed_settings", []entplatform.ToolsetConfigBinding{
		{ToolsetName: builtinWebRequestToolset, ConfigValues: map[string]any{"web_request": map[string]any{"timeout": float64(30), "allow_loopback": true}}},
	}, json.RawMessage(`{"volumes":[{"name":"data","provision":"prompt_user"}]}`))

	op = apply("      volumes: [{name: other, provision: prompt_user}]\n", fetch+"          tool_config: {timeout: 60, allow_loopback: false}\n")
	if got := warningsContaining(op.Warnings, "set with `sam config apply`, not by the Builder"); len(got) != 1 || !strings.HasPrefix(got[0], "volumes, tool_config.allow_loopback are set") {
		t.Errorf("warnings = %q, want one naming volumes and tool_config.allow_loopback", got)
	}
	after := agentNamed(ctx, t, svc, "managed_settings")
	wantConfig := map[string]any{"web_request": map[string]any{"timeout": float64(60), "allow_loopback": true}}
	if got := toolsetConfigValues(after, builtinWebRequestToolset); !reflect.DeepEqual(got, wantConfig) {
		t.Errorf("tool_config = %v, want the stored allow_loopback kept: %v", got, wantConfig)
	}
	if volumes, _ := storedAdditional(ctx, t, svc, "managed_settings")["volumes"].([]any); len(volumes) != 1 || volumes[0].(map[string]any)["name"] != "data" {
		t.Errorf("stored volumes = %v, want the stored slot untouched", volumes)
	}

	op = apply("", fetch+"          tool_config: {allow_loopback: false}\n")
	if got := toolsetConfigValues(agentNamed(ctx, t, svc, "managed_settings"), builtinWebRequestToolset); !reflect.DeepEqual(got, wantConfig) {
		t.Errorf("a tool_config that states only allow_loopback changed the stored one: %v", got)
	}

	op = apply("", fetch+"          tool_config: null\n")
	if got, wantKept := toolsetConfigValues(agentNamed(ctx, t, svc, "managed_settings"), builtinWebRequestToolset), map[string]any{"web_request": map[string]any{"allow_loopback": true}}; !reflect.DeepEqual(got, wantKept) {
		t.Errorf("tool_config: null left %v, want only the managed allow_loopback: %v", got, wantKept)
	}
	if got := warningsContaining(op.Warnings, "removed except allow_loopback"); len(got) != 1 {
		t.Errorf("warnings = %q, want one saying allow_loopback was kept", op.Warnings)
	}
}

func TestApply_AnOwnersWeakerCopyDoesNotReplaceAGate(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(tools string) BuildOperationResult {
		t.Helper()
		resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("weaker_owner", tools)}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, "weaker_owner")
	}
	tools := webTools + researchGroup + "        - tool_type: builtin\n          tool_name: web_search_google\n"
	apply(tools)
	nonGating := map[string]any{"require_approval": false}
	nonOwnerGate := map[string]any{"require_approval": true, "approval_message": "kept"}
	seedToolsetConfigs(t, svc, "weaker_owner", []entplatform.ToolsetConfigBinding{
		{ToolsetName: researchToolset, ConfigValues: map[string]any{"hil": map[string]any{"web_search_google": nonGating}}},
		{ToolsetName: builtinWebRequestToolset, ConfigValues: map[string]any{"hil": map[string]any{"web_search_google": nonOwnerGate}}},
	}, nil)

	op := apply(tools)
	got := agentNamed(ctx, t, svc, "weaker_owner")
	if gate := gateFor(t, got, researchToolset, "web_search_google"); !reflect.DeepEqual(gate, any(nonOwnerGate)) {
		t.Errorf("owner's gate = %v, want the gating copy %v", gate, nonOwnerGate)
	}
	if stray := toolsetConfigValues(got, builtinWebRequestToolset); stray != nil {
		t.Errorf("non-owner copy survived: %v", stray)
	}
	if w := warningsContaining(op.Warnings, "requires approval"); len(w) != 1 ||
		!strings.Contains(w[0], "web_search_google") || !strings.Contains(w[0], "so the gate stays") {
		t.Errorf("warnings = %q, want one saying the gate was kept", op.Warnings)
	}
	if len(warningsContaining(op.Warnings, "approval gate")) != 0 {
		t.Errorf("warnings = %q, want no removed-gate warning when the gate was kept", op.Warnings)
	}
}

func TestApply_ChangingAnExistingGateIsSaved(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(message string) BuildOperationResult {
		t.Helper()
		resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("edited_gate",
			"        - tool_type: builtin\n          tool_name: web_request\n          hil: {require_approval: true, approval_message: \""+message+"\"}\n")}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, "edited_gate")
	}
	apply("Fetch it?")
	if op := apply("Really fetch it?"); op.Action != actionUpdate {
		t.Fatalf("op = %+v, want an update for the changed message", op)
	}
	want := map[string]any{"require_approval": true, "approval_message": "Really fetch it?"}
	if got := gateFor(t, agentNamed(ctx, t, svc, "edited_gate"), builtinWebRequestToolset, "web_request"); !reflect.DeepEqual(got, any(want)) {
		t.Errorf("stored gate = %v, want %v", got, want)
	}
}

func TestApply_ToolConfigFollowsItsToolToANewOwner(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(tools string) {
		t.Helper()
		if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("config_mover", tools)}}, testActor); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	search := "        - tool_type: builtin\n          tool_name: web_search_google\n          tool_config: {max_results: 3}\n"
	apply(search)
	if toolsetConfigValues(agentNamed(ctx, t, svc, "config_mover"), researchToolset)["web_search_google"] == nil {
		t.Fatal("seed: tool_config should sit under builtin_research_tools")
	}
	apply(strings.Replace(search, "          tool_config: {max_results: 3}\n", "", 1) + "        - tool_type: builtin\n          tool_name: web_request\n")
	got := agentNamed(ctx, t, svc, "config_mover")
	want := map[string]any{"web_search_google": map[string]any{"max_results": float64(3)}}
	if !reflect.DeepEqual(toolsetConfigValues(got, builtinWebRequestToolset), want) || toolsetConfigValues(got, researchToolset) != nil {
		t.Errorf("toolsetConfigs = %+v, want the tool_config moved to %s", got.ToolsetConfigs, builtinWebRequestToolset)
	}
}

func TestApply_GroupHILToolsMapGatesNamedMembers(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("group_tools_map",
		webTools+"          hil:\n            show_args: false\n            tools:\n              web_request: {require_approval: true}\n")}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "group_tools_map"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want a succeeded save", op)
	}
	got := agentNamed(ctx, t, svc, "group_tools_map")
	if g := gateFor(t, got, builtinWebRequestToolset, "web_request"); !reflect.DeepEqual(g, any(map[string]any{"show_args": false, "require_approval": true})) {
		t.Errorf("web_request = %v, want the member's override over the group default", g)
	}
	if g := gateFor(t, got, builtinWebRequestToolset, "web_search_google"); !reflect.DeepEqual(g, any(map[string]any{"show_args": false})) {
		t.Errorf("web_search_google = %v, want only the group default", g)
	}
}

func TestApply_GroupHILToolsNamingANonMemberIsRefusedOnBothPaths(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	build := []BuildComponentInput{builtinSettingsAgent("group_tools_typo",
		webTools+"          hil:\n            tools:\n              not_a_member: {require_approval: true}\n")}
	const path = "tools[0].hil.tools.not_a_member"
	for _, dryRun := range []bool{true, false} {
		_, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: build}, testActor)
		invalid, ok := errors.AsType[*entplatform.ValidationError](err)
		if !ok || len(invalid.Details[path]) == 0 {
			t.Errorf("dryRun=%v: error = %v, want a validation error with a detail at %s", dryRun, err, path)
		}
	}
}

func TestApply_DryRunRefusesBuiltinBindingsTheWriteRefuses(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	tools := "        - tool_type: builtin\n          tool_name: volume_read\n"
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("binding_parity", tools)}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedToolsetConfigs(t, svc, "binding_parity", []entplatform.ToolsetConfigBinding{
		{ToolsetName: "builtin_volume_tools", VolumeBindings: map[string]string{"workspace": "data"}},
	}, json.RawMessage(`{"volumes":[{"name":"data","provision":"prompt_user"}]}`))
	seedRawAdditional(ctx, t, svc, "binding_parity", `{"volumes":[{"name":"other","provision":"prompt_user"}]}`)

	build := []BuildComponentInput{builtinSettingsAgent("binding_parity", tools+"          hil: {require_approval: true}\n")}
	dry, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: build}, testActor)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	applied, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	for which, resp := range map[string]*ApplyBuildResponse{"dry run": dry, "apply": applied} {
		op := opFor(t, resp, kindAgent, "binding_parity")
		if op.Status != statusFailed || op.Error == nil || !strings.Contains(strings.Join(slices.Concat(mapValues(op.Error.ValidationDetails)...), " "), `unknown volume slot "data"`) {
			t.Errorf("%s op = %+v (error %+v), want a failure naming the unknown volume slot", which, op, op.Error)
		}
	}
}

func mapValues(m map[string][]string) [][]string {
	out := make([][]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
