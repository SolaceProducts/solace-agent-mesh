// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func claimFor(agent *Agent, taskID string) session.RunningTask {
	return session.RunningTask{
		TaskID: taskID, AppName: agent.cfg.Name, UserID: concurrentUser.UserID, SessionID: concurrentCtxID,
	}
}

func listRunningAfter(t *testing.T, agent *Agent, liveAfter time.Time) []string {
	t.Helper()
	ids, err := agent.svc.Sessions().ListRunningTasks(context.Background(), agent.cfg.Name, concurrentUser.UserID, concurrentCtxID, liveAfter)
	if err != nil {
		t.Fatalf("list running tasks: %v", err)
	}
	return ids
}

func containsTask(ids []string, taskID string) bool {
	for _, id := range ids {
		if id == taskID {
			return true
		}
	}
	return false
}

func userInputResponseMessage(t *testing.T, taskID, requestID string) a2a.Message {
	t.Helper()
	sent := a2a.Message{
		Kind:      "message",
		MessageID: "m-hil-answer",
		Role:      a2a.RoleUser,
		Parts: []a2a.Part{a2a.NewDataPart(a2a.UserInputResponseData{
			Type: "user_input_response", RequestID: requestID, Status: "answered",
			Data: map[string]any{"decision": "approve"},
		})},
		TaskID: taskID,
	}
	wire, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{"message": sent}))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var req a2a.Request
	if err = json.Unmarshal(wire, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	received, err := extractA2AMessage(req.Params)
	if err != nil {
		t.Fatalf("extract message: %v", err)
	}
	return received
}

func TestHILAnswerDoesNotReleaseTheRunningTasksClaim(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	slowTool := func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		close(started)
		<-release
		return tool.OK("slow-result"), nil
	}
	agent, mock := newConcurrentSessionAgent(t, "slow_tool", slowTool,
		singleToolCallResponse("call-a1", "slow_tool"),
		&llm.ChatResponse{Content: "pong"},
		&llm.ChatResponse{Content: "A done"},
	)

	taskADone := make(chan struct{})
	go func() {
		defer close(taskADone)
		if _, err := agent.HandleTask(context.Background(), "task-a", concUserMessage("work W01", concurrentCtxID), concurrentUser); err != nil {
			t.Errorf("task A: %v", err)
		}
	}()
	released := false
	releaseA := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(func() {
		releaseA()
		<-taskADone
	})
	waitOrFail(t, started, "task A's tool to start")

	answer := userInputResponseMessage(t, "task-a", "hil-req-unmatched")
	resp, err := agent.HandleTask(t.Context(), "task-a", answer, concurrentUser)
	if err != nil || resp != nil {
		t.Fatalf("HIL answer = (%+v, %v), want it intercepted with no response", resp, err)
	}

	if got := runningTaskIDs(t, agent); !containsTask(got, "task-a") {
		t.Fatalf("running after the HIL answer = %v, want task-a still claimed", got)
	}

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}
	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("LLM calls = %d, want 2 (A, B)", len(calls))
	}
	assertNoSyntheticRepairs(t, "task B after the HIL answer", calls[1])
	if !requestContains(calls[1], siblingInProgressContent) {
		t.Error("task B's request does not say task A is still in progress")
	}
	releaseA()
}

