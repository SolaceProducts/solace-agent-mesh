// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func shortID(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 8 {
		s = s[len(s)-8:]
	}
	return s
}

type DAGExecutor struct {
	cfg      *WorkflowConfig
	dag      *DependencyGraph
	state    *WorkflowExecutionState
	execCtx  *WorkflowExecutionContext
	instance *WorkflowInstance
	logger   *slog.Logger

	mu sync.Mutex

	parkedPauses map[string]parkedStep

	reconstructed bool

	parkWritten      bool
	parkRetryPending bool
	parkUnsure       bool

	claimMu      sync.Mutex
	unsureClaims []string

	unsureClaimCount atomic.Int32

	lost bool

	checkpoint checkpointCursor

	panickedNodes sync.Map

	staleRefWarnedSites sync.Map

	approvalWarnedTools sync.Map

	emptyImageNodesWarned sync.Map

	refusalBudget       context.Context
	cancelRefusalBudget context.CancelFunc
	refusalsDropped     int

	inputPreviews map[string]string
}

func (e *DAGExecutor) Advance(ctx context.Context, event Event) error {
	e.mu.Lock()
	parked := false
	defer func() {
		e.endRefusalBudget()
		e.mu.Unlock()
		if parked && e.instance != nil {
			e.instance.evictParked(e)
		}
	}()

	if e.parkWritten || e.lost {
		return nil
	}
	if e.state.Finalized {
		e.reconcileTimers()
		return nil
	}
	if e.instance != nil && e.instance.cutByProcessExit() {
		return nil
	}

	e.applyEvent(ctx, event)

	if !e.state.Finalized {
		e.progress(ctx)
	}

	e.reconcileTimers()
	if !e.state.Finalized && !e.lost {
		e.writeCheckpointLocked()
	}
	e.settleRequestLocked()
	parked = e.parkWritten || e.lost
	return nil
}

func (e *DAGExecutor) reconcileTimers() {
	if e.instance == nil || e.instance.timerManager == nil {
		return
	}
	tm := e.instance.timerManager
	execID := e.state.ExecutionID
	if e.state.Finalized {
		tm.Cancel(execID)
		return
	}
	snap := append([]Timer(nil), e.state.Timers...)
	tm.Schedule(execID, snap)
}

func skipMetadata(reason string, unresolvedReference bool) map[string]any {
	metadata := map[string]any{"skip_reason": reason}
	if unresolvedReference {
		metadata["unresolved_reference"] = true
	}
	return metadata
}

func (e *DAGExecutor) staleRefWarnedOnce(purpose, nodeID string) bool {
	_, warned := e.staleRefWarnedSites.LoadOrStore(purpose+":"+nodeID, struct{}{})
	return warned
}

func (e *DAGExecutor) namedOutputNodes() schemaDerived {
	if e.instance == nil {
		return nil
	}
	return e.instance.namedOutputNodes
}

func (e *DAGExecutor) applyEvent(ctx context.Context, event Event) {
	switch evt := event.(type) {
	case *EventStart:
		if violations := e.instance.inputViolations(evt.Input); len(violations) > 0 {
			e.rejectInvalidInput(violations)
			return
		}
		e.initWorkflowTimeout(ctx)
		e.logger.Info("starting workflow execution",
			"executionID", e.state.ExecutionID,
			"nodeCount", len(e.cfg.Nodes),
		)

	case *EventRestore:
		e.redispatchInFlightSteps()

	case *EventNodeComplete:
		e.handleCompletion(NodeCompletion{
			NodeID:     evt.NodeID,
			SubTaskID:  evt.SubTaskID,
			Output:     evt.Output,
			MapNodeID:  evt.MapNodeID,
			LoopNodeID: evt.LoopNodeID,
			ItemIndex:  evt.ItemIndex,
		})

	case *EventNodeFailed:
		e.handleCompletion(NodeCompletion{
			NodeID:     evt.NodeID,
			SubTaskID:  evt.SubTaskID,
			Error:      evt.Err,
			ErrorKind:  evt.ErrKind,
			MapNodeID:  evt.MapNodeID,
			LoopNodeID: evt.LoopNodeID,
			ItemIndex:  evt.ItemIndex,
		})

	case *EventNodePaused:
		e.handlePause(ctx, evt)

	case *EventNodeApproved:
		e.handleApproved(evt)

	case *EventWake:
		e.handleWake(ctx)

	case *EventSignal:
		e.handleSignal(evt)

	case *EventRetryDue:
		e.handleCompletion(NodeCompletion{
			NodeID: evt.NodeID,
			Retry:  true,
		})

	case *EventNodeTimeout:
		msg := evt.TimeoutMsg
		if msg == "" {
			if evt.Duration > 0 {
				msg = fmt.Sprintf("node %q timed out after %s", evt.NodeID, evt.Duration)
			} else {
				msg = fmt.Sprintf("node %q timed out", evt.NodeID)
			}
		}
		e.handleCompletion(NodeCompletion{
			NodeID:     evt.NodeID,
			SubTaskID:  evt.SubTaskID,
			Error:      fmt.Errorf("%s", msg),
			ErrorKind:  ErrorKindError,
			MapNodeID:  evt.MapNodeID,
			LoopNodeID: evt.LoopNodeID,
			ItemIndex:  evt.ItemIndex,
		})

	case *EventWorkflowTimeout:
		if !e.state.InExitHandler {
			e.endEarly(a2a.TerminationWorkflowTimeout)
			e.state.Cancelled = true
			e.cancelInFlightToolNodes()
		}
		e.state.RemoveTimer(TimerKindWorkflowTimeout, "")

	case *EventCancel:
		if e.state.InExitHandler || e.state.Finalized {
			return
		}
		e.endEarly(a2a.TerminationCancelled)
		e.state.Cancelled = true
		e.cancelInFlightToolNodes()
		e.instance.cancelActiveSubTasksForState(e.state)

	case *EventInstanceStopping:
		e.finalizeForInstanceStop()
	}
}

func (e *DAGExecutor) finalizeForInstanceStop() {
	if e.state.Finalized {
		return
	}
	if e.state.InExitHandler {
		e.logger.Warn("instance stopping during on_exit chain; abandoning remaining exit handlers",
			"executionID", e.state.ExecutionID,
			"outcome", e.state.PendingOutcome,
			"pendingNodes", slices.Sorted(maps.Keys(e.state.PendingNodes)),
			"queuedHandlers", e.state.ExitHandlerQueue,
		)
	} else {
		e.endEarly(a2a.TerminationInstanceStopped)
		e.state.Cancelled = true
		e.cancelInFlightToolNodes()
		e.instance.cancelActiveSubTasksForState(e.state)
		e.cancelAllNodeTimers()
		e.state.PendingOutcome = "failure"
		e.state.PendingErrorMsg = workflowInstanceStoppedMsg
	}
	e.publishFinalResponse()
}

func (e *DAGExecutor) handlePause(ctx context.Context, evt *EventNodePaused) {
	if !e.state.PendingNodes[evt.NodeID] {
		e.logger.Debug("workflow HIL: pause skipped; node no longer pending",
			"node", evt.NodeID, "requestID", evt.RequestID)
		evt.unparked = e.whyNodeIsNoLongerPending()
		return
	}
	e.cancelNodeTimer(evt.SubTaskID)
	if e.evalRun() {
		e.refusePauseInEvalRun(ctx, evt)
		return
	}
	if err := e.instance.pauseWorkflowNode(ctx, e, evt); err != nil {
		if errors.Is(err, errRunLost) {
			evt.unparked = tool.RefusalRunEnded
			return
		}
		e.logger.Error("workflow HIL pause failed; failing the node",
			"node", evt.NodeID, "requestID", evt.RequestID, "executionID", e.state.ExecutionID, "error", err)
		if errors.Is(err, errPauseRowStillOpen) {
			evt.parked = true
		} else {
			evt.unparked = tool.RefusalPromptFailed
		}
		if evt.Kind == PauseKindApproval {
			e.auditApprovalRefusal(ctx, evt.ToolName, tool.RefusalPromptFailed)
		}
		e.handleCompletion(NodeCompletion{
			NodeID:     evt.NodeID,
			SubTaskID:  evt.SubTaskID,
			Error:      fmt.Errorf("workflow HIL pause failed: %w", err),
			ErrorKind:  ErrorKindError,
			MapNodeID:  evt.MapNodeID,
			LoopNodeID: evt.LoopNodeID,
			ItemIndex:  evt.ItemIndex,
		})
		return
	}
	evt.parked = true
	e.logger.Info("workflow node paused for HIL input",
		"node", evt.NodeID, "requestID", evt.RequestID, "executionID", e.state.ExecutionID)
}

func (e *DAGExecutor) whyNodeIsNoLongerPending() tool.ApprovalRefusal {
	if e.state.PendingOutcome != "" {
		return tool.RefusalRunEnded
	}
	return tool.RefusalTimedOut
}

func (e *DAGExecutor) handleApproved(evt *EventNodeApproved) {
	if !e.state.PendingNodes[evt.NodeID] {
		e.logger.Debug("workflow HIL: approval dispatch skipped; node no longer pending",
			"node", evt.NodeID, "tool", evt.ToolName)
		e.refuseApprovedCall(evt, hilRefusalResult(tool.RefusalRunEnded))
		return
	}
	toolNodeID := cmp.Or(evt.ToolNodeID, evt.NodeID)
	node := e.dag.Nodes[toolNodeID]
	if node == nil {
		vanished := fmt.Errorf("approved tool node %q is no longer in the workflow", toolNodeID)
		e.refuseApprovedCall(evt, tool.PreDispatchFailure(vanished))
		e.handleCompletion(NodeCompletion{
			NodeID:     evt.NodeID,
			SubTaskID:  evt.SubTaskID,
			Error:      vanished,
			ErrorKind:  ErrorKindError,
			MapNodeID:  evt.MapNodeID,
			LoopNodeID: evt.LoopNodeID,
			ItemIndex:  evt.ItemIndex,
		})
		return
	}
	routing := NodeCompletion{
		NodeID:     evt.NodeID,
		SubTaskID:  evt.SubTaskID,
		MapNodeID:  evt.MapNodeID,
		LoopNodeID: evt.LoopNodeID,
		ItemIndex:  evt.ItemIndex,
	}
	if !e.stillHoldsLocked("dispatch an approved tool") {
		return
	}
	e.armNodeTimer(node, evt.SubTaskID, routing)
	e.logger.Info("workflow HIL: approval granted; dispatching tool node",
		"node", evt.NodeID, "tool", evt.ToolName)
	e.invokeApprovedToolNode(node, evt.Args, routing, evt.retry)
}

func (e *DAGExecutor) cancelInFlightToolNodes() {
	if e.execCtx != nil && e.execCtx.ToolCancel != nil {
		e.execCtx.ToolCancel()
	}
}

func (e *DAGExecutor) progress(_ context.Context) {
	startTime := e.state.StartTime

	for {
		if e.state.Finalized {
			return
		}
		if e.state.InExitHandler {
			return
		}
		if e.cancelRequested() {
			e.cancelAndFinalize()
			return
		}

		readyNodes := e.getReadyNodes()
		if len(readyNodes) == 0 {
			if e.state.InExitHandler {
				return
			}
			if len(e.state.PendingNodes) > 0 {
				return
			}
			e.finalizeSuccess(time.Since(startTime))
			return
		}

		parallelGroupID := e.assignParallelGroup(readyNodes)
		if stop := e.dispatchReady(readyNodes, parallelGroupID); stop {
			return
		}
	}
}

func (e *DAGExecutor) initWorkflowTimeout(_ context.Context) {
	if e.cfg.WorkflowTimeout <= 0 || !e.state.WorkflowDeadline.IsZero() {
		return
	}
	e.state.WorkflowDeadline = time.Now().Add(e.cfg.WorkflowTimeout)
	e.state.AddTimer(Timer{
		Kind:    TimerKindWorkflowTimeout,
		FiresAt: e.state.WorkflowDeadline,
	})
}

func (e *DAGExecutor) assignParallelGroup(readyNodes []string) string {
	if len(readyNodes) > 1 {
		groupID := idgen.New()
		if e.state.ParallelBranchAssignments == nil {
			e.state.ParallelBranchAssignments = make(map[string]map[string]int)
		}
		assignments := make(map[string]int, len(readyNodes))
		for i, nodeID := range readyNodes {
			assignments[nodeID] = i
		}
		e.state.ParallelBranchAssignments[groupID] = assignments
		return groupID
	}
	if groupID, branchIdx, ok := e.getInheritedBranch(readyNodes[0]); ok {
		e.state.ParallelBranchAssignments[groupID][readyNodes[0]] = branchIdx
		return groupID
	}
	return ""
}

