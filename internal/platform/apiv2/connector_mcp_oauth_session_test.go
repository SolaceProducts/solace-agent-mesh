// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const (
	mcpOAuthOwner             = "owner@test.local"
	mcpOAuthCallerEmail       = "person@example.com"
	mcpOAuthServerMismatchLog = "OAuth discovery refused a server URL other than the one the session authenticated against"
)

var (
	mcpOAuthWildcardPost  = regexp.MustCompile(`postMessage\([^)]*"\*"\)`)
	mcpOAuthOwnOriginPost = regexp.MustCompile(`postMessage\([^)]*location\.origin\)`)
)

func newMCPOAuthAPI(t *testing.T) (*apiClient, *entplatform.MCPDiscoveryService) {
	t.Helper()
	core, deps := newTestCore(t)
	return newAPIClient(t, core), deps.MCPSvc
}

func mustStoreOAuthState(t *testing.T, mcp *entplatform.MCPDiscoveryService, state string, st *entplatform.OAuthState) {
	t.Helper()
	if err := mcp.StoreOAuthState(t.Context(), state, st); err != nil {
		t.Fatalf("store OAuth state: %v", err)
	}
}

func mcpOAuthCallAs(t *testing.T, api *apiClient, userID, email, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := newJSONRequest(t, method, path, body)
	subject := auth.NewTestSubject(auth.SubjectFields{UserID: userID, Email: email, Authenticated: true, Scopes: []string{"*"}})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), subject))
	rec := httptest.NewRecorder()
	checkedAgainstSpec(t, api.core.Handler()).ServeHTTP(rec, req)
	return rec
}

func mcpOAuthStatusAs(t *testing.T, api *apiClient, userID, sessionID string) string {
	t.Helper()
	rec := mcpOAuthCallAs(t, api, userID, "", http.MethodGet, "/connectors/mcp/oauth/status?sessionId="+sessionID, nil)
	expectStatus(t, rec, http.StatusOK)
	return decodeData[MCPOAuthStatusResult](t, rec).Status
}

type mcpOAuthServerSpy struct {
	server *httptest.Server
	mu     sync.Mutex
	hits   int
	auth   string
}

func newMCPOAuthServerSpy(t *testing.T) *mcpOAuthServerSpy {
	t.Helper()
	spy := &mcpOAuthServerSpy{}
	spy.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spy.mu.Lock()
		spy.hits++
		spy.auth = r.Header.Get("Authorization")
		spy.mu.Unlock()
		http.Error(w, "not an MCP server", http.StatusInternalServerError)
	}))
	t.Cleanup(spy.server.Close)
	return spy
}

func (s *mcpOAuthServerSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

func (s *mcpOAuthServerSpy) authorization() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auth
}

func mcpOAuthColdDiscoveryCache(t *testing.T) {
	t.Helper()
	toolauth.DiscoveryCacheMu.Lock()
	defer toolauth.DiscoveryCacheMu.Unlock()
	toolauth.DiscoveryCache = make(map[string]*toolauth.OAuthMetadata)
}

func TestMCPOAuthCallback_AnExpiredStateIsRefusedAndForgotten(t *testing.T) {
	api, mcp := newMCPOAuthAPI(t)
	const state = "expiredstate"
	mustStoreOAuthState(t, mcp, state, &entplatform.OAuthState{
		SessionID: "sess1", OwnerUserID: mcpOAuthOwner, CodeVerifier: "verifier", CreatedAt: time.Now().Add(-time.Hour),
	})

	rec := api.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=abc&state="+state, nil)

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if got := decodeError(t, rec).Message; got != "state expired" {
		t.Errorf("message = %q, want %q", got, "state expired")
	}
	if got, err := mcp.LoadOAuthState(t.Context(), state); err != nil || got != nil {
		t.Errorf("the expired state is still stored: %+v, %v", got, err)
	}
}

func TestMCPOAuthCallback_PagesPostOnlyToTheirOwnOrigin(t *testing.T) {
	api, mcp := newMCPOAuthAPI(t)
	for name, state := range map[string]*entplatform.OAuthState{
		"authorization complete": {SessionID: "sess-complete", OwnerUserID: mcpOAuthOwner, CreatedAt: time.Now()},
		"token exchange failed": {SessionID: "sess-failed", OwnerUserID: mcpOAuthOwner, CreatedAt: time.Now(),
			TokenURL: "http://127.0.0.1:1", ClientID: "cid"},
	} {
		t.Run(name, func(t *testing.T) {
			mustStoreOAuthState(t, mcp, state.SessionID, state)

			rec := api.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=c&state="+state.SessionID, nil)

			expectStatus(t, rec, http.StatusOK)
			body := rec.Body.String()
			if mcpOAuthWildcardPost.MatchString(body) {
				t.Errorf("the page posts to any origin: %s", body)
			}
			if !mcpOAuthOwnOriginPost.MatchString(body) {
				t.Errorf("the page does not post to location.origin: %s", body)
			}
		})
	}
}

