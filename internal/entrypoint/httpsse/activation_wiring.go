// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package httpsse

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/activation/edition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/freellm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/solaceid"
)

const (
	pathActivation             = "/api/v1/activation"
	pathActivationSignIn       = "/api/v1/activation:signIn"
	pathActivationCancelSignIn = "/api/v1/activation:cancelSignIn"
	pathActivationAccept       = "/api/v1/activation:accept"
	pathActivationTerms        = "/api/v1/activation/terms"
	pathFreeModel              = "/api/v1/freeModel"
	pathFreeModelSignIn        = "/api/v1/freeModel:signIn"
	pathFreeModelRefresh       = "/api/v1/freeModel:refresh"
)

func registerActivationRoutes(a *HTTPSSEAdapter, mux *http.ServeMux, core *entrypoint.GatewayCore) {
	svc, ok := core.Activation().(*activation.Service)
	if !ok {
		return
	}
	svc.SetAuditor(core.Services().AuditLogger())
	h := &activationHandler{svc: svc, core: core, logger: a.logger}
	if free, linked := freellm.ConfigFromBuild(); linked {
		svc.SetFreeModel(freeModelPlatform(free, core, a.logger))
		h.allowances = freellm.NewAllowances(free, tokenbroker.NewControlPlaneTokens(core.Services().Broker(), core.Namespace()))
	}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathActivation, Scope: rbac.ScopeActivationRead}, h.handleGet)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathActivationSignIn, Scope: rbac.ScopeActivationUpdate}, h.handleSignIn)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathActivationCancelSignIn, Scope: rbac.ScopeActivationUpdate}, h.handleCancelSignIn)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathActivationAccept, Scope: rbac.ScopeActivationUpdate}, h.handleAccept)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathActivationTerms, Scope: rbac.ScopeActivationRead}, h.handleGetTerms)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathFreeModel, Scope: rbac.ScopeActivationRead}, h.handleGetFreeModel)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathFreeModelSignIn, Scope: rbac.ScopeActivationUpdate}, h.handleFreeModelSignIn)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathFreeModelRefresh, Scope: rbac.ScopeActivationUpdate}, h.handleRefreshFreeModel)
}

func freeModelPlatform(free freellm.Config, core *entrypoint.GatewayCore, logger *slog.Logger) *freellm.Platform {
	sid := edition.SolaceIDConfig()
	brk, namespace := core.Services().Broker(), core.Namespace()
	tokenEndpoint := func(ctx context.Context) (string, error) {
		return solaceid.TokenEndpoint(ctx, sid, logger)
	}
	return freellm.NewPlatform(free, sid, tokenEndpoint, tokenbroker.NewControlPlaneGrants(brk, namespace), tokenbroker.NewControlPlaneTokens(brk, namespace))
}
