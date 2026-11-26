// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

const discoveryTTL = 24 * time.Hour

const maxAuthResponseSize = 2 << 20

const (
	wellKnownProtectedResource   = "/.well-known/oauth-protected-resource"
	wellKnownAuthorizationServer = "/.well-known/oauth-authorization-server"
	wellKnownOpenIDConfiguration = "/.well-known/openid-configuration"
	contentTypeJSON              = "application/json"
	headerContentType            = "Content-Type"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

func discoveryClient(ctx context.Context) *http.Client {
	if rt, ok := ctx.Value(baseTransportKey{}).(http.RoundTripper); ok && rt != nil {
		return &http.Client{Timeout: 30 * time.Second, Transport: rt}
	}
	return httpClient
}

type OAuthMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`

	DiscoveredAt time.Time

	Resource string
}

func (m *OAuthMetadata) IsExpired() bool {
	if m == nil {
		return true
	}
	return time.Since(m.DiscoveredAt) > discoveryTTL
}

type ResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
}

var supportedTokenAuthMethods = map[string]bool{
	"client_secret_basic": true,
	"client_secret_post":  true,
	"none":                true,
}

func SelectTokenAuthMethod(supported []string) string {
	for _, method := range supported {
		if supportedTokenAuthMethods[method] {
			return method
		}
	}
	return "client_secret_basic"
}

type ClientRegistration struct {
	ClientID                string `json:"client_id"`
	ClientSecret            string `json:"client_secret,omitempty"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method,omitempty"`
	ClientSecretExpiresAt   int64  `json:"client_secret_expires_at,omitempty"`
	ClientIDIssuedAt        int64  `json:"client_id_issued_at,omitempty"`
	FromEnv                 bool   `json:"-"`
}

const clientSecretExpiryBufferSeconds = 60

func (r *ClientRegistration) IsExpired() bool {
	if r == nil || r.FromEnv || r.ClientSecretExpiresAt <= 0 {
		return false
	}
	return time.Now().Unix()+clientSecretExpiryBufferSeconds >= r.ClientSecretExpiresAt
}

var ErrInvalidRegistrationResponse = errors.New("toolauth: invalid RFC 7591 registration response")

var ErrDCRServer = errors.New("toolauth: dynamic client registration failed")

var (
	DiscoveryCache   = make(map[string]*OAuthMetadata)
	DiscoveryCacheMu sync.RWMutex
)

var discoveryFlight singleflight.Group

func DiscoverOAuthMetadata(ctx context.Context, resourceURL string, loggers ...*slog.Logger) (*OAuthMetadata, error) {
	logger := pickLogger(loggers)
	cacheKey := normaliseCacheKey(resourceURL)
	if cached := cachedMetadata(cacheKey); cached != nil {
		return cached, nil
	}

	result, err, _ := discoveryFlight.Do(cacheKey, func() (any, error) {
		if cached := cachedMetadata(cacheKey); cached != nil {
			return cached, nil
		}
		return performDiscovery(context.WithoutCancel(ctx), resourceURL, cacheKey, logger)
	})
	if err != nil {
		return nil, err
	}
	return result.(*OAuthMetadata), nil
}

func cachedMetadata(cacheKey string) *OAuthMetadata {
	DiscoveryCacheMu.RLock()
	defer DiscoveryCacheMu.RUnlock()
	if cached, ok := DiscoveryCache[cacheKey]; ok && !cached.IsExpired() {
		return cached
	}
	return nil
}

func normaliseCacheKey(resourceURL string) string {
	u, err := url.Parse(resourceURL)
	if err != nil || u.Host == "" {
		return strings.TrimRight(strings.ToLower(resourceURL), "/")
	}
	u.Host = strings.ToLower(u.Host)
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/"
	}
	u.Path = path
	return u.String()
}

func performDiscovery(ctx context.Context, resourceURL, cacheKey string, logger *slog.Logger) (*OAuthMetadata, error) {
	parsedResource, err := url.Parse(resourceURL)
	if err != nil {
		return nil, fmt.Errorf("parse resource URL: %w", err)
	}

	authServer, prmResource := discoverAuthServer(ctx, parsedResource)

	wellKnownURLs := BuildWellKnownURLs(authServer, parsedResource)
	for _, wkURL := range wellKnownURLs {
		metadata, err := fetchMetadata(ctx, wkURL)
		if err == nil && metadata != nil {
			metadata.DiscoveredAt = time.Now()
			metadata.Resource = prmResource
			validateMetadataCapabilities(metadata, logger)
			cacheMetadata(cacheKey, metadata)
			return metadata, nil
		}
	}

	return nil, fmt.Errorf("OAuth2 metadata discovery failed for %s", logging.RedactURL(resourceURL))
}

