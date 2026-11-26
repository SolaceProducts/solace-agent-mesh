// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package httpsse

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

type consentAuditor interface {
	LogConsentChange(ctx context.Context, event coreauth.ConsentChangeEvent)
}

const consentScopeInstall = "install"

const consentStateUnknown = "unknown"

const msgConsentUnavailable = "usage analytics setting temporarily unavailable"

const getErrorThrottleTTL = 15 * time.Second

const setConsentTimeout = 15 * time.Second

type productAnalyticsConsentHandler struct {
	state            productanalytics.ConsentManager
	audit            consentAuditor
	logger           *slog.Logger
	mu               sync.Mutex
	getErrorLoggedAt time.Time
}

type ConsentResponse struct {
	State string `json:"state"`
}

type UpdateConsentRequest struct {
	Granted *bool `json:"granted"`
}

func (h *productAnalyticsConsentHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	state, err := h.state.ConsentWithError(r.Context())
	if err != nil {
		errID := idgen.New()
		if h.shouldLogGetError() {
			h.logger.Warn("read product analytics consent error", "errorId", errID, "error", err)
		}
		writeErrorWithID(w, http.StatusServiceUnavailable, msgConsentUnavailable, errID)
		return
	}
	writeJSON(w, http.StatusOK, ConsentResponse{State: string(state)})
}

func (h *productAnalyticsConsentHandler) shouldLogGetError() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if !h.getErrorLoggedAt.IsZero() && now.Sub(h.getErrorLoggedAt) < getErrorThrottleTTL {
		return false
	}
	h.getErrorLoggedAt = now
	return true
}

func (h *productAnalyticsConsentHandler) handleSet(w http.ResponseWriter, r *http.Request) {
	var req UpdateConsentRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if req.Granted == nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message:           "validation error",
			ErrorID:           idgen.New(),
			ValidationDetails: map[string][]string{"granted": {"required"}},
		})
		return
	}

	state := productanalytics.ConsentDenied
	if *req.Granted {
		state = productanalytics.ConsentGranted
	}
	previous := consentStateUnknown
	if prior, err := h.state.ConsentWithError(r.Context()); err == nil {
		previous = string(prior)
	} else {
		errID := idgen.New()
		h.logger.Warn("read product analytics consent before write failed", "errorId", errID, "error", err)
	}
	setCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), setConsentTimeout)
	defer cancel()
	if err := h.state.SetConsent(setCtx, state); err != nil {
		errID := idgen.New()
		h.logger.Error("set product analytics consent error", "errorId", errID, "error", err)
		writeErrorWithID(w, http.StatusInternalServerError, msgInternalErr, errID)
		return
	}
	h.auditChange(r, previous, string(state))
	writeJSON(w, http.StatusOK, ConsentResponse{State: string(state)})
}

func (h *productAnalyticsConsentHandler) auditChange(r *http.Request, from, to string) {
	if h.audit == nil {
		return
	}
	actor := "anonymous"
	if id, ok := auth.SubjectFromContext(r.Context()); ok {
		if id.UserID() != "" {
			actor = id.UserID()
		} else if id.Email() != "" {
			actor = id.Email()
		}
	}
	h.audit.LogConsentChange(r.Context(), coreauth.ConsentChangeEvent{
		Actor: actor,
		Scope: consentScopeInstall,
		From:  from,
		To:    to,
	})
}
