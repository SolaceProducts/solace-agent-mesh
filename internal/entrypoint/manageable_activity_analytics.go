// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/google/uuid"
)

const (
	activityDeadlineMargin = 5 * time.Second
	activityQueryTimeout   = control.DefaultTimeout - activityDeadlineMargin
)

func validateActivityPathUUID(id string) error {
	if id == "" {
		return errors.New("path taskId is required")
	}
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("path taskId must be a valid UUID")
	}
	return nil
}

func hasStrictActivityReadGrant(identity *auth.Identity) bool {
	return identity != nil && len(identity.Scopes) > 0 &&
		identityHasStrictScope(identity, rbac.ScopeActivityRead, true)
}

func activityControlEndpoints() []control.Endpoint {
	return []control.Endpoint{
		{Method: "get", Path: "activity/tasks/summary", Description: "Task health summary (scope-aware)"},
		{Method: "get", Path: "activity/tasks/failed", Description: "Failed tasks (paginated, scope-aware)"},
		{Method: "get", Path: "activity/tokens/summary", Description: "Token usage by day or user (paginated, scope-aware)"},
		{Method: "get", Path: "activity/agents/performance", Description: "Per-agent activity rollup (paginated)"},
		{Method: "get", Path: "activity/workflows/performance", Description: "Per-workflow-root rollup with subtree token totals (paginated)"},
		{Method: "get", Path: "activity/workflows/breakdown", Description: "Nested workflow → handler breakdown (recursive CTE, paginated)"},
		{Method: "get", Path: "activity/feedback", Description: "Feedback rows — rating, comment, subject, entrypoint, time (scope-aware, no pagination)"},
		{Method: "get", Path: "activity/feedback/summary", Description: "Feedback rollup per agent/workflow plus a recent negative sample (bounded by the caller's read_feedback reach)"},
		{Method: "get", Path: "activity/feedback/rows", Description: "Feedback rows for named agents/workflows or a rater (bounded by the caller's read_feedback reach, capped)"},
		{Method: "get", Path: "activity/feedback/{feedbackId}/session", Description: "Chat session up to one feedback item's rated turn — every turn's task tree (owner, activity:*:read, or read_trace covering every agent in every turn; refused whole otherwise: as not found when the caller cannot read the feedback row, else with a generic trace-access refusal)"},
		{Method: "get", Path: "activity/feedback/task/{taskId}/session", Description: "The same session as activity/feedback/{feedbackId}/session, found by the rated turn's task id; considers only feedback rows on that task the caller can read, newest first, and answers a task with none as not found"},
		{Method: "get", Path: "activity/tasks/{taskId}/trace", Description: "Trace bundle for one task — root metadata, events, descendants (owner, activity:*:read, or read_trace covering the root agent and every agent in the tree; 404-on-miss otherwise)"},
	}
}

type activityRequest struct {
	StartDate     int64  `json:"startDate"`
	EndDate       int64  `json:"endDate"`
	ExecutionMode string `json:"executionMode"`
	PageNumber    int    `json:"pageNumber"`
	PageSize      int    `json:"pageSize"`
	ThresholdMs   int64  `json:"thresholdMs"`
	GroupBy       string `json:"groupBy"`
	TargetUserID  string `json:"userId"`
	HandlerType   string `json:"handlerType"`
	Window        string `json:"window"`
}

const maxTopUsersPageSize = 5000

func (req activityRequest) toQuery(report string) (analytics.Query, error) {
	switch req.ExecutionMode {
	case "", "foreground", "background":
	default:
		return analytics.Query{}, fmt.Errorf("activity: executionMode must be 'foreground' or 'background'")
	}
	switch req.GroupBy {
	case "", "day", "user":
	default:
		return analytics.Query{}, fmt.Errorf("activity: groupBy must be 'day' or 'user'")
	}
	switch req.HandlerType {
	case "", store.TaskHandlerTypeAgent, store.TaskHandlerTypeWorkflow:
	default:
		return analytics.Query{}, fmt.Errorf("activity: handlerType must be 'agent' or 'workflow'")
	}

	page := req.PageNumber
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	maxPageSize := maxAnalyticsPageSize
	if report == "tokens/summary" && req.GroupBy == "user" {
		maxPageSize = maxTopUsersPageSize
	}
	switch {
	case pageSize == 0:
		pageSize = defaultAnalyticsPageSize
	case pageSize < 1 || pageSize > maxPageSize:
		return analytics.Query{}, fmt.Errorf("activity: pageSize must be between 1 and %d", maxPageSize)
	}

	startDate, endDate, werr := resolveActivityWindow(req.Window, req.StartDate, req.EndDate, time.Now())
	if werr != nil {
		return analytics.Query{}, werr
	}

	return analytics.Query{
		StartDate:     startDate,
		EndDate:       endDate,
		ExecutionMode: req.ExecutionMode,
		PageNumber:    page,
		PageSize:      pageSize,
		ThresholdMs:   req.ThresholdMs,
		GroupBy:       req.GroupBy,
		HandlerType:   req.HandlerType,
	}, nil
}

