// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type stubScopeResolver struct{ scopes []string }

func (s stubScopeResolver) ResolveScopes(userID string, roles []string) []string {
	return s.scopes
}

type recordingScopeResolver struct {
	scopes []string
	userID string
	calls  int
}

func (r *recordingScopeResolver) ResolveScopes(userID string, _ []string) []string {
	r.userID = userID
	r.calls++
	return r.scopes
}

func newFilterAdapter(t *testing.T, toolMap map[string]string, resolver entrypoint.ScopeResolver) *Adapter {
	t.Helper()
	core := newFakeGatewayCore(t, "go-mcp-gw")
	if resolver != nil {
		core.SetScopeResolver(resolver)
	}
	a := &Adapter{
		core:    core,
		cfg:     &Config{EnableAuth: true, Transport: "sse"},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		toolMap: map[string]toolMapping{},
	}
	for name, agent := range toolMap {
		a.toolMap[name] = toolMapping{agentName: agent}
	}
	return a
}

func TestToolListFilter_DevMode_ReturnsAll(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
		"agent_b_skill_y": "agent_b",
	}, nil)
	a.cfg.DevMode = true
	a.cfg.EnableAuth = true

	tools := []mcplib.Tool{{Name: "agent_a_skill_x"}, {Name: "agent_b_skill_y"}}
	out := a.toolListFilter(context.Background(), tools)
	if len(out) != 2 {
		t.Errorf("dev_mode bypass: got %d, want 2", len(out))
	}
}

func TestToolListFilter_AuthDisabled_StillFiltersOnSynthesizedPrincipal(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
		"agent_b_skill_y": "agent_b",
	}, stubScopeResolver{scopes: []string{rbac.AgentInvokeScope("agent_a")}})
	a.cfg.EnableAuth = false
	a.cfg.RunAs = "scoped"

	tools := []mcplib.Tool{{Name: "agent_a_skill_x"}, {Name: "agent_b_skill_y"}}
	out := a.toolListFilter(context.Background(), tools)
	if len(out) != 1 || out[0].Name != "agent_a_skill_x" {
		t.Errorf("auth disabled with a scoped principal: got %v, want only agent_a_skill_x", toolNames(out))
	}
}

func TestToolListFilter_NoAuthorizationConfigured_ReturnsAll(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{"agent_a_skill_x": "agent_a"}, nil)
	a.cfg.EnableAuth = false

	tools := []mcplib.Tool{{Name: "agent_a_skill_x"}}
	out := a.toolListFilter(context.Background(), tools)
	if len(out) != 1 {
		t.Errorf("no authorization configured: got %d, want 1", len(out))
	}
}

func toolNames(tools []mcplib.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}

func TestToolListFilter_WorkflowGatedOnInvokeScope(t *testing.T) {
	a := newFilterAdapter(t, nil, nil)
	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"
	a.toolMap["wf_skill"] = toolMapping{agentName: wfCard, isWorkflow: true}
	tools := []mcplib.Tool{{Name: "wf_skill"}}

	agentCtx := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"agent:*:invoke"}}))
	if out := a.toolListFilter(agentCtx, tools); len(out) != 0 {
		t.Errorf("workflow tool must be hidden for agent:*:invoke (workflow category not covered), got %v", names(out))
	}

	wfCtx := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"workflow:" + dashedUUID + ":invoke"}}))
	if out := a.toolListFilter(wfCtx, tools); len(out) != 1 {
		t.Errorf("workflow tool must be visible for workflow:<uuid>:invoke, got %v", names(out))
	}
}

func TestToolListFilter_FriendlyNamedWorkflow(t *testing.T) {
	a := newFilterAdapter(t, nil, nil)
	const wf = "data-pipeline"
	a.toolMap["wf_skill"] = toolMapping{agentName: wf, isWorkflow: true}
	tools := []mcplib.Tool{{Name: "wf_skill"}}

	agentCtx := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"agent:*:invoke"}}))
	if out := a.toolListFilter(agentCtx, tools); len(out) != 0 {
		t.Errorf("friendly-named workflow must be hidden for agent:*:invoke, got %v", names(out))
	}

	wfCtx := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"workflow:" + wf + ":invoke"}}))
	if out := a.toolListFilter(wfCtx, tools); len(out) != 1 {
		t.Errorf("friendly-named workflow must be visible for workflow:%s:invoke, got %v", wf, names(out))
	}
}

