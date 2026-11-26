// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type toolAuthManager interface {
	ListToolAuth(ctx context.Context, userID string) []agent.ToolAuthInfo
	BeginToolAuth(ctx context.Context, credentialKey, userID string) (*a2a.AuthenticationRequiredData, error)
	DisconnectToolAuth(ctx context.Context, credentialKey, userID string) error
}

type toolAuthQueryRequest struct {
	UserID string `json:"userId"`
}

type toolAuthCredentialRequest struct {
	CredentialKey string `json:"credentialKey"`
	UserID        string `json:"userId"`
}

func (e *Executor) authorizeToolAuth(ctx context.Context, instanceName, action string, identity *auth.Identity) error {
	resource := "awe/instances/" + instanceName + "/toolAuth/" + action

	allowed := identity == nil || len(identity.Scopes) == 0 ||
		rbac.CheckScopes(identity.Scopes, []string{rbac.ScopeEvaluationInvoke})

	userID := ""
	if identity != nil {
		userID = identity.UserID
	}
	e.svc.AuditLogger().LogControlAccess(ctx, auth.ControlAccessEvent{
		UserID:  userID,
		Method:  "post",
		AppName: resource,
		Allowed: allowed,
	})
	if !allowed {
		return fmt.Errorf("insufficient permissions: requires %s", rbac.ScopeEvaluationInvoke)
	}
	return nil
}

func requireSystemPrincipal(userID string) error {
	if userID == "" {
		return fmt.Errorf("'userId' field required")
	}
	if !rbac.IsSystemPrincipal(userID) {
		return fmt.Errorf("'userId' must name a system principal")
	}
	return nil
}

func decodeToolAuthCredentialRequest(body json.RawMessage) (*toolAuthCredentialRequest, error) {
	var req toolAuthCredentialRequest
	if err := decodeStrictBody(body, &req); err != nil {
		return nil, err
	}
	if err := requireSystemPrincipal(req.UserID); err != nil {
		return nil, err
	}
	if req.CredentialKey == "" {
		return nil, fmt.Errorf("'credentialKey' field required")
	}
	return &req, nil
}

func (e *Executor) toolAuthManagerFor(name string) (toolAuthManager, error) {
	e.mu.RLock()
	mi := e.findInstance(name)
	e.mu.RUnlock()
	if mi == nil {
		return nil, fmt.Errorf("instance %q: %w", name, control.ErrResourceNotFound)
	}
	mgr, ok := mi.inst.(toolAuthManager)
	if !ok {
		return nil, fmt.Errorf("instance %q does not support tool auth management", name)
	}
	if mi.getStatus() != StatusRunning {
		return nil, fmt.Errorf("instance %q %s: %w", name, control.InstanceNotRunning, control.ErrResourceNotFound)
	}
	return mgr, nil
}

func (e *Executor) handleToolAuthQuery(ctx context.Context, name string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if err := e.authorizeToolAuth(ctx, name, "query", identity); err != nil {
		return nil, err
	}
	mgr, err := e.toolAuthManagerFor(name)
	if err != nil {
		return nil, err
	}
	var req toolAuthQueryRequest
	if err := decodeStrictBody(body, &req); err != nil {
		return nil, err
	}
	if err := requireSystemPrincipal(req.UserID); err != nil {
		return nil, err
	}
	return mgr.ListToolAuth(ctx, req.UserID), nil
}

func (e *Executor) handleToolAuthBegin(ctx context.Context, name string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if err := e.authorizeToolAuth(ctx, name, "begin", identity); err != nil {
		return nil, err
	}
	mgr, err := e.toolAuthManagerFor(name)
	if err != nil {
		return nil, err
	}
	req, err := decodeToolAuthCredentialRequest(body)
	if err != nil {
		return nil, err
	}

	e.logger.Info("beginning delegated tool auth flow",
		"instance", name,
		"credentialKey", req.CredentialKey,
		"targetUserID", req.UserID,
	)
	return mgr.BeginToolAuth(ctx, req.CredentialKey, req.UserID)
}

func (e *Executor) handleToolAuthDisconnect(ctx context.Context, name string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if err := e.authorizeToolAuth(ctx, name, "disconnect", identity); err != nil {
		return nil, err
	}
	mgr, err := e.toolAuthManagerFor(name)
	if err != nil {
		return nil, err
	}
	req, err := decodeToolAuthCredentialRequest(body)
	if err != nil {
		return nil, err
	}
	return nil, mgr.DisconnectToolAuth(ctx, req.CredentialKey, req.UserID)
}
