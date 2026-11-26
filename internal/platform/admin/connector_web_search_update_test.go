// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const wrongKeyBody = `{"detail":{"error":"Unauthorized: missing or invalid API key."}}`

func createWebSearchConnector(t *testing.T, svc *ConnectorService, name string, values map[string]any) string {
	t.Helper()
	created, err := createWebSearchThroughService(t, svc, name, "tavily", values)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return created.ID
}

func updateWebSearchConnector(t *testing.T, svc *ConnectorService, id string, values map[string]any) (*ConnectorResponse, error) {
	t.Helper()
	return svc.UpdateWithRedeploy(t.Context(), id, UpdateConnectorRequest{Values: values}, "tester", ConnectorUpdateOptions{})
}

func TestWebSearchConnectorUpdate_NeverSendsTheStoredKey(t *testing.T) {
	const storedKey = "tvly-stored-key"
	keyOmitted := map[string]any{}
	keyNull := map[string]any{"api_key": nil}
	keyRedacted := map[string]any{"api_key": redactedSentinel}
	for name, keyField := range map[string]map[string]any{
		"the key omitted": keyOmitted, "the key null": keyNull, "the key as the redacted placeholder": keyRedacted,
	} {
		t.Run(name, func(t *testing.T) {
			original := newFakeSearchAPI(t, http.StatusOK, searchResultsBody)
			allowProbeTo(t, original.server.URL)
			svc := newWebSearchServiceOnly(t)
			id := createWebSearchConnector(t, svc, "web-move", map[string]any{"api_key": storedKey, "api_base": original.server.URL})
			other := newFakeSearchAPI(t, http.StatusOK, searchResultsBody)
			allowProbeTo(t, other.server.URL)

			values := map[string]any{"api_base": other.server.URL}
			for k, v := range keyField {
				values[k] = v
			}
			_, err := updateWebSearchConnector(t, svc, id, values)
			if verr, refused := errors.AsType[*ValidationError](err); !refused || verr.Field != "values.api_key" {
				t.Fatalf("update = %v, want a values.api_key refusal for moving the stored key", err)
			}
			if got := storedWebSearchValue(t, svc, id, "api_base"); got != original.server.URL {
				t.Fatalf("stored api_base = %v after a refused move, want the original", got)
			}
			if keys := other.receivedKeys(); len(keys) != 0 {
				t.Fatalf("the new api_base received keys %v from an update that carried none", keys)
			}

			if _, err := updateWebSearchConnector(t, svc, id, map[string]any{"api_base": other.server.URL, "api_key": "tvly-fresh-key"}); err != nil {
				t.Fatalf("update with a fresh key: %v", err)
			}
			if keys := other.receivedKeys(); len(keys) != 1 || keys[0] != "tvly-fresh-key" {
				t.Errorf("the new api_base received %v, want only the fresh key (the server is reachable, so the silence above was the gate)", keys)
			}
		})
	}
}

func TestWebSearchConnectorUpdate_AnotherFieldSavesWithoutCallingTheProvider(t *testing.T) {
	api := newFakeSearchAPI(t, http.StatusOK, searchResultsBody)
	allowProbeTo(t, api.server.URL)
	svc := newWebSearchServiceOnly(t)
	id := createWebSearchConnector(t, svc, "web-revoked", map[string]any{"api_key": "tvly-stored-key", "api_base": api.server.URL})
	api.respondWith(http.StatusUnauthorized, wrongKeyBody)
	sentBeforeUpdate := len(api.receivedKeys())

	if _, err := updateWebSearchConnector(t, svc, id, map[string]any{"api_base": api.server.URL, "max_results": 3}); err != nil {
		t.Fatalf("update: %v, want it saved though the stored key is now rejected", err)
	}
	if sent := len(api.receivedKeys()); sent != sentBeforeUpdate {
		t.Errorf("the update called the provider %d times", sent-sentBeforeUpdate)
	}
	if got, _ := getConnectorThroughService(t, svc, id).Values.(map[string]any); got["max_results"] != float64(3) {
		t.Errorf("max_results = %v, want 3", got["max_results"])
	}
}

