// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ToolAuthInfo struct {
	CredentialKey string    `json:"credentialKey"`
	DisplayName   string    `json:"displayName,omitempty"`
	AuthType      string    `json:"authType,omitempty"`
	Interactive   bool      `json:"interactive"`
	Status        string    `json:"status"`
	ExpiresAt     time.Time `json:"expiresAt,omitzero"`
	GrantRef      string    `json:"grantRef,omitempty"`
}

const (
	toolAuthStatusConnected    = "connected"
	toolAuthStatusNotConnected = "not_connected"

	preAuthClientID = "platform-toolauth"
)

func (a *Agent) ListToolAuth(ctx context.Context, userID string) []ToolAuthInfo {
	a.authHandlersMu.RLock()
	handlers := make([]tool.AuthResponseHandler, 0, len(a.authHandlers))
	for _, h := range a.authHandlers {
		handlers = append(handlers, h)
	}
	a.authHandlersMu.RUnlock()
	return ListToolAuthInfo(ctx, handlers, a.cfg.Name, userID, a.svc.CredentialService())
}

func (a *Agent) BeginToolAuth(ctx context.Context, credentialKey, userID string) (*a2a.AuthenticationRequiredData, error) {
	a.authHandlersMu.RLock()
	handler, ok := a.authHandlers[credentialKey]
	a.authHandlersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("credential key %q: %w", credentialKey, control.ErrResourceNotFound)
	}
	return BeginToolAuthFlow(ctx, handler, a.cfg.Name, userID, a.svc.CredentialService())
}

func (a *Agent) DisconnectToolAuth(ctx context.Context, credentialKey, userID string) error {
	a.authHandlersMu.RLock()
	handler, ok := a.authHandlers[credentialKey]
	a.authHandlersMu.RUnlock()
	if !ok {
		return fmt.Errorf("credential key %q: %w", credentialKey, control.ErrResourceNotFound)
	}
	return DisconnectToolAuthCredential(ctx, handler, a.cfg.Name, userID, a.svc.CredentialService())
}

func ListToolAuthInfo(ctx context.Context, handlers []tool.AuthResponseHandler, component, userID string, credentials auth.CredentialService) []ToolAuthInfo {
	toolCtx := &tool.Context{UserID: userID, AgentName: component, CredentialService: credentials}
	infos := make([]ToolAuthInfo, 0, len(handlers))
	for _, handler := range handlers {
		info := ToolAuthInfo{CredentialKey: handler.CredentialKey(), Status: toolAuthStatusNotConnected}
		if d, ok := handler.(tool.UserAuthDescriptor); ok {
			info.AuthType = d.AuthType()
			info.DisplayName = d.AuthDisplayName()
			info.Interactive = d.SupportsUserAuth()
		}
		if checker, ok := handler.(tool.StoredCredentialChecker); ok {
			if stored := checker.CheckStoredCredential(ctx, toolCtx); stored.Connected {
				info.Status = toolAuthStatusConnected
				info.ExpiresAt = stored.ExpiresAt
				info.GrantRef = stored.GrantRef
			}
		}
		infos = append(infos, info)
	}
	slices.SortFunc(infos, func(x, y ToolAuthInfo) int { return cmp.Compare(x.CredentialKey, y.CredentialKey) })
	return infos
}

func BeginToolAuthFlow(ctx context.Context, handler tool.AuthResponseHandler, component, userID string, credentials auth.CredentialService) (*a2a.AuthenticationRequiredData, error) {
	credentialKey := handler.CredentialKey()
	if d, isDescriptor := handler.(tool.UserAuthDescriptor); !isDescriptor || !d.SupportsUserAuth() {
		return nil, fmt.Errorf("credential key %q does not support interactive auth", credentialKey)
	}
	builder, ok := handler.(tool.AuthSignalBuilder)
	if !ok {
		return nil, fmt.Errorf("credential key %q does not support interactive auth", credentialKey)
	}
	id := "preauth-" + idgen.New()
	return builder.BuildAuthSignal(ctx, &tool.Context{
		UserID:            userID,
		AgentName:         component,
		ToolCallID:        id,
		ParentTaskID:      id,
		TraceID:           idgen.New(),
		ClientID:          preAuthClientID,
		CredentialService: credentials,
	})
}

func DisconnectToolAuthCredential(ctx context.Context, handler tool.AuthResponseHandler, component, userID string, credentials auth.CredentialService) error {
	clearer, ok := handler.(tool.CredentialClearer)
	if !ok {
		return fmt.Errorf("credential key %q does not support disconnect", handler.CredentialKey())
	}
	return clearer.ClearCredential(ctx, &tool.Context{UserID: userID, AgentName: component, CredentialService: credentials})
}
