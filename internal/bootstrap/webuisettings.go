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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

const webuiSettingsControlTimeout = 3 * time.Second

func NewWebuiSettingsRegistry(brk broker.Broker, namespace string, logger *slog.Logger) *webuisettings.Registry {
	if brk == nil {
		return webuisettings.NewRegistry(nil, logger)
	}
	return webuisettings.NewRegistry(NewPlatformWebuiSettingsFetcher(brk, namespace, logger), logger)
}

func NewPlatformWebuiSettingsFetcher(brk broker.Broker, namespace string, logger *slog.Logger) webuisettings.Fetcher {
	client := control.NewClient(brk, namespace, control.WithTimeout(webuiSettingsControlTimeout))
	return func(ctx context.Context) (*webuisettings.Settings, error) {
		raw, err := client.Custom(ctx, platformControlApp, platformControlGetMethod, "webui-settings", nil)
		if err != nil {
			if ce, ok := err.(*control.ClientError); ok && ce.Code == control.ErrNotFound {
				logger.Debug("platform has no webui settings overrides")
				return nil, nil
			}
			return nil, fmt.Errorf("platform webui-settings lookup: %w", err)
		}
		var settings webuisettings.Settings
		if unmarshalErr := json.Unmarshal(raw, &settings); unmarshalErr != nil {
			return nil, fmt.Errorf("decode platform webui-settings response: %w", unmarshalErr)
		}
		return &settings, nil
	}
}
