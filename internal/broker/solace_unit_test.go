// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
)

func TestReconnectionStrategy(t *testing.T) {
	t.Parallel()

	t.Run("parameterized when not forever", func(t *testing.T) {
		s := SolaceBrokerConfig{ReconnectRetries: 60, ReconnectDelay: 5 * time.Second}.reconnectionStrategy()
		if got := s.GetRetries(); got != 60 {
			t.Errorf("GetRetries() = %d, want 60", got)
		}
		if got := s.GetRetryInterval(); got != 5*time.Second {
			t.Errorf("GetRetryInterval() = %v, want 5s", got)
		}
	})

	t.Run("forever uses -1 retries and configured interval", func(t *testing.T) {
		s := SolaceBrokerConfig{ReconnectForever: true, ReconnectRetries: 60, ReconnectDelay: 10 * time.Second}.reconnectionStrategy()
		if got := s.GetRetries(); got != -1 {
			t.Errorf("GetRetries() = %d, want -1 (forever)", got)
		}
		if got := s.GetRetryInterval(); got != 10*time.Second {
			t.Errorf("GetRetryInterval() = %v, want 10s", got)
		}
	})
}

func TestDurableQueueResource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		queueName string
		exclusive bool
	}{
		{"non-exclusive", "test-q", false},
		{"exclusive", "test-q", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := durableQueueResource(tt.queueName, tt.exclusive)
			if got == nil {
				t.Fatal("durableQueueResource returned nil")
			}
			if !got.IsDurable() {
				t.Errorf("expected durable queue")
			}
			if got.IsExclusivelyAccessible() != tt.exclusive {
				t.Errorf("IsExclusivelyAccessible() = %v, want %v",
					got.IsExclusivelyAccessible(), tt.exclusive)
			}
			if got.GetName() != tt.queueName {
				t.Errorf("GetName() = %q, want %q", got.GetName(), tt.queueName)
			}
		})
	}
}

func TestSolaceBrokerCreateQueuePropagatesOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts QueueOptions
	}{
		{"defaults", QueueOptions{}},
		{"named", QueueOptions{Named: true}},
		{"named-exclusive", QueueOptions{Named: true, Exclusive: true}},
		{"named-redelivery", QueueOptions{Named: true, MaxRedelivery: 3}},
		{"named-respect-ttl", QueueOptions{Named: true, RespectTTL: true}},
		{"named-quota", QueueOptions{Named: true, QuotaMB: 1024}},
		{"named-all", QueueOptions{Named: true, Exclusive: true, MaxRedelivery: 5, RespectTTL: true, QuotaMB: 256}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
			queueName := "unit-test-" + tt.name
			_, err := b.CreateQueue(queueName, tt.opts)
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			sr, ok := b.receivers[queueName]
			if !ok {
				t.Fatal("receiver not registered")
			}
			if sr.named != tt.opts.Named {
				t.Errorf("named = %v, want %v", sr.named, tt.opts.Named)
			}
			if sr.exclusive != tt.opts.Exclusive {
				t.Errorf("exclusive = %v, want %v", sr.exclusive, tt.opts.Exclusive)
			}
			if sr.maxRedelivery != tt.opts.MaxRedelivery {
				t.Errorf("maxRedelivery = %d, want %d", sr.maxRedelivery, tt.opts.MaxRedelivery)
			}
			if sr.respectTTL != tt.opts.RespectTTL {
				t.Errorf("respectTTL = %v, want %v", sr.respectTTL, tt.opts.RespectTTL)
			}
			if sr.quotaMB != tt.opts.QuotaMB {
				t.Errorf("quotaMB = %d, want %d", sr.quotaMB, tt.opts.QuotaMB)
			}
		})
	}
}

func TestSolaceBrokerCreateQueueIdempotentMismatchWarns(t *testing.T) {
	t.Parallel()

	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b := NewSolaceBroker(SolaceBrokerConfig{}, logger)

	queueName := "mismatch-q"
	if _, err := b.CreateQueue(queueName, QueueOptions{Named: true, Exclusive: true}); err != nil {
		t.Fatalf("first CreateQueue: %v", err)
	}
	if _, err := b.CreateQueue(queueName, QueueOptions{Named: true, Exclusive: false}); err != nil {
		t.Fatalf("second CreateQueue: %v", err)
	}

	sr := b.receivers[queueName]
	if !sr.exclusive {
		t.Error("expected exclusive=true to be preserved from first call")
	}

	out := buf.String()
	for _, want := range []string{
		"CreateQueue called twice with mismatched options",
		"queue=" + queueName,
		"existingExclusive=true",
		"requestedExclusive=false",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("warn log missing %q\nfull log:\n%s", want, out)
		}
	}
}

