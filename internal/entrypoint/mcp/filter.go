// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func (a *Adapter) toolListFilter(ctx context.Context, tools []mcplib.Tool) []mcplib.Tool {
	if a.cfg == nil || a.cfg.DevMode {
		return tools
	}

	identity, fromRequest := auth.SubjectFromContext(ctx)
	if !fromRequest || identity == nil {
		identity = a.defaultIdentity()
	} else if len(identity.Scopes()) == 0 && !identity.ScopesResolved() {
		a.fillScopesFromPlatformWhenLocalSourceEmpty(identity)
	}
	scopes := identity.Scopes()

	denyEnterpriseEmpty := a.authzEnforced() && len(scopes) == 0
	if denyEnterpriseEmpty {
		a.logger.Debug("MCP tools/list: empty scopes after resolution, returning empty catalog",
			"gatewayID", a.gatewayID(),
			"userID", identity.UserID(),
		)
		return []mcplib.Tool{}
	}
	if len(scopes) == 0 {
		return tools
	}

	invokeIdentity := coreauth.Identity{UserID: identity.UserID(), Scopes: scopes}
	agentNames := make([]string, 0, len(tools))
	workflowNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		a.toolMu.RLock()
		mapping, mapped := a.toolMap[tool.Name]
		a.toolMu.RUnlock()
		if !mapped {
			continue
		}
		if mapping.isWorkflow {
			workflowNames = append(workflowNames, mapping.agentName)
		} else {
			agentNames = append(agentNames, mapping.agentName)
		}
	}
	allowedInvoke := make(map[string]struct{}, len(agentNames)+len(workflowNames))
	for _, n := range a.agentAuthz().FilterAgents(ctx, &invokeIdentity, agentNames) {
		allowedInvoke[n] = struct{}{}
	}
	for _, n := range a.agentAuthz().FilterWorkflows(ctx, &invokeIdentity, workflowNames) {
		allowedInvoke[n] = struct{}{}
	}
	delete(allowedInvoke, entrypoint.BuilderAgentName)
	if coreauth.HasScope(identity.Scopes(), rbac.ScopeBuilderUse) {
		allowedInvoke[entrypoint.BuilderAgentName] = struct{}{}
	}

	out := make([]mcplib.Tool, 0, len(tools))
	var hidden []string
	for _, tool := range tools {
		a.toolMu.RLock()
		mapping, mapped := a.toolMap[tool.Name]
		a.toolMu.RUnlock()
		if !mapped {
			out = append(out, tool)
			continue
		}
		if _, ok := allowedInvoke[mapping.agentName]; ok && a.hasRequiredScopes(identity, mapping.agentName) {
			out = append(out, tool)
			continue
		}
		hidden = append(hidden, tool.Name)
	}
	if len(hidden) > 0 {
		a.logger.Debug("MCP tools/list: hidden by RBAC",
			"gatewayID", a.gatewayID(),
			"userID", identity.UserID(),
			"hiddenCount", len(hidden),
			"totalCount", len(tools),
			"hiddenTools", hidden,
			"reason", "missing invoke scope for backing component",
		)
	}
	return out
}

func (a *Adapter) fillScopesFromPlatformWhenLocalSourceEmpty(identity *auth.Subject) {
	if resolver := a.scopeResolverOrNil(); resolver != nil {
		identity.FillScopes(resolver.ResolveScopes(identity.UserID(), identity.Roles()))
	}
}

const logToolCallFiltered = "MCP tools/call: denied by RBAC"

func (a *Adapter) reportFilteredToolCall(ctx context.Context, _ any, method mcplib.MCPMethod, message any, err error) {
	if method != mcplib.MethodToolsCall || !errors.Is(err, server.ErrToolNotFound) {
		return
	}
	request, ok := message.(*mcplib.CallToolRequest)
	if !ok {
		return
	}
	a.toolMu.RLock()
	mapping, mapped := a.toolMap[request.Params.Name]
	a.toolMu.RUnlock()
	if !mapped {
		return
	}
	a.logger.Warn(logToolCallFiltered,
		"gatewayID", a.gatewayID(),
		"userFP", strutil.PIIFingerprint(toolCallAttribution(toolCallCaller(ctx))),
		"agentName", mapping.agentName,
		"reason", "missing_invoke_scope",
	)
	a.core.ReportSubmitRefusal(taxonomy.RejectForbidden)
}

func (a *Adapter) hasRequiredScopes(identity *auth.Subject, agentName string) bool {
	if a.core == nil {
		return true
	}
	card := a.core.AgentRegistry().Get(agentName)
	if card == nil {
		return true
	}
	return coreauth.HasAllScopes(identity.Scopes(), a2a.RequiredScopesFromCard(*card))
}

func (a *Adapter) scopeResolverOrNil() entrypoint.ScopeResolver {
	if a.core == nil {
		return nil
	}
	return a.core.ScopeResolver()
}

func (a *Adapter) authzEnforced() bool {
	return a.core != nil && a.core.AuthzEnforced()
}

func (a *Adapter) agentAuthz() coreauth.AgentAuthorizer {
	if a.core != nil {
		return a.core.AgentAuthz()
	}
	return rbac.NewAgentAuth(nil, a.logger)
}
