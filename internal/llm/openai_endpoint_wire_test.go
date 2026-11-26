// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type requestLine struct{ path, query string }

type lineRecorder struct {
	mu    sync.Mutex
	lines []requestLine
}

func (r *lineRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.lines = append(r.lines, requestLine{req.URL.Path, req.URL.RawQuery})
	r.mu.Unlock()
	body, _ := io.ReadAll(req.Body)
	if strings.Contains(string(body), `"stream":true`) {
		streamHandler([]string{"ok"})(w, req)
		return
	}
	nonStreamHandler("ok", "stop")(w, req)
}

func (r *lineRecorder) recorded() []requestLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]requestLine(nil), r.lines...)
}

func chatRequestLines(t *testing.T, provider, baseSuffix string) (chat, stream requestLine) {
	t.Helper()
	return modelChatRequestLines(t, provider, "m", baseSuffix)
}

func modelChatRequestLines(t *testing.T, provider, model, baseSuffix string) (chat, stream requestLine) {
	t.Helper()
	rec := &lineRecorder{}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	client, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider:   provider,
		Model:      model,
		BaseURL:    srv.URL + baseSuffix,
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	msgs := []ChatMessage{{Role: "user", Content: "hi"}}
	if _, err = client.ChatCompletion(t.Context(), &ChatRequest{Messages: msgs}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	ch, err := client.ChatCompletionStream(t.Context(), &ChatRequest{Messages: msgs})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	for range ch {
	}

	lines := rec.recorded()
	if len(lines) != 2 {
		t.Fatalf("got %d upstream requests, want 2: %+v", len(lines), lines)
	}
	return lines[0], lines[1]
}

func TestChatRequestLineForExistingBaseURLs(t *testing.T) {
	for _, tc := range []struct {
		name, provider, suffix string
		want                   requestLine
	}{
		{"bare host", "openai", "", requestLine{"/v1/chat/completions", ""}},
		{"trailing slash", "openai", "/", requestLine{"/v1/chat/completions", ""}},
		{"trailing v1", "openai", "/v1", requestLine{"/v1/chat/completions", ""}},
		{"trailing v1 slash", "openai", "/v1/", requestLine{"/v1/chat/completions", ""}},
		{"gateway prefix", "openai", "/openai", requestLine{"/openai/v1/chat/completions", ""}},
		{"other version prefix", "openai", "/api/v3", requestLine{"/api/v3/v1/chat/completions", ""}},
		{"query on the base", "openai", "/api/v3?k=v", requestLine{"/api/v3", "k=v/v1/chat/completions"}},
		{"chat path inside the base", "openai", "/chat/completions/proxy", requestLine{"/chat/completions/proxy/v1/chat/completions", ""}},
		{"v1 after a chat path", "openai", "/chat/completions/v1", requestLine{"/chat/completions/v1/chat/completions", ""}},
		{"full endpoint on another provider", "groq", "/api/v3/chat/completions", requestLine{"/api/v3/chat/completions/v1/chat/completions", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(defaultAPIKeyEnv(tc.provider), "sk-test")
			chat, stream := chatRequestLines(t, tc.provider, tc.suffix)
			if chat != tc.want {
				t.Errorf("chat request line = %+v, want %+v", chat, tc.want)
			}
			if stream != tc.want {
				t.Errorf("streamed chat request line = %+v, want %+v", stream, tc.want)
			}
		})
	}
}

func TestChatRequestLineForACompleteEndpoint(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	for _, tc := range []struct {
		name, suffix string
		want         requestLine
	}{
		{"endpoint", "/api/v3/chat/completions", requestLine{"/api/v3/chat/completions", ""}},
		{"trailing slash", "/api/v3/chat/completions/", requestLine{"/api/v3/chat/completions", ""}},
		{"query kept", "/api/v3/chat/completions?k=v", requestLine{"/api/v3/chat/completions", "k=v"}},
		{"at the root", "/chat/completions", requestLine{"/chat/completions", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chat, stream := chatRequestLines(t, "openai", tc.suffix)
			if chat != tc.want {
				t.Errorf("chat request line = %+v, want %+v", chat, tc.want)
			}
			if stream != tc.want {
				t.Errorf("streamed chat request line = %+v, want %+v", stream, tc.want)
			}
		})
	}
}

func TestClaudeBehindAGatewayRequestLine(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	for _, tc := range []struct {
		name, suffix string
		want         requestLine
	}{
		{"base URL", "", requestLine{"/v1/chat/completions", ""}},
		{"trailing v1", "/v1", requestLine{"/v1/chat/completions", ""}},
		{"other version prefix", "/api/v3", requestLine{"/api/v3/v1/chat/completions", ""}},
		{"complete endpoint", "/api/v3/chat/completions", requestLine{"/api/v3/chat/completions", ""}},
		{"complete endpoint with trailing slash", "/api/v3/chat/completions/", requestLine{"/api/v3/chat/completions", ""}},
		{"complete endpoint with query", "/api/v3/chat/completions?k=v", requestLine{"/api/v3/chat/completions", "k=v"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chat, stream := modelChatRequestLines(t, "openai", "claude-sonnet-4-6", tc.suffix)
			if chat != tc.want {
				t.Errorf("chat request line = %+v, want %+v", chat, tc.want)
			}
			if stream != tc.want {
				t.Errorf("streamed chat request line = %+v, want %+v", stream, tc.want)
			}
		})
	}
}

func TestCompleteEndpointStaysOnChatCompletions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider:   "openai",
		Model:      "gpt-5-pro",
		BaseURL:    srv.URL + "/api/v3/chat/completions",
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	_, err = client.ChatCompletion(t.Context(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("ChatCompletion succeeded, want the chat rejection surfaced")
	}
	fake.onlyCall(t, "/api/v3/chat/completions")
}
