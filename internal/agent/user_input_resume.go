// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func (a *Agent) wireUserInput(toolCtx *tool.Context, sp *StatusPublisher, state *TaskExecutionState, webInboxOnly bool) {
	recipientUserID, traceID, sessionID := toolCtx.UserID, toolCtx.TraceID, toolCtx.SessionID
	request := func(requestID, source string, surface json.RawMessage) hilRequest {
		return hilRequest{
			requestID:       requestID,
			source:          source,
			recipientUserID: recipientUserID,
			traceID:         traceID,
			sessionID:       sessionID,
			surface:         surface,
			webInboxOnly:    webInboxOnly,
		}
	}
	toolCtx.WaitForUserInput = func(ctx context.Context, requestID, source string, surface json.RawMessage, timeout time.Duration) *a2a.UserInputResponseData { //nolint:staticcheck // WaitForUserInput is deprecated but still used as legacy fallback
		return a.waitForUserInput(ctx, sp, request(requestID, source, surface), timeout)
	}
	if a.checkpoints != nil {
		toolCtx.PublishUserInput = func(ctx context.Context, requestID, source string, surface json.RawMessage, timeout time.Duration) {
			a.expectEarlyAnswer(sp.TaskID(), requestID, recipientUserID, timeout)
			a.publishPrompt(ctx, sp, state, request(requestID, source, surface), timeout)
		}
	}
}

func (a *Agent) statusPublisherFor(state *TaskExecutionState) *StatusPublisher {
	return NewStatusPublisher(a.svc.Broker(), StatusPublisherConfig{
		StatusTopic: state.UserProps.A2AStatusTopic,
		TaskID:      state.TaskID,
		ContextID:   state.ContextID,
		AgentName:   a.cfg.Name,
		ClientID:    state.UserProps.ClientID,
		UserID:      state.UserProps.UserID,
		UserConfig:  state.UserProps.A2AUserConfig,
		TraceID:     state.UserProps.TraceID,
		SeqGen:      a.seqGen,
	}, a.logger)
}

func (a *Agent) publishAuthOutcome(ctx context.Context, sp *StatusPublisher, taskID string, outcome *authOutcome) {
	sp.PublishAuthenticationResult(ctx, a2a.AuthenticationResultData{
		Type:                      "authentication_result",
		TargetAgent:               a.cfg.Name,
		TargetAgentTaskID:         taskID,
		Success:                   outcome.success,
		CredentialKey:             outcome.credentialKey,
		AuthRequestFunctionCallID: outcome.functionCallID,
		ToolDisplayName:           outcome.toolDisplayName,
	})
}

func (a *Agent) checkpointedTaskFor(ctx context.Context, corrID string) (*session.PendingCall, *TaskExecutionState, error) {
	storeCtx, cancel := detach(ctx)
	defer cancel()
	pending, err := a.checkpoints.lookup(storeCtx, corrID)
	if err != nil {
		a.logger.Warn("failed to look up a paused call for a response", "corrID", corrID, "error", err)
		return nil, nil, err
	}
	if pending == nil || (pending.AgentName != "" && pending.AgentName != a.cfg.Name) {
		return nil, nil, nil
	}
	cp, err := a.checkpoints.store.GetCheckpoint(storeCtx, pending.TaskID)
	if err != nil {
		a.logger.Warn("failed to read the checkpoint of a task a response is for",
			"corrID", corrID, "taskID", pending.TaskID, "traceID", pending.TraceID, "error", err)
		return nil, nil, err
	}
	if cp == nil {
		return nil, nil, nil
	}
	state, err := unmarshalTaskState(cp.State)
	if err != nil {
		a.logger.Warn("cannot decode the checkpoint of a task a response is for; leaving it for a replica that can",
			"corrID", corrID, "taskID", pending.TaskID, "error", err)
		return nil, nil, nil
	}
	return pending, state, nil
}

