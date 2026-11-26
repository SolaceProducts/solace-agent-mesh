// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	toolApprovalArgsKey      = "tool_approval_args"
	toolApprovalStatusKey    = "tool_approval_status"
	toolApprovalDataKey      = "tool_approval_data"
	toolApprovalRequestIDKey = "tool_approval_request_id"
)

const approvalAnswerUnreadableMessage = "The approval answer could not be read; the call did not run."

func (a *Agent) pausesToolApprovals(tCtx *tool.Context) bool {
	return a.checkpoints != nil && tCtx.PauseResume
}

func (a *Agent) pauseForToolApproval(ctx context.Context, sp *StatusPublisher, toolName string, toolArgs map[string]any, hilCfg HILToolConfig, tCtx *tool.Context) (*tool.Result, tool.ApprovalRefusal) {
	hr, timeout, err := a.toolApprovalRequest(toolName, toolArgs, hilCfg, tCtx)
	if err != nil {
		a.logger.Error("failed to generate approval surface", "tool", toolName, "error", err)
		return tool.Error("Internal error generating approval prompt."), tool.RefusalPromptFailed
	}
	a.expectEarlyAnswer(sp.TaskID(), hr.requestID, hr.recipientUserID, timeout)
	a.publishPrompt(ctx, sp, nil, hr, timeout)
	return tool.Pending("Waiting for approval", tool.WithData(map[string]any{
		"pending_kind":      session.PendingKindToolApproval,
		"hil_request_id":    hr.requestID,
		"hil_timeout_ns":    float64(timeout),
		toolApprovalArgsKey: toolArgs,
	}), tool.WithTrustedPending(tool.TrustedPendingToolApproval)), ""
}

type pausedApprovalCall struct {
	requestID string
	args      map[string]any
	timeout   time.Duration
}

func pausedApprovalCallOf(pc *PendingToolCall) pausedApprovalCall {
	return pausedApprovalCall{requestID: pc.HILRequestID, args: pc.OriginalArgs, timeout: pc.Timeout}
}

func toolApprovalAnswer(resp *a2a.UserInputResponseData, call pausedApprovalCall) *tool.Result {
	return tool.OK("The approval for this call was answered; the call has not run yet.", tool.WithData(map[string]any{
		toolApprovalStatusKey:    resp.Status,
		toolApprovalDataKey:      resp.Data,
		toolApprovalRequestIDKey: call.requestID,
	}))
}

func pausedApprovalOf(result *tool.Result, call pausedApprovalCall) (*a2a.UserInputResponseData, bool) {
	if result == nil {
		return nil, false
	}
	status, ok := result.Data[toolApprovalStatusKey].(string)
	if !ok {
		return nil, false
	}
	data, _ := result.Data[toolApprovalDataKey].(map[string]any)
	return &a2a.UserInputResponseData{Type: string(a2a.SignalUserInputResponse), RequestID: call.requestID, Status: status, Data: data}, true
}

type pausedToolCall struct {
	toolCall llm.ToolCall
	approval pausedApprovalCall
}

func (a *Agent) settlePausedApproval(ctx context.Context, state *TaskExecutionState, sp *StatusPublisher, toolCtx *tool.Context, held pausedToolCall, answer *tool.Result, logger *slog.Logger) *tool.Result {
	tc, call := held.toolCall, held.approval
	resp, ok := pausedApprovalOf(answer, call)
	if !ok {
		logger.Error("the stored answer to a paused tool approval cannot be read; the call was not run",
			"taskID", state.TaskID, "toolCallID", tc.ID, "tool", tc.Name, "requestID", call.requestID)
		a.auditApprovalRefusal(ctx, toolCtx, tc.Name, tool.RefusalUnreadable)
		return tool.Error(approvalAnswerUnreadableMessage)
	}
	return a.settleToolApproval(ctx, state, sp, toolCtx, held, resp, logger)
}

