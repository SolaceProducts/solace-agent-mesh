// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

const approvalRequestIDPrefix = "hil-approval-"

type gatedCall struct {
	node          *NodeConfig
	input         map[string]any
	routing       NodeCompletion
	toolCtx       *tool.Context
	inExitHandler bool
	telemetry     *toolNodeTelemetry
	fail          func(ErrorKind, error)
}

func (e *DAGExecutor) gateToolCall(ctx context.Context, call gatedCall) (handled bool) {
	node, input, routing, toolCtx, inExitHandler, telemetry, fail :=
		call.node, call.input, call.routing, call.toolCtx, call.inExitHandler, call.telemetry, call.fail
	hilCfg, ok := e.instance.hilToolConfigs[node.ToolName]
	if !ok || !tool.ShouldGateCall(e.instance.tools.GetTool(node.ToolName), hilCfg, input) {
		return false
	}
	if !e.instance.toolApprovalEnforced() {
		toolCtx.ApprovalReason = tool.ApprovalEnforcementDisabled
		if _, seen := e.approvalWarnedTools.LoadOrStore(node.ToolName, struct{}{}); !seen {
			e.logger.Warn("dispatching a tool that requires approval without asking anyone: workflow approval enforcement is disabled",
				"node", node.ID, "tool", node.ToolName, "executionID", e.state.ExecutionID)
		}
		return false
	}
	if e.evalRun() {
		e.auditApprovalRefusal(ctx, node.ToolName, tool.RefusalNoHumanInEvalRun)
		_ = telemetry.refusedBeforeDispatch(input, hilRefusalResult(tool.RefusalNoHumanInEvalRun))
		fail(ErrorKindError, fmt.Errorf("tool %q requires human approval: %w", node.ToolName, errNoHumanInEvalRun))
		return true
	}
	if hilCfg.Approvers.Configured() {
		e.auditApprovalRefusal(ctx, node.ToolName, tool.RefusalNoApprovalRoute)
		_ = telemetry.refusedBeforeDispatch(input, hilRefusalResult(tool.RefusalNoApprovalRoute))
		fail(ErrorKindError, fmt.Errorf(
			"tool %q requires approval from its configured approvers, which a workflow cannot ask yet; "+
				"call the tool from an agent, or remove hil.approvers", node.ToolName))
		return true
	}
	if !e.instance.workflowHILEnabled() {
		e.auditApprovalRefusal(ctx, node.ToolName, tool.RefusalNoApprovalRoute)
		_ = telemetry.refusedBeforeDispatch(input, hilRefusalResult(tool.RefusalNoApprovalRoute))
		fail(ErrorKindError, e.noApprovalRouteError(node.ToolName))
		return true
	}
	if inExitHandler {
		e.auditApprovalRefusal(ctx, node.ToolName, tool.RefusalExitHandler)
		_ = telemetry.refusedBeforeDispatch(input, hilRefusalResult(tool.RefusalExitHandler))
		fail(ErrorKindError, fmt.Errorf(
			"tool %q requires human approval and cannot run in the on_exit handler of node %q: "+
				"the run is already finishing and would be held open waiting for an answer",
			node.ToolName, node.ID))
		return true
	}

	pause, err := e.approvalPauseEvent(node, input, routing, hilCfg)
	if err != nil {
		e.auditApprovalRefusal(ctx, node.ToolName, tool.RefusalPromptFailed)
		_ = telemetry.refusedBeforeDispatch(input, hilRefusalResult(tool.RefusalPromptFailed))
		fail(ErrorKindError, err)
		return true
	}
	pause.retry = telemetry.call.retry
	_ = e.Advance(ctx, pause)
	if !pause.parked && !pause.closed {
		_ = telemetry.refusedBeforeDispatch(input, hilRefusalResult(cmp.Or(pause.unparked, tool.RefusalRunEnded)))
	}
	return true
}

func (e *DAGExecutor) noApprovalRouteError(toolName string) error {
	configured := e.instance.cfg.HILDatabaseURL != ""
	enabled := e.instance.svc != nil && e.instance.svc.FeatureRegistry() != nil &&
		e.instance.svc.FeatureRegistry().IsEnabled("workflow_hil_pause_resume")
	if configured && enabled {
		return fmt.Errorf(
			"tool %q requires human approval, but this workflow's approval store is not available "+
				"(the instance is stopping, or the store could not be opened at startup)", toolName)
	}
	return fmt.Errorf(
		"tool %q requires human approval, but this workflow cannot request it; "+
			"enable workflow_hil_pause_resume, configure a session database, and run the "+
			"notification service, which is what delivers the prompt and carries the answer back",
		toolName)
}

