// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type failingBroker struct {
	broker.Broker
	receiveCount atomic.Int64
}

func (f *failingBroker) Receive(ctx context.Context, _ broker.Queue) (*broker.Message, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	f.receiveCount.Add(1)
	return nil, broker.ErrQueueNotFound
}

func TestDispatchLoop_ExitsOnQueueNotFound(t *testing.T) {
	inner := broker.NewDevBroker()
	if err := inner.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inner.Disconnect(context.Background()) }) //nolint:contextcheck

	fb := &failingBroker{Broker: inner}
	svc := &testServices{brk: fb}

	manifestPath := writeTestManifest(t)
	w := newTestWorker(t, svc, manifestPath)

	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.dispatchLoop(t.Context(), t.Context())
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatchLoop did not exit after ErrQueueNotFound within 2s")
	}

	count := fb.receiveCount.Load()
	if count > 5 {
		t.Errorf("dispatchLoop called Receive %d times before exiting; expected ≤5 (must exit on ErrQueueNotFound)", count)
	}
}

func TestWorkerStop_PreservesQueueForSuccessor(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	invokeTopic := remotetool.InvokeTopic("test-ns", "echo_tool")
	if err := brk.Publish(t.Context(), invokeTopic, &broker.Message{Payload: []byte(`{"probe":true}`)}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if err := w.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	w2 := newTestWorker(t, svc, manifestPath)
	w2.cfg.WorkerID = w.cfg.WorkerID
	if err := w2.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := w2.syncSubscriptions(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.DeleteQueue(w2.queue.Name()) })

	if err := brk.Publish(t.Context(), invokeTopic, &broker.Message{Payload: []byte(`{"successor":true}`)}); err != nil {
		t.Fatalf("Publish to successor: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, w2.queue)
	if err != nil {
		t.Fatalf("successor Receive: %v — queue was likely removed by Stop, violating rolling-upgrade contract", err)
	}
	if msg == nil {
		t.Fatal("no message on successor queue — spooled messages lost after Stop")
	}
	msg.Ack()
}
