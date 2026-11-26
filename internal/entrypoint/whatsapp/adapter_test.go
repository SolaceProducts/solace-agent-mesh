// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type waMock struct {
	server *httptest.Server
	t      *testing.T
	mu     sync.Mutex
	sends  []outboundTextMessage
	raw    [][]byte

	adapter *Adapter

	attempted         [][]byte
	rejectInteractive bool
	rejectTemplate    bool
	media             *mediaFixture

	rejectAll bool

	mediaReqs int

	typingGate chan struct{}
}

func (m *waMock) mediaRequests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mediaReqs
}

type mediaFixture struct {
	mime  string
	bytes []byte
}

const mediaDownloadPath = "/__inbound_media"

func (m *waMock) serveMedia(mime string, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.media = &mediaFixture{mime: mime, bytes: body}
}

func newWAMock(t *testing.T) *waMock {
	t.Helper()
	m := &waMock{t: t}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		fixture := m.media
		m.mu.Unlock()
		if r.Method == http.MethodGet && fixture != nil {
			if r.URL.Path == mediaDownloadPath {
				_, _ = w.Write(fixture.bytes)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"url":"http://%s%s","mime_type":%q,"file_size":%d,"id":"MEDIA1"}`,
				r.Host, mediaDownloadPath, fixture.mime, len(fixture.bytes))
			return
		}
		var body outboundTextMessage
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		m.mu.Lock()
		reject := m.rejectInteractive && body.Type == "interactive"
		rejectTmpl := m.rejectTemplate && body.Type == "template"
		rejectAll := m.rejectAll
		typingGate := m.typingGate
		m.attempted = append(m.attempted, b)
		if !reject && !rejectTmpl && !rejectAll {
			m.sends = append(m.sends, body)
			m.raw = append(m.raw, b)
		}
		m.mu.Unlock()
		if typingGate != nil && isReadStatus(b) {
			<-typingGate
		}
		w.Header().Set("Content-Type", "application/json")
		if rejectAll {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":131026,"message":"Message undeliverable","error_data":{"details":"to: 15551234567"}}}`))
			return
		}
		if reject {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":100,"message":"unsupported interactive type"}}`))
			return
		}
		if rejectTmpl {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":132001,"message":"Template name does not exist in the translation"}}`))
			return
		}
		_, _ = w.Write([]byte(mockSendResponse()))
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *waMock) countMediaRequest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mediaReqs++
}

func (m *waMock) attempts() [][]byte {
	m.drain()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.attempted))
	copy(out, m.attempted)
	return out
}

func (m *waMock) rawSends() [][]byte {
	m.drain()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.raw))
	copy(out, m.raw)
	return out
}

func (m *waMock) calls() []outboundTextMessage {
	m.drain()
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sends)
}

func (m *waMock) drain() {
	if m.adapter == nil {
		return
	}
	m.t.Helper()
	waitQueueDrained(m.t, m.adapter)
}

func (m *waMock) waitDelivered(n int, why string) [][]byte {
	m.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.mu.Lock()
		got := len(m.raw)
		m.mu.Unlock()
		if got >= n {
			return m.rawSends()
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("%s: delivered sends = %d, want at least %d", why, got, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func seedPendingPrompt(t *testing.T, a *Adapter, senderID string, row store.WhatsAppPendingPrompt) {
	t.Helper()
	if row.SurfaceJSON == "" {
		row.SurfaceJSON = askSurfaceSingleChoiceWA
	}
	if row.ExpiresAt == 0 {
		row.ExpiresAt = nowMillis() + defaultHILTTL.Milliseconds()
	}
	row.GatewayID, row.SenderID = a.gatewayID(), senderID
	row.CreatedAt, row.UpdatedAt = nowMillis(), nowMillis()
	if _, err := a.prompts.Upsert(context.Background(), nil, &row); err != nil {
		t.Fatalf("seed pending prompt: %v", err)
	}
}

func pendingPromptRow(t *testing.T, a *Adapter, senderID string) *store.WhatsAppPendingPrompt {
	t.Helper()
	row, err := a.prompts.Get(context.Background(), a.gatewayID(), senderID)
	if err != nil {
		t.Fatalf("read pending prompt: %v", err)
	}
	return row
}

func waitPendingPrompt(t *testing.T, a *Adapter, senderID string) *store.WhatsAppPendingPrompt {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if row := pendingPromptRow(t, a, senderID); row != nil {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pending prompt for %s was never delivered", senderID)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitPromptDelivered(t *testing.T, a *Adapter, recipient string) *pendingHIL {
	t.Helper()
	p, err := decodePendingPrompt(waitPendingPrompt(t, a, recipient))
	if err != nil {
		t.Fatalf("decode pending prompt: %v", err)
	}
	return p
}

func waitQueueDrained(t *testing.T, a *Adapter) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err := a.outbound.PendingCount(context.Background(), a.gatewayID())
		if err != nil {
			t.Fatalf("PendingCount: %v", err)
		}
		if pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out with %d whatsapp sends still pending", pending)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitMockSent(t *testing.T, m *rawMock, want, why string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		bodies := m.recorded()
		for _, b := range bodies {
			if strings.Contains(string(b), want) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: no send containing %q arrived (%d sends)", why, want, len(bodies))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

var mockWamidSeq atomic.Int64

func mockSendResponse() string {
	return fmt.Sprintf(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.OUT-%d"}]}`, mockWamidSeq.Add(1))
}

func rigLogger(override []*slog.Logger) *slog.Logger {
	if len(override) > 0 && override[0] != nil {
		return override[0]
	}
	return slog.New(newTapHandler())
}

type logTap struct {
	mu  sync.Mutex
	buf bytes.Buffer
	on  bool
}

func (l *logTap) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.on {
		l.buf.Write(p)
	}
	return len(p), nil
}

func (l *logTap) arm() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Reset()
	l.on = true
}

func (l *logTap) disarm() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on = false
	return l.buf.String()
}

type tapHandler struct {
	slog.Handler
	tap *logTap
}

func newTapHandler() *tapHandler {
	tap := &logTap{}
	return &tapHandler{
		Handler: slog.NewJSONHandler(tap, &slog.HandlerOptions{Level: slog.LevelDebug}),
		tap:     tap,
	}
}

func (h *tapHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &tapHandler{Handler: h.Handler.WithAttrs(attrs), tap: h.tap}
}

func (h *tapHandler) WithGroup(name string) slog.Handler {
	return &tapHandler{Handler: h.Handler.WithGroup(name), tap: h.tap}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var whatsappDBTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	return dbtest.NewTemplateFromURL("", "whatsapp", func(u string) error {
		db, err := store.Open(u, discardLogger())
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		return db.Migrate(context.Background())
	})
})

func testDBURL(t *testing.T) string {
	t.Helper()
	tmpl, err := whatsappDBTemplate()
	if err != nil {
		t.Fatalf("init whatsapp db template: %v", err)
	}
	return tmpl.Clone(t)
}

func initCoreForTest(t *testing.T, core *entrypoint.GatewayCore) {
	t.Helper()
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	t.Cleanup(func() {
		if db := core.Store(); db != nil {
			_ = db.Close()
		}
	})
}

func testAppConfig(t *testing.T, gatewayID string) map[string]any {
	t.Helper()
	return map[string]any{
		"namespace":  "test-ns",
		"gateway_id": gatewayID,
		"session_service": map[string]any{
			"type": "sql", "database_url": testDBURL(t),
		},
	}
}

func newTestCore(t *testing.T) *entrypoint.GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return newTestCoreOn(t, brk)
}

func newTestCoreOn(t *testing.T, brk broker.Broker) *entrypoint.GatewayCore {
	t.Helper()
	rt := runtime.NewRuntime(brk, nil)
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, testAppConfig(t, "test-gw"))
	core.SetServices(rt.Services())
	initCoreForTest(t, core)
	return core
}

func newStorelessCore(t *testing.T) *entrypoint.GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	initCoreForTest(t, core)
	return core
}

func newArtifactCore(t *testing.T) *entrypoint.GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, testAppConfig(t, "test-gw"))
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithArtifacts(&artifact.ScopedService{Inner: artmem.New()})).Services())
	initCoreForTest(t, core)
	return core
}

func newAdapterRig(t *testing.T, mock *waMock, defaultAgent string, logger ...*slog.Logger) *Adapter {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return newAdapterRigOn(t, brk, mock, defaultAgent, logger...)
}

func newAdapterRigOn(t *testing.T, brk broker.Broker, mock *waMock, defaultAgent string, logger ...*slog.Logger) *Adapter {
	t.Helper()
	log := rigLogger(logger)
	cfg := &WhatsAppConfig{
		AccessToken:         "tok-test",
		PhoneNumberID:       "555111",
		GraphAPIVersion:     "v26.0",
		GraphAPIBaseURL:     mock.server.URL,
		DefaultAgentName:    defaultAgent,
		DisableRateLimiting: true,
	}
	a := &Adapter{
		core:   newTestCoreOn(t, brk),
		cfg:    cfg,
		logger: log,
		claims: newMemoryClaims(), claimLease: defaultClaimLease,
		retryDelay: 0,
		hilDedup:   runtime.NewMapCache(),
		client:     NewClient(cfg, log),
		ctx:        t.Context(),
	}
	if err := a.initIngress(); err != nil {
		t.Fatalf("initIngress: %v", err)
	}
	startTestSender(t, a)
	mock.bindAdapter(a)
	return a
}

func (m *waMock) bindAdapter(a *Adapter) { m.adapter = a }

func startTestSender(t *testing.T, a *Adapter) {
	t.Helper()
	if err := a.initDurableState(); err != nil {
		t.Fatalf("initDurableState: %v", err)
	}
	a.sender.idleTimeout = 50 * time.Millisecond
	a.sender.Start(t.Context())
	t.Cleanup(func() {
		if a.stopped.Load() {
			return
		}
		if !a.stopSender(context.Background()) {
			t.Error("sender pool did not drain; the cleanup below closes the database it is still writing to")
		}
	})
}

func (a *Adapter) deliver(value *webhookChangeValue, msg *inboundMessage) ingressSettlement {
	return a.deliverInbound(a.ctx, &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)})
}

func (a *Adapter) claimState(wamid string) string {
	return a.claims.(*memoryClaims).state(a.core.GatewayID(), wamid)
}

func mustPublish(t *testing.T, a *Adapter, value *webhookChangeValue, msg *inboundMessage) {
	t.Helper()
	if err := a.publishInbound(t.Context(), value, msg); err != nil {
		t.Fatalf("publishInbound(%s): %v", msg.ID, err)
	}
}