func (e *DAGExecutor) dispatchReady(readyNodes []string, parallelGroupID string) bool {
	for _, nodeID := range readyNodes {
		if e.cancelRequested() {
			return true
		}
		if stop := e.dispatchReadyNode(nodeID, parallelGroupID); stop {
			return true
		}
	}
	return false
}

func (e *DAGExecutor) dispatchReadyNode(nodeID, parallelGroupID string) bool {
	node := e.dag.Nodes[nodeID]

	if node.When != "" {
		proceed, stop := e.evaluateWhenClause(nodeID, node.When)
		if stop {
			return true
		}
		if !proceed {
			return false
		}
	}

	var subTaskID string
	if node.Type == "agent" || node.Type == "workflow" || node.Type == "tool" || node.Type == "decision" {
		subTaskID = idgen.New()
	}
	e.publishNodeStart(node, parallelGroupID, subTaskID)

	dctx, dcancel := e.dispatchCtx()
	defer dcancel()
	switch node.Type {
	case "agent", "workflow":
		e.dispatchAgentNode(dctx, node, subTaskID)
	case "tool":
		e.dispatchToolNode(dctx, node, subTaskID)
	case "decision":
		e.dispatchDecisionNode(dctx, node, subTaskID)
	case "switch":
		e.executeSwitchNode(node)
	case "map":
		e.executeMapNode(dctx, node)
	case "loop":
		e.executeLoopNode(dctx, node)
	case NodeTypeTimer:
		e.dispatchTimerNode(node)
	case NodeTypeWait:
		e.dispatchWaitNode(dctx, node)
	default:
		retrying := e.handleNodeError(nodeID, fmt.Errorf("unknown node type %q", node.Type))
		if !retrying && e.cfg.FailFast {
			return true
		}
	}
	return false
}

func (e *DAGExecutor) evaluateWhenClause(nodeID, expr string) (proceed, stop bool) {
	shouldRun, stale, err := evaluateCondition(expr, e.state.NodeOutputs, e.namedOutputNodes())
	if isOrdinaryNotMatched(err) {
		reason := "when clause depends on a workflow input the caller did not supply"
		if errors.Is(err, errNotProduced) {
			reason = "when clause depends on a value no node produced and the caller did not supply"
			e.logger.Debug("when clause depends on a value no node produced and the caller did not supply; treating as not matched",
				"node", nodeID, "error", err)
		} else {
			e.logger.Debug("when clause depends on a workflow input the caller did not supply; treating as not matched",
				"node", nodeID, "error", err)
		}
		e.skipNodeCause(nodeID, reason, SkipCauseConditionFalse, false)
		return false, false
	}
	if errors.Is(err, ErrUnresolvedReference) {
		e.logger.Warn("when clause references an unresolved field; treating as not matched",
			"node", nodeID, "error", err)
		e.skipNodeCause(nodeID, "when clause references a field the upstream node did not produce", SkipCauseUnresolved, true)
		return false, false
	}
	if err != nil {
		e.logger.Warn("condition evaluation error", "node", nodeID, "error", err)
		retrying := e.handleNodeError(nodeID, err)
		return false, !retrying && e.cfg.FailFast
	}
	if stale != nil {
		e.logger.Warn("when clause references an unresolved field but the rest settles it; deciding on that",
			"node", nodeID, "matched", shouldRun, "error", stale)
	}
	if !shouldRun {
		e.skipNodeCause(nodeID, "condition not met", SkipCauseConditionFalse, stale != nil)
		return false, false
	}
	return true, false
}

func (e *DAGExecutor) cancelRequested() bool {
	return e.state.Cancelled || e.ctxEnded() || e.deadlineElapsed()
}

func (e *DAGExecutor) ctxEnded() bool {
	return e.execCtx != nil && e.execCtx.Ctx != nil && e.execCtx.Ctx.Err() != nil
}

func (e *DAGExecutor) deadlineElapsed() bool {
	return !e.state.WorkflowDeadline.IsZero() && !time.Now().Before(e.state.WorkflowDeadline)
}

func (e *DAGExecutor) dispatchCtx() (context.Context, context.CancelFunc) {
	if e.state.WorkflowDeadline.IsZero() {
		return e.execCtx.Ctx, noopCancel
	}
	return context.WithDeadline(e.execCtx.Ctx, e.state.WorkflowDeadline)
}

func noopCancel() {
}

func (e *DAGExecutor) getReadyNodes() []string {
	var ready []string
	for _, node := range e.cfg.Nodes {
		if e.state.IsNodeCompleted(node.ID) || e.state.IsNodePending(node.ID) || e.state.IsNodeSkipped(node.ID) {
			continue
		}

		if e.dag.InnerNodes[node.ID] {
			continue
		}

		allDeps := true
		for _, dep := range node.waitsFor() {
			if !e.state.IsNodeCompleted(dep) && !e.state.IsNodeSkipped(dep) {
				allDeps = false
				break
			}
		}
		if allDeps {
			ready = append(ready, node.ID)
		}
	}
	return ready
}

func (e *DAGExecutor) getInheritedBranch(nodeID string) (groupID string, branchIdx int, ok bool) {
	node, exists := e.dag.Nodes[nodeID]
	if !exists || len(node.waitsFor()) == 0 {
		return "", 0, false
	}

	if e.state.ParallelBranchAssignments == nil {
		return "", 0, false
	}

	type branchInfo struct {
		groupID   string
		branchIdx int
	}

	var parallelDeps []branchInfo
	for _, depID := range node.waitsFor() {
		if e.state.IsNodeSkipped(depID) {
			continue
		}

		found := false
		for gID, assignments := range e.state.ParallelBranchAssignments {
			if idx, inGroup := assignments[depID]; inGroup {
				parallelDeps = append(parallelDeps, branchInfo{gID, idx})
				found = true
				break
			}
		}
		if !found {
			parallelDeps = append(parallelDeps, branchInfo{})
		}
	}

	var inParallel []branchInfo
	for _, b := range parallelDeps {
		if b.groupID != "" {
			inParallel = append(inParallel, b)
		}
	}

	if len(inParallel) == 0 {
		return "", 0, false
	}

	first := inParallel[0]
	for _, b := range inParallel[1:] {
		if b.groupID != first.groupID || b.branchIdx != first.branchIdx {
			return "", 0, false
		}
	}

	return first.groupID, first.branchIdx, true
}

func (e *DAGExecutor) allNodesComplete() bool {
	for _, node := range e.cfg.Nodes {
		if e.dag.InnerNodes[node.ID] {
			continue
		}
		if !e.state.IsNodeCompleted(node.ID) && !e.state.IsNodeSkipped(node.ID) {
			return false
		}
	}
	return true
}

func (e *DAGExecutor) cancelAndFinalize() bool {
	if !e.ctxEnded() && e.deadlineElapsed() {
		e.endEarly(a2a.TerminationWorkflowTimeout)
	}
	e.state.Cancelled = true
	e.finalizeCancelled()
	return false
}

func (e *DAGExecutor) handleCompletion(nc NodeCompletion) {
	e.recordDecisionUsage(nc)

	if e.state.InExitHandler {
		if !e.isExitHandlerNode(nc.NodeID) {
			e.logger.Debug("dropping straggler completion during exit chain", "node", nc.NodeID)
			return
		}
		e.advanceExitHandlers(nc)
		return
	}
	if e.cancelRequested() {
		return
	}
	nodeID := nc.NodeID

	if nc.MapNodeID != "" {
		e.handleMapChildCompletion(nc)
		return
	}

	if nc.LoopNodeID != "" {
		e.handleLoopIterationComplete(e.execCtx.Ctx, nc.LoopNodeID, nc)
		return
	}

	if nc.Retry {
		if e.state.IsNodeCompleted(nodeID) || e.state.IsNodeSkipped(nodeID) {
			e.logger.Debug("ignoring retry for already-finished node", "node", nodeID)
			return
		}
		e.redispatchNode(nodeID)
		return
	}

	if e.state.IsNodeCompleted(nodeID) || e.state.IsNodeSkipped(nodeID) {
		e.logger.Debug("ignoring duplicate completion for finished node", "node", nodeID)
		return
	}

	if nc.SubTaskID != "" {
		e.cancelNodeTimer(nc.SubTaskID)
	}

	e.state.RemovePending(nodeID)

	if nc.Error != nil {
		e.handleNodeErrorKind(nodeID, nc.Error, nc.ErrorKind)
		return
	}

	if nc.Output != nil {
		e.state.NodeOutputs[nodeID] = map[string]any{"output": nc.Output}
	}

	if err := e.routeOnEscalationAnswer(nodeID); err != nil {
		e.handleNodeErrorKind(nodeID, err, ErrorKindFailure)
		return
	}

	if node := e.dag.Nodes[nodeID]; node != nil && node.Type == "decision" {
		e.completeDecisionNode(node, nc.Output)
		return
	}

	e.state.CompletedNodes[nodeID] = nc.SubTaskID

	e.publishNodeResult(nodeID, "success", nil)

	e.logger.Debug("node completed", "node", nodeID)
}

func (e *DAGExecutor) handleNodeError(nodeID string, err error) bool {
	return e.handleNodeErrorKind(nodeID, err, ErrorKindError)
}

