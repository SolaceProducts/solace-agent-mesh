// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust/trusttest"
	"github.com/golang-jwt/jwt/v5"
)

func newTestFinishHandler(t *testing.T) (*Handler, *trust.Signer) {
	t.Helper()
	km, err := trust.NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := trust.NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)
	sv := trusttest.NewStateVerifier(km, "mcp-a", map[string]bool{"example.com": true})

	h := New(Config{
		EnableAuth:              true,
		GatewayID:               "mcp-a",
		StateRedirectionEnabled: true,
		OAuthDispatcherURL:      "https://example.com/api/v1/auth/callback",
		AllowedRedirectURIs:     []string{"http://localhost:8081/cb"},
		IDPClient:               fakeIDPClient{},
		SAMTokenMinter:          &fakeMinter{},
		StateSigner:             trust.NewStateSigner(signer),
		StateVerifier:           sv,
	})
	return h, signer
}

func mintStateForFinish(t *testing.T, signer *trust.Signer, gwID, verifierHashHex string) string {
	t.Helper()
	return mintStateForFinishWithIss(t, signer, gwID, gwID, verifierHashHex)
}

func mintStateForFinishWithIss(t *testing.T, signer *trust.Signer, iss, target, verifierHashHex string) string {
	t.Helper()
	hashPrefix := verifierHashHex
	if len(hashPrefix) > 8 {
		hashPrefix = hashPrefix[:8]
	}
	claims := trust.StateClaims{
		TargetLinkURI:      "https://example.com/gw/" + target + "/oauth/finish",
		GwPKCEVerifierHash: verifierHashHex,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    iss,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			ID:        "jti-" + iss + "-" + hashPrefix,
		},
	}
	raw, err := trust.NewStateSigner(signer).Mint(claims)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return raw
}

func TestHandleFinish_HappyPath(t *testing.T) {
	h, signer := newTestFinishHandler(t)

	verifier := "pkce-verifier-32-bytes-long-aaaaa"
	hash := sha256.Sum256([]byte(verifier))
	hashHex := hex.EncodeToString(hash[:])

	mem(h).mu.Lock()
	mem(h).states["internal-state-1"] = &oauthState{
		CodeVerifier:      verifier,
		ClientRedirectURI: "http://localhost:8081/cb",
		ClientState:       "client-nonce",
		CreatedAt:         time.Now(),
		TTL:               5 * time.Minute,
	}
	mem(h).mu.Unlock()

	raw := mintStateForFinish(t, signer, "mcp-a", hashHex)
	req := httptest.NewRequest(http.MethodGet, "/oauth/finish?code=idp-code-1&state="+url.QueryEscape(raw), nil)
	rec := httptest.NewRecorder()
	h.HandleFinish(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: want 302, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Host == "" || loc.Path != "/cb" {
		t.Fatalf("Location should redirect to client cb: %s", loc)
	}
	if got := loc.Query().Get("state"); got != "client-nonce" {
		t.Fatalf("client state: want client-nonce, got %s", got)
	}
	if loc.Query().Get("code") == "" {
		t.Fatal("MCP authcode missing from redirect")
	}
}

func TestHandleFinish_MissingInProcessState(t *testing.T) {
	h, signer := newTestFinishHandler(t)

	emptyHash := sha256.Sum256([]byte(""))
	hashHex := hex.EncodeToString(emptyHash[:])
	raw := mintStateForFinish(t, signer, "mcp-a", hashHex)

	req := httptest.NewRequest(http.MethodGet, "/oauth/finish?code=idp-code&state="+url.QueryEscape(raw), nil)
	rec := httptest.NewRecorder()
	h.HandleFinish(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

func TestHandleFinish_IssMismatch(t *testing.T) {
	h, signer := newTestFinishHandler(t)

	raw := mintStateForFinishWithIss(t, signer, "mcp-b", "mcp-b", "irrelevant-hash")

	req := httptest.NewRequest(http.MethodGet, "/oauth/finish?code=idp-code&state="+url.QueryEscape(raw), nil)
	rec := httptest.NewRecorder()
	h.HandleFinish(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400 for iss mismatch, got %d", rec.Code)
	}
}

func TestHandleFinish_IdPErrorPropagatesToClient(t *testing.T) {
	h, signer := newTestFinishHandler(t)

	verifier := "pkce-verifier-32-bytes-long-bbbbb"
	hash := sha256.Sum256([]byte(verifier))
	hashHex := hex.EncodeToString(hash[:])

	mem(h).mu.Lock()
	mem(h).states["internal-state-2"] = &oauthState{
		CodeVerifier:      verifier,
		ClientRedirectURI: "http://localhost:8081/cb",
		ClientState:       "client-nonce-2",
		CreatedAt:         time.Now(),
		TTL:               5 * time.Minute,
	}
	mem(h).mu.Unlock()

	raw := mintStateForFinish(t, signer, "mcp-a", hashHex)
	target := "/oauth/finish?error=access_denied&error_description=user_cancelled&state=" + url.QueryEscape(raw)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.HandleFinish(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: want 302 (RFC 6749 §4.1.2.1), got %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := loc.Query().Get("error"); got != "access_denied" {
		t.Fatalf("error: want access_denied, got %s", got)
	}
	if got := loc.Query().Get("state"); got != "client-nonce-2" {
		t.Fatalf("client state: want client-nonce-2, got %s", got)
	}
}

func TestHandleFinish_MissingCode(t *testing.T) {
	h, signer := newTestFinishHandler(t)

	verifier := "pkce-verifier-32-bytes-long-ccccc"
	hash := sha256.Sum256([]byte(verifier))
	hashHex := hex.EncodeToString(hash[:])

	mem(h).mu.Lock()
	mem(h).states["internal-state-3"] = &oauthState{
		CodeVerifier:      verifier,
		ClientRedirectURI: "http://localhost:8081/cb",
		ClientState:       "x",
		CreatedAt:         time.Now(),
		TTL:               5 * time.Minute,
	}
	mem(h).mu.Unlock()

	raw := mintStateForFinish(t, signer, "mcp-a", hashHex)
	req := httptest.NewRequest(http.MethodGet, "/oauth/finish?state="+url.QueryEscape(raw), nil)
	rec := httptest.NewRecorder()
	h.HandleFinish(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400 for missing code, got %d", rec.Code)
	}
}
