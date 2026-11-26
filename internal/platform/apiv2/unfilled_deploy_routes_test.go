// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"strings"
	"testing"
)

func TestWorkflowDeployments_ConnectorPlaceholderIs422WithTheMatchedSentence(t *testing.T) {
	api, awe := newAWEAPI(t)
	createConnectorWithPassword(t, api, "v2-wf-api", "<<__SAM_REQUIRED__>>")
	rec := api.call(http.MethodPost, "/workflows", map[string]any{
		"name": "v2-bound-workflow", "description": "Workflow whose tool node binds a connector.",
		"appConfig": map[string]any{
			"namespace": "${NAMESPACE, solace-agent-mesh}",
			"workflow": map[string]any{
				"output_mapping": map[string]any{"rows": "{{call.output}}"},
				"nodes":          []any{map[string]any{"id": "call", "name": "call", "type": "tool", "connector": "v2-wf-api"}},
			},
		},
	})
	workflow := expectCreated[Workflow](t, rec, "workflows")

	rec = deployWorkflow(api, workflow.ID)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	body := decodeError(t, rec)
	if want := "The workflow cannot be deployed until all required fields are provided"; body.Message != want {
		t.Errorf("message = %q, want %q", body.Message, want)
	}
	if details := strings.Join(body.ValidationDetails["connectors.v2-wf-api.password"], " "); !strings.Contains(details, `"v2-wf-api"`) {
		t.Errorf("details = %v, want the connector named", body.ValidationDetails)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want none: a refused deploy keeps no record", loc)
	}
	if awe.lastAddBody() != "" {
		t.Errorf("a refused deploy sent an instance to the executor: %s", awe.lastAddBody())
	}
}

func TestAgentDeployments_RemoteAgentPlaceholderIs422WithTheMatchedSentence(t *testing.T) {
	api, awe := newAWEAPI(t)
	body := remoteAgentBody("v2-unfilled-remote")
	body["agentAuthConfig"] = map[string]any{"type": "bearer", "token": "<<__SAM_REQUIRED__>>"}
	remote := createRemoteAgent(api, body)

	rec := deployAgent(api, remote.ID)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	got := decodeError(t, rec)
	if want := "The remote agent cannot be deployed until all required fields are provided"; got.Message != want {
		t.Errorf("message = %q, want %q", got.Message, want)
	}
	if _, named := got.ValidationDetails["agentAuthConfig.token"]; !named {
		t.Errorf("details = %v, want agentAuthConfig.token named", got.ValidationDetails)
	}
	if awe.lastAddBody() != "" {
		t.Errorf("a refused deploy sent an instance to the executor: %s", awe.lastAddBody())
	}
}
