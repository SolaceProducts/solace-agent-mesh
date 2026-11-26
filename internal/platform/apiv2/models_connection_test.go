// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	modelsJevAnswer  = `{"model":"jev-1.13.0","answers":{"connection_test":{"type":"noul","noul":0.98}}}`
	modelsLunaAnswer = `{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"connection_test","probability":0.98}]}`
	modelsLunaName   = "gpt-6-luna-2026-09"
)

type modelsSeenRequest struct {
	path   string
	header http.Header
	body   map[string]any
}

type modelsRecordingProvider struct {
	url string

	mu   sync.Mutex
	seen []modelsSeenRequest
}

func newModelsRecordingProvider(t *testing.T, respond http.HandlerFunc) *modelsRecordingProvider {
	t.Helper()
	p := &modelsRecordingProvider{}
	p.url = fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		p.mu.Lock()
		p.seen = append(p.seen, modelsSeenRequest{path: r.URL.Path, header: r.Header.Clone(), body: body})
		p.mu.Unlock()
		respond(w, r)
	})
	return p
}

func (p *modelsRecordingProvider) requests() []modelsSeenRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

func modelsDecisionAPI(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/systemone":
			answerWith(modelsJevAnswer)(w, r)
		case "/v1/decisions":
			answerWith(modelsLunaAnswer)(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

func modelsCreateLuna(api *entrypointAPI, alias, apiBase string) Model {
	api.t.Helper()
	return expectCreated[Model](api.t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": alias, "provider": "openai", "modelName": modelsLunaName, "modelType": "decision",
		"apiBase": apiBase, "authConfig": keyAuth("sk-decisions"),
	}), "models")
}

func modelsProbe(api *entrypointAPI, body any) ModelTestConnectionResult {
	api.t.Helper()
	return probeResult(api.t, api.call(http.MethodPut, "/models/testConnection", body))
}

func TestModels_TestConnectionRefusesAHeaderCarryingCRLF(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(chatCompletionBody))

	expectDetailSaying(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": provider.url, "authConfig": keyAuth("sk"),
		"modelParams": map[string]any{"extra_headers": map[string]any{"X-Bad": "v\r\nX-Injected: pwn"}},
	}), "modelParams", "CR/LF")
	if got := provider.requests(); len(got) != 0 {
		t.Errorf("the provider was sent %d requests carrying the injected header", len(got))
	}
}

func TestModels_TestConnectionOfASavedModelSendsTheEditedHeaders(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(chatCompletionBody))
	saved := createModelAt(api, "probe-hdr-edit", provider.url, keyAuth("sk-stored"))

	result := modelsProbe(api, map[string]any{
		"modelId":     saved.ID,
		"modelParams": map[string]any{"extra_headers": map[string]any{"X-Edit": "edited-value"}},
	})

	if !result.Success {
		t.Fatalf("probe = %+v, want it to pass", result)
	}
	seen := provider.requests()
	if len(seen) != 1 || seen[0].header.Get("X-Edit") != "edited-value" {
		t.Errorf("the provider saw %d requests, want one carrying the edited X-Edit header", len(seen))
	}
}

func TestModels_TestConnectionOfASavedModelSendsTheParamsSent(t *testing.T) {
	tests := []struct {
		name       string
		params     map[string]any
		wantTopK   any
		wantHeader string
	}{
		{name: "sent params replace the saved ones", params: map[string]any{"cache_strategy": "5m"}},
		{name: "no params probes the saved ones", wantTopK: float64(40), wantHeader: "saved-value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newModelAPI(t)
			provider := newModelsRecordingProvider(t, answerWith(chatCompletionBody))
			saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
				"alias": "probe-params", "provider": "openai", "modelName": "gpt-4o",
				"apiBase": provider.url, "authConfig": keyAuth("sk-stored"),
				"modelParams": map[string]any{"top_k": 40, "extra_headers": map[string]any{"X-Saved": "saved-value"}},
			}), "models")
			body := map[string]any{"modelId": saved.ID}
			if tc.params != nil {
				body["modelParams"] = tc.params
			}

			if result := modelsProbe(api, body); !result.Success {
				t.Fatalf("probe = %+v, want it to pass", result)
			}

			seen := provider.requests()
			if len(seen) != 1 {
				t.Fatalf("the provider saw %d requests, want 1", len(seen))
			}
			if got := seen[0].body["top_k"]; got != tc.wantTopK {
				t.Errorf("top_k sent = %v, want %v", got, tc.wantTopK)
			}
			if got := seen[0].header.Get("X-Saved"); got != tc.wantHeader {
				t.Errorf("X-Saved sent = %q, want %q", got, tc.wantHeader)
			}
		})
	}
}

