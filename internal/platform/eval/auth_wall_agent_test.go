// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestAuthWallAgent(t *testing.T) {
	const workflow = "workflow_01a15ab4_63c4_7bb8_8d64_3d7fd560f32a"
	held := func(agent string) *a2a.AuthenticationRequiredData {
		return &a2a.AuthenticationRequiredData{TargetAgent: agent}
	}
	cases := []struct {
		name           string
		ext            extractedResponse
		workflowTarget bool
		want           string
	}{
		{"a workflow's node agent holding the tool is named", extractedResponse{authPeer: workflow, authRequired: held("agent_classifier")}, true, "agent_classifier"},
		{"a wall naming no holder keeps the forwarding peer", extractedResponse{authPeer: workflow, authRequired: held("")}, true, workflow},
		{"a wall naming the target keeps the forwarding peer", extractedResponse{authPeer: workflow, authRequired: held(workflow)}, true, workflow},
		{"an agent target keeps its forwarding peer", extractedResponse{authPeer: "JiraAgent", authRequired: held("JiraAgent")}, false, "JiraAgent"},
		{"an agent target's own wall names nobody", extractedResponse{authRequired: held("OrderDesk")}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := authWallAgent(tc.ext, tc.workflowTarget, workflow); got != tc.want {
				t.Errorf("authWallAgent = %q, want %q", got, tc.want)
			}
		})
	}
}

func forwardedAuthWall(forwardedBy, nodeAgent, holder string) map[string]any {
	return map[string]any{"result": map[string]any{
		"kind":     "status-update",
		"metadata": map[string]any{"agent_name": nodeAgent},
		"status": map[string]any{
			"state": string(a2a.TaskStateInputNeeded),
			"message": map[string]any{
				"metadata": map[string]any{"agent_name": forwardedBy, "forwarded_from_peer": forwardedBy},
				"parts": []any{map[string]any{"kind": "data", "data": map[string]any{
					"type": string(a2a.SignalAuthenticationRequired), "target_agent": holder, "credential_key": "crm",
				}}},
			},
		},
	}}
}

func TestAForwardedWorkflowAuthWallNamesTheAgentHoldingTheTool(t *testing.T) {
	const workflow = "workflow_01a15ab4_63c4_7bb8_8d64_3d7fd560f32a"
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"a node agent's wall", forwardedAuthWall(workflow, "agent_classifier", "agent_classifier"), "agent_classifier"},
		{"a node agent's wall two workflows deep", forwardedAuthWall(workflow, "workflow_inner", "agent_classifier"), "agent_classifier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext := extractResponse(tc.payload)
			if ext.authRequired == nil {
				t.Fatal("the wall was not recognised")
			}
			if got := authWallAgent(ext, true, workflow); got != tc.want {
				t.Errorf("authWallAgent = %q, want %q", got, tc.want)
			}
		})
	}
}
