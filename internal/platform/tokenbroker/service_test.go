// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokenbroker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type fakeIDP struct {
	srv   *httptest.Server
	calls atomic.Int32

	mu       sync.Mutex
	issued   int
	refused  map[string]bool
	failWith int
	hold     chan struct{}
	arrived  chan struct{}
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	idp := &fakeIDP{refused: map[string]bool{}}
	idp.srv = httptest.NewServer(http.HandlerFunc(idp.handle))
	t.Cleanup(idp.srv.Close)
	return idp
}

func (f *fakeIDP) handle(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "refresh_token" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	hold, arrived := f.hold, f.arrived
	f.mu.Unlock()
	if hold != nil {
		arrived <- struct{}{}
		<-hold
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.failWith != 0 {
		w.WriteHeader(f.failWith)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
		return
	}
	if f.refused[r.PostForm.Get("refresh_token")] {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	f.issued++
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  fmt.Sprintf("access-%d", f.issued),
		"refresh_token": fmt.Sprintf("refresh-%d", f.issued),
		"expires_in":    3600,
	})
}

func (f *fakeIDP) fail(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = status
}

func (f *fakeIDP) refuse(refreshToken string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refused[refreshToken] = true
}

func (f *fakeIDP) holdRefreshes(t *testing.T) (arrived <-chan struct{}, release func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hold = make(chan struct{})
	f.arrived = make(chan struct{}, 16)
	hold := f.hold
	release = sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	return f.arrived, release
}

type fixture struct {
	db  *store.DB
	idp *fakeIDP
	svc *Service
	now time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{db: store.TestDB(t), idp: newFakeIDP(t), now: time.Now()}
	f.svc = f.newService()
	return f
}

func (f *fixture) newService() *Service {
	return New(f.db, nil, WithHTTPClient(f.idp.srv.Client()), WithClock(func() time.Time { return f.now }), WithLoopbackHTTP())
}

func (f *fixture) createGrant(t *testing.T, expiresIn time.Duration) *store.ModelOAuthGrant {
	t.Helper()
	s := &store.ModelOAuthGrant{
		Name:                   "Example ID",
		Flow:                   store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer:                 f.idp.srv.URL + "/",
		TokenEndpoint:          f.idp.srv.URL + "/oauth/token",
		ClientID:               "desktop",
		APIBase:                testAPIBase,
		AccountSubject:         "user|1",
		RefreshToken:           "refresh-0",
		AccessToken:            "access-0",
		AccessTokenExpiresTime: f.now.Add(expiresIn).UnixMilli(),
	}
	if err := f.svc.Create(t.Context(), s, "desktop"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s
}

func (f *fixture) stored(t *testing.T, id string) *store.ModelOAuthGrant {
	t.Helper()
	row, err := f.svc.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return row
}

func TestGrantToken_HandsOutTheStoredTokenWhileItHasTimeLeft(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, tokenReuseMargin+time.Minute)

	token, err := f.svc.GrantToken(t.Context(), s.ID, "")
	if err != nil {
		t.Fatalf("GrantToken: %v", err)
	}
	if token.AccessToken != "access-0" || f.idp.calls.Load() != 0 {
		t.Errorf("token = %q after %d refreshes; want the stored one, no refresh", token.AccessToken, f.idp.calls.Load())
	}
}

func TestGrantToken_RefreshesWhenDueAndStoresTheRotatedToken(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, tokenReuseMargin-time.Minute)

	token, err := f.svc.GrantToken(t.Context(), s.ID, "")
	if err != nil {
		t.Fatalf("GrantToken: %v", err)
	}
	if token.AccessToken != "access-1" {
		t.Errorf("token = %q, want the refreshed one", token.AccessToken)
	}
	row := f.stored(t, s.ID)
	if row.RefreshToken != "refresh-1" || row.AccessToken != "access-1" || row.Version != 3 || row.RefreshStartedTime != 0 {
		t.Errorf("stored = %q/%q v%d started %d; want the rotated pair at version 3 (claimed, then saved) with the refresh ended",
			row.RefreshToken, row.AccessToken, row.Version, row.RefreshStartedTime)
	}
}

