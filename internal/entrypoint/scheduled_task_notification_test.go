// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func testLogger() *slog.Logger {
	return slog.Default()
}

type fakeUserNotifier struct {
	mu    sync.Mutex
	calls []userNotifCall
}

type userNotifCall struct {
	UserID    string
	EventType string
	Data      any
}

func (f *fakeUserNotifier) SendUserNotification(userID, eventType string, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, userNotifCall{UserID: userID, EventType: eventType, Data: data})
	return nil
}

func (f *fakeUserNotifier) Calls() []userNotifCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]userNotifCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestValidateWebhookURL_AcceptsValidSchemes(t *testing.T) {
	err := validateWebhookURL("http://example.com/hook")
	if err != nil {
		t.Errorf("expected http://example.com to pass validation, got: %v", err)
	}
	err = validateWebhookURL("https://example.com/hook")
	if err != nil {
		t.Errorf("expected https://example.com to pass validation, got: %v", err)
	}
}

func TestValidateWebhookURL_BlocksPrivateIPs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"loopback", "http://127.0.0.1/hook"},
		{"private 10.x", "http://10.0.0.1/hook"},
		{"private 172.16.x", "http://172.16.0.1/hook"},
		{"private 192.168.x", "http://192.168.1.1/hook"},
		{"link-local", "http://169.254.169.254/latest/meta-data/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWebhookURL(tt.url)
			if err == nil {
				t.Errorf("expected %s to be blocked", tt.url)
			}
		})
	}
}

func TestValidateWebhookURL_BlocksBadScheme(t *testing.T) {
	err := validateWebhookURL("ftp://example.com/hook")
	if err == nil {
		t.Error("expected ftp scheme to be blocked")
	}
}

func TestValidateWebhookURL_BlocksEmptyHostname(t *testing.T) {
	err := validateWebhookURL("http:///path")
	if err == nil {
		t.Error("expected empty hostname to be blocked")
	}
}

func TestIsBlockedIP(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"::1", true},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP: %s", tt.ip)
			}
			got := isBlockedIP(ip)
			if got != tt.blocked {
				t.Errorf("isBlockedIP(%s) = %v, want %v", tt.ip, got, tt.blocked)
			}
		})
	}
}

func TestWebhookClient_BlocksPrivateIP(t *testing.T) {
	client := newWebhookHTTPClient(net.DefaultResolver.LookupIPAddr)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1/hook", nil)
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Error("expected webhook client to block loopback")
	}
}

func TestDeniedWebhookHeaders(t *testing.T) {
	denied := []string{
		"Authorization", "Cookie", "Host",
		"Proxy-Authorization", "X-Forwarded-For",
		"X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP",
	}

	for _, h := range denied {
		t.Run(h, func(t *testing.T) {
			if !deniedWebhookHeaders[strings.ToLower(h)] {
				t.Errorf("expected %s to be in denied headers map", h)
			}
		})
	}
}

func TestValidateNotificationConfig_BrokerTopicRejected(t *testing.T) {
	cfg := `{"on_success":true,"on_failure":true,"channels":[{"type":"broker_topic","config":{"topic":"scheduled-tasks/my-topic"}}]}`
	if err := ValidateNotificationConfig(cfg); err == nil {
		t.Error("expected broker_topic to be rejected after removal")
	}
}

func TestValidateNotificationConfig_DeniedHeaders(t *testing.T) {
	cfg := `{"channels":[{"type":"webhook","config":{"url":"https://example.com/hook","headers":{"Authorization":"Bearer token"}}}]}`
	err := ValidateNotificationConfig(cfg)
	if err == nil {
		t.Error("expected error for denied Authorization header")
	}
}

func TestValidateNotificationConfig_UnknownChannelType(t *testing.T) {
	cfg := `{"channels":[{"type":"sms","config":{}}]}`
	err := ValidateNotificationConfig(cfg)
	if err == nil {
		t.Error("expected error for unknown channel type")
	}
}

