// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const secretToolAgentName = "searching_agent"

func googleSearchAgent(key, cseID string) BuildComponentInput {
	return builtinSettingsAgent(secretToolAgentName,
		"        - tool_type: builtin\n          tool_name: web_search_google\n"+
			"          tool_config: {google_search_api_key: "+key+", google_cse_id: "+cseID+"}\n")
}

func applyGoogleSearchAgent(t *testing.T, svc *Service, key, cseID string) BuildOperationResult {
	t.Helper()
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{googleSearchAgent(key, cseID)}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindAgent, secretToolAgentName)
	if op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want succeeded", op)
	}
	return op
}

func storedGoogleSearchConfig(t *testing.T, svc *Service) map[string]any {
	t.Helper()
	agent := agentNamed(t.Context(), t, svc, secretToolAgentName)
	rows, err := store.NewToolPackageRepo(svc.deps.DB).GetBuiltinToolsetConfigsForAgent(t.Context(), agent.ID)
	if err != nil {
		t.Fatalf("read built-in toolset configs: %v", err)
	}
	for _, row := range rows {
		var values map[string]map[string]any
		if row.ConfigValues == nil || json.Unmarshal([]byte(*row.ConfigValues), &values) != nil {
			continue
		}
		if cfg, ok := values["web_search_google"]; ok {
			return cfg
		}
	}
	t.Fatalf("no stored web_search_google config in %d rows", len(rows))
	return nil
}

func TestApply_BuiltinToolSecretLiteralIsIdempotent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	want := []string{actionCreate, actionUnchanged, actionUnchanged}
	for i, action := range want {
		op := applyGoogleSearchAgent(t, svc, "key-alpha", "cx1")
		if op.Action != action {
			t.Fatalf("apply %d: action = %q (changed %v), want %q", i+1, op.Action, op.ChangedFields, action)
		}
		if op.ChangesDeclined {
			t.Errorf("apply %d: ChangesDeclined = true, want false", i+1)
		}
		if got := storedGoogleSearchConfig(t, svc)["google_search_api_key"]; got != "key-alpha" {
			t.Errorf("apply %d: stored key = %v, want the literal kept", i+1, got)
		}
	}
}

func TestApply_BuiltinToolSecretRotationIsWritten(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	applyGoogleSearchAgent(t, svc, "key-alpha", "cx1")

	op := applyGoogleSearchAgent(t, svc, "key-beta", "cx1")
	if op.Action != actionUpdate {
		t.Fatalf("action = %q, want update for a rotated key", op.Action)
	}
	if strings.Join(op.ChangedFields, ",") != "toolsetConfigs" {
		t.Errorf("changedFields = %v, want [toolsetConfigs]", op.ChangedFields)
	}
	if got := storedGoogleSearchConfig(t, svc)["google_search_api_key"]; got != "key-beta" {
		t.Errorf("stored key = %v, want key-beta", got)
	}
	if op := applyGoogleSearchAgent(t, svc, "key-beta", "cx1"); op.Action != actionUnchanged {
		t.Errorf("re-apply after rotation = %q, want unchanged", op.Action)
	}
}

func TestApply_BuiltinToolNonSecretChangeIsAnUpdateWithTheKeyKept(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	applyGoogleSearchAgent(t, svc, "key-alpha", "cx1")

	op := applyGoogleSearchAgent(t, svc, "key-alpha", "cx2")
	if op.Action != actionUpdate {
		t.Fatalf("action = %q, want update for a changed engine id", op.Action)
	}
	if strings.Join(op.ChangedFields, ",") != "toolsetConfigs" {
		t.Errorf("changedFields = %v, want [toolsetConfigs]", op.ChangedFields)
	}
	stored := storedGoogleSearchConfig(t, svc)
	if stored["google_cse_id"] != "cx2" || stored["google_search_api_key"] != "key-alpha" {
		t.Errorf("stored = %v, want the new engine id and the same key", stored)
	}
}

const requiredPlaceholder = `"<<__SAM_REQUIRED__>>"`

func TestApply_BuiltinToolSecretEmptyStringKeepsTheStoredKey(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	applyGoogleSearchAgent(t, svc, "key-alpha", "cx1")

	for i := range 3 {
		op := applyGoogleSearchAgent(t, svc, `""`, "cx1")
		if op.Action != actionUnchanged {
			t.Fatalf("apply %d with an empty key: action = %q (changed %v), want unchanged", i+1, op.Action, op.ChangedFields)
		}
		if got := storedGoogleSearchConfig(t, svc)["google_search_api_key"]; got != "key-alpha" {
			t.Errorf("apply %d: stored key = %v, want it kept, as for a package secret with no default", i+1, got)
		}
	}
}

