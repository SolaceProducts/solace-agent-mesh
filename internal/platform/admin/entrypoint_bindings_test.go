// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func bindingFixture(t *testing.T) (*store.UsersRepo, *store.UserIdentityClaimsRepo, *store.EntrypointIdentityBindingsRepo, *usersRepoIdentityResolver) {
	t.Helper()
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)
	bindingsRepo := store.NewEntrypointIdentityBindingsRepo(db)
	resolver := newUsersRepoIdentityResolver(usersRepo, discardLogger()).
		withClaimProjection(claimsRepo).
		withContinuity(bindingsRepo)
	return usersRepo, claimsRepo, bindingsRepo, resolver
}

func phoneLookup(gatewayID, value, token string) identityLookupRequest {
	return identityLookupRequest{
		UserID:          "whatsapp:" + value,
		Key:             ClaimKeyPhoneNumber,
		Value:           value,
		GatewayID:       gatewayID,
		ContinuityToken: token,
	}
}

func TestContinuityFirstMessageBindsAndSecondIsStable(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)

	for attempt := range 2 {
		res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "15551234567"))
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if res.Outcome != identity.LookupMatched {
			t.Fatalf("attempt %d outcome = %q, want matched", attempt, res.Outcome)
		}
	}
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil || row == nil {
		t.Fatalf("binding = %v, err = %v; want a row", row, err)
	}
	if row.ContinuityToken != "15551234567" {
		t.Errorf("continuity token = %q, want the sender id", row.ContinuityToken)
	}
}

func TestContinuityRotationRefusesEveryLaterMessageToo(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)

	if res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.1111")); err != nil || res.Outcome != identity.LookupMatched {
		t.Fatalf("first lookup outcome = %q, err = %v; want matched", res.Outcome, err)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, "15551234567", userID, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	for attempt := range 3 {
		res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.2222"))
		if err != nil {
			t.Fatalf("rotated lookup %d: %v", attempt+1, err)
		}
		if res.Outcome != identity.LookupRefused {
			t.Fatalf("message %d outcome = %q, want refused — the value reached us from a sender it did not before", attempt+1, res.Outcome)
		}
		if res.RefusalReason != refusalSenderChanged {
			t.Errorf("message %d refusal reason = %q, want %q, which names the withhold", attempt+1, res.RefusalReason, refusalSenderChanged)
		}
		if res.UserID != "" {
			t.Fatalf("message %d resolved to user %q; a refused lookup must name nobody", attempt+1, res.UserID)
		}
	}

	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if row.UserID == nil || *row.UserID != userID {
		t.Errorf("binding user = %v after a rotation, want %q kept — it names who the value belonged to, which the reassignment check and the revocation both key on", row.UserID, userID)
	}
	if row.RotatedAt == nil {
		t.Error("binding carries no rotation marker, so the refusal lasts exactly one message")
	}
}

func TestLoginDoesNotSettleARotation(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.ALICE")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if res, _ := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.STRANGER")); res.Outcome != identity.LookupRefused {
		t.Fatalf("rotated outcome = %q, want refused", res.Outcome)
	}

	if got := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`); got != userID {
		t.Fatalf("re-login created a second user %q", got)
	}

	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.STRANGER"))
	if err != nil {
		t.Fatalf("post-login lookup: %v", err)
	}
	if res.Outcome != identity.LookupRefused {
		t.Fatalf("outcome = %q, want refused — alice's login says nothing about who US.STRANGER is", res.Outcome)
	}
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if row.RotatedAt == nil {
		t.Error("the login cleared the rotation marker")
	}
}

func TestRevocationLandsInTheRBACAuditStream(t *testing.T) {
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	usersSvc := NewUsersService(usersRepo, store.NewRBACRepo(db), logger)
	usersSvc.SetIdentityClaims(claimsRepo)
	usersSvc.SetIdentityBindings(store.NewEntrypointIdentityBindingsRepo(db))

	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	buf.Reset()

	resp, err := usersSvc.RevokeUserIdentityClaims(t.Context(), userID, "admin@example.com")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if resp.Revoked == 0 {
		t.Fatal("nothing was revoked; the audit assertion below would pass vacuously")
	}

	rec := findAuditLine(t, buf, auditActionDeleted, auditResourceIdentityClaims)
	if rec["resourceId"] != userID {
		t.Errorf("resourceId = %v, want the surrogate user id %s", rec["resourceId"], userID)
	}
	if rec["actor"] != "admin@example.com" {
		t.Errorf("actor = %v, want admin@example.com", rec["actor"])
	}
	if rec["revoked"] != float64(resp.Revoked) {
		t.Errorf("revoked = %v, want %d", rec["revoked"], resp.Revoked)
	}
	if s := buf.String(); strings.Contains(s, "5551234567") {
		t.Error("the audit line carries the revoked phone number")
	}
}

func TestRevokingTheHolderLetsTheValueStartOver(t *testing.T) {
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)
	bindingsRepo := store.NewEntrypointIdentityBindingsRepo(db)
	resolver := newUsersRepoIdentityResolver(usersRepo, discardLogger()).
		withClaimProjection(claimsRepo).
		withContinuity(bindingsRepo)
	usersSvc := NewUsersService(usersRepo, store.NewRBACRepo(db), discardLogger())
	usersSvc.SetIdentityClaims(claimsRepo)
	usersSvc.SetIdentityBindings(bindingsRepo)

	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.OLD-PHONE")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if res, _ := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.NEW-PHONE")); res.Outcome != identity.LookupRefused {
		t.Fatalf("rotated outcome = %q, want refused", res.Outcome)
	}

	resp, err := usersSvc.RevokeUserIdentityClaims(t.Context(), userID, "admin@example.com")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if resp.BindingsReset != 1 {
		t.Errorf("bindingsReset = %d, want 1 — the withheld binding must go with the projection", resp.BindingsReset)
	}
	if row, _ := bindingsRepo.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567"); row != nil {
		t.Fatalf("binding survived the revocation: %+v", row)
	}
	if res, _ := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.NEW-PHONE")); res.Outcome != identity.LookupNoMatch {
		t.Fatalf("post-revoke outcome = %q, want no_match", res.Outcome)
	}

	if got := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`); got != userID {
		t.Fatalf("re-login created a second user %q", got)
	}
	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.NEW-PHONE"))
	if err != nil {
		t.Fatalf("post-re-enrol lookup: %v", err)
	}
	if res.Outcome != identity.LookupMatched {
		t.Errorf("outcome = %q, want matched — revoke and re-enrol is the documented remedy", res.Outcome)
	}
}

