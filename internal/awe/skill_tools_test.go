// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

func TestRegisterOneToolEntry_Builtin(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	got, err := registerOneToolEntry(ag, map[string]any{
		"tool_type": "builtin",
		"tool_name": "get_current_time",
	}, exec, logger)
	if err != nil {
		t.Fatalf("registerOneToolEntry: %v", err)
	}
	if !slices.Equal(got.Added, []string{"get_current_time"}) {
		t.Errorf("got %+v, want get_current_time added", got)
	}
	if ag.Tools().GetTool("get_current_time") == nil {
		t.Error("get_current_time should be registered on the agent")
	}
}

func TestRegisterOneToolEntry_BuiltinGroup_ExpandsMembers(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())
	ag.AddSubComponent(exec)

	got, err := registerOneToolEntry(ag, map[string]any{
		"tool_type":  "builtin-group",
		"group_name": "artifact_management",
	}, exec, logger)
	if err != nil {
		t.Fatalf("registerOneToolEntry: %v", err)
	}
	want := lookupBuiltinGroup("artifact_management")
	if len(got.Added) != len(want) {
		t.Errorf("group registered %d tools, want %d (%v vs %v)", len(got.Added), len(want), got.Added, want)
	}
}

func TestRegisterOneToolEntry_UnknownToolType(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	_, err := registerOneToolEntry(ag, map[string]any{
		"tool_type": "totally_made_up",
		"tool_name": "x",
	}, exec, logger)
	if err == nil || !strings.Contains(err.Error(), "unsupported tool_type") {
		t.Errorf("err = %v, want unsupported tool_type", err)
	}
}

func TestRegisterOneToolEntry_MissingType(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	_, err := registerOneToolEntry(ag, map[string]any{}, exec, logger)
	if err == nil || !strings.Contains(err.Error(), "missing tool_type") {
		t.Errorf("err = %v, want missing tool_type", err)
	}
}

func TestRegisterOneToolEntry_MCPInvalidConfig(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	_, err := registerOneToolEntry(ag, map[string]any{
		"tool_type": "mcp",
	}, exec, logger)
	if err == nil || !strings.Contains(err.Error(), "invalid mcp tool config") {
		t.Errorf("err = %v, want invalid mcp tool config", err)
	}
}

func TestRegisterOneToolEntry_OpenAPIInvalidConfig(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	_, err := registerOneToolEntry(ag, map[string]any{
		"tool_type": "openapi",
	}, exec, logger)
	if err == nil || !strings.Contains(err.Error(), "invalid openapi tool config") {
		t.Errorf("err = %v, want invalid openapi tool config", err)
	}
}

func TestRegisterOneToolEntry_UnknownGroup(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	_, err := registerOneToolEntry(ag, map[string]any{
		"tool_type":  "builtin-group",
		"group_name": "no_such_group",
	}, exec, logger)
	if err == nil || !strings.Contains(err.Error(), "unknown builtin group") {
		t.Errorf("err = %v, want unknown builtin group", err)
	}
}

func TestRegisterOneToolEntry_GroupHandlesAlreadyRegistered(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	if _, err := registerOneToolEntry(ag, map[string]any{
		"tool_type": "builtin",
		"tool_name": "list_artifacts",
	}, exec, logger); err != nil {
		t.Fatal(err)
	}

	got, err := registerOneToolEntry(ag, map[string]any{
		"tool_type":  "builtin-group",
		"group_name": "artifact_management",
	}, exec, logger)
	if err != nil {
		t.Fatalf("group expansion: %v", err)
	}
	want := slices.DeleteFunc(lookupBuiltinGroup("artifact_management"), func(n string) bool { return n == "list_artifacts" })
	slices.Sort(got.Added)
	slices.Sort(want)
	if !slices.Equal(got.Added, want) || !slices.Equal(got.Existing, []string{"list_artifacts"}) {
		t.Errorf("group registered %+v, want %v added and list_artifacts already there", got, want)
	}
}

