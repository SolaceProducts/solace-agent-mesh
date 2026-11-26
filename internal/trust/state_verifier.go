// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type StateVerifier struct {
	v          *Verifier
	seenJTI    JTICache
	knownHosts func() map[string]bool
}

func NewStateVerifierFromComponents(v *Verifier, seenJTI JTICache, knownHosts func() map[string]bool) *StateVerifier {
	return &StateVerifier{v: v, seenJTI: seenJTI, knownHosts: knownHosts}
}

func (sv *StateVerifier) KnownHosts() map[string]bool {
	return sv.knownHosts()
}

func (sv *StateVerifier) VerifyStateToken(ctx context.Context, raw string) (*StateClaims, error) {
	if sv == nil {
		return nil, errors.New("state verifier: nil")
	}
	_ = ctx
	parserOpts := []jwt.ParserOption{
		jwt.WithLeeway(sv.v.clockSkew),
		jwt.WithValidMethods([]string{"ES256"}),
	}
	var claims StateClaims
	token, err := jwt.ParseWithClaims(raw, &claims, sv.v.keyFunc, parserOpts...)
	if err != nil {
		return nil, fmt.Errorf("state JWT: %w", err)
	}

	typ, _ := token.Header["typ"].(string)
	if typ != StateJWTType {
		return nil, fmt.Errorf("state JWT: wrong typ %q", typ)
	}

	u, err := url.Parse(claims.TargetLinkURI)
	if err != nil || u.Host == "" {
		return nil, errors.New("state JWT: target_link_uri invalid")
	}
	if !sv.knownHosts()[u.Host] {
		return nil, fmt.Errorf("state JWT: target_link_uri host %q not in trust registry", u.Host)
	}

	if claims.ID == "" {
		return nil, errors.New("state JWT: missing jti")
	}
	if claims.ExpiresAt == nil {
		return nil, errors.New("state JWT: missing exp")
	}
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl <= 0 {
		return nil, errors.New("state JWT: expired")
	}
	if !sv.seenJTI.MarkOnceWithTTL(claims.ID, ttl) {
		return nil, errors.New("state JWT: jti replay")
	}

	return &claims, nil
}
