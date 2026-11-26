// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func builtinSettingsAgent(name, tools string) BuildComponentInput {
	return BuildComponentInput{Kind: kindAgent, Name: name, Content: `
apps:
  - name: ` + name + `
    app_config:
      agent_name: ` + name + `
      instruction: You are a test agent that answers questions.
      tools:
` + tools + `      agent_card:
        description: "An agent with configured built-in tools."
`}
}

const webRequestGated = `        - tool_type: builtin
          tool_name: web_request
          tool_config: {timeout: 30}
          hil: {require_approval: true, approval_message: "Fetch {{.url}}?"}
`

var webRequestGatedValues = map[string]any{
	"web_request": map[string]any{"timeout": float64(30)},
	"hil":         map[string]any{"web_request": map[string]any{"require_approval": true, "approval_message": "Fetch {{.url}}?"}},
}

func toolsetConfigValues(a entplatform.AgentResponse, toolset string) map[string]any {
	for _, b := range a.ToolsetConfigs {
		if b.ToolsetName == toolset {
			return b.ConfigValues
		}
	}
	return nil
}

func TestApply_BuiltinToolSettingsAreSavedOnCreate(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("gated_agent", webRequestGated)}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "gated_agent"); op.Status != statusSucceeded || len(op.Warnings) != 0 {
		t.Fatalf("op = %+v, want a clean succeeded create", op)
	}
	if got := toolsetConfigValues(agentNamed(ctx, t, svc, "gated_agent"), builtinWebRequestToolset); !reflect.DeepEqual(got, webRequestGatedValues) {
		t.Errorf("configValues = %v, want %v", got, webRequestGatedValues)
	}
}

func TestApply_UnreadableToolSettingIsReported(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("typo_agent",
		"        - tool_type: builtin\n          tool_name: web_request\n          hil: true\n")}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindAgent, "typo_agent")
	if !slices.ContainsFunc(op.Warnings, func(w string) bool { return strings.Contains(w, "tools[0].hil is a boolean") }) {
		t.Errorf("warnings = %v, want the unreadable hil reported", op.Warnings)
	}
}

func TestApply_BuiltinToolSettingsMergeOnUpdate(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(tools string) BuildOperationResult {
		t.Helper()
		resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("merging_agent", tools)}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, "merging_agent")
	}
	stored := func() map[string]any {
		return toolsetConfigValues(agentNamed(ctx, t, svc, "merging_agent"), builtinWebRequestToolset)
	}
	plain := "        - tool_type: builtin\n          tool_name: web_request\n"

	apply(plain)
	if op := apply(webRequestGated); op.Action != actionUpdate || !reflect.DeepEqual(stored(), webRequestGatedValues) {
		t.Fatalf("op = %+v, stored = %v; want the settings written by an update", op, stored())
	}
	if op := apply(webRequestGated); op.Action != actionUnchanged {
		t.Errorf("re-save = %+v, want unchanged", op)
	}
	if op := apply(plain); op.Action != actionUnchanged || !reflect.DeepEqual(stored(), webRequestGatedValues) {
		t.Errorf("omitting the settings must keep them: op = %+v, stored = %v", op, stored())
	}
	apply(plain + "          tool_config: null\n")
	if got := stored(); got["web_request"] != nil || got["hil"] == nil {
		t.Errorf("tool_config: null must remove only tool_config, got %v", got)
	}
	apply(plain + "          hil: null\n")
	if got := stored(); got != nil {
		t.Errorf("removing the last setting must remove the binding, got %v", got)
	}
}

