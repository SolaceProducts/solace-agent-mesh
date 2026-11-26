// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestStopBoundsDrainWhenTaskWedged(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.svc.(*mockServices).brk = newMockBroker()
	agent.drainTimeout = 100 * time.Millisecond

	agent.activeTasksMu.Lock()
	agent.activeTasks["wedged"] = &activeTaskEntry{
		cancel:      func() {},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Unlock()

	done := make(chan struct{})
	go func() {
		_ = agent.Stop(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return: drain is unbounded and blocks on the wedged task")
	}
}

func TestStopDrainCompletesWhenTaskFinishes(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.svc.(*mockServices).brk = newMockBroker()
	agent.drainTimeout = 5 * time.Second

	agent.activeTasksMu.Lock()
	agent.activeTasks["finishing"] = &activeTaskEntry{
		cancel:      func() {},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Unlock()

	go func() {
		time.Sleep(300 * time.Millisecond)
		agent.activeTasksMu.Lock()
		delete(agent.activeTasks, "finishing")
		agent.activeTasksMu.Unlock()
	}()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		_ = agent.Stop(context.Background())
		close(done)
	}()

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed >= 5*time.Second {
			t.Fatalf("Stop hit the drain timeout (%s) instead of returning when the task finished", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the in-flight task finished")
	}
}

func TestStopForProcessExitDrainsPastControlCap(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.svc.(*mockServices).brk = newMockBroker()
	agent.drainTimeout = 100 * time.Millisecond

	agent.activeTasksMu.Lock()
	agent.activeTasks["slow"] = &activeTaskEntry{
		cancel:      func() {},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Unlock()

	go func() {
		time.Sleep(time.Second)
		agent.activeTasksMu.Lock()
		delete(agent.activeTasks, "slow")
		agent.activeTasksMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_ = agent.StopForProcessExit(ctx)

	agent.activeTasksMu.Lock()
	remaining := len(agent.activeTasks)
	agent.activeTasksMu.Unlock()
	if remaining != 0 {
		t.Fatal("StopForProcessExit stopped draining at the control-plane cap instead of the grace deadline")
	}
}

const drainAgentName = "drain-agent"

type gatedLLM struct {
	*mockLLM
	entered chan struct{}
	release chan struct{}
	lastCtx atomic.Pointer[context.Context]
}

func newGatedLLM() *gatedLLM {
	return &gatedLLM{
		mockLLM: newMockLLM(&llm.ChatResponse{Content: "done", FinishReason: "stop"}),
		entered: make(chan struct{}, 4),
		release: make(chan struct{}),
	}
}

func (g *gatedLLM) wait(ctx context.Context) error {
	g.lastCtx.Store(&ctx)
	g.entered <- struct{}{}
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *gatedLLM) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if err := g.wait(ctx); err != nil {
		return nil, err
	}
	return g.mockLLM.ChatCompletion(ctx, req)
}

func (g *gatedLLM) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if err := g.wait(ctx); err != nil {
		return nil, err
	}
	return g.mockLLM.ChatCompletionStream(ctx, req)
}

func (g *gatedLLM) taskCancelled() bool {
	ctx := g.lastCtx.Load()
	return ctx == nil || (*ctx).Err() != nil
}

func startDrainAgent(t *testing.T, client llm.Client) (*Agent, *mockBroker) {
	t.Helper()
	return startDrainAgentUnder(t.Context(), t, client)
}

func startDrainAgentUnder(startCtx context.Context, t *testing.T, client llm.Client) (*Agent, *mockBroker) {
	t.Helper()
	brk := newMockBroker()
	agent := New(AgentConfig{
		Name:                       drainAgentName,
		SystemPrompt:               "You are a helpful assistant.",
		Model:                      "test-model",
		MaxTokens:                  1000,
		MaxLLMCalls:                5,
		Namespace:                  "test-ns",
		CardPublishIntervalSeconds: 1,
	})
	svc := newMockServices(client, newMemSessionStore())
	svc.brk = brk
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := agent.Start(startCtx); err != nil {
		t.Fatalf("start: %v", err)
	}
	return agent, brk
}

func deliverTaskRequest(t *testing.T, brk *mockBroker, taskID, replyTo string) *atomic.Int32 {
	t.Helper()
	return deliverTaskRequestWith(t, brk, taskID, a2a.UserProperties{ReplyTo: replyTo})
}

func deliverTaskRequestWith(t *testing.T, brk *mockBroker, taskID string, props a2a.UserProperties) *atomic.Int32 {
	t.Helper()
	msg := a2a.Message{
		Kind: "message", MessageID: "m-" + taskID, Role: a2a.RoleUser,
		Parts: []a2a.Part{a2a.NewTextPart("hello")}, ContextID: idgen.New(),
	}
	payload, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{"message": msg}))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	acks := &atomic.Int32{}
	delivered := broker.NewDeliveredMessage("id-"+taskID, a2a.AgentRequestTopic("test-ns", drainAgentName), payload,
		func() { acks.Add(1) }, func() {})
	props.Timestamp, props.ClientID, props.UserID = time.Now().UnixMilli(), "gw-1", "user-1"
	delivered.UserProperties = a2a.EncodeUserProperties(&props)
	requestQueueChannel(brk) <- delivered
	return acks
}

