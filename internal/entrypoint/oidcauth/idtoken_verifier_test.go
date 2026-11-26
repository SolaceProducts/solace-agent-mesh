// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func ecXY(t *testing.T, pub *ecdsa.PublicKey) (xBytes, yBytes []byte) {
	t.Helper()
	raw, err := pub.Bytes()
	if err != nil {
		t.Fatalf("PublicKey.Bytes: %v", err)
	}
	if len(raw) != 65 || raw[0] != 0x04 {
		t.Fatalf("unexpected uncompressed encoding length=%d header=%x", len(raw), raw[0])
	}
	return raw[1:33], raw[33:65]
}

type idpStub struct {
	t        *testing.T
	current  *signingKey
	previous *signingKey
	hits     atomic.Int32
	httpMode atomic.Int32
}

type signingKey struct {
	kid string
	key *rsa.PrivateKey
}

func newSigningKey(t *testing.T) *signingKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return &signingKey{kid: fmt.Sprintf("kid-%d", time.Now().UnixNano()), key: k}
}

func (s *idpStub) Rotate(t *testing.T) {
	t.Helper()
	s.previous = s.current
	s.current = newSigningKey(t)
}

func (s *idpStub) Mint(t *testing.T, key *signingKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = key.kid
	signed, err := tok.SignedString(key.key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func (s *idpStub) MintWithMethod(t *testing.T, method jwt.SigningMethod, signKey any, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	signed, err := tok.SignedString(signKey)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func (s *idpStub) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	s.hits.Add(1)
	switch s.httpMode.Load() {
	case 1:
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	case 2:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"abc","n":"@@@","e":"@@@"}]}`))
		return
	}
	keys := []map[string]any{rsaJWK(&s.current.key.PublicKey, s.current.kid)}
	if s.previous != nil {
		keys = append(keys, rsaJWK(&s.previous.key.PublicKey, s.previous.kid))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}

func rsaJWK(pub *rsa.PublicKey, kid string) map[string]any {
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	return map[string]any{
		"kty": "RSA",
		"alg": "RS256",
		"use": "sig",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(eBytes),
	}
}

func startIDPStub(t *testing.T, issuer, clientID string) (*idpStub, *JWKSIDTokenVerifier, *httptest.Server) {
	t.Helper()
	stub := &idpStub{t: t, current: newSigningKey(t)}
	srv := httptest.NewServer(http.HandlerFunc(stub.handleJWKS))
	t.Cleanup(srv.Close)

	v, err := NewJWKSIDTokenVerifier(
		issuer,
		clientID,
		func(_ context.Context) (string, error) { return srv.URL, nil },
		srv.Client(),
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("NewJWKSIDTokenVerifier: %v", err)
	}
	v.minRefresh = 0
	return stub, v, srv
}

func validClaims(issuer, aud string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":    issuer,
		"aud":    aud,
		"sub":    "user-1",
		"groups": []string{"admins"},
		"iat":    now.Unix(),
		"exp":    now.Add(time.Hour).Unix(),
	}
}

func mustVerifyError(t *testing.T, err error, want IDTokenVerificationFailure) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	verr, ok := errors.AsType[*IDTokenVerificationError](err)
	if !ok {
		t.Fatalf("expected *IDTokenVerificationError, got %T: %v", err, err)
	}
	if verr.Reason != want {
		t.Fatalf("Reason = %s, want %s (err=%v)", verr.Reason, want, err)
	}
}

func TestVerifier_Empty(t *testing.T) {
	_, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	claims, err := v.VerifyIDToken(t.Context(), "")
	if err != nil || claims != nil {
		t.Fatalf("empty token: claims=%v, err=%v", claims, err)
	}
}

func TestVerifier_ValidToken(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	tok := stub.Mint(t, stub.current, validClaims("https://issuer.example", "client-1"))

	claims, err := v.VerifyIDToken(t.Context(), tok)
	if err != nil {
		t.Fatalf("VerifyIDToken: %v", err)
	}
	if claims["sub"] != "user-1" {
		t.Errorf("sub = %v, want user-1", claims["sub"])
	}
}

func TestVerifier_RejectsWrongSignature(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	rogue := newSigningKey(t)
	rogue.kid = stub.current.kid
	tok := stub.Mint(t, rogue, validClaims("https://issuer.example", "client-1"))

	_, err := v.VerifyIDToken(t.Context(), tok)
	mustVerifyError(t, err, IDTokenFailSignature)
}

func TestVerifier_RejectsWrongAudience(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-sam")
	tok := stub.Mint(t, stub.current, validClaims("https://issuer.example", "client-other"))

	_, err := v.VerifyIDToken(t.Context(), tok)
	mustVerifyError(t, err, IDTokenFailAudience)
}

func TestVerifier_RejectsWrongIssuer(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	tok := stub.Mint(t, stub.current, validClaims("https://wrong-issuer.example", "client-1"))

	_, err := v.VerifyIDToken(t.Context(), tok)
	mustVerifyError(t, err, IDTokenFailIssuer)
}

func TestVerifier_AcceptsArrayAudienceContainingClientID(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	claims := validClaims("https://issuer.example", "client-1")
	claims["aud"] = []string{"other-app", "client-1", "yet-another"}
	tok := stub.Mint(t, stub.current, claims)

	if _, err := v.VerifyIDToken(t.Context(), tok); err != nil {
		t.Fatalf("array-aud containing client_id: %v", err)
	}
}

func TestVerifier_RejectsArrayAudienceWithoutClientID(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-sam")
	claims := validClaims("https://issuer.example", "client-sam")
	claims["aud"] = []string{"sibling-app-a", "sibling-app-b"}
	tok := stub.Mint(t, stub.current, claims)

	_, err := v.VerifyIDToken(t.Context(), tok)
	mustVerifyError(t, err, IDTokenFailAudience)
}

func TestVerifier_AcceptsAnyIssuer_WhenIssuerEmpty(t *testing.T) {
	stub, v, _ := startIDPStub(t, "", "client-1")
	tok := stub.Mint(t, stub.current, validClaims("https://anywhere.example", "client-1"))

	if _, err := v.VerifyIDToken(t.Context(), tok); err != nil {
		t.Fatalf("VerifyIDToken: %v", err)
	}
}

func TestVerifier_RejectsExpired(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	claims := validClaims("https://issuer.example", "client-1")
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	tok := stub.Mint(t, stub.current, claims)

	_, err := v.VerifyIDToken(t.Context(), tok)
	mustVerifyError(t, err, IDTokenFailExpired)
}

func TestVerifier_RejectsAlgNone(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT","kid":"` + stub.current.kid + `"}`))
	payload, _ := json.Marshal(validClaims("https://issuer.example", "client-1"))
	body := base64.RawURLEncoding.EncodeToString(payload)
	tok := header + "." + body + "."

	_, err := v.VerifyIDToken(t.Context(), tok)
	mustVerifyError(t, err, IDTokenFailAlgorithm)
}

