// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type rawMock struct {
	server       *httptest.Server
	t            *testing.T
	mu           sync.Mutex
	bodies       [][]byte
	reject       bool
	closedWindow int
	adapter      *Adapter
}

func newRawMock(t *testing.T) *rawMock {
	t.Helper()
	m := &rawMock{t: t}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		reject := m.reject
		windowShut := m.closedWindow > 0
		if windowShut {
			m.closedWindow--
		}
		if !reject && !windowShut {
			m.bodies = append(m.bodies, b)
		}
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if windowShut {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":131047,"message":"Re-engagement message required"}}`))
			return
		}
		if reject {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":131026,"message":"Message undeliverable"}}`))
			return
		}
		_, _ = w.Write([]byte(mockSendResponse()))
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *rawMock) rejectAll(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reject = on
}

func (m *rawMock) refuseWindow(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closedWindow = n
}

func (m *rawMock) recorded() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.bodies)
}

func (m *rawMock) all() [][]byte {
	if m.adapter != nil {
		m.t.Helper()
		waitQueueDrained(m.t, m.adapter)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.bodies)
}

func newHILRig(t *testing.T, logger ...*slog.Logger) (*Adapter, *rawMock) {
	t.Helper()
	log := rigLogger(logger)
	mock := newRawMock(t)
	cfg := &WhatsAppConfig{
		AccessToken:         "tok-test",
		PhoneNumberID:       "555111",
		GraphAPIVersion:     "v26.0",
		GraphAPIBaseURL:     mock.server.URL,
		DisableRateLimiting: true,
		MaxMediaSizeMB:      7,
		DefaultAgentName:    "Builder",
	}
	a := &Adapter{
		core:       newTestCore(t),
		cfg:        cfg,
		logger:     log,
		claims:     newMemoryClaims(),
		claimLease: defaultClaimLease,
		hilDedup:   runtime.NewMapCache(),
		client:     NewClient(cfg, log),
		ctx:        t.Context(),
	}
	startTestSender(t, a)
	mock.adapter = a
	return a, mock
}

func inputNeededEvent(t *testing.T, requestID string, surface json.RawMessage) *a2a.TaskStatusUpdateEvent {
	t.Helper()
	reqData := a2a.UserInputRequestData{Type: "user_input_request", RequestID: requestID, Surface: surface}
	b, err := json.Marshal(reqData)
	if err != nil {
		t.Fatalf("marshal request data: %v", err)
	}
	var dataMap map[string]any
	if err := json.Unmarshal(b, &dataMap); err != nil {
		t.Fatalf("unmarshal request data: %v", err)
	}
	return &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateInputNeeded,
			Message: &a2a.Message{Parts: []a2a.Part{{Kind: "data", Data: dataMap}}},
		},
	}
}

func interactiveReply(from, wamid, replyID string) (*webhookChangeValue, *inboundMessage) {
	msg := &inboundMessage{
		From: from, ID: wamid, Type: "interactive",
		Interactive: &inboundInteractive{Type: "button_reply", ButtonReply: &replyRef{ID: replyID}},
	}
	return &webhookChangeValue{Messages: []inboundMessage{*msg}}, msg
}

func TestAdapter_HIL_ButtonRoundTrip(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, mock := newHILRig(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	_ = a.core.Tasks().Register(taskT1, "Builder", "user-1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess"})

	event := inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA))
	a.OnStatusUpdate(t.Context(), taskT1, event, nil)

	pending := waitPendingPrompt(t, a, phone)
	if pending.RequestID != "req-1" || pending.TaskID != taskT1 {
		t.Fatalf("pending row = %+v, want requestID=req-1 taskID=t1", pending)
	}

	bodies := mock.all()
	if len(bodies) != 1 {
		t.Fatalf("sent %d messages, want 1 interactive prompt", len(bodies))
	}
	var sent outboundInteractiveMessage
	if err := json.Unmarshal(bodies[0], &sent); err != nil {
		t.Fatalf("decode interactive prompt: %v", err)
	}
	if sent.Type != "interactive" || sent.Interactive.Type != "button" {
		t.Fatalf("prompt type = %q/%q, want interactive/button", sent.Type, sent.Interactive.Type)
	}
	var optButtons int
	for _, b := range sent.Interactive.Action.Buttons {
		if b.Reply.ID == optionRefID(0) || b.Reply.ID == optionRefID(1) {
			optButtons++
		}
	}
	if optButtons != 2 {
		t.Fatalf("option buttons = %d, want 2 (Red, Blue; Other dropped): %+v",
			optButtons, sent.Interactive.Action.Buttons)
	}

	if pending.PromptWamid == "" {
		t.Fatal("the prompt must be recorded with its wamid before a reply can correlate")
	}
	value, msg := interactiveReply(phone, "wamid.r1", "opt:1")
	msg.Context = &messageContext{From: phone, ID: pending.PromptWamid}
	value.Messages[0].Context = msg.Context
	a.deliver(value, msg)
	if pendingPromptRow(t, a, phone) != nil {
		t.Error("the pending HIL prompt should be cleared after a valid reply")
	}
}

