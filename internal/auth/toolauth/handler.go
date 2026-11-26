// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"cmp"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tlsutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"golang.org/x/sync/singleflight"
)

var ErrAuthRequired = errors.New("authentication required")

var ErrTransientRefresh = errors.New("transient oauth2 refresh failure")

var ErrStateMismatch = errors.New("toolauth: auth response state does not match stored state (possible CSRF)")

var errUserDisconnected = errors.New("the user disconnected the stored credential")

const maxConditionalWriteAttempts = 3

const pendingAuthKeySuffix = ".pending"

type pendingAuth struct {
	CodeVerifier string    `json:"code_verifier"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"created_at"`
}

type AuthHandler struct {
	cfg                          *AuthConfig
	discoveredMeta               *OAuthMetadata
	registeredCred               *AuthCredential
	registeredAuthMethod         string
	registeredClientRegistration *ClientRegistration
	registrationGen              uint64
	mu                           sync.Mutex
	logger                       *slog.Logger

	baseTransport http.RoundTripper

	refreshFailuresMu sync.Mutex
	refreshFailures   map[refreshFailureKey]refreshFailure

	now            func() time.Time
	refreshFlights *singleflight.Group
}

var (
	_ tool.AuthResponseHandler     = (*AuthHandler)(nil)
	_ tool.AuthSignalBuilder       = (*AuthHandler)(nil)
	_ tool.StoredCredentialChecker = (*AuthHandler)(nil)
	_ tool.UserAuthDescriptor      = (*AuthHandler)(nil)
)

func NewAuthHandler(cfg *AuthConfig, logger *slog.Logger) *AuthHandler {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.CredentialKey == "" && cfg.Type == "oauth2" {
		cfgCopy := *cfg
		cfgCopy.CredentialKey = DefaultOAuthCredentialKey(&cfgCopy)
		cfg = &cfgCopy
	}
	h := &AuthHandler{
		cfg:    cfg,
		logger: logger.With("logger_name", "tool_auth", "authType", cfg.Type),
	}
	h.baseTransport = BuildBaseTransport(cfg.CACertPath, cfg.InsecureSkipVerify, h.logger)
	return h
}

func BuildBaseTransport(caCertPath string, insecureSkipVerify bool, logger *slog.Logger) http.RoundTripper {
	if caCertPath == "" && !insecureSkipVerify {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	client, err := tlsutil.BuildHTTPClient(caCertPath, insecureSkipVerify)
	if err != nil {
		logger.Error("tool oauth: failed to build TLS http client, using default trust store",
			"caCertPath", caCertPath,
			"insecureSkipVerify", insecureSkipVerify,
			"error", err)
		return nil
	}
	if insecureSkipVerify {
		logger.Warn("tool oauth: TLS certificate verification disabled — do not use in production")
	}
	return client.Transport
}

func (h *AuthHandler) httpCtx(ctx context.Context) context.Context {
	return WithBaseTransport(ctx, h.baseTransport)
}

func (h *AuthHandler) CredentialKey() string {
	return h.cfg.CredentialKey
}

func (h *AuthHandler) Config() *AuthConfig {
	return h.cfg
}

func (h *AuthHandler) GetAuthHeaders(ctx context.Context, toolCtx *tool.Context) (http.Header, error) {
	switch h.cfg.Type {
	case "basic":
		return h.basicHeaders()
	case "bearer":
		return h.bearerHeaders()
	case "oauth2":
		return h.oauth2Headers(ctx, toolCtx)
	default:
		return nil, nil
	}
}

func (h *AuthHandler) GetOAuthToken(ctx context.Context, toolCtx *tool.Context) (*OAuthToken, error) {
	if h.cfg.Type != "oauth2" {
		return nil, nil
	}
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return nil, ErrAuthRequired
	}
	ctx = h.httpCtx(ctx)
	taskLogger := h.logger.With("taskID", toolCtx.ParentTaskID, "traceID", toolCtx.TraceID)

	token, _, err := h.loadToken(ctx, toolCtx)
	if err != nil {
		taskLogger.Warn("failed to load stored token, triggering re-auth", "error", err)
		return nil, ErrAuthRequired
	}
	if token == nil {
		return nil, ErrAuthRequired
	}

	if token.IsExpired() {
		if token.RefreshToken == "" {
			return nil, ErrAuthRequired
		}
		return h.refreshExpiredToken(ctx, toolCtx, taskLogger)
	}

	return token, nil
}

func (h *AuthHandler) CheckStoredCredential(ctx context.Context, toolCtx *tool.Context) tool.StoredCredential {
	if h.cfg.Type != "oauth2" || toolCtx == nil || toolCtx.CredentialService == nil {
		return tool.StoredCredential{}
	}
	token, _, err := h.loadToken(ctx, toolCtx)
	if err != nil || token == nil {
		return tool.StoredCredential{}
	}
	if token.AccessToken == "" || (token.IsExpired() && token.RefreshToken == "") {
		return tool.StoredCredential{}
	}
	sc := tool.StoredCredential{Connected: true, GrantRef: token.GrantRef}
	if token.ExpiresAt > 0 {
		sc.ExpiresAt = time.Unix(token.ExpiresAt, 0).UTC()
	}
	return sc
}