func validateMetadataCapabilities(m *OAuthMetadata, logger *slog.Logger) {
	if m == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	if len(m.CodeChallengeMethodsSupported) > 0 && !containsString(m.CodeChallengeMethodsSupported, "S256") {
		discoveryCapabilityWarn(logger, msgDiscoveryMissingS256, m)
	}
	if len(m.GrantTypesSupported) > 0 {
		if !containsString(m.GrantTypesSupported, "authorization_code") {
			discoveryCapabilityWarn(logger, msgDiscoveryMissingAuthorizationCode, m)
		}
	}
	if len(m.ResponseTypesSupported) > 0 && !containsString(m.ResponseTypesSupported, "code") {
		discoveryCapabilityWarn(logger, msgDiscoveryMissingCodeResponseType, m)
	}
}

const (
	msgDiscoveryMissingS256              = "oauth2 discovery: code_challenge_methods_supported does not advertise S256"
	msgDiscoveryMissingAuthorizationCode = "oauth2 discovery: grant_types_supported does not advertise authorization_code"
	msgDiscoveryMissingCodeResponseType  = "oauth2 discovery: response_types_supported does not advertise code"
)

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func pickLogger(loggers []*slog.Logger) *slog.Logger {
	for _, l := range loggers {
		if l != nil {
			return l
		}
	}
	return slog.Default()
}

func discoveryCapabilityWarn(logger *slog.Logger, msg string, m *OAuthMetadata) {
	logger.Warn(msg,
		"issuer", m.Issuer,
		"code_challenge_methods_supported", m.CodeChallengeMethodsSupported,
		"grant_types_supported", m.GrantTypesSupported,
		"response_types_supported", m.ResponseTypesSupported,
	)
}

func discoverAuthServer(ctx context.Context, resource *url.URL) (*url.URL, string) {
	var challengeResource string
	if hint := probeWWWAuthenticate(ctx, resource); hint != "" {
		var as *url.URL
		as, challengeResource = fetchAndParseAuthServer(ctx, hint)
		if as != nil {
			return as, challengeResource
		}
	}

	rmURL := resource.Scheme + "://" + resource.Host + wellKnownProtectedResource +
		strings.TrimRight(resource.Path, "/")
	wkAS, wellKnownResource := fetchAndParseAuthServer(ctx, rmURL)
	if wkAS != nil {
		if wellKnownResource == "" {
			wellKnownResource = challengeResource
		}
		return wkAS, wellKnownResource
	}

	if challengeResource == "" {
		challengeResource = wellKnownResource
	}
	return nil, challengeResource
}

func fetchAndParseAuthServer(ctx context.Context, rmURL string) (*url.URL, string) {
	rm, err := fetchResourceMetadata(ctx, rmURL)
	if err != nil || rm == nil || len(rm.AuthorizationServers) == 0 {
		return nil, ""
	}
	as, err := url.Parse(rm.AuthorizationServers[0])
	if err != nil || as.Host == "" {
		return nil, rm.Resource
	}
	return as, rm.Resource
}

const mcpInitializeProbeBody = `{"jsonrpc":"2.0","id":0,"method":"initialize",` +
	`"params":{"protocolVersion":"2025-06-18","capabilities":{},` +
	`"clientInfo":{"name":"solace-agent-mesh","version":"1.0.0"}}}`

func probeWWWAuthenticate(ctx context.Context, resource *url.URL) string {
	hint := challengeHint(ctx, http.MethodPost, resource.String(), mcpInitializeProbeBody)
	if hint == "" {
		hint = challengeHint(ctx, http.MethodGet, resource.String(), "")
	}
	if hint == "" {
		return ""
	}
	if u, err := url.Parse(hint); err == nil {
		return resource.ResolveReference(u).String()
	}
	return hint
}

func challengeHint(ctx context.Context, method, target, body string) string {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return ""
	}
	if body != "" {
		req.Header.Set(headerContentType, contentTypeJSON)
	}
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := discoveryClient(ctx).Do(req)
	if err != nil {
		return ""
	}
	defer drainAndClose(resp)

	for _, challenge := range resp.Header.Values("WWW-Authenticate") {
		if hint := ParseResourceMetadataURL(challenge); hint != "" {
			return hint
		}
	}
	return ""
}

func drainAndClose(resp *http.Response) {
	if !strings.Contains(resp.Header.Get(headerContentType), "text/event-stream") {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	}
	_ = resp.Body.Close()
}

type RegistrationRequest struct {
	Scopes             []string
	InitialAccessToken string
	DisableEnvFallback bool
	ClientName         string
}

func RegisterClient(ctx context.Context, registrationURL, redirectURI string, metadata *OAuthMetadata) (*ClientRegistration, error) {
	return RegisterClientWithConfig(ctx, registrationURL, redirectURI, metadata, RegistrationRequest{})
}

