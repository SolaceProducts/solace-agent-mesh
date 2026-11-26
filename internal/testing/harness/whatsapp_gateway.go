// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	whatsappTestAccessToken   = "test-access-token"
	whatsappTestPhoneNumberID = "test-phone-number-id"
	whatsappTestAppSecret     = "test-app-secret"
	whatsappTestVerifyToken   = "test-verify-token"
)

type WhatsAppTestConfig struct {
	DefaultAgent string `yaml:"default_agent"`
	FromPhone    string `yaml:"from_phone"`

	DisableFileUploads bool `yaml:"disable_file_uploads"`

	ResponseFormat string `yaml:"response_format"`

	ScriptSendFailures     []string `yaml:"script_send_failures"`
	ReopenWindowAfterReply bool     `yaml:"reopen_window_after_reply"`
}

func scriptedSendFailure(name string) (ScriptedResponse, error) {
	switch name {
	case "rate_limited":
		return RateLimited(), nil
	case "server_error":
		return ServerError(), nil
	case "window_closed":
		return WindowClosed(), nil
	case "token_expired":
		return TokenExpired(), nil
	default:
		return ScriptedResponse{}, fmt.Errorf("unknown whatsapp script_send_failures entry %q", name)
	}
}

type WhatsAppTestGateway struct {
	brk       broker.Broker
	namespace string
	gatewayID string
	logger    *slog.Logger
	svc       runtime.Services

	adapter    *whatsapp.Adapter
	core       *entrypoint.GatewayCore
	mockServer *MockWhatsAppServer
	handler    http.Handler

	whatsappConfig *WhatsAppTestConfig

	dbURL  string
	dbDrop func()

	responseQueue broker.Queue
	statusQueue   broker.Queue
	mu            sync.Mutex
	events        []CollectedEvent
	done          chan struct{}
}

func NewWhatsAppTestGateway(
	brk broker.Broker,
	waCfg *WhatsAppTestConfig,
	namespace, gatewayID string,
	logger *slog.Logger,
) *WhatsAppTestGateway {
	if waCfg == nil {
		waCfg = &WhatsAppTestConfig{}
	}
	return &WhatsAppTestGateway{
		brk:            brk,
		namespace:      namespace,
		gatewayID:      gatewayID,
		logger:         logger,
		whatsappConfig: waCfg,
		done:           make(chan struct{}),
	}
}

func (g *WhatsAppTestGateway) SetServices(svc runtime.Services) { g.svc = svc }

func (g *WhatsAppTestGateway) Setup(ctx context.Context) error {
	g.mockServer = NewMockWhatsAppServer()
	g.mockServer.RequireBearerToken(whatsappTestAccessToken)

	tmpl, err := getGatewayDBTemplate()
	if err != nil {
		return fmt.Errorf("init gateway db template: %w", err)
	}
	dbURL, drop, cloneErr := tmpl.CloneDB()
	if cloneErr != nil {
		return fmt.Errorf("clone gateway db: %w", cloneErr)
	}
	g.dbURL = dbURL
	g.dbDrop = drop

	for _, name := range g.whatsappConfig.ScriptSendFailures {
		resp, failErr := scriptedSendFailure(name)
		if failErr != nil {
			return failErr
		}
		g.mockServer.ScriptResponses(EndpointSend, resp)
	}

	g.responseQueue, err = g.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create response queue: %w", err)
	}
	if subErr := g.brk.Subscribe(g.responseQueue, a2a.GatewayResponseSubscription(g.namespace, g.gatewayID)); subErr != nil {
		return fmt.Errorf("subscribe response: %w", subErr)
	}
	g.statusQueue, err = g.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create status queue: %w", err)
	}
	if subErr := g.brk.Subscribe(g.statusQueue, a2a.GatewayStatusSubscription(g.namespace, g.gatewayID)); subErr != nil {
		return fmt.Errorf("subscribe status: %w", subErr)
	}

	return g.setupAdapter(ctx)
}

