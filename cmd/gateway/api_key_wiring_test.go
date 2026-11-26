// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const wiringTestKey = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

type ownerKeyAuthority struct{}

func (ownerKeyAuthority) Validate(context.Context, string) (apikey.Resolution, error) {
	return apikey.Resolution{KeyID: "key-1", OwnerSub: "system:ci-deployer", Scopes: []string{"agent:*:invoke"}}, nil
}

func boundKeyHolder() *apikey.LazyAuthority {
	holder := apikey.NewLazyAuthority()
	holder.SetAuthority(ownerKeyAuthority{})
	return holder
}

func assertProviderServesKeys(t *testing.T, gatewayID string) {
	t.Helper()
	built, err := auth.CreateProvider(gatewayID, map[string]any{
		"external_auth_provider": "test-idp",
		"session_secret_key":     "test-session-secret",
	})
	if err != nil {
		t.Fatalf("CreateProvider(%q): %v", gatewayID, err)
	}
	provider, ok := built.(*oidcauth.Provider)
	if !ok {
		t.Fatalf("provider type = %T, want *oidcauth.Provider", built)
	}

	var got *auth.Subject
	handler := provider.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+wiringTestKey)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: the gateway's provider was built without the key holder; body=%s",
			resp.Code, resp.Body.String())
	}
	if got == nil || got.UserID() != "system:ci-deployer" {
		t.Errorf("identity = %+v, want the key owner", got)
	}
}

func TestSetupPerGatewaySigners_WiresTheKeyHolder(t *testing.T) {
	srv := newMockOIDCServer(t)
	exe, appConfigs := buildExecutorWithAdapters(t, []struct {
		name    string
		adapter entrypoint.GatewayAdapter
	}{
		{name: "webui-gw-keys", adapter: httpsse.NewHTTPSSEAdapter()},
	}, "test-idp")

	ec := newLocalTrustManager(t)
	ec.KeyAuthority = boundKeyHolder()
	catalog := map[string]*oidcauth.OIDCConfig{
		"test-idp": {Issuer: srv.URL, ClientID: "id", ClientSecret: "secret"},
	}
	if _, err := setupPerGatewaySigners(t.Context(), exe, appConfigs, catalog, ec, nil, "test", discardLogger()); err != nil {
		t.Fatalf("setupPerGatewaySigners: %v", err)
	}
	t.Cleanup(func() { oidcauth.UnregisterFactoryForGateway("webui-gw-keys") })

	assertProviderServesKeys(t, "webui-gw-keys")
}

func TestSetupRuntimeGateway_WiresTheKeyHolder(t *testing.T) {
	srv := newMockOIDCServer(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	appConfig := map[string]any{"gateway_id": "webui-gw-runtime-keys", "external_auth_provider": "test-idp"}
	core := entrypoint.NewGatewayCore(httpsse.NewHTTPSSEAdapter(), appConfig)
	ec := newLocalTrustManager(t)
	ec.KeyAuthority = boundKeyHolder()
	deps := runtimeGatewayDeps{
		providersCatalog: map[string]*oidcauth.OIDCConfig{
			"test-idp": {Issuer: srv.URL, ClientID: "id", ClientSecret: "secret"},
		},
		ec:        ec,
		broker:    brk,
		namespace: "test",
		logger:    discardLogger(),
	}
	if err := setupRuntimeGateway(t.Context(), core, appConfig, deps); err != nil {
		t.Fatalf("setupRuntimeGateway: %v", err)
	}
	t.Cleanup(func() { oidcauth.UnregisterFactoryForGateway("webui-gw-runtime-keys") })

	assertProviderServesKeys(t, "webui-gw-runtime-keys")
}

func TestDisableKeysUnlessPlatformHosted_SettlesTheHolder(t *testing.T) {
	holder := apikey.NewLazyAuthority()
	disableKeysUnlessPlatformHosted(holder, false, nil, discardLogger())

	if !holder.Bound() {
		t.Fatal("the holder is still pending")
	}
	if _, err := holder.Validate(t.Context(), "any-hash"); !errors.Is(err, apikey.ErrNotEnabled) {
		t.Errorf("Validate error = %v, want ErrNotEnabled (401, not 503)", err)
	}
	disableKeysUnlessPlatformHosted(nil, false, nil, discardLogger())
}

func TestDisableKeysUnlessPlatformHosted_LeavesTheHolderToAHostedPlatform(t *testing.T) {
	holder := apikey.NewLazyAuthority()
	disableKeysUnlessPlatformHosted(holder, true, nil, discardLogger())

	if holder.Bound() {
		t.Fatal("the holder was settled although this process hosts the platform that binds it")
	}
}

func TestDisableKeysUnlessPlatformHosted_WarnsOnlyWhenKeysAreExplicitlySwitchedOn(t *testing.T) {
	for name, tc := range map[string]struct {
		env      string
		wantWarn bool
	}{
		"explicitly on":  {"true", true},
		"explicitly off": {"false", false},
		"default":        {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SAM_FEATURE_MACHINE_TOKENS", tc.env)
			if tc.env == "" {
				_ = os.Unsetenv("SAM_FEATURE_MACHINE_TOKENS")
			}
			flags := features.NewRegistry()
			flags.Register(features.Definition{Key: features.KeyMachineTokens, Default: true})
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

			holder := apikey.NewLazyAuthority()
			disableKeysUnlessPlatformHosted(holder, false, flags, logger)

			if !holder.Bound() {
				t.Error("the holder was left pending")
			}
			if got := bytes.Contains(logs.Bytes(), []byte("level=WARN")); got != tc.wantWarn {
				t.Errorf("WARN logged = %v, want %v:\n%s", got, tc.wantWarn, logs.String())
			}
		})
	}
}