func TestValidateNotificationConfig_InvalidJSON(t *testing.T) {
	err := ValidateNotificationConfig(`{bad json}`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFormatSlackPayload(t *testing.T) {
	payload := map[string]any{
		"task_name":    "daily-report",
		"status":       "completed",
		"execution_id": "exec-123",
	}

	result := formatSlackPayload(payload)
	text, ok := result["text"].(string)
	if !ok || text == "" {
		t.Error("expected text field in Slack payload")
	}
	blocks, ok := result["blocks"].([]map[string]any)
	if !ok || len(blocks) == 0 {
		t.Error("expected blocks in Slack payload")
	}
}

func TestFormatSlackPayload_FailedStatus(t *testing.T) {
	payload := map[string]any{
		"task_name":    "daily-report",
		"status":       "failed",
		"execution_id": "exec-123",
	}

	result := formatSlackPayload(payload)
	text := result["text"].(string)
	if text == "" {
		t.Error("expected non-empty text")
	}
}

func TestFormatTeamsPayload(t *testing.T) {
	payload := map[string]any{
		"task_name":    "daily-report",
		"status":       "completed",
		"execution_id": "exec-123",
	}

	result := formatTeamsPayload(payload)
	if result["@type"] != "MessageCard" {
		t.Error("expected @type MessageCard")
	}
	if result["themeColor"] != "00FF00" {
		t.Error("expected green theme color for completed")
	}
}

func TestFormatTeamsPayload_FailedStatus(t *testing.T) {
	payload := map[string]any{
		"task_name":    "daily-report",
		"status":       "failed",
		"execution_id": "exec-123",
	}

	result := formatTeamsPayload(payload)
	if result["themeColor"] != "FF0000" {
		t.Error("expected red theme color for failed")
	}
}

func TestFormatWebhookPayload_Generic(t *testing.T) {
	payload := map[string]any{"task_name": "test"}
	config := map[string]any{"webhook_type": "generic"}

	result := formatWebhookPayload(payload, config)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map result for generic webhook")
	}
	if m["task_name"] != "test" {
		t.Error("expected payload to be passed through")
	}
}

func TestFormatWebhookPayload_DefaultIsGeneric(t *testing.T) {
	payload := map[string]any{"task_name": "test"}
	config := map[string]any{}

	result := formatWebhookPayload(payload, config)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map result for default webhook type")
	}
	if m["task_name"] != "test" {
		t.Error("expected payload to be passed through")
	}
}

type notifTestBroker struct {
	published []notifBrokerMsg
	down      bool
}

type notifBrokerMsg struct {
	topic   string
	payload []byte
	props   map[string]string
}

func (b *notifTestBroker) Connect(context.Context) error    { return nil }
func (b *notifTestBroker) Disconnect(context.Context) error { return nil }
func (b *notifTestBroker) IsConnected() bool                { return true }
func (b *notifTestBroker) CreateQueue(string, broker.QueueOptions) (broker.Queue, error) {
	return nil, nil
}
func (b *notifTestBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (b *notifTestBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (b *notifTestBroker) CreateDirectSubscription() (broker.Queue, error) { return nil, nil }
func (b *notifTestBroker) DeleteQueue(string) error                        { return nil }
func (b *notifTestBroker) DeprovisionQueue(string) error                   { return nil }
func (b *notifTestBroker) Subscribe(broker.Queue, string) error            { return nil }
func (b *notifTestBroker) Unsubscribe(broker.Queue, string) error          { return nil }
func (b *notifTestBroker) Receive(context.Context, broker.Queue) (*broker.Message, error) {
	return nil, nil
}
func (b *notifTestBroker) Request(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}

func (b *notifTestBroker) Publish(_ context.Context, topic string, msg *broker.Message) error {
	if b.down {
		return errors.New("broker down")
	}
	b.published = append(b.published, notifBrokerMsg{
		topic:   topic,
		payload: msg.Payload,
		props:   msg.UserProperties,
	})
	return nil
}

func (b *notifTestBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	return b.Publish(ctx, topic, msg)
}

func TestNotifyExecutionComplete_OnSuccessFilter(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())

	task := &store.ScheduledTask{
		ID:        "task-1",
		Name:      "daily-report",
		CreatedBy: "u1",
		NotificationConfig: strPtr(`{
			"on_success": true,
			"on_failure": false,
			"channels": [{"type": "inbox"}]
		}`),
	}

	exec := &store.ScheduledTaskExecution{
		ID:     "exec-1",
		Status: "completed",
	}
	sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent == "" {
		t.Error("expected notification to be sent for completed execution")
	}
	if len(brk.published) != 1 {
		t.Errorf("expected 1 broker message, got %d", len(brk.published))
	}

	brk.published = nil
	exec.Status = "failed"
	sent = svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent != "" {
		t.Error("expected no notification for failed execution with on_failure=false")
	}
}

func TestNotifyExecutionComplete_OnFailureFilter(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())

	task := &store.ScheduledTask{
		ID:        "task-1",
		Name:      "daily-report",
		CreatedBy: "u1",
		NotificationConfig: strPtr(`{
			"on_success": false,
			"on_failure": true,
			"channels": [{"type": "inbox"}]
		}`),
	}

	exec := &store.ScheduledTaskExecution{
		ID:     "exec-1",
		Status: "failed",
	}
	sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent == "" {
		t.Error("expected notification for failed execution")
	}

	brk.published = nil
	exec.Status = "completed"
	sent = svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent != "" {
		t.Error("expected no notification for completed execution with on_success=false")
	}
}

