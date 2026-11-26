// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func mustParseSchemes(t *testing.T, spec string) []SecuritySchemeInfo {
	t.Helper()
	schemes, err := ParseSecuritySchemes([]byte(spec))
	if err != nil {
		t.Fatalf("ParseSecuritySchemes: %v", err)
	}
	return schemes
}

func TestParseSecuritySchemes_Bearer(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {
	   "bearerAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}
	 }}
	}`
	schemes := mustParseSchemes(t, spec)
	if len(schemes) != 1 {
		t.Fatalf("got %d schemes", len(schemes))
	}
	s := schemes[0]
	if s.AuthType != "http" || s.SchemeName != "bearerAuth" {
		t.Errorf("unexpected scheme: %+v", s)
	}
	details := s.Details.(map[string]string)
	if details["scheme"] != "bearer" || details["bearer_format"] != "JWT" {
		t.Errorf("details = %+v", details)
	}
}

func TestParseSecuritySchemes_Basic(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"basicAuth": {"type": "http", "scheme": "basic"}}}
	}`
	schemes := mustParseSchemes(t, spec)
	if len(schemes) != 1 || schemes[0].AuthType != "http" {
		t.Fatalf("unexpected: %+v", schemes)
	}
	details := schemes[0].Details.(map[string]string)
	if details["scheme"] != "basic" {
		t.Errorf("scheme = %q", details["scheme"])
	}
}

func TestParseSecuritySchemes_APIKeyHeader(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {
	   "apiKey": {"type": "apiKey", "in": "header", "name": "X-API-Key"}
	 }}
	}`
	schemes := mustParseSchemes(t, spec)
	if len(schemes) != 1 || schemes[0].AuthType != "apikey" {
		t.Fatalf("unexpected: %+v", schemes)
	}
	details := schemes[0].Details.(map[string]string)
	if details["in"] != "header" || details["name"] != "X-API-Key" {
		t.Errorf("details = %+v", details)
	}
}

func TestParseSecuritySchemes_APIKeyQuery(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"key": {"type": "apiKey", "in": "query", "name": "api_key"}}}
	}`
	schemes := mustParseSchemes(t, spec)
	details := schemes[0].Details.(map[string]string)
	if details["in"] != "query" || details["name"] != "api_key" {
		t.Errorf("details = %+v", details)
	}
}

func TestParseSecuritySchemes_APIKeyCookie(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"ck": {"type": "apiKey", "in": "cookie", "name": "session"}}}
	}`
	schemes := mustParseSchemes(t, spec)
	details := schemes[0].Details.(map[string]string)
	if details["in"] != "cookie" {
		t.Errorf("details = %+v", details)
	}
}

func TestParseSecuritySchemes_OAuth2AuthCode(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"oauth2": {
	   "type": "oauth2",
	   "flows": {
	     "authorizationCode": {
	       "authorizationUrl": "https://example.com/auth",
	       "tokenUrl": "https://example.com/token",
	       "scopes": {"read": "Read access"}
	     }
	   }
	 }}}
	}`
	schemes := mustParseSchemes(t, spec)
	if len(schemes) != 1 || schemes[0].AuthType != "oauth" {
		t.Fatalf("unexpected: %+v", schemes)
	}
	details := schemes[0].Details.(map[string]any)
	flows := details["flows"].(map[string]any)
	flow := flows["authorizationCode"].(map[string]any)
	if flow["authorization_url"] != "https://example.com/auth" {
		t.Errorf("auth url = %+v", flow["authorization_url"])
	}
	if flow["token_url"] != "https://example.com/token" {
		t.Errorf("token url = %+v", flow["token_url"])
	}
}

