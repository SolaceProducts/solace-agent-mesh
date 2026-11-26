// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const emailAskSurfaceJSON = `{
  "surfaceId":"req-1",
  "components":[
    {"id":"q0-label","component":"Text","text":{"literalString":"Favorite color?"},"variant":"h3"},
    {"id":"q0-picker","component":"ChoicePicker","label":{"literalString":"Color"},"variant":"mutuallyExclusive",
      "options":[{"label":{"literalString":"Red"},"value":"Red"},{"label":{"literalString":"Blue"},"value":"Blue"}],
      "value":{"path":"/answers/q0"}},
    {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
    {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
      "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"}}}}},
    {"id":"cancel-text","component":"Text","text":{"literalString":"Cancel"}},
    {"id":"cancel-btn","component":"Button","child":"cancel-text","action":{"event":{"name":"cancel","context":{}}}}
  ],
  "dataModel":{"answers":{"q0":""}}
}`

func emailSurfaceT(t *testing.T) *a2ui.Surface {
	t.Helper()
	s, err := a2ui.Parse(json.RawMessage(emailAskSurfaceJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

func newHILServerTest(t *testing.T) (*hilServer, *broker.DevBroker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newHILServer(core, []byte("secret"), "https://gw.example.com", "test-gw", logger), brk
}

func registerHIL(t *testing.T, s *hilServer, reqID, taskID, agent string, surface *a2ui.Surface) string {
	t.Helper()
	expiry := time.Now().Add(10 * time.Minute)
	_ = s.core.Tasks().Register(taskID, agent, "u1", entrypoint.TaskIdentity{})
	s.register(reqID, &pendingHIL{surface: surface, taskID: taskID, agentName: agent, userID: "u1", expiry: expiry})
	token, err := signHILToken(hilToken{RequestID: reqID, TaskID: taskID, AgentName: agent, UserID: "u1", ExpiresAt: expiry.Unix()}, s.secret)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func postForm(t *testing.T, h http.Handler, token string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/userInput/"+token, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEmailHIL_GetThenPost_RoundTrip(t *testing.T) {
	s, brk := newHILServerTest(t)
	token := registerHIL(t, s, "req-1", "task-1", "Agent", emailSurfaceT(t))
	h := s.Handler()

	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/userInput/"+token, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET code = %d, want 200", getRec.Code)
	}
	body := getRec.Body.String()
	if !strings.Contains(body, `type="radio"`) {
		t.Errorf("form missing radio inputs")
	}
	if strings.Contains(body, `name="_fields"`) || strings.Contains(body, `name="_buttons"`) {
		t.Errorf("form must not ship client-trusted field/button metadata")
	}

	q, err := brk.CreateQueue("email-hil", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Agent")); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"_btn": {"submit-btn"}, "f0": {"Blue"}}
	if rec := postForm(t, h, token, form); rec.Code != http.StatusOK {
		t.Fatalf("POST code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	uir := emailRecv(t, brk, q)
	if uir["status"] != "answered" {
		t.Errorf("status = %v, want answered", uir["status"])
	}
	respData, _ := uir["data"].(map[string]any)
	answers, _ := respData["answers"].(map[string]any)
	if answers["q0"] != "Blue" {
		t.Errorf("answers.q0 = %v, want Blue", answers["q0"])
	}

	if rec := postForm(t, h, token, form); rec.Code != http.StatusGone {
		t.Errorf("replay code = %d, want 410", rec.Code)
	}
}

func TestEmailHIL_PostCannotForgeButtonContext(t *testing.T) {
	s, _ := newHILServerTest(t)
	token := registerHIL(t, s, "req-forge", "task-forge", "Agent", emailSurfaceT(t))
	h := s.Handler()

	form := url.Values{"_btn": {"evil-approve"}, "f0": {"Blue"}}
	if rec := postForm(t, h, token, form); rec.Code != http.StatusBadRequest {
		t.Errorf("forged _btn code = %d, want 400", rec.Code)
	}
}

func TestEmailHIL_MultiSelectAndOther(t *testing.T) {
	const surfaceJSON = `{
      "surfaceId":"req-mo",
      "components":[
        {"id":"q0-label","component":"Text","text":{"literalString":"Teams?"},"variant":"h3"},
        {"id":"q0-picker","component":"ChoicePicker","variant":"multipleSelection",
          "options":[{"label":{"literalString":"A"},"value":"A"},{"label":{"literalString":"B"},"value":"B"},{"label":{"literalString":"Other"},"value":"__other__"}],
          "value":{"path":"/answers/q0"}},
        {"id":"q0-other","component":"TextField","value":{"path":"/other_text/q0"}},
        {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
        {"id":"submit-btn","component":"Button","child":"submit-text",
          "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"},"other_text":{"path":"/other_text"}}}}}
      ],
      "dataModel":{"answers":{"q0":[]},"other_text":{"q0":""}}
    }`
	surface, err := a2ui.Parse(json.RawMessage(surfaceJSON))
	if err != nil {
		t.Fatal(err)
	}
	s, brk := newHILServerTest(t)
	token := registerHIL(t, s, "req-mo", "task-mo", "Agent", surface)

	q, err := brk.CreateQueue("email-mo", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Agent")); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"_btn": {"submit-btn"}, "f0": {"B", "__other__"}, "f1": {"Zeta"}}
	if rec := postForm(t, s.Handler(), token, form); rec.Code != http.StatusOK {
		t.Fatalf("POST code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	uir := emailRecv(t, brk, q)
	respData, _ := uir["data"].(map[string]any)
	answers, _ := respData["answers"].(map[string]any)
	got, _ := answers["q0"].([]any)
	if len(got) != 2 || got[0] != "B" || got[1] != "__other__" {
		t.Errorf("answers.q0 = %v, want [B __other__]", answers["q0"])
	}
	other, _ := respData["other_text"].(map[string]any)
	if other["q0"] != "Zeta" {
		t.Errorf("other_text.q0 = %v, want Zeta", other["q0"])
	}
}

func TestEmailHIL_Cancel(t *testing.T) {
	s, brk := newHILServerTest(t)
	token := registerHIL(t, s, "req-cxl", "task-cxl", "Agent", emailSurfaceT(t))

	q, err := brk.CreateQueue("email-cxl", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Agent")); err != nil {
		t.Fatal(err)
	}

	if rec := postForm(t, s.Handler(), token, url.Values{"_btn": {"cancel-btn"}}); rec.Code != http.StatusOK {
		t.Fatalf("cancel POST code = %d, want 200", rec.Code)
	}
	uir := emailRecv(t, brk, q)
	if uir["status"] != "cancelled" {
		t.Errorf("status = %v, want cancelled", uir["status"])
	}
}

func TestEmailHIL_EscapesSurfaceText(t *testing.T) {
	const surfaceJSON = `{
      "surfaceId":"req-xss",
      "components":[
        {"id":"q0-label","component":"Text","text":{"literalString":"<script>alert(1)</script>"},"variant":"h3"},
        {"id":"q0-text","component":"TextField","value":{"path":"/answers/q0"}},
        {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
        {"id":"submit-btn","component":"Button","child":"submit-text",
          "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"}}}}}
      ],
      "dataModel":{"answers":{"q0":""}}
    }`
	surface, err := a2ui.Parse(json.RawMessage(surfaceJSON))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := newHILServerTest(t)
	token := registerHIL(t, s, "req-xss", "task-xss", "Agent", surface)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/userInput/"+token, nil))
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("surface label rendered unescaped — XSS risk")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped label in body")
	}
}

func TestEmailHIL_StaleTaskRejected(t *testing.T) {
	s, _ := newHILServerTest(t)
	expiry := time.Now().Add(10 * time.Minute)
	s.register("req-stale", &pendingHIL{surface: emailSurfaceT(t), taskID: "task-stale", agentName: "Agent", userID: "u1", expiry: expiry})
	token, _ := signHILToken(hilToken{RequestID: "req-stale", TaskID: "task-stale", AgentName: "Agent", UserID: "u1", ExpiresAt: expiry.Unix()}, s.secret)

	if rec := postForm(t, s.Handler(), token, url.Values{"_btn": {"submit-btn"}, "f0": {"Blue"}}); rec.Code != http.StatusGone {
		t.Errorf("stale-task POST code = %d, want 410", rec.Code)
	}
}

func TestEmailHIL_ExpiredToken(t *testing.T) {
	s, _ := newHILServerTest(t)
	token, _ := signHILToken(hilToken{RequestID: "req-x", ExpiresAt: time.Now().Add(-time.Minute).Unix()}, s.secret)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/userInput/"+token, nil))
	if rec.Code != http.StatusGone {
		t.Errorf("GET expired code = %d, want 410", rec.Code)
	}
}

func TestEmailHIL_BadTokenIs404(t *testing.T) {
	s, _ := newHILServerTest(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/userInput/garbage", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET bad token code = %d, want 404", rec.Code)
	}
}

func emailRecv(t *testing.T, brk *broker.DevBroker, q broker.Queue) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no broker message: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	params, _ := req.Params.(map[string]any)
	msgData, _ := params["message"].(map[string]any)
	parts, _ := msgData["parts"].([]any)
	part, _ := parts[0].(map[string]any)
	data, _ := part["data"].(map[string]any)
	return data
}

func TestEmailHIL_Capability(t *testing.T) {
	off := &EmailAdapter{}
	if off.Capabilities()[tool.CapInteractiveUserInput] {
		t.Error("capability true with no HIL server, want false")
	}
	on := &EmailAdapter{hil: &hilServer{}}
	if !on.Capabilities()[tool.CapInteractiveUserInput] {
		t.Error("capability false with HIL server, want true")
	}
}

func TestEmailHIL_OnStatusUpdate_SendsLink(t *testing.T) {
	s, _ := newHILServerTest(t)
	rep := newFakeReplier()
	a := &EmailAdapter{
		cfg:     &EmailConfig{HILTokenTTL: 10 * time.Minute},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		core:    s.core,
		replier: rep,
		hil:     s,
	}
	_ = a.core.Tasks().Register("task-1", "Agent", "u1", entrypoint.TaskIdentity{})
	meta := &EmailTaskMeta{UserID: "u1", ReplyTo: EmailAddress{Address: "user@example.com"}, Subject: "Re: hello"}
	a.taskMeta.Store("task-1", meta)

	ev := &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type":       "user_input_request",
				"request_id": "req-1",
				"surface":    json.RawMessage(emailAskSurfaceJSON),
			})}},
		},
	}
	a.OnStatusUpdate(t.Context(), "task-1", ev, nil)

	if len(rep.calls) != 1 {
		t.Fatalf("replier calls = %d, want 1", len(rep.calls))
	}
	if !strings.Contains(rep.calls[0].BodyText, "/gw/test-gw/userInput/") {
		t.Errorf("reply body missing magic link: %q", rep.calls[0].BodyText)
	}
	meta.streamingMu.Lock()
	buffered := meta.streamingBuffer.String()
	meta.streamingMu.Unlock()
	if buffered != "" {
		t.Errorf("streaming buffer = %q, want empty (HIL surface not buffered)", buffered)
	}
}

func TestEmailHIL_GetUnknownRequest(t *testing.T) {
	s, _ := newHILServerTest(t)
	token, _ := signHILToken(hilToken{RequestID: "req-missing", TaskID: "t", AgentName: "Agent", UserID: "u1", ExpiresAt: time.Now().Add(time.Minute).Unix()}, s.secret)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/userInput/"+token, nil))
	if rec.Code != http.StatusGone {
		t.Errorf("GET unknown request code = %d, want 410", rec.Code)
	}
}

func TestEmailHIL_RegisterSweepsExpired(t *testing.T) {
	s, _ := newHILServerTest(t)
	s.register("old", &pendingHIL{surface: emailSurfaceT(t), taskID: "t-old", expiry: time.Now().Add(-time.Minute)})
	s.register("new", &pendingHIL{surface: emailSurfaceT(t), taskID: "t-new", expiry: time.Now().Add(time.Minute)})
	if _, ok := s.peek("old"); ok {
		t.Error("expired entry should have been swept on register")
	}
	if _, ok := s.peek("new"); !ok {
		t.Error("live entry should remain")
	}
}
