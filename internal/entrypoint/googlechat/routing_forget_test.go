// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestQueueForAfterForgetTask_ReturnsClosedQueueThatPostsNothing(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	st := &taskState{meta: dmMeta()}
	a.trackTask(tid("task-1"), st)

	a.forgetTask(tid("task-1"))
	q := a.queueFor(tid("task-1"), st)

	sent := make(chan struct{})
	go func() {
		q.Placeholder("late placeholder")
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("a send on the queue of a forgotten task blocked")
	}
	if writes, _ := chat.snapshot(); len(writes) != 0 {
		t.Errorf("a forgotten task's queue wrote to Chat: %+v", writes)
	}
	if st.queue != nil {
		t.Error("queueFor attached a queue to a forgotten task")
	}
}
