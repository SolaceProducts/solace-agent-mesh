// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestClient_DiffSecrets(t *testing.T) {
	cases := []struct {
		name     string
		matches  map[string]bool
		wantAll  bool
		wantPath string
	}{
		{name: "all match", matches: map[string]bool{"slack_bot_token": true, "x": true}, wantAll: true},
		{name: "any mismatch", matches: map[string]bool{"slack_bot_token": false, "x": true}, wantAll: false},
		{name: "empty result", matches: map[string]bool{}, wantAll: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("PUT /api/v2/platform/connectors/cid-1/secretsDiff", func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var req map[string]any
				_ = json.Unmarshal(body, &req)
				values, ok := req["values"].(map[string]any)
				if !ok {
					t.Errorf("request body missing values: %s", body)
				}
				if values["slack_bot_token"] != "xoxb-rotated" {
					t.Errorf("candidate token = %v; want xoxb-rotated", values["slack_bot_token"])
				}
				resp := apiv2.SecretsDiffResult{Secrets: map[string]apiv2.SecretMatch{}}
				for k, v := range tc.matches {
					resp.Secrets[k] = apiv2.SecretMatch{Matches: v}
				}
				writeV2Single(w, http.StatusOK, resp)
			})
			c := newV2PlatformClient(t, mux)
			candidate := map[string]any{"slack_bot_token": "xoxb-rotated"}
			allMatch, err := c.DiffSecrets(t.Context(), "connectors", "cid-1", candidate)
			if err != nil {
				t.Fatalf("DiffSecrets: %v", err)
			}
			if allMatch != tc.wantAll {
				t.Errorf("allMatch = %v; want %v", allMatch, tc.wantAll)
			}
		})
	}
}

func TestClient_DiffSecrets_HTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/entrypoints/gid-1/secretsDiff", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusInternalServerError, "boom", nil)
	})
	c := newV2PlatformClient(t, mux)
	allMatch, err := c.DiffSecrets(t.Context(), "entrypoints", "gid-1", map[string]any{"k": "v"})
	if err == nil {
		t.Fatal("expected error on 500; got nil")
	}
	if allMatch {
		t.Errorf("allMatch = true on error; want false")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error must surface status code; got %v", err)
	}
}

func TestClient_DiffModelSecrets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/models/mid-1/secretsDiff", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		auth, ok := req["authConfig"].(map[string]any)
		if !ok {
			t.Errorf("request body missing authConfig: %s", body)
		}
		if auth["api_key"] != "rotated-key" {
			t.Errorf("candidate api_key = %v; want rotated-key", auth["api_key"])
		}
		writeV2Single(w, http.StatusOK, apiv2.ModelSecretsDiffResult{Secrets: map[string]apiv2.SecretMatch{"api_key": {Matches: false}}})
	})
	c := newV2PlatformClient(t, mux)
	allMatch, err := c.DiffModelSecrets(t.Context(), "mid-1", map[string]any{"api_key": "rotated-key"})
	if err != nil {
		t.Fatalf("DiffModelSecrets: %v", err)
	}
	if allMatch {
		t.Errorf("allMatch = true on matches=false response; want false")
	}
}

type stubSecretDiffer struct {
	matchByID map[string]bool
	errByID   map[string]error
	calls     []stubDiffCall
}

type stubDiffCall struct {
	kind   string
	id     string
	values any
}

func (s *stubSecretDiffer) DiffSecrets(_ context.Context, kind, id string, values any) (bool, error) {
	s.calls = append(s.calls, stubDiffCall{kind: kind, id: id, values: values})
	if err, ok := s.errByID[id]; ok {
		return false, err
	}
	if matches, ok := s.matchByID[id]; ok {
		return matches, nil
	}
	return true, nil
}

func (s *stubSecretDiffer) DiffModelSecrets(_ context.Context, id string, authConfig map[string]any) (bool, error) {
	s.calls = append(s.calls, stubDiffCall{kind: "models", id: id, values: authConfig})
	if err, ok := s.errByID[id]; ok {
		return false, err
	}
	if matches, ok := s.matchByID[id]; ok {
		return matches, nil
	}
	return true, nil
}

