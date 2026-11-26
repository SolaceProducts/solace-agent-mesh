// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const defaultAnalyticsPageSize = 20

const maxAnalyticsPageSize = 100

func analyticsControlEndpoints() []control.Endpoint {
	return []control.Endpoint{
		{Method: "get", Path: "analytics/tasks/summary", Description: "Task health summary"},
		{Method: "get", Path: "analytics/tasks/failed", Description: "Failed tasks (paginated)"},
		{Method: "get", Path: "analytics/tasks/slow", Description: "Slowest tasks (paginated)"},
		{Method: "get", Path: "analytics/agents/performance", Description: "Per-agent performance (paginated)"},
		{Method: "get", Path: "analytics/tokens/summary", Description: "Token usage by day or user (paginated)"},
		{Method: "get", Path: "analytics/tasks/query", Description: "General task analytics: filter by agent/user/status + group by agent/user/status/day/week/month (paginated)"},
		{Method: "get", Path: "analytics/scheduledTasks/health", Description: "Scheduled-task health summary"},
		{Method: "get", Path: "analytics/feedback/summary", Description: "Feedback summary"},
	}
}

type analyticsRequest struct {
	StartDate     int64    `json:"startDate"`
	EndDate       int64    `json:"endDate"`
	ExecutionMode string   `json:"executionMode"`
	PageNumber    int      `json:"pageNumber"`
	PageSize      int      `json:"pageSize"`
	ThresholdMs   int64    `json:"thresholdMs"`
	GroupBy       string   `json:"groupBy"`
	AgentName     string   `json:"agentName"`
	UserID        string   `json:"userId"`
	Status        string   `json:"status"`
	GroupDims     []string `json:"groupDims"`
}

func (req analyticsRequest) toQuery() (analytics.Query, error) {
	switch req.ExecutionMode {
	case "", "foreground", "background":
	default:
		return analytics.Query{}, fmt.Errorf("analytics: executionMode must be 'foreground' or 'background'")
	}
	switch req.GroupBy {
	case "", "day", "user":
	default:
		return analytics.Query{}, fmt.Errorf("analytics: groupBy must be 'day' or 'user'")
	}

	page := req.PageNumber
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	switch {
	case pageSize == 0:
		pageSize = defaultAnalyticsPageSize
	case pageSize < 1 || pageSize > maxAnalyticsPageSize:
		return analytics.Query{}, fmt.Errorf("analytics: pageSize must be between 1 and %d", maxAnalyticsPageSize)
	}
	return analytics.Query{
		StartDate:     req.StartDate,
		EndDate:       req.EndDate,
		ExecutionMode: req.ExecutionMode,
		PageNumber:    page,
		PageSize:      pageSize,
		ThresholdMs:   req.ThresholdMs,
		GroupBy:       req.GroupBy,
		AgentName:     req.AgentName,
		UserID:        req.UserID,
		Status:        req.Status,
		GroupDims:     req.GroupDims,
	}, nil
}

func (e *GatewayExecutor) handleAnalytics(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if method != "get" {
		return nil, fmt.Errorf("analytics: unsupported method %q", method)
	}
	if e.svc == nil {
		return nil, fmt.Errorf("analytics: services unavailable")
	}
	if reg := e.svc.FeatureRegistry(); reg == nil || !reg.IsEnabled(features.KeyObservabilityAgent) {
		return nil, fmt.Errorf("analytics: observability_agent feature is not enabled")
	}
	authzEnabled := e.controlAuthzEnabled()
	if err := requireIdentityScope(identity, rbac.ScopeAnalyticsRead, authzEnabled); err != nil {
		return nil, err
	}
	svc, namespace, err := e.analyticsService()
	if err != nil {
		return nil, err
	}

	var req analyticsRequest
	if len(body) > 0 {
		if jsonErr := json.Unmarshal(body, &req); jsonErr != nil {
			return nil, fmt.Errorf("analytics: invalid request body: %w", jsonErr)
		}
	}
	q, err := req.toQuery()
	if err != nil {
		return nil, err
	}
	q.Namespace = namespace

	q.IncludeContent = identityHasScope(identity, rbac.ScopeAnalyticsReadContent, authzEnabled)

	report := strings.Join(path, "/")
	var (
		res  any
		rerr error
	)
	switch report {
	case "tasks/summary":
		res, rerr = svc.TaskHealth(ctx, q)
	case "tasks/failed":
		res, rerr = svc.FailedTasks(ctx, q)
	case "tasks/slow":
		res, rerr = svc.SlowTasks(ctx, q)
	case "agents/performance":
		res, rerr = svc.AgentPerformance(ctx, q)
	case "tokens/summary":
		res, rerr = svc.TokenUsage(ctx, q)
	case "tasks/query":
		res, rerr = svc.QueryTasks(ctx, q)
	case "scheduledTasks/health":
		res, rerr = svc.ScheduledHealth(ctx, q)
	case "feedback/summary":
		res, rerr = svc.FeedbackSummary(ctx, q)
	default:
		return nil, fmt.Errorf("analytics: unknown report %q", report)
	}
	if rerr != nil {
		if errors.Is(rerr, analytics.ErrForbidden) {
			e.logger.Warn("analytics access denied", "report", report, "error", rerr)
			return nil, rerr
		}
		errID := idgen.New()
		e.logger.Error("analytics query failed", "errorId", errID, "report", report, "error", rerr)
		return nil, fmt.Errorf("analytics %s failed (errorId=%s): %w", report, errID, rerr)
	}
	return res, nil
}

func (e *GatewayExecutor) analyticsService() (*analytics.Service, string, error) {
	core := e.storeCore()
	if core == nil {
		return nil, "", fmt.Errorf("analytics unavailable: no gateway instance has a database configured")
	}
	return analytics.NewService(core.Store()).
		WithAgentNameResolver(core.AgentRegistry().DisplayNameResolver()).
		WithLogger(e.logger).
		WithTaskRetentionDays(core.TaskRetentionDays()), core.Namespace(), nil
}

func (e *GatewayExecutor) storeCore() *GatewayCore {
	for _, core := range e.initializedCores() {
		if core.Store() != nil {
			return core
		}
	}
	return nil
}

func (e *GatewayExecutor) controlAuthzEnabled() bool {
	if e.svc == nil {
		return false
	}
	_, noop := e.svc.ControlAuthorizer().(auth.NoopControlAuthorizer)
	return !noop
}

func requireIdentityScope(identity *auth.Identity, required string, authzEnabled bool) error {
	if identityHasScope(identity, required, authzEnabled) {
		return nil
	}
	return fmt.Errorf("insufficient permissions: requires %s", required)
}

func identityHasScope(identity *auth.Identity, required string, authzEnabled bool) bool {
	if identity == nil || len(identity.Scopes) == 0 {
		return !authzEnabled
	}
	for _, granted := range identity.Scopes {
		if auth.MatchScope(granted, required) {
			return true
		}
	}
	return false
}

func identityHasStrictScope(identity *auth.Identity, required string, authzEnabled bool) bool {
	if identity == nil || len(identity.Scopes) == 0 {
		return !authzEnabled
	}
	return rbac.HasStrictScope(identity.Scopes, required)
}
