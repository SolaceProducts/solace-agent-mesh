// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"reflect"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestBuildProbeConfig_OAuth2Remap(t *testing.T) {
	t.Run("fresh_config_remaps_oauth_keys", func(t *testing.T) {
		env := setupModelConfigMux(t)

		req := &ModelConfigTestRequest{
			Provider:  "openai",
			ModelName: "gpt-4o",
			APIBase:   "https://llm.example.com/v1",
			AuthConfig: map[string]any{
				"type":          "oauth2",
				"client_id":     "cid",
				"client_secret": "csecret",
				"token_url":     "https://idp/token",
				"scope":         "openai.chat",
			},
		}
		probe, err := env.handler.buildProbeConfig(t.Context(), req, req.AuthConfig)
		if err != nil {
			t.Fatalf("buildProbeConfig: %v", err)
		}
		raw := probe.raw
		for _, k := range []string{"client_id", "client_secret", "token_url", "scope"} {
			if _, present := raw[k]; present {
				t.Errorf("raw[%q] present; oauth2 keys must be remapped, not written verbatim (keys=%v)", k, raw)
			}
		}
		want := map[string]string{
			"oauth_client_id":     "cid",
			"oauth_client_secret": "csecret",
			"oauth_token_url":     "https://idp/token",
			"oauth_scope":         "openai.chat",
		}
		for k, v := range want {
			if got, present := raw[k]; !present || got != v {
				t.Errorf("raw[%q] = %v, want %q", k, got, v)
			}
		}
	})

	t.Run("edit_mode_overlays_new_secret_via_remap", func(t *testing.T) {
		env := setupModelConfigMux(t)

		created := createModelFromJSON(t, env, `{"alias":"oauth-edit","provider":"openai","modelName":"gpt-4o","apiBase":"https://llm.example.com/v1","authConfig":{"type":"oauth2","client_id":"cid","client_secret":"stored-secret","token_url":"https://idp/token"}}`)

		incoming := map[string]any{
			"type":          "oauth2",
			"client_id":     "cid",
			"client_secret": "new-secret",
			"token_url":     "https://idp/token",
		}
		req := &ModelConfigTestRequest{
			ModelID:    created.ID,
			AuthConfig: incoming,
		}
		probe, err := env.handler.buildProbeConfig(t.Context(), req, incoming)
		if err != nil {
			t.Fatalf("buildProbeConfig: %v", err)
		}
		raw := probe.raw
		if probe.provider != "openai" {
			t.Errorf("probe provider = %q, want the stored provider openai for an edit-mode test", probe.provider)
		}
		if got := raw["oauth_client_secret"]; got != "new-secret" {
			t.Errorf("oauth_client_secret = %v, want %q (new secret must overlay stored via remap)", got, "new-secret")
		}
		if _, present := raw["client_secret"]; present {
			t.Errorf("client_secret present alongside oauth_client_secret; samapp reads only oauth_* (raw=%v)", raw)
		}
	})
}

func TestBuildProbeConfig_TrimsModelName(t *testing.T) {
	t.Run("padded_new_config_name_is_probed_trimmed", func(t *testing.T) {
		env := setupModelConfigMux(t)

		req := &ModelConfigTestRequest{
			Provider:   "openai",
			ModelName:  " gpt-4o ",
			APIBase:    "https://llm.example.com/v1",
			AuthConfig: map[string]any{"type": "apikey", "api_key": "sk"},
		}
		probe, err := env.handler.buildProbeConfig(t.Context(), req, req.AuthConfig)
		if err != nil {
			t.Fatalf("buildProbeConfig: %v", err)
		}
		if want := platpkg.ResolveLiteLLMModelName("openai", "", "gpt-4o"); probe.raw["model"] != want {
			t.Errorf("raw[model] = %q, want %q (padding must be trimmed before probing)", probe.raw["model"], want)
		}
	})

	t.Run("whitespace_override_keeps_stored_name", func(t *testing.T) {
		env := setupModelConfigMux(t)

		created := createModelFromJSON(t, env, `{"alias":"whitespace-override","provider":"openai","modelName":"gpt-4o","apiBase":"https://llm.example.com/v1","authConfig":{"type":"apikey","api_key":"sk-stored"}}`)

		req := &ModelConfigTestRequest{ModelID: created.ID, Provider: "openai", ModelName: "   "}
		probe, err := env.handler.buildProbeConfig(t.Context(), req, nil)
		if err != nil {
			t.Fatalf("buildProbeConfig: %v", err)
		}
		if want := platpkg.ResolveLiteLLMModelName("openai", "", "gpt-4o"); probe.raw["model"] != want {
			t.Errorf("raw[model] = %q, want the stored %q when the override is only whitespace", probe.raw["model"], want)
		}
	})
}