func TestToolListFilter_ScopePresent_Included(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
	}, nil)

	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"agent:agent_a:invoke"}})
	ctx := auth.ContextWithSubject(context.Background(), id)
	out := a.toolListFilter(ctx, []mcplib.Tool{{Name: "agent_a_skill_x"}})
	if len(out) != 1 {
		t.Errorf("scope present: got %d, want 1", len(out))
	}
}

func TestToolListFilter_ScopeAbsent_Dropped(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
		"agent_b_skill_y": "agent_b",
	}, stubScopeResolver{})

	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"agent:agent_a:invoke"}})
	ctx := auth.ContextWithSubject(context.Background(), id)
	out := a.toolListFilter(ctx, []mcplib.Tool{
		{Name: "agent_a_skill_x"}, {Name: "agent_b_skill_y"},
	})
	if len(out) != 1 || out[0].Name != "agent_a_skill_x" {
		t.Errorf("filtered: got %v, want only agent_a_skill_x", names(out))
	}
}

func TestToolListFilter_RequiredScopesAbsent_Dropped(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"obs_skill":  "observability-agent",
		"open_skill": "open-agent",
	}, nil)

	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "observability-agent",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIRequiredScopes,
				Params: map[string]any{"scopes": []any{"analytics:_:read"}},
			}},
		},
	})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "open-agent"})

	tools := []mcplib.Tool{{Name: "obs_skill"}, {Name: "open_skill"}}

	invokeOnly := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"agent:*:invoke"}}))
	if out := a.toolListFilter(invokeOnly, tools); len(out) != 1 || out[0].Name != "open_skill" {
		t.Errorf("required_scopes leak: got %v, want only open_skill", names(out))
	}

	fullyScoped := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "bob", Scopes: []string{"agent:*:invoke", "analytics:_:read"}}))
	if out := a.toolListFilter(fullyScoped, tools); len(out) != 2 {
		t.Errorf("scoped caller: got %v, want both tools", names(out))
	}
}

func TestToolListFilter_DeployedComponentScopes(t *testing.T) {
	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const agentCard = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"

	newAdapter := func(t *testing.T) *Adapter {
		return newFilterAdapter(t, map[string]string{
			"wf_tool":    wfCard,
			"agent_tool": agentCard,
		}, nil)
	}
	tools := []mcplib.Tool{{Name: "wf_tool"}, {Name: "agent_tool"}}

	agentWildcard := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"agent:*:invoke"}}))
	if out := newAdapter(t).toolListFilter(agentWildcard, tools); len(out) != 1 || out[0].Name != "agent_tool" {
		t.Errorf("agent wildcard: got %v, want only agent_tool", names(out))
	}

	exact := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "bob", Scopes: []string{"workflow:" + dashedUUID + ":invoke", "agent:" + dashedUUID + ":invoke"}}))
	if out := newAdapter(t).toolListFilter(exact, tools); len(out) != 2 {
		t.Errorf("exact dashed-uuid grants: got %v, want both tools", names(out))
	}

	wfWildcard := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "carol", Scopes: []string{"workflow:*:invoke"}}))
	if out := newAdapter(t).toolListFilter(wfWildcard, tools); len(out) != 1 || out[0].Name != "wf_tool" {
		t.Errorf("workflow wildcard: got %v, want only wf_tool", names(out))
	}
}

func TestToolListFilter_WildcardScope_AllIncluded(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
		"agent_b_skill_y": "agent_b",
	}, stubScopeResolver{scopes: []string{"*"}})

	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"*"}})
	ctx := auth.ContextWithSubject(context.Background(), id)
	out := a.toolListFilter(ctx, []mcplib.Tool{
		{Name: "agent_a_skill_x"}, {Name: "agent_b_skill_y"},
	})
	if len(out) != 2 {
		t.Errorf("wildcard: got %d, want 2", len(out))
	}
}

func TestToolListFilter_AuthDisabled_EmptyScopes_DeniedAll(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
		"agent_b_skill_y": "agent_b",
	}, stubScopeResolver{})
	a.cfg.EnableAuth = false
	a.cfg.RunAs = "ungranted"

	out := a.toolListFilter(context.Background(), []mcplib.Tool{
		{Name: "agent_a_skill_x"}, {Name: "agent_b_skill_y"},
	})
	if len(out) != 0 {
		t.Errorf("auth disabled with an unscoped principal: got %v, want an empty catalog", toolNames(out))
	}
}

