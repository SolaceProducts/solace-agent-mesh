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
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

var ErrRefreshTokenRejected = errors.New("oauth2cc: refresh token rejected")

const defaultAccessTokenLifetime = time.Hour

type RefreshRequest struct {
	TokenURL     string
	ClientID     string
	RefreshToken string
}

type RefreshedTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func Refresh(ctx context.Context, client *http.Client, req RefreshRequest) (RefreshedTokens, error) {
	if req.TokenURL == "" || req.ClientID == "" || req.RefreshToken == "" {
		return RefreshedTokens{}, errors.New("oauth2cc: token URL, client ID and refresh token are required")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {req.ClientID},
		"refresh_token": {req.RefreshToken},
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return RefreshedTokens{}, fmt.Errorf("oauth2cc: build refresh request: %w", httputil.TransportErrorCause(err))
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	issuedAt := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return RefreshedTokens{}, fmt.Errorf("oauth2cc: refresh request: %s", httputil.SafeTransportErrorDetail(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RefreshedTokens{}, fmt.Errorf("oauth2cc: read refresh response: %w", err)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
	}
	decodeErr := json.Unmarshal(body, &tokenResp)
	if resp.StatusCode != http.StatusOK {
		if tokenResp.Error == "invalid_grant" {
			return RefreshedTokens{}, ErrRefreshTokenRejected
		}
		return RefreshedTokens{}, safeTokenFetchError(&tokenHTTPError{StatusCode: resp.StatusCode, Body: string(body)})
	}
	if decodeErr != nil {
		return RefreshedTokens{}, fmt.Errorf("oauth2cc: parse refresh response: %w", decodeErr)
	}
	if tokenResp.AccessToken == "" {
		return RefreshedTokens{}, errors.New("oauth2cc: empty access_token in refresh response")
	}

	lifetime := time.Duration(tokenResp.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = defaultAccessTokenLifetime
	}
	refreshed := RefreshedTokens{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    issuedAt.Add(lifetime),
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = req.RefreshToken
	}
	return refreshed, nil
}
