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

func TestAzureAPIVersionWarningMatchesWhatHappens(t *testing.T) {
	const ignoredWarning = "azure api_version is no longer configurable"

	tests := []struct {
		name        string
		baseURL     string
		wantIgnored bool
		wantRoute   bool
	}{
		{
			name:        "first-party host drops the value and says so",
			baseURL:     "https://contoso.openai.azure.com",
			wantIgnored: true,
		},
		{
			name:      "gateway honours the value and stays quiet about it",
			baseURL:   "https://lite-llm.example.net",
			wantRoute: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AZURE_API_KEY", "az-key")
			var buf bytes.Buffer
			client, err := NewBifrostClient(t.Context(), BifrostConfig{
				Provider:   "azure",
				Model:      "dep",
				BaseURL:    tc.baseURL,
				MaxRetries: new(0),
				Logger: slog.New(slog.NewTextHandler(&buf,
					&slog.HandlerOptions{Level: slog.LevelInfo})),
				ProviderConfig: map[string]any{"api_version": "2024-10-21"},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Shutdown)

			logged := buf.String()
			if got := strings.Contains(logged, ignoredWarning); got != tc.wantIgnored {
				t.Errorf("ignored-warning = %v, want %v: %s", got, tc.wantIgnored, logged)
			}
			if got := strings.Contains(logged, "using the deployment chat route"); got != tc.wantRoute {
				t.Errorf("route notice = %v, want %v: %s", got, tc.wantRoute, logged)
			}
			if got := client.azureGateway.active; got != tc.wantRoute {
				t.Errorf("route active = %v, want %v", got, tc.wantRoute)
			}
		})
	}
}
