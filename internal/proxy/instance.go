// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const healthKeyRemoteFetch = "remote:fetch"

type ProxyInstance struct {
	cfg      *ProxyConfig
	svc      runtime.Services
	logger   *slog.Logger
	adapter  ProtocolAdapter
	tasks    *TaskStore
	queue    broker.Queue
	cancelFn context.CancelFunc
	started  chan struct{}

	cachedCard         atomic.Pointer[a2a.AgentCard]
	cardHealthy        atomic.Bool
	cardRefused        atomic.Bool
	unreachableFetches atomic.Int32
	cardRetryInterval  time.Duration

	health health.State

	instanceID string

	requestsInFlight atomic.Int64
	intakeStopped    atomic.Bool
	handingOff       atomic.Bool
	replaceDrain     time.Duration
}

const defaultReplaceDrain = 20 * time.Second

func NewInstance(cfg config.Config) (*ProxyInstance, error) {
	name := cfg.GetString("name")
	appCfg := cfg.GetSection("app_config")
	if appCfg == nil {
		return nil, fmt.Errorf("proxy %q: missing 'app_config' section", name)
	}

	pc, err := ParseProxyConfig(name, appCfg, slog.Default().With("proxy", name))
	if err != nil {
		return nil, err
	}

	return NewInstanceFromConfig(pc), nil
}

func NewInstanceFromConfig(pc *ProxyConfig) *ProxyInstance {
	return &ProxyInstance{
		cfg:               pc,
		tasks:             NewTaskStore(),
		instanceID:        idgen.New(),
		cardRetryInterval: defaultCardRetryInterval,
		replaceDrain:      defaultReplaceDrain,
	}
}

func (p *ProxyInstance) SetAdapter(adapter ProtocolAdapter) {
	p.adapter = adapter
}

func (p *ProxyInstance) Name() string { return p.cfg.Name }

func (p *ProxyInstance) DisplayName() string { return p.cfg.DisplayName }

func (p *ProxyInstance) Kind() string { return "proxy" }

func (p *ProxyInstance) Init(_ context.Context, _ config.Config, svc runtime.Services) error {
	p.svc = svc
	p.logger = slog.Default().With("logger_name", "proxy", "proxy", p.cfg.Name)

	if p.adapter == nil {
		if newDefaultAdapter == nil {
			return fmt.Errorf("no protocol adapter registered (import proxy/a2a)")
		}
		adapter, err := newDefaultAdapter(p.cfg, p.logger)
		if err != nil {
			return fmt.Errorf("create protocol adapter: %w", err)
		}
		p.adapter = adapter
	}

	return nil
}

var newDefaultAdapter func(cfg *ProxyConfig, logger *slog.Logger) (ProtocolAdapter, error)

func RegisterDefaultAdapter(factory func(cfg *ProxyConfig, logger *slog.Logger) (ProtocolAdapter, error)) {
	newDefaultAdapter = factory
}

func (p *ProxyInstance) Start(ctx context.Context) error {
	p.health.Reset()
	p.cardRefused.Store(false)
	p.unreachableFetches.Store(0)
	p.cachedCard.Store(nil)
	p.intakeStopped.Store(false)
	p.handingOff.Store(false)

	ctx, cancel := context.WithCancel(ctx)
	p.cancelFn = cancel

	q, err := p.svc.Broker().CreateQueue(a2a.QueueName(p.cfg.Namespace, "proxy", p.cfg.Name), broker.QueueOptions{
		Named:         true,
		MaxRedelivery: 1,
		RespectTTL:    true,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("create queue: %w", err)
	}
	p.queue = q

	topic := a2a.AgentRequestTopic(p.cfg.Namespace, p.cfg.Name)
	if err := p.svc.Broker().Subscribe(q, topic); err != nil {
		_ = p.svc.Broker().DeleteQueue(q.Name())
		cancel()
		return fmt.Errorf("subscribe: %w", err)
	}

	started := make(chan struct{})
	p.started = started
	go func() {
		defer close(started)
		if err := p.fetchAndPublishCard(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			p.logger.Warn("initial card fetch failed (will retry)", "error", err)
			p.cardHealthy.Store(false)
			p.setCardFetchFailed(err)
		} else {
			p.cardHealthy.Store(true)
		}
		p.startDiscoveryTimer(ctx)
		p.startCleanupTimer(ctx)
		go p.receiveLoop(ctx, q)
	}()
	return nil
}

func (p *ProxyInstance) Stop(_ context.Context) error {
	if p.cancelFn != nil {
		p.cancelFn()
	}
	detachedCtx, cancelPub := context.WithTimeout(context.Background(), 2*time.Second)
	p.publishRemovalCard(detachedCtx)
	cancelPub()
	if p.queue != nil && p.svc != nil {
		if brk := p.svc.Broker(); brk != nil {
			if err := brk.DeleteQueue(p.queue.Name()); err != nil && p.logger != nil {
				p.logger.Warn("delete proxy queue failed", "queue", p.queue.Name(), "error", err)
			}
		}
	}
	if p.adapter != nil {
		return p.adapter.Close()
	}
	return nil
}

func (p *ProxyInstance) StopForReplace(ctx context.Context) error {
	p.intakeStopped.Store(true)
	drainCtx, cancel := context.WithTimeout(ctx, p.replaceDrain)
	remaining := p.waitForRequests(drainCtx)
	cancel()
	if remaining > 0 && p.logger != nil {
		p.logger.Warn("replace deadline reached; leaving running requests unacknowledged for the new copy",
			"remaining", remaining)
	}
	p.handingOff.Store(true)
	return p.Stop(ctx)
}

func (p *ProxyInstance) waitForRequests(ctx context.Context) int64 {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		n := p.requestsInFlight.Load()
		if n == 0 {
			return 0
		}
		select {
		case <-ctx.Done():
			return n
		case <-ticker.C:
		}
	}
}

func (p *ProxyInstance) cutByReplace(ctx context.Context) bool {
	return p.handingOff.Load() && ctx.Err() != nil
}

