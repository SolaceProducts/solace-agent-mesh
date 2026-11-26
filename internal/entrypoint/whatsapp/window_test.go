// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type windowMock struct {
	server *httptest.Server

	mu           sync.Mutex
	sends        []outboundTextMessage
	closedWindow int
}

func newWindowMock(t *testing.T) *windowMock {
	t.Helper()
	m := &windowMock{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body outboundTextMessage
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		var status outboundReadStatus
		_ = json.Unmarshal(raw, &status)
		if status.Status != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}

		m.mu.Lock()
		refuse := m.closedWindow > 0
		if refuse {
			m.closedWindow--
		} else {
			m.sends = append(m.sends, body)
		}
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if refuse {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":131047,"message":"Re-engagement message required","error_data":{"details":"to: 15551234567"}}}`))
			return
		}
		_, _ = w.Write([]byte(mockSendResponse()))
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *windowMock) refuseNext(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closedWindow = n
}

func (m *windowMock) delivered() []outboundTextMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]outboundTextMessage, len(m.sends))
	copy(out, m.sends)
	return out
}

func (m *windowMock) waitDelivered(t *testing.T, n int) []outboundTextMessage {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := m.delivered(); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d deliveries; got %d", n, len(m.delivered()))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type windowRig struct {
	adapter *Adapter
	mock    *windowMock
	metrics *countingMetrics
	logs    *recordCollector
}

func newWindowRig(t *testing.T, dsn ...string) *windowRig {
	t.Helper()
	mock := newWindowMock(t)
	metrics := newCountingMetrics()
	logs := &recordCollector{}
	logger := slog.New(logs)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var appConfig map[string]any
	if len(dsn) > 0 {
		appConfig = map[string]any{
			"namespace":  "test-ns",
			"gateway_id": "test-gw",
			"session_service": map[string]any{
				"type": "sql", "database_url": dsn[0], "default_behavior": "PERSISTENT",
			},
		}
	} else {
		appConfig = testAppConfig(t, "test-gw")
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, appConfig)
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithMetrics(metrics)).Services())
	initCoreForTest(t, core)

	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: mock.server.URL, MaxMediaSizeMB: 7,
		DefaultAgentName: "Builder", DisableRateLimiting: true,
	}
	a := &Adapter{
		core: core, cfg: cfg, logger: logger,
		claims: newMemoryClaims(), claimLease: defaultClaimLease, hilDedup: runtime.NewMapCache(),
		client: NewClient(cfg, logger), ctx: t.Context(),
	}
	startTestSender(t, a)
	return &windowRig{adapter: a, mock: mock, metrics: metrics, logs: logs}
}

func (r *windowRig) heardFrom(t *testing.T, sender string, ago time.Duration) {
	t.Helper()
	at := time.Now().Add(-ago).UnixMilli()
	_, err := r.adapter.conversations.RecordInbound(context.Background(), &store.WhatsAppConversation{
		GatewayID: r.adapter.gatewayID(), SenderID: sender, WaID: sender, FromID: sender,
		LastInboundAt: at, CreatedAt: at, UpdatedAt: at,
	}, sessionIdleReset)
	if err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
}

func (r *windowRig) waitParked(t *testing.T, sender string) *store.WhatsAppOutbound {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		row, err := r.adapter.outbound.GetParked(context.Background(), r.adapter.gatewayID(), sender)
		if err != nil {
			t.Fatalf("GetParked: %v", err)
		}
		if row != nil {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for a reply to be parked")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (r *windowRig) rowState(t *testing.T, id string) string {
	t.Helper()
	row, err := r.adapter.outbound.Get(context.Background(), id)
	if err != nil || row == nil {
		t.Fatalf("Get %s = %v, err %v", id, row, err)
	}
	return row.State
}

func (r *windowRig) sawLog(msg string) bool {
	for _, rec := range r.logs.all() {
		if rec.Message == msg {
			return true
		}
	}
	return false
}

func TestAdapter_ClosedWindowParksTheReplyInsteadOfLosingIt(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230000"
	rig.heardFrom(t, phone, 25*time.Hour)

	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask("the late answer"), nil)

	parked := rig.waitParked(t, phone)
	if !strings.Contains(parked.Payload, "the late answer") {
		t.Errorf("parked payload = %q, want the agent's reply held intact", parked.Payload)
	}
	if got := len(rig.mock.delivered()); got != 0 {
		t.Errorf("sent %d messages to a closed window; the pre-check should have parked instead", got)
	}
	if rig.metrics.total(metricWhatsAppParkedReplies) != 1 {
		t.Errorf("parked replies counted = %d, want 1", rig.metrics.total(metricWhatsAppParkedReplies))
	}
	if !rig.sawLog("parked a whatsapp reply until the customer-service window reopens") {
		t.Error("the park must be visible in the log; a silent park is the silent drop again")
	}
}