func (h *AuthHandler) SupportsUserAuth() bool {
	return h.cfg.Type == "oauth2"
}

func (h *AuthHandler) AuthType() string {
	return h.cfg.Type
}

func (h *AuthHandler) AuthDisplayName() string {
	return cmp.Or(h.cfg.DisplayName, hostFromURL(h.cfg.ResourceURL))
}

func (h *AuthHandler) basicHeaders() (http.Header, error) {
	if h.cfg.Credential == nil {
		return nil, fmt.Errorf("basic auth configured but no credentials provided")
	}
	encoded := base64.StdEncoding.EncodeToString(
		[]byte(h.cfg.Credential.Username + ":" + h.cfg.Credential.Password),
	)
	headers := http.Header{}
	headers.Set("Authorization", "Basic "+encoded)
	return headers, nil
}

func (h *AuthHandler) bearerHeaders() (http.Header, error) {
	if h.cfg.Credential == nil || h.cfg.Credential.Token == "" {
		return nil, fmt.Errorf("bearer auth configured but no token provided")
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+h.cfg.Credential.Token)
	return headers, nil
}

func (h *AuthHandler) oauth2Headers(ctx context.Context, toolCtx *tool.Context) (http.Header, error) {
	token, err := h.GetOAuthToken(ctx, toolCtx)
	if err != nil {
		return nil, err
	}

	headers := http.Header{}
	headers.Set("Authorization", bearerPrefix+token.AccessToken)
	return headers, nil
}

const bearerPrefix = "Bearer "

func BearerAccessToken(headers http.Header) string {
	token, _ := strings.CutPrefix(headers.Get("Authorization"), bearerPrefix)
	return token
}

func (h *AuthHandler) refreshStoredToken(ctx context.Context, toolCtx *tool.Context, previous *OAuthToken, stored auth.VersionedCredential) (*OAuthToken, error) {
	taskLogger := h.logger.With("taskID", toolCtx.ParentTaskID, "traceID", toolCtx.TraceID)
	token, err := h.requestRefresh(ctx, toolCtx, previous.RefreshToken, taskLogger)
	if err != nil {
		if errors.Is(err, ErrTransientRefresh) {
			return nil, err
		}
		if IsTransientRefreshFailure(err) {
			return nil, fmt.Errorf("refresh failed: %w: %w", err, ErrTransientRefresh)
		}
		return nil, err
	}

	if token != nil && token.ExpiresAt > 0 && time.Now().Unix() >= token.ExpiresAt {
		taskLogger.Warn("refresh returned an already-expired token; forcing re-auth",
			"credentialKey", h.cfg.CredentialKey,
			"expiresAt", token.ExpiresAt,
		)
		return nil, ErrAuthRequired
	}

	token.GrantRef = previous.GrantRef
	storeErr := h.storeRefreshedToken(ctx, toolCtx, token, stored)
	if storeErr != nil && !errors.Is(storeErr, errUserDisconnected) && !errors.Is(storeErr, auth.ErrCredentialChanged) {
		taskLogger.Warn("failed to store refreshed token; trying once more", "error", storeErr)
		storeErr = h.storeRefreshedToken(ctx, toolCtx, token, stored)
	}
	switch {
	case errors.Is(storeErr, errUserDisconnected):
		taskLogger.Debug("refreshed token not stored: the user disconnected the credential")
	case errors.Is(storeErr, auth.ErrCredentialChanged):
		taskLogger.Debug("refreshed token not stored: another writer stored a usable token first")
	case storeErr != nil:
		taskLogger.Warn("failed to store refreshed token", "error", storeErr)
	}

	return token, nil
}

func (h *AuthHandler) storeRefreshedToken(ctx context.Context, toolCtx *tool.Context, token *OAuthToken, stored auth.VersionedCredential) error {
	err := h.storeTokenAt(ctx, toolCtx, token, stored)
	if errors.Is(err, auth.ErrCredentialChanged) {
		return h.storeRefreshedOverRemoved(ctx, toolCtx, token)
	}
	return err
}

