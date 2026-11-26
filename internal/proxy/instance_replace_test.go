// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type replaceHeldAdapter struct {
	switchableAdapter
	release chan struct{}
}

func (a *replaceHeldAdapter) ForwardRequest(ctx context.Context, tc *TaskContext, _ *a2a.Request) (<-chan ResponseEvent, error) {
	a.forwarded.Add(1)
	ch := make(chan ResponseEvent, 1)
	go func() {
		defer close(ch)
		select {
		case <-a.release:
			ch <- ResponseEvent{Task: &a2a.Task{ID: tc.TaskID, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}}
		case <-ctx.Done():
			ch <- ResponseEvent{Error: errors.New("downstream: " + ctx.Err().Error())}
		}
	}()
	return ch, nil
}

func replaceRequest(t *testing.T, id string) (*broker.Message, *atomic.Int32) {
	t.Helper()
	req := &a2a.Request{Method: "message/send", ID: id, Params: map[string]any{"message": map[string]any{
		"contextId": "s", "parts": []any{map[string]any{"kind": "text", "text": "hi"}},
	}}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	acks := &atomic.Int32{}
	msg := broker.NewDeliveredMessage("m-"+id, "topic", data, func() { acks.Add(1) }, nil)
	msg.UserProperties = map[string]string{"replyTo": "reply/topic", "a2aStatusTopic": "status/topic", "userId": "user1"}
	return msg, acks
}

func startReplaceProxy(t *testing.T) (*ProxyInstance, *replaceHeldAdapter, context.Context, broker.Queue) {
	t.Helper()
	p := newBrokerProxy(t)
	adapter := &replaceHeldAdapter{release: make(chan struct{})}
	p.adapter = adapter
	runCtx, cancel := context.WithCancel(context.Background())
	p.cancelFn = cancel
	t.Cleanup(cancel)
	replyQ, err := p.svc.Broker().CreateQueue("reply-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.svc.Broker().Subscribe(replyQ, "reply/topic"); err != nil {
		t.Fatal(err)
	}
	return p, adapter, runCtx, replyQ
}

func replies(t *testing.T, brk broker.Broker, q broker.Queue) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	n := 0
	for {
		m, err := brk.Receive(ctx, q)
		if err != nil || m == nil {
			return n
		}
		n++
	}
}

func TestStopForReplaceLeavesRunningRequestForTheNewCopy(t *testing.T) {
	p, adapter, runCtx, replyQ := startReplaceProxy(t)
	p.replaceDrain = 200 * time.Millisecond
	msg, acks := replaceRequest(t, "running")
	p.routeMessage(runCtx, msg)
	waitUntilTrue(t, func() bool { return adapter.forwarded.Load() == 1 })

	stopped := make(chan struct{})
	go func() {
		_ = p.StopForReplace(context.Background())
		close(stopped)
	}()
	waitUntilTrue(t, p.intakeStopped.Load)
	late, lateAcks := replaceRequest(t, "late")
	p.routeMessage(runCtx, late)
	<-stopped
	waitUntilTrue(t, func() bool { return p.requestsInFlight.Load() == 0 })

	if got := acks.Load(); got != 0 {
		t.Errorf("the cut request was acknowledged %d times; it must stay for the new copy", got)
	}
	if got := lateAcks.Load(); got != 0 || adapter.forwarded.Load() != 1 {
		t.Errorf("a request arriving during the drain was taken (acks=%d, forwarded=%d)", got, adapter.forwarded.Load())
	}
	if got := replies(t, p.svc.Broker(), replyQ); got != 0 {
		t.Errorf("%d replies published for a request the replace cut, want none", got)
	}
}

func TestStopForReplaceLetsAFinishingRequestAnswer(t *testing.T) {
	p, adapter, runCtx, replyQ := startReplaceProxy(t)
	p.replaceDrain = 10 * time.Second
	msg, acks := replaceRequest(t, "finishing")
	p.routeMessage(runCtx, msg)
	waitUntilTrue(t, func() bool { return adapter.forwarded.Load() == 1 })
	go func() {
		for !p.intakeStopped.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		close(adapter.release)
	}()

	start := time.Now()
	if err := p.StopForReplace(context.Background()); err != nil {
		t.Fatalf("StopForReplace: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("StopForReplace took %s, want it to return once the request finished", elapsed)
	}
	if got := acks.Load(); got != 1 {
		t.Errorf("the finished request was acknowledged %d times, want 1", got)
	}
	if got := replies(t, p.svc.Broker(), replyQ); got != 1 {
		t.Errorf("%d replies published, want the finished request's one", got)
	}
}

func waitUntilTrue(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
