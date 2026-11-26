// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package bootstrap

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestBuildGatewayRuntimeOptions_OwnsProductAnalyticsWhenFlagOn(t *testing.T) {
	forceProductAnalyticsAvailable(t)

	apps := []samapp.AppEntry{{
		Name: "webui",
		AppConfig: map[string]any{
			"session_service": map[string]any{
				"type":         "sql",
				"database_url": "sqlite:///" + t.TempDir() + "/gw.db",
			},
		},
	}}

	opts, err := BuildGatewayRuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err != nil {
		t.Fatalf("build options: %v", err)
	}

	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)
	svc, _ := rt.Services().UsageReporter().(*productanalytics.Service)
	if svc == nil {
		t.Fatal("gateway process must own the product-analytics service when this build carries the collection path")
	}
	if svc.ConsentManager() == nil {
		t.Error("a configured database must yield a consent manager, otherwise consent cannot be recorded")
	}
}

func TestBuildGatewayRuntimeOptions_NoProductAnalyticsWhenFlagOff(t *testing.T) {
	forceProductAnalyticsUnavailable(t)

	apps := []samapp.AppEntry{{
		Name: "webui",
		AppConfig: map[string]any{
			"session_service": map[string]any{
				"type":         "sql",
				"database_url": "sqlite:///" + t.TempDir() + "/gw.db",
			},
		},
	}}

	opts, err := BuildGatewayRuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err != nil {
		t.Fatalf("build options: %v", err)
	}

	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)
	if rt.Services().UsageReporter() != nil {
		t.Error("no service should be owned in a build with no collection path")
	}
}

func TestNewProductAnalyticsForApps(t *testing.T) {
	apps := []samapp.AppEntry{
		{Name: "no-db", AppConfig: map[string]any{}},
		{Name: "webui", AppConfig: map[string]any{
			"session_service": map[string]any{
				"type":         "sql",
				"database_url": "sqlite:///" + t.TempDir() + "/gw.db",
			},
		}},
	}

	t.Run("flag on yields an owner with a consent manager", func(t *testing.T) {
		forceProductAnalyticsAvailable(t)
		svc := NewProductAnalyticsForApps(context.Background(), apps, slog.Default())
		if svc == nil {
			t.Fatal("expected a service when this build carries the collection path")
		}
		if svc.ConsentManager() == nil {
			t.Error("database URL was not found on a later app entry")
		}
	})

	t.Run("flag off yields no owner", func(t *testing.T) {
		forceProductAnalyticsUnavailable(t)
		if NewProductAnalyticsForApps(context.Background(), apps, slog.Default()) != nil {
			t.Error("expected no service in a build with no collection path")
		}
	})
}
