// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package freellm

import (
	"context"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/solaceid"
)

const GrantID = llm.FreeModelGrantID

const (
	grantName = "Solace Agent Mesh free model"
	provider  = "openai"
)

func (c Config) Settings() activation.FreeModelSettings {
	return activation.FreeModelSettings{
		GrantID:           GrantID,
		Provider:          provider,
		APIBase:           c.BaseURL + "/v1",
		GeneralModelName:  c.GeneralModel,
		PlanningModelName: c.PlanningModel,
	}
}

type Grants interface {
	Save(ctx context.Context, id string, req tokenbroker.SaveRequest) error
	Delete(ctx context.Context, id string) error
}

type TokenEndpointFunc func(ctx context.Context) (string, error)

type Platform struct {
	cfg           Config
	sid           solaceid.Config
	tokenEndpoint TokenEndpointFunc
	grants        Grants
	tokens        llm.GrantTokens
}

func NewPlatform(cfg Config, sid solaceid.Config, tokenEndpoint TokenEndpointFunc, grants Grants, tokens llm.GrantTokens) *Platform {
	return &Platform{cfg: cfg, sid: sid, tokenEndpoint: tokenEndpoint, grants: grants, tokens: tokens}
}

func (p *Platform) SaveGrant(ctx context.Context, result activation.SignInResult) error {
	tokenEndpoint, err := p.tokenEndpoint(ctx)
	if err != nil {
		return fmt.Errorf("find the Solace ID token endpoint: %w", err)
	}
	return p.grants.Save(ctx, GrantID, tokenbroker.SaveRequest{
		Name:           grantName,
		Flow:           store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer:         p.sid.Issuer,
		TokenEndpoint:  tokenEndpoint,
		ClientID:       p.sid.ClientID,
		Audience:       p.cfg.Audience,
		Scopes:         strings.Join(solaceid.Scopes(p.cfg.Audience), " "),
		APIBase:        p.cfg.BaseURL,
		AccountSubject: result.Subject,
		AccountLabel:   result.Email,
		RefreshToken:   result.Tokens.RefreshToken,
		AccessToken:    result.Tokens.AccessToken,
		ExpiresAt:      result.Tokens.ExpiresAt,
	})
}

func (p *Platform) Refresh(ctx context.Context) error {
	current, err := p.tokens.GrantToken(ctx, GrantID, "")
	if err != nil {
		return fmt.Errorf("read the free model's token: %w", err)
	}
	if _, err := p.tokens.GrantToken(ctx, GrantID, llm.TokenFingerprint(current.AccessToken)); err != nil {
		return fmt.Errorf("refresh the free model's grant: %w", err)
	}
	return nil
}

func (p *Platform) DeleteGrant(ctx context.Context) error {
	return p.grants.Delete(ctx, GrantID)
}
