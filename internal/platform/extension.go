// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

type BackgroundService interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type ExtensionPoints struct {
	RegisterRoutes func(mux *http.ServeMux, core *PlatformCore)

	BackgroundServices func(core *PlatformCore) []BackgroundService

	LeaderJobs func(core *PlatformCore) []leader.Job

	AuthMiddleware func(next http.Handler) http.Handler
}
