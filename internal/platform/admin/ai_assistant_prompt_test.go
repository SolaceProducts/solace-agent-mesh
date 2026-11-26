// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func TestBuildAIAssistantSystemPrompt_EmptyLists(t *testing.T) {
	t.Parallel()

	out := buildAIAssistantSystemPrompt(nil, nil, nil, nil, builtinToolsets, 32000)

	if !strings.Contains(out, "**Existing Connector Instances:**\nNone\n") {
		t.Errorf("expected 'Existing Connector Instances: None' section; got:\n%s", out)
	}
	if !strings.Contains(out, "**Available Connector Types:**\nNone\n") {
		t.Errorf("expected 'Available Connector Types: None' section; got:\n%s", out)
	}
	if strings.Contains(out, "Existing Agent Names (must not conflict)") {
		t.Errorf("agent-names section should be omitted when empty")
	}
}

func TestBuildAIAssistantSystemPrompt_PopulatedLists(t *testing.T) {
	t.Parallel()

	out := buildAIAssistantSystemPrompt(
		[]string{"ExistingAgent"},
		[]string{"- `c1`: existing conn"},
		[]string{"- available conn type"},
		nil,
		builtinToolsets,
		32000,
	)

	if !strings.Contains(out, "- `c1`: existing conn") {
		t.Error("existing connector description should be rendered")
	}
	if !strings.Contains(out, "- available conn type") {
		t.Error("available connector description should be rendered")
	}
	if !strings.Contains(out, "Existing Agent Names (must not conflict)") {
		t.Error("agent-names section should be included when non-empty")
	}
	if !strings.Contains(out, "- ExistingAgent") {
		t.Error("existing agent name bullet should be rendered")
	}
	if strings.Contains(out, "**Existing Connector Instances:**\nNone") {
		t.Error("'None' sentinel should not appear for populated existing connectors")
	}
	if strings.Contains(out, "**Available Connector Types:**\nNone") {
		t.Error("'None' sentinel should not appear for populated available types")
	}
}

func TestBuildAIAssistantSystemPrompt_VolumeGuidanceGated(t *testing.T) {
	t.Parallel()

	withVolumes := []toolsetDefinition{
		{ID: "builtin_artifact_tools", Name: "Artifact Tools", Description: "x", Default: true},
		{ID: builtinVolumeToolsetID, Name: "Volume Tools", Description: "y"},
	}
	withoutVolumes := []toolsetDefinition{
		{ID: "builtin_artifact_tools", Name: "Artifact Tools", Description: "x", Default: true},
	}

	on := buildAIAssistantSystemPrompt(nil, nil, nil, nil, withVolumes, 32000)
	if !strings.Contains(on, "Volume Tools guidance") {
		t.Errorf("expected volume guidance when the volume toolset is allowed; got:\n%s", on)
	}
	if !strings.Contains(on, "workspace volume") {
		t.Errorf("expected volume how-to (workspace) when allowed")
	}

	off := buildAIAssistantSystemPrompt(nil, nil, nil, nil, withoutVolumes, 32000)
	if strings.Contains(off, "Volume Tools guidance") {
		t.Errorf("volume guidance must be omitted when the toolset is not allowed (flag off); got:\n%s", off)
	}
}

func TestToolsetsInclude(t *testing.T) {
	t.Parallel()
	ts := []toolsetDefinition{{ID: "a"}, {ID: builtinVolumeToolsetID}}
	if !toolsetsInclude(ts, builtinVolumeToolsetID) {
		t.Error("expected true for present id")
	}
	if toolsetsInclude(ts, "missing") {
		t.Error("expected false for absent id")
	}
}

func TestBuildAIAssistantSystemPrompt_StatesConfiguredInstructionBound(t *testing.T) {
	t.Parallel()

	out := buildAIAssistantSystemPrompt(nil, nil, nil, nil, builtinToolsets, 4242)
	if !strings.Contains(out, "up to 4242 chars") {
		t.Errorf("prompt must state the configured instruction bound; got:\n%s", out)
	}
	if strings.Contains(out, "100-10000") {
		t.Error("prompt still states the removed 100-10000 bound")
	}
}
