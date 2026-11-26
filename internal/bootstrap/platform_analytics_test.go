// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package bootstrap

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func platformApp(t *testing.T, consentStoreURL string) samapp.AppEntry {
	t.Helper()
	cfg := map[string]any{
		"component_type": "platform",
		"namespace":      "split-deploy-test",
		"database_url":   "sqlite:///" + filepath.Join(t.TempDir(), "platform.db"),
	}
	if consentStoreURL != "" {
		cfg["product_analytics"] = map[string]any{"database_url": consentStoreURL}
	}
	return samapp.AppEntry{Name: "platform_service_app", AppConfig: cfg}
}

func grantedConsentStore(t *testing.T) string {
	t.Helper()
	url := "sqlite:///" + filepath.Join(t.TempDir(), "gateway.db")
	state, closeStore, err := NewProductAnalyticsState(context.Background(), url, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open consent store: %v", err)
	}
	defer func() {
		if closeErr := closeStore(); closeErr != nil {
			t.Fatalf("close consent store: %v", closeErr)
		}
	}()
	if err := state.SetConsent(context.Background(), productanalytics.ConsentGranted); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	return url
}

func markerOf(t *testing.T, svc *productanalytics.Service, sinkFile, eventName string) string {
	t.Helper()
	svc.Enqueue(context.Background(), productanalytics.SurfaceServer, []productanalytics.RawEvent{{Name: eventName}})
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}

	f, err := os.Open(sinkFile)
	if err != nil {
		t.Fatalf("open the debug sink: %v", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var record struct {
			Name       string `json:"name"`
			DistinctID string `json:"distinctId"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("debug sink line is not JSON: %v (%q)", err, scanner.Text())
		}
		if record.Name == eventName {
			return record.DistinctID
		}
	}
	return ""
}

func TestSplitPlatformSharesTheGatewaysInstallMarker(t *testing.T) {
	forceProductAnalyticsAvailable(t)
	consentStore := grantedConsentStore(t)
	sinkFile := filepath.Join(t.TempDir(), "events.ndjson")
	t.Setenv(productanalytics.EnvDebugSinkFile, sinkFile)
	ctx := context.Background()

	gatewaySvc := NewProductAnalytics(ctx, consentStore, nil, slog.New(slog.DiscardHandler))
	if gatewaySvc == nil {
		t.Fatal("the gateway must build an analytics service when this build carries the collection path")
	}
	gatewayMarker := markerOf(t, gatewaySvc, sinkFile, productanalytics.EventAgentDeployed)
	if gatewayMarker == "" {
		t.Fatal("the gateway reported no install marker, so there is nothing to compare against")
	}

	platformSvc := NewPlatformProductAnalytics(ctx, []samapp.AppEntry{platformApp(t, consentStore)}, slog.New(slog.DiscardHandler))
	if platformSvc == nil {
		t.Fatal("a platform process with no gateway must build its own analytics service")
	}
	platformMarker := markerOf(t, platformSvc, sinkFile, productanalytics.EventInstallInventory)

	if platformMarker != gatewayMarker {
		t.Errorf("the platform reported install marker %q, the gateway %q; a split deployment must be one install, not two", platformMarker, gatewayMarker)
	}
}

func TestSplitPlatformFindsTheConsentStoreInTheEnvironment(t *testing.T) {
	forceProductAnalyticsAvailable(t)
	consentStore := grantedConsentStore(t)
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", consentStore)

	app := platformApp(t, "")
	if got := platformConsentStoreURL(app.AppConfig); got != consentStore {
		t.Errorf("consent store = %q, want the gateway store %q", got, consentStore)
	}

	svc := NewPlatformProductAnalytics(context.Background(), []samapp.AppEntry{app}, slog.New(slog.DiscardHandler))
	if svc == nil {
		t.Fatal("a platform process with the gateway store in its environment must build a service")
	}
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
}

func TestTheConsentStoreIsNeverThePlatformsOwnDatabase(t *testing.T) {
	forceProductAnalyticsAvailable(t)
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", "")
	app := platformApp(t, "")
	platformDB, _ := app.AppConfig["database_url"].(string)
	if platformDB == "" {
		t.Fatal("precondition: the platform app entry must carry its own database_url")
	}

	got := platformConsentStoreURL(app.AppConfig)
	if got == platformDB {
		t.Errorf("consent store resolved to the platform's own database %q", got)
	}
	if got != "" {
		t.Errorf("consent store = %q, want nothing when neither config nor environment names one", got)
	}

	if svc := NewPlatformProductAnalytics(context.Background(), []samapp.AppEntry{app}, slog.New(slog.DiscardHandler)); svc != nil {
		t.Error("with no consent store named, the platform must build no service rather than guess at one")
	}
}

func TestExplicitConsentStoreConfigWins(t *testing.T) {
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", "sqlite:///from-the-environment.db")
	app := platformApp(t, "sqlite:///from-the-config.db")
	if got := platformConsentStoreURL(app.AppConfig); got != "sqlite:///from-the-config.db" {
		t.Errorf("consent store = %q, want the configured one", got)
	}
}

func TestAProcessHostingAGatewayBuildsNoSecondService(t *testing.T) {
	forceProductAnalyticsAvailable(t)
	consentStore := grantedConsentStore(t)
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", consentStore)

	apps := []samapp.AppEntry{
		platformApp(t, consentStore),
		{Name: "webui_gateway", AppConfig: map[string]any{
			"gateway_id":      "web-ui",
			"session_service": map[string]any{"database_url": consentStore},
		}},
	}
	if svc := NewPlatformProductAnalytics(context.Background(), apps, slog.New(slog.DiscardHandler)); svc != nil {
		t.Error("a co-located gateway and platform must build one analytics service, not two")
	}
}

func TestPlatformCoreReadsTheServiceItsOwnBootstrapBuilt(t *testing.T) {
	forceProductAnalyticsAvailable(t)
	consentStore := grantedConsentStore(t)
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", consentStore)
	ctx := context.Background()

	app := platformApp(t, "")
	opts := BuildPlatformRuntimeOptions(ctx, nil, []samapp.AppEntry{app}, slog.New(slog.DiscardHandler))
	rt := runtime.NewRuntime(nil, nil, opts...)
	if rt.Services().UsageReporter() == nil {
		t.Fatal("a platform-only process's runtime options must carry an analytics service")
	}

	core := NewPlatformCore(rt.Services(), app.AppConfig, platform.ExtensionPoints{})
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("platform init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	if core.ResourceAnalytics() == nil {
		t.Error("the platform core built no reporter, so it read no analytics service from its own runtime")
	}
}

func TestAPlatformWithNoConsentStoreSaysWhatItWillDoInstead(t *testing.T) {
	forceProductAnalyticsAvailable(t)
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", "")
	var lines strings.Builder
	logger := slog.New(slog.NewTextHandler(&lines, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if svc := NewPlatformProductAnalytics(context.Background(), []samapp.AppEntry{platformApp(t, "")}, logger); svc != nil {
		t.Fatal("expected no service")
	}
	logged := lines.String()
	if !strings.Contains(logged, "no consent store configured") {
		t.Errorf("expected a line naming the missing consent store, got %q", logged)
	}
	if !strings.Contains(logged, "published over the broker") {
		t.Errorf("expected the line to name the broker fallback, got %q", logged)
	}
	if !strings.Contains(logged, "WEB_UI_GATEWAY_DATABASE_URL") {
		t.Errorf("expected the line to name the setting that reports here instead, got %q", logged)
	}
}
