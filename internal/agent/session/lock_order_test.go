// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func isBenignRaceOutcome(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "not outstanding")
}

func isConnectionExhaustion(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many clients") ||
		strings.Contains(msg, "remaining connection slots") ||
		strings.Contains(msg, "failed to connect")
}

func TestNoDeadlockBetweenRecordAndDelete(t *testing.T) {
	if os.Getenv("SAM_TEST_LOCK_ORDER") == "" {
		t.Skip("connection-hungry and Postgres-only; set SAM_TEST_LOCK_ORDER=1 with SAM_TEST_SESSION_DB_URL to run it")
	}
	if os.Getenv("SAM_TEST_SESSION_DB_URL") == "" {
		t.Skip("lock ordering is only observable on Postgres; set SAM_TEST_SESSION_DB_URL")
	}
	store := newTestSQLStore(t)
	ctx := context.Background()

	const tasks = 8
	const maxInFlight = 8
	sem := make(chan struct{}, maxInFlight)
	acquire := func() { sem <- struct{}{} }
	releaseSlot := func() { <-sem }
	type fixture struct{ taskID, corrID, sessID string }
	seedFixtures := func(n int) []fixture {
		out := make([]fixture, 0, n)
		for range n {
			taskID, sessID, corrID := idgen.New(), idgen.New(), idgen.New()
			seedSession(t, store, "app", "user", sessID)
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: "app", PendingCount: 1,
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
				CorrID: corrID, TaskID: taskID, AgentName: "app",
				ToolCallID: "tc-1", Kind: PendingKindPeer,
			}}); err != nil {
				t.Fatalf("register: %v", err)
			}
			out = append(out, fixture{taskID: taskID, corrID: corrID, sessID: sessID})
		}
		return out
	}
	fixtures := seedFixtures(tasks)
	popFixtures := seedFixtures(tasks)

	var wg sync.WaitGroup
	errs := make(chan error, tasks*2)
	start := make(chan struct{})
	for _, f := range fixtures {
		wg.Add(2)
		go func(f fixture) {
			defer wg.Done()
			<-start
			acquire()
			defer releaseSlot()
			if _, err := store.RecordToolResult(ctx, f.taskID, f.corrID, &ToolCallResult{
				ToolCallID: "tc-1", Content: "answer",
			}); err != nil && !isBenignRaceOutcome(err) {
				errs <- err
			}
		}(f)
		go func(f fixture) {
			defer wg.Done()
			<-start
			acquire()
			defer releaseSlot()
			if err := store.DeleteCheckpoint(ctx, f.taskID); err != nil {
				errs <- err
			}
		}(f)

	}
	for _, f := range popFixtures {
		wg.Add(2)
		go func(f fixture) {
			defer wg.Done()
			<-start
			acquire()
			defer releaseSlot()
			if _, err := store.RecordToolResult(ctx, f.taskID, f.corrID, &ToolCallResult{
				ToolCallID: "tc-1", Content: "answer",
			}); err != nil && !isBenignRaceOutcome(err) {
				errs <- err
			}
		}(f)
		go func(f fixture) {
			defer wg.Done()
			<-start
			acquire()
			defer releaseSlot()
			if _, err := store.PopCheckpoint(ctx, f.taskID); err != nil {
				errs <- err
			}
		}(f)
	}

	close(start)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the record/delete pairs never finished — the two transactions are taking the tables in opposite orders and are waiting on each other")
	}
	close(errs)

	for err := range errs {
		if isConnectionExhaustion(err) {
			t.Fatalf("the database ran out of connections, so this test proved nothing about lock ordering and starved every other DB package sharing the server: %v", err)
		}
		if strings.Contains(strings.ToLower(err.Error()), "deadlock") {
			t.Errorf("deadlock between RecordToolResult and DeleteCheckpoint: %v — the loser is aborted with no retry, so a real peer answer is dropped and the reaper later synthesises a timeout over it", err)
			continue
		}
		t.Errorf("unexpected error: %v", err)
	}
}
