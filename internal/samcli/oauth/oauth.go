// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	htmlpkg "html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	PlatformURL string

	Issuer string

	ClientID string

	Scopes []string
}

type CLIConfigResponse struct {
	Issuer   string   `json:"issuer"`
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

type Tokens struct {
	AccessToken    string
	SAMAccessToken string
	RefreshToken   string
	ExpiresAt      time.Time
	ObtainedAt     time.Time
}

type Client struct {
	HTTP *http.Client

	LoginTimeout time.Duration
}

var defaultTransport http.RoundTripper

func SetDefaultTransport(rt http.RoundTripper) func() {
	prev := defaultTransport
	defaultTransport = rt
	return func() { defaultTransport = prev }
}

func NewClient() *Client {
	hc := &http.Client{Timeout: 30 * time.Second}
	if defaultTransport != nil {
		hc.Transport = defaultTransport
	}
	return &Client{
		HTTP:         hc,
		LoginTimeout: 90 * time.Second,
	}
}

func (c *Client) Discover(ctx context.Context, platformURL string) (Config, error) {
	platformURL = strings.TrimRight(platformURL, "/")
	if platformURL == "" {
		return Config{}, fmt.Errorf("oauth discover: platformURL is empty")
	}

	cfg := Config{PlatformURL: platformURL}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, platformURL+"/api/v1/auth/cli-config", nil)
	if err != nil {
		return cfg, fmt.Errorf("oauth discover: build request: %w", err)
	}
	req.Header.Set("Accept", contentTypeJSON)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return cfg, fmt.Errorf("oauth discover: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		var body CLIConfigResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return cfg, fmt.Errorf("oauth discover: parse cli-config: %w", err)
		}
		cfg.Issuer = body.Issuer
		cfg.ClientID = body.ClientID
		cfg.Scopes = body.Scopes
		return cfg, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return cfg, nil
	default:
		body, _ := io.ReadAll(resp.Body)
		return cfg, fmt.Errorf("oauth discover: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

type loopbackResult struct {
	Tokens Tokens
	Err    error
}

func (c *Client) Login(ctx context.Context, cfg Config) (Tokens, error) {
	if cfg.PlatformURL == "" {
		return Tokens{}, fmt.Errorf("oauth login: platform URL is empty (call Discover first)")
	}

	listener, err := net.Listen("tcp", ipv4LoopbackListenAddr)
	if err != nil {
		return Tokens{}, fmt.Errorf("oauth login: bind loopback: %w", err)
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return Tokens{}, fmt.Errorf("oauth login: unexpected listener address type %T", listener.Addr())
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", addr.Port)

	_, challenge, err := GeneratePKCE()
	if err != nil {
		_ = listener.Close()
		return Tokens{}, err
	}
	state, err := GenerateState()
	if err != nil {
		_ = listener.Close()
		return Tokens{}, err
	}

	resultCh := make(chan loopbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackAddr(r.RemoteAddr) {
			http.Error(w, "loopback only", http.StatusForbidden)
			resultCh <- loopbackResult{Err: fmt.Errorf("oauth login: rejected non-loopback callback from %s", r.RemoteAddr)}
			return
		}

		w.Header().Set(headerContentType, "text/html; charset=utf-8")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")

		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			_, _ = fmt.Fprintf(w, callbackHTMLError, htmlEscape(e), htmlEscape(desc))
			resultCh <- loopbackResult{Err: fmt.Errorf("oauth login: idp returned %s: %s", e, desc)}
			return
		}
		if got := q.Get("state"); got != state {
			_, _ = io.WriteString(w, callbackHTMLStateMismatch)
			resultCh <- loopbackResult{Err: fmt.Errorf("oauth login: state mismatch")}
			return
		}

		samToken := q.Get("sam_access_token")
		accessToken := q.Get("access_token")
		refreshToken := q.Get("refresh_token")
		expiresInStr := q.Get("expires_in")
		if samToken == "" {
			_, _ = io.WriteString(w, callbackHTMLNoToken)
			resultCh <- loopbackResult{Err: fmt.Errorf("oauth login: callback missing sam_access_token")}
			return
		}

		now := time.Now()
		expiresAt := now.Add(1 * time.Hour)
		if expiresInStr != "" {
			if seconds, parseErr := parseExpiresIn(expiresInStr); parseErr == nil {
				expiresAt = now.Add(time.Duration(seconds) * time.Second)
			}
		}

		_, _ = io.WriteString(w, callbackHTMLSuccess)
		resultCh <- loopbackResult{Tokens: Tokens{
			AccessToken:    accessToken,
			SAMAccessToken: samToken,
			RefreshToken:   refreshToken,
			ExpiresAt:      expiresAt,
			ObtainedAt:     now,
		}}
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Serve(listener)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	authorizeURL, err := buildLoginURL(cfg.PlatformURL, redirectURI, state, challenge)
	if err != nil {
		return Tokens{}, err
	}

	if err := OpenBrowser(authorizeURL); err != nil {
		_, _ = fmt.Fprintf(stderr(), "could not auto-open the browser (%v); paste this URL to continue:\n  %s\n", err, authorizeURL)
	} else {
		_, _ = fmt.Fprintf(stderr(), "Opened browser to complete authentication. If it didn't open, visit:\n  %s\n", authorizeURL)
	}

	timeout := c.LoginTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-resultCh:
		if res.Err != nil {
			return Tokens{}, res.Err
		}
		return res.Tokens, nil
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			return Tokens{}, fmt.Errorf("oauth login: loopback server: %w", err)
		}
		return Tokens{}, fmt.Errorf("oauth login: loopback server stopped before callback")
	case <-timer.C:
		return Tokens{}, fmt.Errorf("oauth login: timed out after %s waiting for browser callback", timeout)
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	}
}

func (c *Client) Refresh(ctx context.Context, cfg Config, refreshToken, samAccessToken string) (Tokens, error) {
	if refreshToken == "" {
		return Tokens{}, fmt.Errorf("oauth refresh: refresh token is empty")
	}
	if cfg.PlatformURL == "" {
		return Tokens{}, fmt.Errorf("oauth refresh: platform URL is empty")
	}

	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return Tokens{}, fmt.Errorf("oauth refresh: encode body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.PlatformURL+"/api/v1/auth/refresh", strings.NewReader(string(body)))
	if err != nil {
		return Tokens{}, fmt.Errorf("oauth refresh: build request: %w", err)
	}
	req.Header.Set(headerContentType, contentTypeJSON)
	req.Header.Set("Accept", contentTypeJSON)
	if samAccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+samAccessToken)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("oauth refresh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return Tokens{}, fmt.Errorf("%w: status %d: %s", ErrRefreshDenied, resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	if resp.StatusCode != http.StatusOK {
		return Tokens{}, fmt.Errorf("oauth refresh: status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var refreshResp struct {
		AccessToken    string `json:"access_token"`
		SAMAccessToken string `json:"sam_access_token"`
		RefreshToken   string `json:"refresh_token"`
		ExpiresIn      int    `json:"expires_in"`
	}
	if err := json.Unmarshal(bodyBytes, &refreshResp); err != nil {
		return Tokens{}, fmt.Errorf("oauth refresh: parse response: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(1 * time.Hour)
	if refreshResp.ExpiresIn > 0 {
		expiresAt = now.Add(time.Duration(refreshResp.ExpiresIn) * time.Second)
	}

	return Tokens{
		AccessToken:    refreshResp.AccessToken,
		SAMAccessToken: refreshResp.SAMAccessToken,
		RefreshToken:   refreshResp.RefreshToken,
		ExpiresAt:      expiresAt,
		ObtainedAt:     now,
	}, nil
}

func (c *Client) Logout(ctx context.Context, cfg Config, samAccessToken string) error {
	if cfg.PlatformURL == "" {
		return fmt.Errorf("oauth logout: platform URL is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.PlatformURL+"/api/v1/auth/logout", strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("oauth logout: build request: %w", err)
	}
	req.Header.Set(headerContentType, contentTypeJSON)
	if samAccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+samAccessToken)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("oauth logout: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 500 {
		return fmt.Errorf("oauth logout: status %d", resp.StatusCode)
	}
	return nil
}

func buildLoginURL(platformURL, redirectURI, state, challenge string) (string, error) {
	u, err := url.Parse(platformURL)
	if err != nil {
		return "", fmt.Errorf("oauth: parse platform url: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/auth/login"

	q := u.Query()
	q.Set("response_mode", "cli")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func htmlEscape(s string) string { return htmlpkg.EscapeString(s) }

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

func parseExpiresIn(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

const (
	headerContentType = "Content-Type"
	contentTypeJSON   = "application/json"
)

const ipv4LoopbackListenAddr = "127.0.0.1:0"
