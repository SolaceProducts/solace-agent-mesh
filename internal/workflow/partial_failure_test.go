// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func nonFailFastExecutorWithFailedNode(t *testing.T) *DAGExecutor {
	t.Helper()
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	state.AddPending("a")
	e.handleNodeError("a", fmt.Errorf("upstream service unreachable"))
	return e
}

func TestPartialFailureMessage_NamesTheFailedNode(t *testing.T) {
	e := nonFailFastExecutorWithFailedNode(t)

	report := e.partialFailureReport()

	if report == nil {
		t.Fatal("a run that lost a node must report a partial failure")
	}
	msg := report.message()
	for _, want := range []string{"a", "upstream service unreachable"} {
		if !strings.Contains(msg, want) {
			t.Errorf("partial-failure message %q must mention %q", msg, want)
		}
	}
}

func TestPartialFailureMetadata_ListsFailedAndSkippedNodes(t *testing.T) {
	e := nonFailFastExecutorWithFailedNode(t)

	report := e.partialFailureReport()

	if report == nil {
		t.Fatal("a run that lost a node must expose a partial-failure report")
	}
	if report.FailedNodeID != "a" {
		t.Errorf("failed_node_id = %v, want a", report.FailedNodeID)
	}
	skipped := report.SkippedNodes
	for _, want := range []string{"a", "b"} {
		if !slices.Contains(skipped, want) {
			t.Errorf("skipped_nodes %v must include %q", skipped, want)
		}
	}
}

func TestPartialFailure_CleanRunReportsNothing(t *testing.T) {
	nodes := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "e1"))
	t.Cleanup(e.execCtx.Cancel)

	if e.partialFailureReport() != nil {
		t.Error("a run with no failed node must not report a partial failure")
	}
}

func TestPartialFailure_RetryThatSucceedsIsNotReported(t *testing.T) {
	nodes := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	cfg := &WorkflowConfig{
		Nodes:    nodes,
		FailFast: false,
		RetryStrategy: &RetryStrategy{
			Limit:   1,
			Backoff: &BackoffStrategy{Duration: time.Millisecond, Factor: 1},
		},
	}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	state.AddPending("a")
	if !e.handleNodeErrorKind("a", fmt.Errorf("transient"), ErrorKindFailure) {
		t.Fatal("the first failure should be retried")
	}
	state.CompletedNodes["a"] = "artifact-a"

	if report := e.partialFailureReport(); report != nil {
		t.Errorf("a node that recovered on retry must not be reported as lost: %+v", report)
	}
}

func TestPartialFailureMetadata_ExcludesControlFlowSkips(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "gate", Type: "agent", AgentName: "agentGate"},
		{ID: "pruned", Type: "agent", AgentName: "agentPruned", DependsOn: []string{"gate"}},
		{ID: "worker", Type: "agent", AgentName: "agentWorker"},
		{ID: "downstream", Type: "agent", AgentName: "agentDown", DependsOn: []string{"worker"}},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	e.skipNodeCause("pruned", "condition not met", SkipCauseConditionFalse, false)

	state.AddPending("worker")
	e.handleNodeError("worker", fmt.Errorf("boom"))

	skipped := e.partialFailureReport().SkippedNodes

	if slices.Contains(skipped, "pruned") {
		t.Errorf("skipped_nodes %v must not include a node pruned by control flow", skipped)
	}
	for _, want := range []string{"worker", "downstream"} {
		if !slices.Contains(skipped, want) {
			t.Errorf("skipped_nodes %v must include cascade-skipped %q", skipped, want)
		}
	}
}

