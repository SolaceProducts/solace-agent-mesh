// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func earlySlotCount(a *Agent) int {
	a.early.mu.Lock()
	defer a.early.mu.Unlock()
	return len(a.early.slots)
}

func answerFor(requestID string) *a2a.UserInputResponseData {
	return &a2a.UserInputResponseData{Type: "user_input_response", RequestID: requestID, Status: "answered", Data: map[string]any{"decision": "approved"}}
}

type manualClock struct{ now time.Time }

func (c *manualClock) Now() time.Time { return c.now }

func TestEarlyAnswerSlotsOfTwoTasksUsingOneRequestIdAreKeptApart(t *testing.T) {
	var slots earlyAnswers
	slots.expect("task-a", "req-shared", "u1", time.Hour)
	slots.expect("task-b", "req-shared", "u1", time.Hour)

	slots.stashFrom(answerFor("req-shared"), hilSenderUser("u1"))

	type held struct{ A, B int }
	taken := func(taskID string) int {
		n := 0
		for _, s := range slots.takeTask(taskID) {
			if s.slot.answer != nil {
				n++
			}
		}
		return n
	}
	if diff := cmp.Diff(held{A: 1, B: 1}, held{A: taken("task-a"), B: taken("task-b")}); diff != "" {
		t.Errorf("answers held for each task (-want +got):\n%s", diff)
	}
}

func TestOpeningTheSlotOfATaskAndRequestIdAgainKeepsTheAnswerAlreadyHeldInIt(t *testing.T) {
	var slots earlyAnswers
	slots.expect("task-a", "req-twice", "u1", time.Hour)
	slots.stashFrom(answerFor("req-twice"), hilSenderUser("u1"))

	slots.expect("task-a", "req-twice", "u1", time.Hour)

	held := 0
	for _, s := range slots.takeTask("task-a") {
		if s.slot.answer != nil {
			held++
		}
	}
	if held != 1 {
		t.Errorf("answers held in the slot after it was opened again = %d, want the answer kept", held)
	}
}

func TestOpeningTheSlotOfATaskAndRequestIdAgainNeverShortensItsLife(t *testing.T) {
	tests := []struct {
		name   string
		first  time.Duration
		second time.Duration
	}{
		{name: "opened again for a longer wait", first: time.Minute, second: time.Hour},
		{name: "opened again for a shorter wait", first: time.Hour, second: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var slots earlyAnswers
			clock := &manualClock{now: time.Now()}
			slots.now = clock.Now
			slots.expect("task-a", "req-reopened", "u1", tt.first)
			slots.expect("task-a", "req-reopened", "u1", tt.second)
			clock.now = clock.now.Add(30 * time.Minute)

			_, found, refused := slots.stashFrom(answerFor("req-reopened"), hilSenderUser("u1"))

			if !found || refused {
				t.Errorf("answer 30 minutes later: found = %v, refused = %v, want it held by the slot that lives for the longer of the two waits", found, refused)
			}
		})
	}
}

func TestOpeningTheSlotOfATaskAndRequestIdAgainKeepsTheOwnerWhoFirstOpenedIt(t *testing.T) {
	type outcome struct{ Found, Refused bool }
	tests := []struct {
		name   string
		sender string
		want   outcome
	}{
		{name: "an answer from the first owner is held", sender: "u1", want: outcome{Found: true}},
		{name: "an answer from the user who opened it again is refused", sender: "u2", want: outcome{Found: true, Refused: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var slots earlyAnswers
			slots.expect("task-a", "req-owned", "u1", time.Hour)
			slots.expect("task-a", "req-owned", "u2", time.Hour)

			_, found, refused := slots.stashFrom(answerFor("req-owned"), hilSenderUser(tt.sender))

			if diff := cmp.Diff(tt.want, outcome{Found: found, Refused: refused}); diff != "" {
				t.Errorf("answer from %s (-want +got):\n%s", tt.sender, diff)
			}
		})
	}
}

