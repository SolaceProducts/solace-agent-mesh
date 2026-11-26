// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type fakePeerFilters struct {
	managed map[string]toolAuthPeerFilter
	err     error
	asked   []string
}

func (f *fakePeerFilters) PeerFilter(_ context.Context, agentName string) (toolAuthPeerFilter, bool, error) {
	f.asked = append(f.asked, agentName)
	if f.err != nil {
		return toolAuthPeerFilter{}, false, f.err
	}
	filter, ok := f.managed[agentName]
	return filter, ok, nil
}

type fakeMesh []toolAuthMeshAgent

func (m fakeMesh) MeshAgents() []toolAuthMeshAgent { return m }

func denyAll() toolAuthPeerFilter { return toolAuthPeerFilter{AllowList: []string{}} }

func allow(names ...string) toolAuthPeerFilter {
	return toolAuthPeerFilter{AllowList: names}
}

func graphNames(t *testing.T, graph toolAuthGraph) []string {
	t.Helper()
	names := make([]string, 0, len(graph.Agents))
	for i, node := range graph.Agents {
		if (i == 0) != node.IsTarget {
			t.Fatalf("expected exactly the first node to be the target, got IsTarget=%v at index %d", node.IsTarget, i)
		}
		names = append(names, node.AgentName)
	}
	return names
}

func TestDiscoverToolAuthGraph(t *testing.T) {
	tests := []struct {
		name         string
		filters      map[string]toolAuthPeerFilter
		mesh         fakeMesh
		wantAgents   []string
		wantWildcard bool
	}{
		{
			name: "linear chain is walked transitively",
			filters: map[string]toolAuthPeerFilter{
				"Alpha": allow("Beta"),
				"Beta":  allow("Gamma"),
				"Gamma": denyAll(),
			},
			wantAgents: []string{"Alpha", "Beta", "Gamma"},
		},
		{
			name: "a cycle visits each agent once",
			filters: map[string]toolAuthPeerFilter{
				"Alpha": allow("Beta"),
				"Beta":  allow("Gamma"),
				"Gamma": allow("Alpha", "Beta"),
			},
			wantAgents: []string{"Alpha", "Beta", "Gamma"},
		},
		{
			name: "the depth cap stops the walk and reports discovery incomplete",
			filters: map[string]toolAuthPeerFilter{
				"Alpha":   allow("Beta"),
				"Beta":    allow("Gamma"),
				"Gamma":   allow("Delta"),
				"Delta":   allow("Epsilon"),
				"Epsilon": denyAll(),
			},
			wantAgents:   []string{"Alpha", "Beta", "Gamma", "Delta"},
			wantWildcard: true,
		},
		{
			name: "a graph ending exactly at the depth cap is complete",
			filters: map[string]toolAuthPeerFilter{
				"Alpha": allow("Beta"),
				"Beta":  allow("Gamma"),
				"Gamma": allow("Delta"),
				"Delta": denyAll(),
			},
			wantAgents: []string{"Alpha", "Beta", "Gamma", "Delta"},
		},
		{
			name:         "a wildcard allow list is not enumerated",
			filters:      map[string]toolAuthPeerFilter{"Alpha": allow("*")},
			mesh:         fakeMesh{{RoutingName: "uuid-1", DisplayName: "Beta"}},
			wantAgents:   []string{"Alpha"},
			wantWildcard: true,
		},
		{
			name:         "an agent the platform does not manage is treated as wildcard",
			filters:      map[string]toolAuthPeerFilter{},
			wantAgents:   []string{"Alpha"},
			wantWildcard: true,
		},
		{
			name: "a peer reached through a wildcard peer flags the graph incomplete",
			filters: map[string]toolAuthPeerFilter{
				"Alpha": allow("Beta"),
				"Beta":  {AllowList: nil},
			},
			wantAgents:   []string{"Alpha", "Beta"},
			wantWildcard: true,
		},
		{
			name: "a peer that is not running is still discovered",
			filters: map[string]toolAuthPeerFilter{
				"Alpha": allow("Ghost"),
			},
			mesh:         fakeMesh{{RoutingName: "uuid-1", DisplayName: "Alpha"}},
			wantAgents:   []string{"Alpha", "Ghost"},
			wantWildcard: true,
		},
		{
			name: "glob entries expand against the running mesh",
			filters: map[string]toolAuthPeerFilter{
				"Alpha": allow("Conf*"),
			},
			mesh: fakeMesh{
				{RoutingName: "uuid-1", DisplayName: "Confluence Agent"},
				{RoutingName: "Conf-slug", DisplayName: "Legacy Agent"},
				{RoutingName: "uuid-3", DisplayName: "Jira Agent"},
			},
			wantAgents:   []string{"Alpha", "Confluence Agent", "Legacy Agent"},
			wantWildcard: true,
		},
		{
			name: "an entry naming a deployment slug resolves to the display name",
			filters: map[string]toolAuthPeerFilter{
				"Alpha": allow("uuid-beta"),
			},
			mesh:         fakeMesh{{RoutingName: "uuid-beta", DisplayName: "Beta"}},
			wantAgents:   []string{"Alpha", "Beta"},
			wantWildcard: true,
		},
		{
			name: "the deny list wins over the allow list",
			filters: map[string]toolAuthPeerFilter{
				"Alpha": {AllowList: []string{"Beta", "Gamma"}, DenyList: []string{"Gamma"}},
				"Beta":  denyAll(),
			},
			wantAgents: []string{"Alpha", "Beta"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := discoverToolAuthGraph(t.Context(), "Alpha", nil, &fakePeerFilters{managed: tc.filters}, tc.mesh)
			if err != nil {
				t.Fatalf("discoverToolAuthGraph: %v", err)
			}
			if got := graphNames(t, graph); !slices.Equal(got, tc.wantAgents) {
				t.Fatalf("agents = %v, want %v", got, tc.wantAgents)
			}
			if graph.AllowListIsWildcard != tc.wantWildcard {
				t.Fatalf("AllowListIsWildcard = %v, want %v", graph.AllowListIsWildcard, tc.wantWildcard)
			}
		})
	}
}

