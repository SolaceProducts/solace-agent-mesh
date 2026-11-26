// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func jsonMarshalToken(tok *toolauth.OAuthToken) (string, error) {
	if tok == nil {
		return "", nil
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return "", fmt.Errorf("marshal token: %w", err)
	}
	return string(b), nil
}

func jsonUnmarshalToken(raw string, tok *toolauth.OAuthToken) error {
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), tok); err != nil {
		return fmt.Errorf("unmarshal token: %w", err)
	}
	return nil
}

type openAPIAuthHandler struct {
	authType AuthType

	inner *toolauth.AuthHandler

	tokenURL                string
	clientID                string
	clientSecret            string
	scopes                  []string
	tokenEndpointAuthMethod string
	serviceAccountJSON      string

	baseTransport http.RoundTripper

	credentialKey string

	logger *slog.Logger
}

var (
	_ tool.AuthResponseHandler     = (*openAPIAuthHandler)(nil)
	_ tool.AuthSignalBuilder       = (*openAPIAuthHandler)(nil)
	_ tool.CredentialClearer       = (*openAPIAuthHandler)(nil)
	_ tool.StoredCredentialChecker = (*openAPIAuthHandler)(nil)
	_ tool.UserAuthDescriptor      = (*openAPIAuthHandler)(nil)
)

func (h *openAPIAuthHandler) CredentialKey() string {
	return h.credentialKey
}

func (h *openAPIAuthHandler) HandleAuthResponse(
	ctx context.Context,
	resp *a2a.AuthResponseData,
	toolCtx *tool.Context,
) error {
	if h.inner == nil {
		return nil
	}
	return h.inner.HandleAuthResponse(ctx, resp, toolCtx)
}

func (h *openAPIAuthHandler) AnswersPendingPrompt(ctx context.Context, resp *a2a.AuthResponseData, toolCtx *tool.Context) (matches, known bool) {
	if h.inner == nil {
		return false, false
	}
	return h.inner.AnswersPendingPrompt(ctx, resp, toolCtx)
}

func (h *openAPIAuthHandler) ClearCredentialForReauth(ctx context.Context, toolCtx *tool.Context, rejectedAccessToken string) error {
	if h.inner == nil {
		return nil
	}
	return h.inner.ClearCredentialForReauth(ctx, toolCtx, rejectedAccessToken)
}

func (h *openAPIAuthHandler) ClearCredential(ctx context.Context, toolCtx *tool.Context) error {
	if h.inner == nil {
		return nil
	}
	return h.inner.ClearCredential(ctx, toolCtx)
}

func (h *openAPIAuthHandler) BuildAuthSignal(
	ctx context.Context,
	toolCtx *tool.Context,
) (*a2a.AuthenticationRequiredData, error) {
	if h.inner == nil {
		return nil, fmt.Errorf(
			"openapi auth handler: authentication type %q does not support user-redirect flow",
			h.authType,
		)
	}
	return h.inner.BuildAuthSignal(ctx, toolCtx)
}

func (h *openAPIAuthHandler) CheckStoredCredential(ctx context.Context, toolCtx *tool.Context) tool.StoredCredential {
	if h.inner == nil {
		return tool.StoredCredential{}
	}
	return h.inner.CheckStoredCredential(ctx, toolCtx)
}

func (h *openAPIAuthHandler) SupportsUserAuth() bool {
	return h.inner != nil
}

func (h *openAPIAuthHandler) AuthType() string {
	return string(h.authType)
}

func (h *openAPIAuthHandler) AuthDisplayName() string {
	if h.inner == nil {
		return ""
	}
	return h.inner.AuthDisplayName()
}

func (h *openAPIAuthHandler) GetAuthHeaders(
	ctx context.Context,
	toolCtx *tool.Context,
) (http.Header, error) {
	switch h.authType {
	case AuthTypeOAuth2AuthorizationCode:
		if h.inner == nil {
			return nil, fmt.Errorf("openapi auth handler: authorization_code path missing inner handler")
		}
		return h.inner.GetAuthHeaders(ctx, toolCtx)

	case AuthTypeOAuth2ClientCredentials:
		return h.getClientCredentialsHeaders(ctx, toolCtx)

	case AuthTypeServiceAccount:
		return h.getServiceAccountHeaders(ctx, toolCtx)
	}
	return nil, nil
}

