// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (h *e2eHarness) createBearerRemoteAgent(name, token string) string {
	h.t.Helper()
	resp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Name:              name,
		Description:       "A remote agent used to check the credential guard.",
		AgentURL:          "https://remote.example.com",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "default-skill"}},
		AgentAuthType:     "bearer",
		AgentAuthConfig:   map[string]any{"token": token},
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create remote agent %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eHarness) setStoredAuth(a2aID, authType, authConfig string) {
	h.t.Helper()
	row, err := h.a2aRepo.GetByID(h.ctx, a2aID)
	if err != nil || row == nil {
		h.t.Fatalf("read remote agent %s: %v", a2aID, err)
	}
	row.AgentAuthType, row.AgentAuthConfig = &authType, &authConfig
	if err := h.a2aRepo.Update(h.ctx, row); err != nil {
		h.t.Fatalf("write remote agent %s: %v", a2aID, err)
	}
}

func (h *e2eHarness) breakTable(table string) {
	h.t.Helper()
	if _, err := h.db.ExecContext(h.ctx, "ALTER TABLE "+table+" RENAME TO "+table+"_gone"); err != nil {
		h.t.Fatalf("rename %s: %v", table, err)
	}
}

func requireNotARefusal(t *testing.T, err error, wantInMessage string) {
	t.Helper()
	if err == nil {
		t.Fatal("deploy succeeded although the credentials could not be read")
	}
	if _, isValidation := errors.AsType[*ValidationError](err); isValidation {
		t.Errorf("a read failure was reported as a validation error: %v", err)
	}
	if !strings.Contains(err.Error(), wantInMessage) {
		t.Errorf("error = %v, want it to come from the guard (%q)", err, wantInMessage)
	}
}

func TestRemoteAgentDeploy_RefusesAStaleConfigOfAnotherType(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	a2aID := h.createBearerRemoteAgent("remote-type-only-update", unfilled)
	if _, err := h.a2aSvc.Update(h.ctx, a2aID, UpdateRemoteAgentRequest{AgentAuthType: "apikey"}, "e2e-user"); err != nil {
		t.Fatalf("change only the auth type: %v", err)
	}

	verr := unfilledDetails(t, h.tryDeploy(a2aID, DeployActionDeploy), unfilledRemoteAgentMessage)
	if _, named := verr.Details["agentAuthConfig.apiKey"]; !named || len(verr.Details) != 1 {
		t.Errorf("details = %v, want only agentAuthConfig.apiKey, the field whose update fixes the row", verr.Details)
	}
	h.assertNothingDeployed(a2aID)
}

func TestRemoteAgentDeploy_RefusesADoubleEncodedPlaceholderConfig(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	a2aID := h.createBearerRemoteAgent("remote-double-encoded", realSecret)
	encoded, err := json.Marshal(`{"type":"bearer","token":"` + unfilled + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	h.setStoredAuth(a2aID, "bearer", string(encoded))

	verr := unfilledDetails(t, h.tryDeploy(a2aID, DeployActionDeploy), unfilledRemoteAgentMessage)
	if _, named := verr.Details["agentAuthConfig.token"]; !named {
		t.Errorf("details = %v, want agentAuthConfig.token named", verr.Details)
	}
}

func TestRemoteAgentGuard_JudgesWhatTheProxyConfigSends(t *testing.T) {
	t.Parallel()
	authTypes := []string{authTypeBearer, authTypeAPIKey, authTypeBasic, authTypeOAuth2, authTypeNone}
	for _, storedType := range authTypes {
		stored := map[string]any{"type": storedType}
		for _, pair := range authSecretPairs(storedType) {
			stored[pair.stored] = unfilled
		}
		for _, storedName := range authNonSecretWireMap(storedType) {
			stored[storedName] = unfilled
		}
		raw, err := json.Marshal(stored)
		if err != nil {
			t.Fatal(err)
		}
		config := string(raw)
		for _, columnType := range authTypes {
			for _, surface := range []string{"agent", "card"} {
				t.Run(storedType+" stored under "+columnType+" on "+surface, func(t *testing.T) {
					t.Parallel()
					a := &store.A2AAgent{Name: "parity", AgentURL: "https://remote.example.com", AgentCardLocation: "none"}
					sent := "authentication"
					if surface == "agent" {
						a.AgentAuthType, a.AgentAuthConfig = &columnType, &config
					} else {
						a.AgentCardAuthType, a.AgentCardAuthConfig = &columnType, &config
						sent = "agent_card_authentication"
					}
					emitted, _ := buildA2AProxyAppConfig(a, "ns")[sent].(map[string]any)
					placeholders := map[string][]string{}
					collectPlaceholderFields("", emitted, placeholders)

					err := requireRemoteAgentAuthFilled(a)
					if len(placeholders) == 0 {
						if err != nil {
							t.Fatalf("refused although the proxy config carries no placeholder: %v", err)
						}
						return
					}
					verr, ok := errors.AsType[*ValidationError](err)
					if !ok {
						t.Fatalf("the proxy config carries %v, but the guard answered %v", placeholders, err)
					}
					if len(verr.Details) != len(placeholders) {
						t.Errorf("details = %v, want one entry per placeholder sent (%v)", verr.Details, placeholders)
					}
				})
			}
		}
	}
}

func TestWorkflowDeploy_RefusesAnUnfilledConnectorWhicheverBindingItIs(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	for _, tc := range []struct {
		name            string
		unfilledCreated int
	}{{"unfilled created first", 0}, {"unfilled created second", 1}} {
		suffix := strings.ReplaceAll(tc.name, " ", "-")
		filled, unfilledName := "wf-b-filled-"+suffix, "wf-a-unfilled-"+suffix
		if tc.unfilledCreated == 1 {
			filled, unfilledName = "wf-a-filled-"+suffix, "wf-b-unfilled-"+suffix
		}
		creations := []func(){
			func() {
				h.createSQLConnector(filled, "postgres", postgresValues(realSecret, map[string]any{"database": "okdb"}))
			},
			func() { h.createSQLConnector(unfilledName, "postgres", postgresValues(unfilled, nil)) },
		}
		if tc.unfilledCreated == 0 {
			creations[0], creations[1] = creations[1], creations[0]
		}
		creations[0]()
		creations[1]()
		nodes := []any{
			map[string]any{"id": "call0", "type": "tool", "connector": filled},
			map[string]any{"id": "call1", "type": "tool", "connector": unfilledName},
		}
		wfID := h.createWorkflow("wf_"+strings.ReplaceAll(tc.name, " ", "_"), map[string]any{"output_mapping": map[string]any{"rows": "{{call0.output}}"}, "nodes": nodes})

		_, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionDeploy}, "e2e-user")
		verr := unfilledDetails(t, err, unfilledWorkflowMessage)
		if text := detailsText(verr); !strings.Contains(text, `"`+unfilledName+`"`) || strings.Contains(text, filled) {
			t.Errorf("%s: details %q must name the unfilled connector and only it", tc.name, text)
		}
	}
}