func TestModels_TestConnectionAfterAProviderSwitchSendsNoSavedKey(t *testing.T) {
	api, _ := newModelAPI(t)
	newHost := newModelsRecordingProvider(t, answerWith(chatCompletionBody))
	saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "switch-key", "provider": "anthropic", "modelName": "claude-3-5-sonnet",
		"apiBase": "https://anthropic.example.com", "authConfig": keyAuth("sk-anthropic-stored"),
	}), "models")

	modelsProbe(api, map[string]any{
		"modelId": saved.ID, "provider": "openai", "modelName": "gpt-4o", "apiBase": newHost.url,
		"authConfig": map[string]any{"type": "apikey"},
	})

	seen := newHost.requests()
	if len(seen) != 1 {
		t.Fatalf("the new provider saw %d requests, want 1", len(seen))
	}
	if got := seen[0].header.Get("Authorization"); strings.Contains(got, "sk-anthropic-stored") {
		t.Errorf("the new provider was sent Authorization %q, the key saved for the old provider", got)
	}
}

func TestModels_SaveAfterAProviderSwitchKeepsNoAuthFieldOfTheOldProvider(t *testing.T) {
	const azureScope = "https://cognitiveservices.azure.com/.default"
	newAuth := map[string]any{
		"type": "oauth2", "client_id": "new-client", "client_secret": "new-secret", "token_url": "https://idp.example.com/token",
	}
	tests := []struct {
		name     string
		provider string
		want     map[string]any
	}{
		{name: "another provider", provider: "custom", want: map[string]any{
			"type": "oauth2", "client_id": "new-client", "client_secret": redacted, "token_url": "https://idp.example.com/token",
		}},
		{name: "the same provider", provider: "azure_openai", want: map[string]any{
			"type": "oauth2", "client_id": "new-client", "client_secret": redacted, "token_url": "https://idp.example.com/token",
			"scope": azureScope,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newModelAPI(t)
			saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
				"alias": "switch-oauth", "provider": "azure_openai", "modelName": "gpt-4o",
				"apiBase": "https://example.openai.azure.com", "authConfig": map[string]any{
					"type": "oauth2", "client_id": "azure-client", "client_secret": "azure-secret",
					"token_url": "https://login.example.com/token", "scope": azureScope,
				},
			}), "models")

			expectStatus(t, api.call(http.MethodPatch, "/models/"+saved.ID, map[string]any{
				"provider": tc.provider, "modelName": "gpt-4o", "apiBase": "https://llm.example.com/v1", "authConfig": newAuth,
			}), http.StatusOK)

			rec := api.call(http.MethodGet, "/models/"+saved.ID, nil)
			expectStatus(t, rec, http.StatusOK)
			if diff := cmp.Diff(tc.want, decodeData[Model](t, rec).AuthConfig); diff != "" {
				t.Errorf("stored authConfig (-want +got):\n%s", diff)
			}
		})
	}
}

func TestModels_TestConnectionAfterAProviderSwitchSendsNothingToTheSavedEndpoint(t *testing.T) {
	api, _ := newModelAPI(t)
	savedHost := newModelsRecordingProvider(t, answerWith(chatCompletionBody))
	saved := createModelAt(api, "switch-endpoint", savedHost.url, keyAuth("sk-stored"))

	result := modelsProbe(api, map[string]any{
		"modelId": saved.ID, "provider": "azure_openai", "modelName": "gpt-4o",
		"authConfig": keyAuth("sk-azure"),
	})

	if result.Success || deref(result.ErrorClass) != "config_incomplete" {
		t.Errorf("probe = success %v, errorClass %v; want config_incomplete, Azure having no default endpoint", result.Success, deref(result.ErrorClass))
	}
	if got := len(savedHost.requests()); got != 0 {
		t.Errorf("the endpoint saved for the old provider was sent %d requests", got)
	}
}

