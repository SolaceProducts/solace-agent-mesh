// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestPlanModels_PruneLeavesGrantModels(t *testing.T) {
	actual := []wire.ModelResponse{
		{ID: "id-free", Alias: "general", Provider: sp("openai"), ModelName: sp("free"), AuthType: platform.AuthTypeOAuth2Delegated},
		{ID: "id-obsolete", Alias: "obsolete", Provider: sp("openai"), ModelName: sp("gpt-5"), AuthType: "apikey"},
	}

	plan, err := planModels(t.Context(), nil, nil, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].Name != "obsolete" {
		t.Errorf("Deletes = %+v; want only 'obsolete', never an oauth2_delegated model", plan.Deletes)
	}
}

func TestPlanModels_RefusesADeclaredGrantModel(t *testing.T) {
	spec := fixtureSpec(func(m map[string]any) {
		m["authConfig"] = map[string]any{"type": platform.AuthTypeOAuth2Delegated, "grant_id": "0192f7e2-8a4b-7c3d-9e5f-1a2b3c4d5e6f"}
	})

	_, err := planModels(t.Context(), nil, []resolve.ResolvedResource{resolved("general", spec)}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be declared in config") {
		t.Fatalf("err = %v, want a refusal of the declared oauth2_delegated model", err)
	}
}