func TestParseSecuritySchemes_OAuth2ClientCredentials(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"m2m": {
	   "type": "oauth2",
	   "flows": {"clientCredentials": {"tokenUrl": "https://example.com/token"}}
	 }}}
	}`
	schemes := mustParseSchemes(t, spec)
	details := schemes[0].Details.(map[string]any)
	flows := details["flows"].(map[string]any)
	if _, ok := flows["clientCredentials"]; !ok {
		t.Fatalf("missing clientCredentials flow: %+v", flows)
	}
}

func TestParseSecuritySchemes_OpenIDConnect(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"oidc": {
	   "type": "openIdConnect",
	   "openIdConnectUrl": "https://example.com/.well-known/openid-configuration"
	 }}}
	}`
	schemes := mustParseSchemes(t, spec)
	if len(schemes) != 1 || schemes[0].AuthType != "oauth" {
		t.Fatalf("unexpected: %+v", schemes)
	}
	details := schemes[0].Details.(map[string]any)
	if details["openid_connect_url"] != "https://example.com/.well-known/openid-configuration" {
		t.Errorf("openid_connect_url = %+v", details["openid_connect_url"])
	}
}

func TestParseSecuritySchemes_MutualTLS(t *testing.T) {
	spec := `{
	 "openapi": "3.1.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"mtls": {"type": "mutualTLS", "description": "Client cert"}}}
	}`
	schemes := mustParseSchemes(t, spec)
	if len(schemes) != 1 || schemes[0].AuthType != "mutualTLS" {
		t.Fatalf("unexpected: %+v", schemes)
	}
}

func TestParseSecuritySchemes_UnknownSchemeTypeSkipped(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {
	   "good": {"type": "http", "scheme": "bearer"},
	   "bad":  {"type": "quantum_something"}
	 }}
	}`
	schemes := mustParseSchemes(t, spec)
	if len(schemes) != 1 || schemes[0].SchemeName != "good" {
		t.Errorf("unexpected schemes: %+v", schemes)
	}
}

func TestParseSecuritySchemes_EmptyComponents(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {}
	}`
	schemes := mustParseSchemes(t, spec)
	if len(schemes) != 0 {
		t.Errorf("got %d schemes, want 0", len(schemes))
	}
}

func TestParseSecuritySchemes_Swagger2Rejected(t *testing.T) {
	spec := `{"swagger": "2.0", "info": {"title": "old"}}`
	_, err := ParseSecuritySchemes([]byte(spec))
	if err == nil || !strings.Contains(err.Error(), "Swagger") {
		t.Fatalf("expected Swagger rejection, got %v", err)
	}
}

func TestDetectPrimaryAuth_PicksAuthorizationCodeOverBearer(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {
	   "bearerAuth": {"type": "http", "scheme": "bearer"},
	   "oauth": {
	     "type": "oauth2",
	     "flows": {"authorizationCode": {
	       "authorizationUrl": "https://ex.com/auth",
	       "tokenUrl": "https://ex.com/token",
	       "scopes": {}
	     }}
	   }
	 }}
	}`
	schemes := mustParseSchemes(t, spec)
	primary := DetectPrimaryAuth(schemes)
	if primary == nil || primary.AuthType != "oauth" {
		t.Fatalf("expected oauth primary, got %+v", primary)
	}
	config := primary.Config.(map[string]any)
	if config["schemeName"] != "oauth" {
		t.Errorf("schemeName = %v", config["schemeName"])
	}
	if config["authorizationUrl"] != "https://ex.com/auth" {
		t.Errorf("authorizationUrl = %v", config["authorizationUrl"])
	}
}

func TestDetectPrimaryAuth_ClientCredentialsOverBearer(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {
	   "bearerAuth": {"type": "http", "scheme": "bearer"},
	   "m2m": {
	     "type": "oauth2",
	     "flows": {"clientCredentials": {"tokenUrl": "https://ex.com/token"}}
	   }
	 }}
	}`
	schemes := mustParseSchemes(t, spec)
	primary := DetectPrimaryAuth(schemes)
	if primary == nil || primary.AuthType != "oauth" {
		t.Fatalf("expected oauth, got %+v", primary)
	}
	config := primary.Config.(map[string]any)
	if config["grantType"] != "clientCredentials" {
		t.Errorf("grantType = %v", config["grantType"])
	}
}

func TestDetectPrimaryAuth_BearerOverBasic(t *testing.T) {
	schemes := []SecuritySchemeInfo{
		{SchemeName: "basicAuth", AuthType: "http", Details: map[string]string{"scheme": "basic"}},
		{SchemeName: "bearerAuth", AuthType: "http", Details: map[string]string{"scheme": "bearer"}},
	}
	primary := DetectPrimaryAuth(schemes)
	if primary == nil || primary.AuthType != "http" {
		t.Fatalf("got %+v", primary)
	}
	config := primary.Config.(map[string]any)
	if config["httpScheme"] != "bearer" {
		t.Errorf("httpScheme = %v", config["httpScheme"])
	}
}