func (p *ProxyInstance) Remove(ctx context.Context) error {
	requestQueueName := ""
	if p.queue != nil {
		requestQueueName = p.queue.Name()
	} else if p.cfg != nil {
		requestQueueName = a2a.QueueName(p.cfg.Namespace, "proxy", p.cfg.Name)
	}

	stopErr := p.Stop(ctx)

	if requestQueueName != "" && p.svc != nil {
		if brk := p.svc.Broker(); brk != nil {
			if err := brk.DeprovisionQueue(requestQueueName); err != nil && p.logger != nil {
				p.logger.Warn("deprovision proxy queue failed",
					"queue", requestQueueName, "error", err)
			}
		}
	}
	return stopErr
}

func (p *ProxyInstance) publishRemovalCard(ctx context.Context) {
	if p.svc == nil || p.svc.Broker() == nil || p.cfg == nil {
		return
	}
	card := a2a.AgentCard{
		Name: p.cfg.Name,
		URL:  "solace:" + a2a.AgentRequestTopic(p.cfg.Namespace, p.cfg.Name),
	}
	data, err := json.Marshal(card)
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("failed to marshal removal card", "error", err)
		}
		return
	}
	topic := a2a.DiscoveryAgentCardsTopic(p.cfg.Namespace)
	if err := p.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"removed": "true", "instance_id": p.instanceID},
	}); err != nil {
		if p.logger != nil {
			p.logger.Warn("failed to publish proxy removal card", "topic", topic, "error", err)
		}
	}
}

func (p *ProxyInstance) Problems() []health.Problem {
	return p.health.Snapshot()
}

func (p *ProxyInstance) CachedCard() *a2a.AgentCard {
	return p.cachedCard.Load()
}

func (p *ProxyInstance) Health() error {
	if !p.cardHealthy.Load() {
		return fmt.Errorf("agent card not available: %w", runtime.ErrDegraded)
	}
	return nil
}

func (p *ProxyInstance) receiveLoop(ctx context.Context, q broker.Queue) {
	broker.RunReceiveLoop(ctx, p.svc.Broker(), q, p.logger, broker.ReceiveLoopOptions{
		Health: &p.health,
	}, func(msg *broker.Message) {
		p.routeMessage(ctx, msg)
	})
}

func (p *ProxyInstance) routeMessage(ctx context.Context, msg *broker.Message) {
	if p.intakeStopped.Load() {
		return
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		msg.Ack()
		return
	}

	switch req.Method {
	case "message/send", "message/stream":
		if p.isAuthResponse(&req) {
			go p.handleAuthResponse(ctx, msg, &req)
		} else {
			p.requestsInFlight.Add(1)
			go func() {
				defer p.requestsInFlight.Add(-1)
				p.handleRequest(ctx, msg, &req)
			}()
		}
	case "tasks/cancel":
		go p.handleCancel(ctx, msg, &req)
	default:
		msg.Ack()
	}
}

func (p *ProxyInstance) handleRequest(ctx context.Context, msg *broker.Message, req *a2a.Request) {
	defer func() {
		if !p.cutByReplace(ctx) {
			msg.Ack()
		}
	}()

	props := a2a.DecodeUserProperties(msg.UserProperties)
	isStreaming := req.Method == "message/stream"

	tc := &TaskContext{
		TaskID:         req.ID,
		SessionID:      p.extractSessionID(req),
		TraceID:        props.TraceID,
		ReplyToTopic:   props.ReplyTo,
		StatusTopic:    props.A2AStatusTopic,
		UserID:         props.UserID,
		ClientID:       props.ClientID,
		IsStreaming:    isStreaming,
		UserProperties: msg.UserProperties,
		CreatedAt:      time.Now(),
		Method:         req.Method,
	}
	p.tasks.Add(tc)

	if p.refuseUntrustedTask(ctx, tc) {
		return
	}

	p.resolveInboundArtifacts(ctx, req)

	if authCfg := p.cfg.EffectiveTaskAuth(); authCfg != nil && authCfg.Type == "oauth2_authorization_code" {
		token, err := p.getOAuth2AuthCodeToken(ctx, tc)
		if err != nil {
			if rbac.IsNonDelegatablePrincipal(tc.UserID) {
				p.logger.Info("no-delegation principal; skipping proxy auth prompt",
					"userID", tc.UserID, "taskID", tc.TaskID, "proxy", p.cfg.Name)
				p.publishErrorResponse(ctx, tc, -32603, toolauth.NoDelegationMessage(p.cfg.Name))
				p.tasks.Remove(tc.TaskID)
				return
			}
			if errors.Is(err, errSignInProviderUnavailable) {
				p.publishErrorResponse(ctx, tc, -32603, toolauth.SignInProviderUnavailableMessage(p.cfg.Name))
				p.tasks.Remove(tc.TaskID)
				return
			}
			p.triggerOAuth2AuthFlow(ctx, tc, req)
			return
		}
		tc.OAuth2Token = token
	}

	p.forwardAndProcess(ctx, tc, req)
}

func (p *ProxyInstance) forwardAndProcess(ctx context.Context, tc *TaskContext, req *a2a.Request) {
	if p.refuseUntrustedTask(ctx, tc) {
		return
	}
	monitor := observability.NewRemoteAgentProxyMonitor(cmp.Or(p.cfg.DisplayName, p.cfg.Name))

	events, err := p.adapter.ForwardRequest(ctx, tc, req)
	if err != nil {
		monitor.Stop(&err)
		p.logger.Error("forward request failed", "taskID", tc.TaskID, "error", err)
		p.publishErrorResponse(ctx, tc, -32603, fmt.Sprintf("proxy error: %v", err))
		p.tasks.Remove(tc.TaskID)
		return
	}

	p.processResponseEvents(ctx, tc, events, monitor)
}