func TestNotifyExecutionComplete_TimeoutTriggersOnFailure(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())

	task := &store.ScheduledTask{
		ID:        "task-1",
		Name:      "daily-report",
		CreatedBy: "u1",
		NotificationConfig: strPtr(`{
			"on_success": false,
			"on_failure": true,
			"channels": [{"type": "inbox"}]
		}`),
	}

	exec := &store.ScheduledTaskExecution{
		ID:     "exec-1",
		Status: "timeout",
	}
	sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent == "" {
		t.Error("expected notification for timeout execution")
	}
}

func TestNotifyExecutionComplete_TimeoutRespectsOnFailureOff(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())
	task := &store.ScheduledTask{
		ID:                 "task-1",
		Name:               "daily-report",
		CreatedBy:          "u1",
		NotificationConfig: strPtr(`{"on_success": true, "on_failure": false, "channels": [{"type": "inbox"}]}`),
	}

	sent := svc.NotifyExecutionComplete(t.Context(), &store.ScheduledTaskExecution{ID: "exec-1", Status: "timeout"}, "", task, nil)

	if sent != "" || len(brk.published) != 0 {
		t.Errorf("a timed-out run with on_failure off should not notify, got %q and %d published", sent, len(brk.published))
	}
}

func TestNotifyExecutionComplete_WebhookSend(t *testing.T) {
	var receivedPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())
	svc.httpClient = srv.Client()
	svc.skipURLValidation = true

	task := &store.ScheduledTask{
		ID:        "task-1",
		Name:      "webhook-test",
		Namespace: "default",
		NotificationConfig: strPtr(`{
			"on_success": true,
			"on_failure": true,
			"channels": [{"type": "webhook", "config": {"url": "` + srv.URL + `", "webhook_type": "generic"}}]
		}`),
	}

	exec := &store.ScheduledTaskExecution{
		ID:     "exec-1",
		Status: "completed",
	}

	sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent == "" {
		t.Fatal("expected notification to be sent")
	}

	if receivedPayload == nil {
		t.Fatal("webhook did not receive payload")
	}
	if receivedPayload["task_name"] != "webhook-test" {
		t.Errorf("webhook payload task_name = %v, want webhook-test", receivedPayload["task_name"])
	}
}

func TestNotifyExecutionComplete_WebhookMarksABudgetStop(t *testing.T) {
	var receivedPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := NewNotificationService(&notifTestBroker{}, testLogger())
	svc.httpClient = srv.Client()
	svc.skipURLValidation = true
	task := &store.ScheduledTask{
		ID: "task-1", Name: "webhook-test", Namespace: "default",
		NotificationConfig: strPtr(`{"on_failure": true, "channels": [{"type": "webhook", "config": {"url": "` + srv.URL + `"}}]}`),
	}
	exec := &store.ScheduledTaskExecution{ID: "exec-1", Status: "skipped", BudgetStop: strPtr(store.BudgetStopFirst)}

	if sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil); sent == "" {
		t.Fatal("expected the first budget stop to notify")
	}
	if receivedPayload["budget_stop"] != store.BudgetStopFirst || receivedPayload["status"] != "skipped" {
		t.Errorf("webhook payload = %v, want status skipped with budget_stop first", receivedPayload)
	}
}