func TestASkillDoesNotClaimABuiltinTheAgentAlreadyHas(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())
	if _, err := toolinit.RegisterOneToolEntry(agentToolinitHost{Agent: ag}, map[string]any{
		"tool_type": "builtin",
		"tool_name": "get_current_time",
	}, exec, agentToolinitOptions(nil), slog.Default()); err != nil {
		t.Fatal(err)
	}
	ownProvenance, _ := ag.ToolProvenanceFor("get_current_time")

	mgr := skill.NewManager()
	r := &skillToolRegistrar{agent: ag, executor: exec, tools: ag.Tools(), logger: slog.Default()}
	if _, err := mgr.Hold(t.Context(), "timekit", &skill.LoadedSkill{Name: "timekit", Tools: []map[string]any{
		{"tool_type": "builtin", "tool_name": "get_current_time"},
	}}, r); err != nil {
		t.Fatal(err)
	}

	if owners := mgr.SkillToolOwners("get_current_time"); owners != nil {
		t.Errorf("get_current_time owned by %v; the agent's own tool must stay open to every session", owners)
	}
	if got, _ := ag.ToolProvenanceFor("get_current_time"); got != ownProvenance {
		t.Errorf("provenance changed to %+v by the skill load, want %+v", got, ownProvenance)
	}
}

func newSkillTestAgent(t *testing.T) (*agent.Agent, *skillToolRegistrar) {
	t.Helper()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())
	return ag, &skillToolRegistrar{agent: ag, executor: exec, tools: ag.Tools(), logger: slog.Default()}
}

func TestTwoSkillsDeclaringOneBuiltinBothOwnIt(t *testing.T) {
	t.Parallel()
	_, r := newSkillTestAgent(t)
	mgr := skill.NewManager()
	for _, name := range []string{"skill-a", "skill-b"} {
		if _, err := mgr.Hold(t.Context(), name, &skill.LoadedSkill{Name: name, Tools: []map[string]any{
			{"tool_type": "builtin", "tool_name": "get_current_time"},
		}}, r); err != nil {
			t.Fatal(err)
		}
	}
	if owners := mgr.SkillToolOwners("get_current_time"); !slices.Equal(owners, []string{"skill-a", "skill-b"}) {
		t.Errorf("owners = %v, want both skills", owners)
	}
}

func TestTwoSkillsDeclaringOneBuiltinAtOnceBothOwnIt(t *testing.T) {
	t.Parallel()
	entry := []map[string]any{{"tool_type": "builtin", "tool_name": "get_current_time"}}
	for round := range 200 {
		_, r := newSkillTestAgent(t)
		var wg sync.WaitGroup
		claims := make([][]string, 2)
		for i := range claims {
			wg.Go(func() { claims[i], _ = r.RegisterSkillTools(fmt.Sprintf("skill-%d", i), entry) })
		}
		wg.Wait()
		for i, claim := range claims {
			if !slices.Equal(claim, []string{"get_current_time"}) {
				t.Fatalf("round %d: skill-%d owns %v, want get_current_time", round, i, claim)
			}
		}
	}
}

func TestASkillBuiltinDoesNotClaimAnotherToolOfTheSameName(t *testing.T) {
	t.Parallel()
	ag, r := newSkillTestAgent(t)
	mcpLike := &fakeTool{name: "get_current_time"}
	if err := ag.Tools().Register(mcpLike); err != nil {
		t.Fatal(err)
	}
	got, retry := r.RegisterSkillTools("timekit", []map[string]any{{"tool_type": "builtin", "tool_name": "get_current_time"}})
	if len(got) != 0 || len(retry) != 0 {
		t.Errorf("owned %v, retry %v; want neither for a name another tool holds", got, retry)
	}
	if ag.Tools().GetTool("get_current_time") != mcpLike {
		t.Error("the skill's builtin replaced the tool already registered under its name")
	}
}