func hilResultFor(logger *slog.Logger, pc *PendingToolCall, resp *a2a.UserInputResponseData) *tool.Result {
	switch pc.Kind {
	case PendingCallHILAsk:
		questions, _ := pc.OriginalArgs["questions"].([]any)
		return hilsurface.AskUserQuestionResult(questions, resp, pc.Timeout)
	case PendingCallToolApproval:
		return toolApprovalAnswer(resp, pausedApprovalCallOf(pc))
	}
	return buildApprovalResultFromHILResponse(logger, resp, pc.Timeout)
}

func (a *Agent) relayUserInputResponse(ctx context.Context, resp *a2a.UserInputResponseData, sender hilSender, passOn func()) hilDelivery {
	if a.checkpoints == nil || !usableHILRequestID(resp.RequestID) {
		return hilUnmatched
	}
	pending, state, err := a.checkpointedTaskFor(ctx, resp.RequestID)
	if err != nil {
		return hilUnchecked
	}
	if state == nil {
		return hilUnmatched
	}
	storeCtx, cancel := detach(ctx)
	defer cancel()
	logger := a.logger.With("traceID", state.UserProps.TraceID, "taskID", state.TaskID, "requestID", resp.RequestID)
	if a.pausedTasks.Load(state.TaskID) != nil {
		return hilUnmatched
	}
	pc := pausedCallForRequest(state, resp.RequestID)
	if pc == nil {
		return hilUnmatched
	}
	if !sender.mayAnswer(state.UserProps.UserID) {
		warnControlResponseRefused(logger, a2a.SignalUserInputResponse, sender)
		return hilRefused
	}

	logger.Debug("recording a user's answer for a task paused on another replica")
	result := hilResultFor(logger, pc, resp)
	recorded := recordWithRetries(storeCtx, func() error {
		if a.adoptCheckpointedTask(storeCtx, pending, resp.RequestID, result, logger) {
			return nil
		}
		return errAnswerNotRecorded
	})
	if recorded != nil {
		return hilNotRecorded
	}
	a.republishUserInputResponse(storeCtx, resp, logger)
	if passOn != nil {
		passOn()
	}
	return hilDelivered
}

func (a *Agent) bankAnswer(state *TaskExecutionState, pc *PendingToolCall, result *tool.Result) {
	if a.checkpoints == nil {
		return
	}
	state.mu.Lock()
	state.durableWrites.Add(1)
	state.mu.Unlock()
	defer state.durableWrites.Done()
	corrID, _, _ := pendingCallCorrelation(state.TaskID, pc)
	ctx, cancel := detach(context.Background())
	defer cancel()
	resolvedElsewhere := false
	err := recordWithRetries(ctx, func() error {
		_, err := a.checkpoints.store.RecordToolResult(ctx, state.TaskID, corrID, a.checkpoints.toolCallResult(state.TaskID, pc.ToolCallID, pc.ToolName, result))
		if errors.Is(err, session.ErrCallNotOutstanding) {
			resolvedElsewhere = true
			return nil
		}
		return err
	})
	if err != nil {
		a.logger.Error("could not bank a user's answer; resuming the task with it in hand",
			"taskID", state.TaskID, "requestID", pc.HILRequestID, "traceID", state.UserProps.TraceID, "error", err)
		return
	}
	if resolvedElsewhere {
		a.useRecordedResult(ctx, state, pc)
	}
}

func boundedByDurableWrite(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, bounded := ctx.Deadline(); bounded {
		return context.WithCancel(ctx)
	}
	return detach(ctx)
}

