// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func peerStatusMessage() *broker.Message {
	return &broker.Message{Payload: []byte(`{"jsonrpc":"2.0","result":{"kind":"status-update"}}`)}
}

func pendingExpiry(t *testing.T, store session.Store, agentName, corrID string) *time.Time {
	t.Helper()
	row, err := store.LookupPendingCall(context.Background(), agentName, corrID)
	if err != nil || row == nil {
		t.Fatalf("lookup %s: row=%+v err=%v", corrID, row, err)
	}
	return row.ExpiresAt
}

func TestPeerStatusKeepsDurablePendingCallAliveAfterOwnerDies(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")

	const taskID, subTaskID = "task-long-peer", "sub-long-peer"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	loseOwner(t, store, state, 1)

	budgetPassed := time.Now().Add(-time.Minute)
	if err := store.RegisterPendingCalls(context.Background(), taskID, heldOwner(t, store, taskID), []session.PendingCall{
		{CorrID: subTaskID, TaskID: taskID, AgentName: survivor.cfg.Name,
			ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer, ExpiresAt: &budgetPassed},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	survivor.peerRouter.HandleStatus(subTaskID, peerStatusMessage())

	wantAtLeast := time.Now().Add(survivor.delegationBackstops().peer)
	if !waitUntil(func() bool { return pendingExpiry(t, store, survivor.cfg.Name, subTaskID).After(wantAtLeast) }) {
		t.Fatalf("the peer reported status but the durable expiry is still %v, so the reaper times the call out although the peer is alive",
			pendingExpiry(t, store, survivor.cfg.Name, subTaskID))
	}

	survivor.reapExpiredPendingCalls(context.Background(), time.Now())
	cp, err := store.GetCheckpoint(context.Background(), taskID)
	if err != nil || cp == nil {
		t.Fatalf("read back: %v, %v", cp, err)
	}
	if len(cp.ToolResults) != 0 {
		t.Fatalf("a timeout was banked over a call whose peer is still reporting status (%d results)", len(cp.ToolResults))
	}

	survivor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "peer finished the work"))
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "peer finished the work")
}

type extensionExtender interface {
	ExtendPendingCall(ctx context.Context, agentName, corrID string, until time.Time) error
}

type countingStore struct {
	session.Store
	mu        sync.Mutex
	lookups   map[string]int
	extended  map[string]int
	lookupErr error
	extendErr error
}

func newCountingStore() *countingStore {
	return &countingStore{Store: session.NewMemoryStore(), lookups: map[string]int{}, extended: map[string]int{}}
}

func (s *countingStore) LookupPendingCall(ctx context.Context, agentName, corrID string) (*session.PendingCall, error) {
	s.mu.Lock()
	s.lookups[corrID]++
	err := s.lookupErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.Store.LookupPendingCall(ctx, agentName, corrID)
}

func (s *countingStore) ExtendPendingCall(ctx context.Context, agentName, corrID string, until time.Time) error {
	s.mu.Lock()
	s.extended[corrID]++
	err := s.extendErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.(extensionExtender).ExtendPendingCall(ctx, agentName, corrID, until)
}

func (s *countingStore) count(m map[string]int, corrID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return m[corrID]
}

func (s *countingStore) setErrors(lookupErr, extendErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookupErr, s.extendErr = lookupErr, extendErr
}