func TestResumedTaskReleasesItsClaimWhenItCompletes(t *testing.T) {
	agent, mock := newConcurrentSessionAgent(t, "test_hil_tool", stubReturnsPendingHIL("call-h1", "hil-req-1"),
		singleToolCallResponse("call-h1", "test_hil_tool"),
		&llm.ChatResponse{Content: "resumed and finished"},
	)

	agent.runCtx = t.Context()

	resp, err := agent.HandleTask(t.Context(), "task-a", concUserMessage("approve W02", concurrentCtxID), concurrentUser)
	if err != nil || resp != nil {
		t.Fatalf("task A = (%+v, %v), want it paused on the HIL call", resp, err)
	}

	answer := userInputResponseMessage(t, "task-a", "hil-req-1")
	if resp, err := agent.HandleTask(t.Context(), "task-a", answer, concurrentUser); err != nil || resp != nil {
		t.Fatalf("HIL answer = (%+v, %v), want it intercepted with no response", resp, err)
	}

	if !waitUntil(func() bool { return len(mock.getCalls()) == 2 }) {
		t.Fatal("the HIL answer did not resume task A through to a second LLM call")
	}
	if !waitUntil(func() bool { return agent.pausedTasks.Load("task-a") == nil }) {
		t.Fatal("task A never left pausedTasks")
	}
	if !waitUntil(func() bool { return len(runningTaskIDs(t, agent)) == 0 }) {
		t.Errorf("running after task A completed = %v, want its claim released", runningTaskIDs(t, agent))
	}
}

func TestAdopterOwnsTheClaimAndTheOldOwnerCannotRemoveIt(t *testing.T) {
	former, _ := newConcurrentSessionAgent(t, "unused", alwaysOKTool())
	adopter, _, _ := newTestAgent()
	adopter.svc.(*mockServices).sessions = former.svc.Sessions()
	ctx := context.Background()
	const taskID = "task-a"

	former.claimRunningTask(ctx, claimFor(former, taskID), former.logger)
	adopter.claimRunningTask(ctx, claimFor(adopter, taskID), adopter.logger)
	if former.runningTasks.ownerID == adopter.runningTasks.ownerID {
		t.Fatal("the two agents share an owner id; the test cannot tell them apart")
	}

	former.releaseRunningTask(taskID)

	if got := runningTaskIDs(t, adopter); !containsTask(got, taskID) {
		t.Fatalf("running after the former owner released = %v, want the adopter's claim intact", got)
	}

	adopter.releaseRunningTask(taskID)
	if got := runningTaskIDs(t, adopter); containsTask(got, taskID) {
		t.Errorf("running after the adopter released = %v, want the claim gone", got)
	}
}

func TestRenewalExtendsLeasesAndRestoresAMissingRow(t *testing.T) {
	agent, _ := newConcurrentSessionAgent(t, "unused", alwaysOKTool())
	ctx := context.Background()
	const taskID = "task-a"

	agent.claimRunningTask(ctx, claimFor(agent, taskID), agent.logger)
	if err := agent.svc.Sessions().RemoveRunningTask(ctx, taskID, agent.runningTasks.ownerID); err != nil {
		t.Fatalf("remove row: %v", err)
	}
	if got := runningTaskIDs(t, agent); containsTask(got, taskID) {
		t.Fatalf("row still listed after removal: %v", got)
	}

	now := time.Now().Add(10 * time.Minute)
	agent.renewRunningTasks(ctx, now)

	if got := listRunningAfter(t, agent, now.Add(runningTaskLease/2)); !containsTask(got, taskID) {
		t.Errorf("running after renewal = %v, want the row restored with a lease past now+lease/2", got)
	}
}

type contextLimitOnceLLM struct {
	*mockLLM
	refused  atomic.Bool
	onRefuse func()
}

func (c *contextLimitOnceLLM) refuseOnce() error {
	if !c.refused.CompareAndSwap(false, true) {
		return nil
	}
	if c.onRefuse != nil {
		c.onRefuse()
	}
	return &llm.Error{Code: "context_length_exceeded", StatusCode: 400, Message: "context too long"}
}

func (c *contextLimitOnceLLM) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if err := c.refuseOnce(); err != nil {
		return nil, err
	}
	return c.mockLLM.ChatCompletion(ctx, req)
}

func (c *contextLimitOnceLLM) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if err := c.refuseOnce(); err != nil {
		return nil, err
	}
	return c.mockLLM.ChatCompletionStream(ctx, req)
}