func (p *ProxyInstance) processResponseEvents(ctx context.Context, tc *TaskContext, events <-chan ResponseEvent, monitor *observability.Monitor) {
	for evt := range events {
		if evt.Error != nil {
			monitor.Stop(&evt.Error)
			p.logger.Error("downstream error", "taskID", tc.TaskID, "error", evt.Error)
			p.publishErrorResponse(ctx, tc, -32603, fmt.Sprintf("downstream error: %v", evt.Error))
			p.tasks.Remove(tc.TaskID)
			return
		}

		if evt.StatusUpdate != nil {
			if tc.DownstreamTaskID == "" && evt.StatusUpdate.TaskID != "" {
				tc.DownstreamTaskID = evt.StatusUpdate.TaskID
				p.tasks.IndexDownstreamID(tc.TaskID, tc.DownstreamTaskID)
			}
			isFinal := evt.StatusUpdate.Final
			finalState := evt.StatusUpdate.Status.State
			if p.cfg.ConvertProgressUpdates && evt.StatusUpdate.Status.Message != nil && !isFinal {
				p.convertToProgressUpdate(evt.StatusUpdate)
			}
			p.publishStatusUpdate(ctx, tc, evt.StatusUpdate)
			if isFinal {
				stopMonitorForTerminalState(monitor, finalState)
				return
			}
		}

		if evt.ArtifactUpdate != nil {
			if tc.DownstreamTaskID == "" && evt.ArtifactUpdate.TaskID != "" {
				tc.DownstreamTaskID = evt.ArtifactUpdate.TaskID
				p.tasks.IndexDownstreamID(tc.TaskID, tc.DownstreamTaskID)
			}
			p.handleArtifactUpdate(ctx, tc, evt.ArtifactUpdate)
		}

		if evt.Task != nil {
			if tc.DownstreamTaskID == "" && evt.Task.ID != "" {
				tc.DownstreamTaskID = evt.Task.ID
				p.tasks.IndexDownstreamID(tc.TaskID, tc.DownstreamTaskID)
			}
			if endsTurn(evt.Task.Status.State) {
				stopMonitorForTerminalState(monitor, evt.Task.Status.State)
				p.handleFinalTask(ctx, tc, evt.Task)
				p.tasks.Remove(tc.TaskID)
				return
			}
		}
	}

	monitor.StopWithErrorType("stream_disconnected")
	p.logger.Warn("downstream agent disconnected without completing task", "taskID", tc.TaskID, "traceID", tc.TraceID)
	p.publishErrorResponse(ctx, tc, -32603, "downstream agent disconnected without completing the task")
	p.tasks.Remove(tc.TaskID)
}

func endsTurn(s a2a.TaskState) bool {
	switch s {
	case a2a.TaskStateCompleted, a2a.TaskStateFailed, a2a.TaskStateCancelled,
		a2a.TaskStateRejected, a2a.TaskStateInputNeeded, a2a.TaskStateAuthRequired:
		return true
	default:
		return false
	}
}

func stopMonitorForTerminalState(monitor *observability.Monitor, state a2a.TaskState) {
	if state == a2a.TaskStateFailed {
		monitor.StopWithErrorType("task_failed")
	} else {
		monitor.Stop(nil)
	}
}

func (p *ProxyInstance) handleCancel(ctx context.Context, msg *broker.Message, req *a2a.Request) {
	defer msg.Ack()

	tc := p.tasks.Get(req.ID)
	if tc == nil {
		return
	}

	if err := p.adapter.CancelTask(ctx, tc); err != nil {
		p.logger.Warn("cancel forwarding failed", "taskID", req.ID, "error", err)
	}

	p.tasks.Remove(tc.TaskID)
}

func (p *ProxyInstance) publishStatusUpdate(ctx context.Context, tc *TaskContext, su *a2a.TaskStatusUpdateEvent) {
	su.TaskID = tc.TaskID
	su.ContextID = tc.SessionID

	if su.Final {
		task := &a2a.Task{
			ID:        tc.TaskID,
			ContextID: tc.SessionID,
			Kind:      "task",
			Status:    su.Status,
			Metadata:  su.Metadata,
		}
		remapStatusMessageIDs(tc, task.Status.Message)
		foldStreamedArtifactTextIntoFinal(tc, task)
		p.setAgentNameMetadata(task)
		p.publishFinalResponse(ctx, tc, task)
		p.tasks.Remove(tc.TaskID)
		return
	}

	if tc.StatusTopic == "" {
		return
	}

	data, err := json.Marshal(a2a.NewResponse(tc.TaskID, su))
	if err != nil {
		p.logger.Error("marshal status update", "taskID", tc.TaskID, "traceID", tc.TraceID, "error", err)
		return
	}

	userProps := p.buildResponseUserProps(tc)
	if pubErr := p.svc.Broker().Publish(ctx, tc.StatusTopic, &broker.Message{
		Payload:        data,
		UserProperties: userProps,
	}); pubErr != nil {
		p.logger.Error("publish status update", "taskID", tc.TaskID, "traceID", tc.TraceID, "error", pubErr)
	}
}

func (p *ProxyInstance) handleArtifactUpdate(ctx context.Context, tc *TaskContext, au *a2a.TaskArtifactUpdateEvent) {
	au.TaskID = tc.TaskID
	au.ContextID = tc.SessionID

	p.processOutboundArtifacts(ctx, tc, []a2a.Artifact{au.Artifact})

	if isTextOnlyArtifact(au.Artifact) {
		var textParts []string
		for _, part := range au.Artifact.Parts {
			if part.Kind == "text" && part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
		textContent := strings.Join(textParts, "\n")

		if textContent != "" {
			tc.addStreamedArtifactText(au.Artifact.ArtifactID, textContent, au.Append)
		}

		su := &a2a.TaskStatusUpdateEvent{
			TaskID:    tc.TaskID,
			ContextID: tc.SessionID,
			Kind:      "status-update",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateWorking,
				Message: &a2a.Message{
					MessageID: idgen.New(),
					Role:      "agent",
					Parts:     []a2a.Part{{Kind: "text", Text: textContent}},
					TaskID:    tc.TaskID,
					ContextID: tc.SessionID,
				},
			},
			Final:    false,
			Metadata: au.Metadata,
		}
		p.publishStatusUpdate(ctx, tc, su)
		return
	}

	if tc.StatusTopic == "" {
		return
	}

	data, err := json.Marshal(a2a.NewResponse(tc.TaskID, au))
	if err != nil {
		p.logger.Error("marshal artifact update", "taskID", tc.TaskID, "traceID", tc.TraceID, "error", err)
		return
	}

	userProps := p.buildResponseUserProps(tc)
	if pubErr := p.svc.Broker().Publish(ctx, tc.StatusTopic, &broker.Message{
		Payload:        data,
		UserProperties: userProps,
	}); pubErr != nil {
		p.logger.Error("publish artifact update", "taskID", tc.TaskID, "traceID", tc.TraceID, "error", pubErr)
	}
}

