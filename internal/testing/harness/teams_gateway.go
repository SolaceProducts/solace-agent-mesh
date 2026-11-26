// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/teams"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type TeamsTestFileAttachment struct {
	Name        string `yaml:"name"`
	ContentType string `yaml:"content_type"`
	Content     string `yaml:"content"`
}

type TeamsTestConfig struct {
	DefaultAgent     string                    `yaml:"default_agent"`
	ConversationID   string                    `yaml:"conversation_id"`
	ConversationType string                    `yaml:"conversation_type"`
	UserID           string                    `yaml:"user_id"`
	UserName         string                    `yaml:"user_name"`
	FileAttachments  []TeamsTestFileAttachment `yaml:"file_attachments"`
}

type TeamsTestGateway struct {
	brk       broker.Broker
	namespace string
	gatewayID string
	logger    *slog.Logger
	svc       runtime.Services

	adapter    *teams.TeamsAdapter
	core       *entrypoint.GatewayCore
	mockServer *MockTeamsServer

	teamsConfig *TeamsTestConfig

	responseQueue broker.Queue
	statusQueue   broker.Queue
	mu            sync.Mutex
	events        []CollectedEvent
	done          chan struct{}
}

func NewTeamsTestGateway(
	brk broker.Broker,
	teamsCfg *TeamsTestConfig,
	namespace, gatewayID string,
	logger *slog.Logger,
) *TeamsTestGateway {
	if teamsCfg == nil {
		teamsCfg = &TeamsTestConfig{}
	}
	return &TeamsTestGateway{
		brk:         brk,
		namespace:   namespace,
		gatewayID:   gatewayID,
		logger:      logger,
		teamsConfig: teamsCfg,
		done:        make(chan struct{}),
	}
}

func (g *TeamsTestGateway) SetServices(svc runtime.Services) {
	g.svc = svc
}

func (g *TeamsTestGateway) Setup(ctx context.Context) error {
	g.mockServer = NewMockTeamsServer()

	adapterConfig := map[string]any{
		"app_id":                        "test-app-id",
		"app_password":                  "test-app-password",
		"default_agent_name":            g.teamsConfig.DefaultAgent,
		"initial_status_message":        "Processing your request...",
		"api_base_url":                  g.mockServer.URL(),
		"http_host":                     "127.0.0.1",
		"http_port":                     0,
		"teams_email_cache_ttl_seconds": 0,
	}

	appConfig := map[string]any{
		"namespace":  g.namespace,
		"gateway_id": g.gatewayID,
		"gateway_adapter": map[string]any{
			"type":           "teams",
			"adapter_config": adapterConfig,
		},
	}

	adapter, err := teams.NewTeamsAdapter(adapterConfig)
	if err != nil {
		return fmt.Errorf("create teams adapter: %w", err)
	}
	g.adapter = adapter

	g.core = entrypoint.NewGatewayCore(adapter, appConfig)
	g.core.SetServices(g.svc)

	if initErr := g.core.Init(ctx, nil); initErr != nil {
		return fmt.Errorf("teams gateway init: %w", initErr)
	}

	g.responseQueue, err = g.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create response queue: %w", err)
	}
	responseSub := a2a.GatewayResponseSubscription(g.namespace, g.gatewayID)
	if subErr := g.brk.Subscribe(g.responseQueue, responseSub); subErr != nil {
		return fmt.Errorf("subscribe response: %w", subErr)
	}

	g.statusQueue, err = g.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create status queue: %w", err)
	}
	statusSub := a2a.GatewayStatusSubscription(g.namespace, g.gatewayID)
	if err := g.brk.Subscribe(g.statusQueue, statusSub); err != nil {
		return fmt.Errorf("subscribe status: %w", err)
	}

	if err := g.core.Start(ctx); err != nil {
		return fmt.Errorf("teams gateway start: %w", err)
	}

	return nil
}