func TestGrantToken_CallersInOneProcessShareARefresh(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	arrived, release := f.idp.holdRefreshes(t)

	f.svc.onClaimWait = func() {
		t.Error("a caller waited on its own process's claim instead of sharing the refresh")
	}

	const callers = 8
	tokens := make(chan string, callers)
	var wg, entered sync.WaitGroup
	entered.Add(callers)
	for range callers {
		wg.Go(func() {
			entered.Done()
			token, err := f.svc.GrantToken(t.Context(), s.ID, "")
			if err != nil {
				t.Errorf("GrantToken: %v", err)
			}
			tokens <- token.AccessToken
		})
	}
	<-arrived
	entered.Wait()
	release()
	wg.Wait()
	close(tokens)

	if got := f.idp.calls.Load(); got != 1 {
		t.Errorf("refreshes = %d, want 1 shared by every caller", got)
	}
	for token := range tokens {
		if token != "access-1" {
			t.Errorf("a caller got %q, want access-1", token)
		}
	}
}

func TestGrantToken_TwoProcessesRefreshAtTheProviderOnce(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	other := f.newService()
	waiting := make(chan struct{}, 1)
	other.onClaimWait = func() { waiting <- struct{}{} }
	arrived, release := f.idp.holdRefreshes(t)

	results := make(chan string, 2)
	var wg sync.WaitGroup
	fetch := func(svc *Service) {
		wg.Go(func() {
			token, err := svc.GrantToken(t.Context(), s.ID, "")
			if err != nil {
				t.Errorf("GrantToken: %v", err)
			}
			results <- token.AccessToken
		})
	}
	fetch(f.svc)
	<-arrived
	fetch(other)
	select {
	case <-waiting:
	case <-arrived:
		t.Error("the second process presented the refresh token while the first held the claim")
	case <-time.After(5 * time.Second):
		t.Fatal("the second process neither waited on the claim nor refreshed")
	}
	release()
	wg.Wait()
	close(results)

	if calls := f.idp.calls.Load(); calls != 1 {
		t.Errorf("the provider saw %d refreshes, want 1: only the process holding the claim may present the refresh token", calls)
	}
	row := f.stored(t, s.ID)
	for token := range results {
		if token != row.AccessToken {
			t.Errorf("a process handed out %q, but the stored token is %q", token, row.AccessToken)
		}
	}
}

func TestGrantToken_AProcessWaitsForTheRefreshAnotherClaimed(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	repo := store.NewModelOAuthGrantRepo(f.db)
	if claimed, err := repo.ClaimRefresh(t.Context(), s.ID, s.Version, f.now.UnixMilli()); err != nil || !claimed {
		t.Fatalf("ClaimRefresh = %v, %v", claimed, err)
	}

	waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := f.svc.refreshGrant(waitCtx, s.ID, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want to still be waiting on the other process's refresh", err)
	}

	saved := store.ModelOAuthGrantTokens{RefreshToken: "refresh-other", AccessToken: "access-other", AccessTokenExpiresTime: f.now.Add(time.Hour).UnixMilli()}
	if won, err := repo.SaveRefreshed(t.Context(), s.ID, s.Version+1, saved); err != nil || !won {
		t.Fatalf("SaveRefreshed = %v, %v", won, err)
	}
	token, err := f.svc.GrantToken(t.Context(), s.ID, "")
	if err != nil {
		t.Fatalf("GrantToken: %v", err)
	}
	if token.AccessToken != "access-other" || f.idp.calls.Load() != 0 {
		t.Errorf("token = %q after %d refreshes; want the other process's token and no refresh of our own", token.AccessToken, f.idp.calls.Load())
	}
}

func TestGrantToken_TheClaimOfAStoppedProcessIsTakenOver(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	repo := store.NewModelOAuthGrantRepo(f.db)
	startedLongAgo := f.now.Add(-staleRefreshClaim - time.Second).UnixMilli()
	if claimed, err := repo.ClaimRefresh(t.Context(), s.ID, s.Version, startedLongAgo); err != nil || !claimed {
		t.Fatalf("ClaimRefresh = %v, %v", claimed, err)
	}

	token, err := f.svc.GrantToken(t.Context(), s.ID, "")
	if err != nil {
		t.Fatalf("GrantToken: %v", err)
	}
	if token.AccessToken != "access-1" || f.idp.calls.Load() != 1 {
		t.Errorf("token = %q after %d refreshes; want a refresh of our own", token.AccessToken, f.idp.calls.Load())
	}
	if row := f.stored(t, s.ID); row.RefreshStartedTime != 0 {
		t.Errorf("refresh_started_time = %d, want the refresh ended", row.RefreshStartedTime)
	}
}

