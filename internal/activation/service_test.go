// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

const (
	testActor = "desktop-user"
)

var (
	ada = SignInResult{Subject: "auth0|ada", Email: "ada@example.com", Name: "Ada Lovelace", Tokens: FreeModelTokens{RefreshToken: "refresh-ada", AccessToken: "access-ada"}}

	errSignInRefused = errors.New("solace id refused the sign-in")
)

type signInOutcome struct {
	account SignInResult
	err     error
}

type fakeSolaceID struct {
	started  chan struct{}
	outcomes chan signInOutcome
}

func newFakeSolaceID() *fakeSolaceID {
	return &fakeSolaceID{started: make(chan struct{}, 4), outcomes: make(chan signInOutcome, 4)}
}

func (f *fakeSolaceID) SignIn(ctx context.Context) (SignInResult, error) {
	f.started <- struct{}{}
	select {
	case o := <-f.outcomes:
		return o.account, o.err
	case <-ctx.Done():
		return SignInResult{}, ctx.Err()
	}
}

func (f *fakeSolaceID) signsInAs(account SignInResult) *fakeSolaceID {
	f.outcomes <- signInOutcome{account: account}
	return f
}

func newService(t *testing.T, home string, client SignInClient) *Service {
	t.Helper()
	s, _ := newServiceWithPlatform(t, home, client)
	return s
}

func newServiceWithPlatform(t *testing.T, home string, client SignInClient) (*Service, *fakePlatform) {
	t.Helper()
	platform := &fakePlatform{}
	s := NewService(NewStore(home), client, slog.New(slog.DiscardHandler), WithFreeModel(testFreeModel), linkedTo(t, home))
	s.SetFreeModel(platform)
	t.Cleanup(s.Close)
	return s, platform
}

func signIn(t *testing.T, s *Service) (Status, error) {
	t.Helper()
	if status, err := startSignIn(t, s); err != nil {
		return status, err
	}
	return awaitSignIn(t, s)
}

func startSignIn(t *testing.T, s *Service) (Status, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	return s.StartSignIn(ctx, testActor)
}

func awaitSignIn(t *testing.T, s *Service) (Status, error) {
	t.Helper()
	require.Eventually(t, func() bool { return !s.Status().SigningIn }, 5*time.Second, 10*time.Millisecond)
	status := s.Status()
	return status, status.SignInError
}

func activate(t *testing.T, s *Service) {
	t.Helper()
	_, err := signIn(t, s)
	require.NoError(t, err)
	_, err = s.Accept(t.Context(), testActor, false, nil)
	require.NoError(t, err)
	require.True(t, s.Active())
}

func storedRecord(t *testing.T, home string) Record {
	t.Helper()
	record, err := NewStore(home).Load()
	require.NoError(t, err)
	return record
}

func withRecordFromAnOlderRelease(t *testing.T, home string) {
	t.Helper()
	record := `account:
  subject: auth0|ada
  email: ada@example.com
  signed_in_at: 2026-11-01T10:00:00Z
eula:
  subject: auth0|ada
  version: an-older-version
  accepted_at: 2026-11-01T10:01:00Z
`
	require.NoError(t, os.WriteFile(filepath.Join(home, fileName), []byte(record), 0o600))
}

func TestAFreshInstallIsSignedOut(t *testing.T) {
	s := newService(t, t.TempDir(), newFakeSolaceID())

	status := s.Status()

	assert.Equal(t, StateSignedOut, status.State)
	assert.False(t, s.Active())
	assert.Nil(t, status.Account)
}

func TestTheStatusNamesTheAccountAndTheClickwraps(t *testing.T) {
	agreements := Agreements{BaseURL: "https://api.us.spotdraft.com", EULAClickwrapID: "eula-1"}
	s := NewService(NewStore(t.TempDir()), newFakeSolaceID().signsInAs(ada), slog.New(slog.DiscardHandler), WithFreeModel(testFreeModel), WithAgreements(agreements, nil))
	s.SetFreeModel(&fakePlatform{})
	t.Cleanup(s.Close)

	status, err := signIn(t, s)

	require.NoError(t, err)
	assert.Equal(t, agreements, status.Agreements)
	require.NotNil(t, status.Account)
	assert.Equal(t, ada.Subject, status.Account.Subject)
	assert.Equal(t, ada.Email, status.Account.Email)
	assert.Equal(t, ada.Name, status.Account.Name)
}

func TestSigningInWaitsForTheEULA(t *testing.T) {
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))

	status, err := signIn(t, s)

	require.NoError(t, err)
	assert.Equal(t, StateEULAPending, status.State)
	assert.False(t, s.Active())
	record := storedRecord(t, home)
	require.NotNil(t, record.Account)
	assert.Equal(t, ada.Email, record.Account.Email)
	assert.Nil(t, record.EULA)
}

