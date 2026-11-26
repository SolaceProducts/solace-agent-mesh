// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const webSearchResults = `{"results":[{"title":"Agent Mesh","url":"https://example.com/","content":"An agent mesh.","highlights":["An agent mesh."]}]}`

func newSearchProvider(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func createWebSearchConnector(app *apiClient, name, subtype string, values map[string]any) *httptest.ResponseRecorder {
	return app.call(http.MethodPost, "/connectors", map[string]any{
		"name": name, "description": "Web search for the research agent.",
		"connectorType": "web_search", "connectorSubtype": subtype, "values": values,
	})
}

func TestConnectors_WebSearchCreatesForEachProviderAndNeverReadsTheKeyBack(t *testing.T) {
	t.Setenv("SAM_PLATFORM_ALLOW_PRIVATE_MCP", "true")
	app := newConnectorAPI(t)
	for _, subtype := range []string{"tavily", "exa"} {
		t.Run(subtype, func(t *testing.T) {
			provider := newSearchProvider(t, http.StatusOK, webSearchResults)

			rec := createWebSearchConnector(app, "web-"+subtype, subtype, map[string]any{"api_key": "key-" + subtype, "api_base": provider.URL})
			expectStatus(t, rec, http.StatusCreated)
			created := decodeData[Connector](t, rec)
			if len(created.Warnings) != 0 {
				t.Errorf("warnings = %v for a key the provider accepted", created.Warnings)
			}
			fetched := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+created.ID, nil))
			for read, c := range map[string]Connector{"create": created, "get": fetched} {
				if c.ConnectorType != "web_search" || c.ConnectorSubtype != subtype {
					t.Errorf("%s: type = %s/%s", read, c.ConnectorType, c.ConnectorSubtype)
				}
				if c.Values["api_key"] != redacted {
					t.Errorf("%s: api_key = %v, want %s", read, c.Values["api_key"], redacted)
				}
				if c.Values["api_base"] != provider.URL {
					t.Errorf("%s: api_base = %v, want it as stored", read, c.Values["api_base"])
				}
			}
		})
	}
}

func TestConnectors_WebSearchRefusesAKeyTheProviderRejects(t *testing.T) {
	t.Setenv("SAM_PLATFORM_ALLOW_PRIVATE_MCP", "true")
	app := newConnectorAPI(t)
	provider := newSearchProvider(t, http.StatusUnauthorized, `{"error":"Invalid API key"}`)

	rec := createWebSearchConnector(app, "web-bad-key", "exa", map[string]any{"api_key": "not-a-real-key", "api_base": provider.URL})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if body := rec.Body.String(); !strings.Contains(body, "Exa rejected the API key") || strings.Contains(body, "not-a-real-key") {
		t.Errorf("refusal = %s, want it to say Exa rejected the key without quoting it", body)
	}
}

func TestConnectors_WebSearchSavesARateLimitedKeyWithAWarning(t *testing.T) {
	t.Setenv("SAM_PLATFORM_ALLOW_PRIVATE_MCP", "true")
	app := newConnectorAPI(t)
	provider := newSearchProvider(t, http.StatusTooManyRequests, `{"detail":{"error":"slow down"}}`)

	rec := createWebSearchConnector(app, "web-busy", "tavily", map[string]any{"api_key": "busy-key", "api_base": provider.URL})

	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[Connector](t, rec)
	if len(created.Warnings) != 1 || !strings.Contains(created.Warnings[0], "rate limit") {
		t.Errorf("warnings = %v, want one about the rate limit", created.Warnings)
	}
}

func TestConnectors_WebSearchRefusesAToolNameTheAgentWouldNotRunInProcess(t *testing.T) {
	app := newConnectorAPI(t)

	rec := createWebSearchConnector(app, "web-renamed", "tavily", map[string]any{"api_key": "<<__SAM_REQUIRED__>>", "tool_name": "company_search"})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if !strings.Contains(rec.Body.String(), "web_search_") {
		t.Errorf("refusal = %s, want it to name the web_search_ prefix", rec.Body.String())
	}
}
