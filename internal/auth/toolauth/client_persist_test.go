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
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func dcrCountingServer(t *testing.T) (url string, calls *atomic.Int64) {
	t.Helper()
	calls = &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		n := strconv.FormatInt(calls.Add(1), 10)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "client-" + n,
			"client_secret":              "secret-" + n,
			"token_endpoint_auth_method": "client_secret_basic",
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, calls
}

func newDiscoveredHandler(regURL, issuer string) *AuthHandler {
	h := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "atlassian_test"}, slog.Default())
	h.discoveredMeta = &OAuthMetadata{
		Issuer:                issuer,
		AuthorizationEndpoint: issuer + "/authorize",
		TokenEndpoint:         issuer + "/token",
		RegistrationEndpoint:  regURL,
		DiscoveredAt:          time.Now(),
	}
	return h
}

func TestEnsureCred_PersistedClient_ReusedAcrossRestart(t *testing.T) {
	regURL, calls := dcrCountingServer(t)
	const issuer = "https://mcp.example.com/v1/mcp"

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	ctx := context.Background()

	h1 := newDiscoveredHandler(regURL, issuer)
	cred1, err := h1.ensureCred(ctx, toolCtx)
	if err != nil {
		t.Fatalf("h1 ensureCred: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 registration after first handler, got %d", calls.Load())
	}

	h2 := newDiscoveredHandler(regURL, issuer)
	cred2, err := h2.ensureCred(ctx, toolCtx)
	if err != nil {
		t.Fatalf("h2 ensureCred: %v", err)
	}

	if calls.Load() != 1 {
		t.Errorf("second handler re-registered (client_id rotated): registration endpoint hit %d times, want 1", calls.Load())
	}
	if cred2.ClientID != cred1.ClientID {
		t.Errorf("client_id rotated across restart: got %q, want persisted %q", cred2.ClientID, cred1.ClientID)
	}
	if cred2.ClientSecret != cred1.ClientSecret {
		t.Errorf("client_secret rotated across restart")
	}
}

