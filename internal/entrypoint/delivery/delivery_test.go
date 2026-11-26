// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package delivery

import "testing"

func TestReleaseWithoutHoldsAcknowledges(t *testing.T) {
	acks := 0
	ctx, d := Attach(t.Context(), "m1", true, func() { acks++ })
	if got := From(ctx); got != d || got.BrokerMessageID != "m1" || !got.Redelivered {
		t.Fatalf("From(ctx) = %+v, want the attached delivery", got)
	}
	d.Release()
	if acks != 1 {
		t.Fatalf("acks = %d, want 1", acks)
	}
}

func TestAcknowledgesAfterTheLastHold(t *testing.T) {
	for _, order := range []string{"worker first", "hold first"} {
		t.Run(order, func(t *testing.T) {
			acks := 0
			_, d := Attach(t.Context(), "", false, func() { acks++ })
			release := d.Hold()
			if order == "worker first" {
				d.Release()
				if acks != 0 {
					t.Fatal("acknowledged while a hold was taken")
				}
				release()
			} else {
				release()
				if acks != 0 {
					t.Fatal("acknowledged while the worker's hold was taken")
				}
				d.Release()
			}
			if acks != 1 {
				t.Fatalf("acks = %d, want 1", acks)
			}
		})
	}
}

func TestHoldReleaseIsIdempotent(t *testing.T) {
	acks := 0
	_, d := Attach(t.Context(), "", false, func() { acks++ })
	first, second := d.Hold(), d.Hold()
	first()
	first()
	d.Release()
	if acks != 0 {
		t.Fatal("a repeated release counted twice")
	}
	second()
	if acks != 1 {
		t.Fatalf("acks = %d, want 1", acks)
	}
}

func TestFromWithoutDelivery(t *testing.T) {
	if From(t.Context()) != nil {
		t.Fatal("From on a bare context should be nil")
	}
}

func TestHoldAfterTheAcknowledgementTakesNothing(t *testing.T) {
	acks := 0
	_, d := Attach(t.Context(), "", false, func() { acks++ })
	d.Release()
	d.Hold()()
	if acks != 1 {
		t.Fatalf("acks = %d, want the one acknowledgement", acks)
	}
}

func TestKeepLeavesTheMessageUnacknowledged(t *testing.T) {
	acks := 0
	_, d := Attach(t.Context(), "", false, func() { acks++ })
	d.Keep()
	d.Release()
	if acks != 0 {
		t.Fatalf("acks = %d, want 0", acks)
	}
}

func TestOnAckRunsAfterTheAcknowledgement(t *testing.T) {
	var order []string
	_, d := Attach(t.Context(), "", false, func() { order = append(order, "ack") })
	release := d.Hold()
	d.OnAck(func() { order = append(order, "after") })
	d.Release()
	if len(order) != 0 {
		t.Fatalf("ran %v while a hold was taken", order)
	}
	release()
	if len(order) != 2 || order[0] != "ack" || order[1] != "after" {
		t.Fatalf("order = %v, want [ack after]", order)
	}
}

func TestOnAckAfterTheAcknowledgementRunsAtOnce(t *testing.T) {
	_, d := Attach(t.Context(), "", false, func() {})
	d.Release()
	ran := false
	d.OnAck(func() { ran = true })
	if !ran {
		t.Fatal("OnAck after the acknowledgement did not run")
	}
}

func TestOnAckNeverRunsForAKeptMessage(t *testing.T) {
	_, d := Attach(t.Context(), "", false, func() {})
	ran := false
	d.OnAck(func() { ran = true })
	d.Keep()
	d.Release()
	if ran {
		t.Fatal("OnAck ran for a kept message")
	}
}
