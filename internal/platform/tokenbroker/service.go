// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokenbroker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const tokenReuseMargin = 10 * time.Minute

const refreshTimeout = 30 * time.Second

const refreshWaitPoll = 250 * time.Millisecond

const staleRefreshClaim = refreshTimeout + saveAttempts*(saveTimeout+saveRetryPause) + 5*time.Second

const releaseTimeout = 5 * time.Second

const saveTimeout = 5 * time.Second

const saveAttempts = 3

const saveRetryPause = 200 * time.Millisecond

var errRefreshFailedElsewhere = errors.New("refresh of the OAuth grant in another process failed")

const lastErrorRefused = "the provider refused the refresh token"

var ErrNotFound = errors.New("OAuth grant not found")

var ErrAccountMismatch = errors.New("OAuth grant is by a different account")

type grantStore interface {
	Create(ctx context.Context, grant *store.ModelOAuthGrant, actor string) error
	Get(ctx context.Context, id string) (*store.ModelOAuthGrant, error)
	ClaimRefresh(ctx context.Context, id string, readVersion, startedTime int64) (bool, error)
	ReleaseRefresh(ctx context.Context, id string, claimedVersion int64) error
	SaveRefreshed(ctx context.Context, id string, claimedVersion int64, tokens store.ModelOAuthGrantTokens) (bool, error)
	MarkReauthorizationRequired(ctx context.Context, id string, claimedVersion int64, lastError string) (bool, error)
	Replace(ctx context.Context, id string, tokens store.ModelOAuthGrantTokens, signInFingerprint, accountLabel, actor string) (bool, error)
	Overwrite(ctx context.Context, grant *store.ModelOAuthGrant, actor string) (bool, error)
	Delete(ctx context.Context, id string) error
}

type Service struct {
	repo         grantStore
	client       *http.Client
	logger       *slog.Logger
	now          func() time.Time
	refresh      singleflight.Group
	loopbackHTTP bool
	onClaimWait  func()
}

type Option func(*Service)

func WithHTTPClient(c *http.Client) Option {
	return func(s *Service) { s.client = c }
}

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func WithLoopbackHTTP() Option {
	return func(s *Service) { s.loopbackHTTP = true }
}

func New(db *store.DB, logger *slog.Logger, opts ...Option) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		repo:   store.NewModelOAuthGrantRepo(db),
		client: &http.Client{Timeout: refreshTimeout, CheckRedirect: refuseRedirect},
		logger: logger.With("logger_name", "token-broker"),
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func (s *Service) Create(ctx context.Context, grant *store.ModelOAuthGrant, actor string) error {
	if err := validateNewGrant(grant, s.loopbackHTTP); err != nil {
		return err
	}
	grant.SignInFingerprint = llm.TokenFingerprint(grant.RefreshToken)
	return s.repo.Create(ctx, grant, actor)
}

func (s *Service) Get(ctx context.Context, id string) (*store.ModelOAuthGrant, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get OAuth grant: %w", err)
	}
	if row == nil {
		return nil, ErrNotFound
	}
	return row, nil
}