func remapStatusMessageIDs(tc *TaskContext, msg *a2a.Message) {
	if msg == nil {
		return
	}
	msg.TaskID = tc.TaskID
	msg.ContextID = tc.SessionID
	if msg.MessageID == "" {
		msg.MessageID = idgen.New()
	}
}

func (p *ProxyInstance) handleFinalTask(ctx context.Context, tc *TaskContext, task *a2a.Task) {
	task.ID = tc.TaskID
	task.ContextID = tc.SessionID

	remapStatusMessageIDs(tc, task.Status.Message)

	if len(task.Artifacts) > 0 {
		manifest := p.processOutboundArtifacts(ctx, tc, task.Artifacts)
		if len(manifest) > 0 {
			if task.Metadata == nil {
				task.Metadata = make(map[string]any)
			}
			task.Metadata["produced_artifacts"] = manifest
		}
	}

	p.consolidateTextArtifacts(task)

	foldStreamedArtifactTextIntoFinal(tc, task)

	p.setAgentNameMetadata(task)

	p.publishFinalResponse(ctx, tc, task)
}

func (p *ProxyInstance) publishFinalResponse(ctx context.Context, tc *TaskContext, task *a2a.Task) {
	if tc.ReplyToTopic == "" {
		p.logger.Warn("no replyTo topic, dropping response", "taskID", tc.TaskID)
		return
	}

	resp := a2a.NewResponse(tc.TaskID, task)
	data, err := json.Marshal(resp)
	if err != nil {
		p.logger.Error("marshal response", "taskID", tc.TaskID, "traceID", tc.TraceID, "error", err)
		return
	}

	userProps := p.buildResponseUserProps(tc)
	if pubErr := p.svc.Broker().Publish(ctx, tc.ReplyToTopic, &broker.Message{
		Payload:        data,
		UserProperties: userProps,
	}); pubErr != nil {
		p.logger.Error("publish response", "taskID", tc.TaskID, "traceID", tc.TraceID, "error", pubErr)
	}
}

func (p *ProxyInstance) publishErrorResponse(ctx context.Context, tc *TaskContext, code int, message string) {
	if tc.ReplyToTopic == "" || p.cutByReplace(ctx) {
		return
	}

	resp := a2a.NewErrorResponse(tc.TaskID, code, message)
	data, err := json.Marshal(resp)
	if err != nil {
		p.logger.Error("marshal error response", "taskID", tc.TaskID, "traceID", tc.TraceID, "error", err)
		return
	}

	userProps := p.buildResponseUserProps(tc)
	if pubErr := p.svc.Broker().Publish(ctx, tc.ReplyToTopic, &broker.Message{
		Payload:        data,
		UserProperties: userProps,
	}); pubErr != nil {
		p.logger.Error("publish error response", "taskID", tc.TaskID, "traceID", tc.TraceID, "error", pubErr)
	}
}

func (p *ProxyInstance) buildResponseUserProps(tc *TaskContext) map[string]string {
	props := map[string]string{
		"delegating_agent_name": p.cfg.Name,
		"timestamp":             strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
	if tc.ClientID != "" {
		props["clientId"] = tc.ClientID
	}
	if tc.UserID != "" {
		props["userId"] = tc.UserID
	}
	if tc.UserProperties != nil {
		if v, ok := tc.UserProperties["a2aUserConfig"]; ok {
			props["a2aUserConfig"] = v
		}
		if v, ok := tc.UserProperties["authToken"]; ok && v != "" {
			props["authToken"] = v
		}
	}
	return props
}

func (p *ProxyInstance) extractSessionID(req *a2a.Request) string {
	if m, ok := req.Params.(map[string]any); ok {
		if msg, ok := m["message"].(map[string]any); ok {
			if cid, ok := msg["contextId"].(string); ok {
				return cid
			}
		}
	}
	return ""
}

func (p *ProxyInstance) convertToProgressUpdate(su *a2a.TaskStatusUpdateEvent) {
	if su.Status.Message == nil {
		return
	}
	var converted []a2a.Part
	for _, part := range su.Status.Message.Parts {
		if part.Kind == "text" && part.Text != "" {
			converted = append(converted, a2a.NewDataPart(a2a.AgentProgressUpdateData{
				Type:       string(a2a.SignalAgentProgressUpdate),
				StatusText: part.Text,
			}))
		} else {
			converted = append(converted, part)
		}
	}
	su.Status.Message.Parts = converted
}

func (p *ProxyInstance) resolveInboundArtifacts(ctx context.Context, req *a2a.Request) {
	artSvc := p.svc.Artifacts()
	if artSvc == nil {
		return
	}

	params, ok := req.Params.(map[string]any)
	if !ok {
		return
	}
	msg, ok := params["message"].(map[string]any)
	if !ok {
		return
	}
	parts, ok := msg["parts"].([]any)
	if !ok {
		return
	}

	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if part["kind"] != "file" {
			continue
		}
		file, ok := part["file"].(map[string]any)
		if !ok {
			continue
		}
		uriStr, _ := file["uri"].(string)
		if !strings.HasPrefix(uriStr, "artifact://") {
			continue
		}

		content, mimeType, err := p.loadArtifactByURI(ctx, artSvc, uriStr)
		if err != nil {
			p.logger.Warn("failed to resolve inbound artifact URI",
				"uriLength", len(uriStr), "error", err)
			continue
		}

		delete(file, "uri")
		file["bytes"] = base64.StdEncoding.EncodeToString(content)
		if mimeType != "" && file["mimeType"] == nil {
			file["mimeType"] = mimeType
		}
	}
}

