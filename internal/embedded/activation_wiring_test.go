// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func activationWiringApps(t *testing.T) EmbeddedApps {
	t.Helper()
	return EmbeddedApps{Gateway: []samapp.AppEntry{{Name: "webui", AppConfig: map[string]any{
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": "sqlite:///" + t.TempDir() + "/gw.db",
		},
	}}}}
}

func newActivationForWiring(t *testing.T, record activation.Record) *activation.Service {
	t.Helper()
	store := activation.NewStore(t.TempDir())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	return activation.NewService(store, nil, slog.New(slog.DiscardHandler))
}

func gatedProxyHandler(t *testing.T, svc *activation.Service) (http.Handler, *Instance) {
	t.Helper()
	inst := newTestInstance(Config{Activation: svc})
	setupMinimalRuntime(t, inst)
	proxy, err := inst.registerGatewayPlatformProxy(activationWiringApps(t), "test-ns")
	if err != nil {
		t.Fatalf("registerGatewayPlatformProxy: %v", err)
	}
	if proxy == nil {
		t.Fatal("expected a proxy for a gateway config")
	}
	return proxy.Handler(), inst
}

func errorType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	s, _ := body["errorType"].(string)
	return s
}

func TestEmbedded_GatesEveryAPIUntilActivated(t *testing.T) {
	handler, _ := gatedProxyHandler(t, newActivationForWiring(t, activation.Record{}))

	for _, path := range []string{"/api/v1/platform/agents", "/api/v2/platform/agents", "/api/v1/sessions", "/gw/any/mcp", "/gw/test-gw/mcp"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Origin", "https://elsewhere.example")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || errorType(t, rec) != activation.ErrorTypeActivationRequired {
			t.Errorf("%s: got %d %q, want 403 %s", path, rec.Code, errorType(t, rec), activation.ErrorTypeActivationRequired)
		}
		if acao := rec.Header().Get("Access-Control-Allow-Origin"); acao != "" {
			t.Errorf("%s: the gate's 403 carries Access-Control-Allow-Origin %q, want none", path, acao)
		}
	}
}

func TestEmbedded_ServesTheAPIOnceActivated(t *testing.T) {
	now := time.Now().UTC()
	handler, _ := gatedProxyHandler(t, newActivationForWiring(t, activation.Record{
		Account: &activation.Account{Subject: "auth0|ada", SignedInAt: now},
		EULA:    &activation.Acceptance{Subject: "auth0|ada", AcceptedAt: now},
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil))

	if errorType(t, rec) == activation.ErrorTypeActivationRequired {
		t.Fatalf("an activated install must not be gated, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestEmbedded_HandsTheActivationToEveryGateway(t *testing.T) {
	svc := newActivationForWiring(t, activation.Record{})
	inst := newTestInstance(Config{Activation: svc})
	setupMinimalRuntime(t, inst)

	gwe, err := inst.registerGatewayExecutor(activationWiringApps(t))
	if err != nil {
		t.Fatalf("registerGatewayExecutor: %v", err)
	}

	if n := len(gwe.Gateways()); n != 1 {
		t.Fatalf("gateways = %d, want 1", n)
	}
	for _, gw := range gwe.Gateways() {
		if gw.Activation() != svc {
			t.Errorf("gateway %s holds activation %p, want %p", gw.GatewayID(), gw.Activation(), svc)
		}
	}
}

func TestEmbedded_LeavesTheAPIOpenWithoutActivation(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)
	proxy, err := inst.registerGatewayPlatformProxy(activationWiringApps(t), "test-ns")
	if err != nil {
		t.Fatalf("registerGatewayPlatformProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil))

	if errorType(t, rec) == activation.ErrorTypeActivationRequired {
		t.Fatal("a build without the activation gate must never answer activation_required")
	}
}

func TestEmbedded_RefusesTheGateOnAGatewayThatRequiresALogin(t *testing.T) {
	inst := newTestInstance(Config{Activation: newActivationForWiring(t, activation.Record{})})
	setupMinimalRuntime(t, inst)
	apps := activationWiringApps(t)
	apps.Gateway[0].AppConfig["frontend_use_authorization"] = true

	_, err := inst.registerGatewayPlatformProxy(apps, "test-ns")

	if err == nil || !strings.Contains(err.Error(), "activation gate") {
		t.Fatalf("got %v, want a refusal naming the activation gate", err)
	}
}
