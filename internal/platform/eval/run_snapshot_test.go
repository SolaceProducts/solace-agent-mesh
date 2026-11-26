// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type fakeAgentRepo struct {
	agents map[string]*store.Agent
	rels   map[string][4][]string
	err    error
}

func (f *fakeAgentRepo) GetByName(_ context.Context, name string) (*store.Agent, []string, []string, []string, []string, error) {
	if f.err != nil {
		return nil, nil, nil, nil, nil, f.err
	}
	a, ok := f.agents[name]
	if !ok {
		return nil, nil, nil, nil, nil, nil
	}
	r := f.rels[name]
	return a, r[0], r[1], r[2], r[3], nil
}

func (f *fakeAgentRepo) GetByID(_ context.Context, id string) (*store.Agent, []string, []string, []string, []string, error) {
	if f.err != nil {
		return nil, nil, nil, nil, nil, f.err
	}
	for name, a := range f.agents {
		if a.ID == id {
			r := f.rels[name]
			return a, r[0], r[1], r[2], r[3], nil
		}
	}
	return nil, nil, nil, nil, nil, nil
}

type countingAgentRepo struct {
	byIDCalls int
}

func (c *countingAgentRepo) GetByName(_ context.Context, _ string) (*store.Agent, []string, []string, []string, []string, error) {
	return nil, nil, nil, nil, nil, nil
}

func (c *countingAgentRepo) GetByID(_ context.Context, _ string) (*store.Agent, []string, []string, []string, []string, error) {
	c.byIDCalls++
	return nil, nil, nil, nil, nil, nil
}

type fakeAgentRepoErrOnID struct{}

func (f *fakeAgentRepoErrOnID) GetByName(_ context.Context, _ string) (*store.Agent, []string, []string, []string, []string, error) {
	return nil, nil, nil, nil, nil, nil
}

func (f *fakeAgentRepoErrOnID) GetByID(_ context.Context, _ string) (*store.Agent, []string, []string, []string, []string, error) {
	return nil, nil, nil, nil, nil, errors.New("id lookup boom")
}

type fakeConnectorRepo struct{ items map[string]*store.Connector }

func (f *fakeConnectorRepo) GetByID(_ context.Context, id string) (*store.Connector, error) {
	c, ok := f.items[id]
	if !ok {
		return nil, nil
	}
	return c, nil
}

type fakeRegistry struct{ cards map[string]*a2a.AgentCard }

func (r *fakeRegistry) Get(name string) *a2a.AgentCard { return r.cards[name] }

func TestBuildAgentSnapshot_DBPathHydratesAllFields(t *testing.T) {
	t.Parallel()
	desc := "an agent"
	prompt := "you are helpful"
	provider := "general"
	repo := &fakeAgentRepo{
		agents: map[string]*store.Agent{
			"MyAgent": {
				ID:            "agent-1",
				Name:          "MyAgent",
				Description:   &desc,
				Type:          "standard",
				SystemPrompt:  &prompt,
				ModelProvider: &provider,
				Skills:        ptr(`[{"id":"s1","name":"Skill One"}]`),
				CreatedBy:     "alice",
				UpdatedBy:     "bob",
				CreatedTime:   100,
				UpdatedTime:   200,
			},
		},
		rels: map[string][4][]string{
			"MyAgent": {
				{"toolset-a", "toolset-b"},
				{"text"},
				{"text", "image"},
				{"conn-1"},
			},
		},
	}
	connectors := &fakeConnectorRepo{items: map[string]*store.Connector{
		"conn-1": {ID: "conn-1", Name: "Slack", Type: "mcp", Subtype: ptr("http")},
	}}
	resolver := func(id string) (map[string]any, map[string]any) {
		if id == "general" {
			return map[string]any{"model": "gpt-4o"},
				map[string]any{"alias": "general", "modelName": "gpt-4o"}
		}
		return nil, nil
	}

	svc := &RunService{logger: discardLogger(), agentRepo: repo, connectorRepo: connectors, modelResolver: resolver}
	snap := svc.buildAgentSnapshot(context.Background(), "MyAgent")

	if snap["name"] != "MyAgent" || snap["systemPrompt"].(*string) != &prompt {
		t.Errorf("scalar fields wrong: %+v", snap)
	}
	if got := snap["toolsets"].([]string); !reflect.DeepEqual(got, []string{"toolset-a", "toolset-b"}) {
		t.Errorf("toolsets = %v", got)
	}
	if got := snap["connectors"].([]map[string]any); len(got) != 1 || got[0]["id"] != "conn-1" {
		t.Errorf("connectors = %v", got)
	}
	if got := snap["modelConfig"].(map[string]any); got["modelName"] != "gpt-4o" {
		t.Errorf("modelConfig = %v", got)
	}
}

