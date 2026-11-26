// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"testing"
)

func TestOrderExceptionWorkflow_SwitchConditions(t *testing.T) {
	tests := []struct {
		name        string
		condition   string
		nodeOutputs map[string]map[string]any
		want        bool
	}{
		{
			name:      "exception detected",
			condition: "{{check_inventory.output.has_exception}} == true",
			nodeOutputs: map[string]map[string]any{
				"check_inventory": {"output": map[string]any{"has_exception": true}},
			},
			want: true,
		},
		{
			name:      "no exception",
			condition: "{{check_inventory.output.has_exception}} == true",
			nodeOutputs: map[string]map[string]any{
				"check_inventory": {"output": map[string]any{"has_exception": false}},
			},
			want: false,
		},
		{
			name:      "approval granted",
			condition: "{{request_approval.output.approved}} == true",
			nodeOutputs: map[string]map[string]any{
				"request_approval": {"output": map[string]any{"approved": true}},
			},
			want: true,
		},
		{
			name:      "approval denied",
			condition: "{{request_approval.output.approved}} == true",
			nodeOutputs: map[string]map[string]any{
				"request_approval": {"output": map[string]any{"approved": false}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateCondition(tt.condition, tt.nodeOutputs)
			if err != nil {
				t.Fatalf("EvaluateCondition: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrderExceptionWorkflow_OutputMappingCoalesce(t *testing.T) {
	coalesceMapping := map[string]any{
		"coalesce": []any{
			"{{execute_resolution.output.customer_response}}",
			"{{deny_order.output.customer_response}}",
			"{{fulfill_directly.output.customer_response}}",
		},
	}

	tests := []struct {
		name        string
		nodeOutputs map[string]map[string]any
		want        string
	}{
		{
			name: "approved path",
			nodeOutputs: map[string]map[string]any{
				"execute_resolution": {"output": map[string]any{"customer_response": "Order confirmed!"}},
			},
			want: "Order confirmed!",
		},
		{
			name: "denied path",
			nodeOutputs: map[string]map[string]any{
				"deny_order": {"output": map[string]any{"customer_response": "Order denied."}},
			},
			want: "Order denied.",
		},
		{
			name: "direct fulfill path",
			nodeOutputs: map[string]map[string]any{
				"fulfill_directly": {"output": map[string]any{"customer_response": "Shipping now!"}},
			},
			want: "Shipping now!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveValue(coalesceMapping, tt.nodeOutputs)
			if err != nil {
				t.Fatalf("ResolveValue: %v", err)
			}
			gotStr, ok := got.(string)
			if !ok {
				t.Fatalf("got type %T, want string", got)
			}
			if gotStr != tt.want {
				t.Errorf("got %q, want %q", gotStr, tt.want)
			}
		})
	}

	t.Run("no output returns nil", func(t *testing.T) {
		got, err := ResolveValue(coalesceMapping, map[string]map[string]any{})
		if err != nil {
			t.Fatalf("ResolveValue: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}
