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

func TestStateSigner_MintProducesParseableToken(t *testing.T) {
	km, err := NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)
	ss := NewStateSigner(signer)

	claims := StateClaims{
		TargetLinkURI: "https://example.com/gw/mcp-a/oauth/finish",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mcp-a",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			ID:        "jti-1",
		},
	}
	raw, err := ss.Mint(claims)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	token, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	if got, want := token.Header["typ"], StateJWTType; got != want {
		t.Fatalf("typ: want %q, got %v", want, got)
	}
	if token.Header["kid"] == "" || token.Header["kid"] == nil {
		t.Fatalf("kid missing from header")
	}
	if got, want := token.Method.Alg(), "ES256"; got != want {
		t.Fatalf("alg: want %s, got %s", want, got)
	}
}

func TestStateSigner_MintRejectsNilSigner(t *testing.T) {
	ss := NewStateSigner(nil)
	if _, err := ss.Mint(StateClaims{}); err == nil {
		t.Fatal("expected error for nil signer")
	}
}
