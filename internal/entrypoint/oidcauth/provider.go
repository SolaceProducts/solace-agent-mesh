// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

const (
	authReasonInvalidToken         = "invalid_token"
	authReasonExpiredToken         = "expired_token"
	authReasonMissingToken         = "missing_token"
	authReasonOIDCMissingCode      = "oidc_missing_code"
	authReasonOIDCStateMismatch    = "oidc_state_mismatch"
	authReasonOIDCCodeExchangeFail = "oidc_code_exchange_failed"
	authReasonOIDCUserinfoFail     = "oidc_userinfo_failed"
	authReasonOIDCIDTokenFail      = "oidc_idtoken_verify_failed"
	authReasonOIDCSAMMintFail      = "oidc_sam_mint_failed"
	authReasonOIDCReservedSubject  = "oidc_reserved_subject"
	authReasonOIDCLogin            = "oidc_login"
	authReasonLogout               = "logout"
)

const accessTokenCookieName = "sam_access_token"

const idTokenVerificationFailedMsg = "id_token verification failed"

type stateDispatcherHandler interface {
	Handle(w http.ResponseWriter, r *http.Request)
}

type Provider struct {
	oidcClient       OIDCClient
	idTokenVerifier  IDTokenVerifier
	signer           *trust.Signer
	authzSvc         *AuthorizationService
	auditLogger      coreauth.AuditLogger
	config           *Config
	gatewayID        string
	profileCache     *auth.ProfileCache
	stateDispatcher  atomic.Pointer[stateDispatcherHandler]
	userUpsertClient auth.UserUpsertClient
	userUpsertSem    chan struct{}

	keyAuthority atomic.Pointer[apikey.Authority]

	logger *slog.Logger
}

const maxConcurrentUserUpserts = 64

func (p *Provider) SetProfileCache(c *auth.ProfileCache) {
	p.profileCache = c
}

func (p *Provider) SetUserUpsertClient(c auth.UserUpsertClient) {
	p.userUpsertClient = c
}

func (p *Provider) SetKeyAuthority(a apikey.Authority) {
	if a == nil {
		return
	}
	p.keyAuthority.Store(&a)
}

func (p *Provider) keyAuthorityOrNil() apikey.Authority {
	if held := p.keyAuthority.Load(); held != nil {
		return *held
	}
	return nil
}

func (p *Provider) SetStateDispatcher(d stateDispatcherHandler) {
	if d == nil {
		p.stateDispatcher.Store(nil)
		return
	}
	p.stateDispatcher.Store(&d)
}

func NewProvider(cfg *Config, signer *trust.Signer, gatewayID string, auditLogger coreauth.AuditLogger, logger *slog.Logger) (*Provider, error) {
	if signer == nil {
		return nil, fmt.Errorf("enterprise auth: trust signer is required")
	}
	if auditLogger == nil {
		auditLogger = coreauth.NoopAuditLogger{}
	}

	providerName := cfg.ExternalAuthProvider

	if cfg.ExternalAuthServiceURL != "" {
		logger.Warn("external_auth_service_url is no longer supported and will be ignored; configure a providers: catalog instead",
			"externalAuthServiceURL", cfg.ExternalAuthServiceURL,
			"gatewayID", gatewayID,
			"providerName", providerName,
		)
	}

	if cfg.OIDC == nil || cfg.OIDC.Issuer == "" {
		return nil, fmt.Errorf("enterprise auth: native OIDC is required (cfg.OIDC is nil); ParseConfig should have caught this")
	}

	native, err := NewNativeOIDCClient(*cfg.OIDC, logger)
	if err != nil {
		return nil, fmt.Errorf("native OIDC client: %w", err)
	}
	warmCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if warmErr := native.WarmDiscovery(warmCtx); warmErr != nil {
		logger.Warn("OIDC discovery deferred to first request",
			"issuer", cfg.OIDC.Issuer,
			"providerName", providerName,
			"error", warmErr,
		)
	}
	cancel()
	var oidcClient OIDCClient = native
	logger.Info("using native OIDC provider",
		"issuer", cfg.OIDC.Issuer,
		"providerName", providerName,
	)

	authzSvc, err := NewAuthorizationService(cfg.AuthorizationService, gatewayID, logger)
	if err != nil {
		return nil, fmt.Errorf("create authorization service: %w", err)
	}

	idTokenVerifier, err := NewJWKSIDTokenVerifier(
		cfg.OIDC.Issuer,
		cfg.OIDC.ClientID,
		native.JWKSURI,
		native.HTTPClient(),
		logger.With("providerName", providerName),
	)
	if err != nil {
		return nil, fmt.Errorf("build id_token verifier: %w", err)
	}

	if cfg.SyntheticAuth != nil {
		if err := cfg.SyntheticAuth.InitVerifier(native.HTTPClient(), logger.With("providerName", providerName)); err != nil {
			return nil, err
		}
		logger.Info("synthetic-monitor auth path enabled",
			"role", cfg.SyntheticAuth.RoleName,
			"appidAllowlistCount", len(cfg.SyntheticAuth.AppIDAllowlist),
			"endpointAllowlistCount", len(cfg.SyntheticAuth.EndpointRules),
			"issuersCount", len(cfg.SyntheticAuth.Issuers),
			"principalRoles", cfg.SyntheticAuth.PrincipalRoles,
		)
	}

	return &Provider{
		oidcClient:      oidcClient,
		idTokenVerifier: idTokenVerifier,
		signer:          signer,
		authzSvc:        authzSvc,
		auditLogger:     auditLogger,
		config:          cfg,
		gatewayID:       gatewayID,
		userUpsertSem:   make(chan struct{}, maxConcurrentUserUpserts),
		logger: logger.With(
			"logger_name", "enterprise-auth",
			"gatewayID", gatewayID,
			"providerName", providerName,
		),
	}, nil
}

