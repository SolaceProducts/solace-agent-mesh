// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type blockingAuthorizer struct {
	auth.NoopAgentAuthorizer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingAuthorizer(t *testing.T) *blockingAuthorizer {
	b := &blockingAuthorizer{entered: make(chan struct{}, 1), release: make(chan struct{})}
	t.Cleanup(b.let)
	return b
}

func (b *blockingAuthorizer) let() { b.once.Do(func() { close(b.release) }) }

func (b *blockingAuthorizer) AuthorizeAgent(context.Context, *auth.Identity, string) error {
	b.entered <- struct{}{}
	<-b.release
	return nil
}

func (b *blockingAuthorizer) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(arrivalWait):
		t.Fatal("the peer call never reached its delegation check")
	}
}

func endTaskT1(t *testing.T, r *rig, _ *client) {
	t.Helper()
	r.adapter.agent(agentName).sweep(time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute))
	waitFor(t, func() bool {
		ended := false
		r.locked(func(s *agentState) { ended = s.tasks["T1"].final })
		return ended
	})
}

func cancelTaskT1(t *testing.T, r *rig, c *client) {
	t.Helper()
	r.cancel("T1")
	c.next(typeCancel)
}

func TestAPeerCallWhoseTaskEndsWhileItsDelegationIsChecked(t *testing.T) {
	for _, tc := range []struct {
		name     string
		leave    func(*testing.T, *rig, *client)
		wantCode string
	}{
		{"was cancelled", cancelTaskT1, errCodeForbidden},
		{"ended", endTaskT1, errCodeUnknownTask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authz := newBlockingAuthorizer(t)
			r := newRig(t, nil, authz)
			c := r.connect()
			r.consume(c)
			r.taskOn(c, "T1", nil)
			requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))

			c.send(peerCall("S1", peerName, "Is policy P-9 active?"))
			authz.awaitEntered(t)
			tc.leave(t, r, c)
			authz.let()

			c.refused(tc.wantCode, "S1")
			none(t, requests)
			if hasSubtask(r, "S1") {
				t.Fatal("a peer call of a task that is over stays registered")
			}
		})
	}
}
