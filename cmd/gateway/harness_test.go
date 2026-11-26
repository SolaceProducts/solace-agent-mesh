// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func newMockOIDCServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                srv.URL,
				"authorization_endpoint":                srv.URL + "/authorize",
				"token_endpoint":                        srv.URL + "/token",
				"userinfo_endpoint":                     srv.URL + "/userinfo",
				"jwks_uri":                              srv.URL + "/jwks",
				"response_types_supported":              []string{"code"},
				"subject_types_supported":               []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewMockOIDCServer_ServesDiscovery(t *testing.T) {
	srv := newMockOIDCServer(t)
	resp, err := http.Get(srv.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET discovery: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discovery status = %d, want 200", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc["issuer"] != srv.URL {
		t.Errorf("issuer = %q, want %q", doc["issuer"], srv.URL)
	}
}

type fakeAdapter struct{}

func (a *fakeAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNone}
}
func (a *fakeAdapter) Init(context.Context, *entrypoint.GatewayCore) error                        { return nil }
func (a *fakeAdapter) Start(context.Context) error                                                { return nil }
func (a *fakeAdapter) Stop(context.Context) error                                                 { return nil }
func (a *fakeAdapter) OnStatusUpdate(context.Context, string, *a2a.TaskStatusUpdateEvent, []byte) {}
func (a *fakeAdapter) OnArtifactUpdate(context.Context, string, *a2a.TaskArtifactUpdateEvent, []byte) {
}
func (a *fakeAdapter) OnFinalResponse(context.Context, string, *a2a.Task, []byte) {}
func (a *fakeAdapter) OnError(context.Context, string, *a2a.Error, []byte)        {}
func (a *fakeAdapter) OnAgentDiscovered(*a2a.AgentCard)                           {}
func (a *fakeAdapter) OnAgentRemoved(string)                                      {}

func newTestServices(t *testing.T) runtime.Services {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil)
	return rt.Services()
}

func buildExecutorWithAdapters(
	t *testing.T,
	adapters []struct {
		name    string
		adapter entrypoint.GatewayAdapter
	},
	idpKey string,
) (*entrypoint.GatewayExecutor, []samapp.AppEntry) {
	t.Helper()

	svc := newTestServices(t)

	factory := func(appConfig map[string]any, svc runtime.Services) (*entrypoint.GatewayCore, error) {
		name, _ := appConfig["__test_name"].(string)
		var adapter entrypoint.GatewayAdapter
		for _, e := range adapters {
			if e.name == name {
				adapter = e.adapter
				break
			}
		}
		if adapter == nil {
			t.Fatalf("no adapter for %q", name)
		}
		core := entrypoint.NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	}
	exe := entrypoint.NewGatewayExecutor(svc, factory)

	appConfigs := make([]samapp.AppEntry, 0, len(adapters))
	for _, e := range adapters {
		appCfg := map[string]any{
			"namespace":              "test",
			"gateway_id":             e.name,
			"__test_name":            e.name,
			"enable_auth":            true,
			"external_auth_provider": idpKey,
			"session_secret_key":     "test-session-key",
		}
		if err := exe.LoadConfig(e.name, appCfg); err != nil {
			t.Fatalf("LoadConfig %q: %v", e.name, err)
		}
		appConfigs = append(appConfigs, samapp.AppEntry{
			Name:      e.name,
			AppConfig: appCfg,
		})
	}
	return exe, appConfigs
}

func TestBuildExecutorWithAdapters_ReturnsExpectedShape(t *testing.T) {
	srv := newMockOIDCServer(t)
	_ = srv

	exe, cfgs := buildExecutorWithAdapters(t, []struct {
		name    string
		adapter entrypoint.GatewayAdapter
	}{
		{name: "fake-h1", adapter: &fakeAdapter{}},
	}, "test-idp")

	if got := len(exe.Gateways()); got != 1 {
		t.Fatalf("Gateways() len = %d, want 1", got)
	}
	if got := exe.Gateways()[0].GatewayID(); got != "fake-h1" {
		t.Errorf("GatewayID = %q, want fake-h1", got)
	}
	if len(cfgs) != 1 || cfgs[0].Name != "fake-h1" {
		t.Errorf("appConfigs shape unexpected: %+v", cfgs)
	}
}

func newLocalTrustManager(t *testing.T) *bootstrap.Components {
	t.Helper()
	cfg := trust.DefaultConfig()
	cfg.Enabled = false
	mgr := trust.NewManager(cfg, "gateway", "test-host", "test-ns", nil, slog.Default())
	if err := mgr.Init(); err != nil {
		t.Fatalf("trust.Manager.Init: %v", err)
	}
	return &bootstrap.Components{
		TrustManager: mgr,
		AuditLogger:  coreauth.NoopAuditLogger{},
	}
}

func TestNewLocalTrustManager_AcceptsRegistration(t *testing.T) {
	ec := newLocalTrustManager(t)
	km, err := trust.NewKeyManager("smoke-gw")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	if err := ec.TrustManager.RegisterAdditionalSigner(t.Context(), "smoke-gw", km, ""); err != nil {
		t.Fatalf("RegisterAdditionalSigner: %v", err)
	}
}

func newTestTrustSigner(t *testing.T, gatewayID string) *trust.Signer {
	t.Helper()
	km, err := trust.NewKeyManager(gatewayID)
	if err != nil {
		t.Fatalf("trust.NewKeyManager: %v", err)
	}
	return trust.NewSigner(km, gatewayID, time.Hour, 2*time.Hour)
}
