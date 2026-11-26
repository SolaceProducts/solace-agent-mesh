// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/atrest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
	"github.com/golang-jwt/jwt/v5"
)

const defaultRefreshTokenTTLSeconds = 30 * 24 * 3600

const errRefreshTokenInvalidOrExpired = "Refresh token is invalid or expired"

type Config struct {
	EnableAuth bool

	ExternalAuthProvider string

	RequirePKCE bool

	OAuthStateTTLSeconds int

	OAuthCodeTTLSeconds int

	Host string

	Port int

	UserIDClaim string

	Logger *slog.Logger

	HTTPClient *http.Client

	IDPClient IDPClient

	SAMTokenMinter SAMTokenMinter

	AuthzResolver AuthzResolver

	Issuer string

	GatewayID string

	RefreshTokenTTLSeconds int

	AllowedRedirectURIs []string

	StateRedirectionEnabled bool

	OAuthDispatcherURL string

	StateSigner *trust.StateSigner

	StateVerifier *trust.StateVerifier

	Records   RecordRepo
	RecordBox *atrest.Box
}

type oauthState struct {
	ClientRedirectURI   string
	ClientState         string
	CodeChallenge       string
	CodeChallengeMethod string
	CodeVerifier        string
	CreatedAt           time.Time
	TTL                 time.Duration
}

type oauthCode struct {
	AccessToken     string
	IDPRefreshToken string
	CodeChallenge   string
	RedirectURI     string
	CreatedAt       time.Time
	TTL             time.Duration
}

type mcpRefreshTokenEntry struct {
	IDPRefreshToken string
	CreatedAt       time.Time
	TTL             time.Duration
}

const maxMapEntries = 10000

type Handler struct {
	cfg        Config
	logger     *slog.Logger
	httpClient *http.Client
	idp        IDPClient
	minter     SAMTokenMinter
	authz      AuthzResolver
	store      recordStore
}

func New(cfg Config) *Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "mcp-oauth2")

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	if cfg.OAuthStateTTLSeconds <= 0 {
		cfg.OAuthStateTTLSeconds = 300
	}
	if cfg.OAuthCodeTTLSeconds <= 0 {
		cfg.OAuthCodeTTLSeconds = 300
	}
	if cfg.RefreshTokenTTLSeconds <= 0 {
		cfg.RefreshTokenTTLSeconds = defaultRefreshTokenTTLSeconds
	}

	if cfg.EnableAuth && len(cfg.AllowedRedirectURIs) == 0 {
		logger.Warn("MCP OAuth: no allowed_redirect_uris configured — any client redirect_uri will be accepted; combined with the unauthenticated /oauth/register endpoint this enables a DCR-redirect phishing chain. Configure mcp_auth.allowed_redirect_uris with the URIs your MCP clients use.")
	}

	if cfg.StateRedirectionEnabled {
		if cfg.StateSigner == nil {
			logger.Warn("MCP OAuth: state_redirection_enabled=true requires StateSigner; falling back to legacy callback")
			cfg.StateRedirectionEnabled = false
		}
		if cfg.StateVerifier == nil {
			logger.Warn("MCP OAuth: state_redirection_enabled=true requires StateVerifier; falling back to legacy callback")
			cfg.StateRedirectionEnabled = false
		}
		if cfg.OAuthDispatcherURL == "" {
			logger.Warn("MCP OAuth: state_redirection_enabled=true requires oauth_dispatcher_url; falling back to legacy callback")
			cfg.StateRedirectionEnabled = false
		}
	}

	var store recordStore = newMemoryStore()
	if cfg.Records != nil && cfg.RecordBox != nil {
		store = &dbStore{repo: cfg.Records, box: cfg.RecordBox, gatewayID: cfg.GatewayID, logger: logger}
	}

	return &Handler{
		cfg:        cfg,
		logger:     logger,
		httpClient: httpClient,
		idp:        cfg.IDPClient,
		minter:     cfg.SAMTokenMinter,
		authz:      cfg.AuthzResolver,
		store:      store,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+PathAuthorize, h.HandleAuthorize)
	mux.HandleFunc("GET "+PathCallback, h.HandleCallback)
	mux.HandleFunc("GET "+PathFinish, h.HandleFinish)
	mux.HandleFunc("POST "+PathToken, h.HandleToken)
	mux.HandleFunc("POST "+PathRegister, h.HandleRegister)
	mux.HandleFunc("GET "+PathWellKnown, h.HandleMetadata)
	mux.HandleFunc("GET "+PathProtectedResource, h.HandleProtectedResourceMetadata)
}

