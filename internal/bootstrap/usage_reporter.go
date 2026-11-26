// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package bootstrap

import (
	"context"
	"log/slog"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func GatewayUsageReporterOptions(ctx context.Context, appConfig map[string]any, logger *slog.Logger) []runtime.Option {
	logProductAnalyticsEdition(logger)
	svc := NewProductAnalytics(ctx, gatewayDatabaseURL(appConfig), nil, logger)
	if svc == nil {
		return nil
	}
	namespace, _ := appConfig["namespace"].(string)
	return []runtime.Option{
		runtime.WithUsageReporter(svc),
		withProductAnalyticsBrokerIngest(svc, namespace, logger),
	}
}

func PlatformUsageReporterOptions(ctx context.Context, apps []samapp.AppEntry, logger *slog.Logger) []runtime.Option {
	svc := NewPlatformProductAnalytics(ctx, apps, logger)
	if svc == nil {
		return nil
	}
	return []runtime.Option{runtime.WithUsageReporter(svc)}
}

func EmbeddedUsageReporterOptions(ctx context.Context, apps []samapp.AppEntry, logger *slog.Logger) []runtime.Option {
	svc := NewProductAnalyticsForApps(ctx, apps, logger)
	if svc == nil {
		return nil
	}
	return []runtime.Option{runtime.WithUsageReporter(svc)}
}

func NewPlatformProductAnalytics(ctx context.Context, apps []samapp.AppEntry, logger *slog.Logger) *productanalytics.Service {
	if logger == nil {
		logger = slog.Default()
	}
	if hostsGateway(apps) {
		return nil
	}
	var platformApp samapp.AppEntry
	if len(apps) > 0 {
		platformApp = apps[0]
	}
	consentStoreURL := platformConsentStoreURL(platformApp.AppConfig)
	if consentStoreURL == "" {
		if productAnalyticsAvailable {
			logger.With("logger_name", "product-analytics").Warn(
				"no consent store configured for this platform process: the install inventory will be published over the broker for the entrypoint to report; set product_analytics.database_url to the database the entrypoint's session_service uses, or WEB_UI_GATEWAY_DATABASE_URL in its environment, to report it here instead")
		}
		return nil
	}
	return NewProductAnalytics(ctx, consentStoreURL, nil, logger)
}

func platformConsentStoreURL(appConfig map[string]any) string {
	if section, ok := appConfig["product_analytics"].(map[string]any); ok {
		if url, ok := section["database_url"].(string); ok && url != "" {
			return url
		}
	}
	return os.Getenv("WEB_UI_GATEWAY_DATABASE_URL")
}

func hostsGateway(apps []samapp.AppEntry) bool {
	for _, app := range apps {
		if app.AppConfig["gateway_id"] != nil {
			return true
		}
	}
	return false
}

func gatewayDatabaseURL(appConfig map[string]any) string {
	sessionService, ok := appConfig["session_service"].(map[string]any)
	if !ok {
		return ""
	}
	url, _ := sessionService["database_url"].(string)
	return url
}
