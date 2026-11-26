// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft"
)

const signInTimeout = 5 * time.Minute

const grantSaveTimeout = 30 * time.Second

var errNoAccount = errors.New("activation: the sign-in returned no account")

type Service struct {
	store      *Store
	client     SignInClient
	logger     *slog.Logger
	freeModel  *FreeModelSettings
	agreements Agreements
	clickwraps Clickwraps

	active   atomic.Bool
	accepted atomic.Pointer[Account]

	acceptMu sync.Mutex

	cleanupCtx  context.Context
	stopCleanup context.CancelFunc

	mu            sync.Mutex
	record        Record
	signIn        *signInAttempt
	lastSignInErr error
	audit         Auditor
	setup         FreeModel
	shown         map[string]spotdraft.Clickwrap
}

type Auditor interface {
	LogActivationChange(ctx context.Context, event auth.ActivationChangeEvent)
}

type signInAttempt struct {
	actor         string
	freeModelOnly bool
	cancel        context.CancelFunc
	done          chan struct{}
	err           error
}

type Option func(*Service)

func WithFreeModel(settings FreeModelSettings) Option {
	return func(s *Service) { s.freeModel = &settings }
}

func NewService(store *Store, client SignInClient, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{store: store, client: client, logger: logger.With("logger_name", "activation"), shown: map[string]spotdraft.Clickwrap{}}
	s.cleanupCtx, s.stopCleanup = context.WithCancel(context.Background())
	for _, opt := range opts {
		opt(s)
	}
	record, err := store.Load()
	if err != nil {
		s.logger.Warn("activation record unreadable, starting signed out", "error", err)
	}
	s.record = record
	s.publishState(record)
	return s
}

func (s *Service) SetAuditor(a Auditor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = a
}

func (s *Service) SetFreeModel(f FreeModel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setup = f
	if s.freeModel != nil && s.record.choseOwnModel() {
		go s.removeUnusedGrant(f)
	}
}

func (s *Service) removeUnusedGrant(f FreeModel) {
	bo := backoff.Exponential{Initial: time.Second, Max: time.Minute}
	for {
		err := f.DeleteGrant(s.cleanupCtx)
		if err == nil {
			s.logger.Info("unused free model grant removed")
			return
		}
		s.logger.Warn("unused free model grant not removed yet, retrying", "error", err)
		if bo.Wait(s.cleanupCtx) != nil {
			return
		}
	}
}

func (s *Service) Active() bool {
	return s.active.Load()
}

func (s *Service) AcceptedAccount() (subject, email string, ok bool) {
	account := s.accepted.Load()
	if account == nil {
		return "", "", false
	}
	return account.Subject, account.Email, true
}

func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Service) StartSignIn(ctx context.Context, actor string) (Status, error) {
	return s.replaceSignIn(ctx, actor, false)
}

func (s *Service) StartFreeModelSignIn(ctx context.Context, actor string) (Status, error) {
	return s.replaceSignIn(ctx, actor, true)
}

func (s *Service) RefreshFreeModel(ctx context.Context) error {
	s.mu.Lock()
	chosen, setup := s.freeModelChosenLocked(), s.setup
	s.mu.Unlock()
	if !chosen || setup == nil {
		return ErrFreeModelNotChosen
	}
	return setup.Refresh(ctx)
}

func (s *Service) freeModelChosenLocked() bool {
	return s.freeModel != nil && s.record.State() == StateActive && s.record.FreeModelTerms != nil
}

func (s *Service) replaceSignIn(ctx context.Context, actor string, freeModelOnly bool) (Status, error) {
	for {
		running, err := s.startSignIn(actor, freeModelOnly)
		if running == nil {
			return s.Status(), err
		}
		running.cancel()
		select {
		case <-running.done:
		case <-ctx.Done():
			return s.Status(), ctx.Err()
		}
		if running.err == nil {
			return s.Status(), nil
		}
	}
}

func (s *Service) CancelSignIn() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signIn != nil {
		s.signIn.cancel()
	}
	return s.statusLocked()
}

