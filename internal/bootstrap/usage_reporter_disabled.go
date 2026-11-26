// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package bootstrap

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func GatewayUsageReporterOptions(context.Context, map[string]any, *slog.Logger) []runtime.Option {
	return nil
}

func PlatformUsageReporterOptions(context.Context, []samapp.AppEntry, *slog.Logger) []runtime.Option {
	return nil
}

func EmbeddedUsageReporterOptions(context.Context, []samapp.AppEntry, *slog.Logger) []runtime.Option {
	return nil
}
