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
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const controlQuerierTimeout = 10 * time.Second

const toolStatusPublishTimeout = 5 * time.Second

func undeclaredToolArgs(t tool.Tool, input map[string]any) []string {
	schema := t.Parameters()
	if schema == nil {
		return nil
	}
	if schema.Raw != nil {
		return nil
	}
	if len(schema.Properties) == 0 && !tool.HasAuthoredSchemas(t) {
		return nil
	}
	var undeclared []string
	for name := range input {
		if _, declared := schema.Properties[name]; !declared {
			undeclared = append(undeclared, name)
		}
	}
	slices.Sort(undeclared)
	return undeclared
}

func declaredToolArgs(t tool.Tool) []string {
	schema := t.Parameters()
	if schema == nil || schema.Raw != nil {
		return nil
	}
	names := slices.Collect(maps.Keys(schema.Properties))
	slices.Sort(names)
	return names
}

func undeclaredArgsMessage(nodeID, toolName string, undeclared, declared []string) string {
	return fmt.Sprintf(
		"tool node %q passes argument(s) %s that tool %q does not accept; its input parameters are: %s "+
			"(these are INPUT parameter names — they are not the tool's result field names, so do not reuse them in a {{%s.output.<field>}} reference)",
		nodeID, strings.Join(undeclared, ", "), toolName, strings.Join(declared, ", "), nodeID)
}

func toolDispatchContext(execCtx *WorkflowExecutionContext, inExitHandler bool) context.Context {
	if inExitHandler {
		return execCtx.Ctx
	}
	return execCtx.ToolCtx
}

func (e *DAGExecutor) dispatchToolNode(ctx context.Context, node *NodeConfig, subTaskID string) {
	routing := NodeCompletion{NodeID: node.ID, SubTaskID: subTaskID}
	if unresolvable := e.unresolvableTool(node); unresolvable != nil {
		e.refuseInOrder(e.newToolNodeTelemetry(node, routing), nil, tool.PreDispatchFailure(unresolvable))
		e.handleNodeError(node.ID, unresolvable)
		return
	}

	input, err := e.resolveNodeInput(node)
	if err != nil {
		resolveErr := fmt.Errorf("resolve input: %w", err)
		e.refuseInOrder(e.newToolNodeTelemetry(node, routing), nil, tool.PreDispatchFailure(resolveErr))
		e.handleNodeError(node.ID, resolveErr)
		return
	}
	e.recordInputPreview(node.ID, input)

	e.state.TrackSubTask(subTaskID, node.ID)
	e.state.AddPending(node.ID)

	if _, saveErr := e.saveInputArtifact(ctx, node.ID, subTaskID, input); saveErr != nil {
		e.refuseInOrder(e.newToolNodeTelemetry(node, routing), input,
			tool.PreDispatchFailure(errors.New("the tool's input could not be saved as an artifact")))
		e.handleNodeError(node.ID, fmt.Errorf("save input artifact: %w", saveErr))
		return
	}

	e.armNodeTimer(node, subTaskID, NodeCompletion{
		NodeID:    node.ID,
		SubTaskID: subTaskID,
	})

	e.invokeToolNode(node, input, NodeCompletion{
		NodeID:    node.ID,
		SubTaskID: subTaskID,
	})
}

func (e *DAGExecutor) recordToolNodeUsage(toolName string) {
	provenance, classified := e.instance.toolProvenance[toolName]
	if !classified {
		return
	}
	e.state.UsageFacts.AddToolCategory(provenance.Category)
	e.state.UsageFacts.AddConnectorCategory(provenance.ConnectorCategory)
	e.state.UsageFacts.AddBuiltinCategory(provenance.BuiltinCategory)
}

func errNoToolSet(node *NodeConfig) error {
	return fmt.Errorf("tool node %q references tool %q but workflow declares no `tools:` or `connectors:`", node.ID, node.ToolName)
}

func (e *DAGExecutor) unresolvableTool(node *NodeConfig) error {
	if e.instance.tools == nil {
		return errNoToolSet(node)
	}
	if e.instance.tools.GetTool(node.ToolName) == nil {
		return fmt.Errorf("tool node %q references unknown tool %q", node.ID, node.ToolName)
	}
	return nil
}

