// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestWarnIfProxiedAzureEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		baseURL  string
		wantWarn bool
	}{
		{
			name:     "proxy in front of azure warns",
			provider: "azure",
			baseURL:  "https://lite-llm.example.net",
			wantWarn: true,
		},
		{
			name:     "api management is a proxy, not a first-party host",
			provider: "azure",
			baseURL:  "https://contoso.azure-api.net",
			wantWarn: true,
		},
		{
			name:     "api management in china is also a proxy",
			provider: "azure",
			baseURL:  "https://contoso.azure-api.cn",
			wantWarn: true,
		},
		{
			name:     "21vianet cognitive services host stays quiet",
			provider: "azure",
			baseURL:  "https://contoso.cognitiveservices.azure.cn",
		},
		{
			name:     "first-party azure openai stays quiet",
			provider: "azure",
			baseURL:  "https://contoso.openai.azure.com",
		},
		{
			name:     "cognitive services host stays quiet",
			provider: "azure",
			baseURL:  "https://contoso.cognitiveservices.azure.com",
		},
		{
			name:     "microsoft foundry host stays quiet",
			provider: "azure",
			baseURL:  "https://contoso.services.ai.azure.com",
		},
		{
			name:     "azure government stays quiet",
			provider: "azure",
			baseURL:  "https://contoso.openai.azure.us",
		},
		{
			name:     "case and trailing dot do not defeat the check",
			provider: "azure",
			baseURL:  "https://CONTOSO.OpenAI.Azure.Com./",
		},
		{
			name:     "another provider through the same proxy is not this problem",
			provider: "openai",
			baseURL:  "https://lite-llm.example.net",
		},
		{
			name:     "no base url means nothing to judge",
			provider: "azure",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			warnIfProxiedAzureEndpoint(BifrostConfig{Provider: tc.provider, BaseURL: tc.baseURL}, logger)
			warned := strings.Contains(buf.String(), "not a first-party Azure host")
			if warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v (log: %s)", warned, tc.wantWarn, buf.String())
			}
		})
	}
}

func TestWarnIfProxiedAzureEndpointNilLogger(t *testing.T) {
	warnIfProxiedAzureEndpoint(BifrostConfig{
		Provider: "azure",
		BaseURL:  "https://lite-llm.example.net",
	}, nil)
}
