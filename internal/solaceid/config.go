// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package solaceid

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
)

var (
	buildIssuer   string
	buildClientID string
)

func ConfigFromBuild() Config {
	return Config{Issuer: buildIssuer, ClientID: buildClientID}
}

func TokenEndpoint(ctx context.Context, cfg Config, logger *slog.Logger) (string, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return "", ErrNotConfigured
	}
	idp, err := oidcauth.NewNativeOIDCClient(oidcauth.OIDCConfig{Issuer: cfg.Issuer, ClientID: cfg.ClientID}, logger)
	if err != nil {
		return "", fmt.Errorf("solace id: %w", err)
	}
	return idp.TokenEndpoint(ctx)
}
