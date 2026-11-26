// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestToolRegistrationResponses_CopiesToolNames(t *testing.T) {
	regs := []platpkg.ToolEntryRegistration{
		{ToolType: "openapi", ConnectorID: "conn-1", Name: "pets", Status: "ready", ToolCount: 2, ToolNames: []string{"list_pets", "get_pet"}},
		{ToolType: "mcp", ConnectorID: "conn-2", Name: "figma", Status: "failed", Error: "boom"},
	}

	out := toolRegistrationResponses(regs)

	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if !reflect.DeepEqual(out[0].ToolNames, []string{"list_pets", "get_pet"}) {
		t.Errorf("ready entry ToolNames = %v, want [list_pets get_pet]", out[0].ToolNames)
	}
	if out[1].ToolNames != nil {
		t.Errorf("failed entry ToolNames = %v, want nil", out[1].ToolNames)
	}
}

func TestToolRegistrationResponse_ToolNamesJSONTag(t *testing.T) {
	withNames, err := json.Marshal(ToolRegistrationResponse{ToolType: "openapi", Status: "ready", ToolCount: 1, ToolNames: []string{"whoami"}})
	if err != nil {
		t.Fatalf("marshal with names: %v", err)
	}
	if !strings.Contains(string(withNames), `"toolNames":["whoami"]`) {
		t.Errorf("marshaled DTO = %s, want camelCase toolNames array", withNames)
	}

	without, err := json.Marshal(ToolRegistrationResponse{ToolType: "openapi", Status: "failed"})
	if err != nil {
		t.Fatalf("marshal without names: %v", err)
	}
	if strings.Contains(string(without), "toolNames") {
		t.Errorf("marshaled DTO = %s, want toolNames omitted when empty", without)
	}
}