func TestBuildAgentSnapshot_DropsUnknownConnectors(t *testing.T) {
	t.Parallel()
	repo := &fakeAgentRepo{
		agents: map[string]*store.Agent{"A": {ID: "1", Name: "A", Type: "standard"}},
		rels:   map[string][4][]string{"A": {nil, nil, nil, {"conn-found", "conn-missing"}}},
	}
	connectors := &fakeConnectorRepo{items: map[string]*store.Connector{
		"conn-found": {ID: "conn-found", Name: "ok", Type: "mcp"},
	}}
	svc := &RunService{logger: discardLogger(), agentRepo: repo, connectorRepo: connectors}
	snap := svc.buildAgentSnapshot(context.Background(), "A")

	got := snap["connectors"].([]map[string]any)
	if len(got) != 1 || got[0]["id"] != "conn-found" {
		t.Fatalf("expected only the found connector, got %v", got)
	}
}

func TestBuildAgentSnapshot_InstanceNameFallsBackToGetByID(t *testing.T) {
	t.Parallel()
	desc := "the boss"
	repo := &fakeAgentRepo{
		agents: map[string]*store.Agent{
			"Orchestrator": {
				ID:          "019e97e8-a8e3-7975-9a8f-7a9a28b947b3",
				Name:        "Orchestrator",
				Description: &desc,
				Type:        "standard",
			},
		},
		rels: map[string][4][]string{"Orchestrator": {nil, nil, nil, nil}},
	}
	svc := &RunService{logger: discardLogger(), agentRepo: repo}
	snap := svc.buildAgentSnapshot(t.Context(), "agent_019e97e8_a8e3_7975_9a8f_7a9a28b947b3")

	if snap["name"] != "Orchestrator" {
		t.Errorf("name = %v, want 'Orchestrator' (slug should have been reversed)", snap["name"])
	}
	if snap["id"] != "019e97e8-a8e3-7975-9a8f-7a9a28b947b3" {
		t.Errorf("id = %v, want UUID from reversed slug", snap["id"])
	}
}

