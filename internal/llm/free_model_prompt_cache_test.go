// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type freeModelGrant struct{}

const freeModelGrantToken = "free-model-token"

func (freeModelGrant) GrantToken(context.Context, string, string) (GrantToken, error) {
	return GrantToken{AccessToken: freeModelGrantToken, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func newFreeModelClient(t *testing.T, gw *capturingGateway, model string, logger *slog.Logger) *BifrostClient {
	t.Helper()
	srv := httptest.NewServer(gw.handler())
	t.Cleanup(srv.Close)
	t.Setenv("OPENAI_API_KEY", "")
	client, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider: "openai",
		Model:    model,
		BaseURL:  srv.URL + "/v1",
		GrantID:  FreeModelGrantID,
		Grants:   freeModelGrant{},
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewBifrostClient: %v", err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

func TestFreeModelKeepsCacheMarkers(t *testing.T) {
	for _, model := range []string{"solace-free-general", "solace-free-planning", "any-alias-the-free-llm-picks"} {
		t.Run(model, func(t *testing.T) {
			gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 10, 0) }}
			client := newFreeModelClient(t, gw, model, nil)

			if _, err := client.ChatCompletion(t.Context(), markedRequest("")); err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}

			raw, auth := gw.raw(t, 0)
			msgs := gw.body(t, 0)["messages"].([]any)
			if !cacheControlOnLastBlock(msgs[0].(map[string]any)) {
				t.Errorf("system message lost its cache_control marker: %s", raw)
			}
			if got := gw.body(t, 0)["model"]; got != model {
				t.Errorf("model = %v, want %q untouched", got, model)
			}
			if auth != "Bearer "+freeModelGrantToken {
				t.Errorf("Authorization = %q, want the free model grant's token", auth)
			}
		})
	}
}

func TestFreeModelStreamKeepsCacheMarkers(t *testing.T) {
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) {
		streamHandler([]string{"ok"})(w, nil)
	}}
	client := newFreeModelClient(t, gw, "solace-free-general", nil)

	stream, err := client.ChatCompletionStream(t.Context(), markedRequest(""))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	for range stream {
	}

	raw, auth := gw.raw(t, 0)
	msgs := gw.body(t, 0)["messages"].([]any)
	if !cacheControlOnLastBlock(msgs[0].(map[string]any)) {
		t.Errorf("system message lost its cache_control marker: %s", raw)
	}
	if auth != "Bearer "+freeModelGrantToken {
		t.Errorf("Authorization = %q, want the free model grant's token", auth)
	}
}

func TestFreeModelCacheRouteNeedsTheFreeGrantOnTheOpenAIProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  BifrostConfig
		want bool
	}{
		{"free grant, openai provider", BifrostConfig{Provider: "openai", GrantID: FreeModelGrantID}, true},
		{"another grant", BifrostConfig{Provider: "openai", GrantID: "grant-1"}, false},
		{"no grant", BifrostConfig{Provider: "openai"}, false},
		{"free grant, another provider", BifrostConfig{Provider: "anthropic", GrantID: FreeModelGrantID}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFreeModelConfig(tc.cfg); got != tc.want {
				t.Fatalf("isFreeModelConfig = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFreeModelRouteIsOpenRouterWhateverTheModelName(t *testing.T) {
	client := &BifrostClient{cfg: BifrostConfig{Provider: "openai", GrantID: FreeModelGrantID}}
	for _, model := range []string{"solace-free-general", "gpt-5.6", "claude-sonnet-5", ""} {
		if got := client.wireProvider(model); got != schemas.OpenRouter {
			t.Errorf("wireProvider(%q) = %q, want %q", model, got, schemas.OpenRouter)
		}
	}
}

func TestFreeModelPromptCacheNeverServedWarns(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 5000, 0) }}
	client := newFreeModelClient(t, gw, "solace-free-general", logger)

	for i := 0; i < promptCacheMissesBeforeWarn+5; i++ {
		if _, err := client.ChatCompletion(t.Context(), markedRequest("")); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if n := strings.Count(logs.String(), neverServedMessage); n != 1 {
		t.Fatalf("warning logged %d times, want once:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "model=solace-free-general") {
		t.Errorf("warning lacks the free model's name:\n%s", logs.String())
	}
}