func (h *AuthHandler) requestRefresh(ctx context.Context, toolCtx *tool.Context, refreshToken string, taskLogger *slog.Logger) (*OAuthToken, error) {
	var rejectedClientID string
	var rejection error
	for range 2 {
		if _, err := h.ensureScheme(ctx); err != nil {
			return nil, fmt.Errorf("resolve OAuth2 endpoints for refresh: %w: %w", err, ErrTransientRefresh)
		}
		cred, err := h.ensureCred(ctx, toolCtx)
		if err != nil {
			return nil, fmt.Errorf("resolve OAuth2 client credentials for refresh: %w: %w", err, ErrTransientRefresh)
		}
		if rejection != nil && clientIDOf(cred) == rejectedClientID {
			return nil, rejection
		}
		scheme := h.withResourceIndicator(h.resolveScheme())

		token, err := RefreshToken(ctx, scheme, cred, refreshToken)
		if err == nil || IsTransientRefreshFailure(err) || ClassifyOAuth2Error(err) != "invalid_client" {
			return token, err
		}
		rejectedClientID, rejection = clientIDOf(cred), err
		if h.deletePersistedClient(ctx, toolCtx, rejectedClientID) {
			taskLogger.Warn("refresh failed with invalid_client; the stored client registration differs from the rejected one", RefreshFailureLogAttrs(err)...)
			h.forgetRegisteredClient()
			continue
		}
		taskLogger.Warn("refresh failed with invalid_client, clearing registration to trigger re-DCR", RefreshFailureLogAttrs(err)...)
		h.resetRegistration()
		return nil, err
	}
	return nil, rejection
}

func (h *AuthHandler) storeRefreshedOverRemoved(ctx context.Context, toolCtx *tool.Context, token *OAuthToken) error {
	current, currentStored, err := h.loadToken(ctx, toolCtx)
	if err != nil {
		return err
	}
	if currentStored.Disconnected {
		return errUserDisconnected
	}
	if current != nil && !current.IsExpired() {
		return auth.ErrCredentialChanged
	}
	return h.storeTokenAt(ctx, toolCtx, token, currentStored)
}

func (h *AuthHandler) BuildAuthSignal(ctx context.Context, toolCtx *tool.Context) (*a2a.AuthenticationRequiredData, error) {
	if toolCtx != nil && (toolCtx.AuthPromptUnavailable || AuthFlowClassifyOnly(toolCtx.UserID, toolCtx.Trigger)) {
		return h.classifyOnlyAuthRequired(toolCtx), nil
	}
	ctx = h.httpCtx(ctx)
	scheme, err := h.ensureScheme(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve OAuth2 endpoints: %w", err)
	}

	cred, err := h.ensureCred(ctx, toolCtx)
	if err != nil {
		return nil, fmt.Errorf("resolve OAuth2 client credentials: %w", err)
	}
	if cred == nil || cred.ClientID == "" {
		return nil, fmt.Errorf("no OAuth2 client ID available for %s: configure credential.client_id, set TOOL_OAUTH_CLIENT_ID, or point at an auth server that supports RFC 7591 dynamic client registration", logging.RedactURL(h.cfg.ResourceURL))
	}

	var verifier string
	if !h.cfg.DisablePKCE {
		verifier = GenerateVerifier()
	}
	state := GenerateState()

	redirectURI := ResolveRedirectURI()
	var urlOpts []AuthURLOption
	if h.cfg.DisableConsentPrompt {
		urlOpts = append(urlOpts, WithoutConsentPrompt())
	}
	authURI, err := BuildAuthorizationURL(h.withResourceIndicator(scheme), cred.ClientID, state, verifier, redirectURI, urlOpts...)
	if err != nil {
		return nil, err
	}

	if toolCtx.ToolCallID == "" {
		h.logger.Warn("BuildAuthSignal called with empty ToolCallID; pending-auth state cannot be scoped per-flow (concurrent flows for this credential may collide)",
			"credentialKey", h.cfg.CredentialKey,
			"taskID", toolCtx.ParentTaskID,
			"traceID", toolCtx.TraceID,
		)
	}
	if err := h.storePending(ctx, toolCtx, promptFlow(toolCtx), pendingAuth{
		CodeVerifier: verifier,
		State:        state,
		CreatedAt:    time.Now(),
	}); err != nil {
		h.logger.Warn("failed to persist pending auth state; CSRF validation will be skipped on callback",
			"credentialKey", h.cfg.CredentialKey,
			"taskID", toolCtx.ParentTaskID,
			"traceID", toolCtx.TraceID,
			"error", err,
		)
	} else if err := h.dropLegacyPending(ctx, toolCtx, promptFlow(toolCtx)); err != nil {
		h.logger.Warn("failed to delete the pending sign-in row of an earlier release; an AWE of that release may ignore the answer to this prompt",
			"credentialKey", h.cfg.CredentialKey,
			"taskID", toolCtx.ParentTaskID,
			"traceID", toolCtx.TraceID,
			"error", err,
		)
	}

	return &a2a.AuthenticationRequiredData{
		Type:                      string(a2a.SignalAuthenticationRequired),
		TargetAgent:               toolCtx.AgentName,
		TargetAgentTaskID:         toolCtx.ParentTaskID,
		UserID:                    toolCtx.UserID,
		ClientID:                  toolCtx.ClientID,
		AuthURI:                   authURI,
		AuthRequestFunctionCallID: toolCtx.ToolCallID,
		CredentialKey:             h.cfg.CredentialKey,
		GatewayTaskID:             nil,
		CodeVerifier:              verifier,
		ToolDisplayName:           h.AuthDisplayName(),
	}, nil
}