func (e *DAGExecutor) handleNodeErrorKind(nodeID string, err error, kind ErrorKind) bool {
	e.state.RemovePending(nodeID)

	if e.state.InExitHandler {
		e.logger.Warn("exit handler node failed", "node", nodeID, "error", err)
		errMsg := err.Error()
		e.publishNodeResult(nodeID, "error", &errMsg)
		e.onExitHandlerDone(nodeID)
		return false
	}

	if exhausted, ok := errors.AsType[*budget.ExhaustedError](err); ok {
		exhausted = exhausted.ForPrincipal(spend.SpendPrincipal(e.execCtx.UserProps, e.execCtx.Identity))
		resetsAt := exhausted.ResetsAt
		e.state.BudgetResetsAt = &resetsAt
		e.state.BudgetTier = exhausted.Tier
		e.state.BudgetSystemUser = exhausted.SystemUser
	} else if !errors.Is(err, errNoHumanInEvalRun) && e.shouldRetryNode(nodeID, kind) {
		e.scheduleRetry(nodeID, err)
		return true
	}

	errMsg := err.Error()
	failure := ErrorInfo{
		FailedNodeID:  nodeID,
		FailureReason: failureReasonOf(err),
		ErrorMessage:  errMsg,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
	e.state.FailedNodes = e.failedNodes()
	e.state.ErrorState = &failure
	if !slices.ContainsFunc(e.state.FailedNodes, func(f ErrorInfo) bool {
		return f.FailedNodeID == nodeID
	}) {
		e.state.FailedNodes = append(e.state.FailedNodes, failure)
	}

	e.publishNodeResultMeta(nodeID, "error", &errMsg, nodeFailureMetadata(failure.FailureReason))

	if errors.Is(err, errNoHumanInEvalRun) {
		e.logger.Warn("node failed", "node", nodeID, "error", err)
	} else {
		e.logger.Error("node failed", "node", nodeID, "error", err)
	}

	if e.cfg.FailFast {
		e.finalizeFailure(fmt.Sprintf("node %q failed: %s", nodeID, errMsg))
		return false
	}

	e.state.markSkipped(nodeID, fmt.Sprintf("upstream error: %s", errMsg), SkipCauseFailed)
	for _, depID := range e.dag.Forward[nodeID] {
		if !e.state.IsNodeCompleted(depID) && !e.state.IsNodeSkipped(depID) && !e.state.IsNodePending(depID) {
			e.skipNode(depID, fmt.Sprintf("dependency %q failed", nodeID), SkipCauseUpstreamFailed)
		}
	}
	return false
}

func (e *DAGExecutor) effectiveRetryStrategy(nodeID string) *RetryStrategy {
	if node := e.dag.Nodes[nodeID]; node != nil && node.Retry != nil {
		return node.Retry
	}
	return e.cfg.RetryStrategy
}

const pythonDefaultRetryPolicy = "OnFailure"

func (e *DAGExecutor) shouldRetryNode(nodeID string, kind ErrorKind) bool {
	if n := e.dag.Nodes[nodeID]; n != nil && (n.Type == "tool" || n.Type == "decision" || isDurableWaitNode(n)) {
		return false
	}
	rs := e.effectiveRetryStrategy(nodeID)
	if rs == nil || rs.Limit <= 0 {
		return false
	}
	if e.state.RetryCounts[nodeID] >= rs.Limit {
		return false
	}
	policy := rs.RetryPolicy
	if policy == "" {
		policy = pythonDefaultRetryPolicy
	}
	effectiveKind := kind
	if effectiveKind == ErrorKindUnknown {
		effectiveKind = ErrorKindFailure
	}
	switch policy {
	case "Always":
		return true
	case "OnFailure":
		return effectiveKind == ErrorKindFailure
	case "OnError":
		return effectiveKind == ErrorKindError
	default:
		return effectiveKind == ErrorKindFailure
	}
}

func (e *DAGExecutor) scheduleRetry(nodeID string, err error) {
	rs := e.effectiveRetryStrategy(nodeID)
	attempt := e.state.RetryCounts[nodeID]
	e.state.RetryCounts[nodeID] = attempt + 1

	delay := calculateBackoff(attempt, rs.Backoff)
	firesAt := time.Now().Add(delay)

	e.logger.Debug("scheduling node retry",
		"node", nodeID,
		"attempt", attempt+1,
		"limit", rs.Limit,
		"delay", delay,
		"error", err,
	)

	e.state.AddPending(nodeID)

	e.state.AddTimer(Timer{
		Kind:    TimerKindRetry,
		NodeID:  nodeID,
		FiresAt: firesAt,
	})
}

func (e *DAGExecutor) redispatchNode(nodeID string) {
	node := e.dag.Nodes[nodeID]
	if node == nil {
		e.handleNodeError(nodeID, fmt.Errorf("node %q not found for retry", nodeID))
		return
	}

	e.state.RemoveTimer(TimerKindRetry, nodeID)

	if oldSubTaskID, ok := e.state.SubTaskForNode(nodeID); ok {
		e.cancelNodeTimer(oldSubTaskID)
	}
	e.state.RemoveSubTaskByNode(nodeID)

	e.logger.Debug("retrying node", "node", nodeID, "attempt", e.state.RetryCounts[nodeID])

	var subTaskID string
	if node.Type == "agent" || node.Type == "workflow" {
		subTaskID = idgen.New()
	}
	e.publishNodeStart(node, "", subTaskID)

	dctx, dcancel := e.dispatchCtx()
	defer dcancel()
	switch node.Type {
	case "agent", "workflow":
		e.state.RemovePending(nodeID)
		e.dispatchAgentNode(dctx, node, subTaskID)
	case "map":
		e.state.RemovePending(nodeID)
		e.executeMapNode(dctx, node)
	case "loop":
		e.state.RemovePending(nodeID)
		e.executeLoopNode(dctx, node)
	default:
		e.state.RemovePending(nodeID)
		e.handleNodeError(nodeID, fmt.Errorf("cannot retry node type %q", node.Type))
	}
}

func calculateBackoff(attempt int, bs *BackoffStrategy) time.Duration {
	if bs == nil {
		bs = &BackoffStrategy{
			Duration: 1 * time.Second,
			Factor:   2.0,
		}
	}

	delay := bs.Duration
	for range attempt {
		delay = time.Duration(float64(delay) * bs.Factor)
		if bs.MaxDuration > 0 && delay > bs.MaxDuration {
			delay = bs.MaxDuration
			break
		}
	}
	return delay
}

func (e *DAGExecutor) totalRetryCount() int {
	total := 0
	for _, count := range e.state.RetryCounts {
		total += count
	}
	return total
}

func (e *DAGExecutor) skipNode(nodeID, reason string, cause SkipCause) {
	e.skipNodeCause(nodeID, reason, cause, false)
}

func branchAgentName(node *NodeConfig) string {
	if node == nil {
		return ""
	}
	switch node.Type {
	case "agent":
		return node.AgentName
	case "workflow":
		return node.WorkflowName
	}
	return ""
}

func addSkippedNodePlacement(metadata map[string]any, node *NodeConfig) {
	if node == nil {
		return
	}
	metadata["node_type"] = node.Type
	dependsOn := node.waitsFor()
	if dependsOn == nil {
		dependsOn = []string{}
	}
	metadata["depends_on"] = dependsOn
	if name := branchAgentName(node); name != "" {
		metadata["agent_name"] = name
	}
	if node.Type == "tool" && node.ToolName != "" {
		metadata["tool_name"] = node.ToolName
	}
}

func (e *DAGExecutor) skipNodeCause(nodeID, reason string, cause SkipCause, unresolvedReference bool) {
	e.state.markSkipped(nodeID, reason, cause)
	metadata := skipMetadata(reason, unresolvedReference)
	addSkippedNodePlacement(metadata, e.dag.Nodes[nodeID])
	e.publishNodeResultMeta(nodeID, "skipped", nil, metadata)

	e.logger.Debug("node skipped", "node", nodeID, "reason", reason)
	e.abandonEscalationRouting(nodeID)

	for _, depID := range e.dag.Forward[nodeID] {
		if e.state.IsNodeCompleted(depID) || e.state.IsNodeSkipped(depID) || e.state.IsNodePending(depID) {
			continue
		}
		allSkipped := true
		for _, upID := range e.dag.Reverse[depID] {
			if !e.state.IsNodeSkipped(upID) {
				allSkipped = false
				break
			}
		}
		if allSkipped {
			e.skipNodeCause(depID, fmt.Sprintf("all dependencies skipped (parent: %s)", nodeID), e.inheritedSkipCause(depID), unresolvedReference)
		}
	}
}

func (e *DAGExecutor) inheritedSkipCause(nodeID string) SkipCause {
	upstream := e.dag.Reverse[nodeID]
	if len(upstream) == 0 {
		return SkipCauseUnknown
	}
	cause := e.state.SkipCause(upstream[0])
	for _, upID := range upstream[1:] {
		if upCause := e.state.SkipCause(upID); upCause.severity() > cause.severity() {
			cause = upCause
		}
	}
	if cause == SkipCauseFailed {
		return SkipCauseUpstreamFailed
	}
	return cause
}

func (e *DAGExecutor) routableWorkflowTargetReason(rawTarget string) string {
	if _, kind, ok := a2a.IDFromInstanceName(rawTarget); ok && kind == a2a.KindWorkflow {
		return "instanceName"
	}
	if rawTarget == e.cfg.Name {
		return "selfReference"
	}
	if _, kind, ok := a2a.IDFromInstanceName(e.cfg.Name); !ok || kind != a2a.KindWorkflow {
		return "yamlAuthoredParent"
	}
	if !e.instance.hasDiscoveredCards() {
		return "coldCardCache"
	}
	return ""
}

func (e *DAGExecutor) resolveDispatchTarget(node *NodeConfig) (string, error) {
	rawTarget := node.AgentName
	targetKind := a2a.KindAgent
	if node.Type == "workflow" {
		rawTarget = node.WorkflowName
		targetKind = a2a.KindWorkflow
	}
	targetName, known, err := e.instance.resolveNodeTarget(rawTarget, targetKind)
	if err != nil {
		return "", fmt.Errorf("%s node %q target %q %w", node.Type, node.ID, rawTarget, err)
	}
	if known {
		return targetName, nil
	}

	if node.Type == "agent" && node.AgentInstanceName != "" {
		e.logger.Debug("agent node target not in discovered cards; using deploy-time instance name",
			"node", node.ID, "target", rawTarget, "instanceName", node.AgentInstanceName)
		return node.AgentInstanceName, nil
	}

	if node.Type != "workflow" {
		e.logger.Warn("agent node target not in discovered cards; publishing literally",
			"node", node.ID, "target", rawTarget)
		return targetName, nil
	}
	if reason := e.routableWorkflowTargetReason(rawTarget); reason != "" {
		e.logger.Warn("workflow node target not in discovered cards; publishing literally",
			"node", node.ID, "target", rawTarget, "reason", reason)
		return targetName, nil
	}
	return "", fmt.Errorf("workflow node %q target %q did not resolve to any deployed workflow (no broker subscriber) — check workflow_name and that the target workflow is deployed", node.ID, rawTarget)
}

func (e *DAGExecutor) dispatchAgentNode(ctx context.Context, node *NodeConfig, subTaskID string) {
	input, err := e.resolveNodeInput(node)
	if err != nil {
		e.handleNodeError(node.ID, fmt.Errorf("resolve input: %w", err))
		return
	}
	e.recordInputPreview(node.ID, input)

	targetName, targetErr := e.resolveDispatchTarget(node)
	if targetErr != nil {
		e.handleNodeError(node.ID, targetErr)
		return
	}

	e.state.TrackSubTask(subTaskID, node.ID)
	e.state.SetSubTaskTarget(subTaskID, targetName)
	e.instance.trackSubTask(subTaskID, e.execCtx.TaskID)
	e.state.AddPending(node.ID)

	inputArtifactName, err := e.saveInputArtifact(ctx, node.ID, subTaskID, input)
	if err != nil {
		e.handleNodeError(node.ID, fmt.Errorf("save input artifact: %w", err))
		return
	}

	parts := e.buildRequestParts(node, targetName, inputArtifactName, input, "", e.state.NodeOutputs)

	message := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     parts,
		ContextID: e.execCtx.ContextID,
		Metadata:  e.peerRequestMetadata(node.ID, NodeCompletion{NodeID: node.ID, SubTaskID: subTaskID}),
	}

	request := a2a.NewRequest(subTaskID, "message/send", map[string]any{
		"message": message,
	})

	reqBytes, err := json.Marshal(request)
	if err != nil {
		e.handleNodeError(node.ID, fmt.Errorf("marshal request: %w", err))
		return
	}

	replyTo := a2a.PeerResponseTopic(e.cfg.Namespace, e.cfg.Name, subTaskID)
	statusTopic := a2a.PeerStatusTopic(e.cfg.Namespace, e.cfg.Name, subTaskID)
	userProps := map[string]string{
		"timestamp":      fmt.Sprintf("%d", time.Now().UnixMilli()),
		"replyTo":        replyTo,
		"a2aStatusTopic": statusTopic,
		"callDepth":      fmt.Sprintf("%d", e.execCtx.UserProps.CallDepth+1),
	}
	if e.execCtx.UserProps.ClientID != "" {
		userProps["clientId"] = e.execCtx.UserProps.ClientID
	}
	if e.execCtx.UserProps.UserID != "" {
		userProps["userId"] = e.execCtx.UserProps.UserID
	}
	if e.execCtx.UserProps.A2AUserConfig != "" {
		userProps["a2aUserConfig"] = e.execCtx.UserProps.A2AUserConfig
	}
	if e.execCtx.UserProps.AuthToken != "" {
		userProps["authToken"] = e.execCtx.UserProps.AuthToken
	}
	userProps["delegating_agent_name"] = e.cfg.Name
	a2a.AddTraceID(userProps, e.execCtx.UserProps.TraceID)
	a2a.AddTrigger(userProps, e.execCtx.UserProps.Trigger)
	e.addRoot(userProps, node.ID)
	a2a.AddOrigin(userProps, e.execCtx.UserProps.ForwardedOrigin())

	topic := a2a.AgentRequestTopic(e.cfg.Namespace, targetName)
	if pubErr := e.instance.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        reqBytes,
		UserProperties: userProps,
	}); pubErr != nil {
		e.handleNodeError(node.ID, fmt.Errorf("publish request: %w", pubErr))
		return
	}

	e.armNodeTimer(node, subTaskID, NodeCompletion{
		NodeID:    node.ID,
		SubTaskID: subTaskID,
	})

	e.logger.Debug("dispatched agent node",
		"node", node.ID,
		"target", targetName,
		"subTaskID", subTaskID,
		"timeout", e.effectiveNodeTimeout(node),
	)
}

func (e *DAGExecutor) addRoot(userProps map[string]string, topLevelNodeID string) {
	kind, id, nodeID := e.root(topLevelNodeID)
	a2a.AddRoot(userProps, kind, id, nodeID)
}

func (e *DAGExecutor) root(topLevelNodeID string) (kind, id, nodeID string) {
	up := e.execCtx.UserProps
	if up.RootKind != "" && up.RootID != "" {
		return up.RootKind, up.RootID, up.RootNodeID
	}
	return a2a.RootKindWorkflow, a2a.CanonicalComponentID(e.cfg.Name), topLevelNodeID
}

