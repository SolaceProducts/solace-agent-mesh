// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

type boundedReads struct {
	session.Store
	mu     sync.Mutex
	err    error
	bounds []bool
}

func (s *boundedReads) GetCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	deadline, hasDeadline := ctx.Deadline()
	s.mu.Lock()
	s.bounds = append(s.bounds, hasDeadline && time.Until(deadline) <= durableWriteTimeout)
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.Store.GetCheckpoint(ctx, taskID)
}

func (s *boundedReads) reads() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.bounds)
}

func assertOwnTimeoutResult(t *testing.T, state *TaskExecutionState, recordedElsewhere string) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()
	got := state.SyncResults[0]
	if got == nil || strings.Contains(got.Message, recordedElsewhere) {
		t.Errorf("SyncResults[0] = %+v, want this replica's own timeout result: the recorded one could not be read", got)
	}
}

func TestTimeoutHandlerReadsTheRecordedResultUnderADeadlineAndFallsBackWhenItCannot(t *testing.T) {
	wantBounded := slices.Repeat([]bool{true}, orphanAdoptionMaxAttempts)

	t.Run("a question's timeout", func(t *testing.T) {
		useResumeHook(t, func(*Agent, *TaskExecutionState) {})
		base := session.NewMemoryStore()
		holder, _, _ := replicaOf(t, base, "holder")
		other, _, _ := replicaOf(t, base, "other")
		state := pausedOnQuestion(t, base, holder, "task-ask")
		if got := other.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
			t.Fatalf("delivery = %v, want delivered", got)
		}
		reads := &boundedReads{Store: base, err: errStoreBlip}
		holder.checkpoints.store = reads

		holder.handlePendingHILTimeout("task-ask", "tc-1")

		if diff := cmp.Diff(wantBounded, reads.reads()); diff != "" {
			t.Errorf("checkpoint reads bounded by a deadline (-want +got):\n%s", diff)
		}
		assertOwnTimeoutResult(t, state, "EMEA")
	})

	t.Run("a login's timeout", func(t *testing.T) {
		useResumeHook(t, func(*Agent, *TaskExecutionState) {})
		agent, _ := newLoginAgent(t, nil)
		state := loginPausedState(t, agent)
		agent.pausedTasks.Store(state)
		recorded := &session.ToolCallResult{ToolCallID: loginCallID, ToolName: "stubtool", Content: "signed in on another replica"}
		if _, err := agent.checkpoints.store.RecordToolResult(context.Background(), loginTaskID, authCorrelationID(loginTaskID, loginCallID), recorded); err != nil {
			t.Fatalf("record on the other replica: %v", err)
		}
		reads := &boundedReads{Store: agent.checkpoints.store, err: errStoreBlip}
		agent.checkpoints.store = reads

		agent.handlePendingAuthTimeout(loginTaskID, loginCallID)

		if diff := cmp.Diff(wantBounded, reads.reads()); diff != "" {
			t.Errorf("checkpoint reads bounded by a deadline (-want +got):\n%s", diff)
		}
		assertOwnTimeoutResult(t, state, "signed in on another replica")
	})
}
