// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	invariantReplicas = 3
	invariantBaseOps  = 11
)

type pauseUnderTest struct {
	name    string
	setup   func(t *testing.T, replica *Agent)
	pause   func(t *testing.T, store session.Store, owner *Agent) *TaskExecutionState
	corrID  func(state *TaskExecutionState) string
	deliver func(t *testing.T, replica *Agent, state *TaskExecutionState)
	races   []func(w *invariantWorld)
	check   func(w *invariantWorld)
}

var pausesUnderTest = []pauseUnderTest{
	{
		name: "question",
		pause: func(t *testing.T, store session.Store, owner *Agent) *TaskExecutionState {
			return pausedOnQuestion(t, store, owner, "task-inv")
		},
		corrID: func(*TaskExecutionState) string { return askRequestID },
		deliver: func(t *testing.T, replica *Agent, state *TaskExecutionState) {
			replica.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID))
		},
	},
	{
		name: "approval",
		pause: func(t *testing.T, store session.Store, owner *Agent) *TaskExecutionState {
			return pausedOnApproval(t, store, owner, "task-inv")
		},
		corrID: func(*TaskExecutionState) string { return approvalRequestID },
		deliver: func(t *testing.T, replica *Agent, state *TaskExecutionState) {
			replica.routeUserInputResponse(t.Context(), approve(approvalRequestID, true), hilSenderUser(state.UserProps.UserID))
		},
		races: []func(w *invariantWorld){
			(*invariantWorld).expireOnTheHolder,
			(*invariantWorld).adoptAbandoned,
		},
		check: (*invariantWorld).checkApprovedCallRanIffTheModelSawItsResult,
	},
	{
		name:  "remote call",
		setup: func(t *testing.T, replica *Agent) { registerRemoteTool(t, replica, strBroker(t)) },
		pause: func(t *testing.T, store session.Store, owner *Agent) *TaskExecutionState {
			return pausedOnLiveRemoteCall(t, store, owner, "task-inv", remoteInvariantCorrID)
		},
		corrID: func(*TaskExecutionState) string { return remoteInvariantCorrID },
		deliver: func(t *testing.T, replica *Agent, state *TaskExecutionState) {
			if replica.pausedTasks.Load(state.TaskID) == state {
				holdersDelivery(state)(remoteReply("report ready"))
				return
			}
			replica.adoptOrphanReply(replica.remoteToolReplyAdoption(remoteInvariantCorrID, remoteToolReplyPayload(t, remoteInvariantCorrID, "report ready")))
		},
		races: []func(w *invariantWorld){
			(*invariantWorld).expireTheRemoteCallOnTheHolder,
			(*invariantWorld).adoptAbandoned,
		},
	},
}

const (
	remoteInvariantCorrID  = "corr-inv-remote"
	remoteWaitInHarness    = 2 * time.Minute
	remoteTimeoutInHarness = "SANDBOX_TIMEOUT: the remote call timed out"
)

var holderDeliveries sync.Map

func holdersDelivery(state *TaskExecutionState) func(*tool.Result) {
	deliver, _ := holderDeliveries.Load(state)
	return deliver.(func(*tool.Result))
}

func remoteReply(text string) *tool.Result { return tool.OK(text) }

func pausedOnLiveRemoteCall(t *testing.T, store session.Store, owner *Agent, taskID, corrID string) *TaskExecutionState {
	t.Helper()
	state := pausedOnPeerCall(t, store, taskID, corrID)
	state.Messages[1] = llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: remoteReportTool}}}
	state.PausedAssistantMsg = state.Messages[1]
	pc := state.PendingCalls["tc-1"]
	pc.ToolName = remoteReportTool
	pc.Kind = PendingCallRemoteTool
	pc.Timeout = remoteWaitInHarness
	if err := owner.checkpoints.save(t.Context(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)
	var deliver func(*tool.Result)
	pending := tool.Pending("waiting for the STR")
	pending.OnDelivery = func(d func(*tool.Result)) { deliver = d }
	owner.wireDelivery(t.Context(), state, armedPendingCall{pc: pc, result: pending}, &tool.Context{}, time.Now(), owner.logger)
	state.armPublishHandoff()
	state.passPublishHandoff()
	holderDeliveries.Store(state, deliver)
	t.Cleanup(func() { holderDeliveries.Delete(state) })
	pc.TimeoutTimer = time.AfterFunc(remoteWaitInHarness, func() { deliver(tool.Error(remoteTimeoutInHarness)) })
	return state
}

type invariantWorld struct {
	t         *testing.T
	rng       *rand.Rand
	store     session.Store
	pause     pauseUnderTest
	state     *TaskExecutionState
	replicas  []*Agent
	llms      []*mockLLM
	brokers   []*mockBroker
	runs      []*approvalRuns
	dead      []bool
	fannedOut []int
	now       time.Time
	trace     []string
	cancels   int
	killed    []*TaskExecutionState

	answerAcked bool
}

