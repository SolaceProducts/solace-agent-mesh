// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
	"gopkg.in/yaml.v3"
)

type simplifiedResponse struct {
	Text             string      `json:"text"`
	Files            []fileEntry `json:"files"`
	Data             []any       `json:"data"`
	StructuredResult any         `json:"structured_result"`
	StructuredOutput any         `json:"structured_output"`
	A2ATaskResponse  any         `json:"a2a_task_response"`
}

type fileEntry struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Content  string `json:"content,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (a *EventMeshAdapter) handleFinalResponse(ctx context.Context, taskID string, task *a2a.Task) {
	pt, ok, readErr := a.takePending(ctx, taskID)
	if !ok {
		if readErr != nil {
			a.logger.Error("response not published: the task's routing could not be read", "taskID", taskID, "error", readErr)
			keepEvent(ctx)
		}
		return
	}

	handler := pt.handlerCfg
	resp := buildSimplifiedResponse(task)

	if failure := a.resolveResult(ctx, taskID, task, pt, resp); failure != "" {
		if errorHandler, found := a.errorOutputHandler(handler); found {
			if err := a.publishAndSettle(ctx, taskID, pt, errorHandler, errorOnlyResponse(taskFailedErrorCode, failure), handler.OnError); err != nil {
				a.logger.Error("failed to publish error response", "taskID", taskID, "handlerName", handler.OnError, "error", err)
			}
			return
		}
		resp.Text = failure
	}

	if handler.OnSuccess == "" {
		a.logger.Debug("no on_success handler configured — response discarded", "taskID", taskID)
		a.recordOutcome(ctx, taskID, pt, store.InboundOutcomeAck)
		return
	}

	outputHandler, ok := a.cfg.OutputHandlers[handler.OnSuccess]
	if !ok {
		a.logger.Error("output handler not found", "handlerName", handler.OnSuccess)
		a.recordOutcome(ctx, taskID, pt, store.InboundOutcomeReject)
		return
	}

	if err := a.publishAndSettle(ctx, taskID, pt, outputHandler, resp, handler.OnSuccess); err != nil {
		a.logger.Error("failed to publish response", "taskID", taskID, "handlerName", handler.OnSuccess, "error", err)
	}
}

func targetsWorkflow(handler *EventHandlerConfig) bool {
	return handler.TargetWorkflowName != "" || handler.TargetWorkflowNameExpr != ""
}

func (a *EventMeshAdapter) publishAndSettle(ctx context.Context, taskID string, pt *pendingTask, outputHandler *OutputHandlerConfig, resp *simplifiedResponse, handlerName string) error {
	if err := a.publishResponse(ctx, a.logger.With("taskID", taskID), outputHandler, resp, pt.forwardContext); err != nil {
		a.recordOutcome(ctx, taskID, pt, store.InboundOutcomeReject)
		return err
	}
	a.recordOutcome(ctx, taskID, pt, store.InboundOutcomeAck)
	a.logger.Debug("response published", "taskID", taskID, "handlerName", handlerName)
	return nil
}

func (a *EventMeshAdapter) handleErrorResponse(ctx context.Context, taskID string, a2aErr *a2a.Error) {
	pt, ok, readErr := a.takePending(ctx, taskID)
	if !ok {
		if readErr != nil {
			a.logger.Error("error response not published: the task's routing could not be read",
				"taskID", taskID, "errorCode", a2aErr.Code, "error", readErr)
			keepEvent(ctx)
		}
		return
	}

	errorHandler, _ := a.errorOutputHandler(pt.handlerCfg)
	a.publishFailure(ctx, taskID, pt, errorHandler, errorOnlyResponse(a2aErr.Code, a2aErr.Message), pt.handlerCfg.OnError)
}

func errorOnlyResponse(code int, message string) *simplifiedResponse {
	return &simplifiedResponse{
		Text:            message,
		A2ATaskResponse: map[string]any{"error": errorObject(code, message)},
	}
}

func isUnsuccessfulTerminal(state a2a.TaskState) bool {
	switch state {
	case a2a.TaskStateFailed, a2a.TaskStateRejected, a2a.TaskStateCancelled:
		return true
	default:
		return false
	}
}

const taskFailedErrorCode = -32603

func failedTaskMessage(task *a2a.Task) string {
	if text := statusText(task); text != "" {
		return text
	}
	if structuredErr := structuredInvocationError(task); structuredErr != "" {
		return structuredErr
	}
	if metaErr, ok := task.Metadata["error"].(string); ok && metaErr != "" {
		return metaErr
	}
	return fmt.Sprintf("task ended in state %q", task.Status.State)
}

func statusText(task *a2a.Task) string {
	if task.Status.Message == nil {
		return ""
	}
	var textParts []string
	for _, part := range task.Status.Message.Parts {
		if part.Kind == "text" {
			textParts = append(textParts, part.Text)
		}
	}
	return strings.Join(textParts, "\n")
}

func structuredInvocationError(task *a2a.Task) string {
	msg, _ := structuredInvocationResult(task)["error_message"].(string)
	return msg
}

func structuredInvocationResult(task *a2a.Task) map[string]any {
	if task.Status.Message == nil {
		return nil
	}
	for _, part := range task.Status.Message.Parts {
		data, ok := part.Data.(map[string]any)
		if ok && part.Kind == "data" && data["type"] == string(a2a.SignalStructuredInvocationRes) {
			return data
		}
	}
	return nil
}

func (a *EventMeshAdapter) resolveResult(ctx context.Context, taskID string, task *a2a.Task, pt *pendingTask, resp *simplifiedResponse) string {
	result := structuredInvocationResult(task)
	resp.StructuredResult = result
	if isUnsuccessfulTerminal(task.Status.State) && (targetsWorkflow(pt.handlerCfg) || pt.sentStructuredRequest || result != nil) {
		return failedTaskMessage(task)
	}
	if result == nil {
		if pt.sentStructuredRequest {
			a.logger.Warn("structured invocation reply carried no structured result; publishing null structured_output",
				"taskID", taskID, "handler", pt.handlerCfg.Name)
		}
		if targetsWorkflow(pt.handlerCfg) && len(resp.Data) > 0 {
			resp.StructuredOutput = resp.Data[0]
		}
		return ""
	}
	if result["status"] != "success" {
		return failedTaskMessage(task)
	}
	output, err := a.loadStructuredOutput(ctx, task, result, pt.userID)
	if err != nil {
		a.logger.Error("structured output unavailable; reporting the run as failed",
			"taskID", taskID, "handler", pt.handlerCfg.Name, "error", err)
		if errors.Is(err, context.DeadlineExceeded) {
			return structuredOutputTimeoutMsg
		}
		return structuredOutputUnavailableMsg
	}
	resp.StructuredOutput = output
	return ""
}

const defaultStructuredOutputLoadTimeout = 15 * time.Second

const (
	structuredOutputUnavailableMsg = "structured output could not be loaded"
	structuredOutputTimeoutMsg     = "structured output could not be loaded in time"
)

func (a *EventMeshAdapter) loadStructuredOutput(ctx context.Context, task *a2a.Task, result map[string]any, userID string) (any, error) {
	ref, _ := result["output_artifact_ref"].(map[string]any)
	name, _ := ref["name"].(string)
	switch {
	case name == "":
		return nil, errNoOutputArtifactRef
	case a.artifacts == nil:
		return nil, errNoArtifactService
	}
	agentName, _ := task.Metadata["agent_name"].(string)
	loc := a.artifacts.Resolve(agentName, userID, task.ContextID, name)
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cmp.Or(a.outputLoadTimeout, defaultStructuredOutputLoadTimeout))
	defer cancel()
	content, _, err := a.artifacts.Inner.Load(loadCtx, loc, artifactRefVersion(ref))
	if err != nil {
		return nil, fmt.Errorf("load output artifact %q: %w", name, err)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("load output artifact %q: %w", name, errStructuredOutputMissing)
	}
	var output any
	if err := json.Unmarshal(content, &output); err != nil {
		return string(content), nil
	}
	return output, nil
}

var (
	errNoOutputArtifactRef     = errors.New("structured result references no output artifact")
	errNoArtifactService       = errors.New("no artifact service configured")
	errStructuredOutputMissing = errors.New("artifact not found or empty")
)

func artifactRefVersion(ref map[string]any) *int {
	switch v := ref["version"].(type) {
	case float64:
		return new(int(v))
	case int:
		return new(v)
	default:
		return nil
	}
}

func (a *EventMeshAdapter) publishFailure(ctx context.Context, taskID string, pt *pendingTask, errorHandler *OutputHandlerConfig, resp *simplifiedResponse, handlerName string) {
	if errorHandler != nil {
		if err := a.publishResponse(ctx, a.logger.With("taskID", taskID), errorHandler, resp, pt.forwardContext); err != nil {
			a.logger.Error("failed to publish error response", "taskID", taskID, "handlerName", handlerName, "error", err)
		}
	}
	a.recordOutcome(ctx, taskID, pt, store.InboundOutcomeReject)
}

func (a *EventMeshAdapter) errorOutputHandler(handler *EventHandlerConfig) (*OutputHandlerConfig, bool) {
	if handler.OnError == "" {
		return nil, false
	}
	outputHandler, ok := a.cfg.OutputHandlers[handler.OnError]
	return outputHandler, ok && outputHandler != nil
}

func errorObject(code int, message string) map[string]any {
	return map[string]any{
		"code":    code,
		"message": message,
	}
}

const identityFailureCode = -32003

func (a *EventMeshAdapter) publishIdentityFailure(ctx context.Context, logger *slog.Logger, handler *EventHandlerConfig, forwardContext map[string]any, message string) {
	outputHandler, ok := a.errorOutputHandler(handler)
	if !ok {
		return
	}
	if err := a.publishResponse(ctx, logger, outputHandler, errorOnlyResponse(identityFailureCode, message), forwardContext); err != nil {
		logger.Error("failed to publish identity failure response", "error", err)
	}
}

func (a *EventMeshAdapter) publishBudgetRefusal(ctx context.Context, logger *slog.Logger, handler *EventHandlerConfig, forwardContext map[string]any, refusal error) {
	outputHandler, ok := a.errorOutputHandler(handler)
	if !ok {
		return
	}
	resp := errorOnlyResponse(entrypoint.BudgetExhaustedErrorCode, entrypoint.BudgetRefusalText(refusal))
	if err := a.publishResponse(ctx, logger, outputHandler, resp, forwardContext); err != nil {
		logger.Warn("failed to publish budget refusal response", "error", err)
	}
}

func (a *EventMeshAdapter) publishResponse(
	ctx context.Context,
	logger *slog.Logger,
	handler *OutputHandlerConfig,
	resp *simplifiedResponse,
	forwardContext map[string]any,
) error {
	respJSON, _ := json.Marshal(resp)
	var respMap any
	if err := json.Unmarshal(respJSON, &respMap); err != nil {
		logger.Debug("unmarshal response for output handler expression context", "error", err)
	}

	exprCtx := sacexpr.NewContext(respMap, "", nil)

	if len(forwardContext) > 0 {
		_ = exprCtx.SetData("user_data.forward_context:", forwardContext)
	}

	if len(handler.OutputTransforms) > 0 {
		transforms := rewriteTaskResponseExpressions(handler.OutputTransforms)
		if err := sacexpr.ApplyTransforms(exprCtx, transforms); err != nil {
			return fmt.Errorf("output transforms: %w", err)
		}
	}

	topicExpr := rewriteTaskResponseStr(handler.TopicExpression)
	topicVal, err := exprCtx.GetData(topicExpr)
	if err != nil {
		return fmt.Errorf("topic_expression: %w", err)
	}
	topic, ok := topicVal.(string)
	if !ok || topic == "" {
		return fmt.Errorf("topic_expression resolved to empty/non-string: %v", topicVal)
	}

	payloadExpr := rewriteTaskResponseStr(handler.PayloadExpression)
	payloadVal, err := exprCtx.GetData(payloadExpr)
	if err != nil {
		return fmt.Errorf("payload_expression: %w", err)
	}

	payloadBytes, err := encodePayload(payloadVal, handler.PayloadFormat)
	if err != nil {
		return fmt.Errorf("payload encoding: %w", err)
	}

	if handler.OutputSchema != nil {
		if err := ValidatePayload(payloadVal, handler.OutputSchema); err != nil {
			if handler.OnValidationError == "drop" {
				logger.Warn("output schema validation failed — dropping message",
					"topic", topic, "error", err)
				return nil
			}
			logger.Warn("output schema validation failed",
				"topic", topic, "error", err)
		}
	}

	msg := outboundMessage(exprCtx, handler, payloadBytes, logger)
	publish := a.dataBroker.Publish
	if handler.guaranteed() {
		publish = a.dataBroker.PublishGuaranteed
	}
	if err := publish(ctx, topic, msg); err != nil {
		return fmt.Errorf("data plane publish to %q: %w", topic, err)
	}

	return nil
}

func buildSimplifiedResponse(task *a2a.Task) *simplifiedResponse {
	resp := &simplifiedResponse{}

	taskJSON, _ := json.Marshal(task)
	var taskMap any
	_ = json.Unmarshal(taskJSON, &taskMap)
	resp.A2ATaskResponse = taskMap

	if task.Status.Message == nil {
		return resp
	}

	var textParts []string
	for _, part := range task.Status.Message.Parts {
		switch part.Kind {
		case "text":
			textParts = append(textParts, part.Text)
		case "file":
			if part.File != nil {
				entry := fileEntry{
					Name:     part.File.Name,
					MimeType: part.File.MIMEType,
				}
				if part.File.URI != "" {
					entry.Content = part.File.URI
				}
				if part.File.Bytes != "" {
					entry.Bytes = part.File.Bytes
				}
				resp.Files = append(resp.Files, entry)
			}
		case "data":
			resp.Data = append(resp.Data, part.Data)
		}
	}

	resp.Text = strings.Join(textParts, "\n")

	return resp
}

func encodePayload(val any, format string) ([]byte, error) {
	switch strings.ToLower(format) {
	case "json", "":
		return json.Marshal(val)
	case "text":
		switch v := val.(type) {
		case string:
			return []byte(v), nil
		case []byte:
			return v, nil
		default:
			return []byte(fmt.Sprintf("%v", v)), nil
		}
	case "yaml":
		return yaml.Marshal(val)
	case "csv":
		return json.Marshal(val)
	default:
		return json.Marshal(val)
	}
}

func rewriteTaskResponseStr(s string) string {
	if strings.HasPrefix(s, "task_response:") {
		return "input.payload:" + s[len("task_response:"):]
	}
	return strings.ReplaceAll(s, "task_response:", "input.payload:")
}

func rewriteTaskResponseExpressions(transforms []sacexpr.TransformConfig) []sacexpr.TransformConfig {
	rewritten := make([]sacexpr.TransformConfig, len(transforms))
	for i, t := range transforms {
		rewritten[i] = t
		rewritten[i].SourceExpression = rewriteTaskResponseStr(t.SourceExpression)
		rewritten[i].DestExpression = rewriteTaskResponseStr(t.DestExpression)
		rewritten[i].SourceListExpression = rewriteTaskResponseStr(t.SourceListExpression)
		rewritten[i].DestListExpression = rewriteTaskResponseStr(t.DestListExpression)
	}
	return rewritten
}

const outcomeWriteTimeout = 5 * time.Second

func (a *EventMeshAdapter) recordOutcome(ctx context.Context, taskID string, pt *pendingTask, outcome string) {
	if !pt.deferredACK || pt.inboundID == "" {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), outcomeWriteTimeout)
	written, err := a.ingress.setOutcome(writeCtx, pt.inboundID, taskID, outcome)
	cancel()
	if err == nil {
		if written {
			a.held.apply(ctx, pt.inboundID, outcome)
		}
		return
	}
	a.logger.Warn("outcome of the input event not recorded; the answer will be handled again", "taskID", taskID, "outcome", outcome, "error", err)
	a.answering.Delete(taskID)
	keepEvent(ctx)
}

func keepEvent(ctx context.Context) {
	if d := delivery.From(ctx); d != nil {
		d.Keep()
	}
}
