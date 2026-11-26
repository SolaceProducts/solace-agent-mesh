// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
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

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type settleRecordingBroker struct {
	*broker.DevBroker

	mu         sync.Mutex
	acked      []string
	nacked     []string
	publishErr error
	receiveErr error
}

func newSettleRecordingBroker(t *testing.T) *settleRecordingBroker {
	t.Helper()
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	return &settleRecordingBroker{DevBroker: dev}
}

func (b *settleRecordingBroker) record(into *[]string, payload string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*into = append(*into, payload)
}

func (b *settleRecordingBroker) settled() (acked, nacked []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.acked), slices.Clone(b.nacked)
}

func (b *settleRecordingBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	b.mu.Lock()
	receiveErr := b.receiveErr
	b.mu.Unlock()
	if receiveErr != nil {
		return nil, receiveErr
	}
	msg, err := b.DevBroker.Receive(ctx, q)
	if err != nil || msg == nil {
		return msg, err
	}
	payload := string(msg.Payload)
	return broker.NewDeliveredMessage(msg.ID, msg.Topic, msg.Payload,
		func() { b.record(&b.acked, payload) },
		func() { b.record(&b.nacked, payload) }), nil
}

func (b *settleRecordingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	b.mu.Lock()
	publishErr := b.publishErr
	b.mu.Unlock()
	if publishErr != nil {
		return publishErr
	}
	return b.DevBroker.PublishGuaranteed(ctx, topic, msg)
}

