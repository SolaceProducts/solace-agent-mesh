// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tlsutil"
	"golang.org/x/oauth2"
)

type OIDCClient interface {
	BuildAuthURL(ctx context.Context, state, redirectURI, codeChallenge, codeChallengeMethod string) (string, error)
	ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*TokenResponse, error)
	RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error)
	GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error)
	ValidateToken(ctx context.Context, token string) (bool, error)
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type UserInfo struct {
	Sub          string         `json:"sub"`
	Email        string         `json:"email"`
	Name         string         `json:"name"`
	OIDCProvider string         `json:"oidc_provider"`
	Claims       map[string]any `json:"claims"`
	RawResponse  map[string]any `json:"-"`
}

type OIDCConfig struct {
	Issuer             string
	ClientID           string
	ClientSecret       string
	RedirectURI        string
	Scopes             []string
	DevMode            bool
	CACertPath         string
	InsecureSkipVerify bool
}

type NativeOIDCClient struct {
	issuer       string
	clientID     string
	clientSecret string
	redirectURI  string
	scopes       []string
	httpClient   *http.Client
	logger       *slog.Logger

	discovery atomic.Pointer[oidcDiscovery]
	discoMu   sync.Mutex
}

func NewNativeOIDCClient(cfg OIDCConfig, logger *slog.Logger) (*NativeOIDCClient, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, fmt.Errorf("native OIDC: issuer and client_id are required")
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile", "offline_access"}
	}

	if logger == nil {
		logger = slog.Default()
	}

	httpClient, err := tlsutil.BuildHTTPClient(cfg.CACertPath, cfg.InsecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("native OIDC: build HTTP client: %w", err)
	}

	if cfg.InsecureSkipVerify {
		logger.Warn("OIDC TLS certificate verification disabled — do not use in production",
			"issuer", cfg.Issuer)
		if cfg.CACertPath != "" {
			logger.Warn("ca_cert_path is ignored when insecure_skip_verify is true",
				"caCertPath", cfg.CACertPath)
		}
	} else if cfg.CACertPath != "" {
		logger.Info("OIDC custom CA bundle loaded",
			"caCertPath", cfg.CACertPath,
			"issuer", cfg.Issuer)
	}

	logger.Debug("native OIDC client constructed",
		"issuer", cfg.Issuer,
		"clientID", cfg.ClientID,
		"redirectURI", cfg.RedirectURI,
		"caCertPath", cfg.CACertPath,
		"insecureSkipVerify", cfg.InsecureSkipVerify,
		"devMode", cfg.DevMode)

	return &NativeOIDCClient{
		issuer:       cfg.Issuer,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		redirectURI:  cfg.RedirectURI,
		scopes:       scopes,
		httpClient:   httpClient,
		logger:       logger.With("logger_name", "native-oidc"),
	}, nil
}

func (c *NativeOIDCClient) getDiscovery(ctx context.Context) (*oidcDiscovery, error) {
	if d := c.discovery.Load(); d != nil {
		return d, nil
	}

	c.discoMu.Lock()
	defer c.discoMu.Unlock()

	if d := c.discovery.Load(); d != nil {
		return d, nil
	}

	d, err := c.refreshDiscovery(ctx)
	if err != nil {
		c.logger.Warn("OIDC discovery failed; will retry on next request",
			"issuer", c.issuer, "error", err)
		return nil, fmt.Errorf("OIDC discovery for %s: %w", c.issuer, err)
	}

	c.logger.Info("native OIDC discovery complete",
		"issuer", c.issuer,
		"authorization_endpoint", d.AuthorizationEndpoint,
		"token_endpoint", d.TokenEndpoint,
		"userinfoEndpoint", d.UserinfoEndpoint,
	)
	c.discovery.Store(d)
	return d, nil
}

func (c *NativeOIDCClient) refreshDiscovery(ctx context.Context) (d *oidcDiscovery, err error) {
	defer observability.NewOAuthRemoteMonitor(observability.OAuthOpDiscovery).Stop(&err)
	return fetchOIDCDiscovery(ctx, c.issuer, c.httpClient)
}

func (c *NativeOIDCClient) WarmDiscovery(ctx context.Context) error {
	_, err := c.getDiscovery(ctx)
	return err
}

func (c *NativeOIDCClient) JWKSURI(ctx context.Context) (string, error) {
	d, err := c.getDiscovery(ctx)
	if err != nil {
		return "", err
	}
	if d.JwksURI == "" {
		return "", fmt.Errorf("OIDC discovery for %s did not advertise a jwks_uri", c.issuer)
	}
	return d.JwksURI, nil
}

