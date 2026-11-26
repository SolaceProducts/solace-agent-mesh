// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var ErrAuthRequired = toolauth.ErrAuthRequired

type AuthHandler struct {
	*toolauth.AuthHandler
}

var _ tool.AuthResponseHandler = (*AuthHandler)(nil)

func NewAuthHandler(cfg *MCPAuthConfig, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{
		AuthHandler: toolauth.NewAuthHandler(cfg, logger),
	}
}

func (h *AuthHandler) GetAuthHeaders(ctx context.Context, toolCtx *tool.Context) (http.Header, error) {
	return h.AuthHandler.GetAuthHeaders(ctx, toolCtx)
}

func (h *AuthHandler) BuildAuthSignal(ctx context.Context, toolCtx *tool.Context) (*a2a.AuthenticationRequiredData, error) {
	return h.AuthHandler.BuildAuthSignal(ctx, toolCtx)
}

func (h *AuthHandler) HandleAuthResponse(ctx context.Context, resp *a2a.AuthResponseData, toolCtx *tool.Context) error {
	return h.AuthHandler.HandleAuthResponse(ctx, resp, toolCtx)
}

const substringAuthCheckMaxTextLen = 2000

func IsAuthError(result *rawCallToolResult) bool {
	if result == nil {
		return false
	}

	for _, raw := range result.Content {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			if result.IsError && len(raw) <= substringAuthCheckMaxTextLen {
				if isAuthErrorText(strings.ToLower(string(raw))) {
					return true
				}
			}
			continue
		}

		if hasStructuredAuthCode(item) {
			return true
		}

		text, _ := item["text"].(string)

		if text != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(text), &parsed); err == nil {
				if hasStructuredAuthCode(parsed) {
					return true
				}
			}
		}

		if result.IsError && len(text) <= substringAuthCheckMaxTextLen {
			if isAuthErrorText(strings.ToLower(text)) {
				return true
			}
		}
	}

	return false
}

func IsScopeError(result *rawCallToolResult) bool {
	if result == nil || !result.IsError {
		return false
	}

	for _, raw := range result.Content {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			if len(raw) <= substringAuthCheckMaxTextLen && isScopeErrorText(strings.ToLower(string(raw))) {
				return true
			}
			continue
		}

		if hasScopeErrorField(item) {
			return true
		}

		text, _ := item["text"].(string)
		if text != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(text), &parsed); err == nil && hasScopeErrorField(parsed) {
				return true
			}
			if len(text) <= substringAuthCheckMaxTextLen && isScopeErrorText(strings.ToLower(text)) {
				return true
			}
		}
	}

	return false
}

func hasScopeErrorField(m map[string]any) bool {
	for _, field := range []string{"error", "code"} {
		if s, ok := m[field].(string); ok && isScopeErrorText(strings.ToLower(s)) {
			return true
		}
	}
	return false
}

func isScopeErrorText(lower string) bool {
	return strings.Contains(lower, "missing_scope") ||
		strings.Contains(lower, "missing scope") ||
		strings.Contains(lower, "insufficient_scope") ||
		strings.Contains(lower, "insufficient scope")
}

func hasStructuredAuthCode(m map[string]any) bool {
	for _, field := range []string{"code", "status"} {
		switch v := m[field].(type) {
		case float64:
			if v == 401 || v == 403 {
				return true
			}
		case string:
			if v == "401" || v == "403" {
				return true
			}
		}
	}
	return false
}

func isAuthErrorText(lower string) bool {
	return (strings.Contains(lower, "401") && strings.Contains(lower, "unauthorized")) ||
		(strings.Contains(lower, "403") && strings.Contains(lower, "forbidden"))
}
