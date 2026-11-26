// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

var modelPreferences = map[string][]string{
	"openai": {
		"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		"gpt-5.5", "gpt-5.4", "gpt-5.1", "gpt-4.1",
		"gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "gpt-4", "gpt-3.5-turbo",
	},
	"anthropic": {
		"claude-sonnet-5", "claude-opus-5",
		"claude-opus-4-8", "claude-sonnet-4-6", "claude-opus-4-6",
		"claude-haiku-4-5-20251001", "claude-sonnet-4-5-20250929",
		"claude-sonnet-4-20250514", "claude-opus-4-20250514",
		"claude-3-haiku-20240307",
	},
	"google": {
		"gemini-3.6-flash", "gemini-3.5-flash", "gemini-3.5-flash-lite",
		"gemini-3.1-pro-preview", "gemini-3.1-flash-lite", "gemini-3-flash-preview",
		"gemini-2.5-flash", "gemini-2.0-flash",
		"gemini-1.5-flash", "gemini-1.5-pro",
	},
	"groq": {
		"llama-3.3-70b-versatile", "llama-3.1-70b-versatile",
		"mixtral-8x7b-32768",
	},
	"deepseek": {
		"deepseek-chat", "deepseek-coder",
	},
	"mistral": {
		"mistral-large-latest", "mistral-medium-latest", "mistral-small-latest",
	},
}

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

func ListModels(ctx context.Context, baseURL, apiKey, provider string) ([]string, error) {
	if baseURL == "" {
		return nil, nil
	}

	switch provider {
	case "anthropic":
		return listModelsAnthropic(ctx, baseURL, apiKey)
	case "google":
		return listModelsGoogle(ctx, baseURL, apiKey)
	default:
		return listModelsOpenAI(ctx, baseURL, apiKey, provider)
	}
}

func listModelsOpenAI(ctx context.Context, baseURL, apiKey, provider string) ([]string, error) {
	pageSeparator := "?"
	var endpoint string
	if _, ok := llm.ChatCompletionsEndpoint(baseURL); ok && provider == "openai" {
		endpoint = llm.OpenAIModelsURL(baseURL)
		if strings.Contains(endpoint, "?") {
			pageSeparator = "&"
		}
	} else {
		if llm.UsesOpenAIV1BaseURL(provider) {
			baseURL = llm.TrimTrailingV1(baseURL)
		}
		endpoint = strings.TrimRight(baseURL, "/") + "/v1/models"
	}
	client := &http.Client{Timeout: 15 * time.Second}

	var allModels []string
	afterCursor := ""

	for {
		reqURL := endpoint
		if afterCursor != "" {
			reqURL += pageSeparator + "after=" + afterCursor
		}

		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		if provider == "azure" {
			req.Header.Set("api-key", apiKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, nil
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, nil
		}

		var body openAIModelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			_ = resp.Body.Close()
			return nil, nil
		}
		_ = resp.Body.Close()

		for _, m := range body.Data {
			if m.ID != "" {
				allModels = append(allModels, m.ID)
			}
		}

		if !body.HasMore || body.LastID == "" {
			break
		}
		afterCursor = body.LastID
	}

	return allModels, nil
}

func listModelsAnthropic(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	endpoint := strings.TrimRight(llm.TrimTrailingV1(baseURL), "/") + "/v1/models"
	client := &http.Client{Timeout: 15 * time.Second}

	var allModels []string
	afterCursor := ""

	for {
		reqURL := endpoint
		if afterCursor != "" {
			reqURL += "?after_id=" + afterCursor
		}

		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := client.Do(req)
		if err != nil {
			return nil, nil
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, nil
		}

		var body openAIModelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			_ = resp.Body.Close()
			return nil, nil
		}
		_ = resp.Body.Close()

		for _, m := range body.Data {
			if m.ID != "" {
				allModels = append(allModels, m.ID)
			}
		}

		if !body.HasMore || body.LastID == "" {
			break
		}
		afterCursor = body.LastID
	}

	return allModels, nil
}

type googleModelsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
	NextPageToken string `json:"nextPageToken"`
}

