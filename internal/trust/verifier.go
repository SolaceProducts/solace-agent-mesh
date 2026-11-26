// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type Verifier struct {
	registry  *Registry
	clockSkew time.Duration
	logger    *slog.Logger

	keyNotInJWKSFailures atomic.Uint64
}

func NewVerifier(registry *Registry, clockSkew time.Duration, logger *slog.Logger) *Verifier {
	return &Verifier{
		registry:  registry,
		clockSkew: clockSkew,
		logger:    logger,
	}
}

func (v *Verifier) Authenticate(_ context.Context, authToken string, taskID string) (*auth.Identity, error) {
	if authToken == "" {
		return nil, nil
	}

	claims, err := v.parseAndVerify(authToken)
	if err != nil {
		v.logVerifyFailure(err)
		return nil, fmt.Errorf("authentication failed")
	}

	jwtTaskID, _ := claims["task_id"].(string)
	if jwtTaskID != taskID {
		v.logger.Warn("JWT task_id mismatch",
			"jwtTaskID", jwtTaskID,
			"requestTaskID", taskID,
		)
		return nil, fmt.Errorf("authentication failed")
	}

	return v.buildIdentity(claims, authToken), nil
}

func (v *Verifier) VerifyWithoutTaskBinding(ctx context.Context, authToken string) (*auth.Identity, error) {
	return v.VerifyWithSoftTaskBinding(ctx, authToken, "")
}

func (v *Verifier) VerifyWithSoftTaskBinding(_ context.Context, authToken string, expectedTaskID string) (*auth.Identity, error) {
	if authToken == "" {
		return nil, nil
	}

	claims, err := v.parseAndVerify(authToken)
	if err != nil {
		v.logVerifyFailure(err)
		return nil, fmt.Errorf("authentication failed")
	}

	if expectedTaskID != "" {
		jwtTaskID, hasTaskID := claims["task_id"].(string)
		switch {
		case !hasTaskID || jwtTaskID == "":
			v.logger.Warn("a2a JWT carries no task_id claim — soft binding accepted",
				"event", "task_id_binding_missing",
				"expectedTaskID", expectedTaskID,
			)
		case jwtTaskID != expectedTaskID:
			v.logger.Warn("a2a JWT task_id claim does not match expected task",
				"event", "task_id_binding_mismatch",
				"jwtTaskID", jwtTaskID,
				"expectedTaskID", expectedTaskID,
			)
			return nil, auth.ErrTaskIDBindingMismatch
		}
	}

	return v.buildIdentity(claims, authToken), nil
}

func (v *Verifier) parseAndVerify(tokenString string) (jwt.MapClaims, error) {
	parserOpts := []jwt.ParserOption{
		jwt.WithLeeway(v.clockSkew),
	}

	token, err := jwt.Parse(tokenString, v.keyFunc, parserOpts...)
	if err != nil {
		return nil, fmt.Errorf("parse JWT: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type")
	}

	return claims, nil
}

func (v *Verifier) logVerifyFailure(err error) {
	if errors.Is(err, errKeyNotInJWKS) {
		if n := v.keyNotInJWKSFailures.Add(1); n == 1 || n%100 == 0 {
			v.logger.Warn("JWT rejected: signing key not in trust registry (expected transiently after a gateway key rotation or restart, until tokens signed by the old key expire)",
				"error", err, "occurrences", n)
		}
		return
	}
	v.logger.Warn("JWT verification failed", "error", err)
}

