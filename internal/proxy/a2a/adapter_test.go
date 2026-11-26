// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func mustNewA2AAdapter(t *testing.T, cfg *proxy.ProxyConfig) *A2AAdapter {
	t.Helper()
	adapter, err := NewA2AAdapter(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewA2AAdapter: %v", err)
	}
	return adapter
}

func TestFetchAgentCard_HTTP(t *testing.T) {
	card := a2a.AgentCard{
		Name:            "test-agent",
		Description:     "A test agent",
		Version:         "1.0.0",
		ProtocolVersion: "0.3.0",
		URL:             "https://downstream.example.com",
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
		},
		Skills: []a2a.AgentSkill{
			{ID: "s1", Name: "skill-1", Description: "does things"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/agent-card.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:            "test",
		URL:             srv.URL,
		AgentCardPath:   "/.well-known/agent-card.json",
		SSLVerify:       true,
		UseAgentCardURL: true,
	})
	defer func() { _ = adapter.Close() }()

	got, err := adapter.FetchAgentCard(context.Background())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if got.Name != "test-agent" {
		t.Errorf("Name = %q, want %q", got.Name, "test-agent")
	}
	if !got.Capabilities.Streaming {
		t.Error("expected Streaming capability")
	}
	if len(got.Skills) != 1 || got.Skills[0].ID != "s1" {
		t.Errorf("unexpected skills: %+v", got.Skills)
	}
	want := proxy.RemoteInterface{URL: "https://downstream.example.com", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion03}
	if chosen := adapter.negotiated.Load().chosen(); chosen != want {
		t.Errorf("chosen interface = %+v, want %+v", chosen, want)
	}
}

func TestFetchAgentCard_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           srv.URL,
		AgentCardPath: "/.well-known/agent-card.json",
		SSLVerify:     true,
	})
	defer func() { _ = adapter.Close() }()

	_, err := adapter.FetchAgentCard(context.Background())
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestFetchAgentCard_NonOKResponseBodyNeverLeaksIntoError(t *testing.T) {
	secretBody := "Invalid API key: sk-live-super-secret-token-do-not-leak"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(secretBody))
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           srv.URL,
		AgentCardPath: "/.well-known/agent-card.json",
		SSLVerify:     true,
	})
	defer func() { _ = adapter.Close() }()

	_, err := adapter.FetchAgentCard(context.Background())
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if strings.Contains(err.Error(), "sk-live-super-secret-token-do-not-leak") {
		t.Fatalf("error leaked the response body: %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should still name the status code, got: %v", err)
	}
}

func TestFetchAgentCard_TransportErrorNeverLeaksURL(t *testing.T) {
	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           "http://127.0.0.1:1",
		AgentCardPath: "/.well-known/agent-card.json?apiKey=super-secret-value",
		SSLVerify:     true,
	})
	defer func() { _ = adapter.Close() }()

	_, err := adapter.FetchAgentCard(context.Background())
	if err == nil {
		t.Fatal("expected a transport error against an unreachable host")
	}
	if strings.Contains(err.Error(), "apiKey") || strings.Contains(err.Error(), "super-secret-value") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaked the request URL: %v", err)
	}
}

func TestFetchAgentCard_LegacyFallback(t *testing.T) {
	card := a2a.AgentCard{Name: "legacy-agent", Description: "legacy", Version: "1.0.0", ProtocolVersion: "0.3.0"}
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/.well-known/agent.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           srv.URL,
		AgentCardPath: "/.well-known/agent-card.json",
		SSLVerify:     true,
	})
	defer func() { _ = adapter.Close() }()

	got, err := adapter.FetchAgentCard(context.Background())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if got.Name != "legacy-agent" {
		t.Errorf("Name = %q, want %q", got.Name, "legacy-agent")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "/.well-known/agent-card.json" || seen[1] != "/.well-known/agent.json" {
		t.Errorf("expected current-then-legacy fetch order, got %v", seen)
	}
}

func TestFetchAgentCard_NoFallbackOnAuthError(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           srv.URL,
		AgentCardPath: "/.well-known/agent-card.json",
		SSLVerify:     true,
	})
	defer func() { _ = adapter.Close() }()

	if _, err := adapter.FetchAgentCard(context.Background()); err == nil {
		t.Fatal("expected error for 401 response")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "/.well-known/agent-card.json" {
		t.Errorf("401 must not fall back to the legacy path; requests seen: %v", seen)
	}
}