func (e *DAGExecutor) auditToolExecution(ctx context.Context, node *NodeConfig, approval tool.ApprovalReason, result *tool.Result, dispatchErr error, took time.Duration) {
	success := dispatchErr == nil && result != nil &&
		result.Status != tool.StatusError && result.Status != tool.StatusFailed

	errClass := ""
	switch {
	case dispatchErr != nil:
		errClass = tool.ClassifyDispatchError(dispatchErr)
	case !success:
		errClass = tool.AuditToolErrorCode
	}

	e.auditToolEvent(ctx, auth.ToolExecutionEvent{
		ToolName:        node.ToolName,
		DurationMs:      float64(took.Microseconds()) / 1000,
		Success:         success,
		Error:           errClass,
		ApprovalReason:  string(approval),
		ApprovalRefusal: string(resultRefusal(result)),
	})
}

func resultRefusal(result *tool.Result) tool.ApprovalRefusal {
	if result == nil {
		return ""
	}
	return result.ApprovalRefusal
}

func (e *DAGExecutor) auditApprovalRefusal(ctx context.Context, toolName string, refusal tool.ApprovalRefusal) {
	e.auditToolEvent(ctx, auth.ToolExecutionEvent{
		ToolName:        toolName,
		Success:         false,
		Error:           tool.AuditToolErrorCode,
		ApprovalRefusal: string(refusal),
	})
}

func (e *DAGExecutor) auditToolEvent(ctx context.Context, event auth.ToolExecutionEvent) {
	if e.instance.svc == nil || e.instance.svc.AuditLogger() == nil {
		return
	}
	event.UserID = e.execCtx.UserProps.UserID
	event.AgentName = e.cfg.Name
	event.SessionID = e.execCtx.ContextID
	e.instance.svc.AuditLogger().LogToolExecution(ctx, event)
}