func TestAcceptingTheEULAActivates(t *testing.T) {
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	before := time.Now().UTC()

	status, err := s.Accept(t.Context(), testActor, false, nil)

	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
	assert.True(t, s.Active())
	record := storedRecord(t, home)
	require.NotNil(t, record.EULA)
	assert.Equal(t, ada.Subject, record.EULA.Subject)
	assert.NotEmpty(t, record.EULA.AgreementID, "the SpotDraft contract")
	assert.False(t, record.EULA.AcceptedAt.Before(before.Truncate(time.Second)))
	assert.Nil(t, record.FreeModelTerms, "adding their own model accepts only the EULA")
}

func TestTheAcceptedAccountIsNamedOnlyOnceTheLicenseIsAccepted(t *testing.T) {
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))
	_, _, accepted := s.AcceptedAccount()
	assert.False(t, accepted, "signed out")

	_, err := signIn(t, s)
	require.NoError(t, err)
	_, _, accepted = s.AcceptedAccount()
	assert.False(t, accepted, "signed in, license not accepted")

	_, err = s.Accept(t.Context(), testActor, false, nil)
	require.NoError(t, err)
	subject, email, accepted := s.AcceptedAccount()
	assert.True(t, accepted)
	assert.Equal(t, ada.Subject, subject)
	assert.Equal(t, ada.Email, email)

	subject, _, accepted = newService(t, home, newFakeSolaceID()).AcceptedAccount()
	assert.True(t, accepted, "after a restart")
	assert.Equal(t, ada.Subject, subject)
}

func TestChoosingTheFreeModelAcceptsItsTermsToo(t *testing.T) {
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)

	_, err = s.Accept(t.Context(), testActor, true, nil)

	require.NoError(t, err)
	assert.True(t, s.Active())
	record := storedRecord(t, home)
	require.NotNil(t, record.FreeModelTerms)
	assert.Equal(t, ada.Subject, record.FreeModelTerms.Subject)
	assert.NotEmpty(t, record.FreeModelTerms.AgreementID, "the SpotDraft contract")
	assert.Equal(t, record.EULA.AcceptedAt, record.FreeModelTerms.AcceptedAt)
}

func TestABuildWithoutTheFreeModelRefusesItsTerms(t *testing.T) {
	home := t.TempDir()
	s := NewService(NewStore(home), newFakeSolaceID().signsInAs(ada), slog.New(slog.DiscardHandler), linkedTo(t, home))
	t.Cleanup(s.Close)
	_, err := signIn(t, s)
	require.NoError(t, err)
	assert.False(t, s.Status().FreeModelAvailable)

	_, err = s.Accept(t.Context(), testActor, true, nil)

	require.ErrorIs(t, err, ErrFreeModelUnavailable)
	assert.False(t, s.Active())
	assert.Nil(t, storedRecord(t, home).EULA, "a refused choice records nothing")
}

func TestTheFreeModelIsNotOfferedAfterChoosingYourOwn(t *testing.T) {
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))
	activate(t, s)

	_, err := s.Accept(t.Context(), testActor, true, nil)

	require.ErrorIs(t, err, ErrFreeModelNotOffered)
	assert.Nil(t, storedRecord(t, home).FreeModelTerms)
}

func TestAcceptingNeedsASignedInAccount(t *testing.T) {
	s := newService(t, t.TempDir(), newFakeSolaceID())

	_, err := s.Accept(t.Context(), testActor, false, nil)

	require.ErrorIs(t, err, ErrNotSignedIn)
	assert.False(t, s.Active())
}

func TestActivationSurvivesARestart(t *testing.T) {
	home := t.TempDir()
	activate(t, newService(t, home, newFakeSolaceID().signsInAs(ada)))

	restarted := newService(t, home, newFakeSolaceID())

	assert.True(t, restarted.Active())
	assert.Equal(t, StateActive, restarted.Status().State)
}

func TestQuittingBeforeAcceptingReturnsToTheEULA(t *testing.T) {
	home := t.TempDir()
	_, err := signIn(t, newService(t, home, newFakeSolaceID().signsInAs(ada)))
	require.NoError(t, err)

	restarted := newService(t, home, newFakeSolaceID())

	assert.Equal(t, StateEULAPending, restarted.Status().State)
	assert.False(t, restarted.Active())
}

func TestAnAcceptanceFromAnOlderReleaseStaysActive(t *testing.T) {
	home := t.TempDir()
	withRecordFromAnOlderRelease(t, home)

	s := newService(t, home, newFakeSolaceID())

	assert.Equal(t, StateActive, s.Status().State)
	assert.True(t, s.Active())
}

