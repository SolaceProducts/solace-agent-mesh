// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func createTestGrant(t *testing.T, repo *ModelOAuthGrantRepo) *ModelOAuthGrant {
	t.Helper()
	s := &ModelOAuthGrant{
		Name:                   "Example ID",
		Flow:                   ModelOAuthGrantFlowAuthorizationCode,
		Issuer:                 "https://id.example.com/",
		TokenEndpoint:          "https://id.example.com/oauth/token",
		ClientID:               "desktop-client",
		Audience:               "https://llm.example.com",
		APIBase:                "https://llm.example.com",
		AccountSubject:         "user|123",
		AccountLabel:           "user@example.com",
		RefreshToken:           "refresh-1",
		AccessToken:            "access-1",
		AccessTokenExpiresTime: 1_000,
	}
	if err := repo.Create(t.Context(), s, "desktop"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s
}

func mustGetGrant(ctx context.Context, t *testing.T, repo *ModelOAuthGrantRepo, id string) *ModelOAuthGrant {
	t.Helper()
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: grant missing")
	}
	return got
}

func TestModelGrantRepo_CreateStartsActiveAtVersionOne(t *testing.T) {
	db := TestDB(t)
	repo := NewModelOAuthGrantRepo(db)
	created := createTestGrant(t, repo)

	got := mustGetGrant(t.Context(), t, repo, created.ID)
	if got.Status != ModelOAuthGrantStatusActive || got.Version != 1 {
		t.Errorf("status, version = %q, %d; want active, 1", got.Status, got.Version)
	}
	if got.RefreshToken != "refresh-1" || got.AccessToken != "access-1" || got.AccessTokenExpiresTime != 1_000 {
		t.Errorf("tokens did not round-trip: %+v", got)
	}
	if got.AccountSubject != "user|123" || got.Audience != "https://llm.example.com" || got.APIBase != "https://llm.example.com" || got.CreatedBy != "desktop" {
		t.Errorf("columns did not round-trip: %+v", got)
	}
}

func TestModelGrantRepo_GetAbsentReturnsNil(t *testing.T) {
	db := TestDB(t)
	got, err := NewModelOAuthGrantRepo(db).Get(t.Context(), "00000000-0000-0000-0000-000000000001")
	if err != nil || got != nil {
		t.Fatalf("Get absent = %+v, %v; want nil, nil", got, err)
	}
}

func TestModelGrantRepo_SaveRefreshedOnlyAtTheVersionRead(t *testing.T) {
	db := TestDB(t)
	repo := NewModelOAuthGrantRepo(db)
	ctx := t.Context()
	s := createTestGrant(t, repo)

	won, err := repo.SaveRefreshed(ctx, s.ID, 1, ModelOAuthGrantTokens{RefreshToken: "refresh-2", AccessToken: "access-2", AccessTokenExpiresTime: 2_000})
	if err != nil || !won {
		t.Fatalf("first SaveRefreshed = %v, %v; want true", won, err)
	}
	won, err = repo.SaveRefreshed(ctx, s.ID, 1, ModelOAuthGrantTokens{RefreshToken: "refresh-3", AccessToken: "access-3", AccessTokenExpiresTime: 3_000})
	if err != nil || won {
		t.Fatalf("SaveRefreshed at a stale version = %v, %v; want false", won, err)
	}

	got := mustGetGrant(ctx, t, repo, s.ID)
	if got.RefreshToken != "refresh-2" || got.AccessToken != "access-2" || got.Version != 2 {
		t.Errorf("stored = %q/%q v%d; want the first refresh's tokens at version 2", got.RefreshToken, got.AccessToken, got.Version)
	}
	if got.UpdatedBy != ModelOAuthGrantRefresher {
		t.Errorf("updated_by = %q, want %q", got.UpdatedBy, ModelOAuthGrantRefresher)
	}
}

func TestModelGrantRepo_MarkGrantRequiredLosesToALaterWrite(t *testing.T) {
	db := TestDB(t)
	repo := NewModelOAuthGrantRepo(db)
	ctx := t.Context()
	s := createTestGrant(t, repo)

	if won, err := repo.SaveRefreshed(ctx, s.ID, 1, ModelOAuthGrantTokens{RefreshToken: "refresh-2", AccessToken: "access-2", AccessTokenExpiresTime: 2_000}); err != nil || !won {
		t.Fatalf("SaveRefreshed = %v, %v", won, err)
	}
	if won, err := repo.MarkReauthorizationRequired(ctx, s.ID, 1, "invalid_grant"); err != nil || won {
		t.Fatalf("MarkReauthorizationRequired at a stale version = %v, %v; want false", won, err)
	}
	if got := mustGetGrant(ctx, t, repo, s.ID); got.Status != ModelOAuthGrantStatusActive {
		t.Errorf("status = %q; a refused stale refresh must not mark a refreshed grant", got.Status)
	}

	if won, err := repo.MarkReauthorizationRequired(ctx, s.ID, 2, "invalid_grant"); err != nil || !won {
		t.Fatalf("MarkReauthorizationRequired = %v, %v; want true", won, err)
	}
	got := mustGetGrant(ctx, t, repo, s.ID)
	if got.Status != ModelOAuthGrantStatusReauthorizationRequired || got.LastError != "invalid_grant" || got.AccessToken != "" || got.Version != 3 {
		t.Errorf("after mark = status %q, error %q, access %q, v%d", got.Status, got.LastError, got.AccessToken, got.Version)
	}
}

