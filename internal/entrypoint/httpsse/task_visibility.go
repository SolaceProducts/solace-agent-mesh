// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func callerTaskVisibility(r *http.Request, callerUserID string) store.TaskVisibility {
	vis := store.TaskVisibility{OwnerUserID: callerUserID}
	if callerReadsAnyTask(r) {
		vis.Everything = true
		return vis
	}
	reach := callerFeedbackReach(r).AgentTrace
	vis.AllAgentRoots = reach.All
	vis.AgentRootHandlers = agentHandlerNames(reach.IDs)
	return vis
}

func callerFeedbackReach(r *http.Request) rbac.FeedbackReach {
	return callerFeedbackReachCtx(r.Context())
}

func callerFeedbackReachCtx(ctx context.Context) rbac.FeedbackReach {
	id, ok := auth.SubjectFromContext(ctx)
	if !ok || id == nil || len(id.Scopes()) == 0 {
		return rbac.FeedbackReach{}
	}
	return rbac.ResolveFeedbackReach(id.Scopes())
}

func agentHandlerNames(ids []string) []string {
	names := make([]string, 0, 2*len(ids))
	for _, id := range ids {
		if _, _, isCardName := a2a.IDFromInstanceName(id); isCardName {
			continue
		}
		names = append(names, id)
		if _, err := uuid.Parse(id); err == nil {
			names = append(names, a2a.InstanceName(a2a.KindAgent, id))
		}
	}
	return names
}

func traceReachOpensTask(ctx context.Context, deps traceAccessDeps, r *http.Request, task *store.Task) (traceDecision, error) {
	return decideTraceReach(ctx, deps, callerFeedbackReach(r), task)
}

func traceReachOpensTaskCtx(ctx context.Context, deps traceAccessDeps, task *store.Task) (traceDecision, error) {
	return decideTraceReach(ctx, deps, callerFeedbackReachCtx(ctx), task)
}

func decideTraceReach(ctx context.Context, deps traceAccessDeps, reach rbac.FeedbackReach, task *store.Task) (traceDecision, error) {
	d, err := entrypoint.TraceReachOpensTask(ctx, entrypoint.TraceReachDeps{Tasks: deps.tasks, Events: deps.events}, reach, task)
	if err != nil {
		return denied(d.Reason), err
	}
	if !d.Open {
		return denied(d.Reason), nil
	}
	return traceDecision{tier: traceFull, scopedTo: d.ScopedTo, grant: grantAgentTraceFull}, nil
}