func TestApply_BuiltinToolSettingsKeepVolumeBindings(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	tools := "        - tool_type: builtin\n          tool_name: volume_read\n"
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("volume_agent", tools)}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := agentNamed(ctx, t, svc, "volume_agent")
	if _, err := svc.deps.Agents.Update(ctx, a.ID, entplatform.UpdateAgentRequest{
		Name: a.Name, Description: a.Description, SystemPrompt: a.SystemPrompt,
		Toolsets: a.Toolsets, ToolsetExcludedTools: a.ToolsetExcludedTools,
		AdditionalConfigurations: json.RawMessage(`{"volumes":[{"name":"ws","provision":"auto_create"}]}`),
		ToolsetConfigs:           []entplatform.ToolsetConfigBinding{{ToolsetName: "builtin_volume_tools", VolumeBindings: map[string]string{"workspace": "ws"}}},
	}, "agent-page"); err != nil {
		t.Fatalf("seed volume binding: %v", err)
	}
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("volume_agent",
		tools+"          hil: {require_approval: true}\n")}}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	b := agentNamed(ctx, t, svc, "volume_agent").ToolsetConfigs
	if len(b) != 1 || b[0].VolumeBindings["workspace"] != "ws" || b[0].ConfigValues["hil"] == nil {
		t.Errorf("toolsetConfigs = %+v, want the volume binding kept and the hil added", b)
	}
}

func TestApply_BuiltinToolSettingsFollowTheirTool(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(tools string) BuildOperationResult {
		t.Helper()
		resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("moving_agent", tools)}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, "moving_agent")
	}
	search := "        - tool_type: builtin\n          tool_name: web_search_google\n"
	apply(search + "          hil: {require_approval: true}\n")
	if toolsetConfigValues(agentNamed(ctx, t, svc, "moving_agent"), "builtin_research_tools")["hil"] == nil {
		t.Fatal("seed: a lone web_search_google should be gated under builtin_research_tools")
	}

	apply(search + "        - tool_type: builtin\n          tool_name: web_request\n")
	got := agentNamed(ctx, t, svc, "moving_agent")
	want := map[string]any{"hil": map[string]any{"web_search_google": map[string]any{"require_approval": true}}}
	if !reflect.DeepEqual(toolsetConfigValues(got, builtinWebRequestToolset), want) || len(got.ToolsetConfigs) != 1 {
		t.Errorf("toolsetConfigs = %+v, want the gate moved to %s", got.ToolsetConfigs, builtinWebRequestToolset)
	}
	if op := apply(search + "        - tool_type: builtin\n          tool_name: web_request\n"); op.Action != actionUnchanged {
		t.Errorf("re-save after a move = %+v, want unchanged", op)
	}

	op := apply("        - tool_type: builtin\n          tool_name: get_current_time\n")
	if !slices.ContainsFunc(op.Warnings, func(w string) bool {
		return strings.Contains(w, "dropped the stored hil for built-in tool web_search_google")
	}) {
		t.Errorf("warnings = %v, want the dropped gate named", op.Warnings)
	}
}

func TestApply_SharedMemberGateMovesToTheOwner(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(tools string) {
		t.Helper()
		if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("shared_agent", tools)}}, testActor); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	gatedToolsets := func() []string {
		var holders []string
		for _, b := range agentNamed(ctx, t, svc, "shared_agent").ToolsetConfigs {
			if gates, _ := b.ConfigValues["hil"].(map[string]any); gates["web_search_google"] != nil {
				holders = append(holders, b.ToolsetName)
			}
		}
		return holders
	}
	webTools := "        - tool_type: builtin-group\n          group_name: web_tools\n"
	research := "        - tool_type: builtin-group\n          group_name: research\n"
	gated := "        - tool_type: builtin\n          tool_name: web_search_google\n          hil: {require_approval: true}\n"

	apply(webTools + gated)
	if got := gatedToolsets(); !reflect.DeepEqual(got, []string{builtinWebRequestToolset}) {
		t.Fatalf("seed: gate held by %v, want only %s", got, builtinWebRequestToolset)
	}

	apply(webTools + research + gated)
	if got := gatedToolsets(); !reflect.DeepEqual(got, []string{"builtin_research_tools"}) {
		t.Errorf("after adding research: gate held by %v, want only builtin_research_tools", got)
	}

	apply(webTools + research + strings.Replace(gated, "hil: {require_approval: true}", "hil: null", 1))
	if got := gatedToolsets(); len(got) != 0 {
		t.Errorf("after hil: null: gate still held by %v", got)
	}
}

