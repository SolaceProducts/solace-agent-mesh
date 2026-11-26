// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestAgents_WritersRefuseARemoteAgentsMirror(t *testing.T) {
	callers := []struct {
		name   string
		scopes func(scope string) []string
	}{
		{"session user", func(string) []string { return nil }},
		{"caller holding only the agent scope", func(scope string) []string { return []string{scope} }},
	}
	writers := []struct {
		name   string
		method string
		scope  string
		body   map[string]any
	}{
		{"PUT renames it", http.MethodPut, rbac.ScopeAgentBuilderUpdate, map[string]any{
			"name": "hijacked-mirror", "description": "Renamed through the agents route.", "systemPrompt": "x",
		}},
		{"DELETE removes it", http.MethodDelete, rbac.ScopeAgentBuilderDelete, nil},
	}
	for _, caller := range callers {
		for _, writer := range writers {
			t.Run(caller.name+"/"+writer.name, func(t *testing.T) {
				api, _ := newAWEAPI(t)
				remote := createRemoteAgent(api, remoteAgentBody("weather-remote"))

				rec := api.call(writer.method, "/agents/"+remote.ID, writer.body, caller.scopes(writer.scope)...)

				expectStatus(t, rec, http.StatusConflict)
				decodeError(t, rec)
				got := api.call(http.MethodGet, "/remoteAgents/"+remote.ID, nil)
				expectStatus(t, got, http.StatusOK)
				if name := decodeData[RemoteAgent](t, got).Name; name != "weather-remote" {
					t.Errorf("remote agent name = %q after the refused write", name)
				}
				expectStatus(t, api.call(http.MethodPost, "/agents", map[string]any{
					"name": "weather-remote", "description": "Takes the remote agent's name.", "systemPrompt": "x",
				}), http.StatusConflict)
			})
		}
	}
}

func TestAgents_WritersStillWriteAStandardAgent(t *testing.T) {
	api, _ := newAWEAPI(t)
	agent := createAgent(api, "standard-agent")

	expectStatus(t, api.call(http.MethodPut, "/agents/"+agent.ID, map[string]any{
		"name": "renamed-standard", "description": agent.Description, "systemPrompt": agent.SystemPrompt,
	}), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, "/agents/"+agent.ID, nil), http.StatusNoContent)
}

func TestAgents_RepeatedValuesAreRejectedUnderTheV2FieldNames(t *testing.T) {
	const connectorID = "01a1457a-b74e-7b56-96d3-aaaaaaaaaaaa"
	api, _ := newAWEAPI(t)
	existing := createAgent(api, "standard-agent")
	body := map[string]any{
		"name": "repeating-agent", "description": "Agent whose lists repeat a value.", "systemPrompt": "x",
		"inputModes": []string{"text", "text"}, "connectorIds": []string{connectorID, connectorID},
	}
	update := map[string]any{
		"name": "standard-agent", "description": existing.Description, "systemPrompt": existing.SystemPrompt,
		"inputModes": []string{"text", "text"}, "connectorIds": []string{connectorID, connectorID},
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"create": api.call(http.MethodPost, "/agents", body),
		"update": api.call(http.MethodPut, "/agents/"+existing.ID, update),
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, rec, http.StatusUnprocessableEntity)
			details := decodeError(t, rec).ValidationDetails
			for _, field := range []string{"inputModes", "connectorIds"} {
				if len(details[field]) == 0 {
					t.Errorf("validationDetails misses %q: %v", field, details)
				}
			}
			if _, v1Name := details["connectors"]; v1Name {
				t.Errorf("validationDetails uses the v1 field name connectors: %v", details)
			}
		})
	}
}