type stateValidatingClient interface {
	RequiresStateValidation() bool
}

func (p *Provider) requiresStateValidation() bool {
	if v, ok := p.oidcClient.(stateValidatingClient); ok {
		return v.RequiresStateValidation()
	}
	return false
}

func (p *Provider) logAuthEvent(ctx context.Context, userID string, success bool, reason string) {
	p.auditLogger.LogAuth(ctx, coreauth.AuthEvent{
		UserID:  userID,
		Success: success,
		Reason:  reason,
	})
	result := observability.AuthResultFailure
	if success {
		result = observability.AuthResultSuccess
	}
	observability.RecordAuthLogin(p.gatewayID, result, reason)
}

func (p *Provider) recordPreRouteReject(r *http.Request, status int) {
	observability.RecordGatewayReject(p.gatewayID, observability.RejectRouteAuth, r.Method, status)
}

func (p *Provider) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.ShouldSkipAuth(r) {
				next.ServeHTTP(w, r)
				return
			}

			softAuth := auth.IsSoftAuthPath(r)

			token, position, extractErr := p.extractToken(r)
			if extractErr != nil {
				if softAuth {
					next.ServeHTTP(w, r)
					return
				}
				p.logAuthEvent(r.Context(), "", false, authReasonInvalidToken)
				p.recordPreRouteReject(r, http.StatusUnauthorized)
				auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, extractErr.Error())
				return
			}
			if token == "" {
				if softAuth {
					next.ServeHTTP(w, r)
					return
				}
				p.logAuthEvent(r.Context(), "", false, authReasonMissingToken)
				p.recordPreRouteReject(r, http.StatusUnauthorized)
				auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, "Not authenticated")
				return
			}

			if apikey.HasMarker(token) {
				p.handleAPIKeyAuth(w, r, next, token, position, softAuth)
				return
			}

			if p.config.SyntheticAuth != nil {
				handled := p.trySyntheticAuth(w, r, next, token, softAuth)
				if handled {
					return
				}
			}

			identity, samErr := auth.ForSAMAccessToken(token, samTokenVerifier{p.signer}, NewScopeExpander(p.authzSvc))
			if samErr == nil {
				next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
				return
			}
			p.logger.Debug("SAM token validation failed; falling through to IdP validation", "error", samErr)

			identity, err := p.IdentityFromIDPToken(r.Context(), token)
			if err != nil {
				if softAuth {
					next.ServeHTTP(w, r)
					return
				}
				p.logger.Info("IdP token validation failed; returning 401",
					"path", auth.LoggablePath(r),
				)
				reason := authReasonInvalidToken
				if errors.Is(samErr, jwt.ErrTokenExpired) {
					reason = authReasonExpiredToken
				}
				p.logAuthEvent(r.Context(), "", false, reason)
				p.recordPreRouteReject(r, http.StatusUnauthorized)
				auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorInvalidToken, "Invalid or expired session token, please log in again")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
		})
	}
}

func (p *Provider) handleAPIKeyAuth(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	token string,
	position credentialPosition,
	softAuth bool,
) {
	if position != credentialPositionHeader {
		p.rejectAPIKey(w, r, next, auth.RejectAPIKeyPosition(), softAuth)
		return
	}

	identity, rejection := auth.ResolveAPIKey(r.Context(), p.keyAuthorityOrNil(), token)
	if rejection != nil {
		p.rejectAPIKey(w, r, next, rejection, softAuth)
		return
	}
	next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
}

func (p *Provider) rejectAPIKey(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	rejection *auth.APIKeyRejection,
	softAuth bool,
) {
	if r.Context().Err() != nil {
		return
	}
	observability.RecordAuthTokenValidationFailure(p.gatewayID, rejection.Reason)
	if softAuth {
		p.logger.Warn("api key refused on a soft-auth path; continuing anonymously", "reason", rejection.Reason)
		next.ServeHTTP(w, r)
		return
	}
	p.logAuthEvent(r.Context(), "", false, rejection.Reason)
	p.recordPreRouteReject(r, rejection.Status)
	rejection.Respond(w, r)
}

