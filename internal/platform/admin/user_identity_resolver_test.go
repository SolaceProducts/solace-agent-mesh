// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestUsersRepoIdentityResolver_ResolveLookupContext(t *testing.T) {
	ctx := context.Background()
	db := testOpenDB(t)
	repo := store.NewUsersRepo(db)
	resolver := newUsersRepoIdentityResolver(repo, discardLogger())

	emailUser := &store.User{Issuer: "iss", Sub: "email-sub", Email: usPtr("alice@example.com"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, emailUser, nil); err != nil {
		t.Fatalf("seed email user: %v", err)
	}
	e164User := &store.User{Issuer: "iss", Sub: "e164-sub", Phone: usPtr("+15551230000"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, e164User, nil); err != nil {
		t.Fatalf("seed e164 user: %v", err)
	}
	bareUser := &store.User{Issuer: "iss", Sub: "bare-sub", Phone: usPtr("15559990000"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, bareUser, nil); err != nil {
		t.Fatalf("seed bare-phone user: %v", err)
	}
	dupA := &store.User{Issuer: "issA", Sub: "dup-a", Email: usPtr("dup@example.com"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, dupA, nil); err != nil {
		t.Fatalf("seed dup user A: %v", err)
	}
	dupB := &store.User{Issuer: "issB", Sub: "dup-b", Email: usPtr("dup@example.com"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, dupB, nil); err != nil {
		t.Fatalf("seed dup user B: %v", err)
	}
	systemUser := &store.User{
		Issuer:         rbac.SystemUserIssuer,
		Sub:            rbac.SystemUserPrincipal("bridge"),
		ProvisionedVia: rbac.ProvisionedViaSystem,
		Email:          usPtr("bridge@example.com"),
		Phone:          usPtr("+15557770000"),
		CreatedBy:      "x",
		UpdatedBy:      "x",
	}
	if err := repo.UpsertUser(ctx, systemUser, nil); err != nil {
		t.Fatalf("seed system user: %v", err)
	}
	inactiveUser := &store.User{Issuer: "iss", Sub: "inactive-sub", Email: usPtr("gone@example.com"), Phone: usPtr("+15556660000"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, inactiveUser, nil); err != nil {
		t.Fatalf("seed inactive user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET active = ? WHERE id = ?`, 0, inactiveUser.ID); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	tests := []struct {
		name   string
		userID string
		want   string
	}{
		{"email resolves (mixed case lowercased to match stored)", "Alice@Example.com", emailUser.ID},
		{"unknown email resolves to empty", "nobody@example.com", ""},
		{"ambiguous email fails closed to empty", "dup@example.com", ""},
		{"a whatsapp principal no longer resolves against a stored E.164 phone", "whatsapp:15551230000", ""},
		{"a whatsapp principal no longer resolves against a bare stored phone", "whatsapp:15559990000", ""},
		{"unknown whatsapp phone resolves to empty", "whatsapp:19998887777", ""},
		{"a real users.id is not resolved as a surrogate id", emailUser.ID, ""},
		{"a system user's email is not resolved", "bridge@example.com", ""},
		{"a system user's phone is not resolved", "whatsapp:15557770000", ""},
		{"a deactivated user's email is not resolved", "gone@example.com", ""},
		{"a deactivated user's phone is not resolved", "whatsapp:15556660000", ""},
		{"machine principal resolves to empty", "channel-user", ""},
		{"system principal resolves to empty", "system:default", ""},
		{"system principal with an email-shaped name resolves to empty", "system:svc@corp.com", ""},
		{"slack no-email fallback resolves to empty", "slack:T0001:U0001", ""},
		{"unknown opaque string resolves to empty", "no-such-id", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := resolver.Resolve(ctx, identityLookupRequest{UserID: tt.userID})
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.userID, err)
			}
			if tt.want == "" {
				if res.Context != nil {
					t.Errorf("Resolve(%q) = %v, want a nil context (no match)", tt.userID, res.Context)
				}
				if res.Outcome == identity.LookupMatched {
					t.Errorf("Resolve(%q) outcome = matched, want anything but", tt.userID)
				}
				return
			}
			if got, _ := res.Context["sam_user_id"].(string); got != tt.want {
				t.Errorf("Resolve(%q) sam_user_id = %q, want %q", tt.userID, got, tt.want)
			}
			if res.Outcome != identity.LookupMatched {
				t.Errorf("Resolve(%q) outcome = %q, want matched", tt.userID, res.Outcome)
			}
		})
	}
}