func TestAgentIDFromInstanceName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"agent_019e97e8_a8e3_7975_9a8f_7a9a28b947b3", "019e97e8-a8e3-7975-9a8f-7a9a28b947b3", true},
		{"agent_abc", "", false},
		{"Orchestrator", "", false},
		{"agent_", "", false},
		{"", "", false},
		{"workflow_019e045e_1491_7ba0_9611_717bd7cc595e", "", false},
	}
	for _, tc := range cases {
		got, ok := agentIDFromInstanceName(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("agentIDFromInstanceName(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBuildAgentSnapshot_InstanceNameFallsThroughToRegistry(t *testing.T) {
	t.Parallel()
	const slug = "agent_019e97e8_a8e3_7975_9a8f_7a9a28b947b3"
	repo := &fakeAgentRepo{agents: map[string]*store.Agent{}}
	card := &a2a.AgentCard{Name: slug, DefaultInputModes: []string{"text"}}
	svc := &RunService{
		logger:        discardLogger(),
		agentRepo:     repo,
		agentRegistry: &fakeRegistry{cards: map[string]*a2a.AgentCard{slug: card}},
	}
	snap := svc.buildAgentSnapshot(t.Context(), slug)
	if snap["origin"] != "discovered" {
		t.Errorf("origin = %v, want 'discovered' (registry fallback)", snap["origin"])
	}
}

func TestBuildAgentSnapshot_DisplayNameInputDoesNotTriggerIDLookup(t *testing.T) {
	t.Parallel()
	repo := &countingAgentRepo{}
	svc := &RunService{logger: discardLogger(), agentRepo: repo}
	snap := svc.buildAgentSnapshot(t.Context(), "Orchestrator")
	if snap["name"] != "Orchestrator" || len(snap) != 1 {
		t.Errorf("expected name-only fallback, got %v", snap)
	}
	if repo.byIDCalls != 0 {
		t.Errorf("GetByID called %d times for display-name input; want 0", repo.byIDCalls)
	}
}

func TestBuildAgentSnapshot_IDLookupErrorFallsThroughToRegistry(t *testing.T) {
	t.Parallel()
	const slug = "agent_019e97e8_a8e3_7975_9a8f_7a9a28b947b3"
	repo := &fakeAgentRepoErrOnID{}
	card := &a2a.AgentCard{Name: slug, DefaultInputModes: []string{"text"}}
	svc := &RunService{
		logger:        discardLogger(),
		agentRepo:     repo,
		agentRegistry: &fakeRegistry{cards: map[string]*a2a.AgentCard{slug: card}},
	}
	snap := svc.buildAgentSnapshot(t.Context(), slug)
	if snap["origin"] != "discovered" {
		t.Errorf("expected registry fallback after GetByID error, got %v", snap)
	}
}

func TestBuildAgentSnapshot_RegistryPathWhenNotInDB(t *testing.T) {
	t.Parallel()
	card := &a2a.AgentCard{
		Name:               "Disco",
		Description:        "discovered agent",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills: []a2a.AgentSkill{
			{ID: "sk1", Name: "Skill 1", Description: "first"},
		},
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI: toolsExtensionURI,
					Params: map[string]any{
						"tools": []any{
							map[string]any{"id": "t1", "name": "tool one", "description": "first tool"},
							map[string]any{"name": "t2"},
							map[string]any{},
						},
					},
				},
			},
		},
	}
	svc := &RunService{
		logger:        discardLogger(),
		agentRepo:     &fakeAgentRepo{agents: map[string]*store.Agent{}},
		agentRegistry: &fakeRegistry{cards: map[string]*a2a.AgentCard{"Disco": card}},
	}
	snap := svc.buildAgentSnapshot(context.Background(), "Disco")

	if snap["origin"] != "discovered" {
		t.Errorf("origin = %v, want 'discovered'", snap["origin"])
	}
	if snap["name"] != "Disco" {
		t.Errorf("name = %v", snap["name"])
	}
	if got := snap["tools"].([]map[string]any); len(got) != 2 {
		t.Errorf("tools = %v (want 2 valid entries)", got)
	}
}

func TestBuildAgentSnapshot_NameOnlyFallback(t *testing.T) {
	t.Parallel()
	svc := &RunService{logger: discardLogger()}
	snap := svc.buildAgentSnapshot(context.Background(), "Mystery")
	if snap["name"] != "Mystery" || len(snap) != 1 {
		t.Errorf("expected name-only fallback, got %v", snap)
	}
}

func TestBuildAgentSnapshot_RepoErrorFallsThroughToRegistry(t *testing.T) {
	t.Parallel()
	repo := &fakeAgentRepo{err: errors.New("db down")}
	card := &a2a.AgentCard{Name: "Reg", DefaultInputModes: []string{"text"}}
	svc := &RunService{
		logger:        discardLogger(),
		agentRepo:     repo,
		agentRegistry: &fakeRegistry{cards: map[string]*a2a.AgentCard{"Reg": card}},
	}
	snap := svc.buildAgentSnapshot(context.Background(), "Reg")
	if snap["origin"] != "discovered" {
		t.Errorf("expected registry fallback after repo error, got %v", snap)
	}
}