func (h *Handler) baseURL() string {
	if h.cfg.Issuer != "" {
		return strings.TrimRight(h.cfg.Issuer, "/")
	}
	if v := os.Getenv("MCP_EXTERNAL_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return fmt.Sprintf("http://%s:%d", h.cfg.Host, h.cfg.Port)
}

func (h *Handler) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")

	if redirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing redirect_uri")
		return
	}

	if !h.redirectURIAllowed(redirectURI) {
		h.logger.Warn("MCP OAuth: redirect_uri rejected — not on allowlist",
			"redirectURI", redirectURI)
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"redirect_uri is not on the entrypoint's allowlist")
		return
	}

	if h.cfg.RequirePKCE {
		if codeChallenge == "" {
			h.logger.Warn("MCP OAuth: code_challenge required but not provided")
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		if codeChallengeMethod == "" {
			h.logger.Warn("MCP OAuth: code_challenge_method required but not provided")
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		if codeChallengeMethod != "S256" {
			h.logger.Warn("MCP OAuth: unsupported code_challenge_method", "method", codeChallengeMethod)
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
	}

	if h.cfg.RequirePKCE {
		h.logger.Debug("MCP OAuth authorize: PKCE enforced",
			"method", codeChallengeMethod, "redirectURI", redirectURI)
	}

	internalState := generateSecureToken(32)

	gwVerifier := generateSecureToken(32)
	gwChallenge := ComputeS256Challenge(gwVerifier)

	live, err := h.store.countLiveStates(r.Context())
	if err != nil {
		h.logger.Error("MCP OAuth: count authorization states failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}
	if live >= maxMapEntries {
		h.logger.Error("MCP OAuth: too many pending authorization states")
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}
	if err = h.store.putState(r.Context(), internalState, &oauthState{
		ClientRedirectURI:   redirectURI,
		ClientState:         state,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		CodeVerifier:        gwVerifier,
		CreatedAt:           time.Now(),
		TTL:                 time.Duration(h.cfg.OAuthStateTTLSeconds) * time.Second,
	}); err != nil {
		h.logger.Error("MCP OAuth: store authorization state failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}

	h.logger.Info("stored OAuth state", "stateFP", stateFingerprint(internalState))

	stateForIDP := internalState
	redirectURIForIDP := h.baseURL() + PathCallback
	if h.cfg.StateRedirectionEnabled {
		sum := sha256.Sum256([]byte(gwVerifier))
		verifierHashHex := hex.EncodeToString(sum[:])
		claims := trust.StateClaims{
			TargetLinkURI:      h.baseURL() + PathFinish,
			GwPKCEVerifierHash: verifierHashHex,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    h.cfg.GatewayID,
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(h.cfg.OAuthStateTTLSeconds) * time.Second)),
				ID:        idgen.New(),
			},
		}
		signedState, mintErr := h.cfg.StateSigner.Mint(claims)
		if mintErr != nil {
			h.dropState(r, internalState)
			h.logger.Error("MCP OAuth: state JWT mint failed", "error", mintErr)
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "")
			return
		}
		stateForIDP = signedState
		redirectURIForIDP = h.cfg.OAuthDispatcherURL
	}
	idpURL, err := h.idp.BuildAuthURL(r.Context(), stateForIDP, redirectURIForIDP, gwChallenge, "S256")
	if err != nil {
		h.dropState(r, internalState)
		h.logger.Error("MCP OAuth: BuildAuthURL failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "OIDC discovery unavailable")
		return
	}
	h.logger.Info("MCP OAuth: redirecting to IdP", "stateFP", stateFingerprint(internalState))
	http.Redirect(w, r, idpURL, http.StatusFound)
}

func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	gatewayCode := q.Get("code")
	returnedState := q.Get("state")
	idpError := q.Get("error")
	idpErrorDescription := q.Get("error_description")

	stateData, err := h.store.takeState(r.Context(), returnedState)
	if err != nil {
		h.logger.Error("MCP OAuth callback: read authorization state failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}
	if stateData == nil {
		if idpError != "" {
			h.logger.Info("MCP OAuth callback: IdP returned error with no matching state",
				"idpError", idpError, "stateFP", stateFingerprint(returnedState))
			writeOAuthError(w, http.StatusBadRequest, idpError, idpErrorDescription)
			return
		}
		h.logger.Warn("MCP OAuth callback: invalid or expired state", "stateFP", stateFingerprint(returnedState))
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}

	h.logger.Info("retrieved OAuth state", "stateFP", stateFingerprint(returnedState))

	if idpError != "" {
		h.logger.Info("MCP OAuth: IdP returned error", "idpError", idpError)
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, idpError, idpErrorDescription)
		return
	}

	if gatewayCode == "" {
		h.logger.Warn("MCP OAuth callback: missing gateway code")
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "invalid_request", "Missing code parameter")
		return
	}

	redirectURI := h.baseURL() + PathCallback
	h.completeMCPFlow(w, r, stateData, gatewayCode, redirectURI)
}

func (h *Handler) completeMCPFlow(
	w http.ResponseWriter,
	r *http.Request,
	stateData *oauthState,
	idpCode string,
	redirectURI string,
) {
	codeVerifier := stateData.CodeVerifier
	idpTokens, err := h.idp.ExchangeCode(r.Context(), idpCode, redirectURI, codeVerifier)
	if err != nil {
		h.logger.Error("MCP OAuth: IdP code exchange failed", "error", err)
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "server_error", "IdP code exchange failed")
		return
	}

	userInfo, err := h.idp.GetUserInfo(r.Context(), idpTokens.AccessToken)
	if err != nil {
		h.logger.Error("MCP OAuth: userinfo failed", "error", err)
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "server_error", "userinfo failed")
		return
	}

	if isSentinel(userInfo.Sub) {
		h.logger.Error("MCP OAuth: IdP userinfo carried a sentinel sub — refusing to mint",
			"sub", userInfo.Sub)
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "server_error", "userinfo missing subject")
		return
	}
	scrubSentinelProfile(userInfo)

	userID := canonicalUserID(userInfo, h.cfg.UserIDClaim)
	if userID != "" && rbac.IsSystemPrincipal(userID) {
		h.logger.Warn("MCP OAuth: rejecting reserved system: principal resolved from IdP claims")
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "access_denied", "resolved identity is in the reserved system namespace")
		return
	}

	var roles, scopes []string
	var platformUserID string
	if h.authz != nil {
		gatewayContext := map[string]any{
			"gateway_id":    h.cfg.GatewayID,
			"oidc_provider": h.cfg.ExternalAuthProvider,
			"idp_claims":    userInfo.Raw,
			"source":        "mcp_oauth_login",
		}
		var lookupErr error
		roles, scopes, platformUserID, lookupErr = h.authz.Lookup(r.Context(), userID, gatewayContext)
		if lookupErr != nil {
			h.logger.Error("MCP OAuth: authz lookup failed", "error", lookupErr)
			redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "server_error", "authorization service unavailable")
			return
		}
	}

	samToken, err := h.minter.MintForUser(userID, platformUserID, userInfo.Email, userInfo.Name, roles, scopes, h.minter.TTL())
	if err != nil {
		h.logger.Error("MCP OAuth: SAM token mint failed", "error", err)
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "server_error", "token mint failed")
		return
	}

	mcpAuthCode := generateSecureToken(32)

	if err = h.store.putCode(r.Context(), mcpAuthCode, &oauthCode{
		AccessToken:     samToken,
		IDPRefreshToken: idpTokens.RefreshToken,
		CodeChallenge:   stateData.CodeChallenge,
		RedirectURI:     stateData.ClientRedirectURI,
		CreatedAt:       time.Now(),
		TTL:             time.Duration(h.cfg.OAuthCodeTTLSeconds) * time.Second,
	}); err != nil {
		h.logger.Error("MCP OAuth: store authorization code failed", "error", err)
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "server_error", "")
		return
	}

	clientURL, err := url.Parse(stateData.ClientRedirectURI)
	if err != nil {
		h.logger.Error("MCP OAuth callback: invalid client redirect URI", "uri", stateData.ClientRedirectURI, "error", err)
		redirectClientError(w, r, stateData.ClientRedirectURI, stateData.ClientState, "server_error", "")
		return
	}
	clientQuery := clientURL.Query()
	clientQuery.Set("code", mcpAuthCode)
	if stateData.ClientState != "" {
		clientQuery.Set("state", stateData.ClientState)
	}
	clientURL.RawQuery = clientQuery.Encode()

	h.logger.Info("MCP OAuth: callback complete, redirecting to client",
		"client_redirect_uri", stateData.ClientRedirectURI)

	http.Redirect(w, r, clientURL.String(), http.StatusFound)
}

