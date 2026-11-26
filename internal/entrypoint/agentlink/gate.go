// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import "sync"

type workGate struct {
	mu     sync.Mutex
	closed bool
	work   sync.WaitGroup
}

func (g *workGate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.work.Add(1)
	return true
}

func (g *workGate) leave() { g.work.Done() }

func (g *workGate) closeAndWait() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.work.Wait()
}
