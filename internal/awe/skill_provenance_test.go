// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

var wantSkillProvenance = agent.ToolProvenance{Category: taxonomy.ToolCategorySkill}

func TestLoadAndRegisterSkillsClassifiesTheFrameworkTools(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-provenance"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	before := toolNames(ag)

	cfg := &agent.AgentConfig{
		Name:      "skill-provenance",
		SkillRefs: []agent.SkillRef{{Name: "sam-templates"}},
	}
	if err := LoadAndRegisterSkills(ag, cfg, svc, nil); err != nil {
		t.Fatal(err)
	}

	added := 0
	for _, name := range toolNames(ag) {
		if slices.Contains(before, name) {
			continue
		}
		added++
		got, classified := ag.ToolProvenanceFor(name)
		if !classified {
			t.Errorf("%s was registered by the skill system but never classified, so its use is reported by nothing", name)
			continue
		}
		if got != wantSkillProvenance {
			t.Errorf("%s provenance = %+v, want %+v", name, got, wantSkillProvenance)
		}
	}
	if added == 0 {
		t.Fatal("the skill system registered no tools, so this test proves nothing")
	}
}

func toolNames(ag *agent.Agent) []string {
	registered := ag.Tools().ListTools()
	names := make([]string, 0, len(registered))
	for _, registeredTool := range registered {
		names = append(names, registeredTool.Name())
	}
	return names
}

func TestRegisterSkillBundledToolIsClassified(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "bundled-provenance"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())
	registrar := newSkillToolRegistrar(ag, exec, nil, nil, slogDiscard())

	const bundled = "acme_payroll__lookup"
	registrar.RegisterSkillBundledTool(remotetool.SkillBundledTool{ToolName: bundled})

	got, classified := ag.ToolProvenanceFor(bundled)
	if !classified {
		t.Fatal("a skill-bundled tool was registered but never classified, so skill adoption is undercounted")
	}
	if got != wantSkillProvenance {
		t.Errorf("bundled tool provenance = %+v, want %+v", got, wantSkillProvenance)
	}
}

func TestASkillDeclaredToolIsClassifiedAsASkillTool(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "skill-declared"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	exec := remote.NewExecutor(svc.Broker(), "", ag.Name())

	result, err := registerOneToolEntry(ag, map[string]any{
		"tool_type": "builtin",
		"tool_name": "get_current_time",
	}, exec, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(result.Added, "get_current_time") {
		t.Fatalf("registered %+v, want get_current_time added", result)
	}

	got, classified := ag.ToolProvenanceFor("get_current_time")
	if !classified {
		t.Fatal("a skill-declared tool was registered but never classified")
	}
	if got != wantSkillProvenance {
		t.Errorf("skill-declared tool provenance = %+v, want %+v", got, wantSkillProvenance)
	}
}

func TestTheSkillCategorySurvivesToTheWire(t *testing.T) {
	t.Parallel()
	category := toolinit.SkillProvenance().Category

	var run usagefacts.Facts
	for _, other := range taxonomy.ToolCategories {
		if other != category {
			run.AddToolCategory(other)
		}
	}
	run.AddToolCategory(category)

	sent := map[string]any{}
	run.AddTo(sent)

	var received usagefacts.Facts
	received.Merge(sent)
	arrived := map[string]any{}
	received.AddTo(arrived)

	categories, _ := arrived[a2a.MetadataToolCategories].([]string)
	if !slices.Contains(categories, category) {
		t.Errorf("%q did not survive the round trip; arrived with %v", category, categories)
	}
}
