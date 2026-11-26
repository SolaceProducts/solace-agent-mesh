// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func firstPublishedTrigger(t *testing.T, brk *mockBroker) string {
	t.Helper()
	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.published) == 0 {
		t.Fatal("expected a broker publish")
	}
	return brk.published[0].userProps[a2a.PropTrigger]
}

func TestSubTaskForwardsTrigger(t *testing.T) {
	for _, trigger := range []string{a2a.TriggerScheduled, a2a.TriggerInteractive} {
		t.Run(trigger, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			brk := newMockBroker()
			agent.svc.(*mockServices).brk = brk
			agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

			st := &subTaskTool{agent: agent}
			toolCtx := &tool.Context{UserID: "u", SessionID: "s", AgentName: "a", Trigger: trigger}
			if _, err := st.Execute(context.Background(), map[string]any{
				"instruction": "do the thing", "type": "fresh",
			}, toolCtx); err != nil {
				t.Fatal(err)
			}
			if got := firstPublishedTrigger(t, brk); got != trigger {
				t.Errorf("sub-task trigger = %q, want %q", got, trigger)
			}
		})
	}
}

func TestWorkflowPeerForwardsTrigger(t *testing.T) {
	for _, trigger := range []string{a2a.TriggerScheduled, a2a.TriggerInteractive} {
		t.Run(trigger, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			agent.cfg.WorkflowRequestTimeoutSeconds = 1
			brk := newMockBroker()
			agent.svc.(*mockServices).brk = brk
			agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

			wt := &workflowAgentTool{
				peerName:    "wf",
				card:        a2a.AgentCard{Name: "wf"},
				agent:       agent,
				namespace:   agent.cfg.Namespace,
				inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			}
			if _, err := wt.Execute(context.Background(), map[string]any{}, &tool.Context{UserID: "u", Trigger: trigger}); err != nil {
				t.Fatal(err)
			}
			if got := firstPublishedTrigger(t, brk); got != trigger {
				t.Errorf("workflow-peer trigger = %q, want %q", got, trigger)
			}
		})
	}
}

func TestPeerForwardsTrigger(t *testing.T) {
	for _, trigger := range []string{a2a.TriggerScheduled, a2a.TriggerInteractive} {
		t.Run(trigger, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			agent.cfg.PeerRequestTimeoutSeconds = 1
			brk := newMockBroker()
			agent.svc.(*mockServices).brk = brk
			agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)

			pt := &peerAgentTool{peerName: "peer", agent: agent, namespace: agent.cfg.Namespace}
			if _, err := pt.Execute(context.Background(), map[string]any{
				"task_description": "do the thing",
			}, &tool.Context{UserID: "u", Trigger: trigger}); err != nil {
				t.Fatal(err)
			}
			if got := firstPublishedTrigger(t, brk); got != trigger {
				t.Errorf("peer trigger = %q, want %q", got, trigger)
			}
		})
	}
}