func (a *Agent) settleToolApproval(ctx context.Context, state *TaskExecutionState, sp *StatusPublisher, toolCtx *tool.Context, held pausedToolCall, resp *a2a.UserInputResponseData, logger *slog.Logger) *tool.Result {
	tc, call := held.toolCall, held.approval
	callCtx := *toolCtx
	callCtx.PrepareCall(tc.ID, tc.Name)
	logger = logger.With("toolCallID", tc.ID, "tool", tc.Name)
	refused, refusal := toolApprovalOutcome(resp, call.timeout)
	if refused == nil {
		refused, refusal = a.refusalToRunApprovedCall(ctx, state, logger)
	}
	if refused != nil {
		a.auditApprovalRefusal(ctx, &callCtx, tc.Name, refusal)
		logger.Debug("a paused tool approval ended without running its call", "refusal", refusal)
		return refused
	}
	if a.tools.GetTool(tc.Name) == nil {
		logger.Warn("an approved call was not run: the agent no longer has the tool")
		a.auditApprovalRefusal(ctx, &callCtx, tc.Name, tool.RefusalUnexpected)
		return tool.Error(fmt.Sprintf("This call was approved, but %q was removed from this agent's tools while the approval was waiting, so it was not run.", tc.Name))
	}
	if failed := a.prepareToolCall(ctx, sp, tc.Name, &callCtx); failed != nil {
		a.auditApprovalRefusal(ctx, &callCtx, tc.Name, tool.RefusalUnexpected)
		return failed
	}
	args, err := json.Marshal(call.args)
	if err != nil {
		logger.Error("cannot encode the arguments of an approved call", "error", err)
		a.auditApprovalRefusal(ctx, &callCtx, tc.Name, tool.RefusalUnreadable)
		return tool.Error("The call was approved, but its arguments could not be read back; it was not run.")
	}
	callCtx.ApprovalReason = tool.ApprovalHuman
	callCtx.PauseResume = false
	callCtx.PauseRemoteTools = false
	logger.Debug("running a call approved while its task was paused")
	result := a.executeToolCall(ctx, tool.Call{ID: tc.ID, Name: tc.Name, Arguments: string(args)}, &callCtx)
	if result != nil && result.Status == tool.StatusPending {
		return a.unfinishedApprovedCall(callCtx, result)
	}
	return result
}

func (a *Agent) refusalToRunApprovedCall(ctx context.Context, state *TaskExecutionState, logger *slog.Logger) (*tool.Result, tool.ApprovalRefusal) {
	if state.Cancelled.Load() || ctx.Err() != nil {
		return tool.Error("This task was cancelled or finished elsewhere; the approved call was not run."), tool.RefusalCancelled
	}
	if a.checkpoints == nil || !state.checkpointed.Load() {
		return nil, ""
	}
	storeCtx, cancel := detach(ctx)
	defer cancel()
	owner, exists, err := a.checkpoints.store.CheckpointOwner(storeCtx, state.TaskID)
	if err != nil {
		logger.Warn("could not confirm this replica still holds the task; not running the approved call", "error", err)
		return tool.Error("The call was approved, but this replica could not confirm it still holds the task, so the call was not run."), tool.RefusalCancelled
	}
	if !exists || (owner != "" && owner != a.checkpoints.ownerID) {
		logger.Debug("not running an approved call: the task was cancelled, finished or taken on another replica")
		return tool.Error("This task was cancelled or finished elsewhere; the approved call was not run."), tool.RefusalCancelled
	}
	return nil, ""
}

func (a *Agent) unfinishedApprovedCall(callCtx tool.Context, pending *tool.Result) *tool.Result {
	if toolauth.IsPendingAuthResult(pending) {
		a.endAuthFlow(callCtx.UserID, callCtx.TraceID, toolauth.PendingAuthCredentialKey(pending))
		return authRetryExhaustedError(pending)
	}
	return tool.Error("The call was approved but did not finish; try it again.")
}

func pausedApprovalToolNames(state *TaskExecutionState) []string {
	var names []string
	for _, tc := range state.PausedAssistantMsg.ToolCalls {
		if _, paused := state.PausedApprovals[tc.ID]; paused {
			names = append(names, tc.Name)
		}
	}
	return names
}

func (a *Agent) endCancelledApprovals(state *TaskExecutionState, toolNames []string) {
	if len(toolNames) == 0 {
		return
	}
	ctx, cancel := detach(context.Background())
	defer cancel()
	caller := &tool.Context{Identity: state.Identity, UserID: state.UserProps.UserID, SessionID: state.OriginalSessionID}
	for _, toolName := range toolNames {
		a.auditApprovalRefusal(ctx, caller, toolName, tool.RefusalCancelled)
	}
}

func pendingApprovalExpiry(pending *session.PendingCall) *tool.Result {
	return toolApprovalAnswer(&a2a.UserInputResponseData{RequestID: pending.CorrID, Status: "timed_out"},
		pausedApprovalCall{requestID: pending.CorrID})
}
