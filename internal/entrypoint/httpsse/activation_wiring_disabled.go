// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !solace_id

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func registerActivationRoutes(*HTTPSSEAdapter, *http.ServeMux, *entrypoint.GatewayCore) {
}
