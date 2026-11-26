// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	rolloverAgent  = "Builder"
	rolloverSender = "15551230000"
)

const testSessionEpoch = 1787000000000

type rolloverRig struct {
	adapter *Adapter
	mock    *waMock

	nowMs atomic.Int64
	seen  map[string]bool
}

func newRolloverRig(t *testing.T) *rolloverRig {
	t.Helper()
	mock := newWAMock(t)
	dbURL := testDBURL(t)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace": "test-ns", "gateway_id": "test-gw",
		"session_service": map[string]any{
			"type": "sql", "database_url": dbURL, "default_behavior": "PERSISTENT",
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithArtifacts(&artifact.ScopedService{Inner: artmem.New(), Scope: "namespace", Namespace: "test-ns"})).Services())
	initCoreForTest(t, core)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: rolloverAgent})

	cfg := &WhatsAppConfig{
		AccessToken: "tok-test", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: mock.server.URL, DefaultAgentName: rolloverAgent,
		DisableRateLimiting: true,
	}
	log := discardLogger()
	r := &rolloverRig{mock: mock, seen: map[string]bool{}}
	a := &Adapter{
		core: core, cfg: cfg, logger: log,
		sessKeys: store.NewGatewaySessionKeyRepo(core.Store()),
		claims:   newMemoryClaims(), claimLease: defaultClaimLease,
		retryDelay: 0, hilDedup: runtime.NewMapCache(),
		client: NewClient(cfg, log), ctx: t.Context(),
		now: func() time.Time { return time.UnixMilli(r.nowMs.Load()) },
	}
	if err := a.initIngress(); err != nil {
		t.Fatalf("initIngress: %v", err)
	}
	startTestSender(t, a)
	mock.bindAdapter(a)
	r.adapter = a
	return r
}

func (r *rolloverRig) at(ts time.Time) { r.nowMs.Store(ts.UnixMilli()) }

