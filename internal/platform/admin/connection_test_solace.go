// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package platform

import (
	"context"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func defaultTestBrokerConnection(ctx context.Context, brokerURL, vpn, username, password string, tlsSkipVerify bool) error {
	cfg := broker.SolaceBrokerConfig{
		BrokerURL:        brokerURL,
		VPN:              vpn,
		Username:         username,
		Password:         password,
		TLSSkipVerify:    tlsSkipVerify,
		ReconnectRetries: 0,
		ReconnectDelay:   time.Second,
	}
	brk := broker.NewSolaceBroker(cfg, slog.Default())
	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := brk.Connect(timeoutCtx); err != nil {
		return err
	}
	_ = brk.Disconnect(ctx)
	return nil
}