func TestAdapter_HIL_TextFallback(t *testing.T) {
	t.Parallel()

	newPending := func(t *testing.T, s *a2ui.Surface, logger ...*slog.Logger) (*Adapter, *rawMock, *pendingHIL) {
		t.Helper()
		a, mock := newHILRig(t, logger...)
		p := &pendingHIL{requestID: "req-x", taskID: taskT1, agentName: "Builder", userID: "u", fromPhone: "15551230000", surface: s}
		return a, mock, p
	}

	t.Run("numeric choice resolves", func(t *testing.T) {
		a, _, p := newPending(t, choiceSurface("Apple", "Banana", "Cherry"))
		if a.handleHILText(p, "2") != outcomeSubmitted {
			t.Fatal("numeric choice should resolve the request")
		}
	})

	t.Run("label match resolves", func(t *testing.T) {
		a, _, p := newPending(t, choiceSurface("Apple", "Banana"))
		if a.handleHILText(p, " banana ") != outcomeSubmitted {
			t.Fatal("label match should resolve the request")
		}
	})

	t.Run("unrecognized reply re-prompts and stays pending", func(t *testing.T) {
		logs := &recordCollector{}
		a, mock, p := newPending(t, choiceSurface("Apple", "Banana"), slog.New(logs))
		if a.handleHILText(p, "kiwi") == outcomeSubmitted {
			t.Fatal("unrecognized reply must NOT resolve the request")
		}
		if len(mock.all()) != 1 {
			t.Errorf("expected a re-prompt to be sent, got %d messages", len(mock.all()))
		}
		found := false
		for _, r := range logs.all() {
			if r.Message == "text reply matches no option of the pending prompt" {
				found = true
				if r.Level != slog.LevelWarn {
					t.Errorf("no-match text reply logged at %v, want Warn", r.Level)
				}
			}
		}
		if !found {
			t.Error("no record for the unmatched text reply — the re-prompt must leave a visible trace")
		}
	})

	t.Run("free-text field binds the typed answer", func(t *testing.T) {
		s := &a2ui.Surface{Fields: []a2ui.Field{{Kind: a2ui.FieldText, Path: "/answers/q0"}}}
		a, _, p := newPending(t, s)
		if a.handleHILText(p, "the answer") != outcomeSubmitted {
			t.Fatal("free-text reply should resolve the request")
		}
	})

	t.Run("empty surface cancels", func(t *testing.T) {
		a, _, p := newPending(t, &a2ui.Surface{})
		if a.handleHILText(p, "anything") != outcomeSubmitted {
			t.Fatal("a surface with no field should cancel-resolve")
		}
	})
}

