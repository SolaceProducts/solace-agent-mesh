// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

type toolNodeTelemetry struct {
	target      statusTarget
	logger      *slog.Logger
	workflow    string
	call        toolCallIdentity
	inFlight    bool
	budget      context.Context
	startFailed bool
}

type toolCallIdentity struct {
	toolNodeID string
	toolName   string
	callID     string
	retry      retryIdentity
}

func (e *DAGExecutor) newToolNodeTelemetry(node *NodeConfig, routing NodeCompletion) *toolNodeTelemetry {
	return e.telemetryFor(toolCallIdentity{
		toolNodeID: node.ID,
		toolName:   node.ToolName,
		callID:     routing.SubTaskID,
		retry:      e.retryIdentityFor(routing),
	})
}

func (e *DAGExecutor) telemetryFor(call toolCallIdentity) *toolNodeTelemetry {
	return &toolNodeTelemetry{target: e.statusTarget(), logger: e.logger, workflow: e.cfg.Name, call: call}
}

func (t *toolNodeTelemetry) started(args map[string]any) error {
	args = argsForPythonWireParity(args)
	t.inFlight = true
	err := t.send(a2a.SignalToolInvocationStart, a2a.ToolInvocationStartData{
		Type:           string(a2a.SignalToolInvocationStart),
		ToolName:       t.call.toolName,
		ToolArgs:       hilsurface.RedactToolArgs(args),
		FunctionCallID: t.call.callID,
	})
	t.startFailed = err != nil
	return err
}

func argsForPythonWireParity(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	return args
}

func (t *toolNodeTelemetry) finished(result *tool.Result) error {
	t.inFlight = false
	if t.startFailed {
		return nil
	}
	return t.send(a2a.SignalToolResult, a2a.ToolResultData{
		Type:           string(a2a.SignalToolResult),
		ToolName:       t.call.toolName,
		ResultData:     hilsurface.RedactValue(tool.PublishedResultData(result)),
		FunctionCallID: t.call.callID,
	})
}

func (t *toolNodeTelemetry) refusedBeforeDispatch(args map[string]any, result *tool.Result) error {
	if err := t.started(args); err != nil {
		return err
	}
	t.budget = nil
	return t.finished(result)
}

func (t *toolNodeTelemetry) abandoned(err error) {
	if t.inFlight {
		_ = t.finished(tool.DispatchFailure(err))
	}
}

func (t *toolNodeTelemetry) send(signal a2a.SignalType, data any) error {
	if t.target.topic == "" {
		return nil
	}
	metadata := t.call.retry.stamp(map[string]any{
		"agent_name":    t.workflow,
		"workflow_name": t.workflow,
		"node_id":       t.call.toolNodeID,
		"node_type":     "tool",
		"sub_task_id":   t.call.callID,
	})
	if err := t.target.publishTelemetry(cmp.Or(t.budget, context.Background()), []a2a.Part{a2a.NewDataPart(data)}, metadata); err != nil {
		t.logger.Error("tool node telemetry publish failed",
			"node", t.call.toolNodeID, "tool", t.call.toolName, "subTaskID", t.call.callID,
			"signalType", string(signal), "error", err)
		return err
	}
	return nil
}

const telemetrySafeSaveFailureMessage = "the tool's output could not be saved as an artifact"

func outcomeResult(result *tool.Result, dispatchErr, saveErr, nodeErr error) *tool.Result {
	switch {
	case dispatchErr != nil:
		return tool.DispatchFailure(dispatchErr)
	case saveErr != nil:
		return tool.Error(telemetrySafeSaveFailureMessage)
	case nodeErr == nil:
		return result
	case result == nil:
		return tool.Error("the tool returned no result")
	case result.Status == tool.StatusError || result.Status == tool.StatusFailed:
		return result
	}
	return tool.Error("the tool returned a pending result, which a workflow tool node cannot service",
		tool.WithErrorCode(taxonomy.ErrorCodePendingNotSupported))
}

func hilRefusalResult(refusal tool.ApprovalRefusal) *tool.Result {
	return tool.Error("not approved: "+string(refusal),
		tool.WithErrorCode(tool.HILRefusalErrorCode(refusal)))
}

func deferredResumeResult(nodeStatus string, output map[string]any) *tool.Result {
	if nodeStatus != "answered" {
		return hilRefusalResult(approvalRefusalCode(nodeStatus, true))
	}
	if approved, ok := output["approved"].(bool); ok && !approved {
		return hilRefusalResult(tool.RefusalDenied)
	}
	return &tool.Result{Status: tool.StatusSuccess, Data: output}
}