func (p *Provider) trySyntheticAuth(w http.ResponseWriter, r *http.Request, next http.Handler, token string, softAuth bool) bool {
	sc := p.config.SyntheticAuth
	claims, err := sc.ValidateSyntheticToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrSyntheticTokenNotApplicable) {
			return false
		}
		p.logger.Warn("synthetic token rejected",
			"error", err,
			"path", auth.LoggablePath(r),
			"method", r.Method,
		)
		if softAuth {
			next.ServeHTTP(w, r)
			return true
		}
		p.logAuthEvent(r.Context(), "", false, authReasonInvalidToken)
		p.recordPreRouteReject(r, http.StatusUnauthorized)
		auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorInvalidToken, "Invalid or expired token")
		return true
	}

	if !sc.IsEndpointAllowed(r.Method, r.URL.Path) {
		appid := stringClaim(claims, "appid")
		if appid == "" {
			appid = stringClaim(claims, "azp")
		}
		p.logger.Warn("synthetic endpoint denied",
			"appid", appid,
			"method", r.Method,
			"path", auth.LoggablePath(r),
		)
		p.recordPreRouteReject(r, http.StatusForbidden)
		auth.WriteErrorReq(w, r, http.StatusForbidden, auth.ErrorAuthorizationFailure, "Forbidden")
		return true
	}

	identity := sc.BuildSyntheticIdentity(claims, p.authzSvc)
	p.logger.Info("synthetic authenticated",
		"appid", identity.Metadata()["appid"],
		"method", r.Method,
		"path", auth.LoggablePath(r),
		"roles", identity.Roles(),
	)
	next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
	return true
}

type credentialPosition int

const (
	credentialPositionNone credentialPosition = iota
	credentialPositionHeader
	credentialPositionCookie
	credentialPositionQuery
)

func (p *Provider) extractToken(r *http.Request) (string, credentialPosition, error) {
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		token, isBearer := apikey.BearerCredential(authHeader)
		if !isBearer {
			return "", credentialPositionNone, errors.New("invalid authorization header format")
		}
		return token, credentialPositionHeader, nil
	}
	if cookieToken, err := getSignedCookie(r, accessTokenCookieName, p.config.SessionSecretKey); err == nil && cookieToken != "" {
		return strings.TrimSpace(cookieToken), credentialPositionCookie, nil
	}
	if qToken := r.URL.Query().Get("token"); qToken != "" {
		p.logger.Debug("token accepted from query parameter, consider using Authorization header or cookie",
			"path", auth.LoggablePath(r),
			"remoteAddr", r.RemoteAddr,
		)
		return strings.TrimSpace(qToken), credentialPositionQuery, nil
	}
	return "", credentialPositionNone, nil
}

func (p *Provider) IdentityFromIDPToken(ctx context.Context, token string) (*auth.Subject, error) {
	userInfo, err := p.oidcClient.GetUserInfo(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("idp userinfo: %w", err)
	}

	userID := p.resolveUserID(userInfo)
	if userID != "" && rbac.IsSystemPrincipal(userID) {
		p.logger.Warn("rejecting reserved system: principal resolved from IdP claims")
		return nil, fmt.Errorf("resolved user identity is in the reserved system: namespace")
	}
	if isSentinel(userID) {
		p.logger.Warn("resolved UserID is a sentinel value; OIDC claims may be misconfigured",
			"sub", userInfo.Sub,
		)
	}
	if userID == "" {
		return nil, fmt.Errorf("cannot resolve user identity from IdP claims (both email and sub are absent or sentinel)")
	}
	email := userInfo.Email
	if isSentinel(email) {
		email = userID
	}
	name := userInfo.Name
	if isSentinel(name) {
		name = userID
	}

	gatewayContext := p.buildGatewayContext(userInfo, nil)
	identity, lookupErr := auth.ForIDPAccessToken(ctx,
		auth.ExternalID{Type: externalIDTypeFor(userID), Value: userID},
		email, name, gatewayContext, lookupFor(p.authzSvc))
	if lookupErr != nil {
		return nil, lookupErr
	}

	if p.profileCache != nil && auth.ShouldCacheEnrichment(identity.EnrichmentStatus()) {
		p.profileCache.Put(userID, auth.CachedProfile{
			Profile:          identity.Profile(),
			EnrichmentStatus: identity.EnrichmentStatus(),
			EnrichmentReason: identity.EnrichmentReason(),
		})
	}

	return identity, nil
}

func externalIDTypeFor(userID string) auth.IDType {
	if strings.Contains(userID, "@") {
		return auth.IDEmail
	}
	return auth.IDSub
}

func (p *Provider) cookieSecure() bool {
	return p.config.OIDC == nil || !p.config.OIDC.DevMode
}