func TestAdapter_HandleHILReply_DeclinedTapWarnsAndRePrompts(t *testing.T) {
	t.Parallel()
	logs := &recordCollector{}
	a, mock := newHILRig(t, slog.New(logs))
	p := &pendingHIL{requestID: "r", taskID: taskT1, agentName: "Builder", userID: "u", fromPhone: "p", surface: choiceSurface("Yes", "No")}

	if a.handleHILReply(p, &inboundMessage{Interactive: &inboundInteractive{ButtonReply: &replyRef{ID: "opt:0"}}}) != outcomeSubmitted {
		t.Error("valid option reply should resolve")
	}
	if got := a.handleHILReply(p, &inboundMessage{ID: "wamid.noid", Interactive: &inboundInteractive{Type: "button_reply"}}); got != outcomeDeclined {
		t.Errorf("reply with no interactive id = %v, want outcomeDeclined", got)
	}
	if got := a.handleHILReply(p, &inboundMessage{ID: "wamid.bogus", Interactive: &inboundInteractive{ButtonReply: &replyRef{ID: "bogus"}}}); got != outcomeDeclined {
		t.Errorf("unparseable reply id = %v, want outcomeDeclined", got)
	}

	if got := len(mock.all()); got != 2 {
		t.Errorf("re-prompts sent = %d, want 2 — a declined tap must not leave the user without a response", got)
	}

	for _, wantMsg := range []string{"interactive reply without a reply id ignored", "unrecognized interactive reply id"} {
		found := false
		for _, r := range logs.all() {
			if r.Message == wantMsg {
				found = true
				if r.Level != slog.LevelWarn {
					t.Errorf("%q logged at %v, want Warn", wantMsg, r.Level)
				}
			}
			if r.Message == "interactive reply without a reply id ignored" {
				r.Attrs(func(at slog.Attr) bool {
					if at.Key == "interactiveType" && at.Value.String() != "button_reply" {
						t.Errorf("interactiveType = %q, want button_reply", at.Value.String())
					}
					return true
				})
			}
		}
		if !found {
			t.Errorf("no %q record — an ignored interactive reply must leave a visible trace", wantMsg)
		}
	}
}

func TestAdapter_ResolvePendingHIL_NonReply(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, mock := newHILRig(t)
	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{TaskID: taskT1, RequestID: "req-1"})

	value := &webhookChangeValue{}
	msg := &inboundMessage{From: phone, ID: "wamid.img", Type: "image"}
	a.deliver(value, msg)

	if pendingPromptRow(t, a, phone) == nil {
		t.Error("a non-reply message must keep the prompt pending")
	}
	if len(mock.all()) != 1 {
		t.Errorf("expected a nudge message, got %d", len(mock.all()))
	}
}

func TestHILTTL(t *testing.T) {
	t.Parallel()
	if d := hilTTL(""); d != defaultHILTTL {
		t.Errorf("empty expiresAt → %v, want default %v", d, defaultHILTTL)
	}
	if d := hilTTL("not-a-timestamp"); d != defaultHILTTL {
		t.Errorf("garbage expiresAt → %v, want default", d)
	}
	if d := hilTTL("2000-01-01T00:00:00Z"); d != defaultHILTTL {
		t.Errorf("past expiresAt → %v, want default (non-positive collapses)", d)
	}
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	if d := hilTTL(future); d <= 0 || d > 2*time.Hour {
		t.Errorf("future expiresAt → %v, want a positive duration ≤ 2h", d)
	}
}

func TestTruncate_Edges(t *testing.T) {
	t.Parallel()
	if got := truncate("hello", 0); got != "" {
		t.Errorf("max=0 → %q, want empty", got)
	}
	if got := truncate("hello", 1); got != "h" {
		t.Errorf("max=1 → %q, want single rune (no ellipsis)", got)
	}
}

func TestClassifySurface_Edges(t *testing.T) {
	t.Parallel()
	onlyOther := &a2ui.Surface{Fields: []a2ui.Field{{
		Kind:    a2ui.FieldSingleChoice,
		Options: []a2ui.Option{{Label: "Other", Value: a2ui.OtherSentinel}},
	}}}
	if mode, _ := classifySurface(onlyOther); mode != hilText {
		t.Errorf("only-Other field: mode=%v, want text", mode)
	}

	wide := choiceSurface("Short", "Also short", "twenty-two-char-label!")
	if mode, _ := classifySurface(wide); mode != hilList {
		t.Errorf("wide-label trio: mode=%v, want list (button cap exceeded)", mode)
	}
}