func spooled(t *testing.T, a *Adapter) []ingressEnvelope {
	t.Helper()
	var out []ingressEnvelope
	brk := a.core.Services().Broker()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		msg, err := brk.Receive(ctx, a.ingressQueue)
		cancel()
		if err != nil || msg == nil {
			return out
		}
		var env ingressEnvelope
		if err := json.Unmarshal(msg.Payload, &env); err != nil {
			t.Fatalf("unmarshal spooled envelope: %v", err)
		}
		msg.Ack()
		out = append(out, env)
	}
}

func systemMessage(wamid, from string, n *systemNotification) (*webhookChangeValue, *inboundMessage) {
	msg := &inboundMessage{From: from, ID: wamid, Type: "system", System: n}
	return &webhookChangeValue{Messages: []inboundMessage{*msg}}, msg
}

func textMessage(wamid, from, body string) (*webhookChangeValue, *inboundMessage) {
	msg := &inboundMessage{From: from, ID: wamid, Type: "text"}
	msg.Text.Body = body
	value := &webhookChangeValue{
		Contacts: []webhookContact{{WaID: from}},
		Messages: []inboundMessage{*msg},
	}
	value.Contacts[0].Profile.Name = "Alice"
	return value, msg
}

func bsuidTextMessage(wamid, from, bsuid, body string) (*webhookChangeValue, *inboundMessage) {
	value, msg := textMessage(wamid, from, body)
	msg.FromUserID = bsuid
	value.Messages[0].FromUserID = bsuid
	value.Contacts[0].UserID = bsuid
	return value, msg
}

func TestAdapter_FactoryRegistered(t *testing.T) {
	t.Parallel()
	if !slices.Contains(entrypoint.RegisteredAdapters(), "whatsapp") {
		t.Fatalf(`adapter factory "whatsapp" not registered; have %v`, entrypoint.RegisteredAdapters())
	}
	a, err := NewAdapter(map[string]any{"phone_number_id": "1"})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if spec := a.MountIntent(); spec.Kind != entrypoint.MountNamed {
		t.Errorf("MountIntent kind = %v, want MountNamed", spec.Kind)
	}
	var got http.Handler
	a.SetOnHandlerReady(func(h http.Handler) { got = h })
	if a.onHandlerReady == nil {
		t.Fatal("SetOnHandlerReady did not store the callback")
	}
	a.onHandlerReady(nil)
	_ = got
}

func TestAdapter_Lifecycle(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	rawCfg := map[string]any{
		"phone_number_id":    "555111",
		"access_token":       "tok-test",
		"app_secret":         "secret",
		"verify_token":       "verify",
		"graph_api_base_url": mock.server.URL,
	}
	a, err := NewAdapter(rawCfg)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	var handlerReady http.Handler
	a.SetOnHandlerReady(func(h http.Handler) { handlerReady = h })

	if err = a.Init(t.Context(), newStorelessCore(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if a.cfg == nil || a.client == nil || a.server == nil {
		t.Fatal("Init did not build cfg/client/server")
	}
	if handlerReady == nil {
		t.Fatal("onHandlerReady was not fired with the webhook handler")
	}
	if a.fallbackDB == nil {
		t.Fatal("Init did not open the scratch outbound store a databaseless gateway falls back to")
	}
	t.Cleanup(func() { _ = os.RemoveAll(a.fallbackDir) })
	info, err := os.Stat(a.fallbackDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("os.Stat(fallbackDir=%q) = %v, %v; want a directory", a.fallbackDir, info, err)
	}
	if _, ok := a.claims.(*memoryClaims); !ok {
		t.Errorf("claims = %T, want *memoryClaims — a databaseless gateway has no dedup table to claim in", a.claims)
	}

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if !a.stopped.Load() {
			a.sender.Stop(context.Background(), 2*time.Second)
		}
		a.cancel()
	})

	row := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: a.gatewayID(), Recipient: "15551230000",
		Kind: kindText, Payload: `{"text":"over the scratch store"}`,
		State: store.WhatsAppOutboundQueued, CreatedAt: a.nowMs(), UpdatedAt: a.nowMs(),
	}
	if err := a.sender.Enqueue(t.Context(), nil, row); err != nil {
		t.Fatalf("enqueue over the scratch store: %v", err)
	}
	if sent := waitRowState(t, a.outbound, row.ID, store.WhatsAppOutboundSent); sent.Wamid == "" {
		t.Error("the delivered row carries no wamid; the scratch store recorded a send Meta never acknowledged")
	}

	if err := a.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(a.fallbackDir); !os.IsNotExist(err) {
		t.Errorf("os.Stat(fallbackDir=%q) after Stop = %v, want a not-exist error — the scratch store outlived the adapter", a.fallbackDir, err)
	}
}

func TestAdapter_Start_ResetsHealth(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	rawCfg := map[string]any{
		"phone_number_id":    "555111",
		"access_token":       "tok-test",
		"app_secret":         "secret",
		"verify_token":       "verify",
		"graph_api_base_url": mock.server.URL,
	}
	a, err := NewAdapter(rawCfg)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if err := a.Init(t.Context(), newTestCore(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	a.health.Set("ingress", health.Fatal, health.DetailIngressDown)
	if len(a.HealthProblems()) == 0 {
		t.Fatal("test setup: expected a seeded health problem before Start")
	}

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if problems := a.HealthProblems(); len(problems) != 0 {
		t.Errorf("HealthProblems() after Start = %v, want none — a prior run's entry survived the restart", problems)
	}

	if err := a.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestAdapter_Init_NoConfig(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	if err := a.Init(t.Context(), newTestCore(t)); err == nil {
		t.Fatal("expected error when no adapter_config is present")
	}
}

func TestAdapter_HandleInbound_SubmitsTask(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	value, msg := textMessage("wamid.1", "15551230000", "  hello there  ")
	a.deliver(value, msg)

	ids := a.core.Tasks().AllByAgent("Builder")
	if len(ids) != 1 {
		t.Fatalf("expected 1 task submitted to Builder, got %d", len(ids))
	}
	info := a.core.Tasks().Get(ids[0])
	if info == nil {
		t.Fatal("task info missing")
	}
	meta, ok := info.Metadata.(*taskMeta)
	if !ok || meta.FromPhone != "15551230000" {
		t.Fatalf("task metadata = %+v, want FromPhone=15551230000", info.Metadata)
	}
}

func TestAdapter_HandleInbound_Dedup(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	value, msg := textMessage("wamid.dup", "15551230000", "hi")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("first delivery settled %v, want settleAck", got)
	}
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("redelivery of a completed wamid settled %v, want settleAck", got)
	}

	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 1 {
		t.Fatalf("expected dedup to yield 1 task, got %d", len(ids))
	}
	if got := a.claimState(msg.ID); got != store.WhatsAppMessageDone {
		t.Errorf("claim state after a submitted task = %q, want %q", got, store.WhatsAppMessageDone)
	}

	brk := a.core.Services().Broker()
	if err := brk.Disconnect(t.Context()); err != nil {
		t.Fatalf("disconnect broker: %v", err)
	}
	retryValue, retryMsg := textMessage("wamid.retry", "15551230000", "hi again")
	if got := a.deliver(retryValue, retryMsg); got != settleNack {
		t.Fatalf("failed submit settled %v, want settleNack", got)
	}
	if got := a.claimState(retryMsg.ID); got != store.WhatsAppMessageRetrying {
		t.Fatalf("claim state after a failed submit = %q, want %q so the redelivery reclaims it",
			got, store.WhatsAppMessageRetrying)
	}

	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("reconnect broker: %v", err)
	}
	if got := a.deliver(retryValue, retryMsg); got != settleAck {
		t.Fatalf("redelivery after recovery settled %v, want settleAck", got)
	}
	if got := a.claimState(retryMsg.ID); got != store.WhatsAppMessageDone {
		t.Errorf("claim state after the successful retry = %q, want %q", got, store.WhatsAppMessageDone)
	}
	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 2 {
		t.Fatalf("expected the redelivery to dispatch (2 tasks total), got %d", len(ids))
	}
}

func TestAdapter_PublishInbound_PhoneNumberFilter(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")

	otherValue, otherMsg := textMessage("wamid.other", "15551230000", "hi")
	otherValue.Metadata.PhoneNumberID = "999999"
	if err := a.publishInbound(t.Context(), otherValue, otherMsg); err != nil {
		t.Fatalf("a foreign-number event must be dropped, not failed: %v", err)
	}

	mineValue, mineMsg := textMessage("wamid.mine", "15551230000", "hi")
	mineValue.Metadata.PhoneNumberID = "555111"
	if err := a.publishInbound(t.Context(), mineValue, mineMsg); err != nil {
		t.Fatalf("publishInbound: %v", err)
	}

	got := spooled(t, a)
	if len(got) != 1 {
		t.Fatalf("spooled %d messages, want only the one for this number", len(got))
	}
	if got[0].Message.ID != "wamid.mine" {
		t.Errorf("spooled wamid = %q, want wamid.mine", got[0].Message.ID)
	}
}

func TestAdapter_HandleStatus_DifferentNumberIgnored(t *testing.T) {
	t.Parallel()
	logs := &recordCollector{}
	a := &Adapter{
		logger: slog.New(logs),
		cfg:    &WhatsAppConfig{PhoneNumberID: "555111"},
	}
	value := &webhookChangeValue{}
	value.Metadata.PhoneNumberID = "999999"
	a.handleStatus(value, &messageStatus{ID: "wamid.s", Status: "failed", RecipientID: "15551234567"})

	if len(logs.all()) != 1 || logs.all()[0].Message != "ignoring status for a different phone number" {
		t.Fatalf("want one 'ignoring status' record, got %+v", logs.all())
	}
	if logs.all()[0].Level != slog.LevelWarn {
		t.Errorf("first foreign-number drop = %v, want Warn (a misconfigured phone_number_id must be visible)", logs.all()[0].Level)
	}
}

func TestAdapter_HandleInbound_SessionResolveFailureIsRetryable(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	db, err := store.Open("sqlite:///:memory:", discardLogger())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	_ = db.Close()
	a.sessKeys = store.NewGatewaySessionKeyRepo(db)

	value, msg := textMessage("wamid.sess", "15551230000", "hi")
	if got := a.deliver(value, msg); got != settleNack {
		t.Fatalf("session-resolve failure settled %v, want settleNack", got)
	}

	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
		t.Fatalf("session-resolve failure should submit no task, got %d", len(ids))
	}
	if got := a.claimState(msg.ID); got != store.WhatsAppMessageRetrying {
		t.Fatalf("claim state after a session-resolve failure = %q, want %q", got, store.WhatsAppMessageRetrying)
	}
}

