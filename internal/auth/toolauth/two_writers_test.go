// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/credentials"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"golang.org/x/sync/singleflight"
)

const twoWritersWait = 10 * time.Second

const (
	freshAccessResponse     = `{"access_token":"fresh-access","refresh_token":"rt-2","token_type":"Bearer","expires_in":3600}`
	refreshedAccessResponse = `{"access_token":"refreshed-access","refresh_token":"rt-2","token_type":"Bearer","expires_in":3600}`
)

func sharedCredentialStores(t *testing.T) (first, second auth.CredentialService) {
	t.Helper()
	shared := session.NewMemoryStore()
	km, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: "test-key-1234567890abcdef"}, "TestAgent", nil, nil)
	if err != nil {
		t.Fatalf("NewKeyManager() error = %v", err)
	}
	return credentials.NewAuthAdapter(credentials.NewSessionStore(shared, km, nil)),
		credentials.NewAuthAdapter(credentials.NewSessionStore(shared, km, nil))
}

func replicaToolCtx(svc auth.CredentialService) *tool.Context {
	return &tool.Context{AgentName: "TestAgent", UserID: "user1", ParentTaskID: "task-1", CredentialService: svc}
}

type heldServer struct {
	arrived chan int
	gates   [2]chan struct{}
	count   atomic.Int64
}

func newHeldServer(t *testing.T, respond func(w http.ResponseWriter, arrival int)) (*heldServer, string) {
	t.Helper()
	s := &heldServer{arrived: make(chan int, 2), gates: [2]chan struct{}{make(chan struct{}), make(chan struct{})}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrival := int(s.count.Add(1) - 1)
		s.arrived <- arrival
		<-s.gates[arrival]
		respond(w, arrival)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		for _, gate := range s.gates {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	})
	return s, srv.URL
}

func (s *heldServer) waitArrival(t *testing.T) {
	t.Helper()
	select {
	case <-s.arrived:
	case <-time.After(twoWritersWait):
		t.Fatalf("request did not reach the server within %v", twoWritersWait)
	}
}

type tokenResult struct {
	token *OAuthToken
	err   error
}

func waitToken(t *testing.T, results chan tokenResult) tokenResult {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(twoWritersWait):
		t.Fatalf("GetOAuthToken() did not return within %v", twoWritersWait)
		return tokenResult{}
	}
}

func TestRefreshByTwoWritersKeepsTheFreshToken(t *testing.T) {
	const (
		winner = 0
		loser  = 1
	)
	tests := []struct {
		name            string
		releaseOrder    []int
		wantLoserAccess string
		wantLoserErr    error
	}{
		{
			name:            "winner stores before the loser is rejected and the loser uses the stored token",
			releaseOrder:    []int{winner, loser},
			wantLoserAccess: "fresh-access",
		},
		{
			name:         "loser is rejected and clears before the winner stores and the fresh token is still stored",
			releaseOrder: []int{loser, winner},
			wantLoserErr: ErrAuthRequired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, url := newHeldServer(t, func(w http.ResponseWriter, arrival int) {
				w.Header().Set("Content-Type", "application/json")
				if arrival != winner {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				_, _ = w.Write([]byte(freshAccessResponse))
			})
			newReplica := func() *AuthHandler {
				return NewAuthHandler(&AuthConfig{
					Type:          "oauth2",
					CredentialKey: "test_key",
					Scheme:        &OAuthScheme{AuthorizationURL: url + "/authorize", TokenURL: url + "/token"},
					Credential:    &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
				}, slog.Default())
			}
			firstSvc, secondSvc := sharedCredentialStores(t)
			expired, _ := json.Marshal(OAuthToken{AccessToken: "old-access", RefreshToken: "rt-1", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Hour).Unix()})
			ctx := t.Context()
			if err := firstSvc.Set(ctx, "TestAgent", "TestAgent", "user1", "test_key", string(expired)); err != nil {
				t.Fatalf("Set() error = %v", err)
			}

			results := [2]chan tokenResult{make(chan tokenResult, 1), make(chan tokenResult, 1)}
			ctxs := [2]*tool.Context{replicaToolCtx(firstSvc), replicaToolCtx(secondSvc)}
			for i := range 2 {
				h := leaseHolderThenLapsedClock(newReplica(), i)
				go func() {
					tok, err := h.GetOAuthToken(ctx, ctxs[i])
					results[i] <- tokenResult{token: tok, err: err}
				}()
				provider.waitArrival(t)
			}

			var got [2]tokenResult
			for _, who := range tt.releaseOrder {
				close(provider.gates[who])
				got[who] = waitToken(t, results[who])
			}

			if got[winner].err != nil || got[winner].token.AccessToken != "fresh-access" {
				t.Errorf("winner GetOAuthToken() = %+v, want access token %q", got[winner], "fresh-access")
			}
			if !errors.Is(got[loser].err, tt.wantLoserErr) {
				t.Errorf("loser GetOAuthToken() error = %v, want %v", got[loser].err, tt.wantLoserErr)
			}
			if tt.wantLoserErr == nil && got[loser].token.AccessToken != tt.wantLoserAccess {
				t.Errorf("loser GetOAuthToken() access token = %q, want %q", got[loser].token.AccessToken, tt.wantLoserAccess)
			}
			raw, err := firstSvc.Get(ctx, "TestAgent", "TestAgent", "user1", "test_key")
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			var stored OAuthToken
			if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.AccessToken != "fresh-access" {
				t.Errorf("stored token = %q, want access token %q", raw, "fresh-access")
			}
		})
	}
}

