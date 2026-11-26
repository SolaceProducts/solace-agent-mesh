// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveThroughGate(s *Service, method, path string) *httptest.ResponseRecorder {
	return serveRequestThroughGate(s, httptest.NewRequest(method, path, nil))
}

func serveRequestThroughGate(s *Service, r *http.Request) *httptest.ResponseRecorder {
	served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rec := httptest.NewRecorder()
	s.Gate(served).ServeHTTP(rec, r)
	return rec
}

func TestTheGateRefusesTheAPIUntilActivated(t *testing.T) {
	s := newService(t, t.TempDir(), newFakeSolaceID().signsInAs(ada))

	for _, path := range []string{
		"/api/v1/sessions",
		"/api/v1/platform/agents",
		"/api/v2/platform/agents",
		"/api/v1/platform/models",
		"/api/v2/platform/models",
		"/api/v1/notify/channels",
		"/api/v1/productAnalytics/ingest/cli",
		"/api/v1/activationX",
		"/api/v1/config/x",
		"/api/v2/platform/modelStatus/x",
		"/gw/mcp-1/mcp",
		"/gw",
	} {
		rec := serveThroughGate(s, http.MethodGet, path)
		require.Equal(t, http.StatusForbidden, rec.Code, path)
		var body map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, ErrorTypeActivationRequired, body["errorType"], path)
		assert.NotEmpty(t, body["errorId"], path)
	}

}

func TestTheGateStaysClosedUntilTheEULAIsAccepted(t *testing.T) {
	s := newService(t, t.TempDir(), newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, serveThroughGate(s, http.MethodGet, "/api/v1/sessions").Code)
}

func TestTheGateStaysClosedForAnotherAccountsAcceptance(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, NewStore(home).Save(Record{
		Account: &Account{Subject: ada.Subject},
		EULA:    &Acceptance{Subject: "auth0|someone-else"},
	}))
	s := newService(t, home, nil)

	assert.Equal(t, http.StatusForbidden, serveThroughGate(s, http.MethodGet, "/api/v1/sessions").Code)
}

func TestTheGateServesWhatTheSignInNeeds(t *testing.T) {
	s := newService(t, t.TempDir(), nil)

	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/assets/index.js"},
		{http.MethodGet, "/health"},
		{http.MethodGet, "/.well-known/oauth-protected-resource/gw/mcp-1"},
		{http.MethodGet, "/api/v1/config"},
		{http.MethodGet, "/api/v1/config/"},
		{http.MethodGet, "/api/v1/config/features"},
		{http.MethodGet, "/api/v1/config/capabilities"},
		{http.MethodGet, "/api/v1/csrf-token"},
		{http.MethodGet, "/api/v2/chat/config"},
		{http.MethodGet, "/api/v2/chat/config/login"},
		{http.MethodGet, "/api/v1/version"},
		{http.MethodGet, "/api/v2/platform/modelStatus"},
		{http.MethodPost, "/api/v1/productAnalytics/ingest"},
		{http.MethodGet, "/api/v1/productAnalytics/consent"},
		{http.MethodGet, "/api/v1/activation"},
		{http.MethodGet, "/api/v1/activation/terms"},
		{http.MethodPost, "/api/v1/activation:signIn"},
		{http.MethodPost, "/api/v1/activation:accept"},
	} {
		assert.Equal(t, http.StatusTeapot, serveThroughGate(s, req.method, req.path).Code, req.path)
	}
}

func TestTheGateOpensOnceActivated(t *testing.T) {
	s := newService(t, t.TempDir(), newFakeSolaceID().signsInAs(ada))
	activate(t, s)

	assert.Equal(t, http.StatusTeapot, serveThroughGate(s, http.MethodGet, "/api/v1/sessions").Code)
	assert.Equal(t, http.StatusTeapot, serveThroughGate(s, http.MethodPost, "/gw/mcp-1/mcp").Code)
}

func TestTheGateLetsAPreflightThrough(t *testing.T) {
	s := newService(t, t.TempDir(), nil)

	for _, path := range []string{"/api/v1/sessions", "/api/v2/platform/agents", "/gw/mcp-1/mcp"} {
		preflight := httptest.NewRequest(http.MethodOptions, path, nil)
		preflight.Header.Set("Origin", "http://localhost:5173")
		preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
		assert.Equal(t, http.StatusTeapot, serveRequestThroughGate(s, preflight).Code, path)
	}
}

func TestTheGateRefusesAnOptionsRequestThatIsNotAPreflight(t *testing.T) {
	s := newService(t, t.TempDir(), nil)

	for _, path := range []string{"/api/v1/sessions", "/gw/mcp-1/mcp"} {
		assert.Equal(t, http.StatusForbidden, serveThroughGate(s, http.MethodOptions, path).Code, path)
	}
}