func TestDiscoverToolAuthGraphPropagatesFilterErrors(t *testing.T) {
	sentinel := errors.New("db down")
	_, err := discoverToolAuthGraph(t.Context(), "Alpha", nil, &fakePeerFilters{err: sentinel}, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestDiscoverToolAuthGraphWithoutFilterSource(t *testing.T) {
	graph, err := discoverToolAuthGraph(t.Context(), "Alpha", nil, nil, nil)
	if err != nil {
		t.Fatalf("discoverToolAuthGraph: %v", err)
	}
	if names := graphNames(t, graph); len(names) != 1 || names[0] != "Alpha" {
		t.Fatalf("agents = %v, want just the target", names)
	}
	if graph.AllowListIsWildcard {
		t.Fatal("AllowListIsWildcard should stay false when no config source is wired")
	}
}

func TestDiscoverToolAuthGraphPinnedAgents(t *testing.T) {
	filters := map[string]toolAuthPeerFilter{
		"Alpha":  allow("Beta"),
		"Beta":   denyAll(),
		"Pinned": allow("Gamma"),
		"Gamma":  denyAll(),
	}
	pinned := []toolAuthGraphNode{
		{AgentName: "Pinned", AddedBy: "ops@test"},
		{AgentName: "beta", AddedBy: "ops@test"},
		{AgentName: "alpha", AddedBy: "ops@test"},
	}
	graph, err := discoverToolAuthGraph(t.Context(), "Alpha", pinned, &fakePeerFilters{managed: filters}, nil)
	if err != nil {
		t.Fatalf("discoverToolAuthGraph: %v", err)
	}
	want := []toolAuthGraphNode{
		{AgentName: "Alpha", IsTarget: true},
		{AgentName: "Beta", AddedBy: "ops@test"},
		{AgentName: "Pinned", AddedBy: "ops@test"},
		{AgentName: "Gamma"},
	}
	if !reflect.DeepEqual(graph.Agents, want) {
		t.Fatalf("agents = %+v, want %+v", graph.Agents, want)
	}
	if graph.AllowListIsWildcard {
		t.Fatal("AllowListIsWildcard should stay false: every agent's allow list was enumerated")
	}

	graph, err = discoverToolAuthGraph(t.Context(), "Alpha", pinned[:1], nil, nil)
	if err != nil {
		t.Fatalf("discoverToolAuthGraph: %v", err)
	}
	if names := graphNames(t, graph); len(names) != 2 || names[1] != "Pinned" {
		t.Fatalf("agents = %v, want the target plus the pinned agent", names)
	}
}

func TestToolAuthGraphAdmitsUnenumerated(t *testing.T) {
	secrets := toolAuthMeshAgent{RoutingName: "uuid-secrets", DisplayName: "Secrets Agent"}
	reporter := toolAuthMeshAgent{RoutingName: "uuid-reporter", DisplayName: "Reporter"}

	t.Run("a denied peer is refused", func(t *testing.T) {
		graph, err := discoverToolAuthGraph(t.Context(), "Alpha", nil,
			&fakePeerFilters{managed: map[string]toolAuthPeerFilter{
				"Alpha": {AllowList: []string{"*"}, DenyList: []string{"Secrets*"}},
			}}, fakeMesh{secrets, reporter})
		if err != nil {
			t.Fatal(err)
		}
		if !graph.AllowListIsWildcard {
			t.Fatal("expected the wildcard allow list to report discovery incomplete")
		}
		if graph.AdmitsUnenumerated(secrets) {
			t.Error("an agent the deny list excludes must not be admitted")
		}
		if !graph.AdmitsUnenumerated(reporter) {
			t.Error("an agent the deny list does not name must stay admitted")
		}
	})

	t.Run("the deny list matches the routing name too", func(t *testing.T) {
		graph, err := discoverToolAuthGraph(t.Context(), "Alpha", nil,
			&fakePeerFilters{managed: map[string]toolAuthPeerFilter{
				"Alpha": {AllowList: []string{"*"}, DenyList: []string{"uuid-secrets"}},
			}}, fakeMesh{secrets, reporter})
		if err != nil {
			t.Fatal(err)
		}
		if graph.AdmitsUnenumerated(secrets) {
			t.Error("a deny pattern written against the broker name must apply")
		}
	})

	t.Run("one permissive delegate is enough", func(t *testing.T) {
		graph, err := discoverToolAuthGraph(t.Context(), "Alpha", nil,
			&fakePeerFilters{managed: map[string]toolAuthPeerFilter{
				"Alpha": allow("Beta", "Gamma"),
				"Beta":  {AllowList: []string{"*"}, DenyList: []string{"Secrets*"}},
				"Gamma": {AllowList: []string{"*"}},
			}}, fakeMesh{secrets, reporter})
		if err != nil {
			t.Fatal(err)
		}
		if !graph.AdmitsUnenumerated(secrets) {
			t.Error("expected the delegate with no deny list to admit the peer")
		}
	})

	t.Run("a fully enumerated graph admits nothing extra", func(t *testing.T) {
		graph, err := discoverToolAuthGraph(t.Context(), "Alpha", nil,
			&fakePeerFilters{managed: map[string]toolAuthPeerFilter{"Alpha": denyAll()}}, fakeMesh{reporter})
		if err != nil {
			t.Fatal(err)
		}
		if graph.AdmitsUnenumerated(reporter) {
			t.Error("nothing outside a complete graph may be admitted")
		}
	})
}

func TestPeerFilter_ReadsTheDeployedConfigNotTheDraft(t *testing.T) {
	t.Parallel()

	const rowWildcard = `{"interAgentCommunication":{"allowList":["*"]}}`
	const rowNarrow = `{"interAgentCommunication":{"allowList":["Reporter"]}}`
	deployedWith := func(allowList string) string {
		return "apps:\n  - name: Orchestrator\n    app_config:\n      inter_agent_communication:\n        allow_list: " + allowList + "\n"
	}

	tests := []struct {
		name          string
		row           string
		snapshot      string
		undeployed    bool
		noDeployRepo  bool
		wantAllowList []string
		wantDenyList  []string
	}{
		{
			name:          "a drifted row does not shrink the deployed graph",
			row:           `{}`,
			snapshot:      deployedWith(`["*"]`),
			wantAllowList: []string{"*"},
		},
		{
			name:          "the deployed list wins even when it is the narrower one",
			row:           rowWildcard,
			snapshot:      deployedWith(`["Reporter"]`),
			wantAllowList: []string{"Reporter"},
		},
		{
			name:          "a deployed deny-all is not widened by a drifted row",
			row:           rowWildcard,
			snapshot:      deployedWith(`[]`),
			wantAllowList: []string{},
		},
		{
			name:          "an absent allow_list in the deployed block is allow-all, and its deny list survives",
			row:           rowNarrow,
			snapshot:      "apps:\n  - name: Orchestrator\n    app_config:\n      inter_agent_communication:\n        deny_list: [\"Blocked\"]\n",
			wantAllowList: nil,
			wantDenyList:  []string{"Blocked"},
		},
		{
			name:          "falls back to the row when nothing was deployed",
			row:           `{"interAgentCommunication":{"allowList":["Reporter"],"denyList":["Blocked"]}}`,
			wantAllowList: []string{"Reporter"},
			wantDenyList:  []string{"Blocked"},
		},
		{
			name:          "falls back to the row once the deployed config is undeployed",
			row:           rowNarrow,
			snapshot:      deployedWith(`["*"]`),
			undeployed:    true,
			wantAllowList: []string{"Reporter"},
		},
		{
			name:          "falls back when no deployment repo is wired",
			row:           rowNarrow,
			snapshot:      deployedWith(`["*"]`),
			noDeployRepo:  true,
			wantAllowList: []string{"Reporter"},
		},
		{
			name:          "falls back when the snapshot carries no peer block",
			row:           rowNarrow,
			snapshot:      `{"agentUrl":"https://remote.example","name":"Remote"}`,
			wantAllowList: []string{"Reporter"},
		},
		{
			name:          "falls back when the snapshot does not parse",
			row:           rowNarrow,
			snapshot:      "apps: [oops\n",
			wantAllowList: []string{"Reporter"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			db := testOpenDB(t)
			agentRepo := store.NewAgentRepo(db)
			deploymentRepo := store.NewAgentDeploymentRepo(db)

			agent := &store.Agent{
				Name:                     "Orchestrator",
				AdditionalConfigurations: []byte(tc.row),
				CreatedBy:                "t",
				UpdatedBy:                "t",
			}
			if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
				t.Fatalf("seed agent: %v", err)
			}
			if tc.snapshot != "" {
				snapshot := tc.snapshot
				if err := deploymentRepo.Create(ctx, &store.AgentDeployment{
					AgentID:             agent.ID,
					Status:              "success",
					Action:              "deploy",
					AgentConfigSnapshot: &snapshot,
					CreatedBy:           "t",
				}); err != nil {
					t.Fatalf("seed deployment: %v", err)
				}
			}
			if tc.undeployed {
				if err := deploymentRepo.Create(ctx, &store.AgentDeployment{
					AgentID:   agent.ID,
					Status:    "success",
					Action:    "undeploy",
					CreatedBy: "t",
				}); err != nil {
					t.Fatalf("seed undeploy: %v", err)
				}
			}

			adapter := &agentPeerFilterAdapter{repo: agentRepo, logger: slog.New(slog.DiscardHandler)}
			if !tc.noDeployRepo {
				adapter.deployments = deploymentRepo
			}
			filter, managed, err := adapter.PeerFilter(ctx, "Orchestrator")
			if err != nil {
				t.Fatalf("PeerFilter: %v", err)
			}
			if !managed {
				t.Fatal("managed = false, want true for an agent the platform holds a row for")
			}
			if !slices.Equal(filter.AllowList, tc.wantAllowList) {
				t.Errorf("AllowList = %#v, want %#v", filter.AllowList, tc.wantAllowList)
			}
			if (filter.AllowList == nil) != (tc.wantAllowList == nil) {
				t.Errorf("AllowList nil = %v, want %v (nil is allow-all, empty is deny-all)",
					filter.AllowList == nil, tc.wantAllowList == nil)
			}
			if !slices.Equal(filter.DenyList, tc.wantDenyList) {
				t.Errorf("DenyList = %#v, want %#v", filter.DenyList, tc.wantDenyList)
			}
		})
	}
}

func TestPeerFilter_SurfacesADeploymentReadFailure(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)

	agent := &store.Agent{Name: "Agent", CreatedBy: "t", UpdatedBy: "t"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	brokenDB := testOpenDB(t)
	if err := brokenDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	adapter := &agentPeerFilterAdapter{repo: agentRepo, deployments: store.NewAgentDeploymentRepo(brokenDB)}

	if _, _, err := adapter.PeerFilter(ctx, "Agent"); err == nil {
		t.Fatal("PeerFilter err = nil, want the deployments read failure surfaced")
	}
}
