// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokenbroker

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const savedGrantID = "0197b2c4-0000-7000-8000-0000000000aa"

func signedIn(subject, refreshToken string) *store.ModelOAuthGrant {
	return &store.ModelOAuthGrant{
		ID: savedGrantID, Name: "Example ID", Flow: store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer: "https://id.example.test/", TokenEndpoint: "https://id.example.test/oauth/token",
		ClientID: "desktop", Audience: "https://llm.example.test", Scopes: "openid offline_access",
		APIBase: testAPIBase, AccountSubject: subject, AccountLabel: subject + "@example.test",
		RefreshToken: refreshToken, AccessToken: "access-" + refreshToken,
	}
}

func modelNamesIt(used bool) InUseFunc {
	return func(context.Context) (bool, error) { return used, nil }
}

func TestSave_CreatesAnAbsentGrant(t *testing.T) {
	f := newFixture(t)

	if err := f.svc.Save(t.Context(), signedIn("user|1", "refresh-1"), "desktop", modelNamesIt(false)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	row := f.stored(t, savedGrantID)
	if row.AccountSubject != "user|1" || row.RefreshToken != "refresh-1" || row.Status != store.ModelOAuthGrantStatusActive {
		t.Errorf("stored %+v, want user|1's active grant with refresh-1", row)
	}
}

func TestSave_ASecondSignInBySameAccountKeepsTheGrantAndTakesItsTokens(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Save(t.Context(), signedIn("user|1", "refresh-1"), "desktop", modelNamesIt(false)); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := f.svc.repo.MarkReauthorizationRequired(t.Context(), savedGrantID, f.stored(t, savedGrantID).Version, lastErrorRefused); err != nil {
		t.Fatalf("mark reauthorization required: %v", err)
	}
	created := f.stored(t, savedGrantID).CreatedTime

	if err := f.svc.Save(t.Context(), signedIn("user|1", "refresh-2"), "desktop", modelNamesIt(true)); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	row := f.stored(t, savedGrantID)
	if row.RefreshToken != "refresh-2" || row.Status != store.ModelOAuthGrantStatusActive || row.CreatedTime != created {
		t.Errorf("stored %+v, want the same row, active again, with refresh-2", row)
	}
}

func TestSave_AnotherAccountReplacesAGrantNoModelNames(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Save(t.Context(), signedIn("user|1", "refresh-1"), "desktop", modelNamesIt(false)); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	if err := f.svc.Save(t.Context(), signedIn("user|2", "refresh-2"), "desktop", modelNamesIt(false)); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	if row := f.stored(t, savedGrantID); row.AccountSubject != "user|2" || row.RefreshToken != "refresh-2" {
		t.Errorf("stored %s with %s, want user|2's grant", row.AccountSubject, row.RefreshToken)
	}
}

func TestSave_NeverMovesAGrantAModelNamesToAnotherAccountOrProvider(t *testing.T) {
	cases := map[string]func(*store.ModelOAuthGrant){
		"another account":        func(g *store.ModelOAuthGrant) { g.AccountSubject = "user|2" },
		"another token endpoint": func(g *store.ModelOAuthGrant) { g.TokenEndpoint = "https://other.example.test/oauth/token" },
		"another API":            func(g *store.ModelOAuthGrant) { g.APIBase = "https://other-llm.example.test" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if err := f.svc.Save(t.Context(), signedIn("user|1", "refresh-1"), "desktop", modelNamesIt(false)); err != nil {
				t.Fatalf("first Save: %v", err)
			}
			moved := signedIn("user|1", "refresh-2")
			change(moved)

			if err := f.svc.Save(t.Context(), moved, "desktop", modelNamesIt(true)); !errors.Is(err, ErrGrantInUse) {
				t.Fatalf("Save = %v, want ErrGrantInUse", err)
			}
			if row := f.stored(t, savedGrantID); row.RefreshToken != "refresh-1" || row.AccountSubject != "user|1" {
				t.Errorf("stored %s with %s, want user|1's grant untouched", row.AccountSubject, row.RefreshToken)
			}
		})
	}
}