func (s *Service) Replace(ctx context.Context, id, accountSubject, accountLabel string, tokens store.ModelOAuthGrantTokens, actor string) error {
	row, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if row.AccountSubject != accountSubject {
		return ErrAccountMismatch
	}
	found, err := s.repo.Replace(ctx, id, tokens, llm.TokenFingerprint(tokens.RefreshToken), accountLabel, actor)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func (s *Service) GrantToken(ctx context.Context, id, rejectedFingerprint string) (llm.GrantToken, error) {
	row, err := s.Get(ctx, id)
	if err != nil {
		return llm.GrantToken{}, err
	}
	token, ok, err := s.usable(row, rejectedFingerprint)
	if ok || err != nil {
		return token, err
	}
	result, err, _ := s.refresh.Do(id, func() (any, error) {
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		defer cancel()
		return s.refreshGrant(refreshCtx, id, rejectedFingerprint)
	})
	if err != nil {
		return llm.GrantToken{}, err
	}
	return result.(llm.GrantToken), nil
}

func (s *Service) usable(row *store.ModelOAuthGrant, rejectedFingerprint string) (llm.GrantToken, bool, error) {
	if row.Status == store.ModelOAuthGrantStatusReauthorizationRequired {
		return llm.GrantToken{}, false, reauthorizationRequired(row.ID)
	}
	if row.AccessToken == "" {
		return llm.GrantToken{}, false, nil
	}
	if rejectedFingerprint != "" && llm.TokenFingerprint(row.AccessToken) == rejectedFingerprint {
		return llm.GrantToken{}, false, nil
	}
	expiresAt := time.UnixMilli(row.AccessTokenExpiresTime)
	if expiresAt.Sub(s.now()) <= tokenReuseMargin {
		return llm.GrantToken{}, false, nil
	}
	return llm.GrantToken{AccessToken: row.AccessToken, ExpiresAt: expiresAt}, true, nil
}

func (s *Service) refreshGrant(ctx context.Context, id, rejectedFingerprint string) (llm.GrantToken, error) {
	var waitedOn *store.ModelOAuthGrant
	for {
		row, err := s.Get(ctx, id)
		if err != nil {
			return llm.GrantToken{}, err
		}
		token, ok, err := s.usable(row, rejectedFingerprint)
		if ok || err != nil {
			return token, err
		}
		switch {
		case s.refreshRunning(row):
			if waitedOn == nil {
				waitedOn = row
				if s.onClaimWait != nil {
					s.onClaimWait()
				}
			}
			if err := sleep(ctx, refreshWaitPoll); err != nil {
				return llm.GrantToken{}, fmt.Errorf("wait for OAuth grant refresh: %w", err)
			}
		case waitedOn != nil && row.RefreshStartedTime == 0:
			return s.adoptOtherRefresh(row, waitedOn, rejectedFingerprint)
		default:
			claimed, err := s.repo.ClaimRefresh(ctx, id, row.Version, s.now().UnixMilli())
			if err != nil {
				return llm.GrantToken{}, err
			}
			if claimed {
				return s.refreshClaimed(ctx, row, row.Version+1, rejectedFingerprint)
			}
		}
	}
}

func (s *Service) refreshRunning(row *store.ModelOAuthGrant) bool {
	return row.RefreshStartedTime != 0 && s.now().Sub(time.UnixMilli(row.RefreshStartedTime)) < staleRefreshClaim
}

func (s *Service) adoptOtherRefresh(row, before *store.ModelOAuthGrant, rejectedFingerprint string) (llm.GrantToken, error) {
	if row.Status == store.ModelOAuthGrantStatusReauthorizationRequired {
		return llm.GrantToken{}, reauthorizationRequired(row.ID)
	}
	if row.AccessToken != "" && row.AccessToken != before.AccessToken {
		return llm.GrantToken{AccessToken: row.AccessToken, ExpiresAt: time.UnixMilli(row.AccessTokenExpiresTime)}, nil
	}
	if token, ok := s.unexpired(row, rejectedFingerprint); ok {
		return token, nil
	}
	return llm.GrantToken{}, fmt.Errorf("OAuth grant %s: %w", row.ID, errRefreshFailedElsewhere)
}

func (s *Service) unexpired(row *store.ModelOAuthGrant, rejectedFingerprint string) (llm.GrantToken, bool) {
	if row.AccessToken == "" || (rejectedFingerprint != "" && llm.TokenFingerprint(row.AccessToken) == rejectedFingerprint) {
		return llm.GrantToken{}, false
	}
	expiresAt := time.UnixMilli(row.AccessTokenExpiresTime)
	if !expiresAt.After(s.now()) {
		return llm.GrantToken{}, false
	}
	return llm.GrantToken{AccessToken: row.AccessToken, ExpiresAt: expiresAt}, true
}

func (s *Service) refreshClaimed(ctx context.Context, row *store.ModelOAuthGrant, claimedVersion int64, rejectedFingerprint string) (llm.GrantToken, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	id := row.ID
	refreshed, err := oauth2cc.Refresh(ctx, s.client, oauth2cc.RefreshRequest{
		TokenURL:     row.TokenEndpoint,
		ClientID:     row.ClientID,
		RefreshToken: row.RefreshToken,
	})
	if errors.Is(err, oauth2cc.ErrRefreshTokenRejected) {
		return s.markReauthorizationRequired(ctx, id, claimedVersion)
	}
	if err != nil {
		s.release(ctx, id, claimedVersion)
		token, ok := s.unexpired(row, rejectedFingerprint)
		s.logger.Warn("OAuth grant refresh failed", "grantID", id, "usingStoredToken", ok, "oauthError", oauth2cc.TokenEndpointErrorCode(err), "error", err)
		if ok {
			return token, nil
		}
		return llm.GrantToken{}, fmt.Errorf("refresh OAuth grant: %w", err)
	}

	tokens := store.ModelOAuthGrantTokens{
		RefreshToken:           refreshed.RefreshToken,
		AccessToken:            refreshed.AccessToken,
		AccessTokenExpiresTime: refreshed.ExpiresAt.UnixMilli(),
	}
	won, err := s.saveRefreshed(ctx, id, claimedVersion, tokens)
	if err != nil {
		s.logger.Error("OAuth grant rotated but its new tokens were not saved", "grantID", id, "attempts", saveAttempts, "error", err)
		return llm.GrantToken{}, err
	}
	if !won {
		s.logger.Debug("OAuth grant changed during its refresh, using the stored tokens", "grantID", id)
		return s.stored(ctx, id)
	}
	s.logger.Debug("OAuth grant refreshed", "grantID", id, "expiresAt", refreshed.ExpiresAt)
	return llm.GrantToken{AccessToken: refreshed.AccessToken, ExpiresAt: refreshed.ExpiresAt}, nil
}

func (s *Service) saveRefreshed(ctx context.Context, id string, claimedVersion int64, tokens store.ModelOAuthGrantTokens) (bool, error) {
	detached := context.WithoutCancel(ctx)
	var err error
	for attempt := 1; attempt <= saveAttempts; attempt++ {
		if attempt > 1 {
			s.logger.Warn("OAuth grant refresh not saved, trying again", "grantID", id, "attempt", attempt-1, "error", err)
			_ = sleep(detached, saveRetryPause)
		}
		saveCtx, cancel := context.WithTimeout(detached, saveTimeout)
		var won bool
		won, err = s.repo.SaveRefreshed(saveCtx, id, claimedVersion, tokens)
		cancel()
		if err == nil {
			return won, nil
		}
	}
	return false, err
}

func (s *Service) release(ctx context.Context, id string, claimedVersion int64) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.repo.ReleaseRefresh(releaseCtx, id, claimedVersion); err != nil {
		s.logger.Warn("OAuth grant refresh claim not released", "grantID", id, "error", err)
	}
}

