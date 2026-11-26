// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"context"
	"time"
)

type UserInfo struct {
	Sub   string
	Email string
	Name  string
	Raw   map[string]any
}

type IDPTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    int
}

type IDPClient interface {
	BuildAuthURL(ctx context.Context, state, redirectURI, codeChallenge, codeChallengeMethod string) (string, error)

	ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*IDPTokens, error)

	GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error)

	RefreshToken(ctx context.Context, refreshToken string) (*IDPTokens, error)
}

type SAMTokenMinter interface {
	MintForUser(canonicalUserID, platformUserID, email, name string, roles, scopes []string, ttl time.Duration) (string, error)
	TTL() time.Duration
}

type AuthzResolver interface {
	Lookup(ctx context.Context, userID string, gatewayContext map[string]any) (roles, scopes []string, platformUserID string, err error)
}
