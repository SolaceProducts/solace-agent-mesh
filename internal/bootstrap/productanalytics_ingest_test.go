// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package bootstrap

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const (
	ingestComponentName        = "product_analytics_broker_ingest"
	usageReporterComponentName = "usage_reporter"
)

func gatewayAppWithConsentStore(t *testing.T) []samapp.AppEntry {
	t.Helper()
	return []samapp.AppEntry{{
		Name: "web-ui",
		AppConfig: map[string]any{
			"gateway_id":      "web-ui",
			"namespace":       "ingest-wiring-test",
			"session_service": map[string]any{"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "gateway.db")},
		},
	}}
}

func gatewayRuntimeOptions(t *testing.T) []runtime.Option {
	t.Helper()
	forceProductAnalyticsAvailable(t)
	opts, err := BuildGatewayRuntimeOptions(t.Context(), nil, gatewayAppWithConsentStore(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("build gateway runtime options: %v", err)
	}
	return opts
}

func registeredComponents(rt *runtime.Runtime) map[string]bool {
	names := make(map[string]bool)
	for _, c := range rt.Components() {
		names[c.Name] = true
	}
	return names
}

func TestGatewayRuntimeRegistersTheBrokerIngestListener(t *testing.T) {
	opts := gatewayRuntimeOptions(t)
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)
	names := registeredComponents(rt)
	if !names[usageReporterComponentName] {
		t.Fatal("precondition: the gateway runtime should own the analytics pipeline")
	}
	if !names[ingestComponentName] {
		t.Errorf("%s not registered; a split platform would publish to nobody", ingestComponentName)
	}
}

func TestNoIngestListenerWithoutABroker(t *testing.T) {
	opts := gatewayRuntimeOptions(t)
	rt := runtime.NewRuntime(nil, nil, opts...)
	names := registeredComponents(rt)
	if !names[usageReporterComponentName] {
		t.Fatal("precondition: the gateway runtime should own the analytics pipeline")
	}
	if names[ingestComponentName] {
		t.Errorf("%s registered with no broker to listen on", ingestComponentName)
	}
}

func TestNoIngestListenerWhenThisProcessCollectsNothing(t *testing.T) {
	forceProductAnalyticsUnavailable(t)
	opts, err := BuildGatewayRuntimeOptions(t.Context(), nil, gatewayAppWithConsentStore(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("build gateway runtime options: %v", err)
	}
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)
	names := registeredComponents(rt)
	if names[usageReporterComponentName] {
		t.Fatal("precondition: no pipeline should be built with the feature off")
	}
	if names[ingestComponentName] {
		t.Errorf("%s registered in a process that collects nothing", ingestComponentName)
	}
}
