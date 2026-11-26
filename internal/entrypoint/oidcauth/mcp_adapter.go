// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type mcpSAMMinter struct {
	signer *trust.Signer
	ttl    time.Duration
}

func NewMCPSAMMinter(signer *trust.Signer, ttl time.Duration) mcpauth.SAMTokenMinter {
	return &mcpSAMMinter{signer: signer, ttl: ttl}
}

func (m *mcpSAMMinter) MintForUser(userID, platformUserID, email, name string, roles, scopes []string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = m.ttl
	}
	return MintSAMToken(m.signer, MintClaims{
		UserID:         userID,
		PlatformUserID: platformUserID,
		Email:          email,
		Name:           name,
		Roles:          roles,
		Scopes:         scopes,
	}, ttl)
}

func (m *mcpSAMMinter) TTL() time.Duration { return m.ttl }

type mcpAuthzResolver struct {
	svc *AuthorizationService
}

func NewMCPAuthzResolver(svc *AuthorizationService) mcpauth.AuthzResolver {
	return &mcpAuthzResolver{svc: svc}
}

func (r *mcpAuthzResolver) Lookup(ctx context.Context, userID string, gatewayContext map[string]any) (roles, scopes []string, platformUserID string, err error) {
	res, err := r.svc.Lookup(ctx, userID, gatewayContext)
	if err != nil {
		return nil, nil, "", err
	}
	return res.Roles, res.Scopes, res.MatchedUserID(), nil
}

type mcpIDPClient struct {
	inner OIDCClient
}

func NewMCPIDPClient(inner OIDCClient) mcpauth.IDPClient {
	return &mcpIDPClient{inner: inner}
}

func (c *mcpIDPClient) BuildAuthURL(ctx context.Context, state, redirectURI, codeChallenge, codeChallengeMethod string) (string, error) {
	return c.inner.BuildAuthURL(ctx, state, redirectURI, codeChallenge, codeChallengeMethod)
}

func (c *mcpIDPClient) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*mcpauth.IDPTokens, error) {
	tokens, err := c.inner.ExchangeCode(ctx, code, redirectURI, codeVerifier)
	if err != nil {
		return nil, err
	}
	return &mcpauth.IDPTokens{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		IDToken:      tokens.IDToken,
		ExpiresIn:    tokens.ExpiresIn,
	}, nil
}

func (c *mcpIDPClient) GetUserInfo(ctx context.Context, accessToken string) (*mcpauth.UserInfo, error) {
	ui, err := c.inner.GetUserInfo(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	return &mcpauth.UserInfo{
		Sub:   ui.Sub,
		Email: ui.Email,
		Name:  ui.Name,
		Raw:   ui.Claims,
	}, nil
}

func (c *mcpIDPClient) RefreshToken(ctx context.Context, refreshToken string) (*mcpauth.IDPTokens, error) {
	tokens, err := c.inner.RefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	return &mcpauth.IDPTokens{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		IDToken:      tokens.IDToken,
		ExpiresIn:    tokens.ExpiresIn,
	}, nil
}