func TestRegistrationByTwoWritersEndsWithOneClient(t *testing.T) {
	registrar, regURL := newHeldServer(t, func(w http.ResponseWriter, arrival int) {
		n := strconv.Itoa(arrival)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "client-" + n,
			"client_secret":              "secret-" + n,
			"token_endpoint_auth_method": "client_secret_basic",
		})
	})
	firstSvc, secondSvc := sharedCredentialStores(t)
	ctxs := [2]*tool.Context{replicaToolCtx(firstSvc), replicaToolCtx(secondSvc)}
	ctx := t.Context()

	type credResult struct {
		cred *AuthCredential
		err  error
	}
	results := [2]chan credResult{make(chan credResult, 1), make(chan credResult, 1)}
	handlers := [2]*AuthHandler{}
	for i := range 2 {
		handlers[i] = newDiscoveredHandler(regURL, "https://mcp.example.com/v1/mcp")
		h := handlers[i]
		go func() {
			cred, err := h.ensureCred(ctx, ctxs[i])
			results[i] <- credResult{cred: cred, err: err}
		}()
		registrar.waitArrival(t)
	}

	var got [2]credResult
	for i := range 2 {
		close(registrar.gates[i])
		select {
		case got[i] = <-results[i]:
		case <-time.After(twoWritersWait):
			t.Fatalf("ensureCred() did not return within %v", twoWritersWait)
		}
	}

	for i, r := range got {
		if r.err != nil || r.cred.ClientID != "client-0" {
			t.Errorf("writer %d ensureCred() = %+v, want client %q", i, r, "client-0")
		}
	}
	restarted := newDiscoveredHandler(regURL, "https://mcp.example.com/v1/mcp")
	reg, _ := restarted.loadPersistedClient(ctx, ctxs[1], restarted.discoveredMeta)
	if reg == nil || reg.ClientID != "client-0" {
		t.Errorf("stored client = %+v, want %q", reg, "client-0")
	}
}

type interceptedStore struct {
	*credentials.AuthAdapter
	beforeSetAt    func(ctx context.Context, ref auth.CredentialRef)
	beforeDeleteAt func(ctx context.Context, ref auth.CredentialRef)
	failSetAt      func(value string) error
}

func interceptStore(t *testing.T, svc auth.CredentialService) *interceptedStore {
	t.Helper()
	adapter, ok := svc.(*credentials.AuthAdapter)
	if !ok {
		t.Fatalf("credential service is %T, want *credentials.AuthAdapter", svc)
	}
	return &interceptedStore{AuthAdapter: adapter}
}

func (s *interceptedStore) SetAt(ctx context.Context, ref auth.CredentialRef, value string, ttl time.Duration, read auth.VersionedCredential) (auth.VersionedCredential, error) {
	if hook := s.beforeSetAt; hook != nil {
		s.beforeSetAt = nil
		hook(ctx, ref)
	}
	if fail := s.failSetAt; fail != nil {
		if err := fail(value); err != nil {
			return auth.VersionedCredential{}, err
		}
	}
	return s.AuthAdapter.SetAt(ctx, ref, value, ttl, read)
}

func (s *interceptedStore) DeleteAt(ctx context.Context, ref auth.CredentialRef, read auth.VersionedCredential) error {
	if hook := s.beforeDeleteAt; hook != nil {
		s.beforeDeleteAt = nil
		hook(ctx, ref)
	}
	return s.AuthAdapter.DeleteAt(ctx, ref, read)
}

