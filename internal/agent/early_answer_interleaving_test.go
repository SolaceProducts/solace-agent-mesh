// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

const pauseInterleavingWindow = 250 * time.Millisecond

type pauseDuringAnswer struct {
	holder  *Agent
	state   *TaskExecutionState
	resumes *atomic.Int32
	done    chan struct{}
}

func newPauseDuringAnswer(t *testing.T) *pauseDuringAnswer {
	t.Helper()
	holder, _, _ := replicaOf(t, session.NewMemoryStore(), "holder")
	resumes := &atomic.Int32{}
	useResumeHook(t, func(*Agent, *TaskExecutionState) { resumes.Add(1) })
	state := pausedOnQuestion(t, holder.checkpoints.store, holder, "task-race")
	holder.pausedTasks.Delete(state.TaskID)
	holder.expectEarlyAnswer(state.TaskID, askRequestID, state.UserProps.UserID, time.Hour)
	return &pauseDuringAnswer{holder: holder, state: state, resumes: resumes, done: make(chan struct{})}
}

func (p *pauseDuringAnswer) pause() {
	started := make(chan struct{})
	go func() {
		defer close(p.done)
		close(started)
		p.holder.pausedTasks.Store(p.state)
		p.holder.applyEarlyAnswers(p.state.TaskID, []string{askRequestID})
	}()
	<-started
}

func (p *pauseDuringAnswer) pauseAndWaitForIt() {
	p.pause()
	select {
	case <-p.done:
	case <-time.After(pauseInterleavingWindow):
	}
}

func (p *pauseDuringAnswer) answer(t *testing.T) {
	t.Helper()
	if got := p.holder.deliverHILResponse(regionAnswer(), hilSenderUser(p.state.UserProps.UserID)); got != hilDelivered {
		t.Errorf("deliverHILResponse() = %v, want delivered", got)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the task never finished pausing")
	}
	if got := p.resumes.Load(); got != 1 {
		t.Errorf("the task resumed %d times, want once with the answer that raced its pause", got)
	}
}

func TestAnswerThatLandsBetweenTheOwnerCheckAndTheStashIsNotLostToThePause(t *testing.T) {
	race := newPauseDuringAnswer(t)
	race.holder.early.afterOwnerCheck = race.pauseAndWaitForIt

	race.answer(t)
}

func TestAnswerThatMissesThePausedTaskIsDeliveredWhenThePauseFinishesBeforeTheStash(t *testing.T) {
	race := newPauseDuringAnswer(t)
	race.holder.early.beforeStash = func() {
		race.pause()
		<-race.done
	}

	race.answer(t)
}