func TestAdapter_MetaWindowRejectionParksTheReply(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230001"
	rig.heardFrom(t, phone, time.Minute)
	rig.mock.refuseNext(1)

	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask("clock-skew answer"), nil)

	parked := rig.waitParked(t, phone)
	if parked.ErrorCode != codeWindowClosed {
		t.Errorf("parked errorCode = %d, want %d recorded on the row", parked.ErrorCode, codeWindowClosed)
	}
	if !strings.Contains(parked.ErrorDetail, "15551234567") {
		t.Errorf("errorDetail = %q, want Meta's reason kept on the row (never in a log)", parked.ErrorDetail)
	}
	if parked.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — a refused window is a deferral, not a spent attempt", parked.Attempts)
	}
}

func TestAdapter_NextInboundReleasesTheParkedReply(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230002"
	rig.heardFrom(t, phone, 25*time.Hour)
	rig.adapter.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask("the late answer"), nil)
	parked := rig.waitParked(t, phone)

	value, msg := textMessage("wamid.reopen", phone, "are you still there?")
	rig.adapter.deliver(value, msg)

	delivered := rig.mock.waitDelivered(t, 1)
	if !strings.Contains(delivered[0].Text.Body, "the late answer") {
		t.Fatalf("delivered %q, want the parked reply", delivered[0].Text.Body)
	}
	if !strings.HasPrefix(delivered[0].Text.Body, msgParkedReplyPrefix) {
		t.Errorf("delivered %q, want it to open with %q so the user knows what it answers",
			delivered[0].Text.Body, msgParkedReplyPrefix)
	}
	if state := rig.rowState(t, parked.ID); state == store.WhatsAppOutboundParked {
		t.Error("the row is still parked after the window reopened")
	}
	if rig.metrics.total(metricWhatsAppParkedReplies) != 2 {
		t.Errorf("park counter = %d, want 2 (parked, then released)", rig.metrics.total(metricWhatsAppParkedReplies))
	}
}

func TestAdapter_SupersededParkIsCountedNotSilentlyDropped(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230003"
	rig.heardFrom(t, phone, 25*time.Hour)

	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask("the older answer"), nil)
	first := rig.waitParked(t, phone)

	registerReplyTask(rig.adapter, taskT2, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT2, completedTask("the newer answer"), nil)

	deadline := time.Now().Add(10 * time.Second)
	for rig.rowState(t, first.ID) == store.WhatsAppOutboundParked {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the older park to be displaced")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := rig.rowState(t, first.ID); got != store.WhatsAppOutboundSuperseded {
		t.Errorf("displaced park = %q, want superseded — 'failed' has to keep meaning genuine non-delivery", got)
	}
	if rig.metrics.total(metricWhatsAppParkedSuperseded) != 1 {
		t.Errorf("superseded counter = %d, want 1", rig.metrics.total(metricWhatsAppParkedSuperseded))
	}
	newest := rig.waitParked(t, phone)
	if !strings.Contains(newest.Payload, "the newer answer") {
		t.Errorf("live park = %q, want the newest reply", newest.Payload)
	}
}

func TestAdapter_StaleParkIsRetiredRatherThanDelivered(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230004"
	rig.heardFrom(t, phone, 25*time.Hour)
	rig.adapter.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask("a week-old answer"), nil)
	parked := rig.waitParked(t, phone)

	aged := time.Now().Add(-parkTTL - time.Hour).UnixMilli()
	if _, err := ledgerDB(rig.adapter).ExecContext(context.Background(),
		`UPDATE whatsapp_outbound SET parked_at = ? WHERE id = ?`, aged, parked.ID); err != nil {
		t.Fatalf("age the park: %v", err)
	}

	value, msg := textMessage("wamid.late", phone, "hello again")
	rig.adapter.deliver(value, msg)

	if got := rig.rowState(t, parked.ID); got != store.WhatsAppOutboundSuperseded {
		t.Errorf("stale park = %q, want superseded", got)
	}
	for _, sent := range rig.mock.delivered() {
		if strings.Contains(sent.Text.Body, "a week-old answer") {
			t.Fatal("a park past its TTL was delivered anyway")
		}
	}
	if rig.metrics.total(metricWhatsAppParkedSuperseded) != 1 {
		t.Errorf("superseded counter = %d, want 1", rig.metrics.total(metricWhatsAppParkedSuperseded))
	}
}