func openTestSpool(t *testing.T, brk broker.Broker) (*brokerSpool, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	spool, err := openBrokerSpool(ctx, brk, "ns", "gw-spool", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return spool, cancel
}

func spoolOne(t *testing.T, spool *brokerSpool, payload string) pulledMessage {
	t.Helper()
	if err := spool.Publish(t.Context(), []byte(payload), "users/1"); err != nil {
		t.Fatal(err)
	}
	msgs, err := spool.Pull(shortCtx(t), 1)
	if err != nil || len(msgs) != 1 || string(msgs[0].Data) != payload {
		t.Fatalf("Pull = (%v, %v), want %q", msgs, err, payload)
	}
	return msgs[0]
}

func TestBrokerSpool_AckAndImmediateNackSettleTheBrokerMessage(t *testing.T) {
	brk := newSettleRecordingBroker(t)
	spool, _ := openTestSpool(t, brk)

	first := spoolOne(t, spool, `{"n":1}`)
	if err := spool.Ack(t.Context(), []string{first.AckID, "unknown"}); err != nil {
		t.Fatal(err)
	}
	second := spoolOne(t, spool, `{"n":2}`)
	if err := spool.Nack(t.Context(), []string{second.AckID}, 0); err != nil {
		t.Fatal(err)
	}
	if acked, nacked := brk.settled(); !slices.Equal(acked, []string{`{"n":1}`}) || !slices.Equal(nacked, []string{`{"n":2}`}) {
		t.Errorf("settled acked=%v nacked=%v, want the first acked and the second nacked", acked, nacked)
	}
	if err := spool.Ack(t.Context(), []string{first.AckID}); err != nil {
		t.Fatal(err)
	}
	if acked, _ := brk.settled(); len(acked) != 1 {
		t.Errorf("a second ack of one id settled the message again: %v", acked)
	}
}

func TestBrokerSpool_DelayedNackDoesNotBlockTheWorker(t *testing.T) {
	brk := newSettleRecordingBroker(t)
	spool, _ := openTestSpool(t, brk)
	msg := spoolOne(t, spool, `{"n":1}`)

	const delay = 200 * time.Millisecond
	start := time.Now()
	if err := spool.Nack(t.Context(), []string{msg.AckID}, delay); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited >= delay {
		t.Fatalf("Nack blocked the worker for %v", waited)
	}
	if _, nacked := brk.settled(); len(nacked) != 0 {
		t.Fatal("the message was nacked before its delay")
	}
	spool.waitDelayed()
	if _, nacked := brk.settled(); !slices.Equal(nacked, []string{`{"n":1}`}) {
		t.Errorf("nacked = %v, want the message nacked once its delay ended", nacked)
	}
}

func TestBrokerSpool_StoppingEndsADelayedNackAtOnce(t *testing.T) {
	brk := newSettleRecordingBroker(t)
	spool, stop := openTestSpool(t, brk)
	msg := spoolOne(t, spool, `{"n":1}`)
	watchdog := time.AfterFunc(5*time.Second, stop)
	defer watchdog.Stop()
	start := time.Now()
	if err := spool.Nack(t.Context(), []string{msg.AckID}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("Nack blocked the worker for %v of its retry delay", waited)
	}
	stop()
	done := make(chan struct{})
	go func() { spool.waitDelayed(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stopping spool kept waiting out a delayed nack")
	}
	if _, nacked := brk.settled(); len(nacked) != 1 {
		t.Errorf("nacked = %v, want the held message released for a successor", nacked)
	}
}

func TestBrokerSpool_BindProbeKeepsAWaitingMessage(t *testing.T) {
	brk := newSettleRecordingBroker(t)
	predecessor, _ := openTestSpool(t, brk)
	if err := predecessor.Publish(t.Context(), []byte(`{"backlog":true}`), "users/1"); err != nil {
		t.Fatal(err)
	}
	successor, _ := openTestSpool(t, brk)
	msgs, err := successor.Pull(shortCtx(t), 1)
	if err != nil || len(msgs) != 1 || string(msgs[0].Data) != `{"backlog":true}` {
		t.Fatalf("first Pull = (%v, %v), want the backlog message", msgs, err)
	}
}

func TestBrokerSpool_ReceiveFailureRefusesNewEvents(t *testing.T) {
	brk := newSettleRecordingBroker(t)
	spool, _ := openTestSpool(t, brk)
	brk.mu.Lock()
	brk.receiveErr = errors.New("flow unbound")
	brk.mu.Unlock()
	if _, err := spool.Pull(shortCtx(t), 1); err == nil {
		t.Fatal("Pull must report a receive failure")
	}
	if err := spool.Publish(t.Context(), []byte(`{}`), "users/1"); err == nil {
		t.Error("a spool nothing drains must refuse new events")
	}
}

func newHTTPSAdapterOn(t *testing.T, brk broker.Broker) *Adapter {
	t.Helper()
	a := NewAdapter(nil)
	a.logger = slog.New(slog.DiscardHandler)
	a.ctx, a.cancel = context.WithCancel(context.Background())
	t.Cleanup(a.cancel)
	spool, err := openBrokerSpool(a.ctx, brk, "ns", "gw-https", a.logger)
	if err != nil {
		t.Fatal(err)
	}
	a.spool, a.sub = spool, spool
	a.verifier = stubVerifier{accept: "good-token"}
	return a
}

func TestEventsHandler_PublishFailureAnswers503(t *testing.T) {
	brk := newSettleRecordingBroker(t)
	brk.publishErr = errors.New("spool full")
	a := newHTTPSAdapterOn(t, brk)
	if code := postEvent(t, a, "good-token", dmEventJSON); code != http.StatusServiceUnavailable {
		t.Errorf("publish failure = %d, want 503", code)
	}
}

func TestEventsHandler_OversizedOrUnreadableBodyIsRefusedVisibly(t *testing.T) {
	a := newHTTPSAdapter(t)
	a.gatewayID = "gw-https"
	logs := &logCapture{}
	a.logger = slog.New(logs)
	metrics := &countingMetrics{}
	a.metrics = metrics

	body := `{"type":"MESSAGE","pad":"` + strings.Repeat("x", maxEventBytes) + `"}`
	if code := postEvent(t, a, "good-token", body); code != http.StatusBadRequest {
		t.Errorf("oversized body = %d, want 400", code)
	}
	req := httptest.NewRequest(http.MethodPost, eventsPath, io.NopCloser(failingReader{}))
	req.Header.Set("Authorization", "Bearer good-token")
	rec := httptest.NewRecorder()
	a.eventsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unreadable body = %d, want 400", rec.Code)
	}

	if msgs, _ := a.spool.Pull(shortCtx(t), 1); len(msgs) != 0 {
		t.Error("a refused body reached the spool")
	}
	if n := logs.count(slog.LevelWarn, "google chat HTTPS event could not be decoded"); n != 1 {
		t.Errorf("refusal warnings = %d, want 1 (throttled)", n)
	}
	if n := metrics.count(metricAckedWithoutTask, "gatewayId", "gw-https", "reason", "undecodable"); n != 2 {
		t.Errorf("refusal count = %d, want 2", n)
	}
}
