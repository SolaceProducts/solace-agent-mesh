// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type recordedRequest struct {
	path   string
	header http.Header
	model  string
}

func firstModelRequest(t *testing.T, model string) recordedRequest {
	t.Helper()
	var (
		mu       sync.Mutex
		requests []recordedRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var sent struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &sent)
		mu.Lock()
		requests = append(requests, recordedRequest{path: r.URL.Path, header: r.Header.Clone(), model: sent.Model})
		mu.Unlock()
		http.Error(w, `{"error":{"message":"rejected by the test server"}}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	cfg, err := ParseModelConfig(map[string]any{"model": model, "api_base": srv.URL, "api_key": "test-key", "num_retries": 0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := llm.NewBifrostClient(t.Context(), *cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)
	_, _ = client.ChatCompletion(t.Context(), &llm.ChatRequest{Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}})

	mu.Lock()
	defer mu.Unlock()
	if len(requests) == 0 {
		t.Fatalf("model %q sent no request", model)
	}
	return requests[0]
}

func TestModelPrefixDecidesTheAPIWhateverTheModelName(t *testing.T) {
	for _, tc := range []struct {
		model, wantPath string
		bearerAuth      bool
	}{
		{"openai/claude-sonnet-4-6", "/v1/chat/completions", true},
		{"openai/us.anthropic.claude-opus-4-8", "/v1/chat/completions", true},
		{"openai/gemini-2.5-pro", "/v1/chat/completions", true},
		{"anthropic/claude-sonnet-4-6", "/v1/messages", false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := firstModelRequest(t, tc.model)
			if got.path != tc.wantPath {
				t.Errorf("request path = %q, want %q", got.path, tc.wantPath)
			}
			if tc.bearerAuth {
				if auth := got.header.Get("Authorization"); auth != "Bearer test-key" {
					t.Errorf("Authorization = %q, want the key as a Bearer token", auth)
				}
				return
			}
			if key := got.header.Get("X-Api-Key"); key != "test-key" {
				t.Errorf("X-Api-Key = %q, want the key", key)
			}
		})
	}
}

func TestTheEndpointGetsTheModelNameWithOnlyTheRoutedPrefixRemoved(t *testing.T) {
	for _, tc := range []struct{ model, wantPath, wantModel string }{
		{"openai/openai/gpt-oss-120b", "/v1/chat/completions", "openai/gpt-oss-120b"},
		{"openai/meta-llama/Llama-3-70b", "/v1/chat/completions", "meta-llama/Llama-3-70b"},
		{"hosted_vllm/llama-3", "/v1/chat/completions", "hosted_vllm/llama-3"},
		{"openai/hosted_vllm/llama-3", "/v1/chat/completions", "hosted_vllm/llama-3"},
		{"litellm_proxy/claude-sonnet-4-6", "/v1/messages", "litellm_proxy/claude-sonnet-4-6"},
		{"anthropic/litellm_proxy/claude-sonnet-4-6", "/v1/messages", "litellm_proxy/claude-sonnet-4-6"},
		{"anthropic/claude-sonnet-4-6", "/v1/messages", "claude-sonnet-4-6"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := firstModelRequest(t, tc.model)
			if got.path != tc.wantPath || got.model != tc.wantModel {
				t.Errorf("request to %s with model %q, want %s with model %q", got.path, got.model, tc.wantPath, tc.wantModel)
			}
		})
	}
}

func TestTheNameTheWebUISavesCallsTheSameAPIAndModelAsTheStoredName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ stored, saved string }{
		{"meta-llama/Llama-3.3-70B-Instruct", "openai/meta-llama/Llama-3.3-70B-Instruct"},
		{"hf.co/org/model", "openai/hf.co/org/model"},
		{"hosted_vllm/llama-3", "openai/hosted_vllm/llama-3"},
		{"litellm_proxy/claude-sonnet-4-6", "anthropic/litellm_proxy/claude-sonnet-4-6"},
		{"my-anthropic-gw/llama-3", "anthropic/my-anthropic-gw/llama-3"},
		{"hosted_vllm/gemini-2.5-pro", "gemini/hosted_vllm/gemini-2.5-pro"},
		{"claude-sonnet-4-6", "anthropic/claude-sonnet-4-6"},
		{"gemini-2.5-pro", "gemini/gemini-2.5-pro"},
		{"gpt-oss-120b", "openai/gpt-oss-120b"},
	} {
		t.Run(tc.stored, func(t *testing.T) {
			t.Parallel()
			wantProvider, wantModel := ParseModelProvider(tc.stored)
			if provider, model := ParseModelProvider(tc.saved); provider != wantProvider || model != wantModel {
				t.Errorf("%q is called as %s/%s, but the stored %q was called as %s/%s", tc.saved, provider, model, tc.stored, wantProvider, wantModel)
			}
		})
	}
}
