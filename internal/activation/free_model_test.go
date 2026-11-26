// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errPlatformDown = errors.New("the platform did not answer")

var testFreeModel = FreeModelSettings{GrantID: "1c58a105-c275-4401-b9af-9b1af81a80af", Provider: "openai", APIBase: "https://llm.example.test/v1", GeneralModelName: "free", PlanningModelName: "free-planning"}

type fakePlatform struct {
	mu           sync.Mutex
	grantSubject string
	saves        int
	refreshes    int
	deletes      int

	failSaves, failRefreshes, failDeletes int

	refreshStarted chan struct{}
	refreshGate    chan struct{}

	saveStarted chan struct{}
	saveGate    chan struct{}
}

func (p *fakePlatform) SaveGrant(ctx context.Context, result SignInResult) error {
	if p.saveStarted != nil {
		p.saveStarted <- struct{}{}
	}
	if p.saveGate != nil {
		<-p.saveGate
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saves++
	if p.failSaves > 0 {
		p.failSaves--
		return errPlatformDown
	}
	p.grantSubject = result.Subject
	return nil
}

func (p *fakePlatform) Refresh(context.Context) error {
	if p.refreshStarted != nil {
		p.refreshStarted <- struct{}{}
	}
	if p.refreshGate != nil {
		<-p.refreshGate
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failRefreshes > 0 {
		p.failRefreshes--
		return errPlatformDown
	}
	if p.grantSubject == "" {
		return errors.New("no free model grant")
	}
	p.refreshes++
	return nil
}

func (p *fakePlatform) DeleteGrant(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deletes++
	if p.failDeletes > 0 {
		p.failDeletes--
		return errPlatformDown
	}
	p.grantSubject = ""
	return nil
}

func (p *fakePlatform) state() (grantSubject string, refreshes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.grantSubject, p.refreshes
}

func relaunch(t *testing.T, home string, client SignInClient, platform *fakePlatform) *Service {
	t.Helper()
	s := NewService(NewStore(home), client, slog.New(slog.DiscardHandler), WithFreeModel(testFreeModel), linkedTo(t, home))
	s.SetFreeModel(platform)
	t.Cleanup(s.Close)
	return s
}

func TestASignInSavesTheFreeModelGrantBeforeItIsRecorded(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))

	_, err := signIn(t, s)

	require.NoError(t, err)
	subject, _ := platform.state()
	assert.Equal(t, ada.Subject, subject)
	assert.Equal(t, StateEULAPending, storedRecord(t, home).State())
}

func TestASignInWhoseGrantIsNotSavedIsNotRecorded(t *testing.T) {
	home := t.TempDir()
	solaceID := newFakeSolaceID().signsInAs(ada)
	s, platform := newServiceWithPlatform(t, home, solaceID)
	platform.failSaves = 1

	_, err := signIn(t, s)

	require.ErrorIs(t, err, ErrFreeModelGrantNotSaved)
	assert.Equal(t, StateSignedOut, storedRecord(t, home).State())

	solaceID.signsInAs(ada)
	_, err = signIn(t, s)
	require.NoError(t, err, "signing in again saves the grant and records the sign-in")
	assert.Equal(t, StateEULAPending, storedRecord(t, home).State())
}

func TestASignInWithoutARefreshTokenIsNotRecorded(t *testing.T) {
	home := t.TempDir()
	noRefresh := ada
	noRefresh.Tokens = FreeModelTokens{AccessToken: "access-ada"}
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(noRefresh))

	_, err := signIn(t, s)

	require.ErrorIs(t, err, ErrFreeModelGrantNotSaved)
	assert.Equal(t, "no_refresh_token", signInFailureReason(err))
	assert.Equal(t, StateSignedOut, storedRecord(t, home).State())
	assert.Zero(t, platform.saves)
}

func TestASignInCutShortAfterTheGrantResumesBySigningInAgain(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write to")
	}
	home := t.TempDir()
	platform := &fakePlatform{}
	s := relaunch(t, home, newFakeSolaceID().signsInAs(ada), platform)
	unwritable(t, home)

	_, err := signIn(t, s)
	require.ErrorIs(t, err, ErrRecordNotSaved)
	subject, _ := platform.state()
	require.Equal(t, ada.Subject, subject, "the grant was saved first")

	require.NoError(t, os.Chmod(home, 0o700))
	s = relaunch(t, home, newFakeSolaceID().signsInAs(ada), platform)
	assert.Equal(t, StateSignedOut, s.Status().State)
	_, err = signIn(t, s)
	require.NoError(t, err)
	assert.Equal(t, StateEULAPending, s.Status().State)
	assert.Equal(t, 2, platform.saves)
}

func TestGetStartedRefreshesTheGrantBeforeActivating(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	assert.False(t, s.Status().FreeModelChosen)

	status, err := s.Accept(t.Context(), testActor, true, nil)

	require.NoError(t, err)
	assert.True(t, s.Active())
	_, refreshes := platform.state()
	assert.Equal(t, 1, refreshes)
	assert.NotNil(t, storedRecord(t, home).FreeModelTerms)
	assert.True(t, status.FreeModelChosen, "the web UI creates the free model's models next")
	assert.Equal(t, &testFreeModel, status.FreeModel)
}

