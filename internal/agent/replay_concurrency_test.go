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
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestReplayAndShutdownHandoffDoNotRaceOnMessages(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "owner")
	ctx := context.Background()

	const tasks = 8
	states := make([]*TaskExecutionState, 0, tasks)
	for range tasks {
		taskID, subTaskID := idgen.New(), idgen.New()
		state := pausedOnPeerCall(t, store, taskID, subTaskID)
		if err := agent.checkpoints.save(ctx, state); err != nil {
			t.Fatalf("save: %v", err)
		}
		if _, err := store.RecordToolResult(ctx, taskID, subTaskID, &session.ToolCallResult{
			ToolCallID: "tc-1", ToolName: "peer_agent", Content: "the peer answered",
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
		state.SyncResults[0] = tool.OK("the peer answered")
		state.PendingCalls = map[string]*PendingToolCall{}
		state.TotalPending = 0
		agent.pausedTasks.Store(state)
		states = append(states, state)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	for _, state := range states {
		wg.Add(1)
		go func(s *TaskExecutionState) {
			defer wg.Done()
			<-start
			agent.ResumeTask(s.TaskID)
		}(state)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			agent.flushPausedTasksOnShutdown(t.Context())
		}
	}()

	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the resume and handoff goroutines did not finish")
	}
}

func TestCancellingStandsTheSuccessorDown(t *testing.T) {
	store := session.NewMemoryStore()
	canceller, _, _ := replicaOf(t, store, "canceller")
	successor, _, _ := replicaOf(t, store, "successor")
	ctx := context.Background()

	taskID, sessionID := idgen.New(), idgen.New()
	state := fullTaskState()
	state.TaskID = taskID
	state.EffectiveSessionID = sessionID
	state.ContextID = sessionID
	state.OriginalSessionID = sessionID
	state.IsRunBased = false
	state.StructuredCtx = nil
	state.ReplyTo = ""
	state.UserProps.ReplyTo = ""
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "go"}}
	state.PausedAssistantMsg = llm.ChatMessage{}
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	state.checkpointed.Store(true)

	soon := time.Now().Add(time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", AgentName: "test-agent",
		UserID: state.UserProps.UserID, SessionID: sessionID,
		State:   []byte(`{"version":1}`),
		OwnerID: successor.checkpoints.ownerID, LeaseExpiresAt: &soon,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	canceller.pausedTasks.Store(state)

	canceller.cancelPausedTask(state, cancelFromElsewhere)

	if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
		t.Fatalf("the cancel left the successor's row in place (owner %q) — that replica is not fenced and publishes `completed` for a task already answered `cancelled`, so the gateway sees two terminal events for one task", cp.OwnerID)
	}

	if takenElsewhere := successor.checkpoints.discardIfOwned(ctx, taskID); !takenElsewhere {
		t.Error("the successor's terminal path did not read the missing row as a takeover, so it publishes anyway")
	}
}

func TestCancelDuringAResumedTurnDoesNotTouchItsState(t *testing.T) {
	gate := &gatedAppendStore{Store: session.NewMemoryStore(), entered: make(chan struct{}, 64), release: make(chan struct{})}
	store := gate
	agent, mock, _ := replicaOf(t, store, "owner")
	ctx := context.Background()

	const tasks = 8
	states := make([]*TaskExecutionState, 0, tasks)
	for range tasks {
		taskID, subTaskID := idgen.New(), idgen.New()
		state := pausedOnPeerCall(t, store, taskID, subTaskID)
		if err := agent.checkpoints.save(ctx, state); err != nil {
			t.Fatalf("save: %v", err)
		}
		if _, err := store.RecordToolResult(ctx, taskID, subTaskID, &session.ToolCallResult{
			ToolCallID: "tc-1", ToolName: "peer_agent", Content: "the peer answered",
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
		state.SyncResults[0] = tool.OK("the peer answered")
		agent.pausedTasks.Store(state)
		states = append(states, state)
	}

	var wg sync.WaitGroup
	for _, state := range states {
		wg.Add(1)
		go func(s *TaskExecutionState) {
			defer wg.Done()
			agent.ResumeTask(s.TaskID)
		}(state)
	}

	for range states {
		select {
		case <-gate.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("a resumed turn never reached its session write, so no cancel could land mid-turn and this test proves nothing")
		}
	}

	callsBeforeCancel := len(mock.getCalls())

	var cancels sync.WaitGroup
	for _, state := range states {
		cancels.Add(1)
		go func(s *TaskExecutionState) {
			defer cancels.Done()
			agent.cancelPausedTask(s, cancelFromElsewhere)
		}(state)
	}
	cancels.Wait()

	close(gate.release)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the resume and cancel goroutines did not finish")
	}

	for _, s := range states {
		if !s.Cancelled.Load() {
			t.Errorf("task %s stood down without recording the cancel, so a late peer or HIL delivery can still resume it and the caller never learns it was cancelled", s.TaskID)
		}
	}
	for _, s := range states {
		deadline := time.Now().Add(15 * time.Second)
		for s.resuming.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if s.resuming.Load() {
			t.Fatalf("task %s never finished its turn", s.TaskID)
		}
	}
	for _, s := range states {
		if agent.pausedTasks.Load(s.TaskID) != nil {
			t.Errorf("task %s is still parked in pausedTasks after its cancelled turn ended, so a late peer or HIL delivery can still try to resume it", s.TaskID)
		}
	}

	for _, s := range states {
		if !s.teardownStarted.Load() {
			t.Errorf("task %s was delegated a cancel that its own turn never performed — no synthesize, no peer propagation, no cancelled response", s.TaskID)
		}
	}

	if n := len(mock.getCalls()) - callsBeforeCancel; n != 0 {
		t.Errorf("%d LLM call(s) ran after the cancels were recorded — a cancelled turn kept calling the model, at the caller's expense, for a request they had withdrawn", n)
	}
}

func TestTheOrphanWarnBranchReadsTheCopiedMap(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "owner")
	ctx := context.Background()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults = map[int]*tool.Result{}
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-other": {ToolCallID: "tc-other", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 9, SubTaskID: "sub-other"},
	}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)

	stop := make(chan struct{})
	writing := make(chan struct{})
	go func() {
		close(writing)
		for {
			select {
			case <-stop:
				return
			default:
			}
			state.mu.Lock()
			state.PendingCalls["tc-churn"] = &PendingToolCall{ToolCallID: "tc-churn", Index: 42}
			delete(state.PendingCalls, "tc-churn")
			state.mu.Unlock()
		}
	}()
	<-writing

	agent.ResumeTask(taskID)
	deadline := time.Now().Add(15 * time.Second)
	for state.resuming.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(stop)
	if state.resuming.Load() {
		t.Fatal("the resumed turn never finished")
	}
}