func TestAdapter_HandleInbound_EmptyText(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	value, msg := textMessage("wamid.empty", "15551230000", "   ")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("empty body settled %v, want settleAck", got)
	}

	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
		t.Fatalf("blank message should not submit a task, got %d", len(ids))
	}
	if got := a.claimState(msg.ID); got != store.WhatsAppMessageDone {
		t.Errorf("claim state after an empty-body drop = %q, want %q", got, store.WhatsAppMessageDone)
	}
}

func TestAdapter_HandleInbound_NonText(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")

	msg := &inboundMessage{From: "15551230000", ID: "wamid.vid", Type: "video"}
	a.deliver(&webhookChangeValue{}, msg)

	calls := mock.calls()
	if len(calls) != 1 {
		t.Fatalf("expected one notice send for unsupported type, got %d", len(calls))
	}
	if calls[0].To != "15551230000" || calls[0].Text.Body == "" {
		t.Errorf("notice = %+v, want a non-empty body to the sender", calls[0])
	}
}

func TestAdapter_SubmitTask_Misconfigured(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "does-not-exist")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	value, msg := textMessage("wamid.mis", "15551230000", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("misconfigured default settled %v, want settleAck (no redelivery will conjure the agent)", got)
	}

	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
		t.Fatalf("misconfigured default must not silently route to Builder, got %d", len(ids))
	}
	calls := mock.calls()
	if len(calls) != 1 {
		t.Fatalf("expected one error notice, got %d", len(calls))
	}
	if got := a.claimState(msg.ID); got != store.WhatsAppMessageDone {
		t.Errorf("claim state after a misconfigured-route decline = %q, want %q", got, store.WhatsAppMessageDone)
	}
}

func TestAdapter_InboundFailureSettlement(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		defaultAgent string
		msg          func() (*webhookChangeValue, *inboundMessage)
		wantSettle   ingressSettlement
		wantClaim    string
		wantDeclines int
	}{
		{
			name:         "no agent to route to",
			defaultAgent: "",
			msg: func() (*webhookChangeValue, *inboundMessage) {
				return textMessage("wamid.noroute", "15551230000", "hello")
			},
			wantSettle:   settleNack,
			wantClaim:    store.WhatsAppMessageRetrying,
			wantDeclines: 0,
		},
		{
			name:         "unsupported message type",
			defaultAgent: "Builder",
			msg: func() (*webhookChangeValue, *inboundMessage) {
				msg := &inboundMessage{From: "15551230000", ID: "wamid.sticker", Type: "sticker"}
				return &webhookChangeValue{Contacts: []webhookContact{{WaID: "15551230000"}}, Messages: []inboundMessage{*msg}}, msg
			},
			wantSettle:   settleAck,
			wantClaim:    store.WhatsAppMessageDone,
			wantDeclines: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, tt.defaultAgent)

			value, msg := tt.msg()
			if got := a.deliver(value, msg); got != tt.wantSettle {
				t.Fatalf("settlement = %v, want %v", got, tt.wantSettle)
			}
			if got := a.claimState(msg.ID); got != tt.wantClaim {
				t.Fatalf("claim state = %q, want %q", got, tt.wantClaim)
			}
			if got := len(mock.calls()); got != tt.wantDeclines {
				t.Fatalf("declines sent = %d, want %d", got, tt.wantDeclines)
			}

			a.deliver(value, msg)
			wantAfterRedelivery := tt.wantDeclines
			if tt.wantSettle == settleNack {
				wantAfterRedelivery *= 2
			}
			if got := len(mock.calls()); got != wantAfterRedelivery {
				t.Errorf("declines after redelivery = %d, want %d", got, wantAfterRedelivery)
			}
		})
	}
}

func TestAdapter_InboundDropPathsAreVisible(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		wantMsg   string
		wantLevel slog.Level
		rig       func(t *testing.T, logger *slog.Logger) *Adapter
		drive     func(t *testing.T, a *Adapter)
	}{
		{
			name:      "message for a different business number",
			wantMsg:   "ignoring inbound message for a different phone number",
			wantLevel: slog.LevelWarn,
			drive: func(t *testing.T, a *Adapter) {
				value, msg := textMessage("wamid.foreign", "15551230000", "hi")
				value.Metadata.PhoneNumberID = "999999"
				_ = a.publishInbound(t.Context(), value, msg)
			},
		},
		{
			name:      "duplicate wamid",
			wantMsg:   "duplicate inbound message ignored",
			wantLevel: slog.LevelInfo,
			drive: func(t *testing.T, a *Adapter) {
				value, msg := textMessage("wamid.dup2", "15551230000", "hi")
				a.deliver(value, msg)
				a.deliver(value, msg)
			},
		},
		{
			name:      "empty text body",
			wantMsg:   "inbound text message with empty body ignored",
			wantLevel: slog.LevelWarn,
			drive: func(t *testing.T, a *Adapter) {
				value, msg := textMessage("wamid.blank", "15551230000", "   ")
				a.deliver(value, msg)
			},
		},
		{
			name:      "unsupported message type",
			wantMsg:   "unsupported inbound message type declined",
			wantLevel: slog.LevelInfo,
			drive: func(t *testing.T, a *Adapter) {
				a.deliver(&webhookChangeValue{}, &inboundMessage{From: "15551230000", ID: "wamid.stick", Type: "sticker"})
			},
		},
		{
			name:      "non-answer while a prompt is pending",
			wantMsg:   "inbound message cannot answer the pending interactive prompt",
			wantLevel: slog.LevelInfo,
			drive: func(t *testing.T, a *Adapter) {
				seedPendingPrompt(t, a, "15551230000", store.WhatsAppPendingPrompt{TaskID: taskT1, RequestID: "req-1"})
				a.deliver(&webhookChangeValue{}, &inboundMessage{From: "15551230000", ID: "wamid.img", Type: "image"})
			},
		},
		{
			name:      "non-content notification",
			wantMsg:   "non-content inbound message ignored",
			wantLevel: slog.LevelInfo,
			drive: func(t *testing.T, a *Adapter) {
				a.deliver(&webhookChangeValue{}, &inboundMessage{From: "15551230000", ID: "wamid.sys", Type: "system"})
			},
		},
		{
			name:      "interactive reply with no pending prompt",
			wantMsg:   "interactive reply with no pending prompt declined",
			wantLevel: slog.LevelInfo,
			drive: func(t *testing.T, a *Adapter) {
				a.deliver(&webhookChangeValue{}, &inboundMessage{
					From: "15551230000", ID: "wamid.stale",
					Type: "interactive", Interactive: &inboundInteractive{ButtonReply: &replyRef{ID: "opt:0"}}})
			},
		},
		{
			name:      "inbound media exceeds size cap",
			wantMsg:   "inbound media exceeds size cap",
			wantLevel: slog.LevelInfo,
			rig: func(t *testing.T, logger *slog.Logger) *Adapter {
				a, _, _ := mediaAdapter(t,
					`{"url":"${SRV}/dl/MEDIA1","mime_type":"image/jpeg","file_size":99000000,"id":"MEDIA1"}`,
					"/dl/MEDIA1", []byte("\xff\xd8\xffJPEG"), logger)
				return a
			},
			drive: func(t *testing.T, a *Adapter) {
				msg := &inboundMessage{From: "15551230000", ID: "wamid.toobig", Type: "image", Image: &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg"}}
				value := &webhookChangeValue{Contacts: []webhookContact{{WaID: "15551230000"}}, Messages: []inboundMessage{*msg}}
				a.deliver(value, msg)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logs := &recordCollector{}
			var a *Adapter
			if tt.rig != nil {
				a = tt.rig(t, slog.New(logs))
			} else {
				a = newAdapterRig(t, newWAMock(t), "Builder", slog.New(logs))
			}
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

			tt.drive(t, a)
			waitQueueDrained(t, a)

			for _, r := range logs.all() {
				if r.Message != tt.wantMsg {
					continue
				}
				if r.Level != tt.wantLevel {
					t.Errorf("%q logged at %v, want %v", tt.wantMsg, r.Level, tt.wantLevel)
				}
				return
			}
			t.Errorf("no %q record among %d records — a dismissed message must leave a visible trace", tt.wantMsg, len(logs.all()))
		})
	}
}

func TestAdapter_ForeignNumberWarnRateLimit(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	logs := &recordCollector{}
	a := newAdapterRig(t, mock, "Builder", slog.New(logs))
	now := time.Unix(1720000000, 0)
	a.foreignNumbers.now = func() time.Time { return now }

	value, msg := textMessage("wamid.f1", "15551230000", "hi")
	value.Metadata.PhoneNumberID = "999999"
	mustPublish(t, a, value, msg)

	statusValue := &webhookChangeValue{}
	statusValue.Metadata.PhoneNumberID = "999999"
	a.handleStatus(statusValue, &messageStatus{ID: "wamid.f2", Status: "delivered"})

	otherValue, otherMsg := textMessage("wamid.f3", "15551230000", "hi")
	otherValue.Metadata.PhoneNumberID = "888888"
	mustPublish(t, a, otherValue, otherMsg)

	now = now.Add(foreignNumberWarnWindow + time.Second)
	refire, refireMsg := textMessage("wamid.f4", "15551230000", "hi")
	refire.Metadata.PhoneNumberID = "999999"
	mustPublish(t, a, refire, refireMsg)

	var got []slog.Level
	var lastWarn slog.Record
	for _, r := range logs.all() {
		if strings.Contains(r.Message, "different phone number") {
			got = append(got, r.Level)
			if r.Level == slog.LevelWarn {
				lastWarn = r
			}
		}
	}
	want := []slog.Level{slog.LevelWarn, slog.LevelDebug, slog.LevelWarn, slog.LevelWarn}
	if !slices.Equal(got, want) {
		t.Fatalf("foreign-number drop levels = %v, want %v", got, want)
	}

	suppressed := -1
	lastWarn.Attrs(func(at slog.Attr) bool {
		if at.Key == "suppressedSinceLast" {
			suppressed = int(at.Value.Int64())
		}
		return true
	})
	if suppressed != 1 {
		t.Errorf("re-fired warn suppressedSinceLast = %d, want 1 (the Debug-suppressed status event)", suppressed)
	}
}

