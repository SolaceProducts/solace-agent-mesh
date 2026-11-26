// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth2cc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tlsutil"
)

type Config struct {
	TokenURL                  string
	ClientID                  string
	ClientSecret              string
	Scope                     string
	CACertPath                string
	TokenRefreshBufferSeconds int
	MaxRetries                int
	AllowInsecureTokenURL     bool
	InsecureSkipVerify        bool
}

type TokenManager struct {
	cfg    Config
	client *http.Client
	logger *slog.Logger

	mu        sync.Mutex
	fetchMu   sync.Mutex
	token     string
	expiresAt time.Time
}

func NewTokenManager(cfg Config, logger *slog.Logger) (*TokenManager, error) {
	if cfg.TokenURL == "" {
		return nil, fmt.Errorf("oauth2cc: token_url is required")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("oauth2cc: client_id is required")
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("oauth2cc: client_secret is required")
	}
	if cfg.TokenRefreshBufferSeconds < 0 {
		return nil, fmt.Errorf("oauth2cc: token_refresh_buffer_seconds must be >= 0")
	}
	if cfg.MaxRetries < 0 {
		return nil, fmt.Errorf("oauth2cc: max_retries must be >= 0")
	}

	if !strings.HasPrefix(cfg.TokenURL, "https://") && !cfg.AllowInsecureTokenURL {
		return nil, fmt.Errorf("oauth2cc: token_url must use HTTPS (got %q); set allow_insecure_token_url to override for development", cfg.TokenURL)
	}

	if cfg.TokenRefreshBufferSeconds == 0 {
		cfg.TokenRefreshBufferSeconds = 300
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}

	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "oauth2cc-token-manager")

	httpClient, err := tlsutil.BuildHTTPClient(cfg.CACertPath, cfg.InsecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("oauth2cc: build HTTP client: %w", err)
	}

	tokenURLHost := cfg.TokenURL
	if parsed, parseErr := url.Parse(cfg.TokenURL); parseErr == nil {
		tokenURLHost = parsed.Host
	}
	logger.Info("OAuth2 client credentials token manager initialized",
		"tokenURLHost", tokenURLHost,
		"clientID", cfg.ClientID)

	return &TokenManager{
		cfg:    cfg,
		client: httpClient,
		logger: logger,
	}, nil
}

func (tm *TokenManager) GetToken(ctx context.Context) (string, error) {
	tm.mu.Lock()
	if tm.token != "" && time.Now().Before(tm.expiresAt) {
		tok := tm.token
		tm.mu.Unlock()
		tm.logger.Debug("using cached OAuth2 token")
		return tok, nil
	}
	tm.mu.Unlock()

	tm.fetchMu.Lock()
	defer tm.fetchMu.Unlock()

	tm.mu.Lock()
	if tm.token != "" && time.Now().Before(tm.expiresAt) {
		tok := tm.token
		tm.mu.Unlock()
		tm.logger.Debug("using cached OAuth2 token (fetched by another goroutine)")
		return tok, nil
	}
	tm.mu.Unlock()

	token, expiresIn, err := tm.fetchTokenWithRetry(ctx)
	if err != nil {
		return "", err
	}

	if expiresIn <= 0 {
		expiresIn = defaultTokenExpiresInSeconds
	}

	ttlSeconds := expiresIn - tm.cfg.TokenRefreshBufferSeconds
	if ttlSeconds < 60 {
		ttlSeconds = 60
	}
	if ttlSeconds > expiresIn {
		ttlSeconds = expiresIn
	}

	tm.mu.Lock()
	tm.token = token
	tm.expiresAt = time.Now().Add(time.Duration(ttlSeconds) * time.Second)
	tm.mu.Unlock()

	tm.logger.Info("OAuth2 token cached",
		"ttl_seconds", ttlSeconds,
		"expires_in", expiresIn)

	return token, nil
}

func (tm *TokenManager) Invalidate() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.token = ""
	tm.expiresAt = time.Time{}
}

