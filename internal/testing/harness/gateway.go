// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type CollectedEvent struct {
	Type         string
	Topic        string
	TaskID       string
	Payload      json.RawMessage
	Timestamp    time.Time
	RequestIndex int
}

type Gateway interface {
	Setup(ctx context.Context) error

	SendRequest(ctx context.Context, agentName string, input *GatewayInput) (string, error)

	SendCancel(ctx context.Context, agentName string, taskID string) error

	CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error)

	Cleanup()
}

var _ Gateway = (*TestGateway)(nil)

type OAuthCapableGateway interface {
	Gateway
	AuthRequiredChan() <-chan *a2a.AuthenticationRequiredData
	SendAuthResponse(ctx context.Context, agentName string, signal *a2a.AuthenticationRequiredData, code, csrfState string) error
}

var _ OAuthCapableGateway = (*TestGateway)(nil)

type TestGateway struct {
	gatewayID string
	namespace string
	broker    broker.Broker
	logger    *slog.Logger
	artSvc    *artifact.ScopedService

	responseQueue broker.Queue
	statusQueue   broker.Queue

	mu       sync.Mutex
	events   []CollectedEvent
	traceIDs []string
	taskIDs  []string
	done     chan struct{}

	hilRequestCh chan string

	authRequiredCh chan *a2a.AuthenticationRequiredData

	toolStartCh chan string

	lastUserID string
}

func NewTestGateway(gatewayID, namespace string, brk broker.Broker, logger *slog.Logger) *TestGateway {
	return &TestGateway{
		gatewayID:      gatewayID,
		namespace:      namespace,
		broker:         brk,
		logger:         logger,
		done:           make(chan struct{}),
		hilRequestCh:   make(chan string, 1),
		authRequiredCh: make(chan *a2a.AuthenticationRequiredData, 8),
		toolStartCh:    make(chan string, 16),
	}
}

func (g *TestGateway) SetArtifactService(artSvc *artifact.ScopedService) {
	g.artSvc = artSvc
}

func (g *TestGateway) Setup(ctx context.Context) error {
	var err error

	g.responseQueue, err = g.broker.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create response queue: %w", err)
	}
	responseSub := a2a.GatewayResponseSubscription(g.namespace, g.gatewayID)
	if subErr := g.broker.Subscribe(g.responseQueue, responseSub); subErr != nil {
		return fmt.Errorf("subscribe response: %w", subErr)
	}

	g.statusQueue, err = g.broker.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create status queue: %w", err)
	}
	statusSub := a2a.GatewayStatusSubscription(g.namespace, g.gatewayID)
	if subErr := g.broker.Subscribe(g.statusQueue, statusSub); subErr != nil {
		return fmt.Errorf("subscribe status: %w", subErr)
	}

	return nil
}

