// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SAMOAuth2Handler struct {
	externalAuthServiceURL string
	callbackURL            string
	provider               string
	httpClient             *http.Client
	logger                 *slog.Logger
}

type SAMOAuth2Config struct {
	ExternalAuthServiceURL string
	CallbackURL            string
	Provider               string
	HTTPClient             *http.Client
	Logger                 *slog.Logger
}

type TokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

func NewSAMOAuth2Handler(cfg SAMOAuth2Config) (*SAMOAuth2Handler, error) {
	serviceURL := strings.TrimRight(cfg.ExternalAuthServiceURL, "/")
	if serviceURL == "" {
		return nil, fmt.Errorf("SAMOAuth2Handler: external_auth_service_url must be specified")
	}
	if cfg.CallbackURL == "" {
		return nil, fmt.Errorf("SAMOAuth2Handler: callback_url must be specified")
	}

	provider := cfg.Provider
	if provider == "" {
		provider = "unknown"
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "sam-oauth2-handler")

	logger.Info("initialized SAMOAuth2Handler",
		"provider", provider,
		"callback", cfg.CallbackURL,
		"proxy", serviceURL,
	)

	return &SAMOAuth2Handler{
		externalAuthServiceURL: serviceURL,
		callbackURL:            cfg.CallbackURL,
		provider:               provider,
		httpClient:             client,
		logger:                 logger,
	}, nil
}

func (h *SAMOAuth2Handler) AuthorizeURL(state string) string {
	params := url.Values{}
	params.Set("gateway_uri", h.callbackURL)
	if state != "" {
		params.Set("state", state)
	}

	authURL := h.externalAuthServiceURL + "/api/v1/gateway-oauth/authorize?" + params.Encode()

	h.logger.Debug("initiating OAuth2 authorization flow via proxy",
		"proxy", h.externalAuthServiceURL,
	)

	return authURL
}

func (h *SAMOAuth2Handler) ExchangeCode(ctx context.Context, code string) (*TokenData, error) {
	if code == "" {
		return nil, fmt.Errorf("SAMOAuth2Handler: gateway code is empty")
	}

	h.logger.Debug("exchanging gateway code for tokens")

	exchangeURL := h.externalAuthServiceURL + "/api/v1/gateway-oauth/exchange"

	body, err := json.Marshal(map[string]string{
		"code":        code,
		"gateway_uri": h.callbackURL,
	})
	if err != nil {
		return nil, fmt.Errorf("SAMOAuth2Handler: marshal exchange request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("SAMOAuth2Handler: create exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SAMOAuth2Handler: exchange gateway code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("SAMOAuth2Handler: read exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		h.logger.Warn("gateway code exchange failed",
			"httpStatus", resp.StatusCode,
		)
		return nil, fmt.Errorf("SAMOAuth2Handler: exchange failed with HTTP %d", resp.StatusCode)
	}

	var tokenData TokenData
	if err := json.Unmarshal(respBody, &tokenData); err != nil {
		return nil, fmt.Errorf("SAMOAuth2Handler: decode exchange response: %w", err)
	}

	if tokenData.AccessToken == "" {
		return nil, fmt.Errorf("SAMOAuth2Handler: exchange response missing access_token")
	}

	h.logger.Debug("successfully obtained OAuth2 tokens")

	return &tokenData, nil
}
