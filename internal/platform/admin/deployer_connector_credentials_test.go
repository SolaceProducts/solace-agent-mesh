// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const (
	unfilled        = "<<__SAM_REQUIRED__>>"
	realSecret      = "s3cr3t-value-0d9f"
	deployPrompt    = "You are a database assistant that helps users query their database"
	connectorsRoute = "Connectors page"
	agentsRoute     = "Agents page"
)

func postgresValues(password any, extra map[string]any) map[string]any {
	values := map[string]any{
		"hostname": "db.example.com",
		"port":     "5432",
		"database": "testdb",
		"username": "testuser",
		"password": password,
	}
	for k, v := range extra {
		values[k] = v
	}
	return values
}

func (h *e2eHarness) tryDeploy(agentID, action string) error {
	h.t.Helper()
	_, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agentID, Action: action}, "e2e-user")
	return err
}

func (h *e2eHarness) assertNothingDeployed(agentID string) {
	h.t.Helper()
	latest, err := h.deployRepo.GetLatestByAgentID(h.ctx, agentID)
	if err != nil {
		h.t.Fatalf("read latest deployment: %v", err)
	}
	if latest != nil {
		h.t.Errorf("a refused deploy left a deployment row (status %q)", latest.Status)
	}
	if info, err := h.aweExe.GetInstance(h.agentInstanceName(agentID)); err == nil && info != nil {
		h.t.Errorf("a refused deploy started instance %s", info.Name)
	}
}

func unfilledDetails(t *testing.T, err error, wantMessage string) *ValidationError {
	t.Helper()
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("deploy error = %v (%T), want a *ValidationError (422)", err, err)
	}
	if verr.Message != wantMessage {
		t.Errorf("message = %q, want %q", verr.Message, wantMessage)
	}
	if !strings.HasSuffix(verr.Message, "cannot be deployed until all required fields are provided") {
		t.Errorf("message %q lacks the wording the Builder UI routes to the user", verr.Message)
	}
	if _, matched := UnfilledDeployRefusal(err); !matched {
		t.Errorf("UnfilledDeployRefusal does not recognise %v, so the v2 handlers would drop its sentence", err)
	}
	return verr
}

func detailsText(verr *ValidationError) string {
	var b strings.Builder
	for key, msgs := range verr.Details {
		b.WriteString(key + " " + strings.Join(msgs, " ") + "\n")
	}
	return b.String()
}

