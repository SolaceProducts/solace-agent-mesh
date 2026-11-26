// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const (
	checkpointWriteTimeout = 2 * time.Second

	maxCheckpointBytes = 8 << 20

	checkpointDeadlineMargin = time.Hour

	checkpointMaxIdle = 7 * 24 * time.Hour

	settleRequestAttempts = 3

	defaultCheckpointPurgeEvery = time.Hour
	checkpointPurgeBatch        = 500

	defaultCheckpointPurgeFirstWithin = 5 * time.Minute

	checkpointBreakerFailures = 3
	checkpointBreakerCooldown = time.Minute
)

var mapCounterSuffixes = [...]string{"_completed", "_total", "_concurrency"}

type checkpointCursor struct {
	last       []byte
	lastOpen   string
	unwritable bool
	retired    bool
}

type checkpointBreaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

func (b *checkpointBreaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !now.Before(b.openUntil)
}

func (b *checkpointBreaker) record(failed bool, now time.Time) (opened, closed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !failed {
		closed = b.failures >= checkpointBreakerFailures
		b.failures, b.openUntil = 0, time.Time{}
		return false, closed
	}
	b.failures++
	if b.failures >= checkpointBreakerFailures {
		b.openUntil = now.Add(checkpointBreakerCooldown)
	}
	return b.failures == checkpointBreakerFailures, false
}

func restoreMetadataTypes(meta map[string]any, dag *DependencyGraph) {
	if meta == nil {
		return
	}
	if dag != nil {
		for id, node := range dag.Nodes {
			if node != nil && node.Type == "map" {
				restoreMapCounters(meta, id)
			}
		}
	}
	if f, ok := meta[finalKeyDurationNs].(float64); ok {
		meta[finalKeyDurationNs] = int64(f)
	}
}

func restoreMapCounters(meta map[string]any, mapNodeID string) {
	if v, ok := meta[mapNodeID+"_done"].([]any); ok {
		meta[mapNodeID+"_done"] = boolsOf(v)
	}
	for _, suffix := range mapCounterSuffixes {
		if f, ok := meta[mapNodeID+suffix].(float64); ok {
			meta[mapNodeID+suffix] = int(f)
		}
	}
}

func boolsOf(values []any) []bool {
	out := make([]bool, len(values))
	for i, v := range values {
		out[i], _ = v.(bool)
	}
	return out
}

type definitionShape struct {
	Shape  int         `json:"shape"`
	Nodes  []nodeShape `json:"nodes"`
	OnExit []string    `json:"on_exit"`
}

const definitionShapeVersion = 1

type nodeShape struct {
	ID             string   `json:"id"`
	Type           string   `json:"type"`
	DependsOn      []string `json:"depends_on"`
	BodyNode       string   `json:"body_node"`
	TemplateNode   string   `json:"template_node"`
	Branches       []string `json:"branches"`
	EscalationMode string   `json:"escalation_mode"`
	Signal         string   `json:"signal,omitempty"`
}

