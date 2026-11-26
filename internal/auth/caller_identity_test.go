// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import "testing"

func TestCallerIdentityResolved(t *testing.T) {
	if (CallerIdentity{ID: "01_abc"}).Resolved() != true {
		t.Error("expected Resolved() true when ID is set")
	}
	if (CallerIdentity{Email: "a@example.com", Phone: "+1555"}).Resolved() != false {
		t.Error("expected Resolved() false when ID is empty, even with email/phone present")
	}
}

func TestCallerFromIdentity(t *testing.T) {
	got := CallerFromIdentity(&Identity{
		UserID:       "sub-alice",
		Scopes:       []string{"agent:x:invoke"},
		CredentialID: "key-1",
		Claims:       map[string]any{"email": "alice@example.com", "iss": "https://idp.example.com", "name": "Alice"},
	})
	if got.Sub != "sub-alice" || got.Email != "alice@example.com" || got.Issuer != "https://idp.example.com" || got.Name != "Alice" || got.CredentialID != "key-1" {
		t.Errorf("derivation wrong: %+v", got)
	}
	if got.Claims == nil || got.Claims["iss"] != "https://idp.example.com" {
		t.Errorf("raw claims not passed through: %+v", got.Claims)
	}
	if got.Resolved() {
		t.Error("agent-side identity must not carry a canonical ID")
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "agent:x:invoke" {
		t.Errorf("scopes not passed through: %v", got.Scopes)
	}
}

func TestCallerFromIdentity_NilAndMissingClaims(t *testing.T) {
	if nilCaller := CallerFromIdentity(nil); nilCaller.Sub != "" || nilCaller.Email != "" || nilCaller.Scopes != nil || nilCaller.Claims != nil {
		t.Errorf("nil identity should yield zero CallerIdentity, got %+v", nilCaller)
	}
	got := CallerFromIdentity(&Identity{UserID: "u1"})
	if got.Sub != "u1" || got.Email != "" || got.Issuer != "" {
		t.Errorf("missing-claims identity should yield only Sub: %+v", got)
	}
}
