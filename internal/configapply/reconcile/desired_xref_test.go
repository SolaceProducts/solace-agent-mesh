// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/registry"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type desiredXrefLister struct{}

func (desiredXrefLister) ListSkills(context.Context) ([]registry.NamedID, error)     { return nil, nil }
func (desiredXrefLister) ListConnectors(context.Context) ([]registry.NamedID, error) { return nil, nil }
func (desiredXrefLister) ListToolsets(context.Context) ([]registry.NamedID, error)   { return nil, nil }

func newDesiredRegistry(t *testing.T, seed func(*registry.Registry)) *registry.Registry {
	t.Helper()
	r := registry.New(desiredXrefLister{})
	if err := r.Load(context.Background()); err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	if seed != nil {
		seed(r)
	}
	return r
}

func registryHooks(r *registry.Registry) translate.AgentHooks {
	return translate.AgentHooks{
		LookupSkillID:     r.LookupSkillID,
		LookupConnectorID: r.LookupConnectorID,
		ValidateToolset:   r.ValidateToolset,
	}
}

func desiredXrefAgent(name string, spec map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Name:        name,
		Description: "desired-xref fixture",
		SourceFile:  "agents/" + name + ".yaml",
		Spec:        spec,
	}
}

func TestPlanAgents_AcceptsInManifestRefs(t *testing.T) {
	cases := []struct {
		name         string
		seed         func(*registry.Registry)
		spec         map[string]any
		assertCreate func(*testing.T, wire.CreateAgentRequest)
	}{
		{
			name: "connector",
			seed: func(r *registry.Registry) { r.SetDesiredConnectors([]string{"atlassian-mcp"}) },
			spec: map[string]any{"systemPrompt": "p", "connectors": []string{"atlassian-mcp"}},
			assertCreate: func(t *testing.T, c wire.CreateAgentRequest) {
				if len(c.ConnectorIDs) != 1 || c.ConnectorIDs[0] != "atlassian-mcp" {
					t.Errorf("expected connector sentinel = [atlassian-mcp], got %+v", c.ConnectorIDs)
				}
			},
		},
		{
			name: "toolset",
			seed: func(r *registry.Registry) { r.SetDesiredToolsets([]string{"custom-tools"}) },
			spec: map[string]any{"systemPrompt": "p", "toolsets": []string{"custom-tools"}},
			assertCreate: func(t *testing.T, c wire.CreateAgentRequest) {
				if len(c.Toolsets) != 1 || c.Toolsets[0] != "custom-tools" {
					t.Errorf("expected toolset = [custom-tools], got %+v", c.Toolsets)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newDesiredRegistry(t, tc.seed)
			plan, err := planAgents(
				t.Context(),
				nil,
				[]resolve.ResolvedResource{desiredXrefAgent("a", tc.spec)},
				nil,
				registryHooks(reg),
			)
			if err != nil {
				t.Fatalf("planAgents with desired %s failed: %v", tc.name, err)
			}
			if len(plan.Creates) != 1 {
				t.Fatalf("expected one create, got %+v", plan.Creates)
			}
			tc.assertCreate(t, plan.Creates[0].Create)
		})
	}
}

func TestPlanAgents_RejectsDanglingRefs(t *testing.T) {
	cases := []struct {
		name string
		spec map[string]any
	}{
		{name: "connector", spec: map[string]any{"systemPrompt": "p", "connectors": []string{"missing"}}},
		{name: "toolset", spec: map[string]any{"systemPrompt": "p", "toolsets": []string{"missing"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newDesiredRegistry(t, nil)
			_, err := planAgents(
				t.Context(),
				nil,
				[]resolve.ResolvedResource{desiredXrefAgent("orphan", tc.spec)},
				nil,
				registryHooks(reg),
			)
			if err == nil {
				t.Fatalf("expected error for dangling %s reference", tc.name)
			}
			if !strings.Contains(err.Error(), "not found on platform and not declared in the desired manifest") {
				t.Errorf("error message changed: %v", err)
			}
		})
	}
}

func TestNamesFromPlan_ExcludesDeletes(t *testing.T) {
	t.Run("connector", func(t *testing.T) {
		got := connectorNamesFromPlan(&connectorKindPlan{
			Creates:   []connectorOp{{Name: "c1"}},
			Updates:   []connectorOp{{Name: "c2"}},
			Unchanged: []connectorOp{{Name: "c3"}},
			Deletes:   []connectorOp{{Name: "c4"}},
		})
		assertNames(t, got, []string{"c1", "c2", "c3"})
		if connectorNamesFromPlan(nil) != nil {
			t.Errorf("nil plan should return nil slice")
		}
	})
	t.Run("toolset", func(t *testing.T) {
		got := toolsetNamesFromPlan(&toolsetPlan{
			Creates:   []toolsetOp{{Name: "t1"}},
			Updates:   []toolsetOp{{Name: "t2"}},
			Unchanged: []toolsetOp{{Name: "t3"}},
			Deletes:   []toolsetOp{{Name: "t4"}},
		})
		assertNames(t, got, []string{"t1", "t2", "t3"})
		if toolsetNamesFromPlan(nil) != nil {
			t.Errorf("nil plan should return nil slice")
		}
	})
}

func assertNames(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	gotSet := make(map[string]bool, len(got))
	for _, n := range got {
		gotSet[n] = true
	}
	for _, n := range want {
		if !gotSet[n] {
			t.Errorf("expected %q in result, got %v", n, got)
		}
	}
}

func TestResolvePlaceholderConnectorIDs(t *testing.T) {
	cases := []struct {
		name      string
		platform  []wire.ConnectorResponse
		input     []string
		want      []string
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "rewrites sentinel to UUID",
			platform: []wire.ConnectorResponse{{ID: "11111111-2222-3333-4444-555555555555", Name: "atlassian-mcp"}},
			input:    []string{"atlassian-mcp"},
			want:     []string{"11111111-2222-3333-4444-555555555555"},
		},
		{
			name:      "fails when connector vanished post-apply",
			platform:  nil,
			input:     []string{"vanished"},
			wantErr:   true,
			errSubstr: "connector apply phase",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClientForTest(t, fakeConnectorListServer(t, tc.platform))
			plan := &agentKindPlan{
				Creates: []agentOp{{
					Name:   "agent",
					Create: wire.CreateAgentRequest{ConnectorIDs: tc.input},
				}},
				Updates: []agentOp{{
					Name:   "agent-upd",
					Update: wire.UpdateAgentRequest{ConnectorIDs: append([]string(nil), tc.input...)},
				}},
			}
			err := resolvePlaceholderConnectorIDs(t.Context(), c, plan)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %v should contain %q", err, tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePlaceholderConnectorIDs: %v", err)
			}
			got := plan.Creates[0].Create.ConnectorIDs
			if len(got) != len(tc.want) || got[0] != tc.want[0] {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			gotUpd := plan.Updates[0].Update.ConnectorIDs
			if len(gotUpd) != len(tc.want) || gotUpd[0] != tc.want[0] {
				t.Errorf("update connectors got %v, want %v", gotUpd, tc.want)
			}
		})
	}
}