func storedAccessToken(t *testing.T, svc auth.CredentialService) string {
	t.Helper()
	raw, err := svc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if raw == "" {
		return ""
	}
	var stored OAuthToken
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("stored token %q does not decode: %v", raw, err)
	}
	return stored.AccessToken
}

func setStoredToken(t *testing.T, svc auth.CredentialService, token OAuthToken) {
	t.Helper()
	data, _ := json.Marshal(token)
	if err := svc.Set(t.Context(), "TestAgent", "TestAgent", "user1", "test_key", string(data)); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
}

func expiredToken(access string) OAuthToken {
	return OAuthToken{AccessToken: access, RefreshToken: "rt-" + access, TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Hour).Unix()}
}

func freshToken(access string) OAuthToken {
	return OAuthToken{AccessToken: access, RefreshToken: "rt-" + access, TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour).Unix()}
}

func inOwnProcess(h *AuthHandler) *AuthHandler {
	h.refreshFlights = new(singleflight.Group)
	return h
}

func leaseHolderThenLapsedClock(h *AuthHandler, i int) *AuthHandler {
	if i > 0 {
		h.now = func() time.Time { return time.Now().Add(refreshLeaseTTL + time.Second) }
	}
	return inOwnProcess(h)
}

func staticClientReplica(tokenURL string) *AuthHandler {
	return NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{AuthorizationURL: tokenURL + "/authorize", TokenURL: tokenURL + "/token"},
		Credential:    &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())
}

func TestRejectedRefreshUsesTheTokenStoredBeforeItsClear(t *testing.T) {
	const (
		winner = 0
		loser  = 1
	)
	provider, url := newHeldServer(t, func(w http.ResponseWriter, arrival int) {
		w.Header().Set("Content-Type", "application/json")
		if arrival != winner {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_, _ = w.Write([]byte(freshAccessResponse))
	})
	firstSvc, secondSvc := sharedCredentialStores(t)
	setStoredToken(t, firstSvc, expiredToken("old-access"))
	loserSvc := interceptStore(t, secondSvc)
	ctx := t.Context()

	results := [2]chan tokenResult{make(chan tokenResult, 1), make(chan tokenResult, 1)}
	ctxs := [2]*tool.Context{replicaToolCtx(firstSvc), replicaToolCtx(loserSvc)}
	for i := range 2 {
		h := leaseHolderThenLapsedClock(staticClientReplica(url), i)
		go func() {
			tok, err := h.GetOAuthToken(ctx, ctxs[i])
			results[i] <- tokenResult{token: tok, err: err}
		}()
		provider.waitArrival(t)
	}
	var winnerGot tokenResult
	loserSvc.beforeDeleteAt = func(context.Context, auth.CredentialRef) {
		close(provider.gates[winner])
		winnerGot = waitToken(t, results[winner])
	}

	close(provider.gates[loser])
	loserGot := waitToken(t, results[loser])

	if winnerGot.err != nil || winnerGot.token.AccessToken != "fresh-access" {
		t.Errorf("winner GetOAuthToken() = %+v, want access token %q", winnerGot, "fresh-access")
	}
	if loserGot.err != nil || loserGot.token.AccessToken != "fresh-access" {
		t.Errorf("loser GetOAuthToken() = %+v, want access token %q", loserGot, "fresh-access")
	}
	if got := storedAccessToken(t, firstSvc); got != "fresh-access" {
		t.Errorf("stored access token = %q, want %q", got, "fresh-access")
	}
}

func TestRefreshStoredAfterAnotherWriteToTheRow(t *testing.T) {
	tests := []struct {
		name       string
		otherWrite otherRowWrite
		wantStored string
	}{
		{name: "a usable token another writer stored is kept", otherWrite: storeOtherUsableToken, wantStored: "other-access"},
		{name: "an expired token another writer stored is replaced", otherWrite: storeOtherExpiredToken, wantStored: "refreshed-access"},
		{name: "a row another writer cleared for sign-in is written", otherWrite: clearForSignIn, wantStored: "refreshed-access"},
		{name: "a row the user disconnected stays disconnected", otherWrite: disconnectUser, wantStored: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, url := newHeldServer(t, func(w http.ResponseWriter, _ int) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(refreshedAccessResponse))
			})
			refresherSvc, otherSvc := sharedCredentialStores(t)
			setStoredToken(t, refresherSvc, expiredToken("old-access"))
			ctx := t.Context()

			results := make(chan tokenResult, 1)
			h := staticClientReplica(url)
			go func() {
				tok, err := h.GetOAuthToken(ctx, replicaToolCtx(refresherSvc))
				results <- tokenResult{token: tok, err: err}
			}()
			provider.waitArrival(t)
			writeRow(t, otherSvc, tt.otherWrite)
			close(provider.gates[0])
			got := waitToken(t, results)

			if got.err != nil || got.token.AccessToken != "refreshed-access" {
				t.Errorf("GetOAuthToken() = %+v, want access token %q", got, "refreshed-access")
			}
			if stored := storedAccessToken(t, otherSvc); stored != tt.wantStored {
				t.Errorf("stored access token = %q, want %q", stored, tt.wantStored)
			}
		})
	}
}