func TestStartingASignInAnswersWhileTheBrowserIsStillOpen(t *testing.T) {
	fake := newFakeSolaceID()
	s := newService(t, t.TempDir(), fake)

	status, err := startSignIn(t, s)

	require.NoError(t, err)
	assert.True(t, status.SigningIn)
	assert.Equal(t, StateSignedOut, status.State)
	<-fake.started
	fake.signsInAs(ada)
	status, err = awaitSignIn(t, s)
	require.NoError(t, err)
	assert.Equal(t, StateEULAPending, status.State)
}

func TestSigningInAgainReplacesARunningSignIn(t *testing.T) {
	fake := newFakeSolaceID()
	s := newService(t, t.TempDir(), fake)
	_, err := startSignIn(t, s)
	require.NoError(t, err)
	<-fake.started

	status, err := startSignIn(t, s)

	require.NoError(t, err)
	assert.True(t, status.SigningIn)
	<-fake.started
	fake.signsInAs(ada)
	status, err = awaitSignIn(t, s)
	require.NoError(t, err)
	assert.Equal(t, StateEULAPending, status.State)
}

func TestARefusedSignInLeavesTheRunningOneAlone(t *testing.T) {
	fake := newFakeSolaceID()
	s := newService(t, t.TempDir(), fake)
	_, err := startSignIn(t, s)
	require.NoError(t, err)
	<-fake.started

	_, err = s.StartFreeModelSignIn(t.Context(), testActor)

	require.ErrorIs(t, err, ErrFreeModelNotChosen)
	fake.signsInAs(ada)
	status, err := awaitSignIn(t, s)
	require.NoError(t, err)
	assert.Equal(t, StateEULAPending, status.State)
}

func TestCancelingASignInRecordsNothing(t *testing.T) {
	home := t.TempDir()
	fake := newFakeSolaceID()
	s := newService(t, home, fake)
	_, err := startSignIn(t, s)
	require.NoError(t, err)
	<-fake.started

	s.CancelSignIn()

	status, err := awaitSignIn(t, s)
	require.NoError(t, err, "a canceled sign-in is not a failure to report")
	assert.Equal(t, StateSignedOut, status.State)
	assert.Equal(t, Record{}, storedRecord(t, home))
}

func TestASignInOutlivesTheRequestThatStartedIt(t *testing.T) {
	fake := newFakeSolaceID()
	s := newService(t, t.TempDir(), fake)
	ctx, reload := context.WithCancel(t.Context())
	_, err := s.StartSignIn(ctx, testActor)
	require.NoError(t, err)
	<-fake.started

	reload()

	assert.True(t, s.Status().SigningIn)
	fake.signsInAs(ada)
	require.Eventually(t, func() bool { return s.Status().State == StateEULAPending }, 5*time.Second, 10*time.Millisecond)
}

func TestAFailedSignInChangesNothing(t *testing.T) {
	home := t.TempDir()
	fake := newFakeSolaceID()
	fake.outcomes <- signInOutcome{err: errSignInRefused}
	s := newService(t, home, fake)

	status, err := signIn(t, s)

	require.ErrorIs(t, err, errSignInRefused)
	assert.Equal(t, StateSignedOut, status.State)
	assert.False(t, status.SigningIn)
	assert.Equal(t, Record{}, storedRecord(t, home))
}

func TestAFailureIsReportedUntilTheNextSignIn(t *testing.T) {
	fake := newFakeSolaceID()
	fake.outcomes <- signInOutcome{err: errSignInRefused}
	s := newService(t, t.TempDir(), fake)
	_, err := signIn(t, s)
	require.ErrorIs(t, err, errSignInRefused)
	require.ErrorIs(t, s.Status().SignInError, errSignInRefused)

	status, err := startSignIn(t, s)

	require.NoError(t, err)
	assert.NoError(t, status.SignInError)
	<-fake.started
	fake.signsInAs(ada)
	status, err = awaitSignIn(t, s)
	require.NoError(t, err)
	assert.NoError(t, status.SignInError)
}

func TestSigningInNeedsAConfiguredSolaceIDApplication(t *testing.T) {
	_, err := signIn(t, newService(t, t.TempDir(), nil))

	require.ErrorIs(t, err, ErrSignInUnavailable)
}

func TestSigningInTwiceIsRefused(t *testing.T) {
	s := newService(t, t.TempDir(), newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)

	_, err = signIn(t, s)

	require.ErrorIs(t, err, ErrAlreadySignedIn)
}

func TestAnUnreadableRecordKeepsTheGateClosed(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, fileName), []byte("account: [not, a, map"), 0o600))

	s := newService(t, home, nil)

	assert.Equal(t, StateSignedOut, s.Status().State)
	assert.False(t, s.Active())
}

