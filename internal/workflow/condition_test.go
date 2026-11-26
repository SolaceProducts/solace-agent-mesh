// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"testing"
)

func TestEvaluateCondition(t *testing.T) {
	outputs := map[string]map[string]any{
		"classify": {"output": map[string]any{
			"category": "urgent",
			"priority": "high",
			"score":    0.95,
			"count":    10,
		}},
		"check": {"output": map[string]any{
			"status":  "success",
			"enabled": true,
		}},
	}

	tests := []struct {
		name    string
		expr    string
		want    bool
		wantErr bool
	}{
		{"string equal true", "'{{classify.output.category}}' == 'urgent'", true, false},
		{"string equal false", "'{{classify.output.category}}' == 'normal'", false, false},
		{"string not equal", "'{{classify.output.category}}' != 'normal'", true, false},

		{"number greater than", "{{classify.output.score}} > 0.8", true, false},
		{"number less than", "{{classify.output.score}} < 0.5", false, false},
		{"number greater equal", "{{classify.output.count}} >= 10", true, false},
		{"number less equal", "{{classify.output.count}} <= 5", false, false},

		{"and true", "'{{classify.output.priority}}' == 'high' and {{classify.output.score}} > 0.8", true, false},
		{"and false", "'{{classify.output.priority}}' == 'high' and {{classify.output.score}} < 0.5", false, false},
		{"or true", "'{{classify.output.priority}}' == 'low' or {{classify.output.score}} > 0.8", true, false},
		{"not", "not ('{{classify.output.category}}' == 'normal')", true, false},

		{"true literal", "true", true, false},
		{"false literal", "false", false, false},

		{"empty string", "", true, false},

		{"grouped", "('{{classify.output.priority}}' == 'high') and ({{classify.output.score}} > 0.5)", true, false},

		{"missing node in string comparison", "'{{missing.output.val}}' == '{{missing.output.val}}'", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateCondition(tt.expr, outputs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateCondition_ArgoAliases(t *testing.T) {
	outputs := map[string]map[string]any{
		"_map_item": {"output": map[string]any{
			"status": "active",
		}},
	}

	got, err := EvaluateCondition("'{{item.status}}' == 'active'", outputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected true, got false")
	}
}