func TestGetStartedCutShortResumesAtTheRelaunch(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write to")
	}
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	unwritable(t, home)

	_, err = s.Accept(t.Context(), testActor, true, nil)

	require.ErrorIs(t, err, ErrRecordNotSaved)
	assert.False(t, s.Active())
	require.NoError(t, os.Chmod(home, 0o700))
	assert.Nil(t, storedRecord(t, home).EULA)

	s = relaunch(t, home, newFakeSolaceID(), platform)
	assert.Equal(t, StateEULAPending, s.Status().State)
	_, err = s.Accept(t.Context(), testActor, true, nil)
	require.NoError(t, err)
	assert.True(t, s.Active())
	_, refreshes := platform.state()
	assert.Equal(t, 2, refreshes)
}

func TestGetStartedActivatesWhenTheRefreshFails(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	platform.failRefreshes = 1

	_, err = s.Accept(t.Context(), testActor, true, nil)

	require.NoError(t, err)
	assert.True(t, s.Active())
	assert.True(t, s.Status().FreeModelChosen)
}

func TestAddingYourOwnModelRemovesTheFreeModelGrant(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))

	activate(t, s)

	subject, refreshes := platform.state()
	assert.Empty(t, subject)
	assert.Zero(t, refreshes)
	assert.False(t, s.Status().FreeModelChosen)
}

func TestAddingYourOwnModelKeepsTheGrantWhenTheChoiceIsNotSaved(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write to")
	}
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	unwritable(t, home)

	_, err = s.Accept(t.Context(), testActor, false, nil)

	require.ErrorIs(t, err, ErrRecordNotSaved)
	subject, _ := platform.state()
	assert.Equal(t, ada.Subject, subject)
	assert.Zero(t, platform.deletes)
}

func TestOverlappingAcceptancesKeepTheGrantOfTheFreeModelChoice(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	platform.refreshStarted, platform.refreshGate = make(chan struct{}, 1), make(chan struct{})

	freeDone := make(chan error, 1)
	go func() {
		_, err := s.Accept(t.Context(), testActor, true, nil)
		freeDone <- err
	}()
	<-platform.refreshStarted
	ownDone := make(chan error, 1)
	go func() {
		_, err := s.Accept(t.Context(), testActor, false, nil)
		ownDone <- err
	}()
	select {
	case <-ownDone:
		ownDone <- nil
	case <-time.After(200 * time.Millisecond):
	}
	close(platform.refreshGate)
	require.NoError(t, <-freeDone)
	require.NoError(t, <-ownDone)

	subject, _ := platform.state()
	if storedRecord(t, home).FreeModelTerms != nil {
		assert.Equal(t, ada.Subject, subject, "the free model is chosen, so its grant must be kept")
	}
}

func TestAddingYourOwnModelKeepsTheGrantOfAFreeModelInstall(t *testing.T) {
	home := t.TempDir()
	solaceID := newFakeSolaceID()
	s, platform := newServiceWithPlatform(t, home, solaceID)
	activateOnFreeModel(t, s, solaceID)

	_, err := s.Accept(t.Context(), testActor, false, nil)

	require.NoError(t, err)
	subject, _ := platform.state()
	assert.Equal(t, ada.Subject, subject)
	assert.Zero(t, platform.deletes)
	assert.True(t, s.Status().FreeModelChosen)
}

func TestTheFreeModelIsNotOfferedToAnOwnModelInstall(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	activate(t, s)

	_, err := s.Accept(t.Context(), testActor, true, nil)

	require.ErrorIs(t, err, ErrFreeModelNotOffered)
	assert.False(t, s.Status().FreeModelChosen)
	_, refreshes := platform.state()
	assert.Zero(t, refreshes)
}

func TestAFailedGrantRemovalIsRetriedWhileSAMRuns(t *testing.T) {
	s, platform := newServiceWithPlatform(t, t.TempDir(), newFakeSolaceID().signsInAs(ada))
	platform.failDeletes = 2

	activate(t, s)

	require.Eventually(t, func() bool {
		subject, _ := platform.state()
		return subject == ""
	}, 10*time.Second, 20*time.Millisecond, "the removal is retried until the grant is gone")
}

func TestAGrantLeftByAddingYourOwnModelIsRemovedAtTheNextStart(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	platform.failDeletes = 2

	activate(t, s)
	s.Close()
	subject, _ := platform.state()
	require.Equal(t, ada.Subject, subject, "SAM quit before a removal succeeded, so the grant is still there")

	relaunch(t, home, newFakeSolaceID(), platform)

	require.Eventually(t, func() bool {
		subject, _ := platform.state()
		return subject == ""
	}, 10*time.Second, 20*time.Millisecond, "the next start retries until the grant is gone")
}