func TestGrantToken_ARefusedRefreshNeedsTheUserToGrantAgain(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	f.idp.refuse("refresh-0")

	_, err := f.svc.GrantToken(t.Context(), s.ID, "")
	if !errors.Is(err, llm.ErrReauthorizationRequired) {
		t.Fatalf("err = %v, want ErrReauthorizationRequired", err)
	}
	row := f.stored(t, s.ID)
	if row.Status != store.ModelOAuthGrantStatusReauthorizationRequired || row.LastError != lastErrorRefused {
		t.Errorf("status, last_error = %q, %q", row.Status, row.LastError)
	}

	callsBefore := f.idp.calls.Load()
	if _, err := f.svc.GrantToken(t.Context(), s.ID, ""); !errors.Is(err, llm.ErrReauthorizationRequired) {
		t.Fatalf("second err = %v, want ErrReauthorizationRequired", err)
	}
	if f.idp.calls.Load() != callsBefore {
		t.Error("a grant that needs its user was refreshed again")
	}
}

func TestGrantToken_ATransientFailureLeavesTheGrantActive(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	f.idp.fail(http.StatusServiceUnavailable)

	_, err := f.svc.GrantToken(t.Context(), s.ID, "")
	if err == nil || errors.Is(err, llm.ErrReauthorizationRequired) {
		t.Fatalf("err = %v, want a transient failure", err)
	}
	if row := f.stored(t, s.ID); row.Status != store.ModelOAuthGrantStatusActive || row.RefreshToken != "refresh-0" || row.RefreshStartedTime != 0 {
		t.Errorf("status = %q, refresh = %q, started %d; want the grant untouched and the refresh released", row.Status, row.RefreshToken, row.RefreshStartedTime)
	}
}

func TestGrantToken_AFailedRefreshHandsOutTheStoredTokenUntilItExpires(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, tokenReuseMargin-time.Minute)
	f.idp.fail(http.StatusServiceUnavailable)

	token, err := f.svc.GrantToken(t.Context(), s.ID, "")
	if err != nil {
		t.Fatalf("GrantToken: %v", err)
	}
	if token.AccessToken != "access-0" || f.idp.calls.Load() != 1 {
		t.Errorf("token = %q after %d refreshes; want the stored token after one failed refresh", token.AccessToken, f.idp.calls.Load())
	}
	if row := f.stored(t, s.ID); row.RefreshStartedTime != 0 || row.Status != store.ModelOAuthGrantStatusActive {
		t.Errorf("started %d, status %q; want the refresh released and the grant active", row.RefreshStartedTime, row.Status)
	}

	if _, err := f.svc.GrantToken(t.Context(), s.ID, llm.TokenFingerprint("access-0")); err == nil {
		t.Error("a failed refresh handed out the token the provider had rejected")
	}
}

func TestGrantToken_AFailedRefreshElsewhereHandsOutTheStoredToken(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, tokenReuseMargin-time.Minute)
	repo := store.NewModelOAuthGrantRepo(f.db)
	if claimed, err := repo.ClaimRefresh(t.Context(), s.ID, s.Version, f.now.UnixMilli()); err != nil || !claimed {
		t.Fatalf("ClaimRefresh = %v, %v", claimed, err)
	}
	waiting := make(chan struct{}, 1)
	f.svc.onClaimWait = func() { waiting <- struct{}{} }

	got := make(chan string, 1)
	go func() {
		token, err := f.svc.GrantToken(t.Context(), s.ID, "")
		if err != nil {
			t.Errorf("GrantToken: %v", err)
		}
		got <- token.AccessToken
	}()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh did not wait on the claim another process holds")
	}
	if err := repo.ReleaseRefresh(t.Context(), s.ID, s.Version+1); err != nil {
		t.Fatalf("ReleaseRefresh: %v", err)
	}

	if token := <-got; token != "access-0" || f.idp.calls.Load() != 0 {
		t.Errorf("token = %q after %d refreshes; want the stored token and no refresh of our own", token, f.idp.calls.Load())
	}
}