func TestPlanConnectors_VaultRotated_ReclassifiesToUpdate(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(nil))}
	actual := []wire.ConnectorResponse{{
		ID:               "id-1",
		Name:             "ops-slack",
		Description:      sp("fixture"),
		ConnectorType:    "messaging",
		ConnectorSubtype: "slack",
		Values: map[string]any{
			"slack_bot_token": "<REDACTED>",
			"slack_channel":   "#ops-alerts",
		},
	}}
	differ := &stubSecretDiffer{matchByID: map[string]bool{"id-1": false}}

	plan, err := planConnectors(t.Context(), differ, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Fatalf("Updates = %+v", plan.Updates)
	}
	if len(plan.Unchanged) != 0 {
		t.Errorf("Unchanged = %+v; expected empty after reclassification", plan.Unchanged)
	}
	if len(differ.calls) != 1 || differ.calls[0].kind != "connectors" || differ.calls[0].id != "id-1" {
		t.Errorf("expected one DiffSecrets call for connectors/id-1; got %+v", differ.calls)
	}
}

func TestPlanConnectors_VaultUnchanged_StaysUnchanged(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(nil))}
	actual := []wire.ConnectorResponse{{
		ID:               "id-1",
		Name:             "ops-slack",
		Description:      sp("fixture"),
		ConnectorType:    "messaging",
		ConnectorSubtype: "slack",
		Values: map[string]any{
			"slack_bot_token": "<REDACTED>",
			"slack_channel":   "#ops-alerts",
		},
	}}
	differ := &stubSecretDiffer{matchByID: map[string]bool{"id-1": true}}

	plan, err := planConnectors(t.Context(), differ, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("Unchanged = %+v", plan.Unchanged)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("Updates = %+v; expected empty when secrets match", plan.Updates)
	}
}

func redactedSlackConnector() []wire.ConnectorResponse {
	return []wire.ConnectorResponse{{
		ID:               "id-1",
		Name:             "ops-slack",
		Description:      sp("fixture"),
		ConnectorType:    "messaging",
		ConnectorSubtype: "slack",
		Values: map[string]any{
			"slack_bot_token": "<REDACTED>",
			"slack_channel":   "#ops-alerts",
		},
	}}
}

func TestPlanConnectors_DiffForbidden_FailsNamingField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/connectors/id-1/secretsDiff", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusForbidden, "Insufficient permissions", nil)
	})
	c := newV2PlatformClient(t, mux)
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(nil))}

	_, err := planConnectors(t.Context(), c, desired, redactedSlackConnector())
	if err == nil {
		t.Fatal("planConnectors succeeded; want an error when the secret compare is forbidden")
	}
	for _, want := range []string{`connector "ops-slack"`, "values.slack_bot_token", rbac.ScopeConnectorUpdate, "omit the field to keep the stored value"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestPlanConnectors_DiffError_FailsPlan(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(nil))}
	diffErr := errors.New("network unreachable")
	differ := &stubSecretDiffer{errByID: map[string]error{"id-1": diffErr}}

	_, err := planConnectors(t.Context(), differ, desired, redactedSlackConnector())
	if !errors.Is(err, diffErr) {
		t.Fatalf("planConnectors error = %v; want it to wrap %v", err, diffErr)
	}
}

func TestPlanConnectors_SecretOmitted_SkipsDiff(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(func(m map[string]any) {
		m["values"] = map[string]any{"slack_channel": "#ops-alerts"}
	}))}
	differ := &stubSecretDiffer{errByID: map[string]error{"id-1": &HTTPError{StatusCode: http.StatusForbidden}}}

	plan, err := planConnectors(t.Context(), differ, desired, redactedSlackConnector())
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
		t.Errorf("unchanged=%d updates=%d; want the connector unchanged", len(plan.Unchanged), len(plan.Updates))
	}
	if len(differ.calls) != 0 {
		t.Errorf("expected no diff call when the manifest omits the secret; got %+v", differ.calls)
	}
}

func TestPlanConnectors_NoSecrets_SkipsDifferCall(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(func(m map[string]any) {
		m["values"] = map[string]any{"slack_channel": "#ops-alerts"}
	}))}
	actual := []wire.ConnectorResponse{{
		ID:               "id-1",
		Name:             "ops-slack",
		Description:      sp("fixture"),
		ConnectorType:    "messaging",
		ConnectorSubtype: "slack",
		Values:           map[string]any{"slack_channel": "#ops-alerts"},
	}}
	differ := &stubSecretDiffer{}

	plan, err := planConnectors(t.Context(), differ, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v", plan.Unchanged)
	}
	if len(differ.calls) != 0 {
		t.Errorf("expected zero diff calls on a non-secret-bearing resource; got %+v", differ.calls)
	}
}