func TestNotifyExecutionComplete_SSEUserNotifier(t *testing.T) {
	notif := &fakeUserNotifier{}
	svc := NewNotificationService(nil, testLogger())
	svc.SetUserNotifier(notif)

	userID := "user-1"
	task := &store.ScheduledTask{
		ID:        "task-1",
		Name:      "sse-test",
		UserID:    &userID,
		CreatedBy: "user-1",
		NotificationConfig: strPtr(`{
			"on_success": true,
			"channels": [{"type": "sse", "config": {}}]
		}`),
	}

	exec := &store.ScheduledTaskExecution{
		ID:     "exec-1",
		Status: "completed",
	}

	sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent == "" {
		t.Fatal("expected notification to be sent")
	}

	calls := notif.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 user notification (deduped), got %d", len(calls))
	}
	if calls[0].UserID != "user-1" {
		t.Errorf("user_id = %s, want user-1", calls[0].UserID)
	}
	if calls[0].EventType != "session_created" {
		t.Errorf("event_type = %s, want session_created", calls[0].EventType)
	}
	payload, ok := calls[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map payload, got %T", calls[0].Data)
	}
	if payload["task_id"] != "task-1" {
		t.Errorf("task_id = %v, want task-1", payload["task_id"])
	}
}

func TestNotifyExecutionComplete_SSEUserNotifier_FansOutToUserAndCreator(t *testing.T) {
	notif := &fakeUserNotifier{}
	svc := NewNotificationService(nil, testLogger())
	svc.SetUserNotifier(notif)

	owner := "owner-id"
	task := &store.ScheduledTask{
		ID:        "task-2",
		Name:      "fanout-test",
		UserID:    &owner,
		CreatedBy: "creator-id",
		NotificationConfig: strPtr(`{
			"on_success": true,
			"channels": [{"type": "sse", "config": {}}]
		}`),
	}
	exec := &store.ScheduledTaskExecution{ID: "exec-2", Status: "completed"}

	_ = svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)

	calls := notif.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (owner + creator), got %d", len(calls))
	}
	got := map[string]bool{}
	for _, c := range calls {
		got[c.UserID] = true
		if c.EventType != "session_created" {
			t.Errorf("event_type = %s, want session_created", c.EventType)
		}
	}
	if !got["owner-id"] || !got["creator-id"] {
		t.Errorf("expected both owner-id and creator-id, got %v", got)
	}
}

func TestNotifyExecutionComplete_RecordsFailure(t *testing.T) {
	svc := NewNotificationService(nil, testLogger())

	task := &store.ScheduledTask{
		ID:   "task-1",
		Name: "fail-test",
		NotificationConfig: strPtr(`{
			"on_success": true,
			"channels": [{"type": "webhook", "config": {"url": "http://127.0.0.1/hook"}}]
		}`),
	}

	exec := &store.ScheduledTaskExecution{
		ID:     "exec-1",
		Status: "completed",
	}

	sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent == "" {
		t.Fatal("expected notification records even on failure")
	}

	var records []notificationRecord
	if err := json.Unmarshal([]byte(sent), &records); err != nil {
		t.Fatalf("unmarshal notification records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Status != "failed" {
		t.Errorf("record status = %s, want failed", records[0].Status)
	}
	if records[0].Error == "" {
		t.Error("expected error message in failed record")
	}
}

func TestNotifyExecutionComplete_EmailStubSkipped(t *testing.T) {
	svc := NewNotificationService(nil, testLogger())

	task := &store.ScheduledTask{
		ID:   "task-1",
		Name: "email-test",
		NotificationConfig: strPtr(`{
			"on_success": true,
			"channels": [{"type": "email", "config": {}}]
		}`),
	}

	exec := &store.ScheduledTaskExecution{
		ID:     "exec-1",
		Status: "completed",
	}

	sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)
	if sent != "" {
		t.Errorf("email stub should not produce notification records, got: %s", sent)
	}
}