func (p *Provider) setAccessCookie(w http.ResponseWriter, token string, maxAgeSeconds int) {
	maxAge := maxAgeSeconds
	switch {
	case maxAge <= 0:
		maxAge = 3600
	case maxAge > 3600:
		p.logger.Warn("SAM access cookie MaxAge clamped to 3600s; cookie will expire before its JWT, forcing re-auth on cookie-authed paths",
			"configured_seconds", maxAgeSeconds,
			"cookie_max_age_seconds", 3600,
		)
		maxAge = 3600
	}
	setSignedCookie(w, cookieOpts{
		Name:     accessTokenCookieName,
		MaxAge:   maxAge,
		Secure:   p.cookieSecure(),
		SameSite: http.SameSiteLaxMode,
	}, token, p.config.SessionSecretKey)
}

func (p *Provider) clearAccessCookie(w http.ResponseWriter) {
	clearSignedCookie(w, accessTokenCookieName, p.cookieSecure())
}

func (p *Provider) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/login", p.handleLogin)
	mux.HandleFunc("GET /api/v1/auth/callback", p.handleCallback)
	mux.Handle("POST /api/v1/auth/refresh", httputil.RequireJSONMiddleware(http.HandlerFunc(p.handleRefresh)))
	mux.Handle("POST /api/v1/auth/logout", httputil.RequireJSONMiddleware(http.HandlerFunc(p.handleLogout)))
	mux.HandleFunc("GET /api/v1/csrf-token", auth.HandleCSRFToken)
	mux.HandleFunc("GET /api/v1/auth/cli-config", p.handleCLIConfig)
}

func (p *Provider) ProviderType() string { return auth.ProviderTypeEnterprise }

func (p *Provider) SetAuthorizationClient(client auth.AuthorizationClient) {
	p.authzSvc.SetAuthorizationClient(client)
}

func (p *Provider) AuthorizationService() *AuthorizationService {
	return p.authzSvc
}

const promptSelectAccount = "select_account"

func loginPromptOptions(q url.Values) []oauth2.AuthCodeOption {
	if q.Get("prompt") != promptSelectAccount {
		return nil
	}
	return []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("prompt", promptSelectAccount)}
}

