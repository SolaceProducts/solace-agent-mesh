// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newCallbackHandler(t *testing.T) *oauthCallbackHandler {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(t.Context()) })

	core := entrypoint.NewGatewayCore(NewHTTPSSEAdapter(), map[string]any{
		"namespace":  "test",
		"gateway_id": "gw",
	})
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())
	return &oauthCallbackHandler{core: core, logger: slog.Default()}
}

func TestOAuthCallback_QueryError_Browser(t *testing.T) {
	handler := newCallbackHandler(t)

	req := httptest.NewRequest("GET",
		"/api/v1/auth/tool/callback?error=invalid_scope&error_description=The+client+has+not+been+granted+permission&state=s1",
		nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html for a browser navigation", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "invalid_scope") {
		t.Errorf("HTML error page missing the OAuth error code: %s", body)
	}
	if !strings.Contains(body, "The client has not been granted permission") {
		t.Errorf("HTML error page missing the error description: %s", body)
	}
}

func TestOAuthCallback_QueryError_HTMLEscapesAuthServerValues(t *testing.T) {
	handler := newCallbackHandler(t)

	req := httptest.NewRequest("GET",
		"/api/v1/auth/tool/callback?error=invalid_scope&error_description=%3Cscript%3Ealert(1)%3C/script%3E&state=s1",
		nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	body := w.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("auth-server-controlled error_description rendered unescaped: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("expected html/template escaping of error_description; body=%s", body)
	}
}

func TestOAuthCallback_QueryError_JSON(t *testing.T) {
	handler := newCallbackHandler(t)

	req := httptest.NewRequest("GET",
		"/api/v1/auth/tool/callback?error=invalid_scope&error_description=nope&state=s1",
		nil)
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json for non-browser caller", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v (body=%s)", err, w.Body.String())
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "invalid_scope") {
		t.Errorf("message = %q, want it to carry the OAuth error", body["message"])
	}
	if body["errorType"] != string("invalid_token") {
		t.Errorf("errorType = %v, want invalid_token", body["errorType"])
	}
}

func TestOAuthCallback_FragmentBounce(t *testing.T) {
	handler := newCallbackHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/auth/tool/callback", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html bounce page", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "location.hash") {
		t.Errorf("bounce page must read the URL fragment: %s", body)
	}
}

func TestOAuthCallback_FragmentBounce_NavigateHeader(t *testing.T) {
	handler := newCallbackHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/auth/tool/callback", nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "location.hash") {
		t.Errorf("Sec-Fetch-Mode: navigate must serve the bounce page: %s", w.Body.String())
	}
}

func TestOAuthCallback_PostBounceEmpty(t *testing.T) {
	handler := newCallbackHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/auth/tool/callback?__fragbounce=1", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "location.hash") {
		t.Errorf("post-bounce re-entry must not serve the bounce page again: %s", w.Body.String())
	}
}

func TestOAuthCallback_CodeWithoutState_NoBounce(t *testing.T) {
	handler := newCallbackHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/auth/tool/callback?code=abc", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "location.hash") {
		t.Errorf("code-without-state must not serve the bounce page: %s", w.Body.String())
	}
}

func TestOAuthCallback_HTMLResponse_CacheHeaders(t *testing.T) {
	handler := newCallbackHandler(t)

	req := httptest.NewRequest("GET",
		"/api/v1/auth/tool/callback?error=invalid_scope&error_description=nope&state=s1", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	for k, want := range map[string]string{
		"Cache-Control":          "no-store",
		"Pragma":                 "no-cache",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