func (h *openAPIAuthHandler) getClientCredentialsHeaders(
	ctx context.Context,
	toolCtx *tool.Context,
) (http.Header, error) {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return h.exchangeAndBuildHeader(ctx, toolCtx, h.exchangeClientCredentials)
	}

	scope := h.agentScope(toolCtx)
	token, err := loadToken(ctx, toolCtx, scope, h.credentialKey)
	if err != nil {
		h.logger.Warn("failed to load cached client_credentials token, re-exchanging", "error", err)
		token = nil
	}
	if token != nil && !token.IsExpired() {
		return bearerHeader(token), nil
	}

	return h.exchangeAndBuildHeader(ctx, toolCtx, h.exchangeClientCredentials)
}

func (h *openAPIAuthHandler) getServiceAccountHeaders(
	ctx context.Context,
	toolCtx *tool.Context,
) (http.Header, error) {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return h.exchangeAndBuildHeader(ctx, toolCtx, h.exchangeServiceAccount)
	}

	scope := h.agentScope(toolCtx)
	token, err := loadToken(ctx, toolCtx, scope, h.credentialKey)
	if err != nil {
		h.logger.Warn("failed to load cached serviceaccount token, re-exchanging", "error", err)
		token = nil
	}
	if token != nil && !token.IsExpired() {
		return bearerHeader(token), nil
	}

	return h.exchangeAndBuildHeader(ctx, toolCtx, h.exchangeServiceAccount)
}

func (h *openAPIAuthHandler) exchangeAndBuildHeader(
	ctx context.Context,
	toolCtx *tool.Context,
	exchange func(context.Context) (*toolauth.OAuthToken, error),
) (http.Header, error) {
	token, err := exchange(ctx)
	if err != nil {
		return nil, err
	}
	if toolCtx != nil && toolCtx.CredentialService != nil {
		if storeErr := storeToken(ctx, toolCtx, h.agentScope(toolCtx), h.credentialKey, token); storeErr != nil {
			h.logger.Warn("failed to cache non-user-scoped token", "error", storeErr)
		}
	}
	return bearerHeader(token), nil
}

func (h *openAPIAuthHandler) exchangeClientCredentials(ctx context.Context) (*toolauth.OAuthToken, error) {
	ctx = toolauth.WithBaseTransport(ctx, h.baseTransport)
	return toolauth.ExchangeClientCredentials(
		ctx, h.tokenURL, h.clientID, h.clientSecret, h.scopes, h.tokenEndpointAuthMethod,
	)
}

func (h *openAPIAuthHandler) exchangeServiceAccount(ctx context.Context) (*toolauth.OAuthToken, error) {
	if h.serviceAccountJSON == "" {
		return nil, fmt.Errorf("serviceaccount: service_account_json is empty")
	}
	cfg, err := google.JWTConfigFromJSON([]byte(h.serviceAccountJSON), h.scopes...)
	if err != nil {
		return nil, fmt.Errorf("serviceaccount: parse JWT config: %w", err)
	}
	if h.baseTransport != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{
			Timeout:   30 * time.Second,
			Transport: h.baseTransport,
		})
	}
	src := cfg.TokenSource(ctx)
	tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("serviceaccount: token exchange failed: %w", err)
	}
	out := &toolauth.OAuthToken{
		AccessToken: tok.AccessToken,
		TokenType:   tok.TokenType,
	}
	if out.TokenType == "" {
		out.TokenType = "bearer"
	}
	if !tok.Expiry.IsZero() {
		out.ExpiresAt = tok.Expiry.Unix()
	}
	return out, nil
}

type credScope struct {
	AgentName, SessionID, UserID string
}

func (h *openAPIAuthHandler) agentScope(toolCtx *tool.Context) credScope {
	return credScope{
		AgentName: toolCtx.AgentName,
		SessionID: toolCtx.AgentName,
		UserID:    toolCtx.AgentName,
	}
}

func bearerHeader(token *toolauth.OAuthToken) http.Header {
	h := http.Header{}
	if token != nil && token.AccessToken != "" {
		h.Set("Authorization", "Bearer "+token.AccessToken)
	}
	return h
}