func TestVerifier_RejectsAlgConfusion_HS256(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	pubKeyBytes := stub.current.key.N.Bytes()
	tok := stub.MintWithMethod(t, jwt.SigningMethodHS256, pubKeyBytes, stub.current.kid, validClaims("https://issuer.example", "client-1"))

	_, err := v.VerifyIDToken(t.Context(), tok)
	mustVerifyError(t, err, IDTokenFailAlgorithm)
}

func TestVerifier_KidMissTriggersRefresh(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	tok1 := stub.Mint(t, stub.current, validClaims("https://issuer.example", "client-1"))
	if _, err := v.VerifyIDToken(t.Context(), tok1); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	hitsBefore := stub.hits.Load()

	stub.Rotate(t)
	tok2 := stub.Mint(t, stub.current, validClaims("https://issuer.example", "client-1"))

	if _, err := v.VerifyIDToken(t.Context(), tok2); err != nil {
		t.Fatalf("post-rotation verify: %v", err)
	}
	if stub.hits.Load() <= hitsBefore {
		t.Errorf("expected JWKS refresh after kid miss; hits before=%d after=%d", hitsBefore, stub.hits.Load())
	}

	tok3 := stub.Mint(t, stub.previous, validClaims("https://issuer.example", "client-1"))
	if _, err := v.VerifyIDToken(t.Context(), tok3); err != nil {
		t.Fatalf("previous-key verify within overlap window: %v", err)
	}
}

func TestVerifier_StaleKidAfterRefreshFailsNeverTrustsTheToken(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	v.minRefresh = 0

	primer := stub.Mint(t, stub.current, validClaims("https://issuer.example", "client-1"))
	if _, err := v.VerifyIDToken(t.Context(), primer); err != nil {
		t.Fatalf("primer verify: %v", err)
	}

	stub.httpMode.Store(1)
	rogue := newSigningKey(t)
	bad := stub.Mint(t, rogue, validClaims("https://issuer.example", "client-1"))

	_, err := v.VerifyIDToken(t.Context(), bad)
	mustVerifyError(t, err, IDTokenFailKeyResolution)
}

func TestVerifier_MalformedJWKS(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	stub.httpMode.Store(2)

	rogue := newSigningKey(t)
	tok := stub.Mint(t, rogue, validClaims("https://issuer.example", "client-1"))

	_, err := v.VerifyIDToken(t.Context(), tok)
	mustVerifyError(t, err, IDTokenFailKeyResolution)
}

func TestVerifier_AcceptsES256ForPythonParity(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	kid := "ec-kid-1"
	xBytes, yBytes := ecXY(t, &ecKey.PublicKey)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "EC",
				"alg": "ES256",
				"use": "sig",
				"kid": kid,
				"crv": "P-256",
				"x":   base64.RawURLEncoding.EncodeToString(xBytes),
				"y":   base64.RawURLEncoding.EncodeToString(yBytes),
			}},
		})
	}))
	t.Cleanup(srv.Close)

	v, err := NewJWKSIDTokenVerifier(
		"https://issuer.example",
		"client-1",
		func(_ context.Context) (string, error) { return srv.URL, nil },
		srv.Client(),
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("NewJWKSIDTokenVerifier: %v", err)
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodES256, validClaims("https://issuer.example", "client-1"))
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(ecKey)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	claims, err := v.VerifyIDToken(t.Context(), signed)
	if err != nil {
		t.Fatalf("VerifyIDToken (ES256): %v", err)
	}
	if claims["sub"] != "user-1" {
		t.Errorf("sub = %v, want user-1", claims["sub"])
	}
}

