// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
)

type connectorProbe func(ctx context.Context, logger *slog.Logger, values map[string]any, hasResource bool) (warnings []string, err error)

var connectorProbes = map[string]connectorProbe{}

func registerConnectorProbe(typ, subtype string, fn connectorProbe) {
	connectorProbes[connectorProbeKey(typ, subtype)] = fn
}

func connectorProbeKey(typ, subtype string) string {
	return typ + ":" + subtype
}

func runConnectorProbe(ctx context.Context, logger *slog.Logger, typ, subtype string, values any, hasResource bool) ([]string, error) {
	fn, ok := connectorProbes[connectorProbeKey(typ, subtype)]
	if !ok {
		return nil, nil
	}
	vmap, ok := values.(map[string]any)
	if !ok || vmap == nil {
		return nil, nil
	}
	return fn(ctx, logger, vmap, hasResource)
}
