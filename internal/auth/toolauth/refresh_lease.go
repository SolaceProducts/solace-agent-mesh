// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	refreshLeaseTTL      = 50 * time.Second
	refreshHolderTimeout = 40 * time.Second
	refreshLeasePoll     = 200 * time.Millisecond
)

var refreshFlight singleflight.Group

func (h *AuthHandler) refreshFlightGroup() *singleflight.Group {
	if h.refreshFlights != nil {
		return h.refreshFlights
	}
	return &refreshFlight
}

func (h *AuthHandler) refreshFlightKey(toolCtx *tool.Context) string {
	return strings.Join([]string{toolCtx.AgentName, toolCtx.UserID, h.cfg.CredentialKey, toolCtx.Trigger,
		strconv.FormatBool(toolCtx.AuthPromptUnavailable)}, "\x00")
}

func (h *AuthHandler) refreshExpiredToken(ctx context.Context, toolCtx *tool.Context, taskLogger *slog.Logger) (*OAuthToken, error) {
	ch := h.refreshFlightGroup().DoChan(h.refreshFlightKey(toolCtx), func() (any, error) {
		shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshLeaseTTL+refreshHolderTimeout)
		defer cancel()
		return h.refreshUnderLease(shared, toolCtx, taskLogger)
	})
	select {
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(*OAuthToken), nil
	case <-ctx.Done():
		return nil, refreshInterrupted(ctx.Err())
	}
}

func (h *AuthHandler) refreshUnderLease(ctx context.Context, toolCtx *tool.Context, taskLogger *slog.Logger) (*OAuthToken, error) {
	for {
		token, stored, settled, err := h.tokenToRefresh(ctx, toolCtx, taskLogger)
		if settled {
			return token, err
		}
		if _, versioned := toolCtx.CredentialService.(auth.VersionedCredentialService); !versioned {
			return h.refreshStored(ctx, toolCtx, token, stored, taskLogger)
		}
		if h.refreshLeaseHeld(token) {
			if waitErr := waitForLeaseHolder(ctx); waitErr != nil {
				taskLogger.Warn("gave up waiting for another refresh of this token", "credentialKey", h.cfg.CredentialKey, "error", waitErr)
				return nil, waitErr
			}
			continue
		}
		if token.RefreshStartedAt > 0 {
			taskLogger.Info("taking over a token refresh lease that lapsed; its holder stopped or could not store the token",
				"credentialKey", h.cfg.CredentialKey)
		}
		held, err := h.takeRefreshLease(ctx, toolCtx, token, stored)
		if errors.Is(err, auth.ErrCredentialChanged) {
			continue
		}
		if err != nil {
			taskLogger.Warn("could not take the token refresh lease; refreshing without it", "error", err)
			return h.refreshStored(ctx, toolCtx, token, stored, taskLogger)
		}
		return h.refreshHoldingLease(ctx, toolCtx, token, held, taskLogger)
	}
}

func (h *AuthHandler) tokenToRefresh(ctx context.Context, toolCtx *tool.Context, taskLogger *slog.Logger) (*OAuthToken, auth.VersionedCredential, bool, error) {
	token, stored, err := h.loadToken(ctx, toolCtx)
	if err != nil {
		taskLogger.Warn("failed to load stored token, triggering re-auth", "error", err)
		return nil, stored, true, ErrAuthRequired
	}
	if token == nil || (token.IsExpired() && token.RefreshToken == "") {
		return nil, stored, true, ErrAuthRequired
	}
	if !token.IsExpired() {
		return token, stored, true, nil
	}
	if deferred := h.deferredRefreshFailure(newRefreshFailureKey(toolCtx)); deferred != nil {
		taskLogger.Debug("token refresh deferred after a recent transient failure")
		return nil, stored, true, deferred
	}
	return token, stored, false, nil
}

func waitForLeaseHolder(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return refreshInterrupted(ctx.Err())
	case <-time.After(refreshLeasePoll):
		return nil
	}
}