func TestToolListFilter_EmptyScopesEnterprise_DeniedAll(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
	}, stubScopeResolver{})

	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: nil})
	ctx := auth.ContextWithSubject(context.Background(), id)
	out := a.toolListFilter(ctx, []mcplib.Tool{{Name: "agent_a_skill_x"}})
	if len(out) != 0 {
		t.Errorf("denyEnterpriseEmpty: got %d, want 0", len(out))
	}
}

func TestToolListFilter_EnforcedNoResolver_DeniedAll(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
	}, nil)
	a.core.SetAuthzEnabled(true)

	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: nil})
	ctx := auth.ContextWithSubject(context.Background(), id)
	out := a.toolListFilter(ctx, []mcplib.Tool{{Name: "agent_a_skill_x"}})
	if len(out) != 0 {
		t.Errorf("enforced + no resolver + empty scopes: got %d tools, want 0 (must not leak the catalog)", len(out))
	}
}

func TestToolListFilter_NoIdentity_UsesDefaultUserIdentity(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
	}, nil)
	a.cfg.DefaultUserIdentity = "anonymous"

	out := a.toolListFilter(context.Background(), []mcplib.Tool{{Name: "agent_a_skill_x"}})
	if len(out) != 1 {
		t.Errorf("no identity, no resolver: got %d, want 1", len(out))
	}
}

func TestToolListFilter_NoIdentity_UsesRunAsPrincipal(t *testing.T) {
	rec := &recordingScopeResolver{scopes: []string{"agent:agent_a:invoke"}}
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
	}, rec)
	a.cfg.RunAs = "orders-bot"

	out := a.toolListFilter(context.Background(), []mcplib.Tool{{Name: "agent_a_skill_x"}})
	if want := rbac.SystemUserPrincipal("orders-bot"); rec.userID != want {
		t.Errorf("resolved scopes for %q, want %q", rec.userID, want)
	}
	if len(out) != 1 {
		t.Errorf("got %d tools, want 1", len(out))
	}
}

func TestPlantDefaultIdentity_StdioListAndCallAgree(t *testing.T) {
	rec := &recordingScopeResolver{scopes: []string{"agent:agent_a:invoke"}}
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
	}, rec)
	a.cfg.Transport = "stdio"
	a.cfg.RunAs = "orders-bot"
	want := rbac.SystemUserPrincipal("orders-bot")

	out := a.toolListFilter(context.Background(), []mcplib.Tool{{Name: "agent_a_skill_x"}})
	if len(out) != 1 {
		t.Errorf("tools/list returned %d tools, want 1", len(out))
	}
	listPrincipal := rec.userID

	var callPrincipal string
	handler := a.plantDefaultIdentity(func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if id, ok := auth.SubjectFromContext(ctx); ok && id != nil {
			callPrincipal = id.UserID()
		}
		return nil, nil
	})
	if _, err := handler(context.Background(), mcplib.CallToolRequest{}); err != nil {
		t.Fatalf("tool handler: %v", err)
	}

	if listPrincipal != want {
		t.Errorf("tools/list authorized as %q, want %q", listPrincipal, want)
	}
	if callPrincipal != want {
		t.Errorf("tools/call authorized as %q, want %q", callPrincipal, want)
	}
}

func TestPlantDefaultIdentity_PreservesExistingIdentity(t *testing.T) {
	a := newFilterAdapter(t, nil, nil)
	a.cfg.RunAs = "orders-bot"

	var got string
	handler := a.plantDefaultIdentity(func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if id, ok := auth.SubjectFromContext(ctx); ok && id != nil {
			got = id.UserID()
		}
		return nil, nil
	})
	ctx := auth.ContextWithSubject(context.Background(), auth.NewTestSubject(auth.SubjectFields{UserID: "real@corp"}))
	if _, err := handler(ctx, mcplib.CallToolRequest{}); err != nil {
		t.Fatalf("tool handler: %v", err)
	}
	if got != "real@corp" {
		t.Errorf("identity = %q, want real@corp — the middleware must not displace a validated caller", got)
	}
}

func TestToolListFilter_UnmappedTool_PassesThrough(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{}, nil)
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"agent:foo:invoke"}})
	ctx := auth.ContextWithSubject(context.Background(), id)
	out := a.toolListFilter(ctx, []mcplib.Tool{{Name: "builtin-tool"}})
	if len(out) != 1 {
		t.Errorf("unmapped tool should pass through: got %d, want 1", len(out))
	}
}