func (s *Service) Accept(ctx context.Context, actor string, freeModel bool, opened []string) (Status, error) {
	s.acceptMu.Lock()
	defer s.acceptMu.Unlock()
	s.mu.Lock()
	err := s.checkAcceptLocked(freeModel)
	if err == nil {
		_, err = s.termsSubjectLocked()
	}
	if err != nil {
		defer s.mu.Unlock()
		return s.statusLocked(), err
	}
	setup := s.setup
	onFreeModel := s.record.FreeModelTerms != nil
	eulaRecorded := s.record.acceptedByAccount(s.record.EULA)
	account := *s.record.Account
	s.mu.Unlock()

	var agreementID string
	if !eulaRecorded || freeModel {
		if agreementID, err = s.sendAgreement(ctx, s.agreements.EULAClickwrapID, account, opened); err != nil {
			return s.agreementNotRecorded(err, freeModel)
		}
	}
	if freeModel {
		s.refreshFreeModel(ctx, setup)
	}

	s.mu.Lock()
	status, err := s.recordAcceptanceLocked(actor, freeModel, agreementID)
	s.mu.Unlock()
	if err == nil && !freeModel && !onFreeModel && s.freeModel != nil && setup != nil {
		if deleteErr := setup.DeleteGrant(ctx); deleteErr != nil {
			s.logger.Warn("free model grant not removed, retrying in the background", "error", deleteErr)
			go s.removeUnusedGrant(setup)
		}
	}
	return status, err
}

func (s *Service) agreementNotRecorded(err error, freeModelTerms bool) (Status, error) {
	s.logger.Warn("agreement not recorded by Solace, SAM stays locked", "freeModelTerms", freeModelTerms, "error", err)
	return s.Status(), causedBy(ErrAgreementNotRecorded, err)
}

func (s *Service) checkAcceptLocked(freeModel bool) error {
	switch {
	case s.record.Account == nil:
		return ErrNotSignedIn
	case freeModel && s.freeModel == nil:
		return ErrFreeModelUnavailable
	case freeModel && s.record.choseOwnModel():
		return ErrFreeModelNotOffered
	}
	return nil
}

func (s *Service) refreshFreeModel(ctx context.Context, setup FreeModel) {
	if setup == nil {
		s.logger.Warn("free model chosen with no platform to refresh its grant, its first call asks for the terms again")
		return
	}
	if err := setup.Refresh(ctx); err != nil {
		s.logger.Warn("free model refresh after agreement failed, its first call asks for the terms again", "error", err)
	}
}

func (s *Service) recordAcceptanceLocked(actor string, freeModel bool, agreementID string) (Status, error) {
	if s.record.Account == nil {
		return s.statusLocked(), ErrNotSignedIn
	}
	next := s.record
	now := time.Now().UTC()
	if !next.acceptedByAccount(next.EULA) {
		next.EULA = &Acceptance{Subject: next.Account.Subject, AgreementID: agreementID, AcceptedAt: now}
	}
	if freeModel && !next.acceptedByAccount(next.FreeModelTerms) {
		next.FreeModelTerms = &Acceptance{Subject: next.Account.Subject, AgreementID: agreementID, AcceptedAt: now}
	}
	if next == s.record {
		return s.statusLocked(), nil
	}
	if err := s.commitLocked(next); err != nil {
		return s.statusLocked(), err
	}
	s.logger.Info("license agreement accepted", "freeModelTerms", freeModel)
	s.auditLocked(auth.ActivationChangeEvent{
		Actor:          actor,
		Change:         auth.ActivationChangeAccepted,
		Subject:        next.Account.Subject,
		FreeModelTerms: freeModel,
		AgreementID:    agreementID,
	})
	return s.statusLocked(), nil
}

func (s *Service) Close() {
	s.CancelSignIn()
	s.stopCleanup()
}

func (s *Service) startSignIn(actor string, freeModelOnly bool) (running *signInAttempt, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.client == nil:
		return nil, ErrSignInUnavailable
	case freeModelOnly && !s.freeModelChosenLocked():
		return nil, ErrFreeModelNotChosen
	case !freeModelOnly && s.record.Account != nil:
		return nil, ErrAlreadySignedIn
	case s.signIn != nil:
		return s.signIn, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), signInTimeout)
	attempt := &signInAttempt{actor: actor, freeModelOnly: freeModelOnly, cancel: cancel, done: make(chan struct{})}
	s.signIn = attempt
	s.lastSignInErr = nil
	go s.runSignIn(ctx, attempt)
	return nil, nil
}

