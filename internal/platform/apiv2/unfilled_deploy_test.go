// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const unfilledAgentSentence = "The agent cannot be deployed until all required fields are provided"

const msgUnfilledFields = "The " + entplatform.UnfilledDeployFieldsMessage

func createConnectorWithPassword(t *testing.T, api *entrypointAPI, name, password string) string {
	t.Helper()
	rec := api.call(http.MethodPost, "/connectors", map[string]any{
		"name": name, "description": "A connector the deploy test binds.",
		"connectorType": "sql", "connectorSubtype": "postgres",
		"values": map[string]any{"hostname": "db.example.com", "port": "5432", "database": "testdb", "username": "svc", "password": password},
	})
	expectStatus(t, rec, http.StatusCreated)
	return decodeData[Connector](t, rec).ID
}

func TestAgentDeployments_ConnectorPlaceholderIs422WithTheMatchedSentence(t *testing.T) {
	api, awe := newAWEAPI(t)
	connectorID := createConnectorWithPassword(t, api, "v2-bound-api", "<<__SAM_REQUIRED__>>")
	rec := api.call(http.MethodPost, "/agents", map[string]any{
		"name": "v2-bound-agent", "description": "Agent bound to a connector with no password.",
		"systemPrompt": "You help with tests.", "inputModes": []string{"text"}, "connectorIds": []string{connectorID},
	})
	agent := expectCreated[Agent](t, rec, "agents")

	for attempt := 1; attempt <= 2; attempt++ {
		rec = deployAgent(api, agent.ID)
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		body := decodeError(t, rec)
		if body.Message != unfilledAgentSentence {
			t.Errorf("attempt %d: message = %q, want %q", attempt, body.Message, unfilledAgentSentence)
		}
		details := strings.Join(body.ValidationDetails["connectors.v2-bound-api.password"], " ")
		if !strings.Contains(details, `"v2-bound-api"`) || !strings.Contains(details, "password") {
			t.Errorf("attempt %d: details = %v, want the connector and value named", attempt, body.ValidationDetails)
		}
		if strings.Contains(rec.Body.String(), "<<__SAM_REQUIRED__>>") {
			t.Errorf("attempt %d: the response carries the placeholder: %s", attempt, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("attempt %d: Location = %q, want none: a refused deploy keeps no record", attempt, loc)
		}
	}
	if awe.lastAddBody() != "" {
		t.Errorf("a refused deploy sent an instance to the executor: %s", awe.lastAddBody())
	}
}

func TestKeepUnfilledSentence(t *testing.T) {
	details := map[string][]string{"field": {"field is required"}}
	for _, tc := range []struct {
		name        string
		err         error
		wantMessage string
	}{
		{"agent", &entplatform.ValidationError{Message: "agent cannot be deployed until all required fields are provided", Details: details}, "The agent cannot be deployed until all required fields are provided"},
		{"remote agent", &entplatform.ValidationError{Message: "remote agent cannot be deployed until all required fields are provided", Details: details}, "The remote agent cannot be deployed until all required fields are provided"},
		{"workflow", &entplatform.ValidationError{Message: "workflow cannot be deployed until all required fields are provided", Details: details}, "The workflow cannot be deployed until all required fields are provided"},
		{"entrypoint", &entplatform.ValidationError{Message: entplatform.UnfilledDeployFieldsMessage, Details: details}, msgUnfilledFields},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, isCaller := keepUnfilledSentence(tc.err).(*CallerError)
			if !isCaller || got.Status != http.StatusUnprocessableEntity || got.Message != tc.wantMessage || len(got.Details) != 1 {
				t.Errorf("keepUnfilledSentence = %+v, want a 422 %q carrying the details", got, tc.wantMessage)
			}
		})
	}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"another validation failure", &entplatform.ValidationError{Message: "agent must have a system prompt before deploying", Details: details}},
		{"the sentence with no details", &entplatform.ValidationError{Message: "agent cannot be deployed until all required fields are provided"}},
		{"not a validation error", errors.New("agent cannot be deployed until all required fields are provided")},
	} {
		t.Run("passes through "+tc.name, func(t *testing.T) {
			if got := keepUnfilledSentence(tc.err); got != tc.err {
				t.Errorf("keepUnfilledSentence changed %v into %v", tc.err, got)
			}
		})
	}
}
