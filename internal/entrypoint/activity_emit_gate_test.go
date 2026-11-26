// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type activityGateRecorder struct {
	noopAdapter
	calls int
}

func (a *activityGateRecorder) OnActivityEvent(context.Context, string, string, map[string]any) {
	a.calls++
}

var _ ActivityEventEmitter = (*activityGateRecorder)(nil)

func TestHandleStatusEvent_ActivityEmitGate(t *testing.T) {
	const taskID = "task-gate"

	statusMsg := func(payload []byte) *broker.Message {
		return &broker.Message{Topic: a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), Payload: payload}
	}
	respBytes := func(ev any) []byte {
		b, err := json.Marshal(a2a.NewResponse(taskID, ev))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	statusEvent := func(state a2a.TaskState) *a2a.TaskStatusUpdateEvent {
		return &a2a.TaskStatusUpdateEvent{
			TaskID: taskID, ContextID: "c", Kind: "status-update",
			Status: a2a.TaskStatus{State: state, Message: &a2a.Message{
				Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("delta")},
			}},
		}
	}

	cases := []struct {
		name     string
		payload  []byte
		wantEmit bool
	}{
		{"working_text_filtered", respBytes(statusEvent(a2a.TaskStateWorking)), false},
		{"non_filtered_completed", respBytes(statusEvent(a2a.TaskStateCompleted)), true},
		{"error_branch", []byte(`{"jsonrpc":"2.0","id":"task-gate","error":{"code":-32000,"message":"boom"}}`), true},
		{"typed_parse_fail", []byte(`{"jsonrpc":"2.0","id":"task-gate","result":{"kind":"status-update","final":"not-a-bool"}}`), true},
		{"malformed_json_dropped", []byte(`not json`), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			brk := broker.NewDevBroker()
			if err := brk.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			rec := &activityGateRecorder{}
			core := NewGatewayCore(rec, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
			core.SetServices(runtime.NewRuntime(brk, nil).Services())
			if err := core.Init(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			core.logger = slog.New(slog.DiscardHandler)
			_ = core.tasks.Register(taskID, "BenchAgent", "user-1", TaskIdentity{TraceID: "c"})

			core.handleStatusEvent(context.Background(), statusMsg(tc.payload))

			if got := rec.calls > 0; got != tc.wantEmit {
				t.Fatalf("emitted=%v want=%v (calls=%d)", got, tc.wantEmit, rec.calls)
			}
		})
	}
}
