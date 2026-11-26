// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

type AuthType string

const (
	AuthTypeUnset                   AuthType = ""
	AuthTypeNone                    AuthType = "none"
	AuthTypeBearer                  AuthType = "bearer"
	AuthTypeAPIKey                  AuthType = "api_key"
	AuthTypeBasic                   AuthType = "basic"
	AuthTypeOAuth2AuthorizationCode AuthType = "oauth2_authorization_code"
	AuthTypeOAuth2ClientCredentials AuthType = "oauth2_client_credentials"
	AuthTypeServiceAccount          AuthType = "serviceaccount"
)

func (t AuthType) IsOAuth2() bool {
	switch t {
	case AuthTypeOAuth2AuthorizationCode, AuthTypeOAuth2ClientCredentials:
		return true
	}
	return false
}

func (t AuthType) RequiresToolsetExchange() bool {
	switch t {
	case AuthTypeOAuth2ClientCredentials, AuthTypeServiceAccount:
		return true
	}
	return false
}