func TestApply_BuiltinToolPlaceholderNeverOverwritesTheStoredKey(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	applyGoogleSearchAgent(t, svc, "key-alpha", "cx1")

	if op := applyGoogleSearchAgent(t, svc, requiredPlaceholder, "cx1"); op.Action != actionUnchanged {
		t.Errorf("placeholder alone = %q (changed %v), want unchanged", op.Action, op.ChangedFields)
	}
	if op := applyGoogleSearchAgent(t, svc, requiredPlaceholder, "cx2"); op.Action != actionUpdate {
		t.Errorf("placeholder with a changed engine id = %q, want update", op.Action)
	}
	stored := storedGoogleSearchConfig(t, svc)
	if stored["google_search_api_key"] != "key-alpha" || stored["google_cse_id"] != "cx2" {
		t.Errorf("stored = %v, want the key kept and the engine id updated", stored)
	}
	if op := applyGoogleSearchAgent(t, svc, requiredPlaceholder, "cx2"); op.Action != actionUnchanged {
		t.Errorf("re-apply = %q (changed %v), want unchanged", op.Action, op.ChangedFields)
	}
}

func TestApply_LiteralOnOneToolDoesNotWriteAPlaceholderOverAnother(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	agent := func(deepResearchKey string) BuildComponentInput {
		return builtinSettingsAgent(secretToolAgentName,
			"        - tool_type: builtin\n          tool_name: web_search_google\n"+
				"          tool_config: {google_search_api_key: key-search, google_cse_id: cx1}\n"+
				"        - tool_type: builtin\n          tool_name: deep_research\n"+
				"          tool_config: {google_search_api_key: "+deepResearchKey+", google_cse_id: cx1}\n")
	}
	apply := func(c BuildComponentInput) BuildOperationResult {
		t.Helper()
		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{c}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, secretToolAgentName)
	}
	apply(agent("key-research"))

	for i := range 2 {
		if op := apply(agent(requiredPlaceholder)); op.Action != actionUnchanged {
			t.Errorf("apply %d with a placeholder on deep_research = %q (changed %v), want unchanged", i+1, op.Action, op.ChangedFields)
		}
	}
	agentRow := agentNamed(t.Context(), t, svc, secretToolAgentName)
	rows, err := store.NewToolPackageRepo(svc.deps.DB).GetBuiltinToolsetConfigsForAgent(t.Context(), agentRow.ID)
	if err != nil {
		t.Fatalf("read built-in toolset configs: %v", err)
	}
	keys := map[string]any{}
	for _, row := range rows {
		var values map[string]map[string]any
		if row.ConfigValues != nil && json.Unmarshal([]byte(*row.ConfigValues), &values) == nil {
			for tool, cfg := range values {
				keys[tool] = cfg["google_search_api_key"]
			}
		}
	}
	if keys["web_search_google"] != "key-search" || keys["deep_research"] != "key-research" {
		t.Errorf("stored keys = %v, want both literals intact", keys)
	}
}

func applySecretToolAgent(t *testing.T, svc *Service, toolsYAML string) BuildOperationResult {
	t.Helper()
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent(secretToolAgentName, toolsYAML)}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindAgent, secretToolAgentName)
	if op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want succeeded", op)
	}
	return op
}

func TestApply_PlaceholderKeepsTheStoredKeyWhenTheToolMovesToolset(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	search := func(key string) string {
		return "        - tool_type: builtin\n          tool_name: web_search_google\n" +
			"          tool_config: {google_search_api_key: " + key + ", google_cse_id: cx1}\n"
	}
	const webRequest = "        - tool_type: builtin\n          tool_name: web_request\n"
	applySecretToolAgent(t, svc, search("key-alpha"))

	steps := []struct {
		name  string
		tools string
	}{
		{"adding web_request", search(requiredPlaceholder) + webRequest},
		{"removing web_request", search(requiredPlaceholder)},
	}
	for _, step := range steps {
		applySecretToolAgent(t, svc, step.tools)
		if got := storedGoogleSearchConfig(t, svc)["google_search_api_key"]; got != "key-alpha" {
			t.Errorf("after %s: stored key = %v, want key-alpha kept over the placeholder", step.name, got)
		}
	}
}

func TestApply_PlaceholderWithNothingStoredMarksTheKeyOwed(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	for i, action := range []string{actionCreate, actionUnchanged} {
		op := applyGoogleSearchAgent(t, svc, requiredPlaceholder, "cx1")
		if op.Action != action {
			t.Errorf("apply %d = %q (changed %v), want %q", i+1, op.Action, op.ChangedFields, action)
		}
		if got := storedGoogleSearchConfig(t, svc)["google_search_api_key"]; got != "<<__SAM_REQUIRED__>>" {
			t.Errorf("apply %d: stored key = %v, want the placeholder kept", i+1, got)
		}
	}
}

func TestApply_RedactedSentinelWithNothingBehindItConverges(t *testing.T) {
	t.Parallel()
	const sentinel = `"<REDACTED>"`
	cases := map[string]struct {
		created   string
		wantKey   any
		wantShown bool
	}{
		"stored empty": {
			created: "        - tool_type: builtin\n          tool_name: web_search_google\n" +
				"          tool_config: {google_search_api_key: \"\", google_cse_id: cx1}\n",
			wantKey: "", wantShown: true,
		},
		"nothing stored": {
			created: "        - tool_type: builtin\n          tool_name: web_search_google\n" +
				"          tool_config: {google_cse_id: cx1}\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			applySecretToolAgent(t, svc, tc.created)

			for i := range 3 {
				if op := applyGoogleSearchAgent(t, svc, sentinel, "cx1"); op.Action != actionUnchanged {
					t.Errorf("apply %d with the sentinel = %q (changed %v), want unchanged", i+1, op.Action, op.ChangedFields)
				}
				got, shown := storedGoogleSearchConfig(t, svc)["google_search_api_key"]
				if shown != tc.wantShown || got != tc.wantKey {
					t.Errorf("apply %d: stored key = %v (present %v), want %v (present %v)", i+1, got, shown, tc.wantKey, tc.wantShown)
				}
			}
		})
	}
}