func (c *NativeOIDCClient) TokenEndpoint(ctx context.Context) (string, error) {
	d, err := c.getDiscovery(ctx)
	if err != nil {
		return "", err
	}
	if d.TokenEndpoint == "" {
		return "", fmt.Errorf("OIDC discovery for %s did not advertise a token_endpoint", c.issuer)
	}
	return d.TokenEndpoint, nil
}

func (c *NativeOIDCClient) HTTPClient() *http.Client { return c.httpClient }

func (c *NativeOIDCClient) Issuer() string { return c.issuer }

func (c *NativeOIDCClient) ClientID() string { return c.clientID }

func (c *NativeOIDCClient) RequiresStateValidation() bool { return true }

func (c *NativeOIDCClient) AuthorizationURL(ctx context.Context, state string, opts ...oauth2.AuthCodeOption) (string, error) {
	d, err := c.getDiscovery(ctx)
	if err != nil {
		return "", err
	}
	return c.oauth2Config(d, c.redirectURI).AuthCodeURL(state, opts...), nil
}

func (c *NativeOIDCClient) BuildAuthURL(ctx context.Context, state, redirectURI, codeChallenge, codeChallengeMethod string) (string, error) {
	d, err := c.getDiscovery(ctx)
	if err != nil {
		return "", fmt.Errorf("OIDC discovery: %w", err)
	}
	u, err := url.Parse(d.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse authorization_endpoint %q: %w", d.AuthorizationEndpoint, err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	if len(c.scopes) > 0 {
		q.Set("scope", strings.Join(c.scopes, " "))
	} else {
		q.Set("scope", "openid email profile")
	}
	if codeChallenge != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", codeChallengeMethod)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *NativeOIDCClient) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	d, err := c.getDiscovery(ctx)
	if err != nil {
		return nil, err
	}

	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)

	var opts []oauth2.AuthCodeOption
	if codeVerifier != "" {
		opts = append(opts, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	}

	token, err := c.oauth2Config(d, redirectURI).Exchange(ctx, code, opts...)
	if err != nil {
		return nil, fmt.Errorf("code exchange: %w", err)
	}

	resp := &TokenResponse{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
	}
	if idt, ok := token.Extra("id_token").(string); ok {
		resp.IDToken = idt
	}
	if !token.Expiry.IsZero() {
		resp.ExpiresIn = int(time.Until(token.Expiry).Seconds())
	}
	return resp, nil
}

func (c *NativeOIDCClient) RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	d, err := c.getDiscovery(ctx)
	if err != nil {
		return nil, err
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh token: status %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}
	return &tokenResp, nil
}

var errInvalidUserInfoResponse = errors.New("invalid userinfo response")

type userInfoStatusError struct{ status int }

func (e *userInfoStatusError) Error() string { return fmt.Sprintf("userinfo: status %d", e.status) }

func (c *NativeOIDCClient) GetUserInfo(ctx context.Context, accessToken string) (ui *UserInfo, err error) {
	defer observability.NewOAuthRemoteMonitor(observability.OAuthOpGetUserInfo).Stop(&err)

	d, err := c.getDiscovery(ctx)
	if err != nil {
		return nil, err
	}

	if d.UserinfoEndpoint == "" {
		return nil, fmt.Errorf("userinfo endpoint not available in OIDC discovery")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.UserinfoEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read userinfo response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &userInfoStatusError{status: resp.StatusCode}
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse userinfo: %w: %w", errInvalidUserInfoResponse, err)
	}

	info := &UserInfo{
		Sub:         firstString(raw, "sub", "id"),
		Email:       strings.ToLower(firstString(raw, "email", "preferred_username")),
		Name:        firstString(raw, "name", "given_name"),
		RawResponse: raw,
		Claims:      raw,
	}

	if info.Sub == "" {
		info.Sub = "Unknown"
	}
	if info.Email == "" {
		info.Email = "unknown"
	}
	if info.Name == "" {
		info.Name = "Unknown"
	}

	return info, nil
}

func (c *NativeOIDCClient) ValidateToken(ctx context.Context, token string) (bool, error) {
	_, err := c.GetUserInfo(ctx, token)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (c *NativeOIDCClient) oauth2Config(d *oidcDiscovery, redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.clientID,
		ClientSecret: c.clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  d.AuthorizationEndpoint,
			TokenURL: d.TokenEndpoint,
		},
		Scopes:      c.scopes,
		RedirectURL: redirectURI,
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