func (g *WhatsAppTestGateway) setupAdapter(ctx context.Context) error {
	adapterConfig := map[string]any{
		"access_token":          whatsappTestAccessToken,
		"phone_number_id":       whatsappTestPhoneNumberID,
		"app_secret":            whatsappTestAppSecret,
		"verify_token":          whatsappTestVerifyToken,
		"default_agent_name":    g.whatsappConfig.DefaultAgent,
		"graph_api_base_url":    g.mockServer.URL(),
		"graph_api_version":     "v26.0",
		"disable_rate_limiting": true,
		"feedback_enabled":      true,
	}
	if g.whatsappConfig.DisableFileUploads {
		adapterConfig["disable_file_uploads"] = true
	}
	appConfig := map[string]any{
		"namespace":  g.namespace,
		"gateway_id": g.gatewayID,
		"gateway_adapter": map[string]any{
			"type":           "whatsapp",
			"adapter_config": adapterConfig,
		},
		"session_service": map[string]any{
			"type":             "sql",
			"database_url":     g.dbURL,
			"default_behavior": "PERSISTENT",
		},
		"task_logging": map[string]any{
			"enabled": true,
		},
	}
	if g.whatsappConfig.ResponseFormat != "" {
		appConfig["response_format"] = g.whatsappConfig.ResponseFormat
	}

	adapter, err := whatsapp.NewAdapter(adapterConfig)
	if err != nil {
		return fmt.Errorf("create whatsapp adapter: %w", err)
	}
	g.adapter = adapter
	adapter.SetOnHandlerReady(func(h http.Handler) { g.handler = h })

	g.core = entrypoint.NewGatewayCore(adapter, appConfig)
	g.core.SetServices(g.svc)
	if initErr := g.core.Init(ctx, nil); initErr != nil {
		return fmt.Errorf("whatsapp gateway init: %w", initErr)
	}
	if g.handler == nil {
		return fmt.Errorf("whatsapp adapter did not publish a webhook handler during Init")
	}
	if startErr := g.core.Start(ctx); startErr != nil {
		return fmt.Errorf("whatsapp gateway start: %w", startErr)
	}
	return nil
}

func (g *WhatsAppTestGateway) SendRequest(_ context.Context, _ string, input *GatewayInput) (string, error) {
	fromPhone := g.whatsappConfig.FromPhone
	if fromPhone == "" {
		fromPhone = "15551234567"
	}
	if input.UserIdentity != "" {
		fromPhone = input.UserIdentity
	}

	var text string
	var hasFile bool
	var fileName, fileB64, fileMime string
	for _, p := range input.GetParts() {
		if p.Type == "file" {
			if !hasFile {
				name, b64 := resolveFilePartFields(p)
				if b64 != "" {
					hasFile, fileName, fileB64, fileMime = true, name, b64, p.MimeType
				}
			}
			continue
		}
		if t := p.GetText(); t != "" && text == "" {
			text = t
		}
	}

	msgID := fmt.Sprintf("wamid.TEST-%d", time.Now().UnixNano())
	ts := fmt.Sprintf("%d", time.Now().Unix())

	message := map[string]any{"from": fromPhone, "from_user_id": testBSUID(fromPhone), "id": msgID, "timestamp": ts}
	if hasFile {
		content, decErr := base64.StdEncoding.DecodeString(fileB64)
		if decErr != nil {
			return "", fmt.Errorf("decode file part: %w", decErr)
		}
		mediaID := fmt.Sprintf("media-%d", time.Now().UnixNano())
		g.mockServer.RegisterMedia(mediaID, content, fileMime)

		mediaType := "document"
		mediaObj := map[string]any{"id": mediaID, "mime_type": fileMime}
		switch {
		case strings.HasPrefix(fileMime, "image/"):
			mediaType = "image"
		case strings.HasPrefix(fileMime, "audio/"):
			mediaType = "audio"
		case strings.HasPrefix(fileMime, "video/"):
			mediaType = "video"
		default:
			mediaObj["filename"] = fileName
		}
		if text != "" {
			mediaObj["caption"] = text
		}
		message["type"] = mediaType
		message[mediaType] = mediaObj
	} else {
		message["type"] = "text"
		message["text"] = map[string]any{"body": text}
	}

	envelope := map[string]any{
		"object": "whatsapp_business_account",
		"entry": []map[string]any{{
			"id": "test-waba-id",
			"changes": []map[string]any{{
				"field": "messages",
				"value": map[string]any{
					"messaging_product": "whatsapp",
					"metadata": map[string]any{
						"display_phone_number": "15550000000",
						"phone_number_id":      whatsappTestPhoneNumberID,
					},
					"contacts": []map[string]any{senderContact(fromPhone)},
					"messages": []map[string]any{message},
				},
			}},
		}},
	}
	if err := g.postWebhook(envelope); err != nil {
		return "", err
	}

	taskID := g.discoverTaskID(500 * time.Millisecond)
	return taskID, nil
}

func (g *WhatsAppTestGateway) postWebhook(envelope map[string]any) error {
	status, body, err := g.postWebhookStatus(envelope)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("webhook POST returned %d (expected 200): %s", status, body)
	}
	return nil
}

