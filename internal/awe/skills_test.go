// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func TestLoadAndRegisterSkills_SeedsManagerNotCardSkills(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{
		Name:      "skill-agent",
		Namespace: "ns",
		Skills:    []a2a.AgentSkill{{ID: "search", Name: "Search", Description: "Find things"}},
	})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	cfg := &agent.AgentConfig{
		Name:      "skill-agent",
		Namespace: "ns",
		SkillRefs: []agent.SkillRef{{Name: "pptx"}},
	}
	exec := remote.NewExecutor(svc.Broker(), "ns", ag.Name())
	if err := LoadAndRegisterSkills(ag, cfg, svc, exec); err != nil {
		t.Fatalf("LoadAndRegisterSkills: %v", err)
	}

	mgr := ag.SkillManager()
	if mgr == nil {
		t.Fatal("skill manager not set on agent")
	}
	avail := mgr.AvailableSkillsForCard()
	if len(avail) != 1 || avail[0].Name != "pptx" {
		t.Fatalf("manager available = %v, want [pptx]", avail)
	}

	card := ag.BuildAgentCard()
	if len(card.Skills) != 1 || card.Skills[0].ID != "search" {
		t.Fatalf("card.Skills = %v, want only curated 'search'", card.Skills)
	}

	var found bool
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == "https://solace.com/a2a/extensions/sam/skills" {
			found = true
			skills, ok := ext.Params["skills"].([]any)
			if !ok || len(skills) != 1 {
				t.Fatalf("sam/skills params = %v", ext.Params["skills"])
			}
		}
	}
	if !found {
		t.Error("missing sam/skills extension")
	}
}

func TestPropagateBundledToolUpdates_AppliesNewSchema(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(brk, "ns", "agent-1")

	rt := remote.NewTool(&remote.ToolConfig{
		ToolName:    "myskill__count_text",
		Description: "Counts text",
	}, exec)
	exec.RegisterTool(rt)

	rt.ApplyInitParams(&remotetool.InitParams{
		ToolName:     "myskill__count_text",
		Instructions: "schema-v1",
		Parameters: map[string]any{
			"properties": map[string]any{},
		},
	})

	updated := []remotetool.SkillBundledTool{{
		ToolName:     "myskill__count_text",
		Description:  "Counts text",
		Instructions: "schema-v2",
		Parameters: map[string]any{
			"properties": map[string]any{
				"text":   map[string]any{"type": "string"},
				"locale": map[string]any{"type": "string"},
			},
			"required": []any{"text"},
		},
	}}

	propagateBundledToolUpdates(exec, updated)

	if got := rt.Instructions(); got != "schema-v2" {
		t.Errorf("instructions = %q, want %q", got, "schema-v2")
	}
	schema := rt.Parameters()
	if schema == nil {
		t.Fatal("Parameters() returned nil")
	}
	if len(schema.Properties) != 2 {
		t.Errorf("Properties count = %d, want 2", len(schema.Properties))
	}
}

func TestPropagateBundledToolUpdates_KeepsTheToolsEffect(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(brk, "ns", "agent-1")
	rt := remote.NewTool(&remote.ToolConfig{ToolName: "myskill__count_text", Description: "Counts text"}, exec)
	exec.RegisterTool(rt)
	declaring := &remotetool.InitParams{
		ToolName: "myskill__count_text", Instructions: "v1", Effect: remotetool.EffectReadOnly,
	}
	rt.ApplyInitParams(declaring)

	propagateBundledToolUpdates(exec, []remotetool.SkillBundledTool{{ToolName: "myskill__count_text", Instructions: "v2"}})
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after a skill init, EffectClassOf = %+v, want the broadcast's read_only kept", got)
	}
	if got := rt.Instructions(); got != "v2" {
		t.Fatalf("after a skill init, instructions = %q, want v2", got)
	}

	rt.ApplyInitParams(declaring)
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after the STR re-announces the same init, EffectClassOf = %+v, want read_only", got)
	}

	rt.ApplyInitParams(&remotetool.InitParams{ToolName: "myskill__count_text", Instructions: "v3"})
	if got := tool.EffectClassOf(rt); got != tool.DefaultEffectClass {
		t.Fatalf("after an STR init that drops the effect, EffectClassOf = %+v, want the default", got)
	}
}

func TestPropagateBundledToolUpdates_SkipsUnregistered(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(brk, "ns", "agent-1")

	propagateBundledToolUpdates(exec, []remotetool.SkillBundledTool{{
		ToolName:     "unknown_skill__some_tool",
		Instructions: "ignored",
		Parameters: map[string]any{
			"properties": map[string]any{"x": map[string]any{"type": "string"}},
		},
	}})

	if got := exec.LookupTool("unknown_skill__some_tool"); got != nil {
		t.Errorf("lookup created a tool unexpectedly: %v", got)
	}
}

func TestPropagateBundledToolUpdates_SkipsEmptyPayload(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(brk, "ns", "agent-1")

	rt := remote.NewTool(&remote.ToolConfig{
		ToolName:    "myskill__count_text",
		Description: "Counts text",
	}, exec)
	exec.RegisterTool(rt)
	rt.ApplyInitParams(&remotetool.InitParams{
		ToolName:     "myskill__count_text",
		Instructions: "preserved",
		Parameters: map[string]any{
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
	})

	propagateBundledToolUpdates(exec, []remotetool.SkillBundledTool{{
		ToolName: "myskill__count_text",
	}})

	if got := rt.Instructions(); got != "preserved" {
		t.Errorf("instructions overwritten with empty payload: got %q", got)
	}
}

func TestRegisterSkillBundledTool_ARegistrationDeclaresNoEffectAndLeavesTheNextBroadcastFree(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-effect-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	r := &skillToolRegistrar{agent: ag, executor: remote.NewExecutor(svc.Broker(), "", ag.Name()), tools: ag.Tools(), logger: slog.Default()}
	r.RegisterSkillBundledTool(remotetool.SkillBundledTool{ToolName: "manim__render", Instructions: "render it", Parameters: map[string]any{"properties": map[string]any{}}})

	rt, ok := ag.Tools().GetTool("manim__render").(*remote.Tool)
	if !ok {
		t.Fatalf("registered tool is %T, want *remote.Tool", ag.Tools().GetTool("manim__render"))
	}
	rt.ApplyInitParams(&remotetool.InitParams{ToolName: "manim__render", Instructions: "render it", Effect: remotetool.EffectReadOnly, InstanceID: "pod-a"})
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("a read_only broadcast right after load_skill: EffectClassOf = %+v, want read_only, not held back by the registration", got)
	}
}
