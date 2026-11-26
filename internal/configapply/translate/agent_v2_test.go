// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestReverseAgentV2_ReadsBackTheSameYAMLAsV1(t *testing.T) {
	skillID := "track_order"
	skillName := "order-lookup"
	toolsetValues := func() map[string]any {
		return map[string]any{"web_request": map[string]any{"timeout_seconds": 30.0, "proxy": nil}}
	}
	skillValues := func() map[string]map[string]any {
		return map[string]map[string]any{"lookup_order": {"api_token": "<REDACTED>", "region": nil}}
	}
	cases := map[string]struct {
		v1 entplatform.AgentResponse
		v2 wire.AgentResponse
	}{
		"every authored field set": {
			v1: entplatform.AgentResponse{
				Name: "orders", Type: "standard", Description: "Answers order questions.", SystemPrompt: "Help.",
				Toolsets:                 []string{"builtin_web_request_tools"},
				ToolsetExcludedTools:     map[string][]string{"builtin_web_request_tools": {"web_search_google"}},
				ToolsetConfigs:           []entplatform.ToolsetConfigBinding{{ToolsetName: "builtin_web_request_tools", ConfigValues: toolsetValues()}},
				SkillConfigs:             []entplatform.SkillConfigBinding{{SkillID: "0190f5e6", SkillName: skillName, ConfigValues: skillValues()}},
				Skills:                   []entplatform.SkillResponse{{ID: skillID, Name: "Track order", Description: "Finds an order.", Tags: []string{"orders"}}},
				SkillRefs:                []string{"sam-docs"},
				SkillIDs:                 []string{"0190f5e6"},
				Connectors:               []string{"orders-db"},
				InputModes:               []string{"text"},
				OutputModes:              []string{"text"},
				ModelProvider:            []string{"planning"},
				AdditionalConfigurations: json.RawMessage(`{"maxLlmCallsPerTask":20}`),
			},
			v2: wire.AgentResponse{
				Name: "orders", AgentType: "standard", Type: "agent", Description: "Answers order questions.", SystemPrompt: "Help.",
				Toolsets:             []string{"builtin_web_request_tools"},
				ToolsetExcludedTools: map[string][]string{"builtin_web_request_tools": {"web_search_google"}},
				ToolsetConfigs:       []apiv2.ToolsetConfig{{ToolsetName: "builtin_web_request_tools", ConfigValues: toolsetValues()}},
				SkillConfigs:         []apiv2.SkillConfig{{SkillID: "0190f5e6", SkillName: &skillName, ConfigValues: skillValues()}},
				Skills: []apiv2.AgentSkill{{
					ID: &skillID, Name: "Track order", Description: "Finds an order.", Tags: []string{"orders"},
					Examples: []string{}, InputModes: []string{}, OutputModes: []string{},
				}},
				SkillRefs:                []string{"sam-docs"},
				SkillIDs:                 []string{"0190f5e6"},
				ConnectorIDs:             []string{"orders-db"},
				InputModes:               []string{"text"},
				OutputModes:              []string{"text"},
				ModelProvider:            []string{"planning"},
				AdditionalConfigurations: map[string]any{"maxLlmCallsPerTask": 20.0},
			},
		},
		"only the required fields set": {
			v1: entplatform.AgentResponse{Name: "minimal", Type: "standard", Description: "A minimal agent.", SystemPrompt: "Help."},
			v2: wire.AgentResponse{
				Name: "minimal", AgentType: "standard", Type: "agent", Description: "A minimal agent.", SystemPrompt: "Help.",
				Toolsets: []string{}, ToolsetConfigs: []apiv2.ToolsetConfig{}, SkillConfigs: []apiv2.SkillConfig{},
				Skills: []apiv2.AgentSkill{}, SkillRefs: []string{}, SkillIDs: []string{},
				InputModes: []string{}, OutputModes: []string{}, ConnectorIDs: []string{}, ModelProvider: []string{},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fromV1, err := ReverseAgent(tc.v1, AgentHooks{})
			if err != nil {
				t.Fatalf("ReverseAgent: %v", err)
			}
			fromV2, err := ReverseAgentV2(roundTrip(t, tc.v2), AgentV2Hooks{})
			if err != nil {
				t.Fatalf("ReverseAgentV2: %v", err)
			}
			if diff := cmp.Diff(withoutV1Empties(t, asYAMLValues(t, fromV1)), asYAMLValues(t, fromV2)); diff != "" {
				t.Errorf("the agent reads back as different YAML from v2 (-v1 +v2):\n%s", diff)
			}
		})
	}
}