type pausedToolCall struct {
	call      toolCallIdentity
	routing   NodeCompletion
	announced bool
	args      map[string]any
}

func (r persistedRouting) toolCall() pausedToolCall {
	return pausedToolCall{
		call: toolCallIdentity{
			toolNodeID: r.ToolNodeID,
			toolName:   r.ToolName,
			callID:     r.SubTaskID,
			retry:      retryIdentity{group: r.RetryGroupID, attempt: r.RetryAttempt},
		},
		routing: NodeCompletion{NodeID: r.NodeID, SubTaskID: r.SubTaskID,
			MapNodeID: r.MapNodeID, LoopNodeID: r.LoopNodeID, ItemIndex: r.ItemIndex},
		announced: r.Kind != PauseKindApproval && r.RetryGroupID != "" && !r.StartUnpublished,
		args:      r.PendingArgs,
	}
}

func (c *pausedToolCall) resolveToolNode(dag *DependencyGraph) bool {
	if c.call.toolNodeID != "" && c.call.toolName != "" {
		return true
	}
	if dag == nil {
		return false
	}
	node := dag.Nodes[cmp.Or(c.call.toolNodeID, c.routing.NodeID)]
	if node == nil || node.ToolName == "" {
		return false
	}
	c.call.toolNodeID, c.call.toolName = node.ID, node.ToolName
	return true
}

func (e *DAGExecutor) settlePausedToolCall(paused pausedToolCall, result *tool.Result) {
	if !paused.resolveToolNode(e.dag) {
		return
	}
	if paused.call.retry.group == "" {
		e.mu.Lock()
		paused.call.retry = e.retryIdentityFor(paused.routing)
		e.mu.Unlock()
	}
	_ = e.telemetryFor(paused.call).settle(paused, result)
}

func (t *toolNodeTelemetry) settle(paused pausedToolCall, result *tool.Result) error {
	if !paused.announced {
		return t.refusedBeforeDispatch(paused.args, result)
	}
	return t.finished(result)
}

func (e *DAGExecutor) refuseInOrder(t *toolNodeTelemetry, args map[string]any, result *tool.Result) bool {
	return e.publishInOrder(t, func() { _ = t.refusedBeforeDispatch(args, result) })
}

func (e *DAGExecutor) finishInOrder(t *toolNodeTelemetry, result *tool.Result) bool {
	return e.publishInOrder(t, func() { _ = t.finished(result) })
}

func (e *DAGExecutor) publishInOrder(t *toolNodeTelemetry, publish func()) bool {
	if e.refusalBudget == nil {
		e.refusalBudget, e.cancelRefusalBudget = context.WithTimeout(context.Background(), toolStatusPublishTimeout)
	}
	if e.refusalBudget.Err() != nil {
		e.refusalsDropped++
		return false
	}
	t.budget = e.refusalBudget
	publish()
	return true
}

func (e *DAGExecutor) closeRefusedPauseInOrder(evt *EventNodePaused, result *tool.Result) {
	approval := evt.Kind == PauseKindApproval
	if !approval && evt.startUnpublished {
		return
	}
	t := e.telemetryFor(toolCallIdentity{
		toolNodeID: cmp.Or(evt.ToolNodeID, evt.NodeID),
		toolName:   evt.ToolName,
		callID:     evt.SubTaskID,
		retry:      evt.retry,
	})
	if approval {
		evt.closed = e.refuseInOrder(t, evt.PendingArgs, result)
		return
	}
	evt.closed = e.finishInOrder(t, result)
}

func (e *DAGExecutor) endRefusalBudget() {
	if e.refusalsDropped > 0 {
		e.logger.Warn("tool node telemetry budget spent; refused calls not published",
			"executionID", e.state.ExecutionID, "dropped", e.refusalsDropped)
	}
	if e.cancelRefusalBudget != nil {
		e.cancelRefusalBudget()
	}
	e.refusalBudget, e.cancelRefusalBudget, e.refusalsDropped = nil, nil, 0
}

func (e *DAGExecutor) refuseToolIteration(node *NodeConfig, routing NodeCompletion, err error) {
	if node.Type != "tool" {
		return
	}
	routing.SubTaskID = cmp.Or(routing.SubTaskID, idgen.New())
	e.refuseInOrder(e.newToolNodeTelemetry(node, routing), nil, tool.PreDispatchFailure(err))
}

