// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type manifestLockTable struct {
	mu   sync.Mutex
	held map[artifact.Locator]*manifestLock
}

type manifestLock struct {
	mu    sync.Mutex
	users int
}

var manifestLocks = &manifestLockTable{held: map[artifact.Locator]*manifestLock{}}

func (t *manifestLockTable) lock(loc artifact.Locator) func() {
	t.mu.Lock()
	entry, ok := t.held[loc]
	if !ok {
		entry = &manifestLock{}
		t.held[loc] = entry
	}
	entry.users++
	t.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		t.mu.Lock()
		defer t.mu.Unlock()
		entry.users--
		if entry.users == 0 {
			delete(t.held, loc)
		}
	}
}

type manifestHold struct{ release func() }

func (h manifestHold) Unlock() {
	if h.release != nil {
		h.release()
	}
}

func lockBuildManifest(toolCtx *tool.Context, filename string) manifestHold {
	if filename != manifestFilename || toolCtx == nil || toolCtx.ArtifactService == nil {
		return manifestHold{}
	}
	loc := toolCtx.ArtifactService.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename)
	return manifestHold{release: manifestLocks.lock(loc)}
}
