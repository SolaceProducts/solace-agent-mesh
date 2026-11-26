// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"crypto/ecdsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Signer struct {
	keyManager  *KeyManager
	componentID string
	defaultTTL  time.Duration
	maxTTL      time.Duration
}

func NewSigner(km *KeyManager, componentID string, defaultTTL, maxTTL time.Duration) *Signer {
	return &Signer{
		keyManager:  km,
		componentID: componentID,
		defaultTTL:  defaultTTL,
		maxTTL:      maxTTL,
	}
}

func (s *Signer) PublicKey() *ecdsa.PublicKey {
	kp := s.keyManager.CurrentKeyPair()
	if kp == nil {
		return nil
	}
	return kp.PublicKey
}

type SignClaims struct {
	Subject string
	TaskID  string
	Name    string
	Email   string
	Roles   []string
	Scopes  []string
	TTL     time.Duration

	AuthMethod   string
	CredentialID string

	PlatformUserID string
}

const (
	claimAuthMethod     = "auth_method"
	claimCredentialID   = "credential_id"
	ClaimPlatformUserID = "platform_user_id"
)

func (s *Signer) Sign(claims SignClaims) (string, error) {
	kp := s.keyManager.CurrentKeyPair()
	if kp == nil {
		return "", fmt.Errorf("no key pair available")
	}

	ttl := claims.TTL
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	if ttl > s.maxTTL {
		ttl = s.maxTTL
	}

	now := time.Now()
	mapClaims := jwt.MapClaims{
		"iss":     s.componentID,
		"sub":     claims.Subject,
		"iat":     now.Unix(),
		"exp":     now.Add(ttl).Unix(),
		"task_id": claims.TaskID,
	}
	if claims.Name != "" {
		mapClaims["name"] = claims.Name
	}
	if claims.Email != "" {
		mapClaims["email"] = claims.Email
	}
	if len(claims.Roles) > 0 {
		mapClaims["roles"] = claims.Roles
	}
	if len(claims.Scopes) > 0 {
		mapClaims["scopes"] = claims.Scopes
	}
	if claims.AuthMethod != "" {
		mapClaims[claimAuthMethod] = claims.AuthMethod
	}
	if claims.CredentialID != "" {
		mapClaims[claimCredentialID] = claims.CredentialID
	}
	if claims.PlatformUserID != "" {
		mapClaims[ClaimPlatformUserID] = claims.PlatformUserID
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, mapClaims)
	token.Header["kid"] = kp.KeyID

	signed, err := token.SignedString(kp.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}
	return signed, nil
}

func (s *Signer) SignTyped(typ string, claims jwt.Claims) (string, error) {
	kp := s.keyManager.CurrentKeyPair()
	if kp == nil {
		return "", fmt.Errorf("no key pair available")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = kp.KeyID
	if typ != "" {
		token.Header["typ"] = typ
	}
	signed, err := token.SignedString(kp.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("signing JWT (typ=%s): %w", typ, err)
	}
	return signed, nil
}