func TestResolvePlaceholderSkillIDs(t *testing.T) {
	c := newV2PlatformClient(t, customSkillListMux("11111111-2222-3333-4444-555555555555", "summarize"))

	updateSkills := []string{"summarize"}
	plan := &agentKindPlan{
		Creates: []agentOp{{
			Name:   "alpha",
			Create: wire.CreateAgentRequest{SkillIDs: []string{"summarize"}},
		}},
		Updates: []agentOp{
			{Name: "beta", Update: wire.UpdateAgentRequest{SkillIDs: updateSkills}},
			{Name: "gamma", Update: wire.UpdateAgentRequest{SkillIDs: nil}},
		},
	}
	if err := resolvePlaceholderSkillIDs(t.Context(), c, plan); err != nil {
		t.Fatalf("resolvePlaceholderSkillIDs: %v", err)
	}
	if got := plan.Creates[0].Create.SkillIDs[0]; got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("create sentinel not rewritten: %v", got)
	}
	if got := plan.Updates[0].Update.SkillIDs[0]; got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("update sentinel not rewritten: %v", got)
	}
	if len(plan.Updates[1].Update.SkillIDs) != 0 {
		t.Errorf("an agent with no custom skills must keep none, got %v", plan.Updates[1].Update.SkillIDs)
	}
}