func TestAdapter_WindowClosesEarlyByTheSkewMargin(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)

	rig.heardFrom(t, "inside", conversationWindow-windowSkewMargin-time.Minute)
	if !rig.adapter.windowOpen(t.Context(), "inside") {
		t.Error("a conversation still inside the window minus the margin must be sendable")
	}

	rig.heardFrom(t, "margin", conversationWindow-windowSkewMargin+time.Minute)
	if rig.adapter.windowOpen(t.Context(), "margin") {
		t.Error("the last minutes before Meta's deadline must count as closed; there is no way to check their clock")
	}
}

func TestAdapter_UnknownConversationCountsAsOpen(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	if !rig.adapter.windowOpen(t.Context(), "never-heard-from") {
		t.Error("an unknown conversation must be sendable; Meta's 131047 is the backstop")
	}
}

func TestAdapter_NonContentMessageStillStampsTheWindow(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230005"
	rig.heardFrom(t, phone, 25*time.Hour)

	rig.adapter.deliver(&webhookChangeValue{Contacts: []webhookContact{{WaID: phone}}},
		&inboundMessage{From: phone, ID: "wamid.react", Type: "reaction"})

	if !rig.adapter.windowOpen(t.Context(), phone) {
		t.Error("a reaction reopens Meta's window as surely as a question does")
	}
}

func TestAdapter_InboundStoresBothSenderIdentifiers(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const bsuid = "US.13491208655302741918"
	rig.adapter.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	value, msg := textMessage("wamid.bsuid", bsuid, "hello")
	value.Contacts = []webhookContact{{WaID: "15551234567"}}
	rig.adapter.deliver(value, msg)

	conv, err := rig.adapter.conversations.Get(context.Background(), rig.adapter.gatewayID(), bsuid)
	if err != nil || conv == nil {
		t.Fatalf("conversation = %v, err %v", conv, err)
	}
	if conv.FromID != bsuid {
		t.Errorf("fromID = %q, want the opaque sender id every outbound row addresses", conv.FromID)
	}
	if conv.WaID != "15551234567" {
		t.Errorf("waID = %q, want the phone number the payload carried separately", conv.WaID)
	}
}

