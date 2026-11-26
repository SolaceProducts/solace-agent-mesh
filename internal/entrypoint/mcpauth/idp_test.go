// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"context"
	"testing"
	"time"
)

type fakeIDPClient struct{}

func (fakeIDPClient) BuildAuthURL(_ context.Context, state, redirectURI, codeChallenge, codeChallengeMethod string) (string, error) {
	return "https://idp.example.com/authorize", nil
}
func (fakeIDPClient) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*IDPTokens, error) {
	return &IDPTokens{AccessToken: "idp-at", RefreshToken: "idp-rt", ExpiresIn: 3600}, nil
}
func (fakeIDPClient) GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	return &UserInfo{Sub: "u-1", Email: "u@example.com", Name: "U"}, nil
}
func (fakeIDPClient) RefreshToken(ctx context.Context, refreshToken string) (*IDPTokens, error) {
	return &IDPTokens{AccessToken: "idp-at-2", RefreshToken: "idp-rt-2", ExpiresIn: 3600}, nil
}

type fakeMinter struct {
	LastUserID         string
	LastPlatformUserID string
	LastEmail          string
	LastName           string
	LastRoles          []string
	LastScopes         []string
	LastTTL            time.Duration
}

func (m *fakeMinter) MintForUser(userID, platformUserID, email, name string, roles, scopes []string, ttl time.Duration) (string, error) {
	m.LastUserID = userID
	m.LastPlatformUserID = platformUserID
	m.LastEmail = email
	m.LastName = name
	m.LastRoles = roles
	m.LastScopes = scopes
	m.LastTTL = ttl
	return "sam-token", nil
}
func (m *fakeMinter) TTL() time.Duration { return time.Hour }

type fakeResolver struct{}

func (fakeResolver) Lookup(_ context.Context, userID string, ctx map[string]any) (roles, scopes []string, platformUserID string, err error) {
	return []string{"viewer"}, nil, "", nil
}

func TestInterfaces_CompileTime(t *testing.T) {
	var _ IDPClient = fakeIDPClient{}
	var _ SAMTokenMinter = &fakeMinter{}
	var _ AuthzResolver = fakeResolver{}
}