func (e *DAGExecutor) invokeToolNode(node *NodeConfig, input map[string]any, routing NodeCompletion) {
	e.invokeToolNodeWithApproval(node, input, routing, tool.ApprovalNone, retryIdentity{})
}

func (e *DAGExecutor) invokeApprovedToolNode(node *NodeConfig, input map[string]any, routing NodeCompletion, parked retryIdentity) {
	e.invokeToolNodeWithApproval(node, input, routing, tool.ApprovalHuman, parked)
}

func (e *DAGExecutor) checkDeclaredArgs(node *NodeConfig, input map[string]any, routing NodeCompletion) error {
	if node.Input == nil || isOperatorMap(node.Input) {
		return nil
	}
	resolved := e.instance.tools.GetTool(node.ToolName)
	if resolved == nil {
		return nil
	}
	undeclared := undeclaredToolArgs(resolved, input)
	if len(undeclared) == 0 {
		return nil
	}
	if tool.HasAuthoredSchemas(resolved) && !e.instance.permissiveToolValidation() {
		return errors.New(undeclaredArgsMessage(node.ID, node.ToolName, undeclared, declaredToolArgs(resolved)))
	}
	logUndeclared := e.logger.Warn
	if routing.MapNodeID != "" || routing.LoopNodeID != "" {
		logUndeclared = e.logger.Debug
	}
	logUndeclared("tool node passes undeclared arguments",
		"node", node.ID, "tool", node.ToolName, "undeclaredArgs", undeclared)
	return nil
}

func (e *DAGExecutor) invokeToolNodeWithApproval(node *NodeConfig, input map[string]any, routing NodeCompletion, approval tool.ApprovalReason, parked retryIdentity) {
	toolCtx := e.buildToolContext(node, routing)
	toolCtx.ApprovalReason = approval
	facadeSaved := &savedArtifactCollector{}
	toolCtx.NotifyArtifactSaved = facadeSaved.record
	inExitHandler := e.state.InExitHandler
	dispatchCtx := toolDispatchContext(e.execCtx, inExitHandler)

	e.logger.Debug("dispatched tool node",
		"node", node.ID,
		"tool", node.ToolName,
		"subTaskID", routing.SubTaskID,
		"timeout", e.effectiveNodeTimeout(node),
	)

	telemetry := e.newToolNodeTelemetry(node, routing)
	if parked.group != "" {
		telemetry.call.retry = parked
	}

	fail := func(kind ErrorKind, err error) {
		_ = e.Advance(dispatchCtx, &EventNodeFailed{
			NodeID:     routing.NodeID,
			SubTaskID:  routing.SubTaskID,
			Err:        err,
			ErrKind:    kind,
			MapNodeID:  routing.MapNodeID,
			LoopNodeID: routing.LoopNodeID,
			ItemIndex:  routing.ItemIndex,
		})
	}

	e.instance.wg.Go(func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			record := e.logger.Error
			args := []any{
				"node", node.ID,
				"tool", node.ToolName,
				"subTaskID", routing.SubTaskID,
				"panicValue", fmt.Sprint(r),
			}
			iteration := routing.MapNodeID != "" || routing.LoopNodeID != ""
			if iteration {
				args = append(args, "itemIndex", routing.ItemIndex)
			}
			if _, repeat := e.panickedNodes.LoadOrStore(node.ID, struct{}{}); !repeat {
				args = append(args, "stack", string(debug.Stack()))
			} else if iteration {
				record = e.logger.Debug
			}
			record("tool node panicked", args...)
			panicErr := fmt.Errorf("tool %q panicked: %v", node.ToolName, r)
			telemetry.abandoned(panicErr)
			fail(ErrorKindError, panicErr)
		}()
		if unresolvable := e.unresolvableTool(node); unresolvable != nil {
			_ = telemetry.refusedBeforeDispatch(input, tool.PreDispatchFailure(unresolvable))
			fail(ErrorKindError, unresolvable)
			return
		}
		if refused := e.checkDeclaredArgs(node, input, routing); refused != nil {
			_ = telemetry.refusedBeforeDispatch(input, tool.PreDispatchFailure(refused))
			fail(ErrorKindError, refused)
			return
		}

		if approval == tool.ApprovalNone && e.gateToolCall(dispatchCtx, gatedCall{
			node: node, input: input, routing: routing, toolCtx: toolCtx,
			inExitHandler: inExitHandler, telemetry: telemetry, fail: fail,
		}) {
			return
		}

		e.recordToolNodeUsage(node.ToolName)

		argsJSON, err := json.Marshal(input)
		if err != nil {
			marshalErr := fmt.Errorf("marshal args: %w", err)
			_ = telemetry.refusedBeforeDispatch(nil, tool.PreDispatchFailure(marshalErr))
			fail(ErrorKindError, marshalErr)
			return
		}
		call := tool.Call{
			ID:        routing.SubTaskID,
			Name:      node.ToolName,
			Arguments: string(argsJSON),
		}
		authorisedBy := toolCtx.ApprovalReason
		_ = telemetry.started(input)
		dispatchStart := time.Now()
		result, dispatchErr := e.instance.tools.Dispatch(dispatchCtx, call, toolCtx)
		e.auditToolExecution(dispatchCtx, node, authorisedBy, result, dispatchErr, time.Since(dispatchStart))
		if dispatchErr == nil && (e.instance.workflowHILEnabled() || e.evalRun()) {
			if pause := pauseEventForResult(routing, result); pause != nil {
				pause.ToolNodeID, pause.ToolName, pause.retry = node.ID, node.ToolName, telemetry.call.retry
				pause.startUnpublished = telemetry.startFailed
				_ = e.Advance(dispatchCtx, pause)
				if !pause.parked && !pause.closed {
					_ = telemetry.finished(hilRefusalResult(cmp.Or(pause.unparked, tool.RefusalRunEnded)))
				}
				return
			}
		}
		saved, saveErr := e.persistToolDataObjects(dispatchCtx, toolCtx, result)
		var kind ErrorKind
		var output map[string]any
		var nodeErr error
		if saveErr == nil {
			kind, output, nodeErr = classifyToolResult(node.ToolName, result, dispatchErr,
				append(saved, facadeSaved.artifacts()...))
		}
		_ = telemetry.finished(outcomeResult(result, dispatchErr, saveErr, nodeErr))
		switch {
		case saveErr != nil:
			fail(ErrorKindError, saveErr)
			return
		case nodeErr != nil:
			fail(kind, nodeErr)
			return
		}
		_ = e.Advance(dispatchCtx, &EventNodeComplete{
			NodeID:     routing.NodeID,
			SubTaskID:  routing.SubTaskID,
			Output:     output,
			MapNodeID:  routing.MapNodeID,
			LoopNodeID: routing.LoopNodeID,
			ItemIndex:  routing.ItemIndex,
		})
	})
}