func resolveActivityWindow(window string, start, end int64, now time.Time) (int64, int64, error) {
	nowMs := now.UnixMilli()
	if window != "" {
		dur, err := analytics.ParseWindowDuration(window)
		if err != nil {
			return 0, 0, fmt.Errorf("activity: %w", err)
		}
		return nowMs - dur.Milliseconds(), nowMs, nil
	}
	if end == 0 || end > nowMs {
		end = nowMs
	}
	if start <= 0 {
		start = end - 24*time.Hour.Milliseconds()
	}
	if start >= end {
		return 0, 0, fmt.Errorf("activity: startDate must be strictly before endDate")
	}
	if end-start > analytics.MaxWindow.Milliseconds() {
		return 0, 0, fmt.Errorf("activity: window exceeds %s maximum", analytics.MaxWindow)
	}
	return start, end, nil
}

func (e *GatewayExecutor) handleActivity(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if method != "get" {
		return nil, fmt.Errorf("activity: unsupported method %q", method)
	}
	if e.svc == nil {
		return nil, fmt.Errorf("activity: services unavailable")
	}
	if reg := e.svc.FeatureRegistry(); reg == nil || !reg.IsEnabled(features.KeyActivityMonitor) {
		return nil, fmt.Errorf("activity: activity_monitor feature is not enabled")
	}
	if len(path) == 3 && path[0] == "tasks" && path[2] == "trace" {
		if err := validateActivityPathUUID(path[1]); err != nil {
			return nil, fmt.Errorf("activity: %w", err)
		}
	}
	svc, namespace, err := e.analyticsService()
	if err != nil {
		return nil, err
	}

	var req activityRequest
	if len(body) > 0 {
		if jsonErr := json.Unmarshal(body, &req); jsonErr != nil {
			return nil, fmt.Errorf("activity: invalid request body: %w", jsonErr)
		}
	}
	report := strings.Join(path, "/")
	q, err := req.toQuery(report)
	if err != nil {
		return nil, err
	}
	q.Namespace = namespace

	callerUserID, err := e.resolveActivityCallerUserID(identity)
	if err != nil {
		return nil, err
	}
	isAdmin := hasStrictActivityReadGrant(identity)

	ctx, cancel := context.WithTimeout(ctx, activityQueryTimeout)
	defer cancel()

	if len(path) == 2 && path[0] == "feedback" {
		return e.handleActivityFeedback(ctx, svc, path[1], body, identity, callerUserID, isAdmin)
	}
	if len(path) == 3 && path[0] == "feedback" && path[2] == "session" {
		resp, ferr := e.handleFeedbackSession(ctx, svc, path[1], identity, callerUserID, isAdmin)
		if ferr != nil {
			return nil, ferr
		}
		return resp, nil
	}
	if len(path) == 4 && path[0] == "feedback" && path[1] == "task" && path[3] == "session" {
		resp, ferr := e.handleFeedbackSessionByTask(ctx, svc, path[2], identity, callerUserID, isAdmin)
		if ferr != nil {
			return nil, ferr
		}
		return resp, nil
	}

	if len(path) == 3 && path[0] == "tasks" && path[2] == "trace" {
		tree, terr := e.handleActivityTaskTrace(ctx, svc, path[1], identity, callerUserID, isAdmin)
		if terr != nil {
			return nil, terr
		}
		return tree, nil
	}

	owner, ownerErr := analytics.ResolveOwnerScope(callerUserID, req.TargetUserID, isAdmin)
	if ownerErr != nil {
		e.logger.Warn("activity access denied",
			"reason", "cross-user read requires "+rbac.ScopeActivityRead,
			"callerUser", callerUserID,
			"targetUser", req.TargetUserID,
			"report", strings.Join(path, "/"))
		return nil, fmt.Errorf("activity: targetUserId %q rejected — %w", req.TargetUserID, ownerErr)
	}
	q.OwnerUserID = owner
	q.IncludeContent = true

	var (
		res  any
		rerr error
	)
	switch report {
	case "tasks/summary":
		res, rerr = svc.TaskHealth(ctx, q)
	case "tasks/failed":
		res, rerr = svc.FailedTasks(ctx, q)
	case "tokens/summary":
		res, rerr = svc.TokenUsage(ctx, q)
	case "agents/performance":
		res, rerr = svc.ActivityAgentPerformance(ctx, q)
	case "workflows/performance":
		res, rerr = svc.ActivityWorkflowPerformance(ctx, q)
	case "workflows/breakdown":
		res, rerr = svc.ActivityWorkflowBreakdown(ctx, q)
	case "feedback":
		res, rerr = svc.ActivityFeedback(ctx, q)
	default:
		return nil, fmt.Errorf("activity: unknown report %q", report)
	}
	if rerr != nil {
		if errors.Is(rerr, analytics.ErrForbidden) || errors.Is(rerr, analytics.ErrForbiddenCrossUser) {
			e.logger.Warn("activity access denied", "report", report, "error", rerr)
			return nil, rerr
		}
		errID := idgen.New()
		e.logger.Error(msgActivityQueryFailed, "errorId", errID, "report", report, "error", rerr)
		return nil, fmt.Errorf("activity %s failed (errorId=%s)", report, errID)
	}
	auditTarget, crossUser := analytics.AuditTarget(q.OwnerUserID, callerUserID)
	e.logger.Info("activity read",
		"report", report,
		"callerUser", callerUserID,
		"targetUser", auditTarget,
		"crossUser", crossUser,
		"startDate", q.StartDate,
		"endDate", q.EndDate)
	return res, nil
}