func names(tools []mcplib.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func TestToolListFilter_EndToEnd_AppliedByMCPServer(t *testing.T) {
	a := newFilterAdapter(t, map[string]string{
		"agent_a_skill_x": "agent_a",
		"agent_b_skill_y": "agent_b",
	}, nil)

	srv := mcpserver.NewMCPServer(
		"test-server", "1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithToolFilter(a.toolListFilter),
	)
	srv.AddTool(mcplib.NewTool("agent_a_skill_x"), nopHandler)
	srv.AddTool(mcplib.NewTool("agent_b_skill_y"), nopHandler)

	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{"agent:agent_a:invoke"}})
	ctx := auth.ContextWithSubject(context.Background(), id)

	resp := srv.HandleMessage(ctx, []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/list"
	}`))

	jsonResp, ok := resp.(mcplib.JSONRPCResponse)
	if !ok {
		t.Fatalf("response not JSONRPCResponse: %T = %+v", resp, resp)
	}
	result, ok := jsonResp.Result.(mcplib.ListToolsResult)
	if !ok {
		t.Fatalf("result not ListToolsResult: %T = %+v", jsonResp.Result, jsonResp.Result)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("filtered list: got %d tools (%v), want 1", len(result.Tools), names(result.Tools))
	}
	if result.Tools[0].Name != "agent_a_skill_x" {
		t.Errorf("filtered list: got %q, want %q", result.Tools[0].Name, "agent_a_skill_x")
	}
}

func TestPlantDefaultIdentity_EndToEnd_AppliedByMCPServer(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "default",
		"gateway_id": "go-mcp-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init gateway core: %v", err)
	}

	a := &Adapter{rawCfg: map[string]any{
		"transport":   "stdio",
		"enable_auth": false,
		"run_as":      "orders-bot",
	}}
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}

	var seen string
	a.mcpServer.AddTool(mcplib.NewTool("agent_a_skill_x"), func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if id, ok := auth.SubjectFromContext(ctx); ok && id != nil {
			seen = id.UserID()
		}
		return mcplib.NewToolResultText(""), nil
	})

	resp := a.mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call",
		"params": {"name": "agent_a_skill_x"}
	}`))
	if _, ok := resp.(mcplib.JSONRPCResponse); !ok {
		t.Fatalf("response not JSONRPCResponse: %T = %+v", resp, resp)
	}
	if want := rbac.SystemUserPrincipal("orders-bot"); seen != want {
		t.Errorf("tool handler saw identity %q, want %q", seen, want)
	}
}

func TestAToolCallTheFilterRefusesIsLoggedAsADenial(t *testing.T) {
	for name, tc := range map[string]struct {
		scopes     []string
		tool       string
		wantDenied bool
	}{
		"agent with its invoke scope":    {scopes: []string{"agent:agent_a:invoke"}, tool: "agent_a_skill_x"},
		"agent without its invoke scope": {scopes: []string{"agent:agent_a:invoke"}, tool: "agent_b_skill_y", wantDenied: true},
		"Builder with builder use":       {scopes: []string{rbac.ScopeBuilderUse}, tool: "builder_chat"},
		"Builder without builder use":    {scopes: []string{"agent:agent_a:invoke"}, tool: "builder_chat", wantDenied: true},
	} {
		t.Run(name, func(t *testing.T) {
			a := newFilterAdapter(t, map[string]string{
				"agent_a_skill_x": "agent_a",
				"agent_b_skill_y": "agent_b",
				"builder_chat":    entrypoint.BuilderAgentName,
			}, stubScopeResolver{})
			logged := &bytes.Buffer{}
			a.logger = slog.New(slog.NewTextHandler(logged, nil))
			a.buildMCPServer()
			for tool := range a.toolMap {
				a.mcpServer.AddTool(mcplib.NewTool(tool), nopHandler)
			}

			ctx := auth.ContextWithSubject(context.Background(), auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: tc.scopes}))
			resp := a.mcpServer.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tc.tool+`"}}`))
			if _, refused := resp.(mcplib.JSONRPCError); refused != tc.wantDenied {
				t.Fatalf("tools/call %s answered %T %+v, want refused=%v", tc.tool, resp, resp, tc.wantDenied)
			}
			if !tc.wantDenied {
				if strings.Contains(logged.String(), logToolCallFiltered) {
					t.Errorf("an allowed call was logged as a denial:\n%s", logged.String())
				}
				return
			}
			line := logLineWith(t, logged, logToolCallFiltered)
			if got := logFieldValue(t, line, "level"); got != "WARN" {
				t.Errorf("denial logged at %s, want WARN", got)
			}
			if got := logFieldValue(t, line, "reason"); got != "missing_invoke_scope" {
				t.Errorf("denial reason %q, want missing_invoke_scope", got)
			}
			if strings.Contains(line, "alice") {
				t.Errorf("denial logs the raw user id: %q", line)
			}
		})
	}
}

