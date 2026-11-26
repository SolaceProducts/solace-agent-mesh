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
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

func ClassifyOAuth2Error(err error) string {
	if err == nil {
		return ""
	}
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) {
		return ""
	}
	if retrieveErr.ErrorCode != "" {
		return retrieveErr.ErrorCode
	}
	if len(retrieveErr.Body) == 0 {
		return ""
	}
	var body struct {
		Error string `json:"error"`
	}
	if jsonErr := json.Unmarshal(retrieveErr.Body, &body); jsonErr != nil {
		return ""
	}
	return body.Error
}

func IsTransientRefreshFailure(err error) bool {
	retrieveErr, ok := errors.AsType[*oauth2.RetrieveError](err)
	if !ok {
		return true
	}
	code := ClassifyOAuth2Error(err)
	if code == "invalid_grant" || code == "invalid_client" {
		return false
	}
	if code == "server_error" || code == "temporarily_unavailable" || code == oauthCodeSlowDown {
		return true
	}
	if retrieveErr.Response != nil {
		status := retrieveErr.Response.StatusCode
		if status >= http.StatusInternalServerError || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
			return true
		}
		if code == "" && (status == http.StatusForbidden || status == http.StatusNotFound) {
			return true
		}
		if status >= http.StatusBadRequest {
			return false
		}
	}
	return code == ""
}

const oauthCodeSlowDown = "slow_down"

func refreshFailureStatus(err error) int {
	if ClassifyOAuth2Error(err) == oauthCodeSlowDown {
		return http.StatusTooManyRequests
	}
	retrieveErr, ok := errors.AsType[*oauth2.RetrieveError](err)
	if !ok || retrieveErr.Response == nil {
		return 0
	}
	return retrieveErr.Response.StatusCode
}

func RefreshFailureLogAttrs(err error) []any {
	if _, ok := errors.AsType[*oauth2.RetrieveError](err); !ok {
		return []any{"error", logging.RedactURLQueries(err.Error())}
	}
	return []any{"oauthCode", ClassifyOAuth2Error(err), "httpStatus", refreshFailureStatus(err)}
}

const tokenExpiryBuffer = 60 * time.Second

var ErrPKCEVerificationFailed = errors.New("toolauth: PKCE verification failed")

type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresAt    int64  `json:"expires_at"`
	Scope        string `json:"scope,omitempty"`

	RefreshStartedAt int64 `json:"refresh_started_at,omitempty"`

	GrantRef string `json:"grant_ref,omitempty"`
}

func (t *OAuthToken) IsExpired() bool {
	if t == nil || t.AccessToken == "" {
		return true
	}
	if t.ExpiresAt == 0 {
		return false
	}
	return time.Now().Add(tokenExpiryBuffer).Unix() >= t.ExpiresAt
}

func GenerateVerifier() string {
	return oauth2.GenerateVerifier()
}

func GenerateState() string {
	return oauth2.GenerateVerifier()
}

type AuthURLOption func(*authURLSettings)

type authURLSettings struct {
	omitConsentPrompt bool
}

func WithoutConsentPrompt() AuthURLOption {
	return func(s *authURLSettings) { s.omitConsentPrompt = true }
}

func BuildAuthorizationURL(scheme *OAuthScheme, clientID, state, codeVerifier, redirectURI string, urlOpts ...AuthURLOption) (string, error) {
	if scheme == nil || scheme.AuthorizationURL == "" {
		return "", fmt.Errorf("missing authorization_url in OAuth2 scheme")
	}
	var settings authURLSettings
	for _, opt := range urlOpts {
		opt(&settings)
	}

	cfg := &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			AuthURL: scheme.AuthorizationURL,
		},
		RedirectURL: redirectURI,
		Scopes:      scheme.Scopes,
	}

	opts := []oauth2.AuthCodeOption{oauth2.AccessTypeOffline}
	if !settings.omitConsentPrompt {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "consent"))
	}
	if codeVerifier != "" {
		opts = append(opts, oauth2.S256ChallengeOption(codeVerifier))
	}
	if scheme.Audience != "" {
		opts = append(opts, oauth2.SetAuthURLParam("audience", scheme.Audience))
	}
	if scheme.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", scheme.Resource))
	}

	return cfg.AuthCodeURL(state, opts...), nil
}

func ExchangeCode(ctx context.Context, scheme *OAuthScheme, cred *AuthCredential, code, codeVerifier, redirectURI string) (*OAuthToken, error) {
	if scheme == nil || scheme.TokenURL == "" {
		return nil, fmt.Errorf("missing token_url in OAuth2 scheme")
	}

	cfg := buildConfig(scheme, cred, redirectURI)
	ctx = contextWithHTTPClient(ctx)

	var opts []oauth2.AuthCodeOption
	if codeVerifier != "" {
		opts = append(opts, oauth2.VerifierOption(codeVerifier))
	}
	if scheme.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", scheme.Resource))
	}
	tok, err := cfg.Exchange(ctx, code, opts...)
	if err != nil {
		if looksLikePKCEError(err) {
			return nil, fmt.Errorf("%w: %w", ErrPKCEVerificationFailed, err)
		}
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	return fromOAuth2Token(tok), nil
}

func looksLikePKCEError(err error) bool {
	if err == nil {
		return false
	}
	if code := ClassifyOAuth2Error(err); code == "invalid_grant" {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "code_verifier") || strings.Contains(msg, "pkce")
}

