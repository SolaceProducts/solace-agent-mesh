// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import "github.com/golang-jwt/jwt/v5"

const StateJWTType = "sol-state+jwt"

type StateClaims struct {
	TargetLinkURI string `json:"target_link_uri"`

	GwPKCEVerifierHash string `json:"gw_pkce_verifier_hash"`

	jwt.RegisteredClaims
}
