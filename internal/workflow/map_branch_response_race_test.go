// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestConcurrentMapBranchResponsesCompleteTheirOwnItems(t *testing.T) {
	const items, limit = 24, 3
	nodes := []*NodeConfig{
		{ID: "each", Type: "map", TemplateNode: "body", ItemsExpression: make([]any, items), ConcurrencyLimit: limit},
		{ID: "body", Type: "agent", AgentName: "Worker", DependsOn: []string{"each"}},
	}
	e, _ := newUsageFactsExecutor(t, nodes)
	if err := e.Advance(context.Background(), &EventStart{}); err != nil {
		t.Fatalf("start: %v", err)
	}

	for delivered := 0; delivered < items; {
		wave := unansweredBranches(e, "each", delivered)
		if len(wave) == 0 {
			t.Fatalf("no branch in flight after %d of %d responses", delivered, items)
		}
		var wg sync.WaitGroup
		for _, branch := range wave {
			wg.Go(func() { deliverBranchItem(t, e, branch) })
		}
		wg.Wait()
		delivered += len(wave)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, done := e.state.CompletedNodes["each"]; !done {
		t.Fatalf("map did not complete; pending=%v", e.state.PendingNodes)
	}
	results, _ := e.state.NodeOutputs["each"]["output"].(map[string]any)["results"].([]any)
	if len(results) != items {
		t.Fatalf("map produced %d results, want %d", len(results), items)
	}
	for i, got := range results {
		want := map[string]any{"text": itemReply(i)}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("results[%d] = %v, want %v", i, got, want)
		}
	}
}

func unansweredBranches(e *DAGExecutor, mapNodeID string, skip int) []BranchInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]BranchInfo(nil), e.state.ActiveBranches[mapNodeID][skip:]...)
}

func itemReply(index int) string {
	return fmt.Sprintf("item %d", index)
}

func deliverBranchItem(t *testing.T, e *DAGExecutor, branch BranchInfo) {
	t.Helper()
	task := a2a.Task{
		ID:   branch.SubTaskID,
		Kind: "task",
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateCompleted,
			Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart(itemReply(branch.Index))}},
		},
	}
	payload, err := json.Marshal(a2a.NewResponse(branch.SubTaskID, task))
	if err != nil {
		t.Errorf("marshal branch response: %v", err)
		return
	}
	e.instance.handleAgentResponse(context.Background(),
		a2a.Request{ID: branch.SubTaskID}, &broker.Message{Payload: payload})
}