func seedToolsetConfigs(t *testing.T, svc *Service, name string, configs []entplatform.ToolsetConfigBinding, extra json.RawMessage) {
	t.Helper()
	a := agentNamed(t.Context(), t, svc, name)
	if _, err := svc.deps.Agents.Update(t.Context(), a.ID, entplatform.UpdateAgentRequest{
		Name: a.Name, Description: a.Description, SystemPrompt: a.SystemPrompt,
		Toolsets: a.Toolsets, ToolsetExcludedTools: a.ToolsetExcludedTools,
		AdditionalConfigurations: extra,
		ToolsetConfigs:           configs,
	}, "agent-page"); err != nil {
		t.Fatalf("seed toolsetConfigs: %v", err)
	}
}

func TestApply_OwnersGateSurvivesANonOwnersCopy(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(tools string) BuildOperationResult {
		t.Helper()
		resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("owner_agent", tools)}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, "owner_agent")
	}
	tools := "        - tool_type: builtin-group\n          group_name: web_tools\n" +
		"        - tool_type: builtin-group\n          group_name: research\n" +
		"        - tool_type: builtin\n          tool_name: web_search_google\n"
	apply(tools)
	ownerGate := map[string]any{"require_approval": true, "approval_message": "owner"}
	seedToolsetConfigs(t, svc, "owner_agent", []entplatform.ToolsetConfigBinding{
		{ToolsetName: "builtin_research_tools", ConfigValues: map[string]any{"hil": map[string]any{"web_search_google": ownerGate}}},
		{ToolsetName: builtinWebRequestToolset, ConfigValues: map[string]any{"hil": map[string]any{"web_search_google": map[string]any{"require_approval": false}}}},
	}, nil)

	op := apply(tools)
	got := agentNamed(ctx, t, svc, "owner_agent")
	wantOwner := map[string]any{"hil": map[string]any{"web_search_google": ownerGate}}
	if !reflect.DeepEqual(toolsetConfigValues(got, "builtin_research_tools"), wantOwner) {
		t.Errorf("owner configValues = %v, want %v", toolsetConfigValues(got, "builtin_research_tools"), wantOwner)
	}
	if stray := toolsetConfigValues(got, builtinWebRequestToolset); stray != nil {
		t.Errorf("non-owner copy survived: %v", stray)
	}
	if len(op.Warnings) != 1 || !strings.Contains(op.Warnings[0], "web_search_google") ||
		!strings.Contains(op.Warnings[0], "builtin_research_tools") || !strings.Contains(op.Warnings[0], builtinWebRequestToolset) {
		t.Errorf("warnings = %v, want one naming the tool, the kept toolset and the dropped toolset", op.Warnings)
	}
	if again := apply(tools); again.Action != actionUnchanged || len(again.Warnings) != 0 {
		t.Errorf("re-save = %+v, want unchanged and silent", again)
	}
}

func TestApply_UnboundVolumeToolsetConfigIsCleared(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	apply := func(tools string) BuildOperationResult {
		t.Helper()
		resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("unbound_agent", tools)}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, "unbound_agent")
	}
	apply("        - tool_type: builtin\n          tool_name: volume_read\n")
	seedToolsetConfigs(t, svc, "unbound_agent", []entplatform.ToolsetConfigBinding{{
		ToolsetName:    "builtin_volume_tools",
		VolumeBindings: map[string]string{"workspace": "ws"},
		ConfigValues:   map[string]any{"hil": map[string]any{"volume_read": map[string]any{"require_approval": true}}},
	}}, json.RawMessage(`{"volumes":[{"name":"ws","provision":"auto_create"}]}`))

	timeOnly := "        - tool_type: builtin\n          tool_name: get_current_time\n"
	apply(timeOnly)
	for _, b := range agentNamed(ctx, t, svc, "unbound_agent").ToolsetConfigs {
		if b.ToolsetName == "builtin_volume_tools" {
			t.Errorf("builtin_volume_tools row survived unbinding: %+v", b)
		}
	}
	if again := apply(timeOnly); again.Action != actionUnchanged || len(again.Warnings) != 0 {
		t.Errorf("re-save = %+v, want unchanged and silent", again)
	}
}