func TestAdapter_WamidlessMessagesAreNotDuplicates(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	logs := &recordCollector{}
	a := newAdapterRig(t, mock, "Builder", slog.New(logs))
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	first, firstMsg := textMessage("", "15551230000", "first")
	second, secondMsg := textMessage("", "15551230000", "second")
	a.deliver(first, firstMsg)
	a.deliver(second, secondMsg)

	if got := len(a.core.Tasks().AllByAgent("Builder")); got != 2 {
		t.Errorf("tasks submitted = %d, want 2 — an id-less message must not be mislabeled a duplicate", got)
	}
	sawGuard := false
	for _, r := range logs.all() {
		switch r.Message {
		case "duplicate inbound message ignored":
			t.Error("id-less message reported as a duplicate")
		case "inbound message missing wamid, dedup skipped":
			sawGuard = true
			if r.Level != slog.LevelWarn {
				t.Errorf("missing-wamid guard logged at %v, want Warn", r.Level)
			}
		}
	}
	if !sawGuard {
		t.Error("no missing-wamid record — an undeduplicable message must leave a visible trace")
	}
}

func TestAdapter_OnFinalResponse(t *testing.T) {
	t.Parallel()

	register := func(a *Adapter, taskID, phone string) {
		_ = a.core.Tasks().Register(taskID, "Builder", "user", entrypoint.TaskIdentity{})
		a.core.Tasks().SetMetadata(taskID, &taskMeta{FromPhone: phone, SessionID: "sess"})
	}
	taskWith := func(state a2a.TaskState, text string) *a2a.Task {
		task := &a2a.Task{Status: a2a.TaskStatus{State: state}}
		if text != "" {
			task.Status.Message = &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart(text)}}
		}
		return task
	}

	t.Run("delivers text and removes task", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		register(a, "t-ok", "15551230000")
		a.OnFinalResponse(t.Context(), "t-ok", taskWith(a2a.TaskStateCompleted, "the answer is 42"), nil)

		calls := mock.calls()
		if len(calls) != 1 || calls[0].Text.Body != "the answer is 42" {
			t.Fatalf("delivered = %+v, want body %q", calls, "the answer is 42")
		}
		if a.core.Tasks().Get("t-ok") != nil {
			t.Error("task should be removed after final response")
		}
	})

	for _, reply := range []string{"---", "---\n  \n---"} {
		t.Run("reply that renders to nothing notifies the user: "+reply, func(t *testing.T) {
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, "Builder")
			register(a, "t-rule", "15551230000")
			a.OnFinalResponse(t.Context(), "t-rule", taskWith(a2a.TaskStateCompleted, reply), nil)

			calls := mock.calls()
			if len(calls) != 1 || calls[0].Text.Body != msgNoDisplayableReply {
				t.Fatalf("delivered = %+v, want the no-displayable-reply notice", calls)
			}
		})
	}

	t.Run("cancelled", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		register(a, "t-cancel", "15551230000")
		a.OnFinalResponse(t.Context(), "t-cancel", taskWith(a2a.TaskStateCancelled, ""), nil)
		if calls := mock.calls(); len(calls) != 1 {
			t.Fatalf("expected one cancel notice, got %d", len(calls))
		}
	})

	t.Run("failed", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		register(a, "t-fail", "15551230000")
		a.OnFinalResponse(t.Context(), "t-fail", taskWith(a2a.TaskStateFailed, ""), nil)
		if calls := mock.calls(); len(calls) != 1 {
			t.Fatalf("expected one failure notice, got %d", len(calls))
		}
	})

	t.Run("artifact-only response notifies the user", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		register(a, "t-empty", "15551230000")
		a.OnFinalResponse(t.Context(), "t-empty", taskWith(a2a.TaskStateCompleted, ""), nil)
		if calls := mock.calls(); len(calls) != 1 {
			t.Fatalf("empty final text should send one notice, got %d", len(calls))
		}
	})

	t.Run("unknown task is a no-op", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		a.OnFinalResponse(t.Context(), "missing", taskWith(a2a.TaskStateCompleted, "hi"), nil)
		if calls := mock.calls(); len(calls) != 0 {
			t.Fatalf("unknown task should send nothing, got %d", len(calls))
		}
	})
}

func TestAdapter_OnError(t *testing.T) {
	t.Parallel()

	t.Run("generic error", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		_ = a.core.Tasks().Register("e1", "Builder", "user", entrypoint.TaskIdentity{})
		a.core.Tasks().SetMetadata("e1", &taskMeta{FromPhone: "15551230000"})
		a.OnError(t.Context(), "e1", &a2a.Error{Message: "boom"}, nil)
		if calls := mock.calls(); len(calls) != 1 {
			t.Fatalf("expected one error notice, got %d", len(calls))
		}
		if a.core.Tasks().Get("e1") != nil {
			t.Error("task should be removed after error")
		}
	})

	t.Run("timeout renders as cancellation", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		_ = a.core.Tasks().Register("e2", "Builder", "user", entrypoint.TaskIdentity{})
		a.core.Tasks().SetMetadata("e2", &taskMeta{FromPhone: "15551230000"})
		a.OnError(t.Context(), "e2", &a2a.Error{Code: entrypoint.TaskTimeoutErrorCode, Message: "task timed out"}, nil)
		calls := mock.calls()
		if len(calls) != 1 || !strings.Contains(calls[0].Text.Body, "canceled") {
			t.Fatalf("timeout should render the canceled notice, got %+v", calls)
		}
	})

	t.Run("real error containing 'cancel' is not masked", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		_ = a.core.Tasks().Register("e3", "Builder", "user", entrypoint.TaskIdentity{})
		a.core.Tasks().SetMetadata("e3", &taskMeta{FromPhone: "15551230000"})
		a.OnError(t.Context(), "e3", &a2a.Error{Message: "context canceled while uploading artifact"}, nil)
		calls := mock.calls()
		if len(calls) != 1 || strings.Contains(calls[0].Text.Body, "canceled") {
			t.Fatalf("error containing 'cancel' must not render as canceled, got %+v", calls)
		}
	})

	t.Run("unknown task is a no-op", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		a.OnError(t.Context(), "missing", &a2a.Error{Message: "boom"}, nil)
		if calls := mock.calls(); len(calls) != 0 {
			t.Fatalf("unknown task error should send nothing, got %d", len(calls))
		}
	})
}

func TestAdapter_ResolveAgentName(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Helper"})

	if res := a.resolveTarget(t.Context(), nil, "hey @Helper do this"); res.Name != "Helper" {
		t.Errorf("mention route = %q, want Helper", res.Name)
	}
	if res := a.resolveTarget(t.Context(), nil, "no mention here"); res.Name != "Builder" {
		t.Errorf("default route = %q, want Builder", res.Name)
	}

	a.cfg.DefaultAgentName = "ghost"
	if res := a.resolveTarget(t.Context(), nil, "hello"); !res.Misconfigured {
		t.Errorf("expected misconfigured for unknown default, got %+v", res)
	}
}

func TestAdapter_NoOpHooks(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")

	a.OnStatusUpdate(t.Context(), "task", &a2a.TaskStatusUpdateEvent{}, nil)
	a.OnArtifactUpdate(t.Context(), "task", &a2a.TaskArtifactUpdateEvent{}, nil)
	a.OnAgentDiscovered(&a2a.AgentCard{Name: "Builder"})
	a.OnAgentRemoved("Builder")
	a.handleStatus(&webhookChangeValue{}, &messageStatus{ID: "wamid.s", Status: "delivered", RecipientID: "15551230000"})
	a.handleStatus(&webhookChangeValue{}, &messageStatus{ID: "wamid.f", Status: "failed", RecipientID: "15551230000"})
}

func TestResolveIdentity(t *testing.T) {
	t.Parallel()
	id := senderIdentity("15551230000", "Alice")
	if id.UserID() != "whatsapp:15551230000" {
		t.Errorf("UserID = %q, want whatsapp:15551230000", id.UserID())
	}
	if id.Name() != "Alice" || id.AuthMethod() != "whatsapp" {
		t.Errorf("identity = %+v, want Name=Alice AuthMethod=whatsapp", id)
	}
	if id.Metadata()["whatsapp_wa_id"] != "15551230000" {
		t.Errorf("metadata wa_id = %v, want 15551230000", id.Metadata()["whatsapp_wa_id"])
	}
}

func TestBuildExternalKey(t *testing.T) {
	t.Parallel()
	if got, want := buildExternalKey("15551230000", 1787000000000), "whatsapp:15551230000:1787000000000"; got != want {
		t.Errorf("external key = %q, want %q", got, want)
	}
	if buildExternalKey("15551230000", 1) == buildExternalKey("15551230001", 1) {
		t.Error("two senders in the same epoch produced the same key")
	}
	if buildExternalKey("15551230000", 1) == buildExternalKey("15551230000", 2) {
		t.Error("two epochs for the same sender produced the same key")
	}
}

func TestResolveSessionID_NoStore(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	id, err := a.resolveSessionID(t.Context(), "whatsapp:15551230000:20260608", "Builder", "whatsapp:15551230000")
	if err != nil {
		t.Fatalf("resolveSessionID: %v", err)
	}
	if id == "" {
		t.Fatal("expected a generated session id in sessionless mode")
	}
}

func TestContactName(t *testing.T) {
	t.Parallel()
	value := &webhookChangeValue{
		Contacts: []webhookContact{{WaID: "15551230000"}},
		Messages: []inboundMessage{{From: "15551230000"}},
	}
	value.Contacts[0].Profile.Name = "Alice"
	if got := contactName(value, &inboundMessage{From: "15551230000"}); got != "Alice" {
		t.Errorf("contactName = %q, want Alice", got)
	}

	bsuid := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.ALICE"}},
		Messages: []inboundMessage{{FromUserID: "US.ALICE"}},
	}
	bsuid.Contacts[0].Profile.Name = "Alice"
	if got := contactName(bsuid, &inboundMessage{FromUserID: "US.ALICE"}); got != "Alice" {
		t.Errorf("contactName for a BSUID sender = %q, want Alice", got)
	}

	empty := &webhookChangeValue{
		Contacts: []webhookContact{{}},
		Messages: []inboundMessage{{}},
	}
	empty.Contacts[0].Profile.Name = "Somebody Else"
	if got := contactName(empty, &inboundMessage{}); got != "" {
		t.Errorf("contactName for an unidentified sender = %q, want empty", got)
	}
}