func TestReverseAgentV2_KeepsWhatAuthorsPutInOpenObjects(t *testing.T) {
	name := "order-lookup"
	resp := roundTrip(t, wire.AgentResponse{
		Name: "orders", AgentType: "standard",
		ToolsetConfigs: []apiv2.ToolsetConfig{{ToolsetName: "lookup", ConfigValues: map[string]any{"lookup": map[string]any{"proxy": nil}}}},
		SkillConfigs:   []apiv2.SkillConfig{{SkillID: "0190f5e6", SkillName: &name, ConfigValues: map[string]map[string]any{"lookup_order": {"api_token": "<REDACTED>"}}}},
	})
	spec, err := ReverseAgentV2(resp, AgentV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseAgentV2: %v", err)
	}
	got := asYAMLValues(t, spec).(map[string]any)
	want := map[string]any{
		"toolsetConfigs": []any{map[string]any{"toolsetName": "lookup", "configValues": map[string]any{"lookup": map[string]any{"proxy": nil}}}},
		"skillConfigs":   []any{map[string]any{"skillId": "0190f5e6", "skillName": name, "configValues": map[string]any{"lookup_order": map[string]any{"api_token": "<REDACTED>"}}}},
	}
	for _, key := range []string{"toolsetConfigs", "skillConfigs"} {
		if diff := cmp.Diff(want[key], got[key]); diff != "" {
			t.Errorf("%s lost an author's value or a redacted secret on the way to YAML (-want +got):\n%s", key, diff)
		}
	}
}

func withoutV1Empties(t *testing.T, spec any) any {
	t.Helper()
	m, ok := spec.(map[string]any)
	if !ok {
		t.Fatalf("v1 spec is %T, want a map", spec)
	}
	for key := range m {
		deleteIfNull(m, key)
	}
	skills, _ := m["skills"].([]any)
	for _, s := range skills {
		if skill, ok := s.(map[string]any); ok {
			deleteIfNull(skill, "required")
			deleteIfNull(skill, "optional")
		}
	}
	return m
}

func deleteIfNull(m map[string]any, key string) {
	if value, ok := m[key]; ok && value == nil {
		delete(m, key)
	}
}

func asYAMLValues(t *testing.T, spec map[string]any) any {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func roundTrip(t *testing.T, resp wire.AgentResponse) wire.AgentResponse {
	t.Helper()
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var out wire.AgentResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestForwardAgentV2_SendsTheV2FieldNames(t *testing.T) {
	req, err := ForwardAgentV2(map[string]any{
		"name": "orders", "description": "Answers order questions.", "systemPrompt": "Help.",
		"type": "standard", "connectors": []any{"orders-db"},
	}, AgentV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardAgentV2: %v", err)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if got := body["agentType"]; got != "standard" {
		t.Errorf("agentType = %v, want the YAML type", got)
	}
	if got, want := body["connectorIds"], []any{"orders-db"}; !cmp.Equal(got, want) {
		t.Errorf("connectorIds = %v, want the YAML connectors %v", got, want)
	}
	for _, v1Name := range []string{"type", "connectors"} {
		if _, sent := body[v1Name]; sent {
			t.Errorf("the request sends the v1 field %q, which v2 rejects", v1Name)
		}
	}
}
