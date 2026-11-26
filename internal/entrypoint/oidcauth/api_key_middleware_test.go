// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

const validKeyPlaintext = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

type recordingKeyAuthority struct {
	resolution apikey.Resolution
	err        error
	calls      int
	gotHash    string
}

func (r *recordingKeyAuthority) Validate(_ context.Context, keyHash string) (apikey.Resolution, error) {
	r.calls++
	r.gotHash = keyHash
	return r.resolution, r.err
}

func keyProvider(t *testing.T, authority apikey.Authority) (*Provider, *recordingAuditLogger) {
	t.Helper()
	p, rec := newAuditingProvider(t, nil)
	p.config.SessionSecretKey = "test-session-secret"
	if authority != nil {
		p.SetKeyAuthority(authority)
	}
	return p, rec
}

func resolvedAuthority() *recordingKeyAuthority {
	return &recordingKeyAuthority{resolution: apikey.Resolution{
		KeyID:    "key-1",
		OwnerID:  "11111111-1111-1111-1111-111111111111",
		OwnerSub: "system:ci-deployer",
		Scopes:   []string{"agent_builder:_:create"},
	}}
}

func TestMiddleware_ValidAPIKeyAuthenticatesAsItsOwner(t *testing.T) {
	p, rec := keyProvider(t, resolvedAuthority())

	var got *auth.Subject
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if got == nil {
		t.Fatal("no identity reached the handler")
	}
	if got.AuthMethod() != auth.APIKeyAuthMethod {
		t.Errorf("AuthMethod = %q, want %q — this is the value the platform's key-management gate keys on",
			got.AuthMethod(), auth.APIKeyAuthMethod)
	}
	if got.UserID() != "system:ci-deployer" {
		t.Errorf("UserID = %q, want the owner's subject", got.UserID())
	}
	if !slices.Equal(got.Scopes(), []string{"agent_builder:_:create"}) {
		t.Errorf("Scopes = %v, want the surviving set", got.Scopes())
	}
	assertNoAuthEvents(t, rec.snapshot())
}

func TestMiddleware_APIKeyNeverFallsThroughToTokenValidation(t *testing.T) {
	authority := &recordingKeyAuthority{err: apikey.ErrInvalidKey}
	fake := &fakeOIDCClient{userInfo: &UserInfo{Sub: "attacker", Email: "a@b.c"}}
	p, rec := newAuditingProvider(t, fake)
	p.SetKeyAuthority(authority)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler ran for a rejected API key")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
	if authority.calls != 1 {
		t.Errorf("authority calls = %d, want 1", authority.calls)
	}
	if fake.calls != 0 {
		t.Errorf("the IdP userinfo endpoint was called %d times for a samk_ credential; a failed key must not be retried as an IdP token",
			fake.calls)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, auth.APIKeyReasonInvalid)
}

func TestMiddleware_APIKeyRejectionEmitsExactlyOneAuditEvent(t *testing.T) {
	for name, tc := range map[string]struct {
		authority  apikey.Authority
		wantStatus int
		wantReason string
	}{
		"invalid": {
			&recordingKeyAuthority{err: apikey.ErrInvalidKey},
			http.StatusUnauthorized, auth.APIKeyReasonInvalid,
		},
		"authorization mode disabled": {
			&recordingKeyAuthority{err: apikey.ErrAuthorizationModeDisabled},
			http.StatusUnauthorized, auth.APIKeyReasonModeDisabled,
		},
		"authority unavailable": {
			&recordingKeyAuthority{err: apikey.ErrAuthorityUnavailable},
			http.StatusServiceUnavailable, auth.APIKeyReasonUnavailable,
		},
		"keys not enabled here": {
			nil,
			http.StatusUnauthorized, auth.APIKeyReasonNotEnabled,
		},
		"holder never resolved": {
			apikey.NewLazyAuthority(),
			http.StatusServiceUnavailable, auth.APIKeyReasonUnavailable,
		},
		"holder resolved as disabled": {
			func() apikey.Authority {
				lazy := apikey.NewLazyAuthority()
				lazy.Disable()
				return lazy
			}(),
			http.StatusUnauthorized, auth.APIKeyReasonNotEnabled,
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, rec := keyProvider(t, tc.authority)
			handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("handler ran for a rejected API key")
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)

			if resp.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body=%s", resp.Code, tc.wantStatus, resp.Body.String())
			}
			assertSingleAuthEvent(t, rec.snapshot(), false, tc.wantReason)
		})
	}
}