func TestSkillToolRegistrar_RegisterSkillTools_PartialFailure(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	r := &skillToolRegistrar{
		agent:    ag,
		executor: exec,
		tools:    ag.Tools(),
		logger:   logger,
	}
	got, retry := r.RegisterSkillTools("demo", []map[string]any{
		{"tool_type": "builtin", "tool_name": "get_current_time"},
		{"tool_type": "weird"},
	})
	if len(got) != 1 || got[0] != "get_current_time" {
		t.Errorf("got %v, want [get_current_time]", got)
	}
	if len(retry) != 0 {
		t.Errorf("retry = %v, want an invalid entry never retried", retry)
	}
}

func TestSkillToolRegistrar_ToolInstructions(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())
	r := &skillToolRegistrar{agent: ag, executor: exec, tools: ag.Tools(), logger: slog.Default()}
	r.RegisterSkillBundledTool(remotetool.SkillBundledTool{ToolName: "demo__lint", Description: "lint", Instructions: "Lint before committing."})

	if got := r.ToolInstructions("demo__lint"); got != "Lint before committing." {
		t.Errorf("ToolInstructions(demo__lint) = %q, want the bundled tool's instructions", got)
	}
	if got := r.ToolInstructions("demo__missing"); got != "" {
		t.Errorf("ToolInstructions(demo__missing) = %q, want empty for an unregistered tool", got)
	}
}

func TestSkillToolRegistrar_RegisterSkillTools_UnresolvedVault(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-vault-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	r := &skillToolRegistrar{
		agent:    ag,
		executor: exec,
		tools:    ag.Tools(),
		logger:   slog.Default(),
	}
	got, retry := r.RegisterSkillTools("demo", []map[string]any{
		{
			"tool_type":   "builtin",
			"tool_name":   "web_search_google",
			"tool_config": map[string]any{"api_key": "vault://kv/data/skills/demo#api_key"},
		},
		{"tool_type": "builtin", "tool_name": "get_current_time"},
	})
	if len(got) != 1 || got[0] != "get_current_time" {
		t.Errorf("got %v, want only [get_current_time]: the vault entry must be skipped "+
			"without taking its sibling down with it", got)
	}
	if len(retry) != 1 || retry[0]["tool_name"] != "web_search_google" {
		t.Errorf("retry = %v, want the vault entry kept for a later attempt", retry)
	}
}

func TestSkillToolRegistrar_RegisterSkillTools_SharesVaultLookups(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-memo-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	f := &stubVaultFetcher{failOn: map[string]error{"kv/data/shared": errors.New("connection refused")}}
	r := &skillToolRegistrar{
		agent:    ag,
		executor: remote.NewExecutor(svc.Broker(), "", ag.Name()),
		tools:    ag.Tools(),
		vault:    f,
		logger:   slog.Default(),
	}

	entries := make([]map[string]any, 5)
	for i := range entries {
		entries[i] = map[string]any{
			"tool_type":   "builtin",
			"tool_name":   fmt.Sprintf("tool_%d", i),
			"tool_config": map[string]any{"api_key": "vault://kv/data/shared#token"},
		}
	}
	if got, _ := r.RegisterSkillTools("demo", entries); len(got) != 0 {
		t.Errorf("got %v, want nothing registered", got)
	}
	if f.calls["kv/data/shared"] != 1 {
		t.Errorf("fetched %d times for 5 entries naming one secret, want 1",
			f.calls["kv/data/shared"])
	}
}

func TestNewSkillToolRegistrar_WiresVaultFetcher(t *testing.T) {
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-wiring-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	r := newSkillToolRegistrar(ag, remote.NewExecutor(svc.Broker(), "", ag.Name()), nil, nil, slog.Default())
	if r.vault == nil {
		t.Fatal("registrar has no vault fetcher; every vault:// reference would be reported unresolvable")
	}
	t.Setenv("VAULT_ADDR", "")
	if _, err := r.vault.Fetch("kv/data/x"); err == nil {
		t.Error("expected an error with VAULT_ADDR unset")
	} else if !strings.Contains(err.Error(), "VAULT_ADDR") {
		t.Errorf("err = %v, want VAULT_ADDR guidance", err)
	}

	if r.allowStdioMCP {
		t.Error("stdio mcp from a skill must default to refused")
	}
}

