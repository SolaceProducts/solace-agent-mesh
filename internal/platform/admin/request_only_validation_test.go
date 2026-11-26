// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestValidateAgentCreate(t *testing.T) {
	valid := CreateAgentRequest{Name: "an_agent", SystemPrompt: "You answer questions."}
	if err := ValidateAgentCreate(valid); err != nil {
		t.Fatalf("a valid request was refused: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*CreateAgentRequest)
		wantMsg string
	}{
		{"no instruction on a standard agent", func(r *CreateAgentRequest) { r.SystemPrompt = "" }, "system prompt is required"},
		{"unknown built-in skill", func(r *CreateAgentRequest) { r.SkillRefs = []string{"sam-no-such-skill"} }, `unknown built-in skill "sam-no-such-skill"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			tc.mutate(&req)
			err := ValidateAgentCreate(req)
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantMsg)
			}
		})
	}
}

func TestValidateWorkflowRequest(t *testing.T) {
	body := func(extra map[string]any) map[string]any {
		cfg := map[string]any{"workflow": map[string]any{"nodes": []any{}}}
		for k, v := range extra {
			cfg[k] = v
		}
		return cfg
	}
	withNodes := func(nodes ...any) map[string]any {
		return map[string]any{"workflow": map[string]any{"nodes": nodes}}
	}
	decision := map[string]any{"id": "dec", "type": "decision"}

	valid := map[string]map[string]any{
		"no nodes":                           body(nil),
		"a decision node with a model alias": body(map[string]any{"decision": map[string]any{"model_alias": "judge"}, "workflow": map[string]any{"nodes": []any{decision}}}),
		"a tool node bound to a connector":   withNodes(map[string]any{"id": "t", "type": "tool", "connector": "db"}),
		"distinct toolsets":                  body(map[string]any{"toolsets": []any{"a", "b"}}),
	}
	for name, cfg := range valid {
		t.Run("accepts "+name, func(t *testing.T) {
			if err := ValidateWorkflowRequest("wf", cfg); err != nil {
				t.Fatalf("a valid request was refused: %v", err)
			}
		})
	}

	tests := []struct {
		name    string
		cfg     map[string]any
		wantMsg string
	}{
		{"inline decision model", body(map[string]any{"decision": map[string]any{"model": "gpt-4"}}), "decision.model is for file-loaded configs only"},
		{"a decision node without a model alias", withNodes(decision), "no decision.model_alias"},
		{"duplicate toolset", body(map[string]any{"toolsets": []any{"a", "a"}}), `toolsets lists "a" more than once`},
		{"empty toolset name", body(map[string]any{"toolsets": []any{""}}), "non-empty toolset name"},
		{"toolsets that is not a list", body(map[string]any{"toolsets": "a"}), "toolsets must be a list"},
		{"connector_refs", map[string]any{"workflow": map[string]any{"connector_refs": []any{"x"}}}, "connector_refs is no longer supported"},
		{"a connector on a node that is not a tool node", withNodes(map[string]any{"id": "n", "type": "agent", "connector": "db"}), "connector is only valid on tool nodes"},
		{"a tool selector without a connector", withNodes(map[string]any{"id": "n", "type": "tool", "tool": "query"}), "requires 'connector'"},
		{"a workflow-level connector restating a node's connector", map[string]any{"workflow": map[string]any{
			"nodes":      []any{map[string]any{"id": "t", "type": "tool", "connector": "db"}},
			"connectors": []any{map[string]any{"name": "db"}},
		}}, "restates the platform connector"},
	}
	for _, tc := range tests {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			err := ValidateWorkflowRequest("wf", tc.cfg)
			if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("err = %#v, want a validation error containing %q", err, tc.wantMsg)
			}
		})
	}
}

func TestValidateAgentUpdate(t *testing.T) {
	if err := ValidateAgentUpdate(AgentTypeStandard, UpdateAgentRequest{SystemPrompt: "You answer questions."}); err != nil {
		t.Fatalf("a valid request was refused: %v", err)
	}
	err := ValidateAgentUpdate(AgentTypeStandard, UpdateAgentRequest{})
	if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "system prompt is required") {
		t.Errorf("err = %#v, want the instruction rule", err)
	}
	err = ValidateAgentUpdate(AgentTypeStandard, UpdateAgentRequest{SystemPrompt: "x", SkillRefs: []string{"sam-no-such-skill"}})
	if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "unknown built-in skill") {
		t.Errorf("err = %#v, want the skill-ref rule", err)
	}
}

func TestValidateConnectorValuesShape(t *testing.T) {
	for name, values := range map[string]any{"absent": nil, "an object": map[string]any{"hostname": "db"}, "a nil object": map[string]any(nil)} {
		if err := ValidateConnectorValuesShape(values); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	for name, values := range map[string]any{"a list": []any{}, "a string": "hostname", "a number": 5.0, "a boolean": true} {
		err := ValidateConnectorValuesShape(values)
		if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "values must be a JSON object") {
			t.Errorf("%s: err = %#v, want the object-shape rule", name, err)
		}
	}
}

func TestA2AAgentService_UpdateRejectsANonHTTPAgentURL(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewA2AAgentService(store.NewA2AAgentRepo(db), store.NewAgentDeploymentRepo(db), store.NewAgentRepo(db), nil, nil, nil, "test-ns", nil)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description: "a remote agent used to test updates", AgentURL: "https://example.com/agent", Name: "url-update",
		AgentCardLocation: "well_known", AgentAuthConfig: map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Update(t.Context(), created.ID, UpdateRemoteAgentRequest{AgentURL: "ftp://x"}, "test-user")

	if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "agentUrl must start with http:// or https://") {
		t.Fatalf("err = %#v, want a validation error naming the url rule", err)
	}
}

func TestValidateRemoteAgentUpdate(t *testing.T) {
	if err := ValidateRemoteAgentUpdate(UpdateRemoteAgentRequest{}); err != nil {
		t.Errorf("an update that leaves the url alone was refused: %v", err)
	}
	if err := ValidateRemoteAgentUpdate(UpdateRemoteAgentRequest{AgentURL: "https://example.com"}); err != nil {
		t.Errorf("an https url was refused: %v", err)
	}
	err := ValidateRemoteAgentUpdate(UpdateRemoteAgentRequest{AgentURL: "ftp://x"})
	if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "agentUrl must start with http:// or https://") {
		t.Errorf("err = %#v, want a validation error naming the url rule", err)
	}
}

func TestValidateAgentUpdate_ChecksTheInstructionOnlyForTheTypesThatNeedOne(t *testing.T) {
	if err := ValidateAgentUpdate(AgentTypeA2AProxy, UpdateAgentRequest{}); err != nil {
		t.Errorf("an a2a proxy agent was held to the standard agent's instruction rule: %v", err)
	}
}

func TestAgentService_UpdateRunsTheRequestOnlyChecks(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewAgentService(store.NewAgentRepo(db), nil, nil, nil, nil, nil, nil)
	created, err := svc.Create(t.Context(), CreateAgentRequest{
		Name: "update-checks", Description: "an agent used to test updates", SystemPrompt: "You answer questions.",
		InputModes: []string{"text"}, OutputModes: []string{"text"},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	update := func(mutate func(*UpdateAgentRequest)) error {
		req := UpdateAgentRequest{Name: "update-checks", Description: "an agent used to test updates", SystemPrompt: "You answer questions.", InputModes: []string{"text"}, OutputModes: []string{"text"}}
		mutate(&req)
		_, updateErr := svc.Update(t.Context(), created.ID, req, "tester")
		return updateErr
	}

	if updateErr := update(func(*UpdateAgentRequest) {}); updateErr != nil {
		t.Fatalf("a valid update was refused: %v", updateErr)
	}
	err = update(func(r *UpdateAgentRequest) { r.SystemPrompt = "" })
	if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "system prompt is required") {
		t.Errorf("err = %#v, want the instruction rule", err)
	}
	err = update(func(r *UpdateAgentRequest) { r.SkillRefs = []string{"sam-no-such-skill"} })
	if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "unknown built-in skill") {
		t.Errorf("err = %#v, want the skill-ref rule", err)
	}
}

func TestValidateRemoteAgentCardSkills(t *testing.T) {
	if err := ValidateRemoteAgentCardSkills("none", true); err != nil {
		t.Errorf("a card-less agent with skills was refused: %v", err)
	}
	if err := ValidateRemoteAgentCardSkills("well_known", false); err != nil {
		t.Errorf("an agent with a card to fetch was refused for lacking skills: %v", err)
	}
	err := ValidateRemoteAgentCardSkills("none", false)
	if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), "skills are required when agentCardLocation is 'none'") {
		t.Errorf("err = %#v, want the skills rule", err)
	}
}

func TestValidateGatewayEventRules(t *testing.T) {
	rule := func(name string, extra map[string]any) map[string]any {
		r := map[string]any{
			"name": name, "targetAgent": "an_agent", "promptTemplate": "{{text}}",
			"subscriptions": []any{map[string]any{"topic": "events/incoming"}},
		}
		for k, v := range extra {
			r[k] = v
		}
		return r
	}
	bag := func(rules ...any) map[string]any { return map[string]any{"event_rules": rules} }

	if err := ValidateGatewayEventRules(bag(rule("one", nil), rule("two", nil))); err != nil {
		t.Fatalf("valid rules were refused: %v", err)
	}
	tests := []struct {
		name    string
		values  map[string]any
		wantMsg string
	}{
		{"duplicate rule names", bag(rule("one", nil), rule("ONE", nil)), "duplicate rule name"},
		{"an agent and a workflow target", bag(rule("one", map[string]any{"targetWorkflowName": "wf"})), "mutually exclusive"},
		{"no subscriptions", bag(rule("one", map[string]any{"subscriptions": []any{}})), "subscriptions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGatewayEventRules(tc.values)
			if _, ok := err.(*ValidationError); !ok || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("err = %#v, want a validation error containing %q", err, tc.wantMsg)
			}
		})
	}
}

func TestValidateGatewayEventRules_NeverRejectsWhatTheFullValidationAccepts(t *testing.T) {
	t.Setenv("SOLACE_BROKER_URL", "")
	rule := func(name string, extra map[string]any) map[string]any {
		r := map[string]any{
			"name": name, "targetAgent": "an_agent", "promptTemplate": "{{text}}",
			"subscriptions": []any{map[string]any{"topic": "events/incoming"}},
		}
		for k, v := range extra {
			r[k] = v
		}
		return r
	}
	bags := map[string]map[string]any{
		"valid":                          {"event_rules": []any{rule("one", nil)}},
		"absent event_rules":             {},
		"empty event_rules":              {"event_rules": []any{}},
		"event_rules that is not a list": {"event_rules": "one"},
		"duplicate names":                {"event_rules": []any{rule("one", nil), rule("One", nil)}},
		"both targets":                   {"event_rules": []any{rule("one", map[string]any{"targetWorkflowName": "wf"})}},
		"no subscriptions":               {"event_rules": []any{rule("one", map[string]any{"subscriptions": []any{}})}},
		"a rule that is not an object":   {"event_rules": []any{"one"}},
	}
	for name, values := range bags {
		t.Run(name, func(t *testing.T) {
			if err := ValidateGatewayEventRules(values); err != nil {
				if full := validateEventMeshValues(values); full == nil {
					t.Errorf("the request-only check refused %v but the full validation accepted it: %v", values, err)
				}
			}
		})
	}
}
