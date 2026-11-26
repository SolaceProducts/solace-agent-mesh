// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

func asUser(t *testing.T, userID string) context.Context {
	subject := auth.NewTestSubject(auth.SubjectFields{UserID: userID, Authenticated: true})
	return auth.ContextWithSubject(t.Context(), subject)
}

func TestMCPOAuthSignInCompletesAcrossInstances(t *testing.T) {
	t.Setenv("SESSION_SECRET_KEY", "shared-secret")
	db := testOpenDB(t)
	first := NewMCPDiscoveryService(discardLogger(), db)
	second := NewMCPDiscoveryService(discardLogger(), db)
	ctx := asUser(t, "alice")

	resp, err := first.BeginOAuth(ctx, MCPOAuthInitiateRequest{
		ServerURL: "http://mcp.example.com", Mode: "manual",
		AuthorizationURL: "http://idp.example.com/auth", TokenURL: "", ClientID: "cid",
	}, "http://sam.example.com/callback")
	if err == nil || resp != nil {
		t.Fatalf("manual mode without token URL accepted: %+v, %v", resp, err)
	}

	resp, err = first.BeginOAuth(ctx, MCPOAuthInitiateRequest{
		ServerURL: "http://mcp.example.com", Mode: "manual",
		AuthorizationURL: "http://idp.example.com/auth", TokenURL: "http://idp.example.com/token", ClientID: "cid", ClientSecret: "client-secret",
	}, "http://sam.example.com/callback")
	if err != nil {
		t.Fatalf("BeginOAuth: %v", err)
	}

	var data, token string
	if scanErr := db.QueryRowContext(ctx, `SELECT data, access_token FROM mcp_connector_oauth_states`).Scan(&data, &token); scanErr != nil {
		t.Fatal(scanErr)
	}
	for _, secret := range []string{"client-secret", "http://mcp.example.com", resp.State} {
		if strings.Contains(data, secret) {
			t.Errorf("stored record carries %q in plaintext", secret)
		}
	}

	if got, findErr := second.FindOAuthStateBySession(ctx, resp.SessionID, "alice"); findErr != nil || got == nil || got.AccessToken != "" || got.ClientSecret != "client-secret" {
		t.Fatalf("poll on the second instance = %+v, %v", got, findErr)
	}

	if storeErr := second.StoreOAuthState(ctx, "plain-state", &OAuthState{SessionID: "plain-session", OwnerUserID: "alice", ServerURL: "http://127.0.0.1:1/mcp"}); storeErr != nil {
		t.Fatal(storeErr)
	}
	sessionID, err := first.CompleteOAuth(ctx, "the-access-token", "plain-state")
	if err != nil || sessionID != "plain-session" {
		t.Fatalf("CompleteOAuth = %q, %v", sessionID, err)
	}
	if again, err := second.CompleteOAuth(ctx, "other-code", "plain-state"); err != nil || again != "plain-session" {
		t.Fatalf("second callback = %q, %v", again, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT access_token FROM mcp_connector_oauth_states WHERE session_id = 'plain-session'`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token == "" || strings.Contains(token, "the-access-token") {
		t.Errorf("access token stored as %q", token)
	}
	got, findErr := second.FindOAuthStateBySession(ctx, "plain-session", "alice")
	if findErr != nil || got == nil || got.AccessToken != "the-access-token" {
		t.Fatalf("poll after callback = %+v, %v", got, findErr)
	}

	if _, err := second.DiscoverWithSessionToken(ctx, MCPOAuthDiscoverRequest{SessionID: "plain-session"}, "bob"); err == nil {
		t.Fatal("another user's session discovered")
	}
	if got, err := first.FindOAuthStateBySession(ctx, "plain-session", "alice"); err != nil || got != nil {
		t.Errorf("a session claimed by the wrong user is not spent: %+v, %v", got, err)
	}
}

func TestMCPOAuthStateFromAnotherSecretReadsAsAbsent(t *testing.T) {
	db := testOpenDB(t)
	t.Setenv("SESSION_SECRET_KEY", "old")
	writer := NewMCPDiscoveryService(discardLogger(), db)
	t.Setenv("SESSION_SECRET_KEY", "new")
	reader := NewMCPDiscoveryService(discardLogger(), db)
	ctx := t.Context()

	if err := writer.StoreOAuthState(ctx, "st", &OAuthState{SessionID: "s", OwnerUserID: "o", AccessToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	if got, err := reader.LoadOAuthState(ctx, "st"); err != nil || got != nil {
		t.Errorf("record sealed under another secret = %+v, %v; want absent", got, err)
	}
	if got, err := reader.FindOAuthStateBySession(ctx, "s", "o"); err != nil || got != nil {
		t.Errorf("session sealed under another secret = %+v, %v; want absent", got, err)
	}
	_, err := reader.CompleteOAuth(ctx, "c", "st")
	if refusal, ok := MCPOAuthRefusal(err); !ok || refusal.Status != http.StatusBadRequest {
		t.Errorf("callback on an unreadable record = %v; want the invalid state refusal", err)
	}
}

func TestMCPOAuthStateExpiredDuringExchangeFails(t *testing.T) {
	t.Setenv("SESSION_SECRET_KEY", "shared-secret")
	db := testOpenDB(t)
	svc := NewMCPDiscoveryService(discardLogger(), db)
	ctx := t.Context()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := db.ExecContext(r.Context(), `UPDATE mcp_connector_oauth_states SET expires_at = 0`); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer"}`))
	}))
	defer idp.Close()

	if err := svc.StoreOAuthState(ctx, "st", &OAuthState{SessionID: "s", OwnerUserID: "o", TokenURL: idp.URL, ClientID: "cid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteOAuth(ctx, "code", "st"); err == nil {
		t.Error("callback reported success for a sign-in that expired during the code exchange")
	}
}

