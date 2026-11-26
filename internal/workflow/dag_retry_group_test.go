// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestAgentNodeRetryKeepsItsGroupAcrossAttempts(t *testing.T) {
	nodes := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	dag, _ := BuildDependencyGraph(nodes)
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, OutputMapping: map[string]any{"done": "yes"},
		RetryStrategy: &RetryStrategy{Limit: 1, RetryPolicy: "Always",
			Backoff: &BackoffStrategy{Duration: time.Millisecond, Factor: 1}}}
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	requestTopic := a2a.AgentRequestTopic("ns", "agentA")

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatal(err)
	}
	first := waitForAgentRequests(t, brk, requestTopic, 1)[0]
	if err := e.Advance(e.execCtx.Ctx, &EventNodeFailed{NodeID: "a", SubTaskID: first.ID,
		Err: errors.New("agent reported failure"), ErrKind: ErrorKindFailure}); err != nil {
		t.Fatal(err)
	}
	requests := waitForAgentRequests(t, brk, requestTopic, 2)

	if requests[0].ID == requests[1].ID {
		t.Fatalf("both attempts used task ID %q; every attempt needs its own", requests[0].ID)
	}
	group0, _ := requests[0].Metadata["retry_group_id"].(string)
	group1, _ := requests[1].Metadata["retry_group_id"].(string)
	if group0 == "" || group0 != group1 {
		t.Errorf("retry_group_id = %q then %q, want one stable non-empty group", group0, group1)
	}
	if requests[0].Metadata["retry_attempt"] != float64(0) || requests[1].Metadata["retry_attempt"] != float64(1) {
		t.Errorf("retry_attempt = %v then %v, want 0 then 1",
			requests[0].Metadata["retry_attempt"], requests[1].Metadata["retry_attempt"])
	}
}

func TestRetryGroupSurvivesAStateSnapshot(t *testing.T) {
	nodes := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	dag, _ := BuildDependencyGraph(nodes)
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	requestTopic := a2a.AgentRequestTopic("ns", "agentA")

	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatal(err)
	}
	original := waitForAgentRequests(t, brk, requestTopic, 1)[0]
	if group, _ := original.Metadata["retry_group_id"].(string); group == "" {
		t.Fatalf("the first attempt carried no retry_group_id: %#v", original.Metadata)
	}

	snapshot, err := json.Marshal(e.state)
	if err != nil {
		t.Fatal(err)
	}
	restored := &WorkflowExecutionState{}
	if err := json.Unmarshal(snapshot, restored); err != nil {
		t.Fatal(err)
	}
	restored.RetryCounts["a"] = 1
	resumed, resumedBrk := newCapturingExecutor(t, cfg, dag, restored)
	resumed.redispatchNode("a")

	retried := waitForAgentRequests(t, resumedBrk, requestTopic, 1)[0]
	if retried.Metadata["retry_group_id"] != original.Metadata["retry_group_id"] {
		t.Errorf("retry_group_id changed across the snapshot: %v then %v",
			original.Metadata["retry_group_id"], retried.Metadata["retry_group_id"])
	}
	if retried.Metadata["retry_attempt"] != float64(1) {
		t.Errorf("retry_attempt = %v, want 1", retried.Metadata["retry_attempt"])
	}
}

func TestMapIterationsAreDistinctRetryGroups(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "each", Type: "map", TemplateNode: "body", ItemsExpression: []any{"x", "y"}},
		{ID: "body", Type: "agent", AgentName: "agentA", DependsOn: []string{"each"}},
	}
	dag, _ := BuildDependencyGraph(nodes)
	e, brk := newCapturingExecutor(t, &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}, dag, NewExecutionState("wf", "exec-1"))

	e.executeMapNode(e.execCtx.Ctx, nodes[0])

	requests := waitForAgentRequests(t, brk, a2a.AgentRequestTopic("ns", "agentA"), 2)
	group0, _ := requests[0].Metadata["retry_group_id"].(string)
	group1, _ := requests[1].Metadata["retry_group_id"].(string)
	if group0 == "" || group1 == "" || group0 == group1 {
		t.Errorf("map iterations share or lack a retry group: %q / %q", group0, group1)
	}
	for i, r := range requests {
		if r.Metadata["retry_attempt"] != float64(0) {
			t.Errorf("iteration %d retry_attempt = %v, want 0", i, r.Metadata["retry_attempt"])
		}
	}
}

type agentRequest struct {
	ID       string
	Metadata map[string]any
}

func waitForAgentRequests(t *testing.T, brk *capturingBroker, topic string, want int) []agentRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var got []agentRequest
		for _, m := range brk.snapshot() {
			if m.Topic != topic {
				continue
			}
			var req struct {
				ID     string `json:"id"`
				Params struct {
					Message struct {
						Metadata map[string]any `json:"metadata"`
					} `json:"message"`
				} `json:"params"`
			}
			if err := json.Unmarshal(m.Payload, &req); err != nil {
				t.Fatalf("unmarshal agent request: %v", err)
			}
			got = append(got, agentRequest{ID: req.ID, Metadata: req.Params.Message.Metadata})
		}
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("saw %d requests on %s, want %d", len(got), topic, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