func requestQueueChannel(brk *mockBroker) chan *broker.Message {
	brk.mu.Lock()
	defer brk.mu.Unlock()
	return brk.receiveChs[a2a.QueueName("test-ns", "a2a", drainAgentName)]
}

func removalCards(brk *mockBroker) int {
	n := 0
	for _, m := range publishedOn(brk, a2a.DiscoveryAgentCardsTopic("test-ns")) {
		if m.userProps["removed"] == "true" {
			n++
		}
	}
	return n
}

func waitIntakeStopped(t *testing.T, intakeDone <-chan struct{}) {
	t.Helper()
	select {
	case <-intakeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop never stopped reading the request queue")
	}
}

func waitEntered(t *testing.T, g *gatedLLM) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the task never reached the model")
	}
}

func TestStopForProcessExitLeavesRunningTaskForRedelivery(t *testing.T) {
	model := newGatedLLM()
	agent, brk := startDrainAgent(t, model)
	t.Cleanup(func() {
		close(model.release)
		agent.cancelFn()
		agent.wg.Wait()
	})
	const replyTo = "test-ns/reply/drain-running"

	acks := deliverTaskRequest(t, brk, "task-running", replyTo)
	waitEntered(t, model)
	cardsBefore := len(publishedOn(brk, a2a.DiscoveryAgentCardsTopic("test-ns")))

	const grace = 1500 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), grace)
	defer cancel()
	stopped := make(chan struct{})
	start := time.Now()
	go func() {
		_ = agent.StopForProcessExit(ctx)
		close(stopped)
	}()
	waitIntakeStopped(t, agent.requestIntakeDone)
	lateAcks := deliverTaskRequest(t, brk, "task-late", replyTo)
	<-stopped

	if elapsed := time.Since(start); elapsed < grace-100*time.Millisecond {
		t.Errorf("StopForProcessExit returned after %s with a task running, want it to wait the grace period", elapsed)
	}
	if model.taskCancelled() {
		t.Error("the running task was cancelled at the deadline")
	}
	if got := acks.Load(); got != 0 {
		t.Errorf("the running task's request was acknowledged %d times; it must stay for redelivery", got)
	}
	if got := len(publishedOn(brk, replyTo)); got != 0 {
		t.Errorf("%d answers published on the reply topic at the deadline, want none", got)
	}
	if got := lateAcks.Load(); got != 0 || len(requestQueueChannel(brk)) != 1 {
		t.Errorf("a request arriving during the drain was taken (acks=%d, left on queue=%d)", got, len(requestQueueChannel(brk)))
	}
	if got := removalCards(brk); got != 0 {
		t.Errorf("%d removal cards published at process exit, want none", got)
	}
	if cardsAfter := len(publishedOn(brk, a2a.DiscoveryAgentCardsTopic("test-ns"))); cardsAfter <= cardsBefore {
		t.Errorf("no agent card published during the drain (%d before, %d after)", cardsBefore, cardsAfter)
	}
}

func TestStopForProcessExitReturnsWhenTasksFinish(t *testing.T) {
	model := newGatedLLM()
	agent, brk := startDrainAgent(t, model)
	const replyTo = "test-ns/reply/drain-finishing"

	acks := deliverTaskRequest(t, brk, "task-finishing", replyTo)
	waitEntered(t, model)
	intakeDone := agent.requestIntakeDone
	go func() {
		<-intakeDone
		close(model.release)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := agent.StopForProcessExit(ctx); err != nil {
		t.Fatalf("StopForProcessExit: %v", err)
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("StopForProcessExit took %s, want it to return once the task finished", elapsed)
	}
	if got := acks.Load(); got != 1 {
		t.Errorf("the finished task's request was acknowledged %d times, want 1", got)
	}
	if got := len(publishedOn(brk, replyTo)); got != 1 {
		t.Errorf("%d answers published, want the finished task's one", got)
	}
	if got := removalCards(brk); got != 0 {
		t.Errorf("%d removal cards published at process exit, want none", got)
	}
}

func TestStopForProcessExitReleasesPausedCheckpointOwnership(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "exiting")
	taskID, _ := pausedAndCheckpointed(t, store, agent)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := agent.StopForProcessExit(ctx); err != nil {
		t.Fatalf("StopForProcessExit: %v", err)
	}

	owner, exists, err := store.CheckpointOwner(context.Background(), taskID)
	if err != nil {
		t.Fatalf("owner after stop: %v", err)
	}
	if !exists {
		t.Fatal("the process exit deleted the paused task's checkpoint")
	}
	if owner != "" {
		t.Errorf("owner = %q after process exit, want released", owner)
	}
}