func TestEnsureCred_PersistedClient_InvalidatedOnIssuerChange(t *testing.T) {
	regURL, calls := dcrCountingServer(t)
	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	ctx := context.Background()

	h1 := newDiscoveredHandler(regURL, "https://old-issuer.example.com")
	if _, err := h1.ensureCred(ctx, toolCtx); err != nil {
		t.Fatalf("h1 ensureCred: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("want 1 registration, got %d", calls.Load())
	}

	h2 := newDiscoveredHandler(regURL, "https://new-issuer.example.com")
	if _, err := h2.ensureCred(ctx, toolCtx); err != nil {
		t.Fatalf("h2 ensureCred: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("issuer change should force re-registration: registration hits = %d, want 2", calls.Load())
	}
}

func TestLoadPersistedClient_ExpiredSecret_Ignored(t *testing.T) {
	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	ctx := context.Background()
	h := newDiscoveredHandler("https://reg.example.com/register", "https://iss.example.com")

	expired := persistedDCRClient{
		ClientID:              "stale",
		ClientSecret:          "s",
		ClientSecretExpiresAt: time.Now().Add(-time.Hour).Unix(),
		Issuer:                "https://iss.example.com",
		RegistrationEndpoint:  "https://reg.example.com/register",
	}
	data, _ := json.Marshal(expired)
	_ = credSvc.Set(ctx, "TestAgent", "TestAgent", sharedDCRClientUserID, "atlassian_test"+dcrClientKeySuffix, string(data))

	if got, _ := h.loadPersistedClient(ctx, toolCtx, h.discoveredMeta); got != nil {
		t.Errorf("expected expired persisted client to be ignored, got client_id=%q", got.ClientID)
	}
}

func seedPersistedClient(credSvc *mockCredentialService, credKey, clientID string) {
	pc := persistedDCRClient{ClientID: clientID, ClientSecret: "s", Issuer: "https://iss", RegistrationEndpoint: "https://reg"}
	data, _ := json.Marshal(pc)
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", sharedDCRClientUserID, credKey+dcrClientKeySuffix, string(data))
}

func getPersistedClientRaw(credSvc *mockCredentialService, credKey string) string {
	v, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", sharedDCRClientUserID, credKey+dcrClientKeySuffix)
	return v
}

func refreshErrorHandler(t *testing.T, oauthErr string) *AuthHandler {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"` + oauthErr + `"}`))
	}))
	t.Cleanup(srv.Close)
	return NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{AuthorizationURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token"},
		Credential:    &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())
}

func TestPersistedClientOnInvalidClientRefresh(t *testing.T) {
	tests := []struct {
		name           string
		storedClientID string
		wantKept       bool
	}{
		{name: "stored client that was rejected is deleted", storedClientID: "cid", wantKept: false},
		{name: "stored client another writer registered is kept", storedClientID: "other-client", wantKept: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := refreshErrorHandler(t, "invalid_client")
			credSvc := newMockCredentialService()
			storeToken(t, credSvc, &OAuthToken{AccessToken: "expired", RefreshToken: "rt", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Hour).Unix()})
			seedPersistedClient(credSvc, "test_key", tt.storedClientID)
			toolCtx := testToolCtx(credSvc)

			if _, err := h.GetOAuthToken(t.Context(), toolCtx); !errors.Is(err, ErrAuthRequired) {
				t.Fatalf("GetOAuthToken() error = %v, want ErrAuthRequired", err)
			}
			if kept := getPersistedClientRaw(credSvc, "test_key") != ""; kept != tt.wantKept {
				t.Errorf("GetOAuthToken() left stored client %q present = %v, want %v", tt.storedClientID, kept, tt.wantKept)
			}
		})
	}
}

func TestPersistedClient_PreservedOnInvalidGrantRefresh(t *testing.T) {
	h := refreshErrorHandler(t, "invalid_grant")
	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{AccessToken: "expired", RefreshToken: "dead-rt", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Hour).Unix()})
	seedPersistedClient(credSvc, "test_key", "kept-client")
	toolCtx := testToolCtx(credSvc)

	if _, err := h.GetOAuthToken(context.Background(), toolCtx); !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("GetOAuthToken: got %v, want ErrAuthRequired", err)
	}
	raw := getPersistedClientRaw(credSvc, "test_key")
	if raw == "" {
		t.Fatal("persisted client must be preserved on invalid_grant (a single user's dead token must not wipe the shared client)")
	}
	var pc persistedDCRClient
	if err := json.Unmarshal([]byte(raw), &pc); err != nil || pc.ClientID != "kept-client" {
		t.Errorf("persisted client changed on invalid_grant: %s", raw)
	}
}

type ttlRecordingCredSvc struct {
	*mockCredentialService
	lastTTL time.Duration
	ttlUsed bool
}

func (m *ttlRecordingCredSvc) SetWithTTL(ctx context.Context, agentName, appName, userID, key, value string, ttl time.Duration) error {
	m.ttlUsed = true
	m.lastTTL = ttl
	return m.Set(ctx, agentName, appName, userID, key, value)
}

func TestStorePersistedClient_UsesLongTTL(t *testing.T) {
	fake := &ttlRecordingCredSvc{mockCredentialService: newMockCredentialService()}
	toolCtx := &tool.Context{AgentName: "TestAgent", CredentialService: fake}
	h := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "test_key"}, slog.Default())

	_ = h.storePersistedClient(t.Context(), toolCtx,
		&ClientRegistration{ClientID: "c", ClientSecret: "s"},
		&OAuthMetadata{Issuer: "i", RegistrationEndpoint: "r"}, auth.VersionedCredential{})

	if !fake.ttlUsed {
		t.Fatal("expected storePersistedClient to use the SetWithTTL path")
	}
	if fake.lastTTL != dcrClientPersistTTL {
		t.Errorf("persisted with ttl=%v, want %v", fake.lastTTL, dcrClientPersistTTL)
	}
}
