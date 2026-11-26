// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type analyticsWiring struct{}

func (w *analyticsWiring) init(*entrypoint.GatewayCore) {
}

func (w *analyticsWiring) registerRoutes(*HTTPSSEAdapter, *http.ServeMux, *entrypoint.GatewayCore) {
}
