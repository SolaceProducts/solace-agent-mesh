// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"testing"
)

func TestParseAgentYAML_RequiresInstruction(t *testing.T) {
	t.Parallel()
	yamlStr := `apps:
  - app_config:
      display_name: No Instruction Agent
      agent_card:
        description: An agent YAML missing the instruction field
`
	_, err := ParseAgentYAML([]byte(yamlStr))
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("ParseAgentYAML err = %v, want *ValidationError", err)
	}
	if verr.Field != "instruction" || verr.Message != "instruction is required" {
		t.Errorf("ValidationError = {Field:%q Message:%q}, want {instruction, instruction is required}", verr.Field, verr.Message)
	}
}

func TestContentTypeFromKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  string
		want string
	}{
		{"ns/connectors/123/spec.json", "application/json"},
		{"ns/connectors/123/spec.yaml", "application/x-yaml"},
		{"ns/connectors/123/spec.yml", "application/x-yaml"},
		{"ns/connectors/123/spec.txt", "application/octet-stream"},
		{"", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := contentTypeFromKey(tt.key); got != tt.want {
			t.Errorf("contentTypeFromKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