func TestContactPhone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value *webhookChangeValue
		msg   *inboundMessage
		want  string
	}{
		{name: "nil payload", value: nil, msg: &inboundMessage{From: "155"}, want: ""},
		{
			name:  "matched on the phone",
			value: &webhookChangeValue{Contacts: []webhookContact{{WaID: "155"}, {WaID: "166"}}},
			msg:   &inboundMessage{From: "155"},
			want:  "155",
		},
		{
			name: "matched on the BSUID even among several contacts",
			value: &webhookChangeValue{Contacts: []webhookContact{
				{WaID: "155", UserID: "US.111"},
				{WaID: "166", UserID: "US.222"},
			}},
			msg:  &inboundMessage{FromUserID: "US.222"},
			want: "166",
		},
		{
			name:  "a withheld number yields no phone",
			value: &webhookChangeValue{Contacts: []webhookContact{{UserID: "US.111"}}},
			msg:   &inboundMessage{FromUserID: "US.111"},
			want:  "",
		},
		{
			name:  "single-contact fallback needs a single message too",
			value: &webhookChangeValue{Contacts: []webhookContact{{WaID: "15551234567"}}, Messages: []inboundMessage{{FromUserID: "US.999"}}},
			msg:   &inboundMessage{FromUserID: "US.999"},
			want:  "15551234567",
		},
		{
			name: "a multi-message batch refuses the fallback",
			value: &webhookChangeValue{
				Contacts: []webhookContact{{WaID: "15551234567"}},
				Messages: []inboundMessage{{FromUserID: "US.999"}, {FromUserID: "US.888"}},
			},
			msg:  &inboundMessage{FromUserID: "US.999"},
			want: "",
		},
		{name: "no contacts", value: &webhookChangeValue{}, msg: &inboundMessage{From: "155"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contactPhone(tt.value, tt.msg); got != tt.want {
				t.Errorf("contactPhone = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSenderKeyPrefersThePhoneThenTheBSUID(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                       string
		msg                        inboundMessage
		contacts                   []webhookContact
		wantSender, wantContinuity string
	}{
		{"phone only leaves the continuity token empty, never the phone", inboundMessage{From: "15551234567"}, nil, "15551234567", ""},
		{"both present", inboundMessage{From: "15551234567", FromUserID: "US.111"}, nil, "15551234567", "US.111"},
		{"BSUID only", inboundMessage{FromUserID: "US.111"}, nil, "US.111", "US.111"},
		{"neither", inboundMessage{}, nil, "", ""},
		{
			"BSUID carried only on the contact",
			inboundMessage{From: "15551234567"},
			[]webhookContact{{WaID: "15551234567", UserID: "US.222"}},
			"15551234567", "US.222",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.msg.senderID(); got != tt.wantSender {
				t.Errorf("senderID = %q, want %q", got, tt.wantSender)
			}
			value := &webhookChangeValue{Contacts: tt.contacts, Messages: []inboundMessage{tt.msg}}
			if got := continuityTokenFor(value, &tt.msg); got != tt.wantContinuity {
				t.Errorf("continuityTokenFor = %q, want %q", got, tt.wantContinuity)
			}
		})
	}
}

func TestParkRetireReason(t *testing.T) {
	t.Parallel()
	nowMs := time.Now().UnixMilli()

	t.Run("still worth delivering", func(t *testing.T) {
		row := &store.WhatsAppOutbound{CreatedAt: nowMs, ParkedAt: nowMs, Payload: `{"text":"answer"}`}
		if got := parkRetireReason(row, nowMs); got != "" {
			t.Errorf("retire = %q, want delivery", got)
		}
	})

	t.Run("aged out", func(t *testing.T) {
		row := &store.WhatsAppOutbound{ParkedAt: nowMs - parkTTL.Milliseconds() - 1, Payload: `{"text":"x"}`}
		if got := parkRetireReason(row, nowMs); got != parkRetireStale {
			t.Errorf("retire = %q, want %q", got, parkRetireStale)
		}
	})

	t.Run("question that died while it waited", func(t *testing.T) {
		row := &store.WhatsAppOutbound{CreatedAt: nowMs, ParkedAt: nowMs, Payload: `{"text":"pick","prompt":{"kind":"hil","expiresAt":1}}`}
		if got := parkRetireReason(row, nowMs); got != parkRetireQuestionExpired {
			t.Errorf("retire = %q, want %q — a dead question must not arrive looking live", got, parkRetireQuestionExpired)
		}
	})

	t.Run("unreadable payload", func(t *testing.T) {
		row := &store.WhatsAppOutbound{CreatedAt: nowMs, ParkedAt: nowMs, Payload: `not json`}
		if got := parkRetireReason(row, nowMs); got != parkRetireUnreadable {
			t.Errorf("retire = %q, want %q", got, parkRetireUnreadable)
		}
	})
}

func TestWasParked(t *testing.T) {
	t.Parallel()
	if wasParked(&store.WhatsAppOutbound{}) {
		t.Error("a row that never parked must not be prefixed")
	}
	if !wasParked(&store.WhatsAppOutbound{ParkedAt: 1}) {
		t.Error("a released park must be prefixed so the user knows what it answers")
	}
}

func TestStaleForDelivery(t *testing.T) {
	t.Parallel()
	nowMs := time.Now().UnixMilli()
	if staleForDelivery(&store.WhatsAppOutbound{CreatedAt: nowMs}, nowMs) {
		t.Error("a fresh reply is not stale")
	}
	if !staleForDelivery(&store.WhatsAppOutbound{CreatedAt: nowMs - parkTTL.Milliseconds() - 1}, nowMs) {
		t.Error("a reply older than the park window must not be delivered verbatim")
	}
}

func TestIsWindowClosed(t *testing.T) {
	t.Parallel()
	if !isWindowClosed(errWindowClosed) {
		t.Error("this side's pre-check must read as a closed window")
	}
	meta := &graphAPIError{HTTPStatus: 400, GraphErr: graphError{Code: codeWindowClosed}}
	if !isWindowClosed(meta) {
		t.Error("Meta's 131047 is the authoritative closed-window signal")
	}
	other := &graphAPIError{HTTPStatus: 400, GraphErr: graphError{Code: 131026}}
	if isWindowClosed(other) {
		t.Error("an ordinary undeliverable must stay a failure, not become a park")
	}
}

func TestAdapter_ReparkedReplyStillAgesOutAndIsPrefixedOnce(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230200"
	rig.heardFrom(t, phone, 25*time.Hour)

	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask("the late answer"), nil)
	parked := rig.waitParked(t, phone)
	firstParkedAt := parked.ParkedAt

	rig.mock.refuseNext(1)
	rig.heardFrom(t, phone, time.Minute)
	rig.adapter.releaseParkedReply(t.Context(), phone, nowMillis())

	deadline := time.Now().Add(10 * time.Second)
	var again *store.WhatsAppOutbound
	for {
		again, _ = rig.adapter.outbound.GetParked(context.Background(), rig.adapter.gatewayID(), phone)
		if again != nil && again.State == store.WhatsAppOutboundParked && again.ParkedAt > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the reply to park again")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if again.ParkedAt != firstParkedAt {
		t.Errorf("parkedAt = %d after a re-park, want the original %d: a clock that resets is a park that never ages out",
			again.ParkedAt, firstParkedAt)
	}
	if strings.Count(again.Payload, msgParkedReplyPrefix) != 0 {
		t.Errorf("stored payload = %q, want the agent's text untouched; the prefix belongs at send time", again.Payload)
	}

	rig.adapter.releaseParkedReply(t.Context(), phone, nowMillis())
	delivered := rig.mock.waitDelivered(t, 1)
	if n := strings.Count(delivered[0].Text.Body, msgParkedReplyPrefix); n != 1 {
		t.Errorf("delivered %q carries %d prefixes, want exactly one", delivered[0].Text.Body, n)
	}
}

func TestAdapter_PeriodicTickerRetiresAParkNobodyCameBackFor(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230201"
	rig.heardFrom(t, phone, 25*time.Hour)

	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask("nobody came back"), nil)
	parked := rig.waitParked(t, phone)

	if _, err := ledgerDB(rig.adapter).ExecContext(context.Background(),
		`UPDATE whatsapp_outbound SET parked_at = ? WHERE id = ?`,
		time.Now().Add(-parkTTL-time.Hour).UnixMilli(), parked.ID); err != nil {
		t.Fatalf("age the park: %v", err)
	}
	rig.adapter.promptSweep = 10 * time.Millisecond
	rig.adapter.ingressActive.Store(true)
	rig.adapter.ingressOwned = true
	if err := rig.adapter.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rig.adapter.Stop(context.Background()) })

	deadline := time.Now().Add(10 * time.Second)
	for rig.rowState(t, parked.ID) == store.WhatsAppOutboundParked {
		if time.Now().After(deadline) {
			t.Fatal("the periodic sweep never retired the stale park")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := rig.rowState(t, parked.ID); got != store.WhatsAppOutboundSuperseded {
		t.Errorf("swept park = %q, want superseded — nothing else was ever going to retire it", got)
	}
	if rig.metrics.total(metricWhatsAppParkedSuperseded) != 1 {
		t.Errorf("superseded counter = %d, want 1", rig.metrics.total(metricWhatsAppParkedSuperseded))
	}
}

func TestAdapter_StaleQueuedReplyIsRetiredNotSent(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230202"
	rig.heardFrom(t, phone, time.Minute)

	aged := time.Now().Add(-parkTTL - time.Hour).UnixMilli()
	stale := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: rig.adapter.gatewayID(), Recipient: phone,
		TaskID: taskT1, Kind: kindText, Payload: `{"text":"yesterday's answer"}`,
		State: store.WhatsAppOutboundQueued, CreatedAt: aged, UpdatedAt: aged,
	}
	fresh := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: rig.adapter.gatewayID(), Recipient: phone,
		TaskID: taskT2, Kind: kindText, Payload: `{"text":"today's answer"}`,
		State:     store.WhatsAppOutboundQueued,
		CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli(),
	}
	if stale.ID >= fresh.ID {
		t.Fatalf("precondition: the stale row's id (%s) must sort before the fresh one (%s)", stale.ID, fresh.ID)
	}
	for _, row := range []*store.WhatsAppOutbound{stale, fresh} {
		if err := rig.adapter.sender.Enqueue(t.Context(), nil, row); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	waitQueueDrained(t, rig.adapter)

	if got := rig.rowState(t, stale.ID); got != store.WhatsAppOutboundSuperseded {
		t.Errorf("stale queued reply = %q, want superseded", got)
	}
	if got := rig.rowState(t, fresh.ID); got != store.WhatsAppOutboundSent {
		t.Errorf("the fresh reply = %q, want it delivered", got)
	}
	for _, sent := range rig.mock.delivered() {
		if strings.Contains(sent.Text.Body, "yesterday") {
			t.Fatal("a reply older than the park window was delivered verbatim, ahead of anything the user just asked")
		}
	}
}