func (p *Provider) handleLogin(w http.ResponseWriter, r *http.Request) {
	native, ok := p.oidcClient.(*NativeOIDCClient)
	if !ok {
		auth.WriteError(w, http.StatusServiceUnavailable, auth.ErrorServerConfiguration, "Login URL not configured")
		return
	}

	q := r.URL.Query()
	if q.Get("response_mode") == cliResponseModeValue {
		clean, err := IsLoopbackRedirectURI(q.Get("redirect_uri"))
		if err != nil {
			p.logger.Warn("cli-mode login rejected: redirect_uri invalid",
				"error", err)
			auth.WriteError(w, http.StatusBadRequest, auth.ErrorInvalidToken,
				"redirect_uri must be a loopback URL (http://127.0.0.1:<port>/... or http://localhost:<port>/...)")
			return
		}
		if q.Get("confirmed") != cliConfirmedQueryValue {
			renderCLIConsentPage(w, r, clean, q.Get("state"), p.config.FrontendServerURL)
			return
		}
		secure := p.cookieSecure()
		setCLIRedirectCookie(w, clean, p.config.SessionSecretKey, secure)
		setCLIStateCookie(w, q.Get("state"), p.config.SessionSecretKey, secure)
	}

	state, err := getOIDCStateCookie(r, p.config.SessionSecretKey)
	if err != nil || state == "" {
		state = generateState()
		secure := p.cookieSecure()
		setOIDCStateCookie(w, state, p.config.SessionSecretKey, secure)
	}
	authURL, err := native.AuthorizationURL(r.Context(), state, loginPromptOptions(q)...)
	if err != nil {
		p.logger.Warn("OIDC authorization URL unavailable; discovery not ready", "error", err)
		auth.WriteError(w, http.StatusServiceUnavailable, auth.ErrorServiceUnavailable, "Authentication provider not ready, please retry")
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (p *Provider) handleCallback(w http.ResponseWriter, r *http.Request) {
	if dp := p.stateDispatcher.Load(); dp != nil && looksLikeJWS(r.URL.Query().Get("state")) {
		(*dp).Handle(w, r)
		return
	}
	p.handleLocalCallback(w, r)
}

func (p *Provider) handleLocalCallback(w http.ResponseWriter, r *http.Request) {
	logger := p.logger.With("flow", "webui_login")
	ctx := r.Context()

	code := r.URL.Query().Get("code")
	if code == "" {
		p.logAuthEvent(ctx, "", false, authReasonOIDCMissingCode)
		p.writeCallbackError(w, r, &authFailure{status: http.StatusBadRequest, errType: auth.ErrorInvalidToken, detail: "Missing authorization code"})
		return
	}

	if p.requiresStateValidation() {
		expectedState, err := getOIDCStateCookie(r, p.config.SessionSecretKey)
		if err != nil {
			clearOIDCStateCookie(w, p.cookieSecure())
			logger.Warn("state cookie validation failed", "error", err)
			p.logAuthEvent(ctx, "", false, authReasonOIDCStateMismatch)
			p.writeCallbackError(w, r, &authFailure{status: http.StatusBadRequest, errType: auth.ErrorInvalidToken, detail: "Invalid or expired state"})
			return
		}
		if r.URL.Query().Get("state") != expectedState {
			logger.Warn("state mismatch", "expectedLen", len(expectedState), "gotLen", len(r.URL.Query().Get("state")))
			p.logAuthEvent(ctx, "", false, authReasonOIDCStateMismatch)
			p.writeCallbackError(w, r, &authFailure{status: http.StatusBadRequest, errType: auth.ErrorInvalidToken, detail: "State parameter mismatch"})
			return
		}
		clearOIDCStateCookie(w, p.cookieSecure())
	}

	callbackURI := p.config.ExternalAuthCallbackURI
	if callbackURI == "" {
		callbackURI = p.config.FrontendServerURL + "/api/v1/auth/callback"
	}

	tokenResp, err := p.oidcClient.ExchangeCode(ctx, code, callbackURI, "")
	if err != nil {
		logger.Error("code exchange failed", "error", err)
		p.logAuthEvent(ctx, "", false, authReasonOIDCCodeExchangeFail)
		p.writeCallbackError(w, r, &authFailure{status: http.StatusBadGateway, errType: auth.ErrorInvalidToken, detail: "Authentication failed", transient: true})
		return
	}

	samToken, userInfo, roles, fail := p.mintFromOIDCResponse(ctx, w, tokenResp, "", "")
	if fail != nil {
		p.logAuthEvent(ctx, "", false, fail.reason)
		p.writeCallbackError(w, r, fail)
		return
	}

	p.logAuthEvent(ctx, p.resolveUserID(userInfo), true, authReasonOIDCLogin)
	logger.Debug("user authenticated", "sub", userInfo.Sub, "roles", roles)

	if cliRedirect, cliErr := getCLIRedirectCookie(r, p.config.SessionSecretKey); cliErr == nil && cliRedirect != "" {
		clearCLIRedirectCookie(w)
		clean, err := IsLoopbackRedirectURI(cliRedirect)
		if err != nil {
			logger.Warn("cli callback: redirect_uri rejected at callback",
				"error", err, "redirectURI", cliRedirect)
			auth.WriteError(w, http.StatusBadRequest, auth.ErrorInvalidToken, "Invalid CLI redirect_uri")
			return
		}
		cliState, stateErr := getCLIStateCookie(r, p.config.SessionSecretKey)
		if stateErr != nil {
			logger.Warn("cli callback: state cookie invalid; loopback will see empty state",
				"error", stateErr)
			cliState = ""
		}
		clearCLIStateCookie(w)
		finalURL, err := buildCLICallbackURL(clean, tokenResp.AccessToken, samToken,
			tokenResp.RefreshToken, cliState, int(p.config.SAMAccessToken.TTL().Seconds()))
		if err != nil {
			logger.Error("cli callback: build redirect failed", "error", err)
			auth.WriteError(w, http.StatusInternalServerError, auth.ErrorServerConfiguration, "Failed to build CLI redirect")
			return
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		http.Redirect(w, r, finalURL, http.StatusFound)
		return
	}

	redirectURL := p.config.FrontendRedirectURL
	if redirectURL == "" {
		redirectURL = "/"
	}

	fragment := url.Values{}
	fragment.Set("access_token", tokenResp.AccessToken)
	if tokenResp.RefreshToken != "" {
		fragment.Set("refresh_token", tokenResp.RefreshToken)
	}
	fragment.Set("sam_access_token", samToken)

	finalURL := redirectURL + "#" + fragment.Encode()
	http.Redirect(w, r, finalURL, http.StatusFound)
}

func (p *Provider) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := readJSON(r, &req); err != nil {
		auth.WriteError(w, http.StatusBadRequest, auth.ErrorInvalidToken, "Invalid request body")
		return
	}

	if req.RefreshToken == "" {
		auth.WriteError(w, http.StatusBadRequest, auth.ErrorAuthenticationRequired, "Missing refresh_token")
		return
	}

	sessionSubject, fail := p.checkSessionIdle(r)
	if fail != nil {
		auth.WriteError(w, fail.status, fail.errType, fail.detail)
		return
	}

	ctx := r.Context()

	tokenResp, err := p.oidcClient.RefreshToken(ctx, req.RefreshToken)
	if err != nil {
		p.logger.Warn("token refresh failed", "error", err)
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		auth.WriteError(w, http.StatusBadGateway, auth.ErrorTokenRefreshFailed, "Token refresh failed")
		return
	}

	samToken, _, _, fail := p.mintFromOIDCResponse(ctx, w, tokenResp, " during refresh", sessionSubject)
	if fail != nil {
		auth.WriteError(w, fail.status, fail.errType, fail.detail)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":     tokenResp.AccessToken,
		"refresh_token":    tokenResp.RefreshToken,
		"sam_access_token": samToken,
		"token_type":       "Bearer",
		"expires_in":       int(p.config.SAMAccessToken.TTL().Seconds()),
	})
}

func (p *Provider) checkSessionIdle(r *http.Request) (subject string, fail *authFailure) {
	idle := p.config.SAMAccessToken.IdleTimeout()
	if idle == 0 {
		return "", nil
	}
	token, _, err := p.extractToken(r)
	if err != nil || token == "" {
		return "", &authFailure{status: http.StatusUnauthorized, errType: auth.ErrorSessionExpired, detail: "Current session token required to refresh"}
	}
	subject, issuedAt, err := SAMTokenSession(token, p.signer)
	if err != nil {
		return "", &authFailure{status: http.StatusUnauthorized, errType: auth.ErrorInvalidToken, detail: "Invalid session token"}
	}
	if sinceMint := time.Since(issuedAt); sinceMint > idle {
		p.logger.Warn("token refresh refused: session idle timeout exceeded",
			"idleSeconds", int(idle.Seconds()),
			"sinceMintSeconds", int(sinceMint.Seconds()),
		)
		return "", &authFailure{status: http.StatusUnauthorized, errType: auth.ErrorSessionExpired, detail: "Session expired due to inactivity, please log in again"}
	}
	return subject, nil
}

type authFailure struct {
	status    int
	errType   auth.ErrorType
	detail    string
	transient bool
	reason    string
}

func (p *Provider) mintFromOIDCResponse(
	ctx context.Context,
	w http.ResponseWriter,
	tokenResp *TokenResponse,
	logPrefix string,
	expectSubject string,
) (samToken string, userInfo *UserInfo, roles []string, fail *authFailure) {
	userInfo, err := p.oidcClient.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		idpStatus, reason := classifyUserInfoError(err)
		logArgs := []any{"flow", logPrefix, "errorType", fmt.Sprintf("%T", err), "detail", reason}
		if idpStatus != 0 {
			logArgs = append(logArgs, "httpStatus", idpStatus)
		}
		p.logger.Error("user info fetch failed", logArgs...)
		return "", nil, nil, &authFailure{status: http.StatusBadGateway, errType: auth.ErrorServiceUnavailable, detail: "Failed to get user info", transient: true, reason: authReasonOIDCUserinfoFail}
	}

	idTokenClaims, err := p.verifyIDTokenClaims(ctx, tokenResp.IDToken)
	if err != nil {
		p.logger.Error(idTokenVerificationFailedMsg, "flow", logPrefix, "error", err)
		return "", nil, nil, &authFailure{status: http.StatusUnauthorized, errType: auth.ErrorInvalidToken, detail: idTokenVerificationFailedMsg, reason: authReasonOIDCIDTokenFail}
	}
	gatewayContext := p.buildGatewayContext(userInfo, idTokenClaims)
	userID := p.resolveUserID(userInfo)
	if expectSubject != "" && userID != expectSubject {
		p.logger.Warn("rejecting refresh: session token subject does not match the refreshed identity", "flow", logPrefix)
		return "", nil, nil, &authFailure{status: http.StatusUnauthorized, errType: auth.ErrorInvalidToken, detail: "Session token does not match the refreshed identity", reason: authReasonInvalidToken}
	}
	if userID != "" && rbac.IsSystemPrincipal(userID) {
		p.logger.Warn("rejecting reserved system: principal resolved from IdP claims", "flow", logPrefix)
		return "", nil, nil, &authFailure{status: http.StatusUnauthorized, errType: auth.ErrorInvalidToken, detail: "resolved identity is in the reserved system namespace", reason: authReasonOIDCReservedSubject}
	}
	if isSentinel(userID) {
		p.logger.Warn("minting SAM token for sentinel UserID; check OIDC claims config",
			"sub", userInfo.Sub,
		)
	}
	upserted := p.provisionUserAtLogin(ctx, userInfo, idTokenClaims)
	if upserted.ID != "" {
		gatewayContext["sam_user_id"] = upserted.ID
		if upserted.RolesChanged {
			gatewayContext["roles_changed"] = true
		}
	}

	lookup, err := p.authzSvc.Lookup(ctx, userID, gatewayContext)
	if err != nil {
		p.logger.Error("authz lookup failed", "flow", logPrefix, "userID", userID, "error", err)
		return "", nil, nil, &authFailure{status: http.StatusServiceUnavailable, errType: auth.ErrorServiceUnavailable, detail: "Authorization service unavailable", transient: true, reason: authReasonOIDCSAMMintFail}
	}
	roles = lookup.Roles

	email := userInfo.Email
	if isSentinel(email) {
		email = userID
	}
	name := userInfo.Name
	if isSentinel(name) {
		name = userID
	}

	samToken, err = MintSAMToken(p.signer, MintClaims{
		UserID:         userID,
		Email:          email,
		Name:           name,
		Roles:          roles,
		Scopes:         lookup.Scopes,
		Provider:       p.config.ExternalAuthProvider,
		PlatformUserID: cmp.Or(upserted.ID, lookup.MatchedUserID()),
	}, p.config.SAMAccessToken.TTL())
	if err != nil {
		p.logger.Error("SAM token mint failed", "flow", logPrefix, "userID", userID, "error", err)
		return "", nil, nil, &authFailure{status: http.StatusInternalServerError, errType: auth.ErrorServerConfiguration, detail: "Token creation failed", reason: authReasonOIDCSAMMintFail}
	}

	if p.profileCache != nil && auth.ShouldCacheEnrichment(lookup.EnrichmentStatus) {
		p.profileCache.Put(userID, auth.CachedProfile{
			Profile:          lookup.Profile,
			EnrichmentStatus: lookup.EnrichmentStatus,
			EnrichmentReason: lookup.EnrichmentReason,
		})
	}

	p.setAccessCookie(w, samToken, int(p.config.SAMAccessToken.TTL().Seconds()))
	return samToken, userInfo, roles, nil
}