func (e *DAGExecutor) peerRequestMetadata(nodeID string, routing NodeCompletion) map[string]any {
	metadata := map[string]any{
		"workflow_name": e.cfg.Name,
		"node_id":       nodeID,
		"sub_task_id":   routing.SubTaskID,
		"parentTaskId":  e.execCtx.TaskID,
	}
	if e.execCtx.ModelOverrideAlias != "" {
		metadata[a2a.MetadataModelOverride] = map[string]any{a2a.MetadataModelID: e.execCtx.ModelOverrideAlias}
	}
	return e.retryIdentityFor(routing).stamp(metadata)
}

func (e *DAGExecutor) logDroppedInputKeys(node *NodeConfig, resolved map[string]any) {
	if isOperatorMap(node.Input) {
		return
	}
	var dropped []string
	for k := range node.Input {
		if _, kept := resolved[k]; !kept {
			dropped = append(dropped, k)
		}
	}
	if len(dropped) == 0 {
		return
	}
	slices.Sort(dropped)
	e.logger.Debug("node input omitted arguments referencing unsupplied workflow inputs",
		"node", node.ID, "droppedKeys", dropped)
}

func (e *DAGExecutor) resolveNodeInput(node *NodeConfig) (map[string]any, error) {
	if node.Input != nil {
		resolved, err := resolveValue(node.Input, e.state.NodeOutputs, e.instance.namedOutputNodes)
		if err != nil {
			return nil, err
		}
		if m, ok := resolved.(map[string]any); ok {
			e.logDroppedInputKeys(node, m)
			return m, nil
		}
		return map[string]any{"data": resolved}, nil
	}

	if len(node.DependsOn) == 1 {
		depID := node.DependsOn[0]
		if out, ok := e.state.NodeOutputs[depID]; ok {
			if data, ok := out["output"].(map[string]any); ok {
				return data, nil
			}
		}
	}

	if out, ok := e.state.NodeOutputs["workflow_input"]; ok {
		if data, ok := out["output"].(map[string]any); ok {
			return data, nil
		}
	}

	return map[string]any{}, nil
}

func (e *DAGExecutor) saveInputArtifact(ctx context.Context, nodeID, subTaskID string, input map[string]any) (string, error) {
	if e.instance.svc.Artifacts() == nil {
		return "", nil
	}

	data, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal input: %w", err)
	}

	artifactName := fmt.Sprintf(a2a.WorkflowHandoffArtifactPrefix+"%s_input_%s.json", nodeID, shortID(subTaskID))

	_, err = artifact.SaveWithMetadata(ctx, e.instance.svc.Artifacts(),
		e.cfg.Name, e.execCtx.UserProps.UserID, e.execCtx.ContextID,
		artifactName, data, "application/json",
		artifact.MetadataOpts{
			Description:    fmt.Sprintf("Input for node %s", nodeID),
			Source:         "workflow_execution",
			SuppressSchema: true,
			Tags:           []string{a2a.ArtifactTagWorking},
		}, e.logger)
	if err != nil {
		return "", fmt.Errorf("save artifact: %w", err)
	}

	return artifactName, nil
}

func (e *DAGExecutor) buildRequestParts(node *NodeConfig, targetName, inputArtifactName string, input map[string]any, filenameSuffix string, outputs map[string]map[string]any) []a2a.Part {
	var parts []a2a.Part

	sir := map[string]any{
		"type":          "structured_invocation_request",
		"workflow_name": e.cfg.Name,
		"node_id":       node.ID,
	}
	inputSchema := node.InputSchemaOverride
	outputSchema := node.OutputSchemaOverride
	if inputSchema == nil || outputSchema == nil {
		if node.Type == "agent" && node.AgentName != "" && e.instance != nil {
			cardIn, cardOut := e.instance.agentSchemas(targetName)
			if inputSchema == nil {
				inputSchema = cardIn
			}
			if outputSchema == nil {
				outputSchema = cardOut
			}
		}
		if node.Type == "agent" && outputSchema == nil {
			outputSchema = map[string]any{"type": "object"}
		}
	}
	sir["input_schema"] = inputSchema
	sir["output_schema"] = outputSchema

	suggestedFilename := fmt.Sprintf(a2a.WorkflowHandoffArtifactPrefix+"%s_output_%s%s.json", node.ID, shortID(e.state.ExecutionID), filenameSuffix)
	sir["suggested_output_filename"] = suggestedFilename

	parts = append(parts, a2a.NewDataPart(sir))

	if len(input) > 0 || node.Input != nil {
		inputBytes, _ := json.Marshal(input)
		b64 := base64.StdEncoding.EncodeToString(inputBytes)
		parts = append(parts, a2a.NewFilePartWithBytes(
			b64,
			inputArtifactName,
			"application/json",
		))
	}

	instruction := node.Instruction
	if instruction != "" {
		resolved, err := resolveStringTemplates(instruction, outputs, e.namedOutputNodes())
		if err != nil {
			if !e.staleRefWarnedOnce("instruction", node.ID) {
				e.logger.Warn("instruction references an unresolved field; sending it verbatim",
					"node", node.ID, "error", err)
			}
		}
		parts = append(parts, a2a.NewTextPart(resolved))
	}

	return parts
}

func switchResultMetadata(node *NodeConfig, selectedBranch string, selectedIndex, unresolvedCases int) map[string]any {
	metadata := map[string]any{"cases_unresolved": unresolvedCases}
	if selectedBranch == "" {
		return metadata
	}
	metadata["selected_branch"] = selectedBranch
	if selectedIndex == len(node.Cases) {
		metadata["selected_case_index"] = nil
	} else {
		metadata["selected_case_index"] = selectedIndex
	}
	return metadata
}

func (e *DAGExecutor) executeSwitchNode(node *NodeConfig) {
	selectedBranch := ""
	selectedIndex := -1
	unresolvedCases := 0
	undecidedTargets := make(map[string]bool)

	for i, sc := range node.Cases {
		result, stale, err := evaluateCondition(sc.Condition, e.state.NodeOutputs, e.namedOutputNodes())
		if isOrdinaryNotMatched(err) {
			e.logger.Debug("switch case depends on a value that was not supplied; treating as not matched",
				"node", node.ID, "case", i, "error", err)
			continue
		}
		if errors.Is(err, ErrUnresolvedReference) {
			unresolvedCases++
			undecidedTargets[sc.Node] = true
			e.logger.Warn("switch case references an unresolved field; treating as not matched",
				"node", node.ID, "case", i, "error", err)
			continue
		}
		if err != nil {
			undecidedTargets[sc.Node] = true
			e.logger.Warn("switch condition error", "node", node.ID, "case", i, "error", err)
			continue
		}
		if stale != nil {
			unresolvedCases++
			e.logger.Warn("switch case references an unresolved field but the rest settles it; deciding on that",
				"node", node.ID, "case", i, "matched", result, "error", stale)
		}
		if result {
			selectedBranch = sc.Node
			selectedIndex = i
			break
		}
	}

	if selectedBranch == "" && node.DefaultCase != "" {
		selectedBranch = node.DefaultCase
		selectedIndex = len(node.Cases)
	}

	output := map[string]any{
		"selected_branch":     selectedBranch,
		"selected_case_index": selectedIndex,
		"cases_unresolved":    unresolvedCases,
	}
	e.state.NodeOutputs[node.ID] = map[string]any{"output": output}
	e.state.CompletedNodes[node.ID] = "switch"

	if selectedBranch == "" && unresolvedCases > 0 {
		e.logger.Warn("switch selected no branch after a case could not be evaluated",
			"node", node.ID, "casesUnresolved", unresolvedCases, "hasDefault", node.DefaultCase != "")
	}

	e.publishNodeResultMeta(node.ID, "success", nil, switchResultMetadata(node, selectedBranch, selectedIndex, unresolvedCases))

	e.skipUnselectedSwitchTargets(node, selectedBranch, selectedBranch == "" && unresolvedCases > 0, undecidedTargets)

	e.logger.Debug("switch node completed",
		"node", node.ID,
		"selectedBranch", selectedBranch,
		"selectedIndex", selectedIndex,
	)
}

func (e *DAGExecutor) skipUnselectedSwitchTargets(node *NodeConfig, selectedBranch string, selectedNothing bool, undecidedTargets map[string]bool) {
	allTargets := make(map[string]bool)
	for _, sc := range node.Cases {
		allTargets[sc.Node] = true
	}
	if node.DefaultCase != "" {
		allTargets[node.DefaultCase] = true
	}
	for target := range allTargets {
		switch {
		case target == selectedBranch:
		case undecidedTargets[target]:
			e.skipBranchFor(target, fmt.Sprintf("case for it could not be evaluated by switch %q", node.ID), SkipCauseUnresolved, selectedNothing)
		case selectedNothing:
			e.skipBranch(target, node, SkipCauseUnresolved, true)
		default:
			e.skipBranch(target, node, SkipCauseNotSelected, false)
		}
	}
}

func (e *DAGExecutor) skipBranch(nodeID string, parent *NodeConfig, cause SkipCause, unresolvedReference bool) {
	e.skipBranchFor(nodeID, fmt.Sprintf("not selected by %s %q", parent.Type, parent.ID), cause, unresolvedReference)
}

func (e *DAGExecutor) skipBranchFor(nodeID, reason string, cause SkipCause, unresolvedReference bool) {
	if e.state.IsNodeCompleted(nodeID) || e.state.IsNodeSkipped(nodeID) {
		return
	}
	e.skipNodeCause(nodeID, reason, cause, unresolvedReference)
}

const defaultToolMapConcurrency = 8

func runsInProcess(n *NodeConfig) bool {
	return n.Type == "tool" || n.Type == "decision"
}

func (e *DAGExecutor) effectiveMapConcurrency(node *NodeConfig, itemCount int) int {
	if node.ConcurrencyLimit > 0 {
		if node.ConcurrencyLimit < itemCount {
			return node.ConcurrencyLimit
		}
		return itemCount
	}
	if tmpl := e.dag.Nodes[node.TemplateNode]; tmpl != nil && runsInProcess(tmpl) && itemCount > defaultToolMapConcurrency {
		return defaultToolMapConcurrency
	}
	return itemCount
}

func (e *DAGExecutor) executeMapNode(ctx context.Context, node *NodeConfig) {
	items, err := e.resolveMapItems(node)
	if err != nil {
		e.handleNodeError(node.ID, fmt.Errorf("resolve map items: %w", err))
		return
	}

	if len(items) == 0 {
		e.state.NodeOutputs[node.ID] = map[string]any{"output": map[string]any{"results": []any{}}}
		e.state.CompletedNodes[node.ID] = "map_empty"
		e.publishNodeResult(node.ID, "success", nil)
		return
	}

	if node.MaxItems > 0 && len(items) > node.MaxItems {
		items = items[:node.MaxItems]
	}

	e.state.Metadata[node.ID+"_items"] = items
	e.state.Metadata[node.ID+"_results"] = make([]any, len(items))
	e.state.Metadata[node.ID+"_done"] = make([]bool, len(items))
	e.state.Metadata[node.ID+"_completed"] = 0
	e.state.Metadata[node.ID+"_total"] = len(items)
	e.state.ActiveBranches[node.ID] = nil

	e.state.AddPending(node.ID)

	concurrency := e.effectiveMapConcurrency(node, len(items))
	e.state.Metadata[node.ID+"_concurrency"] = concurrency

	for started := 0; started < concurrency && started < len(items); started = len(e.state.ActiveBranches[node.ID]) {
		if ctx.Err() != nil || !e.state.PendingNodes[node.ID] {
			return
		}
		e.launchMapIteration(ctx, node, items[started], started)
	}

	e.instance.publishWorkflowEvent(ctx, e.execCtx, WorkflowMapProgressData{
		Type:           string(a2a.SignalWorkflowMapProgress),
		NodeID:         node.ID,
		TotalItems:     len(items),
		CompletedItems: 0,
		Status:         "in_progress",
	})
}

func (e *DAGExecutor) resolveMapItems(node *NodeConfig) ([]any, error) {
	resolved, err := resolveValue(node.ItemsExpression, e.state.NodeOutputs, e.instance.namedOutputNodes)
	if err != nil {
		return nil, err
	}

	if v, ok := resolved.([]any); ok {
		return v, nil
	}
	rv := reflect.ValueOf(resolved)
	if rv.Kind() != reflect.Slice {
		return nil, fmt.Errorf("items expression resolved to %T, expected list", resolved)
	}
	if rv.Type().Elem().Kind() == reflect.Uint8 {
		return nil, fmt.Errorf("items expression resolved to %T, expected list: a byte slice is data, not a list of items", resolved)
	}
	return typedSliceItems(rv, node.MaxItems)
}