func TestDirectoryNamingANewHolderEndsTheWithhold(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	alice := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.ALICE")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if res, _ := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.BOB")); res.Outcome != identity.LookupRefused {
		t.Fatalf("rotated outcome = %q, want refused while the directory still names alice", res.Outcome)
	}

	upsertWithClaims(t, usersRepo, "alice", `{"email":"alice@example.com"}`)
	bob := upsertWithClaims(t, usersRepo, "bob", `{"phone_number":"+15551234567","phone_number_verified":true}`)

	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.BOB"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Outcome != identity.LookupMatched || res.UserID != bob {
		t.Fatalf("outcome = %q user = %q, want matched as bob %q", res.Outcome, res.UserID, bob)
	}
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil || row == nil {
		t.Fatalf("binding = %v, err = %v", row, err)
	}
	if row.RotatedAt != nil || row.UserID == nil || *row.UserID != bob || row.ContinuityToken != "US.BOB" {
		t.Errorf("binding = %+v, want re-seated on bob with his token and no rotation marker", row)
	}
	if *row.UserID == alice {
		t.Error("binding still names alice")
	}
	if res, _ := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.ALICE")); res.Outcome != identity.LookupRefused {
		t.Errorf("the previous holder's account presenting the reassigned number = %q, want refused", res.Outcome)
	}
}

func TestAbsentTokenDoesNotRotate(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.ALICE")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", ""))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Outcome != identity.LookupMatched {
		t.Fatalf("outcome = %q, want matched — an absent id is no evidence of a different sender", res.Outcome)
	}
	row, _ := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if row.ContinuityToken != "US.ALICE" || row.RotatedAt != nil {
		t.Errorf("binding = %+v, want the stored token kept and no rotation", row)
	}
	if res, _ := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.ALICE")); res.Outcome != identity.LookupMatched {
		t.Errorf("follow-up with the id = %q, want matched", res.Outcome)
	}
}

func TestTokenlessBindingAdoptsTheFirstToken(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.ALICE"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Outcome != identity.LookupMatched {
		t.Fatalf("outcome = %q, want matched", res.Outcome)
	}
	row, _ := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if row.ContinuityToken != "US.ALICE" || row.RotatedAt != nil {
		t.Errorf("binding = %+v, want the first token adopted without a rotation", row)
	}
	if res, _ := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.STRANGER")); res.Outcome != identity.LookupRefused {
		t.Errorf("a different id after adoption = %q, want refused", res.Outcome)
	}
}

func TestAValueHeldByAnotherUserIsRefusedWithNoToken(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	alice := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15559999999","phone_number_verified":true}`)
	upsertWithClaims(t, usersRepo, "bob", `{"phone_number":"+15551234567","phone_number_verified":true}`)

	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, "15551234567", alice, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", ""))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Outcome != identity.LookupRefused {
		t.Errorf("outcome = %q, want refused — this value was corroborated by somebody else", res.Outcome)
	}
}

