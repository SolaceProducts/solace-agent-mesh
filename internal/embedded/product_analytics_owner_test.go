// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package embedded

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func newOwnerTestInstance() *Instance {
	return &Instance{
		sharedBroker: broker.NewDevBroker(),
		logger:       slog.Default(),
	}
}

func ownerTestApps(t *testing.T) EmbeddedApps {
	t.Helper()
	return EmbeddedApps{
		Platform: []samapp.AppEntry{{Name: "platform", AppConfig: map[string]any{"component_type": "platform"}}},
		Gateway: []samapp.AppEntry{{Name: "webui", AppConfig: map[string]any{
			"gateway_id": "test-gw",
			"session_service": map[string]any{
				"type":         "sql",
				"database_url": "sqlite:///" + t.TempDir() + "/gw.db",
			},
		}}},
	}
}

func TestEmbeddedOwnsProductAnalyticsOnTheAWEBranch(t *testing.T) {
	if !buildedition.ProductAnalytics {
		t.Skip("requires -tags product_analytics")
	}
	inst := newOwnerTestInstance()

	if err := inst.buildRuntime(context.Background(), map[string]any{}, ownerTestApps(t), "test-ns"); err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	svc, _ := inst.rt.Services().UsageReporter().(*productanalytics.Service)
	if svc == nil {
		t.Fatal("embedded must own the product-analytics service, otherwise desktop collects nothing while every endpoint still answers")
	}
	if svc.ConsentManager() == nil {
		t.Error("a configured gateway database must yield a consent manager, otherwise consent cannot be recorded")
	}
}

func TestEmbeddedOwnsNoProductAnalyticsWhenFlagOff(t *testing.T) {
	if buildedition.ProductAnalytics {
		t.Skip("only meaningful without -tags product_analytics")
	}
	inst := newOwnerTestInstance()

	if err := inst.buildRuntime(context.Background(), map[string]any{}, ownerTestApps(t), "test-ns"); err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	if inst.rt.Services().UsageReporter() != nil {
		t.Error("no service should be owned in a build with no collection path")
	}
}