func (h *AuthHandler) classifyOnlyAuthRequired(toolCtx *tool.Context) *a2a.AuthenticationRequiredData {
	return &a2a.AuthenticationRequiredData{
		Type:                      string(a2a.SignalAuthenticationRequired),
		TargetAgent:               toolCtx.AgentName,
		TargetAgentTaskID:         toolCtx.ParentTaskID,
		UserID:                    toolCtx.UserID,
		ClientID:                  toolCtx.ClientID,
		AuthRequestFunctionCallID: toolCtx.ToolCallID,
		CredentialKey:             h.cfg.CredentialKey,
		ToolDisplayName:           h.AuthDisplayName(),
	}
}

func (h *AuthHandler) resourceIndicator(scheme *OAuthScheme) string {
	if scheme != nil && scheme.Resource != "" {
		return scheme.Resource
	}
	if h.cfg.Scheme != nil && h.cfg.Scheme.Resource != "" {
		return h.cfg.Scheme.Resource
	}
	h.mu.Lock()
	discovered := ""
	if h.discoveredMeta != nil {
		discovered = h.discoveredMeta.Resource
	}
	h.mu.Unlock()
	if discovered != "" {
		return discovered
	}
	return h.cfg.ResourceURL
}

func (h *AuthHandler) withResourceIndicator(scheme *OAuthScheme) *OAuthScheme {
	if scheme == nil {
		return nil
	}
	resource := h.resourceIndicator(scheme)
	if resource == scheme.Resource {
		return scheme
	}
	clone := *scheme
	clone.Resource = resource
	return &clone
}

func hostFromURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (h *AuthHandler) HandleAuthResponse(ctx context.Context, resp *a2a.AuthResponseData, toolCtx *tool.Context) error {
	if toolCtx == nil || toolCtx.UserID == "" {
		return fmt.Errorf("toolauth: auth_response missing user_id — credential cannot be scoped")
	}
	ctx = h.httpCtx(ctx)
	if resp.FunctionCallID == "" {
		h.logger.Warn("auth_response missing function_call_id; pending-auth state cannot be scoped per-flow (concurrent flows for this credential may collide)",
			"credentialKey", h.cfg.CredentialKey,
		)
	}
	rows, pendingErr := h.loadPending(ctx, toolCtx, answerFlow(resp))
	codeVerifier := resp.CodeVerifier
	if pendingErr == nil && len(rows) > 0 {
		pending := &rows[0]
		if resp.State != "" {
			pending = pendingForState(rows, resp.State)
		}
		if pending == nil {
			h.logger.Warn("OAuth2 state mismatch (possible CSRF)",
				"credentialKey", h.cfg.CredentialKey,
				"userID", toolCtx.UserID,
				"taskID", toolCtx.ParentTaskID,
			)
			_ = h.deletePending(ctx, toolCtx, answerFlow(resp))
			return ErrStateMismatch
		}
		codeVerifier = pending.CodeVerifier
	} else {
		reason := "not_found"
		if pendingErr != nil {
			reason = "load_error"
		}
		h.logger.Warn("OAuth2 CSRF state validation skipped: pending state unavailable, exchange will use response-carried verifier",
			"credentialKey", h.cfg.CredentialKey,
			"userID", toolCtx.UserID,
			"taskID", toolCtx.ParentTaskID,
			"reason", reason,
		)
	}

	if _, err := h.ensureScheme(ctx); err != nil {
		_ = h.deletePending(ctx, toolCtx, answerFlow(resp))
		return fmt.Errorf("resolve OAuth2 endpoints for code exchange: %w", err)
	}
	cred, err := h.ensureCred(ctx, toolCtx)
	if err != nil {
		_ = h.deletePending(ctx, toolCtx, answerFlow(resp))
		return fmt.Errorf("resolve OAuth2 client credentials for code exchange: %w", err)
	}
	scheme := h.withResourceIndicator(h.resolveScheme())

	redirectURI := ResolveRedirectURI()
	token, err := ExchangeCode(ctx, scheme, cred, resp.Code, codeVerifier, redirectURI)
	if err != nil {
		_ = h.deletePending(ctx, toolCtx, answerFlow(resp))
		if ClassifyOAuth2Error(err) == "invalid_client" {
			h.logger.Warn("code exchange failed with invalid_client, clearing registration to trigger re-DCR",
				"credentialKey", h.cfg.CredentialKey, "error", err)
			h.deletePersistedClient(ctx, toolCtx, clientIDOf(cred))
			h.resetRegistration()
		}
		return fmt.Errorf("code exchange failed: %w", err)
	}

	_ = h.deletePending(ctx, toolCtx, answerFlow(resp))

	token.GrantRef = a2a.ToolAuthGrantRef(resp.State)
	if err := h.storeSignInToken(ctx, toolCtx, token); err != nil {
		return err
	}
	h.forgetRefreshFailure(newRefreshFailureKey(toolCtx))
	return nil
}