func TestSkillToolRegistrar_RegisterSkillBundledTool_InjectsToolConfig(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-cfg-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	r := &skillToolRegistrar{
		agent:    ag,
		executor: exec,
		tools:    ag.Tools(),
		toolConfigBySkill: map[string]map[string]map[string]any{
			"manim": {
				"render":  {"api_key": "sk-secret", "region": "us-east-1"},
				"cleanup": {"ttl_seconds": 3600},
			},
		},
		logger: logger,
	}

	bt := remotetool.SkillBundledTool{
		ToolName:    "manim__render",
		Description: "Render Manim animations",
	}
	r.RegisterSkillBundledTool(bt)

	got := ag.Tools().GetTool("manim__render")
	if got == nil {
		t.Fatal("manim__render not registered")
	}
	rt, ok := got.(*remote.Tool)
	if !ok {
		t.Fatalf("registered tool is %T, want *remote.Tool", got)
	}
	cfg := rt.ToolPassthroughConfig()
	if cfg == nil {
		t.Fatal("ToolPassthroughConfig is nil — skill tool_config did not flow through")
	}
	if cfg["api_key"] != "sk-secret" {
		t.Errorf("api_key = %v, want sk-secret", cfg["api_key"])
	}
	if cfg["region"] != "us-east-1" {
		t.Errorf("region = %v, want us-east-1", cfg["region"])
	}
}

func TestSkillToolRegistrar_RegisterSkillBundledTool_NoConfigForOtherSkill(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-noconfig-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	r := &skillToolRegistrar{
		agent:    ag,
		executor: exec,
		tools:    ag.Tools(),
		toolConfigBySkill: map[string]map[string]map[string]any{
			"other_skill": {"render": {"api_key": "sk-secret"}},
		},
		logger: slog.Default(),
	}

	r.RegisterSkillBundledTool(remotetool.SkillBundledTool{
		ToolName:    "manim__render",
		Description: "Render Manim animations",
	})

	got := ag.Tools().GetTool("manim__render")
	rt := got.(*remote.Tool)
	if cfg := rt.ToolPassthroughConfig(); cfg != nil {
		t.Errorf("ToolPassthroughConfig should be nil for skill not in map, got %v", cfg)
	}
}

func TestSkillToolRegistrar_RegisterSkillBundledTool_UnprefixedName(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-bare-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	r := &skillToolRegistrar{
		agent:    ag,
		executor: exec,
		tools:    ag.Tools(),
		toolConfigBySkill: map[string]map[string]map[string]any{
			"manim": {"render": {"api_key": "sk-secret"}},
		},
		logger: slog.Default(),
	}

	r.RegisterSkillBundledTool(remotetool.SkillBundledTool{
		ToolName:    "render",
		Description: "Globally exposed render",
	})

	got := ag.Tools().GetTool("render")
	rt := got.(*remote.Tool)
	if cfg := rt.ToolPassthroughConfig(); cfg != nil {
		t.Errorf("unprefixed tool should have no passthrough config, got %v", cfg)
	}
}

func TestDescribeToolEntry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		entry map[string]any
		want  string
	}{
		{map[string]any{"tool_name": "x"}, "x"},
		{map[string]any{"group_name": "g"}, "group:g"},
		{map[string]any{"tool_type": "mcp"}, "mcp"},
		{map[string]any{}, "<unknown>"},
	}
	for _, tc := range cases {
		if got := describeToolEntry(tc.entry); got != tc.want {
			t.Errorf("describeToolEntry(%v) = %q, want %q", tc.entry, got, tc.want)
		}
	}
}