func TestCarryBuiltinToolSettings_StatedSettingForUnownedToolIsReported(t *testing.T) {
	t.Parallel()
	catalog := toolsetCatalog{toolNamesByToolsetID: map[string][]string{builtinWebRequestToolset: {"web_request"}}}
	_, warnings := carryBuiltinToolSettings(nil, []string{builtinWebRequestToolset}, nil, builtinToolSettings{
		stated:  []toolSetting{{tool: "volume_read", kind: hilSetting, value: map[string]any{"require_approval": true}}},
		catalog: catalog,
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "hil for built-in tool volume_read was not saved") {
		t.Errorf("warnings = %v, want the unsaved hil named", warnings)
	}
}

func TestCarryBuiltinToolSettings_GroupHILWithNoStorableToolsIsReported(t *testing.T) {
	t.Parallel()
	catalog := toolsetCatalog{toolNamesByToolsetID: map[string][]string{builtinWebRequestToolset: {"web_request"}}}
	gate := map[string]any{"require_approval": true}
	tests := []struct {
		name     string
		stated   []toolSetting
		excluded map[string][]string
		want     []string
	}{
		{"group missing from the catalog", []toolSetting{{index: 3, kind: hilSetting, group: "ghost_tools", value: gate}}, nil,
			[]string{"tools[3] (ghost_tools): hil on this built-in group was not saved: it has no tools whose approval can be stored"}},
		{"every member excluded", []toolSetting{{index: 1, kind: hilSetting, group: "web_tools", value: gate}}, map[string][]string{builtinWebRequestToolset: {"web_request"}},
			[]string{"tools[1] (web_tools): hil on this built-in group was not saved: it has no tools whose approval can be stored"}},
		{"explicit null stays silent", []toolSetting{{index: 3, kind: hilSetting, group: "ghost_tools"}}, nil, nil},
		{"group with members stays silent", []toolSetting{{index: 0, kind: hilSetting, group: "web_tools", value: gate}}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, warnings := carryBuiltinToolSettings(nil, []string{builtinWebRequestToolset}, tt.excluded, builtinToolSettings{stated: tt.stated, catalog: catalog})
			if !reflect.DeepEqual(warnings, tt.want) {
				t.Errorf("warnings = %v, want %v", warnings, tt.want)
			}
		})
	}
}

func TestStatedBuiltinToolSettings_RecordsTheEntryIndex(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{"tools": []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
		map[string]any{"tool_type": "builtin-group", "group_name": "web_tools", "hil": map[string]any{"require_approval": true}},
	}}
	stated, warnings := statedBuiltinToolSettings(appConfig)
	if len(warnings) != 0 || len(stated) != 1 || stated[0].index != 1 {
		t.Errorf("stated = %+v, warnings = %v, want one setting from tools[1]", stated, warnings)
	}
}

func TestCarryBuiltinToolSettings_DroppedWarningIsNotRepeated(t *testing.T) {
	t.Parallel()
	catalog := toolsetCatalog{toolNamesByToolsetID: map[string][]string{
		builtinWebRequestToolset: {"web_search_google"},
		"builtin_research_tools": {"web_search_google"},
	}}
	gate := map[string]any{"hil": map[string]any{"web_search_google": map[string]any{"require_approval": true}}}
	stored := []entplatform.ToolsetConfigBinding{
		{ToolsetName: builtinWebRequestToolset, ConfigValues: gate},
		{ToolsetName: "builtin_research_tools", ConfigValues: gate},
	}
	_, warnings := carryBuiltinToolSettings(stored, nil, nil, builtinToolSettings{catalog: catalog})
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want the one dropped gate named once", warnings)
	}
}