func (a *Agent) useRecordedResult(ctx context.Context, state *TaskExecutionState, pc *PendingToolCall) *tool.Result {
	ctx, cancel := boundedByDurableWrite(ctx)
	defer cancel()
	var recorded *tool.Result
	err := recordWithRetries(ctx, func() error {
		cp, err := a.checkpoints.store.GetCheckpoint(ctx, state.TaskID)
		if err != nil {
			return err
		}
		recorded = recordedAnswerFor(cp, pc.ToolCallID)
		return nil
	})
	logger := a.logger.With("taskID", state.TaskID, "traceID", state.UserProps.TraceID, "toolCallID", pc.ToolCallID)
	if err != nil {
		logger.Warn("a call is no longer outstanding in the store but its recorded result could not be read; resuming with this replica's result", "error", err)
		a.refuseApprovalWithNoRecordedAnswer(state, pc)
		return nil
	}
	if recorded == nil {
		logger.Warn("a call is no longer outstanding in the store and no answer is recorded for it; resuming with this replica's result")
		a.refuseApprovalWithNoRecordedAnswer(state, pc)
		return nil
	}
	state.mu.Lock()
	state.SyncResults[pc.Index] = recorded
	state.mu.Unlock()
	return recorded
}

func (a *Agent) refuseApprovalWithNoRecordedAnswer(state *TaskExecutionState, pc *PendingToolCall) {
	if pc.Kind != PendingCallToolApproval {
		return
	}
	state.mu.Lock()
	state.SyncResults[pc.Index] = tool.Error(approvalAnswerUnreadableMessage)
	state.mu.Unlock()
}

func recordedAnswerFor(cp *session.TaskCheckpoint, toolCallID string) *tool.Result {
	if cp == nil {
		return nil
	}
	for _, recorded := range cp.ToolResults {
		if recorded.ToolCallID != toolCallID {
			continue
		}
		if result := recordedToolResult(recorded); !isPersonRequest(result) {
			return result
		}
		return nil
	}
	return nil
}

func (a *Agent) recordAnswerForTaskHeldElsewhere(resp *a2a.UserInputResponseData, sender hilSender) bool {
	return a.recordAnswerForTaskHeldElsewhereThen(resp, sender, nil)
}

func (a *Agent) recordAnswerForTaskHeldElsewhereThen(resp *a2a.UserInputResponseData, sender hilSender, onSettled func(recorded bool)) bool {
	return a.queueOrphanAdoption(orphanAdoption{
		corrID:     resp.RequestID,
		source:     "user answer",
		kinds:      []string{session.PendingKindHILApproval, session.PendingKindToolApproval},
		userAnswer: true,
		onSettled:  onSettled,
		result: func(pending *session.PendingCall) (*tool.Result, error) {
			return a.checkpointedAnswerResult(pending, resp, sender)
		},
	})
}

func (a *Agent) checkpointedAnswerResult(pending *session.PendingCall, resp *a2a.UserInputResponseData, sender hilSender) (*tool.Result, error) {
	ctx, cancel := detach(context.Background())
	defer cancel()
	cp, err := a.checkpoints.store.GetCheckpoint(ctx, pending.TaskID)
	if err != nil {
		return nil, fmt.Errorf("read the checkpoint: %w", err)
	}
	if cp == nil {
		return nil, nil
	}
	state, err := unmarshalTaskState(cp.State)
	if err != nil {
		return nil, fmt.Errorf("%w: decode the checkpoint: %w", errOrphanNotForThisReplica, err)
	}
	pc := pausedCallForRequest(state, resp.RequestID)
	if pc == nil {
		return nil, fmt.Errorf("%w: the checkpoint has no paused call for request %s", errOrphanNotForThisReplica, resp.RequestID)
	}
	logger := a.logger.With("traceID", pending.TraceID, "taskID", pending.TaskID, "requestID", resp.RequestID)
	if !sender.mayAnswer(state.UserProps.UserID) {
		warnControlResponseRefused(logger, a2a.SignalUserInputResponse, sender)
		return nil, nil
	}
	return hilResultFor(logger, pc, resp), nil
}

