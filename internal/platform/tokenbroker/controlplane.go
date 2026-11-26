// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokenbroker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	ControlResource = "model-grants"
	ControlAction   = "token"

	platformControlApp  = "platform"
	controlPostMethod   = "post"
	controlPutMethod    = "put"
	controlDeleteMethod = "delete"
)

type TokenRequest struct {
	RejectedTokenFingerprint string `json:"rejectedTokenFingerprint,omitempty"`
}

type TokenResponse struct {
	AccessToken             string    `json:"accessToken,omitempty"`
	ExpiresAt               time.Time `json:"expiresAt,omitzero"`
	ReauthorizationRequired bool      `json:"reauthorizationRequired,omitempty"`
}

func (s *Service) Answer(ctx context.Context, id string, req TokenRequest) (TokenResponse, error) {
	token, err := s.GrantToken(ctx, id, req.RejectedTokenFingerprint)
	if errors.Is(err, llm.ErrReauthorizationRequired) {
		return TokenResponse{ReauthorizationRequired: true}, nil
	}
	if errors.Is(err, ErrNotFound) {
		return TokenResponse{}, fmt.Errorf("OAuth grant %s: %w", id, control.ErrResourceNotFound)
	}
	if err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{AccessToken: token.AccessToken, ExpiresAt: token.ExpiresAt}, nil
}

type ControlPlaneTokens struct {
	client *control.Client
}

func NewControlPlaneTokens(brk broker.Broker, namespace string) *ControlPlaneTokens {
	return &ControlPlaneTokens{client: control.NewClient(brk, namespace)}
}

func (c *ControlPlaneTokens) GrantToken(ctx context.Context, grantID, rejectedFingerprint string) (llm.GrantToken, error) {
	body, err := json.Marshal(TokenRequest{RejectedTokenFingerprint: rejectedFingerprint})
	if err != nil {
		return llm.GrantToken{}, fmt.Errorf("encode OAuth grant token request: %w", err)
	}
	raw, err := c.client.Custom(ctx, platformControlApp, controlPostMethod, ControlResource+"/"+grantID+"/"+ControlAction, body)
	if err != nil {
		var ce *control.ClientError
		if errors.As(err, &ce) && ce.Code == control.ErrNotFound {
			return llm.GrantToken{}, fmt.Errorf("OAuth grant %s: %w: %w", grantID, ErrNotFound, llm.ErrReauthorizationRequired)
		}
		return llm.GrantToken{}, fmt.Errorf("platform OAuth grant token: %w", err)
	}
	var resp TokenResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return llm.GrantToken{}, fmt.Errorf("decode OAuth grant token response: %w", err)
	}
	if resp.ReauthorizationRequired {
		return llm.GrantToken{}, reauthorizationRequired(grantID)
	}
	if resp.AccessToken == "" {
		return llm.GrantToken{}, fmt.Errorf("platform returned no access token for OAuth grant %s", grantID)
	}
	return llm.GrantToken{AccessToken: resp.AccessToken, ExpiresAt: resp.ExpiresAt}, nil
}

type SaveRequest struct {
	Name           string    `json:"name"`
	Flow           string    `json:"flow"`
	Issuer         string    `json:"issuer"`
	TokenEndpoint  string    `json:"tokenEndpoint"`
	ClientID       string    `json:"clientId"`
	Audience       string    `json:"audience,omitempty"`
	Scopes         string    `json:"scopes,omitempty"`
	APIBase        string    `json:"apiBase"`
	AccountSubject string    `json:"accountSubject"`
	AccountLabel   string    `json:"accountLabel,omitempty"`
	RefreshToken   string    `json:"refreshToken"`
	AccessToken    string    `json:"accessToken,omitempty"`
	ExpiresAt      time.Time `json:"expiresAt,omitzero"`
}

func (r SaveRequest) Grant(id string) *store.ModelOAuthGrant {
	var expires int64
	if !r.ExpiresAt.IsZero() {
		expires = r.ExpiresAt.UnixMilli()
	}
	return &store.ModelOAuthGrant{
		ID:                     id,
		Name:                   r.Name,
		Flow:                   r.Flow,
		Issuer:                 r.Issuer,
		TokenEndpoint:          r.TokenEndpoint,
		ClientID:               r.ClientID,
		Audience:               r.Audience,
		Scopes:                 r.Scopes,
		APIBase:                r.APIBase,
		AccountSubject:         r.AccountSubject,
		AccountLabel:           r.AccountLabel,
		RefreshToken:           r.RefreshToken,
		AccessToken:            r.AccessToken,
		AccessTokenExpiresTime: expires,
	}
}

type ControlPlaneGrants struct {
	client *control.Client
}

func NewControlPlaneGrants(brk broker.Broker, namespace string) *ControlPlaneGrants {
	return &ControlPlaneGrants{client: control.NewClient(brk, namespace)}
}

func (c *ControlPlaneGrants) Save(ctx context.Context, id string, req SaveRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode OAuth grant: %w", err)
	}
	if _, err := c.client.Custom(ctx, platformControlApp, controlPutMethod, ControlResource+"/"+id, body); err != nil {
		var ce *control.ClientError
		if errors.As(err, &ce) && ce.Code == control.ErrConflict {
			return fmt.Errorf("save OAuth grant %s: %w", id, ErrGrantInUse)
		}
		return fmt.Errorf("save OAuth grant %s: %w", id, err)
	}
	return nil
}

func (c *ControlPlaneGrants) Delete(ctx context.Context, id string) error {
	if _, err := c.client.Custom(ctx, platformControlApp, controlDeleteMethod, ControlResource+"/"+id, nil); err != nil {
		var ce *control.ClientError
		if errors.As(err, &ce) && ce.Code == control.ErrConflict {
			return fmt.Errorf("delete OAuth grant %s: %w", id, ErrGrantInUse)
		}
		return fmt.Errorf("delete OAuth grant %s: %w", id, err)
	}
	return nil
}