func (e *DAGExecutor) approvalPauseEvent(node *NodeConfig, input map[string]any, routing NodeCompletion, hilCfg tool.HILToolConfig) (*EventNodePaused, error) {
	timeout := hilCfg.Timeout
	if timeout == 0 {
		timeout = workflowHILDefaultTimeout
	}

	var schema *tool.JSONSchema
	if t := e.instance.tools.GetTool(node.ToolName); t != nil {
		schema = t.Parameters()
	}

	requestID := approvalRequestIDPrefix + idgen.New()
	surface, err := hilsurface.Generate(hilsurface.Request{
		RequestID:     requestID,
		ToolName:      node.ToolName,
		RequesterKind: hilsurface.RequesterWorkflow,
		RequesterName: e.cfg.Name,
		ToolArgs:      input,
		Schema:        schema,
		Message:       hilCfg.ApprovalMessage,
		ShowArgs:      hilCfg.ShowArgs,
		Timeout:       timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("generate approval surface for tool %q: %w", node.ToolName, err)
	}

	return &EventNodePaused{
		NodeID:      routing.NodeID,
		SubTaskID:   routing.SubTaskID,
		RequestID:   requestID,
		Surface:     surface,
		ExpiresAt:   time.Now().Add(timeout).UTC().Format(time.RFC3339),
		MapNodeID:   routing.MapNodeID,
		LoopNodeID:  routing.LoopNodeID,
		ItemIndex:   routing.ItemIndex,
		Kind:        PauseKindApproval,
		ToolNodeID:  node.ID,
		ToolName:    node.ToolName,
		PendingArgs: input,
	}, nil
}

func (w *WorkflowInstance) resumeApprovalPause(ctx context.Context, requestID, executionID string, exec *WorkflowExecution, routing persistedRouting, nodeStatus string, data map[string]any) {
	if reason, dead := executionNoLongerLive(exec); dead {
		w.logger.Warn("workflow HIL: approval arrived for a run that already ended; not dispatching",
			"requestID", requestID, "executionID", executionID,
			"traceID", exec.Context.UserProps.TraceID,
			"node", routing.NodeID, "tool", routing.ToolName, "reason", reason)
		exec.Executor.auditApprovalRefusal(ctx, routing.ToolName, tool.RefusalRunEnded)
		exec.Executor.settlePausedToolCall(routing.toolCall(), hilRefusalResult(tool.RefusalRunEnded))
		_ = exec.Executor.Advance(ctx, &EventNodeFailed{
			NodeID:     routing.NodeID,
			SubTaskID:  routing.SubTaskID,
			Err:        fmt.Errorf("tool %q was approved, but the run had already ended (%s)", routing.ToolName, reason),
			ErrKind:    ErrorKindFailure,
			MapNodeID:  routing.MapNodeID,
			LoopNodeID: routing.LoopNodeID,
			ItemIndex:  routing.ItemIndex,
		})
		w.cleanupIfFinalized(exec)
		return
	}

	approved, answered := data["approved"].(bool)
	if nodeStatus == "answered" && answered && approved {
		if aerr := exec.Executor.Advance(ctx, &EventNodeApproved{
			NodeID:     routing.NodeID,
			ToolNodeID: routing.ToolNodeID,
			SubTaskID:  routing.SubTaskID,
			ToolName:   routing.ToolName,
			Args:       routing.PendingArgs,
			MapNodeID:  routing.MapNodeID,
			LoopNodeID: routing.LoopNodeID,
			ItemIndex:  routing.ItemIndex,
			retry:      retryIdentity{group: routing.RetryGroupID, attempt: routing.RetryAttempt},
		}); aerr != nil {
			w.logger.Error("workflow HIL: approved dispatch Advance failed",
				"requestID", requestID, "executionID", executionID,
				"traceID", exec.Context.UserProps.TraceID, "error", aerr)
		}
		w.logger.Info("workflow HIL: tool approved; dispatched",
			"requestID", requestID, "executionID", executionID,
			"traceID", exec.Context.UserProps.TraceID,
			"node", routing.NodeID, "tool", routing.ToolName)
		w.cleanupIfFinalized(exec)
		return
	}

	refusal := approvalRefusalCode(nodeStatus, answered)
	exec.Executor.auditApprovalRefusal(ctx, routing.ToolName, refusal)
	exec.Executor.settlePausedToolCall(routing.toolCall(), hilRefusalResult(refusal))
	w.logger.Info("workflow HIL: tool not approved; failing the node",
		"requestID", requestID, "executionID", executionID,
		"traceID", exec.Context.UserProps.TraceID,
		"node", routing.NodeID, "tool", routing.ToolName, "nodeStatus", nodeStatus)
	if aerr := exec.Executor.Advance(ctx, &EventNodeFailed{
		NodeID:     routing.NodeID,
		SubTaskID:  routing.SubTaskID,
		Err:        approvalDenialError(routing.ToolName, nodeStatus, answered),
		ErrKind:    ErrorKindFailure,
		MapNodeID:  routing.MapNodeID,
		LoopNodeID: routing.LoopNodeID,
		ItemIndex:  routing.ItemIndex,
	}); aerr != nil {
		w.logger.Error("workflow HIL: denial Advance failed",
			"requestID", requestID, "executionID", executionID,
			"traceID", exec.Context.UserProps.TraceID, "error", aerr)
	}
	w.cleanupIfFinalized(exec)
}

func executionNoLongerLive(exec *WorkflowExecution) (string, bool) {
	exec.Executor.mu.Lock()
	defer exec.Executor.mu.Unlock()
	switch {
	case exec.State.Finalized:
		return "finalized", true
	case exec.State.Cancelled:
		return "cancelled", true
	case !exec.State.WorkflowDeadline.IsZero() && time.Now().After(exec.State.WorkflowDeadline):
		return "past the workflow deadline", true
	}
	return "", false
}

func approvalRefusalCode(nodeStatus string, answered bool) tool.ApprovalRefusal {
	switch nodeStatus {
	case "timed_out":
		return tool.RefusalTimedOut
	case "cancelled":
		return tool.RefusalCancelled
	case "answered":
		if !answered {
			return tool.RefusalUnreadable
		}
		return tool.RefusalDenied
	default:
		return tool.RefusalUnexpected
	}
}

func approvalDenialError(toolName, nodeStatus string, answered bool) error {
	switch {
	case nodeStatus == "timed_out":
		return fmt.Errorf("approval for tool %q timed out", toolName)
	case nodeStatus == "cancelled":
		return fmt.Errorf("approval for tool %q was cancelled by the user", toolName)
	case nodeStatus == "answered" && !answered:
		return fmt.Errorf("approval for tool %q could not be read and was treated as a refusal", toolName)
	case nodeStatus == "answered":
		return fmt.Errorf("tool %q was denied by the user", toolName)
	default:
		return fmt.Errorf("unexpected approval response %q for tool %q", nodeStatus, toolName)
	}
}