func seedSiblingHistory(t *testing.T, agent *Agent, siblingTaskID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := agent.getOrCreateSession(ctx, concurrentUser.UserID, concurrentCtxID, ""); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := agent.svc.Sessions().AppendMessages(ctx, agent.cfg.Name, concurrentUser.UserID, concurrentCtxID, []session.Message{
		{Role: "user", Content: "first question with enough words to weigh something"},
		{Role: "assistant", Content: "first answer with enough words to weigh something"},
		{Role: "user", Content: "second question with enough words to weigh something"},
		{Role: "assistant", Content: "second answer with enough words to weigh something"},
		{Role: "user", Content: "work W05", TaskID: siblingTaskID},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestContextLimitRetryKeepsALiveSiblingInProgress(t *testing.T) {
	agent, mock := newConcurrentSessionAgent(t, "unused", alwaysOKTool(),
		&llm.ChatResponse{Content: "condensed history"},
		&llm.ChatResponse{Content: "pong"},
	)
	agent.cfg.InjectCurrentTime = new(bool)
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	svc := agent.svc.(*mockServices)
	svc.llmMap[""] = &contextLimitOnceLLM{mockLLM: mock}

	seedSiblingHistory(t, agent, "alice-task")
	if err := agent.svc.Sessions().RegisterRunningTask(context.Background(), claimFor(agent, "alice-task"), "alice-owner", time.Now().Add(runningTaskLease)); err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("LLM calls served = %d, want 2 (summarization, retried turn)", len(calls))
	}
	retried := calls[1]
	siblingAt := firstIndexWithContent(retried, "work W05")
	if siblingAt < 0 {
		t.Fatal("the retried request lost the sibling's request entirely")
	}
	if siblingAt+1 >= len(retried.Messages) || retried.Messages[siblingAt+1].Content != siblingInProgressContent {
		t.Error("the retried request does not say the sibling is still in progress right after its request")
	}
	if requestContains(retried, danglingToolResultContent) {
		t.Error("the retried request carries a dangling-tool-call repair for a live sibling")
	}
}

type checkpointedSibling struct {
	sessionID     string
	state         []byte
	durableResume bool
}

func requestOfTaskBBesideCheckpointedSibling(t *testing.T, sibling checkpointedSibling) llm.ChatRequest {
	t.Helper()
	agent, mock := newConcurrentSessionAgent(t, "unused", alwaysOKTool(), &llm.ChatResponse{Content: "pong"})
	if sibling.durableResume {
		svc := agent.svc.(*mockServices)
		svc.featureRegistry = durableResumeRegistry(true)
		agent.checkpoints = newTaskCheckpointer(svc, agent.cfg.Name, newTestAgentBackstops(), agent.logger)
		if agent.checkpoints == nil {
			t.Fatal("expected durable resume to be enabled")
		}
	}
	seedSiblingHistory(t, agent, "alice-task")
	if err := agent.svc.Sessions().SaveCheckpoint(context.Background(), &session.TaskCheckpoint{
		TaskID: "alice-task", AppName: agent.cfg.Name, AgentName: agent.cfg.Name,
		UserID: concurrentUser.UserID, SessionID: sibling.sessionID,
		PendingCount: 1, State: sibling.state,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}
	return mock.getCalls()[0]
}

var resumableCheckpointState = []byte(`{"version":1}`)

func TestSiblingWithOnlyACheckpointInTheSessionStaysInProgress(t *testing.T) {
	req := requestOfTaskBBesideCheckpointedSibling(t, checkpointedSibling{
		sessionID: concurrentCtxID, state: resumableCheckpointState, durableResume: true,
	})
	assertNoSyntheticRepairs(t, "task B beside a checkpointed sibling", req)
	if !requestContains(req, siblingInProgressContent) {
		t.Error("task B's request does not say the checkpointed sibling is still in progress")
	}
}

func TestLeftoverCheckpointWithDurableResumeOffIsRepairedAsCrashed(t *testing.T) {
	req := requestOfTaskBBesideCheckpointedSibling(t, checkpointedSibling{
		sessionID: concurrentCtxID, state: resumableCheckpointState, durableResume: false,
	})
	if !requestContains(req, cancellationMarkerContent) {
		t.Error("a sibling with only a leftover checkpoint was not marked interrupted")
	}
	if requestContains(req, siblingInProgressContent) {
		t.Error("a sibling with only a leftover checkpoint was shown as in progress")
	}
}

func TestUndecodableCheckpointStillShowsItsTaskInProgress(t *testing.T) {
	req := requestOfTaskBBesideCheckpointedSibling(t, checkpointedSibling{
		sessionID: concurrentCtxID, state: []byte("not a checkpoint"), durableResume: true,
	})
	if !requestContains(req, siblingInProgressContent) {
		t.Error("a sibling with an undecodable checkpoint was not shown as in progress")
	}
}

func TestSiblingWithACheckpointFromAnotherSessionIsRepairedAsCrashed(t *testing.T) {
	req := requestOfTaskBBesideCheckpointedSibling(t, checkpointedSibling{
		sessionID: "ctx-other", state: resumableCheckpointState, durableResume: true,
	})
	if !requestContains(req, cancellationMarkerContent) {
		t.Error("a sibling whose checkpoint belongs to another session was not marked interrupted")
	}
	if requestContains(req, siblingInProgressContent) {
		t.Error("a sibling whose checkpoint belongs to another session was shown as in progress")
	}
}

func firstIndexWithContent(req llm.ChatRequest, content string) int {
	for i, m := range req.Messages {
		if m.Content == content {
			return i
		}
	}
	return -1
}

type failingRunningTaskListStore struct {
	session.Store
}

func (failingRunningTaskListStore) ListRunningTasks(context.Context, string, string, string, time.Time) ([]string, error) {
	return nil, errors.New("running-task table unavailable")
}

func TestListRunningTasksFailureFallsBackToRepairs(t *testing.T) {
	agent, mock := newConcurrentSessionAgent(t, "unused", alwaysOKTool(), &llm.ChatResponse{Content: "pong"})
	store := agent.svc.Sessions()
	agent.svc.(*mockServices).sessions = failingRunningTaskListStore{Store: store}

	seedSiblingHistory(t, agent, "bob-task")
	if err := store.RegisterRunningTask(context.Background(), claimFor(agent, "bob-task"), "bob-owner", time.Now().Add(runningTaskLease)); err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}

	req := mock.getCalls()[0]
	if !requestContains(req, cancellationMarkerContent) {
		t.Error("the open sibling was not repaired as interrupted when the claims could not be read")
	}
	if requestContains(req, siblingInProgressContent) {
		t.Error("the sibling was shown as in progress although its claim could not be confirmed")
	}
}

func TestStartRunsRunningTaskMaintenance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := session.NewMemoryStore()
		agent := startedAgentOn(t, store)
		defer agent.Stop(context.Background()) //nolint:errcheck // test teardown

		const taskID = "task-a"
		task := claimFor(agent, taskID)
		agent.runningTasks.add(task)
		shortLease := time.Now().Add(time.Second)
		if err := store.RegisterRunningTask(context.Background(), task, agent.runningTasks.ownerID, shortLease); err != nil {
			t.Fatalf("register: %v", err)
		}

		time.Sleep(runningTaskRenewInterval + time.Second)
		synctest.Wait()

		if got := listRunningAfter(t, agent, time.Now()); !containsTask(got, taskID) {
			t.Errorf("running after one renewal interval = %v, want the lease renewed past its 1s expiry", got)
		}
	})
}

