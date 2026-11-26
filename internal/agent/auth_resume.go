// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const authAnsweredKey = "auth_answered"

func authAnsweredResult() *tool.Result {
	return tool.Error("The user signed in for this tool, but the process running the task stopped before it ran the tool. Call the tool again with the same arguments.",
		tool.WithData(map[string]any{authAnsweredKey: true}))
}

func isAuthAnswered(r *tool.Result) bool {
	if r == nil || r.Data == nil {
		return false
	}
	answered, _ := r.Data[authAnsweredKey].(bool)
	return answered
}

func (a *Agent) claimLoginAnswer(ctx context.Context, taskID, functionCallID, toolName string) (claimed bool, err error) {
	ctx, cancel := detach(ctx)
	defer cancel()
	result := authAnsweredResult()
	_, err = a.checkpoints.store.RecordToolResult(ctx, taskID, authCorrelationID(taskID, functionCallID), &session.ToolCallResult{
		ToolCallID: functionCallID,
		ToolName:   toolName,
		Content:    result.Message,
		IsError:    true,
		Payload:    a.checkpoints.marshalResult(taskID, result),
	})
	if errors.Is(err, session.ErrCallNotOutstanding) {
		return false, nil
	}
	return err == nil, err
}

func (a *Agent) loginRowFor(ctx context.Context, resp *a2a.AuthResponseData) (*session.PendingCall, *session.TaskCheckpoint, error) {
	ctx, cancel := detach(ctx)
	defer cancel()
	pending, err := a.checkpoints.lookup(ctx, authCorrelationID(resp.TargetAgentTaskID, resp.FunctionCallID))
	if err != nil || pending == nil || pending.TaskID != resp.TargetAgentTaskID {
		return nil, nil, err
	}
	cp, err := a.checkpoints.store.GetCheckpoint(ctx, pending.TaskID)
	if err != nil || cp == nil {
		return nil, nil, err
	}
	return pending, cp, nil
}

func (a *Agent) answerLoginForAbsentTask(ctx context.Context, resp *a2a.AuthResponseData, userProps a2a.UserProperties, exchange func() error) (handled bool) {
	if a.checkpoints == nil {
		return false
	}
	logger := a.logger.With("taskID", resp.TargetAgentTaskID, "traceID", userProps.TraceID, "functionCallID", resp.FunctionCallID)
	pending, cp, err := a.loginRowFor(ctx, resp)
	if err != nil {
		logger.Warn("could not read the pending login of a task held elsewhere; saving the credential only", "error", err)
		return false
	}
	if pending == nil {
		if a.loginAlreadyClaimed(ctx, resp) {
			logger.Info("login answer already claimed by another process; not exchanging the code again")
			return true
		}
		return false
	}
	if sender := hilSenderUser(userProps.UserID); !sender.mayAnswer(cp.UserID) {
		warnControlResponseRefused(logger, a2a.SignalAuthResponse, sender)
		return true
	}
	claimed, err := a.claimLoginAnswer(ctx, pending.TaskID, resp.FunctionCallID, pending.ToolName)
	if err != nil {
		logger.Warn("could not claim the login answer of a task held elsewhere; saving the credential only", "error", err)
		return false
	}
	if !claimed {
		logger.Info("login answer already claimed by another process; not exchanging the code again")
		return true
	}
	if exchangeErr := exchange(); exchangeErr != nil {
		logger.Warn("code exchange failed for a task held elsewhere; the process that resumes it asks the user to sign in again",
			"oauthErrorCode", toolauth.ClassifyOAuth2Error(exchangeErr))
	}
	a.forwardLoginAnswer(ctx, resp, userProps)
	a.adoptIfResolved(ctx, pending.TaskID, logger)
	return true
}