func (p *ProxyInstance) loadArtifactByURI(ctx context.Context, artSvc *artifact.ScopedService, uri string) ([]byte, string, error) {
	ref, version, ok := artifact.ParseURI(uri)
	if !ok || ref.AppName == "" {
		return nil, "", errors.New("artifact URI is not a location: it needs an app name, user, session and file name, and a numeric version if it has one")
	}

	loc := artSvc.Resolve(ref.AppName, ref.UserID, ref.SessionID, ref.Filename)
	content, mimeType, err := artSvc.Inner.Load(ctx, loc, version)
	if err != nil {
		return nil, "", fmt.Errorf("load artifact %q: %w", ref.Filename, err)
	}
	if content == nil {
		return nil, "", fmt.Errorf("artifact %q not found", ref.Filename)
	}

	return content, mimeType, nil
}

func (p *ProxyInstance) processOutboundArtifacts(ctx context.Context, tc *TaskContext, artifacts []a2a.Artifact) []map[string]any {
	artSvc := p.svc.Artifacts()
	if artSvc == nil {
		return nil
	}

	var manifest []map[string]any

	for ai := range artifacts {
		art := &artifacts[ai]

		var textDesc []string
		for _, part := range art.Parts {
			if part.Kind == "text" && part.Text != "" {
				textDesc = append(textDesc, part.Text)
			}
		}

		for pi := range art.Parts {
			part := &art.Parts[pi]
			if part.Kind != "file" || part.File == nil || part.File.Bytes == "" {
				continue
			}
			if part.File.URI != "" {
				continue
			}

			content, err := base64.StdEncoding.DecodeString(part.File.Bytes)
			if err != nil {
				p.logger.Warn("failed to decode outbound artifact bytes",
					"artifactID", art.ArtifactID, "error", err)
				continue
			}

			filename := part.File.Name
			if filename == "" {
				filename = fmt.Sprintf("artifact_%s", art.ArtifactID)
			}
			mimeType := part.File.MIMEType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			desc := art.Description
			if desc == "" && len(textDesc) > 0 {
				desc = strings.Join(textDesc, "\n")
			}
			if desc == "" {
				desc = fmt.Sprintf("Artifact created by %s", p.cfg.Name)
			}

			custom := map[string]any{
				"proxied_from_artifact_id": art.ArtifactID,
			}

			result, saveErr := artifact.SaveWithMetadata(
				ctx, artSvc,
				p.cfg.Name, tc.UserID, tc.SessionID,
				filename, content, mimeType,
				artifact.MetadataOpts{
					Description: desc,
					Custom:      custom,
				},
				p.logger,
			)
			if saveErr != nil {
				p.logger.Error("failed to save outbound artifact",
					"filename", filename, "error", saveErr)
				continue
			}

			loc := artSvc.Resolve(p.cfg.Name, tc.UserID, tc.SessionID, filename)
			savedURI := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
				loc.AppName, loc.UserID, loc.SessionID, loc.Filename, result.DataVersion)

			part.File.URI = savedURI
			part.File.Bytes = ""

			manifest = append(manifest, map[string]any{
				"filename": filename,
				"version":  result.DataVersion,
			})

			p.logger.Debug("saved outbound artifact",
				"filename", filename, "version", result.DataVersion, "uri", savedURI)
		}
	}

	return manifest
}

func (p *ProxyInstance) consolidateTextArtifacts(task *a2a.Task) {
	if len(task.Artifacts) == 0 {
		return
	}

	var remaining []a2a.Artifact
	var textContent []string

	for _, art := range task.Artifacts {
		if isTextOnlyArtifact(art) {
			for _, part := range art.Parts {
				if part.Kind == "text" && part.Text != "" {
					textContent = append(textContent, part.Text)
				}
			}
		} else {
			remaining = append(remaining, art)
		}
	}

	if len(textContent) == 0 {
		return
	}

	p.logger.Debug("consolidating text-only artifacts",
		"count", len(task.Artifacts)-len(remaining))

	if len(remaining) > 0 {
		task.Artifacts = remaining
	} else {
		task.Artifacts = nil
	}

	consolidated := "The following text-only artifacts were returned and have been consolidated into this message:\n\n---\n\n" +
		strings.Join(textContent, "\n")
	summaryPart := a2a.Part{Kind: "text", Text: consolidated}

	if task.Status.Message == nil {
		task.Status.Message = &a2a.Message{
			MessageID: idgen.New(),
			Role:      "agent",
			Parts:     []a2a.Part{summaryPart},
			TaskID:    task.ID,
			ContextID: task.ContextID,
		}
	} else {
		task.Status.Message.Parts = append(task.Status.Message.Parts, summaryPart)
	}
}

func foldStreamedArtifactTextIntoFinal(tc *TaskContext, task *a2a.Task) {
	if len(tc.streamedArtifactText) == 0 || taskStatusHasText(task) {
		return
	}

	if task.Status.State == a2a.TaskStateInputNeeded || task.Status.State == a2a.TaskStateAuthRequired {
		return
	}

	part := a2a.Part{Kind: "text", Text: strings.Join(tc.streamedArtifactText, "\n")}
	if task.Status.Message == nil {
		task.Status.Message = &a2a.Message{
			MessageID: idgen.New(),
			Role:      "agent",
			Parts:     []a2a.Part{part},
			TaskID:    tc.TaskID,
			ContextID: tc.SessionID,
		}
		return
	}
	task.Status.Message.Parts = append(task.Status.Message.Parts, part)
}

func taskStatusHasText(task *a2a.Task) bool {
	if task.Status.Message == nil {
		return false
	}
	for _, part := range task.Status.Message.Parts {
		if part.Kind == "text" && part.Text != "" {
			return true
		}
	}
	return false
}

func isTextOnlyArtifact(art a2a.Artifact) bool {
	if len(art.Parts) == 0 {
		return false
	}
	for _, part := range art.Parts {
		if part.Kind != "text" {
			return false
		}
	}
	return true
}

func (p *ProxyInstance) setAgentNameMetadata(task *a2a.Task) {
	if task.Metadata == nil {
		task.Metadata = make(map[string]any)
	}
	task.Metadata["agent_name"] = p.cfg.Name
}

