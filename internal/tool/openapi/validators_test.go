// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"log/slog"
	"strings"
	"testing"
)

func TestValidateOpenAPIVersion_RejectsSwagger2(t *testing.T) {
	spec := []byte(`{"swagger": "2.0", "info": {"title": "old"}}`)
	err := validateOpenAPIVersion(spec)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Swagger 2.0") {
		t.Errorf("missing 'Swagger 2.0' in error: %q", msg)
	}
	if !strings.Contains(msg, "https://converter.swagger.io") {
		t.Errorf("missing converter link: %q", msg)
	}
}

func TestValidateOpenAPIVersion_AcceptsOpenAPI3(t *testing.T) {
	for _, v := range []string{"3.0.0", "3.0.3", "3.1.0"} {
		t.Run(v, func(t *testing.T) {
			spec := []byte(`{"openapi":"` + v + `"}`)
			if err := validateOpenAPIVersion(spec); err != nil {
				t.Errorf("unexpected err for %s: %v", v, err)
			}
		})
	}
}

func TestValidateOpenAPIVersion_RejectsMissingField(t *testing.T) {
	spec := []byte(`{"info": {"title": "unknown"}}`)
	err := validateOpenAPIVersion(spec)
	if err == nil || !strings.Contains(err.Error(), "missing 'openapi' version field") {
		t.Errorf("unexpected: %v", err)
	}
}

func TestValidateOpenAPIVersion_RejectsOpenAPI2(t *testing.T) {
	spec := []byte(`{"openapi": "2.0"}`)
	err := validateOpenAPIVersion(spec)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("unexpected: %v", err)
	}
}

func TestValidateOpenAPIVersion_AcceptsNumericYAML(t *testing.T) {
	spec := []byte("openapi: 3.0\ninfo:\n  title: t\n  version: \"1\"\npaths: {}\n")
	if err := validateOpenAPIVersion(spec); err != nil {
		t.Errorf("numeric 3.0 should be accepted, got %v", err)
	}
}

func TestValidateOpenAPIVersion_AcceptsString31(t *testing.T) {
	spec := []byte(`{"openapi": "3.1.0", "info": {"title": "t", "version": "1"}, "paths": {}}`)
	if err := validateOpenAPIVersion(spec); err != nil {
		t.Errorf("string 3.1.0 should be accepted, got %v", err)
	}
}

func TestValidateOpenAPIVersion_RejectsNumeric2(t *testing.T) {
	spec := []byte("openapi: 2.0\ninfo:\n  title: t\n  version: \"1\"\npaths: {}\n")
	err := validateOpenAPIVersion(spec)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("numeric 2.0 should be rejected, got %v", err)
	}
}

func TestOpenAPIMajorVersion(t *testing.T) {
	cases := []struct {
		in        any
		wantMajor int
		wantOK    bool
	}{
		{"3.0.1", 3, true},
		{"3.1", 3, true},
		{"2.0", 2, true},
		{float64(3.0), 3, true},
		{float64(3.1), 3, true},
		{float32(2.0), 2, true},
		{int(3), 3, true},
		{int64(3), 3, true},
		{"", 0, false},
		{"foo", 0, false},
		{nil, 0, false},
	}
	for _, tc := range cases {
		got, _, ok := openAPIMajorVersion(tc.in)
		if ok != tc.wantOK || (ok && got != tc.wantMajor) {
			t.Errorf("openAPIMajorVersion(%#v) = (%d, _, %v); want (%d, _, %v)", tc.in, got, ok, tc.wantMajor, tc.wantOK)
		}
	}
}