func (h *AuthHandler) ClearCredential(ctx context.Context, toolCtx *tool.Context) error {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return nil
	}
	return toolCtx.CredentialService.Delete(ctx, toolCtx.AgentName, toolCtx.AgentName, toolCtx.UserID, h.cfg.CredentialKey)
}

func (h *AuthHandler) ClearCredentialForReauth(ctx context.Context, toolCtx *tool.Context, rejectedAccessToken string) error {
	if h.keepCredentialForReauth(toolCtx) || toolCtx == nil || toolCtx.CredentialService == nil {
		return nil
	}
	current, stored, err := h.loadToken(ctx, toolCtx)
	if err != nil && !stored.Found {
		return err
	}
	if err == nil && (current == nil || !tokenStillRejected(current, rejectedAccessToken)) {
		return nil
	}
	err = deleteAt(ctx, toolCtx.CredentialService, h.tokenRef(toolCtx), stored)
	if errors.Is(err, auth.ErrCredentialChanged) {
		return nil
	}
	return err
}

func (h *AuthHandler) StoredTokenReplaces(ctx context.Context, toolCtx *tool.Context, rejectedAccessToken string) bool {
	if rejectedAccessToken == "" || h.cfg.Type != "oauth2" || toolCtx == nil || toolCtx.CredentialService == nil {
		return false
	}
	current, _, err := h.loadToken(ctx, toolCtx)
	return err == nil && current != nil && !current.IsExpired() && current.AccessToken != rejectedAccessToken
}

func tokenStillRejected(current *OAuthToken, rejectedAccessToken string) bool {
	if rejectedAccessToken == "" {
		return current.IsExpired()
	}
	return current.AccessToken == rejectedAccessToken
}

func (h *AuthHandler) clearForReauthAt(ctx context.Context, toolCtx *tool.Context, stored auth.VersionedCredential) error {
	if h.keepCredentialForReauth(toolCtx) {
		return nil
	}
	return deleteAt(ctx, toolCtx.CredentialService, h.tokenRef(toolCtx), stored)
}

func (h *AuthHandler) freshTokenAfterRejectedRefresh(ctx context.Context, toolCtx *tool.Context, stored auth.VersionedCredential) *OAuthToken {
	if current := h.tokenStoredSince(ctx, toolCtx, stored); current != nil {
		return current
	}
	if !errors.Is(h.clearForReauthAt(ctx, toolCtx, stored), auth.ErrCredentialChanged) {
		return nil
	}
	return h.tokenStoredSince(ctx, toolCtx, stored)
}

func (h *AuthHandler) tokenStoredSince(ctx context.Context, toolCtx *tool.Context, stored auth.VersionedCredential) *OAuthToken {
	current, currentStored, err := h.loadToken(ctx, toolCtx)
	if err != nil || current == nil || current.IsExpired() || currentStored.Value == stored.Value {
		return nil
	}
	return current
}

func (h *AuthHandler) keepCredentialForReauth(toolCtx *tool.Context) bool {
	if toolCtx != nil && toolCtx.AuthPromptUnavailable {
		h.logger.Debug("auth wall in a caller that cannot prompt; stored credential kept",
			"credentialKey", h.cfg.CredentialKey,
			"taskID", toolCtx.ParentTaskID,
			"traceID", toolCtx.TraceID)
		return true
	}
	if toolCtx != nil && toolCtx.Trigger == a2a.TriggerEval {
		h.logger.Warn("preserving stored credential for eval-triggered re-auth",
			"credentialKey", h.cfg.CredentialKey,
			"userID", toolCtx.UserID,
			"taskID", toolCtx.ParentTaskID,
			"traceID", toolCtx.TraceID)
		return true
	}
	if toolCtx != nil && h.refreshEscalated(newRefreshFailureKey(toolCtx)) {
		h.logger.Info("preserving stored credential for re-auth after a sign-in provider outage",
			"credentialKey", h.cfg.CredentialKey,
			"userID", toolCtx.UserID,
			"taskID", toolCtx.ParentTaskID,
			"traceID", toolCtx.TraceID)
		return true
	}
	return false
}

func (h *AuthHandler) loadToken(ctx context.Context, toolCtx *tool.Context) (*OAuthToken, auth.VersionedCredential, error) {
	stored, err := getVersioned(ctx, toolCtx.CredentialService, h.tokenRef(toolCtx))
	if err != nil {
		return nil, stored, err
	}
	if stored.Value == "" {
		return nil, stored, nil
	}

	var token OAuthToken
	if err := json.Unmarshal([]byte(stored.Value), &token); err != nil {
		return nil, stored, fmt.Errorf("deserialize stored token: %w", err)
	}

	return &token, stored, nil
}