func TestApply_GroupHILGatesEveryMemberAndToolEntriesCannotCancelIt(t *testing.T) {
	t.Parallel()
	groupEntry := `        - tool_type: builtin-group
          group_name: web_tools
          hil: {require_approval: true}
          tool_config: {timeout: 30}
`
	toolEntry := `        - tool_type: builtin
          tool_name: web_request
          hil: {require_approval: false}
`
	want := map[string]any{"hil": map[string]any{
		"web_request":       map[string]any{"require_approval": true},
		"web_search_google": map[string]any{"require_approval": true},
	}}
	for name, tools := range map[string]string{"group_first": groupEntry + toolEntry, "tool_first": toolEntry + groupEntry} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, ctx := newTestService(t), t.Context()
			agent := "group_gated_" + name
			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent(agent, tools)}}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := toolsetConfigValues(agentNamed(ctx, t, svc, agent), builtinWebRequestToolset); !reflect.DeepEqual(got, want) {
				t.Errorf("configValues = %v, want %v", got, want)
			}
			var groupConfigWarnings []string
			for _, w := range opFor(t, resp, kindAgent, agent).Warnings {
				if strings.Contains(w, "tool_config") {
					groupConfigWarnings = append(groupConfigWarnings, w)
				}
			}
			wantWarning := "tools[" + map[string]string{"group_first": "0", "tool_first": "1"}[name] + "].tool_config on a builtin-group was not saved; set tool_config on a builtin entry for each tool that needs it"
			if len(groupConfigWarnings) != 1 || groupConfigWarnings[0] != wantWarning {
				t.Errorf("tool_config warnings = %v, want exactly [%q]", groupConfigWarnings, wantWarning)
			}
		})
	}
}

func TestApply_GroupHILIsNotReportedAsUnstored(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("group_hil_only", `        - tool_type: builtin-group
          group_name: web_tools
          hil: {require_approval: true}
`)}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "group_hil_only"); len(op.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", op.Warnings)
	}
}

