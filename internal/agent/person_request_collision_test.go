// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func askingForAPersonWithID(requestID string) func(corrID string) []byte {
	return strReplyWith("pending", map[string]any{
		"pending_kind":   "hil_approval",
		"hil_request_id": requestID,
	})
}

type checkpointWrites struct {
	session.Store

	mu      sync.Mutex
	saves   int
	updates int
	deletes int
}

func (s *checkpointWrites) SaveCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	s.mu.Lock()
	if cp.TaskID == personWaitTaskID {
		s.saves++
	}
	s.mu.Unlock()
	return s.Store.SaveCheckpoint(ctx, cp)
}

func (s *checkpointWrites) UpdateCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	s.mu.Lock()
	if cp.TaskID == personWaitTaskID {
		s.updates++
	}
	s.mu.Unlock()
	return s.Store.UpdateCheckpoint(ctx, cp)
}

func (s *checkpointWrites) DeleteCheckpointIfOwned(ctx context.Context, taskID, ownerID string) (bool, error) {
	s.mu.Lock()
	if taskID == personWaitTaskID {
		s.deletes++
	}
	s.mu.Unlock()
	return s.Store.DeleteCheckpointIfOwned(ctx, taskID, ownerID)
}

func (s *checkpointWrites) counts() (saves, updates, deletes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves, s.updates, s.deletes
}

func TestARefusedPersonWaitCostsTheTaskNoCheckpointAndAnotherReplicaCompletesIt(t *testing.T) {
	store := &checkpointWrites{Store: session.NewMemoryStore()}
	seedTaskHoldingARequestID(t, store, heldElsewhereID)
	holder, _ := pausedOnABatch(t, store, []batchCall{
		{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonSTRReply(0)},
		{id: siblingCall, tool: siblingRemoteTool, reply: askingForAPersonWithID(heldElsewhereID)},
	})
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	if !waitUntil(func() bool { return waitingOnAPerson(holder) }) {
		t.Fatal("the task never waited on the person")
	}

	saves, updates, deletes := store.counts()
	if got := [3]int{saves, updates, deletes}; got != [3]int{1, 1, 0} {
		t.Errorf("checkpoint saves, updates and deletes = %v, want [1 1 0]: the refusal must not delete the checkpoint and write it again", got)
	}
	holder.pausedTasks.Delete(personWaitTaskID)
	ownerDies(t, store, personWaitTaskID)

	if got := survivor.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("delivery to the replica that does not hold the task = %v, want delivered", got)
	}

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, personWaitTaskID, "approved")
	want := []string{"the person's answer", "a refusal: the request id is in use"}
	if diff := cmp.Diff(want, labelledToolTexts(survivorLLM.getCalls()[0])); diff != "" {
		t.Errorf("tool results the adopting replica gave the model (-want +got):\n%s", diff)
	}
	if row, err := store.LookupPendingCall(t.Context(), "test-agent", heldElsewhereID); err != nil || row == nil || row.TaskID != victimTaskID {
		t.Errorf("the other task's correlation row = %+v (err %v), want it left alone", row, err)
	}
}

func TestARepauseWhoseEveryWaitIsRefusedKeepsTheCheckpointForTheResumedTurn(t *testing.T) {
	store := session.NewMemoryStore()
	seedTaskHoldingARequestID(t, store, heldElsewhereID)
	var checkpointDuringResumedTurn atomic.Bool
	agent, mock := pausedOnABatch(t, store, []batchCall{
		{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonWithID(heldElsewhereID)},
	})
	mock.preCallHook = func(callIndex int) {
		if cp, err := store.GetCheckpoint(context.Background(), personWaitTaskID); callIndex == 1 && err == nil && cp != nil {
			checkpointDuringResumedTurn.Store(true)
		}
	}

	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the task did not finish: %d LLM calls", len(mock.getCalls()))
	}

	if !checkpointDuringResumedTurn.Load() {
		t.Error("the resumed turn ran with no checkpoint, so a crash during it would lose the task")
	}
	want := []string{"a refusal: the request id is in use"}
	if diff := cmp.Diff(want, labelledToolTexts(mock.getCalls()[1])); diff != "" {
		t.Errorf("tool results of the resumed turn (-want +got):\n%s", diff)
	}
}

type takenForAnUnrelatedID struct {
	session.Store

	attempts atomic.Int32
}