func TestPlanGateways_VaultRotated_ReclassifiesToUpdate(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(func(m map[string]any) {
		m["values"] = map[string]any{
			"fastapiHost":   "0.0.0.0",
			"fastapiPort":   8080,
			"sessionSecret": "rotated-secret-value",
		}
	}))}
	actual := []wire.EntrypointResponse{{
		ID:             "id-1",
		Name:           "webui",
		Description:    "fixture gateway with a long-enough description",
		EntrypointType: "webui",
		PublicURL:      sp("https://chat.example.com"),
		Values: map[string]any{
			"fastapiHost":   "0.0.0.0",
			"fastapiPort":   float64(8080),
			"sessionSecret": "<REDACTED>",
		},
	}}
	differ := &stubSecretDiffer{matchByID: map[string]bool{"id-1": false}}

	plan, err := planGateways(t.Context(), differ, desired, actual, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Fatalf("Updates = %+v", plan.Updates)
	}
	if !plan.Updates[0].Update.Values.Set {
		t.Errorf("reclassified Update does not send the rotated values: %+v", plan.Updates[0].Update)
	}
	if len(plan.Unchanged) != 0 {
		t.Errorf("Unchanged = %+v; expected empty after reclassification", plan.Unchanged)
	}
	if len(differ.calls) != 1 || differ.calls[0].kind != "entrypoints" {
		t.Errorf("expected one DiffSecrets call for entrypoints; got %+v", differ.calls)
	}
}

func TestPlanModels_VaultRotated_ReclassifiesToUpdate(t *testing.T) {
	desired := []resolve.ResolvedResource{resolved("planning", fixtureSpec(func(m map[string]any) {
		m["authConfig"] = map[string]any{
			"type":   "apikey",
			"apiKey": "rotated-key",
		}
	}))}
	apiBase := "https://api.anthropic.com"
	desc := "fixture"
	actual := []wire.ModelResponse{{
		ID:        "id-1",
		Alias:     "planning",
		Provider:  sp("anthropic"),
		ModelName: sp("claude-opus-4-7"),
		APIBase:   &apiBase,
		AuthType:  "apikey",
		AuthConfig: map[string]any{
			"type":   "apikey",
			"apiKey": "<REDACTED>",
		},
		ModelParams: map[string]any{"temperature": 0.2},
		Description: &desc,
	}}
	differ := &stubSecretDiffer{matchByID: map[string]bool{"id-1": false}}

	plan, err := planModels(t.Context(), differ, desired, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Fatalf("Updates = %+v", plan.Updates)
	}
	if plan.Updates[0].Actual == nil || plan.Updates[0].Actual.ID != "id-1" {
		t.Errorf("reclassified Update missing Actual reference: %+v", plan.Updates[0])
	}
	if len(plan.Unchanged) != 0 {
		t.Errorf("Unchanged = %+v; expected empty after reclassification", plan.Unchanged)
	}
	if len(differ.calls) != 1 || differ.calls[0].kind != "models" {
		t.Errorf("expected one DiffModelSecrets call for models; got %+v", differ.calls)
	}
	patch, err := json.Marshal(plan.Updates[0].Patch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "rotated-key") {
		t.Errorf("PATCH body = %s; want it to carry the rotated key", patch)
	}
}