func (a *Agent) retryLoginsOnAdoption(ctx context.Context, state *TaskExecutionState, stored map[string]bool, logger *slog.Logger) (repaused bool) {
	state.authMu.Lock()
	defer state.authMu.Unlock()
	state.mu.Lock()
	var calls []PendingToolCall
	for _, pc := range state.PendingCalls {
		if pc.Kind != PendingCallAuth || (pc.Result != nil && !isAuthAnswered(pc.Result)) {
			continue
		}
		delete(state.PendingCalls, pc.ToolCallID)
		if stored[pc.ToolCallID] {
			continue
		}
		calls = append(calls, *pc)
	}
	state.mu.Unlock()

	for i := range calls {
		pc := calls[i]
		pc.Result = nil
		result, dispatchErr := a.dispatchToolSingle(ctx, state, &pc)
		if dispatchErr != nil {
			result = tool.Error("auth retry failed: " + dispatchErr.Error())
		}
		authFailed := dispatchErr != nil
		if toolauth.IsPendingAuthResult(result) {
			if a.rearmPendingAuth(state, &pc) {
				repaused = true
				continue
			}
			result = authRetryExhaustedError(result)
			authFailed = true
		}
		state.mu.Lock()
		state.SyncResults[pc.Index] = result
		state.setPendingAuthOutcome(&authOutcome{
			success:         !authFailed,
			credentialKey:   pc.CredentialKey,
			functionCallID:  pc.ToolCallID,
			toolDisplayName: pc.ToolName,
		})
		state.mu.Unlock()
		logger.Info("ran a login-gated tool call of an adopted task", "toolCallID", pc.ToolCallID, "toolName", pc.ToolName, "resultStatus", result.Status)
	}

	state.mu.Lock()
	state.TotalPending = int32(len(state.PendingCalls))
	pausedAgain := state.TotalPending > 0
	state.mu.Unlock()
	return pausedAgain || repaused
}

func (a *Agent) claimHeldLoginAnswer(ctx context.Context, state *TaskExecutionState, resp *a2a.AuthResponseData) (claimedElsewhere bool) {
	state.mu.Lock()
	pc, ok := state.PendingCalls[resp.FunctionCallID]
	isLeader := ok && pc.Kind == PendingCallAuth && !pc.IsAuthFollower
	toolName := ""
	if ok {
		toolName = pc.ToolName
	}
	state.mu.Unlock()
	if !isLeader {
		return false
	}
	claimed, err := a.claimLoginAnswer(ctx, state.TaskID, resp.FunctionCallID, toolName)
	if err != nil {
		a.logger.Warn("could not record the login answer on the checkpoint; a takeover would ask the user to sign in again",
			"taskID", state.TaskID, "traceID", state.UserProps.TraceID, "error", err)
		return false
	}
	return !claimed
}

type promptAnswerChecker interface {
	AnswersPendingPrompt(ctx context.Context, resp *a2a.AuthResponseData, toolCtx *tool.Context) (matches, known bool)
}

func answersPendingPrompt(ctx context.Context, handler tool.AuthResponseHandler, resp *a2a.AuthResponseData, toolCtx *tool.Context) (matches, known bool) {
	checker, ok := handler.(promptAnswerChecker)
	if !ok {
		return false, false
	}
	return checker.AnswersPendingPrompt(ctx, resp, toolCtx)
}

func signedInElsewhere(ctx context.Context, claimedElsewhere, answersPrompt bool, handler tool.AuthResponseHandler, toolCtx *tool.Context) bool {
	if !claimedElsewhere {
		return false
	}
	if answersPrompt {
		return true
	}
	checker, ok := handler.(tool.StoredCredentialChecker)
	return ok && checker.CheckStoredCredential(ctx, toolCtx).Connected
}

func (a *Agent) loginAlreadyClaimed(ctx context.Context, resp *a2a.AuthResponseData) bool {
	ctx, cancel := detach(ctx)
	defer cancel()
	cp, err := a.checkpoints.store.GetCheckpoint(ctx, resp.TargetAgentTaskID)
	if err != nil || cp == nil {
		return false
	}
	for _, r := range cp.ToolResults {
		if r.ToolCallID == resp.FunctionCallID && isAuthAnsweredPayload(r) {
			return true
		}
	}
	return false
}