func TestFetchAgentCard_CustomPathNoFallback(t *testing.T) {
	card := a2a.AgentCard{Name: "legacy-agent", Description: "legacy", Version: "1.0.0"}
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/.well-known/agent.json" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(card)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           srv.URL,
		AgentCardPath: "/custom/card.json",
		SSLVerify:     true,
	})
	defer func() { _ = adapter.Close() }()

	if _, err := adapter.FetchAgentCard(context.Background()); err == nil {
		t.Fatal("expected error: a custom path must not fall back to a well-known path")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "/custom/card.json" {
		t.Errorf("custom path must not fall back to any well-known path; requests seen: %v", seen)
	}
}

func TestCardCandidatePaths(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		want       []string
	}{
		{"default current path falls back to legacy", "/.well-known/agent-card.json", []string{"/.well-known/agent-card.json", "/.well-known/agent.json"}},
		{"legacy path falls back to current", "/.well-known/agent.json", []string{"/.well-known/agent.json", "/.well-known/agent-card.json"}},
		{"custom path has no fallback", "/custom/card.json", []string{"/custom/card.json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cardCandidatePaths(tt.configured)
			if len(got) != len(tt.want) {
				t.Fatalf("cardCandidatePaths(%q) = %v, want %v", tt.configured, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("cardCandidatePaths(%q) = %v, want %v", tt.configured, got, tt.want)
				}
			}
		})
	}
}

func TestFetchAgentCard_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "not a valid agent card")
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           srv.URL,
		AgentCardPath: "/.well-known/agent-card.json",
		SSLVerify:     true,
	})
	defer func() { _ = adapter.Close() }()

	if _, err := adapter.FetchAgentCard(context.Background()); err == nil {
		t.Fatal("expected a parse error for a non-JSON agent card response")
	}
}

func TestFetchAgentCard_WithCardAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a2a.AgentCard{Name: "auth-agent"})
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           srv.URL,
		AgentCardPath: "/.well-known/agent-card.json",
		SSLVerify:     true,
		AgentCardAuth: &proxy.AuthConfig{
			Type:  "static_bearer",
			Token: "card-token",
		},
	})
	defer func() { _ = adapter.Close() }()

	_, err := adapter.FetchAgentCard(context.Background())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if gotAuth != "Bearer card-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer card-token")
	}
}

func TestFetchAgentCard_CustomHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a2a.AgentCard{Name: "hdr-agent"})
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "test",
		URL:           srv.URL,
		AgentCardPath: "/.well-known/agent-card.json",
		SSLVerify:     true,
		AgentCardHeaders: []proxy.HeaderConfig{
			{Name: "X-Custom", Value: "custom-val"},
		},
	})
	defer func() { _ = adapter.Close() }()

	_, _ = adapter.FetchAgentCard(context.Background())
	if gotHeader != "custom-val" {
		t.Errorf("X-Custom = %q, want %q", gotHeader, "custom-val")
	}
}

func TestFetchAgentCard_StaticCard(t *testing.T) {
	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name: "static-agent",
		URL:  "http://unused.example.com",
		AgentCardData: map[string]any{
			"name":        "my-static-agent",
			"description": "A statically-configured agent",
			"version":     "2.0.0",
			"capabilities": map[string]any{
				"streaming": true,
			},
			"skills": []any{
				map[string]any{
					"id":          "sk1",
					"name":        "summarize",
					"description": "Summarizes text",
					"inputModes":  []any{"text"},
					"outputModes": []any{"text"},
				},
			},
		},
		SSLVerify: true,
	})
	defer func() { _ = adapter.Close() }()

	card, err := adapter.FetchAgentCard(context.Background())
	if err != nil {
		t.Fatalf("FetchAgentCard (static): %v", err)
	}
	if card.Name != "my-static-agent" {
		t.Errorf("Name = %q, want %q", card.Name, "my-static-agent")
	}
	if card.Description != "A statically-configured agent" {
		t.Errorf("Description = %q", card.Description)
	}
	if card.Version != "2.0.0" {
		t.Errorf("Version = %q", card.Version)
	}
	if card.ProtocolVersion != "0.3.0" {
		t.Errorf("ProtocolVersion = %q", card.ProtocolVersion)
	}
	if !card.Capabilities.Streaming {
		t.Error("expected Streaming capability")
	}
	if len(card.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(card.Skills))
	}
	if card.Skills[0].ID != "sk1" {
		t.Errorf("skill ID = %q, want %q", card.Skills[0].ID, "sk1")
	}
	if len(card.Skills[0].InputModes) != 1 || card.Skills[0].InputModes[0] != "text" {
		t.Errorf("skill input modes = %v", card.Skills[0].InputModes)
	}
}

