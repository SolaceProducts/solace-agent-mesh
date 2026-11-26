// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func TestForwardWorkflowV2_HeaderFields(t *testing.T) {
	in := map[string]any{
		"name":        "Video Creation",
		"description": "Creates a narrated slide video from a topic and script.",
	}

	got, err := ForwardWorkflowV2(in, WorkflowV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardWorkflowV2: %v", err)
	}

	want := wire.CreateWorkflowRequest{
		Name:        "Video Creation",
		Description: "Creates a narrated slide video from a topic and script.",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("ForwardWorkflowV2 header mismatch (-want +got):\n%s", diff)
	}
}

func TestReverseWorkflowV2_HeaderFields(t *testing.T) {
	in := wire.WorkflowResponse{
		ID:               "wf-1",
		Type:             "workflow",
		Name:             "Video Creation",
		Description:      "desc",
		DeploymentStatus: "deployed",
		CreatedBy:        "alice",
	}
	out, err := ReverseWorkflowV2(in, WorkflowV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseWorkflowV2: %v", err)
	}
	if got := out["name"]; got != "Video Creation" {
		t.Errorf("name mismatch: got %v", got)
	}
	if got := out["description"]; got != "desc" {
		t.Errorf("description mismatch: got %v", got)
	}
	for _, k := range []string{"id", "type", "deploymentStatus", "createdBy"} {
		if _, leaked := out[k]; leaked {
			t.Errorf("server-managed field %q leaked into reverse output", k)
		}
	}
}
