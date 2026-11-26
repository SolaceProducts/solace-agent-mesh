// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "testing"

func TestToolInputProblem_ATemplatedKeyIsNotGraded(t *testing.T) {
	for _, key := range []string{"${TRACKING_KEY}", "{{workflow.input.key}}", "prefix_${SUFFIX}"} {
		if p := ToolInputProblem(getShipmentTarget, map[string]any{key: "x"}); p != nil {
			t.Errorf("key %q: want nothing, got %q", key, p.Message)
		}
	}
}

func TestToolInputProblem_OnlyAMissingRequiredParameterIsFatal(t *testing.T) {
	missing := ToolInputProblem(getShipmentTarget, map[string]any{"carrier_code": "ups"})
	if missing == nil || !missing.Fatal {
		t.Errorf("a missing required parameter must be Fatal, got %+v", missing)
	}
	both := ToolInputProblem(getShipmentTarget, map[string]any{"tracking_number": "x"})
	if both == nil || !both.Fatal {
		t.Errorf("an undeclared key that leaves a required one unnamed fails every run, got %+v", both)
	}
	alone := ToolInputProblem(getShipmentTarget, map[string]any{"tracking": "x", "tracking_number": "x"})
	if alone == nil || alone.Fatal {
		t.Errorf("an undeclared key alone must be reported and not Fatal, got %+v", alone)
	}
}
