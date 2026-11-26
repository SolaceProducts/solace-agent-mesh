// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestWatchlistAgentModelAdapter_ModelProviderForAgent(t *testing.T) {
	t.Parallel()

	db := testOpenDB(t)
	repo := store.NewAgentRepo(db)
	adapter := watchlistAgentModelAdapter{repo: repo}
	ctx := t.Context()

	desc := "test fixture"
	helperProvider := "gpt-4o"
	helperID := idgen.New()
	if err := repo.Create(ctx, &store.Agent{
		ID:            helperID,
		Name:          "Order Helper",
		Description:   &desc,
		Type:          "standard",
		ModelProvider: &helperProvider,
		CreatedBy:     "test",
		UpdatedBy:     "test",
	}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed Order Helper: %v", err)
	}
	noProviderID := idgen.New()
	if err := repo.Create(ctx, &store.Agent{
		ID:          noProviderID,
		Name:        "Renamer Pro",
		Description: &desc,
		Type:        "standard",
		CreatedBy:   "test",
		UpdatedBy:   "test",
	}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed Renamer Pro: %v", err)
	}

	slugFor := func(id string) string { return "agent_" + strings.ReplaceAll(id, "-", "_") }

	cases := []struct {
		name         string
		input        string
		wantProvider string
		wantFound    bool
	}{
		{
			name:         "broker slug resolves to provider via GetByID",
			input:        slugFor(helperID),
			wantProvider: helperProvider,
			wantFound:    true,
		},
		{
			name:         "display name falls through to GetByName (built-in / A2A path)",
			input:        "Order Helper",
			wantProvider: helperProvider,
			wantFound:    true,
		},
		{
			name:         "row exists with no provider yields (\"\", true) so caller can apply default fallback",
			input:        slugFor(noProviderID),
			wantProvider: "",
			wantFound:    true,
		},
		{
			name:         "unknown built-in agent yields (\"\", false)",
			input:        "OrchestratorAgent",
			wantProvider: "",
			wantFound:    false,
		},
		{
			name:         "well-formed slug with no matching DB row falls through to GetByName and reports not-found",
			input:        slugFor(idgen.New()),
			wantProvider: "",
			wantFound:    false,
		},
		{
			name:         "non-UUID body after 'agent_' is rejected by the slug parser, falls back to name lookup",
			input:        "agent_anything",
			wantProvider: "",
			wantFound:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider, found, err := adapter.ModelProviderForAgent(ctx, tc.input)
			if err != nil {
				t.Fatalf("ModelProviderForAgent(%q): %v", tc.input, err)
			}
			if provider != tc.wantProvider {
				t.Errorf("provider = %q, want %q", provider, tc.wantProvider)
			}
			if found != tc.wantFound {
				t.Errorf("found = %v, want %v", found, tc.wantFound)
			}
		})
	}
}
