// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const callbackBudget = 2 * time.Second

type gatingMock struct {
	server *httptest.Server

	mu          sync.Mutex
	sends       []gatedSend
	rateLimited map[string]int
	rejected    map[string]int
	hold        chan struct{}
	inFlight    int
	aborted     int
}

type gatedSend struct {
	to   string
	text string
	at   time.Time
}

func newGatingMock(t *testing.T) *gatingMock {
	t.Helper()
	m := &gatingMock{rateLimited: map[string]int{}, rejected: map[string]int{}}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body outboundTextMessage
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)

		m.mu.Lock()
		hold := m.hold
		limit := m.rateLimited[body.To] > 0
		if limit {
			m.rateLimited[body.To]--
		}
		reject := m.rejected[body.To] > 0
		if reject {
			m.rejected[body.To]--
		}
		m.mu.Unlock()

		if hold != nil {
			m.mu.Lock()
			m.inFlight++
			m.mu.Unlock()
			select {
			case <-hold:
			case <-r.Context().Done():
			}
			if r.Context().Err() != nil {
				m.mu.Lock()
				m.aborted++
				m.mu.Unlock()
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if limit {
			w.Header().Set(headerBusinessUsage, `{"biz":[{"type":"messaging","estimated_time_to_regain_access":2}]}`)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":131056,"message":"too many messages"}}`))
			return
		}
		if reject {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":100,"message":"invalid parameter"}}`))
			return
		}
		m.mu.Lock()
		m.sends = append(m.sends, gatedSend{to: body.To, text: body.Text.Body, at: time.Now()})
		m.mu.Unlock()
		_, _ = w.Write([]byte(mockSendResponse()))
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *gatingMock) holdSends() func() {
	m.mu.Lock()
	m.hold = make(chan struct{})
	held := m.hold
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		m.hold = nil
		m.mu.Unlock()
		close(held)
	}
}

func (m *gatingMock) rejectOnce(recipient string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected[recipient] = 1
}

func (m *gatingMock) rateLimit(recipient string, times int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimited[recipient] = times
}

func (m *gatingMock) waitInFlight(t *testing.T, n int) {
	t.Helper()
	m.waitCounter(t, n, func() int { return m.inFlight }, "in-flight")
}

func (m *gatingMock) waitAborted(t *testing.T, n int) {
	t.Helper()
	m.waitCounter(t, n, func() int { return m.aborted }, "aborted")
}

func (m *gatingMock) waitCounter(t *testing.T, n int, read func() int, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.mu.Lock()
		got := read()
		m.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d %s requests, got %d", n, what, got)
		}
		time.Sleep(time.Millisecond)
	}
}

func (m *gatingMock) delivered() []gatedSend {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]gatedSend, len(m.sends))
	copy(out, m.sends)
	return out
}

func (m *gatingMock) waitForText(t *testing.T, substr string) []gatedSend {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := m.delivered()
		for _, s := range got {
			if strings.Contains(s.text, substr) {
				return got
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no delivered message contained %q; got %+v", substr, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (m *gatingMock) waitDelivered(t *testing.T, n int) []gatedSend {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if got := m.delivered(); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d deliveries; got %v", n, m.delivered())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func pacedRig(t *testing.T, mock *gatingMock, dsn string, pace ...time.Duration) *Adapter {
	t.Helper()
	return pacedRigWithQueue(t, mock, dsn, nil, pace...)
}

func pacedRigWithQueue(t *testing.T, mock *gatingMock, dsn string, wrap func(senderQueue) senderQueue, pace ...time.Duration) *Adapter {
	t.Helper()
	return pacedRigWith(t, mock, dsn, wrap, discardLogger(), pace...)
}

func pacedRigWith(t *testing.T, mock *gatingMock, dsn string, wrap func(senderQueue) senderQueue, logger *slog.Logger, pace ...time.Duration) *Adapter {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	appConfig := map[string]any{
		"namespace": "test-ns", "gateway_id": "test-gw",
		"session_service": map[string]any{
			"type": "sql", "database_url": dsn, "default_behavior": "PERSISTENT",
		},
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, appConfig)
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithArtifacts(&artifact.ScopedService{Inner: artmem.New()})).Services())
	initCoreForTest(t, core)
	if core.Store() == nil {
		t.Fatal("the test core has no database, so this would not exercise the durable path")
	}

	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: mock.server.URL, MaxMediaSizeMB: 7,
	}
	a := &Adapter{
		core: core, cfg: cfg, logger: logger,
		claims: newMemoryClaims(), claimLease: defaultClaimLease,
		hilDedup: runtime.NewMapCache(),
		client:   NewClient(cfg, discardLogger()),
	}
	a.ctx, a.cancel = context.WithCancel(t.Context())
	t.Cleanup(a.cancel)
	if err := a.initDurableState(); err != nil {
		t.Fatalf("initDurableState: %v", err)
	}
	a.sender.idleTimeout = 100 * time.Millisecond
	a.sender.recoveryLease = 20 * time.Millisecond
	a.sender.recoveryInterval = 20 * time.Millisecond
	a.sender.recoveryHardLease = time.Hour
	if wrap != nil {
		a.sender.queue = wrap(a.sender.queue)
		a.ledger = a.sender.queue
	}
	if len(pace) > 0 {
		a.sender.pace = pace[0]
	}
	a.sender.Start(t.Context())
	t.Cleanup(func() {
		if a.stopped.Load() {
			return
		}
		if !a.sender.Stop(context.Background(), 2*time.Second) {
			t.Error("sender pool did not drain; the cleanup below closes the database it is still writing to")
		}
	})
	return a
}