func TestAFreeModelInstallKeepsItsGrantAtTheNextStart(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	_, err = s.Accept(t.Context(), testActor, true, nil)
	require.NoError(t, err)

	relaunch(t, home, newFakeSolaceID(), platform)

	require.Never(t, func() bool {
		platform.mu.Lock()
		defer platform.mu.Unlock()
		return platform.deletes > 0 || platform.grantSubject != ada.Subject
	}, 500*time.Millisecond, 20*time.Millisecond, "no start removes a grant the install's models name")
}

func TestABuildWithoutTheFreeModelNeedsNoRefreshToken(t *testing.T) {
	home := t.TempDir()
	noTokens := SignInResult{Subject: ada.Subject, Email: ada.Email}
	s := NewService(NewStore(home), newFakeSolaceID().signsInAs(noTokens), slog.New(slog.DiscardHandler), linkedTo(t, home))
	t.Cleanup(s.Close)

	_, err := signIn(t, s)

	require.NoError(t, err)
	assert.Equal(t, StateEULAPending, storedRecord(t, home).State())
}

func activateOnFreeModel(t *testing.T, s *Service, solaceID *fakeSolaceID) {
	t.Helper()
	solaceID.signsInAs(ada)
	_, err := signIn(t, s)
	require.NoError(t, err)
	_, err = s.Accept(t.Context(), testActor, true, nil)
	require.NoError(t, err)
}

func signInAgain(t *testing.T, s *Service) (Status, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if status, err := s.StartFreeModelSignIn(ctx, testActor); err != nil {
		return status, err
	}
	return awaitSignIn(t, s)
}

func TestSigningInAgainSavesTheFreeModelGrantAndKeepsTheRecord(t *testing.T) {
	home := t.TempDir()
	solaceID := newFakeSolaceID()
	s, platform := newServiceWithPlatform(t, home, solaceID)
	activateOnFreeModel(t, s, solaceID)
	before := storedRecord(t, home)

	solaceID.signsInAs(ada)
	_, err := signInAgain(t, s)

	require.NoError(t, err)
	assert.Equal(t, 2, platform.saves)
	assert.Equal(t, before, storedRecord(t, home))
	assert.True(t, s.Active())
}

func TestSigningInAgainRefusesAnotherAccount(t *testing.T) {
	home := t.TempDir()
	solaceID := newFakeSolaceID()
	s, platform := newServiceWithPlatform(t, home, solaceID)
	activateOnFreeModel(t, s, solaceID)

	solaceID.signsInAs(SignInResult{Subject: "auth0|grace", Email: "grace@example.com", Tokens: FreeModelTokens{RefreshToken: "refresh-grace"}})
	_, err := signInAgain(t, s)

	require.ErrorIs(t, err, ErrDifferentAccount)
	assert.Equal(t, "different_account", signInFailureReason(err))
	subject, _ := platform.state()
	assert.Equal(t, ada.Subject, subject, "the grant stays ada's")
	assert.Equal(t, 1, platform.saves)
}

func TestSigningInAgainNeedsTheFreeModel(t *testing.T) {
	solaceID := newFakeSolaceID().signsInAs(ada)
	s := newService(t, t.TempDir(), solaceID)
	activate(t, s)

	_, err := signInAgain(t, s)

	require.ErrorIs(t, err, ErrFreeModelNotChosen)
}

func TestRefreshingTheFreeModelNeedsItChosen(t *testing.T) {
	solaceID := newFakeSolaceID()
	s, platform := newServiceWithPlatform(t, t.TempDir(), solaceID)
	solaceID.signsInAs(ada)
	_, err := signIn(t, s)
	require.NoError(t, err)

	require.ErrorIs(t, s.RefreshFreeModel(t.Context()), ErrFreeModelNotChosen, "not before the free model is chosen")

	_, err = s.Accept(t.Context(), testActor, true, nil)
	require.NoError(t, err)
	require.NoError(t, s.RefreshFreeModel(t.Context()))
	_, refreshes := platform.state()
	assert.Equal(t, 2, refreshes, "one at Get Started, one asked for")
}

func TestAGrantSaveThatTimesOutIsLoggedAsNotSaved(t *testing.T) {
	err := causedBy(ErrFreeModelGrantNotSaved, context.DeadlineExceeded)

	assert.Equal(t, "grant_not_saved", signInFailureReason(err))
	assert.Equal(t, "timed_out", signInFailureReason(context.DeadlineExceeded))
}

func TestACancelDuringTheGrantSaveKeepsTheCompletedSignIn(t *testing.T) {
	home := t.TempDir()
	s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
	platform.saveStarted, platform.saveGate = make(chan struct{}, 1), make(chan struct{})

	_, err := startSignIn(t, s)
	require.NoError(t, err)
	<-platform.saveStarted
	s.CancelSignIn()
	close(platform.saveGate)
	_, err = awaitSignIn(t, s)

	require.NoError(t, err)
	assert.Equal(t, StateEULAPending, storedRecord(t, home).State())
	subject, _ := platform.state()
	assert.Equal(t, ada.Subject, subject)
}