func TestAdapter_ParkedLongReplyKeepsTheFullAnswerNotThePreview(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230300"
	rig.heardFrom(t, phone, 25*time.Hour)

	long := strings.Repeat("answer ", waTextBodyMax*4/7)
	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask(long), nil)

	waitQueueDrained(t, rig.adapter)
	parked := rig.waitParked(t, phone)
	var p outboundPayload
	if err := json.Unmarshal([]byte(parked.Payload), &p); err != nil {
		t.Fatalf("decode the parked payload: %v", err)
	}
	if p.Media == nil || p.Media.InlineText == "" {
		t.Fatalf("parked row = %s, want the response.txt carrying the whole answer", parked.Payload)
	}
	if !strings.Contains(p.Media.InlineText, "answer") {
		t.Errorf("parked inline text does not carry the reply: %q", truncate(p.Media.InlineText, 80))
	}
}

func TestYieldsToOwnTask(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		row  store.WhatsAppOutbound
		want string
	}{
		{name: "text never yields", row: store.WhatsAppOutbound{Kind: kindText, TaskID: taskT1}},
		{
			name: "an artifact-backed attachment yields to its own task",
			row:  store.WhatsAppOutbound{Kind: kindMedia, ArtifactRef: "artifact://x", TaskID: taskT1},
			want: taskT1,
		},
		{
			name: "the response.txt of a long reply IS the answer, so it never yields",
			row:  store.WhatsAppOutbound{Kind: kindMedia, TaskID: taskT1},
		},
		{name: "a prompt never yields", row: store.WhatsAppOutbound{Kind: kindInteractivePrompt, TaskID: taskT1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := yieldsToOwnTask(&tt.row); got != tt.want {
				t.Errorf("yieldsToOwnTask = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdapter_StaleReplyIsRetiredEvenWhenTheWindowIsShut(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230301"
	rig.heardFrom(t, phone, 25*time.Hour)

	aged := time.Now().Add(-parkTTL - time.Hour).UnixMilli()
	row := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: rig.adapter.gatewayID(), Recipient: phone,
		TaskID: taskT1, Kind: kindText, Payload: `{"text":"last week's answer"}`,
		State: store.WhatsAppOutboundQueued, CreatedAt: aged, UpdatedAt: aged,
	}
	if err := rig.adapter.sender.Enqueue(t.Context(), nil, row); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitQueueDrained(t, rig.adapter)

	if got := rig.rowState(t, row.ID); got != store.WhatsAppOutboundSuperseded {
		t.Errorf("stale reply for a shut window = %q, want superseded rather than parked for another day", got)
	}
}

func TestAdapter_RetiredParkedPromptAnswersTheAgent(t *testing.T) {
	const phone = "15551230400"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-parked-prompt-q")
	a := env.start(t)
	env.mock.refuseWindow(1)

	_ = a.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess-" + taskT1})
	a.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})
	parked := waitParkedOn(t, a, phone)

	if _, err := ledgerDB(a).ExecContext(context.Background(),
		`UPDATE whatsapp_outbound SET parked_at = ? WHERE id = ?`,
		time.Now().Add(-parkTTL-time.Hour).UnixMilli(), parked.ID); err != nil {
		t.Fatalf("age the park: %v", err)
	}
	a.sweepStaleParks()

	cancelled := receiveWithin(t, env.brk, q, 2*time.Second)
	if cancelled == nil {
		t.Fatal("a retired parked question never told its agent; it hangs to the task timeout")
	}
	if !strings.Contains(string(cancelled.Payload), "cancelled") ||
		!strings.Contains(string(cancelled.Payload), "req-1") {
		t.Errorf("agent response = %s, want req-1 cancelled", cancelled.Payload)
	}
}

func TestAdapter_DisplacedParkedPromptAnswersTheAgent(t *testing.T) {
	const phone = "15551230401"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-displaced-prompt-q")
	a := env.start(t)
	env.mock.refuseWindow(2)

	_ = a.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess-" + taskT1})
	a.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})
	waitParkedOn(t, a, phone)

	registerReplyTask(a, taskT2, phone)
	a.OnFinalResponse(t.Context(), taskT2, completedTask("a newer answer"), nil)

	cancelled := receiveWithin(t, env.brk, q, 3*time.Second)
	if cancelled == nil {
		t.Fatal("the displaced parked question never told its agent")
	}
	if !strings.Contains(string(cancelled.Payload), "req-1") {
		t.Errorf("agent response = %s, want the displaced req-1", cancelled.Payload)
	}
}