func testOutboundDSN(t *testing.T) string {
	t.Helper()
	return testDBURL(t)
}

func registerReplyTask(a *Adapter, taskID, phone string) {
	_ = a.core.Tasks().Register(taskID, "Builder", "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &taskMeta{FromPhone: phone, SessionID: "sess-" + taskID})
}

func completedTask(text string) *a2a.Task {
	return &a2a.Task{Status: a2a.TaskStatus{
		State:   a2a.TaskStateCompleted,
		Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart(text)}},
	}}
}

func TestAdapter_CallbacksDoNotWaitOnDelivery(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t))
	release := mock.holdSends()
	defer release()

	registerReplyTask(a, taskT1, "15551230000")
	long := strings.Repeat("a", waTextBodyMax) + " " + strings.Repeat("b", waTextBodyMax) + " " + strings.Repeat("c", 10)

	start := time.Now()
	a.OnFinalResponse(t.Context(), taskT1, completedTask(long), nil)
	if elapsed := time.Since(start); elapsed > callbackBudget {
		t.Fatalf("OnFinalResponse took %v (budget %v) — the callback is waiting on delivery again", elapsed, callbackBudget)
	}

	pending, err := a.outbound.PendingCount(t.Context(), a.gatewayID())
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if pending == 0 {
		t.Error("the callback returned without leaving anything queued — the reply would be lost")
	}
}

func TestAdapter_RateLimitedRecipientDoesNotDelayAnother(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t))
	mock.rateLimit("15551230000", 1)

	registerReplyTask(a, "t-slow", "15551230000")
	registerReplyTask(a, "t-fast", "15551239999")

	start := time.Now()
	a.OnFinalResponse(t.Context(), "t-slow", completedTask("rate limited reply"), nil)
	a.OnFinalResponse(t.Context(), "t-fast", completedTask("unaffected reply"), nil)
	if elapsed := time.Since(start); elapsed > callbackBudget {
		t.Fatalf("the two callbacks took %v (budget %v); a 429 must not be waited out in-callback", elapsed, callbackBudget)
	}

	got := mock.waitDelivered(t, 1)
	if got[0].to != "15551239999" {
		t.Fatalf("first delivery went to %q; the unaffected recipient must not queue behind the throttled one", got[0].to)
	}
}

func TestAdapter_QueuedReplySurvivesRestart(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	dsn := testOutboundDSN(t)

	first := pacedRig(t, mock, dsn)
	release := mock.holdSends()

	registerReplyTask(first, taskT1, "15551230000")
	first.OnFinalResponse(t.Context(), taskT1, completedTask("survive the restart"), nil)
	if first.core.Tasks().Get(taskT1) != nil {
		t.Fatal("OnFinalResponse should have retired the task registry entry")
	}

	mock.waitInFlight(t, 1)
	first.sender.sendCancel()
	mock.waitAborted(t, 1)
	release()
	if got := mock.delivered(); len(got) != 0 {
		t.Fatalf("the killed instance delivered %+v; the restart is what must deliver it", got)
	}

	second := pacedRig(t, mock, dsn)

	got := mock.waitDelivered(t, 1)
	if got[0].to != "15551230000" || !strings.Contains(got[0].text, "survive the restart") {
		t.Fatalf("delivered %+v, want the queued reply after the restart", got[0])
	}
	waitQueueDrained(t, second)
	if all := mock.delivered(); len(all) != 1 {
		t.Errorf("delivered %d messages, want exactly 1: %+v", len(all), all)
	}
}

