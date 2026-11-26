// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
)

func mockTokenServer(t *testing.T, token string, expiresIn int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := map[string]any{
			"access_token": token,
			"expires_in":   expiresIn,
			"token_type":   "bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func mockTokenServerWithCounter(t *testing.T, token string, expiresIn int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := map[string]any{
			"access_token": token,
			"expires_in":   expiresIn,
			"token_type":   "bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return srv, &count
}

func mockTLSTokenServer(t *testing.T, token string, expiresIn int) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := map[string]any{
			"access_token": token,
			"expires_in":   expiresIn,
			"token_type":   "bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))

	cert := srv.Certificate()
	if cert == nil {
		srv.Close()
		t.Fatal("mockTLSTokenServer: no certificate on TLS server")
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	caPath := filepath.Join(t.TempDir(), "oauth-ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		srv.Close()
		t.Fatalf("mockTLSTokenServer: write CA PEM: %v", err)
	}
	return srv, caPath
}

func TestOAuth2_HeaderInjection(t *testing.T) {
	tokenServer := mockTokenServer(t, "oauth-test-token-xyz", 3600)
	defer tokenServer.Close()

	var capturedHeaders http.Header
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		nonStreamHandler("ok", "stop")(w, r)
	})
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
		OAuth2: &oauth2cc.Config{
			TokenURL:              tokenServer.URL,
			ClientID:              "test-cid",
			ClientSecret:          "test-csecret",
			AllowInsecureTokenURL: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	_, err = client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := capturedHeaders.Get("Authorization"); got != "Bearer oauth-test-token-xyz" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer oauth-test-token-xyz")
	}
}

func TestOAuth2_FallbackToAPIKey(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("token server down"))
	}))
	defer tokenServer.Close()

	var capturedHeaders http.Header
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		nonStreamHandler("ok", "stop")(w, r)
	})
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "fallback-api-key")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:     "openai",
		Model:        "test-model",
		BaseURL:      llmServer.URL,
		APIKeyEnvVar: "OPENAI_API_KEY",
		OAuth2: &oauth2cc.Config{
			TokenURL:              tokenServer.URL,
			ClientID:              "test-cid",
			ClientSecret:          "test-csecret",
			MaxRetries:            0,
			AllowInsecureTokenURL: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	_, err = client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	auth := capturedHeaders.Get("Authorization")
	if strings.Contains(auth, "Bearer oauth") {
		t.Errorf("should not have OAuth2 token, got %q", auth)
	}
}

func TestOAuth2_NoFallback_Error(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer tokenServer.Close()

	llmServer := mockOpenAIServer(t, nonStreamHandler("ok", "stop"))
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
		OAuth2: &oauth2cc.Config{
			TokenURL:              tokenServer.URL,
			ClientID:              "bad-cid",
			ClientSecret:          "bad-csecret",
			MaxRetries:            0,
			AllowInsecureTokenURL: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	_, err = client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when OAuth2 fails and no API key")
	}
}

func TestOAuth2_NotConfigured(t *testing.T) {
	var capturedHeaders http.Header
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		nonStreamHandler("ok", "stop")(w, r)
	})
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "plain-key")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	_, err = client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	auth := capturedHeaders.Get("Authorization")
	if strings.Contains(auth, "Bearer oauth") {
		t.Errorf("unexpected OAuth2 token: %q", auth)
	}
}

func TestOAuth2_StreamingHeaderInjection(t *testing.T) {
	tokenServer := mockTokenServer(t, "stream-token", 3600)
	defer tokenServer.Close()

	var capturedAuth string
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("expected ResponseWriter to support http.Flusher")
			return
		}
		chunks := []string{"Hello", " from", " stream"}
		for i, c := range chunks {
			finishReason := "null"
			if i == len(chunks)-1 {
				finishReason = `"stop"`
			}
			payload := fmt.Sprintf(
				`{"id":"chatcmpl-stream","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":%q},"finish_reason":%s}]}`,
				c, finishReason,
			)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	})
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
		OAuth2: &oauth2cc.Config{
			TokenURL:              tokenServer.URL,
			ClientID:              "test-cid",
			ClientSecret:          "test-csecret",
			AllowInsecureTokenURL: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	stream, err := client.ChatCompletionStream(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var assembled strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		assembled.WriteString(chunk.ContentDelta)
	}

	if capturedAuth != "Bearer stream-token" {
		t.Errorf("Authorization = %q, want %q", capturedAuth, "Bearer stream-token")
	}
	if got := assembled.String(); got != "Hello from stream" {
		t.Errorf("assembled content = %q, want %q", got, "Hello from stream")
	}
}

