// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	taskNotFoundMsg  = "task not found"
	taskDenialStatus = http.StatusNotFound

	msgOwnershipPreflight = "ownership preflight: get task"
)

func requireOwnedTask(
	w http.ResponseWriter,
	r *http.Request,
	deps traceAccessDeps,
	logger *slog.Logger,
	taskID, userID string,
) *store.Task {
	task, ok := authorizeTaskAccess(w, r, deps, logger, taskID, userID)
	return requireTaskRow(w, task, ok)
}

func requireOwnedTaskRawFrames(
	w http.ResponseWriter,
	r *http.Request,
	deps traceAccessDeps,
	logger *slog.Logger,
	taskID, userID string,
) *store.Task {
	task, ok := authorizeRawFrameRead(w, r, deps, logger, taskID, userID)
	return requireTaskRow(w, task, ok)
}

func requireTaskRow(w http.ResponseWriter, task *store.Task, ok bool) *store.Task {
	if !ok {
		return nil
	}
	if task == nil {
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
		return nil
	}
	return task
}

func authorizeTaskAccess(
	w http.ResponseWriter,
	r *http.Request,
	deps traceAccessDeps,
	logger *slog.Logger,
	taskID, userID string,
) (*store.Task, bool) {
	return authorizeTaskRead(w, r, deps, logger, taskID, userID, true)
}

func authorizeRawFrameRead(
	w http.ResponseWriter,
	r *http.Request,
	deps traceAccessDeps,
	logger *slog.Logger,
	taskID, userID string,
) (*store.Task, bool) {
	return authorizeTaskRead(w, r, deps, logger, taskID, userID, false)
}