func TestModels_TestConnectionTakesTheAuthTypeBesideTheCredentials(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := fakeProvider(t, acceptOnlyKey("sk-typeless"))

	result := modelsProbe(api, map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": provider,
		"authType": "apikey", "authConfig": map[string]any{"api_key": "sk-typeless"},
	})

	if !result.Success {
		t.Errorf("probe = %+v, want the top-level authType to apply to the typeless authConfig", result)
	}
}

func TestModels_TestConnectionAsksTheModelToCallATool(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(chatCompletionBody))

	if result := modelsProbe(api, map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": provider.url, "authConfig": keyAuth("sk"),
	}); !result.Success {
		t.Fatalf("probe = %+v, want it to pass with the type inside authConfig", result)
	}

	seen := provider.requests()
	if len(seen) != 1 {
		t.Fatalf("the provider saw %d requests, want one", len(seen))
	}
	if tools, _ := seen[0].body["tools"].([]any); len(tools) == 0 {
		t.Errorf("the probe offered the model no tools: %v", seen[0].body)
	}
}

func TestModels_TestConnectionRefusesABlankModelName(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(chatCompletionBody))

	expectDetail(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "openai", "modelName": "   ", "apiBase": provider.url, "authConfig": keyAuth("sk"),
	}), http.StatusUnprocessableEntity, "modelName")
	if got := provider.requests(); len(got) != 0 {
		t.Errorf("a blank model name reached the provider %d times", len(got))
	}
}

func TestModels_TestConnectionRefusesATypeTheProviderCannotServe(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(chatCompletionBody))

	for name, tc := range map[string]struct {
		provider, modelType, want string
	}{
		"typesafe as chat":      {provider: "typesafe", modelType: "chat", want: "serves decision models only"},
		"decision on anthropic": {provider: "anthropic", modelType: "decision", want: `must use provider "typesafe" or "google_ai_studio" or "openai"`},
		"an unknown type":       {provider: "openai", modelType: "Decision", want: "chat decision"},
	} {
		t.Run(name, func(t *testing.T) {
			expectDetailSaying(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
				"provider": tc.provider, "modelName": "m", "apiBase": provider.url, "modelType": tc.modelType,
				"authConfig": keyAuth("k"),
			}), "modelType", tc.want)
		})
	}
	if got := provider.requests(); len(got) != 0 {
		t.Errorf("a refused test reached the provider %d times", len(got))
	}
}

func TestModels_TestConnectionOfASavedDecisionModelUsesItsSavedType(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, modelsDecisionAPI("sk-decisions"))
	saved := modelsCreateLuna(api, "luna-stored", provider.url+"/v1")

	if result := modelsProbe(api, map[string]any{"modelId": saved.ID, "modelType": "chat"}); !result.Success {
		t.Fatalf("probe = %+v, want the saved decision type to win", result)
	}
	seen := provider.requests()
	if len(seen) != 1 || seen[0].path != "/v1/decisions" || seen[0].body["model"] != modelsLunaName {
		t.Fatalf("the provider saw %+v, want one decision question for %s", seen, modelsLunaName)
	}

	expectDetailSaying(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"modelId": saved.ID, "provider": "typesafe",
	}), "provider", "cannot change")
	if got := provider.requests(); len(got) != 1 {
		t.Errorf("a refused provider change reached a provider (%d requests)", len(got))
	}
}