func startedAgentOn(t *testing.T, store session.Store) *Agent {
	t.Helper()
	agent := New(AgentConfig{Name: "test-agent", Namespace: "test-ns", Model: "test-model", MaxLLMCalls: 5})
	svc := newMockServices(newMockLLM(), store)
	svc.brk = newMockBroker()
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return agent
}

func TestStartSweepsOnlyRowsLapsedBeyondTheSkewAllowance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := session.NewMemoryStore()
		agent := startedAgentOn(t, store)
		defer agent.Stop(context.Background()) //nolint:errcheck // test teardown

		started := time.Now()
		tickAt := started.Add(runningTaskRenewInterval)
		const deadOwner, skewedOwner = "alice-task", "bob-task"
		seed := map[string]time.Time{
			deadOwner:   tickAt.Add(-checkpointClockSkewAllowance - runningTaskLease),
			skewedOwner: tickAt.Add(-checkpointClockSkewAllowance / 2),
		}
		for taskID, until := range seed {
			if err := store.RegisterRunningTask(context.Background(), claimFor(agent, taskID), "other-owner", until); err != nil {
				t.Fatalf("register %s: %v", taskID, err)
			}
		}

		time.Sleep(runningTaskRenewInterval + time.Second)
		synctest.Wait()

		got := listRunningAfter(t, agent, started.Add(-24*time.Hour))
		if containsTask(got, deadOwner) {
			t.Errorf("rows after one sweep = %v, want %s's row (lapsed beyond the skew allowance) swept", got, deadOwner)
		}
		if !containsTask(got, skewedOwner) {
			t.Errorf("rows after one sweep = %v, want %s's row (lapsed within the skew allowance) kept", got, skewedOwner)
		}
	})
}