func workflowDefinitionHash(cfg *WorkflowConfig) string {
	shape := definitionShape{Shape: definitionShapeVersion, Nodes: make([]nodeShape, 0, len(cfg.Nodes))}
	for _, n := range cfg.Nodes {
		if n == nil {
			continue
		}
		shape.Nodes = append(shape.Nodes, nodeShape{
			ID:             n.ID,
			Type:           n.Type,
			DependsOn:      slices.Sorted(slices.Values(n.DependsOn)),
			BodyNode:       n.BodyNode,
			TemplateNode:   n.TemplateNode,
			Branches:       definitionBranches(n),
			EscalationMode: n.EscalationMode,
			Signal:         n.Signal,
		})
	}
	slices.SortFunc(shape.Nodes, func(a, b nodeShape) int { return strings.Compare(a.ID, b.ID) })
	if h := cfg.OnExit; h != nil {
		shape.OnExit = []string{h.Always, h.OnSuccess, h.OnFailure, h.OnCancel}
	}
	raw, _ := json.Marshal(shape)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func definitionBranches(n *NodeConfig) []string {
	targets := make([]string, 0, len(n.Cases)+3+len(n.Routes))
	for _, c := range n.Cases {
		targets = append(targets, c.Node)
	}
	targets = append(targets, n.DefaultCase, n.Escalate, n.Default)
	for _, option := range slices.Sorted(maps.Keys(n.Routes)) {
		targets = append(targets, option+"="+n.Routes[option])
	}
	return targets
}

func (e *DAGExecutor) checkpointStore() *wfstore.RunRepo {
	if e.instance == nil || e.execCtx == nil {
		return nil
	}
	return e.instance.checkpointStore()
}

func (e *DAGExecutor) writeCheckpointLocked() {
	c := &e.checkpoint
	if c.unwritable || c.retired {
		return
	}
	store := e.checkpointStore()
	if store == nil {
		return
	}
	snapshot, err := json.Marshal(e.state)
	if err != nil {
		e.stopCheckpointingLocked("the state does not encode", "error", err)
		return
	}
	if bytes.Equal(snapshot, c.last) && openPausesKey(e.parkedPauses) == c.lastOpen {
		return
	}
	if len(snapshot) > maxCheckpointBytes {
		e.stopCheckpointingLocked("the state is over the size cap",
			"snapshotBytes", len(snapshot), "maxBytes", maxCheckpointBytes)
		return
	}
	e.storeCheckpointLocked(store, snapshot)
}

func (e *DAGExecutor) stopCheckpointingLocked(reason string, attrs ...any) {
	e.checkpoint.unwritable = true
	e.logger.Warn("workflow checkpoint no longer written; a restart continues from the last state written",
		append([]any{"executionID", e.state.ExecutionID, "reason", reason}, attrs...)...)
}

func openPausesKey(pauses map[string]parkedStep) string {
	return strings.Join(slices.Sorted(maps.Keys(pauses)), ",")
}

func (e *DAGExecutor) runState(snapshot []byte, execContext string) wfstore.RunState {
	st := wfstore.RunState{
		TaskID:         e.execCtx.TaskID,
		WorkflowName:   e.cfg.Name,
		DefinitionHash: e.instance.definitionHash,
		StateSnapshot:  string(snapshot),
		ExecContext:    execContext,
		OpenPauses:     slices.Sorted(maps.Keys(e.parkedPauses)),
	}
	if !e.state.WorkflowDeadline.IsZero() {
		deadline := e.state.WorkflowDeadline.UnixMilli()
		st.DeadlineAt = &deadline
	}
	st.WakeAt = e.state.nextWakeAt()
	return st
}

func (e *DAGExecutor) storeCheckpointLocked(store *wfstore.RunRepo, snapshot []byte) bool {
	c := &e.checkpoint
	breaker := &e.instance.checkpointBreaker
	if !breaker.allow(time.Now()) {
		return false
	}
	execContext, err := e.instance.runExecContext(e.execCtx)
	if err != nil {
		e.stopCheckpointingLocked("the exec context does not encode", "error", err)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), checkpointWriteTimeout)
	defer cancel()
	started := time.Now()
	h, err := store.WriteStep(ctx, e.state.ExecutionID, e.execCtx.holderOf(), e.runState(snapshot, execContext))
	if errors.Is(err, wfstore.ErrNotHolder) {
		e.recordCheckpointWrite(nil)
		e.loseRunLocked("write a step")
		return false
	}
	e.recordCheckpointWrite(err)
	if err != nil {
		e.logger.Warn("workflow checkpoint write failed; a restart repeats the work since the previous checkpoint",
			"executionID", e.state.ExecutionID, "error", err)
		return false
	}
	e.execCtx.setHolder(h)
	c.last, c.lastOpen = snapshot, openPausesKey(e.parkedPauses)
	e.logger.Debug("workflow checkpoint written",
		"executionID", e.state.ExecutionID,
		"snapshotBytes", len(snapshot), "durationMs", time.Since(started).Milliseconds())
	return true
}