func TestBuildEvaluatorSnapshot_AddsResolvedModelAndConfig(t *testing.T) {
	t.Parallel()
	resolver := func(id string) (map[string]any, map[string]any) {
		if id != "general" {
			return nil, nil
		}
		return map[string]any{"model": "gpt-4o"},
			map[string]any{"alias": "general", "modelName": "gpt-4o"}
	}
	evals := []*store.EvalEvaluator{
		{ID: "e1", Name: "Judge", Type: "llm_judge", Model: ptr("general")},
		{ID: "e2", Name: "Heuristic", Type: "heuristic"},
	}
	out, _ := buildEvaluatorSnapshot(t.Context(), evals, evaluatorSnapshotDeps{resolver: resolver})
	if len(out) != 2 {
		t.Fatalf("got %d entries", len(out))
	}
	if out[0]["resolved_model_name"] != "gpt-4o" {
		t.Errorf("resolved_model_name = %v", out[0]["resolved_model_name"])
	}
	if mc, _ := out[0]["modelConfig"].(map[string]any); mc == nil || mc["modelName"] != "gpt-4o" {
		t.Errorf("modelConfig wrong on llm_judge: %v", out[0]["modelConfig"])
	}
	if _, ok := out[1]["modelConfig"]; ok {
		t.Errorf("heuristic snapshot should not get modelConfig: %v", out[1])
	}
}

func TestBuildEvaluatorSnapshot_UnknownModelLeavesUnenriched(t *testing.T) {
	t.Parallel()
	resolver := func(_ string) (map[string]any, map[string]any) { return nil, nil }
	evals := []*store.EvalEvaluator{
		{ID: "e1", Name: "Judge", Type: "llm_judge", Model: ptr("deleted-alias")},
	}
	out, _ := buildEvaluatorSnapshot(t.Context(), evals, evaluatorSnapshotDeps{resolver: resolver})
	if len(out) != 1 {
		t.Fatalf("got %d entries", len(out))
	}
	if _, has := out[0]["resolved_model_name"]; has {
		t.Errorf("resolved_model_name must be absent when resolver misses: %v", out[0])
	}
	if _, has := out[0]["modelConfig"]; has {
		t.Errorf("modelConfig must be absent when resolver misses: %v", out[0])
	}
}

func TestBuildEvaluatorSnapshot_NilResolverIsBackwardsCompatible(t *testing.T) {
	t.Parallel()
	evals := []*store.EvalEvaluator{
		{ID: "e1", Name: "Judge", Type: "llm_judge", Model: ptr("general")},
	}
	out, _ := buildEvaluatorSnapshot(t.Context(), evals, evaluatorSnapshotDeps{})
	if _, ok := out[0]["modelConfig"]; ok {
		t.Errorf("nil resolver should not enrich snapshot: %v", out[0])
	}
}

func TestEnrichExperimentModelSnapshots_AttachesResolvedModel(t *testing.T) {
	t.Parallel()
	resolver := func(id string) (map[string]any, map[string]any) {
		if id != "claude-config" {
			return nil, nil
		}
		return map[string]any{"model": "claude-opus-4-7"},
			map[string]any{"alias": "claude-config"}
	}
	models := []map[string]any{
		{"modelConfigId": "claude-config", "weight": 1},
		{"modelConfigId": "unknown", "weight": 1},
		{"weight": 1},
	}
	out := enrichExperimentModelSnapshots(models, resolver)
	if len(out) != 3 {
		t.Fatalf("got %d entries", len(out))
	}
	if out[0]["resolvedModel"] != "claude-opus-4-7" {
		t.Errorf("resolvedModel = %v", out[0]["resolvedModel"])
	}
	if mc, _ := out[0]["modelConfig"].(map[string]any); mc == nil {
		t.Errorf("modelConfig missing on resolved entry")
	}
	if _, mutated := models[0]["resolvedModel"]; mutated {
		t.Errorf("input map was mutated; want immutable")
	}
	if _, ok := out[1]["resolvedModel"]; ok {
		t.Errorf("unknown id should not get resolvedModel: %v", out[1])
	}
}

func TestEnrichExperimentModelSnapshots_NilResolverPassesThrough(t *testing.T) {
	t.Parallel()
	models := []map[string]any{{"modelConfigId": "x"}}
	out := enrichExperimentModelSnapshots(models, nil)
	if len(out) != 1 || out[0]["modelConfigId"] != "x" {
		t.Errorf("pass-through broken: %v", out)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }
