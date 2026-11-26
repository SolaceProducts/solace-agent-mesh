// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func finalResponseMessage(taskID string) *broker.Message {
	return &broker.Message{Topic: a2a.GatewayResponseTopic("test-ns", "test-gw", taskID),
		Payload: []byte(`{"jsonrpc":"2.0","id":"x","result":{"id":"` + taskID + `","status":{"state":"completed"}}}`)}
}

func errorResponseMessage(taskID string) *broker.Message {
	return &broker.Message{Topic: a2a.GatewayResponseTopic("test-ns", "test-gw", taskID),
		Payload: []byte(`{"jsonrpc":"2.0","id":"x","error":{"code":1,"message":"boom"}}`)}
}

func errorStatusMessage(taskID string) *broker.Message {
	return &broker.Message{Topic: a2a.GatewayStatusTopic("test-ns", "test-gw", taskID),
		Payload: []byte(`{"jsonrpc":"2.0","id":"x","error":{"code":1,"message":"boom"}}`)}
}

func finalStatusMessage(taskID string) *broker.Message {
	return &broker.Message{Topic: a2a.GatewayStatusTopic("test-ns", "test-gw", taskID),
		Payload: []byte(`{"jsonrpc":"2.0","id":"x","result":{"kind":"status-update","taskId":"` + taskID +
			`","final":true,"status":{"state":"working","message":{"kind":"message","messageId":"m","role":"agent","parts":[{"kind":"text","text":"answer"}]}}}}`)}
}

func receive(core *GatewayCore, msg *broker.Message) int {
	acks := 0
	ctx, d := delivery.Attach(context.Background(), "m", true, func() { acks++ })
	if msg.Topic == a2a.GatewayResponseTopic("test-ns", "test-gw", extractTaskIDFromTopic(msg.Topic)) {
		core.handleResponse(ctx, msg)
	} else {
		core.handleStatusEvent(ctx, msg)
	}
	d.Release()
	return acks
}

func (a *recordingAdapter) counts() (finals, errs, statuses int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.finalResponses), len(a.errors), len(a.statusUpdates)
}

func TestFinalForAnEndedTaskIsDropped(t *testing.T) {
	duplicates := map[string]func(string) *broker.Message{
		"final response": finalResponseMessage,
		"error response": errorResponseMessage,
		"error status":   errorStatusMessage,
		"final status":   finalStatusMessage,
	}
	for name, duplicate := range duplicates {
		t.Run(name, func(t *testing.T) {
			core, _, adapter := setupTestGateway(t)
			if err := core.Init(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			core.logger = slog.New(slog.DiscardHandler)
			var hooks int
			core.OnTaskComplete(func(string, *a2a.Task, *a2a.Error) { hooks++ })
			taskID := idgen.New()
			if err := core.tasks.Register(taskID, "agent", "user", TaskIdentity{}); err != nil {
				t.Fatal(err)
			}
			receive(core, finalResponseMessage(taskID))
			finals, errs, statuses := adapter.counts()

			if acks := receive(core, duplicate(taskID)); acks != 1 {
				t.Fatalf("acks = %d, want the copy acknowledged", acks)
			}
			f, e, s := adapter.counts()
			if f != finals || e != errs || s != statuses || hooks != 1 {
				t.Fatalf("adapter finals/errors/statuses = %d/%d/%d after the copy, want %d/%d/%d; hooks = %d, want 1",
					f, e, s, finals, errs, statuses, hooks)
			}
		})
	}
}

func TestFinalWhileTheTaskIsEndingIsHandled(t *testing.T) {
	core, _, adapter := setupTestGateway(t)
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	core.adapter = recordKeepingAdapter{adapter, func() {}}
	taskID := idgen.New()
	if err := core.tasks.Register(taskID, "agent", "user", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	ctx, d := delivery.Attach(t.Context(), "m1", false, func() {})
	release := d.Hold()
	core.handleResponse(ctx, finalResponseMessage(taskID))
	d.Release()

	receive(core, finalResponseMessage(taskID))
	release()
	if finals, _, _ := adapter.counts(); finals != 2 {
		t.Fatalf("finals = %d, want the second handled while the first was not acknowledged", finals)
	}
}

func TestFinalIsHandledWhenTheTaskRecordCannotBeRead(t *testing.T) {
	db := store.TestDB(t)
	core, _, adapter := setupTestGateway(t)
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	core.tasks = NewDBTaskRegistry(store.NewTaskRecordRepo(db, "test-gw"), silentLogger())
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	taskID := idgen.New()
	core.answered.add(taskID)
	receive(core, finalResponseMessage(taskID))
	if finals, _, _ := adapter.counts(); finals != 1 {
		t.Fatalf("finals = %d, want the final handled on an unreadable record", finals)
	}
}

func TestLateFinalForATaskTheGatewayEndedIsHandled(t *testing.T) {
	core, _, adapter := setupTestGateway(t)
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	taskID := idgen.New()
	if err := core.tasks.Register(taskID, "agent", "user", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	core.ForgetTask(taskID)

	receive(core, finalResponseMessage(taskID))
	if finals, _, _ := adapter.counts(); finals != 1 {
		t.Fatalf("finals = %d, want the late final handled", finals)
	}
	receive(core, finalResponseMessage(taskID))
	if finals, _, _ := adapter.counts(); finals != 1 {
		t.Fatalf("finals = %d, want the copy of the late final dropped", finals)
	}
}

func TestAnsweredTaskIsForgottenAfterTheMemoryPeriod(t *testing.T) {
	answered := newAnsweredTasks(time.Millisecond)
	answered.add("t1")
	if !answered.has("t1") {
		t.Fatal("t1 not remembered right after its final")
	}
	time.Sleep(5 * time.Millisecond)
	answered.add("t2")
	if answered.has("t1") {
		t.Fatal("t1 still remembered after the memory period")
	}
	if len(answered.order) != 1 || len(answered.ids) != 1 {
		t.Fatalf("order/ids = %d/%d, want only t2 kept", len(answered.order), len(answered.ids))
	}
}

func TestAnsweredTaskMemoryFollowsTheHardTimeout(t *testing.T) {
	answered := newAnsweredTasks(answeredTaskMemory)
	answered.setMemory(3 * time.Hour)
	if answered.memory != 3*time.Hour {
		t.Errorf("memory = %v, want the configured hard timeout of 3h", answered.memory)
	}
	answered.setMemory(0)
	if answered.memory != 3*time.Hour {
		t.Errorf("memory = %v after a zero hard timeout, want it unchanged", answered.memory)
	}
}