func TestNotifyExecutionComplete_InboxChannel(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())

	task := &store.ScheduledTask{
		ID:              "task-1",
		Name:            "daily-report",
		Namespace:       "test-ns",
		TargetAgentName: "Reporter",
		UserID:          strPtr("trent@example.org"),
		NotificationConfig: strPtr(`{
			"on_success": true,
			"on_failure": false,
			"channels": [{"type": "inbox"}]
		}`),
	}
	exec := &store.ScheduledTaskExecution{ID: "exec-1", Status: "completed"}

	sent := svc.NotifyExecutionComplete(t.Context(), exec, "sess-42", task, nil)
	if sent == "" {
		t.Fatal("expected a notifications_sent record")
	}
	if len(brk.published) != 1 {
		t.Fatalf("expected 1 published NotifyRequest, got %d", len(brk.published))
	}

	msg := brk.published[0]
	wantTopic := a2a.NotifyRequestTopic("test-ns", "trent@example.org")
	if msg.topic != wantTopic {
		t.Errorf("topic = %q, want %q", msg.topic, wantTopic)
	}
	var req a2a.NotifyRequest
	if err := json.Unmarshal(msg.payload, &req); err != nil {
		t.Fatalf("payload not a NotifyRequest: %v", err)
	}
	if req.RecipientUserID != "trent@example.org" || req.Mode != a2a.NotifyModeNotify || req.SenderAgent != "scheduled_task" {
		t.Errorf("unexpected NotifyRequest: %+v", req)
	}
	if req.Title != "daily-report completed" {
		t.Errorf("title = %q, want the task name and how the run ended", req.Title)
	}
	if req.Metadata["scheduled_task_id"] != "task-1" || req.Metadata["status"] != "completed" {
		t.Errorf("unexpected metadata: %v", req.Metadata)
	}
	if req.Metadata["session_id"] != "sess-42" {
		t.Errorf("session_id = %v, want sess-42", req.Metadata["session_id"])
	}
	if req.Metadata["agent_id"] != "Reporter" {
		t.Errorf("agent_id = %v, want Reporter", req.Metadata["agent_id"])
	}
	if req.Metadata["link_label"] != "View result" {
		t.Errorf("link_label = %v, want View result", req.Metadata["link_label"])
	}
}

func TestNotifyExecutionComplete_InboxSharedTaskSessionOwnerOnly(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())

	task := &store.ScheduledTask{
		ID:              "task-1",
		Name:            "daily-report",
		Namespace:       "test-ns",
		TargetAgentName: "Reporter",
		UserID:          strPtr("owner@example.org"),
		CreatedBy:       "creator@example.org",
		NotificationConfig: strPtr(`{
			"on_success": true,
			"channels": [{"type": "inbox"}]
		}`),
	}
	exec := &store.ScheduledTaskExecution{ID: "exec-1", Status: "completed"}

	svc.NotifyExecutionComplete(t.Context(), exec, "sess-42", task, nil)
	if len(brk.published) != 2 {
		t.Fatalf("expected 2 published NotifyRequests (owner + creator), got %d", len(brk.published))
	}
	byUser := map[string]a2a.NotifyRequest{}
	for _, m := range brk.published {
		var req a2a.NotifyRequest
		if err := json.Unmarshal(m.payload, &req); err != nil {
			t.Fatalf("payload not a NotifyRequest: %v", err)
		}
		byUser[req.RecipientUserID] = req
	}
	if got := byUser["owner@example.org"].Metadata["session_id"]; got != "sess-42" {
		t.Errorf("owner session_id = %v, want sess-42", got)
	}
	if got := byUser["creator@example.org"].Metadata["session_id"]; got != nil {
		t.Errorf("non-owner creator must NOT get a session deep-link, got session_id=%v", got)
	}
	if got := byUser["creator@example.org"].Metadata["link_label"]; got != nil {
		t.Errorf("non-owner creator must NOT get link_label, got %v", got)
	}
}

