// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type ingressRig struct {
	t     *testing.T
	brk   *broker.DevBroker
	db    *store.DB
	dbURL string
	mock  *waMock
	agent broker.Queue

	adapter *Adapter
	core    *entrypoint.GatewayCore
	handler http.Handler

	tune []func(*Adapter)
}

const ingressTestAppSecret = "ingress-secret"

func newIngressRig(t *testing.T, tune ...func(*Adapter)) *ingressRig {
	t.Helper()
	return newIngressRigBefore(t, nil, tune...)
}

func newIngressRigBefore(t *testing.T, before func(r *ingressRig), tune ...func(*Adapter)) *ingressRig {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	db, dbURL := newIngressDB(t)
	mock := newWAMock(t)

	agentQ, err := brk.CreateQueue("ingress-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err := brk.Subscribe(agentQ, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe agent topic: %v", err)
	}

	r := &ingressRig{t: t, brk: brk, db: db, dbURL: dbURL, mock: mock, agent: agentQ, tune: tune}
	if before != nil {
		before(r)
	}
	r.start()
	t.Cleanup(func() { r.stop() })
	return r
}

func newIngressDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	dbURL := testDBURL(t)
	db, err := store.Open(dbURL, discardLogger())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dbURL
}

func (r *ingressRig) start() {
	r.t.Helper()
	adapterCfg := map[string]any{
		"access_token":          "tok-test",
		"phone_number_id":       "555111",
		"app_secret":            ingressTestAppSecret,
		"verify_token":          "verify",
		"default_agent_name":    "Builder",
		"graph_api_base_url":    r.mock.server.URL,
		"graph_api_version":     "v26.0",
		"disable_rate_limiting": true,
	}
	adapter, err := NewAdapter(adapterCfg)
	if err != nil {
		r.t.Fatalf("NewAdapter: %v", err)
	}
	adapter.SetOnHandlerReady(func(h http.Handler) { r.handler = h })

	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"gateway_adapter": map[string]any{
			"type":           "whatsapp",
			"adapter_config": adapterCfg,
		},
		"session_service": map[string]any{
			"type":             "sql",
			"database_url":     r.dbURL,
			"default_behavior": "PERSISTENT",
		},
	})
	core.SetServices(runtime.NewRuntime(r.brk, nil).Services())
	if err := core.Init(r.t.Context(), nil); err != nil {
		r.t.Fatalf("core.Init: %v", err)
	}
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	for _, tune := range r.tune {
		tune(adapter)
	}
	if err := core.Start(r.t.Context()); err != nil {
		r.t.Fatalf("core.Start: %v", err)
	}
	r.adapter, r.core = adapter, core
	if r.handler == nil {
		r.t.Fatal("adapter did not publish a webhook handler during Init")
	}
}

func (r *ingressRig) stop() {
	if r.core == nil {
		return
	}
	_ = r.core.Stop(context.Background())
	r.core, r.handler = nil, nil
}

func (r *ingressRig) restart() {
	r.t.Helper()
	r.stop()
	r.start()
}

func (r *ingressRig) post(body string) int {
	r.t.Helper()
	return postSigned(r.handler, body)
}

func postSigned(handler http.Handler, body string) int {
	mac := hmac.New(sha256.New, []byte(ingressTestAppSecret))
	mac.Write([]byte(body))
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr.Code
}

