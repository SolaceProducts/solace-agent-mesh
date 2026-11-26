// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestWorkflowPathEvaluatorIsSeededOnlyWithWorkflowEvals(t *testing.T) {
	for _, on := range []bool{true, false} {
		_, core := newWorkflowEvalsPlatform(t, on, rbacBuilt)

		evaluators, err := store.NewEvalEvaluatorRepo(core.DB()).List(t.Context())
		if err != nil {
			t.Fatalf("list evaluators: %v", err)
		}
		seeded := map[string]bool{}
		for _, ev := range evaluators {
			if ev.HeuristicType != nil {
				seeded[*ev.HeuristicType] = true
			}
		}
		for _, heuristic := range []string{"workflow_path", "output_facts"} {
			if seeded[heuristic] != on {
				t.Errorf("workflow_evals %v: %s seeded = %v, want %v", on, heuristic, seeded[heuristic], on)
			}
		}
	}
}