func TestAFailedSaveLeavesTheStateAsItWas(t *testing.T) {
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))
	require.NoError(t, os.Mkdir(filepath.Join(home, fileName), 0o700))

	_, err := signIn(t, s)

	require.Error(t, err)
	assert.Equal(t, StateSignedOut, s.Status().State)
	assert.False(t, s.Active())
}

func TestAFailedAcceptanceKeepsTheGateClosed(t *testing.T) {
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(home, fileName)))
	require.NoError(t, os.Mkdir(filepath.Join(home, fileName), 0o700))

	_, err = s.Accept(t.Context(), testActor, false, nil)

	require.Error(t, err)
	assert.Equal(t, StateEULAPending, s.Status().State)
	assert.False(t, s.Active())
}

type logLines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type providerError struct{ reason string }

func (providerError) Error() string {
	return `oauth2: "invalid_grant" "ada@example.com may not sign in"`
}

func (e providerError) SignInFailureReason() string { return e.reason }

func TestTheLogNamesWhyASignInFailedWithoutTheProvidersWords(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a reason the client gives": {providerError{reason: "token_exchange_status_403"}, "reason=token_exchange_status_403"},
		"a timeout":                 {context.DeadlineExceeded, "reason=timed_out"},
		"anything else":             {errors.New("boom ada@example.com"), "reason=other"},
	} {
		t.Run(name, func(t *testing.T) {
			var logs logLines
			fake := newFakeSolaceID()
			s := NewService(NewStore(t.TempDir()), fake, slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(s.Close)
			fake.outcomes <- signInOutcome{err: tc.err}

			_, err := signIn(t, s)

			require.Error(t, err)
			require.Eventually(t, func() bool { return strings.Contains(logs.String(), "solace id sign-in failed") }, 5*time.Second, 10*time.Millisecond)
			assert.Contains(t, logs.String(), tc.want)
			assert.NotContains(t, logs.String(), "ada@example.com")
		})
	}
}

func unwritable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write to")
	}
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

func TestAcceptingReportsARecordThatCouldNotBeSaved(t *testing.T) {
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	unwritable(t, home)

	_, err = s.Accept(t.Context(), testActor, false, nil)

	require.ErrorIs(t, err, ErrRecordNotSaved)
	assert.Equal(t, StateEULAPending, s.Status().State)
	assert.False(t, s.Active())
}

func TestASignInWhoseRecordCannotBeSavedIsReportedAsNotSaved(t *testing.T) {
	home := t.TempDir()
	fake := newFakeSolaceID()
	s := newService(t, home, fake)
	unwritable(t, home)
	fake.outcomes <- signInOutcome{account: ada}

	status, err := signIn(t, s)

	require.ErrorIs(t, err, ErrRecordNotSaved)
	assert.Equal(t, StateSignedOut, status.State)
}

type recordingAuditor struct {
	mu     sync.Mutex
	events []auth.ActivationChangeEvent
}

func (r *recordingAuditor) LogActivationChange(_ context.Context, event auth.ActivationChangeEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordingAuditor) recorded() []auth.ActivationChangeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]auth.ActivationChangeEvent(nil), r.events...)
}

func TestTheAuditLogNamesTheSignInAndTheAcceptance(t *testing.T) {
	s := newService(t, t.TempDir(), newFakeSolaceID().signsInAs(ada))
	audit := &recordingAuditor{}
	s.SetAuditor(audit)

	_, err := signIn(t, s)
	require.NoError(t, err)
	_, err = s.Accept(t.Context(), testActor, true, nil)
	require.NoError(t, err)

	contractID := s.record.FreeModelTerms.AgreementID
	require.NotEmpty(t, contractID)
	assert.Equal(t, []auth.ActivationChangeEvent{
		{Actor: testActor, Change: auth.ActivationChangeSignedIn, Subject: ada.Subject},
		{Actor: testActor, Change: auth.ActivationChangeAccepted, Subject: ada.Subject, FreeModelTerms: true, AgreementID: contractID},
	}, audit.recorded())
}

func TestTheAuditLogSkipsAFailedSignInAndAnAcceptanceThatChangesNothing(t *testing.T) {
	fake := newFakeSolaceID()
	fake.outcomes <- signInOutcome{err: errSignInRefused}
	s := newService(t, t.TempDir(), fake.signsInAs(ada))
	audit := &recordingAuditor{}
	s.SetAuditor(audit)

	_, err := signIn(t, s)
	require.Error(t, err)
	activate(t, s)
	_, err = s.Accept(t.Context(), testActor, false, nil)
	require.NoError(t, err)

	changes := make([]string, 0, 2)
	for _, event := range audit.recorded() {
		changes = append(changes, event.Change)
	}
	assert.Equal(t, []string{auth.ActivationChangeSignedIn, auth.ActivationChangeAccepted}, changes)
}
