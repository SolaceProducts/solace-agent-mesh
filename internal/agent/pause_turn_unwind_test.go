// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type heldAppendStore struct {
	session.Store

	appends     atomic.Int32
	reachedOnce sync.Once
	reached     chan struct{}
	proceed     chan struct{}
	proceedOnce sync.Once
}

func newHeldAppendStore() *heldAppendStore {
	return &heldAppendStore{Store: session.NewMemoryStore(), reached: make(chan struct{}), proceed: make(chan struct{})}
}

func (s *heldAppendStore) release() {
	s.proceedOnce.Do(func() { close(s.proceed) })
}

func (s *heldAppendStore) AppendMessages(ctx context.Context, appName, userID, sessionID string, msgs []session.Message) error {
	if s.appends.Add(1) > 1 {
		s.reachedOnce.Do(func() { close(s.reached) })
		<-s.proceed
	}
	return s.Store.AppendMessages(ctx, appName, userID, sessionID, msgs)
}

func registerDeliverableCall(t *testing.T, agent *Agent, deliverWhileWiring bool) <-chan func(*tool.Result) {
	t.Helper()
	handed := make(chan func(*tool.Result), 1)
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        deliverableTool,
		description: "delivers its result while its pause is being wired, or hands the delivery out",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			r := tool.Pending("waiting for the delivery", tool.WithData(map[string]any{"sub_task_id": "st"}))
			r.OnDelivery = func(deliver func(*tool.Result)) {
				if deliverWhileWiring {
					deliver(tool.OK("the call finished"))
					return
				}
				handed <- deliver
			}
			return r, nil
		},
	}); err != nil {
		t.Fatalf("register %s: %v", deliverableTool, err)
	}
	return handed
}

func TestAResumeRequestedWhileThePausingTurnIsStillFlushingStartsOnlyAfterTheFlush(t *testing.T) {
	type observed struct {
		LLMCallsDuringFlush int
		StoredRoles         []string
	}
	tests := []struct {
		name               string
		deliverWhileWiring bool
	}{
		{name: "the call is delivered while the pause is being wired", deliverWhileWiring: true},
		{name: "the call is delivered from another goroutine while the pausing turn flushes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newHeldAppendStore()
			t.Cleanup(store.release)
			agent, mock, _ := replicaOf(t, store, "owner")
			handed := registerDeliverableCall(t, agent, tt.deliverWhileWiring)
			mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-d1", deliverableTool), {Content: "All done."}}

			returned := make(chan struct{})
			go func() {
				defer close(returned)
				_, _ = agent.HandleTask(t.Context(), deliverableTaskID, messageWithParentSession("start the call"), holderUser())
			}()
			waitOrFail(t, store.reached, "the pausing turn to reach its flush")
			state := agent.pausedTasks.Load(deliverableTaskID)
			if state == nil {
				t.Fatal("the task is not held while its pausing turn flushes")
			}
			if !tt.deliverWhileWiring {
				select {
				case deliver := <-handed:
					deliver(tool.OK("the call finished"))
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for the tool to hand out its delivery")
				}
			}

			got := observed{LLMCallsDuringFlush: len(mock.getCalls())}
			store.release()
			waitOrFail(t, returned, "the pausing turn to return")
			if !waitUntil(func() bool { return len(mock.getCalls()) == 2 && agent.pausedTasks.Load(deliverableTaskID) == nil }) {
				t.Fatalf("the task did not resume and finish: %d LLM calls", len(mock.getCalls()))
			}
			stored, err := store.Get(t.Context(), "test-agent", concurrentUser.UserID, state.EffectiveSessionID)
			if err != nil || stored == nil {
				t.Fatalf("read the session back: %v", err)
			}
			for _, m := range stored.Messages {
				got.StoredRoles = append(got.StoredRoles, m.Role)
			}

			want := observed{LLMCallsDuringFlush: 1, StoredRoles: []string{"user", "assistant", "tool", "assistant"}}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("resume requested during the pausing turn's flush (-want +got):\n%s", diff)
			}
		})
	}
}