func registerPeerRows(t *testing.T, store session.Store, owner *Agent, taskID string, calls ...session.PendingCall) {
	t.Helper()
	state := pausedOnPeerCall(t, store, taskID, "unused-"+taskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	for i := range calls {
		calls[i].TaskID = taskID
		calls[i].AgentName = owner.cfg.Name
		calls[i].ToolCallID = "tc-" + calls[i].CorrID
	}
	if err := store.RegisterPendingCalls(context.Background(), taskID, heldOwner(t, store, taskID), calls); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func TestPeerStatusExtendsOnlyDelegationRowsByTheirOwnBudget(t *testing.T) {
	store := newCountingStore()
	agent, _, _ := replicaOf(t, store, "replica")
	soon := time.Now().Add(time.Second)
	registerPeerRows(t, store, agent, "task-kinds",
		session.PendingCall{CorrID: "k-peer", Kind: session.PendingKindPeer, ExpiresAt: &soon},
		session.PendingCall{CorrID: "k-subtask", Kind: session.PendingKindSubTask, ExpiresAt: &soon},
		session.PendingCall{CorrID: "k-workflow", Kind: session.PendingKindWorkflowPeer, ExpiresAt: &soon},
		session.PendingCall{CorrID: "k-hil", Kind: session.PendingKindHILApproval, ExpiresAt: &soon},
		session.PendingCall{CorrID: "k-open", Kind: session.PendingKindPeer},
	)

	for _, id := range []string{"k-hil", "k-open", "unknown-corr"} {
		agent.peerRouter.HandleStatus(id, peerStatusMessage())
	}
	budgets := agent.delegationBackstops()
	for id, budget := range map[string]time.Duration{"k-peer": budgets.peer, "k-subtask": budgets.peer, "k-workflow": budgets.workflow} {
		floor := time.Now().Add(budget)
		agent.peerRouter.HandleStatus(id, peerStatusMessage())
		if !waitUntil(func() bool { return pendingExpiry(t, store, agent.cfg.Name, id).After(floor) }) {
			t.Errorf("%s: expiry %v is not past now + its budget %v", id, pendingExpiry(t, store, agent.cfg.Name, id), budget)
		}
	}
	waitUntil(func() bool {
		return store.count(store.lookups, "k-hil") > 0 && store.count(store.lookups, "k-open") > 0
	})

	if got := pendingExpiry(t, store, agent.cfg.Name, "k-hil"); got == nil || !got.Equal(soon) {
		t.Errorf("a non-delegation call's expiry became %v; only peer-family calls follow a peer's status", got)
	}
	if got := pendingExpiry(t, store, agent.cfg.Name, "k-open"); got != nil {
		t.Errorf("a call with no expiry was given %v", got)
	}
	if n := store.count(store.extended, "k-hil") + store.count(store.extended, "k-open"); n != 0 {
		t.Errorf("%d extensions written for rows that must keep their expiry", n)
	}
}

func TestPeerStatusSurvivesStoreErrors(t *testing.T) {
	store := newCountingStore()
	agent, _, _ := replicaOf(t, store, "replica")
	past := time.Now().Add(-time.Minute)
	registerPeerRows(t, store, agent, "task-errs",
		session.PendingCall{CorrID: "e-lookup", Kind: session.PendingKindPeer, ExpiresAt: &past},
		session.PendingCall{CorrID: "e-extend", Kind: session.PendingKindPeer, ExpiresAt: &past},
	)

	store.setErrors(context.DeadlineExceeded, nil)
	agent.peerRouter.HandleStatus("e-lookup", peerStatusMessage())
	if !waitUntil(func() bool { return store.count(store.lookups, "e-lookup") > 0 }) {
		t.Fatal("the status never reached the store")
	}
	time.Sleep(50 * time.Millisecond)
	if n := store.count(store.extended, "e-lookup"); n != 0 {
		t.Errorf("extended %d times although the lookup failed", n)
	}

	store.setErrors(nil, context.DeadlineExceeded)
	agent.peerRouter.HandleStatus("e-extend", peerStatusMessage())
	if !waitUntil(func() bool { return store.count(store.extended, "e-extend") > 0 }) {
		t.Fatal("no extension was attempted after a successful lookup")
	}
}

func TestPeerStatusIsThrottledPerCorrelation(t *testing.T) {
	store := newCountingStore()
	agent, _, _ := replicaOf(t, store, "replica")
	past := time.Now().Add(-time.Minute)
	registerPeerRows(t, store, agent, "task-throttle",
		session.PendingCall{CorrID: "t-a", Kind: session.PendingKindPeer, ExpiresAt: &past},
		session.PendingCall{CorrID: "t-b", Kind: session.PendingKindPeer, ExpiresAt: &past},
	)

	for range 20 {
		agent.peerRouter.HandleStatus("t-a", peerStatusMessage())
	}
	agent.peerRouter.HandleStatus("t-b", peerStatusMessage())

	if !waitUntil(func() bool { return store.count(store.extended, "t-a") > 0 && store.count(store.extended, "t-b") > 0 }) {
		t.Fatal("the statuses were not turned into extensions")
	}
	time.Sleep(100 * time.Millisecond)
	if n := store.count(store.extended, "t-a"); n != 1 {
		t.Errorf("20 statuses within the interval caused %d extensions, want 1", n)
	}
}

func TestPeerStatusDroppedExtensionIsNotCountedAsDone(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := newInitedReplica(t, store)
	agent.orphanAdoptions = make(chan orphanAdoption, 1)
	agent.orphanAdoptions <- orphanAdoption{corrID: "already-queued"}

	agent.peerRouter.HandleStatus("sub-full", peerStatusMessage())
	if len(agent.orphanAdoptions) != 1 {
		t.Fatalf("a full queue grew to %d", len(agent.orphanAdoptions))
	}

	<-agent.orphanAdoptions
	agent.peerRouter.HandleStatus("sub-full", peerStatusMessage())
	if len(agent.orphanAdoptions) != 1 {
		t.Error("a dropped extension was recorded as done, so the next status was ignored")
	}
}

func TestPeerStatusWithoutDurableResumeDoesNotQueue(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.peerRouter = NewPeerRouter(newMockBroker(), agent.cfg.Namespace, agent.logger)
	agent.peerRouter.HandleStatus("sub-off", peerStatusMessage())
}

func newInitedReplica(t *testing.T, store session.Store) (*Agent, *mockLLM, *mockBroker) {
	t.Helper()
	mock := newMockLLM()
	brk := newMockBroker()
	agent := New(AgentConfig{
		Name: "test-agent", SystemPrompt: "x", Model: "test-model",
		MaxTokens: 1000, MaxLLMCalls: 5, Namespace: "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	svc.featureRegistry = durableResumeRegistry(true)
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	return agent, mock, brk
}