func (g *TestGateway) SendRequest(ctx context.Context, agentName string, input *GatewayInput) (string, error) {
	taskID := idgen.New()

	contextID := "test-session-" + taskID
	if input.ExternalContext != nil && input.ExternalContext.A2ASessionID != "" {
		contextID = input.ExternalContext.A2ASessionID
	}

	userID := "test-user"
	if input.UserIdentity != "" {
		userID = input.UserIdentity
	}
	g.mu.Lock()
	g.lastUserID = userID
	g.mu.Unlock()

	var a2aParts []a2a.Part
	for i, p := range input.GetParts() {
		switch p.Type {
		case "file":
			inputFilename, inputBase64 := resolveFilePartFields(p)
			filePart := a2a.Part{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     inputFilename,
					URI:      p.URI,
					MIMEType: p.MimeType,
				},
			}
			if filePart.File.MIMEType == "" && inputBase64 != "" && p.URI != "" {
				filePart.File.MIMEType = inputBase64
			}
			if p.URI == "" && inputBase64 != "" && g.artSvc != nil {
				content, err := base64.StdEncoding.DecodeString(inputBase64)
				if err != nil {
					g.logger.Warn("failed to decode file content_base64", "index", i, "error", err)
				} else {
					filename := inputFilename
					if filename == "" {
						filename = fmt.Sprintf("upload_%d", i)
					}
					mimeType := p.MimeType
					if mimeType == "" {
						mimeType = "application/octet-stream"
					}
					result, saveErr := artifact.SaveWithMetadata(
						ctx, g.artSvc,
						"", userID, contextID,
						filename, content, mimeType,
						artifact.MetadataOpts{
							Description: fmt.Sprintf("Uploaded file: %s", filename),
						},
						g.logger,
					)
					if saveErr != nil {
						g.logger.Error("failed to save uploaded file as artifact", "filename", filename, "error", saveErr)
					} else {
						loc := g.artSvc.Resolve("", userID, contextID, filename)
						filePart.File.URI = fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
							loc.AppName, loc.UserID, loc.SessionID, loc.Filename, result.DataVersion)
						filePart.File.MIMEType = mimeType
						filePart.File.Name = filename
						g.logger.Debug("converted uploaded file to artifact",
							"filename", filename, "version", result.DataVersion, "uri", filePart.File.URI)
					}
				}
			}
			a2aParts = append(a2aParts, filePart)
		case "data":
			if p.Data != nil {
				a2aParts = append(a2aParts, a2a.NewDataPart(p.Data))
			}
		default:
			if t := p.GetText(); t != "" {
				a2aParts = append(a2aParts, a2a.NewTextPart(t))
			}
		}
	}
	if len(a2aParts) == 0 {
		a2aParts = []a2a.Part{a2a.NewTextPart("")}
	}

	msgMetadata := input.MessageMetadata
	if len(input.InvokedWithArtifacts) > 0 {
		if msgMetadata == nil {
			msgMetadata = make(map[string]any, 1)
		}
		msgMetadata["invoked_with_artifacts"] = invokedArtifactsForMetadata(input.InvokedWithArtifacts)
	}

	a2aMsg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     a2aParts,
		ContextID: contextID,
		Metadata:  msgMetadata,
	}

	method := "message/send"
	if input.Streaming {
		method = "message/stream"
	}
	req := a2a.NewRequest(taskID, method, map[string]any{
		"message": a2aMsg,
	})

	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	responseTopic := a2a.GatewayResponseTopic(g.namespace, g.gatewayID, taskID)
	statusTopic := a2a.GatewayStatusTopic(g.namespace, g.gatewayID, taskID)
	if input.OmitStatusTopic {
		statusTopic = ""
	}

	up := &a2a.UserProperties{
		Timestamp:           time.Now().UnixMilli(),
		ClientID:            g.gatewayID,
		UserID:              userID,
		ReplyTo:             responseTopic,
		A2AStatusTopic:      statusTopic,
		GatewayCapabilities: input.GatewayCapabilities,
		TraceID:             idgen.New(),
		Trigger:             cmp.Or(input.Trigger, a2a.TriggerInteractive),
		Origin: a2a.Origin{
			Channel:        cmp.Or(input.Channel, httputil.SurfaceWebUI),
			UserUUID:       input.UserUUID,
			EntrypointUUID: input.EntrypointUUID,
		},
	}
	g.mu.Lock()
	g.traceIDs = append(g.traceIDs, up.TraceID)
	g.taskIDs = append(g.taskIDs, taskID)
	g.mu.Unlock()
	if input.Enterprise != nil {
		up.AuthToken = "test-enterprise-token"
	}
	props := a2a.EncodeUserProperties(up)

	topic := a2a.AgentRequestTopic(g.namespace, agentName)

	msg := &broker.Message{
		Payload:        payload,
		UserProperties: props,
	}

	if err := g.broker.Publish(ctx, topic, msg); err != nil {
		return "", fmt.Errorf("publish request: %w", err)
	}

	return taskID, nil
}

func (g *TestGateway) SendCancel(ctx context.Context, agentName string, taskID string) error {
	req := a2a.NewCancelTaskRequest(taskID)

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal cancel: %w", err)
	}

	topic := a2a.AgentRequestTopic(g.namespace, agentName)
	if pubErr := g.broker.Publish(ctx, topic, &broker.Message{
		Payload: payload,
		UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{
			Timestamp: time.Now().UnixMilli(),
			ClientID:  g.gatewayID,
		}),
	}); pubErr != nil {
		return fmt.Errorf("publish cancel: %w", pubErr)
	}
	return nil
}

