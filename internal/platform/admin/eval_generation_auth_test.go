// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func newEvalGenerationSigner(t *testing.T) *trust.Signer {
	t.Helper()
	km, err := trust.NewKeyManager("webui")
	if err != nil {
		t.Fatal(err)
	}
	return trust.NewSigner(km, "webui", time.Hour, 2*time.Hour)
}

func newEvalGenerationLookup(t *testing.T, composer *rbac.Composer) *AuthzLookupHandler {
	t.Helper()
	return NewAuthzLookupHandler(composer, nil, newTestBroker(t), AuthzLookupConfig{Namespace: "eval-gen-auth"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func evalGenerationClaims(t *testing.T, header string, signer *trust.Signer) *oidcauth.SATClaims {
	t.Helper()
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		t.Fatalf("header = %q, want a bearer token", header)
	}
	claims, err := oidcauth.ValidateSAMToken(token, signer)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	return claims
}

func TestEvalGenerationAuth_NoSignerSendsNoToken(t *testing.T) {
	header, err := NewEvalGenerationAuth(nil, nil).AuthHeader(t.Context(), "alice@example.org", "alice-id")
	if err != nil || header != "" {
		t.Fatalf("AuthHeader = %q, %v; want no header", header, err)
	}
}

func TestEvalGenerationAuth_NoLookupSignsTheUserIDsAlone(t *testing.T) {
	signer := newEvalGenerationSigner(t)
	header, err := NewEvalGenerationAuth(signer, nil).AuthHeader(t.Context(), "alice@example.org", "alice-id")
	if err != nil {
		t.Fatal(err)
	}
	claims := evalGenerationClaims(t, header, signer)
	if claims.Subject != "alice@example.org" || claims.PlatformUserID != "alice-id" || len(claims.Scopes) != 0 {
		t.Errorf("claims = %+v, want alice@example.org with platform user alice-id and no scopes", claims)
	}
}

func newEvalGenerationLookupWithUsers(t *testing.T) (*AuthzLookupHandler, string, *store.UsersRepo) {
	t.Helper()
	composer, usersRepo, _, platformUserID := dbComposerWithClaimMapping(t, "azure", financeAdminClaims)
	lookup := newEvalGenerationLookup(t, composer)
	lookup.SetUserIdentityResolver(newUsersRepoIdentityResolver(usersRepo, discardLogger()))
	return lookup, platformUserID, usersRepo
}

func seedEvalGenerationUser(t *testing.T, users *store.UsersRepo, sub, email, provisionedVia string) string {
	t.Helper()
	u := &store.User{Issuer: "iss", Sub: sub, OIDCProvider: "azure", Email: &email, ProvisionedVia: provisionedVia, CreatedBy: "x", UpdatedBy: "x"}
	if err := users.UpsertUser(t.Context(), u, nil); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestEvalGenerationAuth_SignsTheScopesOfTheUserWithThatPlatformID(t *testing.T) {
	signer := newEvalGenerationSigner(t)
	lookup, platformUserID, users := newEvalGenerationLookupWithUsers(t)
	const otherEmail = "someone-else@example.org"
	seedEvalGenerationUser(t, users, "someone-else", otherEmail, "")
	header, err := NewEvalGenerationAuth(signer, lookup).AuthHeader(t.Context(), otherEmail, platformUserID)
	if err != nil {
		t.Fatal(err)
	}
	claims := evalGenerationClaims(t, header, signer)
	if claims.PlatformUserID != platformUserID ||
		!slices.Contains(claims.Roles, claimGrantRole) || !slices.Contains(claims.Roles, claimMappedRole) {
		t.Errorf("claims = %+v, want the direct grant and claim-mapped role of platform user %s", claims, platformUserID)
	}
}

func TestEvalGenerationAuth_RefusesWithoutAResolvablePlatformID(t *testing.T) {
	lookup, _, users := newEvalGenerationLookupWithUsers(t)
	auth := NewEvalGenerationAuth(newEvalGenerationSigner(t), lookup)
	systemUserID := seedEvalGenerationUser(t, users, "system:bot", "bot@example.org", rbac.ProvisionedViaSystem)
	for name, platformUserID := range map[string]string{
		"no platform user id":         "",
		"an unknown platform user id": "00000000-0000-0000-0000-000000000000",
		"a system user's id":          systemUserID,
	} {
		t.Run(name, func(t *testing.T) {
			if header, err := auth.AuthHeader(t.Context(), claimUserEmail, platformUserID); err == nil || header != "" {
				t.Fatalf("AuthHeader = %q, %v; want an error and no header", header, err)
			}
		})
	}
	noStore := NewEvalGenerationAuth(newEvalGenerationSigner(t), newEvalGenerationLookup(t, newAuthzTestComposer(t)))
	if header, err := noStore.AuthHeader(t.Context(), "alice@example.org", "alice-id"); err == nil || header != "" {
		t.Fatalf("AuthHeader without a user store = %q, %v; want an error and no header", header, err)
	}
}

func TestResolveUserScopes_NoComposerIsAnError(t *testing.T) {
	var h *AuthzLookupHandler
	if _, err := h.ResolveUserScopes(t.Context(), "alice@example.org", "alice-id"); err == nil {
		t.Fatal("ResolveUserScopes on a nil handler returned no error")
	}
}

func TestEvalGenerationAuth_RefusesADeactivatedUser(t *testing.T) {
	db := testOpenDB(t)
	users := store.NewUsersRepo(db)
	const email = "alice@example.org"
	id := seedEvalGenerationUser(t, users, "alice", email, "")
	if _, err := db.ExecContext(t.Context(), `UPDATE users SET active = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	lookup := newEvalGenerationLookup(t, newAuthzTestComposer(t))
	lookup.SetUserIdentityResolver(newUsersRepoIdentityResolver(users, discardLogger()))
	if header, err := NewEvalGenerationAuth(newEvalGenerationSigner(t), lookup).AuthHeader(t.Context(), email, id); err == nil || header != "" {
		t.Fatalf("AuthHeader = %q, %v; want an error and no header", header, err)
	}
}
