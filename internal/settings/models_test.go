// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-4o"},
				{"id": "gpt-3.5-turbo"},
				{"id": "gpt-4-turbo"},
			},
		})
	}))
	defer server.Close()

	ctx := context.Background()
	models, err := ListModels(ctx, server.URL, "test-key", "openai")
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}
}

func TestListModels_OpenAICompatibleStripsTrailingV1(t *testing.T) {
	for _, provider := range []string{"openai", "groq"} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"id": "gpt-4o"}},
				})
			}))
			defer server.Close()

			models, err := ListModels(context.Background(), server.URL+"/v1", "test-key", provider)
			if err != nil {
				t.Fatalf("ListModels error: %v", err)
			}
			if len(models) != 1 || models[0] != "gpt-4o" {
				t.Fatalf("models = %v, want one gpt-4o", models)
			}
		})
	}
}

func TestListModels_AnthropicStripsTrailingV1(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "claude-3-opus"}},
		})
	}))
	defer server.Close()

	models, err := ListModels(context.Background(), server.URL+"/v1", "sk-ant", "anthropic")
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 1 || models[0] != "claude-3-opus" {
		t.Fatalf("models = %v, want one claude-3-opus", models)
	}
}

func TestListModels_NonOpenAICompatiblePreservesV1(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
	}))
	defer server.Close()

	if _, err := ListModels(context.Background(), server.URL+"/v1", "test-key", "azure"); err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if gotPath != "/v1/v1/models" {
		t.Fatalf("probe path = %q, want /v1/v1/models (azure /v1 must not be stripped)", gotPath)
	}
}

func TestListModels_EmptyBaseURL(t *testing.T) {
	models, err := ListModels(context.Background(), "", "test-key", "openai")
	if err != nil {
		t.Fatalf("ListModels with empty baseURL must not error, got: %v", err)
	}
	if models != nil {
		t.Errorf("ListModels with empty baseURL must return nil, got: %v", models)
	}
}

func TestTestConnection_EmptyBaseURL(t *testing.T) {
	err := TestConnection(context.Background(), "", "test-key", "openai", "gpt-4")
	if err == nil {
		t.Fatal("TestConnection with empty baseURL must error")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("error should mention base_url, got: %v", err)
	}
}

func TestListModelsNonSupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	ctx := context.Background()
	models, err := ListModels(ctx, server.URL, "test-key", "openai")
	if err != nil {
		t.Fatalf("expected nil error for non-supported endpoint, got: %v", err)
	}
	if models != nil {
		t.Errorf("expected nil models for non-supported endpoint, got: %v", models)
	}
}

func TestRankModels(t *testing.T) {
	models := []string{"gpt-3.5-turbo", "gpt-4-turbo", "custom-model", "gpt-4o"}
	ranked := RankModels(models, "openai")

	if ranked[0] != "gpt-4o" {
		t.Errorf("expected gpt-4o first, got %q", ranked[0])
	}
	if ranked[1] != "gpt-4-turbo" {
		t.Errorf("expected gpt-4-turbo second, got %q", ranked[1])
	}
	if ranked[2] != "gpt-3.5-turbo" {
		t.Errorf("expected gpt-3.5-turbo third, got %q", ranked[2])
	}
	if ranked[3] != "custom-model" {
		t.Errorf("expected custom-model last, got %q", ranked[3])
	}
}

func TestRankModelsGoogle(t *testing.T) {
	models := []string{"gemini-1.5-pro", "gemini-3.5-flash", "custom-model", "gemini-3.6-flash", "gemini-2.5-flash"}
	ranked := RankModels(models, "google")

	want := []string{"gemini-3.6-flash", "gemini-3.5-flash", "gemini-2.5-flash", "gemini-1.5-pro", "custom-model"}
	for i, w := range want {
		if ranked[i] != w {
			t.Errorf("ranked[%d] = %q, want %q (full order: %v)", i, ranked[i], w, ranked)
		}
	}
}