func TestCancelMidTurnStillRemovesTheRowASuccessorWouldUse(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "owner")
	successor, _, _ := replicaOf(t, store, "successor")
	ctx := context.Background()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	agent.pausedTasks.Store(state)

	state.durableWrites.Add(1)
	resumed := make(chan struct{})
	go func() {
		defer close(resumed)
		agent.ResumeTask(taskID)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !state.resuming.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !state.resuming.Load() {
		t.Fatal("the turn never claimed the state")
	}

	agent.cancelPausedTask(state, cancelFromElsewhere)
	state.durableWrites.Done()
	<-resumed
	turnDone := time.Now().Add(15 * time.Second)
	for state.resuming.Load() && time.Now().Before(turnDone) {
		time.Sleep(time.Millisecond)
	}

	if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
		t.Fatalf("the cancelled task's row survived (owner %q) — a successor is not fenced by it and publishes `completed` for a task the caller cancelled", cp.OwnerID)
	}
	if takenElsewhere := successor.checkpoints.discardIfOwned(ctx, taskID); !takenElsewhere {
		t.Error("a successor's terminal path would not read this as a takeover, so it publishes anyway")
	}
}

type gatedAppendStore struct {
	session.Store
	entered chan struct{}
	release chan struct{}
	once    sync.Map
}

func (s *gatedAppendStore) AppendMessages(ctx context.Context, appName, userID, sessionID string, msgs []session.Message) error {
	if _, seen := s.once.LoadOrStore(sessionID, true); !seen {
		s.entered <- struct{}{}
		<-s.release
	}
	return s.Store.AppendMessages(ctx, appName, userID, sessionID, msgs)
}

func TestShutdownKeepsTheLeaseOfAMidTurnTask(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "shutdown")
	ctx := context.Background()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	agent.pausedTasks.Store(state)
	release, _ := markResuming(state)
	defer release()

	agent.flushPausedTasksOnShutdown(ctx)

	owner, exists, err := store.CheckpointOwner(ctx, taskID)
	if err != nil {
		t.Fatalf("read owner: %v", err)
	}
	if !exists {
		t.Fatal("the handoff deleted a mid-turn task's row")
	}
	if owner != agent.checkpoints.ownerID {
		t.Fatalf("the handoff released a mid-turn task's lease (owner now %q) — a mid-turn task with pending_count 0 becomes adoptable at once and runs twice", owner)
	}
}