func TestAgentDeploy_RefusesAConnectorStillHoldingAPlaceholder(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, []*llm.ChatResponse{{Content: "ok", FinishReason: "stop"}})

	type connector struct {
		name   string
		values map[string]any
	}
	cases := []struct {
		name           string
		action         string
		deployedBefore bool
		connectors     []connector
		wantNamed      []string
		wantFields     []string
		notInError     []string
	}{
		{
			name:       "placeholder password refuses",
			action:     DeployActionDeploy,
			connectors: []connector{{"pet-api-a", postgresValues(unfilled, nil)}},
			wantNamed:  []string{`"pet-api-a"`},
			wantFields: []string{"password"},
		},
		{
			name:           "update of a running agent whose connector was blanked refuses the same way",
			action:         DeployActionUpdate,
			deployedBefore: true,
			connectors:     []connector{{"pet-api-b", postgresValues(unfilled, nil)}},
			wantNamed:      []string{`"pet-api-b"`},
			wantFields:     []string{"password"},
		},
		{
			name:       "filled connector deploys",
			action:     DeployActionDeploy,
			connectors: []connector{{"pet-api-c", postgresValues(realSecret, nil)}},
		},
		{
			name:       "no connector deploys",
			action:     DeployActionDeploy,
			connectors: nil,
		},
		{
			name:   "one of two connectors unfilled names only that one",
			action: DeployActionDeploy,
			connectors: []connector{
				{"pet-api-d-ok", postgresValues(realSecret, map[string]any{"database": "okdb"})},
				{"pet-api-d-bad", postgresValues(unfilled, nil)},
			},
			wantNamed:  []string{`"pet-api-d-bad"`},
			wantFields: []string{"password"},
			notInError: []string{"pet-api-d-ok", realSecret},
		},
		{
			name:   "both connectors unfilled name both",
			action: DeployActionDeploy,
			connectors: []connector{
				{"pet-api-e-one", postgresValues(unfilled, map[string]any{"database": "onedb"})},
				{"pet-api-e-two", postgresValues(unfilled, map[string]any{"database": "twodb"})},
			},
			wantNamed:  []string{`"pet-api-e-one"`, `"pet-api-e-two"`},
			wantFields: []string{"password"},
		},
		{
			name:       "partly filled connector names only what is missing",
			action:     DeployActionDeploy,
			connectors: []connector{{"pet-api-f", postgresValues(realSecret, map[string]any{"username": unfilled})}},
			wantNamed:  []string{`"pet-api-f"`},
			wantFields: []string{"username"},
			notInError: []string{"password", realSecret},
		},
		{
			name:       "two placeholders in one connector name both",
			action:     DeployActionDeploy,
			connectors: []connector{{"pet-api-g", postgresValues(unfilled, map[string]any{"username": unfilled})}},
			wantNamed:  []string{`"pet-api-g"`},
			wantFields: []string{"password", "username"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids := make([]string, 0, len(tc.connectors))
			for _, c := range tc.connectors {
				values := c.values
				if tc.deployedBefore {
					values = postgresValues(realSecret, nil)
				}
				ids = append(ids, h.createSQLConnector(c.name, "postgres", values))
			}
			agentID := h.createAgentWithConnectors("agent-"+strings.ReplaceAll(tc.name, " ", "-"), deployPrompt, ids)
			if tc.deployedBefore {
				h.deploy(agentID)
				for i, c := range tc.connectors {
					if _, err := h.connectorSvc.Update(h.ctx, ids[i], UpdateConnectorRequest{Values: c.values}, "e2e-user"); err != nil {
						t.Fatalf("blank connector %q: %v", c.name, err)
					}
				}
			}

			err := h.tryDeploy(agentID, tc.action)
			if len(tc.wantNamed) == 0 {
				if err != nil {
					t.Fatalf("deploy refused: %v", err)
				}
				return
			}
			verr := unfilledDetails(t, err, unfilledAgentMessage)
			text := detailsText(verr)
			for _, want := range append(slices.Clone(tc.wantNamed), tc.wantFields...) {
				if !strings.Contains(text, want) {
					t.Errorf("details %q do not name %s", text, want)
				}
			}
			if !strings.Contains(text, connectorsRoute) {
				t.Errorf("details %q do not say where to enter the value", text)
			}
			for _, banned := range append([]string{unfilled}, tc.notInError...) {
				if strings.Contains(text, banned) || strings.Contains(verr.Message, banned) {
					t.Errorf("refusal %q %q must not contain %q", verr.Message, text, banned)
				}
			}
			if len(verr.Details) != len(tc.wantFields)*len(tc.wantNamed) {
				t.Errorf("validation details = %v, want one entry per connector field", verr.Details)
			}
			if !tc.deployedBefore {
				h.assertNothingDeployed(agentID)
			}
		})
	}
}

func TestAgentDeploy_RetryAfterTheCredentialIsSet(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, []*llm.ChatResponse{{Content: "ok", FinishReason: "stop"}})

	connectorID := h.createSQLConnector("shared-retry-api", "postgres", postgresValues(unfilled, nil))
	first := h.createAgentWithConnectors("retry-agent-one", deployPrompt, []string{connectorID})
	second := h.createAgentWithConnectors("retry-agent-two", deployPrompt, []string{connectorID})

	for attempt := 1; attempt <= 2; attempt++ {
		for _, agentID := range []string{first, second} {
			verr := unfilledDetails(t, h.tryDeploy(agentID, DeployActionDeploy), unfilledAgentMessage)
			if !strings.Contains(detailsText(verr), `"shared-retry-api"`) {
				t.Errorf("attempt %d: details %q do not name the connector", attempt, detailsText(verr))
			}
			h.assertNothingDeployed(agentID)
		}
	}

	if _, err := h.connectorSvc.Update(h.ctx, connectorID, UpdateConnectorRequest{Values: postgresValues(realSecret, nil)}, "e2e-user"); err != nil {
		t.Fatalf("fill the connector: %v", err)
	}

	for _, agentID := range []string{first, second} {
		if err := h.tryDeploy(agentID, DeployActionDeploy); err != nil {
			t.Fatalf("deploy after the credential is set: %v", err)
		}
		h.waitForInstance(h.agentInstanceName(agentID), 10*time.Second)
	}

	if _, err := h.connectorSvc.Update(h.ctx, connectorID, UpdateConnectorRequest{Values: postgresValues(realSecret, nil)}, "e2e-user"); err != nil {
		t.Fatalf("re-apply the filled connector: %v", err)
	}
	if err := h.tryDeploy(first, DeployActionDeploy); err != nil {
		t.Fatalf("redeploy after a converging re-apply: %v", err)
	}
}