func TestAdapter_PacingHappensOffTheCallback(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t), 300*time.Millisecond)

	registerReplyTask(a, taskT1, "15551230000")
	registerReplyTask(a, taskT2, "15551230000")
	start := time.Now()
	a.OnFinalResponse(t.Context(), taskT1, completedTask("first"), nil)
	a.OnFinalResponse(t.Context(), taskT2, completedTask("second"), nil)
	if elapsed := time.Since(start); elapsed > callbackBudget {
		t.Fatalf("callbacks took %v; the pacing must not be paid on this goroutine", elapsed)
	}

	got := mock.waitDelivered(t, 2)
	if gap := got[1].at.Sub(got[0].at); gap < 250*time.Millisecond {
		t.Errorf("consecutive sends were %v apart, want the pacing interval", gap)
	}
}

func TestAdapter_ReplyLeavesASentLedgerRow(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t))

	registerReplyTask(a, taskT1, "15551230000")
	a.OnFinalResponse(t.Context(), taskT1, completedTask("recorded"), nil)
	mock.waitDelivered(t, 1)
	waitQueueDrained(t, a)

	rows := allLedgerRows(t, a)
	if len(rows) != 1 {
		t.Fatalf("ledger holds %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.State != store.WhatsAppOutboundSent || row.Wamid == "" {
		t.Errorf("row = %q wamid %q, want sent with the id receipts key on", row.State, row.Wamid)
	}
	if row.Kind != kindText {
		t.Errorf("kind = %q, want %q for the agent's own answer", row.Kind, kindText)
	}
	if row.TaskID != taskT1 || row.AgentName != "Builder" || row.SessionID != "sess-"+taskT1 {
		t.Errorf("row provenance = task %q agent %q session %q; all three are needed to send after the task is gone",
			row.TaskID, row.AgentName, row.SessionID)
	}
}

func allLedgerRows(t *testing.T, a *Adapter) []store.WhatsAppOutbound {
	t.Helper()
	rows, err := dbutil.QueryRows[store.WhatsAppOutbound](context.Background(), a.core.Store(),
		`SELECT id, gateway_id, recipient, task_id, trace_id, user_id, agent_name, session_id,
			kind, payload, artifact_ref, state, wamid, error_code, error_detail,
			attempts, not_before, parked_at, created_at, updated_at
		 FROM whatsapp_outbound WHERE gateway_id = ? ORDER BY id`, a.gatewayID())
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return rows
}

func TestAdapter_TerminalChunkFailureWithdrawsTheRemainder(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t), 0)
	mock.rejectOnce("15551230000")

	registerReplyTask(a, taskT1, "15551230000")
	long := strings.Repeat("a", waTextBodyMax) + " " + strings.Repeat("b", 32)
	a.OnFinalResponse(t.Context(), taskT1, completedTask(long), nil)
	waitQueueDrained(t, a)

	if got := mock.delivered(); len(got) != 0 {
		t.Fatalf("delivered %+v; the first chunk failed, so nothing should have gone out", got)
	}
	rows := allLedgerRows(t, a)
	if len(rows) != 2 {
		t.Fatalf("ledger holds %d rows, want the two chunks", len(rows))
	}
	var failed, superseded int
	for _, row := range rows {
		switch row.State {
		case store.WhatsAppOutboundFailed:
			failed++
		case store.WhatsAppOutboundSuperseded:
			superseded++
		}
	}
	if failed != 1 || superseded != 1 {
		t.Errorf("states = %d failed / %d superseded, want 1 each: the failure is real, the withdrawal is not", failed, superseded)
	}
}