func (e *DAGExecutor) buildToolContext(node *NodeConfig, routing NodeCompletion) *tool.Context {
	subTaskID := routing.SubTaskID
	statusTopic := e.execCtx.UserProps.A2AStatusTopic
	traceID := e.execCtx.UserProps.TraceID
	artifactSvc := e.instance.svc.Artifacts()

	return &tool.Context{
		AgentName:             e.cfg.Name,
		UserID:                e.execCtx.UserProps.UserID,
		SessionID:             e.execCtx.ContextID,
		Identity:              e.execCtx.Identity,
		Namespace:             e.cfg.Namespace,
		ToolConfig:            maps.Clone(e.instance.toolConfigs[node.ToolName]),
		TraceID:               traceID,
		ParentTaskID:          e.execCtx.TaskID,
		ToolCallID:            subTaskID,
		RemoteSpend:           e.toolNodeSpend(node.ID, routing.NodeID),
		ClientID:              e.execCtx.UserProps.ClientID,
		A2AStatusTopic:        statusTopic,
		A2AUserConfig:         e.execCtx.UserProps.A2AUserConfig,
		CallDepth:             e.execCtx.UserProps.CallDepth + 1,
		SearchTurns:           &e.state.SearchTurns,
		ArtifactService:       artifactSvc,
		CredentialService:     e.instance.credentialService(),
		Trigger:               a2a.NormalizeTrigger(e.execCtx.UserProps.Trigger),
		AuthPromptUnavailable: true,
		ControlQuerier:        e.buildControlQuerier(),
		SendStatus:            e.toolStatusForwarder(node.ID, subTaskID),
		SendSignal:            e.toolSignalForwarder(node.ID, node.ToolName, subTaskID),
		PublishNotification: func(innerCtx context.Context, req a2a.NotifyRequest) error {
			return e.publishNotification(innerCtx, req)
		},
		ScheduleTask: func(innerCtx context.Context, req a2a.ScheduleRequest) error {
			return e.publishScheduleRequest(innerCtx, req)
		},
	}
}

