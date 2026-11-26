// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/email"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	sessionBehaviorRunBased = "RUN_BASED"
	msgUnreadableRequest    = "Invalid request: no readable message"
)

type requestFacts struct {
	message           a2a.Message
	structured        *a2a.StructuredInvocationRequest
	runBased          bool
	displaysTemplates bool
}

func (a *Adapter) readRequest(params json.RawMessage) (requestFacts, bool) {
	var p struct {
		Message *a2a.Message `json:"message"`
	}
	if json.Unmarshal(params, &p) != nil || p.Message == nil {
		return requestFacts{}, false
	}
	msg := *p.Message
	behavior, _ := msg.Metadata["sessionBehavior"].(string)
	displays, _ := msg.Metadata[a2a.MetadataDisplaysTemplateSignals].(bool)
	invocation := a.invocations.ExtractRequest(msg)
	return requestFacts{
		message:           msg,
		structured:        invocation,
		runBased:          invocation == nil && behavior == sessionBehaviorRunBased,
		displaysTemplates: displays,
	}, true
}

func sharedMetadata(metadata map[string]any) map[string]any {
	shared := map[string]any{}
	if name, ok := metadata["agent_name"]; ok {
		shared["agent_name"] = name
	}
	if emailAuth, ok := metadata[email.EmailAuthMetadataKey].(map[string]any); ok {
		kept := map[string]any{}
		for k, v := range emailAuth {
			if k != "internal_trust_tenant_id" {
				kept[k] = v
			}
		}
		shared[email.EmailAuthMetadataKey] = kept
	}
	return shared
}

func taskFor(facts requestFacts, props a2a.UserProperties, rc agent.RequestContext, shareProfile bool, grantedToolScopes []string) *taskObject {
	msg := facts.message
	task := &taskObject{
		ContextID:         msg.ContextID,
		Files:             []taskFile{},
		SessionBehavior:   sessionSession,
		User:              taskUser{ID: props.UserID},
		CallDepth:         props.CallDepth,
		Trigger:           props.Trigger,
		TraceID:           props.TraceID,
		CalledBy:          props.DelegatingAgentName,
		CanPromptUser:     canPromptUser(props),
		GrantedToolScopes: grantedToolScopes,
		RequestContext: taskRequestCtx{
			SystemPurpose:       rc.SystemPurpose,
			ResponseFormat:      rc.ResponseFormat,
			ProjectID:           rc.ProjectID,
			ConversationContext: rc.ConversationContext,
			UserProfile:         rc.UserProfile,
			UserTimezone:        rc.UserTimezone,
		},
		Metadata: sharedMetadata(msg.Metadata),
	}
	if !shareProfile {
		task.RequestContext.UserProfile = ""
	}
	if facts.runBased {
		task.SessionBehavior = sessionRun
	}
	inputPart := -1
	if facts.structured != nil {
		task.SessionBehavior = sessionRun
		task.Workflow = &taskWorkflow{Name: facts.structured.WorkflowName, NodeID: facts.structured.NodeID}
		inputPart = structured.InputFilePartIndex(msg, facts.structured.InputSchema)
		if input, ok := structuredInput(msg, inputPart); ok {
			task.Input = input
		} else {
			inputPart = -1
		}
	}
	var text []string
	for i, part := range msg.Parts {
		switch {
		case part.Kind == "text":
			text = append(text, part.Text)
		case part.Kind == "file" && part.File != nil && i != inputPart:
			task.Files = append(task.Files, taskFile{Name: part.File.Name, MimeType: part.File.MIMEType, Bytes: part.File.Bytes, URI: part.File.URI})
		}
	}
	task.Text = strings.Join(text, "\n")
	return task
}

func canPromptUser(props a2a.UserProperties) bool {
	if props.A2AStatusTopic == "" {
		return false
	}
	interactive, said := props.GatewayCapabilities[tool.CapInteractiveUserInput]
	return interactive || !said
}

func structuredInput(msg a2a.Message, index int) (any, bool) {
	if index < 0 || msg.Parts[index].File.Bytes == "" {
		return nil, false
	}
	var input any
	if err := json.Unmarshal(structured.DecodeFileBytes(msg.Parts[index].File.Bytes), &input); err != nil {
		return nil, false
	}
	return input, true
}