func TestSolaceBrokerCreateQueueMismatchWarnsOnBindState(t *testing.T) {
	t.Parallel()

	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b := NewSolaceBroker(SolaceBrokerConfig{}, logger)

	queueName := "mismatch-bind-state-q"
	if _, err := b.CreateQueue(queueName, QueueOptions{Named: true}); err != nil {
		t.Fatalf("first CreateQueue: %v", err)
	}
	if _, err := b.CreateQueue(queueName, QueueOptions{Named: true, OnBindState: func(BindState) {}}); err != nil {
		t.Fatalf("second CreateQueue: %v", err)
	}

	if b.receivers[queueName].onBindState != nil {
		t.Error("second call's OnBindState replaced the first call's nil")
	}
	out := buf.String()
	for _, want := range []string{
		"CreateQueue called twice with mismatched options",
		"existingOnBindState=false",
		"requestedOnBindState=true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("warn log missing %q\nfull log:\n%s", want, out)
		}
	}
}

func TestSolaceBrokerCreateQueueIdempotentSameOptsSilent(t *testing.T) {
	t.Parallel()

	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b := NewSolaceBroker(SolaceBrokerConfig{}, logger)

	opts := QueueOptions{Named: true, Exclusive: true}
	for range 3 {
		if _, err := b.CreateQueue("same-q", opts); err != nil {
			t.Fatalf("CreateQueue: %v", err)
		}
	}

	if buf.Len() != 0 {
		t.Errorf("expected no warn log for matching opts, got:\n%s", buf.String())
	}
}

func TestSolaceBrokerImplementsBroker(t *testing.T) {
	t.Parallel()
	var _ Broker = (*SolaceBroker)(nil)
	_ = context.Background()
}

func TestSolaceBrokerReconnectDefaultsAreLongTailed(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	if got, want := b.cfg.ReconnectRetries, defaultReconnectRetries; got != want {
		t.Errorf("ReconnectRetries default = %d, want %d", got, want)
	}
	if got, want := b.cfg.ReconnectDelay, defaultReconnectDelay; got != want {
		t.Errorf("ReconnectDelay default = %v, want %v", got, want)
	}

	override := NewSolaceBroker(SolaceBrokerConfig{
		ReconnectRetries: 7,
		ReconnectDelay:   2 * time.Second,
	}, testLogger())
	if override.cfg.ReconnectRetries != 7 {
		t.Errorf("expected caller override of ReconnectRetries to win, got %d", override.cfg.ReconnectRetries)
	}
	if override.cfg.ReconnectDelay != 2*time.Second {
		t.Errorf("expected caller override of ReconnectDelay to win, got %v", override.cfg.ReconnectDelay)
	}
}

