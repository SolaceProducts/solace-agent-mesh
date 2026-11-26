// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
)

type recordKeepingAdapter struct {
	*recordingAdapter
	whileEnding func()
}

func (recordKeepingAdapter) KeepsTaskRecordUntilAck() {
}

func (a recordKeepingAdapter) OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, raw []byte) {
	a.whileEnding()
	a.recordingAdapter.OnFinalResponse(ctx, taskID, task, raw)
}

func (a recordKeepingAdapter) OnError(ctx context.Context, taskID string, taskErr *a2a.Error, raw []byte) {
	a.whileEnding()
	a.recordingAdapter.OnError(ctx, taskID, taskErr, raw)
}

func TestEndedTaskRecordKeptUntilAck(t *testing.T) {
	taskID := tid("kept-record")
	ending := map[string]func(*GatewayCore, context.Context){
		"final response": func(g *GatewayCore, ctx context.Context) {
			g.handleResponse(ctx, &broker.Message{Topic: a2a.GatewayResponseTopic("test-ns", "test-gw", taskID),
				Payload: []byte(`{"jsonrpc":"2.0","id":"x","result":{"id":"` + taskID + `","status":{"state":"completed"}}}`)})
		},
		"error response": func(g *GatewayCore, ctx context.Context) {
			g.handleResponse(ctx, &broker.Message{Topic: a2a.GatewayResponseTopic("test-ns", "test-gw", taskID),
				Payload: []byte(`{"jsonrpc":"2.0","id":"x","error":{"code":1,"message":"boom"}}`)})
		},
		"error status event": func(g *GatewayCore, ctx context.Context) {
			g.handleStatusEvent(ctx, &broker.Message{Topic: a2a.GatewayStatusTopic("test-ns", "test-gw", taskID),
				Payload: []byte(`{"jsonrpc":"2.0","id":"x","error":{"code":1,"message":"boom"}}`)})
		},
	}
	for name, end := range ending {
		t.Run(name, func(t *testing.T) {
			core, _, adapter := setupTestGateway(t)
			if err := core.Init(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			core.logger = slog.New(slog.DiscardHandler)
			var fired atomic.Bool
			core.adapter = recordKeepingAdapter{adapter, func() {
				if fired.CompareAndSwap(false, true) {
					core.onTaskHardTimeout(t.Context(), taskID, 1)
				}
			}}
			if err := core.tasks.Register(taskID, "agent", "user", TaskIdentity{}); err != nil {
				t.Fatal(err)
			}
			acks := 0
			ctx, d := delivery.Attach(t.Context(), "m1", false, func() { acks++ })
			release := d.Hold()

			end(core, ctx)
			d.Release()
			core.onTaskHardTimeout(t.Context(), taskID, 1)
			if !core.IsTaskRunning(taskID) {
				t.Fatal("task record deleted while the adapter held the event")
			}
			release()
			if acks != 1 || core.IsTaskRunning(taskID) {
				t.Fatalf("acks = %d, running = %v; want the record deleted on the acknowledgement", acks, core.IsTaskRunning(taskID))
			}
		})
	}
}

func TestEndedTaskRecordDeletedAtOnce(t *testing.T) {
	taskID := tid("deleted-record")
	final := []byte(`{"jsonrpc":"2.0","id":"x","result":{"id":"` + taskID + `","status":{"state":"completed"}}}`)
	cases := map[string]struct {
		keeps    bool
		delivery bool
	}{
		"adapter that does not keep it": {keeps: false, delivery: true},
		"event without a delivery":      {keeps: true, delivery: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			core, _, adapter := setupTestGateway(t)
			if err := core.Init(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			core.logger = slog.New(slog.DiscardHandler)
			if tc.keeps {
				core.adapter = recordKeepingAdapter{adapter, func() {}}
			}
			if err := core.tasks.Register(taskID, "agent", "user", TaskIdentity{}); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if tc.delivery {
				var d *delivery.Delivery
				ctx, d = delivery.Attach(ctx, "m1", false, func() {})
				defer d.Hold()()
			}
			core.handleResponse(ctx, &broker.Message{Topic: a2a.GatewayResponseTopic("test-ns", "test-gw", taskID), Payload: final})
			if core.IsTaskRunning(taskID) {
				t.Fatal("task record kept after the final response")
			}
		})
	}
}
