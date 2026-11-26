// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSolaceReceiverSurvivesExternalDeprovision(t *testing.T) {
	cfg := solaceBrokerConfig(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	queueName := fmt.Sprintf("sam-go/repro/deprovision-rebind/%d", suffix)
	topic := fmt.Sprintf("sam-go/repro/deprovision-rebind/%d/request", suffix)

	podOld := NewSolaceBroker(cfg, testLogger())
	must(t, podOld.Connect(ctx))
	defer func() { _ = podOld.Disconnect(ctx) }()

	podNew := NewSolaceBroker(cfg, testLogger())
	must(t, podNew.Connect(ctx))
	defer func() { _ = podNew.Disconnect(ctx) }()
	defer func() { _ = podNew.DeprovisionQueue(queueName) }()

	q, err := podOld.CreateQueue(queueName, QueueOptions{Named: true})
	must(t, err)
	must(t, podOld.Subscribe(q, topic))

	recvCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	go func() {
		time.Sleep(2 * time.Second)
		_ = podNew.Publish(ctx, topic, &Message{Payload: []byte("sanity")})
	}()
	msg, err := podOld.Receive(recvCtx, q)
	cancel()
	must(t, err)
	if msg == nil || string(msg.Payload) != "sanity" {
		t.Fatalf("sanity message not received; got %v", msg)
	}
	msg.Ack()

	must(t, podNew.DeprovisionQueue(queueName))

	q2, err := podNew.CreateQueue(queueName, QueueOptions{Named: true})
	must(t, err)
	must(t, podNew.Subscribe(q2, topic))
	bindCtx, cancelBind := context.WithTimeout(ctx, 2*time.Second)
	_, _ = podNew.Receive(bindCtx, q2)
	cancelBind()
	must(t, podNew.DeleteQueue(queueName))

	time.Sleep(2 * time.Second)
	must(t, podNew.Publish(ctx, topic, &Message{Payload: []byte("task-after-churn")}))

	wedgeCtx, cancelWedge := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWedge()
	msg, err = podOld.Receive(wedgeCtx, q)
	switch {
	case err == nil && msg != nil:
		if got := string(msg.Payload); got != "task-after-churn" {
			t.Fatalf("received unexpected payload %q", got)
		}
		msg.Ack()
	case err != nil && wedgeCtx.Err() == nil:
		t.Fatalf("Receive surfaced an error instead of recovering: %v\n"+
			"Better than wedging silently, but the receiver should re-bind after external deprovision.", err)
	default:
		t.Fatalf("receiver silently wedged: Receive returned neither a message nor an error after the queue was "+
			"deprovisioned and re-created underneath it. In a deployment this means: "+
			"flow dead, msgChan open, no logs, no CPU, /ready green, every task times out with "+
			"%q.", "no agent activity")
	}
}
