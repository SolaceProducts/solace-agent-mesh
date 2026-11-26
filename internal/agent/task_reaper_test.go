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
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestReaperResolvesExpiredPendingCall(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-expired"
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-expired"},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := store.RegisterPendingCalls(ctx, "task-expired", heldOwner(t, store, "task-expired"), []session.PendingCall{
		{CorrID: "sub-expired", TaskID: "task-expired", AgentName: agent.cfg.Name,
			ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer, ExpiresAt: &past},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := store.ReleaseOwnership(ctx, "task-expired", agent.checkpoints.ownerID); err != nil {
		t.Fatalf("release: %v", err)
	}

	agent.reapExpiredPendingCalls(ctx, time.Now())

	if got, err := store.LookupPendingCall(ctx, "test-agent", "sub-expired"); err != nil || got != nil {
		t.Errorf("expired correlation survived the reaper: %v, %v", got, err)
	}
}

func TestReaperSkipsLocallyPausedTask(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-local-timer"
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-local-timer"},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := store.RegisterPendingCalls(ctx, "task-local-timer", heldOwner(t, store, "task-local-timer"), []session.PendingCall{
		{CorrID: "sub-local-timer", TaskID: "task-local-timer", AgentName: agent.cfg.Name,
			ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer, ExpiresAt: &past},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent.pausedTasks.Store(state)

	agent.reapExpiredPendingCalls(ctx, time.Now())

	got, err := store.LookupPendingCall(ctx, "test-agent", "sub-local-timer")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil {
		t.Error("the reaper resolved a call the local process is still timing")
	}
}

