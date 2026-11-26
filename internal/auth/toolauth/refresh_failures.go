// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const refreshRetryBackoff = 30 * time.Second

const refreshEscalateAfter = 15 * time.Minute

type refreshFailureKey struct {
	agentName string
	userID    string
}

func newRefreshFailureKey(toolCtx *tool.Context) refreshFailureKey {
	return refreshFailureKey{agentName: toolCtx.AgentName, userID: toolCtx.UserID}
}

type refreshFailure struct {
	first  time.Time
	last   time.Time
	status int
}

type transientRefreshError struct {
	status int
	cause  error
}

func (e *transientRefreshError) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return "token refresh deferred after a recent failure: " + ErrTransientRefresh.Error()
}

func (e *transientRefreshError) Unwrap() error {
	if e.cause != nil {
		return e.cause
	}
	return ErrTransientRefresh
}

func (h *AuthHandler) recordRefreshFailure(key refreshFailureKey, cause error) error {
	h.refreshFailuresMu.Lock()
	defer h.refreshFailuresMu.Unlock()
	if h.refreshFailures == nil {
		h.refreshFailures = make(map[refreshFailureKey]refreshFailure)
	}
	now := h.clock()
	failure, ok := h.refreshFailures[key]
	if !ok {
		failure.first = now
	}
	failure.last = now
	failure.status = refreshFailureStatus(cause)
	h.refreshFailures[key] = failure
	return h.refreshFailureErrorLocked(failure, cause)
}

func (h *AuthHandler) deferredRefreshFailure(key refreshFailureKey) error {
	h.refreshFailuresMu.Lock()
	defer h.refreshFailuresMu.Unlock()
	failure, ok := h.refreshFailures[key]
	if !ok || h.clock().Sub(failure.last) >= refreshRetryBackoff {
		return nil
	}
	return h.refreshFailureErrorLocked(failure, nil)
}

func (h *AuthHandler) refreshEscalated(key refreshFailureKey) bool {
	h.refreshFailuresMu.Lock()
	defer h.refreshFailuresMu.Unlock()
	failure, ok := h.refreshFailures[key]
	return ok && h.clock().Sub(failure.first) >= refreshEscalateAfter
}

func (h *AuthHandler) forgetRefreshFailure(key refreshFailureKey) {
	h.refreshFailuresMu.Lock()
	defer h.refreshFailuresMu.Unlock()
	delete(h.refreshFailures, key)
}

func (h *AuthHandler) clock() time.Time {
	if h.now == nil {
		return time.Now()
	}
	return h.now()
}

func (h *AuthHandler) refreshFailureErrorLocked(failure refreshFailure, cause error) error {
	if h.clock().Sub(failure.first) >= refreshEscalateAfter {
		return fmt.Errorf("token refresh failing transiently since %s: %w", failure.first.UTC().Format(time.RFC3339), ErrAuthRequired)
	}
	return &transientRefreshError{status: failure.status, cause: cause}
}

func refreshRateLimited(err error) bool {
	transient, ok := errors.AsType[*transientRefreshError](err)
	return ok && transient.status == http.StatusTooManyRequests
}