func (s *takenForAnUnrelatedID) refuse(cp *session.TaskCheckpoint) error {
	for _, c := range cp.PendingCalls {
		if c.Kind == session.PendingKindHILApproval && s.attempts.Add(1) <= 3 {
			return &session.CorrelationTakenError{CorrID: "an-id-no-wait-has"}
		}
	}
	return nil
}

func (s *takenForAnUnrelatedID) SaveCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if err := s.refuse(cp); err != nil {
		return err
	}
	return s.Store.SaveCheckpoint(ctx, cp)
}

func (s *takenForAnUnrelatedID) UpdateCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if err := s.refuse(cp); err != nil {
		return err
	}
	return s.Store.UpdateCheckpoint(ctx, cp)
}

func TestARepauseTheStoreRefusesForAnIdNoWaitHasIsTriedOnceAndStillWaitsOnThePerson(t *testing.T) {
	store := &takenForAnUnrelatedID{Store: session.NewMemoryStore()}
	agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, store, time.Hour)
	if !waitUntil(func() bool { return waitingOnAPerson(agent) }) {
		t.Fatal("the task never waited on the person")
	}

	if got := store.attempts.Load(); got != 1 {
		t.Errorf("registrations of the person's wait = %d, want 1: a refusal that names no wait must not be retried", got)
	}
	if outcome := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); outcome != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", outcome)
	}
	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the answer did not resume the task: %d LLM calls", len(mock.getCalls()))
	}
}

type blindToHeldCorrelations struct {
	session.Store
}

func (blindToHeldCorrelations) PendingCallTask(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func TestARefusalOnlyTheRegistrationCatchesStillLeavesTheTaskItsWaitsAndItsCheckpoint(t *testing.T) {
	tests := []struct {
		name    string
		batch   []batchCall
		answers bool
		want    []string
	}{
		{
			name: "a wait that survives",
			batch: []batchCall{
				{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonSTRReply(0)},
				{id: siblingCall, tool: siblingRemoteTool, reply: askingForAPersonWithID(heldElsewhereID)},
			},
			answers: true,
			want:    []string{"the person's answer", "a refusal: the request id is in use"},
		},
		{
			name: "every wait refused",
			batch: []batchCall{
				{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonWithID(heldElsewhereID)},
			},
			want: []string{"a refusal: the request id is in use"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := session.NewMemoryStore()
			seedTaskHoldingARequestID(t, base, heldElsewhereID)
			store := blindToHeldCorrelations{Store: base}
			var checkpointDuringResumedTurn atomic.Bool
			agent, mock := pausedOnABatch(t, store, tt.batch)
			mock.preCallHook = func(callIndex int) {
				if cp, err := base.GetCheckpoint(context.Background(), personWaitTaskID); callIndex == 1 && err == nil && cp != nil {
					checkpointDuringResumedTurn.Store(true)
				}
			}
			if tt.answers {
				if !waitUntil(func() bool { return waitingOnAPerson(agent) }) {
					t.Fatal("the task never waited on the person")
				}
				if got := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
					t.Fatalf("delivery = %v, want delivered", got)
				}
			}

			if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
				t.Fatalf("the task did not finish: %d LLM calls", len(mock.getCalls()))
			}

			if diff := cmp.Diff(tt.want, labelledToolTexts(mock.getCalls()[1])); diff != "" {
				t.Errorf("tool results of the resumed turn (-want +got):\n%s", diff)
			}
			if !checkpointDuringResumedTurn.Load() {
				t.Error("the resumed turn ran with no checkpoint")
			}
			if row, err := base.LookupPendingCall(t.Context(), "test-agent", heldElsewhereID); err != nil || row == nil || row.TaskID != victimTaskID {
				t.Errorf("the other task's correlation row = %+v (err %v), want it left alone", row, err)
			}
		})
	}
}

func TestSavingATaskAgainWithTheRequestIdItAlreadyHoldsIsNotRefused(t *testing.T) {
	agent, _, _ := replicaOf(t, session.NewMemoryStore(), "owner")
	state := newTestTaskState(agent)
	state.PendingCalls["call-own"] = &PendingToolCall{
		ToolCallID: "call-own", ToolName: "ask_person", Kind: PendingCallHILApproval, HILRequestID: "req-own", Timeout: time.Hour,
	}
	state.TotalPending = 1

	first := agent.checkpoints.save(t.Context(), state)
	second := agent.checkpoints.save(t.Context(), state)

	if first != nil || second != nil || !state.checkpointed.Load() {
		t.Errorf("first save = %v, second save = %v, durable = %v, want both accepted: a task's own row is not another task's", first, second, state.checkpointed.Load())
	}
}