type otherRowWrite int

const (
	storeOtherUsableToken otherRowWrite = iota
	storeOtherExpiredToken
	clearForSignIn
	disconnectUser
)

func writeRow(t *testing.T, svc auth.CredentialService, write otherRowWrite) {
	t.Helper()
	switch write {
	case storeOtherUsableToken:
		setStoredToken(t, svc, freshToken("other-access"))
	case storeOtherExpiredToken:
		setStoredToken(t, svc, expiredToken("other-access"))
	case clearForSignIn:
		ref := auth.CredentialRef{AgentName: "TestAgent", AppName: "TestAgent", UserID: "user1", Key: "test_key"}
		read, err := getVersioned(t.Context(), svc, ref)
		if err != nil {
			t.Fatalf("getVersioned() error = %v", err)
		}
		if err := deleteAt(t.Context(), svc, ref, read); err != nil {
			t.Fatalf("deleteAt() error = %v", err)
		}
	case disconnectUser:
		h := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "test_key"}, slog.Default())
		if err := h.ClearCredential(t.Context(), replicaToolCtx(svc)); err != nil {
			t.Fatalf("ClearCredential() error = %v", err)
		}
	}
}

func TestSignInTokenStoredOverAConcurrentWrite(t *testing.T) {
	first, second := sharedCredentialStores(t)
	setStoredToken(t, first, expiredToken("old-access"))
	svc := interceptStore(t, first)
	svc.beforeSetAt = func(context.Context, auth.CredentialRef) { setStoredToken(t, second, freshToken("other-access")) }
	h := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "test_key"}, slog.Default())

	signIn := freshToken("signed-in-access")
	if err := h.storeSignInToken(t.Context(), replicaToolCtx(svc), &signIn); err != nil {
		t.Fatalf("storeSignInToken() error = %v", err)
	}
	if got := storedAccessToken(t, first); got != "signed-in-access" {
		t.Errorf("stored access token = %q, want %q", got, "signed-in-access")
	}
}

func TestRegistrationStoredAfterTheRowChangedWithNoUsableClient(t *testing.T) {
	regURL, _ := dcrCountingServer(t)
	first, second := sharedCredentialStores(t)
	svc := interceptStore(t, first)
	h := newDiscoveredHandler(regURL, "https://mcp.example.com/v1/mcp")
	other := newDiscoveredHandler(regURL, "https://mcp.example.com/v1/mcp")
	svc.beforeSetAt = func(ctx context.Context, _ auth.CredentialRef) {
		otherCtx := replicaToolCtx(second)
		if err := other.storePersistedClient(ctx, otherCtx, &ClientRegistration{ClientID: "revoked-client"}, other.discoveredMeta, auth.VersionedCredential{}); err != nil {
			t.Fatalf("storePersistedClient() error = %v", err)
		}
		other.deletePersistedClient(ctx, otherCtx, "revoked-client")
	}

	cred, err := h.ensureCred(t.Context(), replicaToolCtx(svc))
	if err != nil {
		t.Fatalf("ensureCred() error = %v", err)
	}
	stored, _ := h.loadPersistedClient(t.Context(), replicaToolCtx(first), h.discoveredMeta)
	if stored == nil || stored.ClientID != cred.ClientID {
		t.Errorf("ensureCred() = client %q, stored client = %+v, want the same client", cred.ClientID, stored)
	}
}