const askSurfaceSingleChoiceWA = `{
  "surfaceId": "req-1",
  "catalogId": "https://a2ui.org/specification/v0_9/basic_catalog.json",
  "components": [
    {"id":"msg","component":"Text","text":{"literalString":"Pick one"},"variant":"body"},
    {"id":"q0-label","component":"Text","text":{"literalString":"Favorite color?"},"variant":"h3"},
    {"id":"q0-picker","component":"ChoicePicker","label":{"literalString":"Color"},"variant":"mutuallyExclusive",
      "options":[
        {"label":{"literalString":"Red"},"value":"Red","description":{"literalString":""}},
        {"label":{"literalString":"Blue"},"value":"Blue","description":{"literalString":""}},
        {"label":{"literalString":"Other"},"value":"__other__"}
      ],
      "value":{"path":"/answers/q0"}},
    {"id":"q0-other","component":"TextField","label":{"literalString":"Please specify"},"value":{"path":"/other_text/q0"}},
    {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
    {"id":"submit-btn","component":"Button","child":"submit-text","variant":"primary",
      "action":{"event":{"name":"submit","context":{"answers":{"path":"/answers"},"other_text":{"path":"/other_text"}}}}},
    {"id":"cancel-text","component":"Text","text":{"literalString":"Cancel"}},
    {"id":"cancel-btn","component":"Button","child":"cancel-text",
      "action":{"event":{"name":"cancel","context":{}}}}
  ],
  "dataModel": {"answers":{"q0":""},"other_text":{"q0":""}}
}`

func TestAdapter_UndeliveredPromptDoesNotSwallowTheNextMessage(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, mock := newHILRig(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	_ = a.core.Tasks().Register("t-hil", "Builder", "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata("t-hil", &taskMeta{FromPhone: phone, SessionID: "sess"})

	a.sender.claimCancel()
	a.handleUserInputRequest("t-hil", &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})

	value, msg := textMessage("wamid.early", phone, "actually never mind")
	a.deliver(value, msg)

	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) < 2 {
		t.Errorf("tasks for the agent = %d, want the message to have started a new one", len(ids))
	}
	for _, body := range mock.recorded() {
		if bytes.Contains(body, []byte("answer")) || bytes.Contains(body, []byte("question")) {
			t.Errorf("the message was treated as an answer to the unsent prompt: %s", body)
		}
	}
	if queued, err := a.outbound.PendingCount(t.Context(), a.gatewayID()); err != nil || queued == 0 {
		t.Errorf("the queued prompt must stay queued (%d, err %v); it is still going to be delivered", queued, err)
	}
}

func TestAdapter_TapOnASupersededPromptDoesNotAnswerTheNewOne(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, mock := newHILRig(t)

	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{
		RequestID: "req-B", TaskID: taskTB, AgentName: "Builder", UserID: "u",
		PromptWamid: "wamid.PROMPT-B"})

	value, msg := interactiveReply(phone, "wamid.tap-a", optionRefID(1))
	msg.Context = &messageContext{From: phone, ID: "wamid.PROMPT-A"}
	value.Messages[0].Context = msg.Context
	a.deliver(value, msg)

	if pendingPromptRow(t, a, phone) == nil {
		t.Error("a tap on a different prompt must not resolve the pending one")
	}
	var told bool
	for _, body := range mock.all() {
		if bytes.Contains(body, []byte("no longer active")) {
			told = true
		}
	}
	if !told {
		t.Errorf("the user must be told their tap was for a retired question, got %d sends", len(mock.all()))
	}
}

func TestAdapter_PromptDeliveredWithoutAWamidStaysTappable(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, mock := newHILRig(t)
	_ = a.core.Tasks().Register(taskT1, "Builder", "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess"})

	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{
		RequestID: "req-1", TaskID: taskT1, AgentName: "Builder", UserID: "whatsapp:" + phone})

	value, msg := interactiveReply(phone, "wamid.tap", optionRefID(1))
	msg.Context = &messageContext{From: phone, ID: "wamid.SOMETHING"}
	value.Messages[0].Context = msg.Context
	a.deliver(value, msg)

	if pendingPromptRow(t, a, phone) != nil {
		t.Error("the tap should have resolved the prompt; an uncorrelatable prompt is not an unanswerable one")
	}
	for _, body := range mock.all() {
		if bytes.Contains(body, []byte("no longer active")) {
			t.Errorf("the user was told their answer was stale: %s", body)
		}
	}
}
