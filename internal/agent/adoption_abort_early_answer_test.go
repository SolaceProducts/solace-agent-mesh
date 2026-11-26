// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type unreadableSessions struct {
	session.Store
}

func (unreadableSessions) Get(context.Context, string, string, string) (*session.Session, error) {
	return nil, errors.New("sessions unavailable")
}

func adoptedStateHoldingAPersonRequest() *TaskExecutionState {
	request := tool.Pending("waiting for approval", tool.WithData(map[string]any{
		"pending_kind":   "hil_approval",
		"hil_request_id": personWaitRequestID,
		"hil_timeout_ns": float64(time.Hour),
	}))
	return &TaskExecutionState{
		TaskID:             "task-adoption-aborted",
		EffectiveSessionID: "sess-adoption-aborted",
		UserProps:          a2a.UserProperties{UserID: "user-1"},
		rebuildFromRows:    true,
		SyncResults:        map[int]*tool.Result{0: request},
	}
}

func assertAnswerIsNotSwallowedBy(t *testing.T, agent *Agent) {
	t.Helper()
	if got := agent.deliverHILResponse(personsAnswer(), hilSenderUser("user-1")); got != hilUnmatched {
		t.Errorf("deliverHILResponse() = %v after the adoption was given up, want unmatched so the answer is relayed through the store to the replica that adopts the task", got)
	}
	if n := earlySlotCount(agent); n != 0 {
		t.Errorf("early answer slots after the adoption was given up = %d, want 0", n)
	}
}

func TestAnAdoptionGivenUpBecauseTheTaskIsNotReadableLeavesNoSlotThatSwallowsThePersonsAnswer(t *testing.T) {
	agent, _, _ := replicaOf(t, unreadableSessions{Store: session.NewMemoryStore()}, "adopter")
	agent.runCtx = t.Context()

	agent.startAdoptedTask(t.Context(), adoptedStateHoldingAPersonRequest(), nil, agent.logger)

	assertAnswerIsNotSwallowedBy(t, agent)
}

func TestAnAdoptionGivenUpBecauseTheProcessIsStoppingLeavesNoSlotThatSwallowsThePersonsAnswer(t *testing.T) {
	agent, _, _ := replicaOf(t, session.NewMemoryStore(), "adopter")
	stopped, stop := context.WithCancel(t.Context())
	stop()
	agent.runCtx = stopped

	agent.startAdoptedTask(t.Context(), adoptedStateHoldingAPersonRequest(), nil, agent.logger)

	assertAnswerIsNotSwallowedBy(t, agent)
}

func TestAnAdoptionGivenUpBecauseTheTaskIsAlreadyClaimedLeavesNoSlotThatSwallowsThePersonsAnswer(t *testing.T) {
	agent, _, _ := replicaOf(t, session.NewMemoryStore(), "adopter")
	agent.runCtx = t.Context()
	state := adoptedStateHoldingAPersonRequest()
	state.rebuildFromRows = false
	if _, claimed := markResuming(state); !claimed {
		t.Fatal("markResuming() did not claim a fresh state")
	}

	agent.startAdoptedTask(t.Context(), state, nil, agent.logger)

	assertAnswerIsNotSwallowedBy(t, agent)
}
