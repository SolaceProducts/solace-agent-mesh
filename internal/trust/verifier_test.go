// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func setupVerifierTest(t *testing.T) (*Signer, *Verifier) {
	t.Helper()
	km, err := NewKeyManager("test-gw")
	if err != nil {
		t.Fatal(err)
	}

	signer := NewSigner(km, "test-gw", time.Hour, 24*time.Hour)

	registry := NewRegistry()
	registry.AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		Namespace:     "test-ns",
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	})

	verifier := NewVerifier(registry, 5*time.Second, slog.Default())
	return signer, verifier
}

func TestVerifierRoundTrip(t *testing.T) {
	t.Parallel()
	signer, verifier := setupVerifierTest(t)

	token, err := signer.Sign(SignClaims{
		Subject: "alice",
		TaskID:  "task-123",
		Name:    "Alice",
		Roles:   []string{"editor"},
		Scopes:  []string{"tool:*:*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	identity, err := verifier.Authenticate(context.Background(), token, "task-123")
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if identity == nil {
		t.Fatal("expected identity, got nil")
	}
	if identity.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", identity.UserID)
	}
	if len(identity.Roles) != 1 || identity.Roles[0] != "editor" {
		t.Errorf("Roles = %v, want [editor]", identity.Roles)
	}
	if len(identity.Scopes) != 1 || identity.Scopes[0] != "tool:*:*" {
		t.Errorf("Scopes = %v, want [tool:*]", identity.Scopes)
	}
	if identity.AuthToken != token {
		t.Error("AuthToken should be the original token")
	}
}

func TestVerifierTaskIDMismatch(t *testing.T) {
	t.Parallel()
	signer, verifier := setupVerifierTest(t)

	token, err := signer.Sign(SignClaims{
		Subject: "alice",
		TaskID:  "task-123",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = verifier.Authenticate(context.Background(), token, "different-task")
	if err == nil {
		t.Fatal("expected error for task_id mismatch")
	}
}

func TestVerifierWithoutTaskBinding(t *testing.T) {
	t.Parallel()
	signer, verifier := setupVerifierTest(t)

	token, err := signer.Sign(SignClaims{
		Subject: "alice",
		TaskID:  "task-123",
	})
	if err != nil {
		t.Fatal(err)
	}

	identity, err := verifier.VerifyWithoutTaskBinding(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyWithoutTaskBinding failed: %v", err)
	}
	if identity.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", identity.UserID)
	}
}

func TestVerifierUnknownIssuer(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("unknown-gw")
	if err != nil {
		t.Fatal(err)
	}
	signer := NewSigner(km, "unknown-gw", time.Hour, 24*time.Hour)

	registry := NewRegistry()
	verifier := NewVerifier(registry, 5*time.Second, slog.Default())

	token, _ := signer.Sign(SignClaims{Subject: "alice", TaskID: "t1"})
	_, err = verifier.Authenticate(context.Background(), token, "t1")
	if err == nil {
		t.Fatal("expected error for unknown issuer")
	}
}

func TestVerifierNonGatewayIssuer(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	signer := NewSigner(km, "agent-1", time.Hour, 24*time.Hour)

	registry := NewRegistry()
	registry.AddOrUpdate(&TrustCard{
		ComponentType: "agent",
		ComponentID:   "agent-1",
		JWKS:          km.JWKS(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
	})

	verifier := NewVerifier(registry, 5*time.Second, slog.Default())
	token, _ := signer.Sign(SignClaims{Subject: "alice", TaskID: "t1"})
	_, err = verifier.Authenticate(context.Background(), token, "t1")
	if err == nil {
		t.Fatal("expected error for non-gateway issuer")
	}
}

func TestVerifierExpiredToken(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("test-gw")
	if err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		JWKS:          km.JWKS(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
	})

	kp := km.CurrentKeyPair()
	now := time.Now()
	mapClaims := jwt.MapClaims{
		"iss":     "test-gw",
		"sub":     "alice",
		"iat":     now.Add(-2 * time.Hour).Unix(),
		"exp":     now.Add(-1 * time.Hour).Unix(),
		"task_id": "t1",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, mapClaims)
	token.Header["kid"] = kp.KeyID
	tokenStr, _ := token.SignedString(kp.PrivateKey)

	verifier := NewVerifier(registry, 5*time.Second, slog.Default())
	_, err = verifier.Authenticate(context.Background(), tokenStr, "t1")
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerifierClockSkewTolerance(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("test-gw")
	if err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		JWKS:          km.JWKS(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
	})

	kp := km.CurrentKeyPair()
	now := time.Now()
	mapClaims := jwt.MapClaims{
		"iss":     "test-gw",
		"sub":     "alice",
		"iat":     now.Add(-time.Hour).Unix(),
		"exp":     now.Add(-3 * time.Second).Unix(),
		"task_id": "t1",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, mapClaims)
	token.Header["kid"] = kp.KeyID
	tokenStr, _ := token.SignedString(kp.PrivateKey)

	verifier := NewVerifier(registry, 5*time.Second, slog.Default())
	identity, err := verifier.Authenticate(context.Background(), tokenStr, "t1")
	if err != nil {
		t.Fatalf("expected clock skew tolerance to accept: %v", err)
	}
	if identity.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", identity.UserID)
	}
}

func TestVerifierInvalidSignature(t *testing.T) {
	t.Parallel()
	km1, _ := NewKeyManager("test-gw")
	km2, _ := NewKeyManager("test-gw")

	signer := NewSigner(km1, "test-gw", time.Hour, 24*time.Hour)

	registry := NewRegistry()
	registry.AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		JWKS:          km2.JWKS(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
	})

	verifier := NewVerifier(registry, 5*time.Second, slog.Default())
	token, _ := signer.Sign(SignClaims{Subject: "alice", TaskID: "t1"})
	_, err := verifier.Authenticate(context.Background(), token, "t1")
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestVerifierEmptyToken(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	verifier := NewVerifier(registry, 5*time.Second, slog.Default())

	identity, err := verifier.Authenticate(context.Background(), "", "t1")
	if err != nil {
		t.Fatalf("empty token should not error: %v", err)
	}
	if identity != nil {
		t.Error("empty token should return nil identity")
	}
}

type recordCountHandler struct{ count int }

func (h *recordCountHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (h *recordCountHandler) Handle(context.Context, slog.Record) error { h.count++; return nil }
func (h *recordCountHandler) WithAttrs([]slog.Attr) slog.Handler        { return h }
func (h *recordCountHandler) WithGroup(string) slog.Handler             { return h }

func TestVerifierKeyNotInJWKSClassified(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("test-gw")
	if err != nil {
		t.Fatal(err)
	}
	signer := NewSigner(km, "test-gw", time.Hour, 24*time.Hour)

	registry := NewRegistry()
	registry.AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		Namespace:     "test-ns",
		JWKS:          map[string]any{"keys": []any{}},
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	verifier := NewVerifier(registry, 5*time.Second, slog.Default())

	token, err := signer.Sign(SignClaims{Subject: "alice", TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = verifier.parseAndVerify(token)
	if err == nil {
		t.Fatal("expected verification error when signing key absent from JWKS")
	}
	if !errors.Is(err, errKeyNotInJWKS) {
		t.Fatalf("error not classified as errKeyNotInJWKS: %v", err)
	}
}

func TestLogVerifyFailureRateLimit(t *testing.T) {
	t.Parallel()
	h := &recordCountHandler{}
	v := &Verifier{logger: slog.New(h)}

	keyErr := fmt.Errorf("key abc %w", errKeyNotInJWKS)
	for range 250 {
		v.logVerifyFailure(keyErr)
	}
	if h.count != 3 {
		t.Fatalf("key-not-found logged %d times, want 3 (rate-limited)", h.count)
	}

	h.count = 0
	for range 5 {
		v.logVerifyFailure(errors.New("token is expired"))
	}
	if h.count != 5 {
		t.Fatalf("generic failure logged %d times, want 5 (per-request)", h.count)
	}
}

func TestVerifierBadSignatureNotRateLimited(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("test-gw")
	if err != nil {
		t.Fatal(err)
	}
	signer := NewSigner(km, "test-gw", time.Hour, 24*time.Hour)

	registry := NewRegistry()
	registry.AddOrUpdate(&TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		Namespace:     "test-ns",
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	verifier := NewVerifier(registry, 5*time.Second, slog.Default())

	good, err := signer.Sign(SignClaims{Subject: "alice", TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := signer.Sign(SignClaims{Subject: "bob", TaskID: "t2"})
	if err != nil {
		t.Fatal(err)
	}
	gp := strings.Split(good, ".")
	op := strings.Split(other, ".")
	forged := gp[0] + "." + gp[1] + "." + op[2]

	_, err = verifier.parseAndVerify(forged)
	if err == nil {
		t.Fatal("expected signature verification to fail")
	}
	if errors.Is(err, errKeyNotInJWKS) {
		t.Fatalf("bad signature must not be classified as key-not-found (would be rate-limited): %v", err)
	}
}

func TestVerifierAttributionClaims(t *testing.T) {
	t.Parallel()
	signer, verifier := setupVerifierTest(t)

	bare, err := signer.Sign(SignClaims{Subject: "alice", TaskID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.Authenticate(t.Context(), bare, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if identity.AuthMethod != "" || identity.CredentialID != "" || identity.PlatformUserID != "" {
		t.Errorf("AuthMethod, CredentialID, PlatformUserID = %q, %q, %q from a token carrying none", identity.AuthMethod, identity.CredentialID, identity.PlatformUserID)
	}
	for _, claim := range []string{claimAuthMethod, claimCredentialID, ClaimPlatformUserID} {
		if _, present := identity.Claims[claim]; present {
			t.Errorf("%s claim emitted when unset", claim)
		}
	}

	const platformUserID = "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	keyed, err := signer.Sign(SignClaims{Subject: "system:ci-deployer", TaskID: "task-1", AuthMethod: "api_key", CredentialID: "key-1", PlatformUserID: platformUserID})
	if err != nil {
		t.Fatal(err)
	}
	identity, err = verifier.Authenticate(t.Context(), keyed, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if identity.AuthMethod != "api_key" || identity.CredentialID != "key-1" || identity.PlatformUserID != platformUserID {
		t.Errorf("AuthMethod, CredentialID, PlatformUserID = %q, %q, %q, want api_key, key-1, %s", identity.AuthMethod, identity.CredentialID, identity.PlatformUserID, platformUserID)
	}

	segments := strings.Split(keyed, ".")
	raw, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["auth_method"] != "api_key" || payload["credential_id"] != "key-1" || payload["platform_user_id"] != platformUserID {
		t.Errorf("wire claims auth_method, credential_id, platform_user_id = %v, %v, %v, want api_key, key-1, %s",
			payload["auth_method"], payload["credential_id"], payload["platform_user_id"], platformUserID)
	}
}

func TestVerifierIgnoresAPlatformUserClaimThatIsNotAString(t *testing.T) {
	t.Parallel()
	signer, verifier := setupVerifierTest(t)
	kp := signer.keyManager.CurrentKeyPair()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": "test-gw", "sub": "alice", "task_id": "task-1",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		ClaimPlatformUserID: 42,
	})
	token.Header["kid"] = kp.KeyID
	signed, err := token.SignedString(kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}

	identity, err := verifier.Authenticate(t.Context(), signed, "task-1")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if identity.UserID != "alice" || identity.PlatformUserID != "" {
		t.Errorf("identity = %q/%q, want alice with no platform user", identity.UserID, identity.PlatformUserID)
	}
}
