// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const maxHILRequestIDBytes = 256

const hilApprovalMaxTimeout = 30 * 24 * time.Hour

const (
	unusableRequestIDMessage = "The tool asked a person for an answer but gave the request an id that cannot be used, so the answer could not be collected."
	requestIDInUseMessage    = "The tool asked a person for an answer with a request id that another task already uses, so the answer could not be collected."
)

func personRequestEssentials(r *tool.Result) *tool.Result {
	id, _ := r.Data["hil_request_id"].(string)
	kind, _ := r.Data["pending_kind"].(string)
	if !usableHILRequestID(id) {
		id = ""
	}
	data := map[string]any{"pending_kind": kind, "hil_request_id": id}
	if ns, ok := r.Data["hil_timeout_ns"].(float64); ok && !math.IsNaN(ns) && !math.IsInf(ns, 0) {
		data["hil_timeout_ns"] = ns
	}
	return tool.Pending(boundedResultContent(r.Message), tool.WithData(data))
}

func usableHILRequestID(id string) bool {
	return id != "" && len(id) <= maxHILRequestIDBytes && utf8.ValidString(id) && !strings.ContainsFunc(id, unicode.IsControl)
}

func hilRequestIDAttr(id string) slog.Attr {
	if usableHILRequestID(id) {
		return slog.String("requestID", id)
	}
	return slog.Int("requestIDBytes", len(id))
}

func isPersonRequest(r *tool.Result) bool {
	return r != nil && r.Status == tool.StatusPending && classifyPendingKind(r) == PendingCallHILApproval
}

func (a *Agent) refuseUnanswerablePersonRequest(state *TaskExecutionState, toolCallID, toolName string, r *tool.Result, claimed map[string]bool) *tool.Result {
	if !isPersonRequest(r) {
		return nil
	}
	id, _ := hilWaitOf(r)
	message := ""
	switch {
	case !usableHILRequestID(id):
		message = unusableRequestIDMessage
	case claimed[id], a.pausedTasks.holdsHILRequestID(id):
		message = requestIDInUseMessage
	}
	if message == "" {
		claimed[id] = true
		return nil
	}
	if !claimed[id] {
		a.forgetEarlyAnswer(state, id)
	}
	a.logger.Warn("refusing a tool's request for a person's answer",
		"traceID", state.UserProps.TraceID, "taskID", state.TaskID, "toolCallID", toolCallID, "toolName", toolName,
		"requestIDBytes", len(id))
	return tool.Error(message)
}

func (a *Agent) expectDeliveredPersonAnswer(state *TaskExecutionState, delivered *tool.Result) {
	if !isPersonRequest(delivered) {
		return
	}
	if id, timeout := hilWaitOf(delivered); usableHILRequestID(id) {
		a.expectEarlyAnswer(state.TaskID, id, state.UserProps.UserID, timeout)
	}
}

func (a *Agent) expectAdoptedPersonAnswers(state *TaskExecutionState) {
	state.mu.Lock()
	results := slices.Collect(maps.Values(state.SyncResults))
	state.mu.Unlock()
	for _, r := range results {
		a.expectDeliveredPersonAnswer(state, r)
	}
}

type pausedCallResult struct {
	call   llm.ToolCall
	index  int
	result *tool.Result
}

func (a *Agent) pausedBatchResults(state *TaskExecutionState, logger *slog.Logger) []pausedCallResult {
	toolCalls := state.PausedAssistantMsg.ToolCalls
	if state.pauseReplayed.Load() {
		logger.Info("this task's paused batch was already replayed before the handoff; continuing the turn without re-pairing its tool calls",
			"toolCallCount", len(toolCalls))
		toolCalls = nil
	}
	syntheticIDs := syntheticErrorCallIDs(state.SyntheticToolErrors)
	state.mu.Lock()
	defer state.mu.Unlock()
	entries := make([]pausedCallResult, 0, len(toolCalls))
	dispatchIdx := 0
	for _, tc := range toolCalls {
		if strings.HasPrefix(tc.ID, hostNotifyCallIDPrefix) || syntheticIDs[tc.ID] {
			continue
		}
		result, inSync := state.SyncResults[dispatchIdx]
		pc, inPending := state.PendingCalls[tc.ID]
		if !inSync && inPending {
			result = pc.Result
		}
		if result == nil {
			logger.Warn("orphaned tool result: no result found for tool call",
				"toolCallID", tc.ID,
				"toolName", tc.Name,
				"dispatchIndex", dispatchIdx,
				"inSyncResults", inSync,
				"inPendingCalls", inPending,
			)
			result = tool.Error("no result available (internal error)")
		}
		entries = append(entries, pausedCallResult{call: tc, index: dispatchIdx, result: result})
		dispatchIdx++
	}
	return entries
}

