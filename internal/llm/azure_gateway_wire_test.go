// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func captureRequestLine(t *testing.T, cfg BifrostConfig, model string) (string, string) {
	t.Helper()

	type line struct{ path, query string }
	lines := make(chan line, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case lines <- line{r.URL.Path, r.URL.RawQuery}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","model":"m",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},
			"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg.BaseURL = srv.URL
	cfg.Model = model
	cfg.MaxRetries = new(0)
	client, err := NewBifrostClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	if _, err := client.ChatCompletion(ctx, &ChatRequest{
		Model:     model,
		Messages:  []ChatMessage{{Role: "user", Content: "Hi"}},
		MaxTokens: 64,
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	select {
	case got := <-lines:
		return got.path, got.query
	case <-time.After(15 * time.Second):
		t.Fatal("no request reached the stand-in endpoint")
		return "", ""
	}
}

func TestAzureGatewayRestoresDeploymentRoute(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")

	path, query := captureRequestLine(t, BifrostConfig{Provider: "azure"}, "azure-gpt-5-mini")

	wantPath := "/openai/deployments/azure-gpt-5-mini/chat/completions"
	if path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}
	if query != "api-version="+classicAzureAPIVersion {
		t.Errorf("query = %q, want the api-version", query)
	}
}

func TestAzureGatewayHonoursConfiguredAPIVersion(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")

	_, query := captureRequestLine(t, BifrostConfig{
		Provider:       "azure",
		ProviderConfig: map[string]any{"api_version": "2025-01-01-preview"},
	}, "dep")

	if query != "api-version=2025-01-01-preview" {
		t.Errorf("query = %q, want the configured api-version", query)
	}
}

func TestAzureGatewayOptOutUsesCurrentRoute(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")

	path, query := captureRequestLine(t, BifrostConfig{
		Provider:       "azure",
		ProviderConfig: map[string]any{azureChatRouteKey: "v1"},
	}, "dep")

	if path != "/openai/v1/chat/completions" {
		t.Errorf("path = %q, want the current route", path)
	}
	if query != "" {
		t.Errorf("query = %q, want none on the current route", query)
	}
}

func TestNonAzureProviderRouteUnchanged(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")

	path, query := captureRequestLine(t, BifrostConfig{Provider: "openai"}, "gpt-4o")

	if path != "/v1/chat/completions" {
		t.Errorf("path = %q, want the openai route untouched", path)
	}
	if query != "" {
		t.Errorf("query = %q, want none", query)
	}
}

func TestAzureGatewayRouteSurvivesAModelOverride(t *testing.T) {
	type line struct{ path, query string }
	lines := make(chan line, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case lines <- line{r.URL.Path, r.URL.RawQuery}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","model":"m",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},
			"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Setenv("AZURE_API_KEY", "k")
	cfg := BifrostConfig{
		Provider:   "azure",
		Model:      "azure-gpt-5-mini",
		BaseURL:    srv.URL,
		MaxRetries: new(0),
	}
	client, err := NewBifrostClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	for _, tc := range []struct {
		name string
		ov   *BifrostConfig
	}{
		{"override names its provider", &BifrostConfig{
			Provider: "azure", Model: "azure-gpt-5-nano",
		}},
		{"override without a provider inherits the parent's rewritten provider", &BifrostConfig{
			Model: "azure-gpt-5-nano",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.ChatCompletion(ctx, &ChatRequest{
				Model:         "azure-gpt-5-mini",
				Messages:      []ChatMessage{{Role: "user", Content: "hi"}},
				ModelOverride: tc.ov,
			}); err != nil {
				t.Fatalf("override request: %v", err)
			}

			select {
			case got := <-lines:
				if !strings.HasPrefix(got.path, "/openai/deployments/") {
					t.Errorf("path = %q, want the deployment route: an override "+
						"must not lose it", got.path)
				}
				if !strings.Contains(got.path, "azure-gpt-5-nano") {
					t.Errorf("path = %q, want the override's deployment in it", got.path)
				}
			default:
				t.Fatal("no request reached the gateway")
			}
		})
	}
}
