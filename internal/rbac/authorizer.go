// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

var errNoIdentity = errors.New("no identity provided")

type ToolAuth struct {
	auditLogger auth.AuditLogger
	logger      *slog.Logger
}

func NewToolAuth(auditLogger auth.AuditLogger, logger *slog.Logger) *ToolAuth {
	return &ToolAuth{
		auditLogger: auditLogger,
		logger:      logger,
	}
}

func (ta *ToolAuth) AuthorizeTool(ctx context.Context, identity *auth.Identity, toolName string, requiredScopes []string) error {
	if identity == nil {
		return errNoIdentity
	}

	allowed := CheckScopes(identity.Scopes, requiredScopes)

	if ta.auditLogger != nil {
		ta.auditLogger.LogToolAccess(ctx, auth.ToolAccessEvent{
			UserID:       identity.UserID,
			CredentialID: identity.CredentialID,
			ToolName:     toolName,
			Allowed:      allowed,
			Scopes:       requiredScopes,
		})
	}

	if !allowed {
		ta.logger.Warn("tool access denied",
			"userFP", strutil.PIIFingerprint(identity.UserID),
			"tool", toolName,
			"requiredScopes", requiredScopes,
		)
		return fmt.Errorf("access denied: insufficient scopes for tool %s", toolName)
	}
	return nil
}

func (ta *ToolAuth) FilterTools(ctx context.Context, identity *auth.Identity, tools []auth.ToolInfo) []auth.ToolInfo {
	if identity == nil {
		return nil
	}

	var allowed []auth.ToolInfo
	for _, tool := range tools {
		if CheckScopes(identity.Scopes, tool.RequiredScopes) {
			allowed = append(allowed, tool)
		} else {
			ta.logger.Debug("tool filtered out",
				"userFP", strutil.PIIFingerprint(identity.UserID),
				"tool", tool.Name,
				"requiredScopes", tool.RequiredScopes,
			)
		}
	}
	return allowed
}

var _ auth.ToolAuthorizer = (*ToolAuth)(nil)

type AgentAuth struct {
	auditLogger auth.AuditLogger
	logger      *slog.Logger
}

func NewAgentAuth(auditLogger auth.AuditLogger, logger *slog.Logger) *AgentAuth {
	return &AgentAuth{
		auditLogger: auditLogger,
		logger:      logger,
	}
}

func (aa *AgentAuth) AuthorizeAgent(ctx context.Context, identity *auth.Identity, agentName string) error {
	var userID, credentialID string
	var scopes []string
	if identity != nil {
		userID = identity.UserID
		credentialID = identity.CredentialID
		scopes = identity.Scopes
	}

	requiredScope := InvokeScopeForInstance(agentName)
	allowed := identity != nil && CheckScopes(scopes, []string{requiredScope})

	if aa.auditLogger != nil {
		aa.auditLogger.LogAgentAccess(ctx, auth.AgentAccessEvent{
			UserID:       userID,
			CredentialID: credentialID,
			AgentName:    agentName,
			Allowed:      allowed,
		})
	}

	if identity == nil {
		aa.logger.Warn("agent delegation denied: no identity",
			"agentName", agentName,
		)
		return errNoIdentity
	}

	if !allowed {
		aa.logger.Warn("agent delegation denied",
			"userFP", strutil.PIIFingerprint(identity.UserID),
			"agentName", agentName,
			"scopeRequired", requiredScope,
		)
		return fmt.Errorf("access denied: insufficient scopes for agent delegation to %s", agentName)
	}
	return nil
}

func (aa *AgentAuth) AuthorizeAgentScopes(ctx context.Context, identity *auth.Identity, requiredScopes []string) error {
	if len(requiredScopes) == 0 {
		return nil
	}
	if identity == nil {
		return errNoIdentity
	}

	allowed := CheckScopes(identity.Scopes, requiredScopes)

	if aa.auditLogger != nil {
		aa.auditLogger.LogAgentAccess(ctx, auth.AgentAccessEvent{
			UserID:       identity.UserID,
			CredentialID: identity.CredentialID,
			AgentName:    "",
			Allowed:      allowed,
		})
	}

	if !allowed {
		aa.logger.Warn("agent scope check denied",
			"userFP", strutil.PIIFingerprint(identity.UserID),
			"requiredScopes", requiredScopes,
		)
		return fmt.Errorf("access denied: insufficient scopes for agent access")
	}
	return nil
}

func (aa *AgentAuth) FilterAgents(_ context.Context, identity *auth.Identity, agentNames []string) []string {
	if identity == nil {
		return nil
	}
	allowed := make([]string, 0, len(agentNames))
	for _, name := range agentNames {
		if CheckScopes(identity.Scopes, []string{InvokeScopeForInstance(name)}) {
			allowed = append(allowed, name)
		}
	}
	return allowed
}

func (aa *AgentAuth) AuthorizeWorkflow(ctx context.Context, identity *auth.Identity, workflowName string) error {
	var userID, credentialID string
	var scopes []string
	if identity != nil {
		userID = identity.UserID
		credentialID = identity.CredentialID
		scopes = identity.Scopes
	}

	requiredScope := WorkflowInvokeScopeForInstance(workflowName)
	allowed := identity != nil && CheckScopes(scopes, []string{requiredScope})

	if aa.auditLogger != nil {
		aa.auditLogger.LogAgentAccess(ctx, auth.AgentAccessEvent{
			UserID:       userID,
			CredentialID: credentialID,
			AgentName:    workflowName,
			Allowed:      allowed,
		})
	}

	if identity == nil {
		aa.logger.Warn("workflow invocation denied: no identity",
			"workflowName", workflowName,
		)
		return errNoIdentity
	}

	if !allowed {
		aa.logger.Warn("workflow invocation denied",
			"userFP", strutil.PIIFingerprint(identity.UserID),
			"workflowName", workflowName,
			"scopeRequired", requiredScope,
		)
		return fmt.Errorf("access denied: insufficient scopes for workflow invocation of %s", workflowName)
	}
	return nil
}

func (aa *AgentAuth) FilterWorkflows(_ context.Context, identity *auth.Identity, workflowNames []string) []string {
	if identity == nil {
		return nil
	}
	allowed := make([]string, 0, len(workflowNames))
	for _, name := range workflowNames {
		if CheckScopes(identity.Scopes, []string{WorkflowInvokeScopeForInstance(name)}) {
			allowed = append(allowed, name)
		}
	}
	return allowed
}

var _ auth.AgentAuthorizer = (*AgentAuth)(nil)