func TestInvalidClientRefreshRetriesWithTheStoredClient(t *testing.T) {
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID, _, ok := r.BasicAuth()
		if !ok {
			_ = r.ParseForm()
			clientID = r.PostForm.Get("client_id")
		}
		w.Header().Set("Content-Type", "application/json")
		if clientID != "stored-client" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		_, _ = w.Write([]byte(refreshedAccessResponse))
	}))
	t.Cleanup(srv.Close)
	issuer = srv.URL
	svc, _ := sharedCredentialStores(t)
	toolCtx := replicaToolCtx(svc)
	h := newDiscoveredHandler(issuer+"/register", issuer)
	h.cfg.CredentialKey = "test_key"
	if err := h.storePersistedClient(t.Context(), toolCtx, &ClientRegistration{ClientID: "stored-client", ClientSecret: "s"}, h.discoveredMeta, auth.VersionedCredential{}); err != nil {
		t.Fatalf("storePersistedClient() error = %v", err)
	}
	h.registeredCred = &AuthCredential{ClientID: "replaced-client", ClientSecret: "s"}
	setStoredToken(t, svc, expiredToken("old-access"))

	got, err := h.GetOAuthToken(t.Context(), toolCtx)
	if err != nil || got.AccessToken != "refreshed-access" {
		t.Fatalf("GetOAuthToken() = (%+v, %v), want access token %q", got, err, "refreshed-access")
	}
	if stored := storedAccessToken(t, svc); stored != "refreshed-access" {
		t.Errorf("stored access token = %q, want %q", stored, "refreshed-access")
	}
	if kept, _ := h.loadPersistedClient(t.Context(), toolCtx, h.discoveredMeta); kept == nil || kept.ClientID != "stored-client" {
		t.Errorf("stored client = %+v, want %q", kept, "stored-client")
	}
}

func TestClearForReauthDeletesOnlyTheRejectedToken(t *testing.T) {
	tests := []struct {
		name       string
		stored     OAuthToken
		rejected   string
		wantStored string
	}{
		{name: "token another writer stored after the rejection is kept", stored: freshToken("new-access"), rejected: "old-access", wantStored: "new-access"},
		{name: "rejected token still stored is cleared", stored: freshToken("old-access"), rejected: "old-access", wantStored: ""},
		{name: "expired token is cleared when no token was sent", stored: expiredToken("old-access"), wantStored: ""},
		{name: "usable token is kept when no token was sent", stored: freshToken("new-access"), wantStored: "new-access"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := sharedCredentialStores(t)
			setStoredToken(t, svc, tt.stored)
			h := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "test_key"}, slog.Default())

			if err := h.ClearCredentialForReauth(t.Context(), replicaToolCtx(svc), tt.rejected); err != nil {
				t.Fatalf("ClearCredentialForReauth() error = %v", err)
			}
			if got := storedAccessToken(t, svc); got != tt.wantStored {
				t.Errorf("stored access token = %q, want %q", got, tt.wantStored)
			}
		})
	}
}

func TestDeleteKeepsARowAnotherWriterChangedAfterTheRead(t *testing.T) {
	clientRow := func(clientID string) string {
		data, _ := json.Marshal(persistedDCRClient{ClientID: clientID, ClientSecret: "s"})
		return string(data)
	}
	tokenRow := func(access string) string {
		data, _ := json.Marshal(freshToken(access))
		return string(data)
	}
	tests := []struct {
		name   string
		userID string
		key    string
		before string
		after  string
		remove func(h *AuthHandler, toolCtx *tool.Context)
	}{
		{
			name:   "the clear before a sign-in prompt",
			userID: "user1", key: "test_key",
			before: tokenRow("old-access"), after: tokenRow("new-access"),
			remove: func(h *AuthHandler, toolCtx *tool.Context) {
				if err := h.ClearCredentialForReauth(t.Context(), toolCtx, "old-access"); err != nil {
					t.Errorf("ClearCredentialForReauth() error = %v", err)
				}
			},
		},
		{
			name:   "the delete of a rejected OAuth client",
			userID: sharedDCRClientUserID, key: "test_key" + dcrClientKeySuffix,
			before: clientRow("cid"), after: clientRow("client-1"),
			remove: func(h *AuthHandler, toolCtx *tool.Context) {
				h.deletePersistedClient(t.Context(), toolCtx, "cid")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shared, other := sharedCredentialStores(t)
			ctx := t.Context()
			if err := shared.Set(ctx, "TestAgent", "TestAgent", tt.userID, tt.key, tt.before); err != nil {
				t.Fatalf("Set() error = %v", err)
			}
			var once sync.Once
			svc := &readSignalStore{interceptedStore: interceptStore(t, shared), onRead: func() {
				once.Do(func() {
					if err := other.Set(ctx, "TestAgent", "TestAgent", tt.userID, tt.key, tt.after); err != nil {
						t.Errorf("other Set() error = %v", err)
					}
				})
			}}
			h := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "test_key"}, slog.Default())

			tt.remove(h, replicaToolCtx(svc))

			if got, err := other.Get(ctx, "TestAgent", "TestAgent", tt.userID, tt.key); err != nil || got != tt.after {
				t.Errorf("stored row = (%q, %v), want %q", got, err, tt.after)
			}
		})
	}
}