func TestValidateSpecSources_ExactlyOne(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ToolsetConfig
		wantErr string
	}{
		{"only url", ToolsetConfig{SpecURL: "http://ex.com"}, ""},
		{"only file", ToolsetConfig{SpecFile: "/tmp/spec.yaml"}, ""},
		{"only inline", ToolsetConfig{SpecInline: "x"}, ""},
		{"none", ToolsetConfig{}, "Must specify one of"},
		{"url + file", ToolsetConfig{SpecURL: "a", SpecFile: "b"}, "Cannot specify more than one"},
		{"url + inline", ToolsetConfig{SpecURL: "a", SpecInline: "b"}, "Cannot specify more than one"},
		{"all three", ToolsetConfig{SpecURL: "a", SpecFile: "b", SpecInline: "c"}, "Cannot specify more than one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSpecSources(&tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected err: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("got err %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateFilterLists_AllowsEmpty(t *testing.T) {
	if err := validateFilterLists(nil, nil); err != nil {
		t.Errorf("empty should be fine: %v", err)
	}
	if err := validateFilterLists([]string{}, []string{}); err != nil {
		t.Errorf("empty should be fine: %v", err)
	}
}

func TestValidateFilterLists_RejectsEmptyString(t *testing.T) {
	err := validateFilterLists([]string{"getUser", ""}, nil)
	if err == nil || !strings.Contains(err.Error(), "allow_list[1]") {
		t.Errorf("got %v", err)
	}
	err = validateFilterLists(nil, []string{"", "x"})
	if err == nil || !strings.Contains(err.Error(), "deny_list[0]") {
		t.Errorf("got %v", err)
	}
}

func TestValidateOperationIDUniqueness_DetectsSourceDuplicates(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "paths": {
	   "/a": {"get": {"operationId": "listStuff"}},
	   "/b": {"get": {"operationId": "listStuff"}}
	 }
	}`)
	err := validateOperationIDUniqueness(spec, nil, nil)
	if err == nil {
		t.Fatal("expected duplicate error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Duplicate operation IDs") {
		t.Errorf("unexpected: %q", msg)
	}
	if !strings.Contains(msg, "listStuff") {
		t.Errorf("missing id in: %q", msg)
	}
}

func TestValidateOperationIDUniqueness_DetectsSnakeCaseCollision(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "paths": {
	   "/a": {"get": {"operationId": "getUser"}},
	   "/b": {"get": {"operationId": "get_user"}}
	 }
	}`)
	err := validateOperationIDUniqueness(spec, nil, nil)
	if err == nil {
		t.Fatal("expected snake-case collision error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "collision after snake_case conversion") {
		t.Errorf("unexpected: %q", msg)
	}
	if !strings.Contains(msg, "'getUser'") || !strings.Contains(msg, "'get_user'") {
		t.Errorf("missing ids: %q", msg)
	}
}

func TestValidateOperationIDUniqueness_AllowListFiltersCheck(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "paths": {
	   "/a": {"get": {"operationId": "getUser"}},
	   "/b": {"get": {"operationId": "get_user"}},
	   "/c": {"get": {"operationId": "listItems"}}
	 }
	}`)
	if err := validateOperationIDUniqueness(spec, []string{"listItems"}, nil); err != nil {
		t.Errorf("should ignore filtered-out collisions: %v", err)
	}
}

func TestValidateOperationIDUniqueness_DenyListSkipsChecks(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "paths": {
	   "/a": {"get": {"operationId": "getUser"}},
	   "/b": {"get": {"operationId": "get_user"}}
	 }
	}`)
	if err := validateOperationIDUniqueness(spec, nil, []string{"get_user"}); err != nil {
		t.Errorf("deny list should avoid collision: %v", err)
	}
}