func createModelFromJSON(t *testing.T, env *testModelConfigEnv, body string) *ModelConfigResponse {
	t.Helper()
	var req CreateModelConfigRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode model: %v", err)
	}
	created, err := env.svc.Create(t.Context(), &req, "tester")
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	return created
}

func TestBuildProbeConfig_StoredParamsEntryHoldsWhatIsBeingTested(t *testing.T) {
	t.Run("a new model is tested with the form's params", func(t *testing.T) {
		env := setupModelConfigMux(t)

		req := &ModelConfigTestRequest{
			Provider:    "openai",
			ModelName:   "gpt-4o",
			AuthConfig:  map[string]any{"type": "apikey", "api_key": "sk-new"},
			ModelParams: map[string]any{"temperature": 0.2},
		}
		probe, err := env.handler.buildProbeConfig(t.Context(), req, req.AuthConfig)
		if err != nil {
			t.Fatalf("buildProbeConfig: %v", err)
		}
		want := map[string]any{"temperature": 0.2}
		if got := probe.raw[samapp.ModelParamsKey]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", samapp.ModelParamsKey, got, want)
		}
	})

	storedModel := `{"alias":"stored-params","provider":"openai","modelName":"gpt-4o","apiBase":"https://llm.example.com/v1","authConfig":{"type":"apikey","api_key":"sk-stored"},"modelParams":{"temperature":0.2,"top_p":0.9,"extra_headers":{"X-Team":"a"}}}`
	cases := map[string]struct {
		formParams      map[string]any
		wantParams      map[string]any
		wantFlattenedTo map[string]any
	}{
		"a stored model is tested with the form's params in place of its own": {
			formParams:      map[string]any{"top_p": 0.5, "seed": float64(7)},
			wantParams:      map[string]any{"top_p": 0.5, "seed": float64(7)},
			wantFlattenedTo: map[string]any{"temperature": nil, "extra_headers": nil, "top_p": 0.5},
		},
		"a stored model is tested with its own params when the form sends none": {
			formParams:      nil,
			wantParams:      map[string]any{"temperature": 0.2, "top_p": 0.9, "extra_headers": map[string]any{"X-Team": "a"}},
			wantFlattenedTo: map[string]any{"temperature": 0.2, "extra_headers": map[string]any{"X-Team": "a"}, "top_p": 0.9},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := setupModelConfigMux(t)
			created := createModelFromJSON(t, env, storedModel)

			req := &ModelConfigTestRequest{ModelID: created.ID, ModelParams: c.formParams}
			probe, err := env.handler.buildProbeConfig(t.Context(), req, nil)
			if err != nil {
				t.Fatalf("buildProbeConfig: %v", err)
			}
			if got := probe.raw[samapp.ModelParamsKey]; !reflect.DeepEqual(got, c.wantParams) {
				t.Errorf("%s = %v, want %v", samapp.ModelParamsKey, got, c.wantParams)
			}
			for key, want := range c.wantFlattenedTo {
				if got := probe.raw[key]; !reflect.DeepEqual(got, want) {
					t.Errorf("raw %s = %v, want %v", key, got, want)
				}
			}
		})
	}
}

func TestToRawLiteLLMConfig_CarriesTheStoredParamsAsTheirOwnEntry(t *testing.T) {
	raw := toRawLiteLLMConfig(&store.ModelConfiguration{
		ID:              "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42",
		Provider:        "openai",
		ModelName:       "gpt-4o",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`,
		ModelParams:     `{"temperature":0.7,"top_p":0.9}`,
	})

	want := map[string]any{"temperature": 0.7, "top_p": 0.9}
	if got := raw[samapp.ModelParamsKey]; !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want the stored params %v", samapp.ModelParamsKey, got, want)
	}
}