func TestRemoteAgentGuard_NamesACollidingStoredKeyDeterministically(t *testing.T) {
	t.Parallel()
	config := `{"type":"apikey","token":"` + unfilled + `","apiKey":"real","header_name":"X-Key"}`
	authType := authTypeAPIKey
	a := &store.A2AAgent{Name: "colliding", AgentURL: "https://remote.example.com", AgentCardLocation: "none", AgentAuthType: &authType, AgentAuthConfig: &config}
	for attempt := 0; attempt < 50; attempt++ {
		verr, ok := errors.AsType[*ValidationError](requireRemoteAgentAuthFilled(a))
		if !ok {
			t.Fatalf("attempt %d: the guard passed a row whose proxy config carries a placeholder", attempt)
		}
		if _, named := verr.Details["agentAuthConfig.apiKey"]; !named || len(verr.Details) != 1 {
			t.Fatalf("attempt %d: details = %v, want only agentAuthConfig.apiKey", attempt, verr.Details)
		}
	}
}

func TestProxyAuthConfigs_ShapeTheProxyReads(t *testing.T) {
	t.Parallel()
	cases := []struct {
		authType string
		stored   string
		want     map[string]any
	}{
		{authTypeBearer, `{"type":"bearer","token":"t"}`, map[string]any{"type": "static_bearer", "token": "t"}},
		{authTypeAPIKey, `{"type":"apikey","api_key":"k","header_name":"X-Key"}`, map[string]any{"type": "static_apikey", "token": "k", "header": "X-Key"}},
		{authTypeBasic, `{"type":"basic","username":"u","password":"p"}`, map[string]any{"type": "basic", "username": "u", "password": "p"}},
		{authTypeOAuth2, `{"type":"oauth2","client_id":"i","client_secret":"s","token_endpoint":"https://idp.example.com/token"}`,
			map[string]any{"type": "oauth2_client_credentials", "client_id": "i", "client_secret": "s", "token_url": "https://idp.example.com/token"}},
	}
	for _, tc := range cases {
		t.Run(tc.authType, func(t *testing.T) {
			t.Parallel()
			agent := &store.A2AAgent{AgentAuthType: &tc.authType, AgentAuthConfig: &tc.stored}
			card := &store.A2AAgent{AgentCardAuthType: &tc.authType, AgentCardAuthConfig: &tc.stored}
			if got, other := proxyAuthConfigs(agent); !reflect.DeepEqual(got, tc.want) || other != nil {
				t.Errorf("agent surface = %v and card = %v, want %v and none", got, other, tc.want)
			}
			if other, got := proxyAuthConfigs(card); !reflect.DeepEqual(got, tc.want) || other != nil {
				t.Errorf("card surface = %v and agent = %v, want %v and none", got, other, tc.want)
			}
		})
	}

	none, empty, bearer, stored := authTypeNone, "", authTypeBearer, `{"type":"bearer","token":"t"}`
	for name, a := range map[string]*store.A2AAgent{
		"agent type none":         {AgentAuthType: &none, AgentAuthConfig: &stored},
		"agent type empty":        {AgentAuthType: &empty, AgentAuthConfig: &stored},
		"agent type unset":        {AgentAuthConfig: &stored},
		"agent config unset":      {AgentAuthType: &bearer},
		"card config unset":       {AgentCardAuthType: &bearer},
		"card type unset":         {AgentCardAuthConfig: &stored},
		"agent config unreadable": {AgentAuthType: new(string), AgentAuthConfig: new(string)},
	} {
		if agent, card := proxyAuthConfigs(a); agent != nil || card != nil {
			t.Errorf("%s: sends %v and %v, want nothing", name, agent, card)
		}
	}
}