func TestTranslateAgent_PromotesBuiltinSkillIDToSkillRefs(t *testing.T) {
	if !builtinskills.HasName("sam-excel") {
		t.Fatal("sam-excel must be an embedded built-in skill for this test")
	}
	r := resolve.ResolvedResource{
		Name:        "orchestrator",
		Description: "An agent that orchestrates work.",
		Spec: map[string]any{
			"systemPrompt": "system",
			"skillIds":     []any{"sam-excel"},
		},
	}
	create, err := translateAgent(r)
	if err != nil {
		t.Fatalf("translateAgent: %v", err)
	}
	if !slices.Contains(create.SkillRefs, "sam-excel") {
		t.Errorf("SkillRefs = %v, want built-in sam-excel promoted from skillIds", create.SkillRefs)
	}
}

func TestResolvePlaceholderSkillIDs_DropsBuiltins(t *testing.T) {
	if !builtinskills.HasName("sam-excel") {
		t.Fatal("sam-excel must be an embedded built-in skill for this test")
	}
	c := newV2PlatformClient(t, customSkillListMux("11111111-2222-3333-4444-555555555555", "summarize"))

	onlyBuiltin := []string{"sam-excel"}
	plan := &agentKindPlan{
		Creates: []agentOp{{
			Name:   "alpha",
			Create: wire.CreateAgentRequest{SkillIDs: []string{"summarize", "sam-excel"}},
		}},
		Updates: []agentOp{
			{Name: "beta", Update: wire.UpdateAgentRequest{SkillIDs: onlyBuiltin}},
		},
	}
	if err := resolvePlaceholderSkillIDs(t.Context(), c, plan); err != nil {
		t.Fatalf("resolvePlaceholderSkillIDs: %v", err)
	}
	got := plan.Creates[0].Create.SkillIDs
	if len(got) != 1 || got[0] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("create SkillIDs = %v, want only the resolved custom UUID (built-in dropped)", got)
	}
	if upd := plan.Updates[0].Update.SkillIDs; len(upd) != 0 {
		t.Errorf("update SkillIDs = %v, want empty (built-in-only agent)", upd)
	}
}

func TestResolvePlaceholder_NoOpEmptyPlans(t *testing.T) {
	c := newClientForTest(t, "http://invalid.example")

	if err := resolvePlaceholderSkillIDs(t.Context(), c, nil); err != nil {
		t.Errorf("nil plan: %v", err)
	}
	if err := resolvePlaceholderConnectorIDs(t.Context(), c, nil); err != nil {
		t.Errorf("nil plan: %v", err)
	}
	if err := resolvePlaceholderSkillIDs(t.Context(), c, &agentKindPlan{}); err != nil {
		t.Errorf("empty plan: %v", err)
	}
	if err := resolvePlaceholderConnectorIDs(t.Context(), c, &agentKindPlan{}); err != nil {
		t.Errorf("empty plan: %v", err)
	}
}

func TestSkillNamesFromPlan_ExcludesDeletes(t *testing.T) {
	got := skillNamesFromPlan(&skillPlan{
		Creates:   []skillOp{{Name: "s1"}},
		Updates:   []skillOp{{Name: "s2"}},
		Unchanged: []skillOp{{Name: "s3"}},
		Deletes:   []skillOp{{Name: "s4"}},
	})
	assertNames(t, got, []string{"s1", "s2", "s3"})
	if skillNamesFromPlan(nil) != nil {
		t.Errorf("nil plan should return nil slice")
	}
}

func fakeConnectorListServer(t *testing.T, conns []wire.ConnectorResponse) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+connectorsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, conns)
	})
	return newV2Platform(t, mux).URL
}

func customSkillListMux(id, name string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+skillsResource.path, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("origin") != "custom" {
			writeV2Error(w, http.StatusBadRequest, "want origin=custom", nil)
			return
		}
		writeV2Page(w, []wire.SkillResponse{{ID: id, Name: name}})
	})
	return mux
}