func (e *DAGExecutor) recordCheckpointWrite(err error) {
	opened, closed := e.instance.checkpointBreaker.record(err != nil, time.Now())
	switch {
	case opened:
		e.instance.logger.Warn("workflow checkpoint writes paused after repeated failures",
			"failures", checkpointBreakerFailures, "pauseSeconds", int(checkpointBreakerCooldown.Seconds()))
	case closed:
		e.instance.logger.Info("workflow checkpoint writes resumed")
	}
}

func (e *DAGExecutor) commitResponseLocked() {
	e.checkpoint.retired = true
	if e.instance == nil || e.execCtx == nil || (e.instance.checkpointStore() == nil && !e.state.HasPausedRows) {
		return
	}
	runs := e.instance.runRepo()
	if runs == nil {
		return
	}
	var err error
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), checkpointWriteTimeout)
		err = runs.MarkAnswered(ctx, e.state.ExecutionID, e.execCtx.holderOf())
		cancel()
		if err == nil {
			return
		}
		if errors.Is(err, wfstore.ErrNotHolder) {
			e.logger.Warn("workflow final answer published by a copy that lost the run in the meantime; the caller may get a second answer",
				"executionID", e.state.ExecutionID)
			e.loseRunLocked("mark the final answer")
			return
		}
	}
	e.logger.Warn("workflow final-response mark not stored; a restart before the request is acknowledged answers the caller again",
		"executionID", e.state.ExecutionID, "error", err)
}

func (e *DAGExecutor) redispatchInFlightSteps() {
	if e.cancelRequested() && !e.state.InExitHandler {
		e.logger.Info("workflow run restored from its checkpoint after it was cancelled or timed out; ending it",
			"executionID", e.state.ExecutionID)
		return
	}
	retrying := map[string]bool{}
	timing := map[string]bool{}
	for _, t := range e.state.Timers {
		switch t.Kind {
		case TimerKindRetry:
			retrying[t.NodeID] = true
		case TimerKindWake:
			if n := e.dag.Nodes[t.NodeID]; n != nil && n.Type == NodeTypeTimer {
				timing[t.NodeID] = true
			}
		}
	}
	parkedNodes, parkedSubTasks := e.parkedStepSets()
	redispatched := 0
	for _, nodeID := range slices.Sorted(maps.Keys(e.state.PendingNodes)) {
		if e.state.Finalized {
			break
		}
		if !retrying[nodeID] && !timing[nodeID] && !parkedNodes[nodeID] {
			redispatched += e.redispatchPendingNode(nodeID, parkedSubTasks)
		}
	}
	e.logger.Info("workflow run restored from its checkpoint",
		"executionID", e.state.ExecutionID,
		"completedNodes", len(e.state.CompletedNodes),
		"redispatchedSteps", redispatched,
		"retryingNodes", len(retrying),
		"timerNodes", len(timing),
		"parkedSteps", len(parkedNodes)+len(parkedSubTasks))
}

func (e *DAGExecutor) redispatchPendingNode(nodeID string, parkedSubTasks map[string]bool) int {
	node := e.dag.Nodes[nodeID]
	switch {
	case node == nil:
		e.handleNodeError(nodeID, fmt.Errorf("node %q of the restored run is not in the workflow", nodeID))
		return 0
	case e.state.InExitHandler && e.isExitHandlerNode(nodeID):
		e.redispatchExitHandler(node)
		return 1
	case e.state.InExitHandler:
		return 0
	case node.Type == "map":
		return e.redispatchMapBranches(node, parkedSubTasks)
	case node.Type == "loop":
		return e.redispatchLoopIteration(node, parkedSubTasks)
	default:
		e.redispatchTopLevelNode(node)
		return 1
	}
}

func (e *DAGExecutor) countRestoredAttempt(group string) {
	if e.state.RestoredAttempts == nil {
		e.state.RestoredAttempts = map[string]int{}
	}
	e.state.RestoredAttempts[group]++
}

func (e *DAGExecutor) dropSubTaskOfNode(nodeID string) {
	if old, ok := e.state.SubTaskForNode(nodeID); ok {
		e.supersedeSubTask(old)
	}
	e.state.RemoveSubTaskByNode(nodeID)
	e.state.RemovePending(nodeID)
}

