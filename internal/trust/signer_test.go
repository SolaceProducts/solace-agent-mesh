// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestSignerSign(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("test-gw")
	if err != nil {
		t.Fatal(err)
	}

	signer := NewSigner(km, "test-gw", time.Hour, 24*time.Hour)

	token, err := signer.Sign(SignClaims{
		Subject: "alice",
		TaskID:  "task-123",
		Name:    "Alice",
		Email:   "alice@example.com",
		Roles:   []string{"editor"},
		Scopes:  []string{"tool:*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}

	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != "test-gw" {
		t.Errorf("iss = %v, want test-gw", claims["iss"])
	}
	if claims["sub"] != "alice" {
		t.Errorf("sub = %v, want alice", claims["sub"])
	}
	if claims["task_id"] != "task-123" {
		t.Errorf("task_id = %v, want task-123", claims["task_id"])
	}
	if claims["name"] != "Alice" {
		t.Errorf("name = %v, want Alice", claims["name"])
	}
	if claims["email"] != "alice@example.com" {
		t.Errorf("email = %v, want alice@example.com", claims["email"])
	}

	if parsed.Header["kid"] != km.CurrentKeyPair().KeyID {
		t.Errorf("kid = %v, want %v", parsed.Header["kid"], km.CurrentKeyPair().KeyID)
	}

	_, err = jwt.Parse(token, func(t *jwt.Token) (any, error) {
		return km.CurrentKeyPair().PublicKey, nil
	})
	if err != nil {
		t.Errorf("signature verification failed: %v", err)
	}
}

func TestSignerDefaultTTL(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("gw")
	if err != nil {
		t.Fatal(err)
	}

	signer := NewSigner(km, "gw", time.Hour, 24*time.Hour)
	token, err := signer.Sign(SignClaims{Subject: "user", TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}

	parsed, _, _ := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	iat, _ := claims.GetIssuedAt()
	exp, _ := claims.GetExpirationTime()
	diff := exp.Sub(iat.Time)
	if diff != time.Hour {
		t.Errorf("TTL = %v, want 1h", diff)
	}
}

func TestSignerMaxTTLCap(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("gw")
	if err != nil {
		t.Fatal(err)
	}

	signer := NewSigner(km, "gw", time.Hour, 2*time.Hour)
	token, err := signer.Sign(SignClaims{
		Subject: "user",
		TaskID:  "t1",
		TTL:     10 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	parsed, _, _ := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	iat, _ := claims.GetIssuedAt()
	exp, _ := claims.GetExpirationTime()
	diff := exp.Sub(iat.Time)
	if diff != 2*time.Hour {
		t.Errorf("TTL = %v, want 2h (capped at maxTTL)", diff)
	}
}

func TestSignerOptionalClaims(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("gw")
	if err != nil {
		t.Fatal(err)
	}

	signer := NewSigner(km, "gw", time.Hour, 24*time.Hour)
	token, err := signer.Sign(SignClaims{Subject: "user", TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}

	parsed, _, _ := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	if _, ok := claims["name"]; ok {
		t.Error("name should be absent")
	}
	if _, ok := claims["email"]; ok {
		t.Error("email should be absent")
	}
	if _, ok := claims["roles"]; ok {
		t.Error("roles should be absent")
	}
	if _, ok := claims["scopes"]; ok {
		t.Error("scopes should be absent")
	}
}