const (
	askPersonTool = "ask_person"
	idInUseText   = "The tool asked a person for an answer with a request id that another task already uses, so the answer could not be collected."
)

func registerAskPersonTool(t *testing.T, agent *Agent) {
	t.Helper()
	err := agent.Tools().Register(&testBuiltinTool{
		name:        askPersonTool,
		description: "asks a person under the request id it is given",
		execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
			id, _ := args["id"].(string)
			return tool.Pending("waiting for a person", tool.WithData(map[string]any{
				"pending_kind":   "hil_approval",
				"hil_request_id": id,
			})), nil
		},
	})
	if err != nil {
		t.Fatalf("register %s: %v", askPersonTool, err)
	}
}

func dispatchBatch(t *testing.T, agent *Agent, state *TaskExecutionState, calls []llm.ToolCall) (results []*tool.Result, paused bool) {
	t.Helper()
	pca := defaultPauseCallArgs(agent, state)
	results, paused = agent.dispatchToolCallsMayPause(t.Context(), state, calls, pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger)
	t.Cleanup(func() {
		for _, pc := range state.PendingCalls {
			if pc.TimeoutTimer != nil {
				pc.TimeoutTimer.Stop()
			}
		}
	})
	return results, paused
}

func TestDispatchRefusesAPersonRequestWhoseIdAnotherTaskHoldsInTheStore(t *testing.T) {
	type observed struct {
		Paused        bool
		Refused       []string
		Waiting       []string
		VictimRowTask string
		OwnRows       []string
		Durable       bool
		Deletes       int
	}
	const freshID = "req-fresh"
	heldCall := llm.ToolCall{ID: "call-held", Name: askPersonTool, Arguments: `{"id":"` + heldElsewhereID + `"}`}
	freshCall := llm.ToolCall{ID: "call-fresh", Name: askPersonTool, Arguments: `{"id":"` + freshID + `"}`}
	okCall := llm.ToolCall{ID: "call-sibling", Name: siblingLocalTool, Arguments: `{}`}
	tests := []struct {
		name  string
		blind bool
		calls []llm.ToolCall
		want  observed
	}{
		{
			name:  "the colliding call is refused and the rest of the batch proceeds",
			calls: []llm.ToolCall{heldCall, freshCall, okCall},
			want: observed{
				Paused: true, Refused: []string{idInUseText}, Waiting: []string{"call-fresh"},
				VictimRowTask: victimTaskID, OwnRows: []string{freshID}, Durable: true,
			},
		},
		{
			name:  "a batch whose only pending call collides is not paused",
			calls: []llm.ToolCall{heldCall, okCall},
			want:  observed{Refused: []string{idInUseText}, VictimRowTask: victimTaskID},
		},
		{
			name:  "a collision only the registration catches is refused the same way",
			blind: true,
			calls: []llm.ToolCall{heldCall, freshCall, okCall},
			want: observed{
				Paused: true, Refused: []string{idInUseText}, Waiting: []string{"call-fresh"},
				VictimRowTask: victimTaskID, OwnRows: []string{freshID}, Durable: true, Deletes: 1,
			},
		},
		{
			name:  "a collision only the registration catches, in a batch with nothing else to wait for, is not paused",
			blind: true,
			calls: []llm.ToolCall{heldCall, okCall},
			want:  observed{Refused: []string{idInUseText}, VictimRowTask: victimTaskID, Deletes: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := &checkpointWrites{Store: session.NewMemoryStore()}
			seedTaskHoldingARequestID(t, base, heldElsewhereID)
			var store session.Store = base
			if tt.blind {
				store = blindToHeldCorrelations{Store: base}
			}
			agent, _, _ := replicaOf(t, store, "owner")
			registerAskPersonTool(t, agent)
			registerSiblingLocalTool(t, agent)
			state := newTestTaskState(agent)
			state.TaskID = personWaitTaskID

			results, paused := dispatchBatch(t, agent, state, tt.calls)

			_, _, deletes := base.counts()
			got := observed{Paused: paused, Durable: state.checkpointed.Load(), Deletes: deletes}
			for _, r := range results {
				if r != nil && r.Status == tool.StatusError {
					got.Refused = append(got.Refused, r.Message)
				}
			}
			if paused {
				for _, r := range state.SyncResults {
					if r.Status == tool.StatusError {
						got.Refused = append(got.Refused, r.Message)
					}
				}
			}
			for id := range state.PendingCalls {
				got.Waiting = append(got.Waiting, id)
			}
			if row, err := base.LookupPendingCall(t.Context(), agent.cfg.Name, heldElsewhereID); err == nil && row != nil {
				got.VictimRowTask = row.TaskID
			}
			if row, err := base.LookupPendingCall(t.Context(), agent.cfg.Name, freshID); err == nil && row != nil && row.TaskID == state.TaskID {
				got.OwnRows = append(got.OwnRows, row.CorrID)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("dispatching a request for a person whose id another task holds (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDispatchRefusingAPersonRequestKeepsTheCheckpointAnEarlierPauseWrote(t *testing.T) {
	type observed struct {
		Paused            bool
		Durable           bool
		CheckpointInStore bool
		Saves             int
		Updates           int
		Deletes           int
	}
	tests := []struct {
		name  string
		blind bool
		want  observed
	}{
		{
			name: "the store names the holder before anything is written",
			want: observed{Durable: true, CheckpointInStore: true, Saves: 1},
		},
		{
			name:  "only the registration catches it, so the checkpoint is deleted and written again",
			blind: true,
			want:  observed{Durable: true, CheckpointInStore: true, Saves: 2, Updates: 1, Deletes: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writes := &checkpointWrites{Store: session.NewMemoryStore()}
			seedTaskHoldingARequestID(t, writes, heldElsewhereID)
			var store session.Store = writes
			if tt.blind {
				store = blindToHeldCorrelations{Store: writes}
			}
			agent, _, _ := replicaOf(t, store, "owner")
			registerAskPersonTool(t, agent)
			state := newTestTaskState(agent)
			state.TaskID = personWaitTaskID
			earlier := llm.ToolCall{ID: "call-earlier", Name: askPersonTool, Arguments: `{"id":"req-earlier"}`}
			later := llm.ToolCall{ID: "call-later", Name: askPersonTool, Arguments: `{"id":"` + heldElsewhereID + `"}`}
			if _, paused := dispatchBatch(t, agent, state, []llm.ToolCall{earlier}); !paused {
				t.Fatal("the first batch did not pause the task")
			}
			state.mu.Lock()
			state.PendingCalls = map[string]*PendingToolCall{}
			state.mu.Unlock()
			agent.pausedTasks.Delete(state.TaskID)

			_, paused := dispatchBatch(t, agent, state, []llm.ToolCall{later})

			cp, err := writes.GetCheckpoint(t.Context(), personWaitTaskID)
			saves, updates, deletes := writes.counts()
			got := observed{
				Paused: paused, Durable: state.checkpointed.Load(), CheckpointInStore: err == nil && cp != nil,
				Saves: saves, Updates: updates, Deletes: deletes,
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("a refused call after an earlier pause wrote the checkpoint (-want +got):\n%s", diff)
			}
		})
	}
}

func TestACheckpointFailureIsLoggedWithoutTheRequestIdAToolChose(t *testing.T) {
	const chosenID = "req-chosen-by-the-tool"
	tests := []struct {
		name    string
		err     error
		wantKey string
		want    string
	}{
		{
			name:    "a request id another task holds is logged by its length",
			err:     fmt.Errorf("register pending calls: %w", &session.CorrelationTakenError{CorrID: chosenID}),
			wantKey: "requestIDBytes",
			want:    fmt.Sprint(len(chosenID)),
		},
		{
			name:    "any other failure is logged as the error",
			err:     errors.New("the session database is unreachable"),
			wantKey: "error",
			want:    "the session database is unreachable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attr := checkpointFailureAttr(tt.err)

			if attr.Key != tt.wantKey || attr.Value.String() != tt.want {
				t.Errorf("logged attribute = %s=%s, want %s=%s", attr.Key, attr.Value, tt.wantKey, tt.want)
			}
			if strings.Contains(attr.Value.String(), chosenID) {
				t.Errorf("logged attribute %s carries the request id the tool chose", attr.Value)
			}
		})
	}
}

type failingRegistration struct {
	session.Store

	err error
}

func (s failingRegistration) SaveCheckpoint(context.Context, *session.TaskCheckpoint) error {
	return s.err
}

func (s failingRegistration) UpdateCheckpoint(context.Context, *session.TaskCheckpoint) error {
	return s.err
}

func TestAFailedRegistrationOfAPersonsWaitLeavesNoCheckpointBehind(t *testing.T) {
	writes := &checkpointWrites{Store: session.NewMemoryStore()}
	store := failingRegistration{Store: writes, err: errors.New("the session database is unreachable")}
	agent, _, _ := replicaOf(t, store, "owner")
	state := newTestTaskState(agent)
	state.TaskID = personWaitTaskID
	state.PendingCalls["call-own"] = &PendingToolCall{
		ToolCallID: "call-own", ToolName: askPersonTool, Kind: PendingCallHILApproval, HILRequestID: "req-own", Timeout: time.Hour,
	}
	state.TotalPending = 1

	err := agent.checkpoints.save(t.Context(), state)

	cp, getErr := writes.GetCheckpoint(t.Context(), personWaitTaskID)
	if err == nil || getErr != nil || cp != nil || state.checkpointed.Load() {
		t.Errorf("save error = %v, checkpoint left = %+v (err %v), durable = %v; want a failed save that left no checkpoint",
			err, cp, getErr, state.checkpointed.Load())
	}
}

type unreadableHolders struct {
	session.Store
}

func (unreadableHolders) PendingCallTask(context.Context, string) (string, bool, error) {
	return "", false, errors.New("the session database is unreachable")
}

func TestDispatchWhenTheStoreCannotNameTheHolderOfARequestIdLetsTheRegistrationDecide(t *testing.T) {
	type observed struct {
		Paused        bool
		Durable       bool
		Waiting       []string
		VictimRowTask string
	}
	const freshID = "req-fresh"
	heldCall := llm.ToolCall{ID: "call-held", Name: askPersonTool, Arguments: `{"id":"` + heldElsewhereID + `"}`}
	freshCall := llm.ToolCall{ID: "call-fresh", Name: askPersonTool, Arguments: `{"id":"` + freshID + `"}`}
	tests := []struct {
		name  string
		calls []llm.ToolCall
		want  observed
	}{
		{
			name:  "an id nobody holds is registered and the task waits durably",
			calls: []llm.ToolCall{freshCall},
			want:  observed{Paused: true, Durable: true, Waiting: []string{"call-fresh"}, VictimRowTask: victimTaskID},
		},
		{
			name:  "an id another task holds is refused by the registration and the rest of the batch waits durably",
			calls: []llm.ToolCall{heldCall, freshCall},
			want:  observed{Paused: true, Durable: true, Waiting: []string{"call-fresh"}, VictimRowTask: victimTaskID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := session.NewMemoryStore()
			seedTaskHoldingARequestID(t, base, heldElsewhereID)
			agent, _, _ := replicaOf(t, unreadableHolders{Store: base}, "owner")
			registerAskPersonTool(t, agent)
			state := newTestTaskState(agent)

			_, paused := dispatchBatch(t, agent, state, tt.calls)

			got := observed{Paused: paused, Durable: state.checkpointed.Load()}
			for id := range state.PendingCalls {
				got.Waiting = append(got.Waiting, id)
			}
			if row, err := base.LookupPendingCall(t.Context(), agent.cfg.Name, heldElsewhereID); err == nil && row != nil {
				got.VictimRowTask = row.TaskID
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("dispatching with a store that cannot name the holder of a request id (-want +got):\n%s", diff)
			}
		})
	}
}

type repauseRefusalOutcome struct {
	Durable           bool
	CheckpointInStore bool
	Saves             int
	Updates           int
	Deletes           int
}

func agentWhoseStoreIsBlindToTheHolderOfARequestID(t *testing.T) (*Agent, *TaskExecutionState, *checkpointWrites) {
	t.Helper()
	writes := &checkpointWrites{Store: session.NewMemoryStore()}
	seedTaskHoldingARequestID(t, writes, heldElsewhereID)
	agent, _, _ := replicaOf(t, blindToHeldCorrelations{Store: writes}, "owner")
	registerAskPersonTool(t, agent)
	state := newTestTaskState(agent)
	state.TaskID = personWaitTaskID
	return agent, state, writes
}

func repauseOnTheRequestIDHeldElsewhere(t *testing.T, agent *Agent, state *TaskExecutionState, writes *checkpointWrites) repauseRefusalOutcome {
	t.Helper()
	entries := []pausedCallResult{{
		call:   llm.ToolCall{ID: "call-held", Name: askPersonTool},
		result: personRequestResult(heldElsewhereID),
	}}

	agent.repauseOnPersonRequests(t.Context(), state, entries, agent.logger)

	cp, err := writes.GetCheckpoint(t.Context(), personWaitTaskID)
	saves, updates, deletes := writes.counts()
	return repauseRefusalOutcome{
		Durable: state.checkpointed.Load(), CheckpointInStore: err == nil && cp != nil,
		Saves: saves, Updates: updates, Deletes: deletes,
	}
}

func TestARepauseWhoseEveryWaitIsRefusedWritesNoCheckpointForATaskThatNeverHadOne(t *testing.T) {
	agent, state, writes := agentWhoseStoreIsBlindToTheHolderOfARequestID(t)
	want := repauseRefusalOutcome{Saves: 1, Deletes: 1}

	got := repauseOnTheRequestIDHeldElsewhere(t, agent, state, writes)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("a re-pause whose every wait the store refused (-want +got):\n%s", diff)
	}
}

func TestARepauseWhoseEveryWaitIsRefusedWritesTheCheckpointAgainForATaskWhoseEarlierCheckpointTheRefusalDeleted(t *testing.T) {
	agent, state, writes := agentWhoseStoreIsBlindToTheHolderOfARequestID(t)
	earlier := llm.ToolCall{ID: "call-earlier", Name: askPersonTool, Arguments: `{"id":"req-earlier"}`}
	if _, paused := dispatchBatch(t, agent, state, []llm.ToolCall{earlier}); !paused {
		t.Fatal("the first batch did not pause the task")
	}
	state.mu.Lock()
	state.PendingCalls = map[string]*PendingToolCall{}
	state.mu.Unlock()
	agent.pausedTasks.Delete(state.TaskID)
	want := repauseRefusalOutcome{Durable: true, CheckpointInStore: true, Saves: 2, Updates: 1, Deletes: 1}

	got := repauseOnTheRequestIDHeldElsewhere(t, agent, state, writes)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("a re-pause whose every wait the store refused (-want +got):\n%s", diff)
	}
}

const chosenRequestID = "req-chosen-by-the-tool"

func agentWhoseStoreTakesEveryRequestIDForAnotherTask(t *testing.T) (*Agent, *TaskExecutionState, *syncLogBuffer) {
	t.Helper()
	logs := &syncLogBuffer{}
	store := &takenForID{Store: session.NewMemoryStore(), id: chosenRequestID}
	agent, _, _ := replicaOf(t, store, "owner", capturingLogs(logs))
	registerAskPersonTool(t, agent)
	state := newTestTaskState(agent)
	state.TaskID = personWaitTaskID
	return agent, state, logs
}

func assertLoggedByLengthOnly(t *testing.T, logs *syncLogBuffer, msg string) {
	t.Helper()
	records := logs.records(msg)
	if len(records) != 1 {
		t.Fatalf("%d log lines %q, want 1", len(records), msg)
	}
	if got := records[0]["requestIDBytes"]; got != float64(len(chosenRequestID)) {
		t.Errorf("requestIDBytes = %v, want %d", got, len(chosenRequestID))
	}
	if strings.Contains(logs.String(), chosenRequestID) {
		t.Errorf("the log carries the request id the tool chose:\n%s", logs.String())
	}
}

func TestACheckpointFailureAtThePauseOfABatchNeverLogsTheRequestIdAToolChose(t *testing.T) {
	agent, state, logs := agentWhoseStoreTakesEveryRequestIDForAnotherTask(t)
	call := llm.ToolCall{ID: "call-fresh", Name: askPersonTool, Arguments: `{"id":"req-fresh"}`}

	dispatchBatch(t, agent, state, []llm.ToolCall{call})

	assertLoggedByLengthOnly(t, logs, "failed to checkpoint paused task; it will not survive a restart of this process")
}

func TestACheckpointFailureAtTheRepauseOnARemoteToolsRequestNeverLogsTheRequestIdAToolChose(t *testing.T) {
	agent, state, logs := agentWhoseStoreTakesEveryRequestIDForAnotherTask(t)
	entries := []pausedCallResult{{
		call:   llm.ToolCall{ID: "call-fresh", Name: askPersonTool},
		result: personRequestResult("req-fresh"),
	}}

	agent.repauseOnPersonRequests(t.Context(), state, entries, agent.logger)

	assertLoggedByLengthOnly(t, logs, "could not write the person's wait to the checkpoint; it will not survive a restart of this process")
}

type takenForID struct {
	session.Store

	id string
}

func (s *takenForID) refuse(cp *session.TaskCheckpoint) error {
	for _, c := range cp.PendingCalls {
		if c.Kind == session.PendingKindHILApproval {
			return &session.CorrelationTakenError{CorrID: s.id}
		}
	}
	return nil
}

func (s *takenForID) SaveCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if err := s.refuse(cp); err != nil {
		return err
	}
	return s.Store.SaveCheckpoint(ctx, cp)
}

func (s *takenForID) UpdateCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if err := s.refuse(cp); err != nil {
		return err
	}
	return s.Store.UpdateCheckpoint(ctx, cp)
}