func (e *DAGExecutor) supersedeSubTask(subTaskID string) {
	e.cancelNodeTimer(subTaskID)
	if target, ok := e.state.TargetForSubTask(subTaskID); ok && target != "" {
		e.instance.sendCancelToSubTask(subTaskID, target)
	}
	e.state.RemoveSubTask(subTaskID)
}

func (e *DAGExecutor) redispatchTopLevelNode(node *NodeConfig) {
	e.countRestoredAttempt(e.state.RetryGroupFor(node.ID))
	e.dropSubTaskOfNode(node.ID)
	subTaskID := idgen.New()
	e.publishNodeStart(node, e.parallelGroupOf(node.ID), subTaskID)
	dctx, dcancel := e.dispatchCtx()
	defer dcancel()
	switch node.Type {
	case "agent", "workflow":
		e.dispatchAgentNode(dctx, node, subTaskID)
	case "tool":
		e.dispatchToolNode(dctx, node, subTaskID)
	case "decision":
		e.dispatchDecisionNode(dctx, node, subTaskID)
	case NodeTypeWait:
		e.dispatchWaitNode(dctx, node)
	case NodeTypeTimer:
		e.dispatchTimerNode(node)
	default:
		e.handleNodeError(node.ID, errNotRedispatchable(node))
	}
}

func (e *DAGExecutor) redispatchExitHandler(node *NodeConfig) {
	e.countRestoredAttempt(e.state.RetryGroupFor(node.ID))
	e.dropSubTaskOfNode(node.ID)
	e.dispatchExitHandler(node.ID)
}

func (e *DAGExecutor) redispatchMapBranches(mapNode *NodeConfig, parkedSubTasks map[string]bool) int {
	done, _ := e.state.Metadata[mapNode.ID+"_done"].([]bool)
	items, _ := e.state.Metadata[mapNode.ID+"_items"].([]any)
	var relaunch []int
	for _, b := range e.state.ActiveBranches[mapNode.ID] {
		if (b.Index < len(done) && done[b.Index]) || parkedSubTasks[b.SubTaskID] || b.Index >= len(items) {
			continue
		}
		e.supersedeSubTask(b.SubTaskID)
		relaunch = append(relaunch, b.Index)
	}
	dctx, dcancel := e.dispatchCtx()
	defer dcancel()
	parentGroup := e.state.RetryGroupFor(mapNode.ID)
	launched := 0
	for _, index := range relaunch {
		if !e.state.PendingNodes[mapNode.ID] || e.cancelRequested() {
			break
		}
		e.countRestoredAttempt(iterationRetryGroup(parentGroup, index))
		e.launchMapIteration(dctx, mapNode, items[index], index)
		launched++
	}
	return launched
}

func (e *DAGExecutor) recordMapBranch(mapNodeID string, branch BranchInfo) {
	branches := e.state.ActiveBranches[mapNodeID]
	for i := range branches {
		if branches[i].Index == branch.Index {
			branches[i] = branch
			return
		}
	}
	e.state.ActiveBranches[mapNodeID] = append(branches, branch)
}

func (e *DAGExecutor) redispatchLoopIteration(loopNode *NodeConfig, parkedSubTasks map[string]bool) int {
	branches := e.state.ActiveBranches[loopNode.ID]
	if len(branches) == 0 {
		e.handleNodeError(loopNode.ID, fmt.Errorf("loop %q of the restored run has no iteration to dispatch again", loopNode.ID))
		return 0
	}
	current := branches[len(branches)-1]
	if parkedSubTasks[current.SubTaskID] {
		return 0
	}
	e.supersedeSubTask(current.SubTaskID)
	e.state.ActiveBranches[loopNode.ID] = branches[:len(branches)-1]
	e.countRestoredAttempt(iterationRetryGroup(e.state.RetryGroupFor(loopNode.ID), current.Iteration))
	dctx, dcancel := e.dispatchCtx()
	defer dcancel()
	e.dispatchLoopIteration(dctx, loopNode, current.Iteration)
	return 1
}

