// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

func TestAnAnswerOrATimeoutAfterARepauseThatCouldNotWriteItsCheckpointStillReachesTheModel(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		answered  bool
		wantInLLM string
	}{
		{name: "the person's answer", timeout: time.Hour, answered: true, wantInLLM: "approved"},
		{name: "the wait's timeout", timeout: 200 * time.Millisecond, wantInLLM: "timed_out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := failingUpdateStore{Store: session.NewMemoryStore(), err: errors.New("the session database is unreachable")}
			logs := &syncLogBuffer{}
			agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, store, tt.timeout, capturingLogs(logs))
			if !waitUntil(func() bool { return waitingOnAPerson(agent) }) {
				t.Fatal("the task never waited on the person")
			}

			if tt.answered {
				if got := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
					t.Fatalf("delivery = %v, want delivered", got)
				}
			}

			if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
				t.Fatalf("the task did not resume with the %s: %d LLM calls", tt.name, len(mock.getCalls()))
			}
			texts := toolTextsOf(mock.getCalls()[1])
			if len(texts) != 1 || !strings.Contains(texts[0], tt.wantInLLM) || strings.Contains(texts[0], "waiting for approval") {
				t.Errorf("tool results of the resumed turn = %q, want only the %s, never the remote tool's pending reply", texts, tt.name)
			}
			if strings.Contains(logs.String(), "another replica") {
				t.Errorf("the log blames another replica, but no other replica took part:\n%s", logs.String())
			}
			if brieflyBecomes(func() bool { return len(mock.getCalls()) > 2 }) {
				t.Errorf("%d LLM calls, want the task resumed once and not paused again on a request that was already answered", len(mock.getCalls()))
			}
		})
	}
}

func TestTheMaintenanceSweepNeverReadsTheStalePendingReplyOfAnUnwrittenRepauseAsAnAnswer(t *testing.T) {
	store := failingUpdateStore{Store: session.NewMemoryStore(), err: errors.New("the session database is unreachable")}
	agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, store, 400*time.Millisecond)
	if !waitUntil(func() bool { return waitingOnAPerson(agent) }) {
		t.Fatal("the task never waited on the person")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !finishedWithAnswer(agent, mock) {
		agent.collectRecordedAnswers(t.Context())
		time.Sleep(20 * time.Millisecond)
	}

	if !finishedWithAnswer(agent, mock) {
		t.Fatalf("%d LLM calls and the task is still held = %v, want the wait to time out while the sweep runs",
			len(mock.getCalls()), agent.pausedTasks.Load(personWaitTaskID) != nil)
	}
	texts := toolTextsOf(mock.getCalls()[1])
	if len(texts) != 1 || !strings.Contains(texts[0], "timed_out") {
		t.Errorf("tool results of the resumed turn = %q, want the wait's timeout", texts)
	}
}

func TestUsingTheResultRecordedForACallWhoseCheckpointIsGoneLeavesTheCallersOwnResultInPlace(t *testing.T) {
	agent, _, _ := replicaOf(t, session.NewMemoryStore(), "owner")
	state := newTestTaskState(agent)
	state.TaskID = personWaitTaskID
	pc := &PendingToolCall{ToolCallID: "call-gone", ToolName: askPersonTool, Kind: PendingCallHILApproval, HILRequestID: "req-gone"}

	got := agent.useRecordedResult(t.Context(), state, pc)

	if got != nil || len(state.SyncResults) != 0 {
		t.Errorf("recorded result = %+v and results kept for the task = %v, want neither when the task's checkpoint is gone", got, state.SyncResults)
	}
}