func TestFetchAgentCard_StaticCard_DefaultValues(t *testing.T) {
	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:          "fallback-agent",
		URL:           "http://unused.example.com",
		AgentCardData: map[string]any{},
		SSLVerify:     true,
	})
	defer func() { _ = adapter.Close() }()

	card, err := adapter.FetchAgentCard(context.Background())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if card.Name != "fallback-agent" {
		t.Errorf("Name = %q, want %q", card.Name, "fallback-agent")
	}
	if card.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", card.Version, "1.0.0")
	}
	if len(card.DefaultInputModes) != 1 || card.DefaultInputModes[0] != "text" {
		t.Errorf("DefaultInputModes = %v", card.DefaultInputModes)
	}
	if len(card.DefaultOutputModes) != 1 || card.DefaultOutputModes[0] != "text" {
		t.Errorf("DefaultOutputModes = %v", card.DefaultOutputModes)
	}
}

func TestBuildSecuritySchemes_StaticBearer(t *testing.T) {
	cfg := &proxy.ProxyConfig{
		TaskAuth: &proxy.AuthConfig{Type: "static_bearer"},
	}
	schemes, security := buildSecuritySchemes(cfg)
	if schemes == nil {
		t.Fatal("expected non-nil schemes")
	}
	s, ok := schemes["bearer"]
	if !ok {
		t.Fatal("expected 'bearer' scheme")
	}
	if s.Type != "http" || s.Scheme != "bearer" {
		t.Errorf("scheme = %+v", s)
	}
	if len(security) != 1 {
		t.Fatalf("expected 1 security entry, got %d", len(security))
	}
}

func TestBuildSecuritySchemes_StaticAPIKey(t *testing.T) {
	cfg := &proxy.ProxyConfig{
		TaskAuth: &proxy.AuthConfig{Type: "static_apikey", Header: "X-My-Key"},
	}
	schemes, security := buildSecuritySchemes(cfg)
	s := schemes["apikey"]
	if s.Type != "apiKey" || s.In != "header" || s.Name != "X-My-Key" {
		t.Errorf("scheme = %+v", s)
	}
	if len(security) != 1 {
		t.Fatalf("expected 1 security entry, got %d", len(security))
	}
}

func TestBuildSecuritySchemes_StaticAPIKey_DefaultHeader(t *testing.T) {
	cfg := &proxy.ProxyConfig{
		TaskAuth: &proxy.AuthConfig{Type: "static_apikey"},
	}
	schemes, _ := buildSecuritySchemes(cfg)
	if schemes["apikey"].Name != "X-Api-Key" {
		t.Errorf("expected default header X-Api-Key, got %q", schemes["apikey"].Name)
	}
}

func TestBuildSecuritySchemes_OAuth2ClientCredentials(t *testing.T) {
	cfg := &proxy.ProxyConfig{
		TaskAuth: &proxy.AuthConfig{
			Type:     "oauth2_client_credentials",
			TokenURL: "https://auth.example.com/token",
			Scope:    "read",
		},
	}
	schemes, security := buildSecuritySchemes(cfg)
	s := schemes["oauth2"]
	if s.Type != "oauth2" {
		t.Errorf("type = %q", s.Type)
	}
	if s.Flows == nil || s.Flows.ClientCredentials == nil {
		t.Fatal("expected ClientCredentials flow")
	}
	if s.Flows.ClientCredentials.TokenURL != "https://auth.example.com/token" {
		t.Errorf("tokenUrl = %q", s.Flows.ClientCredentials.TokenURL)
	}
	if _, ok := s.Flows.ClientCredentials.Scopes["read"]; !ok {
		t.Error("expected 'read' scope")
	}
	if len(security) != 1 {
		t.Fatalf("expected 1 security entry, got %d", len(security))
	}
}