func (g *TestGateway) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
	collectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var wg sync.WaitGroup

	wg.Go(func() {
		g.receiveLoop(collectCtx, g.responseQueue, "response", true)
	})

	wg.Go(func() {
		g.receiveLoop(collectCtx, g.statusQueue, "status", false)
	})

	gotResponse := false
	select {
	case <-g.done:
		gotResponse = true
		time.Sleep(50 * time.Millisecond)
		cancel()
	case <-collectCtx.Done():
	}

	wg.Wait()

	if gotResponse {
		for {
			drainCtx, drainCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			msg, err := g.broker.Receive(drainCtx, g.statusQueue)
			drainCancel()
			if err != nil || msg == nil {
				break
			}
			taskID := extractTaskIDFromTopic(msg.Topic)
			evt := CollectedEvent{
				Type:      "status",
				Topic:     msg.Topic,
				TaskID:    taskID,
				Payload:   json.RawMessage(msg.Payload),
				Timestamp: time.Now(),
			}
			g.mu.Lock()
			g.events = append(g.events, evt)
			g.mu.Unlock()
			msg.Ack()
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	events := make([]CollectedEvent, len(g.events))
	copy(events, g.events)

	if gotResponse {
		return events, nil
	}
	return events, collectCtx.Err()
}

func (g *TestGateway) receiveLoop(ctx context.Context, queue broker.Queue, eventType string, isFinal bool) {
	for {
		msg, err := g.broker.Receive(ctx, queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			g.logger.Warn("receive error", "type", eventType, "error", err)
			continue
		}
		if msg == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		now := time.Now()
		taskID := extractTaskIDFromTopic(msg.Topic)

		evt := CollectedEvent{
			Type:      eventType,
			Topic:     msg.Topic,
			TaskID:    taskID,
			Payload:   json.RawMessage(msg.Payload),
			Timestamp: now,
		}

		g.mu.Lock()
		g.events = append(g.events, evt)
		g.mu.Unlock()

		msg.Ack()

		if eventType == "status" {
			if reqID := extractHILRequestID(evt.Payload); reqID != "" {
				g.logger.Debug("detected user_input_request in status event", "requestID", reqID)
				select {
				case g.hilRequestCh <- reqID:
				default:
				}
			}
			if authData := extractAuthRequired(evt.Payload); authData != nil {
				g.logger.Debug("detected authentication_required in status event",
					"credentialKey", authData.CredentialKey, "targetAgent", authData.TargetAgent)
				select {
				case g.authRequiredCh <- authData:
				default:
				}
			}
			if planData := extractDeepResearchPlan(evt.Payload); planData != nil {
				g.publishDeepResearchPlanResponse(planData)
			}
			if toolName := extractToolInvocationStartName(evt.Payload); toolName != "" {
				select {
				case g.toolStartCh <- toolName:
				default:
				}
			}
		}

		if isFinal {
			select {
			case <-g.done:
			default:
				close(g.done)
			}
		}
	}
}

func (g *TestGateway) ResetForNextRequest() []CollectedEvent {
	g.mu.Lock()
	defer g.mu.Unlock()
	events := make([]CollectedEvent, len(g.events))
	copy(events, g.events)
	g.events = nil
	g.done = make(chan struct{})
	return events
}

func (g *TestGateway) AuthRequiredChan() <-chan *a2a.AuthenticationRequiredData {
	return g.authRequiredCh
}

func (g *TestGateway) ToolStartChan() <-chan string {
	return g.toolStartCh
}

func (g *TestGateway) Cleanup() {
	if g.responseQueue != nil {
		_ = g.broker.DeleteQueue(g.responseQueue.Name())
	}
	if g.statusQueue != nil {
		_ = g.broker.DeleteQueue(g.statusQueue.Name())
	}
}

func extractTaskIDFromTopic(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func invokedArtifactsForMetadata(refs []ArtifactRef) []any {
	out := make([]any, 0, len(refs))
	for _, r := range refs {
		out = append(out, map[string]any{
			"filename": r.Filename,
			"version":  r.Version,
		})
	}
	return out
}

func extractHILRequestID(payload json.RawMessage) string {
	var envelope struct {
		Result struct {
			Status struct {
				Message struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ""
	}
	for _, raw := range envelope.Result.Status.Message.Parts {
		var part struct {
			Kind string `json:"kind"`
			Data struct {
				Type      string `json:"type"`
				RequestID string `json:"request_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &part); err != nil {
			continue
		}
		if part.Kind == "data" && part.Data.Type == "user_input_request" && part.Data.RequestID != "" {
			return part.Data.RequestID
		}
	}
	return ""
}

func extractToolInvocationStartName(payload json.RawMessage) string {
	var envelope struct {
		Result struct {
			Status struct {
				Message struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ""
	}
	for _, raw := range envelope.Result.Status.Message.Parts {
		var part struct {
			Kind string `json:"kind"`
			Data struct {
				Type     string `json:"type"`
				ToolName string `json:"tool_name"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &part); err != nil || part.Kind != "data" {
			continue
		}
		if part.Data.Type == "tool_invocation_start" && part.Data.ToolName != "" {
			return part.Data.ToolName
		}
	}
	return ""
}

func extractAuthRequired(payload json.RawMessage) *a2a.AuthenticationRequiredData {
	var envelope struct {
		Result struct {
			Status struct {
				Message struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil
	}
	for _, raw := range envelope.Result.Status.Message.Parts {
		var part struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &part); err != nil || part.Kind != "data" {
			continue
		}
		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(part.Data, &typeCheck); err != nil || typeCheck.Type != "authentication_required" {
			continue
		}
		var authData a2a.AuthenticationRequiredData
		if err := json.Unmarshal(part.Data, &authData); err == nil {
			return &authData
		}
	}
	return nil
}

func extractDeepResearchPlan(payload json.RawMessage) *a2a.DeepResearchPlanData {
	var envelope struct {
		Result struct {
			Status struct {
				Message struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil
	}
	for _, raw := range envelope.Result.Status.Message.Parts {
		var part struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &part); err != nil || part.Kind != "data" {
			continue
		}
		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(part.Data, &typeCheck); err != nil || typeCheck.Type != "deep_research_plan" {
			continue
		}
		var planData a2a.DeepResearchPlanData
		if err := json.Unmarshal(part.Data, &planData); err == nil {
			return &planData
		}
	}
	return nil
}

func (g *TestGateway) publishDeepResearchPlanResponse(plan *a2a.DeepResearchPlanData) {
	g.logger.Info("auto-resolving deep_research plan-verification",
		"planID", plan.PlanID, "agent", plan.AgentName)
	topic := a2a.SamEventsTopic(g.namespace, "deep_research", "plan_response")
	envelope := map[string]any{
		"event_type":       "plan_response",
		"source_component": g.gatewayID + "_gateway",
		"data": map[string]any{
			"plan_id":    plan.PlanID,
			"agent_name": plan.AgentName,
			"user_id":    g.userIDForPlanResolve(plan.PlanID),
			"action":     "start",
		},
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		g.logger.Warn("failed to marshal plan_response envelope", "error", err)
		return
	}
	if err := g.broker.Publish(context.Background(), topic, &broker.Message{
		Payload: data,
		UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{
			Timestamp: time.Now().UnixMilli(),
			ClientID:  g.gatewayID,
		}),
	}); err != nil {
		g.logger.Warn("failed to publish plan_response", "error", err)
	}
}

func (g *TestGateway) userIDForPlanResolve(_planID string) string {
	return g.requesterUserID()
}

func (g *TestGateway) requesterUserID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lastUserID != "" {
		return g.lastUserID
	}
	return "test-user"
}

func (g *TestGateway) SendAuthResponse(ctx context.Context, agentName string, signal *a2a.AuthenticationRequiredData, code, csrfState string) error {
	authResp := a2a.AuthResponseData{
		Type:              "auth_response",
		TargetAgent:       signal.TargetAgent,
		TargetAgentTaskID: signal.TargetAgentTaskID,
		UserID:            signal.UserID,
		ClientID:          signal.ClientID,
		FunctionCallID:    signal.AuthRequestFunctionCallID,
		Code:              code,
		CredentialKey:     signal.CredentialKey,
		CodeVerifier:      signal.CodeVerifier,
		State:             csrfState,
		IsCredentialSave:  true,
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewDataPart(authResp)},
	}

	taskID := idgen.New()
	req := a2a.NewRequest(taskID, "message/send", map[string]any{
		"message": msg,
	})

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal auth response: %w", err)
	}

	topic := a2a.AgentRequestTopic(g.namespace, agentName)
	return g.broker.Publish(ctx, topic, &broker.Message{
		Payload: payload,
		UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{
			Timestamp: time.Now().UnixMilli(),
			ClientID:  g.gatewayID,
			UserID:    signal.UserID,
		}),
	})
}

func (g *TestGateway) SendHILResponse(ctx context.Context, agentName, taskID, requestID string, resp *HILResponse) error {
	responseData := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: requestID,
		Status:    resp.Status,
		Data:      resp.Data,
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewDataPart(responseData)},
	}

	req := a2a.NewRequest(taskID, "message/send", map[string]any{
		"message": msg,
	})

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal HIL response: %w", err)
	}

	topic := a2a.AgentRequestTopic(g.namespace, agentName)
	return g.broker.Publish(ctx, topic, &broker.Message{
		Payload: payload,
		UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{
			Timestamp: time.Now().UnixMilli(),
			ClientID:  g.gatewayID,
			UserID:    g.requesterUserID(),
		}),
	})
}

func (g *TestGateway) TaskIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.taskIDs)
}

func (g *TestGateway) TraceIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.traceIDs)
}
