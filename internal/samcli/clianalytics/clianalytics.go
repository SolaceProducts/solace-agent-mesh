// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package clianalytics

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

var productAnalyticsAvailable = buildedition.ProductAnalytics

var sessionID = sync.OnceValue(idgen.New)

func SessionID() string { return sessionID() }

const (
	reportTimeout = 5 * time.Second
	drainTimeout  = 2 * time.Second
)

type Options struct {
	DatabaseURL string
	DataDir     string
	Gateway     Gateway
	Sink        productanalytics.Sink
	Logger      *slog.Logger
}

type consentStore interface {
	Consent(ctx context.Context) productanalytics.ConsentState
	ConsentWithError(ctx context.Context) (productanalytics.ConsentState, error)
	SetConsent(ctx context.Context, state productanalytics.ConsentState) error
}

type Reporter struct {
	svc       *productanalytics.Service
	consent   consentStore
	available bool
	dataDir   string
	storeURL  string
	logger    *slog.Logger
}

func Open(ctx context.Context, opts Options) (*Reporter, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	logger = logger.With("logger_name", "cli-product-analytics")

	dataDir := opts.DataDir
	dbURL := opts.DatabaseURL
	if dbURL == "" {
		resolved, err := ResolveDataDir()
		if err != nil {
			return nil, err
		}
		dbURL = databaseURL(resolved)
		if dataDir == "" {
			dataDir = resolved
		}
	}

	state, closeStore, err := bootstrap.NewProductAnalyticsState(ctx, dbURL, logger)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("no product analytics store configured")
	}

	sink := opts.Sink
	releaseSink := func() {}
	if sink == nil {
		sink, releaseSink = bootstrap.NewProductAnalyticsSink(logger)
	}
	svc := productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Logger:   logger,
		OnStop: func() {
			releaseSink()
			if err := closeStore(); err != nil {
				logger.Warn("close product analytics store error", "error", productanalytics.SanitizedError(err))
			}
		},
	})
	svc.FileUnderAccounts(acceptedAccount(dbURL))
	productanalytics.ReportConsentChanges(state, svc)
	if err := svc.Start(ctx); err != nil {
		releaseSink()
		if closeErr := closeStore(); closeErr != nil {
			logger.Warn("close product analytics store error", "error", productanalytics.SanitizedError(closeErr))
		}
		return nil, fmt.Errorf("start product analytics: %w", err)
	}

	return &Reporter{
		svc:       svc,
		consent:   state,
		available: productAnalyticsAvailable,
		dataDir:   dataDir,
		storeURL:  dbURL,
		logger:    logger,
	}, nil
}

func (r *Reporter) StoreURL() string { return dbutil.RedactDSN(r.storeURL) }

func (r *Reporter) Available() bool { return r.available }

func (r *Reporter) Consent(ctx context.Context) productanalytics.ConsentState {
	return r.consent.Consent(ctx)
}

func (r *Reporter) ConsentWithError(ctx context.Context) (productanalytics.ConsentState, error) {
	return r.consent.ConsentWithError(ctx)
}

func (r *Reporter) SetConsent(ctx context.Context, state productanalytics.ConsentState) error {
	return r.consent.SetConsent(ctx, state)
}

func (r *Reporter) Track(ctx context.Context, name productanalytics.EventName, props map[string]any) {
	if !r.available {
		return
	}
	r.svc.Enqueue(ctx, productanalytics.SurfaceCLI, []productanalytics.RawEvent{{
		SessionID:  sessionID(),
		OccurredAt: time.Now().UTC().Format(productanalytics.TimestampLayout),
		Name:       name,
		Properties: props,
	}})
}

func (r *Reporter) Close(ctx context.Context) error {
	drainCtx, cancel := context.WithTimeout(ctx, drainTimeout)
	defer cancel()
	return r.svc.Stop(drainCtx)
}
