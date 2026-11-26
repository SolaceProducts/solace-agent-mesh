// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
)

const (
	platformControlApp           = "platform"
	platformControlGetMethod     = "get"
	platformModelConfigsResource = "model-configs/"
)

func NewBindingsRegistry(brk broker.Broker, namespace string, logger *slog.Logger) *bindings.BindingsRegistry {
	opts := []bindings.Option{bindings.WithLogger(logger)}
	if brk != nil {
		fetcher := NewPlatformModelFetcher(brk, namespace, logger)
		grants := tokenbroker.NewControlPlaneTokens(brk, namespace)
		opts = append(opts,
			bindings.WithFetcher(fetcher),
			bindings.WithClientBuilder(bindings.NewClientBuilder(grants)),
			bindings.WithGrantTokens(grants),
		)
	}
	return bindings.New(opts...)
}

func NewPlatformModelFetcher(brk broker.Broker, namespace string, logger *slog.Logger) bindings.Fetcher {
	client := control.NewClient(brk, namespace)
	return func(ctx context.Context, alias string) (map[string]any, error) {
		raw, err := client.Custom(ctx, platformControlApp, platformControlGetMethod, platformModelConfigsResource+alias, nil)
		if err != nil {
			if ce, ok := err.(*control.ClientError); ok && ce.Code == control.ErrNotFound {
				logger.Debug("platform has no config for alias", "alias", alias)
				return nil, nil
			}
			return nil, fmt.Errorf("platform model-configs lookup: %w", err)
		}
		var cfg map[string]any
		if unmarshalErr := json.Unmarshal(raw, &cfg); unmarshalErr != nil {
			return nil, fmt.Errorf("decode platform model-configs response: %w", unmarshalErr)
		}
		return cfg, nil
	}
}