var (
	errEncoderPanicked  = errors.New("json encoding panicked")
	errNumberOutOfRange = errors.New("number out of range")
)

func typedSliceItems(rv reflect.Value, maxItems int) ([]any, error) {
	if rv.IsNil() {
		return nil, nil
	}
	n := rv.Len()
	if maxItems > 0 && n > maxItems {
		n = maxItems
	}
	items := make([]any, n)
	for i := range items {
		item, err := jsonShaped(rv.Index(i))
		if err != nil {
			return nil, fmt.Errorf("items expression resolved to %s, a list whose item %d cannot be used: %s",
				rv.Type(), i, encodingCause(err))
		}
		items[i] = item
	}
	return items, nil
}

func jsonShaped(elem reflect.Value) (item any, err error) {
	defer func() {
		if r := recover(); r != nil {
			item, err = nil, fmt.Errorf("%w (%T)", errEncoderPanicked, r)
		}
	}()
	v := elem.Interface()
	switch v.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint16, uint32, uint64, float32, float64,
		map[string]any, []any:
		if _, encodeErr := json.Marshal(v); encodeErr != nil {
			return nil, encodeErr
		}
		return v, nil
	}
	raw, encodeErr := json.Marshal(elem.Addr().Interface())
	if encodeErr != nil {
		return nil, encodeErr
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if decodeErr := dec.Decode(&out); decodeErr != nil {
		return nil, decodeErr
	}
	return exactNumbers(out)
}

func exactNumbers(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, nil
		}
		if n, err := strconv.ParseUint(x.String(), 10, 64); err == nil {
			return n, nil
		}
		f, err := x.Float64()
		if err != nil {
			return nil, errNumberOutOfRange
		}
		return f, nil
	case map[string]any:
		for k, e := range x {
			n, err := exactNumbers(e)
			if err != nil {
				return nil, err
			}
			x[k] = n
		}
	case []any:
		for i, e := range x {
			n, err := exactNumbers(e)
			if err != nil {
				return nil, err
			}
			x[i] = n
		}
	}
	return v, nil
}

func encodingCause(err error) string {
	switch {
	case errors.Is(err, errEncoderPanicked):
		return "its JSON encoding panicked" + strings.TrimPrefix(err.Error(), errEncoderPanicked.Error())
	case errors.Is(err, errNumberOutOfRange):
		return "a number out of range"
	}
	if unsupported, ok := errors.AsType[*json.UnsupportedValueError](err); ok {
		if slices.Contains([]string{"NaN", "+Inf", "-Inf"}, unsupported.Str) {
			return "unsupported value " + unsupported.Str
		}
		return "unsupported value"
	}
	if unsupported, ok := errors.AsType[*json.UnsupportedTypeError](err); ok {
		return "unsupported type " + unsupported.Type.String()
	}
	return fmt.Sprintf("its JSON encoding failed (%T)", err)
}

func (e *DAGExecutor) retireMapBranches(mapNodeID string) {
	for _, branch := range e.state.ActiveBranches[mapNodeID] {
		e.cancelNodeTimer(branch.SubTaskID)
	}
	e.state.ActiveBranches[mapNodeID] = nil
}

func (e *DAGExecutor) isCurrentMapBranch(mapNodeID string, nc NodeCompletion) bool {
	for _, branch := range e.state.ActiveBranches[mapNodeID] {
		if branch.Index == nc.ItemIndex && branch.SubTaskID == nc.SubTaskID {
			return true
		}
	}
	return false
}

func (e *DAGExecutor) mapBranchIndex(mapNodeID, subTaskID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, branch := range e.state.ActiveBranches[mapNodeID] {
		if branch.SubTaskID == subTaskID {
			return branch.Index
		}
	}
	return -1
}

func (e *DAGExecutor) launchMapIteration(ctx context.Context, mapNode *NodeConfig, item any, index int) {
	templateNode := e.dag.Nodes[mapNode.TemplateNode]
	if templateNode == nil {
		e.handleNodeError(mapNode.ID, fmt.Errorf("template node %q not found", mapNode.TemplateNode))
		return
	}

	scopedOutputs := make(map[string]map[string]any)
	for k, v := range e.state.NodeOutputs {
		scopedOutputs[k] = v
	}
	scopedOutputs["_map_item"] = map[string]any{"output": item}
	scopedOutputs["_map_index"] = map[string]any{"output": index}

	input, err := e.resolveNodeInputWithOutputs(templateNode, scopedOutputs)
	if err != nil {
		e.logger.Error("map iteration input resolution failed",
			"node", mapNode.ID, "index", index, "error", err)
		failedCallID := idgen.New()
		e.recordMapBranch(mapNode.ID, BranchInfo{Index: index, SubTaskID: failedCallID, Type: "map"})
		e.refuseToolIteration(templateNode, NodeCompletion{NodeID: mapNode.ID, SubTaskID: failedCallID, MapNodeID: mapNode.ID, ItemIndex: index},
			fmt.Errorf("resolve map iteration input: %w", err))
		e.handleMapChildCompletion(NodeCompletion{
			NodeID:    mapNode.ID,
			MapNodeID: mapNode.ID,
			ItemIndex: index,
			Error:     fmt.Errorf("resolve map iteration input: %w", err),
			ErrorKind: ErrorKindError,
		})
		return
	}

	subTaskID := idgen.New()
	e.state.TrackSubTask(subTaskID, mapNode.ID)
	e.instance.trackSubTask(subTaskID, e.execCtx.TaskID)

	e.recordMapBranch(mapNode.ID, BranchInfo{Index: index, SubTaskID: subTaskID, Type: "map"})

	artifactName := fmt.Sprintf(a2a.WorkflowHandoffArtifactPrefix+"%s_map_%d_input_%s.json", mapNode.ID, index, shortID(subTaskID))
	if e.instance.svc.Artifacts() != nil {
		data, _ := json.Marshal(input)
		if _, saveErr := artifact.SaveWithMetadata(ctx, e.instance.svc.Artifacts(),
			e.cfg.Name, e.execCtx.UserProps.UserID, e.execCtx.ContextID,
			artifactName, data, "application/json",
			artifact.MetadataOpts{
				Description:    fmt.Sprintf("Input for map node %s iteration %d", mapNode.ID, index),
				Source:         "workflow_execution",
				SuppressSchema: true,
				Tags:           []string{a2a.ArtifactTagWorking},
			}, e.logger); saveErr != nil {
			e.logger.Error("failed to save map input artifact", "node", mapNode.ID, "index", index, "error", saveErr)
		}
	}

	routing := NodeCompletion{
		NodeID:    mapNode.ID,
		SubTaskID: subTaskID,
		MapNodeID: mapNode.ID,
		ItemIndex: index,
	}
	e.armNodeTimer(templateNode, subTaskID, routing)

	if templateNode.Type == "tool" {
		e.invokeToolNode(templateNode, input, routing)
		return
	}
	if templateNode.Type == "decision" {
		e.invokeDecisionNode(templateNode, input, routing, scopedOutputs)
		return
	}

	targetName, targetErr := e.resolveDispatchTarget(templateNode)
	if targetErr != nil {
		if !e.cfg.FailFast {
			e.logger.Warn("map iteration target unresolvable",
				"node", mapNode.ID, "index", index, "error", targetErr)
		}
		e.handleMapChildCompletion(NodeCompletion{
			NodeID:    mapNode.ID,
			SubTaskID: subTaskID,
			MapNodeID: mapNode.ID,
			ItemIndex: index,
			Error:     targetErr,
			ErrorKind: ErrorKindError,
		})
		return
	}
	e.state.SetSubTaskTarget(subTaskID, targetName)

	parts := e.buildRequestParts(templateNode, targetName, artifactName, input, fmt.Sprintf("_%d", index), scopedOutputs)
	message := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     parts,
		ContextID: e.execCtx.ContextID,
		Metadata:  e.peerRequestMetadata(templateNode.ID, routing),
	}

	request := a2a.NewRequest(subTaskID, "message/send", map[string]any{"message": message})
	reqBytes, _ := json.Marshal(request)

	replyTo := a2a.PeerResponseTopic(e.cfg.Namespace, e.cfg.Name, subTaskID)
	userProps := map[string]string{
		"timestamp": fmt.Sprintf("%d", time.Now().UnixMilli()),
		"replyTo":   replyTo,
		"callDepth": fmt.Sprintf("%d", e.execCtx.UserProps.CallDepth+1),
	}
	if e.execCtx.UserProps.ClientID != "" {
		userProps["clientId"] = e.execCtx.UserProps.ClientID
	}
	if e.execCtx.UserProps.UserID != "" {
		userProps["userId"] = e.execCtx.UserProps.UserID
	}
	if e.execCtx.UserProps.AuthToken != "" {
		userProps["authToken"] = e.execCtx.UserProps.AuthToken
	}
	userProps["delegating_agent_name"] = e.cfg.Name
	a2a.AddTraceID(userProps, e.execCtx.UserProps.TraceID)
	a2a.AddTrigger(userProps, e.execCtx.UserProps.Trigger)
	e.addRoot(userProps, mapNode.ID)
	a2a.AddOrigin(userProps, e.execCtx.UserProps.ForwardedOrigin())

	topic := a2a.AgentRequestTopic(e.cfg.Namespace, targetName)
	if pubErr := e.instance.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        reqBytes,
		UserProperties: userProps,
	}); pubErr != nil {
		e.logger.Error("failed to publish map iteration", "node", mapNode.ID, "index", index, "error", pubErr)
		e.handleMapChildCompletion(NodeCompletion{
			NodeID:    mapNode.ID,
			SubTaskID: subTaskID,
			MapNodeID: mapNode.ID,
			ItemIndex: index,
			Error:     fmt.Errorf("publish map iteration: %w", pubErr),
			ErrorKind: ErrorKindError,
		})
	}
}

func (e *DAGExecutor) resolveNodeInputWithOutputs(node *NodeConfig, outputs map[string]map[string]any) (map[string]any, error) {
	if node.Input != nil {
		resolved, err := resolveValue(node.Input, outputs, e.instance.namedOutputNodes)
		if err != nil {
			return nil, err
		}
		if m, ok := resolved.(map[string]any); ok {
			e.logDroppedInputKeys(node, m)
			return m, nil
		}
		return map[string]any{"data": resolved}, nil
	}
	return map[string]any{}, nil
}

func (e *DAGExecutor) handleMapChildCompletion(nc NodeCompletion) {
	mapNodeID := nc.MapNodeID

	results, _ := e.state.Metadata[mapNodeID+"_results"].([]any)
	done, _ := e.state.Metadata[mapNodeID+"_done"].([]bool)
	if nc.ItemIndex < 0 || nc.ItemIndex >= len(results) {
		return
	}
	if nc.SubTaskID != "" && !e.isCurrentMapBranch(mapNodeID, nc) {
		e.cancelNodeTimer(nc.SubTaskID)
		e.logger.Debug("ignoring map branch completion from a superseded attempt",
			"node", mapNodeID, "index", nc.ItemIndex, "subTaskID", nc.SubTaskID)
		return
	}
	if nc.ItemIndex < len(done) && done[nc.ItemIndex] {
		if nc.SubTaskID != "" {
			e.cancelNodeTimer(nc.SubTaskID)
		}
		e.logger.Debug("ignoring duplicate map branch completion", "node", mapNodeID, "index", nc.ItemIndex)
		return
	}
	if nc.ItemIndex < len(done) {
		done[nc.ItemIndex] = true
	}

	if nc.SubTaskID != "" {
		e.cancelNodeTimer(nc.SubTaskID)
	}

	if nc.Error != nil {
		results[nc.ItemIndex] = map[string]any{"error": nc.Error.Error()}
	} else {
		results[nc.ItemIndex] = nc.Output
	}

	completed, _ := e.state.Metadata[mapNodeID+"_completed"].(int)
	completed++
	e.state.Metadata[mapNodeID+"_completed"] = completed

	total, _ := e.state.Metadata[mapNodeID+"_total"].(int)

	status := "in_progress"
	if completed >= total {
		status = "completed"
	}
	e.instance.publishWorkflowEvent(e.execCtx.Ctx, e.execCtx, WorkflowMapProgressData{
		Type:           string(a2a.SignalWorkflowMapProgress),
		NodeID:         mapNodeID,
		TotalItems:     total,
		CompletedItems: completed,
		Status:         status,
	})

	if nc.Error != nil && e.cfg.FailFast {
		e.retireMapBranches(mapNodeID)
		e.handleNodeErrorKind(mapNodeID, nc.Error, nc.ErrorKind)
		return
	}

	if e.cancelRequested() {
		return
	}
	mapNode := e.dag.Nodes[mapNodeID]
	limit, _ := e.state.Metadata[mapNodeID+"_concurrency"].(int)
	if mapNode != nil && limit > 0 && limit < total {
		dctx, dcancel := e.dispatchCtx()
		defer dcancel()
		items, _ := e.state.Metadata[mapNodeID+"_items"].([]any)
		startedCount := len(e.state.ActiveBranches[mapNodeID])
		inFlight := startedCount - completed
		for inFlight < limit && startedCount < len(items) && e.state.PendingNodes[mapNodeID] && !e.cancelRequested() {
			e.launchMapIteration(dctx, mapNode, items[startedCount], startedCount)
			startedCount = len(e.state.ActiveBranches[mapNodeID])
			inFlight = startedCount - completed
		}
	}

	if completed >= total {
		e.state.RemovePending(mapNodeID)
		e.state.NodeOutputs[mapNodeID] = map[string]any{"output": map[string]any{"results": results}}
		e.state.CompletedNodes[mapNodeID] = "map_complete"
		e.publishNodeResult(mapNodeID, "success", nil)
		e.logger.Debug("map node completed", "node", mapNodeID, "total", total)
	}
}