func TestDetectPrimaryAuth_BasicOverAPIKey(t *testing.T) {
	schemes := []SecuritySchemeInfo{
		{SchemeName: "k", AuthType: "apikey", Details: map[string]string{"name": "X", "in": "header"}},
		{SchemeName: "b", AuthType: "http", Details: map[string]string{"scheme": "basic"}},
	}
	primary := DetectPrimaryAuth(schemes)
	if primary == nil || primary.AuthType != "http" {
		t.Fatalf("got %+v", primary)
	}
}

func TestDetectPrimaryAuth_APIKeyHeaderOverQuery(t *testing.T) {
	schemes := []SecuritySchemeInfo{
		{SchemeName: "q", AuthType: "apikey", Details: map[string]string{"name": "X", "in": "query"}},
		{SchemeName: "h", AuthType: "apikey", Details: map[string]string{"name": "Y", "in": "header"}},
	}
	primary := DetectPrimaryAuth(schemes)
	if primary == nil {
		t.Fatal("expected primary")
	}
	config := primary.Config.(map[string]any)
	if config["schemeName"] != "h" {
		t.Errorf("schemeName = %v, want h", config["schemeName"])
	}
}

func TestDetectPrimaryAuth_Empty(t *testing.T) {
	if got := DetectPrimaryAuth(nil); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestDetectPrimaryAuth_OIDCFallsThroughToConcrete(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {
	   "bearerAuth": {"type": "http", "scheme": "bearer"},
	   "oidc": {
	     "type": "openIdConnect",
	     "openIdConnectUrl": "https://example.com/.well-known/openid-configuration"
	   }
	 }}
	}`
	schemes := mustParseSchemes(t, spec)
	primary := DetectPrimaryAuth(schemes)
	if primary == nil || primary.AuthType != "http" {
		t.Fatalf("expected http bearer to win over OIDC-with-empty-URLs, got %+v", primary)
	}
}

func TestDetectPrimaryAuth_OIDCSoleScheme_FirstSchemeFallback(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {
	   "oidc": {
	     "type": "openIdConnect",
	     "openIdConnectUrl": "https://example.com/.well-known/openid-configuration"
	   }
	 }}
	}`
	schemes := mustParseSchemes(t, spec)
	primary := DetectPrimaryAuth(schemes)
	if primary == nil || primary.AuthType != "oauth" {
		t.Fatalf("expected first-scheme fallback for OIDC-only spec, got %+v", primary)
	}
}

func TestDetectAuthFromSpec_EmitsBearer(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"b": {"type": "http", "scheme": "bearer"}}}
	}`)
	ac, err := DetectAuthFromSpec(spec)
	if err != nil {
		t.Fatalf("DetectAuthFromSpec: %v", err)
	}
	if ac == nil || ac.Type != "bearer" {
		t.Errorf("got %+v, want type=bearer", ac)
	}
}

func TestDetectAuthFromSpec_EmitsBasic(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"b": {"type": "http", "scheme": "basic"}}}
	}`)
	ac, err := DetectAuthFromSpec(spec)
	if err != nil || ac == nil || ac.Type != "basic" {
		t.Errorf("got %+v err %v", ac, err)
	}
}

func TestDetectAuthFromSpec_EmitsAPIKey(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"k": {"type": "apiKey", "in": "header", "name": "X-Tok"}}}
	}`)
	ac, err := DetectAuthFromSpec(spec)
	if err != nil || ac == nil || ac.Type != "api_key" {
		t.Errorf("got %+v err %v", ac, err)
	}
	if ac.HeaderName != "X-Tok" {
		t.Errorf("header = %q", ac.HeaderName)
	}
}

func TestDetectAuthFromSpec_EmitsOAuth2(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"o": {
	   "type": "oauth2",
	   "flows": {"authorizationCode": {
	     "authorizationUrl": "https://ex.com/auth",
	     "tokenUrl": "https://ex.com/token",
	     "scopes": {}
	   }}
	 }}}
	}`)
	ac, err := DetectAuthFromSpec(spec)
	if err != nil || ac == nil || ac.Type != "oauth2_authorization_code" {
		t.Errorf("got %+v err %v", ac, err)
	}
	if ac != nil {
		if ac.AuthorizationURL != "https://ex.com/auth" {
			t.Errorf("AuthorizationURL = %q, want https://ex.com/auth", ac.AuthorizationURL)
		}
		if ac.TokenURL != "https://ex.com/token" {
			t.Errorf("TokenURL = %q, want https://ex.com/token", ac.TokenURL)
		}
	}
}

