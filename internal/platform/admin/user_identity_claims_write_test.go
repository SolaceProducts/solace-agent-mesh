// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func projectedClaims(source string) []store.UserIdentityClaim {
	rows, _ := projectIdentityClaims(source)
	return rows
}

func upsertWithClaims(t *testing.T, repo *store.UsersRepo, sub, source string) string {
	t.Helper()
	return upsertUserWithClaims(t, repo, &store.User{Sub: sub, Source: source})
}

func upsertUserWithClaims(t *testing.T, repo *store.UsersRepo, u *store.User) string {
	t.Helper()
	sub, source := u.Sub, u.Source
	u.Issuer = "https://idp.example.com"
	u.CreatedBy, u.UpdatedBy = "test", "test"
	write := store.IdentityClaimWrite{Replace: true, Claims: projectedClaims(source)}
	if err := repo.UpsertUserWithClaims(t.Context(), u, nil, write); err != nil {
		t.Fatalf("upsert %s: %v", sub, err)
	}
	if err := repo.CorroborateBindings(t.Context(), u.ID, write.Claims); err != nil {
		t.Fatalf("corroborate %s: %v", sub, err)
	}
	return u.ID
}

func claimValuesFor(t *testing.T, claimsRepo *store.UserIdentityClaimsRepo, userID string) []string {
	t.Helper()
	rows, err := claimsRepo.ClaimsForUser(t.Context(), userID)
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ClaimKey+"="+r.ClaimValueCanonical)
	}
	slices.Sort(out)
	return out
}

func TestReloginReplacesProjectionRatherThanMerging(t *testing.T) {
	db := testOpenDB(t)
	repo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)

	userID := upsertWithClaims(t, repo, "user-1",
		`{"phone_number":"+15551234567","phone_number_verified":true}`)
	if got, want := claimValuesFor(t, claimsRepo, userID), []string{"phone_number=15551234567"}; !slices.Equal(got, want) {
		t.Fatalf("after first login = %v, want %v", got, want)
	}

	reUserID := upsertWithClaims(t, repo, "user-1",
		`{"phone_number":"+15559998888","phone_number_verified":true}`)
	if reUserID != userID {
		t.Fatalf("re-login produced a different user id (%s != %s)", reUserID, userID)
	}
	got := claimValuesFor(t, claimsRepo, userID)
	if want := []string{"phone_number=15559998888"}; !slices.Equal(got, want) {
		t.Errorf("after re-login = %v, want %v — the old value must stop matching", got, want)
	}
}

func TestAWithdrawnClaimStopsBeingProjected(t *testing.T) {
	db := testOpenDB(t)
	repo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)

	userID := upsertWithClaims(t, repo, "user-2",
		`{"phone_number":"+15551234567","phone_number_verified":true}`)
	upsertWithClaims(t, repo, "user-2", `{"sub":"user-2"}`)

	if want := []string{"sub=user-2"}; !slices.Equal(claimValuesFor(t, claimsRepo, userID), want) {
		t.Errorf("projection after the claim was withdrawn = %v, want %v", claimValuesFor(t, claimsRepo, userID), want)
	}
}

func TestRecycledValueYieldsTwoHoldersRatherThanOne(t *testing.T) {
	db := testOpenDB(t)
	repo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)

	const source = `{"phone_number":"+15551234567","phone_number_verified":true}`
	first := upsertWithClaims(t, repo, "leaver", source)
	second := upsertWithClaims(t, repo, "joiner", source)

	ids, err := claimsRepo.UserIDsByClaim(t.Context(), ClaimKeyPhoneNumber, "15551234567")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("holders = %v, want both %s and %s — one holder means the value silently resolved to a single user", ids, first, second)
	}
}

func TestUnverifiedClaimCannotMakeAVerifiedHolderAmbiguous(t *testing.T) {
	db := testOpenDB(t)
	repo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)

	victim := upsertWithClaims(t, repo, "victim",
		`{"phone_number":"+15551234567","phone_number_verified":true}`)
	upsertWithClaims(t, repo, "impostor",
		`{"phone_number":"+15551234567","phone_number_verified":false}`)

	ids, err := claimsRepo.UserIDsByClaim(t.Context(), ClaimKeyPhoneNumber, "15551234567")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(ids) != 1 || ids[0] != victim {
		t.Errorf("holders = %v, want only the verified holder %s — an unverified claim must neither match nor create ambiguity", ids, victim)
	}
}
