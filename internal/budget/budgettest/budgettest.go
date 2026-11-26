// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgettest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func MeshState(blocked bool, periodEnd, computedAt time.Time) budget.EnforcementState {
	return budget.EnforcementState{BudgetsInUse: true, MeshBlocked: blocked, PeriodEnd: periodEnd, ComputedAt: computedAt}
}

func MeshExhausted(resetsAt time.Time) *budget.ExhaustedError {
	return &budget.ExhaustedError{Tier: budget.TierMesh, ResetsAt: resetsAt}
}

func UserBlockedState(periodEnd, computedAt time.Time, users ...string) budget.EnforcementState {
	state := MeshState(false, periodEnd, computedAt)
	state.BlockedUsers = users
	return state
}

func WithGate(svc runtime.Services, gate *budget.Gate) runtime.Services {
	return gatedServices{Services: svc, gate: gate}
}

type gatedServices struct {
	runtime.Services
	gate *budget.Gate
}

func (s gatedServices) BudgetGate() *budget.Gate { return s.gate }

func NewGate() *budget.Gate {
	return budget.NewGate(slog.New(slog.DiscardHandler))
}

func BlockedGate(t testing.TB, resetsAt time.Time) *budget.Gate {
	t.Helper()
	gate := NewGate()
	BlockMesh(t, gate, resetsAt)
	return gate
}

func BlockMesh(t testing.TB, gate *budget.Gate, resetsAt time.Time) {
	t.Helper()
	gate.Apply(t.Context(), MeshState(true, resetsAt, time.Now()))
}

func BroadcastMeshBlocked(t testing.TB, brk broker.Broker, namespace string, resetsAt time.Time) {
	t.Helper()
	payload, err := json.Marshal(MeshState(true, resetsAt, time.Now()))
	if err != nil {
		t.Fatalf("marshal budget state: %v", err)
	}
	topic := a2a.BudgetStateTopic(namespace)
	if err := brk.Publish(t.Context(), topic, &broker.Message{Topic: topic, Payload: payload}); err != nil {
		t.Fatalf("publish budget state: %v", err)
	}
}

func WaitForMeshRefusal(t testing.TB, who string, gate *budget.Gate) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(gate.Check(""), budget.ErrExhausted) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("%s still admits work after the platform broadcast a spent mesh budget", who)
}

func TapAgentRequests(t testing.TB, brk broker.Broker, namespace, agent string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue("budget-test-"+agent, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic(namespace, agent)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}

func UserBlockedGate(t testing.TB, resetsAt time.Time, users ...string) *budget.Gate {
	t.Helper()
	gate := NewGate()
	BlockUsers(t, gate, resetsAt, users...)
	return gate
}

func BlockUsers(t testing.TB, gate *budget.Gate, resetsAt time.Time, users ...string) {
	t.Helper()
	gate.Apply(t.Context(), UserBlockedState(resetsAt, time.Now(), users...))
}

func DefaultUsersBlockedGate(t testing.TB, resetsAt time.Time, admitted ...string) *budget.Gate {
	t.Helper()
	state := MeshState(false, resetsAt, time.Now())
	state.DefaultUsersBlocked = true
	state.AdmittedUsers = admitted
	gate := NewGate()
	gate.Apply(t.Context(), state)
	return gate
}

func AssertNotDelivered(t testing.TB, brk broker.Broker, q broker.Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if msg, _ := brk.Receive(ctx, q); msg != nil {
		t.Fatalf("a budget-refused submit reached the agent: %s", msg.Payload)
	}
}

func UnconfirmedGate(t testing.TB) *budget.Gate {
	t.Helper()
	gate := NewGate()
	Unconfirm(t, gate)
	return gate
}

func Unconfirm(t testing.TB, gate *budget.Gate) {
	t.Helper()
	gate.Attach(t.Context(), storedState{MeshState(false, time.Now().Add(time.Hour), time.Now())})
}

type storedState struct{ state budget.EnforcementState }

func (s storedState) LoadBudgetState(context.Context) (*budget.EnforcementState, error) {
	return &s.state, nil
}

func (storedState) SaveBudgetState(context.Context, budget.EnforcementState) error { return nil }
