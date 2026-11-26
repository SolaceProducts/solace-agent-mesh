// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func TestBakedTokenScopesAuthorizeToolsListAndCall(t *testing.T) {
	const sales, other = "sales", "other"
	a, brk := newAuthzTestAdapter(t, mustParseConfig(t, nil), nil)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sales})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: other})
	a.core.SetScopeResolver(authzStubResolver{scopes: []string{rbac.AgentInvokeScope(other)}})
	a.toolMap = map[string]toolMapping{"sales_ask": {agentName: sales}, "other_ask": {agentName: other}}
	a.agentTools = map[string][]string{}
	a.tasks = map[string]*taskState{}

	km, err := trust.NewKeyManager("test-mcp-gw")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := trust.NewSigner(km, "test-mcp-gw", time.Hour, 2*time.Hour)
	authz, err := oidcauth.NewAuthorizationService(oidcauth.AuthorizationServiceConfig{Type: "none"}, "test-mcp-gw", noopLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	a.validator = oidcauth.NewTokenValidator(signer, authz, nil, "test-mcp-gw", noopLogger())
	token, err := oidcauth.MintSAMToken(signer, oidcauth.MintClaims{
		UserID: "alice@example.com",
		Roles:  []string{"viewer"},
		Scopes: []string{rbac.AgentInvokeScope(sales)},
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	var ctx context.Context
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	a.authMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() })).
		ServeHTTP(httptest.NewRecorder(), req)
	if ctx == nil {
		t.Fatal("the middleware refused a valid SAM token")
	}

	var listed []string
	for _, tool := range a.toolListFilter(ctx, []mcplib.Tool{{Name: "sales_ask"}, {Name: "other_ask"}}) {
		listed = append(listed, tool.Name)
	}
	if !slices.Equal(listed, []string{"sales_ask"}) {
		t.Errorf("tools/list = %v, want only the tool the token's scopes grant", listed)
	}

	for tool, wantPublished := range map[string]bool{"sales_ask": true, "other_ask": false} {
		t.Run("tools/call "+tool, func(t *testing.T) {
			q, err := brk.CreateQueue("baked-"+tool, broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", a.toolMap[tool].agentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			callCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _, _ = a.callTool(callCtx, tool, toolCall(tool, "hello"))
			}()

			wait := 300 * time.Millisecond
			if wantPublished {
				wait = 2 * time.Second
			}
			recvCtx, recvCancel := context.WithTimeout(t.Context(), wait)
			defer recvCancel()
			received, _ := brk.Receive(recvCtx, q)
			cancel()
			<-done
			if published := received != nil; published != wantPublished {
				t.Errorf("published = %v, want %v", published, wantPublished)
			}
		})
	}
}
