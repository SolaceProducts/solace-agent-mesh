// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

type blockedRecordStore struct {
	session.Store
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockedRecordStore(inner session.Store) *blockedRecordStore {
	return &blockedRecordStore{Store: inner, entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *blockedRecordStore) RecordToolResult(ctx context.Context, taskID, corrID string, result *session.ToolCallResult) (int, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.Store.RecordToolResult(ctx, taskID, corrID, result)
}

func TestNotifiedAnswerDoesNotHoldTheReceiveLoopWhileItIsBanked(t *testing.T) {
	inner := session.NewMemoryStore()
	blocked := newBlockedRecordStore(inner)
	var releaseOnce sync.Once
	releaseStore := func() { releaseOnce.Do(func() { close(blocked.release) }) }
	t.Cleanup(releaseStore)
	holder, _, _ := replicaOf(t, inner, "holder", func(a *Agent) { a.checkpoints.store = blocked })
	state := pausedOnQuestion(t, inner, holder, "task-ask")
	var resumes atomic.Int32
	useResumeHook(t, func(_ *Agent, resumed *TaskExecutionState) {
		if resumed.TaskID == state.TaskID {
			resumes.Add(1)
		}
	})

	routed := make(chan struct{})
	go func() {
		holder.routeMessage(t.Context(), notifyAnswer(t, regionAnswer()))
		close(routed)
	}()

	select {
	case <-routed:
	case <-time.After(2 * time.Second):
		t.Fatal("routeMessage did not return while the answer's store write was blocked, so every other message on the receive loop waits behind it")
	}
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the answer was never banked")
	}
	releaseStore()
	if !waitUntil(func() bool { return resumes.Load() == 1 }) {
		t.Errorf("the task resumed %d times after the store write finished, want once", resumes.Load())
	}
}
