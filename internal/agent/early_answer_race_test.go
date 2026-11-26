// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

const earlyAnswerRaceRounds = 3000

func TestAnswerRacingTheTaskPausingIsAppliedExactlyOnce(t *testing.T) {
	holder, _, _ := replicaOf(t, session.NewMemoryStore(), "holder")
	var resumes atomic.Int32
	useResumeHook(t, func(*Agent, *TaskExecutionState) { resumes.Add(1) })

	lost := 0
	for round := range earlyAnswerRaceRounds {
		taskID := fmt.Sprintf("task-race-%d", round)
		state := pausedOnQuestion(t, holder.checkpoints.store, holder, taskID)
		holder.pausedTasks.Delete(taskID)
		holder.expectEarlyAnswer(taskID, askRequestID, state.UserProps.UserID, time.Hour)
		before := resumes.Load()

		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Go(func() {
			<-start
			if got := holder.deliverHILResponse(regionAnswer(), hilSenderUser(state.UserProps.UserID)); got == hilRefused {
				t.Errorf("round %d: deliverHILResponse() = refused, want the owner's answer accepted", round)
			}
		})
		wg.Go(func() {
			<-start
			if round%2 == 0 {
				runtime.Gosched()
			}
			holder.pausedTasks.Store(state)
			holder.applyEarlyAnswers(taskID, []string{askRequestID})
		})
		close(start)
		wg.Wait()

		if resumes.Load()-before != 1 {
			lost++
		}
		holder.pausedTasks.Delete(taskID)
		holder.forgetEarlyAnswers(taskID)
	}

	if lost != 0 {
		t.Errorf("%d of %d answers that raced the task pausing were not applied exactly once, so the task would wait out the full input timeout", lost, earlyAnswerRaceRounds)
	}
}