func TestBuildSecuritySchemes_OAuth2AuthorizationCode(t *testing.T) {
	cfg := &proxy.ProxyConfig{
		TaskAuth: &proxy.AuthConfig{
			Type:             "oauth2_authorization_code",
			TokenURL:         "https://auth.example.com/token",
			AuthorizationURL: "https://auth.example.com/authorize",
			Scopes:           []string{"openid", "profile"},
		},
	}
	schemes, _ := buildSecuritySchemes(cfg)
	s := schemes["oauth2"]
	if s.Flows == nil || s.Flows.AuthorizationCode == nil {
		t.Fatal("expected AuthorizationCode flow")
	}
	if s.Flows.AuthorizationCode.AuthorizationURL != "https://auth.example.com/authorize" {
		t.Errorf("authorizationUrl = %q", s.Flows.AuthorizationCode.AuthorizationURL)
	}
	if len(s.Flows.AuthorizationCode.Scopes) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(s.Flows.AuthorizationCode.Scopes))
	}
}

func TestBuildSecuritySchemes_NoAuth(t *testing.T) {
	cfg := &proxy.ProxyConfig{}
	schemes, security := buildSecuritySchemes(cfg)
	if schemes != nil {
		t.Error("expected nil schemes for no auth")
	}
	if security != nil {
		t.Error("expected nil security for no auth")
	}
}

func TestBuildSecuritySchemes_UnknownType(t *testing.T) {
	cfg := &proxy.ProxyConfig{
		TaskAuth: &proxy.AuthConfig{Type: "unknown"},
	}
	schemes, security := buildSecuritySchemes(cfg)
	if schemes != nil || security != nil {
		t.Error("expected nil for unknown auth type")
	}
}

func TestTargetURL_UsesCardURL(t *testing.T) {
	adapter := &A2AAdapter{
		cfg: &proxy.ProxyConfig{
			URL:             "http://config-url.example.com",
			UseAgentCardURL: true,
		},
	}
	if got := adapter.targetURL(proxy.RemoteInterface{URL: "http://card-url.example.com"}); got != "http://card-url.example.com" {
		t.Errorf("targetURL = %q, want card URL", got)
	}
}

func TestTargetURL_FallsBackToConfigURL(t *testing.T) {
	adapter := &A2AAdapter{
		cfg: &proxy.ProxyConfig{
			URL:             "http://config-url.example.com",
			UseAgentCardURL: true,
		},
	}
	if got := adapter.targetURL(proxy.RemoteInterface{}); got != "http://config-url.example.com" {
		t.Errorf("targetURL = %q, want config URL", got)
	}
}

func TestTargetURL_UseAgentCardURLFalse(t *testing.T) {
	adapter := &A2AAdapter{
		cfg: &proxy.ProxyConfig{
			URL:             "http://config-url.example.com",
			UseAgentCardURL: false,
		},
	}
	if got := adapter.targetURL(proxy.RemoteInterface{URL: "http://card-url.example.com"}); got != "http://config-url.example.com" {
		t.Errorf("targetURL = %q, want config URL when UseAgentCardURL=false", got)
	}
}

func TestForwardRequest_NonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a2a.Response{
			JSONRPC: "2.0",
			ID:      "t1",
			Result: a2a.Task{
				ID:   "t1",
				Kind: "task",
				Status: a2a.TaskStatus{
					State: a2a.TaskStateCompleted,
				},
			},
		})
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       srv.URL,
		SSLVerify: true,
	})
	defer func() { _ = adapter.Close() }()

	req := a2a.NewRequest("t1", "message/send", map[string]any{"message": "hello"})
	task := &proxy.TaskContext{TaskID: "t1"}
	ch, err := adapter.ForwardRequest(context.Background(), task, req)
	if err != nil {
		t.Fatalf("ForwardRequest: %v", err)
	}

	evt := <-ch
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.Task == nil {
		t.Fatal("expected Task in response")
	}
	if evt.Task.ID != "t1" {
		t.Errorf("task ID = %q, want %q", evt.Task.ID, "t1")
	}
}

func TestForwardRequest_StreamingSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected Flusher")
		}
		_, _ = w.Write([]byte("data: {\"result\":{\"kind\":\"status-update\",\"taskId\":\"t1\",\"status\":{\"state\":\"working\"}}}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"result\":{\"kind\":\"task\",\"id\":\"t1\",\"status\":{\"state\":\"completed\"}}}\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       srv.URL,
		SSLVerify: true,
	})
	defer func() { _ = adapter.Close() }()

	req := a2a.NewRequest("t1", "message/stream", map[string]any{"message": "hello"})
	task := &proxy.TaskContext{TaskID: "t1", IsStreaming: true}
	ch, err := adapter.ForwardRequest(context.Background(), task, req)
	if err != nil {
		t.Fatalf("ForwardRequest: %v", err)
	}

	evt1 := <-ch
	if evt1.StatusUpdate == nil {
		t.Fatal("expected StatusUpdate as first event")
	}

	evt2 := <-ch
	if evt2.Task == nil {
		t.Fatal("expected Task as second event")
	}
}