func textWebhook(wamid, from, text string) string {
	return fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"waba","changes":[{"field":"messages","value":{`+
		`"metadata":{"display_phone_number":"15550000000","phone_number_id":"555111"},`+
		`"contacts":[{"wa_id":%q,"profile":{"name":"Test User"}}],`+
		`"messages":[{"from":%q,"id":%q,"type":"text","text":{"body":%q}}]}}]}]}`,
		from, from, wamid, text)
}

func (r *ingressRig) agentRequests(within time.Duration) []string {
	r.t.Helper()
	deadline := time.Now().Add(within)
	var got []string
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return got
		}
		ctx, cancel := context.WithTimeout(context.Background(), remaining)
		msg, err := r.brk.Receive(ctx, r.agent)
		cancel()
		if err != nil || msg == nil {
			return got
		}
		msg.Ack()
		got = append(got, inboundTextOf(r.t, msg.Payload))
	}
}

func (r *ingressRig) awaitAgentRequests(want int, settle time.Duration) []string {
	r.t.Helper()
	const idleGiveUp = 3 * time.Second
	var got []string
	lastProgress := time.Now()
	for len(got) < want && time.Since(lastProgress) < idleGiveUp {
		before := len(got)
		got = append(got, r.agentRequests(100*time.Millisecond)...)
		if len(got) > before {
			lastProgress = time.Now()
		}
	}
	got = append(got, r.agentRequests(settle)...)
	return got
}

func inboundTextOf(t *testing.T, payload []byte) string {
	t.Helper()
	var req struct {
		Params struct {
			Message struct {
				Parts []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal agent request: %v", err)
	}
	for _, p := range req.Params.Message.Parts {
		if p.Kind == "text" && p.Text != "" {
			return p.Text
		}
	}
	return ""
}

func TestIngress_DedupSurvivesRestart(t *testing.T) {
	r := newIngressRig(t)
	const wamid = "wamid.RESTART-1"
	body := textWebhook(wamid, "15551230000", "run the report")

	if got := r.post(body); got != http.StatusOK {
		t.Fatalf("first delivery status = %d, want 200", got)
	}
	if got := r.awaitAgentRequests(1, 200*time.Millisecond); len(got) != 1 {
		t.Fatalf("first delivery produced %d agent requests, want 1: %v", len(got), got)
	}
	if state, err := store.NewWhatsAppProcessedRepo(r.db).State(t.Context(), "test-gw", wamid); err != nil || state != store.WhatsAppMessageDone {
		t.Fatalf("claim state = %q (err %v), want %q", state, err, store.WhatsAppMessageDone)
	}

	r.restart()

	if got := r.post(body); got != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200 (the duplicate is dropped, not refused)", got)
	}
	if got := r.agentRequests(500 * time.Millisecond); len(got) != 0 {
		t.Fatalf("redelivery after restart produced %d more agent requests, want 0: %v", len(got), got)
	}
}

func TestIngress_CrashBetweenClaimAndSubmitStillDelivers(t *testing.T) {
	const wamid = "wamid.CRASH-1"
	body := textWebhook(wamid, "15551230000", "run the report")

	var crashed atomic.Bool
	r := newIngressRig(t, func(a *Adapter) {
		a.claimLease = 10 * time.Millisecond
		a.afterClaim = func(string) bool { return !crashed.CompareAndSwap(false, true) }
	})

	if got := r.post(body); got != http.StatusOK {
		t.Fatalf("first delivery status = %d, want 200", got)
	}
	if got := r.agentRequests(500 * time.Millisecond); len(got) != 0 {
		t.Fatalf("a crash before submit must produce no task, got %v", got)
	}
	if state, err := store.NewWhatsAppProcessedRepo(r.db).State(t.Context(), "test-gw", wamid); err != nil || state != store.WhatsAppMessageProcessing {
		t.Fatalf("claim state after the crash = %q (err %v), want %q — a 'done' row here loses the message",
			state, err, store.WhatsAppMessageProcessing)
	}

	time.Sleep(20 * time.Millisecond)
	if got := r.post(body); got != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200", got)
	}
	got := r.awaitAgentRequests(1, 200*time.Millisecond)
	if len(got) != 1 || got[0] != "run the report" {
		t.Fatalf("redelivery after the crash produced %v, want exactly [run the report]", got)
	}
}

func TestIngress_BrokerDownRefusesAckThenRecovers(t *testing.T) {
	r := newIngressRig(t)
	body := textWebhook("wamid.BROKER-1", "15551230000", "run the report")

	if err := r.brk.Disconnect(t.Context()); err != nil {
		t.Fatalf("disconnect broker: %v", err)
	}
	if got := r.post(body); got == http.StatusOK {
		t.Fatal("webhook 200-acked while the broker was down — Meta will never redeliver a 2xx, so the message is lost")
	}
	if got := r.agentRequests(200 * time.Millisecond); len(got) != 0 {
		t.Fatalf("no task can exist while the broker is down, got %v", got)
	}

	if err := r.brk.Connect(t.Context()); err != nil {
		t.Fatalf("reconnect broker: %v", err)
	}
	if got := r.post(body); got != http.StatusOK {
		t.Fatalf("redelivery status after recovery = %d, want 200", got)
	}
	if got := r.awaitAgentRequests(1, 200*time.Millisecond); len(got) != 1 {
		t.Fatalf("redelivery after recovery produced %d agent requests, want 1: %v", len(got), got)
	}
}

func TestIngress_RemoveMidPostRefusesRatherThanDrops(t *testing.T) {
	r := newIngressRig(t)

	const posters = 32
	var acked, refused atomic.Int64
	start := make(chan struct{})
	var posting sync.WaitGroup
	for i := range posters {
		posting.Go(func() {
			<-start
			sender := fmt.Sprintf("1555123%04d", i)
			body := textWebhook(fmt.Sprintf("wamid.RACE-%d", i), sender, "run the report")
			switch got := r.post(body); got {
			case http.StatusOK:
				acked.Add(1)
			case http.StatusServiceUnavailable:
				refused.Add(1)
			default:
				t.Errorf("status = %d, want 200 or 503", got)
			}
		})
	}

	close(start)
	r.adapter.server.Close()
	posting.Wait()

	if total := acked.Load() + refused.Load(); total != posters {
		t.Fatalf("accounted-for responses = %d, want %d", total, posters)
	}
	got := r.awaitAgentRequests(int(acked.Load()), 300*time.Millisecond)
	if int64(len(got)) != acked.Load() {
		t.Fatalf("agent requests = %d, acked deliveries = %d — an acked webhook must never be dropped", len(got), acked.Load())
	}
}

func TestIngress_PerSenderOrdering(t *testing.T) {
	r := newIngressRig(t)

	const sender = "15551230000"
	for i, text := range []string{"first", "second", "third"} {
		if got := r.post(textWebhook(fmt.Sprintf("wamid.ORDER-%d", i), sender, text)); got != http.StatusOK {
			t.Fatalf("delivery %d status = %d, want 200", i, got)
		}
	}

	got := r.awaitAgentRequests(3, 300*time.Millisecond)
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("agent requests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("one sender's messages arrived as %v, want %v — a sender's stream must not be reordered", got, want)
		}
	}
}

func TestIngress_DistinctSendersRunInParallel(t *testing.T) {
	r := newIngressRig(t)

	senders := []string{"15551230000", "15559990000"}
	for _, from := range senders {
		for i, text := range []string{"a", "b"} {
			body := textWebhook(fmt.Sprintf("wamid.PAR-%s-%d", from, i), from, from+"-"+text)
			if got := r.post(body); got != http.StatusOK {
				t.Fatalf("delivery status = %d, want 200", got)
			}
		}
	}

	got := r.awaitAgentRequests(4, 300*time.Millisecond)
	if len(got) != 4 {
		t.Fatalf("agent requests = %v, want 4", got)
	}
	for _, from := range senders {
		var seq []string
		for _, text := range got {
			if strings.HasPrefix(text, from+"-") {
				seq = append(seq, text)
			}
		}
		want := []string{from + "-a", from + "-b"}
		if len(seq) != 2 || seq[0] != want[0] || seq[1] != want[1] {
			t.Fatalf("sender %s stream = %v, want %v", from, seq, want)
		}
	}
}

func TestIngress_SlowSenderDoesNotBlockOthers(t *testing.T) {
	const slowSender, fastSender = "15551230000", "15559990000"
	workers := uint64(resolveIngressWorkers())
	if hashSenderID(slowSender)%workers == hashSenderID(fastSender)%workers {
		t.Skipf("senders collide on worker %d; parallelism is not promised within one worker",
			hashSenderID(slowSender)%workers)
	}

	release := make(chan struct{})
	r := newIngressRig(t, func(a *Adapter) {
		a.afterClaim = func(wamid string) bool {
			if strings.Contains(wamid, "SLOW") {
				<-release
			}
			return true
		}
	})
	t.Cleanup(func() { close(release) })

	if got := r.post(textWebhook("wamid.SLOW-1", slowSender, "slow")); got != http.StatusOK {
		t.Fatalf("slow delivery status = %d, want 200", got)
	}
	if got := r.post(textWebhook("wamid.FAST-1", fastSender, "fast")); got != http.StatusOK {
		t.Fatalf("fast delivery status = %d, want 200", got)
	}

	got := r.awaitAgentRequests(1, 200*time.Millisecond)
	if len(got) != 1 || got[0] != "fast" {
		t.Fatalf("agent requests while one sender is blocked = %v, want exactly [fast] — "+
			"a stalled sender must not hold up the others", got)
	}
}

func TestIngress_SenderHashPinsOneWorker(t *testing.T) {
	t.Parallel()
	const workers = 8
	idx := func(s string) uint64 { return hashSenderID(s) % workers }

	for sender, want := range map[string]uint64{
		"15551230000":             7255444543951041117,
		"US.13491208655302741918": 16930604375165621776,
	} {
		if got := hashSenderID(sender); got != want {
			t.Errorf("hashSenderID(%q) = %d, want %d", sender, got, want)
		}
	}

	seen := map[uint64]bool{}
	for i := range 64 {
		seen[idx(fmt.Sprintf("1555123%04d", i))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("64 senders mapped onto %d worker(s); the pool is not spreading load", len(seen))
	}
}

type queueCallSpy struct {
	broker.Broker
	mu            sync.Mutex
	deleted       []string
	deprovisioned []string
}

func (s *queueCallSpy) DeleteQueue(name string) error {
	s.mu.Lock()
	s.deleted = append(s.deleted, name)
	s.mu.Unlock()
	return s.Broker.DeleteQueue(name)
}

func (s *queueCallSpy) DeprovisionQueue(name string) error {
	s.mu.Lock()
	s.deprovisioned = append(s.deprovisioned, name)
	s.mu.Unlock()
	return s.Broker.DeprovisionQueue(name)
}

func (s *queueCallSpy) calls() (deleted, deprovisioned []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.deleted), slices.Clone(s.deprovisioned)
}

func TestIngress_StopReleasesQueueRemoveDeprovisions(t *testing.T) {
	t.Parallel()
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	spy := &queueCallSpy{Broker: dev}
	mock := newWAMock(t)

	adapterCfg := map[string]any{
		"access_token":       "tok-test",
		"phone_number_id":    "555111",
		"app_secret":         ingressTestAppSecret,
		"verify_token":       "verify",
		"graph_api_base_url": mock.server.URL,
	}
	adapter, err := NewAdapter(adapterCfg)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	adapter.SetOnHandlerReady(func(http.Handler) {})
	core := entrypoint.NewGatewayCore(adapter, testAppConfig(t, "gw-teardown"))
	core.SetServices(runtime.NewRuntime(spy, nil).Services())
	initCoreForTest(t, core)

	queueName := a2a.WhatsAppIngressQueueNameV2("test-ns", "gw-teardown")
	previousName := a2a.WhatsAppIngressQueueName("test-ns", "gw-teardown")

	if err := adapter.DeprovisionAdapterQueues(t.Context()); err != nil {
		t.Fatalf("DeprovisionAdapterQueues: %v", err)
	}
	if err := adapter.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	deleted, deprovisioned := spy.calls()
	if want := []string{queueName, previousName}; !slices.Equal(deprovisioned, want) {
		t.Errorf("permanent removal deprovisioned %v, want %v", deprovisioned, want)
	}
	if !slices.Contains(deleted, queueName) {
		t.Errorf("Stop did not release the receiver for %q; deleted = %v", queueName, deleted)
	}
}

func waitQueueGone(t *testing.T, brk *broker.DevBroker, name string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for brk.HasQueue(name) {
		if time.Now().After(deadline) {
			t.Fatalf("queue %q still exists", name)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestIngress_UndecodableMessageIsDropped(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")

	var acked, nacked atomic.Int64
	msg := broker.NewDeliveredMessage("id", "topic", []byte("{not json"),
		func() { acked.Add(1) }, func() { nacked.Add(1) })
	a.handleIngressMessage(t.Context(), msg)

	if acked.Load() != 1 || nacked.Load() != 0 {
		t.Fatalf("acked=%d nacked=%d, want acked=1 nacked=0", acked.Load(), nacked.Load())
	}
}

func TestIngress_PublishCarriesSenderPartitionKey(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")

	value, msg := textMessage("wamid.key", "15551230000", "hi")
	value.Metadata.PhoneNumberID = "555111"
	if err := a.publishInbound(t.Context(), value, msg); err != nil {
		t.Fatalf("publishInbound: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	got, err := a.core.Services().Broker().Receive(ctx, a.ingressQueue)
	if err != nil {
		t.Fatalf("receive spooled message: %v", err)
	}
	if got.UserProperties["senderId"] != "15551230000" {
		t.Errorf("senderId property = %q, want the sender wa_id", got.UserProperties["senderId"])
	}
}

type stubClaims struct {
	mu        sync.Mutex
	outcomes  []store.ClaimOutcome
	err       error
	attempts  int
	total     int
	kind      string
	released  int
	token     int64
	loseFence bool
}

func (s *stubClaims) Claim(context.Context, string, string, time.Duration) (store.WhatsAppClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return store.WhatsAppClaim{Outcome: store.ClaimInFlight}, s.err
	}
	outcome := store.ClaimInFlight
	if len(s.outcomes) > 0 {
		outcome = s.outcomes[0]
		s.outcomes = s.outcomes[1:]
	}
	s.token++
	return store.WhatsAppClaim{
		Outcome: outcome, Token: s.token,
		Attempts: s.attempts, FailureKind: s.kind, AttemptsTotal: s.total,
	}, nil
}

func (s *stubClaims) MarkDone(context.Context, string, string, int64) (bool, error) {
	return !s.loseFence, nil
}

func (s *stubClaims) ReleaseForRetry(context.Context, string, string, int64, string) (bool, error) {
	s.mu.Lock()
	s.released++
	s.mu.Unlock()
	return !s.loseFence, nil
}

func TestIngress_ClaimHeldByAnotherConsumer(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		outcomes   []store.ClaimOutcome
		claimErr   error
		wantSettle ingressSettlement
	}{
		{
			name:       "resolves within the budget",
			outcomes:   []store.ClaimOutcome{store.ClaimInFlight, store.ClaimDuplicate},
			wantSettle: settleAck,
		},
		{
			name:       "outlasts the budget",
			outcomes:   nil,
			wantSettle: settleNack,
		},
		{
			name:       "claim store unreachable",
			claimErr:   errors.New("database is down"),
			wantSettle: settleNack,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, "Builder")
			claims := &stubClaims{outcomes: tt.outcomes, err: tt.claimErr}
			a.claims = claims
			a.claimWait = 2 * time.Second
			a.claimPoll = 10 * time.Millisecond

			value, msg := textMessage("wamid.inflight", "15551230000", "hi")
			if got := a.deliverInbound(t.Context(), &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}); got != tt.wantSettle {
				t.Fatalf("settlement = %v, want %v", got, tt.wantSettle)
			}
			if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
				t.Errorf("a message held by another consumer must not be processed here, got %d tasks", len(ids))
			}
			if claims.released != 0 {
				t.Errorf("Release called %d times; another consumer's claim is not ours to drop", claims.released)
			}
		})
	}
}

func TestIngress_OutcomeAndSettlementNames(t *testing.T) {
	t.Parallel()
	for got, want := range map[inboundOutcome]string{
		outcomeSubmitted:       "submitted",
		outcomeDeclined:        "declined",
		outcomeTransient:       "transient",
		outcomeUnauthorized:    "unauthorized",
		outcomeHILUnauthorized: "unauthorized_hil",
		outcomeStalled:         "stalled",
		inboundOutcome(9):      "unknown",
	} {
		if got.String() != want {
			t.Errorf("inboundOutcome(%d).String() = %q, want %q", int(got), got.String(), want)
		}
	}
	for got, want := range map[ingressSettlement]string{
		settleAck:            "ack",
		settleNack:           "nack",
		settleAbandon:        "abandon",
		ingressSettlement(9): "unknown",
	} {
		if got.String() != want {
			t.Errorf("ingressSettlement(%d).String() = %q, want %q", int(got), got.String(), want)
		}
	}
}

func TestIngress_TransientFailureIsPaced(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "")
	const pace = 60 * time.Millisecond
	a.retryDelay = pace

	value, msg := textMessage("wamid.paced", "15551230000", "hi")
	start := time.Now()
	if got := a.deliverInbound(t.Context(), &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}); got != settleNack {
		t.Fatalf("settlement = %v, want settleNack", got)
	}
	if elapsed := time.Since(start); elapsed < pace {
		t.Errorf("nacked after %v, want at least the %v pace — an unpaced nack spins the worker", elapsed, pace)
	}
}

type bindFailingBroker struct {
	broker.Broker
	mu          sync.Mutex
	failReceive error
}

func (b *bindFailingBroker) setFailure(err error) {
	b.mu.Lock()
	b.failReceive = err
	b.mu.Unlock()
}

func (b *bindFailingBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	b.mu.Lock()
	fail := b.failReceive
	b.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	return b.Broker.Receive(ctx, q)
}

func TestIngress_UnbindableQueueRefusesWebhooksRatherThanCrashing(t *testing.T) {
	t.Parallel()
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	brk := &bindFailingBroker{Broker: dev}
	brk.setFailure(errors.New("permission denied: create durable endpoint"))
	mock := newWAMock(t)

	adapterCfg := map[string]any{
		"access_token":       "tok-test",
		"phone_number_id":    "555111",
		"app_secret":         ingressTestAppSecret,
		"verify_token":       "verify",
		"graph_api_base_url": mock.server.URL,
	}
	adapter, err := NewAdapter(adapterCfg)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	adapter.SetOnHandlerReady(func(http.Handler) {})

	core := entrypoint.NewGatewayCore(adapter, testAppConfig(t, "gw-bind"))
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init must not fail on a transient bind failure: %v", err)
	}
	t.Cleanup(func() {
		if db := core.Store(); db != nil {
			_ = db.Close()
		}
	})

	if !adapter.ingressDown.Load() {
		t.Fatal("the adapter came up unlatched with an unbindable queue — it would 200-ack messages into a void")
	}
	if problems := adapter.HealthProblems(); len(problems) != 1 || problems[0].ID != "ingress" {
		t.Fatalf("HealthProblems() = %+v, want one problem with id \"ingress\" — the outage must reach the entrypoint's runtimeStatus, not just the local latch", problems)
	}
	value, msg := textMessage("wamid.unbound", "15551230000", "hi")
	value.Metadata.PhoneNumberID = "555111"
	if err := adapter.publishInbound(t.Context(), value, msg); err == nil {
		t.Fatal("publishInbound succeeded while unbound; the webhook would ack a message nothing can receive")
	}
}

func TestIngress_ProbeKeepsAMessageItPulledOff(t *testing.T) {
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	brk := a.core.Services().Broker()
	agentQ, err := brk.CreateQueue("probe-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if subErr := brk.Subscribe(agentQ, a2a.AgentRequestTopic("test-ns", "Builder")); subErr != nil {
		t.Fatalf("subscribe agent topic: %v", subErr)
	}

	value, msg := textMessage("wamid.BACKLOG", "15551230000", "backlog")
	value.Metadata.PhoneNumberID = "555111"
	if pubErr := a.publishInbound(t.Context(), value, msg); pubErr != nil {
		t.Fatalf("spool backlog message: %v", pubErr)
	}

	if !a.probeIngressBinding() {
		t.Fatal("probeIngressBinding reported the queue did not bind")
	}
	if a.pendingIngress == nil {
		t.Fatal("the probe consumed a queued message without holding it — that message is now lost")
	}

	a.startIngressDispatch(a.startIngressWorkers())
	t.Cleanup(a.stopIngressConsumer)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	got, err := brk.Receive(ctx, agentQ)
	if err != nil || got == nil {
		t.Fatalf("held message never reached an agent: %v", err)
	}
	if text := inboundTextOf(t, got.Payload); text != "backlog" {
		t.Fatalf("agent request text = %q, want %q", text, "backlog")
	}
}

func TestIngress_LostFenceIsSurvivable(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		agent      string
		wantSettle ingressSettlement
	}{
		{name: "lost while completing", agent: "Builder", wantSettle: settleAck},
		{name: "lost while releasing", agent: "", wantSettle: settleNack},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, tt.agent)
			if tt.agent != "" {
				a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: tt.agent})
			}
			claims := &stubClaims{outcomes: []store.ClaimOutcome{store.ClaimAcquired}, loseFence: true}
			a.claims = claims
			logs := &recordCollector{}
			a.logger = slog.New(logs)

			value, msg := textMessage("wamid.lostfence", "15551230000", "hi")
			if got := a.deliverInbound(t.Context(), &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}); got != tt.wantSettle {
				t.Fatalf("settlement = %v, want %v", got, tt.wantSettle)
			}
			var sawTakeover bool
			for _, r := range logs.all() {
				if strings.Contains(r.Message, "taken over") {
					sawTakeover = true
					if r.Level != slog.LevelWarn {
						t.Errorf("lost-fence notice logged at %v, want Warn", r.Level)
					}
				}
			}
			if !sawTakeover {
				t.Errorf("a lost fence left no trace; records = %d", len(logs.all()))
			}
		})
	}
}

func TestIngress_IdlessMessageIsNotRetriedForever(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "")

	value, msg := textMessage("", "15551230000", "hi")
	if got := a.deliverInbound(t.Context(), &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}); got != settleAck {
		t.Fatalf("settlement = %v, want settleAck — an untracked message cannot be retried safely", got)
	}
	if calls := mock.calls(); len(calls) != 1 {
		t.Fatalf("declines sent = %d, want exactly 1", len(calls))
	}
}

func TestIngress_ProvisionFailureIsNotTerminal(t *testing.T) {
	t.Parallel()
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	brk := &bindFailingBroker{Broker: dev}
	mock := newWAMock(t)
	a := newAdapterRigOn(t, brk, mock, "Builder")

	brk.setFailure(fmt.Errorf("solace broker: provision cooldown active: %w", broker.ErrQueueNotFound))
	a.startIngressDispatch(a.startIngressWorkers())
	t.Cleanup(a.stopIngressConsumer)

	time.Sleep(150 * time.Millisecond)
	if a.ingressDown.Load() {
		t.Fatal("a brief provisioning failure must not refuse webhooks; the cooldown outlasts the first backoff")
	}

	brk.setFailure(nil)
	value, msg := textMessage("wamid.recovered", "15551230000", "hi")
	value.Metadata.PhoneNumberID = "555111"
	if err := a.publishInbound(t.Context(), value, msg); err != nil {
		t.Fatalf("publishInbound after recovery: %v", err)
	}
}

func TestIngress_LatchClearsWithoutTraffic(t *testing.T) {
	t.Parallel()
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	brk := &bindFailingBroker{Broker: dev}
	mock := newWAMock(t)
	a := newAdapterRigOn(t, brk, mock, "Builder")
	a.ingressGrace = 20 * time.Millisecond
	a.receivePoll = 20 * time.Millisecond

	brk.setFailure(errors.New("provision failed"))
	a.startIngressDispatch(a.startIngressWorkers())
	t.Cleanup(a.stopIngressConsumer)

	if !waitFor(2*time.Second, func() bool { return a.ingressDown.Load() }) {
		t.Fatal("the consumer never latched despite continuous receive failures")
	}
	if problems := a.HealthProblems(); len(problems) != 1 || problems[0].ID != "ingress" {
		t.Fatalf("HealthProblems() = %+v while latched, want one problem with id \"ingress\"", problems)
	}

	brk.setFailure(nil)
	if !waitFor(3*time.Second, func() bool { return !a.ingressDown.Load() }) {
		t.Fatal("the latch never cleared — with no traffic to receive, nothing else can clear it, " +
			"so the pod would 503 every message for its lifetime while reporting Ready")
	}
	if problems := a.HealthProblems(); len(problems) != 0 {
		t.Errorf("HealthProblems() = %+v after recovery, want none — a cleared latch must not leave a stale problem behind", problems)
	}
}

func waitFor(budget time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestIngress_PanickingPayloadIsBoundedAndDeclined(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.logger = discardLogger()
	a.beforeProcess = func(string) { panic("poison payload") }

	value, msg := textMessage("wamid.poison", "15551230000", "hi")
	var deliveries int
	for i := 1; i <= 64; i++ {
		deliveries = i
		if a.deliver(value, msg) == settleAck {
			break
		}
	}
	if deliveries != stalledRetryBudget+1 {
		t.Fatalf("deliveries before the decline = %d, want %d — a panicking payload must be bounded",
			deliveries, stalledRetryBudget+1)
	}
	if calls := mock.calls(); len(calls) != 1 {
		t.Fatalf("declines sent = %d, want exactly 1 — the sender must be told once, not left in silence", len(calls))
	}
	if got := a.claimState(msg.ID); got != store.WhatsAppMessageDone {
		t.Errorf("claim state = %q, want %q so the row can age out", got, store.WhatsAppMessageDone)
	}
}

func TestIngress_AlternatingFailuresStillSettle(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "")

	var n int
	a.afterClaim = func(string) bool {
		n++
		if n%2 == 0 {
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
			a.core.SetScopeResolver(waScopeResolver{scopes: nil})
		} else { //nolint:revive // the two arms are the point
			a.core.SetScopeResolver(waScopeResolver{scopes: []string{"*"}})
			a.core.AgentRegistry().Remove("Builder")
		}
		return true
	}

	value, msg := textMessage("wamid.alternating", "15551230000", "hi")
	for i := 1; i <= maxTransientAttempts+5; i++ {
		if a.deliver(value, msg) == settleAck {
			if got := a.claimState(msg.ID); got != store.WhatsAppMessageDone {
				t.Fatalf("claim state = %q, want %q", got, store.WhatsAppMessageDone)
			}
			return
		}
	}
	t.Fatalf("alternating failures never settled in %d deliveries — neither per-kind budget is ever "+
		"reached when the kind keeps changing, so the ceiling is what has to stop it", maxTransientAttempts+5)
}

func TestIngress_StalledCeilingDeclinesWithoutProcessing(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.claims = &stubClaims{
		outcomes: []store.ClaimOutcome{store.ClaimAcquired},
		attempts: stalledRetryBudget,
		kind:     store.WhatsAppStalledFailure,
	}
	var processed bool
	a.beforeProcess = func(string) { processed = true }

	value, msg := textMessage("wamid.stalledceiling", "15551230000", "hi")
	if got := a.deliverInbound(t.Context(), &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}); got != settleAck {
		t.Fatalf("settlement = %v, want settleAck", got)
	}
	if processed {
		t.Fatal("the payload was processed again despite having spent its stalled budget — " +
			"a message that kills its consumer outright must be declined before it gets another go")
	}
	if len(mock.calls()) != 1 {
		t.Fatalf("declines sent = %d, want exactly 1", len(mock.calls()))
	}
	if ids := a.core.Tasks().AllByAgent("Builder"); len(ids) != 0 {
		t.Fatalf("a declined-unprocessed message must submit no task, got %d", len(ids))
	}
}

func TestIngress_ExhaustionLogLevelsMatchTheCause(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		attempts  int
		total     int
		kind      string
		wantLevel slog.Level
		wantText  string
	}{
		{
			name: "spent authorization budget", attempts: maxAuthzDenialAttempts, total: maxAuthzDenialAttempts,
			kind: "unauthorized", wantLevel: slog.LevelWarn, wantText: msgNotAuthorized,
		},
		{
			name: "absolute ceiling after mixed failures", attempts: 1, total: maxTransientAttempts,
			kind: "unauthorized", wantLevel: slog.LevelError, wantText: msgTemporaryFailure,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, "TestAgent")
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
			a.core.SetScopeResolver(waScopeResolver{scopes: nil})
			a.claims = &stubClaims{
				outcomes: []store.ClaimOutcome{store.ClaimAcquired},
				attempts: tt.attempts, total: tt.total, kind: tt.kind,
			}
			logs := &recordCollector{}
			a.logger = slog.New(logs)

			value, msg := textMessage("wamid.exhaust", "15551230000", "hi")
			if got := a.deliverInbound(t.Context(), &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}); got != settleAck {
				t.Fatalf("settlement = %v, want settleAck", got)
			}

			var found bool
			records := logs.all()
			for _, r := range records {
				if !strings.Contains(r.Message, "declining") {
					continue
				}
				found = true
				if r.Level != tt.wantLevel {
					t.Errorf("terminal line logged at %v, want %v", r.Level, tt.wantLevel)
				}
			}
			if !found {
				t.Fatalf("no terminal decline record among %d", len(records))
			}
			if !sentText(mock.calls(), tt.wantText) {
				t.Errorf("decline wording = %+v, want %q", mock.calls(), tt.wantText)
			}
		})
	}
}

type panickingClaims struct {
	stubClaims
	blown atomic.Bool
}

func (p *panickingClaims) Claim(context.Context, string, string, time.Duration) (store.WhatsAppClaim, error) {
	return store.WhatsAppClaim{Outcome: store.ClaimAcquired, Token: 1}, nil
}

func (p *panickingClaims) MarkDone(context.Context, string, string, int64) (bool, error) {
	if p.blown.CompareAndSwap(false, true) {
		panic("claim store exploded")
	}
	return true, nil
}

func TestIngress_PanicOutsideProcessingNacksOnly(t *testing.T) {
	claims := &panickingClaims{}
	r := newIngressRig(t, func(a *Adapter) { a.claims = claims })

	const sender = "15551230000"
	if got := r.post(textWebhook("wamid.outerpanic", sender, "boom")); got != http.StatusOK {
		t.Fatalf("first delivery status = %d, want 200", got)
	}
	if got := r.awaitAgentRequests(1, 200*time.Millisecond); len(got) != 1 {
		t.Fatalf("first message produced %v, want it processed before the settle panicked", got)
	}

	if got := r.post(textWebhook("wamid.afterpanic", sender, "still here")); got != http.StatusOK {
		t.Fatalf("second delivery status = %d, want 200", got)
	}
	got := r.awaitAgentRequests(1, 200*time.Millisecond)
	if len(got) != 1 || got[0] != "still here" {
		t.Fatalf("agent requests after the panic = %v, want [still here] — the worker did not survive", got)
	}
}

var oldWamidSeq atomic.Int64

func spoolOnOldQueue(t *testing.T, brk broker.Broker, from string, texts ...string) {
	t.Helper()
	old, err := brk.CreateQueue(a2a.WhatsAppIngressQueueName("test-ns", "test-gw"), broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create the old ingress queue: %v", err)
	}
	topic := ingressTopic("test-ns", "test-gw")
	if subErr := brk.Subscribe(old, topic); subErr != nil {
		t.Fatalf("subscribe the old ingress queue: %v", subErr)
	}
	for _, text := range texts {
		env := ingressEnvelope{Message: inboundMessage{From: from, ID: fmt.Sprintf("wamid.OLD-%d", oldWamidSeq.Add(1)), Type: "text"}}
		env.Message.Text.Body = text
		env.Context.Metadata.PhoneNumberID = "555111"
		payload, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		if err := brk.PublishGuaranteed(t.Context(), topic, &broker.Message{
			Topic: topic, Payload: payload, UserProperties: map[string]string{"senderId": from},
		}); err != nil {
			t.Fatalf("publish to the old ingress queue: %v", err)
		}
	}
}

func TestIngress_MovesTheOldQueueBeforeConsuming(t *testing.T) {
	const from = "15551230000"
	r := newIngressRigBefore(t, func(r *ingressRig) {
		spoolOnOldQueue(t, r.brk, from, "one", "two")
	})

	if got := r.awaitAgentRequests(2, 200*time.Millisecond); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("the old queue's messages produced %v, want [one two] in order", got)
	}
	oldName := a2a.WhatsAppIngressQueueName("test-ns", "test-gw")
	newName := a2a.WhatsAppIngressQueueNameV2("test-ns", "test-gw")
	waitQueueGone(t, r.brk, oldName)
	if !r.brk.HasQueue(newName) {
		t.Error("the new ingress queue does not exist")
	}
	if got := r.brk.MatchingQueues(ingressTopic("test-ns", "test-gw")); !slices.Equal(got, []string{newName}) {
		t.Errorf("the ingress topic reaches %v, want only the new queue", got)
	}
	if got := r.brk.MatchingQueues(a2a.WhatsAppDoorbellTopic("test-ns", "test-gw")); !slices.Equal(got, []string{newName}) {
		t.Errorf("the doorbell topic reaches %v, want the new queue", got)
	}

	if got := r.post(textWebhook("wamid.NEW-1", from, "three")); got != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", got)
	}
	if got := r.awaitAgentRequests(1, 200*time.Millisecond); !slices.Equal(got, []string{"three"}) {
		t.Fatalf("a message after the move produced %v, want [three]", got)
	}

	r.restart()
	if r.brk.HasQueue(oldName) {
		t.Error("a restart recreated the old ingress queue")
	}
	if got := r.post(textWebhook("wamid.NEW-2", from, "four")); got != http.StatusOK {
		t.Fatalf("webhook status after restart = %d, want 200", got)
	}
	if got := r.awaitAgentRequests(1, 200*time.Millisecond); !slices.Equal(got, []string{"four"}) {
		t.Fatalf("a message after the restart produced %v, want [four]", got)
	}
}

func TestIngress_UndeployDeletesBothQueueNames(t *testing.T) {
	r := newIngressRigBefore(t, func(r *ingressRig) {
		spoolOnOldQueue(t, r.brk, "15551230000")
	})
	oldName := a2a.WhatsAppIngressQueueName("test-ns", "test-gw")
	newName := a2a.WhatsAppIngressQueueNameV2("test-ns", "test-gw")
	if _, err := r.brk.CreateQueue(oldName, broker.QueueOptions{Named: true}); err != nil {
		t.Fatalf("recreate the old queue: %v", err)
	}
	if err := r.adapter.DeprovisionAdapterQueues(t.Context()); err != nil {
		t.Fatalf("DeprovisionAdapterQueues: %v", err)
	}
	if r.brk.HasQueue(oldName) || r.brk.HasQueue(newName) {
		t.Errorf("queues left after undeploy: old %v new %v", r.brk.HasQueue(oldName), r.brk.HasQueue(newName))
	}
}

func TestIngress_RecheckMovesAQueueRecreatedAfterTheFirstPass(t *testing.T) {
	const from = "15551230000"
	r := newIngressRigBefore(t, func(r *ingressRig) {
		spoolOnOldQueue(t, r.brk, from, "one")
	}, func(a *Adapter) { a.moveRecheck, a.moveIdle = time.Second, 100*time.Millisecond })
	oldName := a2a.WhatsAppIngressQueueName("test-ns", "test-gw")
	if got := r.awaitAgentRequests(1, 100*time.Millisecond); !slices.Equal(got, []string{"one"}) {
		t.Fatalf("the first pass produced %v, want [one]", got)
	}
	waitQueueGone(t, r.brk, oldName)

	spoolOnOldQueue(t, r.brk, from, "left behind")

	if got := r.awaitAgentRequests(1, 100*time.Millisecond); !slices.Equal(got, []string{"left behind"}) {
		t.Fatalf("the recreated queue's message produced %v, want [left behind]", got)
	}
	waitQueueGone(t, r.brk, oldName)
}

func TestIngress_DrainCutShortByItsBudgetIsFinishedByTheRecheck(t *testing.T) {
	const from = "15551230000"
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := newIngressRigBefore(t, func(r *ingressRig) {
		spoolOnOldQueue(t, r.brk, from, "slow one")
	}, func(a *Adapter) {
		a.moveBudget = 300 * time.Millisecond
		a.moveRecheck = time.Second
		a.moveIdle = 100 * time.Millisecond
		a.beforeProcess = func(wamid string) {
			if !strings.HasPrefix(wamid, "wamid.OLD-") {
				return
			}
			once.Do(func() { close(entered) })
			<-release
		}
	})
	oldName := a2a.WhatsAppIngressQueueName("test-ns", "test-gw")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the drained message never reached a worker")
	}
	time.Sleep(400 * time.Millisecond)
	if got := r.post(textWebhook("wamid.NEW-1", from, "after the budget")); got != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", got)
	}
	close(release)

	got := r.awaitAgentRequests(2, 300*time.Millisecond)
	slices.Sort(got)
	if !slices.Equal(got, []string{"after the budget", "slow one"}) {
		t.Fatalf("agent requests = %v, want the slow message once and the later one", got)
	}
	waitQueueGone(t, r.brk, oldName)
}

func TestIngress_ActivationWaitsForTheDefaultAgent(t *testing.T) {
	const from = "15551230000"
	r := newIngressRigBefore(t, func(r *ingressRig) {
		spoolOnOldQueue(t, r.brk, from, "before discovery")
	}, func(a *Adapter) {
		a.agentWait = 3 * time.Second
		a.moveIdle = 100 * time.Millisecond
		a.core.AgentRegistry().Remove("Builder")
	})
	time.Sleep(300 * time.Millisecond)
	if got := r.agentRequests(100 * time.Millisecond); len(got) != 0 {
		t.Fatalf("a message was processed before the agent was discovered: %v", got)
	}
	r.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	if got := r.awaitAgentRequests(1, 100*time.Millisecond); !slices.Equal(got, []string{"before discovery"}) {
		t.Fatalf("after discovery the message produced %v, want [before discovery]", got)
	}
}

func TestIngress_LostBindEndsTheDrain(t *testing.T) {
	const from = "15551230000"
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := newIngressRigBefore(t, func(r *ingressRig) {
		spoolOnOldQueue(t, r.brk, from, "slow one")
	}, func(a *Adapter) {
		a.moveBudget = time.Minute
		a.moveIdle = 100 * time.Millisecond
		a.beforeProcess = func(wamid string) {
			if !strings.HasPrefix(wamid, "wamid.OLD-") {
				return
			}
			once.Do(func() { close(entered) })
			<-release
		}
	})
	defer close(release)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the drained message never reached a worker")
	}

	r.adapter.onIngressBindState(broker.BindStandby)
	deadline := time.Now().Add(3 * time.Second)
	for {
		r.adapter.moveMu.Lock()
		running := r.adapter.moveCancel != nil
		r.adapter.moveMu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the drain still runs 3 s after the bind was lost; its budget is a minute")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !r.brk.HasQueue(a2a.WhatsAppIngressQueueName("test-ns", "test-gw")) {
		t.Error("the old queue was deleted by a drain the lost bind ended")
	}
}
