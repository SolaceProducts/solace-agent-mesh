// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestBuildRequestParts_StaleInstructionShipsVerbatimAndWarnsOnce(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA",
			Instruction: "Process {{prev.output.missing}} now"},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf1", "e1")
	e := newTestDAGExecutor(&WorkflowConfig{Name: "wf1", Nodes: nodes}, dag, state)
	defer e.execCtx.Cancel()

	var logs bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&logs, nil))

	state.NodeOutputs["prev"] = map[string]any{
		"output": map[string]any{"source": "database"},
	}

	var texts []string
	for range 3 {
		for _, p := range e.buildRequestParts(nodes[0], nodes[0].AgentName, "art", map[string]any{}, "", e.state.NodeOutputs) {
			if p.Kind == "text" {
				texts = append(texts, p.Text)
			}
		}
	}

	if len(texts) != 3 {
		t.Fatalf("got %d text parts across 3 calls, want 3 — the instruction must never be dropped", len(texts))
	}
	for _, got := range texts {
		if got != "Process {{prev.output.missing}} now" {
			t.Errorf("instruction = %q, want the literal left verbatim", got)
		}
	}

	if n := strings.Count(logs.String(), "instruction references an unresolved field"); n != 1 {
		t.Errorf("warned %d times across 3 calls, want 1: a 100-item map would be 100 identical lines", n)
	}
}
