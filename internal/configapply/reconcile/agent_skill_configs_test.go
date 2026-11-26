// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type agentPlatform struct {
	agents   []wire.AgentResponse
	failList bool
	failGet  bool

	mu      sync.Mutex
	fetched []string
}

func (p *agentPlatform) client(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+skillsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.SkillResponse{{ID: "skill-uuid-1", Name: "render_skill"}})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+connectorsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ConnectorResponse{})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+toolsetsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ToolsetResponse{})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+agentsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		if p.failList {
			writeV2Error(w, http.StatusInternalServerError, "list failed", nil)
			return
		}
		listed := make([]wire.AgentResponse, len(p.agents))
		for i, a := range p.agents {
			a.SkillConfigs = nil
			listed[i] = a
		}
		writeV2Page(w, listed)
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+agentsResource.path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p.mu.Lock()
		p.fetched = append(p.fetched, id)
		p.mu.Unlock()
		if p.failGet {
			writeV2Error(w, http.StatusInternalServerError, "get failed", nil)
			return
		}
		i := slices.IndexFunc(p.agents, func(a wire.AgentResponse) bool { return a.ID == id })
		if i < 0 {
			writeV2Error(w, http.StatusNotFound, "agent not found", nil)
			return
		}
		writeV2Single(w, http.StatusOK, p.agents[i])
	})
	mux.HandleFunc("PUT "+platform.APIV2Prefix+agentsResource.path+"/{id}/secretsDiff", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, apiv2.SecretsDiffResult{Secrets: map[string]apiv2.SecretMatch{"api_key": {Matches: true}}})
	})
	return newClientForTest(t, newV2Platform(t, mux).URL)
}

func agentWithSkillConfig(id, name string) wire.AgentResponse {
	return wire.AgentResponse{
		ID:           id,
		Name:         name,
		Description:  "d",
		SystemPrompt: "p",
		SkillIDs:     []string{"skill-uuid-1"},
		SkillConfigs: []apiv2.SkillConfig{{
			SkillID:   "skill-uuid-1",
			SkillName: new("render_skill"),
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": "<REDACTED>", "model_id": "gpt-5"},
			},
		}},
	}
}

func TestListAgentsWithSkillConfigs_FetchesOnlyAgentsWithSkills(t *testing.T) {
	fake := &agentPlatform{agents: []wire.AgentResponse{
		agentWithSkillConfig("agent-1", "with-skill"),
		{ID: "agent-2", Name: "no-skill", SystemPrompt: "p"},
	}}
	c := fake.client(t)

	agents, err := c.ListAgentsWithSkillConfigs(t.Context())
	if err != nil {
		t.Fatalf("ListAgentsWithSkillConfigs: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(agents))
	}
	if got := agents[0].SkillConfigs; len(got) != 1 || ptrString(got[0].SkillName) != "render_skill" {
		t.Errorf("with-skill SkillConfigs = %+v, want the render_skill binding from GET /agents/{id}", got)
	}
	if !slices.Equal(fake.fetched, []string{"agent-1"}) {
		t.Errorf("fetched agents %v one by one, want only [agent-1]", fake.fetched)
	}
}

func TestListAgentsWithSkillConfigs_ListFails(t *testing.T) {
	fake := &agentPlatform{agents: []wire.AgentResponse{agentWithSkillConfig("agent-1", "x")}, failList: true}

	if _, err := fake.client(t).ListAgentsWithSkillConfigs(t.Context()); err == nil {
		t.Fatal("ListAgentsWithSkillConfigs succeeded, want the list error")
	}
	if len(fake.fetched) != 0 {
		t.Errorf("fetched agents %v one by one after the list failed, want none", fake.fetched)
	}
}

func TestListAgentsWithSkillConfigs_GetFails(t *testing.T) {
	fake := &agentPlatform{agents: []wire.AgentResponse{agentWithSkillConfig("agent-1", "with-skill")}, failGet: true}

	_, err := fake.client(t).ListAgentsWithSkillConfigs(t.Context())
	if err == nil || !strings.Contains(err.Error(), `get agent "with-skill" for its skill configs`) {
		t.Fatalf("ListAgentsWithSkillConfigs error = %v, want it to name the agent whose GET failed", err)
	}
}

func TestPlanAgents_SkillConfigsFromPlatform_NoChange(t *testing.T) {
	fake := &agentPlatform{agents: []wire.AgentResponse{agentWithSkillConfig("agent-1", "x")}}
	c := fake.client(t)
	desired := []resolve.ResolvedResource{{
		Name:        "x",
		Description: "d",
		SourceFile:  "agents/x.yaml",
		Spec: map[string]any{
			"systemPrompt": "p",
			"skillConfigs": []any{
				map[string]any{
					"skillName": "render_skill",
					"configValues": map[string]any{
						"render": map[string]any{"api_key": "sk-plain", "model_id": "gpt-5"},
					},
				},
			},
		},
	}}

	actual, err := c.ListAgentsWithSkillConfigs(t.Context())
	if err != nil {
		t.Fatalf("ListAgentsWithSkillConfigs: %v", err)
	}
	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 0 || len(plan.Unchanged) != 1 {
		t.Errorf("plan of an unchanged agent with skillConfigs: updates %+v, unchanged %+v; want no updates and one unchanged", plan.Updates, plan.Unchanged)
	}
}

func TestPlanAllAgents_UnchangedAgentWithSkillConfigsPlansNoChange(t *testing.T) {
	fake := &agentPlatform{agents: []wire.AgentResponse{agentWithSkillConfig("agent-1", "x")}}
	root := t.TempDir()
	agentYAML := `kind: agent
name: x
description: d
spec:
  systemPrompt: p
  skillRefs: [render_skill]
  skillConfigs:
    - skillName: render_skill
      configValues:
        render:
          api_key: sk-plain
          model_id: gpt-5
`
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "x.yaml"), []byte(agentYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"agents": {{Name: "x"}}}}
	out := &PlanResult{}

	if err := planAllAgents(t.Context(), fake.client(t), m, resolve.New(root, m, resolve.Options{}), out); err != nil {
		t.Fatalf("planAllAgents: %v", err)
	}
	if out.Agents == nil || len(out.Agents.Updates) != 0 || len(out.Agents.Unchanged) != 1 {
		t.Errorf("plan of an unchanged agent with skillConfigs = %+v; want no updates and one unchanged", out.Agents)
	}
}

func TestPlanAgents_SkillConfigBoundByIDOnly_NoChange(t *testing.T) {
	platform := &agentPlatform{agents: []wire.AgentResponse{agentWithSkillConfig("agent-1", "x")}}
	c := platform.client(t)
	desired := []resolve.ResolvedResource{{
		Name:        "x",
		Description: "d",
		SourceFile:  "agents/x.yaml",
		Spec: map[string]any{
			"systemPrompt": "p",
			"skillIds":     []any{"skill-uuid-1"},
			"skillConfigs": []any{
				map[string]any{
					"skillId": "skill-uuid-1",
					"configValues": map[string]any{
						"render": map[string]any{"api_key": "sk-plain", "model_id": "gpt-5"},
					},
				},
			},
		},
	}}

	actual, err := c.ListAgentsWithSkillConfigs(t.Context())
	if err != nil {
		t.Fatalf("ListAgentsWithSkillConfigs: %v", err)
	}
	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 0 || len(plan.Unchanged) != 1 {
		t.Errorf("plan of an unchanged agent bound by skillId: updates %d, unchanged %d; want no updates and one unchanged", len(plan.Updates), len(plan.Unchanged))
	}
}