func classifyUserInfoError(err error) (idpStatus int, reason string) {
	var statusErr *userInfoStatusError
	var netErr net.Error
	switch {
	case errors.As(err, &statusErr):
		return statusErr.status, "unexpected status"
	case errors.Is(err, errInvalidUserInfoResponse):
		return 0, "invalid response"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return 0, "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return 0, "connection refused"
	default:
		return 0, "request failed"
	}
}

func (p *Provider) writeCallbackError(w http.ResponseWriter, r *http.Request, fail *authFailure) {
	redirectURL := p.config.FrontendRedirectURL
	if redirectURL == "" {
		auth.WriteError(w, fail.status, fail.errType, fail.detail)
		return
	}
	errID := idgen.New()
	logArgs := []any{"httpStatus", fail.status, "errorType", string(fail.errType), "errorId", errID, "detail", fail.detail}
	if fail.status >= 500 {
		p.logger.Error("auth callback failed", logArgs...)
	} else {
		p.logger.Warn("auth callback rejected", logArgs...)
	}
	frag := url.Values{}
	frag.Set("error", string(fail.errType))
	frag.Set("error_description", fail.detail)
	frag.Set("errorId", errID)
	if fail.transient {
		frag.Set("retryable", "1")
	}
	http.Redirect(w, r, redirectURL+"#"+frag.Encode(), http.StatusFound)
}