func TestMCPOAuthDiscoverWithToken_AnotherUsersSessionIsNotFound(t *testing.T) {
	api, mcp := newMCPOAuthAPI(t)
	mustStoreOAuthState(t, mcp, "statekey", &entplatform.OAuthState{
		SessionID: "shared-session", OwnerUserID: mcpOAuthOwner, AccessToken: "owner-access-token", ServerURL: "http://example.com/mcp", CreatedAt: time.Now(),
	})

	rec := mcpOAuthCallAs(t, api, "attacker@test.local", "", http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
		"sessionId": "shared-session", "serverUrl": "http://example.com/mcp", "connectionType": "streamable_http",
	})

	expectStatus(t, rec, http.StatusNotFound)
	if strings.Contains(rec.Body.String(), "owner-access-token") {
		t.Errorf("the response carries the session's token: %s", rec.Body.String())
	}
}

func TestMCPOAuthStatus_NeverRevealsAnotherUsersSession(t *testing.T) {
	api, mcp := newMCPOAuthAPI(t)
	mustStoreOAuthState(t, mcp, "completed-state", &entplatform.OAuthState{
		SessionID: "completed-session", OwnerUserID: mcpOAuthOwner, AccessToken: "tok", CreatedAt: time.Now(),
	})

	for _, tc := range []struct {
		name, asUser, sessionID, want string
	}{
		{"an unknown session reads as expired", mcpOAuthOwner, "ghost", "expired"},
		{"another user's completed session reads as expired", "intruder@test.local", "completed-session", "expired"},
		{"the owner reads it as completed", mcpOAuthOwner, "completed-session", "completed"},
		{"a second poll does not spend it", mcpOAuthOwner, "completed-session", "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpOAuthStatusAs(t, api, tc.asUser, tc.sessionID); got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMCPOAuthStatus_PollRacesTheCallbackSafely(t *testing.T) {
	api, mcp := newMCPOAuthAPI(t)
	const (
		state     = "race-state"
		sessionID = "race-session"
		rounds    = 500
	)
	mustStoreOAuthState(t, mcp, state, &entplatform.OAuthState{SessionID: sessionID, OwnerUserID: mcpOAuthOwner, CreatedAt: time.Now()})
	handler := api.core.Handler()
	owner := auth.NewTestSubject(auth.SubjectFields{UserID: mcpOAuthOwner, Authenticated: true, Scopes: []string{"*"}})

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		for range rounds {
			handler.ServeHTTP(httptest.NewRecorder(), newJSONRequest(t, http.MethodGet, "/connectors/mcp/oauth/callback?code=tok&state="+state, nil))
		}
	})
	wg.Go(func() {
		<-start
		for range rounds {
			req := newJSONRequest(t, http.MethodGet, "/connectors/mcp/oauth/status?sessionId="+sessionID, nil)
			handler.ServeHTTP(httptest.NewRecorder(), req.WithContext(auth.ContextWithSubject(req.Context(), owner)))
		}
	})
	close(start)
	wg.Wait()

	if got := mcpOAuthStatusAs(t, api, mcpOAuthOwner, sessionID); got != "completed" {
		t.Errorf("status after the callbacks = %q, want completed", got)
	}
}

