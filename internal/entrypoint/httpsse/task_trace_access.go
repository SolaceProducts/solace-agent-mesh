// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type traceAccess int

const (
	traceDenied traceAccess = iota
	traceFull
)

type traceAccessDeps struct {
	tasks  *store.TaskRepo
	events *store.TaskEventRepo
}

type traceDecision struct {
	tier     traceAccess
	scopedTo []string
	reason   string
	grant    string
}

func denied(reason string) traceDecision { return traceDecision{tier: traceDenied, reason: reason} }

const traceParentChainMaxDepth = entrypoint.TraceParentChainMaxDepth

const stimExportParentChainMaxDepth = 10

func resolveTraceAccess(r *http.Request, deps traceAccessDeps, task *store.Task, callerUserID string) (traceDecision, error) {
	return resolveTraceAccessCtx(r.Context(), deps, task, callerUserID)
}

func resolveTraceAccessCtx(ctx context.Context, deps traceAccessDeps, task *store.Task, callerUserID string) (traceDecision, error) {
	if task.UserID == callerUserID {
		return traceDecision{tier: traceFull}, nil
	}
	if callerReadsAnyTaskCtx(ctx) {
		return traceDecision{tier: traceFull, grant: grantActivityRead}, nil
	}
	return traceReachOpensTaskCtx(ctx, deps, task)
}

func requireOwnedTaskOrTraceFull(w http.ResponseWriter, r *http.Request, h *taskHandler, taskID, callerUserID string) (*store.Task, []string) {
	task, ok := loadTaskForOwnership(w, r, h.taskRepo, h.logger, taskID)
	if !ok {
		return nil, nil
	}
	if task == nil {
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
		return nil, nil
	}
	decision, err := resolveTraceAccess(r, h.traceDeps(), task, callerUserID)
	if err != nil {
		if clientGone(err) {
			return nil, nil
		}
		h.logger.Error("resolve trace access", "taskID", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, nil
	}
	if decision.tier != traceFull {
		warnTaskAccessDenied(h.logger, callerUserID, taskID, decision.reason)
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
		return nil, nil
	}
	if task.UserID != callerUserID {
		auditCrossUserTaskRead(h.logger, callerUserID, task.UserID, task.ID, decision.grant)
	}
	return task, decision.scopedTo
}
