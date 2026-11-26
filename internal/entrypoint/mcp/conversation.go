// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const sessionIDArg = "sessionId"

const (
	sessionIDArgDescription = "Continue an earlier conversation by passing the sessionId a previous result returned. Omit it to start a new conversation."
	unknownSessionIDMessage = "unknown sessionId; omit it to start a new conversation"
	logSessionIDRefused     = "mcp sessionId refused"
)

var errUnknownSessionID = errors.New("unknown sessionId")

var errSessionIDNotOwned = fmt.Errorf("%w: owned by another user", errUnknownSessionID)

type conversation struct {
	sessionID    string
	mcpSessionID string
	handle       string
}

func (a *Adapter) resolveConversation(ctx context.Context, request mcp.CallToolRequest, agentName, userID string) (conversation, error) {
	if server.IsModernRequest(ctx) {
		return a.resolveModernConversation(ctx, request.GetString(sessionIDArg, ""), agentName, userID)
	}
	sess := server.ClientSessionFromContext(ctx)
	if sess == nil || sess.SessionID() == "" {
		return conversation{sessionID: idgen.New()}, nil
	}
	sessionID, err := a.resolveSessionID(ctx, sess.SessionID(), agentName, userID)
	if err != nil {
		return conversation{}, fmt.Errorf("resolve session id: %w", err)
	}
	return conversation{sessionID: sessionID, mcpSessionID: sess.SessionID()}, nil
}

func (a *Adapter) resolveModernConversation(ctx context.Context, handle, agentName, userID string) (conversation, error) {
	if handle != "" {
		sessionID, err := a.ownedConversation(ctx, handle, userID)
		if err != nil {
			return conversation{}, err
		}
		return conversation{sessionID: sessionID, handle: handle}, nil
	}
	if a.sessKeys == nil || a.sessions == nil {
		return conversation{sessionID: idgen.New()}, nil
	}
	handle = idgen.New()
	sessionID, err := a.resolveSessionID(ctx, handle, agentName, userID)
	if err != nil {
		return conversation{}, fmt.Errorf("start session: %w", err)
	}
	return conversation{sessionID: sessionID, handle: handle}, nil
}

func (a *Adapter) ownedConversation(ctx context.Context, handle, userID string) (string, error) {
	parsed, err := uuid.Parse(handle)
	if err != nil {
		a.logger.Debug(logSessionIDRefused, "userID", userID, "reason", "malformed")
		return "", errUnknownSessionID
	}
	if a.sessKeys == nil || a.sessions == nil {
		a.logger.Debug(logSessionIDRefused, "userID", userID, "reason", "no_store")
		return "", errUnknownSessionID
	}
	key := parsed.String()
	mapped, err := a.sessKeys.GetByGatewayAndExternalKeys(ctx, store.GatewayTypeMCP, []string{key})
	if err != nil {
		return "", fmt.Errorf("look up session: %w", err)
	}
	sessionID, ok := mapped[key]
	if !ok {
		a.logger.Debug(logSessionIDRefused, "userID", userID, "reason", "not_found")
		return "", errUnknownSessionID
	}
	sess, err := a.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("look up session: %w", err)
	}
	if sess == nil {
		a.logger.Debug(logSessionIDRefused, "userID", userID, "sessionID", sessionID, "reason", "deleted")
		return "", errUnknownSessionID
	}
	if sess.UserID != userID {
		a.logger.Warn(logSessionIDRefused, "userFP", strutil.PIIFingerprint(userID), "sessionID", sessionID, "reason", "not_owner")
		return "", errSessionIDNotOwned
	}
	return sessionID, nil
}

func offerSessionIDArg(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	if !server.IsModernRequest(ctx) {
		return tools
	}
	offered := make([]mcp.Tool, len(tools))
	for i, t := range tools {
		props := maps.Clone(t.InputSchema.Properties)
		if props == nil {
			props = map[string]any{}
		}
		props[sessionIDArg] = map[string]any{
			"type":        "string",
			"description": sessionIDArgDescription,
		}
		t.InputSchema.Properties = props
		offered[i] = t
	}
	return offered
}

func withSessionID(result *mcp.CallToolResult, handle string) *mcp.CallToolResult {
	if handle == "" {
		return result
	}
	result.Content = append(result.Content, mcp.NewTextContent(sessionIDArg+": "+handle))
	return result
}

func (a *Adapter) readerUserID(ctx context.Context) string {
	if id, ok := auth.SubjectFromContext(ctx); ok && id != nil && id.UserID() != "" {
		return id.UserID()
	}
	return a.defaultIdentity().UserID()
}