func TestAgentDeploy_UndeployIgnoresConnectorPlaceholders(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, []*llm.ChatResponse{{Content: "ok", FinishReason: "stop"}})

	connectorID := h.createSQLConnector("blanked-later-api", "postgres", postgresValues(realSecret, nil))
	agentID := h.createAgentWithConnectors("undeploy-agent", deployPrompt, []string{connectorID})
	h.deploy(agentID)

	if _, err := h.connectorSvc.Update(h.ctx, connectorID, UpdateConnectorRequest{Values: postgresValues(unfilled, nil)}, "e2e-user"); err != nil {
		t.Fatalf("blank the connector: %v", err)
	}
	if err := h.tryDeploy(agentID, DeployActionUndeploy); err != nil {
		t.Fatalf("undeploy through the deploy route: %v", err)
	}
}

func TestWorkflowDeploy_RefusesAConnectorStillHoldingAPlaceholder(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	connectorID := h.createSQLConnector("wf-bound-api", "postgres", postgresValues(unfilled, nil))
	wfID := h.createWorkflow("wf_bound_to_placeholder", map[string]any{
		"output_mapping": map[string]any{"rows": "{{call.output}}"},
		"nodes":          []any{map[string]any{"id": "call", "type": "tool", "connector": "wf-bound-api"}},
	})

	for attempt := 1; attempt <= 2; attempt++ {
		_, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionDeploy}, "e2e-user")
		verr := unfilledDetails(t, err, unfilledWorkflowMessage)
		if text := detailsText(verr); !strings.Contains(text, `"wf-bound-api"`) || !strings.Contains(text, "password") {
			t.Errorf("attempt %d: details %q do not name the connector and value", attempt, text)
		}
		if latest := h.latestWorkflowDeployment(wfID); latest != nil {
			t.Errorf("attempt %d: a refused deploy left a deployment row (status %q)", attempt, latest.Status)
		}
		if info, getErr := h.aweExe.GetInstance(workflowInstanceName(wfID)); getErr == nil && info != nil {
			t.Errorf("attempt %d: a refused deploy started instance %s", attempt, info.Name)
		}
	}

	if _, err := h.connectorSvc.Update(h.ctx, connectorID, UpdateConnectorRequest{Values: postgresValues(realSecret, nil)}, "e2e-user"); err != nil {
		t.Fatalf("fill the connector: %v", err)
	}
	if resp := h.attemptWorkflowDeploy(wfID); resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy after the credential is set: status %q (%s)", resp.Status, resp.ErrorMessage)
	}
}