func (r *rolloverRig) sendImage(t *testing.T, wamid string) string {
	t.Helper()
	msg := &inboundMessage{From: rolloverSender, ID: wamid, Type: "image",
		Image: &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg"}}
	value := &webhookChangeValue{
		Contacts: []webhookContact{{WaID: rolloverSender}},
		Messages: []inboundMessage{*msg},
	}
	return r.sessionOf(t, wamid, value, msg)
}

func (r *rolloverRig) send(t *testing.T, wamid, body string) string {
	t.Helper()
	value, msg := textMessage(wamid, rolloverSender, body)
	return r.sessionOf(t, wamid, value, msg)
}

func (r *rolloverRig) sessionOf(t *testing.T, wamid string, value *webhookChangeValue, msg *inboundMessage) string {
	t.Helper()
	if got := r.adapter.deliver(value, msg); got != settleAck {
		t.Fatalf("deliver(%s) = %v, want settleAck", wamid, got)
	}
	for _, taskID := range r.adapter.core.Tasks().AllByAgent(rolloverAgent) {
		if r.seen[taskID] {
			continue
		}
		r.seen[taskID] = true
		meta := r.adapter.getTaskMeta(taskID)
		if meta == nil {
			t.Fatalf("task %s carries no whatsapp metadata, so its session is unreadable", taskID)
		}
		return meta.SessionID
	}
	t.Fatalf("deliver(%s) submitted no new task", wamid)
	return ""
}

func TestAdapter_SessionRollsAfterADayOfSilence(t *testing.T) {
	r := newRolloverRig(t)
	start := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	r.at(start)
	first := r.send(t, "wamid.day1.a", "what were we saying about invoices?")

	r.at(start.Add(23 * time.Hour))
	same := r.send(t, "wamid.day1.b", "and the second one?")
	if same != first {
		t.Errorf("session after 23h = %s, want the original %s: a live conversation must not be cut in half", same, first)
	}

	r.at(start.Add(23*time.Hour + 25*time.Hour))
	rolled := r.send(t, "wamid.day2", "hello again")
	if rolled == first {
		t.Errorf("session after 25h of silence = %s, unchanged: a day-old thread must not be resumed as if it were live", rolled)
	}
}

func TestAdapter_SessionSurvivesUTCMidnight(t *testing.T) {
	r := newRolloverRig(t)
	before := time.Date(2026, 3, 14, 23, 58, 0, 0, time.UTC)
	after := before.Add(4 * time.Minute)
	if before.Format("20060102") == after.Format("20060102") {
		t.Fatalf("%s and %s must straddle UTC midnight or this test proves nothing", before, after)
	}

	r.at(before)
	asked := r.send(t, "wamid.2358", "book me the 9am slot")

	r.at(after)
	followUp := r.send(t, "wamid.0002", "actually make it 10")

	if followUp != asked {
		t.Errorf("session at 00:02 = %s, want the 23:58 session %s: four minutes of conversation crossed a date boundary, not a day of silence",
			followUp, asked)
	}
}

func TestAdapter_MediaSharesTheSessionOfTheTextAroundIt(t *testing.T) {
	r := newRolloverRig(t)
	r.mock.serveMedia("image/jpeg", []byte("JPEG-BYTES"))
	start := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	r.at(start)
	asked := r.send(t, "wamid.media.text", "here's the receipt")

	r.at(start.Add(time.Minute))
	shown := r.sendImage(t, "wamid.media.img")

	if shown != asked {
		t.Errorf("session of an image a minute after the text = %s, want the text's %s: a photo and the question about it are one exchange",
			shown, asked)
	}
}

func TestAdapter_SessionKeyUsesTheEpochRecordedForThisMessage(t *testing.T) {
	r := newRolloverRig(t)
	start := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	r.at(start)
	value, msg := textMessage("wamid.pin.a", rolloverSender, "first")
	epoch := r.adapter.recordInbound(r.adapter.ctx, value, msg)
	if epoch != start.UnixMilli() {
		t.Fatalf("first-contact epoch = %d, want %d", epoch, start.UnixMilli())
	}

	returned := time.Date(2026, 3, 20, 9, 0, 0, 0, time.UTC)
	r.at(returned)
	value, msg = textMessage("wamid.pin.b", rolloverSender, "second")
	rolled := r.adapter.recordInbound(r.adapter.ctx, value, msg)

	conv, err := r.adapter.conversations.Get(r.adapter.ctx, r.adapter.gatewayID(), rolloverSender)
	if err != nil || conv == nil {
		t.Fatalf("Get conversation = %v, err %v", conv, err)
	}
	if conv.LastInboundAt != returned.UnixMilli() {
		t.Fatalf("lastInboundAt = %d, want %d — the premise of this test is that the clock has already advanced",
			conv.LastInboundAt, returned.UnixMilli())
	}
	if rolled != returned.UnixMilli() {
		t.Errorf("epoch after six days idle = %d, want %d", rolled, returned.UnixMilli())
	}
	if got := buildExternalKey(rolloverSender, rolled); got == buildExternalKey(rolloverSender, epoch) {
		t.Errorf("external key %q is unchanged across a six-day gap", got)
	}
}

func TestAdapter_SessionKeyDegradesToOneSessionWhenTheStoreIsDown(t *testing.T) {
	r := newRolloverRig(t)
	r.at(time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC))

	r.adapter.conversations = store.NewWhatsAppConversationRepo(closedStore(t))

	first := r.send(t, "wamid.down.a", "hello")
	r.at(time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC))
	second := r.send(t, "wamid.down.b", "still there?")
	if first != second {
		t.Errorf("sessions with the store down = %s and %s, want one stable session per sender", first, second)
	}
}

func closedStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open("sqlite://"+t.TempDir()+"/gone.db", discardLogger())
	if err != nil {
		t.Fatalf("open scratch store: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close scratch store: %v", err)
	}
	return db
}

func TestAdapter_SessionlessModeMintsAFreshSessionPerMessage(t *testing.T) {
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, rolloverAgent)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: rolloverAgent})

	var ids []string
	for _, wamid := range []string{"wamid.nosess.a", "wamid.nosess.b"} {
		value, msg := textMessage(wamid, rolloverSender, "hello")
		if got := a.deliver(value, msg); got != settleAck {
			t.Fatalf("deliver(%s) = %v, want settleAck", wamid, got)
		}
	}
	for _, taskID := range a.core.Tasks().AllByAgent(rolloverAgent) {
		meta := a.getTaskMeta(taskID)
		if meta == nil || meta.SessionID == "" {
			t.Fatalf("task %s has no session id in sessionless mode", taskID)
		}
		ids = append(ids, meta.SessionID)
	}
	if len(ids) != 2 {
		t.Fatalf("submitted %d tasks, want 2", len(ids))
	}
	if ids[0] == ids[1] {
		t.Errorf("sessionless mode reused session %s; it has no mapping to reuse", ids[0])
	}
}
