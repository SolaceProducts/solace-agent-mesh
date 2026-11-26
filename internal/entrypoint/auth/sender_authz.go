// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type SenderAuthorizationRequest struct {
	UserID     string
	ClaimKey   string
	ClaimValue string

	ContinuityToken string

	PredecessorToken string

	DiscardBinding bool
}

type SenderAuthorization struct {
	Resolution     identity.LookupResolution
	ResolvedUserID string
	Roles          []string
	Scopes         []string

	ResolvedPrincipal string

	RefusalReason string

	RotationApplied bool

	Profile          *identity.Profile
	EnrichmentStatus identity.EnrichmentStatus
	EnrichmentReason string
}

type SenderAuthorizer interface {
	AuthorizeSender(ctx context.Context, req SenderAuthorizationRequest) (SenderAuthorization, error)
}

type BrokerSenderAuthorizer struct {
	client    AuthorizationClient
	gatewayID string
}

func NewBrokerSenderAuthorizer(client AuthorizationClient, gatewayID string) *BrokerSenderAuthorizer {
	if client == nil {
		return nil
	}
	return &BrokerSenderAuthorizer{client: client, gatewayID: gatewayID}
}

func (a *BrokerSenderAuthorizer) AuthorizeSender(ctx context.Context, req SenderAuthorizationRequest) (SenderAuthorization, error) {
	gctx := map[string]any{"gateway_id": a.gatewayID}
	if req.ClaimKey != "" {
		gctx["identity_claim_key"] = req.ClaimKey
		gctx["identity_claim_value"] = req.ClaimValue
	}
	if req.ContinuityToken != "" {
		gctx["continuity_token"] = req.ContinuityToken
	}
	if req.PredecessorToken != "" {
		gctx["continuity_predecessor"] = req.PredecessorToken
	}
	if req.DiscardBinding {
		gctx["continuity_discard"] = true
	}
	res, err := a.client.Lookup(ctx, req.UserID, gctx)
	if err != nil {
		return SenderAuthorization{}, err
	}
	return SenderAuthorization{
		Resolution:        res.Resolution,
		ResolvedUserID:    res.ResolvedUserID,
		ResolvedPrincipal: res.ResolvedPrincipal,
		RefusalReason:     res.RefusalReason,
		RotationApplied:   res.RotationApplied,
		Roles:             res.Roles,
		Scopes:            res.Scopes,
		Profile:           res.Profile,
		EnrichmentStatus:  res.EnrichmentStatus,
		EnrichmentReason:  res.EnrichmentReason,
	}, nil
}