func (h *Handler) HandleToken(w http.ResponseWriter, r *http.Request) {
	forbidCaching(w)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}

	grantType := r.FormValue("grant_type")

	switch grantType {
	case "authorization_code":
		h.handleAuthorizationCodeGrant(w, r)
	case "refresh_token":
		h.handleRefreshTokenGrant(w, r)
	default:
		h.logger.Warn("MCP OAuth token: unsupported grant_type", "grant_type", grantType)
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (h *Handler) handleAuthorizationCodeGrant(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	codeVerifier := r.FormValue("code_verifier")

	if code == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}

	codeData, err := h.store.takeCode(r.Context(), code)
	if err != nil {
		h.logger.Error("MCP OAuth token: read authorization code failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}
	if codeData == nil {
		h.logger.Warn("MCP OAuth token: invalid or expired authorization code")
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "")
		return
	}

	if redirectURI != codeData.RedirectURI {
		h.logger.Warn("MCP OAuth token: redirect URI mismatch",
			"expected", codeData.RedirectURI, "got", redirectURI)
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "")
		return
	}

	if err = h.verifyPKCE(codeData.CodeChallenge, codeVerifier); err != nil {
		h.logger.Warn("MCP OAuth token: PKCE verification failed", "error", err)
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "")
		return
	}

	mcpRefreshToken, err := h.storeMCPRefreshToken(r.Context(), codeData.IDPRefreshToken)
	if err != nil {
		h.logger.Error("MCP OAuth token: store refresh token failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}

	h.logger.Info("MCP OAuth: successfully exchanged authorization code for tokens")

	resp := map[string]any{
		"access_token": codeData.AccessToken,
		"token_type":   "Bearer",
		"expires_in":   int(h.minter.TTL().Seconds()),
	}
	if mcpRefreshToken != "" {
		resp["refresh_token"] = mcpRefreshToken
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) storeMCPRefreshToken(ctx context.Context, idpRefreshToken string) (string, error) {
	if idpRefreshToken == "" {
		return "", nil
	}
	mcpRefreshToken := generateSecureToken(32)
	if err := h.store.putRefresh(ctx, mcpRefreshToken, h.newRefreshEntry(idpRefreshToken)); err != nil {
		return "", err
	}
	return mcpRefreshToken, nil
}

func (h *Handler) newRefreshEntry(idpRefreshToken string) *mcpRefreshTokenEntry {
	return &mcpRefreshTokenEntry{
		IDPRefreshToken: idpRefreshToken,
		CreatedAt:       time.Now(),
		TTL:             time.Duration(h.cfg.RefreshTokenTTLSeconds) * time.Second,
	}
}

func (h *Handler) dropState(r *http.Request, key string) {
	if _, err := h.store.takeState(r.Context(), key); err != nil {
		h.logger.Warn("MCP OAuth: drop authorization state failed", "error", err)
	}
}

func (h *Handler) handleRefreshTokenGrant(w http.ResponseWriter, r *http.Request) {
	clientRefreshToken := r.FormValue("refresh_token")
	if clientRefreshToken == "" {
		h.logger.Warn("MCP OAuth token: missing refresh_token parameter")
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing refresh_token parameter")
		return
	}

	entry, err := h.store.getRefresh(r.Context(), clientRefreshToken)
	if err != nil {
		h.logger.Error("MCP OAuth: read refresh token failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}
	if entry == nil {
		h.logger.Warn("MCP OAuth: refresh_token not recognized")
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", errRefreshTokenInvalidOrExpired)
		return
	}
	idpRefreshToken := entry.IDPRefreshToken

	idpTokens, err := h.idp.RefreshToken(r.Context(), idpRefreshToken)
	if err != nil {
		if code := toolauth.ClassifyOAuth2Error(err); code != "" {
			h.logger.Warn("MCP OAuth: IdP refresh failed", "oauthErrorCode", code)
		} else {
			h.logger.Warn("MCP OAuth: IdP refresh failed", "error", err)
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", errRefreshTokenInvalidOrExpired)
		return
	}

	userInfo, err := h.idp.GetUserInfo(r.Context(), idpTokens.AccessToken)
	if err != nil {
		h.logger.Error("MCP OAuth: userinfo failed during refresh", "error", err)
		writeOAuthError(w, http.StatusBadGateway, "server_error", "")
		return
	}

	if isSentinel(userInfo.Sub) {
		h.logger.Error("MCP OAuth refresh: IdP userinfo carried a sentinel sub — refusing to remint",
			"sub", userInfo.Sub)
		writeOAuthError(w, http.StatusBadGateway, "invalid_grant", "userinfo missing subject")
		return
	}
	scrubSentinelProfile(userInfo)

	userID := canonicalUserID(userInfo, h.cfg.UserIDClaim)
	if userID != "" && rbac.IsSystemPrincipal(userID) {
		h.logger.Warn("MCP OAuth: rejecting reserved system: principal resolved from IdP claims during refresh")
		writeOAuthError(w, http.StatusForbidden, "access_denied", "")
		return
	}

	var roles, scopes []string
	var platformUserID string
	if h.authz != nil {
		gatewayContext := map[string]any{
			"gateway_id":    h.cfg.GatewayID,
			"oidc_provider": h.cfg.ExternalAuthProvider,
			"idp_claims":    userInfo.Raw,
			"source":        "mcp_token_refresh",
		}
		var lookupErr error
		roles, scopes, platformUserID, lookupErr = h.authz.Lookup(r.Context(), userID, gatewayContext)
		if lookupErr != nil {
			h.logger.Error("MCP OAuth: authz lookup failed during refresh", "error", lookupErr)
			writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
			return
		}
	}

	samToken, err := h.minter.MintForUser(userID, platformUserID, userInfo.Email, userInfo.Name, roles, scopes, h.minter.TTL())
	if err != nil {
		h.logger.Error("MCP OAuth: SAM token remint failed during refresh", "error", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}

	newIDPRefreshToken := idpTokens.RefreshToken
	if newIDPRefreshToken == "" {
		newIDPRefreshToken = idpRefreshToken
	}
	mcpRefreshToken := generateSecureToken(32)
	rotated, err := h.store.rotateRefresh(r.Context(), clientRefreshToken, mcpRefreshToken, h.newRefreshEntry(newIDPRefreshToken))
	if err != nil {
		h.logger.Error("MCP OAuth: rotate refresh token failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}
	if !rotated {
		h.logger.Warn("MCP OAuth: refresh_token already rotated by a concurrent refresh")
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", errRefreshTokenInvalidOrExpired)
		return
	}

	h.logger.Info("MCP OAuth: refreshed access token via IdP")
	resp := map[string]any{
		"access_token": samToken,
		"token_type":   "Bearer",
		"expires_in":   int(h.minter.TTL().Seconds()),
	}
	resp["refresh_token"] = mcpRefreshToken
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	forbidCaching(w)
	var reqBody map[string]any
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			h.logger.Warn("client registration: failed to read body", "error", err)
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "failed to read request body")
			return
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &reqBody); err != nil {
				h.logger.Warn("client registration: invalid JSON body", "error", err)
				writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
				return
			}
		}
	}

	clientID := generateSecureToken(16)
	clientSecret := generateSecureToken(32)

	resp := map[string]any{
		"client_id":                  clientID,
		"client_secret":              clientSecret,
		"client_id_issued_at":        time.Now().UTC().Unix(),
		"client_secret_expires_at":   0,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}

	if h.cfg.RequirePKCE {
		resp["require_pkce"] = true
	}

	if uris, ok := reqBody["redirect_uris"]; ok {
		resp["redirect_uris"] = uris
	}

	h.logger.Info("dynamic client registration", "clientID", clientID)
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) HandleMetadata(w http.ResponseWriter, _ *http.Request) {
	base := h.baseURL()

	metadata := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + PathAuthorize,
		"token_endpoint":                        base + PathToken,
		"registration_endpoint":                 base + PathRegister,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic", "none"},
		"code_challenge_methods_supported":      []string{"S256"},
	}

	if h.cfg.RequirePKCE {
		metadata["require_pkce"] = true
	}

	writeJSON(w, http.StatusOK, metadata)
}

func (h *Handler) HandleProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	base := h.baseURL()
	resource := strings.TrimRight(base, "/")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"openid", "email", "profile"},
	})
}

