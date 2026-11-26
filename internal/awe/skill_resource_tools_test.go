// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func registerResourceTools(t *testing.T) (*agent.Agent, *skill.Manager) {
	t.Helper()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "resource-tools"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	cfg := &agent.AgentConfig{Name: "resource-tools", SkillRefs: []agent.SkillRef{{Name: "docs"}}}
	if err := LoadAndRegisterSkills(ag, cfg, svc, nil); err != nil {
		t.Fatal(err)
	}
	for _, spec := range skillruntime.ResourceTools() {
		rt, ok := ag.Tools().GetTool(spec.Name).(*remote.Tool)
		if !ok {
			t.Fatalf("%s is not registered as a remote tool", spec.Name)
		}
		rt.ApplyInitParams(&remotetool.InitParams{ToolName: spec.Name, Description: spec.Description, Parameters: spec.Parameters})
	}
	mgr, ok := ag.SkillManager().(*skill.Manager)
	if !ok {
		t.Fatalf("skill manager is %T", ag.SkillManager())
	}
	return ag, mgr
}

func TestSkillResourceToolsPresentTheirRealDescriptions(t *testing.T) {
	ag, _ := registerResourceTools(t)
	for _, spec := range skillruntime.ResourceTools() {
		if got := ag.Tools().GetTool(spec.Name).Description(); got != spec.Description {
			t.Errorf("%s description = %q, want %q", spec.Name, got, spec.Description)
		}
	}
}

func TestReadSkillResourceArgumentRejectionSaysHowToCallIt(t *testing.T) {
	ag, mgr := registerResourceTools(t)
	mgr.SetLoaded("docs", &skill.LoadedSkill{
		Name:          "docs",
		ResourceFiles: []string{"references/auth.md", "assets/schema.json"},
		ResourceCount: 2,
	})
	usage := skillruntime.ResourceToolUsage(skillruntime.ReadResourceToolName)

	cases := []struct {
		name      string
		arguments string
		wantFiles bool
	}{
		{"a grep match's file key in place of path", `{"skill_name": "docs", "file": "references/auth.md"}`, true},
		{"load_skill's name key in place of skill_name", `{"name": "docs", "path": "references/auth.md"}`, false},
		{"a line number as a string", `{"skill_name": "docs", "path": "references/auth.md", "start_line": "3"}`, true},
		{"a skill that is not loaded", `{"skill_name": "other", "file": "references/auth.md"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ag.Tools().Dispatch(t.Context(),
				tool.Call{ID: "c1", Name: skillruntime.ReadResourceToolName, Arguments: tc.arguments}, &tool.Context{})
			if err != nil {
				t.Fatal(err)
			}
			if result.ValidationError == "" {
				t.Fatalf("result = %+v, want a validation rejection", result)
			}
			if !strings.Contains(result.ValidationError, usage) {
				t.Errorf("rejection does not say which arguments the tool takes: %s", result.ValidationError)
			}
			if got := strings.Contains(result.ValidationError, "references/auth.md, assets/schema.json"); got != tc.wantFiles {
				t.Errorf("names the skill's files = %v, want %v: %s", got, tc.wantFiles, result.ValidationError)
			}
		})
	}
}

func TestSkillResourceToolsDeclareTypedArgumentsBeforeTheSTRInit(t *testing.T) {
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "resource-tools"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	cfg := &agent.AgentConfig{Name: "resource-tools", SkillRefs: []agent.SkillRef{{Name: "docs"}}}
	if err := LoadAndRegisterSkills(ag, cfg, svc, nil); err != nil {
		t.Fatal(err)
	}
	for _, spec := range skillruntime.ResourceTools() {
		schema := ag.Tools().GetTool(spec.Name).Parameters()
		want := spec.Parameters["properties"].(map[string]any)
		if len(schema.Properties) != len(want) {
			t.Errorf("%s declares %v, want %v", spec.Name, schema.Properties, want)
		}
		for name, raw := range want {
			got, _ := schema.Properties[name].(map[string]any)
			if got["type"] != raw.(map[string]any)["type"] {
				t.Errorf("%s.%s type = %v, want %v", spec.Name, name, got["type"], raw.(map[string]any)["type"])
			}
		}
		wantRequired := make([]string, 0, 2)
		for _, r := range spec.Parameters["required"].([]any) {
			wantRequired = append(wantRequired, r.(string))
		}
		slices.Sort(wantRequired)
		if !slices.Equal(schema.Required, wantRequired) {
			t.Errorf("%s required = %v, want %v", spec.Name, schema.Required, wantRequired)
		}
	}
}

func TestSkillResourceToolsReadTheSameBeforeAndAfterTheSTRInit(t *testing.T) {
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "resource-tools"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	cfg := &agent.AgentConfig{Name: "resource-tools", SkillRefs: []agent.SkillRef{{Name: "docs"}}}
	if err := LoadAndRegisterSkills(ag, cfg, svc, nil); err != nil {
		t.Fatal(err)
	}
	declared := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, d := range ag.Tools().ToLLMDefinitions() {
			b, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			out[d.Name] = string(b)
		}
		return out
	}
	before := declared()
	for _, spec := range skillruntime.ResourceTools() {
		wire, err := json.Marshal(spec.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		var params map[string]any
		if err := json.Unmarshal(wire, &params); err != nil {
			t.Fatal(err)
		}
		ag.Tools().GetTool(spec.Name).(*remote.Tool).ApplyInitParams(&remotetool.InitParams{
			ToolName: spec.Name, Description: spec.Description, Parameters: params})
	}
	after := declared()
	for _, spec := range skillruntime.ResourceTools() {
		if before[spec.Name] != after[spec.Name] {
			t.Errorf("%s changed when the STR init landed:\nbefore %s\nafter  %s", spec.Name, before[spec.Name], after[spec.Name])
		}
	}
}