func TestSolaceBrokerUnsubscribeUpdatesTopicTracking(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.receivers["test-q"] = &solaceReceiver{
		topics: []string{"a/b/c", "x/y/z", "p/q/r"},
		named:  false,
	}

	if err := b.Unsubscribe(&SimpleQueue{QName: "test-q"}, "x/y/z"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	sr := b.receivers["test-q"]
	want := []string{"a/b/c", "p/q/r"}
	if len(sr.topics) != len(want) {
		t.Fatalf("topics after unsubscribe = %v, want %v", sr.topics, want)
	}
	for i, got := range sr.topics {
		if got != want[i] {
			t.Errorf("topics[%d] = %q, want %q", i, got, want[i])
		}
	}

	if err := b.Unsubscribe(&SimpleQueue{QName: "test-q"}, "not-subscribed"); err != nil {
		t.Errorf("Unsubscribe of unknown topic returned error: %v", err)
	}
	if len(b.receivers["test-q"].topics) != 2 {
		t.Errorf("topics changed after no-op unsubscribe: %v", b.receivers["test-q"].topics)
	}
}

func TestSolaceBrokerSubscribeRecordsTopicOnce(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.receivers["test-q"] = &solaceReceiver{named: true}
	q := &SimpleQueue{QName: "test-q"}

	for range 2 {
		if err := b.Subscribe(q, "a/b/c"); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}
	if got := b.receivers["test-q"].topics; len(got) != 1 {
		t.Fatalf("topics after subscribing to one topic twice = %v, want it once", got)
	}

	if err := b.Unsubscribe(q, "a/b/c"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if got := b.receivers["test-q"].topics; len(got) != 0 {
		t.Errorf("topics after unsubscribe = %v, want none: the leftover is bound again on the next rebind", got)
	}
}

func TestSolaceBrokerRebuildIsNoopWhenDisconnected(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{BrokerURL: "tcp://unreachable.invalid:1"}, testLogger())
	b.disconnected.Store(true)

	done := make(chan struct{})
	go func() {
		b.rebuild()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("rebuild did not return promptly with disconnected=true")
	}

	if b.svc != nil {
		t.Error("expected b.svc to remain nil when disconnected=true")
	}
}

func TestSolaceBrokerRebuildSerializesConcurrentCalls(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{BrokerURL: "tcp://unreachable.invalid:1"}, testLogger())
	if !b.rebuilding.CompareAndSwap(false, true) {
		t.Fatal("rebuilding flag not in expected initial state")
	}
	defer b.rebuilding.Store(false)

	done := make(chan struct{})
	go func() {
		b.rebuild()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("rebuild did not return promptly when another rebuild was in progress")
	}

	if b.svc != nil {
		t.Error("expected b.svc to remain nil when another rebuild is in progress")
	}
	if !b.rebuildRequested.Load() {
		t.Error("expected rebuildRequested to be set when rebuild CAS fails — the active rebuild needs to see the signal to re-run")
	}
}

func TestSolaceBrokerRebuildOnceRestoresBindStateOnConnectFailure(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{BrokerURL: "tcp://unreachable.invalid:1"}, testLogger())
	t.Cleanup(func() {
		if b.retryTimer != nil {
			b.retryTimer.Stop()
		}
	})

	sr := &solaceReceiver{
		named:      true,
		started:    true,
		topics:     []string{"a/b/c"},
		msgChan:    make(chan *Message, 1),
		bindFailed: make(chan error, 1),
	}
	b.receivers["test-q"] = sr

	if shouldStop := b.rebuildOnce(); !shouldStop {
		t.Fatal("rebuildOnce() = false, want true after a failed connectLocked")
	}

	if !sr.started {
		t.Error("started flag not restored on the receiver after a failed rebuild")
	}
	if len(sr.topics) != 1 || sr.topics[0] != "a/b/c" {
		t.Errorf("topics = %v, want [a/b/c] restored after a failed rebuild", sr.topics)
	}
	select {
	case err := <-sr.bindFailed:
		if err == nil {
			t.Error("bindFailed carried a nil error")
		}
	default:
		t.Error("expected bindFailed to be signaled for a started receiver after a failed rebuild")
	}
	if b.retryTimer == nil {
		t.Error("expected a retry timer to be scheduled after a failed rebuild")
	}
}

func TestSolaceBrokerDisconnectCancelsRetryTimer(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	fired := make(chan struct{}, 1)
	b.mu.Lock()
	b.retryTimer = time.AfterFunc(50*time.Millisecond, func() {
		fired <- struct{}{}
	})
	b.mu.Unlock()

	if err := b.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	select {
	case <-fired:
		t.Fatal("retry timer fired after Disconnect — should have been cancelled")
	case <-time.After(150 * time.Millisecond):
	}

	b.mu.Lock()
	timerCleared := b.retryTimer == nil
	b.mu.Unlock()
	if !timerCleared {
		t.Error("expected b.retryTimer to be cleared after Disconnect")
	}
}

func TestSolaceBrokerRebindIsNoopWhenDisconnected(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{BrokerURL: "tcp://unreachable.invalid:1"}, testLogger())
	b.disconnected.Store(true)

	done := make(chan struct{})
	go func() {
		b.rebindReceivers()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("rebindReceivers did not return promptly with disconnected=true")
	}

	if b.rebinding.Load() {
		t.Error("rebinding flag should not be left set when disconnected short-circuited the call")
	}
}