func TestBuildInboundParts(t *testing.T) {
	data := []byte("hello-bytes")
	wantB64 := base64.StdEncoding.EncodeToString(data)

	t.Run("with caption", func(t *testing.T) {
		parts := buildInboundParts("look at this", "photo.jpg", "image/jpeg", data)
		if len(parts) != 2 {
			t.Fatalf("got %d parts, want 2 (caption + file)", len(parts))
		}
		if parts[0].Kind != "text" || parts[0].Text != "look at this" {
			t.Errorf("part[0] = %+v, want text 'look at this'", parts[0])
		}
		if parts[1].Kind != "file" || parts[1].File == nil {
			t.Fatalf("part[1] = %+v, want a file part", parts[1])
		}
		if parts[1].File.Bytes != wantB64 {
			t.Errorf("file bytes = %q, want base64 %q", parts[1].File.Bytes, wantB64)
		}
		if parts[1].File.Name != "photo.jpg" || parts[1].File.MIMEType != "image/jpeg" {
			t.Errorf("file name/mime = %q/%q, want photo.jpg/image/jpeg", parts[1].File.Name, parts[1].File.MIMEType)
		}
	})

	t.Run("without caption", func(t *testing.T) {
		parts := buildInboundParts("", "doc.pdf", "application/pdf", data)
		if len(parts) != 1 {
			t.Fatalf("got %d parts, want 1 (file only)", len(parts))
		}
		if parts[0].Kind != "file" {
			t.Errorf("part[0].Kind = %q, want file", parts[0].Kind)
		}
	})

	t.Run("empty mime defaults to octet-stream", func(t *testing.T) {
		parts := buildInboundParts("", "f.bin", "", data)
		if parts[0].File.MIMEType != "application/octet-stream" {
			t.Errorf("mime = %q, want application/octet-stream", parts[0].File.MIMEType)
		}
	})
}

func TestExtractArtifactReturns(t *testing.T) {
	t.Run("single marker", func(t *testing.T) {
		specs, clean := extractArtifactReturns("Here is your report.\n«artifact_return:report.md» thanks")
		if len(specs) != 1 || specs[0] != "report.md" {
			t.Fatalf("specs = %v, want [report.md]", specs)
		}
		if strings.Contains(clean, "artifact_return") || strings.Contains(clean, "«") {
			t.Errorf("clean text still has marker: %q", clean)
		}
		if !strings.Contains(clean, "Here is your report") || !strings.Contains(clean, "thanks") {
			t.Errorf("clean text lost surrounding prose: %q", clean)
		}
	})

	t.Run("multiple markers", func(t *testing.T) {
		specs, _ := extractArtifactReturns("«artifact_return:a.png»«artifact_return:b.pdf»")
		if len(specs) != 2 || specs[0] != "a.png" || specs[1] != "b.pdf" {
			t.Errorf("specs = %v, want [a.png b.pdf]", specs)
		}
	})

	t.Run("no marker", func(t *testing.T) {
		specs, clean := extractArtifactReturns("just text")
		if len(specs) != 0 {
			t.Errorf("specs = %v, want empty", specs)
		}
		if clean != "just text" {
			t.Errorf("clean = %q, want unchanged", clean)
		}
	})
}

func TestMediaTypeForMIME(t *testing.T) {
	cases := map[string]string{
		"image/png":       "image",
		"Image/JPEG":      "image",
		"audio/ogg":       "audio",
		"video/mp4":       "video",
		"video/3gpp":      "video",
		"application/pdf": "document",
		"text/csv":        "document",
		"application/zip": "document",
		"":                "document",
	}
	for mime, want := range cases {
		if got := mediaTypeForMIME(mime); got != want {
			t.Errorf("mediaTypeForMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}

func TestResolveOutboundMIME(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mime, name, want string
	}{
		{"Image/PNG", "pic.png", "image/png"},
		{"TEXT/CSV", "data.csv", "text/plain"},
		{"application/octet-stream", "famous_places.txt", "text/plain"},
		{"", "notes.txt", "text/plain"},
		{"text/csv", "data.csv", "text/plain"},
		{"text/markdown", "report.md", "text/plain"},
		{"text/plain; charset=utf-8", "a.txt", "text/plain"},
		{"application/pdf", "doc.pdf", "application/pdf"},
		{"image/png", "pic.png", "image/png"},
		{"image/jpeg; charset=binary", "pic.jpg", "image/jpeg"},
		{"application/octet-stream", "blob.unknownext", "application/octet-stream"},
	}
	for _, c := range cases {
		if got := resolveOutboundMIME(c.mime, c.name); got != c.want {
			t.Errorf("resolveOutboundMIME(%q, %q) = %q, want %q", c.mime, c.name, got, c.want)
		}
	}
}

func TestResolveOutboundMIME_MetaAcceptance(t *testing.T) {
	t.Parallel()
	accepted := []struct{ mime, name string }{
		{"application/octet-stream", "a.txt"},
		{"text/csv", "a.csv"},
		{"application/pdf", "a.pdf"},
		{"image/png", "a.png"},
	}
	for _, c := range accepted {
		if got := resolveOutboundMIME(c.mime, c.name); !metaAcceptedMediaMIME[got] {
			t.Errorf("resolveOutboundMIME(%q,%q)=%q not in Meta whitelist", c.mime, c.name, got)
		}
	}
	rejected := []struct{ mime, name string }{
		{"application/zip", "a.zip"},
		{"application/octet-stream", "a.bin"},
	}
	for _, c := range rejected {
		if got := resolveOutboundMIME(c.mime, c.name); metaAcceptedMediaMIME[got] {
			t.Errorf("resolveOutboundMIME(%q,%q)=%q should NOT be in Meta whitelist", c.mime, c.name, got)
		}
	}
}

func TestMaskSenderID(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"16135550178": "whatsapp:***0178",
		"123":         "whatsapp:****",
		"":            "whatsapp:****",
	}
	for in, want := range cases {
		if got := maskSenderID(in); got != want {
			t.Errorf("maskSenderID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAudioFilename(t *testing.T) {
	cases := map[string]string{
		"audio/ogg":              "voice.ogg",
		"audio/ogg; codecs=opus": "voice.ogg",
		"audio/mpeg":             "voice.mpeg",
	}
	for mime, want := range cases {
		if got := audioFilename(&mediaObject{MimeType: mime}); got != want {
			t.Errorf("audioFilename(%q) = %q, want %q", mime, got, want)
		}
	}
	if got := audioFilename(nil); got != "voice.ogg" {
		t.Errorf("audioFilename(nil) = %q, want voice.ogg", got)
	}
}

func TestDocumentFilename(t *testing.T) {
	cases := []struct {
		in   *mediaObject
		want string
	}{
		{nil, "document"},
		{&mediaObject{}, "document"},
		{&mediaObject{Filename: "report.pdf"}, "report.pdf"},
		{&mediaObject{Filename: "../../etc/passwd"}, "passwd"},
		{&mediaObject{Filename: "sub/dir/data.csv"}, "data.csv"},
		{&mediaObject{Filename: "/"}, "document"},
	}
	for _, c := range cases {
		if got := documentFilename(c.in); got != c.want {
			t.Errorf("documentFilename(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func mediaAdapter(t *testing.T, metaJSON, dlPath string, dlBytes []byte, logger ...*slog.Logger) (*Adapter, *waMock, *broker.DevBroker) {
	t.Helper()
	log := rigLogger(logger)
	m := &waMock{t: t}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m.server = srv

	mux.HandleFunc("/v26.0/MEDIA1", func(w http.ResponseWriter, _ *http.Request) {
		m.countMediaRequest()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll(metaJSON, "${SRV}", srv.URL)))
	})
	mux.HandleFunc(dlPath, func(w http.ResponseWriter, _ *http.Request) {
		m.countMediaRequest()
		_, _ = w.Write(dlBytes)
	})
	mux.HandleFunc("/v26.0/555111/messages", func(w http.ResponseWriter, r *http.Request) {
		var b outboundTextMessage
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &b)
		m.mu.Lock()
		m.sends = append(m.sends, b)
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mockSendResponse()))
	})

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, testAppConfig(t, "test-gw"))
	scoped := &artifact.ScopedService{Inner: artmem.New(), Scope: "namespace", Namespace: "test-ns"}
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped)).Services())
	initCoreForTest(t, core)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: srv.URL, MaxMediaSizeMB: 16, DefaultAgentName: "Builder",
		DisableRateLimiting: true,
	}
	a := &Adapter{
		core: core, cfg: cfg, logger: log,
		claims: newMemoryClaims(), claimLease: defaultClaimLease, hilDedup: runtime.NewMapCache(), client: NewClient(cfg, log), ctx: t.Context(),
	}
	startTestSender(t, a)
	m.adapter = a
	return a, m, brk
}