func TestForwardRequest_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       srv.URL,
		SSLVerify: true,
	})
	defer func() { _ = adapter.Close() }()

	req := a2a.NewRequest("t1", "message/send", nil)
	task := &proxy.TaskContext{TaskID: "t1"}
	ch, err := adapter.ForwardRequest(context.Background(), task, req)
	if err != nil {
		t.Fatalf("ForwardRequest: %v", err)
	}

	evt := <-ch
	if evt.Error == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestForwardRequest_WithTaskAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a2a.Response{
			JSONRPC: "2.0",
			ID:      "t1",
			Result:  a2a.Task{ID: "t1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}},
		})
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       srv.URL,
		SSLVerify: true,
		TaskAuth: &proxy.AuthConfig{
			Type:  "static_bearer",
			Token: "task-secret",
		},
	})
	defer func() { _ = adapter.Close() }()

	req := a2a.NewRequest("t1", "message/send", nil)
	task := &proxy.TaskContext{TaskID: "t1"}
	ch, _ := adapter.ForwardRequest(context.Background(), task, req)
	<-ch

	if gotAuth != "Bearer task-secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer task-secret")
	}
}

func TestForwardRequest_TaskAuthWinsOverCustomHeader(t *testing.T) {
	var gotAuth, gotCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Custom")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a2a.Response{
			JSONRPC: "2.0",
			ID:      "t1",
			Result:  a2a.Task{ID: "t1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}},
		})
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       srv.URL,
		SSLVerify: true,
		TaskAuth: &proxy.AuthConfig{
			Type:  "static_bearer",
			Token: "task-secret",
		},
		TaskHeaders: []proxy.HeaderConfig{
			{Name: "Authorization", Value: "Bearer header-override-attempt"},
			{Name: "X-Custom", Value: "custom-val"},
		},
	})
	defer func() { _ = adapter.Close() }()

	req := a2a.NewRequest("t1", "message/send", nil)
	task := &proxy.TaskContext{TaskID: "t1"}
	ch, _ := adapter.ForwardRequest(context.Background(), task, req)
	<-ch

	if gotAuth != "Bearer task-secret" {
		t.Errorf("Authorization = %q, want configured auth to win (%q)", gotAuth, "Bearer task-secret")
	}
	if gotCustom != "custom-val" {
		t.Errorf("X-Custom = %q, want %q", gotCustom, "custom-val")
	}
}

func TestForwardRequest_OAuth2TokenOverride(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a2a.Response{
			JSONRPC: "2.0",
			ID:      "t1",
			Result:  a2a.Task{ID: "t1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}},
		})
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       srv.URL,
		SSLVerify: true,
	})
	defer func() { _ = adapter.Close() }()

	req := a2a.NewRequest("t1", "message/send", nil)
	task := &proxy.TaskContext{TaskID: "t1", OAuth2Token: "user-token-xyz"}
	ch, _ := adapter.ForwardRequest(context.Background(), task, req)
	<-ch

	if gotAuth != "Bearer user-token-xyz" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer user-token-xyz")
	}
}

func TestCancelTask_NoDownstreamID(t *testing.T) {
	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       "http://unused.example.com",
		SSLVerify: true,
	})
	defer func() { _ = adapter.Close() }()

	err := adapter.CancelTask(context.Background(), &proxy.TaskContext{})
	if err != nil {
		t.Errorf("expected no error for empty DownstreamTaskID, got: %v", err)
	}
}

func TestCancelTask_Success(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       srv.URL,
		SSLVerify: true,
	})
	defer func() { _ = adapter.Close() }()

	err := adapter.CancelTask(context.Background(), &proxy.TaskContext{
		DownstreamTaskID: "d-task-1",
	})
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
}

func TestCancelTask_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{
		Name:      "test",
		URL:       srv.URL,
		SSLVerify: true,
	})
	defer func() { _ = adapter.Close() }()

	err := adapter.CancelTask(context.Background(), &proxy.TaskContext{
		DownstreamTaskID: "d-task-1",
	})
	if err == nil {
		t.Fatal("expected error for non-2xx cancel response")
	}
}