func TestAnEarlyAnswerSlotLivesAsLongAsTheWaitItIsForPlusAGrace(t *testing.T) {
	tests := []struct {
		name  string
		after time.Duration
		want  hilDelivery
	}{
		{name: "an answer within the wait is held", after: 10 * time.Minute, want: hilDelivered},
		{name: "an answer within the grace after the wait is held", after: 10*time.Minute + earlyAnswerGrace, want: hilDelivered},
		{name: "an answer after the grace is not held and the slot is gone", after: 10*time.Minute + earlyAnswerGrace + time.Second, want: hilUnmatched},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			clock := &manualClock{now: time.Now()}
			agent.early.now = clock.Now
			agent.expectEarlyAnswer("task-a", "req-a", "u1", 10*time.Minute)
			clock.now = clock.now.Add(tt.after)

			got := agent.stashEarlyAnswer(answerFor("req-a"), hilSenderUser("u1"))

			if got != tt.want {
				t.Errorf("stashEarlyAnswer() = %v, want %v", got, tt.want)
			}
			if tt.want == hilUnmatched && earlySlotCount(agent) != 0 {
				t.Errorf("%d slots left after the wait it was for ended, want none", earlySlotCount(agent))
			}
		})
	}
}

func TestAnExpiredEarlyAnswerSlotIsDroppedWhenAnotherIsOpened(t *testing.T) {
	agent, _, _ := newTestAgent()
	clock := &manualClock{now: time.Now()}
	agent.early.now = clock.Now
	agent.expectEarlyAnswer("task-gone", "req-gone", "u1", time.Minute)
	clock.now = clock.now.Add(time.Minute + earlyAnswerGrace + time.Second)

	agent.expectEarlyAnswer("task-b", "req-b", "u1", time.Minute)

	if got := earlySlotCount(agent); got != 1 {
		t.Errorf("%d slots, want only the one just opened: a slot nothing ended must still expire", got)
	}
}

func TestAnExpiredEarlyAnswerSlotIsDroppedUnlessItHoldsAnAnswer(t *testing.T) {
	sweeps := map[string]func(*earlyAnswers){
		"another slot is opened": func(s *earlyAnswers) { s.expect("task-b", "req-b", "u1", time.Hour) },
		"an answer is stashed":   func(s *earlyAnswers) { s.stashFrom(answerFor("req-b"), hilSenderUser("u1")) },
	}
	tests := []struct {
		name     string
		answered bool
		sweep    string
		want     int
	}{
		{name: "an unanswered slot goes when another is opened", sweep: "another slot is opened", want: 0},
		{name: "an unanswered slot goes when an answer is stashed", sweep: "an answer is stashed", want: 0},
		{name: "an answered slot stays when another is opened", answered: true, sweep: "another slot is opened", want: 1},
		{name: "an answered slot stays when an answer is stashed", answered: true, sweep: "an answer is stashed", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var slots earlyAnswers
			clock := &manualClock{now: time.Now()}
			slots.now = clock.Now
			slots.expect("task-a", "req-a", "u1", 5*time.Minute)
			if tt.answered {
				slots.stashFrom(answerFor("req-a"), hilSenderUser("u1"))
			}
			clock.now = clock.now.Add(2 * time.Hour)

			sweeps[tt.sweep](&slots)

			if got := len(slots.takeRequests("task-a", []string{"req-a"})); got != tt.want {
				t.Errorf("slots of task-a left after its wait expired and %s = %d, want %d", tt.sweep, got, tt.want)
			}
		})
	}
}

func TestAnAnswerHeldInAnEarlySlotSurvivesALongSiblingWaitInTheSameBatch(t *testing.T) {
	var slots earlyAnswers
	clock := &manualClock{now: time.Now()}
	slots.now = clock.Now
	slots.expect("task-a", "req-a", "u1", 5*time.Minute)
	slots.stashFrom(answerFor("req-a"), hilSenderUser("u1"))
	clock.now = clock.now.Add(2 * time.Hour)
	slots.expect("task-b", "req-b", "u2", 24*time.Hour)

	taken := slots.takeRequests("task-a", []string{"req-a"})

	if len(taken) != 1 || taken[0].slot.answer == nil || taken[0].slot.answer.RequestID != "req-a" {
		t.Errorf("slots taken for task-a = %+v, want the answer u1 gave in time, kept while another task's slot was opened two hours later", taken)
	}
}

