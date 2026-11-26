// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	pendingAuthMarkerKey      = "_pending_auth_marker"
	pendingAuthFuncCallID     = "_pending_auth_function_call_id"
	pendingAuthOriginalArgs   = "_pending_auth_original_args"
	pendingAuthCredentialKey  = "_pending_auth_credential_key"
	pendingAuthFollowerKey    = "_pending_auth_follower"
	pendingAuthToolMessageKey = "_pending_auth_tool_message"
)

func NewPendingAuthResult(funcCallID string, originalArgs map[string]any, credentialKey string) *tool.Result {
	return newPendingAuth(funcCallID, originalArgs, credentialKey, false)
}

func NewPendingAuthFollowerResult(funcCallID string, originalArgs map[string]any, credentialKey string) *tool.Result {
	return newPendingAuth(funcCallID, originalArgs, credentialKey, true)
}

func newPendingAuth(funcCallID string, originalArgs map[string]any, credentialKey string, follower bool) *tool.Result {
	argsCopy := make(map[string]any, len(originalArgs))
	for k, v := range originalArgs {
		argsCopy[k] = v
	}
	return &tool.Result{
		Status:  tool.StatusPending,
		Message: "Pending user authorization.",
		Data: map[string]any{
			pendingAuthMarkerKey:     true,
			pendingAuthFuncCallID:    funcCallID,
			pendingAuthOriginalArgs:  argsCopy,
			pendingAuthCredentialKey: credentialKey,
			pendingAuthFollowerKey:   follower,
		},
	}
}

func NewAuthGaveUpResult(toolDisplayName, toolMessage string) *tool.Result {
	msg := fmt.Sprintf(taxonomy.AuthorizationGaveUpFormat, toolDisplayName)
	if toolMessage != "" {
		msg += " " + toolMessage
	}
	return tool.Error(msg, tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassUserInputUnanswered)))
}

func SignInProviderUnavailableMessage(displayName string) string {
	return fmt.Sprintf("The sign-in provider for %q is unavailable right now, so its stored sign-in could not be renewed. Do not retry it in this turn; tell the user it is temporarily unavailable and to try again in a few minutes. There is no need to sign in again.", displayName)
}

func NewSignInProviderUnavailableResult(toolDisplayName string, err error) *tool.Result {
	status := http.StatusServiceUnavailable
	if refreshRateLimited(err) {
		status = http.StatusTooManyRequests
	}
	return tool.Error(SignInProviderUnavailableMessage(toolDisplayName), tool.WithErrorCode(tool.HTTPFailureCode(status)))
}

func NoDelegationMessage(toolDisplayName string) string {
	return fmt.Sprintf("%q needs your personal access, which isn't available in a shared channel. DM me directly and I'll do it under your account.", toolDisplayName)
}

func NewNoDelegationResult(toolDisplayName string) *tool.Result {
	return tool.Error(NoDelegationMessage(toolDisplayName),
		tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassUserInputUnavailable)))
}

func PreAuthShortCircuit(toolCtx *tool.Context, toolDisplayName string, logger *slog.Logger) *tool.Result {
	if toolCtx == nil || !DelegationRefused(toolCtx.UserID, toolCtx.Trigger) {
		return nil
	}
	if logger != nil {
		logger.Info("no-delegation principal; skipping auth prompt",
			"userID", toolCtx.UserID, "tool", toolDisplayName)
	}
	return NewNoDelegationResult(toolDisplayName)
}

func DelegationRefused(userID, trigger string) bool {
	return trigger != a2a.TriggerEval && rbac.IsNonDelegatablePrincipal(userID)
}

func AuthFlowClassifyOnly(userID, trigger string) bool {
	return trigger == a2a.TriggerEval && rbac.IsNonDelegatablePrincipal(userID)
}

func IsPendingAuthResult(r *tool.Result) bool {
	if r == nil || r.Status != tool.StatusPending || r.Data == nil {
		return false
	}
	marker, ok := r.Data[pendingAuthMarkerKey].(bool)
	return ok && marker
}

func PendingAuthFunctionCallID(r *tool.Result) string {
	if !IsPendingAuthResult(r) {
		return ""
	}
	id, _ := r.Data[pendingAuthFuncCallID].(string)
	return id
}

func PendingAuthOriginalArgs(r *tool.Result) map[string]any {
	out := map[string]any{}
	if !IsPendingAuthResult(r) {
		return out
	}
	src, ok := r.Data[pendingAuthOriginalArgs].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range src {
		out[k] = v
	}
	return out
}

func PendingAuthCredentialKey(r *tool.Result) string {
	if !IsPendingAuthResult(r) {
		return ""
	}
	k, _ := r.Data[pendingAuthCredentialKey].(string)
	return k
}

func WithToolMessage(r *tool.Result, msg string) *tool.Result {
	if msg == "" || !IsPendingAuthResult(r) {
		return r
	}
	r.Data[pendingAuthToolMessageKey] = msg
	return r
}

func PendingAuthToolMessage(r *tool.Result) string {
	if !IsPendingAuthResult(r) {
		return ""
	}
	m, _ := r.Data[pendingAuthToolMessageKey].(string)
	return m
}

func IsPendingAuthFollower(r *tool.Result) bool {
	if !IsPendingAuthResult(r) {
		return false
	}
	f, _ := r.Data[pendingAuthFollowerKey].(bool)
	return f
}