func (h *AuthHandler) storeTokenAt(ctx context.Context, toolCtx *tool.Context, token *OAuthToken, stored auth.VersionedCredential) error {
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("serialize token: %w", err)
	}
	_, err = setAt(ctx, toolCtx.CredentialService, h.tokenRef(toolCtx), string(data), 0, stored)
	return err
}

func (h *AuthHandler) storeSignInToken(ctx context.Context, toolCtx *tool.Context, token *OAuthToken) error {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return fmt.Errorf("no credential service available")
	}

	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("serialize token: %w", err)
	}

	svc := toolCtx.CredentialService
	ref := h.tokenRef(toolCtx)
	for attempt := 1; ; attempt++ {
		stored, err := getVersioned(ctx, svc, ref)
		if err != nil {
			return svc.Set(ctx, ref.AgentName, ref.AppName, ref.UserID, ref.Key, string(data))
		}
		_, err = setAt(ctx, svc, ref, string(data), 0, stored)
		if !errors.Is(err, auth.ErrCredentialChanged) || attempt == maxConditionalWriteAttempts {
			return err
		}
	}
}

type pendingFlow struct {
	taskID string
	callID string
}

func promptFlow(toolCtx *tool.Context) pendingFlow {
	return pendingFlow{taskID: toolCtx.ParentTaskID, callID: toolCtx.ToolCallID}
}

func answerFlow(resp *a2a.AuthResponseData) pendingFlow {
	return pendingFlow{taskID: resp.TargetAgentTaskID, callID: resp.FunctionCallID}
}

func (h *AuthHandler) pendingKey(flow pendingFlow) string {
	if flow.taskID == "" {
		return h.legacyPendingKey(flow)
	}
	return h.cfg.CredentialKey + ":" + flow.taskID + ":" + flow.callID + pendingAuthKeySuffix
}

func (h *AuthHandler) legacyPendingKey(flow pendingFlow) string {
	return h.cfg.CredentialKey + ":" + flow.callID + pendingAuthKeySuffix
}

func (h *AuthHandler) storePending(ctx context.Context, toolCtx *tool.Context, flow pendingFlow, p pendingAuth) error {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return fmt.Errorf("no credential service available")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("serialize pending auth: %w", err)
	}
	return toolCtx.CredentialService.Set(ctx, toolCtx.AgentName, toolCtx.AgentName, toolCtx.UserID, h.pendingKey(flow), string(data))
}

func (h *AuthHandler) dropLegacyPending(ctx context.Context, toolCtx *tool.Context, flow pendingFlow) error {
	if flow.taskID == "" {
		return nil
	}
	key := h.legacyPendingKey(flow)
	raw, err := toolCtx.CredentialService.Get(ctx, toolCtx.AgentName, toolCtx.AgentName, toolCtx.UserID, key)
	if err != nil || raw == "" {
		return err
	}
	return toolCtx.CredentialService.Delete(ctx, toolCtx.AgentName, toolCtx.AgentName, toolCtx.UserID, key)
}

func (h *AuthHandler) loadPending(ctx context.Context, toolCtx *tool.Context, flow pendingFlow) ([]pendingAuth, error) {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return nil, nil
	}
	keys := []string{h.pendingKey(flow)}
	if flow.taskID != "" {
		keys = append(keys, h.legacyPendingKey(flow))
	}
	var rows []pendingAuth
	for _, key := range keys {
		raw, err := toolCtx.CredentialService.Get(ctx, toolCtx.AgentName, toolCtx.AgentName, toolCtx.UserID, key)
		if err != nil {
			return nil, err
		}
		if raw == "" {
			continue
		}
		var p pendingAuth
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("deserialize pending auth: %w", err)
		}
		rows = append(rows, p)
	}
	return rows, nil
}

func pendingForState(rows []pendingAuth, state string) *pendingAuth {
	for i := range rows {
		if subtle.ConstantTimeCompare([]byte(rows[i].State), []byte(state)) == 1 {
			return &rows[i]
		}
	}
	return nil
}

func (h *AuthHandler) AnswersPendingPrompt(ctx context.Context, resp *a2a.AuthResponseData, toolCtx *tool.Context) (matches, known bool) {
	if resp == nil || resp.State == "" || toolCtx == nil || toolCtx.UserID == "" {
		return false, false
	}
	rows, err := h.loadPending(ctx, toolCtx, answerFlow(resp))
	if err != nil || len(rows) == 0 {
		return false, false
	}
	return pendingForState(rows, resp.State) != nil, true
}

