// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type nodeAnswerOutcome int

const (
	nodeAnswerNotHere nodeAnswerOutcome = iota
	nodeAnswerDelivered
	nodeAnswerUnmatched
	nodeAnswerRefused
	nodeAnswerUndelivered
)

func (w *WorkflowInstance) answerForwardTopic() string {
	return a2a.AgentForwardTopic(w.cfg.Namespace, w.cfg.Name)
}

func (w *WorkflowInstance) recordNodeQuestion(statusPayload []byte, subTaskID string) {
	requestID := agent.UserInputRequestIDFromStatus(statusPayload)
	if requestID == "" {
		return
	}
	if exec, _ := w.findExecutionForSubTask(subTaskID); exec != nil {
		exec.Context.recordQuestion(requestID, subTaskID)
	}
}

func (w *WorkflowInstance) residentRun(taskID string) *WorkflowExecution {
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()
	return w.executions[taskID]
}

func (w *WorkflowInstance) routeNodeAnswer(ctx context.Context, taskID string, resp *a2a.UserInputResponseData, props a2a.UserProperties, msg *broker.Message, logger *slog.Logger) error {
	switch outcome, err := w.answerNodeQuestion(ctx, taskID, resp, props, logger); outcome {
	case nodeAnswerUndelivered:
		return err
	case nodeAnswerNotHere:
		return w.passAnswerOn(ctx, resp, msg, logger)
	default:
		return nil
	}
}

func (w *WorkflowInstance) passAnswerOn(ctx context.Context, resp *a2a.UserInputResponseData, msg *broker.Message, logger *slog.Logger) error {
	if err := w.svc.Broker().Publish(ctx, w.answerForwardTopic(), &broker.Message{
		Payload:        msg.Payload,
		UserProperties: msg.UserProperties,
	}); err != nil {
		return fmt.Errorf("pass the answer on to the AWE holding its run: %w", err)
	}
	logger.Debug("passed a user_input_response on to the AWE holding its run", "requestID", resp.RequestID)
	return nil
}

func reportUndeliveredAnswer(ctx context.Context, redelivered bool, requestID string, err error, logger *slog.Logger) {
	switch {
	case ctx.Err() != nil:
		logger.Debug("user_input_response not delivered while the instance stops; handing it back to the broker", "requestID", requestID, "error", err)
	case redelivered:
		logger.Error("gave up on a user_input_response after its redelivery; the node that asked waits until its question times out", "requestID", requestID, "error", err)
	default:
		logger.Warn("could not deliver a user_input_response; handing it back to the broker for one more attempt", "requestID", requestID, "error", err)
	}
}

func (w *WorkflowInstance) handlePassedOnAnswer(ctx context.Context, msg *broker.Message) {
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		w.logger.Warn("dropping a passed-on message that is not a JSON-RPC request", "error", err)
		return
	}
	props := a2a.DecodeUserProperties(msg.UserProperties)
	logger := w.logger.With("traceID", props.TraceID, "taskID", req.ID)
	if w.residentRun(req.ID) == nil {
		logger.Debug("passed-on user_input_response is for a run this AWE does not hold")
		return
	}
	message, err := extractTaskMessage(req.Params)
	if err != nil {
		logger.Warn("dropping a passed-on request without a readable message", "error", err)
		return
	}
	resp := agent.UserInputResponseFromMessage(message, logger)
	if resp == nil {
		logger.Warn("dropping a passed-on request that carries no user_input_response")
		return
	}
	if _, refused := w.authenticateRequest(ctx, props, req.ID); refused != nil {
		return
	}
	if outcome, err := w.answerNodeQuestion(ctx, req.ID, resp, props, logger); outcome == nodeAnswerUndelivered {
		logger.Error("lost a passed-on user_input_response; the node that asked waits until its question times out", "requestID", resp.RequestID, "error", err)
	}
}

func extractTaskMessage(params any) (a2a.Message, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return a2a.Message{}, err
	}
	var decoded struct {
		Message a2a.Message `json:"message"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return a2a.Message{}, err
	}
	return decoded.Message, nil
}

func (w *WorkflowInstance) answerNodeQuestion(ctx context.Context, taskID string, resp *a2a.UserInputResponseData, props a2a.UserProperties, logger *slog.Logger) (nodeAnswerOutcome, error) {
	exec := w.residentRun(taskID)
	if exec == nil {
		return nodeAnswerNotHere, nil
	}
	logger = logger.With("requestID", resp.RequestID)
	subTaskID, ok := exec.Context.askerOf(resp.RequestID)
	if !ok {
		logger.Warn("received user_input_response with no matching pending request", "responseStatus", resp.Status)
		return nodeAnswerUnmatched, nil
	}
	if !artifact.SameOwner(exec.Context.UserProps.UserID, props.UserID) {
		logger.Warn("control response refused: sender does not own the paused task",
			"responseType", string(a2a.SignalUserInputResponse), "userID", props.UserID, "subTaskID", subTaskID)
		return nodeAnswerRefused, nil
	}
	target, ok := exec.State.TargetForSubTask(subTaskID)
	if !ok {
		logger.Warn("received user_input_response for a sub-task with no recorded target", "subTaskID", subTaskID)
		return nodeAnswerUnmatched, nil
	}
	nodeID, _ := exec.State.NodeForSubTask(subTaskID)

	payload, err := json.Marshal(a2a.NewRequest(subTaskID, "message/send", map[string]any{
		"message": a2a.Message{
			Kind:      "message",
			MessageID: idgen.New(),
			Role:      a2a.RoleUser,
			Parts:     []a2a.Part{a2a.NewDataPart(resp)},
			TaskID:    subTaskID,
		},
	}))
	if err != nil {
		return nodeAnswerUndelivered, fmt.Errorf("encode the answer for node %s (sub-task %s): %w", nodeID, subTaskID, err)
	}
	if err := w.svc.Broker().Publish(ctx, a2a.AgentRequestTopic(w.cfg.Namespace, target), &broker.Message{
		Payload:        payload,
		UserProperties: w.nodeDelegationAuthProps(exec.Context.UserProps),
	}); err != nil {
		return nodeAnswerUndelivered, fmt.Errorf("publish the answer to node %s (agent %s, sub-task %s): %w", nodeID, target, subTaskID, err)
	}
	exec.Context.forgetQuestion(resp.RequestID)
	logger.Info("forwarded user_input_response down to node",
		"node", nodeID, "subTaskID", subTaskID, "nodeAgent", target, "responseStatus", resp.Status)
	return nodeAnswerDelivered, nil
}

func (w *WorkflowInstance) nodeDelegationAuthProps(run a2a.UserProperties) map[string]string {
	props := map[string]string{"delegating_agent_name": w.cfg.Name}
	for key, value := range map[string]string{
		"clientId":      run.ClientID,
		"userId":        run.UserID,
		"a2aUserConfig": run.A2AUserConfig,
		"authToken":     run.AuthToken,
	} {
		if value != "" {
			props[key] = value
		}
	}
	a2a.AddTraceID(props, run.TraceID)
	return props
}