func (h *AuthHandler) takeRefreshLease(ctx context.Context, toolCtx *tool.Context, token *OAuthToken, stored auth.VersionedCredential) (auth.VersionedCredential, error) {
	leased := *token
	leased.RefreshStartedAt = h.clock().UnixMilli()
	data, err := json.Marshal(&leased)
	if err != nil {
		return auth.VersionedCredential{}, fmt.Errorf("serialize token: %w", err)
	}
	return setAt(ctx, toolCtx.CredentialService, h.tokenRef(toolCtx), string(data), 0, stored)
}

func (h *AuthHandler) refreshHoldingLease(ctx context.Context, toolCtx *tool.Context, token *OAuthToken, held auth.VersionedCredential, taskLogger *slog.Logger) (*OAuthToken, error) {
	holderCtx, cancel := context.WithTimeout(ctx, refreshHolderTimeout)
	defer cancel()
	refreshed, err := h.refreshStored(holderCtx, toolCtx, token, held, taskLogger)
	if err != nil {
		if errors.Is(holderCtx.Err(), context.DeadlineExceeded) {
			taskLogger.Warn("token refresh did not finish in time; releasing the lease", "credentialKey", h.cfg.CredentialKey, "timeout", refreshHolderTimeout)
		}
		h.releaseRefreshLease(ctx, toolCtx, token, held, taskLogger)
	}
	return refreshed, err
}

func (h *AuthHandler) refreshLeaseHeld(token *OAuthToken) bool {
	return token.RefreshStartedAt > 0 && h.clock().Sub(time.UnixMilli(token.RefreshStartedAt)) < refreshLeaseTTL
}

func (h *AuthHandler) releaseRefreshLease(ctx context.Context, toolCtx *tool.Context, token *OAuthToken, held auth.VersionedCredential, taskLogger *slog.Logger) {
	released := *token
	released.RefreshStartedAt = 0
	data, err := json.Marshal(&released)
	if err != nil {
		return
	}
	if _, err := setAt(ctx, toolCtx.CredentialService, h.tokenRef(toolCtx), string(data), 0, held); err != nil && !errors.Is(err, auth.ErrCredentialChanged) {
		taskLogger.Warn("could not release the token refresh lease; it lapses on its own", "credentialKey", h.cfg.CredentialKey, "error", err)
	}
}

func (h *AuthHandler) refreshStored(ctx context.Context, toolCtx *tool.Context, token *OAuthToken, stored auth.VersionedCredential, taskLogger *slog.Logger) (*OAuthToken, error) {
	failureKey := newRefreshFailureKey(toolCtx)
	refreshed, err := h.refreshStoredToken(ctx, toolCtx, token, stored)
	if err == nil {
		h.forgetRefreshFailure(failureKey)
		return refreshed, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, refreshInterrupted(ctxErr)
	}
	if errors.Is(err, ErrTransientRefresh) {
		failure := h.recordRefreshFailure(failureKey, err)
		if errors.Is(failure, ErrAuthRequired) {
			taskLogger.Warn("token refresh still failing transiently, asking for sign-in and preserving credential", RefreshFailureLogAttrs(err)...)
		} else {
			taskLogger.Warn("token refresh failed due to transient error, preserving credential", RefreshFailureLogAttrs(err)...)
		}
		return nil, failure
	}
	h.forgetRefreshFailure(failureKey)
	if current := h.freshTokenAfterRejectedRefresh(ctx, toolCtx, stored); current != nil {
		taskLogger.Debug("token refresh rejected after another writer stored a fresh token; using it")
		return current, nil
	}
	taskLogger.Warn("token refresh rejected, requesting re-auth", RefreshFailureLogAttrs(err)...)
	return nil, ErrAuthRequired
}

func refreshInterrupted(err error) error {
	return fmt.Errorf("token refresh interrupted: %w", err)
}