func (p *ProxyInstance) isAuthResponse(req *a2a.Request) bool {
	params, ok := req.Params.(map[string]any)
	if !ok {
		return false
	}
	msg, ok := params["message"].(map[string]any)
	if !ok {
		return false
	}
	parts, ok := msg["parts"].([]any)
	if !ok {
		return false
	}
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if part["kind"] != "data" {
			continue
		}
		data, ok := part["data"].(map[string]any)
		if !ok {
			continue
		}
		if t, ok := data["type"].(string); ok && t == string(a2a.SignalAuthResponse) {
			return true
		}
	}
	return false
}

func (p *ProxyInstance) extractAuthResponse(req *a2a.Request) *a2a.AuthResponseData {
	params, ok := req.Params.(map[string]any)
	if !ok {
		return nil
	}
	msg, ok := params["message"].(map[string]any)
	if !ok {
		return nil
	}
	parts, ok := msg["parts"].([]any)
	if !ok {
		return nil
	}
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if part["kind"] != "data" {
			continue
		}
		data, ok := part["data"].(map[string]any)
		if !ok {
			continue
		}
		if t, ok := data["type"].(string); ok && t == string(a2a.SignalAuthResponse) {
			raw, err := json.Marshal(data)
			if err != nil {
				return nil
			}
			var authResp a2a.AuthResponseData
			if err := json.Unmarshal(raw, &authResp); err != nil {
				return nil
			}
			return &authResp
		}
	}
	return nil
}

var errSignInProviderUnavailable = errors.New("sign-in provider unavailable")

func (p *ProxyInstance) getOAuth2AuthCodeToken(ctx context.Context, tc *TaskContext) (string, error) {
	credSvc := p.svc.CredentialService()
	if credSvc == nil {
		return "", fmt.Errorf("no credential service available")
	}

	credKey := "a2a_proxy_" + p.cfg.Name
	tc.CredentialKey = credKey

	raw, err := credSvc.Get(ctx, p.cfg.Name, p.cfg.Name, tc.UserID, credKey)
	if err != nil || raw == "" {
		return "", fmt.Errorf("no stored token")
	}

	var token toolauth.OAuthToken
	if err := json.Unmarshal([]byte(raw), &token); err != nil {
		return "", fmt.Errorf("deserialize stored token: %w", err)
	}

	if !token.IsExpired() {
		return token.AccessToken, nil
	}

	if token.RefreshToken != "" {
		scheme := p.buildOAuthScheme()
		cred := p.buildOAuthCred()
		refreshed, err := toolauth.RefreshToken(ctx, scheme, cred, token.RefreshToken)
		if err == nil {
			data, _ := json.Marshal(refreshed)
			_ = credSvc.Set(ctx, p.cfg.Name, p.cfg.Name, tc.UserID, credKey, string(data))
			return refreshed.AccessToken, nil
		}
		if toolauth.IsTransientRefreshFailure(err) {
			p.logger.Warn("OAuth2 token refresh failed due to transient error, preserving credential",
				append([]any{"taskID", tc.TaskID}, toolauth.RefreshFailureLogAttrs(err)...)...)
			return "", fmt.Errorf("%w: %w", errSignInProviderUnavailable, err)
		}
		p.logger.Warn("OAuth2 token refresh failed, re-auth required",
			append([]any{"taskID", tc.TaskID}, toolauth.RefreshFailureLogAttrs(err)...)...)
		_ = credSvc.Delete(ctx, p.cfg.Name, p.cfg.Name, tc.UserID, credKey)
	}

	return "", fmt.Errorf("token expired")
}

func (p *ProxyInstance) triggerOAuth2AuthFlow(ctx context.Context, tc *TaskContext, req *a2a.Request) {
	authCfg := p.cfg.EffectiveTaskAuth()

	verifier := toolauth.GenerateVerifier()
	state := toolauth.GenerateState()

	scheme := p.buildOAuthScheme()
	redirectURI := authCfg.RedirectURI
	if redirectURI == "" {
		redirectURI = toolauth.ResolveRedirectURI()
	}

	authURI, err := toolauth.BuildAuthorizationURL(scheme, authCfg.ClientID, state, verifier, redirectURI)
	if err != nil {
		p.logger.Error("build authorization URL failed", "error", err)
		p.publishErrorResponse(ctx, tc, -32603, fmt.Sprintf("internal error: %v", err))
		p.tasks.Remove(tc.TaskID)
		return
	}

	credKey := "a2a_proxy_" + p.cfg.Name

	tc.OriginalRequest = req
	tc.CodeVerifier = verifier
	tc.CredentialKey = credKey

	authData := a2a.AuthenticationRequiredData{
		Type:                      string(a2a.SignalAuthenticationRequired),
		TargetAgent:               p.cfg.Name,
		TargetAgentTaskID:         tc.TaskID,
		UserID:                    tc.UserID,
		ClientID:                  tc.ClientID,
		AuthURI:                   authURI,
		AuthRequestFunctionCallID: tc.TaskID,
		CredentialKey:             credKey,
		CodeVerifier:              verifier,
	}

	p.publishAuthRequired(ctx, tc, &authData)
	p.logger.Info("OAuth2 auth flow triggered, task paused", "taskID", tc.TaskID)
}