func ExchangeClientCredentials(
	ctx context.Context,
	tokenURL, clientID, clientSecret string,
	scopes []string,
	authMethod string,
) (*OAuthToken, error) {
	if tokenURL == "" {
		return nil, fmt.Errorf("missing token_url for client_credentials exchange")
	}
	if clientID == "" {
		return nil, fmt.Errorf("missing client_id for client_credentials exchange")
	}

	cc := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		Scopes:       scopes,
		AuthStyle:    mapAuthStyle(authMethod),
	}

	ctx = contextWithHTTPClient(ctx)
	tok, err := cc.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("client_credentials exchange failed: %w", err)
	}
	return fromOAuth2Token(tok), nil
}

func RefreshToken(ctx context.Context, scheme *OAuthScheme, cred *AuthCredential, refreshToken string) (*OAuthToken, error) {
	if scheme == nil || scheme.TokenURL == "" {
		return nil, fmt.Errorf("missing token_url in OAuth2 scheme")
	}

	cfg := buildConfig(scheme, cred, "")
	if scheme.RefreshURL != "" {
		cfg.Endpoint.TokenURL = scheme.RefreshURL
	}
	ctx = contextWithHTTPClientResource(ctx, scheme.Resource)

	src := cfg.TokenSource(ctx, &oauth2.Token{
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(-time.Hour),
	})

	tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}

	return fromOAuth2Token(tok), nil
}

const loopbackFrontendOrigin = "http://127.0.0.1:8800"

func ResolveRedirectURI() string {
	if uri := os.Getenv("OAUTH_TOOL_REDIRECT_URI"); uri != "" {
		return uri
	}
	frontend := os.Getenv("FRONTEND_REDIRECT_URI")
	if frontend == "" {
		frontend = loopbackFrontendOrigin
	}
	frontend = strings.TrimRight(frontend, "/")
	return frontend + "/api/v1/auth/tool/callback"
}

func buildConfig(scheme *OAuthScheme, cred *AuthCredential, redirectURI string) *oauth2.Config {
	cfg := &oauth2.Config{
		Endpoint: oauth2.Endpoint{
			AuthURL:   scheme.AuthorizationURL,
			TokenURL:  scheme.TokenURL,
			AuthStyle: mapAuthStyle(scheme.TokenEndpointAuthMethod),
		},
		RedirectURL: redirectURI,
		Scopes:      scheme.Scopes,
	}

	if cred != nil {
		cfg.ClientID = cred.ClientID
		if confidentialClientSendsSecret(scheme.TokenEndpointAuthMethod) {
			cfg.ClientSecret = cred.ClientSecret
		}
	}

	return cfg
}

func confidentialClientSendsSecret(authMethod string) bool {
	return authMethod != "none"
}

func mapAuthStyle(method string) oauth2.AuthStyle {
	switch method {
	case "client_secret_basic":
		return oauth2.AuthStyleInHeader
	case "client_secret_post":
		return oauth2.AuthStyleInParams
	case "none":
		return oauth2.AuthStyleInParams
	default:
		return oauth2.AuthStyleInParams
	}
}

func fromOAuth2Token(t *oauth2.Token) *OAuthToken {
	if t == nil {
		return nil
	}

	out := &OAuthToken{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
	}
	if out.TokenType == "" {
		out.TokenType = "bearer"
	}

	if !t.Expiry.IsZero() {
		out.ExpiresAt = t.Expiry.Unix()
	}

	if scope, ok := t.Extra("scope").(string); ok {
		out.Scope = scope
	}

	return out
}

type baseTransportKey struct{}

func WithBaseTransport(ctx context.Context, rt http.RoundTripper) context.Context {
	if rt == nil {
		return ctx
	}
	return context.WithValue(ctx, baseTransportKey{}, rt)
}

func baseTransportFromContext(ctx context.Context) http.RoundTripper {
	if rt, ok := ctx.Value(baseTransportKey{}).(http.RoundTripper); ok && rt != nil {
		return rt
	}
	return http.DefaultTransport
}

func contextWithHTTPClient(ctx context.Context) context.Context {
	return contextWithHTTPClientResource(ctx, "")
}

func contextWithHTTPClientResource(ctx context.Context, resource string) context.Context {
	var transport http.RoundTripper = &cappedResponseTransport{inner: baseTransportFromContext(ctx), cap: maxAuthResponseSize}
	if resource != "" {
		transport = &resourceParamTransport{inner: transport, resource: resource}
	}
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	})
}

type resourceParamTransport struct {
	inner    http.RoundTripper
	resource string
}

func (t *resourceParamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.resource == "" || req.Body == nil ||
		req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		return t.inner.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read oauth2 token request body for resource injection: %w", err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil || values.Get("resource") != "" {
		clone := req.Clone(req.Context())
		clone.Body = io.NopCloser(strings.NewReader(string(body)))
		return t.inner.RoundTrip(clone)
	}
	values.Set("resource", t.resource)
	encoded := values.Encode()
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(strings.NewReader(encoded))
	clone.ContentLength = int64(len(encoded))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(encoded)), nil
	}
	return t.inner.RoundTrip(clone)
}

type cappedResponseTransport struct {
	inner http.RoundTripper
	cap   int64
}

func (t *cappedResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &limitedReadCloser{rc: resp.Body, r: io.LimitReader(resp.Body, t.cap)}
	return resp, nil
}

type limitedReadCloser struct {
	rc io.ReadCloser
	r  io.Reader
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.r.Read(p) }

func (l *limitedReadCloser) Close() error { return l.rc.Close() }