func TestResolveStatedSettings(t *testing.T) {
	t.Parallel()
	catalog := toolsetCatalog{toolNamesByToolsetID: map[string][]string{builtinWebRequestToolset: {"web_request", "web_search_google"}}}
	gate := map[string]any{"require_approval": true}
	open := map[string]any{"require_approval": false}
	group := toolSetting{kind: hilSetting, group: "web_tools", value: gate}
	tests := []struct {
		name     string
		stated   []toolSetting
		excluded map[string][]string
		want     []toolSetting
	}{
		{"group expands to every member", []toolSetting{group}, nil, []toolSetting{
			{tool: "web_request", kind: hilSetting, value: gate},
			{tool: "web_search_google", kind: hilSetting, value: gate},
		}},
		{"excluded member is skipped", []toolSetting{group}, map[string][]string{builtinWebRequestToolset: {"web_request"}}, []toolSetting{
			{tool: "web_search_google", kind: hilSetting, value: gate},
		}},
		{"a tool entry cannot turn its group's gate off when written first", []toolSetting{{tool: "web_request", kind: hilSetting, value: open}, group}, nil, []toolSetting{
			{tool: "web_request", kind: hilSetting, value: gate},
			{tool: "web_search_google", kind: hilSetting, value: gate},
		}},
		{"a tool entry cannot turn its group's gate off when written last", []toolSetting{group, {tool: "web_request", kind: hilSetting, value: open}}, nil, []toolSetting{
			{tool: "web_request", kind: hilSetting, value: gate},
			{tool: "web_search_google", kind: hilSetting, value: gate},
		}},
		{"a tool entry's null cannot cancel the group's gate", []toolSetting{group, {tool: "web_request", kind: hilSetting}}, nil, []toolSetting{
			{tool: "web_request", kind: hilSetting, value: gate},
			{tool: "web_search_google", kind: hilSetting, value: gate},
		}},
		{"a tool entry's tool_config does not displace the group's hil", []toolSetting{group, {tool: "web_request", kind: toolConfigSetting, value: map[string]any{"timeout": 30}}}, nil, []toolSetting{
			{tool: "web_request", kind: hilSetting, value: gate},
			{tool: "web_request", kind: toolConfigSetting, value: map[string]any{"timeout": 30}},
			{tool: "web_search_google", kind: hilSetting, value: gate},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, _ := resolveStatedSettings(tt.stated, tt.excluded, catalog); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveStatedSettings = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCarryBuiltinToolSettings_NullForUnownedToolIsSilent(t *testing.T) {
	t.Parallel()
	catalog := toolsetCatalog{toolNamesByToolsetID: map[string][]string{builtinWebRequestToolset: {"web_request"}}}
	_, warnings := carryBuiltinToolSettings(nil, []string{builtinWebRequestToolset}, nil, builtinToolSettings{
		stated:  []toolSetting{{tool: "volume_read", kind: hilSetting, value: nil}},
		catalog: catalog,
	})
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for the removal of a setting that is not bound", warnings)
	}
}

func TestApply_NonBuiltinEntriesAreNotBuiltinToolSettings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, entry string
	}{
		{name: "an mcp entry named like a built-in tool", entry: "        - tool_type: builtin\n          tool_name: web_request\n        - tool_type: mcp\n          tool_name: web_request\n          connection_params: {type: stdio, command: echo}\n          hil: {require_approval: true}\n"},
		{name: "a python entry with tool_config", entry: "        - tool_type: python\n          tool_name: my_tool\n          component_module: some.module\n          function_name: run\n          tool_config: {timeout: 30}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, ctx := newTestService(t), t.Context()
			build := []BuildComponentInput{builtinSettingsAgent("foreign_agent", tc.entry)}
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor); err != nil {
				t.Fatalf("create: %v", err)
			}
			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			op := opFor(t, resp, kindAgent, "foreign_agent")
			if slices.ContainsFunc(op.Warnings, func(w string) bool {
				return strings.Contains(w, "built-in") || strings.Contains(w, "tool_config") || strings.Contains(w, "hil")
			}) {
				t.Errorf("warnings = %v, want none about built-in tool settings", op.Warnings)
			}
			for _, b := range agentNamed(ctx, t, svc, "foreign_agent").ToolsetConfigs {
				if b.ConfigValues["hil"] != nil || b.ConfigValues["web_request"] != nil {
					t.Errorf("stored built-in settings %v on %s, want none", b.ConfigValues, b.ToolsetName)
				}
			}
		})
	}
}

func TestApply_DryRunRefusesWhatTheWriteRefusesInBuiltinHIL(t *testing.T) {
	t.Parallel()
	const badTimeout = "        - tool_type: builtin\n          tool_name: web_request\n          hil: {timeout: 600}\n"
	for _, stored := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "update"}[stored], func(t *testing.T) {
			t.Parallel()
			svc, ctx := newTestService(t), t.Context()
			if stored {
				plain := "        - tool_type: builtin\n          tool_name: web_request\n"
				if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent("timed_agent", plain)}}, testActor); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			build := []BuildComponentInput{builtinSettingsAgent("timed_agent", badTimeout)}
			dry, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: build}, testActor)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			applied, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			const wantMessage, wantDetail = "invalid approval settings (hil)", "timeout must be a duration string"
			errs := map[string]*OperationError{}
			for which, resp := range map[string]*ApplyBuildResponse{"dry run": dry, "apply": applied} {
				op := opFor(t, resp, kindAgent, "timed_agent")
				if op.Status != statusFailed || op.Error == nil || !strings.Contains(op.Error.Message, wantMessage) ||
					!strings.Contains(strings.Join(slices.Concat(slices.Collect(maps.Values(op.Error.ValidationDetails))...), " "), wantDetail) {
					t.Errorf("%s op = %+v (error %+v), want a failure %q naming %q", which, op, op.Error, wantMessage, wantDetail)
					continue
				}
				errs[which] = op.Error
			}
			if errs["dry run"] != nil && errs["apply"] != nil && !reflect.DeepEqual(errs["dry run"].ValidationDetails, errs["apply"].ValidationDetails) {
				t.Errorf("dry run says %v, apply says %v", errs["dry run"].ValidationDetails, errs["apply"].ValidationDetails)
			}
		})
	}
}