func TestAdapter_PolicyRejectedMediaSendsOneNotice(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t), 0)

	target := outboundTarget{recipient: "15551230000", taskID: taskT1, userID: "user", agentName: "Builder", sessionID: "sess"}
	if a.enqueue(kindMedia, target, outboundPayload{Media: &mediaPayload{
		Filename:   "notes.xyz",
		MIMEType:   "application/x-nope",
		MediaType:  "document",
		InlineText: "some text",
	}}, "") == "" {
		t.Fatal("failed to queue the media row")
	}
	sent := mock.waitForText(t, "doesn't accept that file type")
	waitQueueDrained(t, a)

	var notices []string
	for _, call := range mock.delivered() {
		notices = append(notices, call.text)
	}
	if len(notices) != 1 {
		t.Fatalf("the user got %d notices, want exactly one (the specific reason, not the generic follow-up too): %v",
			len(notices), notices)
	}
	_ = sent
}

type mediaHostileQueue struct {
	senderQueue
}

func (q mediaHostileQueue) Enqueue(ctx context.Context, tx dbutil.Querier, row *store.WhatsAppOutbound) error {
	if row.Kind == kindMedia {
		return errors.New("no room for media")
	}
	return q.senderQueue.Enqueue(ctx, tx, row)
}

func TestAdapter_AttachmentThatWasNeverQueuedIsAdmitted(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRigWithQueue(t, mock, testOutboundDSN(t),
		func(q senderQueue) senderQueue { return mediaHostileQueue{q} }, time.Millisecond)

	registerReplyTask(a, taskT1, "15551230000")
	tooLong := strings.Repeat("a", waTextBodyMax*maxReplyChunks+1)
	a.OnFinalResponse(t.Context(), taskT1, completedTask(tooLong), nil)

	sent := mock.waitForText(t, msgAttachmentUndeliverable)
	if !slices.ContainsFunc(sent, func(s gatedSend) bool { return strings.Contains(s.text, attachmentNote) }) {
		t.Errorf("the preview was not delivered; got %+v", sent)
	}
}

func pendingCount(t *testing.T, a *Adapter) int {
	t.Helper()
	n, err := a.outbound.PendingCount(context.Background(), a.gatewayID())
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	return n
}

func TestAdapter_OutcomeWritesSurviveTheShutdownWindow(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t))
	release := mock.holdSends()
	defer release()

	registerReplyTask(a, taskT1, "15551230000")
	long := strings.Repeat("a", waTextBodyMax) + " " + strings.Repeat("b", waTextBodyMax) + " " + strings.Repeat("c", 10)
	a.OnFinalResponse(t.Context(), taskT1, completedTask(long), nil)

	deadline := time.Now().Add(5 * time.Second)
	for pendingCount(t, a) < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if before := pendingCount(t, a); before != 3 {
		t.Fatalf("queued %d rows, want the 3 chunks", before)
	}

	a.cancel()

	a.onDeliveryFailed(&store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: a.gatewayID(), TaskID: taskT1,
		Recipient: "15551230000", Kind: kindText, State: store.WhatsAppOutboundFailed,
	}, true)

	for pendingCount(t, a) == 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if after := pendingCount(t, a); after == 3 {
		t.Error("nothing was withdrawn after the inbound context was cancelled; the rest of the reply goes to the next pod")
	}
}

func TestAdapter_UndeliverablePromptNoticeSurvivesTheShutdownWindow(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t), time.Millisecond)

	const phone = "15551230000"
	registerReplyTask(a, taskT1, phone)
	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{
		RequestID: "req-1", TaskID: taskT1, AgentName: "Builder",
		UserID: "whatsapp:" + phone, OutboundID: "row-prompt"})

	a.cancel()

	a.onDeliveryFailed(&store.WhatsAppOutbound{
		ID: "row-prompt", GatewayID: a.gatewayID(), TaskID: taskT1,
		Recipient: phone, Kind: kindInteractivePrompt, State: store.WhatsAppOutboundFailed,
	}, true)

	mock.waitForText(t, msgPromptUndeliverable)
}

func TestAdapter_SettleCtxOutlivesTheInboundContext(t *testing.T) {
	t.Parallel()
	mock := newGatingMock(t)
	a := pacedRig(t, mock, testOutboundDSN(t), time.Millisecond)

	a.cancel()
	if a.ctx.Err() == nil {
		t.Fatal("a.cancel did not cancel a.ctx; the test models nothing")
	}

	ctx, cancel := a.settleCtx()
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Errorf("settleCtx is already dead (%v); every write on it fails during shutdown", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("settleCtx has no deadline; a detached context with no bound can hang shutdown")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > adapterSettleTimeout {
		t.Errorf("deadline is %v out, want a positive bound within %v", remaining, adapterSettleTimeout)
	}
}
