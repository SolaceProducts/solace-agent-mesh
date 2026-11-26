// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"testing"
)

func openAPIOAuthValues(method, pkce string) map[string]any {
	values := map[string]any{
		"specification_url":                     "https://api.example.com/openapi.json",
		"base_url":                              "https://api.example.com",
		"auth_type":                             "oauth",
		"auth_oauth_authorization_url":          "https://idp.example.com/authorize",
		"auth_oauth_token_url":                  "https://idp.example.com/token",
		"auth_oauth_client_id":                  "orders",
		"auth_oauth_token_endpoint_auth_method": method,
	}
	if pkce != "" {
		values["auth_oauth_pkce"] = pkce
	}
	return values
}

func TestValidateConnectorValues_PublicOpenAPIClientCannotDisablePKCE(t *testing.T) {
	t.Parallel()
	err := validateConnectorValues("api", "openapi", openAPIOAuthValues("none", "disabled"))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	if ve.Field != "values.auth_oauth_pkce" || ve.Message != publicClientWithoutPKCEMsg {
		t.Errorf("error = %q on %q, want the PKCE message on values.auth_oauth_pkce", ve.Message, ve.Field)
	}
}

func TestValidateConnectorValues_PKCESettingsThatAreAllowed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, pkce string
	}{
		{"public client with PKCE left unset is accepted", "none", ""},
		{"public client with PKCE enabled is accepted", "none", "enabled"},
		{"confidential basic client may disable PKCE", "client_secret_basic", "disabled"},
		{"confidential post client with PKCE enabled is accepted", "client_secret_post", "enabled"},
		{"confidential basic client with PKCE left unset is accepted", "client_secret_basic", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateConnectorValues("api", "openapi", openAPIOAuthValues(tc.method, tc.pkce)); err != nil {
				t.Errorf("validateConnectorValues(method %q, pkce %q) = %v, want accepted", tc.method, tc.pkce, err)
			}
		})
	}
}

func TestValidateConnectorValues_UnknownPKCEValueIsRefused(t *testing.T) {
	t.Parallel()
	err := validateConnectorValues("api", "openapi", openAPIOAuthValues("client_secret_basic", "sometimes"))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("validateConnectorValues(pkce %q) = %v, want a ValidationError", "sometimes", err)
	}
	const want = `field "auth_oauth_pkce" value "sometimes" is not one of [enabled, disabled]`
	if ve.Message != want {
		t.Errorf("validateConnectorValues(pkce %q) message = %q, want %q", "sometimes", ve.Message, want)
	}
}

func TestBuildOpenAPIOAuth_PKCE(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, pkce string
		want               bool
	}{
		{"confidential client with PKCE unset sends PKCE", "client_secret_basic", "", true},
		{"confidential client with PKCE enabled sends PKCE", "client_secret_basic", "enabled", true},
		{"confidential client with PKCE disabled sends no PKCE", "client_secret_post", "disabled", false},
		{"public client with PKCE unset sends PKCE", "none", "", true},
		{"public client with PKCE disabled still sends PKCE", "none", "disabled", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			auth := buildConnectorTool(apiConnector(openAPIOAuthValues(tc.method, tc.pkce)))["auth"].(map[string]any)
			if got, _ := auth["use_pkce"].(bool); got != tc.want {
				t.Errorf("buildConnectorTool(method %q, pkce %q) use_pkce = %v, want %v", tc.method, tc.pkce, auth["use_pkce"], tc.want)
			}
		})
	}
}