func (s *Service) runSignIn(ctx context.Context, attempt *signInAttempt) {
	defer attempt.cancel()
	result, err := s.client.SignIn(ctx)
	if err == nil && attempt.freeModelOnly {
		err = s.checkSignedInAccount(result)
	}
	if err == nil {
		saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), grantSaveTimeout)
		err = s.saveFreeModelGrant(saveCtx, result)
		cancelSave()
	}

	s.mu.Lock()
	if err == nil && !attempt.freeModelOnly {
		err = s.recordSignInLocked(result)
	}
	if err == nil {
		s.auditLocked(auth.ActivationChangeEvent{Actor: attempt.actor, Change: auth.ActivationChangeSignedIn, Subject: result.Subject})
	}
	if !errors.Is(err, context.Canceled) {
		s.lastSignInErr = err
	}
	s.signIn = nil
	s.mu.Unlock()

	s.logSignIn(err)
	attempt.err = err
	close(attempt.done)
}

func (s *Service) checkSignedInAccount(result SignInResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.Account == nil || result.Subject != s.record.Account.Subject {
		return ErrDifferentAccount
	}
	return nil
}

func (s *Service) saveFreeModelGrant(ctx context.Context, result SignInResult) error {
	if s.freeModel == nil {
		return nil
	}
	s.mu.Lock()
	setup := s.setup
	s.mu.Unlock()
	switch {
	case result.Subject == "":
		return errNoAccount
	case result.Tokens.RefreshToken == "":
		return causedBy(ErrFreeModelGrantNotSaved, ErrNoRefreshToken)
	case setup == nil:
		return ErrFreeModelGrantNotSaved
	}
	if err := setup.SaveGrant(ctx, result); err != nil {
		return causedBy(ErrFreeModelGrantNotSaved, err)
	}
	return nil
}

func (s *Service) recordSignInLocked(result SignInResult) error {
	if result.Subject == "" {
		return errNoAccount
	}
	return s.commitLocked(Record{Account: &Account{Subject: result.Subject, Email: result.Email, Name: result.Name, SignedInAt: time.Now().UTC()}})
}

func (s *Service) auditLocked(event auth.ActivationChangeEvent) {
	if s.audit != nil {
		s.audit.LogActivationChange(context.Background(), event)
	}
}

func (s *Service) commitLocked(next Record) error {
	if err := s.store.Save(next); err != nil {
		return causedBy(ErrRecordNotSaved, err)
	}
	s.record = next
	s.publishState(next)
	return nil
}

func (s *Service) publishState(record Record) {
	active := record.State() == StateActive
	var accepted *Account
	if active {
		account := *record.Account
		accepted = &account
	}
	s.accepted.Store(accepted)
	s.active.Store(active)
}

func (s *Service) statusLocked() Status {
	var freeModel *FreeModelSettings
	if s.freeModel != nil {
		settings := *s.freeModel
		freeModel = &settings
	}
	var account *Account
	if s.record.Account != nil {
		signedIn := *s.record.Account
		account = &signedIn
	}
	return Status{
		State:              s.record.State(),
		SigningIn:          s.signIn != nil,
		SignInError:        s.lastSignInErr,
		Account:            account,
		Agreements:         s.agreements,
		FreeModelAvailable: s.freeModel != nil,
		FreeModel:          freeModel,
		FreeModelChosen:    s.record.FreeModelTerms != nil,
	}
}

func (s *Service) logSignIn(err error) {
	switch {
	case err == nil:
		s.logger.Info("solace id sign-in completed")
	case errors.Is(err, context.Canceled):
		s.logger.Debug("solace id sign-in canceled")
	case errors.Is(err, ErrRecordNotSaved):
		s.logger.Error("solace id sign-in verified an account that could not be recorded", "error", err)
	default:
		s.logger.Warn("solace id sign-in failed", "reason", signInFailureReason(err))
	}
}

func signInFailureReason(err error) string {
	var failure SignInFailure
	switch {
	case errors.Is(err, ErrNoRefreshToken):
		return "no_refresh_token"
	case errors.Is(err, ErrFreeModelGrantNotSaved):
		return "grant_not_saved"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed_out"
	case errors.Is(err, errNoAccount):
		return "no_account"
	case errors.Is(err, ErrDifferentAccount):
		return "different_account"
	case errors.As(err, &failure):
		return failure.SignInFailureReason()
	default:
		return "other"
	}
}

func causedBy(failure, cause error) error {
	return fmt.Errorf("%w: %w", failure, cause)
}
