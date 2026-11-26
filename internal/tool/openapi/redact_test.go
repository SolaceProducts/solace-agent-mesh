// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const querySecret = "sk-SUPERSECRET-must-not-appear"

func TestExecute_QuerySecretDoesNotReachToolResult(t *testing.T) {
	t.Parallel()
	tl := &OpenAPITool{
		name:         "get_pet",
		method:       "GET",
		pathTemplate: "/v1/pet",
		baseURL:      "http://127.0.0.1:1",
		auth: &AuthConfig{
			Type:       AuthTypeAPIKey,
			HeaderName: "apikey",
			APIKey:     querySecret,
			In:         "query",
		},
		manager: NewManager(slog.Default(), nil),
		logger:  slog.Default(),
	}

	res, err := tl.Execute(t.Context(), map[string]any{}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute returned a Go error rather than a tool result: %v", err)
	}
	body := resultText(t, res)
	if body == "" {
		t.Fatal("expected a failure message in the tool result")
	}
	if strings.Contains(body, querySecret) {
		t.Errorf("the resolved api key reached the model:\n%s", body)
	}
	if !strings.Contains(body, "127.0.0.1:1") {
		t.Errorf("the host should survive so the failure is diagnosable:\n%s", body)
	}
}

func resultText(t *testing.T, r *tool.Result) string {
	t.Helper()
	if r == nil {
		return ""
	}
	return fmt.Sprintf("%s %s %v", r.Status, r.Message, r.Data)
}

func TestExecute_ErrorResultWithNilErrorIsRedacted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, `{"error":"bad request to %s"}`, "http://"+r.Host+r.URL.String())
	}))
	defer srv.Close()

	tl := &OpenAPITool{
		name:         "get_pet",
		method:       "GET",
		pathTemplate: "/v1/pet",
		baseURL:      srv.URL,
		auth: &AuthConfig{
			Type:       AuthTypeAPIKey,
			HeaderName: "apikey",
			APIKey:     querySecret,
			In:         "query",
		},
		manager: NewManager(slog.Default(), nil),
		logger:  slog.Default(),
	}

	res, err := tl.Execute(t.Context(), map[string]any{}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute returned a Go error rather than a result: %v", err)
	}
	body := resultText(t, res)
	if !strings.Contains(body, "HTTP 500") {
		t.Fatalf("expected the non-2xx result, got:\n%s", body)
	}
	if strings.Contains(body, querySecret) {
		t.Errorf("the api key reached the model through an err==nil result:\n%s", body)
	}
}