func (h *AuthHandler) deletePending(ctx context.Context, toolCtx *tool.Context, flow pendingFlow) error {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return nil
	}
	err := toolCtx.CredentialService.Delete(ctx, toolCtx.AgentName, toolCtx.AgentName, toolCtx.UserID, h.pendingKey(flow))
	if flow.taskID != "" {
		err = errors.Join(err, toolCtx.CredentialService.Delete(ctx, toolCtx.AgentName, toolCtx.AgentName, toolCtx.UserID, h.legacyPendingKey(flow)))
	}
	return err
}

func (h *AuthHandler) resetRegistration() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.discoveredMeta = nil
	h.forgetRegisteredClientLocked()
}

func (h *AuthHandler) forgetRegisteredClient() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.forgetRegisteredClientLocked()
}

func (h *AuthHandler) forgetRegisteredClientLocked() {
	h.registeredCred = nil
	h.registeredAuthMethod = ""
	h.registeredClientRegistration = nil
	h.registrationGen++
}

func (h *AuthHandler) evictRotatedClientSecretLocked() {
	if h.registeredClientRegistration != nil && h.registeredClientRegistration.IsExpired() {
		h.logger.Info("registered client_secret has expired; re-registering",
			"expiresAt", h.registeredClientRegistration.ClientSecretExpiresAt,
		)
		h.registeredCred = nil
		h.registeredClientRegistration = nil
		h.registeredAuthMethod = ""
	}
}

