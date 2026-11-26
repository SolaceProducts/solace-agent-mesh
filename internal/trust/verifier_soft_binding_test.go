// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func setupVerifierWithLogger(t *testing.T, buf *bytes.Buffer) (*Signer, *Verifier) {
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

	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	verifier := NewVerifier(registry, 5*time.Second, logger)
	return signer, verifier
}

func TestVerifySoftTaskBindingMatch(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	signer, verifier := setupVerifierWithLogger(t, &buf)

	token, err := signer.Sign(SignClaims{Subject: "alice", TaskID: "task-A"})
	if err != nil {
		t.Fatal(err)
	}

	identity, err := verifier.VerifyWithSoftTaskBinding(context.Background(), token, "task-A")
	if err != nil {
		t.Fatalf("VerifyWithSoftTaskBinding: %v", err)
	}
	if identity == nil || identity.UserID != "alice" {
		t.Fatalf("identity = %+v, want UserID=alice", identity)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no WARN log on match, got %s", buf.String())
	}
}

func TestVerifySoftTaskBindingMismatch(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	signer, verifier := setupVerifierWithLogger(t, &buf)

	token, err := signer.Sign(SignClaims{Subject: "alice", TaskID: "task-A"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = verifier.VerifyWithSoftTaskBinding(context.Background(), token, "task-B")
	if !errors.Is(err, auth.ErrTaskIDBindingMismatch) {
		t.Fatalf("err = %v, want ErrTaskIDBindingMismatch", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, `"event":"task_id_binding_mismatch"`) {
		t.Errorf("expected task_id_binding_mismatch event in log, got: %s", logged)
	}
	if !strings.Contains(logged, `"jwtTaskID":"task-A"`) {
		t.Errorf("expected jwtTaskID in log, got: %s", logged)
	}
	if !strings.Contains(logged, `"expectedTaskID":"task-B"`) {
		t.Errorf("expected expectedTaskID in log, got: %s", logged)
	}
}

func TestVerifySoftTaskBindingMissingClaim(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
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
	verifier := NewVerifier(registry, 5*time.Second,
		slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	kp := km.CurrentKeyPair()
	now := time.Now()
	mapClaims := jwt.MapClaims{
		"iss": "test-gw",
		"sub": "alice",
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, mapClaims)
	tok.Header["kid"] = kp.KeyID
	tokenStr, err := tok.SignedString(kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}

	identity, err := verifier.VerifyWithSoftTaskBinding(context.Background(), tokenStr, "task-A")
	if err != nil {
		t.Fatalf("VerifyWithSoftTaskBinding: %v", err)
	}
	if identity == nil || identity.UserID != "alice" {
		t.Fatalf("identity = %+v, want UserID=alice", identity)
	}
	if !strings.Contains(buf.String(), `"event":"task_id_binding_missing"`) {
		t.Errorf("expected task_id_binding_missing event in log, got: %s", buf.String())
	}
}

func TestVerifySoftTaskBindingEmptyExpected(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	signer, verifier := setupVerifierWithLogger(t, &buf)

	token, err := signer.Sign(SignClaims{Subject: "alice", TaskID: "task-A"})
	if err != nil {
		t.Fatal(err)
	}

	identity, err := verifier.VerifyWithSoftTaskBinding(context.Background(), token, "")
	if err != nil {
		t.Fatalf("VerifyWithSoftTaskBinding: %v", err)
	}
	if identity == nil || identity.UserID != "alice" {
		t.Fatalf("identity = %+v, want UserID=alice", identity)
	}
	if buf.Len() != 0 {
		t.Errorf("empty expectedTaskID must not emit a WARN log, got: %s", buf.String())
	}
}

func TestVerifyWithoutTaskBindingDelegates(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	signer, verifier := setupVerifierWithLogger(t, &buf)

	token, err := signer.Sign(SignClaims{Subject: "alice", TaskID: "task-A"})
	if err != nil {
		t.Fatal(err)
	}

	identity, err := verifier.VerifyWithoutTaskBinding(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyWithoutTaskBinding: %v", err)
	}
	if identity == nil || identity.UserID != "alice" {
		t.Fatalf("identity = %+v, want UserID=alice", identity)
	}
	if buf.Len() != 0 {
		t.Errorf("legacy method must not emit a WARN log, got: %s", buf.String())
	}
}

func TestPeerAuthenticatorInterfaceContract(t *testing.T) {
	t.Parallel()
	var pa auth.PeerAuthenticator = (*Verifier)(nil)
	_ = pa
}

func TestSignerAlwaysIncludesTaskID(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	signer, _ := setupVerifierWithLogger(t, &buf)

	token, err := signer.Sign(SignClaims{Subject: "alice", TaskID: "the-task"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("claims type = %T", parsed.Claims)
	}
	if claims["task_id"] != "the-task" {
		t.Errorf("task_id claim = %v, want the-task", claims["task_id"])
	}
}