func TestValidateOperationIDUniqueness_CleanSpec(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "paths": {
	   "/a": {"get": {"operationId": "listUsers"}},
	   "/a/{id}": {"get": {"operationId": "getUser"}}
	 }
	}`)
	if err := validateOperationIDUniqueness(spec, nil, nil); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestValidateOperationIDUniqueness_InvalidAsBothJSONAndYAML(t *testing.T) {
	invalid := []byte("\x00\x01\x02:::\n\t- not yaml either\n")
	err := validateOperationIDUniqueness(invalid, nil, nil)
	if err == nil {
		t.Fatal("expected parse error for unparseable spec")
	}
	if !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("err should mention 'failed to parse', got: %v", err)
	}
}

func TestValidateOperationIDUniqueness_NoOperationIDs(t *testing.T) {
	spec := []byte(`{
	  "openapi": "3.0.0",
	  "info": {"title": "Test", "version": "1.0"},
	  "paths": {
	    "/test": {
	      "get": {
	        "summary": "Test without operationId",
	        "responses": {"200": {"description": "OK"}}
	      }
	    }
	  }
	}`)
	if err := validateOperationIDUniqueness(spec, nil, nil); err != nil {
		t.Errorf("missing operationId should pass uniqueness check, got: %v", err)
	}
}

func TestValidateBasicAuthConfig_Ok(t *testing.T) {
	auth := &AuthConfig{Type: AuthTypeBasic, Username: "alice", Password: "s3cret"}
	if err := validateBasicAuthConfig(auth); err != nil {
		t.Errorf("err: %v", err)
	}
}

func TestValidateBasicAuthConfig_SpecialCharsUsername(t *testing.T) {
	auth := &AuthConfig{
		Type:     AuthTypeBasic,
		Username: "user@example.com",
		Password: "password123",
	}
	if err := validateBasicAuthConfig(auth); err != nil {
		t.Errorf("special-char username should pass: %v", err)
	}
	auth.Username = "ué中文"
	if err := validateBasicAuthConfig(auth); err != nil {
		t.Errorf("unicode username should pass: %v", err)
	}
}

func TestValidateBasicAuthConfig_SpecialCharsPassword(t *testing.T) {
	auth := &AuthConfig{
		Type:     AuthTypeBasic,
		Username: "testuser",
		Password: "p@ssw0rd!#$%",
	}
	if err := validateBasicAuthConfig(auth); err != nil {
		t.Errorf("special-char password should pass: %v", err)
	}
}

func TestValidateBasicAuthConfig_MissingFields(t *testing.T) {
	err := validateBasicAuthConfig(&AuthConfig{Username: "", Password: "x"})
	if err == nil || !strings.Contains(err.Error(), "auth.username") {
		t.Errorf("got %v", err)
	}
	err = validateBasicAuthConfig(&AuthConfig{Username: "u", Password: ""})
	if err == nil || !strings.Contains(err.Error(), "auth.password") {
		t.Errorf("got %v", err)
	}
}

func TestValidateBasicAuthConfig_LengthBounds(t *testing.T) {
	long := strings.Repeat("a", httpBasicUsernameMaxLength+1)
	err := validateBasicAuthConfig(&AuthConfig{Username: long, Password: "x"})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("username too-long not caught: %v", err)
	}
	longPw := strings.Repeat("p", httpBasicPasswordMaxLength+1)
	err = validateBasicAuthConfig(&AuthConfig{Username: "u", Password: longPw})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("password too-long not caught: %v", err)
	}
}

func TestValidateBearerAuthConfig_Ok(t *testing.T) {
	auth := &AuthConfig{Type: AuthTypeBearer, Token: "abc.def.ghi"}
	if err := validateBearerAuthConfig(auth); err != nil {
		t.Errorf("err: %v", err)
	}
}

func TestValidateBearerAuthConfig_Empty(t *testing.T) {
	err := validateBearerAuthConfig(&AuthConfig{Token: ""})
	if err == nil {
		t.Error("expected err for empty token")
	}
	err = validateBearerAuthConfig(&AuthConfig{Token: "   \t\n   "})
	if err == nil {
		t.Error("expected err for whitespace-only token")
	}
}

func TestValidateBearerAuthConfig_TrimsBeforeLengthCheck(t *testing.T) {
	inner := strings.Repeat("x", httpBearerTokenMaxLength)
	err := validateBearerAuthConfig(&AuthConfig{Token: " " + inner + " "})
	if err != nil {
		t.Errorf("trim before length should pass: %v", err)
	}
	over := strings.Repeat("x", httpBearerTokenMaxLength+1)
	err = validateBearerAuthConfig(&AuthConfig{Token: over})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("over-length not caught: %v", err)
	}
}

func TestValidateAPIKeyAuthConfig_Ok(t *testing.T) {
	auth := &AuthConfig{Type: AuthTypeAPIKey, HeaderName: "X-API-Key", APIKey: "abc123"}
	if err := validateAPIKeyAuthConfig(auth); err != nil {
		t.Errorf("err: %v", err)
	}
}

func TestValidateAPIKeyAuthConfig_Missing(t *testing.T) {
	err := validateAPIKeyAuthConfig(&AuthConfig{APIKey: "x"})
	if err == nil || !strings.Contains(err.Error(), "auth.name") {
		t.Errorf("got %v", err)
	}
	err = validateAPIKeyAuthConfig(&AuthConfig{HeaderName: "X"})
	if err == nil || !strings.Contains(err.Error(), "auth.value") {
		t.Errorf("got %v", err)
	}
}

func TestValidateAPIKeyAuthConfig_InAccepted(t *testing.T) {
	for _, in := range []string{"", "header", "query"} {
		auth := &AuthConfig{Type: AuthTypeAPIKey, HeaderName: "X", APIKey: "v", In: in}
		if err := validateAPIKeyAuthConfig(auth); err != nil {
			t.Errorf("in=%q unexpectedly rejected: %v", in, err)
		}
	}
}

func TestValidateAPIKeyAuthConfig_CookieRejected(t *testing.T) {
	auth := &AuthConfig{Type: AuthTypeAPIKey, HeaderName: "X", APIKey: "v", In: "cookie"}
	err := validateAPIKeyAuthConfig(auth)
	if err == nil || !strings.Contains(err.Error(), "cookie") {
		t.Errorf("expected cookie rejection, got %v", err)
	}
}

func TestValidateAPIKeyAuthConfig_InvalidIn(t *testing.T) {
	auth := &AuthConfig{Type: AuthTypeAPIKey, HeaderName: "X", APIKey: "v", In: "body"}
	err := validateAPIKeyAuthConfig(auth)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected invalid-in error, got %v", err)
	}
}

func TestValidateOAuth2Config_MissingURLs(t *testing.T) {
	err := validateOAuth2Config(oauth2AuthConfig{})
	if err == nil || !strings.Contains(err.Error(), "authorization_url") {
		t.Errorf("got %v", err)
	}
	err = validateOAuth2Config(oauth2AuthConfig{AuthorizationURL: "https://a.b/auth"})
	if err == nil || !strings.Contains(err.Error(), "token_url") {
		t.Errorf("got %v", err)
	}
}

func TestValidateOAuth2Config_URLBounds(t *testing.T) {
	shortURL := "http://a"
	err := validateOAuth2Config(oauth2AuthConfig{AuthorizationURL: shortURL, TokenURL: "https://a.b/token"})
	if err == nil || !strings.Contains(err.Error(), "authorization_url length") {
		t.Errorf("short url not caught: %v", err)
	}
	longURL := "https://a.b/" + strings.Repeat("x", oauth2AuthorizationURLMaxLength)
	err = validateOAuth2Config(oauth2AuthConfig{AuthorizationURL: longURL, TokenURL: "https://a.b/token"})
	if err == nil || !strings.Contains(err.Error(), "authorization_url length") {
		t.Errorf("long url not caught: %v", err)
	}
}

func TestValidateOAuth2Config_AuthMethodAllowed(t *testing.T) {
	for _, m := range oauth2TokenEndpointAuthMethods {
		err := validateOAuth2Config(oauth2AuthConfig{
			AuthorizationURL:        "https://a.b/auth",
			TokenURL:                "https://a.b/token",
			TokenEndpointAuthMethod: m,
		})
		if err != nil {
			t.Errorf("method %q should be accepted: %v", m, err)
		}
	}
}

func TestValidateOAuth2Config_AuthMethodRejected(t *testing.T) {
	err := validateOAuth2Config(oauth2AuthConfig{
		AuthorizationURL:        "https://a.b/auth",
		TokenURL:                "https://a.b/token",
		TokenEndpointAuthMethod: "client_secret_jwt",
	})
	if err == nil || !strings.Contains(err.Error(), "token_endpoint_auth_method") {
		t.Errorf("got %v", err)
	}
}

func TestValidateOAuth2Config_ClientSecretBounds(t *testing.T) {
	err := validateOAuth2Config(oauth2AuthConfig{
		AuthorizationURL: "https://a.b/auth",
		TokenURL:         "https://a.b/token",
		ClientID:         "abc",
		ClientSecret:     "x",
	})
	if err == nil || !strings.Contains(err.Error(), "client_secret length") {
		t.Errorf("short secret not caught: %v", err)
	}
	long := strings.Repeat("s", oauth2ClientSecretMaxLength+1)
	err = validateOAuth2Config(oauth2AuthConfig{
		AuthorizationURL: "https://a.b/auth",
		TokenURL:         "https://a.b/token",
		ClientID:         "abc",
		ClientSecret:     long,
	})
	if err == nil || !strings.Contains(err.Error(), "client_secret length") {
		t.Errorf("long secret not caught: %v", err)
	}
}

func TestValidateAuthConfig_NoopForNilOrNone(t *testing.T) {
	if err := validateAuthConfig(nil); err != nil {
		t.Errorf("nil: %v", err)
	}
	if err := validateAuthConfig(&AuthConfig{Type: ""}); err != nil {
		t.Errorf("empty type: %v", err)
	}
	if err := validateAuthConfig(&AuthConfig{Type: AuthTypeNone}); err != nil {
		t.Errorf("none: %v", err)
	}
}

func TestValidateAuthConfig_DispatchesByType(t *testing.T) {
	err := validateAuthConfig(&AuthConfig{Type: AuthTypeBasic})
	if err == nil || !strings.Contains(err.Error(), "auth.username") {
		t.Errorf("basic dispatch: %v", err)
	}
	err = validateAuthConfig(&AuthConfig{Type: AuthTypeBearer})
	if err == nil || !strings.Contains(err.Error(), "auth.token") {
		t.Errorf("bearer dispatch: %v", err)
	}
	err = validateAuthConfig(&AuthConfig{Type: AuthTypeAPIKey})
	if err == nil {
		t.Errorf("api_key dispatch: nil err")
	}
}

func TestLoadAndDiscoverTools_RejectsSwagger2(t *testing.T) {
	spec := `{"swagger": "2.0", "info": {"title": "legacy", "version": "1"}, "paths": {}}`
	cfg := &ToolsetConfig{SpecInline: spec, SpecFormat: "json"}
	mgr := NewManager(slog.Default(), nil)
	_, err := mgr.LoadAndDiscoverTools(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Swagger 2.0") {
		t.Errorf("err should mention Swagger 2.0: %v", err)
	}
}

func TestLoadAndDiscoverTools_RejectsDuplicateOperationIDs(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "servers": [{"url": "http://example.com"}],
	 "paths": {
	   "/a": {"get": {"operationId": "list"}},
	   "/b": {"get": {"operationId": "list"}}
	 }
	}`
	cfg := &ToolsetConfig{SpecInline: spec, SpecFormat: "json"}
	mgr := NewManager(slog.Default(), nil)
	_, err := mgr.LoadAndDiscoverTools(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Duplicate") {
		t.Errorf("unexpected: %v", err)
	}
}