func (v *Verifier) keyFunc(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodECDSA); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
	}
	if token.Method.Alg() != "ES256" {
		return nil, fmt.Errorf("unsupported algorithm: %s", token.Method.Alg())
	}

	issuer, err := token.Claims.GetIssuer()
	if err != nil || issuer == "" {
		return nil, fmt.Errorf("missing iss claim")
	}

	compType, found := v.registry.GetComponentType(issuer)
	if !found {
		return nil, fmt.Errorf("unknown issuer: %s", issuer)
	}
	if compType != "gateway" && compType != "platform" {
		return nil, fmt.Errorf("issuer %s is %s, not gateway or platform", issuer, compType)
	}

	jwks, found := v.registry.GetJWKS(issuer)
	if !found {
		return nil, fmt.Errorf("no JWKS for issuer %s", issuer)
	}

	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, fmt.Errorf("missing kid header")
	}

	pubKey, err := findPublicKey(jwks, kid)
	if err != nil {
		return nil, fmt.Errorf("key lookup: %w", err)
	}

	return pubKey, nil
}

var errKeyNotInJWKS = errors.New("not found in JWKS")

func findPublicKey(jwks map[string]any, kid string) (*ecdsa.PublicKey, error) {
	keysRaw, ok := jwks["keys"]
	if !ok {
		return nil, fmt.Errorf("JWKS has no 'keys' field")
	}
	keys, ok := keysRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("JWKS 'keys' is not an array")
	}

	for _, keyRaw := range keys {
		keyMap, ok := keyRaw.(map[string]any)
		if !ok {
			continue
		}
		keyKID, _ := keyMap["kid"].(string)
		if keyKID != kid {
			continue
		}

		kty, _ := keyMap["kty"].(string)
		crv, _ := keyMap["crv"].(string)
		if kty != "EC" || crv != "P-256" {
			return nil, fmt.Errorf("key %s: unsupported type %s/%s", kid, kty, crv)
		}

		xStr, _ := keyMap["x"].(string)
		yStr, _ := keyMap["y"].(string)
		if xStr == "" || yStr == "" {
			return nil, fmt.Errorf("key %s: missing x or y coordinate", kid)
		}

		xBytes, err := base64.RawURLEncoding.DecodeString(xStr)
		if err != nil {
			return nil, fmt.Errorf("key %s: decode x: %w", kid, err)
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(yStr)
		if err != nil {
			return nil, fmt.Errorf("key %s: decode y: %w", kid, err)
		}

		pubKey, err := auth.P256PublicKey(xBytes, yBytes)
		if err != nil {
			return nil, fmt.Errorf("key %s: %w", kid, err)
		}
		return pubKey, nil
	}

	return nil, fmt.Errorf("key %s %w", kid, errKeyNotInJWKS)
}

func (v *Verifier) buildIdentity(claims jwt.MapClaims, authToken string) *auth.Identity {
	identity := &auth.Identity{
		AuthToken: authToken,
		Claims:    claims,
	}

	if sub, ok := claims["sub"].(string); ok {
		identity.UserID = sub
	}
	identity.AuthMethod, _ = claims[claimAuthMethod].(string)
	identity.CredentialID, _ = claims[claimCredentialID].(string)
	identity.PlatformUserID, _ = claims[ClaimPlatformUserID].(string)

	if roles, ok := claims["roles"].([]any); ok {
		for _, r := range roles {
			if s, ok := r.(string); ok {
				identity.Roles = append(identity.Roles, s)
			}
		}
	}

	if scopes, ok := claims["scopes"].([]any); ok {
		for _, s := range scopes {
			if str, ok := s.(string); ok {
				identity.Scopes = append(identity.Scopes, str)
			}
		}
	}

	return identity
}

func (v *Verifier) AuthenticateWithoutTaskBinding(ctx context.Context, authToken string) (*auth.Identity, error) {
	return v.VerifyWithoutTaskBinding(ctx, authToken)
}

func (v *Verifier) AuthenticateWithSoftTaskBinding(ctx context.Context, authToken string, expectedTaskID string) (*auth.Identity, error) {
	return v.VerifyWithSoftTaskBinding(ctx, authToken, expectedTaskID)
}

var _ auth.Authenticator = (*Verifier)(nil)
var _ auth.PeerAuthenticator = (*Verifier)(nil)