func nopHandler(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	return mcplib.NewToolResultText(""), nil
}

func TestAgentAuthz_NilCoreFallsBackToEnforcingAuthorizer(t *testing.T) {
	a := &Adapter{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, ok := a.agentAuthz().(*rbac.AgentAuth); !ok {
		t.Fatalf("agentAuthz() with no core must fall back to a real *rbac.AgentAuth, got %T", a.agentAuthz())
	}
}

func TestToolListFilter_BearerCallerScopesAreResolved(t *testing.T) {
	rec := &recordingScopeResolver{scopes: []string{"agent:agent_a:invoke"}}
	a := newFilterAdapter(t, map[string]string{"agent_a_skill_x": "agent_a"}, rec)

	ctx := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true}))

	out := a.toolListFilter(ctx, []mcplib.Tool{{Name: "agent_a_skill_x"}})

	if rec.userID != "alice" {
		t.Errorf("resolver consulted for %q, want \"alice\" — the caller's scopes were never resolved", rec.userID)
	}
	if len(out) != 1 {
		t.Fatalf("catalog = %v, want the one tool; an unresolved bearer caller empties the MCP catalog", names(out))
	}
}

func TestToolListFilter_StdioPathDoesNotReResolve(t *testing.T) {
	rec := &recordingScopeResolver{scopes: nil}
	a := newFilterAdapter(t, map[string]string{"agent_a_skill_x": "agent_a"}, rec)

	a.toolListFilter(context.Background(), []mcplib.Tool{{Name: "agent_a_skill_x"}})

	if rec.calls != 1 {
		t.Errorf("resolver consulted %d times on the stdio path, want exactly 1 (the one inside defaultIdentity); "+
			"2 means the fromRequest guard is gone and every stdio tools/list pays a second broker round trip", rec.calls)
	}
}

func TestToolListFilter_ResolvedScopesSatisfyRequiredScopes(t *testing.T) {
	rec := &recordingScopeResolver{scopes: []string{"agent:*:invoke", "analytics:_:read"}}
	a := newFilterAdapter(t, map[string]string{"obs_skill": "observability-agent"}, rec)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "observability-agent",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIRequiredScopes,
				Params: map[string]any{"scopes": []any{"analytics:_:read"}},
			}},
		},
	})
	ctx := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true}))

	out := a.toolListFilter(ctx, []mcplib.Tool{{Name: "obs_skill"}})

	if len(out) != 1 {
		t.Fatalf("catalog = %v, want obs_skill: the resolved scopes hold analytics:_:read", names(out))
	}
}

func TestToolListFilter_ResolvedEmptyScopesAreNotReResolved(t *testing.T) {
	rec := &recordingScopeResolver{scopes: []string{"agent:*:invoke"}}
	a := newFilterAdapter(t, map[string]string{"agent_a_skill_x": "agent_a"}, rec)
	a.core.SetAuthzEnabled(true)
	ctx := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, ScopesResolved: true}))

	out := a.toolListFilter(ctx, []mcplib.Tool{{Name: "agent_a_skill_x"}})

	if rec.calls != 0 {
		t.Errorf("resolver consulted %d times for a caller resolved to no scopes, want 0", rec.calls)
	}
	if len(out) != 0 {
		t.Errorf("catalog = %v, want empty: the caller was granted nothing", names(out))
	}
}

func TestToolListFilter_TypedNilCallerDoesNotReResolve(t *testing.T) {
	rec := &recordingScopeResolver{scopes: nil}
	a := newFilterAdapter(t, map[string]string{"agent_a_skill_x": "agent_a"}, rec)
	ctx := auth.ContextWithSubject(context.Background(), nil)

	a.toolListFilter(ctx, []mcplib.Tool{{Name: "agent_a_skill_x"}})

	if rec.calls != 1 {
		t.Errorf("resolver consulted %d times for a typed-nil caller, want exactly 1 (the one inside defaultIdentity)", rec.calls)
	}
}