func TestMCPOAuthDiscoverWithToken_TokenStaysWithItsServer(t *testing.T) {
	tests := []struct {
		name        string
		requestedAs func(session, attacker string) string
		wantRefused bool
	}{
		{name: "omitted uses the session server", requestedAs: func(string, string) string { return "" }},
		{name: "same URL", requestedAs: func(session, _ string) string { return session }},
		{name: "same URL with surrounding space", requestedAs: func(session, _ string) string { return " " + session + " " }},
		{name: "other server", requestedAs: func(_, attacker string) string { return attacker }, wantRefused: true},
		{name: "other path on the session server", requestedAs: func(session, _ string) string { return session + "/other" }, wantRefused: true},
		{name: "other scheme", requestedAs: func(session, _ string) string { return strings.Replace(session, "http://", "https://", 1) }, wantRefused: true},
		{name: "userinfo spelling of the session host", requestedAs: func(session, attacker string) string {
			return "http://" + strings.TrimPrefix(session, "http://") + "@" + strings.TrimPrefix(attacker, "http://")
		}, wantRefused: true},
		{name: "metadata address", requestedAs: func(string, string) string { return "http://169.254.169.254/latest" }, wantRefused: true},
	}
	api, mcp := newMCPOAuthAPI(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sessionHits, attackerHits := newMCPOAuthServerSpy(t), newMCPOAuthServerSpy(t)
			mustStoreOAuthState(t, mcp, "key", &entplatform.OAuthState{
				SessionID: "session", OwnerUserID: mcpOAuthOwner, AccessToken: "session-token",
				ServerURL: sessionHits.server.URL, ConnectionType: "sse", CreatedAt: time.Now(),
			})

			rec := mcpOAuthCallAs(t, api, mcpOAuthOwner, "", http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
				"sessionId": "session", "serverUrl": tc.requestedAs(sessionHits.server.URL, attackerHits.server.URL), "connectionType": "sse",
			})

			if attackerHits.count() != 0 {
				t.Fatalf("the other server received %d request(s)", attackerHits.count())
			}
			if tc.wantRefused {
				expectDetail(t, rec, http.StatusUnprocessableEntity, "serverUrl")
				if got := decodeError(t, rec).Message; !strings.Contains(got, "Authorize again") {
					t.Errorf("message = %q, want it to ask for a new authorization", got)
				}
				if sessionHits.count() != 0 {
					t.Fatalf("a refused request reached the session server %d time(s)", sessionHits.count())
				}
				return
			}
			if sessionHits.count() == 0 {
				t.Fatalf("the session server was never contacted (status %d, body %s)", rec.Code, rec.Body.String())
			}
			if got := sessionHits.authorization(); got != "Bearer session-token" {
				t.Errorf("the session server saw Authorization %q, want the session's token", got)
			}
		})
	}
}

func TestMCPOAuthDiscoverWithToken_ServerMismatchIsLoggedOnceWithoutURLsOrEmail(t *testing.T) {
	logs := captureLogs(t)
	api, mcp := newMCPOAuthAPI(t)
	mustStoreOAuthState(t, mcp, "key", &entplatform.OAuthState{
		SessionID: "session", OwnerUserID: mcpOAuthCallerEmail, AccessToken: "session-token",
		ServerURL: "https://mcp.session.example.com/mcp", CreatedAt: time.Now(),
	})

	rec := mcpOAuthCallAs(t, api, mcpOAuthCallerEmail, mcpOAuthCallerEmail, http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
		"sessionId": "session", "serverUrl": "https://mcp.other.example.net/mcp",
	})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	var found []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == mcpOAuthServerMismatchLog {
			found = append(found, entry)
		}
	}
	if len(found) != 1 {
		t.Fatalf("lines with message %q = %d, want 1; logs: %s", mcpOAuthServerMismatchLog, len(found), logs.String())
	}
	if found[0]["level"] != "WARN" || found[0]["changedField"] != "serverUrl" || found[0]["userFP"] != strutil.PIIFingerprint(mcpOAuthCallerEmail) {
		t.Errorf("line = %v, want WARN naming serverUrl and the caller's fingerprint", found[0])
	}
	if _, named := found[0]["userID"]; named {
		t.Errorf("line = %v, want no raw user id", found[0])
	}
	for _, secret := range []string{mcpOAuthCallerEmail, "mcp.session.example.com", "mcp.other.example.net", "session-token"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the logs contain %q", secret)
		}
	}
}