func authorizeTaskRead(
	w http.ResponseWriter,
	r *http.Request,
	deps traceAccessDeps,
	logger *slog.Logger,
	taskID, userID string,
	traceArm bool,
) (*store.Task, bool) {
	task, ok := loadTaskForOwnership(w, r, deps.tasks, logger, taskID)
	if !ok || task == nil {
		return nil, ok
	}
	if task.UserID == userID {
		return task, true
	}
	if callerReadsAnyTask(r) {
		auditCrossUserTaskRead(logger, userID, task.UserID, taskID, grantActivityRead)
		return task, true
	}
	if !traceArm {
		warnTaskAccessDenied(logger, userID, taskID, closedTraceArmReason(r))
		writeError(w, taskDenialStatus, taskNotFoundMsg)
		return nil, false
	}
	decision, err := traceReachOpensTask(r.Context(), deps, r, task)
	if err != nil {
		logTraceReachError(logger, taskID, err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return nil, false
	}
	if decision.tier == traceFull {
		auditCrossUserTaskRead(logger, userID, task.UserID, taskID, decision.grant)
		return task, true
	}
	warnTaskAccessDenied(logger, userID, taskID, decision.reason)
	writeError(w, taskDenialStatus, taskNotFoundMsg)
	return nil, false
}

func loadTaskForOwnership(
	w http.ResponseWriter,
	r *http.Request,
	repo *store.TaskRepo,
	logger *slog.Logger,
	taskID string,
) (*store.Task, bool) {
	task, err := repo.GetByID(r.Context(), taskID)
	if err != nil {
		logOwnershipPreflightError(logger, taskID, err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return nil, false
	}
	return task, true
}

func logOwnershipPreflightError(logger *slog.Logger, taskID string, err error) {
	if logger == nil || clientGone(err) {
		return
	}
	logger.Error(msgOwnershipPreflight, "taskID", taskID, "error", err)
}

func logTraceReachError(logger *slog.Logger, taskID string, err error) {
	if logger == nil || clientGone(err) {
		return
	}
	logger.Error("trace reach: lookup failed", "taskID", taskID, "error", err)
}

func closedTraceArmReason(r *http.Request) string {
	if callerFeedbackReach(r).AgentTrace.IsEmpty() {
		return denyNoGrant
	}
	return denyTraceArmClosed
}

func authorizeOwnedTaskWrite(
	w http.ResponseWriter,
	r *http.Request,
	repo *store.TaskRepo,
	logger *slog.Logger,
	taskID, userID string,
) (*store.Task, bool) {
	task, ok := loadTaskForOwnership(w, r, repo, logger, taskID)
	if !ok || task == nil {
		return nil, ok
	}
	if task.UserID == userID {
		return task, true
	}
	warnTaskAccessDenied(logger, userID, taskID, denyWriteNotOwner)
	writeError(w, taskDenialStatus, taskNotFoundMsg)
	return nil, false
}

const (
	grantActivityRead   = "activity_read"
	grantAgentTrace     = "agent_trace"
	grantAgentTraceFull = "agent_trace_full"

	denyNoGrant              = entrypoint.TraceDenyNoGrant
	denyTraceArmClosed       = "trace_arm_closed"
	denyParticipantUncovered = entrypoint.TraceDenyParticipantUncovered
	denyWriteNotOwner        = "write_requires_owner"
	denyReadNotOwner         = "read_requires_owner"

	denyChatTaskHeldElsewhere  = "chat_task_held_by_other_owner_or_session"
	denyTaskOwnedByAnotherUser = "task_owned_by_other_user"

	denyRootRowMissing    = entrypoint.TraceDenyRootRowMissing
	denySubjectNotInReach = entrypoint.TraceDenySubjectNotInReach
	denyTreeTooDeep       = entrypoint.TraceDenyTreeTooDeep
	denyLookupFailed      = entrypoint.TraceDenyLookupFailed
)

func auditCrossUserTaskRead(logger *slog.Logger, callerUserID, ownerUserID, taskID, grant string) {
	if logger == nil {
		return
	}
	logger.Info("task read",
		"callerUser", callerUserID,
		"targetUser", ownerUserID,
		"taskID", taskID,
		"crossUser", true,
		"grant", grant)
}

func warnTaskAccessDenied(logger *slog.Logger, callerUserID, taskID, reason string) {
	if logger == nil {
		return
	}
	logger.Warn("task access denied",
		"callerUser", callerUserID,
		"taskID", taskID,
		"reason", reason)
}

func (a *HTTPSSEAdapter) requireTaskStreamOwner(pathValue string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue(pathValue)
		if taskID != "" {
			if !requireUUID(w, taskID, pathValue) {
				return
			}
			if !a.callerMayReadTask(w, r, taskID) {
				return
			}
		}
		next(w, r)
	}
}

func (a *HTTPSSEAdapter) callerMayReadTask(w http.ResponseWriter, r *http.Request, taskID string) bool {
	userID := resolveUserID(r, a.core)

	if a.taskAccess.tasks != nil {
		task, ok := authorizeRawFrameRead(w, r, a.taskAccess, a.logger, taskID, userID)
		if !ok {
			return false
		}
		if task != nil {
			return true
		}
	}

	if a.sseManager != nil {
		if _, owner := a.sseManager.TaskContext(taskID); owner != "" {
			return a.callerIsOwnerOrReader(w, r, userID, owner, taskID)
		}
	}

	if a.core != nil {
		running, known := a.core.Tasks().RunState(taskID)
		info := a.core.Tasks().Get(taskID)
		if !known || (running && info == nil) {
			a.logger.Warn("task record unavailable for the owner check", "taskID", taskID)
			writeError(w, http.StatusServiceUnavailable, "task record temporarily unavailable")
			return false
		}
		if info != nil && info.UserID != "" {
			return a.callerIsOwnerOrReader(w, r, userID, info.UserID, taskID)
		}
	}

	return a.callerMayReadFromEventLog(w, r, userID, taskID)
}

func (a *HTTPSSEAdapter) callerMayReadFromEventLog(w http.ResponseWriter, r *http.Request, userID, taskID string) bool {
	if a.eventOwners == nil {
		return true
	}
	owner, err := a.eventOwners.Owner(r.Context(), taskID)
	if err != nil {
		if !clientGone(err) {
			a.logger.Error("event log owner lookup failed", "taskID", taskID, "error", err)
		}
		writeError(w, http.StatusInternalServerError, errInternal)
		return false
	}
	if owner == "" {
		return true
	}
	return a.callerIsOwnerOrReader(w, r, userID, owner, taskID)
}

func (a *HTTPSSEAdapter) callerIsOwnerOrReader(w http.ResponseWriter, r *http.Request, userID, owner, taskID string) bool {
	if owner == userID {
		return true
	}
	if callerReadsAnyTask(r) {
		auditCrossUserTaskRead(a.logger, userID, owner, taskID, grantActivityRead)
		return true
	}
	warnTaskAccessDenied(a.logger, userID, taskID, denyNoGrant)
	writeError(w, taskDenialStatus, taskNotFoundMsg)
	return false
}

func liveTaskOwner(r *http.Request, core *entrypoint.GatewayCore, logger *slog.Logger, taskID string) string {
	return liveTaskOwnerCtx(r.Context(), core, logger, taskID)
}

func liveTaskOwnerCtx(ctx context.Context, core *entrypoint.GatewayCore, logger *slog.Logger, taskID string) string {
	if core == nil {
		return ""
	}

	if _, err := uuid.Parse(taskID); err != nil {
		return ""
	}

	if info := core.Tasks().Get(taskID); info != nil && info.UserID != "" {
		return info.UserID
	}

	if db := core.Store(); db != nil {
		task, err := store.NewTaskRepo(db).GetByID(ctx, taskID)
		if err != nil {
			logOwnershipPreflightError(logger, taskID, err)
			return ""
		}
		if task != nil {
			return task.UserID
		}
	}

	return ""
}

func callerMayReadLiveTaskCtx(ctx context.Context, core *entrypoint.GatewayCore, logger *slog.Logger, taskID, userID string) bool {
	owner := liveTaskOwnerCtx(ctx, core, logger, taskID)
	if owner == "" {
		return false
	}
	if owner == userID {
		return true
	}
	if callerReadsAnyTaskCtx(ctx) {
		auditCrossUserTaskRead(logger, userID, owner, taskID, grantActivityRead)
		return true
	}
	warnTaskAccessDenied(logger, userID, taskID, denyNoGrant)
	return false
}

func callerIsLiveTaskOwnerCtx(ctx context.Context, core *entrypoint.GatewayCore, logger *slog.Logger, taskID, userID string) bool {
	owner := liveTaskOwnerCtx(ctx, core, logger, taskID)
	if owner != "" && owner == userID {
		return true
	}
	warnTaskAccessDenied(logger, userID, taskID, denyNoGrant)
	return false
}

func callerReadsAnyTask(r *http.Request) bool { return callerReadsAnyTaskCtx(r.Context()) }

func callerReadsAnyTaskCtx(ctx context.Context) bool {
	id, ok := auth.SubjectFromContext(ctx)
	return ok && id != nil && len(id.Scopes()) > 0 && rbac.HasStrictScope(id.Scopes(), rbac.ScopeActivityRead)
}

func bufferedEventReadableBy(e *store.SSEEventBufferRow, callerUserID string, r *http.Request) bool {
	if e.UserID == "" || e.UserID == callerUserID {
		return true
	}
	return callerReadsAnyTask(r)
}
