// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const agentConfigurationsMaxBytes = 64 * 1024

func agentConfigurationsBody(name string, configurations any) map[string]any {
	return map[string]any{
		"name":                     name,
		"description":              "Agent the configuration test creates.",
		"systemPrompt":             "You help with tests.",
		"toolsets":                 []string{"builtin_artifact_tools"},
		"additionalConfigurations": configurations,
	}
}

func agentConfigurationsExpectWarning(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if warnings := decodeData[Agent](t, rec).Warnings; !strings.Contains(strings.Join(warnings, "\n"), want) {
		t.Errorf("warnings = %v, want one containing %q", warnings, want)
	}
}

func TestAgents_UpdateOfAMissingAgentIsNotFoundBeforeTheStoredMinimumCheck(t *testing.T) {
	api, _ := newAWEAPI(t)

	rec := api.call(http.MethodPut, "/agents/"+missingID, agentConfigurationsBody("missing-agent", json.RawMessage(`{"maxLlmCallsPerTask":0}`)))

	expectStatus(t, rec, http.StatusNotFound)
}

func TestAgents_UnknownConfigurationKeysAreWarnings(t *testing.T) {
	api, _ := newAWEAPI(t)

	created := api.call(http.MethodPost, "/agents", agentConfigurationsBody("unknown-key-agent",
		map[string]any{"agent_card_pulishing": map[string]any{"intervalSeconds": 30}}))
	expectStatus(t, created, http.StatusCreated)
	agentConfigurationsExpectWarning(t, created, "agent_card_pulishing")

	id := decodeData[Agent](t, created).ID
	updated := api.call(http.MethodPut, "/agents/"+id, agentConfigurationsBody("unknown-key-agent",
		map[string]any{"definitely_not_a_real_key": "x"}))
	expectStatus(t, updated, http.StatusOK)
	agentConfigurationsExpectWarning(t, updated, "definitely_not_a_real_key")
}

func TestAgents_ConfigurationOfTheWrongTypeIsRefused(t *testing.T) {
	api, _ := newAWEAPI(t)

	rec := api.call(http.MethodPost, "/agents", agentConfigurationsBody("wrong-type-agent", map[string]any{"supportsStreaming": "yes"}))

	expectDetail(t, rec, http.StatusUnprocessableEntity, "additionalConfigurations")
	if !strings.Contains(rec.Body.String(), "expected boolean") {
		t.Errorf("body = %s, want the expected type named", rec.Body.String())
	}
}

func TestAgents_OversizedConfigurationsAreRefusedBeforeTheAgentIsLookedUp(t *testing.T) {
	api, _ := newAWEAPI(t)
	pad := strings.Repeat("a", agentConfigurationsMaxBytes+128)

	rec := api.call(http.MethodPut, "/agents/"+missingID, agentConfigurationsBody("oversized-agent", map[string]any{"k": pad}))

	expectDetail(t, rec, http.StatusUnprocessableEntity, "additionalConfigurations")
	if !strings.Contains(rec.Body.String(), "limit is 65536") {
		t.Errorf("body = %s, want the size limit named", rec.Body.String())
	}
}