func (a *Agent) collectRecordedAnswers(ctx context.Context) {
	for _, state := range a.pausedTasks.Snapshot() {
		if ctx.Err() != nil {
			return
		}
		waiting := a.callsWhoseRowsAreGone(ctx, state)
		if len(waiting) == 0 {
			continue
		}
		cp, err := a.checkpoints.store.GetCheckpoint(ctx, state.TaskID)
		if err != nil {
			a.warnCollectFailed(ctx, state, "", err)
			continue
		}
		if cp == nil {
			continue
		}
		for id, pc := range waiting {
			a.deliverRecordedAnswer(state, pc, recordedAnswerFor(cp, id))
		}
	}
}

func (a *Agent) callsWhoseRowsAreGone(ctx context.Context, state *TaskExecutionState) map[string]*PendingToolCall {
	var gone map[string]*PendingToolCall
	for id, pc := range callsAnsweredElsewhere(state) {
		corrID, _, ok := pendingCallCorrelation(state.TaskID, pc)
		if !ok {
			continue
		}
		row, err := a.checkpoints.lookup(ctx, corrID)
		if err != nil {
			a.warnCollectFailed(ctx, state, corrID, err)
			continue
		}
		if row != nil {
			continue
		}
		if gone == nil {
			gone = make(map[string]*PendingToolCall)
		}
		gone[id] = pc
	}
	return gone
}

func (a *Agent) warnCollectFailed(ctx context.Context, state *TaskExecutionState, corrID string, err error) {
	if ctx.Err() != nil {
		return
	}
	logger := a.logger.With("taskID", state.TaskID, "traceID", state.UserProps.TraceID)
	if corrID != "" {
		logger = logger.With("corrID", corrID)
	}
	logger.Warn("could not check whether another replica recorded an answer for a paused task", "error", err)
}

func callsAnsweredElsewhere(state *TaskExecutionState) map[string]*PendingToolCall {
	state.mu.Lock()
	defer state.mu.Unlock()
	var waiting map[string]*PendingToolCall
	for id, pc := range state.PendingCalls {
		if !pc.Kind.waitsOnPerson() || pc.notYetDurable {
			continue
		}
		if waiting == nil {
			waiting = make(map[string]*PendingToolCall)
		}
		waiting[id] = pc
	}
	return waiting
}

func (a *Agent) deliverRecordedAnswer(state *TaskExecutionState, pc *PendingToolCall, result *tool.Result) bool {
	if result == nil || state.Cancelled.Load() || !state.claimPendingCall(pc.ToolCallID, pc.Index, result) {
		return false
	}
	if pc.TimeoutTimer != nil {
		pc.TimeoutTimer.Stop()
	}
	a.logger.Debug("collected an answer another replica recorded for a task paused here",
		"taskID", state.TaskID, "traceID", state.UserProps.TraceID, "toolCallID", pc.ToolCallID)
	if state.DecrementPending() == 0 {
		a.ResumeTask(state.TaskID)
	}
	return true
}

func pausedCallForRequest(state *TaskExecutionState, requestID string) *PendingToolCall {
	for _, pc := range state.PendingCalls {
		if pc.HILRequestID == requestID {
			return pc
		}
	}
	return nil
}

func (a *Agent) republishUserInputResponse(ctx context.Context, resp *a2a.UserInputResponseData, logger *slog.Logger) {
	payload, err := json.Marshal(resp)
	if err != nil {
		logger.Warn("failed to encode a user's answer to wake its task's holder", "error", err)
		return
	}
	topic := a2a.NotifyResponseTopic(a.cfg.Namespace, a.cfg.Name, resp.RequestID)
	if err := a.svc.Broker().Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		logger.Warn("failed to wake a paused task's holder; it collects the answer on its next maintenance pass", "error", err)
	}
}

func recordedToolResult(recorded session.ToolCallResult) *tool.Result {
	if result := resultFromPayload(recorded.Payload); result != nil {
		return result
	}
	if recorded.IsError {
		return tool.Error(recorded.Content)
	}
	return tool.OK(recorded.Content)
}
