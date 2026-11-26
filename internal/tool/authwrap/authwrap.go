// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package authwrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type WrappedTool struct {
	inner       tool.BuiltinTool
	authHandler *toolauth.AuthHandler
	logger      *slog.Logger
}

var _ tool.BuiltinTool = (*WrappedTool)(nil)

func Wrap(t tool.BuiltinTool, authHandler *toolauth.AuthHandler, logger *slog.Logger) tool.BuiltinTool {
	if authHandler == nil {
		return t
	}
	return &WrappedTool{inner: t, authHandler: authHandler, logger: logger}
}

func (w *WrappedTool) AuthHandler() *toolauth.AuthHandler {
	return w.authHandler
}

func (w *WrappedTool) Name() string { return w.inner.Name() }

func (w *WrappedTool) Description() string { return w.inner.Description() }

func (w *WrappedTool) Parameters() *tool.JSONSchema { return w.inner.Parameters() }

func (w *WrappedTool) Instructions() string { return w.inner.Instructions() }

func (w *WrappedTool) IsLongRunning() bool { return w.inner.IsLongRunning() }

func (w *WrappedTool) RequiredScopes() []string { return tool.RequiredScopesOf(w.inner) }

func (w *WrappedTool) ResultSchema() *tool.JSONSchema { return tool.ResultSchemaOf(w.inner) }

func (w *WrappedTool) AuthoredSchemas() bool { return tool.HasAuthoredSchemas(w.inner) }

var _ tool.AuthoredSchemaTool = (*WrappedTool)(nil)

func (w *WrappedTool) ArtifactNotFoundHint() string {
	return tool.ArtifactNotFoundHintOf(w.inner)
}

var _ tool.ArtifactNotFoundHinter = (*WrappedTool)(nil)

func (w *WrappedTool) EffectClass() tool.EffectClass { return tool.EffectClassOf(w.inner) }

func (w *WrappedTool) CallEffectClass(args map[string]any) tool.EffectClass {
	return tool.CallEffectClassOf(w.inner, args)
}

var (
	_ tool.EffectClassifier     = (*WrappedTool)(nil)
	_ tool.CallEffectClassifier = (*WrappedTool)(nil)
)

func (w *WrappedTool) ArgumentErrorHint(args map[string]any) string {
	return tool.ArgumentErrorHintOf(w.inner, args)
}

var _ tool.ArgumentErrorHinter = (*WrappedTool)(nil)

func (w *WrappedTool) RawStringArgs() []string { return w.inner.RawStringArgs() }

func (w *WrappedTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return w.inner.ArtifactParams()
}

func (w *WrappedTool) ContextParam() string { return w.inner.ContextParam() }

func (w *WrappedTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	return w.execute(ctx, args, toolCtx, false, tool.CallEffectClassOf(w.inner, args))
}

func (w *WrappedTool) ExecuteResolved(ctx context.Context, args map[string]any, toolCtx *tool.Context, effect tool.EffectClass) (*tool.Result, error) {
	return w.execute(ctx, args, toolCtx, false, effect)
}

var _ tool.ResolvedEffectExecutor = (*WrappedTool)(nil)

func (w *WrappedTool) execute(ctx context.Context, args map[string]any, toolCtx *tool.Context, retried bool, effect tool.EffectClass) (*tool.Result, error) {
	cfg := w.authHandler.Config()
	if cfg == nil {
		return tool.Error("auth configuration is nil"), nil
	}
	var sentAccessToken string
	switch cfg.Type {
	case "oauth2":
		token, err := w.authHandler.GetOAuthToken(ctx, toolCtx)
		if errors.Is(err, toolauth.ErrAuthRequired) {
			return w.triggerAuthFlow(ctx, toolCtx, args, "")
		}
		if errors.Is(err, toolauth.ErrTransientRefresh) {
			return toolauth.NewSignInProviderUnavailableResult(w.Name(), err), nil
		}
		if err != nil {
			return tool.Error("authentication failed"), nil
		}
		if token != nil {
			toolCtx.OAuthToken = token
			sentAccessToken = token.AccessToken
		}
	case "bearer", "basic":
		headers, err := w.authHandler.GetAuthHeaders(ctx, toolCtx)
		if err != nil {
			return tool.Error("authentication failed"), nil
		}
		toolCtx.AuthHeaders = headers
	}

	result, execErr := tool.ExecuteWithEffect(ctx, w.inner, args, toolCtx, effect)
	if execErr != nil {
		return result, execErr
	}

	if result != nil && result.ErrorCode == tool.ErrorCodeAuthRequired {
		return w.afterAuthRequired(ctx, args, toolCtx, retried, sentAccessToken, effect)
	}

	return result, nil
}

func (w *WrappedTool) afterAuthRequired(ctx context.Context, args map[string]any, toolCtx *tool.Context, retried bool, sentAccessToken string, effect tool.EffectClass) (*tool.Result, error) {
	if !retried && w.authHandler.StoredTokenReplaces(ctx, toolCtx, sentAccessToken) {
		return w.execute(ctx, args, toolCtx, true, effect)
	}
	return w.triggerAuthFlow(ctx, toolCtx, args, sentAccessToken)
}

func (w *WrappedTool) triggerAuthFlow(ctx context.Context, toolCtx *tool.Context, args map[string]any, rejectedAccessToken string) (*tool.Result, error) {
	if r := toolauth.PreAuthShortCircuit(toolCtx, w.Name(), w.logger); r != nil {
		return r, nil
	}
	credKey := w.authHandler.CredentialKey()

	if toolCtx.AuthGaveUp != nil && toolCtx.AuthGaveUp(toolCtx.UserID, toolCtx.TraceID, credKey) {
		return toolauth.NewAuthGaveUpResult(w.Name(), ""), nil
	}

	if toolCtx.BeginAuthFlow != nil && !toolCtx.BeginAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey) {
		return toolauth.NewPendingAuthFollowerResult(toolCtx.ToolCallID, args, credKey), nil
	}

	leaderActive := true
	defer func() {
		if leaderActive && toolCtx.EndAuthFlow != nil {
			toolCtx.EndAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey)
		}
	}()

	_ = w.authHandler.ClearCredentialForReauth(ctx, toolCtx, rejectedAccessToken)

	signal, err := w.authHandler.BuildAuthSignal(ctx, toolCtx)
	if err != nil {
		return tool.Error(fmt.Sprintf("failed to build auth signal: %v", err)), nil
	}

	if toolCtx.SendSignal == nil {
		return tool.AuthPromptChannelMissing(), nil
	}
	if err := toolCtx.SendSignal(signal); err != nil {
		return tool.AuthPromptUndelivered(err), nil
	}

	leaderActive = false
	return toolauth.NewPendingAuthResult(toolCtx.ToolCallID, args, credKey), nil
}
