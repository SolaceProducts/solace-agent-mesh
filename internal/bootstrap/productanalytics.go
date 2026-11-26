// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	pastore "github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

var productAnalyticsAvailable = buildedition.ProductAnalytics

type productAnalyticsEdition struct {
	compiledIn      bool
	tokenConfigured bool
}

func builtProductAnalyticsEdition() productAnalyticsEdition {
	return productAnalyticsEdition{
		compiledIn:      buildedition.ProductAnalytics,
		tokenConfigured: productanalytics.TokenConfigured(),
	}
}

func logProductAnalyticsEdition(logger *slog.Logger) {
	logProductAnalyticsEditionFor(builtProductAnalyticsEdition(), logger)
}

func logProductAnalyticsEditionFor(edition productAnalyticsEdition, logger *slog.Logger) {
	if !edition.compiledIn {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "product-analytics")

	if !edition.tokenConfigured {
		logger.Warn("product analytics is compiled in but no vendor token was injected at build time, collection stays off")
		return
	}
	logger.Info("product analytics is compiled in")
}

func NewProductAnalytics(ctx context.Context, databaseURL string, cfg config.Config, logger *slog.Logger) *productanalytics.Service {
	if !productAnalyticsAvailable {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	paLogger := logger.With("logger_name", "product-analytics")

	sink, releaseSink := NewProductAnalyticsSink(paLogger)
	opts := productanalytics.Options{
		Config: productanalytics.ParseConfig(cfg),
		Sink:   sink,
		Logger: paLogger,
		OnStop: releaseSink,
	}

	var consentState *productanalytics.PersistedState
	switch state, closeStore, err := NewProductAnalyticsState(ctx, databaseURL, paLogger); {
	case databaseURL == "":
		paLogger.Warn("no database configured: product analytics consent cannot be recorded, collection stays off")
	case err != nil:
		paLogger.Warn("product analytics store unavailable, collection stays off", "error", productanalytics.SanitizedError(err))
	default:
		consentState = state
		opts.Consent = state
		opts.Identity = state
		opts.OnStop = func() {
			releaseSink()
			if closeErr := closeStore(); closeErr != nil {
				paLogger.Warn("close product analytics store error", "error", productanalytics.SanitizedError(closeErr))
			}
		}
	}
	svc := productanalytics.NewService(opts)
	if consentState != nil {
		productanalytics.ReportConsentChanges(consentState, svc)
	}
	return svc
}

func withProductAnalyticsBrokerIngest(svc *productanalytics.Service, namespace string, logger *slog.Logger) runtime.Option {
	return func(r *runtime.Runtime) {
		brk := r.Services().Broker()
		ingest := productanalytics.NewBrokerIngest(brk, svc, namespace, logger)
		if ingest == nil {
			return
		}
		r.Register("product_analytics_broker_ingest", ingest)
		if cliIngest := productanalytics.NewCLIBrokerIngest(brk, svc, namespace, logger); cliIngest != nil {
			r.Register("product_analytics_cli_broker_ingest", cliIngest)
		}
	}
}

var newVendorSink = productanalytics.NewVendorSink

func NewProductAnalyticsSink(logger *slog.Logger) (productanalytics.Sink, func()) {
	if logger == nil {
		logger = slog.Default()
	}
	debug, err := productanalytics.NewDebugSinkFromEnv()
	if err != nil {
		logger.Warn("product analytics debug sink unavailable, falling back", "error", productanalytics.SanitizedError(err))
	}
	if debug != nil {
		logger.Info("product analytics debug sink enabled, writing scrubbed events as NDJSON")
		return debug, func() {
			if closeErr := debug.Close(); closeErr != nil {
				logger.Warn("close product analytics debug sink error", "error", productanalytics.SanitizedError(closeErr))
			}
		}
	}
	if vendor, ok := newVendorSink(logger); ok {
		return vendor, noRelease
	}
	return productanalytics.NewLoggingSink(logger), noRelease
}

func noRelease() {
}

func NewProductAnalyticsState(ctx context.Context, databaseURL string, logger *slog.Logger) (*productanalytics.PersistedState, func() error, error) {
	if databaseURL == "" {
		return nil, nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	db, err := pastore.Open(databaseURL, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("open product analytics store: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate product analytics store: %w", err)
	}
	return productanalytics.NewPersistedState(pastore.NewProductAnalyticsRepo(db), logger), db.Close, nil
}

func NewProductAnalyticsForApps(ctx context.Context, apps []samapp.AppEntry, logger *slog.Logger) *productanalytics.Service {
	var databaseURL string
	for _, app := range apps {
		if url := gatewayDatabaseURL(app.AppConfig); url != "" {
			databaseURL = url
			break
		}
	}
	return NewProductAnalytics(ctx, databaseURL, nil, logger)
}