func newInvariantWorld(t *testing.T, seed uint64, pause pauseUnderTest) *invariantWorld {
	w := &invariantWorld{
		t: t, rng: rand.New(rand.NewPCG(seed, seed)), store: session.NewMemoryStore(), pause: pause,
		dead: make([]bool, invariantReplicas), fannedOut: make([]int, invariantReplicas), now: time.Now(),
	}
	for i := range invariantReplicas {
		replica, mock, brk, runs := approvalReplica(t, w.store, fmt.Sprintf("replica-%d", i), approvalWaitInHarness)
		w.replicas = append(w.replicas, replica)
		w.llms = append(w.llms, mock)
		w.brokers = append(w.brokers, brk)
		w.runs = append(w.runs, runs)
		if pause.setup != nil {
			pause.setup(t, replica)
		}
	}
	w.state = pause.pause(t, w.store, w.replicas[0])
	for _, pc := range w.state.PendingCalls {
		if pc.Kind.waitsOnPerson() {
			pc.TimeoutTimer = w.replicas[0].armHILTimeout(w.state.TaskID, pc)
		}
	}
	return w
}

func (w *invariantWorld) live() []int {
	var live []int
	for i, dead := range w.dead {
		if !dead {
			live = append(live, i)
		}
	}
	return live
}

func (w *invariantWorld) anyLive() int {
	live := w.live()
	return live[w.rng.IntN(len(live))]
}

func (w *invariantWorld) step() {
	switch op := w.rng.IntN(invariantBaseOps + len(w.pause.races)); {
	case op >= invariantBaseOps:
		w.raceWithAnAnswer(w.pause.races[op-invariantBaseOps])
	case op < 3:
		w.answer()
	case op < 5:
		w.fanOutWakeUps(w.rng.IntN(3) == 0)
	case op < 8:
		w.tick(time.Duration(w.rng.IntN(60)) * time.Second)
	case op < 9:
		w.killHolder()
	case op < 10:
		w.killHolderAfterAnswer()
	default:
		r := w.anyLive()
		w.trace = append(w.trace, fmt.Sprintf("cancel@%d", r))
		w.cancels++
		w.replicas[r].handleCancelRequest(&a2a.Request{ID: fmt.Sprintf("req-cancel-%d", w.cancels), Params: map[string]any{"id": w.state.TaskID}})
	}
	w.checkOneHolder()
}

func (w *invariantWorld) answer() {
	if w.answerAcked {
		return
	}
	r := w.anyLive()
	w.trace = append(w.trace, fmt.Sprintf("answer@%d", r))
	w.pause.deliver(w.t, w.replicas[r], w.state)
}

func (w *invariantWorld) fanOutWakeUps(lose bool) {
	topic := a2a.NotifyResponseTopic("test-ns", "test-agent", w.pause.corrID(w.state))
	for i, brk := range w.brokers {
		published := brk.getPublished()
		for _, m := range published[w.fannedOut[i]:] {
			if m.topic != topic {
				continue
			}
			w.trace = append(w.trace, fmt.Sprintf("wake-up from %d lost=%v", i, lose))
			if lose {
				continue
			}
			for _, r := range w.live() {
				w.replicas[r].handleNotifyHILResponse(&broker.Message{Topic: topic, Payload: m.payload})
			}
		}
		w.fannedOut[i] = len(published)
	}
}

func (w *invariantWorld) tick(advance time.Duration) {
	waitUntilWithin(2*time.Second, w.noTurnRunning)
	w.now = w.now.Add(advance)
	w.trace = append(w.trace, fmt.Sprintf("tick+%s", advance))
	ctx := context.Background()
	live := w.live()
	for _, r := range live {
		w.replicas[r].renewOwnedLeases(ctx, w.now)
	}
	w.rng.Shuffle(len(live), func(i, j int) { live[i], live[j] = live[j], live[i] })
	for _, r := range live {
		replica := w.replicas[r]
		replica.collectRecordedAnswers(ctx)
		replica.reapExpiredPendingCalls(ctx, w.now)
		replica.resumeAbandonedCheckpoints(ctx, w.now)
	}
}

func (w *invariantWorld) noTurnRunning() bool {
	for _, replica := range w.replicas {
		replica.activeTasksMu.Lock()
		_, running := replica.activeTasks[w.state.TaskID]
		replica.activeTasksMu.Unlock()
		if running {
			return false
		}
	}
	return true
}

func (w *invariantWorld) killHolder() {
	for _, r := range w.live() {
		state := w.replicas[r].pausedTasks.Load(w.state.TaskID)
		if state == nil || atomic.LoadInt32(&state.TotalPending) == 0 || len(w.live()) == 1 {
			continue
		}
		w.trace = append(w.trace, fmt.Sprintf("kill %d", r))
		w.die(r, state)
		return
	}
}