func TestWebSearchConnectorUpdate_ChecksAFreshKey(t *testing.T) {
	t.Run("a wrong key is refused", func(t *testing.T) {
		api := newFakeSearchAPI(t, http.StatusOK, searchResultsBody)
		allowProbeTo(t, api.server.URL)
		svc := newWebSearchServiceOnly(t)
		id := createWebSearchConnector(t, svc, "web-wrong", map[string]any{"api_key": "tvly-stored-key", "api_base": api.server.URL})
		api.respondWith(http.StatusUnauthorized, wrongKeyBody)

		_, err := updateWebSearchConnector(t, svc, id, map[string]any{"api_base": api.server.URL, "api_key": "tvly-wrong-key"})

		if verr, ok := errors.AsType[*ValidationError](err); !ok || !strings.Contains(verr.Message, "rejected the API key") {
			t.Fatalf("update err = %v, want a ValidationError (the /api/v2 422) saying the key was rejected", err)
		}
		if keys := api.receivedKeys(); keys[len(keys)-1] != "tvly-wrong-key" {
			t.Errorf("the check sent %v, want it to try the fresh key", keys)
		}
		_, err = svc.Update(t.Context(), id, UpdateConnectorRequest{Values: map[string]any{"api_base": api.server.URL, "api_key": "tvly-wrong-key"}}, "user")
		if verr, ok := errors.AsType[*ValidationError](err); !ok || verr.Field != "values.api_key" {
			t.Errorf("err = %v, want a ValidationError on values.api_key", err)
		}
	})

	t.Run("a valid key saves", func(t *testing.T) {
		api := newFakeSearchAPI(t, http.StatusOK, searchResultsBody)
		allowProbeTo(t, api.server.URL)
		svc := newWebSearchServiceOnly(t)
		id := createWebSearchConnector(t, svc, "web-valid", map[string]any{"api_key": "tvly-stored-key", "api_base": api.server.URL})

		if _, err := updateWebSearchConnector(t, svc, id, map[string]any{"api_base": api.server.URL, "api_key": "tvly-new-key"}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if keys := api.receivedKeys(); keys[len(keys)-1] != "tvly-new-key" {
			t.Errorf("the check sent %v, want it to try the fresh key", keys)
		}
	})
}

func TestWebSearchConnector_AcceptsAToolNameTheRuntimeServes(t *testing.T) {
	svc := newWebSearchServiceOnly(t)

	if _, err := createWebSearchThroughService(t, svc, "web-news", "tavily", map[string]any{"api_key": "<<__SAM_REQUIRED__>>", "tool_name": "web_search_news"}); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestWebSearchConnector_TheKeyNeverReachesTheLogs(t *testing.T) {
	const key = "tvly-canary-key-0123456789"
	echoed := func(text string) string { return fmt.Sprintf(`{"detail":{"error":"%s for %s"}}`, text, key) }
	closed := func() string {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := "http://" + listener.Addr().String()
		_ = listener.Close()
		return addr
	}
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"a rejected key", http.StatusUnauthorized, echoed("invalid key")},
		{"a rate limit", http.StatusTooManyRequests, echoed("slow down")},
		{"a refused request", http.StatusForbidden, echoed("forbidden")},
		{"an answer that is not results", http.StatusOK, echoed("hello")},
		{"an unreachable provider", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs syncBuffer
			svc, _ := newWebSearchService(t, slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			base := closed()
			if tc.status != 0 {
				api := newFakeSearchAPI(t, tc.status, tc.body)
				base = api.server.URL
			}
			allowProbeTo(t, base)

			resp, err := createWebSearchThroughService(t, svc, "web-logs", "tavily", map[string]any{"api_key": key, "api_base": base})

			if err != nil {
				_ = requireRefusal(t, err)
			}
			outcome := outcomeText(t, resp, err)
			if !strings.Contains(logs.String(), "web search connector probe") {
				t.Fatalf("the outcome logged nothing, so the check below proves nothing: %q", logs.String())
			}
			if strings.Contains(logs.String(), key) {
				t.Errorf("the key reached the logs: %s", logs.String())
			}
			if strings.Contains(outcome, key) {
				t.Errorf("the key reached the response: %s", outcome)
			}
		})
	}
}

func TestWebSearchConnector_AProviderThatHangsIsAWarning(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	allowProbeTo(t, server.URL)
	previous := webSearchProbeTimeout
	webSearchProbeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { webSearchProbeTimeout = previous })
	svc := newWebSearchServiceOnly(t)

	start := time.Now()
	resp, err := createWebSearchThroughService(t, svc, "web-hang", "tavily", map[string]any{"api_key": "tvly-hang-key", "api_base": server.URL})

	if err != nil || !strings.Contains(outcomeText(t, resp, err), "could not reach Tavily") {
		t.Fatalf("create = %s, want a save with a warning that Tavily could not be reached", outcomeText(t, resp, err))
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the save waited %s for a provider that never answered", elapsed)
	}
}