func (p *Provider) handleLogout(w http.ResponseWriter, r *http.Request) {
	p.clearAccessCookie(w)

	userID := ""
	if id, ok := auth.SubjectFromContext(r.Context()); ok && id != nil {
		userID = id.UserID()
	}
	p.logAuthEvent(r.Context(), userID, true, authReasonLogout)

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "logged out",
	})
}

func (p *Provider) buildGatewayContext(userInfo *UserInfo, idTokenClaims map[string]any) map[string]any {
	ctx := map[string]any{
		"gateway_id":    p.gatewayID,
		"oidc_provider": p.config.ExternalAuthProvider,
	}

	userInfoClaims := userInfoClaimsMap(userInfo)

	if userInfoClaims == nil && idTokenClaims == nil {
		return ctx
	}

	merged := make(map[string]any, len(userInfoClaims)+len(idTokenClaims))
	maps.Copy(merged, userInfoClaims)
	maps.Copy(merged, idTokenClaims)
	ctx["idp_claims"] = merged

	return ctx
}

const userUpsertTimeout = 5 * time.Second

func (p *Provider) provisionUserAtLogin(ctx context.Context, userInfo *UserInfo, idTokenClaims map[string]any) auth.UserUpsertResult {
	if p.userUpsertClient == nil {
		return auth.UserUpsertResult{}
	}
	rec, ok := p.buildUserRecord(userInfo, idTokenClaims)
	if !ok {
		p.logger.Warn("user store provisioning skipped: no stable identity in OIDC response")
		return auth.UserUpsertResult{}
	}
	if p.userUpsertSem != nil {
		select {
		case p.userUpsertSem <- struct{}{}:
			defer func() { <-p.userUpsertSem }()
		default:
			p.logger.Warn("user store provisioning skipped: pool full", "issuer", rec.Issuer, "sub", rec.Sub)
			return auth.UserUpsertResult{}
		}
	}
	bgCtx, cancel := context.WithTimeout(ctx, userUpsertTimeout)
	defer cancel()
	res, err := p.userUpsertClient.Upsert(bgCtx, rec)
	if err != nil {
		p.logger.Warn("user store provisioning failed", "issuer", rec.Issuer, "sub", rec.Sub, "error", err)
		return auth.UserUpsertResult{}
	}
	return res
}