func TestSaveRefreshed_SavesAfterTheRefreshTimeIsUp(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	repo := store.NewModelOAuthGrantRepo(f.db)
	if claimed, err := repo.ClaimRefresh(t.Context(), s.ID, s.Version, f.now.UnixMilli()); err != nil || !claimed {
		t.Fatalf("ClaimRefresh = %v, %v", claimed, err)
	}
	spent, cancel := context.WithCancel(t.Context())
	cancel()

	tokens := store.ModelOAuthGrantTokens{RefreshToken: "refresh-1", AccessToken: "access-1", AccessTokenExpiresTime: f.now.Add(time.Hour).UnixMilli()}
	if won, err := f.svc.saveRefreshed(spent, s.ID, s.Version+1, tokens); err != nil || !won {
		t.Fatalf("saveRefreshed = %v, %v; want the rotated tokens saved although the refresh's context is done", won, err)
	}
	if row := f.stored(t, s.ID); row.RefreshToken != "refresh-1" {
		t.Errorf("stored refresh = %q, want the rotated one", row.RefreshToken)
	}
}

func TestGrantToken_ARejectedTokenIsRefreshedAtOnce(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, time.Hour)

	token, err := f.svc.GrantToken(t.Context(), s.ID, llm.TokenFingerprint("access-0"))
	if err != nil {
		t.Fatalf("GrantToken: %v", err)
	}
	if token.AccessToken != "access-1" {
		t.Errorf("token = %q, want a refresh in place of the rejected one", token.AccessToken)
	}

	token, err = f.svc.GrantToken(t.Context(), s.ID, llm.TokenFingerprint("access-0"))
	if err != nil {
		t.Fatalf("second GrantToken: %v", err)
	}
	if token.AccessToken != "access-1" || f.idp.calls.Load() != 1 {
		t.Errorf("token = %q after %d refreshes; a token rejected after it was replaced must not refresh again", token.AccessToken, f.idp.calls.Load())
	}
}

func TestGrantToken_ANewGrantDuringARefreshWins(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	arrived, release := f.idp.holdRefreshes(t)

	got := make(chan string, 1)
	go func() {
		token, err := f.svc.GrantToken(t.Context(), s.ID, "")
		if err != nil {
			t.Errorf("GrantToken: %v", err)
		}
		got <- token.AccessToken
	}()
	<-arrived
	replaced := store.ModelOAuthGrantTokens{RefreshToken: "refresh-new", AccessToken: "access-new", AccessTokenExpiresTime: f.now.Add(time.Hour).UnixMilli()}
	if err := f.svc.Replace(t.Context(), s.ID, "user|1", "user@example.com", replaced, "desktop"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	release()

	if token := <-got; token != "access-new" {
		t.Errorf("token = %q, want the new grant's", token)
	}
	if row := f.stored(t, s.ID); row.RefreshToken != "refresh-new" {
		t.Errorf("stored refresh = %q; the late refresh overwrote the new grant", row.RefreshToken)
	}
}

func TestReplace_RefusesADifferentAccount(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, time.Hour)

	err := f.svc.Replace(t.Context(), s.ID, "user|2", "", store.ModelOAuthGrantTokens{RefreshToken: "r"}, "desktop")
	if !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("err = %v, want ErrAccountMismatch", err)
	}
}