func (e *DAGExecutor) buildControlQuerier() tool.ControlQuerier {
	brk := e.instance.svc.Broker()
	if brk == nil {
		return nil
	}
	opts := []control.ClientOption{control.WithTimeout(controlQuerierTimeout)}
	if token := e.execCtx.UserProps.AuthToken; token != "" {
		opts = append(opts, control.WithUserProperties(map[string]string{a2a.PropAuthToken: token}))
	}
	return control.NewClient(brk, e.cfg.Namespace, opts...)
}

func (e *DAGExecutor) persistToolDataObjects(ctx context.Context, toolCtx *tool.Context, result *tool.Result) ([]savedArtifact, error) {
	if result == nil || len(result.DataObjects) == 0 {
		return nil, nil
	}
	if result.Status != tool.StatusSuccess && result.Status != tool.StatusPartial && result.Status != "" {
		return nil, nil
	}
	if toolCtx.ArtifactService == nil {
		return nil, nil
	}
	var saved []savedArtifact
	for _, do := range result.DataObjects {
		if do.ArtifactRef != nil || len(do.Content) == 0 {
			continue
		}
		name := do.Name
		if name == "" {
			name = artifact.FreshName(do.MIMEType)
		}
		res, err := artifact.SaveWithMetadata(ctx, toolCtx.ArtifactService,
			toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, name,
			do.Content, do.MIMEType,
			artifact.MetadataOpts{Description: do.Description, Custom: do.Metadata},
			e.logger)
		if err != nil {
			return nil, fmt.Errorf("persist tool data object %q: %w", name, err)
		}
		saved = append(saved, savedArtifact{name: name, version: res.DataVersion})
	}
	return saved, nil
}

func (e *DAGExecutor) publishScheduleRequest(ctx context.Context, req a2a.ScheduleRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		e.logger.Warn("schedule: marshal ScheduleRequest failed", "error", err, "requestID", req.RequestID)
		return fmt.Errorf("marshal schedule request: %w", err)
	}
	userProps := map[string]string{}
	if req.SenderUserID != "" {
		userProps[a2a.PropUserID] = req.SenderUserID
	}
	topic := a2a.ScheduleRequestTopic(e.cfg.Namespace)
	if err := e.instance.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{Payload: data, UserProperties: userProps}); err != nil {
		e.logger.Error("schedule: publish ScheduleRequest failed", "error", err, "topic", topic, "requestID", req.RequestID)
		return fmt.Errorf("publish schedule request: %w", err)
	}
	e.logger.Info("schedule: published ScheduleRequest",
		"requestID", req.RequestID, "name", req.Name, "target", req.TargetName)
	return nil
}

func (e *DAGExecutor) publishNotification(ctx context.Context, req a2a.NotifyRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal notify request: %w", err)
	}
	userProps := map[string]string{}
	if req.SenderUserID != "" {
		userProps[a2a.PropUserID] = req.SenderUserID
	}
	if tid, ok := req.Metadata["trace_id"].(string); ok && tid != "" {
		userProps[a2a.PropTraceID] = tid
	}
	topic := a2a.NotifyRequestTopic(e.cfg.Namespace, req.RecipientUserID)
	if err := e.instance.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{Payload: data, UserProperties: userProps}); err != nil {
		return fmt.Errorf("publish notify request: %w", err)
	}
	e.logger.Info("notify: published NotifyRequest",
		"requestID", req.RequestID, "recipient", req.RecipientUserID, "mode", req.Mode)
	return nil
}

func (e *DAGExecutor) toolStatusForwarder(nodeID, subTaskID string) func(string) error {
	statusTopic := e.execCtx.UserProps.A2AStatusTopic
	if statusTopic == "" {
		return nil
	}
	traceID := e.execCtx.UserProps.TraceID
	userConfig := e.execCtx.UserProps.A2AUserConfig
	taskID := e.execCtx.TaskID
	contextID := e.execCtx.ContextID

	publish := e.statusPartsPublisher(statusTopic, taskID, contextID, traceID, userConfig)
	return func(message string) error {
		return publish([]a2a.Part{a2a.NewTextPart(message)}, map[string]any{
			"type":          "agent_progress_update",
			"workflow_name": e.cfg.Name,
			"node_id":       nodeID,
			"sub_task_id":   subTaskID,
		})
	}
}