func (p *Provider) buildUserRecord(userInfo *UserInfo, idTokenClaims map[string]any) (auth.UserRecord, bool) {
	if userInfo == nil {
		return auth.UserRecord{}, false
	}
	issuer := ""
	if p.config.OIDC != nil {
		issuer = p.config.OIDC.Issuer
	}
	if issuer == "" {
		issuer = p.config.ExternalAuthProvider
	}
	sub := userInfo.Sub
	if issuer == "" || sub == "" || isSentinel(sub) {
		return auth.UserRecord{}, false
	}
	if rbac.IsSystemPrincipal(sub) {
		p.logger.Warn("user record: rejecting reserved system: subject from IdP", "issuer", issuer)
		return auth.UserRecord{}, false
	}

	merged := mergeClaims(userInfo, idTokenClaims)

	email := userInfo.Email
	if isSentinel(email) {
		email = ""
	}
	if email != "" && !emailVerified(merged) {
		p.logger.Warn("user record: dropping unverified email from identity", "issuer", issuer, "sub", sub)
		email = ""
	}

	displayName := userInfo.Name
	if isSentinel(displayName) {
		displayName = ""
	}
	if displayName == "" {
		if s, ok := merged["name"].(string); ok && !isSentinel(s) {
			displayName = s
		}
	}

	userName, _ := merged["preferred_username"].(string)
	if isSentinel(userName) {
		userName = ""
	}

	source := ""
	if len(merged) > 0 {
		if b, err := json.Marshal(merged); err == nil {
			source = string(b)
		} else {
			p.logger.Warn("user record: source marshal failed", "error", err)
		}
	}

	return auth.UserRecord{
		Issuer:       issuer,
		Sub:          sub,
		OIDCProvider: p.config.ExternalAuthProvider,
		DisplayName:  displayName,
		UserName:     userName,
		Email:        email,
		Phone:        verifiedPhone(merged),
		Groups:       toStringSlice(merged[p.groupClaimKey()]),
		Source:       source,
	}, true
}

func (p *Provider) groupClaimKey() string {
	if c := p.config.AuthorizationService.IDPClaimsConfig; c != nil && c.ClaimKey != "" {
		return c.ClaimKey
	}
	return "groups"
}

func emailVerified(merged map[string]any) bool {
	v, present := merged["email_verified"]
	if !present {
		return true
	}
	switch tv := v.(type) {
	case bool:
		return tv
	case string:
		return strings.EqualFold(strings.TrimSpace(tv), "true")
	default:
		return false
	}
}

func verifiedPhone(merged map[string]any) string {
	verified := false
	switch v := merged["phone_number_verified"].(type) {
	case bool:
		verified = v
	case string:
		verified = v == "true"
	}
	if !verified {
		return ""
	}
	phone, _ := merged["phone_number"].(string)
	return phone
}

func mergeClaims(userInfo *UserInfo, idTokenClaims map[string]any) map[string]any {
	base := userInfoClaimsMap(userInfo)
	merged := make(map[string]any, len(base)+len(idTokenClaims))
	maps.Copy(merged, base)
	maps.Copy(merged, idTokenClaims)
	return merged
}

func (p *Provider) verifyIDTokenClaims(ctx context.Context, idToken string) (map[string]any, error) {
	if idToken == "" {
		return nil, nil
	}
	if p.idTokenVerifier == nil {
		return nil, fmt.Errorf("id_token verifier not configured")
	}
	claims, err := p.idTokenVerifier.VerifyIDToken(ctx, idToken)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

func userInfoClaimsMap(userInfo *UserInfo) map[string]any {
	if userInfo == nil {
		return nil
	}
	if userInfo.RawResponse != nil {
		return userInfo.RawResponse
	}
	if userInfo.Claims != nil {
		return userInfo.Claims
	}
	if userInfo.Sub == "" && userInfo.Email == "" && userInfo.Name == "" {
		return nil
	}
	return map[string]any{
		"sub":   userInfo.Sub,
		"email": userInfo.Email,
		"name":  userInfo.Name,
	}
}

func (p *Provider) resolveUserID(userInfo *UserInfo) string {
	if email := userInfo.Email; !isSentinel(email) {
		return email
	}
	if sub := userInfo.Sub; !isSentinel(sub) {
		return sub
	}
	return ""
}

func isSentinel(s string) bool {
	if s == "" {
		return true
	}
	switch strings.ToLower(s) {
	case "unknown", "null", "none":
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

var _ auth.AuthProvider = (*Provider)(nil)

func looksLikeJWS(s string) bool {
	if len(s) < 3 || s[:3] != "eyJ" {
		return false
	}
	dots := 0
	for i := range len(s) {
		if s[i] == '.' {
			dots++
			if dots > 2 {
				return false
			}
		}
	}
	return dots == 2
}

func LooksLikeJWSForTest(s string) bool { return looksLikeJWS(s) }