func TestApply_SentinelKeepsTheStoredKeyWhenTheToolMovesToolset(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	applyGoogleSearchAgent(t, svc, "key-alpha", "cx1")
	if got := googleSearchToolsets(t, svc); !slices.Equal(got, []string{"builtin_research_tools"}) {
		t.Fatalf("web_search_google stored under %v, want the research toolset before the move", got)
	}

	applySecretToolAgent(t, svc, "        - tool_type: builtin\n          tool_name: web_search_google\n"+
		"          tool_config: {google_search_api_key: \"<REDACTED>\", google_cse_id: cx1}\n"+
		"        - tool_type: builtin\n          tool_name: web_request\n")

	if got := googleSearchToolsets(t, svc); !slices.Equal(got, []string{"builtin_web_request_tools"}) {
		t.Fatalf("web_search_google stored under %v, want the move to the web request toolset in one save", got)
	}
	if got := storedGoogleSearchConfig(t, svc)["google_search_api_key"]; got != "key-alpha" {
		t.Errorf("stored key after the move = %v, want key-alpha resolved from the toolset it left", got)
	}
}

func googleSearchToolsets(t *testing.T, svc *Service) []string {
	t.Helper()
	agent := agentNamed(t.Context(), t, svc, secretToolAgentName)
	rows, err := store.NewToolPackageRepo(svc.deps.DB).GetBuiltinToolsetConfigsForAgent(t.Context(), agent.ID)
	if err != nil {
		t.Fatalf("read built-in toolset configs: %v", err)
	}
	var toolsets []string
	for _, row := range rows {
		var values map[string]any
		if row.ConfigValues != nil && json.Unmarshal([]byte(*row.ConfigValues), &values) == nil {
			if _, ok := values["web_search_google"]; ok {
				toolsets = append(toolsets, row.ToolsetName)
			}
		}
	}
	slices.Sort(toolsets)
	return toolsets
}

func TestApply_BuiltinToolSecretIsToldNotAppliedWithoutUpdateScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	applyGoogleSearchAgent(t, svc, "key-alpha", "cx1")
	noUpdate := identityWith(withReads(rbac.ScopeAgentBuilderCreate)...)

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{googleSearchAgent("key-beta", "cx1")}}, noUpdate)
	if err != nil {
		t.Fatalf("the build must not be refused: %v", err)
	}
	op := opFor(t, resp, kindAgent, secretToolAgentName)
	if op.Action != actionUnchanged || !op.ChangesDeclined {
		t.Fatalf("op = %+v, want unchanged with the change declined", op)
	}
	warnings := strings.Join(op.Warnings, " ")
	for _, want := range []string{"google_search_api_key", rbac.ScopeAgentBuilderUpdate} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings = %v, want %q named", op.Warnings, want)
		}
	}
	if got := storedGoogleSearchConfig(t, svc)["google_search_api_key"]; got != "key-alpha" {
		t.Errorf("stored key = %v, want it untouched", got)
	}
}

func TestHashedAgentViews_LeavesTheWritePayloadAndStoredResponseUntouched(t *testing.T) {
	t.Parallel()
	desired := entplatform.CreateAgentRequest{ToolsetConfigs: []entplatform.ToolsetConfigBinding{{
		ToolsetName:  "t",
		ConfigValues: map[string]any{"tool": map[string]any{"key": "key-alpha", "site": "a"}},
	}}}
	stored := entplatform.AgentResponse{ToolsetConfigs: []entplatform.ToolsetConfigBinding{{
		ToolsetName:  "t",
		ConfigValues: map[string]any{"tool": map[string]any{"key": entplatform.RedactedSentinel, "site": "a"}},
	}}}

	hashedDesired, hashedStored := hashedAgentViews(desired, stored)

	if got := desired.ToolsetConfigs[0].ConfigValues["tool"].(map[string]any)["key"]; got != "key-alpha" {
		t.Errorf("write payload key = %v, want the literal kept", got)
	}
	if got := stored.ToolsetConfigs[0].ConfigValues["tool"].(map[string]any)["key"]; got != entplatform.RedactedSentinel {
		t.Errorf("stored response key = %v, want the sentinel kept", got)
	}
	for name, cfgs := range map[string][]entplatform.ToolsetConfigBinding{"desired": hashedDesired.ToolsetConfigs, "stored": hashedStored.ToolsetConfigs} {
		if _, has := cfgs[0].ConfigValues["tool"].(map[string]any)["key"]; has {
			t.Errorf("hashed %s view still holds the secret path", name)
		}
	}
}