func TestModels_TestConnectionProbesATypeSafeModel(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, modelsDecisionAPI("ts-secret"))
	saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "jev-probe", "provider": "typesafe", "modelName": "jev-1.13.0", "modelType": "decision",
		"apiBase": provider.url, "authConfig": keyAuth("ts-secret"),
	}), "models")

	t.Run("a saved model", func(t *testing.T) {
		if result := modelsProbe(api, map[string]any{"modelId": saved.ID}); !result.Success {
			t.Fatalf("probe = %+v, want it to pass", result)
		}
	})

	t.Run("a new model sent without a type", func(t *testing.T) {
		if result := modelsProbe(api, map[string]any{
			"provider": "typesafe", "modelName": "jev-1.13.0", "apiBase": provider.url, "authConfig": keyAuth("ts-secret"),
		}); !result.Success {
			t.Fatalf("probe = %+v, want a TypeSafe model probed as the decision model it must be", result)
		}
	})

	t.Run("a new model sent as a decision model", func(t *testing.T) {
		before := len(provider.requests())
		if result := modelsProbe(api, map[string]any{
			"provider": "typesafe", "modelName": "jev-1.13.0", "modelType": "decision", "apiBase": provider.url,
			"authType": "apikey", "authConfig": keyAuth("ts-secret"),
		}); !result.Success {
			t.Fatalf("probe = %+v, want the decision probe to pass", result)
		}
		if asked := len(provider.requests()) - before; asked != 1 {
			t.Errorf("the provider was asked %d questions, want one decision question", asked)
		}
	})

	t.Run("a key the provider refuses", func(t *testing.T) {
		const wrongKey = "ts-wrong-key"
		rec := api.call(http.MethodPut, "/models/testConnection", map[string]any{
			"provider": "typesafe", "modelName": "jev-1.13.0", "apiBase": provider.url, "authConfig": keyAuth(wrongKey),
		})
		result := probeResult(t, rec)
		if result.Success || result.ErrorClass == nil || *result.ErrorClass != classAuthCredentialsInvalid {
			t.Errorf("probe = %+v, want an %s failure", result, classAuthCredentialsInvalid)
		}
		if strings.Contains(rec.Body.String(), wrongKey) {
			t.Errorf("the response echoes the refused key: %s", rec.Body.String())
		}
	})

	for _, req := range provider.requests() {
		if req.path != "/v1/systemone" {
			t.Errorf("the provider was asked %s, want only TypeSafe decision questions", req.path)
		}
	}
}

func TestModels_TestConnectionProbesAnOpenAIDecisionModel(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, modelsDecisionAPI("sk-decisions"))

	if result := modelsProbe(api, map[string]any{
		"provider": "openai", "modelName": modelsLunaName, "modelType": "decision",
		"apiBase": provider.url + "/v1", "authConfig": keyAuth("sk-decisions"),
	}); !result.Success {
		t.Fatalf("new-model probe = %+v, want it to pass", result)
	}
	saved := modelsCreateLuna(api, "luna-probe", provider.url+"/v1")
	if result := modelsProbe(api, map[string]any{"modelId": saved.ID}); !result.Success {
		t.Fatalf("saved-model probe = %+v, want it to pass", result)
	}

	seen := provider.requests()
	if len(seen) != 2 {
		t.Fatalf("the provider saw %d requests, want one decision question per probe", len(seen))
	}
	for _, req := range seen {
		if req.path != "/v1/decisions" || req.body["model"] != modelsLunaName {
			t.Errorf("the provider was asked %s for %v, want a decision question for %s", req.path, req.body["model"], modelsLunaName)
		}
	}
}

func TestProviderModels_AnotherProviderIsAskedWithTheKeySent(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(`{"data":[{"id":"claude-3","type":"model"}]}`))
	saved := createModelAt(api, "prov-change", provider.url, keyAuth("sk-stored"))

	rec := api.call(http.MethodPut, "/providers/anthropic/models", map[string]any{
		"modelId": saved.ID, "authConfig": keyAuth("sk-new"), "apiBase": provider.url,
	})

	expectStatus(t, rec, http.StatusOK)
	if got := decodeList[SupportedModel](t, rec).Data; len(got) != 1 {
		t.Errorf("models = %+v, want the one the provider listed", got)
	}
	seen := provider.requests()
	if len(seen) != 1 || seen[0].header.Get("x-api-key") != "sk-new" {
		t.Errorf("the provider saw %d requests, want one carrying the key sent", len(seen))
	}
}

func TestProviderModels_AnotherAuthTypeDropsTheSavedKey(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(`{"data":[{"id":"gpt-4"}]}`))
	saved := createModelAt(api, "auth-change", provider.url+"/v1", keyAuth("sk-stored"))

	rec := api.call(http.MethodPut, "/providers/openai/models", map[string]any{
		"modelId": saved.ID, "apiBase": provider.url + "/v1",
		"authConfig": map[string]any{
			"type": "oauth2", "client_id": "cid", "client_secret": "csec", "token_url": "https://auth.example.com/token",
		},
	})

	expectStatus(t, rec, http.StatusOK)
	if got := decodeList[SupportedModel](t, rec).Data; len(got) != 1 {
		t.Errorf("models = %+v, want the one the provider listed", got)
	}
	seen := provider.requests()
	if len(seen) != 1 || seen[0].header.Get("Authorization") != "" {
		t.Errorf("the provider saw %+v, want one request with no Authorization header", seen)
	}
}

