// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func setupTestSigner(t *testing.T) *trust.Signer {
	t.Helper()
	km, err := trust.NewKeyManager("test-gateway")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	return trust.NewSigner(km, "test-gateway", time.Hour, 2*time.Hour)
}

func TestMintAndValidateSAMToken(t *testing.T) {
	signer := setupTestSigner(t)

	token, err := MintSAMToken(signer, MintClaims{
		UserID:   "alice",
		Email:    "alice@example.com",
		Name:     "Alice",
		Roles:    []string{"admin", "viewer"},
		Provider: "azure",
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := ValidateSAMToken(token, signer)
	if err != nil {
		t.Fatalf("ValidateSAMToken: %v", err)
	}

	if claims.Subject != "alice" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "alice")
	}
	if claims.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", claims.Email, "alice@example.com")
	}
	if claims.Name != "Alice" {
		t.Errorf("Name = %q, want %q", claims.Name, "Alice")
	}
	if len(claims.Roles) != 2 {
		t.Errorf("Roles count = %d, want 2", len(claims.Roles))
	}
	if claims.Issuer != "test-gateway" {
		t.Errorf("Issuer = %q, want %q", claims.Issuer, "test-gateway")
	}
}

func TestMintSAMToken_EmptyUserID(t *testing.T) {
	signer := setupTestSigner(t)

	_, err := MintSAMToken(signer, MintClaims{
		UserID: "",
	}, time.Hour)
	if err == nil {
		t.Fatal("expected error for empty user_id")
	}
}

func TestValidateSAMToken_Expired(t *testing.T) {
	signer := setupTestSigner(t)

	token, err := MintSAMToken(signer, MintClaims{
		UserID: "alice",
	}, time.Millisecond)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	_, err = ValidateSAMToken(token, signer)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestValidateSAMToken_InvalidToken(t *testing.T) {
	signer := setupTestSigner(t)

	_, err := ValidateSAMToken("not-a-jwt", signer)
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestValidateSAMToken_WrongKey(t *testing.T) {
	signer1 := setupTestSigner(t)

	token, err := MintSAMToken(signer1, MintClaims{
		UserID: "alice",
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	signer2 := setupTestSigner(t)
	_, err = ValidateSAMToken(token, signer2)
	if err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestMintSAMToken_MinimalClaims(t *testing.T) {
	signer := setupTestSigner(t)

	token, err := MintSAMToken(signer, MintClaims{
		UserID: "bob",
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	claims, err := ValidateSAMToken(token, signer)
	if err != nil {
		t.Fatalf("ValidateSAMToken: %v", err)
	}
	if claims.Subject != "bob" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "bob")
	}
	if claims.Email != "" {
		t.Errorf("Email should be empty, got %q", claims.Email)
	}
	if len(claims.Roles) != 0 {
		t.Errorf("Roles should be empty, got %v", claims.Roles)
	}
}
