// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func (a *Agent) forwardTopic() string {
	return a2a.AgentForwardTopic(a.cfg.Namespace, a.cfg.Name)
}

func (a *Agent) forwardControlMessage(ctx context.Context, taskID string, msg a2a.Message, userProps a2a.UserProperties) {
	if a.svc == nil || a.svc.Broker() == nil {
		return
	}
	brk := a.svc.Broker()
	payload, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{"message": msg}))
	if err != nil {
		a.logger.Warn("could not encode a control message to pass on", "taskID", taskID, "error", err)
		return
	}
	if err := brk.Publish(ctx, a.forwardTopic(), &broker.Message{
		Payload:        payload,
		UserProperties: a2a.EncodeUserProperties(&userProps),
	}); err != nil {
		a.logger.Warn("could not pass a control message on to the AWE holding its task", "taskID", taskID, "traceID", userProps.TraceID, "error", err)
	}
}

func (a *Agent) forwardLoginAnswer(ctx context.Context, resp *a2a.AuthResponseData, userProps a2a.UserProperties) {
	spent := *resp
	spent.Code, spent.CodeVerifier, spent.State, spent.AuthResponseURI = "", "", "", ""
	raw, err := json.Marshal(spent)
	if err != nil {
		return
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}
	msg := a2a.Message{Kind: "message", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewDataPart(data)}}
	a.forwardControlMessage(ctx, resp.TargetAgentTaskID, msg, userProps)
}

func (a *Agent) handleForwardedControl(ctx context.Context, msg *broker.Message) {
	msg.Ack()
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return
	}
	a2aMsg, err := extractA2AMessage(req.Params)
	if err != nil {
		return
	}
	userProps := a2a.DecodeUserProperties(msg.UserProperties)
	logger := a.logger.With("traceID", userProps.TraceID, "taskID", req.ID)
	if _, refused := a.authenticateAndAuthorize(ctx, req.ID, userProps, logger); refused != nil {
		logger.Warn("refused a passed-on control message that did not authenticate")
		return
	}
	if resp := a.extractUserInputResponse(a2aMsg); resp != nil {
		sender := hilSenderUser(userProps.UserID)
		switch a.routeUserInputResponse(ctx, resp, sender) {
		case hilUnmatched:
			logger.Debug("passed-on answer is for a task this AWE does not hold", hilRequestIDAttr(resp.RequestID))
		case hilNotRecorded, hilUnchecked:
			a.requeuePassedOnAnswer(resp, sender, logger)
		}
		return
	}
	if resp := a.extractAuthResponse(a2aMsg); resp != nil {
		a.applyForwardedLogin(ctx, resp, userProps)
	}
}

func (a *Agent) requeuePassedOnAnswer(resp *a2a.UserInputResponseData, sender hilSender, logger *slog.Logger) {
	if a.recordAnswerForTaskHeldElsewhere(resp, sender) {
		logger.Warn("could not record a passed-on answer; queued it for retry on the adoption workers", "requestID", resp.RequestID)
		return
	}
	logger.Error("could not record a passed-on answer and could not queue it for retry; the task's holder may still apply it, otherwise the task waits out its input timeout", "requestID", resp.RequestID)
}

func (a *Agent) applyForwardedLogin(ctx context.Context, resp *a2a.AuthResponseData, userProps a2a.UserProperties) {
	state := a.pausedTasks.Load(resp.TargetAgentTaskID)
	if state == nil {
		return
	}
	if sender := hilSenderUser(userProps.UserID); !sender.mayAnswer(state.UserProps.UserID) {
		warnControlResponseRefused(a.logger.With("taskID", state.TaskID), a2a.SignalAuthResponse, sender)
		return
	}
	state.authMu.Lock()
	authMuUnlocked := false
	defer func() {
		if !authMuUnlocked {
			state.authMu.Unlock()
		}
	}()
	state.mu.Lock()
	pc, ok := state.PendingCalls[resp.FunctionCallID]
	if !ok || pc.Kind != PendingCallAuth {
		state.mu.Unlock()
		return
	}
	if pc.TimeoutTimer != nil {
		pc.TimeoutTimer.Stop()
	}
	delete(state.PendingCalls, pc.ToolCallID)
	claimed := *pc
	state.mu.Unlock()
	a.logger.Info("running a login-gated call signed in on another AWE", "taskID", state.TaskID, "toolCallID", claimed.ToolCallID)
	a.completeLoginCall(ctx, state, &claimed, resp, userProps, nil, &authMuUnlocked)
}