func TestProxyAppConfig_UsesAgentAuthForTheCardOnlyWhereTheCardIsFetched(t *testing.T) {
	t.Parallel()
	authType, config := authTypeBearer, `{"type":"bearer","token":"t"}`
	for location, want := range map[string]bool{"well_known": true, "custom": true, "none": false} {
		a := &store.A2AAgent{AgentCardLocation: location, AgentAuthType: &authType, AgentAuthConfig: &config}
		_, got := buildA2AProxyAppConfig(a, "ns")["use_auth_for_agent_card"].(bool)
		if got != want {
			t.Errorf("card location %q: use_auth_for_agent_card set = %v, want %v", location, got, want)
		}
		withCardAuthOnly := &store.A2AAgent{AgentCardLocation: location, AgentCardAuthType: &authType, AgentCardAuthConfig: &config}
		if _, set := buildA2AProxyAppConfig(withCardAuthOnly, "ns")["use_auth_for_agent_card"]; set {
			t.Errorf("card location %q: card auth alone must not turn on use_auth_for_agent_card", location)
		}
	}
}

func TestRemoteAgentUndeploy_IgnoresAPlaceholderCredential(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	a2aID := h.createBearerRemoteAgent("remote-undeploy-blanked", realSecret)
	h.deployA2AAgent(a2aID)
	h.setStoredAuth(a2aID, "bearer", `{"type":"bearer","token":"`+unfilled+`"}`)

	if err := h.tryDeploy(a2aID, DeployActionUndeploy); err != nil {
		t.Fatalf("undeploy through the deploy route: %v", err)
	}
}

func TestRemoteAgentDeploy_AuthTypeNoneIgnoresALeftoverPlaceholder(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	a2aID := h.createBearerRemoteAgent("remote-switched-to-none", realSecret)
	h.setStoredAuth(a2aID, "none", `{"type":"bearer","token":"`+unfilled+`"}`)

	if err := h.tryDeploy(a2aID, DeployActionDeploy); err != nil {
		t.Fatalf("a remote agent with no authentication was refused over a credential it does not send: %v", err)
	}
}

func TestWorkflowUndeploy_IgnoresAConnectorPlaceholder(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	connectorID := h.createSQLConnector("wf-blanked-later-api", "postgres", postgresValues(realSecret, nil))
	wfID := h.createWorkflow("wf_blanked_later", map[string]any{
		"output_mapping": map[string]any{"rows": "{{call.output}}"},
		"nodes":          []any{map[string]any{"id": "call", "type": "tool", "connector": "wf-blanked-later-api"}},
	})
	if resp := h.attemptWorkflowDeploy(wfID); resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy: status %q (%s)", resp.Status, resp.ErrorMessage)
	}
	if _, err := h.connectorSvc.Update(h.ctx, connectorID, UpdateConnectorRequest{Values: postgresValues(unfilled, nil)}, "e2e-user"); err != nil {
		t.Fatalf("blank the connector: %v", err)
	}

	resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionUndeploy}, "e2e-user")
	if err != nil {
		t.Fatalf("undeploy through the deploy route: %v", err)
	}
	if resp.Status != DeployStatusSuccess {
		t.Errorf("undeploy status = %q (%s), want %q", resp.Status, resp.ErrorMessage, DeployStatusSuccess)
	}
}

func TestAgentDeploy_FailsClosedWhenTheConnectorCannotBeRead(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, []*llm.ChatResponse{{Content: "ok", FinishReason: "stop"}})

	connectorID := h.createSQLConnector("unreadable-api", "postgres", postgresValues(realSecret, nil))
	agentID := h.createAgentWithConnectors("unreadable-connector-agent", deployPrompt, []string{connectorID})
	h.breakTable("connectors")

	requireNotARefusal(t, h.tryDeploy(agentID, DeployActionDeploy), "load connector "+connectorID)
	h.assertNothingDeployed(agentID)
}

func TestWorkflowDeploy_FailsClosedWhenTheBindingsCannotBeRead(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	h.createSQLConnector("unreadable-wf-api", "postgres", postgresValues(realSecret, nil))
	wfID := h.createWorkflow("wf_unreadable_bindings", map[string]any{
		"output_mapping": map[string]any{"rows": "{{call.output}}"},
		"nodes":          []any{map[string]any{"id": "call", "type": "tool", "connector": "unreadable-wf-api"}},
	})
	h.breakTable("workflow_connectors")

	_, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionDeploy}, "e2e-user")
	requireNotARefusal(t, err, "get workflow connector bindings")
	if latest := h.latestWorkflowDeployment(wfID); latest != nil {
		t.Errorf("a refused deploy left a deployment row (status %q)", latest.Status)
	}
}
