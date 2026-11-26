// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/connector"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const ConnectorTypeName = "email"

func init() {
	connector.DefaultRegistry().MustRegister(ConnectorTypeName, Factory)
}

func Factory(_ context.Context, cfg connector.Config, _ runtime.Services) (connector.Connector, error) {
	connCfg, err := decodeConnectorConfig(cfg.Config)
	if err != nil {
		return nil, fmt.Errorf("decode email config: %w", err)
	}
	connCfg.Name = cfg.Name
	if connCfg.Provider == "" && cfg.Subtype != "" {
		connCfg.Provider = cfg.Subtype
	}

	inner, err := NewConnector(connCfg, slog.Default())
	if err != nil {
		return nil, err
	}
	return &connectorAdapter{inner: inner}, nil
}

func decodeConnectorConfig(raw map[string]any) (ConnectorConfig, error) {
	var cfg ConnectorConfig
	if len(raw) == 0 {
		return cfg, nil
	}
	buf, err := yaml.Marshal(raw)
	if err != nil {
		return cfg, fmt.Errorf("marshal raw config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(buf))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("unmarshal into ConnectorConfig: %w", err)
	}
	return cfg, nil
}

type connectorAdapter struct {
	inner *Connector
}

func (a *connectorAdapter) Name() string { return a.inner.Name() }

func (a *connectorAdapter) Type() string { return ConnectorTypeName }

func (a *connectorAdapter) Tool() tool.BuiltinTool { return a.inner.Tool() }

func (a *connectorAdapter) Start(_ context.Context) error { return nil }

func (a *connectorAdapter) Stop(_ context.Context) error {
	return a.inner.Close()
}

func (a *connectorAdapter) Health() error { return nil }