func TestNotifyExecutionComplete_InboxNoDeepLinkOnFailure(t *testing.T) {
	for _, tc := range []struct{ status, wantTitle string }{
		{"failed", "daily-report failed"},
		{"timeout", "daily-report timed out"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			brk := &notifTestBroker{}
			svc := NewNotificationService(brk, testLogger())
			task := &store.ScheduledTask{
				ID:        "task-1",
				Name:      "daily-report",
				Namespace: "test-ns",
				UserID:    strPtr("owner@example.org"),
				NotificationConfig: strPtr(`{
					"on_failure": true,
					"channels": [{"type": "inbox"}]
				}`),
			}
			exec := &store.ScheduledTaskExecution{ID: "exec-1", Status: tc.status}

			svc.NotifyExecutionComplete(t.Context(), exec, "sess-42", task, nil)
			if len(brk.published) != 1 {
				t.Fatalf("expected 1 published NotifyRequest, got %d", len(brk.published))
			}
			var req a2a.NotifyRequest
			if err := json.Unmarshal(brk.published[0].payload, &req); err != nil {
				t.Fatalf("payload not a NotifyRequest: %v", err)
			}
			if req.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", req.Title, tc.wantTitle)
			}
			if got := req.Metadata["session_id"]; got != nil {
				t.Errorf("a %s run must not stamp a session deep-link, got session_id=%v", tc.status, got)
			}
			if got := req.Metadata["status"]; got != tc.status {
				t.Errorf("status = %v, want %s", got, tc.status)
			}
		})
	}
}

