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

func TestAzureEndpointFromProviderConfigIsSeen(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	warnIfProxiedAzureEndpoint(BifrostConfig{
		Provider:       "azure",
		ProviderConfig: map[string]any{"endpoint": "https://gateway.example.net"},
	}, logger)

	if !strings.Contains(buf.String(), "not a first-party Azure host") {
		t.Errorf("no warning for a gateway endpoint supplied via provider config: %q", buf.String())
	}
}

func TestAzureEndpointBaseURLWinsOverProviderConfig(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	warnIfProxiedAzureEndpoint(BifrostConfig{
		Provider:       "azure",
		BaseURL:        "https://contoso.openai.azure.com",
		ProviderConfig: map[string]any{"endpoint": "https://gateway.example.net"},
	}, logger)

	if !strings.Contains(buf.String(), "not a first-party Azure host") {
		t.Errorf("provider-config endpoint is the one addressed and must be judged: %q", buf.String())
	}
}

func TestAzureEndpointFirstPartyViaProviderConfigStaysQuiet(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	warnIfProxiedAzureEndpoint(BifrostConfig{
		Provider:       "azure",
		ProviderConfig: map[string]any{"endpoint": "https://contoso.openai.azure.com"},
	}, logger)

	if strings.Contains(buf.String(), "not a first-party Azure host") {
		t.Errorf("warned about a first-party host: %q", buf.String())
	}
}