type registrationSwitch struct {
	session.Store

	failWith atomic.Pointer[error]
}

func (s *registrationSwitch) SaveCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if err := s.failWith.Load(); err != nil {
		return *err
	}
	return s.Store.SaveCheckpoint(ctx, cp)
}

func (s *registrationSwitch) UpdateCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if err := s.failWith.Load(); err != nil {
		return *err
	}
	return s.Store.UpdateCheckpoint(ctx, cp)
}

func TestAFailedRegistrationOfADurableTasksNextSaveClearsItsDurableFlagAndRemovesItsCheckpoint(t *testing.T) {
	store := &registrationSwitch{Store: session.NewMemoryStore()}
	agent, _, _ := replicaOf(t, store, "owner")
	state := newTestTaskState(agent)
	state.TaskID = personWaitTaskID
	state.PendingCalls["call-own"] = &PendingToolCall{
		ToolCallID: "call-own", ToolName: askPersonTool, Kind: PendingCallHILApproval, HILRequestID: "req-own", Timeout: time.Hour,
	}
	state.TotalPending = 1
	if err := agent.checkpoints.save(t.Context(), state); err != nil || !state.checkpointed.Load() {
		t.Fatalf("first save: error = %v, durable = %v, want a durable task", err, state.checkpointed.Load())
	}
	failure := errors.New("the session database is unreachable")
	store.failWith.Store(&failure)

	err := agent.checkpoints.save(t.Context(), state)

	cp, getErr := store.GetCheckpoint(t.Context(), personWaitTaskID)
	if err == nil || getErr != nil || cp != nil || state.checkpointed.Load() {
		t.Errorf("second save error = %v, checkpoint left = %+v (err %v), durable = %v; want a failure that removed the checkpoint and cleared the flag",
			err, cp, getErr, state.checkpointed.Load())
	}
}