func TestContinuityIsScopedPerEntrypoint(t *testing.T) {
	usersRepo, _, _, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)

	if res, _ := resolver.Resolve(t.Context(), phoneLookup("gw-a", "15551234567", "US.AAA")); res.Outcome != identity.LookupMatched {
		t.Fatalf("gw-a outcome = %q, want matched", res.Outcome)
	}
	res, err := resolver.Resolve(t.Context(), phoneLookup("gw-b", "15551234567", "US.BBB"))
	if err != nil {
		t.Fatalf("gw-b: %v", err)
	}
	if res.Outcome != identity.LookupMatched {
		t.Errorf("gw-b outcome = %q, want matched — a second entrypoint is not a rotation of the first", res.Outcome)
	}
}

func TestUnmatchedValueCreatesNoBinding(t *testing.T) {
	_, _, bindings, resolver := bindingFixture(t)

	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15559999999", "15559999999"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Outcome != identity.LookupNoMatch {
		t.Fatalf("outcome = %q, want no_match", res.Outcome)
	}
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15559999999")
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if row != nil {
		t.Error("a stranger left a binding row behind")
	}
}

func TestLoginCorroboratesAWaitingBinding(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.1111")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)

	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil || row == nil {
		t.Fatalf("binding = %v, err = %v", row, err)
	}
	if row.UserID == nil || *row.UserID != userID {
		t.Errorf("binding user = %v, want %q — the login should have corroborated it", row.UserID, userID)
	}
}

func TestUnverifiedClaimDoesNotCorroborate(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.1111")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if err := clearBindingUser(t, bindings); err != nil {
		t.Fatalf("reset binding: %v", err)
	}

	impostor := upsertWithClaims(t, usersRepo, "impostor", `{"phone_number":"+15551234567","phone_number_verified":false}`)

	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil || row == nil {
		t.Fatalf("binding = %v, err = %v", row, err)
	}
	if row.UserID != nil && *row.UserID == impostor {
		t.Errorf("an unverified claim corroborated the binding to %q", impostor)
	}
}

func clearBindingUser(t *testing.T, bindings *store.EntrypointIdentityBindingsRepo) error {
	t.Helper()
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil {
		return err
	}
	if row == nil {
		t.Fatal("no binding to reset")
	}
	if row.UserID != nil {
		t.Fatalf("a freshly observed binding already names user %q; it must await a login", *row.UserID)
	}
	return nil
}

func TestRebindKeepsTheCorroboratedUser(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.old")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, "15551234567", userID, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	rebound, err := bindings.Rebind(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567", store.TokenRotation{Predecessor: "US.old", Successor: "US.new"}, 2)
	if err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	if !rebound {
		t.Fatal("Rebind reported no change against the token it was told to replace")
	}
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil || row == nil {
		t.Fatalf("binding = %v, err = %v", row, err)
	}
	if row.ContinuityToken != "US.new" {
		t.Errorf("continuity token = %q, want the new id", row.ContinuityToken)
	}
	if row.UserID == nil || *row.UserID != userID {
		t.Errorf("binding user = %v, want %q kept — a rotation the customer did not cause must not re-prompt them", row.UserID, userID)
	}
}

func TestRebindIsConditionalOnThePredecessor(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.current")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	rebound, err := bindings.Rebind(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567", store.TokenRotation{Predecessor: "US.stale", Successor: "US.new"}, 2)
	if err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	if rebound {
		t.Error("a rebind applied against a predecessor that is no longer the stored token")
	}
	row, _ := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if row.ContinuityToken != "US.current" {
		t.Errorf("continuity token = %q, want the untouched current id", row.ContinuityToken)
	}
}

func TestRebindIgnoresANonRotation(t *testing.T) {
	_, _, bindings, _ := bindingFixture(t)

	for _, tc := range []struct{ name, predecessor, token string }{
		{"no predecessor", "", "US.new"},
		{"no new token", "US.old", ""},
		{"same token", "US.same", "US.same"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rebound, err := bindings.Rebind(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567", store.TokenRotation{Predecessor: tc.predecessor, Successor: tc.token}, 2)
			if err != nil {
				t.Fatalf("Rebind: %v", err)
			}
			if rebound {
				t.Error("a non-rotation reported as a rebind")
			}
		})
	}
}

func TestDiscardTombstonesTheBinding(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.old")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, "15551234567", userID, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	applied, err := bindings.Discard(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567", userID, 5)
	if err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if !applied {
		t.Fatal("Discard reported no row to tombstone")
	}
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row == nil {
		t.Fatal("binding was deleted; a deleted row reads as first-seen and is trusted")
	}
	if row.RotatedAt == nil {
		t.Errorf("binding = %+v, want a tombstone with the rotation marker set", row)
	}
	if row.UserID == nil || *row.UserID != userID {
		t.Errorf("binding user = %v, want %q kept so the revocation can find the row", row.UserID, userID)
	}
}