func (s *agentState) mayRun(ctx context.Context, identity *auth.Identity, requiredScopes []string) bool {
	if len(requiredScopes) == 0 {
		return true
	}
	return s.a.core.Services().AgentAuthorizer().AuthorizeAgentScopes(ctx, identity, requiredScopes) == nil
}

func (s *agentState) grantedToolScopes(ctx context.Context, identity *auth.Identity, toolScopes []string) []string {
	granted := []string{}
	if len(toolScopes) == 0 {
		return granted
	}
	perScope := make([]auth.ToolInfo, 0, len(toolScopes))
	for _, scope := range toolScopes {
		perScope = append(perScope, auth.ToolInfo{Name: scope, RequiredScopes: []string{scope}})
	}
	for _, allowed := range s.a.core.Services().ToolAuthorizer().FilterTools(ctx, identity, perScope) {
		granted = append(granted, allowed.Name)
	}
	return granted
}

func (s *agentState) dropUntakeable(ctx context.Context, req jsonrpcEnvelope, props a2a.UserProperties, answerTo string, m *broker.Message) {
	if answerTo != "" {
		level, reason := s.droppedAnswerLevel(req.ID)
		s.a.logger.Log(ctx, level, droppedAnAnswerToAClosedPrompt, "agentName", s.cfg.Name, "taskID", req.ID,
			"traceID", props.TraceID, "requestID", boundedRef(answerTo), "reason", reason)
		m.Ack()
		return
	}
	s.a.logger.Warn("agent link refused a task without a readable message", "agentName", s.cfg.Name, "taskID", req.ID, "traceID", props.TraceID)
	s.refuseAndSettle(ctx, req.ID, props, msgUnreadableRequest, m)
}

func (s *agentState) droppedAnswerLevel(taskID string) (slog.Level, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch b := s.tasks[taskID]; {
	case b == nil || b.final:
		return slog.LevelDebug, answerDroppedTaskNotOpen
	case b.redelivered:
		return slog.LevelDebug, answerDroppedTaskResumed
	default:
		return slog.LevelWarn, answerDroppedNoSuchPrompt
	}
}

const (
	msgNotAnOpenTask      = "not an open task of this agent"
	msgTaskBoundElsewhere = "the task is bound to another connection of this agent that is still open"

	droppedAnAnswerToAClosedPrompt = "agent link dropped an answer to a prompt that is not open"
	answerDroppedTaskNotOpen       = "the task is not open on this replica"
	answerDroppedTaskResumed       = "the task was delivered again after the prompt's replica handed over"
	answerDroppedNoSuchPrompt      = "the task has no such prompt open"

	refusedATaskFrame   = "agent link refused a frame for a task"
	boundATaskToItsNext = "agent link bound a task to the connection that sent for it: the one it was bound to is gone"

	taskAbsent         = "absent"
	taskFinal          = "final"
	taskExpired        = "expired"
	taskBoundElsewhere = "bound elsewhere"
)

func (s *agentState) openTaskLocked(from *conn, frameType, taskID string) (open *taskBinding, refusal string) {
	b := s.tasks[taskID]
	switch {
	case b == nil:
		return nil, s.refusedTaskFrame(from, frameType, taskID, taskAbsent, "")
	case b.final:
		return nil, s.refusedTaskFrame(from, frameType, taskID, taskFinal, "")
	case s.a.now().After(b.deadline):
		return nil, s.refusedTaskFrame(from, frameType, taskID, taskExpired, "")
	case b.connID == from.id:
		return b, ""
	}
	if bound := s.conns[b.connID]; bound != nil && !bound.isClosed() {
		return nil, s.refusedTaskFrame(from, frameType, taskID, taskBoundElsewhere, b.connID)
	}
	s.a.logger.Info(boundATaskToItsNext, "agentName", s.cfg.Name, "taskID", taskID, "connectionID", from.id, "earlierConnectionID", b.connID)
	b.connID = from.id
	return b, ""
}

