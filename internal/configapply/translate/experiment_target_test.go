// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func TestForwardExperimentV2Target(t *testing.T) {
	cases := []struct {
		name         string
		spec         map[string]any
		wantAgent    string
		wantWorkflow string
	}{
		{"agent target", map[string]any{"name": "nightly", "targetAgent": "Orchestrator"}, "Orchestrator", ""},
		{"workflow target", map[string]any{"name": "nightly", "targetWorkflow": "order_flow"}, "", "order_flow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ForwardExperimentV2(tc.spec, ExperimentV2Hooks{})
			if err != nil {
				t.Fatalf("forward: %v", err)
			}
			if got.TargetAgent != tc.wantAgent || got.TargetWorkflow != tc.wantWorkflow {
				t.Errorf("targetAgent = %q targetWorkflow = %q, want %q and %q", got.TargetAgent, got.TargetWorkflow, tc.wantAgent, tc.wantWorkflow)
			}
		})
	}
}

func TestReverseExperimentV2Target(t *testing.T) {
	workflow := "order_flow"
	cases := []struct {
		name         string
		resp         wire.ExperimentResponse
		wantAgent    any
		wantWorkflow any
	}{
		{"agent target", wire.ExperimentResponse{Name: "nightly", TargetType: "agent", TargetAgent: "Orchestrator"}, "Orchestrator", nil},
		{"workflow target", wire.ExperimentResponse{Name: "nightly", TargetType: "workflow", TargetWorkflow: &workflow}, nil, "order_flow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReverseExperimentV2(tc.resp, ExperimentV2Hooks{})
			if err != nil {
				t.Fatalf("reverse: %v", err)
			}
			assertSpecKey(t, got, "targetAgent", tc.wantAgent)
			assertSpecKey(t, got, "targetWorkflow", tc.wantWorkflow)
		})
	}
}

func assertSpecKey(t *testing.T, spec map[string]any, key string, want any) {
	t.Helper()
	got, present := spec[key]
	if want == nil {
		if present {
			t.Errorf("%s = %v, want it omitted", key, got)
		}
		return
	}
	if got != want {
		t.Errorf("%s = %v (present %v), want %v", key, got, present, want)
	}
}