func (e *DAGExecutor) executeLoopNode(ctx context.Context, node *NodeConfig) {
	e.state.LoopIterations[node.ID] = 0
	e.state.AddPending(node.ID)
	e.dispatchLoopIteration(ctx, node, 0)
}

func (e *DAGExecutor) completeLoopNode(loopNodeID string, iteration int, stoppedReason string) {
	e.state.RemovePending(loopNodeID)
	output := map[string]any{
		"iterations_completed": iteration,
		"stopped_reason":       stoppedReason,
	}
	e.state.NodeOutputs[loopNodeID] = map[string]any{"output": output}
	e.state.CompletedNodes[loopNodeID] = "loop_" + stoppedReason
	e.publishNodeResultMeta(loopNodeID, "success", nil, maps.Clone(output))
}

func (e *DAGExecutor) dispatchLoopIteration(ctx context.Context, loopNode *NodeConfig, iteration int) {
	bodyNode := e.dag.Nodes[loopNode.BodyNode]
	if bodyNode == nil {
		e.handleNodeError(loopNode.ID, fmt.Errorf("loop body node %q not found", loopNode.BodyNode))
		return
	}

	scopedOutputs := make(map[string]map[string]any)
	for k, v := range e.state.NodeOutputs {
		scopedOutputs[k] = v
	}
	scopedOutputs["_loop_iteration"] = map[string]any{"output": iteration}

	input, err := e.resolveNodeInputWithOutputs(bodyNode, scopedOutputs)
	if err != nil {
		resolveErr := fmt.Errorf("resolve loop input: %w", err)
		e.refuseToolIteration(bodyNode, NodeCompletion{NodeID: loopNode.ID, LoopNodeID: loopNode.ID}, resolveErr)
		e.handleNodeError(loopNode.ID, resolveErr)
		return
	}

	subTaskID := idgen.New()
	e.state.TrackSubTask(subTaskID, loopNode.ID)
	e.instance.trackSubTask(subTaskID, e.execCtx.TaskID)

	e.state.ActiveBranches[loopNode.ID] = append(e.state.ActiveBranches[loopNode.ID], BranchInfo{
		Iteration: iteration,
		SubTaskID: subTaskID,
		Type:      "loop",
	})

	artifactName := fmt.Sprintf(a2a.WorkflowHandoffArtifactPrefix+"%s_loop_%d_input_%s.json", loopNode.ID, iteration, shortID(subTaskID))
	if e.instance.svc.Artifacts() != nil {
		data, _ := json.Marshal(input)
		if _, saveErr := artifact.SaveWithMetadata(ctx, e.instance.svc.Artifacts(),
			e.cfg.Name, e.execCtx.UserProps.UserID, e.execCtx.ContextID,
			artifactName, data, "application/json",
			artifact.MetadataOpts{
				Description:    fmt.Sprintf("Input for loop node %s iteration %d", loopNode.ID, iteration),
				Source:         "workflow_execution",
				SuppressSchema: true,
				Tags:           []string{a2a.ArtifactTagWorking},
			}, e.logger); saveErr != nil {
			e.logger.Error("save loop input artifact failed", "node", loopNode.ID, "iteration", iteration, "artifact", artifactName, "error", saveErr)
		}
	}

	routing := NodeCompletion{
		NodeID:     loopNode.ID,
		SubTaskID:  subTaskID,
		LoopNodeID: loopNode.ID,
	}
	e.armNodeTimer(bodyNode, subTaskID, routing)

	if bodyNode.Type == "tool" {
		e.invokeToolNode(bodyNode, input, routing)
		e.logger.Debug("dispatched loop iteration (tool)",
			"node", loopNode.ID, "iteration", iteration, "subTaskID", subTaskID)
		return
	}
	if bodyNode.Type == "decision" {
		e.invokeDecisionNode(bodyNode, input, routing, scopedOutputs)
		e.logger.Debug("dispatched loop iteration (decision)",
			"node", loopNode.ID, "iteration", iteration, "subTaskID", subTaskID)
		return
	}

	targetName, targetErr := e.resolveDispatchTarget(bodyNode)
	if targetErr != nil {
		e.cancelNodeTimer(subTaskID)
		e.handleNodeError(loopNode.ID, targetErr)
		return
	}
	e.state.SetSubTaskTarget(subTaskID, targetName)

	parts := e.buildRequestParts(bodyNode, targetName, artifactName, input, "", scopedOutputs)
	message := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     parts,
		ContextID: e.execCtx.ContextID,
		Metadata:  e.peerRequestMetadata(bodyNode.ID, routing),
	}

	request := a2a.NewRequest(subTaskID, "message/send", map[string]any{"message": message})
	reqBytes, _ := json.Marshal(request)

	replyTo := a2a.PeerResponseTopic(e.cfg.Namespace, e.cfg.Name, subTaskID)
	userProps := map[string]string{
		"timestamp": fmt.Sprintf("%d", time.Now().UnixMilli()),
		"replyTo":   replyTo,
		"callDepth": fmt.Sprintf("%d", e.execCtx.UserProps.CallDepth+1),
	}
	if e.execCtx.UserProps.ClientID != "" {
		userProps["clientId"] = e.execCtx.UserProps.ClientID
	}
	if e.execCtx.UserProps.UserID != "" {
		userProps["userId"] = e.execCtx.UserProps.UserID
	}
	if e.execCtx.UserProps.AuthToken != "" {
		userProps["authToken"] = e.execCtx.UserProps.AuthToken
	}
	userProps["delegating_agent_name"] = e.cfg.Name
	a2a.AddTraceID(userProps, e.execCtx.UserProps.TraceID)
	a2a.AddTrigger(userProps, e.execCtx.UserProps.Trigger)
	e.addRoot(userProps, loopNode.ID)
	a2a.AddOrigin(userProps, e.execCtx.UserProps.ForwardedOrigin())

	topic := a2a.AgentRequestTopic(e.cfg.Namespace, targetName)
	if err := e.instance.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        reqBytes,
		UserProperties: userProps,
	}); err != nil {
		e.logger.Error("publish loop iteration failed", "topic", topic, "error", err)
	}

	e.logger.Debug("dispatched loop iteration",
		"node", loopNode.ID, "iteration", iteration, "subTaskID", subTaskID)
}

func (e *DAGExecutor) handleLoopIterationComplete(ctx context.Context, loopNodeID string, nc NodeCompletion) {
	loopNode := e.dag.Nodes[loopNodeID]
	if loopNode == nil {
		return
	}

	branches := e.state.ActiveBranches[loopNodeID]
	if nc.SubTaskID != "" && len(branches) > 0 {
		current := branches[len(branches)-1]
		if current.SubTaskID != nc.SubTaskID {
			e.logger.Debug("ignoring stale loop iteration completion",
				"node", loopNodeID,
				"subTaskID", nc.SubTaskID,
				"current", current.SubTaskID,
			)
			return
		}
	}

	if nc.SubTaskID != "" {
		e.cancelNodeTimer(nc.SubTaskID)
	}

	iteration := e.state.LoopIterations[loopNodeID]

	if nc.Error != nil {
		e.handleNodeErrorKind(loopNodeID, nc.Error, nc.ErrorKind)
		return
	}

	if nc.Output != nil {
		e.state.NodeOutputs[loopNode.BodyNode] = map[string]any{"output": nc.Output}
	}

	stoppedReason := "condition_false"
	shouldContinue, stale, err := evaluateCondition(loopNode.Condition, e.state.NodeOutputs, e.namedOutputNodes())
	switch {
	case isOrdinaryNotMatched(err):
		e.logger.Debug("loop condition depends on a value that was not supplied; stopping the loop",
			"node", loopNodeID, "error", err)
		shouldContinue = false
	case errors.Is(err, ErrUnresolvedReference):
		e.logger.Warn("loop condition references an unresolved field; stopping the loop",
			"node", loopNodeID, "error", err)
		shouldContinue, stoppedReason = false, "condition_unresolved"
	case err != nil:
		e.handleNodeError(loopNodeID, fmt.Errorf("loop condition error: %w", err))
		return
	}
	if stale != nil && !e.staleRefWarnedOnce("loopCondition", loopNodeID) {
		e.logger.Warn("loop condition references an unresolved field but the rest settles it; deciding on that",
			"node", loopNodeID, "continue", shouldContinue, "error", stale)
	}

	iteration++
	e.state.LoopIterations[loopNodeID] = iteration

	if iteration >= loopNode.MaxIterations {
		reason := "max_iterations"
		if stoppedReason != "condition_false" {
			reason = stoppedReason
		}
		e.completeLoopNode(loopNodeID, iteration, reason)
		if reason == "max_iterations" {
			e.logger.Info("loop stopped at max iterations", "node", loopNodeID, "iterations", iteration)
		} else {
			e.logger.Info("loop stopped on its last iteration", "node", loopNodeID, "iterations", iteration, "stoppedReason", reason)
		}
		return
	}

	if shouldContinue {
		if ctx.Err() != nil {
			return
		}
		if loopNode.Delay > 0 {
			select {
			case <-time.After(loopNode.Delay):
			case <-ctx.Done():
				return
			}
		}
		e.dispatchLoopIteration(ctx, loopNode, iteration)
		return
	}

	e.completeLoopNode(loopNodeID, iteration, stoppedReason)
	e.logger.Debug("loop node completed", "node", loopNodeID, "iterations", iteration, "stoppedReason", stoppedReason)
}

func (e *DAGExecutor) effectiveNodeTimeout(node *NodeConfig) time.Duration {
	if node == nil {
		return 0
	}
	if node.Timeout > 0 {
		return node.Timeout
	}
	return e.cfg.DefaultNodeTimeout
}

func (e *DAGExecutor) armNodeTimer(node *NodeConfig, subTaskID string, completion NodeCompletion) {
	timeout := e.effectiveNodeTimeout(node)
	if timeout <= 0 {
		return
	}
	e.state.AddTimer(Timer{
		Kind:       TimerKindNodeTimeout,
		NodeID:     completion.NodeID,
		SubTaskID:  subTaskID,
		FiresAt:    time.Now().Add(timeout),
		Duration:   timeout,
		MapNodeID:  completion.MapNodeID,
		LoopNodeID: completion.LoopNodeID,
		ItemIndex:  completion.ItemIndex,
	})
}

func (e *DAGExecutor) cancelNodeTimer(subTaskID string) {
	e.state.RemoveTimerBySubTask(TimerKindNodeTimeout, subTaskID)
}

func (e *DAGExecutor) cancelAllNodeTimers() {
	if len(e.state.Timers) == 0 {
		return
	}
	kept := e.state.Timers[:0]
	for _, t := range e.state.Timers {
		if t.Kind != TimerKindNodeTimeout && t.Kind != TimerKindWake {
			kept = append(kept, t)
		}
	}
	for i := len(kept); i < len(e.state.Timers); i++ {
		e.state.Timers[i] = Timer{}
	}
	e.state.Timers = kept
}