func TestAnAnsweredEarlySlotThatOutlivedItsExpiryIsRemovedWhenItsTaskEnds(t *testing.T) {
	agent, _, _ := newTestAgent()
	clock := &manualClock{now: time.Now()}
	agent.early.now = clock.Now
	agent.expectEarlyAnswer("task-a", "req-a", "u1", 5*time.Minute)
	agent.stashEarlyAnswer(answerFor("req-a"), hilSenderUser("u1"))
	clock.now = clock.now.Add(2 * time.Hour)
	agent.expectEarlyAnswer("task-b", "req-b", "u1", time.Hour)

	agent.forgetEarlyAnswers("task-a")

	if got := earlySlotCount(agent); got != 1 {
		t.Errorf("%d slots after task-a ended, want only task-b's: an answered slot must not outlive its task", got)
	}
}

func TestAnEarlyAnswerIsAppliedOnlyToTheTaskItsSlotBelongsTo(t *testing.T) {
	agent, _, _ := newTestAgent()
	resumes := 0
	useResumeHook(t, func(*Agent, *TaskExecutionState) { resumes++ })
	other := pausedStateWithHILEntry(t, agent, "task-other", "call-other", "req-shared")
	agent.expectEarlyAnswer("task-slot", "req-shared", other.UserProps.UserID, time.Hour)

	if got := agent.stashEarlyAnswer(answerFor("req-shared"), hilSenderUser(other.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("stashEarlyAnswer() = %v, want the answer held for the slot's task", got)
	}
	agent.applyEarlyAnswers("task-slot", []string{"req-shared"})

	other.mu.Lock()
	stillWaiting := other.PendingCalls["call-other"] != nil
	other.mu.Unlock()
	if !stillWaiting || resumes != 0 {
		t.Errorf("task-other still waiting = %v and resumed %d times, want it left waiting: another task's early answer is not its answer", stillWaiting, resumes)
	}
}

func TestEarlyAnswerSlotsAreOpenedOnlyForAPersonsRequestWithAUsableId(t *testing.T) {
	tests := []struct {
		name      string
		delivered *tool.Result
		want      int
	}{
		{name: "a person's request with an id", delivered: personRequestResult("req-ok"), want: 1},
		{name: "a person's request without an id", delivered: personRequestResult(""), want: 0},
		{name: "a person's request with an id over 256 bytes", delivered: personRequestResult(string(make([]byte, 257))), want: 0},
		{name: "a plain result", delivered: tool.OK("done"), want: 0},
		{name: "a pending result of another kind", delivered: &tool.Result{Status: tool.StatusPending, Data: map[string]any{"pending_kind": "workflow", "hil_request_id": "req-ok"}}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			state := newTestTaskState(agent)

			agent.expectDeliveredPersonAnswer(state, tt.delivered)

			if got := earlySlotCount(agent); got != tt.want {
				t.Errorf("early answer slots = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestARepauseThatRefusesEveryWaitDropsTheEarlySlotBeforeTheResumedTurnRuns(t *testing.T) {
	store := session.NewMemoryStore()
	seedTaskHoldingARequestID(t, store, heldElsewhereID)
	var slotsDuringResumedTurn atomic.Int32
	slotsDuringResumedTurn.Store(-1)
	agent, mock := pausedOnABatch(t, store, []batchCall{
		{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonWithID(heldElsewhereID)},
	})
	mock.preCallHook = func(callIndex int) {
		if callIndex == 1 {
			slotsDuringResumedTurn.Store(int32(earlySlotCount(agent)))
		}
	}

	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the task did not finish: %d LLM calls", len(mock.getCalls()))
	}

	if got := slotsDuringResumedTurn.Load(); got != 0 {
		t.Errorf("early answer slots while the resumed turn ran = %d, want 0: the refused request's slot is dropped at once, not when the task ends", got)
	}
}

func TestRefusingAWaitDropsTheEarlySlotOfItsRequestAndKeepsTheOthers(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.PendingCalls["call-refused"] = &PendingToolCall{ToolCallID: "call-refused", Kind: PendingCallHILApproval, HILRequestID: "req-refused", Index: 0}
	state.PendingCalls["call-kept"] = &PendingToolCall{ToolCallID: "call-kept", Kind: PendingCallHILApproval, HILRequestID: "req-kept", Index: 1}
	state.TotalPending = 2
	agent.expectEarlyAnswer(state.TaskID, "req-refused", "u1", time.Hour)
	agent.expectEarlyAnswer(state.TaskID, "req-kept", "u1", time.Hour)

	agent.refuseTakenWait(state, "req-refused")

	if got := earlySlotCount(agent); got != 1 {
		t.Errorf("early answer slots after refusing one of two waits = %d, want 1", got)
	}
}

func agentHoldingARefusedWaitWithAnEarlySlot(t *testing.T) (*Agent, *TaskExecutionState, *syncLogBuffer) {
	t.Helper()
	agent, _, _ := newTestAgent()
	logs := captureAgentLogs(agent)
	state := newTestTaskState(agent)
	state.UserProps.TraceID = "trace-refused"
	state.PendingCalls["call-refused"] = &PendingToolCall{ToolCallID: "call-refused", Kind: PendingCallHILApproval, HILRequestID: "req-refused", Index: 0}
	state.TotalPending = 1
	agent.expectEarlyAnswer(state.TaskID, "req-refused", "u1", time.Hour)
	return agent, state, logs
}

const droppedEarlyAnswerLog = "dropping an early answer because its request was refused"

func TestRefusingAWaitWarnsOfTheEarlyAnswerItDropsWithoutNamingTheRequest(t *testing.T) {
	agent, state, logs := agentHoldingARefusedWaitWithAnEarlySlot(t)
	agent.stashEarlyAnswer(answerFor("req-refused"), hilSenderUser("u1"))

	agent.refuseTakenWait(state, "req-refused")

	records := logs.records(droppedEarlyAnswerLog)
	if len(records) != 1 {
		t.Fatalf("%q log lines = %d, want 1", droppedEarlyAnswerLog, len(records))
	}
	if records[0]["level"] != "WARN" || records[0]["traceID"] != "trace-refused" || records[0]["taskID"] != state.TaskID {
		t.Errorf("log line = %v, want a WARN carrying traceID %q and taskID %q", records[0], "trace-refused", state.TaskID)
	}
	if strings.Contains(logs.String(), "req-refused") {
		t.Errorf("the log carries the request id:\n%s", logs.String())
	}
}

func TestRefusingAWaitThatHeldNoEarlyAnswerLogsNoDrop(t *testing.T) {
	agent, state, logs := agentHoldingARefusedWaitWithAnEarlySlot(t)

	agent.refuseTakenWait(state, "req-refused")

	if got := len(logs.records(droppedEarlyAnswerLog)); got != 0 {
		t.Errorf("%q log lines = %d, want 0", droppedEarlyAnswerLog, got)
	}
}

func personRequestResult(requestID string) *tool.Result {
	return &tool.Result{Status: tool.StatusPending, Data: map[string]any{"pending_kind": "hil_approval", "hil_request_id": requestID}}
}

func endTask(a *Agent, s *TaskExecutionState, how string) {
	switch how {
	case "forgotten":
		a.forgetPausedTask(s.TaskID)
	case "cancelled from elsewhere":
		a.cancelPausedTask(s, cancelFromElsewhere)
	case "cancelled by its owning turn":
		a.cancelPausedTask(s, cancelFromOwningTurn)
	case "taken over by another process":
		a.standDownLostTask(s)
	}
}

func TestEveryEndOfATaskDropsTheEarlySlotsItOpened(t *testing.T) {
	for _, how := range []string{"forgotten", "cancelled from elsewhere", "cancelled by its owning turn", "taken over by another process"} {
		t.Run(how, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			state := pausedStateWithHILEntry(t, agent, "task-ends", "call-ends", "req-ends")
			agent.expectEarlyAnswer(state.TaskID, "req-ends", state.UserProps.UserID, time.Hour)

			endTask(agent, state, how)

			if !waitUntil(func() bool { return earlySlotCount(agent) == 0 }) {
				t.Errorf("%d early answer slots left after the task was %s", earlySlotCount(agent), how)
			}
		})
	}
}