func waitParkedOn(t *testing.T, a *Adapter, sender string) *store.WhatsAppOutbound {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		row, err := a.outbound.GetParked(context.Background(), a.gatewayID(), sender)
		if err != nil {
			t.Fatalf("GetParked: %v", err)
		}
		if row != nil {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for a reply to be parked")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestAdapter_ArtifactAttachmentYieldsThroughTheSender(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230402"
	rig.heardFrom(t, phone, 25*time.Hour)

	prose := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: rig.adapter.gatewayID(), Recipient: phone,
		TaskID: taskT1, Kind: kindText, Payload: `{"text":"here is the report"}`,
		State:     store.WhatsAppOutboundQueued,
		CreatedAt: nowMillis(), UpdatedAt: nowMillis(),
	}
	attachment := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: rig.adapter.gatewayID(), Recipient: phone,
		TaskID: taskT1, Kind: kindMedia, ArtifactRef: "artifact://report.pdf",
		Payload:   `{"media":{"filename":"report.pdf","mimeType":"application/pdf"}}`,
		State:     store.WhatsAppOutboundQueued,
		CreatedAt: nowMillis(), UpdatedAt: nowMillis(),
	}
	for _, row := range []*store.WhatsAppOutbound{prose, attachment} {
		if err := rig.adapter.sender.Enqueue(t.Context(), nil, row); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	waitQueueDrained(t, rig.adapter)

	if got := rig.rowState(t, prose.ID); got != store.WhatsAppOutboundParked {
		t.Errorf("the prose = %q, want it to keep the park", got)
	}
	if got := rig.rowState(t, attachment.ID); got != store.WhatsAppOutboundSuperseded {
		t.Errorf("the artifact attachment = %q, want it to have stood aside", got)
	}
	if rig.metrics.total(metricWhatsAppParkedSuperseded) == 0 {
		t.Error("the yield was not counted")
	}
}

