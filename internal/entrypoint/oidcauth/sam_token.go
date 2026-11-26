// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type MintClaims struct {
	UserID         string
	Email          string
	Name           string
	Roles          []string
	Scopes         []string
	Provider       string
	PlatformUserID string
}

type SATClaims struct {
	JTI            string   `json:"jti"`
	Subject        string   `json:"sub"`
	Email          string   `json:"email"`
	Name           string   `json:"name"`
	Roles          []string `json:"roles"`
	Scopes         []string `json:"scopes"`
	Provider       string   `json:"provider"`
	IssuedAt       int64    `json:"iat"`
	Expiry         int64    `json:"exp"`
	Issuer         string   `json:"iss"`
	PlatformUserID string   `json:"platform_user_id"`
}

func MintSAMToken(signer *trust.Signer, claims MintClaims, ttl time.Duration) (string, error) {
	if claims.UserID == "" {
		return "", fmt.Errorf("mint SAM token: user_id is required")
	}

	token, err := signer.Sign(trust.SignClaims{
		Subject:        claims.UserID,
		TaskID:         "",
		Name:           claims.Name,
		Email:          claims.Email,
		Roles:          claims.Roles,
		Scopes:         claims.Scopes,
		TTL:            ttl,
		PlatformUserID: claims.PlatformUserID,
	})
	if err != nil {
		return "", fmt.Errorf("sign SAM token: %w", err)
	}

	return token, err
}

func ValidateSAMToken(tokenStr string, signer *trust.Signer) (*SATClaims, error) {

	parser := jwt.NewParser(
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)

	mapClaims, err := parseSAMTokenClaims(parser, tokenStr, signer)
	if err != nil {
		return nil, fmt.Errorf("validate SAM token: %w", err)
	}

	claims := &SATClaims{
		Subject:        getStringClaim(mapClaims, "sub"),
		Email:          getStringClaim(mapClaims, "email"),
		Name:           getStringClaim(mapClaims, "name"),
		Issuer:         getStringClaim(mapClaims, "iss"),
		PlatformUserID: getStringClaim(mapClaims, trust.ClaimPlatformUserID),
	}

	if jti, ok := mapClaims["jti"].(string); ok {
		claims.JTI = jti
	}
	if provider, ok := mapClaims["provider"].(string); ok {
		claims.Provider = provider
	}
	if iat, ok := mapClaims["iat"].(float64); ok {
		claims.IssuedAt = int64(iat)
	}
	if exp, ok := mapClaims["exp"].(float64); ok {
		claims.Expiry = int64(exp)
	}
	if roles, ok := mapClaims["roles"].([]any); ok {
		for _, r := range roles {
			if s, ok := r.(string); ok {
				claims.Roles = append(claims.Roles, s)
			}
		}
	}
	if scopes, ok := mapClaims["scopes"].([]any); ok {
		for _, s := range scopes {
			if str, ok := s.(string); ok {
				claims.Scopes = append(claims.Scopes, str)
			}
		}
	}

	return claims, nil
}

func getStringClaim(m jwt.MapClaims, key string) string {
	v, _ := m[key].(string)
	return v
}

func SAMTokenSession(tokenStr string, signer *trust.Signer) (subject string, issuedAt time.Time, err error) {
	mapClaims, err := parseSAMTokenClaims(jwt.NewParser(jwt.WithoutClaimsValidation()), tokenStr, signer)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read SAM token: %w", err)
	}
	iat, ok := mapClaims["iat"].(float64)
	if !ok {
		return "", time.Time{}, fmt.Errorf("read SAM token: missing iat claim")
	}
	subject = getStringClaim(mapClaims, "sub")
	if subject == "" {
		return "", time.Time{}, fmt.Errorf("read SAM token: missing sub claim")
	}
	return subject, time.Unix(int64(iat), 0), nil
}

func parseSAMTokenClaims(parser *jwt.Parser, tokenStr string, signer *trust.Signer) (jwt.MapClaims, error) {
	token, err := parser.Parse(tokenStr, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return signer.PublicKey(), nil
	})
	if err != nil {
		return nil, err
	}
	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims type")
	}
	return mapClaims, nil
}