func TestDetectAuthFromSpec_EmitsOAuth2ClientCreds(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"m": {
	   "type": "oauth2",
	   "flows": {"clientCredentials": {"tokenUrl": "https://ex.com/token"}}
	 }}}
	}`)
	ac, err := DetectAuthFromSpec(spec)
	if err != nil || ac == nil || ac.Type != "oauth2_client_credentials" {
		t.Errorf("got %+v err %v", ac, err)
	}
	if ac != nil && ac.TokenURL != "https://ex.com/token" {
		t.Errorf("TokenURL = %q, want https://ex.com/token", ac.TokenURL)
	}
}

func TestDetectAuthFromSpec_NoSchemesReturnsNil(t *testing.T) {
	spec := []byte(`{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {}
	}`)
	ac, err := DetectAuthFromSpec(spec)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if ac != nil {
		t.Errorf("expected nil, got %+v", ac)
	}
}

func TestAuthDetector_DetectAuth_FromURL(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "paths": {},
	 "components": {"securitySchemes": {"b": {"type": "http", "scheme": "bearer"}}}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(spec))
	}))
	defer srv.Close()

	d := NewAuthDetector(nil, srv.Client())
	resp, err := d.DetectAuth(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("DetectAuth: %v", err)
	}
	if len(resp.Schemes) != 1 || resp.SuggestedAuth == nil {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestAuthDetector_RejectsMissingSource(t *testing.T) {
	d := NewAuthDetector(nil, http.DefaultClient)
	if _, err := d.DetectAuth(context.Background(), "", nil); err == nil {
		t.Error("expected error for missing source")
	}
	if _, err := d.DetectAuth(context.Background(), "http://x", []byte("y")); err == nil {
		t.Error("expected error for both source")
	}
}

func TestAuthDetector_FetchFailures(t *testing.T) {
	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
	}
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantStatus int
	}{
		{name: "not found", handler: status(http.StatusNotFound), wantStatus: http.StatusNotFound},
		{name: "server error", handler: status(http.StatusInternalServerError), wantStatus: http.StatusInternalServerError},
		{name: "redirect without location", handler: status(http.StatusFound), wantStatus: http.StatusFound},
		{name: "no content", handler: status(http.StatusNoContent), wantStatus: http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			_, err := NewAuthDetector(nil, srv.Client()).DetectAuth(context.Background(), srv.URL, nil)
			var fetchErr *SpecFetchError
			if !errors.As(err, &fetchErr) {
				t.Fatalf("err = %T %v, want *SpecFetchError", err, err)
			}
			if fetchErr.StatusCode != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d", fetchErr.StatusCode, tt.wantStatus)
			}
		})
	}

	t.Run("transport failure carries the cause and no status", func(t *testing.T) {
		srv := httptest.NewServer(status(http.StatusOK))
		url := srv.URL
		srv.Close()

		_, err := NewAuthDetector(nil, http.DefaultClient).DetectAuth(context.Background(), url+"/spec.json?token=secret", nil)
		var fetchErr *SpecFetchError
		if !errors.As(err, &fetchErr) {
			t.Fatalf("err = %T %v, want *SpecFetchError", err, err)
		}
		if fetchErr.StatusCode != 0 {
			t.Errorf("StatusCode = %d, want 0", fetchErr.StatusCode)
		}
		var opErr *net.OpError
		if !errors.As(err, &opErr) {
			t.Errorf("transport cause not reachable through Unwrap: %v", err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("error text echoes the URL query: %q", err.Error())
		}
	})
}

func TestSpecFetchError_ZeroValueDoesNotPanic(t *testing.T) {
	tests := []struct {
		name string
		err  *SpecFetchError
		want string
	}{
		{"zero value", &SpecFetchError{}, "fetch OpenAPI spec failed"},
		{"status", &SpecFetchError{StatusCode: 404}, "fetch OpenAPI spec: upstream returned status 404"},
		{"cause", &SpecFetchError{Err: errors.New("boom")}, "fetch OpenAPI spec: boom"},
		{"cause drops the request url", &SpecFetchError{Err: &url.Error{Op: "Get", URL: "http://h/?token=secret", Err: errors.New("boom")}}, "fetch OpenAPI spec: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthDetector_NilClientRefusesURLFetch(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	_, err := NewAuthDetector(nil, nil).DetectAuth(context.Background(), srv.URL, nil)
	var detectErr *DetectAuthError
	if !errors.As(err, &detectErr) {
		t.Fatalf("err = %T %v, want *DetectAuthError", err, err)
	}
	if hits.Load() != 0 {
		t.Errorf("server was contacted %d times", hits.Load())
	}
}

func TestAuthDetector_UploadedContentNeedsNoClient(t *testing.T) {
	spec := []byte(`{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{},"components":{"securitySchemes":{"b":{"type":"http","scheme":"bearer"}}}}`)
	resp, err := NewAuthDetector(nil, nil).DetectAuth(context.Background(), "", spec)
	if err != nil {
		t.Fatalf("DetectAuth: %v", err)
	}
	if len(resp.Schemes) != 1 {
		t.Errorf("schemes = %+v", resp.Schemes)
	}
}

func TestAuthDetector_SpecBodyIsSizeCapped(t *testing.T) {
	const offeredMiB = 64
	var sentMiB atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := bytes.Repeat([]byte(" "), 1<<20)
		for range offeredMiB {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			sentMiB.Add(1)
		}
	}))
	defer srv.Close()

	_, err := NewAuthDetector(nil, srv.Client()).DetectAuth(context.Background(), srv.URL, nil)
	var detectErr *DetectAuthError
	if !errors.As(err, &detectErr) {
		t.Fatalf("err = %T %v, want parse failure *DetectAuthError", err, err)
	}
	if got := sentMiB.Load(); got >= offeredMiB {
		t.Errorf("server delivered all %d MiB; reader did not stop at the cap", got)
	}
}

func TestLoadAndDiscoverTools_AutoWiresBearerFromSpec(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "servers": [{"url": "http://example.com"}],
	 "paths": {"/ping": {"get": {"operationId": "ping"}}},
	 "components": {"securitySchemes": {"b": {"type": "http", "scheme": "bearer"}}}
	}`
	cfg := &ToolsetConfig{SpecInline: spec, SpecFormat: "json"}
	mgr := NewManager(slog.Default(), nil)
	if _, err := mgr.LoadAndDiscoverTools(cfg); err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	if cfg.Auth == nil {
		t.Fatal("expected cfg.Auth to be auto-derived")
	}
	if cfg.Auth.Type != "bearer" {
		t.Errorf("got type %q, want bearer", cfg.Auth.Type)
	}
}

func TestLoadAndDiscoverTools_ExplicitAuthWins(t *testing.T) {
	spec := `{
	 "openapi": "3.0.0",
	 "info": {"title": "t", "version": "1"},
	 "servers": [{"url": "http://example.com"}],
	 "paths": {"/ping": {"get": {"operationId": "ping"}}},
	 "components": {"securitySchemes": {"b": {"type": "http", "scheme": "bearer"}}}
	}`
	cfg := &ToolsetConfig{
		SpecInline: spec,
		SpecFormat: "json",
		Auth:       &AuthConfig{Type: AuthTypeAPIKey, HeaderName: "X-K", APIKey: "secret-value"},
	}
	mgr := NewManager(slog.Default(), nil)
	if _, err := mgr.LoadAndDiscoverTools(cfg); err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	if cfg.Auth.Type != "api_key" || cfg.Auth.APIKey != "secret-value" {
		t.Errorf("explicit config clobbered: %+v", cfg.Auth)
	}
}
