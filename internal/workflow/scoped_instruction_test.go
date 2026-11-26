// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"testing"
)

func TestLaunchMapIteration_InstructionResolvesMapItem(t *testing.T) {
	template := &NodeConfig{ID: "impact", Type: "agent", AgentName: "casedesk", Instruction: "Which customers does incident {{_map_item.id}} affect?"}
	mapNode := &NodeConfig{ID: "each", Type: "map", TemplateNode: "impact"}
	cfg := &WorkflowConfig{Name: "Digest", Namespace: "test-ns", Nodes: []*NodeConfig{mapNode, template}}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("Digest", "exec-1"))

	e.launchMapIteration(context.Background(), mapNode, map[string]any{"id": "3101"}, 0)

	assertPublishedText(t, brk, "Which customers does incident 3101 affect?")
}

func TestDispatchLoopIteration_InstructionResolvesIteration(t *testing.T) {
	body := &NodeConfig{ID: "check", Type: "agent", AgentName: "ops", Instruction: "Attempt {{_loop_iteration}}: is the service ready?"}
	loopNode := &NodeConfig{ID: "poll", Type: "loop", BodyNode: "check", MaxIterations: 3}
	cfg := &WorkflowConfig{Name: "Poll", Namespace: "test-ns", Nodes: []*NodeConfig{loopNode, body}}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("Poll", "exec-1"))

	e.dispatchLoopIteration(context.Background(), loopNode, 2)

	assertPublishedText(t, brk, "Attempt 2: is the service ready?")
}

func assertPublishedText(t *testing.T, brk *capturingBroker, want string) {
	t.Helper()
	for _, m := range brk.snapshot() {
		if bytes.Contains(m.Payload, []byte(want)) {
			return
		}
	}
	t.Fatalf("no published request carries %q; saw topics=%v", want, capturedTopics(brk))
}
