// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestStateIsNotPublishedUntilPauseFinishesMutating(t *testing.T) {
	for _, tc := range []struct {
		name          string
		durableResume bool
	}{
		{"flag off", false},
		{"flag on", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var visibleDuringPassTwo bool
			var probed bool
			var agent *Agent

			agent = newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
				r := tool.Pending("waiting for peer", tool.WithData(map[string]any{"sub_task_id": "sub-1"}))
				r.OnDelivery = func(_ func(*tool.Result)) {
					probed = true
					visibleDuringPassTwo = agent.pausedTasks.Load("test-task") != nil
				}
				return r, nil
			})
			if tc.durableResume {
				enableDurableResume(t, agent)
			} else if agent.checkpoints != nil {
				t.Fatal("expected durable resume off")
			}

			state := newTestTaskState(agent)
			pca := defaultPauseCallArgs(agent, state)
			_, paused := agent.dispatchToolCallsMayPause(
				t.Context(), state,
				singleCall("stubtool", "call-p1", "{}"),
				pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
			)

			if !paused {
				t.Fatal("expected the task to pause")
			}
			if !probed {
				t.Fatal("OnDelivery was never invoked — the probe did not run, so this test proves nothing")
			}
			if visibleDuringPassTwo {
				t.Error("state was already published to pausedTasks while pass 2 was still mutating it — cancelPausedTask can now reassign PendingCalls concurrently, which is a fatal concurrent map access, not a recoverable panic")
			}
			if agent.pausedTasks.Load("test-task") == nil {
				t.Error("state was never published — a paused task must be resumable")
			}
		})
	}
}