func TestOAuth2_TokenCacheHit(t *testing.T) {
	tokenServer, counter := mockTokenServerWithCounter(t, "cached-token", 3600)
	defer tokenServer.Close()

	llmServer := mockOpenAIServer(t, nonStreamHandler("ok", "stop"))
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
		OAuth2: &oauth2cc.Config{
			TokenURL:              tokenServer.URL,
			ClientID:              "test-cid",
			ClientSecret:          "test-csecret",
			AllowInsecureTokenURL: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	for i := range 3 {
		if _, err := client.ChatCompletion(ctx, &ChatRequest{
			Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if got := counter.Load(); got != 1 {
		t.Errorf("token server call count = %d, want 1 (cache miss on repeat)", got)
	}
}

func TestOAuth2_InvalidateTriggersRefetch(t *testing.T) {
	tokenServer, counter := mockTokenServerWithCounter(t, "inv-token", 3600)
	defer tokenServer.Close()

	llmServer := mockOpenAIServer(t, nonStreamHandler("ok", "stop"))
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
		OAuth2: &oauth2cc.Config{
			TokenURL:              tokenServer.URL,
			ClientID:              "test-cid",
			ClientSecret:          "test-csecret",
			AllowInsecureTokenURL: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	call := func(step string) {
		if _, err := client.ChatCompletion(ctx, &ChatRequest{
			Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}

	call("first")
	if got := counter.Load(); got != 1 {
		t.Fatalf("after first call, counter = %d, want 1", got)
	}

	call("second (cache hit)")
	if got := counter.Load(); got != 1 {
		t.Fatalf("after second call, counter = %d, want 1 (cache hit expected)", got)
	}

	client.tokenMgr.Invalidate()

	call("third (after invalidate)")
	if got := counter.Load(); got != 2 {
		t.Fatalf("after third call, counter = %d, want 2 (refetch expected)", got)
	}
}

func TestOAuth2_LLM401InvalidatesToken_NextCallFetchesFresh(t *testing.T) {
	var tokenCalls atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		n := tokenCalls.Add(1)
		tok := "token-v1"
		if n >= 2 {
			tok = "token-v2"
		}
		resp := map[string]any{
			"access_token": tok,
			"expires_in":   3600,
			"token_type":   "bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer tokenServer.Close()

	var (
		llmCalls atomic.Int64
		mu       sync.Mutex
		authLog  []string
	)
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authLog = append(authLog, r.Header.Get("Authorization"))
		mu.Unlock()
		n := llmCalls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"token expired","type":"auth_error"}}`))
			return
		}
		nonStreamHandler("ok", "stop")(w, r)
	})
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
		OAuth2: &oauth2cc.Config{
			TokenURL:              tokenServer.URL,
			ClientID:              "test-cid",
			ClientSecret:          "test-csecret",
			AllowInsecureTokenURL: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	if _, err := client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}); err == nil {
		t.Fatal("first call: expected error on LLM 401")
	}

	if _, err := client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}

	if got := tokenCalls.Load(); got != 2 {
		t.Errorf("token server call count = %d, want 2 (refetch after invalidate)", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(authLog) != 2 {
		t.Fatalf("LLM request count = %d, want 2", len(authLog))
	}
	if authLog[0] != "Bearer token-v1" {
		t.Errorf("LLM call 1 Authorization = %q, want %q", authLog[0], "Bearer token-v1")
	}
	if authLog[1] != "Bearer token-v2" {
		t.Errorf("LLM call 2 Authorization = %q, want %q", authLog[1], "Bearer token-v2")
	}
}

func TestOAuth2_TLSCustomCA(t *testing.T) {
	tokenServer, caPath := mockTLSTokenServer(t, "tls-ca-token", 3600)
	defer tokenServer.Close()

	var capturedAuth string
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		nonStreamHandler("ok", "stop")(w, r)
	})
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
		OAuth2: &oauth2cc.Config{
			TokenURL:     tokenServer.URL,
			ClientID:     "test-cid",
			ClientSecret: "test-csecret",
			CACertPath:   caPath,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	if _, err := client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("chat completion: %v", err)
	}

	if capturedAuth != "Bearer tls-ca-token" {
		t.Errorf("Authorization = %q, want %q", capturedAuth, "Bearer tls-ca-token")
	}
}

func TestOAuth2_TLSInsecureSkipVerify(t *testing.T) {
	tokenServer, _ := mockTLSTokenServer(t, "skip-verify-token", 3600)
	defer tokenServer.Close()

	var capturedAuth string
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		nonStreamHandler("ok", "stop")(w, r)
	})
	defer llmServer.Close()

	t.Setenv("OPENAI_API_KEY", "")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  llmServer.URL,
		OAuth2: &oauth2cc.Config{
			TokenURL:           tokenServer.URL,
			ClientID:           "test-cid",
			ClientSecret:       "test-csecret",
			InsecureSkipVerify: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	if _, err := client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("chat completion: %v", err)
	}

	if capturedAuth != "Bearer skip-verify-token" {
		t.Errorf("Authorization = %q, want %q", capturedAuth, "Bearer skip-verify-token")
	}
}