type blockingRenewStore struct {
	session.Store
	entered chan struct{}
	proceed chan struct{}
}

func (s *blockingRenewStore) RenewRunningTasks(ctx context.Context, ownerID string, tasks []session.RunningTask, until time.Time) (int, error) {
	close(s.entered)
	<-s.proceed
	return s.Store.RenewRunningTasks(ctx, ownerID, tasks, until)
}

func TestRenewalInFlightDuringReleaseDoesNotResurrectTheRow(t *testing.T) {
	agent, _ := newConcurrentSessionAgent(t, "unused", alwaysOKTool())
	blocking := &blockingRenewStore{Store: agent.svc.Sessions(), entered: make(chan struct{}), proceed: make(chan struct{})}
	agent.svc.(*mockServices).sessions = blocking
	const taskID = "task-a"
	agent.claimRunningTask(context.Background(), claimFor(agent, taskID), agent.logger)

	renewed := make(chan struct{})
	now := time.Now()
	go func() {
		defer close(renewed)
		agent.renewRunningTasks(context.Background(), now)
	}()
	waitOrFail(t, blocking.entered, "the renewal to reach the store")
	agent.releaseRunningTask(taskID)
	close(blocking.proceed)
	waitOrFail(t, renewed, "the renewal to finish")

	if got := listRunningAfter(t, agent, now); containsTask(got, taskID) {
		t.Errorf("running after the renewal finished = %v, want the released task's row gone", got)
	}
}

type panickingListStore struct {
	session.Store
}

func (panickingListStore) ListRunningTasks(context.Context, string, string, string, time.Time) ([]string, error) {
	panic("synthetic panic while listing running tasks")
}

func TestPanicInTaskSetupAfterTheClaimReleasesIt(t *testing.T) {
	agent, _ := newConcurrentSessionAgent(t, "unused", alwaysOKTool())
	store := agent.svc.Sessions()
	agent.svc.(*mockServices).sessions = panickingListStore{Store: store}
	seedSiblingHistory(t, agent, "alice-task")

	resp, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser)
	if err == nil && (resp == nil || resp.Error == nil) {
		t.Fatalf("HandleTask = (%+v, %v), want the recovered panic reported", resp, err)
	}

	ids, listErr := store.ListRunningTasks(context.Background(), agent.cfg.Name, concurrentUser.UserID, concurrentCtxID, time.Now())
	if listErr != nil {
		t.Fatalf("list running tasks: %v", listErr)
	}
	if containsTask(ids, "task-b") {
		t.Errorf("running after the setup panic = %v, want task-b's claim released", ids)
	}
	if got := agent.runningTasks.snapshot(); len(got) != 0 {
		t.Errorf("claims after the setup panic = %v, want none", got)
	}
}