func (e *DAGExecutor) beginExitChain(outcome, errMsg string) {
	e.state.PendingOutcome = outcome
	e.state.PendingErrorMsg = errMsg

	if e.cfg.OnExit != nil && !e.state.ExitHandlersRan {
		e.state.ExitHandlersRan = true
		e.injectWorkflowStatus(outcome, errMsg)
	}

	var handlers []string
	if e.cfg.OnExit != nil {
		handlers = exitHandlerNodesToRun(e.cfg.OnExit, outcome)
	}

	if len(handlers) == 0 {
		e.publishFinalResponse()
		return
	}

	e.state.InExitHandler = true
	e.state.ExitHandlerQueue = append([]string(nil), handlers[1:]...)
	e.dispatchExitHandler(handlers[0])
}

func (e *DAGExecutor) injectWorkflowStatus(outcome, errMsg string) {
	var failedNodeID string
	if e.state.ErrorState != nil {
		failedNodeID = e.state.ErrorState.FailedNodeID
	}
	wf := e.state.NodeOutputs["workflow"]
	if wf == nil {
		wf = map[string]any{}
	}
	wf["status"] = outcome
	wf["error"] = map[string]any{
		"message": errMsg,
		"node_id": failedNodeID,
	}
	e.state.NodeOutputs["workflow"] = wf
}

func (e *DAGExecutor) dispatchExitHandler(nodeID string) {
	node, ok := e.dag.Nodes[nodeID]
	if !ok {
		e.logger.Warn("exit handler references non-existent node", "node", nodeID)
		e.onExitHandlerDone("")
		return
	}
	e.logger.Info("executing exit handler node",
		"node", nodeID,
		"outcome", e.state.PendingOutcome,
		"timeout", e.effectiveNodeTimeout(node),
	)
	subTaskID := idgen.New()
	e.publishNodeStart(node, "", subTaskID)
	switch node.Type {
	case "agent", "workflow":
		e.dispatchAgentNode(e.execCtx.Ctx, node, subTaskID)
	case "tool":
		e.dispatchToolNode(e.execCtx.Ctx, node, subTaskID)
	default:
		e.handleNodeError(node.ID, fmt.Errorf("exit handler node %q has type %q, which the exit-handler chain cannot run; on_exit supports agent, workflow and tool nodes", node.ID, node.Type))
	}
}

func (e *DAGExecutor) advanceExitHandlers(nc NodeCompletion) {
	if nc.Error == nil && nc.Output != nil {
		e.state.NodeOutputs[nc.NodeID] = map[string]any{"output": nc.Output}
	}
	e.state.RemovePending(nc.NodeID)
	if nc.SubTaskID != "" {
		e.cancelNodeTimer(nc.SubTaskID)
	}
	if nc.Error != nil {
		e.logger.Warn("exit handler node failed", "node", nc.NodeID, "error", nc.Error)
		errMsg := nc.Error.Error()
		e.publishNodeResult(nc.NodeID, "error", &errMsg)
	} else {
		e.logger.Debug("exit handler node completed", "node", nc.NodeID)
		e.publishNodeResult(nc.NodeID, "success", nil)
	}
	e.onExitHandlerDone(nc.NodeID)
}

func (e *DAGExecutor) onExitHandlerDone(_ string) {
	if len(e.state.ExitHandlerQueue) == 0 {
		e.state.InExitHandler = false
		e.publishFinalResponse()
		return
	}
	next := e.state.ExitHandlerQueue[0]
	e.state.ExitHandlerQueue = e.state.ExitHandlerQueue[1:]
	e.dispatchExitHandler(next)
}

func (e *DAGExecutor) publishFinalResponse() {
	if !e.stillHoldsLocked("publish the final answer") {
		return
	}
	published := true
	switch e.state.PendingOutcome {
	case "success":
		e.publishFinalSuccess()
	case "failure":
		e.publishFinalFailure()
	case "cancelled":
		e.publishFinalCancelled()
	default:
		published = false
		e.logger.Error("publishFinalResponse: unknown pending outcome",
			"outcome", e.state.PendingOutcome,
			"executionID", e.state.ExecutionID,
		)
	}
	if published {
		e.commitResponseLocked()
	}
	e.state.Finalized = true
	if e.state.HasPausedRows && !e.lost {
		executionID := e.state.ExecutionID
		e.instance.retirePausedNodesAsync(executionID)
	}
	e.state.ExitHandlerQueue = nil
	if e.execCtx != nil && e.execCtx.Cancel != nil {
		e.execCtx.Cancel()
	}
}

func (e *DAGExecutor) isExitHandlerNode(nodeID string) bool {
	if e.cfg.OnExit == nil {
		return false
	}
	return slices.Contains(exitHandlerNodeIDs(e.cfg.OnExit), nodeID)
}

func exitHandlerNodesToRun(eh *ExitHandler, outcome string) []string {
	if eh == nil {
		return nil
	}
	var nodes []string
	if eh.Always != "" {
		nodes = append(nodes, eh.Always)
	}
	switch outcome {
	case "success":
		if eh.OnSuccess != "" {
			nodes = append(nodes, eh.OnSuccess)
		}
	case "failure":
		if eh.OnFailure != "" {
			nodes = append(nodes, eh.OnFailure)
		}
	case "cancelled":
		if eh.OnCancel != "" {
			nodes = append(nodes, eh.OnCancel)
		}
	}
	return nodes
}

const (
	finalKeyOutputMap  = "__phaseb_final_output_map"
	finalKeyDurationNs = "__phaseb_final_duration_ns"
)

const workflowCancelledMsg = "workflow cancelled"

const workflowInstanceStoppedMsg = "workflow was redeployed or stopped during execution; retry the request"

type parkedStep struct {
	nodeID        string
	subTaskID     string
	iterOfNode    bool
	beforeRebuild bool
}

func parkedStepOf(nodeID, subTaskID, mapNodeID, loopNodeID string) parkedStep {
	return parkedStep{nodeID: nodeID, subTaskID: subTaskID, iterOfNode: mapNodeID != "" || loopNodeID != ""}
}

func (e *DAGExecutor) recordParkedPause(requestID string, step parkedStep) {
	if e.parkedPauses == nil {
		e.parkedPauses = map[string]parkedStep{}
	}
	e.parkedPauses[requestID] = step
}

func (e *DAGExecutor) forgetParkedPause(requestID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.parkedPauses, requestID)
}

func (e *DAGExecutor) parkedHereLocked() bool {
	for _, step := range e.parkedPauses {
		if !step.beforeRebuild {
			return true
		}
	}
	return false
}

func (e *DAGExecutor) hasParkedPause(requestID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.parkedPauses[requestID]
	return ok
}

func (e *DAGExecutor) settleRequestLocked() {
	if e.execCtx == nil || e.lost {
		return
	}
	switch {
	case e.state.Finalized:
		e.endRunLocked()
		e.execCtx.ackRequest()
	case e.parkedLocked() && (e.instance == nil || e.writeParkLocked()):
		e.execCtx.ackRequest()
	}
}

func (e *DAGExecutor) settled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.Finalized || e.parkWritten || (e.instance == nil && e.parkedLocked())
}

func (e *DAGExecutor) parkedLocked() bool {
	s := e.state
	if s.Finalized || s.PendingOutcome != "" || s.InExitHandler || len(s.PendingNodes) == 0 || (len(e.parkedPauses) == 0 && !s.hasWakeTimers()) {
		return false
	}
	if slices.ContainsFunc(s.Timers, func(t Timer) bool { return t.Kind == TimerKindRetry }) {
		return false
	}
	parkedNodes, parkedSubTasks := e.parkedStepSets()
	for nodeID := range s.PendingNodes {
		if !e.pendingNodeParked(nodeID, parkedNodes, parkedSubTasks) {
			return false
		}
	}
	return true
}

func (e *DAGExecutor) parkedStepSets() (parkedNodes, parkedSubTasks map[string]bool) {
	parkedNodes = map[string]bool{}
	parkedSubTasks = map[string]bool{}
	for _, step := range e.parkedPauses {
		if step.iterOfNode {
			parkedSubTasks[step.subTaskID] = true
		} else {
			parkedNodes[step.nodeID] = true
		}
	}
	return parkedNodes, parkedSubTasks
}

func (e *DAGExecutor) pendingNodeParked(nodeID string, parkedNodes, parkedSubTasks map[string]bool) bool {
	node := e.dag.Nodes[nodeID]
	if node == nil {
		return false
	}
	branches := e.state.ActiveBranches[nodeID]
	switch node.Type {
	case "map":
		done, _ := e.state.Metadata[nodeID+"_done"].([]bool)
		open := 0
		for _, b := range branches {
			if b.Index < len(done) && done[b.Index] {
				continue
			}
			if !parkedSubTasks[b.SubTaskID] {
				return false
			}
			open++
		}
		return open > 0
	case "loop":
		return len(branches) > 0 && parkedSubTasks[branches[len(branches)-1].SubTaskID]
	case NodeTypeTimer:
		return e.state.hasWakeTimer(nodeID)
	default:
		return parkedNodes[nodeID]
	}
}

func (e *DAGExecutor) workflowDisplayName() string {
	return cmp.Or(e.cfg.DisplayName, e.cfg.Name)
}

func (e *DAGExecutor) finalizeSuccess(duration time.Duration) {
	if e.state.PendingOutcome != "" {
		return
	}
	e.cancelAllNodeTimers()

	finalOutput, err := resolveOutputMapping(e.cfg.OutputMapping, e.state.NodeOutputs, e.instance.namedOutputNodes)
	if err != nil {
		e.logger.Warn("output mapping reference did not resolve; leaving it verbatim", "problem", err.Error())
		finalOutput = resolveOutputMappingLeavingUnresolved(e.cfg.OutputMapping, e.state.NodeOutputs, e.instance.namedOutputNodes)
	}

	outputMap, ok := finalOutput.(map[string]any)
	if !ok {
		outputMap = map[string]any{"result": finalOutput}
	}
	e.state.Metadata[finalKeyOutputMap] = outputMap
	e.state.Metadata[finalKeyDurationNs] = int64(duration)

	e.beginExitChain("success", "")
}

func (e *DAGExecutor) finalizeFailure(errMsg string) {
	if e.state.PendingOutcome != "" {
		return
	}
	e.cancelAllNodeTimers()
	e.beginExitChain("failure", errMsg)
}

func (e *DAGExecutor) finalizeCancelled() {
	if e.state.PendingOutcome != "" {
		return
	}
	e.cancelAllNodeTimers()
	e.beginExitChain("cancelled", workflowCancelledMsg)
}