func TestARepromptedSignInWhoseCheckpointWriteIsRefusedNeverLogsTheRequestIdAnotherTaskHolds(t *testing.T) {
	logs := &syncLogBuffer{}
	store := session.NewMemoryStore()
	seedTaskHoldingARequestID(t, store, heldElsewhereID)
	agent, _, _ := replicaOf(t, store, "owner", capturingLogs(logs))
	state := newTestTaskState(agent)
	state.TaskID = personWaitTaskID
	state.PendingCalls["call-held"] = &PendingToolCall{
		ToolCallID: "call-held", ToolName: askPersonTool, Kind: PendingCallHILApproval, HILRequestID: heldElsewhereID, Timeout: time.Hour,
	}
	signIn := &PendingToolCall{ToolCallID: "call-sign-in", ToolName: "needs_sign_in", Kind: PendingCallAuth}
	state.PendingCalls[signIn.ToolCallID] = signIn
	t.Cleanup(func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		if timer := state.PendingCalls[signIn.ToolCallID].TimeoutTimer; timer != nil {
			timer.Stop()
		}
	})

	agent.rearmPendingAuth(state, signIn)

	records := logs.records("could not record the new sign-in prompt on the checkpoint; its answer is applied only by this AWE")
	if len(records) != 1 {
		t.Fatalf("%d log lines about the unrecorded sign-in prompt, want 1", len(records))
	}
	if got := records[0]["requestIDBytes"]; got != float64(len(heldElsewhereID)) {
		t.Errorf("requestIDBytes = %v, want %d", got, len(heldElsewhereID))
	}
	if strings.Contains(logs.String(), heldElsewhereID) {
		t.Errorf("the log carries the request id another task holds:\n%s", logs.String())
	}
}