func TestSolaceBrokerRebindSkipsWhenRebuilding(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{BrokerURL: "tcp://unreachable.invalid:1"}, testLogger())
	if !b.rebuilding.CompareAndSwap(false, true) {
		t.Fatal("rebuilding flag not in expected initial state")
	}
	defer b.rebuilding.Store(false)

	done := make(chan struct{})
	go func() {
		b.rebindReceivers()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("rebindReceivers did not return promptly with rebuilding=true")
	}

	if b.rebinding.Load() {
		t.Error("rebinding flag should not be left set when rebuild-in-progress short-circuited the call")
	}
}

func TestSolaceBrokerRebindSerializesConcurrentCalls(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{BrokerURL: "tcp://unreachable.invalid:1"}, testLogger())
	if !b.rebinding.CompareAndSwap(false, true) {
		t.Fatal("rebinding flag not in expected initial state")
	}
	defer b.rebinding.Store(false)

	done := make(chan struct{})
	go func() {
		b.rebindReceivers()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("rebindReceivers did not return promptly when another rebind was in progress")
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *safeBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func TestSolaceBroker_OnReconnectRegistersCallback(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{BrokerURL: "tcp://unreachable.invalid:1"}, testLogger())

	called := make(chan struct{}, 1)
	b.OnReconnect(func() { called <- struct{}{} })

	b.reconnectFnsMu.Lock()
	fns := append([]func(){}, b.reconnectFns...)
	b.reconnectFnsMu.Unlock()

	if len(fns) != 1 {
		t.Fatalf("expected 1 registered callback, got %d", len(fns))
	}
	fns[0]()

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("registered reconnect callback was not invoked")
	}
}

func TestReceiverQueueResource_TemporaryReturnsAnonymous(t *testing.T) {
	t.Parallel()
	sr := &solaceReceiver{temporary: true}
	q := receiverQueueResource(sr, "any-name")
	if q == nil {
		t.Fatal("expected non-nil resource")
	}
	if q.IsDurable() {
		t.Error("temporary receiver queue must be non-durable")
	}
	if !q.IsExclusivelyAccessible() {
		t.Error("temporary receiver queue must be exclusive (one binder)")
	}
}

func TestReceiverQueueResource_NamedDurableExclusive(t *testing.T) {
	t.Parallel()
	sr := &solaceReceiver{named: true, exclusive: true}
	q := receiverQueueResource(sr, "my-q")
	if !q.IsDurable() {
		t.Error("named exclusive queue must be durable")
	}
	if !q.IsExclusivelyAccessible() {
		t.Error("named exclusive queue must be exclusive")
	}
	if q.GetName() != "my-q" {
		t.Errorf("GetName() = %q, want %q", q.GetName(), "my-q")
	}
}

func TestReceiverQueueResource_NamedDurableNonExclusive(t *testing.T) {
	t.Parallel()
	sr := &solaceReceiver{named: true, exclusive: false}
	q := receiverQueueResource(sr, "shared-q")
	if !q.IsDurable() {
		t.Error("named non-exclusive queue must be durable")
	}
	if q.IsExclusivelyAccessible() {
		t.Error("named non-exclusive queue must not be exclusive")
	}
}

func TestSolaceBrokerCreateTemporaryQueue(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	q, err := b.CreateTemporaryQueue()
	if err != nil {
		t.Fatalf("CreateTemporaryQueue: %v", err)
	}
	name := q.Name()
	if name == "" {
		t.Fatal("queue name must not be empty")
	}

	b.mu.RLock()
	sr, ok := b.receivers[name]
	b.mu.RUnlock()
	if !ok {
		t.Fatalf("receiver %q not registered after CreateTemporaryQueue", name)
	}
	if !sr.temporary {
		t.Error("receiver.temporary should be true")
	}
	if !sr.named {
		t.Error("receiver.named should be true")
	}
	if cap(sr.msgChan) != DefaultMessageChannelSize {
		t.Errorf("msgChan cap = %d, want %d", cap(sr.msgChan), DefaultMessageChannelSize)
	}
}

func TestSolaceBrokerDeleteQueueRemovesReceiver(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.receivers["del-q"] = &solaceReceiver{
		msgChan: make(chan *Message, 1),
	}

	if err := b.DeleteQueue("del-q"); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	if _, ok := b.receivers["del-q"]; ok {
		t.Error("receiver still present after DeleteQueue")
	}

	if err := b.DeleteQueue("does-not-exist"); err != nil {
		t.Errorf("DeleteQueue on missing name should be a no-op, got: %v", err)
	}
}