func (g *TeamsTestGateway) SendRequest(ctx context.Context, agentName string, input *GatewayInput) (string, error) {
	userID := g.teamsConfig.UserID
	if userID == "" {
		userID = "teams-test-user"
	}
	if input.UserIdentity != "" {
		userID = input.UserIdentity
	}
	userName := g.teamsConfig.UserName
	if userName == "" {
		userName = "Test User"
	}
	conversationID := g.teamsConfig.ConversationID
	if conversationID == "" {
		conversationID = "test-conversation-1"
	}
	conversationType := g.teamsConfig.ConversationType
	if conversationType == "" {
		conversationType = "personal"
	}

	var text string
	for _, p := range input.GetParts() {
		if t := p.GetText(); t != "" {
			text = t
			break
		}
	}

	activity := map[string]any{
		"type":       "message",
		"id":         fmt.Sprintf("test-activity-%d", time.Now().UnixNano()),
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"channelId":  "msteams",
		"serviceUrl": g.mockServer.URL(),
		"from": map[string]any{
			"id":   userID,
			"name": userName,
		},
		"conversation": map[string]any{
			"id":               conversationID,
			"conversationType": conversationType,
		},
		"recipient": map[string]any{
			"id":   "test-app-id",
			"name": "TestBot",
		},
		"text": text,
	}

	if len(g.teamsConfig.FileAttachments) > 0 {
		var attachments []map[string]any
		for _, fa := range g.teamsConfig.FileAttachments {
			downloadURL := g.mockServer.AddFile(fa.Name, fa.Content, fa.ContentType)
			attachments = append(attachments, map[string]any{
				"contentType": "application/vnd.microsoft.teams.file.download.info",
				"name":        fa.Name,
				"content": map[string]any{
					"downloadUrl": downloadURL,
					"fileType":    strings.TrimPrefix(filepath.Ext(fa.Name), "."),
				},
			})
		}
		activity["attachments"] = attachments
	}

	if conversationType != "personal" {
		activity["entities"] = []map[string]any{
			{
				"type": "mention",
				"mentioned": map[string]any{
					"id":   "test-app-id",
					"name": "TestBot",
				},
				"text": "<at>TestBot</at>",
			},
		}
		activity["text"] = fmt.Sprintf("<at>TestBot</at> %s", text)
	}

	activityJSON, _ := json.Marshal(activity)

	_ = activityJSON

	g.adapter.HandleTestActivity(ctx, activity)

	taskID := g.discoverTaskID(500 * time.Millisecond)
	return taskID, nil
}

func (g *TeamsTestGateway) discoverTaskID(timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	msg, err := g.brk.Receive(ctx, g.statusQueue)
	if err == nil && msg != nil {
		taskID := extractTaskIDFromTopic(msg.Topic)
		g.mu.Lock()
		g.events = append(g.events, CollectedEvent{
			Type:      "status",
			Topic:     msg.Topic,
			TaskID:    taskID,
			Payload:   json.RawMessage(msg.Payload),
			Timestamp: time.Now(),
		})
		g.mu.Unlock()
		msg.Ack()
		return taskID
	}

	msg, err = g.brk.Receive(ctx, g.responseQueue)
	if err == nil && msg != nil {
		taskID := extractTaskIDFromTopic(msg.Topic)
		g.mu.Lock()
		g.events = append(g.events, CollectedEvent{
			Type:      "response",
			Topic:     msg.Topic,
			TaskID:    taskID,
			Payload:   json.RawMessage(msg.Payload),
			Timestamp: time.Now(),
		})
		g.mu.Unlock()
		msg.Ack()
		select {
		case <-g.done:
		default:
			close(g.done)
		}
		return taskID
	}

	return ""
}

func (g *TeamsTestGateway) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
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
		time.Sleep(500 * time.Millisecond)
		cancel()
	case <-collectCtx.Done():
	}

	wg.Wait()

	g.mu.Lock()
	defer g.mu.Unlock()
	events := make([]CollectedEvent, len(g.events))
	copy(events, g.events)

	if gotResponse {
		return events, nil
	}
	return events, collectCtx.Err()
}

func (g *TeamsTestGateway) receiveLoop(ctx context.Context, queue broker.Queue, eventType string, isFinal bool) {
	for {
		msg, err := g.brk.Receive(ctx, queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if msg == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		taskID := extractTaskIDFromTopic(msg.Topic)
		evt := CollectedEvent{
			Type:      eventType,
			Topic:     msg.Topic,
			TaskID:    taskID,
			Payload:   json.RawMessage(msg.Payload),
			Timestamp: time.Now(),
		}

		g.mu.Lock()
		g.events = append(g.events, evt)
		g.mu.Unlock()

		msg.Ack()

		if isFinal {
			select {
			case <-g.done:
			default:
				close(g.done)
			}
		}
	}
}

func (g *TeamsTestGateway) SendCancel(ctx context.Context, agentName string, taskID string) error {
	return g.core.CancelTask(ctx, taskID, agentName)
}

func (g *TeamsTestGateway) Cleanup() {
	ctx := context.Background()
	if g.core != nil {
		_ = g.core.Stop(ctx)
	}
	if g.responseQueue != nil {
		_ = g.brk.DeleteQueue(g.responseQueue.Name())
	}
	if g.statusQueue != nil {
		_ = g.brk.DeleteQueue(g.statusQueue.Name())
	}
	if g.mockServer != nil {
		g.mockServer.Close()
	}
}

func (g *TeamsTestGateway) TeamsCalls() []CapturedTeamsCall {
	if g.mockServer == nil {
		return nil
	}
	return g.mockServer.Calls()
}

var _ Gateway = (*TeamsTestGateway)(nil)
