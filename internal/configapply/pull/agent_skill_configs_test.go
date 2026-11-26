// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const pulledSkillID = "11111111-2222-3333-4444-555555555555"

type pullAgentSkillConfigsMock struct {
	agents []wire.AgentResponse

	mu      sync.Mutex
	fetched []string
}

func skilledAgent(id, name string) wire.AgentResponse {
	return wire.AgentResponse{
		ID:           id,
		Name:         name,
		SystemPrompt: "p",
		SkillIDs:     []string{pulledSkillID},
		SkillConfigs: []apiv2.SkillConfig{{
			SkillID:   pulledSkillID,
			SkillName: new("render_skill"),
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": redactedSentinel, "model_id": "gpt-5"},
			},
		}},
	}
}

func (p *pullAgentSkillConfigsMock) client(t *testing.T) *reconcile.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/skills", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.SkillResponse{{ID: pulledSkillID, Name: "render_skill"}})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/connectors", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ConnectorResponse{})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ToolsetResponse{})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/agents", func(w http.ResponseWriter, _ *http.Request) {
		listed := make([]wire.AgentResponse, len(p.agents))
		for i, a := range p.agents {
			a.SkillConfigs = nil
			listed[i] = a
		}
		writeV2Page(w, listed)
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p.mu.Lock()
		p.fetched = append(p.fetched, id)
		p.mu.Unlock()
		i := slices.IndexFunc(p.agents, func(a wire.AgentResponse) bool { return a.ID == id })
		if i < 0 {
			writeV2Error(w, http.StatusNotFound, "agent not found")
			return
		}
		writeV2Single(w, p.agents[i])
	})
	return newV2PlatformClient(t, mux)
}

func TestPullAgents_WritesSkillConfigsFromTheSingleAgentGet(t *testing.T) {
	fake := &pullAgentSkillConfigsMock{agents: []wire.AgentResponse{skilledAgent("agent-1", "skilled-agent")}}
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

	entries, _, err := pullAgents(t.Context(), fake.client(t), Options{}, res)
	if err != nil {
		t.Fatalf("pullAgents: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d agent files, want 1", len(entries))
	}
	yaml := string(entries[0].content)
	for _, want := range []string{"skillConfigs:", "skillName: render_skill", "model_id: gpt-5", "api_key: ${"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("pulled agent is missing %q:\n%s", want, yaml)
		}
	}
	if strings.Contains(yaml, redactedSentinel) {
		t.Errorf("pulled agent kept the redaction sentinel instead of a placeholder:\n%s", yaml)
	}
	if strings.Contains(yaml, pulledSkillID) {
		t.Errorf("pulled skillConfigs kept the source platform's skill ID, so applying the repo to another platform fails:\n%s", yaml)
	}
}

func TestPullAgents_ByNameFetchesOnlyThatAgent(t *testing.T) {
	fake := &pullAgentSkillConfigsMock{agents: []wire.AgentResponse{
		skilledAgent("agent-1", "wanted"),
		skilledAgent("agent-2", "other"),
	}}
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

	_, names, err := pullAgents(t.Context(), fake.client(t), Options{Name: "wanted"}, res)
	if err != nil {
		t.Fatalf("pullAgents: %v", err)
	}
	if !slices.Equal(names, []string{"wanted"}) {
		t.Errorf("pulled agents %v, want [wanted]", names)
	}
	if !slices.Equal(fake.fetched, []string{"agent-1"}) {
		t.Errorf("fetched agents %v one by one, want only [agent-1]", fake.fetched)
	}
}