func loadToken(
	ctx context.Context,
	toolCtx *tool.Context,
	scope credScope,
	credentialKey string,
) (*toolauth.OAuthToken, error) {
	raw, err := toolCtx.CredentialService.Get(ctx, scope.AgentName, scope.SessionID, scope.UserID, credentialKey)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var tok toolauth.OAuthToken
	if err := jsonUnmarshalToken(raw, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func storeToken(
	ctx context.Context,
	toolCtx *tool.Context,
	scope credScope,
	credentialKey string,
	token *toolauth.OAuthToken,
) error {
	data, err := jsonMarshalToken(token)
	if err != nil {
		return err
	}
	return toolCtx.CredentialService.Set(ctx, scope.AgentName, scope.SessionID, scope.UserID, credentialKey, data)
}

func NewAuthHandlerForOpenAPI(cfg *AuthConfig, logger *slog.Logger) (*openAPIAuthHandler, error) {
	if cfg == nil {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "openapi_auth", "authType", cfg.Type)

	switch cfg.Type {
	case AuthTypeOAuth2AuthorizationCode:
		credKey := cfg.CredentialKey
		if credKey == "" {
			credKey = "openapi_" + string(cfg.Type)
		}
		scheme := &toolauth.OAuthScheme{
			AuthorizationURL:        cfg.AuthorizationURL,
			TokenURL:                cfg.TokenURL,
			RefreshURL:              cfg.RefreshURL,
			Scopes:                  cfg.Scopes,
			TokenEndpointAuthMethod: defaultOAuth2AuthMethod(cfg, "client_secret_basic"),
			Audience:                cfg.Audience,
			Resource:                cfg.Resource,
		}
		cred := &toolauth.AuthCredential{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
		}
		inner := toolauth.NewAuthHandler(&toolauth.AuthConfig{
			Type:                 "oauth2",
			Scheme:               scheme,
			Credential:           cred,
			CredentialKey:        credKey,
			DisplayName:          cfg.DisplayName,
			DisablePKCE:          !cfg.UsePKCE,
			DisableConsentPrompt: cfg.DisableConsentPrompt,
			CACertPath:           cfg.CACertPath,
			InsecureSkipVerify:   cfg.InsecureSkipVerify,
		}, logger)

		h := &openAPIAuthHandler{
			authType:                cfg.Type,
			inner:                   inner,
			tokenURL:                cfg.TokenURL,
			clientID:                cfg.ClientID,
			clientSecret:            cfg.ClientSecret,
			scopes:                  cfg.Scopes,
			tokenEndpointAuthMethod: scheme.TokenEndpointAuthMethod,
			credentialKey:           credKey,
			logger:                  logger,
		}
		return h, nil

	case AuthTypeOAuth2ClientCredentials:
		credKey := cfg.CredentialKey
		if credKey == "" {
			credKey = "openapi_oauth2_client_credentials"
		}
		return &openAPIAuthHandler{
			authType:                cfg.Type,
			tokenURL:                cfg.TokenURL,
			clientID:                cfg.ClientID,
			clientSecret:            cfg.ClientSecret,
			scopes:                  cfg.Scopes,
			tokenEndpointAuthMethod: defaultOAuth2AuthMethod(cfg, "client_secret_basic"),
			baseTransport:           toolauth.BuildBaseTransport(cfg.CACertPath, cfg.InsecureSkipVerify, logger),
			credentialKey:           credKey,
			logger:                  logger,
		}, nil

	case AuthTypeServiceAccount:
		credKey := cfg.CredentialKey
		if credKey == "" {
			credKey = "openapi_serviceaccount"
		}
		return &openAPIAuthHandler{
			authType:           cfg.Type,
			serviceAccountJSON: cfg.ServiceAccountJSON,
			scopes:             cfg.Scopes,
			baseTransport:      toolauth.BuildBaseTransport(cfg.CACertPath, cfg.InsecureSkipVerify, logger),
			credentialKey:      credKey,
			logger:             logger,
		}, nil
	}

	return nil, nil
}

func defaultOAuth2AuthMethod(cfg *AuthConfig, fallback string) string {
	if cfg.TokenEndpointAuthMethod != "" {
		return cfg.TokenEndpointAuthMethod
	}
	return fallback
}

func wrapsErrAuthRequired(err error) bool {
	return errors.Is(err, toolauth.ErrAuthRequired)
}
