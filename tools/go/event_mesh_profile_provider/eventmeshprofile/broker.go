// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmeshprofile

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var newBroker = func(_ context.Context, cfg *Config) (broker.Broker, error) {
	b := broker.NewSolaceBroker(broker.SolaceBrokerConfig{
		BrokerURL:     cfg.BrokerURL,
		VPN:           cfg.BrokerVPN,
		Username:      cfg.BrokerUsername,
		Password:      cfg.BrokerPassword,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}, slog.Default())
	return b, nil
}