func (w *invariantWorld) killHolderAfterAnswer() {
	for _, r := range w.live() {
		state := w.replicas[r].pausedTasks.Load(w.state.TaskID)
		if state == nil || atomic.LoadInt32(&state.TotalPending) == 0 || len(w.live()) == 1 {
			continue
		}
		w.trace = append(w.trace, fmt.Sprintf("answer@%d then kill", r))
		prev := resumeTaskHook
		resumeTaskHook = func(*Agent, *TaskExecutionState) {}
		w.pause.deliver(w.t, w.replicas[r], w.state)
		resumeTaskHook = prev
		w.answerAcked = true
		w.die(r, state)
		return
	}
}

func (w *invariantWorld) die(r int, state *TaskExecutionState) {
	w.replicas[r].pausedTasks.Delete(w.state.TaskID)
	w.dead[r] = true
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, pc := range state.PendingCalls {
		if pc.TimeoutTimer != nil {
			pc.TimeoutTimer.Stop()
		}
	}
	w.killed = append(w.killed, state)
}

func (w *invariantWorld) checkOneHolder() {
	holders := 0
	for _, r := range w.live() {
		if w.replicas[r].pausedTasks.Load(w.state.TaskID) != nil {
			holders++
		}
	}
	if holders > 1 {
		w.t.Errorf("invariant 3: %d live replicas hold the task at once\ntrace: %v", holders, w.trace)
	}
}

func (w *invariantWorld) drain() {
	w.answer()
	w.shortenTakeoverWindow()
	for range 4 {
		w.fanOutWakeUps(false)
		time.Sleep(drainTakeoverWindow + drainTakeoverWindow/2)
		w.tick(checkpointLease + checkpointClockSkewAllowance + time.Second)
		if waitUntilWithin(500*time.Millisecond, func() bool { return len(w.terminalResponses()) > 0 }) {
			break
		}
	}
	brieflyBecomes(func() bool { return len(w.terminalResponses()) > 1 })
}

const drainTakeoverWindow = 100 * time.Millisecond

func (w *invariantWorld) shortenTakeoverWindow() {
	waitUntilWithin(2*time.Second, w.noTurnRunning)
	for _, replica := range w.replicas {
		replica.checkpoints.takeoverAfter = drainTakeoverWindow
	}
}

func (w *invariantWorld) terminalResponses() []map[string]any {
	var responses []map[string]any
	for _, brk := range w.brokers {
		for _, m := range brk.getPublished() {
			if m.topic != resumedReplyTopic {
				continue
			}
			var resp map[string]any
			if json.Unmarshal(m.payload, &resp) == nil {
				responses = append(responses, resp)
			}
		}
	}
	return responses
}

func (w *invariantWorld) describeResponses() []string {
	var described []string
	for i, brk := range w.brokers {
		for _, m := range brk.getPublished() {
			if m.topic != resumedReplyTopic {
				continue
			}
			var resp struct {
				Result struct {
					Status struct {
						State string `json:"state"`
					} `json:"status"`
				} `json:"result"`
			}
			_ = json.Unmarshal(m.payload, &resp)
			described = append(described, fmt.Sprintf("replica %d: %s", i, resp.Result.Status.State))
		}
	}
	return described
}

func (w *invariantWorld) checkInvariants() {
	t := w.t
	t.Helper()

	responses := w.terminalResponses()
	if len(responses) != 1 {
		t.Errorf("invariants 1 and 5: %d terminal responses, want exactly 1\ntrace: %v\nresponses: %v", len(responses), w.trace, w.describeResponses())
	}

	turns := 0
	for _, mock := range w.llms {
		calls := mock.getCalls()
		if len(calls) > 0 {
			turns++
		}
		for _, req := range calls {
			checkEveryToolUseHasOneResult(t, req.Messages, w.trace)
		}
	}
	if turns > 1 {
		t.Errorf("invariant 3: the resumed turn ran on %d replicas\ntrace: %v", turns, w.trace)
	}
	ran := int32(0)
	for _, runs := range w.runs {
		ran += runs.Load()
	}
	if ran > 1 {
		t.Errorf("the call waiting on the user ran %d times\ntrace: %v", ran, w.trace)
	}

	ctx := context.Background()
	cp, err := w.store.GetCheckpoint(ctx, w.state.TaskID)
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	row, err := w.store.LookupPendingCall(ctx, "test-agent", w.pause.corrID(w.state))
	if err != nil {
		t.Fatalf("correlation: %v", err)
	}
	if cp != nil && cp.PendingCount == 0 && row != nil {
		t.Errorf("invariant 4: pending_count is 0 but the correlation row remains\ntrace: %v", w.trace)
	}
	if cp != nil && cp.PendingCount > 0 && row == nil {
		t.Errorf("invariant 4: pending_count is %d but no correlation row remains\ntrace: %v", cp.PendingCount, w.trace)
	}
	if len(responses) == 1 && cp != nil {
		t.Errorf("the task ended but its checkpoint (owner %q, pending %d) was left behind\ntrace: %v", cp.OwnerID, cp.PendingCount, w.trace)
	}
	for _, state := range w.killed {
		state.mu.Lock()
		for _, pc := range state.PendingCalls {
			if pc.TimeoutTimer != nil && pc.TimeoutTimer.Stop() {
				t.Errorf("a killed holder still had a running timer for %s\ntrace: %v", pc.ToolCallID, w.trace)
			}
		}
		state.mu.Unlock()
	}
	if w.pause.check != nil {
		w.pause.check(w)
	}
}