func (s *agentState) refusedTaskFrame(from *conn, frameType, taskID, reason, boundConnID string) (refusal string) {
	level, refusal := slog.LevelDebug, msgNotAnOpenTask
	said := []any{"agentName", s.cfg.Name, "taskID", boundedRef(taskID), "connectionID", from.id, "frameType", frameType, "reason", reason}
	if reason == taskBoundElsewhere {
		refusal, said = msgTaskBoundElsewhere, append(said, "boundConnectionID", boundConnID)
		if frameType != typeEvent {
			level = slog.LevelWarn
		}
	}
	s.a.logger.Log(s.a.ctx, level, refusedATaskFrame, said...)
	return refusal
}

func (s *agentState) openTasksBoundTo(connID string) (bound int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.tasks {
		if !b.final && b.connID == connID {
			bound++
		}
	}
	return bound
}

func (s *agentState) handleFrame(c *conn, f inFrame) {
	if !s.takesFrameFrom(c, f) {
		return
	}
	defer s.frameHandled()
	ctx := s.ctx
	switch f.Type {
	case typeConsume:
		s.mu.Lock()
		described, refused := s.described != nil, c.describeRefused
		c.consuming = c.consuming || described || refused
		if described {
			s.awaitingConnect, s.wakeFailed = false, false
		}
		s.mu.Unlock()
		if refused {
			c.refuseFrame(typeConsume, errCodeBadMessage, "this connection's describe was refused: tasks are delivered once a describe from it is accepted")
			return
		}
		if !described {
			c.refuseFrame(typeConsume, errCodeBadMessage, "send describe before consume: the agent's required scopes are checked from it")
			return
		}
		s.ensureDispatch()
		s.signalCapacity()
	case typeAck, typeNack:
		s.settle(c, f)
	case typeDescribe:
		s.handleDescribe(ctx, c, f)
	case typeRetire:
		s.handleRetire(ctx, c)
	case typeEvent:
		s.handleEvent(ctx, c, f)
	case typePrompt:
		s.handlePrompt(ctx, c, f)
	case typeResult:
		s.handleResult(ctx, c, f)
	case typePeerCall:
		s.handlePeerCall(ctx, c, f)
	case typePeerCancel:
		s.handlePeerCancel(c, f)
	default:
		c.sendError(errCodeUnknownType, "unknown message type", "")
	}
}

func (s *agentState) releaseTask(taskID string) {
	s.releaseTaskWithin(context.Background(), taskID)
}

func (s *agentState) releaseTaskWithin(ctx context.Context, taskID string) {
	s.mu.Lock()
	var prompts, calls []string
	for id, p := range s.asks {
		if p.taskID == taskID {
			prompts = append(prompts, id)
		}
	}
	for _, b := range s.subtasks {
		if b.parent == taskID && !b.cancelled {
			b.cancelled = true
			calls = append(calls, b.peerTaskID)
		}
	}
	s.mu.Unlock()
	for _, id := range prompts {
		s.closePrompt(id, answerCanceled)
	}
	for _, id := range calls {
		s.router.CancelWithPropagationWithin(ctx, id, s.ns())
	}
}

func (s *agentState) endTask(taskID string) {
	s.endTaskWithin(context.Background(), taskID)
}

func (s *agentState) endTaskWithin(ctx context.Context, taskID string) {
	s.releaseTaskWithin(ctx, taskID)
	s.seq.Drop(taskID)
	s.mu.Lock()
	for key, h := range s.held {
		if h.taskID == taskID {
			s.dropHeldLocked(key)
		}
	}
	if b := s.tasks[taskID]; b != nil && b.final {
		b.forgetRequest()
	}
	s.mu.Unlock()
}

func (b *taskBinding) forgetRequest() {
	b.identity = nil
	kept := make(map[string]string, len(b.props))
	for k, v := range b.props {
		if k != a2a.PropAuthToken && k != a2a.PropA2AUserConfig {
			kept[k] = v
		}
	}
	b.props = kept
	b.request = requestFacts{
		message:           a2a.Message{ContextID: b.request.message.ContextID},
		runBased:          b.request.runBased,
		displaysTemplates: b.request.displaysTemplates,
	}
}