func (a *Agent) repauseOnPersonRequests(ctx context.Context, state *TaskExecutionState, entries []pausedCallResult, logger *slog.Logger) (paused bool) {
	claimed := make(map[string]bool)
	var waits []*PendingToolCall
	for i := range entries {
		e := &entries[i]
		if !isPersonRequest(e.result) {
			continue
		}
		if refusal := a.refuseUnanswerablePersonRequest(state, e.call.ID, e.call.Name, e.result, claimed); refusal != nil {
			e.result = refusal
			a.recordRefusedPersonRequest(state, e)
			continue
		}
		id, timeout := hilWaitOf(e.result)
		waits = append(waits, &PendingToolCall{
			ToolCallID:    e.call.ID,
			ToolName:      e.call.Name,
			Kind:          PendingCallHILApproval,
			Index:         e.index,
			HILRequestID:  id,
			Timeout:       timeout,
			notYetDurable: true,
		})
	}
	if len(waits) == 0 {
		a.forgetEarlyAnswers(state.TaskID)
		return false
	}

	state.mu.Lock()
	for _, pc := range waits {
		state.PendingCalls[pc.ToolCallID] = pc
		delete(state.SyncResults, pc.Index)
	}
	atomic.StoreInt32(&state.TotalPending, int32(len(waits)))
	state.PausedAt = time.Now()
	state.mu.Unlock()

	saved, lost := a.saveRepausedTask(ctx, state, entries, &waits, logger)
	switch {
	case lost:
		a.forgetEarlyAnswers(state.TaskID)
		a.standDownLostTask(state)
		return true
	case len(waits) == 0:
		a.forgetEarlyAnswers(state.TaskID)
		return false
	}

	state.mu.Lock()
	for _, pc := range waits {
		pc.TimeoutTimer = a.armHILTimeout(state.TaskID, pc)
		pc.notYetDurable = false
	}
	state.mu.Unlock()
	logger.Info("a paused remote tool call asked for a person's answer; waiting for it",
		"waitingCalls", len(waits), "durable", saved)

	if ctx.Err() != nil || state.Cancelled.Load() {
		a.forgetEarlyAnswers(state.TaskID)
		a.cancelPausedTask(state, cancelFromOwningTurn)
		return true
	}
	a.applyEarlyAnswers(state.TaskID, waitedRequestIDs(waits))
	return true
}

func waitedRequestIDs(waits []*PendingToolCall) []string {
	ids := make([]string, len(waits))
	for i, w := range waits {
		ids[i] = w.HILRequestID
	}
	return ids
}

func (a *Agent) recordRefusedPersonRequest(state *TaskExecutionState, e *pausedCallResult) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.SyncResults[e.index] = e.result
	delete(state.PendingCalls, e.call.ID)
}

func (a *Agent) saveRepausedTask(ctx context.Context, state *TaskExecutionState, entries []pausedCallResult, waits *[]*PendingToolCall, logger *slog.Logger) (durable, lost bool) {
	durableBefore := state.checkpointed.Load()
	err := a.saveRefusingTakenWaits(ctx, state, func(refused *PendingToolCall, refusal *tool.Result) (saveAgain bool) {
		for i := range entries {
			if entries[i].call.ID == refused.ToolCallID {
				entries[i].result = refusal
			}
		}
		*waits = slices.DeleteFunc(*waits, func(w *PendingToolCall) bool { return w == refused })
		return len(*waits) > 0 || state.lostItsCheckpoint(durableBefore)
	})
	switch {
	case err == nil:
		return state.checkpointed.Load(), false
	case errors.Is(err, errTaskLost):
		logger.Warn("another process holds this task; standing down at the person's wait")
		return false, true
	default:
		logger.Error("could not write the person's wait to the checkpoint; it will not survive a restart of this process",
			checkpointFailureAttr(err))
		return false, false
	}
}

func checkpointFailureAttr(err error) slog.Attr {
	var taken *session.CorrelationTakenError
	if errors.As(err, &taken) {
		return slog.Int("requestIDBytes", len(taken.CorrID))
	}
	return slog.Any("error", err)
}

func (s *TaskExecutionState) lostItsCheckpoint(durableBefore bool) bool {
	return durableBefore && !s.checkpointed.Load()
}

func (a *Agent) saveRefusingTakenWaits(ctx context.Context, state *TaskExecutionState, onRefused func(refused *PendingToolCall, refusal *tool.Result) (saveAgain bool)) error {
	for {
		err := a.checkpoints.save(ctx, state)
		var taken *session.CorrelationTakenError
		if !errors.As(err, &taken) {
			return err
		}
		refused, refusal := a.refuseTakenWait(state, taken.CorrID)
		if refused == nil {
			return err
		}
		if !onRefused(refused, refusal) {
			return nil
		}
	}
}

func (a *Agent) refuseTakenWait(state *TaskExecutionState, corrID string) (*PendingToolCall, *tool.Result) {
	state.mu.Lock()
	var refused *PendingToolCall
	for _, pc := range state.PendingCalls {
		if pc.Kind == PendingCallHILApproval && pc.Result == nil && pc.HILRequestID == corrID {
			refused = pc
			break
		}
	}
	if refused == nil {
		state.mu.Unlock()
		return nil, nil
	}
	refusal := tool.Error(requestIDInUseMessage)
	state.SyncResults[refused.Index] = refusal
	delete(state.PendingCalls, refused.ToolCallID)
	atomic.AddInt32(&state.TotalPending, -1)
	state.mu.Unlock()
	a.forgetEarlyAnswer(state, corrID)
	a.logger.Warn("refusing a tool's request for a person's answer",
		"traceID", state.UserProps.TraceID, "taskID", state.TaskID, "toolCallID", refused.ToolCallID, "toolName", refused.ToolName,
		"requestIDBytes", len(corrID))
	return refused, refusal
}