func errNotRedispatchable(node *NodeConfig) error {
	return fmt.Errorf("node %q of type %q cannot be dispatched again after a restore", node.ID, node.Type)
}

func (e *DAGExecutor) parallelGroupOf(nodeID string) string {
	for group, members := range e.state.ParallelBranchAssignments {
		if _, ok := members[nodeID]; ok {
			return group
		}
	}
	return ""
}

type requestOutcome int

const (
	requestStart requestOutcome = iota
	requestNoRun
	requestRestored
	requestDropped
)

func (w *WorkflowInstance) settleDeliveredRequest(ctx context.Context, taskID string, identity *auth.Identity, msg *broker.Message, logger *slog.Logger) requestOutcome {
	runs := w.runRepo()
	if runs == nil {
		return requestNoRun
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redeliveryLookupTimeout)
	defer cancel()
	res, err := settleTaskRequestWithRetry(lookupCtx, runs, taskID, w.checkpointStore() != nil)
	if err != nil {
		logger.Warn("workflow: could not read the task's run for a delivered request; starting the run",
			"redelivered", msg.Redelivered, "error", err)
		return requestStart
	}
	switch res.Outcome {
	case wfstore.RequestCarried:
		logger.Info("workflow task request delivered again after its run parked or moved on; dropping it",
			"redelivered", msg.Redelivered, "executionID", res.Run.ExecutionID)
		return requestDropped
	case wfstore.RequestAnswered:
		logger.Info("workflow task request delivered again after its run answered; dropping it",
			"redelivered", msg.Redelivered, "executionID", res.Run.ExecutionID)
		w.abandonPausedNodes(res.Run.ExecutionID)
		return requestDropped
	case wfstore.RequestRestart:
		logger.Info("workflow task request delivered again after the AWE running it died; starting it over",
			"redelivered", msg.Redelivered, "closedPauses", res.Closed)
		return requestStart
	case wfstore.RequestRestore:
		return w.restoreHeldRun(ctx, runs, res.Run, identity, msg, logger)
	}
	return requestNoRun
}