func (e *GatewayExecutor) resolveActivityCallerUserID(identity *auth.Identity) (string, error) {
	if e.controlAuthzEnabled() {
		if identity == nil || identity.UserID == "" {
			return "", fmt.Errorf("activity: an authenticated identity is required")
		}
		return identity.UserID, nil
	}
	if identity != nil && identity.UserID != "" {
		return identity.UserID, nil
	}
	return e.trustOffDefaultIdentity(), nil
}

func (e *GatewayExecutor) trustOffDefaultIdentity() string {
	if core := e.storeCore(); core != nil {
		if cfg := core.Config(); cfg != nil {
			if id := cmp.Or(cfg.ForceUserIdentity, cfg.DefaultUserIdentity); id != "" {
				return id
			}
		}
	}
	return gwauth.DevIdentity().UserID()
}

const (
	taskTraceReport = "tasks/{id}/trace"

	traceArmOwner        = "owner"
	traceArmActivityRead = "activity_read"
	traceArmAgentTrace   = "agent_trace"
)

func (e *GatewayExecutor) handleActivityTaskTrace(ctx context.Context, svc *analytics.Service, taskID string, identity *auth.Identity, callerUserID string, isAdmin bool) (*analytics.TaskTreeResponse, error) {
	tree, arm, err := e.openTaskTrace(ctx, svc, taskID, identity, callerUserID, isAdmin)
	if errors.Is(err, analytics.ErrTaskNotFound) {
		if e.isReadableFeedbackID(ctx, taskID, identity, callerUserID, isAdmin) {
			return &analytics.TaskTreeResponse{FeedbackID: taskID}, nil
		}
		return nil, fmt.Errorf("activity: task %q not found", taskID)
	}
	if err != nil {
		errID := idgen.New()
		e.logger.Error("activity task tree failed", "errorId", errID, "taskID", taskID, "error", err)
		return nil, fmt.Errorf("activity tasks/%s/trace failed (errorId=%s)", taskID, errID)
	}
	e.logger.Info("activity read",
		"report", taskTraceReport,
		"taskID", taskID,
		"callerUser", callerUserID,
		"targetUser", tree.Owner,
		"crossUser", tree.Owner != callerUserID,
		"arm", arm)
	return tree, nil
}

func (e *GatewayExecutor) openTaskTrace(ctx context.Context, svc *analytics.Service, taskID string, identity *auth.Identity, callerUserID string, isAdmin bool) (*analytics.TaskTreeResponse, string, error) {
	if isAdmin {
		tree, err := svc.TaskTree(ctx, taskID, callerUserID, true)
		return tree, traceArmActivityRead, err
	}
	db := e.activityStoreDB()
	if db == nil {
		e.logger.Warn("activity task trace unavailable",
			"report", taskTraceReport, "callerUser", callerUserID, "taskID", taskID)
		return nil, "", analytics.ErrTaskNotFound
	}
	tasks := store.NewTaskRepo(db)
	row, err := tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, "", fmt.Errorf("trace: load task: %w", err)
	}
	if row == nil {
		return nil, "", analytics.ErrTaskNotFound
	}
	if row.UserID == callerUserID {
		tree, ownerErr := svc.TaskTreeOwnedBy(ctx, taskID, callerUserID)
		if !errors.Is(ownerErr, analytics.ErrTaskNotFound) {
			return tree, traceArmOwner, ownerErr
		}
	}
	tree, err := e.openTaskTraceByReach(ctx, svc, db, row, identity, callerUserID)
	return tree, traceArmAgentTrace, err
}

func (e *GatewayExecutor) openTaskTraceByReach(ctx context.Context, svc *analytics.Service, db *store.DB, row *store.Task, identity *auth.Identity, callerUserID string) (*analytics.TaskTreeResponse, error) {
	deps := TraceReachDeps{Tasks: store.NewTaskRepo(db), Events: store.NewTaskEventRepo(db)}
	d, err := TraceReachOpensTask(ctx, deps, callerFeedbackReach(identity), row)
	if err != nil {
		return nil, err
	}
	if !d.Open {
		e.logActivityAccessDenied(taskTraceReport, callerUserID, "taskID", row.ID, d.Reason)
		return nil, analytics.ErrTaskNotFound
	}
	tree, err := svc.TaskTreeWithin(ctx, row.ID, d.ScopedTo)
	if errors.Is(err, analytics.ErrTaskNotFound) {
		e.logActivityAccessDenied(taskTraceReport, callerUserID, "taskID", row.ID, TraceDenyOutsideDecision)
	}
	return tree, err
}