func RegisterClientWithConfig(ctx context.Context, registrationURL, redirectURI string, metadata *OAuthMetadata, reqCfg RegistrationRequest, loggers ...*slog.Logger) (*ClientRegistration, error) {
	logger := pickLogger(loggers)

	fallback := func(cause error) (*ClientRegistration, error) {
		if reqCfg.DisableEnvFallback {
			if cause != nil {
				return nil, fmt.Errorf("dynamic client registration failed: %w", cause)
			}
			return nil, fmt.Errorf("dynamic client registration failed")
		}
		return envFallbackRegistration()
	}

	if registrationURL == "" {
		return fallback(nil)
	}

	authMethod := "client_secret_basic"
	if metadata != nil {
		authMethod = SelectTokenAuthMethod(metadata.TokenEndpointAuthMethodsSupported)
	}

	grantTypes := []string{"authorization_code"}
	if metadata == nil || len(metadata.GrantTypesSupported) == 0 ||
		containsString(metadata.GrantTypesSupported, "refresh_token") {
		grantTypes = append(grantTypes, "refresh_token")
	}

	clientName := reqCfg.ClientName
	if clientName == "" {
		clientName = "Solace Agent Mesh"
	}

	regReq := map[string]any{
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": authMethod,
		"grant_types":                grantTypes,
		"response_types":             []string{"code"},
		"client_name":                clientName,
	}
	if len(reqCfg.Scopes) > 0 {
		regReq["scope"] = strings.Join(reqCfg.Scopes, " ")
	}

	body, err := json.Marshal(regReq)
	if err != nil {
		return nil, fmt.Errorf("marshal registration request: %w", err)
	}

	accessToken := reqCfg.InitialAccessToken
	if accessToken == "" {
		accessToken = os.Getenv("TOOL_OAUTH_INITIAL_ACCESS_TOKEN")
	}

	type dcrAttempt struct {
		body   []byte
		status int
	}

	var lastAttempt dcrAttempt
	attempt, err := withRetry(ctx, discoveryRetryConfig, func(rctx context.Context) retryResult[dcrAttempt] {
		req, rerr := http.NewRequestWithContext(rctx, http.MethodPost, registrationURL, strings.NewReader(string(body)))
		if rerr != nil {
			return retryResult[dcrAttempt]{Err: fmt.Errorf("create registration request: %w", rerr)}
		}
		req.Header.Set(headerContentType, contentTypeJSON)
		req.Header.Set("Accept", contentTypeJSON)
		if accessToken != "" {
			req.Header.Set("Authorization", "Bearer "+accessToken)
		}

		httpResp, httpErr := discoveryClient(rctx).Do(req)
		if httpErr != nil {
			return retryResult[dcrAttempt]{Err: httpErr}
		}
		defer httpResp.Body.Close() //nolint:errcheck

		b, readErr := io.ReadAll(io.LimitReader(httpResp.Body, maxAuthResponseSize))
		lastAttempt = dcrAttempt{body: b, status: httpResp.StatusCode}
		if readErr != nil {
			return retryResult[dcrAttempt]{Resp: httpResp, Err: readErr}
		}
		if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusCreated {
			return retryResult[dcrAttempt]{
				Value: dcrAttempt{body: b, status: httpResp.StatusCode},
				Resp:  httpResp,
				Err:   fmt.Errorf("registration endpoint returned %d", httpResp.StatusCode),
			}
		}
		return retryResult[dcrAttempt]{Value: dcrAttempt{body: b, status: httpResp.StatusCode}, Resp: httpResp}
	})

	if err != nil {
		if lastAttempt.status != 0 && len(lastAttempt.body) > 0 {
			logDCRServerError(registrationURL, lastAttempt.status, lastAttempt.body, logger)
		}
		return fallback(err)
	}

	respBody := attempt.body

	var reg ClientRegistration
	if err := json.Unmarshal(respBody, &reg); err != nil {
		logger.Warn("dynamic client registration response was not valid JSON",
			"registration_endpoint", registrationURL,
			"error", err,
			"bodyLen", len(respBody),
		)
		return fallback(err)
	}

	if reg.TokenEndpointAuthMethod == "" {
		reg.TokenEndpointAuthMethod = authMethod
	}

	if err := validateRegistrationResponse(&reg); err != nil {
		return nil, err
	}

	return &reg, nil
}

func validateRegistrationResponse(reg *ClientRegistration) error {
	if reg == nil {
		return fmt.Errorf("%w: response is nil", ErrInvalidRegistrationResponse)
	}
	if reg.ClientID == "" {
		return fmt.Errorf("%w: missing client_id", ErrInvalidRegistrationResponse)
	}
	if reg.TokenEndpointAuthMethod != "none" && reg.ClientSecret == "" {
		return fmt.Errorf("%w: missing client_secret for method=%s", ErrInvalidRegistrationResponse, reg.TokenEndpointAuthMethod)
	}
	return nil
}