func TestSolaceBrokerDeprovisionQueueSvcNil(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.receivers["dep-q"] = &solaceReceiver{
		msgChan: make(chan *Message, 1),
	}

	if err := b.DeprovisionQueue("dep-q"); err != nil {
		t.Fatalf("DeprovisionQueue with svc=nil: %v", err)
	}
	if _, ok := b.receivers["dep-q"]; ok {
		t.Error("receiver still present after DeprovisionQueue")
	}

	if err := b.DeprovisionQueue("does-not-exist"); err != nil {
		t.Errorf("DeprovisionQueue on missing name should be a no-op, got: %v", err)
	}
}

func TestSolaceBrokerRequestReplyInboxNotConnected(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	_, err := b.RequestReplyInbox(t.Context(), "some/topic", &Message{Payload: []byte("hi")}, time.Second)
	if err == nil {
		t.Fatal("expected error when svc is nil")
	}
	const want = "solace broker: not connected"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestSolaceBrokerPublishNotConnected(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	err := b.Publish(t.Context(), "a/topic", &Message{Payload: []byte("x")})
	if err == nil || err.Error() != "solace broker: not connected" {
		t.Errorf("Publish on unconnected broker: got %v, want 'not connected'", err)
	}
}

func TestSolaceBrokerPublishGuaranteedNotConnected(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	err := b.PublishGuaranteed(t.Context(), "a/topic", &Message{Payload: []byte("x")})
	if err == nil || err.Error() != "solace broker: not connected" {
		t.Errorf("PublishGuaranteed on unconnected broker: got %v, want 'not connected'", err)
	}
}

func TestResolveTrustStorePath_YAMLPathWins(t *testing.T) {
	t.Setenv("SOLACE_TLS_TRUST_STORE_DIR", "/should/not/be/used")
	path, cleanup, err := resolveTrustStorePath("/explicit/path", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/explicit/path" {
		t.Errorf("got %q, want /explicit/path", path)
	}
	if cleanup != nil {
		t.Error("expected nil cleanup for explicit path")
	}
}

func TestResolveTrustStorePath_EnvVarSolace(t *testing.T) {
	t.Setenv("SOLACE_TLS_TRUST_STORE_DIR", "/from/solace/env")
	t.Setenv("TRUST_STORE", "/should/not/be/used")
	path, cleanup, err := resolveTrustStorePath("", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/from/solace/env" {
		t.Errorf("got %q, want /from/solace/env", path)
	}
	if cleanup != nil {
		t.Error("expected nil cleanup for env-var path")
	}
}

func TestResolveTrustStorePath_EnvVarTrustStore(t *testing.T) {
	t.Setenv("SOLACE_TLS_TRUST_STORE_DIR", "")
	t.Setenv("TRUST_STORE", "/from/trust-store/env")
	path, cleanup, err := resolveTrustStorePath("", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/from/trust-store/env" {
		t.Errorf("got %q, want /from/trust-store/env", path)
	}
	if cleanup != nil {
		t.Error("expected nil cleanup for env-var path")
	}
}

func TestResolveTrustStorePath_PlatformDefault(t *testing.T) {
	t.Setenv("SOLACE_TLS_TRUST_STORE_DIR", "")
	t.Setenv("TRUST_STORE", "")

	if runtime.GOOS == "darwin" {
		t.Skip("platform-default fallback is not reached on macOS — bundled certs take priority")
	}

	path, cleanup, err := resolveTrustStorePath("", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != platformDefaultTrustStorePath() {
		t.Errorf("got %q, want %q", path, platformDefaultTrustStorePath())
	}
	if cleanup != nil {
		t.Error("expected nil cleanup for platform-default path")
	}
}

func TestMaterializeBundledCerts(t *testing.T) {
	t.Parallel()
	dir, err := materializeBundledCerts()
	if err != nil {
		t.Fatalf("materializeBundledCerts: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if dir == "" {
		t.Fatal("expected non-empty directory path")
	}

	pemPath := dir + "/cacert.pem"
	info, err := os.Stat(pemPath)
	if err != nil {
		t.Fatalf("cacert.pem not written: %v", err)
	}
	if info.Size() == 0 {
		t.Error("cacert.pem is empty")
	}
}

func TestSolaceBrokerDisconnectCallsCleanupCerts(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())

	called := false
	b.cleanupCerts = func() { called = true }

	if err := b.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	if !called {
		t.Error("cleanupCerts was not called by Disconnect")
	}
}

func TestSolaceBrokerReceiveClosedChannel(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	ch := make(chan *Message, 1)
	close(ch)
	b.receivers["closed-q"] = &solaceReceiver{msgChan: ch}

	_, err := b.Receive(t.Context(), &SimpleQueue{QName: "closed-q"})
	if err == nil {
		t.Fatal("expected error for closed channel")
	}
	const want = `solace broker: queue "closed-q" closed: queue not found`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, ErrQueueNotFound) {
		t.Errorf("expected errors.Is(err, ErrQueueNotFound) to hold for a closed receive channel, got %v", err)
	}
}

func TestSolaceBrokerReceiveContextCancelled(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.receivers["ctx-q"] = &solaceReceiver{msgChan: make(chan *Message, 1)}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := b.Receive(ctx, &SimpleQueue{QName: "ctx-q"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestPlatformDefaultTrustStorePath(t *testing.T) {
	t.Parallel()
	got := platformDefaultTrustStorePath()
	if got == "" {
		t.Error("expected non-empty path")
	}
	switch runtime.GOOS {
	case "darwin":
		if got != "/etc/ssl" {
			t.Errorf("darwin: got %q, want /etc/ssl", got)
		}
	default:
		if got != "/etc/ssl/certs" {
			t.Errorf("non-darwin: got %q, want /etc/ssl/certs", got)
		}
	}
}

func TestResolveTrustStorePath_DarwinBundledCerts(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-bundled-certs path only reachable on macOS")
	}
	t.Setenv("SOLACE_TLS_TRUST_STORE_DIR", "")
	t.Setenv("TRUST_STORE", "")

	path, cleanup, err := resolveTrustStorePath("", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path for bundled certs")
	}
	if cleanup == nil {
		t.Error("expected non-nil cleanup for bundled certs")
	}
	cleanup()
}

func TestMaterializeBundledCerts_WriteFileFailure(t *testing.T) {
	bundle := bundledCertsPEM()
	sum := sha256.Sum256(bundle)
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("solace-truststore-%x", sum[:8]))
	pemPath := filepath.Join(dir, "cacert.pem")

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
		_ = os.RemoveAll(dir)
	})

	if _, err := os.Stat(pemPath); err == nil {
		t.Skip("pem already exists in read-only dir; skipping write-failure test")
	}

	_, err := materializeBundledCerts()
	if err == nil {
		t.Fatal("expected error when dir is read-only")
	}
	if !strings.Contains(err.Error(), "write CA bundle") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBindStateFromReceiverState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state solace.ReceiverState
		want  BindState
	}{
		{"active", solace.ReceiverActive, BindActive},
		{"passive", solace.ReceiverPassive, BindStandby},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := bindStateFromReceiverState(tt.state); got != tt.want {
				t.Errorf("bindStateFromReceiverState(%v) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestSolaceBrokerCreateQueueStoresOnBindState(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())

	if _, err := b.CreateQueue("with-callback", QueueOptions{Named: true, OnBindState: func(BindState) {}}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if b.receivers["with-callback"].onBindState == nil {
		t.Error("onBindState not propagated to the receiver")
	}

	if _, err := b.CreateQueue("without-callback", QueueOptions{Named: true}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if b.receivers["without-callback"].onBindState != nil {
		t.Error("onBindState set when the option was nil")
	}
}

func TestWithBindStateListener(t *testing.T) {
	t.Parallel()

	t.Run("nil callback registers nothing", func(t *testing.T) {
		t.Parallel()
		f := &fakeReceiverBuilder{}
		withBindStateListener(f, nil)
		if f.listener != nil {
			t.Error("listener registered for a nil callback")
		}
	})

	t.Run("registered listener maps and forwards", func(t *testing.T) {
		t.Parallel()
		f := &fakeReceiverBuilder{}
		var got []BindState
		withBindStateListener(f, func(s BindState) { got = append(got, s) })
		if f.listener == nil {
			t.Fatal("listener not registered")
		}
		f.listener(solace.ReceiverPassive, solace.ReceiverActive, time.Now())
		f.listener(solace.ReceiverActive, solace.ReceiverPassive, time.Now())
		if len(got) != 2 || got[0] != BindActive || got[1] != BindStandby {
			t.Fatalf("forwarded %v, want [active standby]", got)
		}
	})
}