func TestDeleteForGatewayRemovesOnlyThatEntrypointsBindings(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	for _, gw := range []string{"gw-a", "gw-b"} {
		if _, err := resolver.Resolve(t.Context(), phoneLookup(gw, "15551234567", "US."+gw)); err != nil {
			t.Fatalf("seed %s: %v", gw, err)
		}
	}

	removed, err := bindings.DeleteForGateway(t.Context(), "gw-a")
	if err != nil {
		t.Fatalf("DeleteForGateway: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if row, _ := bindings.Get(t.Context(), "gw-a", ClaimKeyPhoneNumber, "15551234567"); row != nil {
		t.Error("the deleted entrypoint's binding survived")
	}
	if row, _ := bindings.Get(t.Context(), "gw-b", ClaimKeyPhoneNumber, "15551234567"); row == nil {
		t.Error("another entrypoint's binding was taken with it")
	}
}

func TestResolverAppliesAProviderReportedRebind(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.old")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, "15551234567", userID, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	req := phoneLookup("wa-gw", "15551234567", "US.new")
	req.PredecessorToken = "US.old"
	res, err := resolver.Resolve(t.Context(), req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Outcome != identity.LookupMatched {
		t.Errorf("outcome = %q, want matched — a rotation the customer did not cause must not refuse them", res.Outcome)
	}
}

func TestResolverAppliesAProviderReportedDiscard(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", "15551234567", "US.old")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	req := phoneLookup("wa-gw", "15551234567", "US.old")
	req.DiscardBinding = true
	res, err := resolver.Resolve(t.Context(), req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Outcome != identity.LookupRefused {
		t.Errorf("outcome = %q, want refused — the provider says this value moved", res.Outcome)
	}
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, "15551234567")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row == nil || row.RotatedAt == nil {
		t.Errorf("binding = %+v, want a tombstone after a reported number change", row)
	}

	next := phoneLookup("wa-gw", "15551234567", "US.brand-new")
	if res, err := resolver.Resolve(t.Context(), next); err != nil || res.Outcome != identity.LookupRefused {
		t.Errorf("follow-up outcome = %q, err = %v; want refused", res.Outcome, err)
	}
}

func TestABareWhatsAppPrincipalResolvesThroughTheProjection(t *testing.T) {
	usersRepo, _, _, resolver := bindingFixture(t)
	userID := upsertWithClaims(t, usersRepo, "alice", `{"phone_number":"+15551234567","phone_number_verified":true}`)

	res, err := resolver.Resolve(t.Context(), identityLookupRequest{UserID: "whatsapp:15551234567"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Outcome != identity.LookupMatched {
		t.Fatalf("outcome = %q, want matched", res.Outcome)
	}
	if res.UserID != userID {
		t.Errorf("resolved user = %q, want %q", res.UserID, userID)
	}
}

func TestABareWhatsAppPrincipalNobodyHoldsIsAMiss(t *testing.T) {
	_, _, _, resolver := bindingFixture(t)

	res, err := resolver.Resolve(t.Context(), identityLookupRequest{UserID: "whatsapp:15559999999"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Outcome != identity.LookupNoMatch {
		t.Errorf("outcome = %q, want no_match", res.Outcome)
	}
}

func TestABindingFaultLeavesTheUserRowIntact(t *testing.T) {
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)

	if _, err := db.ExecContext(t.Context(), `DROP TABLE entrypoint_identity_bindings`); err != nil {
		t.Fatalf("drop bindings table: %v", err)
	}

	const source = `{"phone_number":"+15551234567","phone_number_verified":true}`
	claims := store.IdentityClaimWrite{Replace: true, Claims: projectedClaims(source)}
	user := &store.User{
		Issuer: "https://idp.example.com", Sub: "binding-fault",
		Source: source, CreatedBy: "test", UpdatedBy: "test",
	}
	if err := usersRepo.UpsertUserWithClaims(t.Context(), user, []string{"engineering"}, claims); err != nil {
		t.Fatalf("the user upsert failed because of the bindings table: %v — "+
			"a fault in continuity data must not cost the user their directory row", err)
	}

	stored, _, err := usersRepo.GetByID(t.Context(), user.ID)
	if err != nil || stored == nil {
		t.Fatalf("user row absent after a binding fault (err=%v); this is the invisible-user report", err)
	}
	rows, err := claimsRepo.ClaimsForUser(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	if len(rows) == 0 {
		t.Error("the claim projection was rolled back with the binding; the user would resolve as no_match")
	}

	if err := usersRepo.CorroborateBindings(t.Context(), user.ID, claims.Claims); err == nil {
		t.Error("CorroborateBindings reported success against a dropped table; a real fault would never be logged")
	}
}