func (e *DAGExecutor) statusPartsPublisher(statusTopic, taskID, contextID, traceID, userConfig string) func([]a2a.Part, map[string]any) error {
	return func(parts []a2a.Part, metadata map[string]any) error {
		ctx, cancel := context.WithTimeout(context.Background(), toolStatusPublishTimeout)
		defer cancel()

		event := a2a.NewStatusUpdateEvent(taskID, contextID, parts, false, nil)
		event.Metadata = metadata

		payload, err := json.Marshal(a2a.NewResponse(taskID, event))
		if err != nil {
			return fmt.Errorf("marshal tool status: %w", err)
		}

		userProps := map[string]string{}
		if userConfig != "" {
			userProps["a2aUserConfig"] = userConfig
		}
		a2a.AddTraceID(userProps, traceID)

		return e.instance.svc.Broker().Publish(ctx, statusTopic, &broker.Message{
			Payload:        payload,
			UserProperties: userProps,
		})
	}
}

var workflowForwardableSignals = map[string]bool{
	string(a2a.SignalRAGInfoUpdate):            true,
	string(a2a.SignalDeepResearchProgress):     true,
	string(a2a.SignalDeepResearchReport):       true,
	string(a2a.SignalDeepResearchPlanStale):    true,
	string(a2a.SignalBuilderComponentProgress): true,
}

func (e *DAGExecutor) toolSignalForwarder(nodeID, toolName, subTaskID string) func(any) error {
	publish := discardStatusParts
	if statusTopic := e.execCtx.UserProps.A2AStatusTopic; statusTopic != "" {
		publish = e.statusPartsPublisher(statusTopic, e.execCtx.TaskID, e.execCtx.ContextID,
			e.execCtx.UserProps.TraceID, e.execCtx.UserProps.A2AUserConfig)
	}
	logger := e.instance.svc.Logger().With("logger_name", "workflow_tool_signal")
	return func(signal any) error {
		if _, isAuth := signal.(*a2a.AuthenticationRequiredData); isAuth {
			logger.Warn("tool requires interactive authorization, which a workflow node cannot service",
				"workflowName", e.cfg.Name, "nodeId", nodeID, "toolName", toolName)
			return tool.UnavailableInput(fmt.Errorf(tool.WorkflowNodeAuthRefusalFormat, toolName))
		}
		typed, ok := signal.(a2a.SignalTyper)
		if !ok {
			logger.Warn("dropping tool signal with no type", "workflowName", e.cfg.Name,
				"nodeId", nodeID, "toolName", toolName, "goType", fmt.Sprintf("%T", signal))
			return fmt.Errorf("workflow node cannot forward an untyped tool signal (%T)", signal)
		}
		signalType := typed.GetSignalType()
		if !workflowForwardableSignals[signalType] {
			logger.Warn("refusing to forward a tool signal that needs an awaiter this node lacks",
				"workflowName", e.cfg.Name, "nodeId", nodeID, "toolName", toolName, "signalType", signalType)
			return fmt.Errorf("workflow node cannot service the %q signal: no awaiter is wired for it", signalType)
		}
		if err := publish([]a2a.Part{a2a.NewDataPart(signal)}, map[string]any{
			"agent_name":    e.cfg.Name,
			"workflow_name": e.cfg.Name,
			"node_id":       nodeID,
			"sub_task_id":   subTaskID,
			"signal_type":   signalType,
		}); err != nil {
			logger.Warn("tool signal publish failed; its payload will not reach the UI",
				"workflowName", e.cfg.Name, "nodeId", nodeID, "toolName", toolName,
				"signalType", signalType, "error", err)
			return err
		}
		return nil
	}
}

func discardStatusParts([]a2a.Part, map[string]any) error { return nil }

const workflowHILDefaultTimeout = 45 * time.Minute

