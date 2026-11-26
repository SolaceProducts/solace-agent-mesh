// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

func TestResolveTarget_StaticAgentName(t *testing.T) {
	ctx := sacexpr.NewContext(nil, "", nil)
	h := &EventHandlerConfig{TargetAgentName: "my-agent"}

	name, typ, err := resolveTarget(ctx, h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "my-agent" || typ != "agent" {
		t.Errorf("got (%q, %q), want (my-agent, agent)", name, typ)
	}
}

func TestResolveTarget_DynamicAgentExpression(t *testing.T) {
	ctx := sacexpr.NewContext(map[string]any{"agent_name": "dynamic-agent"}, "", nil)
	h := &EventHandlerConfig{TargetAgentNameExpression: "input.payload:agent_name"}

	name, typ, err := resolveTarget(ctx, h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "dynamic-agent" || typ != "agent" {
		t.Errorf("got (%q, %q), want (dynamic-agent, agent)", name, typ)
	}
}

func TestResolveTarget_DynamicAgentExpression_EmptyResult(t *testing.T) {
	ctx := sacexpr.NewContext(map[string]any{"agent_name": ""}, "", nil)
	h := &EventHandlerConfig{TargetAgentNameExpression: "input.payload:agent_name"}

	_, _, err := resolveTarget(ctx, h)
	if err == nil {
		t.Fatal("expected error for empty agent name expression result")
	}
}

func TestResolveTarget_StaticWorkflowName(t *testing.T) {
	ctx := sacexpr.NewContext(nil, "", nil)
	h := &EventHandlerConfig{TargetWorkflowName: "my-workflow"}

	name, typ, err := resolveTarget(ctx, h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "my-workflow" || typ != "workflow" {
		t.Errorf("got (%q, %q), want (my-workflow, workflow)", name, typ)
	}
}

func TestResolveTarget_DynamicWorkflowExpression(t *testing.T) {
	ctx := sacexpr.NewContext(map[string]any{"wf": "dyn-workflow"}, "", nil)
	h := &EventHandlerConfig{TargetWorkflowNameExpr: "input.payload:wf"}

	name, typ, err := resolveTarget(ctx, h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "dyn-workflow" || typ != "workflow" {
		t.Errorf("got (%q, %q), want (dyn-workflow, workflow)", name, typ)
	}
}

func TestResolveTarget_DynamicWorkflowExpression_EmptyResult(t *testing.T) {
	ctx := sacexpr.NewContext(map[string]any{}, "", nil)
	h := &EventHandlerConfig{TargetWorkflowNameExpr: "input.payload:missing"}

	_, _, err := resolveTarget(ctx, h)
	if err == nil {
		t.Fatal("expected error for missing workflow name expression result")
	}
}

func TestResolveTarget_NoTarget(t *testing.T) {
	ctx := sacexpr.NewContext(nil, "", nil)
	h := &EventHandlerConfig{}

	_, _, err := resolveTarget(ctx, h)
	if err == nil {
		t.Fatal("expected error when no target configured")
	}
}

func TestResolveTarget_WorkflowTakesPrecedence(t *testing.T) {
	ctx := sacexpr.NewContext(nil, "", nil)
	h := &EventHandlerConfig{
		TargetWorkflowName: "my-workflow",
		TargetAgentName:    "my-agent",
	}

	name, typ, err := resolveTarget(ctx, h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "my-workflow" || typ != "workflow" {
		t.Errorf("got (%q, %q), want (my-workflow, workflow)", name, typ)
	}
}

func TestResolveDefinitionField_EmptyExpr(t *testing.T) {
	ctx := sacexpr.NewContext(nil, "", nil)
	got := resolveDefinitionField(ctx, "", "default-val")
	if got != "default-val" {
		t.Errorf("got %q, want default-val", got)
	}
}

func TestResolveDefinitionField_ResolvesString(t *testing.T) {
	ctx := sacexpr.NewContext(map[string]any{"name": "hello.txt"}, "", nil)
	got := resolveDefinitionField(ctx, "input.payload:name", "fallback")
	if got != "hello.txt" {
		t.Errorf("got %q, want hello.txt", got)
	}
}

func TestResolveDefinitionField_NonStringValue(t *testing.T) {
	ctx := sacexpr.NewContext(map[string]any{"count": 42}, "", nil)
	got := resolveDefinitionField(ctx, "input.payload:count", "default")
	if got == "default" {
		t.Errorf("got default, want numeric string")
	}
}

func TestResolveDefinitionField_MissingKey(t *testing.T) {
	ctx := sacexpr.NewContext(map[string]any{}, "", nil)
	got := resolveDefinitionField(ctx, "input.payload:missing", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}