func TestProviderModels_UpstreamFailureIsLoggedWithoutTheKey(t *testing.T) {
	logs := captureLogs(t)
	api, _ := newModelAPI(t)
	const key = "PROOF-SENTINEL-KEY"

	rec := api.call(http.MethodPut, "/providers/google_ai_studio/models", map[string]any{
		"authConfig": keyAuth(key), "apiBase": "http://127.0.0.1:9",
	})

	expectStatus(t, rec, http.StatusBadGateway)
	if _, logged := errorLogEntry(logs.String(), decodeError(t, rec).ErrorID); !logged {
		t.Errorf("no log line carries the 502's errorId:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), key) {
		t.Errorf("the platform log carries the key:\n%s", logs.String())
	}
}

func TestProviderModels_ListsDecisionModelsForAnOpenAIDecisionModel(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(`{"data":[{"id":"gpt-4o"}]}`))

	rec := api.call(http.MethodPut, "/providers/openai/models", map[string]any{
		"authConfig": keyAuth("sk"), "apiBase": provider.url, "modelType": "decision",
	})

	expectStatus(t, rec, http.StatusOK)
	got := decodeList[SupportedModel](t, rec).Data
	if len(got) != 1 || got[0].Key != "gpt-6-luna" {
		t.Errorf("models = %+v, want only gpt-6-luna", got)
	}
	if seen := provider.requests(); len(seen) != 0 {
		t.Errorf("listing decision models reached the provider %d times", len(seen))
	}

	expectDetail(t, api.call(http.MethodPut, "/providers/openai/models", map[string]any{
		"authConfig": keyAuth("sk"), "modelType": "embedding",
	}), http.StatusUnprocessableEntity, "modelType")
}

func TestModels_TestConnectionProbesAGeminiDecisionModelWithoutTheChatRoutingPrefix(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "gk-decisions" || r.Header.Get("Authorization") != "" || r.URL.Path != "/v1beta/decisions" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		answerWith(`{"answers":{"connection_test":{"type":"noul","noul":0.98,"confidence":0.98}}}`)(w, r)
	})
	model := map[string]any{
		"provider": "google_ai_studio", "modelName": "gemini-decision-1", "modelType": "decision",
		"apiBase": provider.url + "/v1beta", "authConfig": keyAuth("gk-decisions"),
	}

	if result := modelsProbe(api, model); !result.Success {
		t.Fatalf("new-model probe = %+v, want it to pass", result)
	}
	model["alias"] = "gemini-probe"
	saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", model), "models")
	if result := modelsProbe(api, map[string]any{"modelId": saved.ID}); !result.Success {
		t.Fatalf("saved-model probe = %+v, want it to pass", result)
	}

	for _, req := range provider.requests() {
		if req.body["model"] != "gemini-decision-1" {
			t.Errorf("the provider was asked for model %v, want gemini-decision-1", req.body["model"])
		}
	}
}

func TestProviderModels_ListsDecisionModelsForAGeminiDecisionModel(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newModelsRecordingProvider(t, answerWith(`{"models":[{"name":"models/gemini-2.5-pro"}]}`))

	rec := api.call(http.MethodPut, "/providers/google_ai_studio/models", map[string]any{
		"authConfig": keyAuth("gk"), "apiBase": provider.url, "modelType": "decision",
	})

	expectStatus(t, rec, http.StatusOK)
	got := decodeList[SupportedModel](t, rec).Data
	if len(got) != 1 || got[0].Key != "gemini-decision-1" {
		t.Errorf("models = %+v, want only gemini-decision-1", got)
	}
	if seen := provider.requests(); len(seen) != 0 {
		t.Errorf("listing decision models reached the provider %d times", len(seen))
	}

	api.call(http.MethodPut, "/providers/google_ai_studio/models", map[string]any{
		"authConfig": keyAuth("gk"), "apiBase": provider.url,
	})
	if seen := provider.requests(); len(seen) == 0 {
		t.Error("listing chat models did not ask the provider")
	}
}
