// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	pathProductAnalyticsConsent = "/api/v1/productAnalytics/consent"

	pathProductAnalyticsIngest    = "/api/v1/productAnalytics/ingest"
	pathProductAnalyticsIngestCLI = "/api/v1/productAnalytics/ingest/cli"

	publicReasonTelemetryIngest = "authenticated telemetry ingest, no resource to scope, consent and allow-list enforced in the service"

	publicReasonTelemetryConsentRead = "authenticated telemetry consent read — decision only, no marker; the Privacy panel must render for every user"
)

type analyticsWiring struct {
	svc     *productanalytics.Service
	consent productanalytics.ConsentManager
}

func (w *analyticsWiring) init(core *entrypoint.GatewayCore) {
	svc := core.Services()
	if svc == nil {
		return
	}
	w.svc, _ = svc.UsageReporter().(*productanalytics.Service)
	if w.svc != nil {
		w.consent = w.svc.ConsentManager()
	}
}

func (w *analyticsWiring) registerRoutes(a *HTTPSSEAdapter, mux *http.ServeMux, core *entrypoint.GatewayCore) {
	if w.svc != nil {
		h := &productAnalyticsHandler{svc: w.svc, logger: a.logger}
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathProductAnalyticsIngest, IsPublic: publicReasonTelemetryIngest}, h.handleIngest)
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathProductAnalyticsIngestCLI, IsPublic: publicReasonTelemetryIngest}, h.handleCLIIngest)
	}
	if w.consent != nil {
		h := &productAnalyticsConsentHandler{
			state:  w.consent,
			audit:  core.Services().AuditLogger(),
			logger: a.logger,
		}
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathProductAnalyticsConsent, IsPublic: publicReasonTelemetryConsentRead}, h.handleGet)
		a.wireRoute(mux, RouteRegistration{Method: "PUT", Path: pathProductAnalyticsConsent, Scope: rbac.ScopeProductAnalyticsConsentUpdate}, h.handleSet)
	}
}
