// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	TraceDenyNoGrant              = "no_grant"
	TraceDenyParticipantUncovered = "participant_not_in_trace_reach"
	TraceDenyRootRowMissing       = "root_row_missing"
	TraceDenySubjectNotInReach    = "subject_not_in_reach"
	TraceDenyTreeTooDeep          = "tree_too_deep"
	TraceDenyLookupFailed         = "lookup_failed"
	TraceDenyOutsideDecision      = "tree_outside_decision"
)

type TraceReachDeps struct {
	Tasks  *store.TaskRepo
	Events *store.TaskEventRepo
}

type TraceReachDecision struct {
	Open     bool
	ScopedTo []string
	Reason   string
}

func traceDenied(reason string) TraceReachDecision { return TraceReachDecision{Reason: reason} }

func TraceReachOpensTask(ctx context.Context, deps TraceReachDeps, reach rbac.FeedbackReach, task *store.Task) (TraceReachDecision, error) {
	if reach.AgentTrace.IsEmpty() {
		return traceDenied(TraceDenyNoGrant), nil
	}
	treeIDs, root, err := loadTaskTree(ctx, deps.Tasks, task)
	if errors.Is(err, ErrTaskTreeTooDeep) {
		return traceDenied(TraceDenyTreeTooDeep), nil
	}
	if err != nil {
		return traceDenied(TraceDenyLookupFailed), err
	}
	if root == nil || root.ParentTaskID != nil {
		return traceDenied(TraceDenyRootRowMissing), nil
	}
	if !rootAgentInTraceReach(root, reach) {
		return traceDenied(TraceDenySubjectNotInReach), nil
	}
	handlers, err := deps.Tasks.HandlersForTasks(ctx, treeIDs)
	if err != nil {
		return traceDenied(TraceDenyLookupFailed), fmt.Errorf("trace access: participants: %w", err)
	}
	if reason := traceRowsCovered(root, handlers, reach); reason != "" {
		return traceDenied(reason), nil
	}
	covered, err := everyForwardedPeerCovered(ctx, deps, treeIDs, handlers, reach)
	if err != nil {
		return traceDenied(TraceDenyLookupFailed), err
	}
	if !covered {
		return traceDenied(TraceDenyParticipantUncovered), nil
	}
	return TraceReachDecision{Open: true, ScopedTo: treeIDs}, nil
}

func rootAgentInTraceReach(root *store.Task, reach rbac.FeedbackReach) bool {
	if root.ParentTaskID != nil {
		return false
	}
	subject, ok := DeriveFeedbackSubject(root)
	return ok && subject.Type == store.SubjectTypeAgent && reach.TracesAgent(subject.ID)
}

var ErrTaskTreeTooDeep = errors.New("task tree exceeds the depth bound")

const TraceParentChainMaxDepth = store.TaskTreeMaxDepth

func loadTaskTree(ctx context.Context, tasks *store.TaskRepo, task *store.Task) ([]string, *store.Task, error) {
	chain, err := tasks.GetParentChain(ctx, task.ID, TraceParentChainMaxDepth)
	if err != nil {
		return nil, nil, fmt.Errorf("trace access: parent chain: %w", err)
	}
	if len(chain) > TraceParentChainMaxDepth {
		return nil, nil, ErrTaskTreeTooDeep
	}
	rootID := task.ID
	if len(chain) > 0 {
		rootID = chain[len(chain)-1]
	}
	descendants, atBound, err := tasks.DescendantIDs(ctx, rootID)
	if err != nil {
		return nil, nil, fmt.Errorf("trace access: descendants: %w", err)
	}
	if atBound {
		return nil, nil, ErrTaskTreeTooDeep
	}
	treeIDs := append(append([]string{}, chain...), descendants...)
	if rootID == task.ID {
		return treeIDs, task, nil
	}
	root, err := tasks.GetByID(ctx, rootID)
	if err != nil {
		return nil, nil, fmt.Errorf("trace access: load root: %w", err)
	}
	return treeIDs, root, nil
}

func traceRowsCovered(root *store.Task, handlers []store.TaskHandler, reach rbac.FeedbackReach) string {
	if !rootAgentInTraceReach(root, reach) {
		return TraceDenySubjectNotInReach
	}
	if !everyAgentCovered(handlers, reach) {
		return TraceDenyParticipantUncovered
	}
	return ""
}

func everyForwardedPeerCovered(ctx context.Context, deps TraceReachDeps, treeIDs []string, handlers []store.TaskHandler, reach rbac.FeedbackReach) (bool, error) {
	peers, err := deps.Events.ForwardedPeerNames(ctx, treeIDs)
	if err != nil {
		return false, fmt.Errorf("trace access: forwarded peers: %w", err)
	}
	return everyPeerCovered(peers, handlers, reach), nil
}

func everyPeerCovered(peers []string, handlers []store.TaskHandler, reach rbac.FeedbackReach) bool {
	for _, name := range peers {
		id, kind := forwardedPeerSubject(name, handlers)
		if kind == store.SubjectTypeWorkflow {
			continue
		}
		if id == "" || !reach.TracesAgent(id) {
			return false
		}
	}
	return true
}

func forwardedPeerSubject(name string, handlers []store.TaskHandler) (id, kind string) {
	if name == "" {
		return "", ""
	}
	if instanceID, instanceKind, isInstance := a2a.IDFromInstanceName(name); isInstance {
		if instanceKind == a2a.KindWorkflow {
			return instanceID, store.SubjectTypeWorkflow
		}
		return instanceID, store.SubjectTypeAgent
	}
	for _, h := range handlers {
		if h.HandlerName != name {
			continue
		}
		if subjectID, subjectKind, ok := participantSubject(h); ok {
			return subjectID, subjectKind
		}
		return "", ""
	}
	return name, store.SubjectTypeAgent
}

func everyAgentCovered(handlers []store.TaskHandler, reach rbac.FeedbackReach) bool {
	agents := 0
	for _, h := range handlers {
		id, kind, ok := participantSubject(h)
		if !ok {
			return false
		}
		if kind != rbac.SubjectKindAgent {
			continue
		}
		agents++
		if !reach.TracesAgent(id) {
			return false
		}
	}
	return agents > 0
}

func participantSubject(h store.TaskHandler) (id, kind string, ok bool) {
	if h.HandlerName == "" || h.HandlerType == "" {
		return "", "", false
	}
	name, handlerType := h.HandlerName, h.HandlerType
	subject, ok := DeriveFeedbackSubject(&store.Task{HandlerName: &name, HandlerType: &handlerType})
	if !ok {
		return "", "", false
	}
	return subject.ID, subject.Type, true
}
