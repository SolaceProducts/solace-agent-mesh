// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestForkRecursionDepthGate(t *testing.T) {
	execAt := func(t *testing.T, maxCallDepth, callerDepth int) (allowed bool, msg string) {
		agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
		agent.cfg.MaxCallDepth = maxCallDepth

		brk := newMockBroker()
		agent.svc.(*mockServices).brk = brk
		agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

		st := &subTaskTool{agent: agent}
		toolCtx := &tool.Context{
			UserID:    "user-1",
			SessionID: "parent-session",
			AgentName: "Builder",
			CallDepth: callerDepth,
		}

		result, err := st.Execute(context.Background(), map[string]any{
			"instruction": "Generate the InventoryChecker agent YAML config.",
			"type":        "fork",
		}, toolCtx)
		if err != nil {
			t.Fatalf("Execute returned a Go error: %v", err)
		}

		brk.mu.Lock()
		published := len(brk.published)
		brk.mu.Unlock()

		return published > 0, result.Message
	}

	forkTreeDepth := func(t *testing.T, maxCallDepth int) int {
		levels := 0
		for caller := 0; caller < 50; caller++ {
			allowed, _ := execAt(t, maxCallDepth, caller)
			if !allowed {
				break
			}
			levels++
		}
		return levels
	}

	t.Run("old_default_max_call_depth_3", func(t *testing.T) {
		for _, caller := range []int{0, 1, 2} {
			if allowed, _ := execAt(t, 3, caller); !allowed {
				t.Errorf("max=3: caller depth %d should be ALLOWED to spawn a sub_task", caller)
			}
		}
		allowed, msg := execAt(t, 3, 3)
		if allowed {
			t.Errorf("max=3: caller depth 3 should be REFUSED")
		}
		if !strings.Contains(msg, "Maximum sub-task delegation depth") {
			t.Errorf("max=3: expected depth-limit refusal, got: %q", msg)
		}
		levels := forkTreeDepth(t, 3)
		t.Logf("max_call_depth=3 (OLD): fork tree is %d levels deep "+
			"(lead→fork→fork→fork). This is the recursive self-fork explosion.", levels)
		if levels != 3 {
			t.Errorf("expected 3 allowed fork levels at max=3, got %d", levels)
		}
	})

	t.Run("fixed_max_call_depth_1", func(t *testing.T) {
		if allowed, _ := execAt(t, 1, 0); !allowed {
			t.Errorf("max=1: lead (caller depth 0) should be ALLOWED to fan out leaves")
		}
		allowed, msg := execAt(t, 1, 1)
		if allowed {
			t.Errorf("max=1: leaf (caller depth 1) should be REFUSED — no deeper forks")
		}
		if !strings.Contains(msg, "Maximum sub-task delegation depth") {
			t.Errorf("max=1: expected depth-limit refusal, got: %q", msg)
		}
		levels := forkTreeDepth(t, 1)
		t.Logf("max_call_depth=1 (FIXED): fork tree is %d level deep "+
			"(lead→leaf only). Leaves cannot recurse — the fix.", levels)
		if levels != 1 {
			t.Errorf("expected 1 allowed fork level at max=1, got %d", levels)
		}
	})
}