func TestAdapter_SweepWaitsForIngressOwnership(t *testing.T) {
	t.Parallel()
	rig := newWindowRig(t)
	const phone = "15551230202"
	rig.heardFrom(t, phone, 25*time.Hour)

	registerReplyTask(rig.adapter, taskT1, phone)
	rig.adapter.OnFinalResponse(t.Context(), taskT1, completedTask("nobody came back"), nil)
	parked := rig.waitParked(t, phone)
	if _, err := ledgerDB(rig.adapter).ExecContext(t.Context(),
		`UPDATE whatsapp_outbound SET parked_at = ? WHERE id = ?`,
		time.Now().Add(-parkTTL-time.Hour).UnixMilli(), parked.ID); err != nil {
		t.Fatalf("age the park: %v", err)
	}
	rig.adapter.promptSweep = time.Hour
	if err := rig.adapter.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rig.adapter.Stop(context.WithoutCancel(t.Context())) })

	rig.adapter.sweepIfOwner()
	if got := rig.rowState(t, parked.ID); got != store.WhatsAppOutboundParked {
		t.Fatalf("row state = %v, want parked while this instance does not own ingress", got)
	}

	rig.adapter.ownerMu.Lock()
	rig.adapter.ingressOwned = true
	rig.adapter.ownerMu.Unlock()
	rig.adapter.sweepIfOwner()
	if got := rig.rowState(t, parked.ID); got != store.WhatsAppOutboundSuperseded {
		t.Fatalf("row state = %v, want superseded once this instance owns ingress", got)
	}
}
