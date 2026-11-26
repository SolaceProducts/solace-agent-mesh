// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package modelclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const completion = `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

const temperatureRefusal = `{"error":{"message":"Unsupported value: 'temperature' does not support 0.2 with this model. Only the default (1) value is supported.","type":"invalid_request_error","param":"temperature","code":"unsupported_value"}}`

func temperatureLockedProvider(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		if _, carries := body["temperature"]; carries {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(temperatureRefusal))
			return
		}
		_, _ = w.Write([]byte(completion))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func storedTemperatureConfig(baseURL string) map[string]any {
	raw := map[string]any{"model": "openai/gpt-4o", "api_base": baseURL, "api_key": "sk-test"}
	samapp.WithModelParams(raw, map[string]any{"temperature": 0.2})
	return raw
}

func build(t *testing.T, raw any, opts Options) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := Build(ctx, raw, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

func userTurn() *llm.ChatRequest {
	return &llm.ChatRequest{Messages: []llm.ChatMessage{{Role: "user", Content: "Hi"}}, WithStoredParams: true}
}

func TestBuild_ServingClientIsMeteredAndKeepsServingPastARefusedStoredTemperature(t *testing.T) {
	provider, requests := temperatureLockedProvider(t)
	client := build(t, storedTemperatureConfig(provider.URL), Options{})

	if _, metered := client.Client.(*observability.InstrumentedLLMClient); !metered {
		t.Errorf("serving client is %T, want it wrapped for the LLM metrics", client.Client)
	}
	if client.Provider != "openai" || client.Model != "gpt-4o" {
		t.Errorf("Provider, Model = %q, %q, want openai, gpt-4o", client.Provider, client.Model)
	}
	if _, err := client.ChatCompletion(t.Context(), userTurn()); err != nil {
		t.Fatalf("a task failed on a stored temperature the model refuses: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("provider received %d requests, want the refused one and its redo", got)
	}
}

func TestBuild_ConnectionTestClientReportsARefusedStoredTemperatureAndIsUnmetered(t *testing.T) {
	provider, requests := temperatureLockedProvider(t)
	client := build(t, storedTemperatureConfig(provider.URL), Options{ConnectionTest: &ConnectionTest{TimeoutSeconds: 10}})

	if _, metered := client.Client.(*observability.InstrumentedLLMClient); metered {
		t.Error("a connection test's calls would be recorded in the LLM metrics under whatever model name the form holds")
	}
	_, err := client.ChatCompletion(t.Context(), userTurn())
	llmErr, ok := errors.AsType[*llm.Error](err)
	if !ok || !llmErr.IsTemperatureUnsupportedError() {
		t.Fatalf("err = %v, want the provider's temperature refusal", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("provider received %d requests, want 1: a connection test makes one attempt", got)
	}
}

type staticGrants struct{ token string }

func (g staticGrants) GrantToken(context.Context, string, string) (llm.GrantToken, error) {
	return llm.GrantToken{AccessToken: g.token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestBuild_GrantModelNeedsTheProcessGrantSource(t *testing.T) {
	raw := map[string]any{"model": "openai/gpt-4o", "api_base": "https://llm.invalid/v1", llm.GrantIDKey: "grant-1"}

	_, err := Build(t.Context(), raw, Options{})
	if _, ok := errors.AsType[*BuildError](err); !ok {
		t.Fatalf("err = %v, want a BuildError for a grant model built without a grant source", err)
	}

	build(t, raw, Options{Grants: staticGrants{token: "grant-token"}})
}

func TestBuild_UnreadableConfigIsAConfigError(t *testing.T) {
	_, err := Build(t.Context(), map[string]any{"model": "openai/gpt-4o", "api_key_file": "/nonexistent/key"}, Options{})
	if _, ok := errors.AsType[*ConfigError](err); !ok {
		t.Fatalf("err = %T %v, want a ConfigError", err, err)
	}
}

func apiKeyEnvVars() []string {
	var names []string
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "SAM_AWE_LLM_API_KEY_") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func TestBuild_FailedBuildLeavesNoAPIKeyInTheEnvironment(t *testing.T) {
	before := apiKeyEnvVars()

	_, err := Build(t.Context(), map[string]any{
		"model":               "openai/gpt-4o",
		"api_key":             "sk-test",
		"oauth_token_url":     "http://insecure.invalid/token",
		"oauth_client_id":     "id",
		"oauth_client_secret": "secret",
	}, Options{})
	buildErr, ok := errors.AsType[*BuildError](err)
	if !ok {
		t.Fatalf("err = %T %v, want a BuildError", err, err)
	}
	if buildErr.Provider != "openai" {
		t.Errorf("BuildError.Provider = %q, want openai", buildErr.Provider)
	}

	if after := apiKeyEnvVars(); !slices.Equal(before, after) {
		t.Errorf("the API key of a config no client was built from stayed in the environment: before %v, after %v", before, after)
	}
}