func TestGrantToken_UnknownGrant(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.GrantToken(t.Context(), "00000000-0000-0000-0000-000000000001", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGrantToken_ACallerThatGivesUpDoesNotLoseTheRotatedToken(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	arrived, release := f.idp.holdRefreshes(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = f.svc.GrantToken(ctx, s.ID, "")
	}()
	<-arrived
	cancel()
	release()
	<-done

	row := f.stored(t, s.ID)
	if row.RefreshToken != "refresh-1" || row.Status != store.ModelOAuthGrantStatusActive || row.RefreshStartedTime != 0 {
		t.Errorf("stored refresh %q, status %q, started %d; want the rotated token saved although its caller gave up",
			row.RefreshToken, row.Status, row.RefreshStartedTime)
	}
}

func TestGrantToken_ARefreshDoesNotFollowARedirect(t *testing.T) {
	f := newFixture(t)
	var followed atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	t.Cleanup(elsewhere.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/oauth/token", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirecting.Close)
	grant := &store.ModelOAuthGrant{
		Name: "Example ID", Flow: store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer: redirecting.URL + "/", TokenEndpoint: redirecting.URL + "/oauth/token",
		ClientID: "desktop", APIBase: testAPIBase, AccountSubject: "user|1", RefreshToken: "refresh-0",
	}
	if err := f.svc.Create(t.Context(), grant, "desktop"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	svc := New(f.db, nil, WithClock(func() time.Time { return f.now }))
	if _, err := svc.GrantToken(t.Context(), grant.ID, ""); err == nil {
		t.Error("GrantToken succeeded through a redirect, want the redirect refused")
	}
	if n := followed.Load(); n != 0 {
		t.Errorf("the redirect target received %d refreshes, want none: following it sends the refresh token on", n)
	}
}

type failingSaves struct {
	grantStore
	mu       sync.Mutex
	failures int
	saves    int
}

func (f *failingSaves) SaveRefreshed(ctx context.Context, id string, claimedVersion int64, tokens store.ModelOAuthGrantTokens) (bool, error) {
	f.mu.Lock()
	f.saves++
	failing := f.saves <= f.failures
	f.mu.Unlock()
	if failing {
		return false, errors.New("database unavailable")
	}
	return f.grantStore.SaveRefreshed(ctx, id, claimedVersion, tokens)
}

func (f *failingSaves) saveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saves
}

func TestGrantToken_ASaveThatFailsOnceIsTriedAgain(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	saves := &failingSaves{grantStore: f.svc.repo, failures: 1}
	f.svc.repo = saves

	token, err := f.svc.GrantToken(t.Context(), s.ID, "")
	if err != nil {
		t.Fatalf("GrantToken: %v", err)
	}
	if token.AccessToken != "access-1" || saves.saveCount() != 2 {
		t.Errorf("token = %q after %d saves; want the refreshed token, saved on the second try", token.AccessToken, saves.saveCount())
	}
	if row := f.stored(t, s.ID); row.RefreshToken != "refresh-1" {
		t.Errorf("stored refresh = %q, want the rotated one", row.RefreshToken)
	}
}

func TestGrantToken_ASaveThatKeepsFailingIsGivenUp(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	saves := &failingSaves{grantStore: f.svc.repo, failures: saveAttempts}
	f.svc.repo = saves

	if _, err := f.svc.GrantToken(t.Context(), s.ID, ""); err == nil {
		t.Fatal("GrantToken succeeded although no save did")
	}
	if n := saves.saveCount(); n != saveAttempts {
		t.Errorf("saves = %d, want %d", n, saveAttempts)
	}
}

func TestMarkReauthorizationRequired_RecordsAfterTheRefreshTimeIsUp(t *testing.T) {
	f := newFixture(t)
	s := f.createGrant(t, 0)
	repo := store.NewModelOAuthGrantRepo(f.db)
	if claimed, err := repo.ClaimRefresh(t.Context(), s.ID, s.Version, f.now.UnixMilli()); err != nil || !claimed {
		t.Fatalf("ClaimRefresh = %v, %v", claimed, err)
	}
	spent, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := f.svc.markReauthorizationRequired(spent, s.ID, s.Version+1); !errors.Is(err, llm.ErrReauthorizationRequired) {
		t.Fatalf("err = %v, want ErrReauthorizationRequired although the refresh's context is done", err)
	}
	if row := f.stored(t, s.ID); row.Status != store.ModelOAuthGrantStatusReauthorizationRequired || row.RefreshStartedTime != 0 {
		t.Errorf("status %q, started %d; want the refusal recorded and the claim ended", row.Status, row.RefreshStartedTime)
	}
}