func TestACancelInThePreRegistrationWindowIsHonoured(t *testing.T) {
	store := session.NewMemoryStore()
	agent, mock, _ := replicaOf(t, store, "owner")
	ctx := context.Background()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	agent.pausedTasks.Store(state)

	state.durableWrites.Add(1)

	resumed := make(chan struct{})
	go func() {
		defer close(resumed)
		agent.ResumeTask(taskID)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !state.resuming.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !state.resuming.Load() {
		t.Fatal("the turn never claimed the state, so the window under test was never entered")
	}
	agent.activeTasksMu.Lock()
	_, registered := agent.activeTasks[taskID]
	agent.activeTasksMu.Unlock()
	if registered {
		t.Fatal("the turn registered in activeTasks before the barrier, so this test is no longer exercising the pre-registration window")
	}

	agent.cancelPausedTask(state, cancelFromElsewhere)

	if !state.Cancelled.Load() {
		t.Error("the cancel stood down without recording that the task was cancelled — a later peer or HIL delivery resumes it and the caller never learns it was cancelled")
	}

	state.durableWrites.Done()
	select {
	case <-resumed:
	case <-time.After(15 * time.Second):
		t.Fatal("ResumeTask never returned")
	}
	turnDone := time.Now().Add(15 * time.Second)
	for state.resuming.Load() && time.Now().Before(turnDone) {
		time.Sleep(time.Millisecond)
	}
	if state.resuming.Load() {
		t.Fatal("the resumed turn never finished")
	}

	if agent.pausedTasks.Load(taskID) != nil {
		t.Error("the cancelled task is still parked in pausedTasks, so a late delivery can still resume it")
	}
	if n := len(mock.getCalls()); n != 0 {
		t.Errorf("the cancelled turn made %d LLM call(s) after the cancel was recorded — it ran on to answer a withdrawn request, and the caller is billed for it", n)
	}
}

func TestTheOnTurnRecheckTearsDownRatherThanDelegating(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubAuthAwareTool("cred-recheck"))

	state := newTestTaskState(agent)
	state.UserProps.TraceID = "trace-recheck"
	args := defaultPauseCallArgs(agent, state)

	release, _ := markResuming(state)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	toolCtx := agent.buildToolCtxFromState(ctx, state, args.sp)
	cancel()

	if _, paused := agent.dispatchToolCallsMayPause(
		ctx, state, singleCall("stubtool", "call-recheck", "{}"),
		toolCtx, args.sp, args.parallelGroupID, args.sanitizeErrors, agent.logger,
	); !paused {
		t.Fatal("the tool did not pause, so the re-check under test was never reached")
	}

	if !state.Cancelled.Load() {
		t.Error("the re-check stood down instead of tearing down, so the task is parked with Cancelled unset — a later peer, HIL or auth delivery resumes it and corrupts history, which is exactly what this re-check exists to prevent")
	}
	if agent.pausedTasks.Load(state.TaskID) != nil {
		t.Error("the cancelled task is still parked in pausedTasks")
	}
}

func TestADelegatedCancelIsPerformedByTheTurnItWasDelegatedTo(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubAuthAwareTool("cred-delegate"))

	state := newTestTaskState(agent)
	state.UserProps.TraceID = "trace-delegate"
	args := defaultPauseCallArgs(agent, state)

	release, _ := markResuming(state)
	defer release()

	agent.cancelPausedTask(state, cancelFromElsewhere)
	if !state.Cancelled.Load() {
		t.Fatal("the external cancel did not acknowledge")
	}
	if state.teardownStarted.Load() {
		t.Fatal("the external cancel performed the teardown itself, so the delegation under test never happened")
	}

	ctx, cancel := context.WithCancel(context.Background())
	toolCtx := agent.buildToolCtxFromState(ctx, state, args.sp)
	cancel()

	if _, paused := agent.dispatchToolCallsMayPause(
		ctx, state, singleCall("stubtool", "call-delegate", "{}"),
		toolCtx, args.sp, args.parallelGroupID, args.sanitizeErrors, agent.logger,
	); !paused {
		t.Fatal("the tool did not pause, so the re-check under test was never reached")
	}

	if !state.teardownStarted.Load() {
		t.Error("the delegate was refused, so nobody performed the teardown: no terminal response, peers still running, timers still armed, and a task parked in pausedTasks that no delivery can resume — after which a successor rebuilds it uncancelled and runs its tools")
	}
	if agent.pausedTasks.Load(state.TaskID) != nil {
		t.Error("the cancelled task is still parked in pausedTasks")
	}
}

func TestOnlyOnePartyPerformsACancelTeardown(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("done"), nil
	})

	state := newTestTaskState(agent)
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
	}
	state.PausedAssistantMsg = state.Messages[1]
	state.SyncResults = map[int]*tool.Result{0: tool.OK("answered")}
	state.PendingCalls = map[string]*PendingToolCall{}
	agent.pausedTasks.Store(state)

	const racers = 8
	var wg sync.WaitGroup
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agent.tearDownCancelledTask(state)
		}()
	}
	wg.Wait()

	paired := 0
	for _, m := range state.Messages {
		if m.ToolCallID == "tc-1" {
			paired++
		}
	}
	if paired > 1 {
		t.Errorf("%d parties performed the teardown, pairing tool_use tc-1 %d times — the provider rejects a duplicate tool_result outright", paired, paired)
	}
}