func (h *AuthHandler) ensureScheme(ctx context.Context) (*OAuthScheme, error) {
	if h.cfg.Scheme != nil && h.cfg.Scheme.AuthorizationURL != "" && h.cfg.Scheme.TokenURL != "" {
		return h.cfg.Scheme, nil
	}

	if envScheme, ok := mcpEnvScheme(h.cfg.Scheme); ok {
		return envScheme, nil
	}

	h.mu.Lock()
	if h.discoveredMeta != nil && !h.discoveredMeta.IsExpired() {
		scheme := h.metaToSchemeLocked()
		h.mu.Unlock()
		return scheme, nil
	}
	if h.cfg.ResourceURL == "" {
		h.mu.Unlock()
		if h.cfg.Scheme != nil {
			return h.cfg.Scheme, nil
		}
		return nil, fmt.Errorf("OAuth2 scheme incomplete and no ResourceURL set for discovery")
	}
	h.mu.Unlock()

	meta, err := DiscoverOAuthMetadata(ctx, h.cfg.ResourceURL, h.logger)
	if err != nil {
		return nil, fmt.Errorf("oauth2 discovery failed for %s: %w", logging.RedactURL(h.cfg.ResourceURL), err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.discoveredMeta != nil && !h.discoveredMeta.IsExpired() {
		return h.metaToSchemeLocked(), nil
	}

	if h.discoveredMeta != nil &&
		(h.discoveredMeta.Issuer != meta.Issuer ||
			h.discoveredMeta.RegistrationEndpoint != meta.RegistrationEndpoint) {
		h.registeredCred = nil
		h.registeredAuthMethod = ""
		h.registeredClientRegistration = nil
	}

	h.discoveredMeta = meta
	h.logger.Info("oauth2 metadata discovered",
		"resource_url", logging.RedactURL(h.cfg.ResourceURL),
		"issuer", meta.Issuer,
		"authorization_endpoint", meta.AuthorizationEndpoint,
		"token_endpoint", meta.TokenEndpoint,
		"registration_endpoint", meta.RegistrationEndpoint,
	)

	return h.metaToSchemeLocked(), nil
}

func (h *AuthHandler) ensureCred(ctx context.Context, toolCtx *tool.Context) (*AuthCredential, error) {
	if h.cfg.Credential != nil && h.cfg.Credential.ClientID != "" {
		return h.cfg.Credential, nil
	}

	h.mu.Lock()
	h.evictRotatedClientSecretLocked()
	if h.registeredCred != nil {
		cred := h.registeredCred
		h.mu.Unlock()
		return cred, nil
	}
	var regURL string
	var meta *OAuthMetadata
	if h.discoveredMeta != nil {
		regURL = h.discoveredMeta.RegistrationEndpoint
		meta = h.discoveredMeta
	}
	gen := h.registrationGen
	h.mu.Unlock()

	var stored auth.VersionedCredential
	if regURL != "" {
		var reg *ClientRegistration
		if reg, stored = h.loadPersistedClient(ctx, toolCtx, meta); reg != nil {
			h.mu.Lock()
			switch {
			case h.registeredCred != nil:
				cred := h.registeredCred
				h.mu.Unlock()
				return cred, nil
			case h.registrationGen != gen:
				h.mu.Unlock()
			default:
				h.registeredCred = &AuthCredential{ClientID: reg.ClientID, ClientSecret: reg.ClientSecret}
				h.registeredClientRegistration = reg
				if reg.TokenEndpointAuthMethod != "" {
					h.registeredAuthMethod = reg.TokenEndpointAuthMethod
				}
				cred := h.registeredCred
				h.mu.Unlock()
				_ = h.storePersistedClient(ctx, toolCtx, reg, meta, stored)
				h.logger.Info("oauth2 reusing persisted dynamic client registration",
					"registration_endpoint", regURL,
					"clientID", reg.ClientID,
				)
				return cred, nil
			}
		}
	}

	if regURL == "" && os.Getenv("TOOL_OAUTH_CLIENT_ID") == "" && os.Getenv("MCP_CLIENT_ID") == "" {
		return h.cfg.Credential, nil
	}

	redirectURI := ResolveRedirectURI()
	var scopes []string
	if h.cfg.Scheme != nil {
		scopes = h.cfg.Scheme.Scopes
	}
	if len(scopes) == 0 && meta != nil {
		scopes = meta.ScopesSupported
	}
	reg, err := RegisterClientWithConfig(ctx, regURL, redirectURI, meta, RegistrationRequest{Scopes: scopes}, h.logger)
	if err != nil {
		return nil, err
	}
	kept, err := h.storeRegisteredClient(ctx, toolCtx, reg, meta, stored)
	if err != nil {
		return nil, err
	}
	if kept.ClientID != reg.ClientID {
		h.logger.Info("oauth2 using the dynamic client registration another replica stored",
			"registration_endpoint", regURL,
			"clientID", kept.ClientID,
			"discardedClientID", reg.ClientID,
		)
		reg = kept
	}

	h.mu.Lock()
	if h.registeredCred != nil {
		cred := h.registeredCred
		h.mu.Unlock()
		return cred, nil
	}

	h.registeredCred = &AuthCredential{
		ClientID:     reg.ClientID,
		ClientSecret: reg.ClientSecret,
	}
	h.registeredClientRegistration = reg

	if reg.TokenEndpointAuthMethod != "" {
		h.registeredAuthMethod = reg.TokenEndpointAuthMethod
	}
	cred := h.registeredCred
	h.mu.Unlock()

	if reg.FromEnv {
		h.logger.Info("oauth2 using env-var client credentials",
			"clientID", reg.ClientID,
			"registrationEndpointTried", regURL,
		)
	} else {
		h.logger.Info("oauth2 dynamic client registration",
			"registration_endpoint", regURL,
			"clientID", reg.ClientID,
			"token_endpoint_auth_method", reg.TokenEndpointAuthMethod,
		)
	}

	return cred, nil
}

func (h *AuthHandler) resolveScheme() *OAuthScheme {
	if h.cfg.Scheme != nil && h.cfg.Scheme.AuthorizationURL != "" && h.cfg.Scheme.TokenURL != "" {
		return h.cfg.Scheme
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.discoveredMeta != nil {
		return h.metaToSchemeLocked()
	}
	if h.cfg.Scheme != nil {
		return h.cfg.Scheme
	}
	return nil
}

func (h *AuthHandler) metaToSchemeLocked() *OAuthScheme {
	if h.discoveredMeta == nil {
		return nil
	}
	method := "client_secret_post"
	if h.registeredAuthMethod != "" {
		method = h.registeredAuthMethod
	}
	if h.cfg.Scheme != nil && h.cfg.Scheme.TokenEndpointAuthMethod != "" {
		method = h.cfg.Scheme.TokenEndpointAuthMethod
	}
	scopes := h.discoveredMeta.ScopesSupported
	if h.cfg.Scheme != nil && len(h.cfg.Scheme.Scopes) > 0 {
		scopes = h.cfg.Scheme.Scopes
	}
	return &OAuthScheme{
		AuthorizationURL:        h.discoveredMeta.AuthorizationEndpoint,
		TokenURL:                h.discoveredMeta.TokenEndpoint,
		Scopes:                  scopes,
		TokenEndpointAuthMethod: method,
	}
}

func mcpEnvScheme(existing *OAuthScheme) (*OAuthScheme, bool) {
	authURL := os.Getenv("MCP_AUTHORIZATION_URL")
	tokenURL := os.Getenv("MCP_TOKEN_URL")
	if authURL == "" || tokenURL == "" {
		return nil, false
	}
	out := &OAuthScheme{
		AuthorizationURL: authURL,
		TokenURL:         tokenURL,
		RefreshURL:       os.Getenv("MCP_REFRESH_URL"),
	}
	if existing != nil {
		if existing.AuthorizationURL != "" {
			out.AuthorizationURL = existing.AuthorizationURL
		}
		if existing.TokenURL != "" {
			out.TokenURL = existing.TokenURL
		}
		if existing.RefreshURL != "" {
			out.RefreshURL = existing.RefreshURL
		}
		out.Scopes = existing.Scopes
		out.TokenEndpointAuthMethod = existing.TokenEndpointAuthMethod
		out.Audience = existing.Audience
		out.Resource = existing.Resource
	}
	return out, true
}
