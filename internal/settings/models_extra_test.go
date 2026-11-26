// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestRankModels_OpenAIOrder(t *testing.T) {
	models := []string{"gpt-3.5-turbo", "gpt-4o-mini", "gpt-4o", "gpt-4-turbo", "unknown-model"}
	ranked := RankModels(models, "openai")

	if ranked[0] != "gpt-4o" {
		t.Errorf("first model = %q, want gpt-4o", ranked[0])
	}
	if ranked[len(ranked)-1] != "unknown-model" {
		t.Errorf("last model = %q, want unknown-model", ranked[len(ranked)-1])
	}
}

func TestRankModels_UnknownProviderAlphabetic(t *testing.T) {
	models := []string{"z-model", "a-model", "m-model"}
	ranked := RankModels(models, "unknown-provider")

	if ranked[0] != "a-model" {
		t.Errorf("first model = %q, want a-model", ranked[0])
	}
}

func TestListModels_OpenAIMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-4o"},
				{"id": "gpt-4o-mini"},
				{"id": "tts-1"},
			},
			"has_more": false,
		})
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), srv.URL, "test-key", "openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}
}

func TestListModels_Anthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			http.Error(w, "missing version header", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "claude-sonnet-4-20250514"},
				{"id": "claude-3-haiku-20240307"},
			},
			"has_more": false,
		})
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), srv.URL, "test-key", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
}

func TestListModels_Google(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "models/gemini-2.5-flash"},
				{"name": "models/gemini-2.0-pro"},
			},
		})
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), srv.URL, "test-key", "google")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0] != "gemini-2.5-flash" {
		t.Errorf("first model = %q, want gemini-2.5-flash (should strip models/ prefix)", models[0])
	}
}

func TestListModels_NoBaseURL(t *testing.T) {
	models, err := ListModels(context.Background(), "", "test-key", "unknown-provider")
	if err != nil {
		t.Fatal(err)
	}
	if models != nil {
		t.Errorf("expected nil models for unknown provider with no base URL")
	}
}

func TestListModels_Pagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch page {
		case 0:
			page++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":     []map[string]any{{"id": "model-1"}, {"id": "model-2"}},
				"has_more": true,
				"last_id":  "model-2",
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":     []map[string]any{{"id": "model-3"}},
				"has_more": false,
			})
		}
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), srv.URL, "test-key", "openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3 (across 2 pages)", len(models))
	}
}

func TestFriendlyError_AllCodes(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{
			name:    "nil error",
			err:     nil,
			wantMsg: "",
		},
		{
			name:    "401 unauthorized",
			err:     &llm.Error{StatusCode: 401, Message: "invalid key"},
			wantMsg: "invalid API key",
		},
		{
			name:    "403 forbidden",
			err:     &llm.Error{StatusCode: 403, Message: "access denied"},
			wantMsg: "access denied",
		},
		{
			name:    "404 not found",
			err:     &llm.Error{StatusCode: 404, Message: "model not found"},
			wantMsg: "model not found",
		},
		{
			name:    "429 rate limit",
			err:     &llm.Error{StatusCode: 429, Message: "rate limited"},
			wantMsg: "rate limited",
		},
		{
			name:    "500 server error",
			err:     &llm.Error{StatusCode: 500, Message: "internal error"},
			wantMsg: "server error",
		},
		{
			name:    "other llm error",
			err:     &llm.Error{StatusCode: 400, Message: "bad request"},
			wantMsg: "bad request",
		},
		{
			name: "network error",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: fmt.Errorf("connection refused"),
			},
			wantMsg: "can't reach the API endpoint",
		},
		{
			name:    "connection refused string",
			err:     fmt.Errorf("connection refused"),
			wantMsg: "connection refused",
		},
		{
			name:    "timeout error",
			err:     fmt.Errorf("request timeout"),
			wantMsg: "timed out",
		},
		{
			name:    "deadline exceeded",
			err:     context.DeadlineExceeded,
			wantMsg: "timed out",
		},
		{
			name:    "generic error",
			err:     fmt.Errorf("something went wrong"),
			wantMsg: "connection test failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := friendlyError(tt.err)
			if tt.wantMsg == "" {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected error, got nil")
			}
			msg := result.Error()
			if !containsSubstring(msg, tt.wantMsg) {
				t.Errorf("error = %q, want to contain %q", msg, tt.wantMsg)
			}
		})
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsCI(s, sub))
}

func containsCI(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
