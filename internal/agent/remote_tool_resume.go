// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func (a *Agent) installRemoteToolOrphanHandlers() {
	seen := make(map[*remote.Executor]bool)
	for _, t := range a.tools.ListTools() {
		rt, ok := t.(*remote.Tool)
		if !ok || rt.Executor() == nil || seen[rt.Executor()] {
			continue
		}
		seen[rt.Executor()] = true
		rt.Executor().SetOrphanReplyHandler(a.resumeOrphanRemoteToolReply)
	}
}

func (a *Agent) resumeOrphanRemoteToolReply(corrID string, payload []byte) bool {
	return a.queueOrphanAdoption(a.remoteToolReplyAdoption(corrID, payload))
}

func (a *Agent) remoteToolReplyAdoption(corrID string, payload []byte) orphanAdoption {
	reply := bytes.Clone(payload)
	return orphanAdoption{
		corrID: corrID,
		source: "remote tool reply",
		kinds:  []string{session.PendingKindRemoteTool},
		result: func(pending *session.PendingCall) (*tool.Result, error) {
			return a.remoteToolReplyResult(pending, reply), nil
		},
	}
}

func (a *Agent) remoteToolReplyResult(pending *session.PendingCall, reply []byte) *tool.Result {
	rt, ok := a.tools.GetTool(pending.ToolName).(*remote.Tool)
	if !ok {
		a.logger.Warn("not recording a remote tool's reply: its tool is not registered on this replica, so another replica may adopt it",
			"traceID", pending.TraceID, "taskID", pending.TaskID, "corrID", pending.CorrID, "tool", pending.ToolName)
		return nil
	}
	if rt.ReplyIsRetryable(reply) {
		a.logger.Debug("leaving a retryable remote tool failure unrecorded: the replica holding the task retries it, and the reaper resolves the call if that replica is gone",
			"traceID", pending.TraceID, "taskID", pending.TaskID, "corrID", pending.CorrID, "tool", pending.ToolName)
		return nil
	}
	return rt.ResultFromReply(reply)
}

func (a *Agent) reportAdoptedRemoteToolCall(ctx context.Context, pending *session.PendingCall, result *tool.Result) {
	a.logger.Debug("recorded a remote tool's reply for a task paused on another replica; result signals, artifact notification and retry were skipped",
		"traceID", pending.TraceID, "taskID", pending.TaskID, "corrID", pending.CorrID, "tool", pending.ToolName)
	caller, sessionID := a.callerOfPausedTask(ctx, pending)
	a.reportDeliveredRemoteToolCall(ctx, pending.ToolName, caller, sessionID, pending.TraceID, pending.CreatedAt, result)
}

func (a *Agent) callerOfPausedTask(ctx context.Context, pending *session.PendingCall) (auth.CallerIdentity, string) {
	cp, err := a.checkpoints.store.GetCheckpoint(ctx, pending.TaskID)
	if err != nil || cp == nil {
		a.warnAdoptedCallUnattributed(pending, err)
		return auth.CallerIdentity{}, ""
	}
	state, err := unmarshalTaskState(cp.State)
	if err != nil {
		a.warnAdoptedCallUnattributed(pending, err)
		return auth.CallerIdentity{Sub: cp.UserID}, cp.SessionID
	}
	caller := tool.Context{Identity: state.Identity, UserID: state.UserProps.UserID}
	return caller.Caller(), state.OriginalSessionID
}

func (a *Agent) warnAdoptedCallUnattributed(pending *session.PendingCall, cause error) {
	logger := a.logger.With("traceID", pending.TraceID, "taskID", pending.TaskID, "corrID", pending.CorrID, "tool", pending.ToolName)
	if cause == nil {
		logger.Warn("the paused task of an adopted remote tool call is gone; its audit event carries no caller")
		return
	}
	logger.Warn("could not read the paused task to attribute an adopted remote tool call; its audit event carries no caller", "error", cause)
}

const remoteCallEndedBeforeReplyMessage = "the remote tool call ended before its reply was delivered"

func (a *Agent) remoteCallUndeliveredReporter(ctx context.Context, toolName string, toolCtx *tool.Context, dispatchedAt time.Time) func() {
	caller, sessionID, traceID := toolCtx.Caller(), toolCtx.SessionID, toolCtx.TraceID
	return func() {
		a.reportDeliveredRemoteToolCall(ctx, toolName, caller, sessionID, traceID, dispatchedAt, tool.Error(remoteCallEndedBeforeReplyMessage))
	}
}

func (a *Agent) cancelRemoteToolCall(toolName, corrID string) {
	rt, ok := a.tools.GetTool(toolName).(*remote.Tool)
	if !ok {
		a.logger.Warn("cannot cancel a paused remote tool call: its tool is not registered here",
			"tool", toolName, "corrID", corrID)
		return
	}
	rt.CancelCall(corrID)
}

func openRemoteCalls(state *TaskExecutionState) []*PendingToolCall {
	var open []*PendingToolCall
	for _, pc := range state.PendingCalls {
		if pc.Kind == PendingCallRemoteTool && pc.SubTaskID != "" && pc.Result == nil {
			open = append(open, pc)
		}
	}
	return open
}

func (a *Agent) remoteCallDispatchTimes(ctx context.Context, state *TaskExecutionState) map[string]time.Time {
	dispatchedAt := make(map[string]time.Time)
	for _, pc := range openRemoteCalls(state) {
		if row, err := a.checkpoints.lookup(ctx, pc.SubTaskID); err == nil && row != nil {
			dispatchedAt[pc.SubTaskID] = row.CreatedAt
		}
	}
	return dispatchedAt
}

func (a *Agent) endCancelledWaitsOfStoredTask(state *TaskExecutionState, dispatchedAt map[string]time.Time) {
	ctx, cancel := detach(context.Background())
	defer cancel()
	caller := tool.Context{Identity: state.Identity, UserID: state.UserProps.UserID}
	for _, pc := range openRemoteCalls(state) {
		a.cancelRemoteToolCall(pc.ToolName, pc.SubTaskID)
		startedAt, known := dispatchedAt[pc.SubTaskID]
		if !known {
			startedAt = time.Now()
		}
		a.reportDeliveredRemoteToolCall(ctx, pc.ToolName, caller.Caller(), state.OriginalSessionID, state.UserProps.TraceID, startedAt,
			tool.Error(remoteCallEndedBeforeReplyMessage))
	}
	a.endCancelledApprovals(state, pausedApprovalToolNames(state))
}
