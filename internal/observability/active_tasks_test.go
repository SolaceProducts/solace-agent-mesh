// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "testing"

func TestRegisterActiveTasks(t *testing.T) {
	reg, reader := newTestRegistry(t)
	series := []ActiveTaskCount{
		{Kind: "agent", Trigger: "interactive", Count: 2},
		{Kind: "workflow", Trigger: "scheduled", Count: 1},
	}
	RegisterActiveTasks(reg, "AWE", func() []ActiveTaskCount { return series })

	dps := collectGauge(t, reader, "test.active.tasks")
	if len(dps) != 2 {
		t.Fatalf("got %d series, want 2", len(dps))
	}
	for _, dp := range dps {
		kind, _ := attrString(dp, "kind")
		trigger, _ := attrString(dp, "trigger")
		comp, _ := attrString(dp, "component_type")
		if comp != "AWE" {
			t.Errorf("component_type = %q, want AWE", comp)
		}
		switch {
		case kind == "agent" && trigger == "interactive":
			if dp.Value != 2 {
				t.Errorf("agent/interactive = %v, want 2", dp.Value)
			}
		case kind == "workflow" && trigger == "scheduled":
			if dp.Value != 1 {
				t.Errorf("workflow/scheduled = %v, want 1", dp.Value)
			}
		default:
			t.Errorf("unexpected series kind=%q trigger=%q", kind, trigger)
		}
	}

	series = nil
	if dps := collectGauge(t, reader, "test.active.tasks"); len(dps) != 0 {
		t.Fatalf("after empty: got %d series, want 0", len(dps))
	}
}

func TestRegisterAgentActiveExecutions(t *testing.T) {
	reg, reader := newTestRegistry(t)
	byAgent := map[string]int{"alpha": 3, "beta": 7}
	RegisterAgentActiveExecutions(reg, func() map[string]int { return byAgent })

	dps := collectGauge(t, reader, "test.agent.active_executions")
	if len(dps) != 2 {
		t.Fatalf("got %d series, want 2", len(dps))
	}
	for _, dp := range dps {
		agent, _ := attrString(dp, "agent")
		switch agent {
		case "alpha":
			if dp.Value != 3 {
				t.Errorf("alpha = %v, want 3", dp.Value)
			}
		case "beta":
			if dp.Value != 7 {
				t.Errorf("beta = %v, want 7", dp.Value)
			}
		default:
			t.Errorf("unexpected series agent=%q", agent)
		}
	}

	byAgent = map[string]int{}
	if dps := collectGauge(t, reader, "test.agent.active_executions"); len(dps) != 0 {
		t.Fatalf("after empty: got %d series, want 0", len(dps))
	}
}