func TestRankModelsAnthropic(t *testing.T) {
	models := []string{"claude-3-haiku-20240307", "claude-opus-5", "custom-model", "claude-sonnet-5"}
	ranked := RankModels(models, "anthropic")

	want := []string{"claude-sonnet-5", "claude-opus-5", "claude-3-haiku-20240307", "custom-model"}
	for i, w := range want {
		if ranked[i] != w {
			t.Errorf("ranked[%d] = %q, want %q (full order: %v)", i, ranked[i], w, ranked)
		}
	}
	if ranked[0] != DefaultModelForProvider("anthropic") {
		t.Errorf("top-ranked %q disagrees with DefaultModelForProvider(anthropic) = %q",
			ranked[0], DefaultModelForProvider("anthropic"))
	}
}

func TestRankModelsOpenAITopIsDefault(t *testing.T) {
	ranked := RankModels([]string{"gpt-4o", "gpt-5.6-sol", "custom-model"}, "openai")
	if ranked[0] != "gpt-5.6-sol" {
		t.Errorf("ranked[0] = %q, want gpt-5.6-sol (full order: %v)", ranked[0], ranked)
	}
	if ranked[0] != DefaultModelForProvider("openai") {
		t.Errorf("top-ranked %q disagrees with DefaultModelForProvider(openai) = %q",
			ranked[0], DefaultModelForProvider("openai"))
	}
}

func TestRankModelsUnknownProvider(t *testing.T) {
	models := []string{"zebra", "alpha", "middle"}
	ranked := RankModels(models, "unknown-provider")

	if ranked[0] != "alpha" || ranked[1] != "middle" || ranked[2] != "zebra" {
		t.Errorf("expected alphabetical sort, got %v", ranked)
	}
}

func TestFriendlyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		contains string
	}{
		{
			name:     "nil error",
			err:      nil,
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := friendlyError(tt.err)
			if tt.err == nil && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
		})
	}
}

func TestListModels_RequestLineForExistingBaseURLs(t *testing.T) {
	for _, tc := range []struct {
		name, provider, suffix, wantPath, wantQuery string
	}{
		{"bare host", "openai", "", "/v1/models", ""},
		{"trailing slash", "openai", "/", "/v1/models", ""},
		{"trailing v1", "openai", "/v1", "/v1/models", ""},
		{"gateway prefix", "openai", "/openai", "/openai/v1/models", ""},
		{"other version prefix", "openai", "/api/v3", "/api/v3/v1/models", ""},
		{"query on the base", "openai", "/api/v3?k=v", "/api/v3", "k=v/v1/models"},
		{"v1 after a chat path", "openai", "/chat/completions/v1", "/chat/completions/v1/models", ""},
		{"full endpoint on another provider", "groq", "/api/v3/chat/completions", "/api/v3/chat/completions/v1/models", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotQuery := listModelsRequestLine(t, tc.provider, tc.suffix)
			if gotPath != tc.wantPath || gotQuery != tc.wantQuery {
				t.Errorf("models request line = %q?%q, want %q?%q", gotPath, gotQuery, tc.wantPath, tc.wantQuery)
			}
		})
	}
}

func TestListModels_CompleteEndpointListsItsSibling(t *testing.T) {
	if path, query := listModelsRequestLine(t, "openai", "/api/v3/chat/completions"); path != "/api/v3/models" || query != "" {
		t.Errorf("models request line = %q?%q, want /api/v3/models", path, query)
	}

	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		page := map[string]any{"data": []map[string]any{{"id": "b"}}}
		if r.URL.Query().Get("after") == "" {
			page = map[string]any{"data": []map[string]any{{"id": "a"}}, "has_more": true, "last_id": "a"}
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	models, err := ListModels(t.Context(), server.URL+"/api/v3/chat/completions?k=v", "test-key", "openai")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || len(queries) != 2 || queries[1] != "k=v&after=a" {
		t.Errorf("models = %v, queries = %v, want two pages with the second at k=v&after=a", models, queries)
	}
}

func listModelsRequestLine(t *testing.T, provider, baseSuffix string) (path, query string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m"}}})
	}))
	defer server.Close()

	if _, err := ListModels(t.Context(), server.URL+baseSuffix, "test-key", provider); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	return path, query
}