func TestPartialFailureMetadata_WalkDoesNotEscapeThroughNodesThatRan(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "fails", Type: "agent", AgentName: "agentF"},
		{ID: "healthy", Type: "agent", AgentName: "agentH"},
		{ID: "merge", Type: "agent", AgentName: "agentM", DependsOn: []string{"fails", "healthy"}},
		{ID: "router", Type: "agent", AgentName: "agentR", DependsOn: []string{"merge", "healthy"}},
		{ID: "branch_a", Type: "agent", AgentName: "agentA", DependsOn: []string{"router"}},
		{ID: "branch_b", Type: "agent", AgentName: "agentB", DependsOn: []string{"router"}},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	state.CompletedNodes["healthy"] = "artifact-h"
	state.AddPending("fails")
	e.handleNodeError("fails", fmt.Errorf("boom"))

	state.CompletedNodes["router"] = "artifact-r"
	e.skipNodeCause("branch_b", `not selected by switch "router"`, SkipCauseNotSelected, false)

	skipped := e.partialFailureReport().SkippedNodes

	if slices.Contains(skipped, "branch_b") {
		t.Errorf("skipped_nodes %v must not reach past a node that ran into its pruned branches", skipped)
	}
	if !slices.Contains(skipped, "fails") {
		t.Errorf("skipped_nodes %v must still include the failed node", skipped)
	}
}

func TestPartialFailureMetadata_SeedNotSkippedStillReportsItsCascade(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	state.AddPending("a")
	e.handleNodeError("a", fmt.Errorf("boom"))
	delete(state.SkippedNodes, "a")

	skipped := e.partialFailureReport().SkippedNodes

	if !slices.Contains(skipped, "b") {
		t.Errorf("skipped_nodes %v must still report the cascade below an unskipped seed", skipped)
	}
}

func TestPartialFailure_BackfillsFromErrorStateOnOldSnapshot(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	state.AddPending("a")
	e.handleNodeError("a", fmt.Errorf("upstream service unreachable"))
	state.FailedNodes = nil

	report := e.partialFailureReport()

	if len(report.FailedNodes) != 1 || report.FailedNodes[0].NodeID != "a" {
		t.Errorf("failed_nodes %v must be backfilled from ErrorState", report.FailedNodes)
	}
	if !slices.Contains(report.SkippedNodes, "b") {
		t.Errorf("skipped_nodes %v must still report the cascade after a resume", report.SkippedNodes)
	}
}

func TestPartialFailure_OldSnapshotFailureSurvivesALaterFailure(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB"},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	state.AddPending("a")
	e.handleNodeError("a", fmt.Errorf("a crashed"))
	state.FailedNodes = nil
	state.AddPending("b")
	e.handleNodeError("b", fmt.Errorf("b crashed"))

	var reported []string
	for _, f := range e.partialFailureReport().FailedNodes {
		reported = append(reported, f.NodeID)
	}
	if !slices.Equal(reported, []string{"a", "b"}) {
		t.Errorf("failed_nodes = %v, want [a b]", reported)
	}
}

func TestPartialFailure_ReportsEveryFailedNode(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB"},
		{ID: "c", Type: "agent", AgentName: "agentC"},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	for _, nodeID := range []string{"a", "b", "c", "a"} {
		state.AddPending(nodeID)
		e.handleNodeError(nodeID, fmt.Errorf("%s crashed", nodeID))
	}

	report := e.partialFailureReport()
	reported := map[string]string{}
	for _, entry := range report.FailedNodes {
		reported[entry.NodeID] = entry.ErrorMessage
	}
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := reported[want]; !ok {
			t.Errorf("failed_nodes %v must include %q — every failed branch is lost work", reported, want)
		}
		if got := reported[want]; got != want+" crashed" {
			t.Errorf("failed node %q reported message %q, want its own error", want, got)
		}
	}

	if len(report.FailedNodes) != 3 {
		t.Errorf("failed_nodes has %d entries, want one per node: %+v", len(report.FailedNodes), report.FailedNodes)
	}
	if msg := report.message(); !strings.Contains(msg, "other failed node") {
		t.Errorf("message %q must disclose that more than one node failed", msg)
	}
}

func TestPartialFailure_ErrorStateMissingFromFailedNodesIsReported(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB"},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: false}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	state.AddPending("a")
	e.handleNodeError("a", fmt.Errorf("a crashed"))
	state.ErrorState = &ErrorInfo{FailedNodeID: "b", FailureReason: "error", ErrorMessage: "b crashed"}

	var got []string
	for _, entry := range e.partialFailureReport().FailedNodes {
		got = append(got, entry.NodeID)
	}
	if want := []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("failed_nodes = %v, want %v", got, want)
	}
	if len(state.FailedNodes) != 1 {
		t.Errorf("reading the report mutated state.FailedNodes: %+v", state.FailedNodes)
	}
}
