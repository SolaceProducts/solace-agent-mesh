// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	reportTaskSummary      = "taskSummary"
	reportFailedTasks      = "failedTasks"
	reportSlowTasks        = "slowTasks"
	reportAgentPerformance = "agentPerformance"
	reportTokenUsage       = "tokenUsage"
	reportTaskQuery        = "taskQuery"
	reportScheduledHealth  = "scheduledHealth"
	reportFeedbackSummary  = "feedbackSummary"
)

type analyticsHandler struct {
	core   *entrypoint.GatewayCore
	svc    *analytics.Service
	logger *slog.Logger
}

func (h *analyticsHandler) handleTaskSummary(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, false)
	if !ok {
		return
	}
	res, err := h.svc.TaskHealth(r.Context(), q)
	if err != nil {
		h.fail(w, reportTaskSummary, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *analyticsHandler) handleFailedTasks(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, true)
	if !ok {
		return
	}
	res, err := h.svc.FailedTasks(r.Context(), q)
	if err != nil {
		h.fail(w, reportFailedTasks, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *analyticsHandler) handleSlowTasks(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, true)
	if !ok {
		return
	}
	if raw := r.URL.Query().Get("thresholdMs"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "thresholdMs must be a non-negative integer")
			return
		}
		q.ThresholdMs = v
	}
	res, err := h.svc.SlowTasks(r.Context(), q)
	if err != nil {
		h.fail(w, reportSlowTasks, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *analyticsHandler) handleAgentPerformance(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, true)
	if !ok {
		return
	}
	q.Namespace = h.core.Namespace()
	res, err := h.svc.AgentPerformance(r.Context(), q)
	if err != nil {
		h.fail(w, reportAgentPerformance, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *analyticsHandler) handleTokenUsage(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, true)
	if !ok {
		return
	}
	switch r.URL.Query().Get("groupBy") {
	case "", "day":
		q.GroupBy = "day"
	case "user":
		q.GroupBy = "user"
	default:
		writeError(w, http.StatusBadRequest, "groupBy must be one of: day, user")
		return
	}
	res, err := h.svc.TokenUsage(r.Context(), q)
	if err != nil {
		h.fail(w, reportTokenUsage, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *analyticsHandler) handleTaskQuery(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, true)
	if !ok {
		return
	}
	query := r.URL.Query()
	q.AgentName = strings.TrimSpace(query.Get("agentName"))
	q.UserID = strings.TrimSpace(query.Get("userId"))
	q.Status = strings.TrimSpace(query.Get("status"))
	if g := strings.TrimSpace(query.Get("group")); g != "" {
		for _, d := range strings.Split(g, ",") {
			if d = strings.TrimSpace(d); d != "" {
				q.GroupDims = append(q.GroupDims, d)
			}
		}
	}
	q.Namespace = h.core.Namespace()
	res, err := h.svc.QueryTasks(r.Context(), q)
	if err != nil {
		switch {
		case errors.Is(err, analytics.ErrInvalidQuery):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, analytics.ErrForbidden):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			h.fail(w, reportTaskQuery, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *analyticsHandler) handleScheduledHealth(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, false)
	if !ok {
		return
	}
	q.Namespace = h.core.Namespace()
	res, err := h.svc.ScheduledHealth(r.Context(), q)
	if err != nil {
		h.fail(w, reportScheduledHealth, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *analyticsHandler) handleFeedbackSummary(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, false)
	if !ok {
		return
	}
	res, err := h.svc.FeedbackSummary(r.Context(), q)
	if err != nil {
		h.fail(w, reportFeedbackSummary, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *analyticsHandler) parseQuery(w http.ResponseWriter, r *http.Request, paged bool) (analytics.Query, bool) {
	var q analytics.Query
	query := r.URL.Query()

	if s := query.Get("startDate"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid startDate format (expected ISO 8601)")
			return q, false
		}
		q.StartDate = t.UnixMilli()
	}
	if s := query.Get("endDate"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid endDate format (expected ISO 8601)")
			return q, false
		}
		q.EndDate = t.UnixMilli()
	}
	switch em := query.Get("executionMode"); em {
	case "", "foreground", "background":
		q.ExecutionMode = em
	default:
		writeError(w, http.StatusBadRequest, "executionMode must be one of: foreground, background")
		return q, false
	}

	q.IncludeContent = analyticsIncludeContent(r)

	if paged {
		page, pageSize, err := parsePagination(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return q, false
		}
		q.PageNumber = page
		q.PageSize = pageSize
	}
	return q, true
}

func analyticsIncludeContent(r *http.Request) bool {
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok {
		return true
	}
	return id.HasScope(rbac.ScopeAnalyticsReadContent)
}

func (h *analyticsHandler) fail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, analytics.ErrForbidden) {
		h.logger.Warn("analytics access denied", "report", what, "error", err)
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	h.logger.Error("analytics query failed", "report", what, "error", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
