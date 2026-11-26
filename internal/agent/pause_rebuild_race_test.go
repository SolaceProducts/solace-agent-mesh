// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestPausingAgainDoesNotRaceReadersOfItsMaps(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "pauser")
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "pausingtool",
		description: "pauses so the rebuild path runs",
		execFn:      stubAuthAwareTool("cred-race"),
	}); err != nil {
		t.Fatalf("register pausing tool: %v", err)
	}

	agent.checkpoints.store = &slowSaveStore{Store: store, delay: 2 * time.Millisecond}

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	agent.pausedTasks.Store(state)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				agent.pausedTasks.FindByHILRequestID("no-such-request")
			}
		}()
	}

	args := defaultPauseCallArgs(agent, state)
	toolCtx := agent.buildToolCtxFromState(context.Background(), state, args.sp)
	for range 12 {
		calls := singleCall("pausingtool", "call-"+idgen.New(), "{}")
		if _, paused := agent.dispatchToolCallsMayPause(
			context.Background(), state, calls,
			toolCtx, args.sp, args.parallelGroupID, args.sanitizeErrors, agent.logger,
		); !paused {
			t.Fatal("the tool did not pause, so the rebuild under test never ran")
		}
	}

	close(stop)
	readers.Wait()
}

type slowSaveStore struct {
	session.Store
	delay time.Duration
}

func (s *slowSaveStore) SaveCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	time.Sleep(s.delay)
	return s.Store.SaveCheckpoint(ctx, cp)
}

func (s *slowSaveStore) UpdateCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	time.Sleep(s.delay)
	return s.Store.UpdateCheckpoint(ctx, cp)
}

var _ = tool.OK