func claimForState(agent *Agent, state *TaskExecutionState) session.RunningTask {
	return session.RunningTask{
		TaskID: state.TaskID, AppName: agent.cfg.Name, UserID: state.UserProps.UserID, SessionID: state.EffectiveSessionID,
	}
}

func claimStateTask(agent *Agent, state *TaskExecutionState) {
	agent.claimRunningTask(context.Background(), claimForState(agent, state), agent.logger)
}

func stateIsListedRunning(t *testing.T, store session.Store, agent *Agent, state *TaskExecutionState) bool {
	t.Helper()
	claim := claimForState(agent, state)
	ids, err := store.ListRunningTasks(context.Background(), claim.AppName, claim.UserID, claim.SessionID, time.Now())
	if err != nil {
		t.Fatalf("list running tasks: %v", err)
	}
	return containsTask(ids, state.TaskID)
}

func stateIsClaimedHere(agent *Agent, state *TaskExecutionState) bool {
	for _, task := range agent.runningTasks.snapshot() {
		if task.TaskID == state.TaskID {
			return true
		}
	}
	return false
}

func assertClaimReleased(t *testing.T, store session.Store, agent *Agent, state *TaskExecutionState, path string) {
	t.Helper()
	if stateIsListedRunning(t, store, agent, state) {
		t.Errorf("%s left the task's running row in the store; siblings read it as in progress until the lease lapses", path)
	}
	if stateIsClaimedHere(agent, state) {
		t.Errorf("%s left the task among this process's claims; every renewal re-inserts its row", path)
	}
}

func shutdownMidTurn(t *testing.T, store session.Store, configure ...func(*Agent)) (*Agent, *TaskExecutionState) {
	t.Helper()
	agent, _, _ := replicaOf(t, store, "leaving", configure...)
	state := pausedOnPeerCall(t, store, idgen.New(), idgen.New())
	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)
	claimStateTask(agent, state)

	stopped, cancelRun := context.WithCancel(context.Background())
	agent.runCtx = stopped
	cancelRun()

	agent.resumeTaskExecution(state)
	return agent, state
}

func TestStopMidTurnKeepsTheRunningRow(t *testing.T) {
	store := session.NewMemoryStore()
	agent, state := shutdownMidTurn(t, store)

	if !stateIsListedRunning(t, store, agent, state) {
		t.Error("the stop removed the running row; siblings read the task as crashed until its successor adopts it")
	}
}

func TestAdopterClaimsTheTaskItAdopts(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	adopter, adopterLLM, _ := replicaOf(t, store, "adopter")

	state := pausedOnPeerCall(t, store, idgen.New(), idgen.New())
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	loseOwner(t, store, state, 1)
	owner.checkpoints.recordResult(context.Background(), state.TaskID,
		state.PendingCalls["tc-1"], tool.OK("peer finished the work"))

	var listed, claimedHere atomic.Bool
	adopterLLM.preCallHook = func(int) {
		listed.Store(stateIsListedRunning(t, store, adopter, state))
		claimedHere.Store(stateIsClaimedHere(adopter, state))
	}

	adopter.resumeAbandonedCheckpoints(context.Background(), time.Now())

	if !waitUntil(func() bool { return len(adopterLLM.getCalls()) > 0 }) {
		t.Fatal("the adopter never ran the turn, so this proves nothing")
	}
	if !listed.Load() {
		t.Error("the adopted task had no running row while its turn ran; siblings in its session read it as crashed")
	}
	if !claimedHere.Load() {
		t.Error("the adopter does not hold the claim it needs to renew while the adopted turn runs")
	}
	if !waitUntil(func() bool { return !stateIsListedRunning(t, store, adopter, state) }) {
		t.Error("the adopted task's running row outlived its completion")
	}
}

