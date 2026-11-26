// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/connector"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const connectorStopTimeout = 5 * time.Second

func extractConnectorsRaw(appCfg config.Config) []any {
	if appCfg == nil {
		return nil
	}
	raw := appCfg.GetRaw("connectors")
	if raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	return list
}

func LoadAndRegisterConnectors(ctx context.Context, ag *agent.Agent, appCfg config.Config, svc runtime.Services) error {
	raw := extractConnectorsRaw(appCfg)
	if len(raw) == 0 {
		return nil
	}
	logger := slog.Default().With("agentName", ag.Name(), "logger_name", "connector-loader")

	for i, item := range raw {
		entryMap, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("connectors[%d]: entry must be a map, got %T", i, item)
		}
		cfg, err := connector.ParseEntry(entryMap)
		if err != nil {
			return fmt.Errorf("connectors[%d]: %w", i, err)
		}

		conn, err := connector.DefaultRegistry().Build(ctx, cfg, svc)
		if err != nil {
			return fmt.Errorf("connectors[%d]: %w", i, err)
		}

		if err := ag.Tools().Register(conn.Tool()); err != nil {
			return fmt.Errorf("connectors[%d] (%s): register tool %q: %w",
				i, cfg.Name, conn.Tool().Name(), err)
		}

		ag.RecordToolProvenance(conn.Tool().Name(), agent.ToolProvenance{
			Category:          taxonomy.ToolCategoryConnector,
			ConnectorCategory: cfg.Type,
		})

		ag.AddSubComponent(connectorStartable{conn: conn, logger: logger.With("connector", cfg.Name)})

		logger.Info("registered connector",
			"name", cfg.Name,
			"type", cfg.Type,
			"tool", conn.Tool().Name(),
		)
	}
	return nil
}

type connectorStartable struct {
	conn   connector.Connector
	logger *slog.Logger
}

func (c connectorStartable) Start(ctx context.Context) error {
	return c.conn.Start(ctx)
}

func (c connectorStartable) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), connectorStopTimeout)
	defer cancel()
	if err := c.conn.Stop(ctx); err != nil {
		c.logger.Warn("connector Stop returned error", "error", err)
	}
}