func logDCRServerError(url string, status int, body []byte, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	var parsed struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr == nil && (parsed.Error != "" || parsed.ErrorDescription != "") {
		logger.Warn("oauth2 dynamic client registration rejected",
			"registration_endpoint", url,
			"httpStatus", status,
			"error", parsed.Error,
			"errorDescription", parsed.ErrorDescription,
		)
		return
	}
	logger.Warn("oauth2 dynamic client registration rejected",
		"registration_endpoint", url,
		"httpStatus", status,
		"bodyLen", len(body),
	)
}

func envFallbackRegistration() (*ClientRegistration, error) {
	clientID := os.Getenv("TOOL_OAUTH_CLIENT_ID")
	if clientID == "" {
		clientID = os.Getenv("MCP_CLIENT_ID")
	}
	if clientID == "" {
		return nil, fmt.Errorf("dynamic client registration failed and TOOL_OAUTH_CLIENT_ID/MCP_CLIENT_ID env vars not set")
	}

	clientSecret := os.Getenv("TOOL_OAUTH_CLIENT_SECRET")
	if clientSecret == "" {
		clientSecret = os.Getenv("MCP_CLIENT_SECRET")
	}

	return &ClientRegistration{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		FromEnv:      true,
	}, nil
}

func ParseResourceMetadataURL(header string) string {
	if header == "" {
		return ""
	}
	lower := strings.ToLower(header)
	idx := strings.Index(lower, "resource_metadata=")
	if idx < 0 {
		return ""
	}
	rest := header[idx+len("resource_metadata="):]
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' {
		rest = rest[1:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return ""
		}
		return rest[:end]
	}
	end := strings.IndexAny(rest, " ,")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func BuildWellKnownURLs(authServer, resource *url.URL) []string {
	as := authServer
	if as == nil {
		as = resource
	}
	asOrigin := as.Scheme + "://" + as.Host
	asPath := strings.TrimRight(as.Path, "/")

	var urls []string
	if asPath != "" {
		urls = append(urls, asOrigin+wellKnownAuthorizationServer+asPath)
	}
	urls = append(urls, asOrigin+wellKnownAuthorizationServer)
	if asPath != "" {
		urls = append(urls, asOrigin+wellKnownOpenIDConfiguration+asPath)
	}
	urls = append(urls, asOrigin+wellKnownOpenIDConfiguration)

	resOrigin := resource.Scheme + "://" + resource.Host
	if resOrigin != asOrigin {
		urls = append(urls, resOrigin+wellKnownOpenIDConfiguration)
	}
	return urls
}

var discoveryRetryConfig = defaultRetryConfig

func fetchJSON(ctx context.Context, u, kind string) ([]byte, error) {
	return withRetry(ctx, discoveryRetryConfig, func(rctx context.Context) retryResult[[]byte] {
		req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
		if err != nil {
			return retryResult[[]byte]{Err: err}
		}
		req.Header.Set("Accept", contentTypeJSON)

		resp, err := discoveryClient(rctx).Do(req)
		if err != nil {
			return retryResult[[]byte]{Err: err}
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			io.Copy(io.Discard, resp.Body) //nolint:errcheck
			return retryResult[[]byte]{
				Resp: resp,
				Err:  fmt.Errorf("%s endpoint returned %d", kind, resp.StatusCode),
			}
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAuthResponseSize))
		if readErr != nil {
			return retryResult[[]byte]{Err: readErr}
		}
		return retryResult[[]byte]{Value: body, Resp: resp}
	})
}

func fetchResourceMetadata(ctx context.Context, metadataURL string) (*ResourceMetadata, error) {
	body, err := fetchJSON(ctx, metadataURL, "resource metadata")
	if err != nil {
		return nil, err
	}

	var rm ResourceMetadata
	if err := json.Unmarshal(body, &rm); err != nil {
		return nil, err
	}

	if len(rm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("resource metadata missing authorization_servers")
	}

	return &rm, nil
}

func fetchMetadata(ctx context.Context, metadataURL string) (*OAuthMetadata, error) {
	body, err := fetchJSON(ctx, metadataURL, "metadata")
	if err != nil {
		return nil, err
	}

	var meta OAuthMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}

	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return nil, fmt.Errorf("metadata missing required endpoints")
	}

	return &meta, nil
}

func cacheMetadata(cacheKey string, meta *OAuthMetadata) {
	if meta == nil {
		return
	}
	DiscoveryCacheMu.Lock()
	DiscoveryCache[cacheKey] = meta
	DiscoveryCacheMu.Unlock()
}