func listModelsGoogle(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	base := strings.TrimRight(baseURL, "/")
	client := &http.Client{Timeout: 15 * time.Second}

	var allModels []string
	pageToken := ""

	for {
		reqURL := base + "/v1beta/models?key=" + apiKey + "&pageSize=100"
		if pageToken != "" {
			reqURL += "&pageToken=" + pageToken
		}

		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, nil
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, nil
		}

		var body googleModelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			_ = resp.Body.Close()
			return nil, nil
		}
		_ = resp.Body.Close()

		for _, m := range body.Models {
			name := strings.TrimPrefix(m.Name, "models/")
			if name != "" {
				allModels = append(allModels, name)
			}
		}

		if body.NextPageToken == "" {
			break
		}
		pageToken = body.NextPageToken
	}

	return allModels, nil
}

func RankModels(models []string, provider string) []string {
	prefs := modelPreferences[provider]
	prefRank := make(map[string]int, len(prefs))
	for i, p := range prefs {
		prefRank[p] = i
	}

	sort.SliceStable(models, func(i, j int) bool {
		ri, oki := prefRank[models[i]]
		rj, okj := prefRank[models[j]]
		if oki && okj {
			return ri < rj
		}
		if oki {
			return true
		}
		if okj {
			return false
		}
		for _, p := range prefs {
			ci := strings.Contains(models[i], p)
			cj := strings.Contains(models[j], p)
			if ci && !cj {
				return true
			}
			if cj && !ci {
				return false
			}
		}
		return models[i] < models[j]
	})
	return models
}

func TestConnection(ctx context.Context, baseURL, apiKey, provider, model string) error {
	if baseURL == "" {
		return fmt.Errorf("base_url is required for provider %q", provider)
	}

	envVar := "SAM_ONBOARD_TEST_KEY"
	_ = os.Setenv(envVar, apiKey)
	defer func() { _ = os.Unsetenv(envVar) }()

	cfg := llm.BifrostConfig{
		Provider:     provider,
		Model:        model,
		BaseURL:      baseURL,
		APIKeyEnvVar: envVar,
		Logger:       samlog.NewStderrLogger(slog.LevelDebug),
	}

	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client, err := llm.NewBifrostClient(testCtx, cfg)
	if err != nil {
		mapped := friendlyError(err)
		slog.Default().With("logger_name", "settings").Warn("LLM connection test failed",
			"provider", provider, "model", model, "stage", "client_init", "error", mapped)
		return mapped
	}
	defer client.Shutdown()

	_, err = client.ChatCompletion(testCtx, &llm.ChatRequest{
		Model:     model,
		Messages:  []llm.ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 1,
	})
	if err != nil {
		mapped := friendlyError(err)
		slog.Default().With("logger_name", "settings").Warn("LLM connection test failed",
			"provider", provider, "model", model, "stage", "chat_completion", "error", mapped)
		return mapped
	}

	return nil
}

func friendlyError(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()

	if llmErr, ok := errors.AsType[*llm.Error](err); ok {
		switch {
		case llmErr.StatusCode == 401:
			return fmt.Errorf("invalid API key — please check that your key is correct and active")
		case llmErr.StatusCode == 403:
			return fmt.Errorf("access denied — your API key may not have permission for this model")
		case llmErr.StatusCode == 404:
			return fmt.Errorf("model not found — try a different model name")
		case llmErr.StatusCode == 429:
			return fmt.Errorf("rate limited — please wait a moment and try again")
		case llmErr.StatusCode >= 500:
			return fmt.Errorf("server error from provider — try again in a moment")
		default:
			return fmt.Errorf("API error: %s", llmErr.Message)
		}
	}

	if _, ok := errors.AsType[*net.OpError](err); ok {
		return fmt.Errorf("can't reach the API endpoint — check your network and base URL")
	}

	if strings.Contains(msg, "connection refused") {
		return fmt.Errorf("connection refused — verify the base URL is correct")
	}
	if strings.Contains(msg, "timeout") || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("connection timed out — if using a proxy, verify the URL is correct")
	}

	return fmt.Errorf("connection test failed: %s", msg)
}