func TestMiddleware_APIKeyRefusedOutsideTheAuthorizationHeader(t *testing.T) {
	t.Run("signed cookie", func(t *testing.T) {
		authority := resolvedAuthority()
		p, rec := keyProvider(t, authority)

		handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("handler ran for a key presented in the session cookie")
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
		req.AddCookie(&http.Cookie{
			Name:  accessTokenCookieName,
			Value: signValue(validKeyPlaintext, p.config.SessionSecretKey),
		})
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.Code)
		}
		if authority.calls != 0 {
			t.Errorf("authority was consulted %d times for a cookie-borne key; the position must be refused before validation", authority.calls)
		}
		assertSingleAuthEvent(t, rec.snapshot(), false, auth.APIKeyReasonWrongPosition)
	})

	t.Run("token query parameter", func(t *testing.T) {
		authority := resolvedAuthority()
		p, rec := keyProvider(t, authority)

		handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("handler ran for a key presented in ?token=")
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?token="+validKeyPlaintext, nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.Code)
		}
		if authority.calls != 0 {
			t.Errorf("authority was consulted %d times for a query-parameter key", authority.calls)
		}
		assertSingleAuthEvent(t, rec.snapshot(), false, auth.APIKeyReasonWrongPosition)
	})
}

func TestMiddleware_EveryAPIKey401IsIndistinguishable(t *testing.T) {
	shapes := make([]map[string]any, 0, 3)
	errorIDs := make(map[string]struct{})
	for _, authority := range []apikey.Authority{
		&recordingKeyAuthority{err: apikey.ErrInvalidKey},
		&recordingKeyAuthority{err: apikey.ErrAuthorizationModeDisabled},
		nil,
	} {
		p, _ := keyProvider(t, authority)
		handler := p.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("401 body is not JSON: %v (%s)", err, resp.Body.String())
		}
		id, ok := body["errorId"].(string)
		if !ok || id == "" {
			t.Fatalf("401 body carries no errorId, so the reason is unreachable from the response: %s", resp.Body.String())
		}
		errorIDs[id] = struct{}{}
		delete(body, "errorId")
		shapes = append(shapes, body)
	}

	if len(errorIDs) != len(shapes) {
		t.Errorf("errorId repeated across responses (%d unique for %d responses); it must correlate one request to one log line",
			len(errorIDs), len(shapes))
	}
	for i, shape := range shapes {
		if !maps.Equal(toStringMap(t, shape), toStringMap(t, shapes[0])) {
			t.Errorf("401 body %d = %v, want identical to %v apart from errorId", i, shape, shapes[0])
		}
	}
}

func toStringMap(t *testing.T, in map[string]any) map[string]string {
	t.Helper()
	out := make(map[string]string, len(in))
	for k, v := range in {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("error body field %q is %T, not a string; extend this comparison", k, v)
		}
		out[k] = s
	}
	return out
}

func TestMiddleware_APIKeyResponseEchoesNeitherPlaintextNorHash(t *testing.T) {
	p, _ := keyProvider(t, &recordingKeyAuthority{err: apikey.ErrInvalidKey})
	handler := p.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	body := resp.Body.String()
	for label, needle := range map[string]string{
		"plaintext": validKeyPlaintext,
		"hash":      apikey.Hash(validKeyPlaintext),
		"marker":    apikey.Marker,
	} {
		if strings.Contains(body, needle) {
			t.Errorf("response body echoes the credential %s: %s", label, body)
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("401 body is not JSON: %v", err)
	}
}

func TestMiddleware_APIKeyOnSoftAuthPathContinuesAnonymously(t *testing.T) {
	p, rec := keyProvider(t, &recordingKeyAuthority{err: apikey.ErrInvalidKey})

	var sawIdentity bool
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawIdentity = auth.SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/share/abcdefghijklmnopqrstu", nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (soft auth continues anonymously)", resp.Code)
	}
	if sawIdentity {
		t.Error("a rejected key left an identity on the context")
	}
	assertNoAuthEvents(t, rec.snapshot())
}

func TestMiddleware_ValidAPIKeyOnSoftAuthPathAuthenticates(t *testing.T) {
	p, _ := keyProvider(t, resolvedAuthority())

	var got *auth.Subject
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/share/abcdefghijklmnopqrstu", nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	if got == nil || got.AuthMethod() != auth.APIKeyAuthMethod {
		t.Errorf("identity = %+v, want a key identity", got)
	}
}

func TestMiddleware_NonKeyCredentialSkipsTheKeyAuthority(t *testing.T) {
	authority := resolvedAuthority()
	p, _ := keyProvider(t, authority)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer not-a-sam-key")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if authority.calls != 0 {
		t.Errorf("key authority consulted %d times for a non-key credential", authority.calls)
	}
}

