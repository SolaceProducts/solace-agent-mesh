// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type memoryCredentials struct {
	mu     sync.Mutex
	values map[string]string
}

func credentialSlot(agentName, appName, userID, key string) string {
	return agentName + "|" + appName + "|" + userID + "|" + key
}

func (m *memoryCredentials) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[credentialSlot(agentName, appName, userID, key)], nil
}

func (m *memoryCredentials) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[credentialSlot(agentName, appName, userID, key)] = value
	return nil
}

func (m *memoryCredentials) Delete(_ context.Context, agentName, appName, userID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, credentialSlot(agentName, appName, userID, key))
	return nil
}

func (m *memoryCredentials) List(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}

func TestToolNodeFindsTheCredentialSeededForItsWorkflow(t *testing.T) {
	creds := &memoryCredentials{}
	token, err := json.Marshal(toolauth.OAuthToken{AccessToken: "seeded-token", TokenType: "Bearer"})
	if err != nil {
		t.Fatal(err)
	}
	if err = creds.Set(t.Context(), "orders-flow", "orders-flow", "system:eval", "crm", string(token)); err != nil {
		t.Fatal(err)
	}
	handler := toolauth.NewAuthHandler(&toolauth.AuthConfig{Type: "oauth2", CredentialKey: "crm"}, slog.Default())
	toolContextFor := func(workflowName string) *tool.Context {
		w := newAuthzWorkflowInstance(t, workflowName, &stubServices{brk: &capturingBroker{}, creds: creds})
		e := &DAGExecutor{cfg: w.cfg, state: NewExecutionState(workflowName, "exec-1"), instance: w, execCtx: NewExecutionContext(t.Context(), "task-1", "ctx-1", nil, a2a.UserProperties{UserID: "system:eval", Trigger: a2a.TriggerEval}, false)}
		return e.buildToolContext(&NodeConfig{ID: "lookup", Type: "tool", ToolName: "crm_lookup"}, NodeCompletion{NodeID: "lookup", SubTaskID: "sub-1"})
	}

	got, err := handler.GetOAuthToken(t.Context(), toolContextFor("orders-flow"))
	if err != nil || got == nil || got.AccessToken != "seeded-token" {
		t.Fatalf("token = %+v, %v; want the credential seeded for this workflow", got, err)
	}
	if _, err := handler.GetOAuthToken(t.Context(), toolContextFor("another-flow")); !errors.Is(err, toolauth.ErrAuthRequired) {
		t.Errorf("another workflow read the credential: %v", err)
	}
}