func (h *Handler) verifyPKCE(codeChallenge, codeVerifier string) error {
	if h.cfg.RequirePKCE {
		if codeChallenge == "" {
			return fmt.Errorf("authorization code missing code_challenge")
		}
		if codeVerifier == "" {
			return fmt.Errorf("missing code_verifier for PKCE")
		}
	} else if codeChallenge == "" {
		return nil
	} else if codeVerifier == "" {
		return fmt.Errorf("missing code_verifier for PKCE")
	}

	computed := ComputeS256Challenge(codeVerifier)
	if computed != codeChallenge {
		return fmt.Errorf("PKCE verification failed")
	}

	if h.cfg.RequirePKCE {
		h.logger.Debug("MCP OAuth token: PKCE verification successful")
	} else {
		h.logger.Debug("MCP OAuth token: optional PKCE verification successful")
	}
	return nil
}

func ComputeS256Challenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func generateSecureToken(nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func stateFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}

func forbidCaching(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func redirectClientError(w http.ResponseWriter, r *http.Request, clientRedirectURI, clientState, code, description string) {
	u := clientRedirectURI
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	u += sep + "error=" + url.QueryEscape(code)
	if description != "" {
		u += "&error_description=" + url.QueryEscape(description)
	}
	if clientState != "" {
		u += "&state=" + url.QueryEscape(clientState)
	}
	http.Redirect(w, r, u, http.StatusFound)
}

func writeOAuthError(w http.ResponseWriter, status int, errCode, errDescription string) {
	resp := map[string]string{"error": errCode}
	if errDescription != "" {
		resp["error_description"] = errDescription
	}
	writeJSON(w, status, resp)
}

func ParseConfig(m map[string]any) Config {
	cfg := Config{
		RequirePKCE:          true,
		OAuthStateTTLSeconds: 300,
		OAuthCodeTTLSeconds:  300,
	}

	if v, ok := m["enable_auth"].(bool); ok {
		cfg.EnableAuth = v
	}
	if v, ok := m["external_auth_provider"].(string); ok {
		cfg.ExternalAuthProvider = v
	}
	if v, ok := m["require_pkce"].(bool); ok {
		cfg.RequirePKCE = v
	}
	if v, ok := m["oauth_state_ttl_seconds"].(int); ok && v > 0 {
		cfg.OAuthStateTTLSeconds = v
	}
	if v, ok := m["oauth_state_ttl_seconds"].(float64); ok && v > 0 {
		cfg.OAuthStateTTLSeconds = int(v)
	}
	if v, ok := m["user_id_claim"].(string); ok {
		cfg.UserIDClaim = v
	}
	if v, ok := m["host"].(string); ok {
		cfg.Host = v
	}
	if v, ok := m["port"].(int); ok {
		cfg.Port = v
	}
	if v, ok := m["port"].(float64); ok {
		cfg.Port = int(v)
	}

	return cfg
}

func (h *Handler) redirectURIAllowed(clientURI string) bool {
	if len(h.cfg.AllowedRedirectURIs) == 0 {
		return true
	}
	cu, err := url.Parse(clientURI)
	if err != nil {
		return false
	}
	for _, pattern := range h.cfg.AllowedRedirectURIs {
		if redirectURIMatch(pattern, clientURI, cu) {
			return true
		}
	}
	return false
}

func redirectURIMatch(pattern, rawClient string, cu *url.URL) bool {
	if strings.TrimRight(pattern, "/") == strings.TrimRight(rawClient, "/") {
		return true
	}
	pu, err := url.Parse(pattern)
	if err != nil {
		return false
	}
	if !isLoopbackHost(pu.Hostname()) || !isLoopbackHost(cu.Hostname()) {
		return false
	}
	if pu.Scheme != cu.Scheme {
		return false
	}
	if pu.Hostname() != cu.Hostname() {
		return false
	}
	return strings.TrimRight(pu.Path, "/") == strings.TrimRight(cu.Path, "/")
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

func canonicalUserID(u *UserInfo, claim string) string {
	var primary string
	switch claim {
	case "sub":
		primary = u.Sub
	case "upn":
		primary = stringClaim(u.Raw, "upn")
	case "preferred_username":
		primary = stringClaim(u.Raw, "preferred_username")
	case "email", "":
		primary = u.Email
	default:
		primary = u.Email
	}
	if !isSentinel(primary) {
		return primary
	}
	if !isSentinel(u.Sub) {
		return u.Sub
	}
	return ""
}

func stringClaim(raw map[string]any, key string) string {
	v, ok := raw[key].(string)
	if !ok {
		return ""
	}
	return v
}

func scrubSentinelProfile(u *UserInfo) {
	if isSentinel(u.Email) {
		u.Email = ""
	}
	if isSentinel(u.Name) {
		u.Name = u.Sub
	}
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