const siblingFinalAnswer = "W05 finished: here is the report"

func finishSibling(t *testing.T, agent *Agent, store session.Store, siblingTaskID, owner string) {
	t.Helper()
	ctx := context.Background()
	if err := store.AppendMessages(ctx, agent.cfg.Name, concurrentUser.UserID, concurrentCtxID, []session.Message{
		{Role: "assistant", Content: siblingFinalAnswer, TaskID: siblingTaskID},
	}); err != nil {
		t.Errorf("flush the sibling's answer: %v", err)
	}
	if err := store.RemoveRunningTask(ctx, siblingTaskID, owner); err != nil {
		t.Errorf("drop the sibling's claim: %v", err)
	}
}

type finishesSiblingOnFirstListStore struct {
	session.Store
	finish func()
	done   atomic.Bool
}

func (s *finishesSiblingOnFirstListStore) ListRunningTasks(ctx context.Context, appName, userID, sessionID string, liveAfter time.Time) ([]string, error) {
	if s.done.CompareAndSwap(false, true) {
		s.finish()
	}
	return s.Store.ListRunningTasks(ctx, appName, userID, sessionID, liveAfter)
}

func assertSiblingShownFinished(t *testing.T, label string, req llm.ChatRequest) {
	t.Helper()
	at := firstIndexWithContent(req, "work W05")
	if at < 0 || at+1 >= len(req.Messages) {
		t.Fatalf("%s: the sibling's request is missing or last", label)
	}
	if got := req.Messages[at+1].Content; got != siblingFinalAnswer {
		t.Errorf("%s: after the sibling's request came %.80q, want its final answer (a finished sibling was repaired as unfinished)", label, got)
	}
	if requestContains(req, siblingInProgressContent) {
		t.Errorf("%s: a sibling that finished was shown as in progress", label)
	}
}

func TestSiblingFinishingAsTheTaskStartsIsNotRepairedAsCancelled(t *testing.T) {
	agent, mock := newConcurrentSessionAgent(t, "unused", alwaysOKTool(), &llm.ChatResponse{Content: "pong"})
	underlying := agent.svc.Sessions()
	seedSiblingHistory(t, agent, "alice-task")
	if err := underlying.RegisterRunningTask(context.Background(), claimFor(agent, "alice-task"), "alice-owner", time.Now().Add(runningTaskLease)); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent.svc.(*mockServices).sessions = &finishesSiblingOnFirstListStore{
		Store:  underlying,
		finish: func() { finishSibling(t, agent, underlying, "alice-task", "alice-owner") },
	}

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}
	assertSiblingShownFinished(t, "task B", mock.getCalls()[0])
}

func TestSiblingFinishingBeforeTheContextLimitRetryIsNotRepairedAsCancelled(t *testing.T) {
	agent, mock := newConcurrentSessionAgent(t, "unused", alwaysOKTool(),
		&llm.ChatResponse{Content: "condensed history"},
		&llm.ChatResponse{Content: "pong"},
	)
	agent.cfg.InjectCurrentTime = new(bool)
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	store := agent.svc.Sessions()
	seedSiblingHistory(t, agent, "alice-task")
	if err := store.RegisterRunningTask(context.Background(), claimFor(agent, "alice-task"), "alice-owner", time.Now().Add(runningTaskLease)); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent.svc.(*mockServices).llmMap[""] = &contextLimitOnceLLM{
		mockLLM:  mock,
		onRefuse: func() { finishSibling(t, agent, store, "alice-task", "alice-owner") },
	}

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}
	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("LLM calls served = %d, want 2 (summarization, retried turn)", len(calls))
	}
	assertSiblingShownFinished(t, "the retried turn", calls[1])
}