func TestModelGrantRepo_ReplaceReactivatesAndBeatsAnInFlightRefresh(t *testing.T) {
	db := TestDB(t)
	repo := NewModelOAuthGrantRepo(db)
	ctx := t.Context()
	s := createTestGrant(t, repo)
	if won, err := repo.MarkReauthorizationRequired(ctx, s.ID, 1, "invalid_grant"); err != nil || !won {
		t.Fatalf("MarkReauthorizationRequired = %v, %v", won, err)
	}

	found, err := repo.Replace(ctx, s.ID, ModelOAuthGrantTokens{RefreshToken: "refresh-new", AccessToken: "access-new", AccessTokenExpiresTime: 9_000}, "sign-in-new", "renamed@example.com", "desktop")
	if err != nil || !found {
		t.Fatalf("Replace = %v, %v; want true", found, err)
	}
	if won, err := repo.SaveRefreshed(ctx, s.ID, 2, ModelOAuthGrantTokens{RefreshToken: "refresh-late", AccessToken: "access-late"}); err != nil || won {
		t.Fatalf("a refresh that read the version before Replace saved = %v, %v; want false", won, err)
	}

	got := mustGetGrant(ctx, t, repo, s.ID)
	if got.Status != ModelOAuthGrantStatusActive || got.LastError != "" || got.RefreshToken != "refresh-new" || got.SignInFingerprint != "sign-in-new" || got.AccountLabel != "renamed@example.com" || got.Version != 3 {
		t.Errorf("after Replace = %+v", got)
	}
}

func TestModelGrantRepo_OverwriteBeatsARefreshOfTheGrantItReplaced(t *testing.T) {
	db := TestDB(t)
	repo := NewModelOAuthGrantRepo(db)
	ctx := t.Context()
	s := createTestGrant(t, repo)
	if won, err := repo.ClaimRefresh(ctx, s.ID, 1, 5_000); err != nil || !won {
		t.Fatalf("ClaimRefresh = %v, %v", won, err)
	}

	other := *s
	other.AccountSubject, other.AccountLabel = "user|456", "other@example.com"
	other.TokenEndpoint, other.APIBase = "https://id.other.example/oauth/token", "https://llm.other.example"
	other.RefreshToken, other.AccessToken, other.SignInFingerprint = "refresh-other", "access-other", "sign-in-other"
	found, err := repo.Overwrite(ctx, &other, "desktop")
	if err != nil || !found {
		t.Fatalf("Overwrite = %v, %v; want true", found, err)
	}
	won, err := repo.SaveRefreshed(ctx, s.ID, 2, ModelOAuthGrantTokens{RefreshToken: "refresh-late", AccessToken: "access-late"})
	if err != nil || won {
		t.Fatalf("the replaced grant's refresh saved = %v, %v; want false", won, err)
	}

	got := mustGetGrant(ctx, t, repo, s.ID)
	if got.AccountSubject != "user|456" || got.TokenEndpoint != other.TokenEndpoint || got.APIBase != other.APIBase ||
		got.RefreshToken != "refresh-other" || got.SignInFingerprint != "sign-in-other" ||
		got.Version != 3 || got.RefreshStartedTime != 0 || got.CreatedTime != s.CreatedTime {
		t.Errorf("after Overwrite = %+v", got)
	}

	absent := other
	absent.ID = "0197b2c4-0000-7000-8000-00000000dead"
	if found, err = repo.Overwrite(ctx, &absent, "desktop"); err != nil || found {
		t.Fatalf("Overwrite of an absent grant = %v, %v; want false", found, err)
	}
}

func TestModelGrantRepo_ReplaceAndDeleteOfAnAbsentGrant(t *testing.T) {
	db := TestDB(t)
	repo := NewModelOAuthGrantRepo(db)
	ctx := t.Context()
	s := createTestGrant(t, repo)

	if err := repo.Delete(ctx, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(ctx, s.ID); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	found, err := repo.Replace(ctx, s.ID, ModelOAuthGrantTokens{RefreshToken: "r"}, "sign-in", "", "desktop")
	if err != nil || found {
		t.Fatalf("Replace of a deleted grant = %v, %v; want false", found, err)
	}
}

func TestModelGrantRepo_OneProcessClaimsARefreshAndItsEndClearsTheClaim(t *testing.T) {
	db := TestDB(t)
	repo := NewModelOAuthGrantRepo(db)
	ctx := t.Context()
	s := createTestGrant(t, repo)

	if claimed, err := repo.ClaimRefresh(ctx, s.ID, 1, 5_000); err != nil || !claimed {
		t.Fatalf("first ClaimRefresh = %v, %v; want true", claimed, err)
	}
	if claimed, err := repo.ClaimRefresh(ctx, s.ID, 1, 6_000); err != nil || claimed {
		t.Fatalf("second ClaimRefresh at the same version = %v, %v; want false", claimed, err)
	}
	if got := mustGetGrant(ctx, t, repo, s.ID); got.RefreshStartedTime != 5_000 || got.Version != 2 {
		t.Fatalf("claimed = started %d v%d; want started 5000 at version 2", got.RefreshStartedTime, got.Version)
	}

	if err := repo.ReleaseRefresh(ctx, s.ID, 2); err != nil {
		t.Fatalf("ReleaseRefresh: %v", err)
	}
	got := mustGetGrant(ctx, t, repo, s.ID)
	if got.RefreshStartedTime != 0 || got.Version != 3 || got.RefreshToken != "refresh-1" {
		t.Errorf("released = started %d v%d refresh %q; want no claim at version 3 and the tokens untouched", got.RefreshStartedTime, got.Version, got.RefreshToken)
	}
}
