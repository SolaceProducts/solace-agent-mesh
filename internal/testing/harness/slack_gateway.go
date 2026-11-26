// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/slack"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/slack-go/slack/slackevents"
)

type SlackTestConfig struct {
	DefaultAgent    string            `yaml:"default_agent"`
	FeedbackEnabled bool              `yaml:"feedback_enabled"`
	CorrectMarkdown *bool             `yaml:"correct_markdown"`
	ChannelID       string            `yaml:"channel_id"`
	UserID          string            `yaml:"user_id"`
	TeamID          string            `yaml:"team_id"`
	EventType       string            `yaml:"event_type"`
	UserEmails      map[string]string `yaml:"user_emails"`
}

type SlackTestGateway struct {
	brk       broker.Broker
	namespace string
	gatewayID string
	logger    *slog.Logger
	svc       runtime.Services

	adapter    *slack.SlackGatewayAdapter
	core       *entrypoint.GatewayCore
	mockServer *MockSlackServer

	slackConfig *SlackTestConfig

	responseQueue broker.Queue
	statusQueue   broker.Queue
	mu            sync.Mutex
	events        []CollectedEvent
	done          chan struct{}
}

func NewSlackTestGateway(
	brk broker.Broker,
	slackCfg *SlackTestConfig,
	namespace, gatewayID string,
	logger *slog.Logger,
) *SlackTestGateway {
	if slackCfg == nil {
		slackCfg = &SlackTestConfig{}
	}
	return &SlackTestGateway{
		brk:         brk,
		namespace:   namespace,
		gatewayID:   gatewayID,
		logger:      logger,
		slackConfig: slackCfg,
		done:        make(chan struct{}),
	}
}

func (g *SlackTestGateway) SetServices(svc runtime.Services) {
	g.svc = svc
}

func (g *SlackTestGateway) Setup(ctx context.Context) error {
	g.mockServer = NewMockSlackServer()
	for uid, email := range g.slackConfig.UserEmails {
		g.mockServer.SetUserEmail(uid, email)
	}

	correctMarkdown := true
	if g.slackConfig.CorrectMarkdown != nil {
		correctMarkdown = *g.slackConfig.CorrectMarkdown
	}

	adapterConfig := map[string]any{
		"slack_bot_token":              "xoxb-test-token",
		"slack_app_token":              "xapp-test-token",
		"slack_api_url":                g.mockServer.URL(),
		"disable_rate_limiting":        true,
		"default_agent_name":           g.slackConfig.DefaultAgent,
		"feedback_enabled":             g.slackConfig.FeedbackEnabled,
		"correct_markdown_formatting":  correctMarkdown,
		"slack_initial_status_message": "Got it, thinking...",
	}

	appConfig := map[string]any{
		"namespace":  g.namespace,
		"gateway_id": g.gatewayID,
		"gateway_adapter": map[string]any{
			"type":           "slack",
			"adapter_config": adapterConfig,
		},
	}

	adapter, err := slack.NewSlackGatewayAdapter(adapterConfig)
	if err != nil {
		return fmt.Errorf("create slack adapter: %w", err)
	}
	g.adapter = adapter

	g.core = entrypoint.NewGatewayCore(adapter, appConfig)
	g.core.SetServices(g.svc)

	if initErr := g.core.Init(ctx, nil); initErr != nil {
		return fmt.Errorf("slack gateway init: %w", initErr)
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
		return fmt.Errorf("slack gateway start: %w", err)
	}

	return nil
}

func (g *SlackTestGateway) SendRequest(ctx context.Context, agentName string, input *GatewayInput) (string, error) {
	channelID := g.slackConfig.ChannelID
	if channelID == "" {
		channelID = "C-test-channel"
	}
	userID := g.slackConfig.UserID
	if userID == "" {
		userID = "U-test-user"
	}
	if input.UserIdentity != "" {
		userID = input.UserIdentity
	}
	teamID := g.slackConfig.TeamID
	if teamID == "" {
		teamID = "T-test-team"
	}

	var text string
	var files []slack.TestFileInfo
	for i, p := range input.GetParts() {
		switch p.Type {
		case "file":
			name, b64 := resolveFilePartFields(p)
			if b64 == "" {
				continue
			}
			content, decErr := base64.StdEncoding.DecodeString(b64)
			if decErr != nil {
				return "", fmt.Errorf("decode file part %d: %w", i, decErr)
			}
			if name == "" {
				name = fmt.Sprintf("upload_%d", i)
			}
			fileID := fmt.Sprintf("F-test-%d", i)
			g.mockServer.RegisterFile(fileID, content)
			files = append(files, slack.TestFileInfo{
				ID:                 fileID,
				Name:               name,
				MimeType:           p.MimeType,
				URLPrivateDownload: g.mockServer.FileDownloadURL(fileID),
			})
		default:
			if t := p.GetText(); t != "" && text == "" {
				text = t
			}
		}
	}

	ts := fmt.Sprintf("%d.%06d", time.Now().Unix(), time.Now().Nanosecond()/1000)

	eventType := g.slackConfig.EventType
	if eventType == "" {
		eventType = "message"
	}
	if len(files) > 0 {
		ev := &slackevents.MessageEvent{
			User:      userID,
			Text:      text,
			Channel:   channelID,
			TimeStamp: ts,
		}
		ev.SubType = "file_share"
		ev.ChannelType = "im"
		g.adapter.HandleMessageWithFiles(ev, teamID, files)
	} else {
		switch eventType {
		case "app_mention":
			ev := &slackevents.AppMentionEvent{
				User:            userID,
				Text:            fmt.Sprintf("<@U-bot> %s", text),
				Channel:         channelID,
				TimeStamp:       ts,
				ThreadTimeStamp: "",
			}
			g.adapter.HandleAppMention(ev, teamID)
		default:
			ev := &slackevents.MessageEvent{
				User:            userID,
				Text:            text,
				Channel:         channelID,
				TimeStamp:       ts,
				ThreadTimeStamp: "",
				ChannelType:     "im",
			}
			g.adapter.HandleMessage(ev, teamID)
		}
	}

	taskID := g.discoverTaskID(500 * time.Millisecond)

	return taskID, nil
}

func (g *SlackTestGateway) discoverTaskID(timeout time.Duration) string {
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

func (g *SlackTestGateway) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
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

func (g *SlackTestGateway) receiveLoop(ctx context.Context, queue broker.Queue, eventType string, isFinal bool) {
	for {
		msg, err := g.brk.Receive(ctx, queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			g.logger.Warn("slack gw receive error", "type", eventType, "error", err)
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

func (g *SlackTestGateway) SendCancel(ctx context.Context, agentName string, taskID string) error {
	return g.core.CancelTask(ctx, taskID, agentName)
}

func (g *SlackTestGateway) Cleanup() {
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

func (g *SlackTestGateway) SlackCalls() []CapturedSlackCall {
	if g.mockServer == nil {
		return nil
	}
	return g.mockServer.Calls()
}

func (g *SlackTestGateway) SlackCallsByMethod(method string) []CapturedSlackCall {
	if g.mockServer == nil {
		return nil
	}
	return g.mockServer.CallsByMethod(method)
}

var _ Gateway = (*SlackTestGateway)(nil)