func pauseEventForResult(routing NodeCompletion, result *tool.Result) *EventNodePaused {
	if result == nil || result.Status != tool.StatusPending {
		return nil
	}
	requestID, _ := result.Data["hil_request_id"].(string)
	if requestID == "" {
		return nil
	}
	var surface json.RawMessage
	if s, ok := result.Data["surface"]; ok && s != nil {
		if b, err := json.Marshal(s); err == nil {
			surface = b
		}
	}
	return &EventNodePaused{
		NodeID:     routing.NodeID,
		SubTaskID:  routing.SubTaskID,
		RequestID:  requestID,
		Surface:    surface,
		ExpiresAt:  hilExpiresAtISO(result),
		MapNodeID:  routing.MapNodeID,
		LoopNodeID: routing.LoopNodeID,
		ItemIndex:  routing.ItemIndex,
	}
}

func hilExpiresAtISO(result *tool.Result) string {
	if s, ok := result.Data["expires_at"].(string); ok && s != "" {
		if _, err := time.Parse(time.RFC3339, s); err == nil {
			return s
		}
	}
	return time.Now().Add(workflowHILDefaultTimeout).UTC().Format(time.RFC3339)
}

func classifyToolResult(toolName string, result *tool.Result, dispatchErr error, saved []savedArtifact) (ErrorKind, map[string]any, error) {
	if dispatchErr != nil {
		return ErrorKindError, nil, fmt.Errorf("tool %q dispatch: %w", toolName, dispatchErr)
	}
	if result == nil {
		return ErrorKindError, nil, fmt.Errorf("tool %q returned nil result", toolName)
	}
	switch result.Status {
	case tool.StatusFailed:
		msg := result.Message
		if msg == "" && result.FailedDetail != nil {
			msg = result.FailedDetail.Message
		}
		if msg == "" {
			msg = "tool reported terminal failure"
		}
		return ErrorKindFailure, nil, fmt.Errorf("tool %q failed: %s", toolName, msg)
	case tool.StatusError:
		msg := result.Message
		if msg == "" && result.ValidationError != "" {
			msg = result.ValidationError
		}
		if msg == "" {
			msg = "tool reported error"
		}
		return ErrorKindError, nil, fmt.Errorf("tool %q error: %s", toolName, msg)
	case tool.StatusPending:
		detail := "interactive auth or HIL flows are not supported for tool nodes"
		if result.Message != "" {
			detail = detail + ": " + result.Message
		}
		return ErrorKindError, nil, fmt.Errorf("tool %q returned pending result (%s)", toolName, detail)
	}
	output := map[string]any{}
	if result.Data != nil {
		output = maps.Clone(result.Data)
	}
	if result.Message != "" {
		if _, ok := output["message"]; !ok {
			output["message"] = result.Message
		}
	}
	if result.Status != "" {
		if _, ok := output["status"]; !ok {
			output["status"] = result.Status
		}
	}
	if created, ok := soleCreatedArtifact(result, saved); ok {
		if _, taken := output["created_artifact"]; !taken {
			output["created_artifact"] = artifact.FormatVersionRef(created.name, created.version)
			setIfAbsent(output, "created_artifact_name", created.name)
			setIfAbsent(output, "created_artifact_version", created.version)
		}
	}
	return ErrorKindUnknown, output, nil
}

func setIfAbsent(m map[string]any, key string, value any) {
	if _, ok := m[key]; !ok {
		m[key] = value
	}
}

type savedArtifact struct {
	name    string
	version int
}

type savedArtifactCollector struct {
	mu    sync.Mutex
	saved []savedArtifact
}

func (c *savedArtifactCollector) record(_ context.Context, info tool.ArtifactSavedInfo) {
	if info.Filename == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saved = append(c.saved, savedArtifact{name: info.Filename, version: info.Version})
}

func (c *savedArtifactCollector) artifacts() []savedArtifact {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.saved)
}

func soleCreatedArtifact(result *tool.Result, saved []savedArtifact) (savedArtifact, bool) {
	if result == nil {
		return savedArtifact{}, false
	}
	all := slices.Clone(saved)
	for i := range result.DataObjects {
		ref := result.DataObjects[i].ArtifactRef
		if ref == nil || ref.Filename == "" {
			continue
		}
		all = append(all, savedArtifact{name: ref.Filename, version: ref.Version})
	}
	var created savedArtifact
	for i, a := range all {
		switch {
		case i == 0:
			created = a
		case a.name != created.name:
			return savedArtifact{}, false
		case a.version > created.version:
			created = a
		}
	}
	return created, len(all) > 0
}
