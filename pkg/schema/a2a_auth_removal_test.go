// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"encoding/json"
	"testing"
)

func TestA2AAgentSchema_AuthRemovalShapeValidates(t *testing.T) {
	artifact := map[string]any{
		"name":              "remote_helper",
		"agentUrl":          "https://remote.example.com/a2a",
		"agentCardLocation": "well_known",
		"agentAuthType":     "none",
		"agentAuthConfig":   map[string]any{"type": "none"},
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	result, err := ValidateA2AAgentConfig(raw)
	if err != nil {
		t.Fatalf("ValidateA2AAgentConfig: %v", err)
	}
	if !result.Valid {
		t.Errorf("the documented removal shape is rejected: %+v", result.Errors)
	}
}

func TestA2AAgentSchema_CardAuthRemovalShapeValidates(t *testing.T) {
	artifact := map[string]any{
		"name":                "remote_helper",
		"agentUrl":            "https://remote.example.com/a2a",
		"agentCardLocation":   "well_known",
		"agentAuthConfig":     map[string]any{"type": "bearer", "token": "tok"},
		"agentCardAuthType":   "none",
		"agentCardAuthConfig": map[string]any{"type": "none"},
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	result, err := ValidateA2AAgentConfig(raw)
	if err != nil {
		t.Fatalf("ValidateA2AAgentConfig: %v", err)
	}
	if !result.Valid {
		t.Errorf("the documented card removal shape is rejected: %+v", result.Errors)
	}
}

func TestA2AAgentSchema_UnknownAuthTypeStillRejected(t *testing.T) {
	artifact := map[string]any{
		"name":              "remote_helper",
		"agentUrl":          "https://remote.example.com/a2a",
		"agentCardLocation": "well_known",
		"agentAuthType":     "mtls",
		"agentAuthConfig":   map[string]any{"type": "none"},
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	result, err := ValidateA2AAgentConfig(raw)
	if err != nil {
		t.Fatalf("ValidateA2AAgentConfig: %v", err)
	}
	if result.Valid {
		t.Error("an auth type nothing implements was accepted")
	}
}