func TestMCPOAuthInitiate_DiscoveryFallsBackToTheOpenIDConfiguration(t *testing.T) {
	api, _ := newMCPOAuthAPI(t)

	t.Run("openid-configuration only", func(t *testing.T) {
		mcpOAuthColdDiscoveryCache(t)
		registration := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id": "test-client-id", "client_secret": "test-client-secret", "token_endpoint_auth_method": "client_secret_post",
			})
		}))
		t.Cleanup(registration.Close)
		var openIDHits atomic.Int32
		var providerURL string
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/.well-known/openid-configuration" {
				http.NotFound(w, r)
				return
			}
			openIDHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": providerURL, "authorization_endpoint": providerURL + "/authorize", "token_endpoint": providerURL + "/token",
				"registration_endpoint":                 registration.URL + "/register",
				"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
			})
		}))
		t.Cleanup(provider.Close)
		providerURL = provider.URL

		rec := api.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
			"serverUrl": provider.URL, "mode": "discovery", "scopes": "openid profile",
		})

		expectStatus(t, rec, http.StatusOK)
		if openIDHits.Load() == 0 {
			t.Error("the openid-configuration endpoint was never asked")
		}
		result := decodeData[MCPOAuthInitiateResult](t, rec)
		if !strings.HasPrefix(result.AuthorizationURL, provider.URL+"/authorize") {
			t.Errorf("authorizationUrl = %q, want prefix %q/authorize", result.AuthorizationURL, provider.URL)
		}
		authURL, err := url.Parse(result.AuthorizationURL)
		if err != nil {
			t.Fatalf("parse authorizationUrl %q: %v", result.AuthorizationURL, err)
		}
		if got := authURL.Query().Get("client_id"); got != "test-client-id" {
			t.Errorf("client_id = %q, want the registered test-client-id", got)
		}
		if got := authURL.Query().Get("code_challenge_method"); got != "S256" {
			t.Errorf("code_challenge_method = %q, want S256", got)
		}
	})

	t.Run("oauth-authorization-server is preferred when both exist", func(t *testing.T) {
		mcpOAuthColdDiscoveryCache(t)
		var oauthServerHits, openIDHits atomic.Int32
		var providerURL string
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			prefix := ""
			switch r.URL.Path {
			case "/.well-known/oauth-authorization-server":
				oauthServerHits.Add(1)
				prefix = "/rfc8414"
			case "/.well-known/openid-configuration":
				openIDHits.Add(1)
				prefix = "/oidc"
			default:
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": providerURL, "authorization_endpoint": providerURL + prefix + "/authorize", "token_endpoint": providerURL + prefix + "/token",
			})
		}))
		t.Cleanup(provider.Close)
		providerURL = provider.URL

		api.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{"serverUrl": provider.URL, "mode": "discovery"})

		if oauthServerHits.Load() == 0 {
			t.Error("the oauth-authorization-server endpoint was never asked")
		}
		if got := openIDHits.Load(); got != 0 {
			t.Errorf("the openid-configuration endpoint was asked %d time(s) after oauth-authorization-server answered", got)
		}
	})

	t.Run("a provider serving neither is a 502", func(t *testing.T) {
		mcpOAuthColdDiscoveryCache(t)
		provider := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(provider.Close)

		rec := api.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{"serverUrl": provider.URL, "mode": "discovery"})

		expectStatus(t, rec, http.StatusBadGateway)
		decodeError(t, rec)
	})
}

func TestMCPOAuth_AStoreOutageIsUnavailableAndTheSignInSurvives(t *testing.T) {
	api, mcp := newMCPOAuthAPI(t)
	const state, sessionID = "outage-state", "outage-session"
	mustStoreOAuthState(t, mcp, state, &entplatform.OAuthState{SessionID: sessionID, OwnerUserID: mcpOAuthOwner, CreatedAt: time.Now()})
	rename := func(from, to string) {
		t.Helper()
		if _, err := api.core.DB().ExecContext(t.Context(), "ALTER TABLE "+from+" RENAME TO "+to); err != nil {
			t.Fatalf("rename %s: %v", from, err)
		}
	}

	rename("mcp_connector_oauth_states", "mcp_connector_oauth_states_away")
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"initiate": mcpOAuthCallAs(t, api, mcpOAuthOwner, "", http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
			"serverUrl": "http://example.com/mcp", "mode": "manual",
			"authorizationUrl": "http://idp.example.com/auth", "tokenUrl": "http://idp.example.com/token", "clientId": "cid",
		}),
		"status":   mcpOAuthCallAs(t, api, mcpOAuthOwner, "", http.MethodGet, "/connectors/mcp/oauth/status?sessionId="+sessionID, nil),
		"callback": api.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=tok&state="+state, nil),
		"discoverWithToken": mcpOAuthCallAs(t, api, mcpOAuthOwner, "", http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
			"sessionId": sessionID, "serverUrl": "http://example.com/mcp", "connectionType": "streamable_http",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, rec, http.StatusServiceUnavailable)
			decodeError(t, rec)
		})
	}
	rename("mcp_connector_oauth_states_away", "mcp_connector_oauth_states")

	if got := mcpOAuthStatusAs(t, api, mcpOAuthOwner, sessionID); got != "pending" {
		t.Fatalf("status after the outage = %q, want pending", got)
	}
	rec := api.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=tok&state="+state, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := mcpOAuthStatusAs(t, api, mcpOAuthOwner, sessionID); got != "completed" {
		t.Errorf("status after the retried callback = %q, want completed", got)
	}
}