func TestLoadAndDiscoverTools_SnakeCaseAllowList(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "servers": [{"url": "http://example.com"}],
	 "paths": {
	   "/a": {"get": {"operationId": "listUsers"}},
	   "/b": {"get": {"operationId": "getOne"}}
	 }
	}`
	for _, entry := range []string{"listUsers", "list_users"} {
		t.Run(entry, func(t *testing.T) {
			cfg := &ToolsetConfig{
				SpecInline: spec,
				SpecFormat: "json",
				AllowList:  []string{entry},
			}
			mgr := NewManager(slog.Default(), nil)
			tools, err := mgr.LoadAndDiscoverTools(cfg)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if len(tools) != 1 {
				t.Fatalf("got %d tools, want 1", len(tools))
			}
		})
	}
}

func TestValidateAuthConfig_OAuth2AuthorizationCode_Happy(t *testing.T) {
	auth := &AuthConfig{
		Type:                    AuthTypeOAuth2AuthorizationCode,
		AuthorizationURL:        "https://idp.example.com/authorize",
		TokenURL:                "https://idp.example.com/token",
		ClientID:                "my-cid",
		ClientSecret:            "my-csecret",
		TokenEndpointAuthMethod: "client_secret_basic",
		Scopes:                  []string{"read"},
	}
	if err := validateAuthConfig(auth); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestValidateAuthConfig_OAuth2AuthorizationCode_RequiresAuthorizationURL(t *testing.T) {
	auth := &AuthConfig{
		Type:         AuthTypeOAuth2AuthorizationCode,
		TokenURL:     "https://idp.example.com/token",
		ClientID:     "cid",
		ClientSecret: "csecret",
	}
	err := validateAuthConfig(auth)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "authorization_url") {
		t.Errorf("missing 'authorization_url' hint: %v", err)
	}
}

func TestValidateAuthConfig_OAuth2AuthorizationCode_RequiresClientID(t *testing.T) {
	auth := &AuthConfig{
		Type:             AuthTypeOAuth2AuthorizationCode,
		AuthorizationURL: "https://idp.example.com/authorize",
		TokenURL:         "https://idp.example.com/token",
		ClientSecret:     "csecret",
	}
	err := validateAuthConfig(auth)
	if err == nil || !strings.Contains(err.Error(), "client_id") {
		t.Errorf("want client_id required, got %v", err)
	}
}

func TestValidateAuthConfig_OAuth2AuthorizationCode_NonePublicClient(t *testing.T) {
	auth := &AuthConfig{
		Type:                    AuthTypeOAuth2AuthorizationCode,
		AuthorizationURL:        "https://idp.example.com/authorize",
		TokenURL:                "https://idp.example.com/token",
		ClientID:                "public-cid",
		TokenEndpointAuthMethod: "none",
	}
	if err := validateAuthConfig(auth); err != nil {
		t.Errorf("method=none public client should be accepted: %v", err)
	}
}

func TestValidateAuthConfig_OAuth2ClientCredentials_Happy(t *testing.T) {
	auth := &AuthConfig{
		Type:         AuthTypeOAuth2ClientCredentials,
		TokenURL:     "https://idp.example.com/token",
		ClientID:     "svc-cid",
		ClientSecret: "svc-csecret",
		Scopes:       []string{"api.read"},
	}
	if err := validateAuthConfig(auth); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestValidateAuthConfig_OAuth2ClientCredentials_RequiresClientSecret(t *testing.T) {
	auth := &AuthConfig{
		Type:     AuthTypeOAuth2ClientCredentials,
		TokenURL: "https://idp.example.com/token",
		ClientID: "svc-cid",
	}
	err := validateAuthConfig(auth)
	if err == nil || !strings.Contains(err.Error(), "client_secret") {
		t.Errorf("want client_secret required, got %v", err)
	}
}

func TestValidateAuthConfig_OAuth2ClientCredentials_RejectsBadMethod(t *testing.T) {
	auth := &AuthConfig{
		Type:                    AuthTypeOAuth2ClientCredentials,
		TokenURL:                "https://idp.example.com/token",
		ClientID:                "cid",
		ClientSecret:            "csecret",
		TokenEndpointAuthMethod: "client_cert_mtls",
	}
	err := validateAuthConfig(auth)
	if err == nil || !strings.Contains(err.Error(), "token_endpoint_auth_method") {
		t.Errorf("want method enum enforcement, got %v", err)
	}
}

func TestValidateAuthConfig_ServiceAccount_Happy(t *testing.T) {
	auth := &AuthConfig{
		Type:               AuthTypeServiceAccount,
		ServiceAccountJSON: `{"type":"service_account","client_email":"x@y.iam.gserviceaccount.com"}`,
		Scopes:             []string{"https://www.googleapis.com/auth/cloud-platform"},
	}
	if err := validateAuthConfig(auth); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestValidateAuthConfig_ServiceAccount_RequiresJSON(t *testing.T) {
	auth := &AuthConfig{
		Type:   AuthTypeServiceAccount,
		Scopes: []string{"x"},
	}
	err := validateAuthConfig(auth)
	if err == nil || !strings.Contains(err.Error(), "service_account_json") {
		t.Errorf("want service_account_json required, got %v", err)
	}
}

func TestValidateAuthConfig_ServiceAccount_RequiresScopes(t *testing.T) {
	auth := &AuthConfig{
		Type:               AuthTypeServiceAccount,
		ServiceAccountJSON: `{"type":"service_account"}`,
	}
	err := validateAuthConfig(auth)
	if err == nil || !strings.Contains(err.Error(), "scopes") {
		t.Errorf("want scopes required, got %v", err)
	}
}

func TestValidateAuthConfig_ServiceAccount_RejectsBadJSON(t *testing.T) {
	auth := &AuthConfig{
		Type:               AuthTypeServiceAccount,
		ServiceAccountJSON: `{not json`,
		Scopes:             []string{"x"},
	}
	err := validateAuthConfig(auth)
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("want bad JSON rejection, got %v", err)
	}
}