func TestSave_RefusesAGrantItCannotKeepSafe(t *testing.T) {
	f := newFixture(t)
	unsafe := signedIn("user|1", "refresh-1")
	unsafe.TokenEndpoint = "http://id.example.test/oauth/token"

	if err := f.svc.Save(t.Context(), unsafe, "desktop", modelNamesIt(false)); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("Save = %v, want ErrInvalidGrant", err)
	}
	if row, _ := f.svc.Get(t.Context(), savedGrantID); row != nil {
		t.Error("a refused grant was stored")
	}
}

func TestSave_TwoProcessesSavingOneSignInBothSucceed(t *testing.T) {
	f := newFixture(t)
	processes := []*Service{f.svc, f.newService()}
	errs := make([]error, len(processes))
	var wg sync.WaitGroup
	for i, svc := range processes {
		wg.Go(func() {
			errs[i] = svc.Save(t.Context(), signedIn("user|1", "refresh-1"), "desktop", modelNamesIt(false))
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("process %d: Save = %v, want success", i, err)
		}
	}
	if row := f.stored(t, savedGrantID); row.RefreshToken != "refresh-1" {
		t.Errorf("stored %s, want refresh-1", row.RefreshToken)
	}
}

func TestSave_ALateCopyOfASavedSignInKeepsTheRotatedTokens(t *testing.T) {
	f := newFixture(t)
	signIn := func() *store.ModelOAuthGrant {
		g := signedIn("user|1", "refresh-signin")
		g.TokenEndpoint, g.Issuer = f.idp.srv.URL+"/oauth/token", f.idp.srv.URL+"/"
		return g
	}
	if err := f.svc.Save(t.Context(), signIn(), "desktop", modelNamesIt(false)); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := f.svc.GrantToken(t.Context(), savedGrantID, ""); err != nil {
		t.Fatalf("GrantToken: %v", err)
	}
	rotated := f.stored(t, savedGrantID)
	if rotated.RefreshToken == "refresh-signin" {
		t.Fatal("the grant was not refreshed, so the test proves nothing")
	}

	if err := f.newService().Save(t.Context(), signIn(), "desktop", modelNamesIt(true)); err != nil {
		t.Fatalf("the late copy: Save = %v, want success", err)
	}

	if row := f.stored(t, savedGrantID); row.RefreshToken != rotated.RefreshToken || row.AccessToken != rotated.AccessToken || row.Version != rotated.Version {
		t.Errorf("stored %s at version %d, want the rotated %s left at version %d", row.RefreshToken, row.Version, rotated.RefreshToken, rotated.Version)
	}
}

func TestSave_AnotherAccountsSignInBeatsARefreshOfTheGrantItReplaces(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Save(t.Context(), signedIn("user|1", "refresh-1"), "desktop", modelNamesIt(false)); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	read := f.stored(t, savedGrantID).Version
	if won, err := f.svc.repo.ClaimRefresh(t.Context(), savedGrantID, read, f.now.UnixMilli()); err != nil || !won {
		t.Fatalf("ClaimRefresh = %v, %v", won, err)
	}

	if err := f.svc.Save(t.Context(), signedIn("user|2", "refresh-2"), "desktop", modelNamesIt(false)); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if won, err := f.svc.repo.SaveRefreshed(t.Context(), savedGrantID, read+1, store.ModelOAuthGrantTokens{RefreshToken: "refresh-late", AccessToken: "access-late"}); err != nil || won {
		t.Fatalf("user|1's refresh saved onto user|2's grant = %v, %v; want false", won, err)
	}

	if row := f.stored(t, savedGrantID); row.AccountSubject != "user|2" || row.RefreshToken != "refresh-2" {
		t.Errorf("stored %s with %s, want user|2's grant with its own tokens", row.AccountSubject, row.RefreshToken)
	}
}