func (s *Service) markReauthorizationRequired(ctx context.Context, id string, claimedVersion int64) (llm.GrantToken, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	won, err := s.repo.MarkReauthorizationRequired(ctx, id, claimedVersion, lastErrorRefused)
	if err != nil {
		return llm.GrantToken{}, err
	}
	if !won {
		return s.stored(ctx, id)
	}
	s.logger.Warn("OAuth grant refused by its provider, its user must sign in again", "grantID", id)
	return llm.GrantToken{}, reauthorizationRequired(id)
}

func (s *Service) stored(ctx context.Context, id string) (llm.GrantToken, error) {
	row, err := s.Get(ctx, id)
	if err != nil {
		return llm.GrantToken{}, err
	}
	if row.Status == store.ModelOAuthGrantStatusReauthorizationRequired {
		return llm.GrantToken{}, reauthorizationRequired(id)
	}
	if row.AccessToken == "" {
		return llm.GrantToken{}, fmt.Errorf("OAuth grant %s holds no access token", id)
	}
	return llm.GrantToken{AccessToken: row.AccessToken, ExpiresAt: time.UnixMilli(row.AccessTokenExpiresTime)}, nil
}

func reauthorizationRequired(id string) error {
	return fmt.Errorf("OAuth grant %s: %w", id, llm.ErrReauthorizationRequired)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