func TestNotifyExecutionComplete_InboxArtifactGating(t *testing.T) {
	arts := []a2a.NotifyArtifact{{UserID: "trent@example.org", SessionID: "s1", Filename: "chart.png"}}
	cases := []struct {
		name       string
		cfg        string
		wantOnWire int
	}{
		{"opted in → artifacts on the wire", `{"on_success":true,"include_artifacts":true,"channels":[{"type":"inbox"}]}`, len(arts)},
		{"opted out → no artifacts", `{"on_success":true,"include_artifacts":false,"channels":[{"type":"inbox"}]}`, 0},
		{"explicit config without the flag → no artifacts", `{"on_success":true,"channels":[{"type":"inbox"}]}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			brk := &notifTestBroker{}
			svc := NewNotificationService(brk, testLogger())
			task := &store.ScheduledTask{
				ID: "task-1", Name: "daily-report", Namespace: "test-ns",
				UserID: strPtr("trent@example.org"), NotificationConfig: strPtr(c.cfg),
			}
			exec := &store.ScheduledTaskExecution{ID: "exec-1", Status: "completed"}

			if sent := svc.NotifyExecutionComplete(t.Context(), exec, "", task, arts); sent == "" {
				t.Fatal("expected a notifications_sent record")
			}
			if len(brk.published) != 1 {
				t.Fatalf("expected 1 published NotifyRequest, got %d", len(brk.published))
			}
			var req a2a.NotifyRequest
			if err := json.Unmarshal(brk.published[0].payload, &req); err != nil {
				t.Fatalf("payload not a NotifyRequest: %v", err)
			}
			if len(req.Artifacts) != c.wantOnWire {
				t.Errorf("got %d artifacts on the wire, want %d", len(req.Artifacts), c.wantOnWire)
			}
		})
	}
}

func TestNotifyExecutionComplete_NoConfigNotifiesTheOwner(t *testing.T) {
	cases := []struct {
		name   string
		config *string
		status string
		want   int
	}{
		{"no config, completed", nil, "completed", 1},
		{"no config, failed", nil, "failed", 1},
		{"no config, timeout", nil, "timeout", 1},
		{"empty config, completed", strPtr(""), "completed", 1},
		{"no config, cancelled", nil, "cancelled", 0},
		{"no config, skipped", nil, "skipped", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			brk := &notifTestBroker{}
			svc := NewNotificationService(brk, testLogger())
			task := &store.ScheduledTask{
				ID: "task-1", Name: "daily-report", Namespace: "test-ns",
				UserID: strPtr("alice@example.com"), CreatedBy: "alice@example.com",
				NotificationConfig: c.config, Source: "ui",
			}
			exec := &store.ScheduledTaskExecution{ID: "exec-1", Status: c.status}

			svc.NotifyExecutionComplete(t.Context(), exec, "", task, nil)

			if len(brk.published) != c.want {
				t.Fatalf("published %d NotifyRequests, want %d", len(brk.published), c.want)
			}
			if c.want == 0 {
				return
			}
			if got, want := brk.published[0].topic, a2a.NotifyRequestTopic("test-ns", "alice@example.com"); got != want {
				t.Errorf("topic = %q, want %q", got, want)
			}
			var req a2a.NotifyRequest
			if err := json.Unmarshal(brk.published[0].payload, &req); err != nil {
				t.Fatalf("payload not a NotifyRequest: %v", err)
			}
			if req.Category != a2a.CategoryScheduledTask {
				t.Errorf("category = %q, want %q so the owner's settings apply", req.Category, a2a.CategoryScheduledTask)
			}
			if want := scheduledInboxDedupPrefix + exec.ID; req.Dedup != want {
				t.Errorf("dedup = %q, want %q so a repeated finish of the run is delivered once", req.Dedup, want)
			}
		})
	}
}

func TestNotifyExecutionComplete_NoConfigDeliversArtifacts(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())
	task := &store.ScheduledTask{ID: "task-1", Name: "daily", Namespace: "test-ns", UserID: strPtr("alice@example.com")}
	arts := []a2a.NotifyArtifact{{UserID: "alice@example.com", SessionID: "s1", Filename: "chart.png"}}

	svc.NotifyExecutionComplete(t.Context(), &store.ScheduledTaskExecution{ID: "exec-1", Status: "completed"}, "", task, arts)

	if len(brk.published) != 1 {
		t.Fatalf("published %d NotifyRequests, want 1", len(brk.published))
	}
	var req a2a.NotifyRequest
	if err := json.Unmarshal(brk.published[0].payload, &req); err != nil {
		t.Fatalf("payload not a NotifyRequest: %v", err)
	}
	if len(req.Artifacts) != 1 {
		t.Errorf("got %d artifacts for a task with no config, want 1", len(req.Artifacts))
	}
}

func TestNotifyExecutionComplete_TaskWithoutOwnerRecordsAFailure(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())
	task := &store.ScheduledTask{ID: "task-1", Name: "orphan", Namespace: "test-ns"}

	sent := svc.NotifyExecutionComplete(t.Context(), &store.ScheduledTaskExecution{ID: "exec-1", Status: "completed"}, "", task, nil)

	if len(brk.published) != 0 {
		t.Fatalf("published %d NotifyRequests for a task with no owner, want 0", len(brk.published))
	}
	if !strings.Contains(sent, `"status":"failed"`) {
		t.Errorf("notifications_sent = %q, want a failed inbox record", sent)
	}
}

func TestNotifyExecutionComplete_ExplicitConfigStillWins(t *testing.T) {
	brk := &notifTestBroker{}
	svc := NewNotificationService(brk, testLogger())
	task := &store.ScheduledTask{
		ID: "task-1", Name: "quiet", Namespace: "test-ns",
		UserID:             strPtr("alice@example.com"),
		NotificationConfig: strPtr(`{"on_success":false,"on_failure":false,"channels":[]}`),
	}

	for _, status := range []string{"completed", "failed"} {
		svc.NotifyExecutionComplete(t.Context(), &store.ScheduledTaskExecution{ID: "exec-1", Status: status}, "", task, nil)
	}

	if len(brk.published) != 0 {
		t.Fatalf("a task that opted out published %d NotifyRequests, want 0", len(brk.published))
	}
}

func TestValidateNotificationConfig_AllowsInbox(t *testing.T) {
	if err := ValidateNotificationConfig(`{"channels":[{"type":"inbox"}]}`); err != nil {
		t.Errorf("inbox channel should validate, got %v", err)
	}
}

func TestScheduledNotificationTitle(t *testing.T) {
	for status, want := range map[string]string{
		"completed": "Weekly report completed",
		"failed":    "Weekly report failed",
		"timeout":   "Weekly report timed out",
		"cancelled": "Weekly report finished",
	} {
		if got := scheduledNotificationTitle("Weekly report", &store.ScheduledTaskExecution{Status: status}); got != want {
			t.Errorf("scheduledNotificationTitle(%q) = %q, want %q", status, got, want)
		}
	}
	stopped := &store.ScheduledTaskExecution{Status: "skipped", BudgetStop: new(store.BudgetStopFirst)}
	if got, want := scheduledNotificationTitle("Weekly report", stopped), "Weekly report stopped by a usage budget"; got != want {
		t.Errorf("budget-stopped title = %q, want %q", got, want)
	}
}