func TestAdapter_HandleInbound_Image(t *testing.T) {
	t.Parallel()
	mediaBytes := []byte("\xff\xd8\xffJPEGbytes")
	a, _, brk := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"image/jpeg","file_size":12,"id":"MEDIA1"}`,
		"/dl/MEDIA1", mediaBytes)

	q, err := brk.CreateQueue("wa-img", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	msg := &inboundMessage{From: "15551230000", ID: "wamid.img", Type: "image",
		Image: &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg", Caption: "look at this"}}
	value := &webhookChangeValue{Contacts: []webhookContact{{WaID: "15551230000"}}, Messages: []inboundMessage{*msg}}
	a.deliver(value, msg)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	bm, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no broker publish — the file part never reached the agent: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(bm.Payload, &req); err != nil {
		t.Fatalf("decode a2a request: %v", err)
	}
	params, _ := req.Params.(map[string]any)
	msgData, _ := params["message"].(map[string]any)
	parts, _ := msgData["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (caption + file), got %d: %v", len(parts), parts)
	}
	filePart, _ := parts[1].(map[string]any)
	if filePart["kind"] != "file" {
		t.Fatalf("part[1] kind = %v, want file", filePart["kind"])
	}
	file, _ := filePart["file"].(map[string]any)
	if file["bytes"] != nil && file["bytes"] != "" {
		t.Errorf("file bytes should be empty (sent by reference), got %v", file["bytes"])
	}
	uri, _ := file["uri"].(string)
	if _, _, ok := entrypoint.ParseArtifactURI(uri); !ok {
		t.Fatalf("file uri = %q, want a parseable artifact:// reference", uri)
	}
	if file["name"] != "image.jpeg" || file["mimeType"] != "image/jpeg" {
		t.Errorf("file name/mime = %v/%v, want image.jpeg / image/jpeg", file["name"], file["mimeType"])
	}
}

func TestAdapter_HandleInbound_Image_NoArtifactService(t *testing.T) {
	t.Parallel()
	a, _, brk := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"image/jpeg","file_size":12,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte("\xff\xd8\xffJPEGbytes"))
	a.core.SetServices(runtime.NewRuntime(brk, nil).Services())

	q, err := brk.CreateQueue("wa-nostore", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	msg := &inboundMessage{From: "15551230000", ID: "wamid.img", Type: "image",
		Image: &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg", Caption: "look at this"}}
	value := &webhookChangeValue{Contacts: []webhookContact{{WaID: "15551230000"}}, Messages: []inboundMessage{*msg}}
	a.deliver(value, msg)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	bm, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no broker publish: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(bm.Payload, &req); err != nil {
		t.Fatalf("decode a2a request: %v", err)
	}
	params, _ := req.Params.(map[string]any)
	msgData, _ := params["message"].(map[string]any)
	parts, _ := msgData["parts"].([]any)
	var sawNote bool
	for _, raw := range parts {
		p, _ := raw.(map[string]any)
		if p["kind"] == "file" {
			t.Errorf("no file part expected without an artifact store, got %v", p)
		}
		if p["kind"] == "text" && strings.Contains(p["text"].(string), "could not be processed") {
			sawNote = true
		}
	}
	if !sawNote {
		t.Errorf("expected a dropped-file note, parts = %v", parts)
	}
}

func TestAdapter_HandleInbound_MediaTooLarge(t *testing.T) {
	t.Parallel()
	a, mock, _ := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"image/jpeg","file_size":99000000,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte("unused"))

	msg := &inboundMessage{From: "15551230000", ID: "wamid.big", Type: "image",
		Image: &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg"}}
	a.deliver(&webhookChangeValue{Messages: []inboundMessage{*msg}}, msg)

	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
		t.Fatalf("oversized media must not submit a task, got %d", len(ids))
	}
	if calls := mock.calls(); len(calls) != 1 || !strings.Contains(calls[0].Text.Body, "file size limit") {
		t.Fatalf("expected a file-size-limit notice, got %+v", mock.calls())
	}
}

func TestAdapter_HandleInbound_MediaMissingObject(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	msg := &inboundMessage{From: "15551230000", ID: "wamid.noimg", Type: "image"}
	a.deliver(&webhookChangeValue{}, msg)

	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
		t.Fatalf("missing media object must not submit a task, got %d", len(ids))
	}
	if calls := mock.calls(); len(calls) != 1 {
		t.Fatalf("expected one couldn't-read notice, got %d", len(calls))
	}
}

func TestAdapter_HandleInbound_MediaFailureSettlement(t *testing.T) {
	t.Parallel()
	const okMeta = `{"url":"${SRV}/dl/MEDIA1","mime_type":"image/jpeg","file_size":12,"id":"MEDIA1"}`
	for _, tt := range []struct {
		name        string
		metaJSON    string
		dlBytes     []byte
		media       *mediaObject
		wantDecline string
		wantSettle  ingressSettlement
		wantClaim   string
	}{
		{
			name: "missing media object", metaJSON: okMeta, dlBytes: []byte("\xff\xd8\xffJPEG"),
			media: nil, wantDecline: "couldn't read",
			wantSettle: settleAck, wantClaim: store.WhatsAppMessageDone,
		},
		{
			name: "media id empty", metaJSON: okMeta, dlBytes: []byte("\xff\xd8\xffJPEG"),
			media: &mediaObject{MimeType: "image/jpeg"}, wantDecline: "couldn't read",
			wantSettle: settleAck, wantClaim: store.WhatsAppMessageDone,
		},
		{
			name:        "exceeds size cap",
			metaJSON:    `{"url":"${SRV}/dl/MEDIA1","mime_type":"image/jpeg","file_size":99000000,"id":"MEDIA1"}`,
			dlBytes:     []byte("\xff\xd8\xffJPEG"),
			media:       &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg"},
			wantDecline: "file size limit",
			wantSettle:  settleAck, wantClaim: store.WhatsAppMessageDone,
		},
		{
			name:       "download fails",
			metaJSON:   `{"url":"${SRV}/dl/GONE","mime_type":"image/jpeg","file_size":12,"id":"MEDIA1"}`,
			dlBytes:    []byte("\xff\xd8\xffJPEG"),
			media:      &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg"},
			wantSettle: settleNack, wantClaim: store.WhatsAppMessageRetrying,
		},
		{
			name:       "download returns no bytes",
			metaJSON:   okMeta,
			dlBytes:    nil,
			media:      &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg"},
			wantSettle: settleNack, wantClaim: store.WhatsAppMessageRetrying,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, mock, _ := mediaAdapter(t, tt.metaJSON, "/dl/MEDIA1", tt.dlBytes)

			msg := &inboundMessage{From: "15551230000", ID: "wamid.mediafail", Type: "image", Image: tt.media}
			value := &webhookChangeValue{Contacts: []webhookContact{{WaID: "15551230000"}}, Messages: []inboundMessage{*msg}}

			if got := a.deliver(value, msg); got != tt.wantSettle {
				t.Fatalf("settlement = %v, want %v", got, tt.wantSettle)
			}
			if got := a.claimState(msg.ID); got != tt.wantClaim {
				t.Fatalf("claim state = %q, want %q", got, tt.wantClaim)
			}

			var declines int
			for _, sent := range mock.calls() {
				if tt.wantDecline != "" && strings.Contains(sent.Text.Body, tt.wantDecline) {
					declines++
				}
			}
			switch tt.wantSettle {
			case settleAck:
				if declines != 1 {
					t.Errorf("notices containing %q = %d, want exactly 1; sends = %+v", tt.wantDecline, declines, mock.calls())
				}
			case settleNack:
				if len(mock.calls()) != 0 {
					t.Errorf("a retryable media failure must stay silent, got sends = %+v", mock.calls())
				}
			}
			if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
				t.Errorf("a media failure must submit no task, got %d", len(ids))
			}
		})
	}
}

func TestImageFilename(t *testing.T) {
	cases := []struct{ mime, want string }{
		{"image/jpeg", "image.jpeg"},
		{"image/png", "image.png"},
		{"image/webp", "image.webp"},
		{"image/svg+xml", "image.jpg"},
		{"image/jpeg; charset=utf-8", "image.jpg"},
		{"", "image.jpg"},
		{"image/", "image.jpg"},
		{"noslash", "image.jpg"},
	}
	for _, c := range cases {
		if got := imageFilename(&mediaObject{MimeType: c.mime}); got != c.want {
			t.Errorf("imageFilename(%q) = %q, want %q", c.mime, got, c.want)
		}
	}
	if got := imageFilename(nil); got != "image.jpg" {
		t.Errorf("imageFilename(nil) = %q, want image.jpg", got)
	}
}

func TestAdapter_HandleInbound_MediaEmptyBytes(t *testing.T) {
	t.Parallel()
	a, mock, _ := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"image/jpeg","file_size":0,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte{})

	msg := &inboundMessage{From: "15551230000", ID: "wamid.zero", Type: "image",
		Image: &mediaObject{ID: "MEDIA1", MimeType: "image/jpeg"}}
	if got := a.deliver(&webhookChangeValue{Messages: []inboundMessage{*msg}}, msg); got != settleNack {
		t.Fatalf("empty media settled %v, want settleNack", got)
	}

	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
		t.Fatalf("empty media must not submit a task, got %d", len(ids))
	}
	if calls := mock.calls(); len(calls) != 0 {
		t.Fatalf("a retryable media failure must stay silent, got %d sends", len(calls))
	}
	if got := a.claimState(msg.ID); got != store.WhatsAppMessageRetrying {
		t.Fatalf("claim state = %q, want %q so the redelivery reclaims it", got, store.WhatsAppMessageRetrying)
	}
}

type outboundMock struct {
	server     *httptest.Server
	t          *testing.T
	mu         sync.Mutex
	uploads    int
	uploaded   [][]byte
	msgs       []map[string]any
	rejectSend bool
	adapter    *Adapter
}

func newOutboundMock(t *testing.T) *outboundMock {
	t.Helper()
	m := &outboundMock{t: t}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m.server = srv
	mux.HandleFunc("/v26.0/555111/media", func(w http.ResponseWriter, r *http.Request) {
		var file []byte
		if part, _, err := r.FormFile("file"); err == nil {
			file, _ = io.ReadAll(part)
			_ = part.Close()
		}
		m.mu.Lock()
		m.uploads++
		m.uploaded = append(m.uploaded, file)
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"media-out-1"}`))
	})
	mux.HandleFunc("/v26.0/555111/messages", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		m.mu.Lock()
		m.msgs = append(m.msgs, body)
		reject := m.rejectSend
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if reject {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":131026,"message":"Message undeliverable","error_data":{"details":"to: 15551234567"}}}`))
			return
		}
		_, _ = w.Write([]byte(mockSendResponse()))
	})
	return m
}

func (m *outboundMock) snapshot() (int, []map[string]any) {
	if m.adapter != nil {
		m.t.Helper()
		waitQueueDrained(m.t, m.adapter)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.uploads, slices.Clone(m.msgs)
}

func newOutboundAdapter(t *testing.T, cfg *WhatsAppConfig, taskID, phone string) *Adapter {
	t.Helper()
	a := &Adapter{
		core: newArtifactCore(t), cfg: cfg, logger: discardLogger(),
		claims: newMemoryClaims(), claimLease: defaultClaimLease, hilDedup: runtime.NewMapCache(),
		client: NewClient(cfg, discardLogger()), ctx: t.Context(),
	}
	startTestSender(t, a)
	_ = a.core.Tasks().Register(taskID, "Builder", "user", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &taskMeta{FromPhone: phone, SessionID: "sess"})
	return a
}

func TestAdapter_OnFinalResponse_DeliversInlineFilePart(t *testing.T) {
	t.Parallel()
	m := newOutboundMock(t)
	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: m.server.URL, MaxMediaSizeMB: 7, DisableRateLimiting: true,
	}
	a := newOutboundAdapter(t, cfg, taskT1, "15551230000")
	m.adapter = a

	pngB64 := base64.StdEncoding.EncodeToString([]byte("\x89PNGdata"))
	task := &a2a.Task{Status: a2a.TaskStatus{
		State: a2a.TaskStateCompleted,
		Message: &a2a.Message{Parts: []a2a.Part{
			{Kind: "text", Text: "here's your chart"},
			{Kind: "file", File: &a2a.FileContent{Bytes: pngB64, MIMEType: "image/png", Name: "out.png"}},
		}},
	}}
	a.OnFinalResponse(t.Context(), taskT1, task, nil)

	uploads, msgs := m.snapshot()
	if uploads != 1 {
		t.Fatalf("expected 1 media upload, got %d", uploads)
	}
	var sawImage bool
	for _, msg := range msgs {
		if msg["type"] == "image" {
			img, _ := msg["image"].(map[string]any)
			if img["id"] == "media-out-1" {
				sawImage = true
			}
		}
	}
	if !sawImage {
		t.Fatalf("no image media message with the uploaded id; messages = %v", msgs)
	}
}

