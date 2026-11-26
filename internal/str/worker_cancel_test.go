// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestRegisterAndCancelInvocation(t *testing.T) {
	w := NewWorker(&testServices{}, WorkerConfig{Namespace: "ns"})

	cancelled := make(chan struct{})
	cancel := func() { close(cancelled) }

	w.registerInvocationCancel("req-1", cancel)
	if !w.cancelInvocation("req-1") {
		t.Fatal("cancelInvocation returned false for registered ID")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancel func was not invoked")
	}

	if w.cancelInvocation("req-1") {
		t.Error("second cancelInvocation should return false")
	}
}

func TestCancelInvocation_UnknownIDIsNoop(t *testing.T) {
	w := NewWorker(&testServices{}, WorkerConfig{Namespace: "ns"})
	if w.cancelInvocation("missing") {
		t.Error("cancelInvocation should return false for unknown ID")
	}
	if w.cancelInvocation("") {
		t.Error("cancelInvocation should return false for empty ID")
	}
}

func TestUnregisterInvocationCancel_RemovesEntry(t *testing.T) {
	w := NewWorker(&testServices{}, WorkerConfig{Namespace: "ns"})
	w.registerInvocationCancel("req-1", func() {})
	w.unregisterInvocationCancel("req-1")
	if w.cancelInvocation("req-1") {
		t.Error("expected cancelInvocation to return false after unregister")
	}
}

func TestCancelDispatch_RoundTrip(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	invokeCtx, cancelInvoke := context.WithCancel(ctx)
	defer cancelInvoke()
	w.registerInvocationCancel("req-xyz", cancelInvoke)
	t.Cleanup(func() { w.unregisterInvocationCancel("req-xyz") })

	if err := brk.Publish(ctx, remotetool.CancelTopic("test-ns", "req-xyz"), &broker.Message{}); err != nil {
		t.Fatalf("publish cancel: %v", err)
	}

	select {
	case <-invokeCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("invocation context was not cancelled within 2s of cancel message")
	}
}