func TestPlanAgents_SkillConfigSecrets_NoPhantomUpdate(t *testing.T) {
	desired := []resolve.ResolvedResource{{
		Name:        "x",
		Description: "d",
		SourceFile:  "agents/x.yaml",
		Spec: map[string]any{
			"systemPrompt": "p",
			"skillConfigs": []any{
				map[string]any{
					"skillName": "render_skill",
					"configValues": map[string]any{
						"render": map[string]any{"api_key": "sk-plain", "model_id": "gpt-5"},
					},
				},
			},
		},
	}}
	actual := []wire.AgentResponse{{
		ID:           "agent-1",
		Name:         "x",
		Description:  "d",
		SystemPrompt: "p",
		SkillConfigs: []apiv2.SkillConfig{{
			SkillID:   "skill-uuid-1",
			SkillName: sp("render_skill"),
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": "<REDACTED>", "model_id": "gpt-5"},
			},
		}},
	}}

	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("expected no updates for redaction-equivalent skill configs, got %+v", plan.Updates)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("expected one unchanged entry, got %+v", plan.Unchanged)
	}

	desired[0].Spec["skillConfigs"] = []any{
		map[string]any{
			"skillName": "render_skill",
			"configValues": map[string]any{
				"render": map[string]any{"api_key": "sk-plain", "model_id": "gpt-6"},
			},
		},
	}
	plan, err = planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Errorf("expected one update for non-secret skill config drift, got %+v", plan.Updates)
	}
}

func TestPlanAgents_OverlayRotated_ReclassifiesToUpdate(t *testing.T) {
	desired := []resolve.ResolvedResource{
		agentResolvedWithToolsetConfig(map[string]any{"region": "us-east-1", "password": "rotated-plain"}),
	}
	a := agentResponseWithToolsetConfig(map[string]any{"region": "us-east-1", "password": "<REDACTED>"})
	a.ID = "agent-1"
	differ := &stubSecretDiffer{matchByID: map[string]bool{"agent-1": false}}

	plan, err := planAgents(t.Context(), differ, desired, []wire.AgentResponse{a}, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "agent-1" {
		t.Fatalf("Updates = %+v", plan.Updates)
	}
	if plan.Updates[0].Actual == nil || plan.Updates[0].Actual.ID != "agent-1" {
		t.Errorf("reclassified Update missing Actual reference: %+v", plan.Updates[0])
	}
	if got := plan.Updates[0].Update.ToolsetConfigs; len(got) != 1 || got[0].ConfigValues["password"] != "rotated-plain" {
		t.Errorf("promoted Update must carry the plaintext secret on the wire payload, got %+v", got)
	}
	if len(plan.Unchanged) != 0 {
		t.Errorf("Unchanged = %+v; expected empty after reclassification", plan.Unchanged)
	}
	if len(differ.calls) != 1 || differ.calls[0].kind != "agents" || differ.calls[0].id != "agent-1" {
		t.Fatalf("expected one DiffSecrets call for agents/agent-1; got %+v", differ.calls)
	}
	cand, ok := differ.calls[0].values.(apiv2.AgentSecretsDiffForm)
	if !ok {
		t.Fatalf("DiffSecrets values = %T; want apiv2.AgentSecretsDiffForm", differ.calls[0].values)
	}
	if len(cand.ToolsetConfigs) != 1 || cand.ToolsetConfigs[0].ConfigValues["password"] != "rotated-plain" {
		t.Errorf("candidate must carry the desired plaintext overlays, got %+v", cand.ToolsetConfigs)
	}
}

func TestPlanAgents_OverlayUnchanged_StaysUnchanged(t *testing.T) {
	desired := []resolve.ResolvedResource{
		agentResolvedWithToolsetConfig(map[string]any{"region": "us-east-1", "password": "plain"}),
	}
	a := agentResponseWithToolsetConfig(map[string]any{"region": "us-east-1", "password": "<REDACTED>"})
	a.ID = "agent-1"
	differ := &stubSecretDiffer{matchByID: map[string]bool{"agent-1": true}}

	plan, err := planAgents(t.Context(), differ, desired, []wire.AgentResponse{a}, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("Unchanged = %+v", plan.Unchanged)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("Updates = %+v; expected empty when overlay secrets match", plan.Updates)
	}
}

func TestPlanAgents_NoRedactedOverlays_SkipsDifferCall(t *testing.T) {
	desired := []resolve.ResolvedResource{
		agentResolvedWithToolsetConfig(map[string]any{"region": "us-east-1"}),
	}
	a := agentResponseWithToolsetConfig(map[string]any{"region": "us-east-1"})
	a.ID = "agent-1"
	differ := &stubSecretDiffer{}

	plan, err := planAgents(t.Context(), differ, desired, []wire.AgentResponse{a}, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("Unchanged = %+v", plan.Unchanged)
	}
	if len(differ.calls) != 0 {
		t.Errorf("expected no DiffSecrets calls without redacted overlays; got %+v", differ.calls)
	}
}