func settleTaskRequestWithRetry(ctx context.Context, runs *wfstore.RunRepo, taskID string, restore bool) (wfstore.RequestResult, error) {
	backoff := 50 * time.Millisecond
	var err error
	for attempt := range settleRequestAttempts {
		var res wfstore.RequestResult
		if res, err = runs.SettleTaskRequest(ctx, taskID, statusClosedForRestart, restore); err == nil {
			return res, nil
		}
		if attempt == settleRequestAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return wfstore.RequestResult{}, err
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return wfstore.RequestResult{}, err
}

func (w *WorkflowInstance) restoreHeldRun(ctx context.Context, runs *wfstore.RunRepo, run *wfstore.WorkflowRun, identity *auth.Identity, msg *broker.Message, logger *slog.Logger) requestOutcome {
	delivered := a2a.DecodeUserProperties(msg.UserProperties)
	if !deliveredByOwner(run.ExecContext, delivered, identity) {
		logger.Warn("workflow task request for a stored run delivered by another user; dropping it",
			"executionID", run.ExecutionID, "redelivered", msg.Redelivered)
		return requestDropped
	}
	reason := ""
	switch {
	case run.WorkflowName != w.cfg.Name:
		reason = "the stored run belongs to another workflow"
	case run.DefinitionHash != w.definitionHash:
		reason = "the workflow definition changed since the run's state was stored"
	default:
		if err := decodableRun(run); err != nil {
			reason = err.Error()
		}
	}
	if reason != "" {
		return w.restartHeldRun(ctx, runs, run, logger, reason)
	}
	retaken, closed, err := runs.Retake(ctx, run.ExecutionID, wfstore.HeldByRequest, statusClosedForRestart)
	switch {
	case errors.Is(err, wfstore.ErrNotHolder):
		logger.Info("workflow task request delivered again after its run moved on; dropping it",
			"executionID", run.ExecutionID)
		return requestDropped
	case err != nil:
		return w.restartHeldRun(ctx, runs, run, logger, err.Error())
	}
	logger.Warn("workflow: the AWE running this run died; continuing it from its last stored step",
		"executionID", run.ExecutionID, "redelivered", msg.Redelivered, "closedPauses", closed)
	w.continueRun(ctx, retaken, msg, wfstore.HeldByRequest, func(exec *WorkflowExecution) {
		if delivered.AuthToken != "" {
			exec.Context.UserProps.AuthToken = delivered.AuthToken
		}
		if identity != nil {
			exec.Context.Identity = identity
		}
		if err := exec.Executor.Advance(ctx, &EventRestore{}); err != nil {
			exec.Executor.logger.Error("restore Advance failed", "executionID", run.ExecutionID, "error", err)
		}
		w.cleanupIfFinalized(exec)
	})
	return requestRestored
}

func (w *WorkflowInstance) restartHeldRun(ctx context.Context, runs *wfstore.RunRepo, run *wfstore.WorkflowRun, logger *slog.Logger, reason string) requestOutcome {
	closed, err := runs.Restart(ctx, run.ExecutionID, statusClosedForRestart)
	if err != nil {
		logger.Warn("workflow: could not close the run before starting it over",
			"executionID", run.ExecutionID, "error", err)
	}
	logger.Warn("workflow: the stored run cannot be continued; starting it over",
		"executionID", run.ExecutionID, "reason", reason, "closedPauses", closed)
	return requestStart
}

func decodableRun(run *wfstore.WorkflowRun) error {
	var state WorkflowExecutionState
	if err := json.Unmarshal([]byte(run.StateSnapshot), &state); err != nil {
		return fmt.Errorf("the stored state does not decode: %w", err)
	}
	var ec persistedExecContext
	if err := json.Unmarshal([]byte(run.ExecContext), &ec); err != nil {
		return fmt.Errorf("the stored context does not decode: %w", err)
	}
	return nil
}

func deliveredByOwner(execContext string, delivered a2a.UserProperties, identity *auth.Identity) bool {
	var ec persistedExecContext
	if json.Unmarshal([]byte(execContext), &ec) != nil {
		return true
	}
	if ec.UserProps.UserID != delivered.UserID {
		return false
	}
	return ec.Identity == nil || (identity != nil && identity.UserID == ec.Identity.UserID)
}

func (w *WorkflowInstance) checkpointPurgeLoop(ctx context.Context) {
	every := w.checkpointPurgeEvery
	if every <= 0 {
		every = defaultCheckpointPurgeEvery
	}
	firstWithin := w.checkpointPurgeFirstWithin
	if firstWithin <= 0 {
		firstWithin = defaultCheckpointPurgeFirstWithin
	}
	timer := time.NewTimer(rand.N(min(every, firstWithin)))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			w.purgeExpiredCheckpoints(ctx)
			timer.Reset(every)
		}
	}
}

func (w *WorkflowInstance) purgeExpiredCheckpoints(ctx context.Context) {
	runs := w.runRepo()
	if runs == nil {
		return
	}
	now := time.Now()
	held := purgeRuns(ctx, w, now.Add(-checkpointDeadlineMargin).UnixMilli(), runs.DeleteHeldPastDeadline)
	idle := purgeRuns(ctx, w, now.Add(-checkpointMaxIdle).UnixMilli(), runs.DeleteHeldIdleOlderThan)
	ended := purgeRuns(ctx, w, now.Add(-resolvedPausedAge).UnixMilli(), runs.DeleteEndedOlderThan)
	if held > 0 || idle > 0 || ended > 0 {
		w.logger.Info("workflow: removed stored runs", "heldPastDeadline", held, "heldIdle", idle, "ended", ended)
	}
}

func purgeRuns(ctx context.Context, w *WorkflowInstance, cutoff int64, del func(context.Context, int64, int) (int64, error)) int64 {
	var total int64
	for ctx.Err() == nil {
		n, err := del(ctx, cutoff, checkpointPurgeBatch)
		total += n
		if err != nil {
			w.logger.Warn("workflow: stored run purge failed; retrying at the next interval", "error", err)
			break
		}
		if n < checkpointPurgeBatch {
			break
		}
	}
	return total
}