func TestMCPOAuthWithoutStoreFailsClosed(t *testing.T) {
	svc := NewMCPDiscoveryService(discardLogger(), nil)
	ctx := t.Context()
	if err := svc.StoreOAuthState(ctx, "st", &OAuthState{}); !errors.Is(err, errOAuthStoreUnavailable) {
		t.Errorf("StoreOAuthState = %v", err)
	}
	if got, err := svc.LoadOAuthState(ctx, "st"); err != nil || got != nil {
		t.Errorf("LoadOAuthState = %+v, %v", got, err)
	}
	if got, err := svc.FindOAuthStateBySession(ctx, "s", "o"); err != nil || got != nil {
		t.Errorf("FindOAuthStateBySession = %+v, %v", got, err)
	}
	if got, err := svc.consumeOAuthStateBySession(ctx, "s"); err != nil || got != nil {
		t.Errorf("consumeOAuthStateBySession = %+v, %v", got, err)
	}
	if err := svc.LeaderJob().Run(ctx, leader.Term{}); err != nil {
		t.Errorf("leader job without store: %v", err)
	}
}

func TestMCPOAuthStoreErrorsAreUnavailable(t *testing.T) {
	db := testOpenDB(t)
	svc := NewMCPDiscoveryService(discardLogger(), db)
	ctx := asUser(t, "alice")
	_ = db.Close()
	_, err := svc.BeginOAuth(ctx, MCPOAuthInitiateRequest{
		Mode: "manual", AuthorizationURL: "http://idp/auth", TokenURL: "http://idp/token", ClientID: "c",
	}, "http://cb")
	expectUnavailable(t, "BeginOAuth", err)
	_, err = svc.LoadOAuthState(ctx, "st")
	expectUnavailable(t, "LoadOAuthState", err)
	_, err = svc.FindOAuthStateBySession(ctx, "s", "alice")
	expectUnavailable(t, "FindOAuthStateBySession", err)
	_, err = svc.consumeOAuthStateBySession(ctx, "s")
	expectUnavailable(t, "consumeOAuthStateBySession", err)
	_, err = svc.CompleteOAuth(ctx, "code", "st")
	expectUnavailable(t, "CompleteOAuth", err)
	_, err = svc.DiscoverWithSessionToken(ctx, MCPOAuthDiscoverRequest{SessionID: "s"}, "alice")
	expectUnavailable(t, "DiscoverWithSessionToken", err)
}