const approvalWaitInHarness = 2 * time.Minute

func (w *invariantWorld) raceWithAnAnswer(op func(w *invariantWorld)) {
	r := w.anyLive()
	w.trace = append(w.trace, fmt.Sprintf("race answer@%d", r))
	var wg sync.WaitGroup
	if !w.answerAcked {
		wg.Go(func() { w.pause.deliver(w.t, w.replicas[r], w.state) })
	}
	wg.Go(func() { op(w) })
	wg.Wait()
}

func (w *invariantWorld) expireOnTheHolder() {
	for _, r := range w.live() {
		if w.replicas[r].pausedTasks.Load(w.state.TaskID) == nil {
			continue
		}
		w.trace = append(w.trace, fmt.Sprintf("expire@%d", r))
		w.replicas[r].handlePendingHILTimeout(w.state.TaskID, "tc-1")
	}
}

func (w *invariantWorld) expireTheRemoteCallOnTheHolder() {
	for _, r := range w.live() {
		if w.replicas[r].pausedTasks.Load(w.state.TaskID) != w.state {
			continue
		}
		w.trace = append(w.trace, fmt.Sprintf("remote timeout@%d", r))
		holdersDelivery(w.state)(tool.Error(remoteTimeoutInHarness))
		return
	}
}

func (w *invariantWorld) adoptAbandoned() {
	r := w.anyLive()
	w.trace = append(w.trace, fmt.Sprintf("adopt@%d", r))
	w.replicas[r].resumeAbandonedCheckpoints(context.Background(), w.now)
}

func (w *invariantWorld) checkApprovedCallRanIffTheModelSawItsResult() {
	ran := int32(0)
	for _, runs := range w.runs {
		ran += runs.Load()
	}
	sawResult, sawRefusal := false, false
	for _, mock := range w.llms {
		for _, req := range mock.getCalls() {
			for _, m := range req.Messages {
				if m.Role != "tool" || m.ToolCallID != "tc-1" {
					continue
				}
				sawResult = sawResult || strings.Contains(m.Content, approvalToolOut)
				sawRefusal = sawRefusal || strings.Contains(m.Content, "Tool approval") || strings.Contains(m.Content, "cancel")
			}
		}
	}
	if sawResult && ran != 1 {
		w.t.Errorf("the model saw the approved call's result but the call ran %d times\ntrace: %v", ran, w.trace)
	}
	if sawRefusal && ran != 0 {
		w.t.Errorf("the model was told the call was refused but it ran %d times\ntrace: %v", ran, w.trace)
	}
	if !sawResult && ran != 0 && w.cancels == 0 {
		w.t.Errorf("the call ran %d times but the model never saw its result, and nothing cancelled the task\ntrace: %v", ran, w.trace)
	}
	if sawResult && sawRefusal {
		w.t.Errorf("the model saw the call both run and refused\ntrace: %v", w.trace)
	}
}

func checkEveryToolUseHasOneResult(t *testing.T, messages []llm.ChatMessage, trace []string) {
	t.Helper()
	results := map[string]int{}
	for _, m := range messages {
		if m.Role == "tool" {
			results[m.ToolCallID]++
		}
	}
	for _, m := range messages {
		for _, tc := range m.ToolCalls {
			if results[tc.ID] != 1 {
				t.Errorf("invariant 2: tool_use %s has %d tool_results\ntrace: %v", tc.ID, results[tc.ID], trace)
			}
		}
	}
}

func TestPausedOnTheUserInvariantsHoldUnderRandomInterleavings(t *testing.T) {
	seeds := 40
	if testing.Short() {
		seeds = 5
	}
	for _, pause := range pausesUnderTest {
		for seed := range uint64(seeds) {
			t.Run(fmt.Sprintf("%s/seed-%d", pause.name, seed), func(t *testing.T) {
				w := newInvariantWorld(t, seed, pause)
				for range 4 + w.rng.IntN(8) {
					w.step()
				}
				w.drain()
				w.checkInvariants()
			})
		}
	}
}
