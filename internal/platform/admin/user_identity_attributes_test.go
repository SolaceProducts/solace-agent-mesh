// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestIdentityAttributesChanged(t *testing.T) {
	user := func(email, phone string) *store.User {
		u := &store.User{Issuer: "iss", Sub: "sub", OIDCProvider: "azure"}
		if email != "" {
			u.Email = &email
		}
		if phone != "" {
			u.Phone = &phone
		}
		return u
	}
	withProvider := func(u *store.User, provider string) *store.User {
		u.OIDCProvider = provider
		return u
	}
	claimsMoved := func() (bool, error) { return true, nil }
	claimsStable := func() (bool, error) { return false, nil }
	claimsUnknown := func() (bool, error) { return false, errors.New("composer unavailable") }

	tests := []struct {
		name      string
		before    *store.User
		after     *store.User
		scimOwned bool
		claims    func() (bool, error)
		want      bool
	}{
		{"no stored row is a change", nil, user("a@example.com", ""), false, claimsStable, true},

		{"unchanged login is not a change", user("a@example.com", "+15551230000"), user("a@example.com", "+15551230000"), false, claimsStable, false},

		{"email moved", user("a@example.com", ""), user("b@example.com", ""), false, claimsStable, true},
		{"email added", user("", ""), user("a@example.com", ""), false, claimsStable, true},
		{"email cleared", user("a@example.com", ""), user("", ""), false, claimsStable, true},
		{"phone moved", user("a@example.com", "+15551230000"), user("a@example.com", "+15559990000"), false, claimsStable, true},
		{"phone cleared by the conflict retry", user("a@example.com", "+15551230000"), user("a@example.com", ""), false, claimsStable, true},

		{"oidc provider moved", withProvider(user("a@example.com", ""), "azure"), withProvider(user("a@example.com", ""), "okta"), false, claimsStable, true},

		{"mapped claim moved", user("a@example.com", ""), user("a@example.com", ""), false, claimsMoved, true},

		{"unanswerable claim comparison is a change", user("a@example.com", ""), user("a@example.com", ""), false, claimsUnknown, true},

		{"scim-owned row is never a change", user("scim@example.com", ""), user("jit@example.com", "+15551230000"), true, claimsMoved, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := identityAttributesChanged(tt.before, tt.after, tt.scimOwned, tt.claims); got != tt.want {
				t.Errorf("identityAttributesChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}
