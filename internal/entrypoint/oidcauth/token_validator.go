// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
	"github.com/golang-jwt/jwt/v5"
)

type TokenValidator struct {
	signer    *trust.Signer
	authzSvc  *AuthorizationService
	provider  *Provider
	gatewayID string
	logger    *slog.Logger
}

func NewTokenValidator(signer *trust.Signer, authzSvc *AuthorizationService, provider *Provider, gatewayID string, logger *slog.Logger) *TokenValidator {
	if logger == nil {
		logger = slog.Default()
	}
	return &TokenValidator{
		signer:    signer,
		authzSvc:  authzSvc,
		provider:  provider,
		gatewayID: gatewayID,
		logger:    logger.With("logger_name", "enterprise-token-validator"),
	}
}

func (v *TokenValidator) Validate(ctx context.Context, token string) (*auth.Subject, error) {
	if apikey.HasMarker(strings.TrimSpace(token)) {
		observability.RecordAuthTokenValidationFailure(v.gatewayID, auth.APIKeyReasonSurfaceNotSupported)
		return nil, errAPIKeyNotAccepted
	}
	identity, samErr := auth.ForSAMAccessToken(token, samTokenVerifier{v.signer}, NewScopeExpander(v.authzSvc))
	if samErr == nil {
		return identity, nil
	}
	v.logger.Debug("SAM token validation failed; falling through to IdP", "error", samErr)
	if v.provider == nil {
		v.recordValidationFailure(samErr)
		return nil, fmt.Errorf("token validator: %w", samErr)
	}
	identity, idpErr := v.provider.IdentityFromIDPToken(ctx, token)
	if idpErr != nil {
		v.recordValidationFailure(samErr)
		return nil, idpErr
	}
	return identity, nil
}

func (v *TokenValidator) recordValidationFailure(samErr error) {
	reason := authReasonInvalidToken
	if errors.Is(samErr, jwt.ErrTokenExpired) {
		reason = authReasonExpiredToken
	}
	observability.RecordAuthTokenValidationFailure(v.gatewayID, reason)
}

var errAPIKeyNotAccepted = errors.New("token validator: api keys are not accepted on this surface")

var _ auth.TokenValidator = (*TokenValidator)(nil)