func (e *DAGExecutor) refuseApprovedCall(evt *EventNodeApproved, result *tool.Result) {
	call := toolCallIdentity{toolNodeID: cmp.Or(evt.ToolNodeID, evt.NodeID), toolName: evt.ToolName, callID: evt.SubTaskID, retry: evt.retry}
	if call.retry.group == "" {
		call.retry = e.retryIdentityFor(NodeCompletion{NodeID: evt.NodeID, SubTaskID: evt.SubTaskID,
			MapNodeID: evt.MapNodeID, LoopNodeID: evt.LoopNodeID, ItemIndex: evt.ItemIndex})
	}
	e.refuseInOrder(e.telemetryFor(call), evt.Args, result)
}

func (w *WorkflowInstance) closeAbandonedToolCalls(budget context.Context, rows []pausedRow) {
	failed := 0
	defer func() {
		if failed > 0 {
			w.logger.Warn("workflow HIL: could not close some tool calls of retired paused rows; their starts stay open",
				"executionID", rows[0].row.ExecutionID, "unclosed", failed)
		}
	}()
	for i, row := range rows {
		if budget.Err() != nil {
			failed += len(rows) - i
			return
		}
		if !row.execContextKnown {
			w.logger.Warn("workflow HIL: a retired paused row's execution context will not decode; its tool call stays open",
				"requestID", row.row.RequestID, "executionID", row.row.ExecutionID)
			continue
		}
		paused := row.routing.toolCall()
		if !paused.resolveToolNode(w.dag) {
			continue
		}
		t := &toolNodeTelemetry{
			target:   w.statusTargetFor(row.execContext),
			logger:   w.logger,
			workflow: w.cfg.Name,
			call:     paused.call,
			budget:   budget,
		}
		if err := t.settle(paused, hilRefusalResult(tool.RefusalRunEndedUnanswered)); err != nil {
			failed++
		}
	}
}

type statusTarget struct {
	broker     broker.Broker
	topic      string
	taskID     string
	contextID  string
	traceID    string
	userConfig string
	userID     string
	clientID   string
	publisher  string
}

func (e *DAGExecutor) statusTarget() statusTarget {
	return e.instance.statusTargetFor(persistedExecContext{
		UserProps: e.execCtx.UserProps, TaskID: e.execCtx.TaskID, ContextID: e.execCtx.ContextID,
	})
}

func (w *WorkflowInstance) statusTargetFor(ec persistedExecContext) statusTarget {
	target := statusTarget{
		topic:      ec.UserProps.A2AStatusTopic,
		taskID:     ec.TaskID,
		contextID:  ec.ContextID,
		traceID:    ec.UserProps.TraceID,
		userConfig: ec.UserProps.A2AUserConfig,
		userID:     ec.UserProps.UserID,
		clientID:   ec.UserProps.ClientID,
		publisher:  w.cfg.Name,
	}
	if w.svc != nil {
		target.broker = w.svc.Broker()
	}
	return target
}

func (t statusTarget) publishTelemetry(parent context.Context, parts []a2a.Part, metadata map[string]any) error {
	if t.broker == nil {
		return errNoBroker
	}
	ctx, cancel := context.WithTimeout(parent, toolStatusPublishTimeout)
	defer cancel()

	event := a2a.NewStatusUpdateEvent(t.taskID, t.contextID, parts, false, nil)
	event.Metadata = metadata
	event.Status.Message.Metadata = metadata
	payload, err := json.Marshal(a2a.NewResponse(t.taskID, event))
	if err != nil {
		return fmt.Errorf("marshal tool telemetry: %w", err)
	}
	return t.broker.PublishGuaranteed(ctx, t.topic, &broker.Message{Payload: payload, UserProperties: t.userProperties()})
}

func (t statusTarget) userProperties() map[string]string {
	props := map[string]string{
		a2a.PropDelegatingAgentName: t.publisher,
		a2a.PropTimestamp:           strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
	for key, value := range map[string]string{a2a.PropUserID: t.userID, a2a.PropClientID: t.clientID, a2a.PropA2AUserConfig: t.userConfig} {
		if value != "" {
			props[key] = value
		}
	}
	a2a.AddTraceID(props, t.traceID)
	return props
}

var errNoBroker = errors.New("no broker")