func TestVerifier_RejectsUnsupportedECCurve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "EC",
				"alg": "ES384",
				"kid": "ec-p384",
				"crv": "P-384",
				"x":   base64.RawURLEncoding.EncodeToString(make([]byte, 48)),
				"y":   base64.RawURLEncoding.EncodeToString(make([]byte, 48)),
			}},
		})
	}))
	t.Cleanup(srv.Close)

	v, err := NewJWKSIDTokenVerifier(
		"https://issuer.example",
		"client-1",
		func(_ context.Context) (string, error) { return srv.URL, nil },
		srv.Client(),
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("NewJWKSIDTokenVerifier: %v", err)
	}

	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims("https://issuer.example", "client-1"))
	tok.Header["kid"] = "ec-p384"
	signed, _ := tok.SignedString(rsaKey)

	_, err = v.VerifyIDToken(t.Context(), signed)
	mustVerifyError(t, err, IDTokenFailKeyResolution)
}

func TestVerifier_RejectsAlgRSAWithECKey(t *testing.T) {
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	kid := "shared-kid"

	xBytes, yBytes := ecXY(t, &ecKey.PublicKey)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "EC", "alg": "ES256", "kid": kid, "crv": "P-256",
				"x": base64.RawURLEncoding.EncodeToString(xBytes),
				"y": base64.RawURLEncoding.EncodeToString(yBytes),
			}},
		})
	}))
	t.Cleanup(srv.Close)

	v, err := NewJWKSIDTokenVerifier(
		"https://issuer.example",
		"client-1",
		func(_ context.Context) (string, error) { return srv.URL, nil },
		srv.Client(),
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("NewJWKSIDTokenVerifier: %v", err)
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims("https://issuer.example", "client-1"))
	tok.Header["kid"] = kid
	signed, _ := tok.SignedString(rsaKey)

	_, err = v.VerifyIDToken(t.Context(), signed)
	mustVerifyError(t, err, IDTokenFailAlgorithm)
}

func TestNewJWKSIDTokenVerifier_RequiresClientID(t *testing.T) {
	for _, tc := range []struct {
		name       string
		clientID   string
		resolver   JWKSResolver
		httpClient *http.Client
	}{
		{name: "missing client_id", resolver: func(_ context.Context) (string, error) { return "", nil }, httpClient: http.DefaultClient},
		{name: "missing resolver", clientID: "c", httpClient: http.DefaultClient},
		{name: "missing httpClient", clientID: "c", resolver: func(_ context.Context) (string, error) { return "", nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewJWKSIDTokenVerifier("iss", tc.clientID, tc.resolver, tc.httpClient, nil); err == nil {
				t.Fatalf("expected construction error for %s", tc.name)
			}
		})
	}
}

func TestVerifier_RateLimitsRefresh(t *testing.T) {
	stub, v, _ := startIDPStub(t, "https://issuer.example", "client-1")
	v.minRefresh = time.Hour

	tok := stub.Mint(t, stub.current, validClaims("https://issuer.example", "client-1"))
	if _, err := v.VerifyIDToken(t.Context(), tok); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	hits1 := stub.hits.Load()

	rogue := newSigningKey(t)
	bad := stub.Mint(t, rogue, validClaims("https://issuer.example", "client-1"))
	_, err := v.VerifyIDToken(t.Context(), bad)
	mustVerifyError(t, err, IDTokenFailKeyResolution)
	if stub.hits.Load() != hits1 {
		t.Errorf("expected no JWKS hit during rate-limit window; hits before=%d after=%d", hits1, stub.hits.Load())
	}
}

func TestVerifier_RateLimitedRefreshUsesTheCacheAConcurrentRefreshFilled(t *testing.T) {
	stub, v, srv := startIDPStub(t, "https://issuer.example", "client-1")
	v.minRefresh = 5 * time.Minute

	primer := stub.Mint(t, stub.current, validClaims("https://issuer.example", "client-1"))
	if _, err := v.VerifyIDToken(t.Context(), primer); err != nil {
		t.Fatalf("primer verify: %v", err)
	}

	stub.Rotate(t)
	rotated := stub.Mint(t, stub.current, validClaims("https://issuer.example", "client-1"))

	v.refreshMu.Lock()

	done := make(chan error, 1)
	go func() { _, err := v.VerifyIDToken(t.Context(), rotated); done <- err }()

	keys, err := v.fetchJWKSInstrumented(t.Context(), srv.URL)
	if err != nil {
		t.Fatalf("stand-in refresh: %v", err)
	}
	v.cache.Store(&jwksCacheEntry{keys: keys})
	v.lastRefresh.Store(time.Now().UnixNano())
	v.refreshMu.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("verification failed although the cache already held the rotated kid: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("verification never returned")
	}
}