func (g *WhatsAppTestGateway) postWebhookStatus(envelope map[string]any) (int, string, error) {
	body, err := json.Marshal(envelope)
	if err != nil {
		return 0, "", fmt.Errorf("marshal webhook envelope: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(whatsappTestAppSecret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", sig)
	rr := httptest.NewRecorder()
	g.handler.ServeHTTP(rr, req)
	return rr.Code, rr.Body.String(), nil
}

func (g *WhatsAppTestGateway) SendTextWithID(fromPhone, wamid, text string) (int, error) {
	msg := map[string]any{
		"from":         g.fromPhone(fromPhone),
		"from_user_id": testBSUID(g.fromPhone(fromPhone)),
		"id":           wamid,
		"timestamp":    fmt.Sprintf("%d", time.Now().Unix()),
		"type":         "text",
		"text":         map[string]any{"body": text},
	}
	status, _, err := g.postWebhookStatus(changeEnvelope(map[string]any{
		"contacts": []map[string]any{senderContact(g.fromPhone(fromPhone))},
		"messages": []map[string]any{msg},
	}))
	return status, err
}

func changeEnvelope(value map[string]any) map[string]any {
	value["messaging_product"] = "whatsapp"
	value["metadata"] = map[string]any{
		"display_phone_number": "15550000000",
		"phone_number_id":      whatsappTestPhoneNumberID,
	}
	return map[string]any{
		"object": "whatsapp_business_account",
		"entry": []map[string]any{{
			"id":      "test-waba-id",
			"changes": []map[string]any{{"field": "messages", "value": value}},
		}},
	}
}

func (g *WhatsAppTestGateway) SendMediaWithID(fromPhone, wamid, mediaType, mime, filename, caption string, content []byte) (int, error) {
	from := g.fromPhone(fromPhone)
	mediaID := "media-" + wamid
	g.mockServer.RegisterMedia(mediaID, content, mime)

	media := map[string]any{"id": mediaID, "mime_type": mime}
	if filename != "" {
		media["filename"] = filename
	}
	if caption != "" {
		media["caption"] = caption
	}
	msg := map[string]any{
		"from":         from,
		"from_user_id": testBSUID(from),
		"id":           wamid,
		"timestamp":    fmt.Sprintf("%d", time.Now().Unix()),
		"type":         mediaType,
		mediaType:      media,
	}
	status, _, err := g.postWebhookStatus(changeEnvelope(map[string]any{
		"contacts": []map[string]any{senderContact(from)},
		"messages": []map[string]any{msg},
	}))
	return status, err
}

func (g *WhatsAppTestGateway) SendStatusReceipt(wamid, status string, errorCode int) error {
	st := map[string]any{
		"id":           wamid,
		"recipient_id": g.fromPhone(""),
		"status":       status,
		"timestamp":    fmt.Sprintf("%d", time.Now().Unix()),
		"conversation": map[string]any{"id": "test-conversation"},
	}
	if errorCode != 0 {
		st["errors"] = []map[string]any{{
			"code":       errorCode,
			"title":      "Message undeliverable",
			"error_data": map[string]any{"details": "to: " + g.fromPhone("") + " undeliverable"},
		}}
	}
	return g.postWebhook(changeEnvelope(map[string]any{"statuses": []map[string]any{st}}))
}

func (g *WhatsAppTestGateway) SendInteractiveReply(fromPhone, replyID, replyTitle, contextWamid string, list bool) error {
	kind := "button_reply"
	if list {
		kind = "list_reply"
	}
	msg := map[string]any{
		"from":         g.fromPhone(fromPhone),
		"from_user_id": testBSUID(g.fromPhone(fromPhone)),
		"id":           fmt.Sprintf("wamid.TEST-%d", time.Now().UnixNano()),
		"timestamp":    fmt.Sprintf("%d", time.Now().Unix()),
		"type":         "interactive",
		"interactive": map[string]any{
			"type": kind,
			kind:   map[string]any{"id": replyID, "title": replyTitle},
		},
	}
	if contextWamid != "" {
		msg["context"] = map[string]any{"from": whatsappTestPhoneNumberID, "id": contextWamid}
	}
	return g.postWebhook(changeEnvelope(map[string]any{
		"contacts": []map[string]any{senderContact(g.fromPhone(fromPhone))},
		"messages": []map[string]any{msg},
	}))
}

func (g *WhatsAppTestGateway) ReopenWindow() error {
	if err := g.waitForParkedReply(5 * time.Second); err != nil {
		return err
	}
	msg := map[string]any{
		"from":         g.fromPhone(""),
		"from_user_id": testBSUID(g.fromPhone("")),
		"id":           fmt.Sprintf("wamid.TEST-%d", time.Now().UnixNano()),
		"timestamp":    fmt.Sprintf("%d", time.Now().Unix()),
		"type":         "reaction",
		"reaction":     map[string]any{"message_id": "wamid.TEST-reacted", "emoji": "👍"},
	}
	return g.postWebhook(changeEnvelope(map[string]any{
		"contacts": []map[string]any{senderContact(g.fromPhone(""))},
		"messages": []map[string]any{msg},
	}))
}

func (g *WhatsAppTestGateway) SendReaction(fromPhone, targetWamid, emoji string) error {
	msg := map[string]any{
		"from":      g.fromPhone(fromPhone),
		"id":        fmt.Sprintf("wamid.TEST-%d", time.Now().UnixNano()),
		"timestamp": fmt.Sprintf("%d", time.Now().Unix()),
		"type":      "reaction",
		"reaction":  map[string]any{"message_id": targetWamid, "emoji": emoji},
	}
	return g.postWebhook(changeEnvelope(map[string]any{
		"contacts": []map[string]any{{"wa_id": g.fromPhone(fromPhone), "profile": map[string]any{"name": "Test User"}}},
		"messages": []map[string]any{msg},
	}))
}

func (g *WhatsAppTestGateway) DBURL() string { return g.dbURL }

func (g *WhatsAppTestGateway) waitForParkedReply(timeout time.Duration) error {
	db, err := store.Open(g.dbURL, g.logger)
	if err != nil {
		return fmt.Errorf("open gateway db: %w", err)
	}
	defer func() { _ = db.Close() }()

	repo := store.NewWhatsAppOutboundRepo(db)
	deadline := time.Now().Add(timeout)
	for {
		row, getErr := repo.GetParked(context.Background(), g.gatewayID, g.fromPhone(""))
		if getErr != nil {
			return fmt.Errorf("look for a parked reply: %w", getErr)
		}
		if row != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no reply was parked within %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testBSUID(phone string) string {
	if phone == "" {
		return ""
	}
	return "US." + phone
}

func senderContact(phone string) map[string]any {
	return map[string]any{
		"wa_id":   phone,
		"user_id": testBSUID(phone),
		"profile": map[string]any{"name": "Test User"},
	}
}

func (g *WhatsAppTestGateway) fromPhone(override string) string {
	if override != "" {
		return override
	}
	if g.whatsappConfig.FromPhone != "" {
		return g.whatsappConfig.FromPhone
	}
	return "15551234567"
}

func (g *WhatsAppTestGateway) MockServer() *MockWhatsAppServer { return g.mockServer }

func (g *WhatsAppTestGateway) Restart(ctx context.Context) error {
	if g.core != nil {
		if err := g.core.Stop(ctx); err != nil {
			return fmt.Errorf("stop gateway for restart: %w", err)
		}
	}
	g.handler = nil
	g.ResetForNextRequest()
	return g.setupAdapter(ctx)
}

func (g *WhatsAppTestGateway) ResetForNextRequest() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = nil
	g.done = make(chan struct{})
}

func (g *WhatsAppTestGateway) doneCh() chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.done
}

func (g *WhatsAppTestGateway) signalDone() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.done:
	default:
		close(g.done)
	}
}

func (g *WhatsAppTestGateway) discoverTaskID(timeout time.Duration) string {
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
		g.signalDone()
		return taskID
	}

	return ""
}

func (g *WhatsAppTestGateway) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
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
	case <-g.doneCh():
		gotResponse = true
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

func (g *WhatsAppTestGateway) receiveLoop(ctx context.Context, queue broker.Queue, eventType string, isFinal bool) {
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
			g.signalDone()
		}
	}
}

func (g *WhatsAppTestGateway) SendCancel(ctx context.Context, agentName string, taskID string) error {
	return g.core.CancelTask(ctx, taskID, agentName)
}

func (g *WhatsAppTestGateway) Cleanup() {
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
	if g.dbDrop != nil {
		g.dbDrop()
	}
}

func (g *WhatsAppTestGateway) WhatsAppCalls() []CapturedWhatsAppCall {
	if g.mockServer == nil {
		return nil
	}
	return g.mockServer.Calls()
}

var _ Gateway = (*WhatsAppTestGateway)(nil)