func TestMCPOAuthSignInSurvivesAStoreOutage(t *testing.T) {
	t.Setenv("SESSION_SECRET_KEY", "shared-secret")
	db := testOpenDB(t)
	svc := NewMCPDiscoveryService(discardLogger(), db)
	ctx := asUser(t, "alice")
	if err := svc.StoreOAuthState(ctx, "st", &OAuthState{SessionID: "s", OwnerUserID: "alice", ServerURL: "http://127.0.0.1:1/mcp"}); err != nil {
		t.Fatal(err)
	}
	rename := func(from, to string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+from+" RENAME TO "+to); err != nil {
			t.Fatal(err)
		}
	}

	rename("mcp_connector_oauth_states", "mcp_connector_oauth_states_away")
	_, err := svc.FindOAuthStateBySession(ctx, "s", "alice")
	expectUnavailable(t, "poll during the outage", err)
	_, err = svc.CompleteOAuth(ctx, "the-access-token", "st")
	expectUnavailable(t, "callback during the outage", err)
	_, err = svc.DiscoverWithSessionToken(ctx, MCPOAuthDiscoverRequest{SessionID: "s"}, "alice")
	expectUnavailable(t, "discovery during the outage", err)
	rename("mcp_connector_oauth_states_away", "mcp_connector_oauth_states")

	if got, err := svc.FindOAuthStateBySession(ctx, "s", "alice"); err != nil || got == nil || got.AccessToken != "" {
		t.Fatalf("poll after the outage = %+v, %v; want pending", got, err)
	}
	if sessionID, err := svc.CompleteOAuth(ctx, "the-access-token", "st"); err != nil || sessionID != "s" {
		t.Fatalf("callback retried after the outage = %q, %v", sessionID, err)
	}
	if got, err := svc.consumeOAuthStateBySession(ctx, "s"); err != nil || got == nil || got.AccessToken != "the-access-token" {
		t.Fatalf("take after the outage = %+v, %v; want the completed sign-in", got, err)
	}
}

func expectUnavailable(t *testing.T, step string, err error) {
	t.Helper()
	if _, ok := errors.AsType[*UnavailableError](err); !ok {
		t.Errorf("%s = %v; want an UnavailableError", step, err)
	}
	if _, ok := MCPOAuthRefusal(err); ok {
		t.Errorf("%s = %v; a store failure must not read as a refusal", step, err)
	}
}

func TestMCPOAuthLeaderJobDeletesExpiredSignIns(t *testing.T) {
	db := testOpenDB(t)
	svc := NewMCPDiscoveryService(discardLogger(), db)
	svc.stateTTL = time.Minute
	ctx := t.Context()
	if err := svc.StoreOAuthState(ctx, "old", &OAuthState{SessionID: "s-old", CreatedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := svc.StoreOAuthState(ctx, "fresh", &OAuthState{SessionID: "s-fresh"}); err != nil {
		t.Fatal(err)
	}
	job := svc.LeaderJob()
	if job.Name != leader.JobPlatformMCPOAuthStateExpiry || job.Every != oauthJanitorInterval {
		t.Errorf("job = %s every %s", job.Name, job.Every)
	}
	if err := job.Run(ctx, leader.Term{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_connector_oauth_states`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d, %v", n, err)
	}
	if got, err := svc.LoadOAuthState(ctx, "fresh"); err != nil || got == nil {
		t.Errorf("fresh sign-in deleted: %v", err)
	}
}