func (e *DAGExecutor) publishFinalSuccess() {
	ctx := e.execCtx.Ctx
	outputMap, _ := e.state.Metadata[finalKeyOutputMap].(map[string]any)
	if outputMap == nil {
		outputMap = map[string]any{}
	}
	durationNs, _ := e.state.Metadata[finalKeyDurationNs].(int64)
	duration := time.Duration(durationNs)

	var outputArtifactRef *ArtifactRef
	if e.instance.svc.Artifacts() != nil {
		data, _ := json.Marshal(outputMap)
		artifactName := fmt.Sprintf(a2a.WorkflowHandoffArtifactPrefix+"%s_output_%s.json", e.cfg.Name, shortID(e.state.ExecutionID))
		saveResult, saveErr := artifact.SaveWithMetadata(ctx, e.instance.svc.Artifacts(),
			e.cfg.Name, e.execCtx.UserProps.UserID, e.execCtx.ContextID,
			artifactName, data, "application/json",
			artifact.MetadataOpts{
				Description:    fmt.Sprintf("Output of workflow %s", e.workflowDisplayName()),
				Source:         "workflow_execution",
				SuppressSchema: true,
				Tags:           []string{a2a.ArtifactTagWorking},
			}, e.logger)
		if saveErr != nil {
			e.logger.Error("failed to save output artifact", "error", saveErr)
		} else {
			outputArtifactRef = &ArtifactRef{Name: artifactName, Version: saveResult.DataVersion}
		}
	}

	partialFailure := e.partialFailureReport()
	var partialFailureMsg *string
	if partialFailure != nil {
		partialFailureMsg = new(partialFailure.message())
	}

	e.instance.publishWorkflowEvent(ctx, e.execCtx, WorkflowExecutionResultData{
		Type:              string(a2a.SignalWorkflowExecutionResult),
		Status:            "success",
		OutputArtifactRef: outputArtifactRef,
		ErrorMessage:      partialFailureMsg,
		WorkflowOutput:    outputMap,
	})

	var responseParts []a2a.Part
	if e.execCtx.IsStructuredInvocation {
		result := map[string]any{
			"type":        "structured_invocation_result",
			"status":      "success",
			"retry_count": e.totalRetryCount(),
		}
		if outputArtifactRef != nil {
			result["output_artifact_ref"] = map[string]any{
				"name":    outputArtifactRef.Name,
				"version": outputArtifactRef.Version,
			}
		}
		if partialFailureMsg != nil {
			result["error_message"] = *partialFailureMsg
		}
		responseParts = append(responseParts, a2a.NewDataPart(result))
	} else {
		summary := fmt.Sprintf("Workflow %q completed successfully.", e.workflowDisplayName())
		if partialFailureMsg != nil {
			summary = fmt.Sprintf("Workflow %q completed, but %s.", e.workflowDisplayName(), *partialFailureMsg)
		}
		responseParts = append(responseParts, a2a.NewDataPart(outputMap))
		responseParts = append(responseParts, a2a.NewTextPart(summary))
	}

	responseMsg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleAgent,
		Parts:     responseParts,
	}

	task := a2a.Task{
		ID:        e.execCtx.TaskID,
		ContextID: e.execCtx.ContextID,
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:     a2a.TaskStateCompleted,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Message:   &responseMsg,
		},
		Metadata: map[string]any{
			"agent_name":       e.cfg.Name,
			"workflow_name":    e.cfg.Name,
			"execution_id":     e.state.ExecutionID,
			"duration_seconds": duration.Seconds(),
		},
	}
	e.state.UsageFacts.AddTo(task.Metadata)
	e.addDecisionTokenUsage(task.Metadata)
	if partialFailure != nil {
		task.Metadata[a2a.MetadataPartialFailure] = partialFailure
	}

	e.instance.publishTaskResponse(ctx, e.execCtx, task)
	if partialFailure != nil {
		e.logger.Warn("workflow completed with partial failure",
			"executionID", e.state.ExecutionID,
			"failedNodeID", partialFailure.FailedNodeID,
			"failedNodeCount", len(partialFailure.FailedNodes),
			"cascadeSkippedCount", len(partialFailure.SkippedNodes),
			"duration", duration,
		)
		return
	}
	e.logger.Info("workflow completed successfully",
		"executionID", e.state.ExecutionID,
		"duration", duration,
	)
}

type failedNodeEntry struct {
	NodeID       string `json:"node_id"`
	ErrorMessage string `json:"error_message"`
}

type partialFailureReport struct {
	FailedNodeID string            `json:"failed_node_id"`
	ErrorMessage string            `json:"error_message"`
	FailedNodes  []failedNodeEntry `json:"failed_nodes"`
	SkippedNodes []string          `json:"skipped_nodes"`
}

func (e *DAGExecutor) partialFailureReport() *partialFailureReport {
	if e.state.ErrorState == nil {
		return nil
	}
	failed := e.failedNodes()
	entries := make([]failedNodeEntry, 0, len(failed))
	for _, f := range failed {
		entries = append(entries, failedNodeEntry{NodeID: f.FailedNodeID, ErrorMessage: f.ErrorMessage})
	}
	return &partialFailureReport{
		FailedNodeID: e.state.ErrorState.FailedNodeID,
		ErrorMessage: e.state.ErrorState.ErrorMessage,
		FailedNodes:  entries,
		SkippedNodes: e.cascadeSkippedNodes(failed),
	}
}

func (r *partialFailureReport) message() string {
	msg := fmt.Sprintf("node %q failed and was skipped: %s", r.FailedNodeID, r.ErrorMessage)
	if others := r.otherFailedNodeIDs(); len(others) > 0 {
		msg = fmt.Sprintf("%s (and %d other failed node(s): %s)",
			msg, len(others), strings.Join(others, ", "))
	}
	return msg
}

func (r *partialFailureReport) otherFailedNodeIDs() []string {
	others := make([]string, 0, len(r.FailedNodes))
	for _, f := range r.FailedNodes {
		if f.NodeID != r.FailedNodeID {
			others = append(others, f.NodeID)
		}
	}
	slices.Sort(others)
	return others
}

func (e *DAGExecutor) failedNodes() []ErrorInfo {
	failed := e.state.FailedNodes
	if es := e.state.ErrorState; es != nil && !slices.ContainsFunc(failed, func(f ErrorInfo) bool { return f.FailedNodeID == es.FailedNodeID }) {
		failed = append(slices.Clone(failed), *es)
	}
	return failed
}

func (e *DAGExecutor) cascadeSkippedNodes(failed []ErrorInfo) []string {
	cascade := make([]string, 0, len(failed))
	seen := make(map[string]bool, len(e.state.SkippedNodes))
	queue := make([]string, 0, len(failed))
	for _, f := range failed {
		if seen[f.FailedNodeID] {
			continue
		}
		seen[f.FailedNodeID] = true
		if e.state.IsNodeSkipped(f.FailedNodeID) {
			cascade = append(cascade, f.FailedNodeID)
		}
		queue = append(queue, e.dag.Forward[f.FailedNodeID]...)
	}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if seen[nodeID] || !e.state.IsNodeSkipped(nodeID) {
			continue
		}
		seen[nodeID] = true
		cascade = append(cascade, nodeID)
		queue = append(queue, e.dag.Forward[nodeID]...)
	}
	slices.Sort(cascade)
	return cascade
}

func (e *DAGExecutor) failureText(errMsg string) string {
	if e.state.BudgetResetsAt != nil {
		stop := &budget.ExhaustedError{Tier: e.state.BudgetTier, ResetsAt: *e.state.BudgetResetsAt, SystemUser: e.state.BudgetSystemUser}
		return fmt.Sprintf("Workflow %q was stopped because %s. %s", e.workflowDisplayName(), budget.SpentText(stop), budget.ContactAdminText)
	}
	return fmt.Sprintf("Workflow %q failed: %s", e.workflowDisplayName(), errMsg)
}

func (e *DAGExecutor) publishFinalFailure() {
	errMsg := e.state.PendingErrorMsg

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	e.instance.publishWorkflowEvent(ctx, e.execCtx, WorkflowExecutionResultData{
		Type:              string(a2a.SignalWorkflowExecutionResult),
		Status:            "error",
		OutputArtifactRef: nil,
		ErrorMessage:      &errMsg,
		WorkflowOutput:    nil,
	})

	var responseParts []a2a.Part
	if e.execCtx.IsStructuredInvocation {
		result := map[string]any{
			"type":          "structured_invocation_result",
			"status":        "error",
			"error_message": errMsg,
			"retry_count":   e.totalRetryCount(),
		}
		responseParts = append(responseParts, a2a.NewDataPart(result))
	} else {
		responseParts = append(responseParts, a2a.NewTextPart(e.failureText(errMsg)))
	}

	responseMsg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleAgent,
		Parts:     responseParts,
	}

	task := a2a.Task{
		ID:        e.execCtx.TaskID,
		ContextID: e.execCtx.ContextID,
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:     a2a.TaskStateFailed,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Message:   &responseMsg,
		},
		Metadata: map[string]any{
			"agent_name":    e.cfg.Name,
			"workflow_name": e.cfg.Name,
			"execution_id":  e.state.ExecutionID,
			"error":         errMsg,
		},
	}
	if e.state.BudgetResetsAt != nil {
		task.Metadata[a2a.ErrorDataLLMErrorCategory] = taxonomy.ErrorCategoryBudgetExhausted
	}
	e.state.UsageFacts.AddTo(task.Metadata)
	e.addDecisionTokenUsage(task.Metadata)
	e.addTerminationMetadata(task.Metadata, "")

	e.instance.publishTaskResponse(ctx, e.execCtx, task)
	e.logger.Warn("workflow failed",
		"executionID", e.state.ExecutionID,
		"error", failureLogText(errMsg),
	)
}

func (e *DAGExecutor) publishFinalCancelled() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cancelledMsg := workflowCancelledMsg
	e.instance.publishWorkflowEvent(ctx, e.execCtx, WorkflowExecutionResultData{
		Type:              string(a2a.SignalWorkflowExecutionResult),
		Status:            "cancelled",
		OutputArtifactRef: nil,
		ErrorMessage:      &cancelledMsg,
		WorkflowOutput:    nil,
	})

	var responseParts []a2a.Part
	if e.execCtx.IsStructuredInvocation {
		result := map[string]any{
			"type":          "structured_invocation_result",
			"status":        "cancelled",
			"error_message": workflowCancelledMsg,
			"retry_count":   e.totalRetryCount(),
		}
		responseParts = append(responseParts, a2a.NewDataPart(result))
	} else {
		responseParts = append(responseParts, a2a.NewTextPart(
			fmt.Sprintf("Workflow %q was cancelled.", e.workflowDisplayName()),
		))
	}

	responseMsg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleAgent,
		Parts:     responseParts,
	}

	task := a2a.Task{
		ID:        e.execCtx.TaskID,
		ContextID: e.execCtx.ContextID,
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:     a2a.TaskStateCancelled,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Message:   &responseMsg,
		},
		Metadata: map[string]any{
			"agent_name":    e.cfg.Name,
			"workflow_name": e.cfg.Name,
			"execution_id":  e.state.ExecutionID,
		},
	}
	e.state.UsageFacts.AddTo(task.Metadata)
	e.addDecisionTokenUsage(task.Metadata)
	e.addTerminationMetadata(task.Metadata, a2a.TerminationCancelled)

	e.instance.publishTaskResponse(ctx, e.execCtx, task)
	e.logger.Debug(workflowCancelledMsg, "executionID", e.state.ExecutionID)
}

func (e *DAGExecutor) publishNodeStart(node *NodeConfig, parallelGroupID, subTaskID string) {
	dependsOn := node.waitsFor()
	if dependsOn == nil {
		dependsOn = []string{}
	}
	data := WorkflowNodeExecutionStartData{
		Type:            string(a2a.SignalWorkflowNodeExecStart),
		NodeID:          node.ID,
		NodeType:        node.Type,
		ParallelGroupID: optString(parallelGroupID),
		SubTaskID:       optString(subTaskID),
		IterationIndex:  e.parallelBranchIndex(parallelGroupID, node.ID),
		DependsOn:       dependsOn,
		AgentName:       optString(branchAgentName(node)),
	}

	switch node.Type {
	case "tool":
		data.ToolName = optString(node.ToolName)
	case "switch":
		for _, c := range node.Cases {
			data.Cases = append(data.Cases, SwitchCaseRef(c))
		}
		data.DefaultBranch = optString(node.DefaultCase)
		data.Condition = optString(node.Condition)
	case "loop":
		data.Condition = optString(node.Condition)
		if node.MaxIterations > 0 {
			n := node.MaxIterations
			data.MaxIterations = &n
		}
		if node.Delay > 0 {
			d := node.Delay.String()
			data.LoopDelay = &d
		}
	case "map":
	}

	e.instance.publishWorkflowEvent(e.execCtx.Ctx, e.execCtx, data)
}

func (e *DAGExecutor) parallelBranchIndex(parallelGroupID, nodeID string) *int {
	if parallelGroupID == "" {
		return nil
	}
	group, ok := e.state.ParallelBranchAssignments[parallelGroupID]
	if !ok {
		return nil
	}
	idx, ok := group[nodeID]
	if !ok {
		return nil
	}
	return &idx
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (e *DAGExecutor) publishNodeResult(nodeID, status string, errMsg *string) {
	e.publishNodeResultMeta(nodeID, status, errMsg, nil)
}

func (e *DAGExecutor) publishNodeResultMeta(nodeID, status string, errMsg *string, metadata map[string]any) {
	completedSubTaskID := e.state.CompletedNodes[nodeID]
	var outputRef *ArtifactRef
	if out, ok := e.state.NodeOutputs[nodeID]; ok && completedSubTaskID != "" {
		_ = out
		outputRef = &ArtifactRef{
			Name:    fmt.Sprintf(a2a.WorkflowHandoffArtifactPrefix+"%s_output_%s.json", nodeID, shortID(e.state.ExecutionID)),
			Version: 0,
		}
	}
	data := WorkflowNodeExecutionResultData{
		Type:              string(a2a.SignalWorkflowNodeExecResult),
		NodeID:            nodeID,
		Status:            status,
		OutputArtifactRef: outputRef,
		ErrorMessage:      errMsg,
		Metadata:          e.withNodePreviews(nodeID, metadata),
	}

	e.instance.publishWorkflowEvent(e.execCtx.Ctx, e.execCtx, data)
}
