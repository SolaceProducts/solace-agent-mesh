// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func decisionSpec(f func(map[string]any)) map[string]any {
	m := map[string]any{
		"provider":   platform.ProviderTypeSafe,
		"modelName":  "jev-1.13.0",
		"modelType":  platform.ModelTypeDecision,
		"apiBase":    "https://api.typesafe.ai",
		"authConfig": map[string]any{"type": "apikey", "api_key": "ts-secret"},
	}
	if f != nil {
		f(m)
	}
	return m
}

func existingModel(alias, provider, modelType string) wire.ModelResponse {
	return wire.ModelResponse{
		ID:        "id-" + alias,
		Alias:     alias,
		Provider:  sp(provider),
		ModelName: sp("existing"),
		ModelType: modelType,
	}
}

func TestPlanModels_ModelTypeRejectedAtPlan(t *testing.T) {
	cases := []struct {
		name    string
		spec    map[string]any
		actual  []wire.ModelResponse
		wantErr string
	}{
		{
			name:    "decision provider with modelType omitted",
			spec:    decisionSpec(func(m map[string]any) { delete(m, "modelType") }),
			wantErr: `provider "typesafe" serves decision models only`,
		},
		{
			name:    "decision type on a chat provider",
			spec:    fixtureSpec(func(m map[string]any) { m["modelType"] = platform.ModelTypeDecision }),
			wantErr: `a decision model must use provider "typesafe"`,
		},
		{
			name:    "unknown modelType",
			spec:    fixtureSpec(func(m map[string]any) { m["modelType"] = "embedding" }),
			wantErr: "modelType must be",
		},
		{
			name:    "existing chat model changed to decision",
			spec:    decisionSpec(nil),
			actual:  []wire.ModelResponse{existingModel("m", "anthropic", platform.ModelTypeChat)},
			wantErr: `modelType cannot change from "chat" to "decision"`,
		},
		{
			name:    "existing decision model moved to a chat provider",
			spec:    fixtureSpec(nil),
			actual:  []wire.ModelResponse{existingModel("m", platform.ProviderTypeSafe, platform.ModelTypeDecision)},
			wantErr: `modelType cannot change from "decision" to "chat"`,
		},
		{
			name: "existing decision model moved to another decision provider",
			spec: decisionSpec(func(m map[string]any) {
				m["provider"] = platform.ProviderOpenAI
				m["modelName"] = "gpt-6-luna"
				m["apiBase"] = "https://api.openai.com/v1"
			}),
			actual:  []wire.ModelResponse{existingModel("m", platform.ProviderTypeSafe, platform.ModelTypeDecision)},
			wantErr: `provider cannot change from "typesafe" to "openai"`,
		},
		{
			name:    "existing model reporting no type counts as chat",
			spec:    decisionSpec(nil),
			actual:  []wire.ModelResponse{existingModel("m", "anthropic", "")},
			wantErr: `modelType cannot change from "chat" to "decision"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := planModels(t.Context(), nil, []resolve.ResolvedResource{resolved("m", tc.spec)}, tc.actual)
			if err == nil {
				t.Fatalf("planModels: want an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), `model "m"`) {
				t.Fatalf("planModels error = %q, want it to name model \"m\" and contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestPlanModels_UnchangedModelTypeUpdates(t *testing.T) {
	cases := []struct {
		name   string
		spec   map[string]any
		actual wire.ModelResponse
	}{
		{
			name:   "decision model keeps its type",
			spec:   decisionSpec(nil),
			actual: existingModel("m", platform.ProviderTypeSafe, platform.ModelTypeDecision),
		},
		{
			name:   "chat model omitting modelType against a platform reporting none",
			spec:   fixtureSpec(nil),
			actual: existingModel("m", "anthropic", ""),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planModels(t.Context(), nil, []resolve.ResolvedResource{resolved("m", tc.spec)}, []wire.ModelResponse{tc.actual})
			if err != nil {
				t.Fatalf("planModels: %v", err)
			}
			if len(plan.Updates) != 1 {
				t.Fatalf("Updates = %+v, want the modelName change as one update", plan.Updates)
			}
		})
	}
}

func TestModelUpdateProjectionOmitsModelType(t *testing.T) {
	cases := map[string]platform.CreateModelConfigRequest{
		"chat": {
			Alias: "plain", Provider: "openai", ModelName: "gpt-4o",
		},
		"decision": {
			Alias: "jev", Provider: platform.ProviderTypeSafe, ModelName: "jev-1.13.0",
			ModelType:  platform.ModelTypeDecision,
			AuthConfig: map[string]any{"type": "apikey", "api_key": "ts-secret"},
		},
	}
	for name, create := range cases {
		t.Run(name, func(t *testing.T) {
			patch := generic.ToUpdate(create, platform.UpdateModelConfigRequest{})
			body, err := json.Marshal(patch)
			if err != nil {
				t.Fatalf("marshal patch: %v", err)
			}
			if bytes.Contains(body, []byte(`"modelType"`)) {
				t.Fatalf("PATCH body carries modelType: %s", body)
			}
		})
	}
}

func storedDecisionModel(authType string, authConfig map[string]any) wire.ModelResponse {
	m := existingModel("m", platform.ProviderTypeSafe, platform.ModelTypeDecision)
	m.AuthType = authType
	m.AuthConfig = authConfig
	return m
}

func TestPlanModels_DecisionAPIKeyRule(t *testing.T) {
	const wantErr = `requires authConfig type "apikey" with a non-empty api_key`
	withAuth := func(auth map[string]any) map[string]any {
		return decisionSpec(func(m map[string]any) {
			if auth == nil {
				delete(m, "authConfig")
				return
			}
			m["authConfig"] = auth
		})
	}
	sentinelStored := storedDecisionModel("apikey", map[string]any{"type": "apikey", "api_key": redactedSentinel})
	droppedStored := storedDecisionModel("apikey", map[string]any{})
	cases := []struct {
		name    string
		spec    map[string]any
		actual  []wire.ModelResponse
		wantErr bool
	}{
		{name: "create without authConfig", spec: withAuth(nil), wantErr: true},
		{name: "create with auth type none", spec: withAuth(map[string]any{"type": "none"}), wantErr: true},
		{name: "create with an empty api_key", spec: withAuth(map[string]any{"type": "apikey", "api_key": "  "}), wantErr: true},
		{name: "create echoing the redacted sentinel", spec: withAuth(map[string]any{"type": "apikey", "api_key": redactedSentinel}), wantErr: true},
		{name: "create with an api_key", spec: withAuth(map[string]any{"type": "apikey", "api_key": "ts-secret"})},
		{name: "chat create without authConfig", spec: fixtureSpec(func(m map[string]any) { delete(m, "authConfig") })},
		{name: "update without authConfig keeps the stored key", spec: withAuth(nil), actual: []wire.ModelResponse{droppedStored}},
		{name: "update with an empty api_key keeps the redacted stored key", spec: withAuth(map[string]any{"type": "apikey", "api_key": ""}), actual: []wire.ModelResponse{sentinelStored}},
		{name: "update omitting api_key keeps the dropped stored key", spec: withAuth(map[string]any{"type": "apikey"}), actual: []wire.ModelResponse{droppedStored}},
		{name: "update echoing the redacted sentinel keeps the stored key", spec: withAuth(map[string]any{"type": "apikey", "api_key": redactedSentinel}), actual: []wire.ModelResponse{sentinelStored}},
		{name: "update switching auth type to none", spec: withAuth(map[string]any{"type": "none"}), actual: []wire.ModelResponse{sentinelStored}, wantErr: true},
		{name: "update moving to apikey from another type without a key", spec: withAuth(map[string]any{"type": "apikey"}), actual: []wire.ModelResponse{storedDecisionModel("none", map[string]any{})}, wantErr: true},
		{name: "update moving to apikey from another type with a key", spec: withAuth(map[string]any{"type": "apikey", "api_key": "ts-new"}), actual: []wire.ModelResponse{storedDecisionModel("none", map[string]any{})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := planModels(t.Context(), nil, []resolve.ResolvedResource{resolved("m", tc.spec)}, tc.actual)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("planModels: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), wantErr) || !strings.Contains(err.Error(), `model "m"`) {
				t.Fatalf("planModels error = %v, want it to name model \"m\" and contain %q", err, wantErr)
			}
		})
	}
}