func TestMiddleware_HandsTheAuthorityOnlyTheHash(t *testing.T) {
	authority := resolvedAuthority()
	p, _ := keyProvider(t, authority)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if authority.gotHash != apikey.Hash(validKeyPlaintext) {
		t.Errorf("authority got %q, want the digest of the complete plaintext", authority.gotHash)
	}
}

func TestSetKeyAuthority_IgnoresNil(t *testing.T) {
	p, _ := keyProvider(t, resolvedAuthority())
	p.SetKeyAuthority(nil)

	if p.keyAuthorityOrNil() == nil {
		t.Fatal("a nil argument cleared the installed authority")
	}
}

func TestSetKeyAuthority_UnsetMeansKeysAreNotServed(t *testing.T) {
	p, _ := keyProvider(t, nil)
	if p.keyAuthorityOrNil() != nil {
		t.Fatal("an unwired provider reports an authority")
	}
}

func TestSetKeyAuthority_LateBindingThroughTheHolder(t *testing.T) {
	lazy := apikey.NewLazyAuthority()
	p, _ := keyProvider(t, lazy)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
		return req
	}

	before := httptest.NewRecorder()
	handler.ServeHTTP(before, newReq())
	if before.Code != http.StatusServiceUnavailable {
		t.Errorf("before wiring: status = %d, want 503", before.Code)
	}

	lazy.SetAuthority(resolvedAuthority())

	after := httptest.NewRecorder()
	handler.ServeHTTP(after, newReq())
	if after.Code != http.StatusOK {
		t.Errorf("after wiring: status = %d, want 200; body=%s", after.Code, after.Body.String())
	}
}

func TestExtractToken_ReportsPosition(t *testing.T) {
	p, _ := keyProvider(t, nil)

	t.Run("header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer abc")
		token, position, err := p.extractToken(req)
		if err != nil || token != "abc" || position != credentialPositionHeader {
			t.Fatalf("got (%q, %v, %v)", token, position, err)
		}
	})
	t.Run("cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.AddCookie(&http.Cookie{
			Name:  accessTokenCookieName,
			Value: signValue("abc", p.config.SessionSecretKey),
		})
		token, position, err := p.extractToken(req)
		if err != nil || token != "abc" || position != credentialPositionCookie {
			t.Fatalf("got (%q, %v, %v)", token, position, err)
		}
	})
	t.Run("query", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/x?token=abc", nil)
		token, position, err := p.extractToken(req)
		if err != nil || token != "abc" || position != credentialPositionQuery {
			t.Fatalf("got (%q, %v, %v)", token, position, err)
		}
	})
	t.Run("absent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		token, position, err := p.extractToken(req)
		if err != nil || token != "" || position != credentialPositionNone {
			t.Fatalf("got (%q, %v, %v)", token, position, err)
		}
	})
	t.Run("malformed header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Basic abc")
		_, position, err := p.extractToken(req)
		if err == nil {
			t.Fatal("want an error for a header without the Bearer prefix")
		}
		if position != credentialPositionNone {
			t.Errorf("position = %v, want none on a malformed header", position)
		}
	})
}