func TestSweepResumesCheckpointWithExpiredLease(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	seeded := fullTaskState()
	seeded.TaskID = "task-abandoned"
	blob, err := marshalTaskState(seeded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	expired := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: "task-abandoned", AppName: agent.cfg.Name, AgentName: agent.cfg.Name,
		SessionID: "sess", PendingCount: 0, State: blob,
		OwnerID: "owner-dead", LeaseExpiresAt: &expired,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.checkpoints.takeoverAfter = 50 * time.Millisecond
	time.Sleep(2 * agent.checkpoints.takeoverAfter)

	agent.resumeAbandonedCheckpoints(ctx, time.Now())

	if !sweepTook(t, agent, store, "task-abandoned", "owner-dead") {
		t.Error("the sweep did not take over a checkpoint whose owner is gone — the task never completes")
	}
}

func sweepTook(t *testing.T, agent *Agent, store session.Store, taskID, priorOwner string) bool {
	t.Helper()
	if agent.pausedTasks.Load(taskID) != nil {
		return true
	}
	owner, exists, err := store.CheckpointOwner(context.Background(), taskID)
	if err != nil {
		t.Fatalf("read checkpoint owner for %s: %v", taskID, err)
	}
	return !exists || owner != priorOwner
}

func TestSweepLeavesLiveOwnerAlone(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	seeded := fullTaskState()
	seeded.TaskID = "task-live"
	blob, err := marshalTaskState(seeded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	held := time.Now().Add(time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: "task-live", AppName: agent.cfg.Name, AgentName: agent.cfg.Name,
		SessionID: "sess", PendingCount: 0, State: blob,
		OwnerID: "owner-alive", LeaseExpiresAt: &held,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	agent.resumeAbandonedCheckpoints(ctx, time.Now())

	if agent.pausedTasks.Load("task-live") != nil {
		t.Error("the sweep took a task whose owner still holds the lease")
	}
}

func TestSweepIgnoresOtherAgentsCheckpoints(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	seeded := fullTaskState()
	seeded.TaskID = "task-other-agent"
	blob, err := marshalTaskState(seeded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	expired := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: "task-other-agent", AppName: "someone-else", AgentName: "someone-else",
		SessionID: "sess", PendingCount: 0, State: blob,
		OwnerID: "owner-dead", LeaseExpiresAt: &expired,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	agent.resumeAbandonedCheckpoints(ctx, time.Now())

	if agent.pausedTasks.Load("task-other-agent") != nil {
		t.Error("the sweep adopted a checkpoint belonging to a different agent")
	}
}

func TestRenewOwnedLeasesProtectsLocallyPausedTasks(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-held"
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-held"},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.pausedTasks.Store(state)

	future := time.Now().Add(2 * checkpointLease)
	agent.renewOwnedLeases(ctx, future)

	if got, err := store.AdoptCheckpoint(ctx, "task-held", "bystander", future.Add(time.Second), time.Minute, testTakeoverWindow, false); err != nil || got != nil {
		t.Errorf("a renewed task was adopted anyway: %v, %v — renewal is not protecting live work", got, err)
	}
}

func TestSweepWaitsOutTheOwnersSilenceWhateverItsOwnClockSays(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	agent.checkpoints.takeoverAfter = 200 * time.Millisecond
	ctx := context.Background()

	seeded := fullTaskState()
	seeded.TaskID = "task-skew"
	blob, err := marshalTaskState(seeded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	lapsed := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: "task-skew", AppName: agent.cfg.Name, AgentName: agent.cfg.Name,
		SessionID: "sess", PendingCount: 0, State: blob,
		OwnerID: "owner-maybe-alive", LeaseExpiresAt: &lapsed,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	agent.resumeAbandonedCheckpoints(ctx, time.Now().Add(time.Hour))
	if sweepTook(t, agent, store, "task-skew", "owner-maybe-alive") {
		t.Error("a task its owner renewed a moment ago was taken because this replica's clock runs ahead")
	}

	time.Sleep(2 * agent.checkpoints.takeoverAfter)
	agent.resumeAbandonedCheckpoints(ctx, time.Now())
	if !sweepTook(t, agent, store, "task-skew", "owner-maybe-alive") {
		t.Error("a task whose owner stayed silent for the takeover window was never taken — a dead owner's work is stuck")
	}
}

func TestFencedReplicaDropsTasksItNoLongerOwns(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-fenced"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.pausedTasks.Store(state)

	if _, err := takeOver(t, store, "task-fenced", "successor"); err != nil {
		t.Fatalf("successor adopt: %v", err)
	}

	agent.renewOwnedLeases(ctx, time.Now())

	if agent.pausedTasks.Load("task-fenced") != nil {
		t.Error("the fenced replica is still holding a task another replica owns — both will run the turn")
	}
}

func TestFenceKeepsTaskWhenNoCheckpointExists(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-unsaved"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)

	agent.renewOwnedLeases(ctx, time.Now())

	if agent.pausedTasks.Load("task-unsaved") == nil {
		t.Error("a task with no checkpoint was abandoned as if fenced — healthy work destroyed with no terminal response")
	}
}

func TestFenceCancelsAnAlreadyRunningResume(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-running"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.pausedTasks.Store(state)

	resumeCtx, resumeCancel := context.WithCancel(context.Background())
	defer resumeCancel()
	agent.activeTasksMu.Lock()
	agent.activeTasks[state.TaskID] = &activeTaskEntry{cancel: stopTaskFunc(resumeCancel), publishOnce: &sync.Once{}}
	agent.activeTasksMu.Unlock()

	answerCtx, answerCancel := context.WithCancel(context.Background())
	defer answerCancel()
	agent.trackControlEntry(state.TaskID, &activeTaskEntry{cancel: stopTaskFunc(answerCancel), publishOnce: &sync.Once{}})
	otherCtx, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	agent.trackControlEntry("task-other", &activeTaskEntry{cancel: stopTaskFunc(otherCancel), publishOnce: &sync.Once{}})

	if _, err := takeOver(t, store, state.TaskID, "successor"); err != nil {
		t.Fatalf("successor adopt: %v", err)
	}

	agent.renewOwnedLeases(ctx, time.Now())

	if resumeCtx.Err() == nil {
		t.Error("the fenced replica's running resume was not cancelled — both replicas will run the turn and publish")
	}
	if answerCtx.Err() == nil {
		t.Error("an answer being handled for the fenced task was not cancelled; its tool replay would run against a state this replica stood down from")
	}
	if otherCtx.Err() != nil {
		t.Error("fencing one task cancelled an answer being handled for another task")
	}
}

func TestDiscardLeavesACheckpointOwnedByAnotherReplica(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-not-mine"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := takeOver(t, store, state.TaskID, "successor"); err != nil {
		t.Fatalf("successor adopt: %v", err)
	}

	agent.checkpoints.discardIfOwned(ctx, state.TaskID)

	owner, exists, err := store.CheckpointOwner(ctx, state.TaskID)
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	if !exists {
		t.Fatal("a fenced replica deleted the checkpoint its successor was working from")
	}
	if owner != "successor" {
		t.Errorf("owner = %q, want the successor to still hold it", owner)
	}
}

func TestFencedReplicaWritesNothingToTheSession(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	if err := store.Create(ctx, &session.Session{
		AppName: agent.cfg.Name, UserID: "user-1", SessionID: "sess-fenced", ContextID: "sess-fenced",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := fullTaskState()
	state.TaskID = "task-fenced-flush"
	state.EffectiveSessionID = "sess-fenced"
	state.IsRunBased = false
	state.HistoryOffset = 0
	state.Fenced.Store(true)

	agent.flushPausedTaskSessionMessages(state)

	sess, err := store.Get(ctx, agent.cfg.Name, "user-1", "sess-fenced")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(sess.Messages) != 0 {
		t.Errorf("a fenced replica wrote %d messages into the successor's session — including a false 'do not re-execute' marker", len(sess.Messages))
	}
}

func TestFenceStandsDownWhenSuccessorAlreadyFinished(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-finished-elsewhere"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.pausedTasks.Store(state)
	claimStateTask(agent, state)

	if err := store.DeleteCheckpoint(ctx, state.TaskID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	agent.renewOwnedLeases(ctx, time.Now())

	if agent.pausedTasks.Load(state.TaskID) != nil {
		t.Error("kept running a task another replica already completed — the turn runs twice")
	}
	if !state.Fenced.Load() {
		t.Error("the task was dropped without being marked fenced, so its flush and publish are not suppressed")
	}
	assertClaimReleased(t, store, agent, state, "fencing a task completed elsewhere")
}

func TestFenceKeepsTaskWhenCheckpointIsUnowned(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-unowned"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.pausedTasks.Store(state)

	if err := store.ReleaseOwnership(ctx, state.TaskID, agent.checkpoints.ownerID); err != nil {
		t.Fatalf("release: %v", err)
	}

	agent.renewOwnedLeases(ctx, time.Now())

	if agent.pausedTasks.Load(state.TaskID) == nil {
		t.Error("abandoned a task whose checkpoint nobody owns — the only live copy is gone")
	}
}

func TestFenceLeavesOtherHealthyTasksAlone(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	healthy := fullTaskState()
	healthy.TaskID = "task-healthy"
	healthy.PendingCalls = map[string]*PendingToolCall{}
	healthy.SyncResults = map[int]*tool.Result{}
	healthy.TotalPending = 0

	taken := fullTaskState()
	taken.TaskID = "task-taken"
	taken.PendingCalls = map[string]*PendingToolCall{}
	taken.SyncResults = map[int]*tool.Result{}
	taken.TotalPending = 0

	for _, s := range []*TaskExecutionState{healthy, taken} {
		if err := agent.checkpoints.save(ctx, s); err != nil {
			t.Fatalf("save %s: %v", s.TaskID, err)
		}
		agent.pausedTasks.Store(s)
		claimStateTask(agent, s)
	}

	if _, err := takeOver(t, store, taken.TaskID, "successor"); err != nil {
		t.Fatalf("successor adopt: %v", err)
	}

	agent.renewOwnedLeases(ctx, time.Now())

	assertClaimReleased(t, store, agent, taken, "fencing a task another replica adopted")
	if !stateIsClaimedHere(agent, healthy) || !stateIsListedRunning(t, store, agent, healthy) {
		t.Error("a healthy task lost its claim because a different task was fenced")
	}

	if agent.pausedTasks.Load(taken.TaskID) != nil {
		t.Error("the taken task was not stood down")
	}
	if agent.pausedTasks.Load(healthy.TaskID) == nil {
		t.Error("a healthy task was abandoned because a different task was fenced")
	}
	if healthy.Fenced.Load() {
		t.Error("a healthy task was marked fenced")
	}
}

func TestMaintainCheckpointsExitsOnContextCancel(t *testing.T) {
	agent := checkpointedAgent(t, session.NewMemoryStore())
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		agent.maintainCheckpoints(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("maintainCheckpoints did not return after its context was cancelled")
	}
}

func TestMaintainCheckpointsReturnsImmediatelyWhenDisabled(t *testing.T) {
	agent, _, _ := newTestAgent()
	done := make(chan struct{})
	go func() {
		agent.maintainCheckpoints(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("maintainCheckpoints must return immediately when durable resume is off")
	}
}

func TestReaperIgnoresAnotherAgentsExpiredCall(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-other-agent"
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-other"},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	if err := store.RegisterPendingCalls(ctx, "task-other-agent", heldOwner(t, store, "task-other-agent"), []session.PendingCall{
		{CorrID: "sub-other", TaskID: "task-other-agent", AgentName: "some-other-agent",
			ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer, ExpiresAt: &past},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	agent.reapExpiredPendingCalls(ctx, time.Now())

	got, err := store.LookupPendingCall(ctx, "some-other-agent", "sub-other")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil {
		t.Error("this agent's reaper consumed another agent's expired call — it adopts that checkpoint and runs the turn with the wrong tools, model and config, then answers for a task that was never its own")
	}
	if agent.pausedTasks.Load("task-other-agent") != nil {
		t.Error("this agent adopted another agent's task")
	}
}

func TestReaperLeavesAnExpiredCallWhoseOwnerIsStillAlive(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-live-owner"
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-live"},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := store.RegisterPendingCalls(ctx, "task-live-owner", heldOwner(t, store, "task-live-owner"), []session.PendingCall{
		{CorrID: "sub-live", TaskID: "task-live-owner", AgentName: agent.cfg.Name,
			ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer, ExpiresAt: &past},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	agent.reapExpiredPendingCalls(ctx, time.Now())

	cp, err := store.GetCheckpoint(ctx, "task-live-owner")
	if err != nil || cp == nil {
		t.Fatalf("read back: %v, %v", cp, err)
	}
	if len(cp.ToolResults) != 0 {
		t.Errorf("a synthesised timeout was banked over a call whose owner still holds its lease (%d results) — the owner's timer resets on peer activity and expires_at does not, so this poisons the durable copy a later adoption replays", len(cp.ToolResults))
	}
	if cp.PendingCount != 1 {
		t.Errorf("pending count = %d, want 1 — the live call was written off", cp.PendingCount)
	}
	if row, _ := store.LookupPendingCall(ctx, agent.cfg.Name, "sub-live"); row == nil {
		t.Error("the live call's correlation row was consumed, so its real answer can no longer be recorded")
	}
}

func TestReaperDiscardsACorrelationWhoseCheckpointIsGone(t *testing.T) {
	store, db := newSQLiteCheckpointStore(t)
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO task_pending_calls (corr_id, task_id, agent_name, tool_call_id, tool_name, trace_id, kind, created_at, expires_at)
		 VALUES ('sub-vanished', 'task-vanished', ?, 'tc-1', 'peer_agent', '', 'peer', '2026-01-01T00:00:00.000000000Z', '2026-01-01T00:00:00.000000000Z')`,
		agent.cfg.Name); err != nil {
		t.Fatalf("seed orphan correlation: %v", err)
	}

	agent.reapExpiredPendingCalls(ctx, time.Now())

	if row, _ := store.LookupPendingCall(ctx, agent.cfg.Name, "sub-vanished"); row != nil {
		t.Error("a correlation whose checkpoint is gone survived the reaper — nothing can ever record against it, so it is retried every tick forever and occupies a slot in the batch")
	}
}
