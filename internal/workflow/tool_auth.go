// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type toolAuthHandlers struct {
	mu       sync.RWMutex
	handlers map[string]tool.AuthResponseHandler
}

func (h *toolAuthHandlers) register(handler tool.AuthResponseHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.handlers == nil {
		h.handlers = make(map[string]tool.AuthResponseHandler)
	}
	h.handlers[handler.CredentialKey()] = handler
}

func (h *toolAuthHandlers) deregister(credentialKey string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.handlers, credentialKey)
}

func (h *toolAuthHandlers) get(credentialKey string) (tool.AuthResponseHandler, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	handler, ok := h.handlers[credentialKey]
	return handler, ok
}

func (h *toolAuthHandlers) all() []tool.AuthResponseHandler {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]tool.AuthResponseHandler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		out = append(out, handler)
	}
	return out
}

func (w *WorkflowInstance) credentialService() auth.CredentialService {
	return w.svc.CredentialService()
}

func (w *WorkflowInstance) ListToolAuth(ctx context.Context, userID string) []agent.ToolAuthInfo {
	return agent.ListToolAuthInfo(ctx, w.toolAuth.all(), w.cfg.Name, userID, w.credentialService())
}

func (w *WorkflowInstance) BeginToolAuth(ctx context.Context, credentialKey, userID string) (*a2a.AuthenticationRequiredData, error) {
	handler, ok := w.toolAuth.get(credentialKey)
	if !ok {
		return nil, fmt.Errorf("credential key %q: %w", credentialKey, control.ErrResourceNotFound)
	}
	return agent.BeginToolAuthFlow(ctx, handler, w.cfg.Name, userID, w.credentialService())
}

func (w *WorkflowInstance) DisconnectToolAuth(ctx context.Context, credentialKey, userID string) error {
	handler, ok := w.toolAuth.get(credentialKey)
	if !ok {
		return fmt.Errorf("credential key %q: %w", credentialKey, control.ErrResourceNotFound)
	}
	return agent.DisconnectToolAuthCredential(ctx, handler, w.cfg.Name, userID, w.credentialService())
}

func (w *WorkflowInstance) handleAuthResponse(ctx context.Context, taskID string, resp *a2a.AuthResponseData, identity *auth.Identity, props a2a.UserProperties, logger *slog.Logger) {
	logger = logger.With("credentialKey", resp.CredentialKey)
	reply := agent.CredentialSavedResponse(taskID, w.cfg.Name)
	handler, ok := w.toolAuth.get(resp.CredentialKey)
	switch {
	case identity != nil && identity.UserID != props.UserID:
		logger.Warn("rejecting auth response for a user other than the signed one")
		reply = a2a.NewErrorResponse(taskID, -32600, "auth response user does not match the signed identity")
	case !ok:
		logger.Warn("auth response for a credential no workflow tool declares")
		reply = a2a.NewErrorResponse(taskID, -32600, fmt.Sprintf("no auth handler for credential key %q", resp.CredentialKey))
	default:
		toolCtx := &tool.Context{UserID: props.UserID, AgentName: w.cfg.Name, CredentialService: w.credentialService()}
		if err := handler.HandleAuthResponse(ctx, resp, toolCtx); err != nil {
			logger.Warn("workflow tool credential exchange failed", toolauth.RefreshFailureLogAttrs(err)...)
			reply = a2a.NewErrorResponse(taskID, -32603, "authorization could not be completed; start it again")
		} else {
			logger.Info("workflow tool credential saved")
		}
	}
	w.publishReply(ctx, props, reply, logger)
}

func (w *WorkflowInstance) publishReply(ctx context.Context, props a2a.UserProperties, reply *a2a.Response, logger *slog.Logger) {
	if props.ReplyTo == "" {
		return
	}
	payload, err := json.Marshal(reply)
	if err != nil {
		logger.Error("failed to marshal reply", "error", err)
		return
	}
	up := map[string]string{}
	a2a.AddTraceID(up, props.TraceID)
	if err := w.svc.Broker().Publish(ctx, props.ReplyTo, &broker.Message{Payload: payload, UserProperties: up}); err != nil {
		logger.Warn("failed to publish reply", "error", err, "replyTo", props.ReplyTo)
	}
}