func TestRegisterFactoryForGateway_InstallsTheKeyAuthority(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {Issuer: "https://azure.example.com/", ClientID: "id", ClientSecret: "secret"},
	}
	cfg := map[string]any{
		"external_auth_provider": "azure",
		"session_secret_key":     "test-session-key",
	}

	t.Run("wired", func(t *testing.T) {
		lazy := apikey.NewLazyAuthority()
		scope := "gw-keyauth-wired"
		RegisterFactoryForGateway(scope, scope, catalog, FactoryDeps{
			Signer:       newTestTrustSigner(t, scope),
			AuditLogger:  coreauth.NoopAuditLogger{},
			KeyAuthority: lazy,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		t.Cleanup(func() { UnregisterFactoryForGateway(scope) })

		built, err := auth.CreateProvider(scope, cfg)
		if err != nil {
			t.Fatalf("CreateProvider: %v", err)
		}
		provider, ok := built.(*Provider)
		if !ok {
			t.Fatalf("provider type = %T, want *Provider", built)
		}
		if provider.keyAuthorityOrNil() == nil {
			t.Error("FactoryDeps.KeyAuthority did not reach the built Provider")
		}
	})

	t.Run("omitted", func(t *testing.T) {
		scope := "gw-keyauth-omitted"
		RegisterFactoryForGateway(scope, scope, catalog, FactoryDeps{
			Signer:      newTestTrustSigner(t, scope),
			AuditLogger: coreauth.NoopAuditLogger{},
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		t.Cleanup(func() { UnregisterFactoryForGateway(scope) })

		built, err := auth.CreateProvider(scope, cfg)
		if err != nil {
			t.Fatalf("CreateProvider: %v", err)
		}
		if got := built.(*Provider).keyAuthorityOrNil(); got != nil {
			t.Errorf("keyAuthority = %v on a provider wired without one", got)
		}
	})
}

func TestMiddleware_BlanksAroundTheKeyDoNotHideIt(t *testing.T) {
	for name, header := range map[string]string{
		"two spaces":      "Bearer  " + validKeyPlaintext,
		"a tab":           "Bearer \t" + validKeyPlaintext,
		"trailing blanks": "Bearer " + validKeyPlaintext + " \t",
	} {
		t.Run(name, func(t *testing.T) {
			authority := resolvedAuthority()
			fake := &fakeOIDCClient{userInfo: &UserInfo{Sub: "attacker", Email: "a@b.c"}}
			p, _ := newAuditingProvider(t, fake)
			p.SetKeyAuthority(authority)

			handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			req.Header.Set("Authorization", header)
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)

			if fake.calls != 0 {
				t.Errorf("the IdP userinfo endpoint was called %d times with a key behind whitespace", fake.calls)
			}
			if authority.calls != 1 {
				t.Errorf("the key authority was asked %d times, want 1", authority.calls)
			}
			if resp.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
			}
		})
	}

	t.Run("a blank before the marker in the query string", func(t *testing.T) {
		authority := resolvedAuthority()
		fake := &fakeOIDCClient{userInfo: &UserInfo{Sub: "attacker", Email: "a@b.c"}}
		p, rec := newAuditingProvider(t, fake)
		p.SetKeyAuthority(authority)
		handler := p.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("handler ran for a key in the query string")
		}))

		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/sessions?token=%20"+validKeyPlaintext, nil))

		if resp.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.Code)
		}
		if fake.calls != 0 || authority.calls != 0 {
			t.Errorf("IdP calls = %d, authority calls = %d, want 0 and 0: a key in the wrong position goes nowhere",
				fake.calls, authority.calls)
		}
		assertSingleAuthEvent(t, rec.snapshot(), false, auth.APIKeyReasonWrongPosition)
	})
}

func TestMiddleware_ARefusedKeyIsAuditedAndCountedOnce(t *testing.T) {
	rec := installRecordingMetrics(t)
	p, audit := keyProvider(t, &recordingKeyAuthority{err: apikey.ErrInvalidKey})
	handler := p.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assertSingleAuthEvent(t, audit.snapshot(), false, auth.APIKeyReasonInvalid)
	got := rec.CounterSum("auth.token.validation",
		"gateway", p.gatewayID, "result", observability.AuthResultFailure, "reason", auth.APIKeyReasonInvalid)
	if got != 1 {
		t.Errorf("auth.token.validation{reason=%s} = %d, want exactly 1; got %+v",
			auth.APIKeyReasonInvalid, got, rec.Counters("auth.token.validation"))
	}
}

func TestMiddleware_ASoftAuthRefusalLeavesAMetricAndAWarn(t *testing.T) {
	rec := installRecordingMetrics(t)
	p, audit := keyProvider(t, &recordingKeyAuthority{err: apikey.ErrInvalidKey})
	var logs bytes.Buffer
	p.logger = slog.New(slog.NewTextHandler(&logs, nil))
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const shareID = "abcdefghijklmnopqrstu"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/share/"+shareID, nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (soft auth continues anonymously)", resp.Code)
	}
	assertNoAuthEvents(t, audit.snapshot())
	if got := rec.CounterSum("auth.token.validation",
		"gateway", p.gatewayID, "result", observability.AuthResultFailure, "reason", auth.APIKeyReasonInvalid); got != 1 {
		t.Errorf("auth.token.validation{reason=%s} = %d, want 1", auth.APIKeyReasonInvalid, got)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, auth.APIKeyReasonInvalid) {
		t.Errorf("no WARN naming the reason:\n%s", out)
	}
	for label, secret := range map[string]string{"share id": shareID, "key": validKeyPlaintext, "hash": apikey.Hash(validKeyPlaintext)} {
		if strings.Contains(out, secret) {
			t.Errorf("the log line carries the %s: %s", label, out)
		}
	}
}

func TestMiddleware_ACallerWhoHasGoneIsNotAuditedAsRejected(t *testing.T) {
	rec := installRecordingMetrics(t)
	p, audit := keyProvider(t, &recordingKeyAuthority{err: apikey.ErrInvalidKey})
	handler := p.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	gone, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil).WithContext(gone)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	assertNoAuthEvents(t, audit.snapshot())
	if n := len(rec.Counters("auth.token.validation")); n != 0 {
		t.Errorf("a disconnect was counted as a validation failure %d times", n)
	}
	if resp.Body.Len() != 0 {
		t.Errorf("a caller who has gone was written to: %s", resp.Body.String())
	}
}