func TestStoppingProcessDoesNotTakeBackReleasedCheckpoints(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "exiting")
	taskID, _ := pausedAndCheckpointed(t, store, agent)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := agent.StopForProcessExit(ctx); err != nil {
		t.Fatalf("StopForProcessExit: %v", err)
	}

	agent.renewOwnedLeases(context.Background(), time.Now())

	owner, _, err := store.CheckpointOwner(context.Background(), taskID)
	if err != nil {
		t.Fatalf("owner after renewal tick: %v", err)
	}
	if owner != "" {
		t.Errorf("owner = %q after a renewal tick on the stopping process, want still released", owner)
	}
	if agent.mayTakeOver(time.Now().Add(time.Hour)) {
		t.Error("a stopping process may take over other owners' tasks")
	}
}

func TestHealthRequiresRestartWhenRequestQueueIsGone(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	agent := New(AgentConfig{Name: drainAgentName, Namespace: "test-ns", Model: "test-model", MaxTokens: 1000, MaxLLMCalls: 5})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })
	if err := agent.Health(); err != nil {
		t.Fatalf("Health before the loss: %v", err)
	}

	if err := brk.DeprovisionQueue(a2a.QueueName("test-ns", "a2a", drainAgentName)); err != nil {
		t.Fatalf("deprovision: %v", err)
	}

	if !waitUntil(func() bool { return errors.Is(agent.Health(), runtime.ErrRestartRequired) }) {
		t.Fatalf("Health = %v after the request queue was deleted, want ErrRestartRequired", agent.Health())
	}
}

func TestTaskCutByProcessExitIsNotAnsweredOrAcknowledged(t *testing.T) {
	model := newGatedLLM()
	startCtx, exitProcess := context.WithCancel(t.Context())
	agent, brk := startDrainAgentUnder(startCtx, t, model)
	t.Cleanup(func() {
		close(model.release)
		agent.wg.Wait()
	})
	const replyTo = "test-ns/reply/cut-by-exit"

	acks := deliverTaskRequest(t, brk, "task-cut", replyTo)
	waitEntered(t, model)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := agent.StopForProcessExit(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	exitProcess()

	deadline := time.Now().Add(5 * time.Second)
	for requestsInProcess(agent) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := requestsInProcess(agent); n != 0 {
		t.Fatalf("%d requests still in process after the exit", n)
	}
	if got := len(publishedOn(brk, replyTo)); got != 0 {
		t.Errorf("%d answers published for a task cut by the exit, want none", got)
	}
	if got := acks.Load(); got != 0 {
		t.Errorf("the cut task's request was acknowledged %d times; it must stay for redelivery", got)
	}
	if storedCancellationMarker(agent, "task-cut") {
		t.Error("the cut task stored a cancellation marker; the redelivered request would be dropped as cancelled")
	}
}

func storedCancellationMarker(agent *Agent, taskID string) bool {
	store := agent.svc.Sessions().(*memSessionStore)
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, sess := range store.sessions {
		for _, m := range sess.Messages {
			if m.TaskID == taskID && m.Content == cancellationMarkerContent {
				return true
			}
		}
	}
	return false
}

func requestsInProcess(agent *Agent) int {
	agent.activeTasksMu.Lock()
	defer agent.activeTasksMu.Unlock()
	return len(agent.requestsInProcess)
}

func TestStopForReplaceLeavesRunningTaskForTheNewCopy(t *testing.T) {
	model := newGatedLLM()
	agent, brk := startDrainAgent(t, model)
	t.Cleanup(func() { close(model.release) })
	agent.drainTimeout = 300 * time.Millisecond
	const replyTo = "test-ns/reply/replace-running"

	acks := deliverTaskRequest(t, brk, "task-replaced", replyTo)
	waitEntered(t, model)
	if err := agent.StopForReplace(t.Context()); err != nil {
		t.Fatalf("StopForReplace: %v", err)
	}

	if got := len(publishedOn(brk, replyTo)); got != 0 {
		t.Errorf("%d answers published for a task the replace cut, want none", got)
	}
	if got := acks.Load(); got != 0 {
		t.Errorf("the cut task's request was acknowledged %d times; it must stay for the new copy", got)
	}
	if storedCancellationMarker(agent, "task-replaced") {
		t.Error("the cut task stored a cancellation marker; the redelivered request would be dropped as cancelled")
	}
	if got := removalCards(brk); got != 1 {
		t.Errorf("%d removal cards published on replace, want 1", got)
	}
}

func TestStopForReplaceLetsAFinishingTaskAnswer(t *testing.T) {
	model := newGatedLLM()
	agent, brk := startDrainAgent(t, model)
	agent.drainTimeout = 10 * time.Second
	const replyTo = "test-ns/reply/replace-finishing"

	acks := deliverTaskRequest(t, brk, "task-finishing-on-replace", replyTo)
	waitEntered(t, model)
	intakeDone := agent.requestIntakeDone
	go func() {
		<-intakeDone
		close(model.release)
	}()

	start := time.Now()
	if err := agent.StopForReplace(t.Context()); err != nil {
		t.Fatalf("StopForReplace: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("StopForReplace took %s, want it to return once the task finished", elapsed)
	}
	if got := acks.Load(); got != 1 {
		t.Errorf("the finished task's request was acknowledged %d times, want 1", got)
	}
	if got := len(publishedOn(brk, replyTo)); got != 1 {
		t.Errorf("%d answers published, want the finished task's one", got)
	}
}