func (p *ProxyInstance) handleAuthResponse(ctx context.Context, msg *broker.Message, req *a2a.Request) {
	defer msg.Ack()

	authResp := p.extractAuthResponse(req)
	if authResp == nil {
		p.logger.Warn("received auth_response but could not extract data")
		return
	}

	tc := p.tasks.Get(authResp.TargetAgentTaskID)
	if tc == nil {
		p.logger.Warn("auth_response for unknown/expired task", "taskID", authResp.TargetAgentTaskID)
		return
	}

	if sender := a2a.DecodeUserProperties(msg.UserProperties).UserID; !artifact.SameOwner(tc.UserID, sender) {
		p.logger.Warn("auth_response refused: sender does not own the paused task",
			"taskID", tc.TaskID, "userID", sender)
		return
	}

	scheme := p.buildOAuthScheme()
	cred := p.buildOAuthCred()

	authCfg := p.cfg.EffectiveTaskAuth()
	redirectURI := authCfg.RedirectURI
	if redirectURI == "" {
		redirectURI = toolauth.ResolveRedirectURI()
	}

	codeVerifier := authResp.CodeVerifier
	if codeVerifier == "" {
		codeVerifier = tc.CodeVerifier
	}

	token, err := toolauth.ExchangeCode(ctx, scheme, cred, authResp.Code, codeVerifier, redirectURI)
	if err != nil {
		p.logger.Error("OAuth2 token exchange failed", "taskID", tc.TaskID, "error", err)
		p.publishErrorResponse(ctx, tc, -32603, fmt.Sprintf("OAuth2 token exchange failed: %v", err))
		p.tasks.Remove(tc.TaskID)
		return
	}

	credSvc := p.svc.CredentialService()
	if credSvc != nil {
		data, _ := json.Marshal(token)
		if storeErr := credSvc.Set(ctx, p.cfg.Name, p.cfg.Name, tc.UserID, tc.CredentialKey, string(data)); storeErr != nil {
			p.logger.Warn("failed to store OAuth2 token", "error", storeErr)
		}
	}

	tc.OAuth2Token = token.AccessToken
	originalReq := tc.OriginalRequest
	if originalReq == nil {
		p.publishErrorResponse(ctx, tc, -32603, "no original request stored for resumption")
		p.tasks.Remove(tc.TaskID)
		return
	}
	tc.OriginalRequest = nil

	p.logger.Info("OAuth2 auth complete, resuming request", "taskID", tc.TaskID)
	p.forwardAndProcess(ctx, tc, originalReq)
}

func (p *ProxyInstance) publishAuthRequired(ctx context.Context, tc *TaskContext, authData *a2a.AuthenticationRequiredData) {
	if tc.StatusTopic == "" {
		p.logger.Warn("no status topic for auth required signal", "taskID", tc.TaskID)
		return
	}

	event := a2a.TaskStatusUpdateEvent{
		TaskID:    tc.TaskID,
		ContextID: tc.SessionID,
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{
				MessageID: idgen.New(),
				Role:      "agent",
				Parts:     []a2a.Part{a2a.NewDataPart(authData)},
			},
		},
		Metadata: map[string]any{"agent_name": p.cfg.Name},
	}

	data, err := json.Marshal(a2a.NewResponse(tc.TaskID, event))
	if err != nil {
		p.logger.Error("marshal auth required event", "error", err)
		return
	}

	userProps := p.buildResponseUserProps(tc)
	if pubErr := p.svc.Broker().Publish(ctx, tc.StatusTopic, &broker.Message{
		Payload:        data,
		UserProperties: userProps,
	}); pubErr != nil {
		p.logger.Error("publish auth required", "error", pubErr)
	}
}

func (p *ProxyInstance) buildOAuthScheme() *toolauth.OAuthScheme {
	auth := p.cfg.EffectiveTaskAuth()
	if auth == nil {
		return nil
	}
	return &toolauth.OAuthScheme{
		AuthorizationURL:        auth.AuthorizationURL,
		TokenURL:                auth.TokenURL,
		Scopes:                  auth.Scopes,
		TokenEndpointAuthMethod: auth.TokenEndpointAuthMethod,
	}
}

func (p *ProxyInstance) buildOAuthCred() *toolauth.AuthCredential {
	auth := p.cfg.EffectiveTaskAuth()
	if auth == nil {
		return nil
	}
	return &toolauth.AuthCredential{
		ClientID:     auth.ClientID,
		ClientSecret: auth.ClientSecret,
	}
}

func (p *ProxyInstance) fetchAndPublishCard(ctx context.Context) error {
	card, err := p.adapter.FetchAgentCard(ctx)
	if err != nil {
		return err
	}
	if card == nil {
		return fmt.Errorf("adapter returned nil card")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	p.health.Clear(healthKeyRemoteFetch)
	p.cardRefused.Store(false)

	p.rewriteCard(card)
	p.cachedCard.Store(card)

	return p.publishCard(ctx)
}

func (p *ProxyInstance) rewriteCard(card *a2a.AgentCard) {
	card.Name = p.cfg.Name
	card.URL = "solace:" + a2a.AgentRequestTopic(p.cfg.Namespace, p.cfg.Name)

	hasAgentType := false
	for i, ext := range card.Capabilities.Extensions {
		if ext.URI == "https://solace.com/a2a/extensions/agent-type" {
			card.Capabilities.Extensions[i].Params = map[string]any{"type": "proxy"}
			hasAgentType = true
			break
		}
	}
	if !hasAgentType {
		card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
			URI:    "https://solace.com/a2a/extensions/agent-type",
			Params: map[string]any{"type": "proxy"},
		})
	}

	if p.cfg.DisplayName != "" {
		hasDisplayName := false
		for i, ext := range card.Capabilities.Extensions {
			if ext.URI == "https://solace.com/a2a/extensions/display-name" {
				card.Capabilities.Extensions[i].Params = map[string]any{"display_name": p.cfg.DisplayName}
				hasDisplayName = true
				break
			}
		}
		if !hasDisplayName {
			card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
				URI:    "https://solace.com/a2a/extensions/display-name",
				Params: map[string]any{"display_name": p.cfg.DisplayName},
			})
		}
	}

	if card.Version == "" {
		card.Version = "1.0.0"
	}
	if card.ProtocolVersion == "" {
		card.ProtocolVersion = "0.3.0"
	}
	if len(card.DefaultInputModes) == 0 {
		card.DefaultInputModes = []string{"text"}
	}
	if len(card.DefaultOutputModes) == 0 {
		card.DefaultOutputModes = []string{"text"}
	}
	if card.Skills == nil {
		card.Skills = []a2a.AgentSkill{}
	}

	p.applyHealthExtension(card)
}

func (p *ProxyInstance) applyHealthExtension(card *a2a.AgentCard) {
	idx := -1
	for i, ext := range card.Capabilities.Extensions {
		if ext.URI == a2a.ExtensionURIHealth {
			idx = i
			break
		}
	}
	problems := p.health.Snapshot()
	switch {
	case len(problems) == 0 && idx >= 0:
		card.Capabilities.Extensions = append(card.Capabilities.Extensions[:idx], card.Capabilities.Extensions[idx+1:]...)
	case len(problems) > 0 && idx >= 0:
		card.Capabilities.Extensions[idx] = a2a.HealthProblemsExtension(problems)
	case len(problems) > 0:
		card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.HealthProblemsExtension(problems))
	}
}