func TestAdapter_OnFinalResponse_ArtifactOnlyDeliveryFailsNotifies(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var msgs []map[string]any
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/v26.0/555111/media", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":100,"message":"unsupported media"}}`))
	})
	mux.HandleFunc("/v26.0/555111/messages", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &b)
		mu.Lock()
		msgs = append(msgs, b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"x"}]}`))
	})

	cfg := &WhatsAppConfig{
		AccessToken: "t", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: srv.URL, MaxMediaSizeMB: 7, DisableRateLimiting: true,
	}
	a := newOutboundAdapter(t, cfg, taskT1, "15551230000")

	pngB64 := base64.StdEncoding.EncodeToString([]byte("\x89PNGdata"))
	task := &a2a.Task{Status: a2a.TaskStatus{
		State: a2a.TaskStateCompleted,
		Message: &a2a.Message{Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{Bytes: pngB64, MIMEType: "image/png", Name: "out.png"}},
		}},
	}}
	a.OnFinalResponse(t.Context(), taskT1, task, nil)

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		seen := slices.Clone(msgs)
		mu.Unlock()
		for _, m := range seen {
			txt, _ := m["text"].(map[string]any)
			if body, _ := txt["body"].(string); strings.Contains(body, "attachment couldn't be delivered") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected an undeliverable-attachment notice, got %v", seen)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newOutboundArtifactRig(t *testing.T, logger ...*slog.Logger) (*Adapter, *outboundMock) {
	t.Helper()
	log := rigLogger(logger)
	m := newOutboundMock(t)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, testAppConfig(t, "test-gw"))
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped)).Services())
	initCoreForTest(t, core)

	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: m.server.URL, MaxMediaSizeMB: 7, DisableRateLimiting: true,
	}
	a := &Adapter{
		core: core, cfg: cfg, logger: log,
		claims: newMemoryClaims(), claimLease: defaultClaimLease, hilDedup: runtime.NewMapCache(), client: NewClient(cfg, log), ctx: t.Context(),
	}
	startTestSender(t, a)
	m.adapter = a
	_ = a.core.Tasks().Register(taskT1, "Builder", "user", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: "15551230000", SessionID: "sess"})

	loc := scoped.Resolve("Builder", "user", "sess", "chart.png")
	if _, err := scoped.Inner.Save(t.Context(), loc, []byte("\x89PNG-chart-bytes"), "image/png"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	return a, m
}

func assertOneImageDelivered(t *testing.T, m *outboundMock) {
	t.Helper()
	uploads, msgs := m.snapshot()
	if uploads != 1 {
		t.Fatalf("expected 1 media upload, got %d", uploads)
	}
	for _, msg := range msgs {
		if msg["type"] == "image" {
			if img, _ := msg["image"].(map[string]any); img["id"] == "media-out-1" {
				return
			}
		}
	}
	t.Fatalf("no image media message with the uploaded id; messages = %v", msgs)
}

func TestAdapter_OnFinalResponse_DeliversFromArtifactStore(t *testing.T) {
	t.Run("artifact_return marker", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		task := &a2a.Task{Status: a2a.TaskStatus{
			State:   a2a.TaskStateCompleted,
			Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("here it is «artifact_return:chart.png»")}},
		}}
		a.OnFinalResponse(t.Context(), taskT1, task, nil)
		assertOneImageDelivered(t, m)
	})

	t.Run("artifact:// file part", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		task := &a2a.Task{Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{Parts: []a2a.Part{
				{Kind: "file", File: &a2a.FileContent{URI: "artifact://Builder/user/sess/chart.png"}},
			}},
		}}
		a.OnFinalResponse(t.Context(), taskT1, task, nil)
		assertOneImageDelivered(t, m)
	})
}

func TestAdapter_OnFinalResponse_FilePartNameDiffersFromURI(t *testing.T) {
	a, m := newOutboundArtifactRig(t)
	artSvc := a.core.Services().Artifacts()
	loc := artSvc.Resolve("Builder", "user", "sess", "report_final_v2.pdf")
	if _, err := artSvc.Inner.Save(t.Context(), loc, []byte("%PDF-1.4 report"), "application/pdf"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	task := &a2a.Task{Status: a2a.TaskStatus{
		State: a2a.TaskStateCompleted,
		Message: &a2a.Message{Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{
				URI:  "artifact://Builder/user/sess/report_final_v2.pdf",
				Name: "Quarterly Report.pdf",
			}},
		}},
	}}
	a.OnFinalResponse(t.Context(), taskT1, task, nil)

	uploads, msgs := m.snapshot()
	if uploads != 1 {
		t.Fatalf("expected 1 media upload, got %d — artifact dropped from the reply; messages = %v", uploads, msgs)
	}
	for _, msg := range msgs {
		if msg["type"] != "document" {
			continue
		}
		doc, _ := msg["document"].(map[string]any)
		if doc["filename"] == "Quarterly Report.pdf" {
			return
		}
	}
	t.Fatalf("no document message under the display name; messages = %v", msgs)
}

func artifactSavedEvent(t *testing.T, saved a2a.ArtifactSavedData, messageMeta map[string]any) *a2a.TaskStatusUpdateEvent {
	t.Helper()
	saved.Type = string(a2a.SignalArtifactSaved)
	raw, err := json.Marshal(a2a.TaskStatusUpdateEvent{
		TaskID: taskT1, Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Metadata: messageMeta,
			Parts: []a2a.Part{a2a.NewDataPart(saved)},
		}},
	})
	if err != nil {
		t.Fatalf("marshal status update: %v", err)
	}
	var event a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("unmarshal status update: %v", err)
	}
	return &event
}

func textOnlyFinal(text string) *a2a.Task {
	return &a2a.Task{Status: a2a.TaskStatus{
		State:   a2a.TaskStateCompleted,
		Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart(text)}},
	}}
}

func TestAdapter_OnFinalResponse_DeliversSavedArtifact(t *testing.T) {
	saved := a2a.ArtifactSavedData{Filename: "chart.png", Version: 0, MIMEType: "image/png"}

	t.Run("saved and not returned", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, saved, nil), nil)
		a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("here is your chart"), nil)
		assertOneImageDelivered(t, m)
	})

	t.Run("saved and also returned is sent once", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, saved, nil), nil)
		a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("here it is «artifact_return:chart.png»"), nil)
		assertOneImageDelivered(t, m)
	})

	t.Run("saved and returned as a file part is sent once", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, saved, nil), nil)
		a.OnFinalResponse(t.Context(), taskT1, &a2a.Task{Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{Parts: []a2a.Part{
				{Kind: "file", File: &a2a.FileContent{URI: "artifact://Builder/user/sess/chart.png"}},
			}},
		}}, nil)
		assertOneImageDelivered(t, m)
	})

	t.Run("saved and returned as inline bytes is sent once", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, saved, nil), nil)
		a.OnFinalResponse(t.Context(), taskT1, &a2a.Task{Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
				Bytes: base64.StdEncoding.EncodeToString([]byte("\x89PNG-inline")), MIMEType: "image/png", Name: "chart.png",
			}}}},
		}}, nil)
		assertOneImageDelivered(t, m)
	})

	t.Run("saved twice is sent once", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, saved, nil), nil)
		a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, saved, nil), nil)
		a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("here is your chart"), nil)
		assertOneImageDelivered(t, m)
	})

	t.Run("forwarded by the task's own target", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, saved, map[string]any{"forwarded_from_peer": "Builder"}), nil)
		a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("Workflow completed successfully."), nil)
		assertOneImageDelivered(t, m)
	})
}

func TestAdapter_OnFinalResponse_SavedArtifactLatestVersionWins(t *testing.T) {
	a, m := newOutboundArtifactRig(t)
	artSvc := a.core.Services().Artifacts()
	loc := artSvc.Resolve("Builder", "user", "sess", "chart.png")
	v1, err := artSvc.Inner.Save(t.Context(), loc, []byte("\x89PNG-chart-v1"), "image/png")
	if err != nil {
		t.Fatalf("save v1: %v", err)
	}
	a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, a2a.ArtifactSavedData{Filename: "chart.png", Version: 0, MIMEType: "image/png"}, nil), nil)
	a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, a2a.ArtifactSavedData{Filename: "chart.png", Version: v1, MIMEType: "image/png"}, nil), nil)
	a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("here is your chart"), nil)

	assertOneImageDelivered(t, m)
	m.mu.Lock()
	got := string(m.uploaded[0])
	m.mu.Unlock()
	if got != "\x89PNG-chart-v1" {
		t.Fatalf("uploaded %q, want the v1 bytes", got)
	}
}

func TestAdapter_OnFinalResponse_SkipsSavedArtifactsNotForTheUser(t *testing.T) {
	chart := a2a.ArtifactSavedData{Filename: "chart.png", MIMEType: "image/png"}
	working := chart
	working.Tags = []string{a2a.ArtifactTagWorking}
	cases := map[string]struct {
		setup   func(t *testing.T, a *Adapter)
		saves   []a2a.ArtifactSavedData
		msgMeta map[string]any
	}{
		"working intermediate":                           {saves: []a2a.ArtifactSavedData{working}},
		"working re-save supersedes the earlier version": {saves: []a2a.ArtifactSavedData{chart, working}},
		"forwarded from another peer": {
			saves:   []a2a.ArtifactSavedData{chart},
			msgMeta: map[string]any{"forwarded_from_peer": "Researcher"},
		},
		"missing from the store": {saves: []a2a.ArtifactSavedData{{Filename: "gone.png", MIMEType: "image/png"}}},
		"file type WhatsApp refuses": {
			setup: func(t *testing.T, a *Adapter) {
				artSvc := a.core.Services().Artifacts()
				loc := artSvc.Resolve("Builder", "user", "sess", "scratch.json")
				if _, err := artSvc.Inner.Save(t.Context(), loc, []byte(`{"a":1}`), "application/json"); err != nil {
					t.Fatalf("save artifact: %v", err)
				}
			},
			saves: []a2a.ArtifactSavedData{{Filename: "scratch.json", MIMEType: "application/json"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a, m := newOutboundArtifactRig(t)
			if tc.setup != nil {
				tc.setup(t, a)
			}
			for _, saved := range tc.saves {
				a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, saved, tc.msgMeta), nil)
			}
			a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("done"), nil)
			uploads, msgs := m.snapshot()
			if uploads != 0 {
				t.Fatalf("expected no media upload, got %d; messages = %v", uploads, msgs)
			}
			if bodies := textBodies(msgs); len(bodies) != 1 || bodies[0] != "done" {
				t.Fatalf("expected only the reply text, got %q", bodies)
			}
		})
	}
}