func TestRemoteAgentDeploy_RefusesACredentialStillHoldingAPlaceholder(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	const tokenURL = "https://idp.example.com/token"
	cases := []struct {
		name       string
		authType   string
		authConfig map[string]any
		cardType   string
		cardConfig map[string]any
		wantFields []string
	}{
		{name: "bearer token placeholder refuses", authType: "bearer", authConfig: map[string]any{"token": unfilled},
			wantFields: []string{"agentAuthConfig.token"}},
		{name: "api key placeholder is named apiKey", authType: "apikey", authConfig: map[string]any{"apiKey": unfilled, "headerName": "X-Key"},
			wantFields: []string{"agentAuthConfig.apiKey"}},
		{name: "api key header name placeholder is named headerName", authType: "apikey", authConfig: map[string]any{"apiKey": realSecret, "headerName": unfilled},
			wantFields: []string{"agentAuthConfig.headerName"}},
		{name: "basic password placeholder refuses", authType: "basic", authConfig: map[string]any{"username": "svc", "password": unfilled},
			wantFields: []string{"agentAuthConfig.password"}},
		{name: "basic username placeholder refuses", authType: "basic", authConfig: map[string]any{"username": unfilled, "password": realSecret},
			wantFields: []string{"agentAuthConfig.username"}},
		{name: "oauth2 names every unfilled field as the request does", authType: "oauth2",
			authConfig: map[string]any{"clientId": unfilled, "clientSecret": unfilled, "tokenEndpoint": tokenURL},
			wantFields: []string{"agentAuthConfig.clientId", "agentAuthConfig.clientSecret"}},
		{name: "card auth placeholder refuses", authType: "none", cardType: "bearer", cardConfig: map[string]any{"token": unfilled},
			wantFields: []string{"agentCardAuthConfig.token"}},
		{name: "agent and card placeholders are both named", authType: "bearer", authConfig: map[string]any{"token": unfilled},
			cardType: "apikey", cardConfig: map[string]any{"apiKey": unfilled, "headerName": "X-Card"},
			wantFields: []string{"agentAuthConfig.token", "agentCardAuthConfig.apiKey"}},
		{name: "filled bearer token deploys", authType: "bearer", authConfig: map[string]any{"token": realSecret}},
		{name: "filled oauth2 deploys", authType: "oauth2",
			authConfig: map[string]any{"clientId": "id", "clientSecret": realSecret, "tokenEndpoint": tokenURL}},
		{name: "no auth deploys", authType: "none", authConfig: map[string]any{}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
				Name:                fmt.Sprintf("remote-placeholder-%d", i),
				Description:         "A remote agent whose credential may still be a placeholder.",
				AgentURL:            "https://remote.example.com",
				AgentCardLocation:   "none",
				Skills:              []any{map[string]any{"name": "default-skill"}},
				AgentAuthType:       tc.authType,
				AgentAuthConfig:     tc.authConfig,
				AgentCardAuthType:   tc.cardType,
				AgentCardAuthConfig: tc.cardConfig,
			}, "e2e-user")
			if err != nil {
				t.Fatalf("create remote agent: %v", err)
			}
			err = h.tryDeploy(resp.ID, DeployActionDeploy)
			if len(tc.wantFields) == 0 {
				if err != nil {
					t.Fatalf("deploy refused: %v", err)
				}
				return
			}
			verr := unfilledDetails(t, err, unfilledRemoteAgentMessage)
			if len(verr.Details) != len(tc.wantFields) {
				t.Errorf("details = %v, want exactly %v", verr.Details, tc.wantFields)
			}
			for _, field := range tc.wantFields {
				if _, named := verr.Details[field]; !named {
					t.Errorf("details %v do not name %s", verr.Details, field)
				}
			}
			text := detailsText(verr)
			if !strings.Contains(text, agentsRoute) || !strings.Contains(text, fmt.Sprintf("%q", fmt.Sprintf("remote-placeholder-%d", i))) {
				t.Errorf("details %q do not name the agent and say where to enter the value", text)
			}
			if strings.Contains(text, unfilled) || strings.Contains(text, realSecret) || strings.Contains(text, "_") {
				t.Errorf("details %q must carry neither a value nor a stored snake_case key", text)
			}
			h.assertNothingDeployed(resp.ID)
		})
	}
}

func TestPlaceholderFields(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	cases := []struct {
		name   string
		values *string
		want   []string
	}{
		{"no stored values", nil, nil},
		{"malformed JSON says nothing", str(`{"password": `), nil},
		{"filled", str(`{"password": "x", "hostname": "h"}`), nil},
		{"top-level placeholder", str(`{"password": "<<__SAM_REQUIRED__>>"}`), []string{"password"}},
		{"nested and listed placeholders", str(`{"auth": {"token": "<<__SAM_REQUIRED__>>"}, "operations": [{"topic": "<<__SAM_REQUIRED__>>"}]}`), []string{"auth.token", "operations[0].topic"}},
		{"text that merely contains the marker is a value", str(`{"note": "see <<__SAM_REQUIRED__>> docs"}`), nil},
		{"empty string is not a placeholder", str(`{"password": ""}`), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := placeholderFields(tc.values); !slices.Equal(got, tc.want) {
				t.Errorf("placeholderFields = %v, want %v", got, tc.want)
			}
		})
	}
}