func (p *ProxyInstance) setCardFetchFailed(err error) {
	switch {
	case errors.Is(err, ErrAgentCardUntrusted), p.cardRefused.Load():
		p.cardRefused.Store(true)
		p.health.Set(healthKeyRemoteFetch, health.Degraded, health.DetailRemoteCardUntrusted)
	case errors.Is(err, ErrAgentCardKeysUnavailable):
		p.health.Set(healthKeyRemoteFetch, health.Degraded, health.DetailRemoteCardKeysUnavailable)
	default:
		p.health.Set(healthKeyRemoteFetch, health.Degraded, health.DetailRemoteFetchFailed)
	}
}

func (p *ProxyInstance) refuseUntrustedTask(ctx context.Context, tc *TaskContext) bool {
	if p.forwardsTasks() {
		return false
	}
	name := cmp.Or(p.cfg.DisplayName, p.cfg.Name)
	message := fmt.Sprintf("%s's agent card failed its signature check, so the task was not sent to it.", name)
	if !p.cardRefused.Load() {
		message = fmt.Sprintf("%s has no agent card with a verified signature yet, so the task was not sent to it.", name)
	}
	p.logger.Warn("task not forwarded: the remote agent has no card that passed its signature policy",
		"taskID", tc.TaskID, "traceID", tc.TraceID, "cardRefused", p.cardRefused.Load())
	p.publishErrorResponse(ctx, tc, -32603, message)
	p.tasks.Remove(tc.TaskID)
	return true
}

func (p *ProxyInstance) forwardsTasks() bool {
	if p.cardRefused.Load() {
		return false
	}
	return !p.cfg.RequireSignedCard || p.cachedCard.Load() != nil
}

func (p *ProxyInstance) republishCachedCardHealth(ctx context.Context) {
	cached := p.cachedCard.Load()
	if cached == nil {
		return
	}
	updated := *cached
	updated.Capabilities.Extensions = append([]a2a.AgentExtension(nil), cached.Capabilities.Extensions...)
	if p.cardRefused.Load() {
		a2a.SetRemoteProtocolCardSignature(&updated, a2a.CardSignatureRefused)
	}
	p.applyHealthExtension(&updated)
	p.cachedCard.Store(&updated)
	if err := p.publishCard(ctx); err != nil {
		p.logger.Warn("failed to republish card with updated health", "error", err)
	}
}

func (p *ProxyInstance) retryCardUntilFetched(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(cmp.Or(p.cardRetryInterval, defaultCardRetryInterval))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if p.refreshCard(ctx, "card fetch retry failed") {
					return
				}
			}
		}
	}()
}

func (p *ProxyInstance) refreshCard(ctx context.Context, failedMsg string) bool {
	if err := p.fetchAndPublishCard(ctx); err != nil {
		if ctx.Err() != nil {
			return false
		}
		p.logger.Warn(failedMsg, "error", err)
		p.cardHealthy.Store(false)
		p.setCardFetchFailed(err)
		if p.republishesAfterFailedFetch(err) {
			p.republishCachedCardHealth(ctx)
		}
		return false
	}
	p.unreachableFetches.Store(0)
	p.cardHealthy.Store(true)
	return true
}

const maxRepublishedUnreachableFetches = 3

func (p *ProxyInstance) republishesAfterFailedFetch(err error) bool {
	answered := errors.Is(err, ErrAgentCardUntrusted) ||
		errors.Is(err, ErrAgentCardKeysUnavailable)
	if answered {
		p.unreachableFetches.Store(0)
		return true
	}
	return p.unreachableFetches.Add(1) <= maxRepublishedUnreachableFetches
}

func (p *ProxyInstance) publishCard(ctx context.Context) error {
	card := p.cachedCard.Load()
	if card == nil {
		return fmt.Errorf("no card to publish")
	}

	data, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}

	topic := a2a.DiscoveryAgentCardsTopic(p.cfg.Namespace)
	if pubErr := p.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"instance_id": p.instanceID},
	}); pubErr != nil {
		return fmt.Errorf("publish card: %w", pubErr)
	}

	p.logger.Debug("published agent card", "topic", topic)
	return nil
}

const defaultCardRetryInterval = 30 * time.Second

const cardPublishWarmupDuration = 3 * time.Minute
const cardPublishWarmupMaxInterval = 10 * time.Second

func (p *ProxyInstance) startDiscoveryTimer(ctx context.Context) {
	interval := p.cfg.DiscoveryIntervalSeconds
	if interval <= 0 {
		if !p.cardHealthy.Load() {
			p.retryCardUntilFetched(ctx)
		}
		return
	}

	normalInterval := time.Duration(interval) * time.Second
	warmupInterval := normalInterval
	if warmupInterval > cardPublishWarmupMaxInterval {
		warmupInterval = cardPublishWarmupMaxInterval
	}

	go func() {
		warmupDeadline := time.Now().Add(cardPublishWarmupDuration)
		ticker := time.NewTicker(warmupInterval)
		defer ticker.Stop()
		inWarmup := warmupInterval != normalInterval

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.refreshCard(ctx, "periodic card fetch failed")
				if inWarmup && time.Now().After(warmupDeadline) {
					ticker.Reset(normalInterval)
					inWarmup = false
				}
			}
		}
	}()
}

func (p *ProxyInstance) startCleanupTimer(ctx context.Context) {
	if p.cfg.TaskCleanupIntervalMinutes <= 0 {
		return
	}

	interval := time.Duration(p.cfg.TaskCleanupIntervalMinutes) * time.Minute
	ttl := time.Duration(p.cfg.TaskStateTTLMinutes) * time.Minute

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if removed := p.tasks.CleanupStale(ttl); removed > 0 {
					p.logger.Debug("cleaned up stale tasks", "count", removed)
				}
			}
		}
	}()
}