func TestAdapter_OnFinalResponse_CapsDeliveredSaves(t *testing.T) {
	t.Run("over the delivery cap", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		artSvc := a.core.Services().Artifacts()
		for i := range maxDeliveredSaves + 2 {
			name := fmt.Sprintf("chart-%d.png", i)
			if _, err := artSvc.Inner.Save(t.Context(), artSvc.Resolve("Builder", "user", "sess", name), []byte("\x89PNG"), "image/png"); err != nil {
				t.Fatalf("save %s: %v", name, err)
			}
			a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, a2a.ArtifactSavedData{Filename: name, MIMEType: "image/png"}, nil), nil)
		}
		a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("done"), nil)
		uploads, msgs := m.snapshot()
		if uploads != maxDeliveredSaves {
			t.Fatalf("uploads = %d, want %d", uploads, maxDeliveredSaves)
		}
		if bodies := textBodies(msgs); !slices.Contains(bodies, fmt.Sprintf(msgMoreSavedFilesFmt, 2)) {
			t.Fatalf("expected the more-files notice, got %q", bodies)
		}
	})

	t.Run("over the recording bound", func(t *testing.T) {
		a, m := newOutboundArtifactRig(t)
		for i := range maxRecordedSaves + 1 {
			a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, a2a.ArtifactSavedData{Filename: fmt.Sprintf("unstored-%d.png", i)}, nil), nil)
		}
		a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("done"), nil)
		if _, msgs := m.snapshot(); !slices.Contains(textBodies(msgs), msgManyMoreSavedFiles) {
			t.Fatalf("expected the more-files notice, got %q", textBodies(msgs))
		}
	})
}

func TestAdapter_ReturnedArtifactMissingFromStoreIsNotSentEmpty(t *testing.T) {
	a, m := newOutboundArtifactRig(t)
	a.OnFinalResponse(t.Context(), taskT1, textOnlyFinal("here «artifact_return:gone.png»"), nil)
	deadline := time.Now().Add(10 * time.Second)
	for {
		uploads, msgs := m.snapshot()
		if uploads != 0 {
			t.Fatalf("a missing artifact was uploaded; messages = %v", msgs)
		}
		if slices.Contains(textBodies(msgs), msgAttachmentUndeliverable) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the undeliverable notice, got %q", textBodies(msgs))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func textBodies(msgs []map[string]any) []string {
	var bodies []string
	for _, msg := range msgs {
		if txt, ok := msg["text"].(map[string]any); ok {
			body, _ := txt["body"].(string)
			bodies = append(bodies, body)
		}
	}
	return bodies
}

func TestAdapter_OutboundMediaPolicy(t *testing.T) {
	t.Parallel()
	a := newAdapterRig(t, newWAMock(t), "Builder")

	if p := a.checkOutboundMedia("image/png", "x.png", 1024); !p.ok || p.mediaType != "image" {
		t.Errorf("png should be accepted as image, got %+v", p)
	}
	p := a.checkOutboundMedia("application/x-totally-unsupported", "x.bin", 10)
	if p.ok {
		t.Error("non-whitelisted MIME must be rejected")
	}
	if !strings.Contains(p.reason, "doesn't accept that file type") {
		t.Errorf("rejection reason = %q, want the unsupported-type notice", p.reason)
	}

	a.cfg.MaxMediaSizeMB = 1
	if over := a.checkOutboundMedia("image/png", "big.png", 2*1024*1024); over.ok {
		t.Error("oversized media must be rejected")
	} else if !strings.Contains(over.reason, "too large") {
		t.Errorf("rejection reason = %q, want the oversize notice", over.reason)
	}
}

func TestAdapter_QueueFilePart_Errors(t *testing.T) {
	t.Parallel()
	a := newAdapterRig(t, newWAMock(t), "Builder")
	target := outboundTarget{recipient: "15551230000", taskID: taskT1, sessionID: "sess"}

	cases := map[string]*a2a.FileContent{
		"bad base64":     {Bytes: "!!! not base64 !!!"},
		"empty":          {},
		"non-artifact":   {URI: "https://example.com/file.png"},
		"uri, no artsvc": {URI: "artifact://Builder/user/sess/chart.png"},
	}
	for name, fc := range cases {
		if a.queueFilePart(target, fc) {
			t.Errorf("%s: expected the file part to be dropped", name)
		}
	}
}

func TestAdapter_TerminalTaskClearsPendingHIL(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"

	setup := func(t *testing.T) *Adapter {
		t.Helper()
		a := newAdapterRig(t, newWAMock(t), "Builder")
		_ = a.core.Tasks().Register(taskT1, "Builder", "user", entrypoint.TaskIdentity{})
		a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess"})
		seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{TaskID: taskT1, RequestID: "req-1"})
		return a
	}

	t.Run("OnFinalResponse failed", func(t *testing.T) {
		a := setup(t)
		a.OnFinalResponse(t.Context(), taskT1, &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}, nil)
		if pendingPromptRow(t, a, phone) != nil {
			t.Fatal("failed task must clear the pending HIL prompt")
		}
	})

	t.Run("OnError", func(t *testing.T) {
		a := setup(t)
		a.OnError(t.Context(), taskT1, &a2a.Error{Message: "boom"}, nil)
		if pendingPromptRow(t, a, phone) != nil {
			t.Fatal("task error must clear the pending HIL prompt")
		}
	})
}

func audioParts(t *testing.T, a *Adapter, brk *broker.DevBroker) []any {
	t.Helper()
	q, err := brk.CreateQueue("wa-aud", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	msg := &inboundMessage{From: "15551230000", ID: "wamid.aud", Type: "audio",
		Audio: &mediaObject{ID: "MEDIA1", MimeType: "audio/ogg"}}
	value := &webhookChangeValue{Contacts: []webhookContact{{WaID: "15551230000"}}, Messages: []inboundMessage{*msg}}
	a.deliver(value, msg)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	bm, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no broker publish for audio: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(bm.Payload, &req); err != nil {
		t.Fatalf("decode a2a request: %v", err)
	}
	params, _ := req.Params.(map[string]any)
	msgData, _ := params["message"].(map[string]any)
	parts, _ := msgData["parts"].([]any)
	return parts
}

func partsHaveFile(parts []any) bool {
	for _, p := range parts {
		if pm, _ := p.(map[string]any); pm["kind"] == "file" {
			return true
		}
	}
	return false
}

func audioMediaAdapter(t *testing.T) (*Adapter, *broker.DevBroker) {
	a, _, brk := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"audio/ogg","file_size":12,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte("OGG-AUDIO-BYTES"))
	return a, brk
}

func TestAdapter_HandleInbound_Audio_ForwardsFile(t *testing.T) {
	t.Parallel()
	a, brk := audioMediaAdapter(t)
	parts := audioParts(t, a, brk)
	if !partsHaveFile(parts) {
		t.Fatalf("expected the audio forwarded as a file part, got %v", parts)
	}
}

func TestAdapter_HandleInbound_Audio_Declines(t *testing.T) {
	t.Parallel()
	assertFailed := func(t *testing.T, a *Adapter, mock *waMock, audio *mediaObject, wantSettle ingressSettlement, wantSubstr string) {
		t.Helper()
		msg := &inboundMessage{From: "15551230000", ID: "wamid.dec", Type: "audio", Audio: audio}
		if got := a.deliver(&webhookChangeValue{Messages: []inboundMessage{*msg}}, msg); got != wantSettle {
			t.Fatalf("settlement = %v, want %v", got, wantSettle)
		}
		if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
			t.Fatalf("failed audio must not submit a task, got %d", len(ids))
		}
		calls := mock.calls()
		if wantSubstr == "" && wantSettle == settleNack {
			if len(calls) != 0 {
				t.Fatalf("a retryable audio failure must stay silent, got %d sends", len(calls))
			}
			return
		}
		if len(calls) != 1 {
			t.Fatalf("expected one decline notice, got %d", len(calls))
		}
		if wantSubstr != "" && !strings.Contains(calls[0].Text.Body, wantSubstr) {
			t.Fatalf("notice %q missing %q", calls[0].Text.Body, wantSubstr)
		}
	}

	t.Run("missing media object", func(t *testing.T) {
		mock := newWAMock(t)
		a := newAdapterRig(t, mock, "Builder")
		a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
		assertFailed(t, a, mock, nil, settleAck, "couldn't read")
	})
	t.Run("too large", func(t *testing.T) {
		a, mock, _ := mediaAdapter(t,
			`{"url":"${SRV}/dl/MEDIA1","mime_type":"audio/ogg","file_size":99000000,"id":"MEDIA1"}`,
			"/dl/MEDIA1", []byte("unused"))
		assertFailed(t, a, mock, &mediaObject{ID: "MEDIA1", MimeType: "audio/ogg"}, settleAck, "file size limit")
	})
	t.Run("empty download", func(t *testing.T) {
		a, mock, _ := mediaAdapter(t,
			`{"url":"${SRV}/dl/MEDIA1","mime_type":"audio/ogg","file_size":0,"id":"MEDIA1"}`,
			"/dl/MEDIA1", []byte{})
		assertFailed(t, a, mock, &mediaObject{ID: "MEDIA1", MimeType: "audio/ogg"}, settleNack, "")
	})
}

func TestAdapter_HandleInbound_Audio_ParamMIME(t *testing.T) {
	t.Parallel()
	a, _, brk := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"audio/ogg; codecs=opus","file_size":12,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte("OGG-OPUS"))
	parts := audioParts(t, a, brk)
	if !partsHaveFile(parts) {
		t.Fatalf("expected audio file part for parameterized MIME, got %v", parts)
	}
}

func TestAdapter_QueueFilePart_RejectsUnacceptedMIME(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	target := outboundTarget{recipient: "15551230000"}
	fc := &a2a.FileContent{
		Bytes:    base64.StdEncoding.EncodeToString([]byte("data")),
		MIMEType: "application/x-evil",
		Name:     "evil.bin",
	}
	if a.queueFilePart(target, fc) {
		t.Fatal("a non-whitelisted MIME must not be queued")
	}
	calls := mock.calls()
	if len(calls) != 1 || !strings.Contains(calls[0].Text.Body, "doesn't accept") {
		t.Fatalf("expected a single 'not accepted' notice, got %v", calls)
	}
}

func testEnvelope(a *Adapter, value *webhookChangeValue, msg *inboundMessage) *ingressEnvelope {
	env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
	authz := a.authorizeSender(a.ctx, env, contactName(value, msg))
	env.authorized, env.authorizedSource = authz.Identity, authz.Source
	return env
}
