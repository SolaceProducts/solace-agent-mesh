// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	reportActivityAgents            = "activityAgents"
	reportActivityWorkflows         = "activityWorkflows"
	reportActivityWorkflowBreakdown = "activityWorkflowBreakdown"
)

type activityHandler struct {
	core   *entrypoint.GatewayCore
	svc    *analytics.Service
	logger *slog.Logger
}

func (h *activityHandler) handleAgentPerformance(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, true)
	if !ok {
		return
	}
	res, err := h.svc.ActivityAgentPerformance(r.Context(), q)
	if err != nil {
		h.fail(w, reportActivityAgents, err)
		return
	}
	h.auditRead(r, reportActivityAgents, q)
	writeJSON(w, http.StatusOK, res)
}

func (h *activityHandler) handleWorkflowPerformance(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, true)
	if !ok {
		return
	}
	res, err := h.svc.ActivityWorkflowPerformance(r.Context(), q)
	if err != nil {
		h.fail(w, reportActivityWorkflows, err)
		return
	}
	h.auditRead(r, reportActivityWorkflows, q)
	writeJSON(w, http.StatusOK, res)
}

func (h *activityHandler) handleWorkflowBreakdown(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseQuery(w, r, true)
	if !ok {
		return
	}
	res, err := h.svc.ActivityWorkflowBreakdown(r.Context(), q)
	if err != nil {
		h.fail(w, reportActivityWorkflowBreakdown, err)
		return
	}
	h.auditRead(r, reportActivityWorkflowBreakdown, q)
	writeJSON(w, http.StatusOK, res)
}

func (h *activityHandler) parseQuery(w http.ResponseWriter, r *http.Request, paged bool) (analytics.Query, bool) {
	q, ok := h.parseCommonQuery(w, r, paged)
	if !ok {
		return q, false
	}

	requestedUserID := r.URL.Query().Get("userId")
	id, haveIdentity := auth.SubjectFromContext(r.Context())
	callerUserID := ""
	if haveIdentity {
		callerUserID = id.UserID()
	}
	if h.core.AuthzEnforced() && haveIdentity && len(id.Scopes()) == 0 {
		h.warnDenied("empty scope set under enforcement", callerUserID, requestedUserID)
		writeError(w, http.StatusForbidden,
			"activity endpoint: empty scope set under enforcement")
		return q, false
	}
	isAdmin := haveIdentity && len(id.Scopes()) > 0 && rbac.HasStrictScope(id.Scopes(), rbac.ScopeActivityRead)
	owner, ownerErr := analytics.ResolveOwnerScope(callerUserID, requestedUserID, isAdmin)
	if ownerErr != nil {
		h.warnDenied("cross-user read requires "+rbac.ScopeActivityRead, callerUserID, requestedUserID)
		writeError(w, http.StatusForbidden,
			"userId query parameter requires the "+rbac.ScopeActivityRead+" scope")
		return q, false
	}
	q.OwnerUserID = owner

	switch v := r.URL.Query().Get("handlerType"); v {
	case "":
	case store.TaskHandlerTypeAgent, store.TaskHandlerTypeWorkflow:
		q.HandlerType = v
	default:
		writeError(w, http.StatusBadRequest,
			"handlerType must be one of: agent, workflow")
		return q, false
	}
	return q, true
}

func (h *activityHandler) parseCommonQuery(w http.ResponseWriter, r *http.Request, paged bool) (analytics.Query, bool) {
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
	nowMs := time.Now().UnixMilli()
	if q.EndDate == 0 || q.EndDate > nowMs {
		q.EndDate = nowMs
	}
	if q.StartDate <= 0 {
		q.StartDate = q.EndDate - (24 * time.Hour).Milliseconds()
	}
	if q.EndDate-q.StartDate > analytics.MaxWindow.Milliseconds() {
		writeError(w, http.StatusBadRequest,
			"window exceeds "+analytics.MaxWindow.String()+" maximum")
		return q, false
	}
	switch em := query.Get("executionMode"); em {
	case "", "foreground", "background":
		q.ExecutionMode = em
	default:
		writeError(w, http.StatusBadRequest,
			"executionMode must be one of: foreground, background")
		return q, false
	}
	q.IncludeContent = true

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

func (h *activityHandler) warnDenied(reason, callerUserID, requestedUserID string) {
	h.logger.Warn("activity access denied",
		"reason", reason,
		"callerUser", callerUserID,
		"targetUser", requestedUserID)
}

func (h *activityHandler) auditRead(r *http.Request, report string, q analytics.Query) {
	caller := ""
	if id, ok := auth.SubjectFromContext(r.Context()); ok {
		caller = id.UserID()
	}
	target, crossUser := analytics.AuditTarget(q.OwnerUserID, caller)
	h.logger.Info("activity read",
		"report", report,
		"callerUser", caller,
		"targetUser", target,
		"crossUser", crossUser,
		"startDate", q.StartDate,
		"endDate", q.EndDate)
}

func (h *activityHandler) fail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, analytics.ErrForbidden) || errors.Is(err, analytics.ErrForbiddenCrossUser) {
		h.logger.Warn("activity access denied", "report", what, "error", err)
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	errID := idgen.New()
	h.logger.Error("activity query failed", "errorId", errID, "report", what, "error", err)
	writeErrorWithID(w, http.StatusInternalServerError, "internal error", errID)
}