func rawPackageConfigRows(t *testing.T, svc *Service, agentID string) map[string]string {
	t.Helper()
	rows, err := store.NewToolPackageRepo(svc.deps.DB).GetToolsetConfigsForAgent(t.Context(), agentID)
	if err != nil {
		t.Fatalf("read package config rows: %v", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.PackageID] = *row.ConfigValues
	}
	return out
}

func TestApply_CustomPackageBindingSurvivesABuiltinHILChange(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	const pkgName = "secret-pkg"
	repo := store.NewToolPackageRepo(svc.deps.DB)
	pkg := &store.ToolPackage{Name: pkgName, DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	schema := `[{"key":"api_key","type":"string","secret":true,"required":true},{"key":"region","type":"string"}]`
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{PackageID: pkg.ID, ToolName: "fetch", ConfigSchema: &schema}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}

	artifact := func(tools string) []BuildComponentInput {
		c := builtinSettingsAgent("package_agent", tools)
		c.Content = strings.Replace(c.Content, "      tools:\n", "      toolsets: ["+pkgName+"]\n      tools:\n", 1)
		return []BuildComponentInput{c}
	}
	plain := "        - tool_type: builtin\n          tool_name: web_request\n"
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: artifact(plain)}, testActor); err != nil {
		t.Fatalf("create: %v", err)
	}
	seeded := agentNamed(ctx, t, svc, "package_agent")
	if !slices.Contains(seeded.Toolsets, pkgName) {
		t.Fatalf("toolsets = %v, want %s bound", seeded.Toolsets, pkgName)
	}
	if _, err := svc.deps.Agents.Update(ctx, seeded.ID, entplatform.UpdateAgentRequest{
		Name: seeded.Name, Description: seeded.Description, SystemPrompt: seeded.SystemPrompt,
		Toolsets: seeded.Toolsets, ToolsetExcludedTools: seeded.ToolsetExcludedTools,
		ToolsetConfigs: []entplatform.ToolsetConfigBinding{{
			ToolsetName:  pkgName,
			ConfigValues: map[string]any{"fetch": map[string]any{"api_key": "sk-live-0123456789", "region": "us-east-1"}},
		}},
	}, "agent-page"); err != nil {
		t.Fatalf("seed package binding: %v", err)
	}
	rowsBefore := rawPackageConfigRows(t, svc, seeded.ID)
	bindingBefore := toolsetConfigValues(agentNamed(ctx, t, svc, "package_agent"), pkgName)
	if len(rowsBefore[pkg.ID]) == 0 || bindingBefore == nil {
		t.Fatalf("seed did not store the package binding: rows=%v binding=%v", rowsBefore, bindingBefore)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: artifact(webRequestGated)}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "package_agent"); op.Status != statusSucceeded || op.Action != actionUpdate {
		t.Fatalf("op = %+v, want a succeeded update", op)
	}
	after := agentNamed(ctx, t, svc, "package_agent")
	if got := toolsetConfigValues(after, builtinWebRequestToolset); !reflect.DeepEqual(got, webRequestGatedValues) {
		t.Errorf("built-in configValues = %v, want %v", got, webRequestGatedValues)
	}
	if !slices.Contains(after.Toolsets, pkgName) {
		t.Errorf("toolsets = %v, want %s still bound", after.Toolsets, pkgName)
	}
	if got := toolsetConfigValues(after, pkgName); !reflect.DeepEqual(got, bindingBefore) {
		t.Errorf("package binding = %v, want %v", got, bindingBefore)
	}
	if rowsAfter := rawPackageConfigRows(t, svc, seeded.ID); !reflect.DeepEqual(rowsAfter, rowsBefore) {
		t.Errorf("stored package config row = %v, want %v byte for byte", rowsAfter, rowsBefore)
	}
}