func (tm *TokenManager) fetchTokenWithRetry(ctx context.Context) (token string, expiresIn int, err error) {
	maxAttempts := tm.cfg.MaxRetries + 1
	for attempt := range maxAttempts {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", 0, fmt.Errorf("oauth2cc: context cancelled: %w", ctxErr)
		}

		token, expiresIn, fetchErr := tm.fetchToken(ctx)
		if fetchErr == nil {
			return token, expiresIn, nil
		}

		if isClientError(fetchErr) {
			tm.logger.Error("OAuth2 token request failed with client error",
				"error", fetchErr)
			return "", 0, safeTokenFetchError(fetchErr)
		}

		if attempt < maxAttempts-1 {
			delay := backoffDelay(attempt)
			tm.logger.Warn("OAuth2 token request failed, retrying",
				"attempt", attempt+1,
				"maxAttempts", maxAttempts,
				"retryIn", delay,
				"error", fetchErr)

			select {
			case <-ctx.Done():
				return "", 0, fmt.Errorf("oauth2cc: context cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
			}
		} else {
			tm.logger.Error("OAuth2 token request failed after all retries",
				"attempts", maxAttempts,
				"error", fetchErr)
			err = safeTokenFetchError(fetchErr)
		}
	}
	return "", 0, err
}

func safeTokenFetchError(err error) error {
	var httpErr *tokenHTTPError
	if errors.As(err, &httpErr) {
		return &TokenEndpointError{StatusCode: httpErr.StatusCode, Code: registeredOAuthError(httpErr.Body)}
	}
	return err
}

type TokenEndpointError struct {
	StatusCode int
	Code       string
}

func (e *TokenEndpointError) Error() string {
	return fmt.Sprintf("oauth2cc: token endpoint returned HTTP %d", e.StatusCode)
}

func TokenEndpointErrorCode(err error) string {
	var endpointErr *TokenEndpointError
	if errors.As(err, &endpointErr) {
		return endpointErr.Code
	}
	return ""
}

var registeredOAuthErrors = map[string]bool{
	"invalid_request":           true,
	"invalid_client":            true,
	"invalid_grant":             true,
	"unauthorized_client":       true,
	"unsupported_grant_type":    true,
	"invalid_scope":             true,
	"access_denied":             true,
	"unsupported_response_type": true,
	"server_error":              true,
	"temporarily_unavailable":   true,
}

func registeredOAuthError(body string) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(body), &parsed) != nil || !registeredOAuthErrors[parsed.Error] {
		return ""
	}
	return parsed.Error
}

type tokenHTTPError struct {
	StatusCode int
	Body       string
}

const maxLoggedTokenErrorBodyBytes = 200

const defaultTokenExpiresInSeconds = 3600

func (e *tokenHTTPError) Error() string {
	return fmt.Sprintf("token endpoint returned %d: %s", e.StatusCode, summariseTokenErrorBody(e.Body))
}

func summariseTokenErrorBody(body string) string {
	if body == "" {
		return ""
	}
	var parsed struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err == nil && parsed.Error != "" {
		desc := truncate(parsed.Description, maxLoggedTokenErrorBodyBytes)
		if desc == "" {
			return parsed.Error
		}
		return fmt.Sprintf("%s: %s", parsed.Error, desc)
	}
	return truncate(body, maxLoggedTokenErrorBodyBytes)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "... (truncated)"
}

func isClientError(err error) bool {
	var httpErr *tokenHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= 400 && httpErr.StatusCode < 500
	}
	return false
}

func (tm *TokenManager) fetchToken(ctx context.Context) (string, int, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {tm.cfg.ClientID},
		"client_secret": {tm.cfg.ClientSecret},
	}
	if tm.cfg.Scope != "" {
		data.Set("scope", tm.cfg.Scope)
	}

	tm.logger.Debug("fetching OAuth2 token", "tokenURL", tm.cfg.TokenURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tm.cfg.TokenURL,
		strings.NewReader(data.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("oauth2cc: build request: %w", httputil.TransportErrorCause(err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := tm.client.Do(req)
	if err != nil {
		tm.logger.Debug("token request transport error", "error", httputil.TransportErrorCause(err))
		return "", 0, fmt.Errorf("oauth2cc: token request: %s", httputil.SafeTransportErrorDetail(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("oauth2cc: read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, &tokenHTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", 0, fmt.Errorf("oauth2cc: parse token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", 0, fmt.Errorf("oauth2cc: empty access_token in response")
	}

	return tokenResp.AccessToken, tokenResp.ExpiresIn, nil
}

func backoffDelay(attempt int) time.Duration {
	base := math.Pow(2, float64(attempt))
	jitter := rand.Float64()*2 - 1
	seconds := base + jitter
	if seconds < 0.1 {
		seconds = 0.1
	}
	return time.Duration(seconds * float64(time.Second))
}
