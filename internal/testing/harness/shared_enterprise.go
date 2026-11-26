// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type SharedEnterpriseInfra struct {
	BrokerServer *broker.DevBrokerServer
	BrokerAddr   string
	GatewayURL   string
	Logger       *slog.Logger

	GatewayCards []*trust.TrustCard

	devBroker *broker.DevBroker
	gateway   *HTTPGateway
}

func NewSharedEnterpriseInfra(logger *slog.Logger) *SharedEnterpriseInfra {
	return &SharedEnterpriseInfra{
		Logger: logger,
	}
}

func (s *SharedEnterpriseInfra) Start(ctx context.Context) error {
	s.devBroker = broker.NewDevBroker(broker.WithDevBrokerLogger(s.Logger))
	if err := s.devBroker.Connect(ctx); err != nil {
		return fmt.Errorf("connect dev broker: %w", err)
	}

	s.BrokerServer = broker.NewDevBrokerServer(s.devBroker, s.Logger)
	addr, err := s.BrokerServer.Start("127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start broker server: %w", err)
	}
	s.BrokerAddr = addr
	s.Logger.Info("shared enterprise broker started", "addr", addr)

	s.gateway = NewHTTPGateway("python", "test-ns", addr, true, s.Logger)
	if err := s.gateway.Setup(context.Background()); err != nil {
		return fmt.Errorf("start enterprise gateway: %w", err)
	}
	s.GatewayURL = s.gateway.baseURL
	s.Logger.Info("shared enterprise gateway started", "url", s.GatewayURL)

	verifyBroker := broker.NewNetworkDevBroker(addr, "verify-trust", "verify-trust-q", s.Logger)
	if err := verifyBroker.Connect(ctx); err != nil {
		return fmt.Errorf("connect verify broker: %w", err)
	}

	trustCfg := trust.DefaultConfig()
	trustCfg.Enabled = true
	trustCfg.CardPublishIntervalSeconds = 5
	trustCfg.StartupWaitTimeoutSeconds = 30
	trustCfg.MinRequiredGateways = 1
	trustCfg.FailOnTimeout = true

	verifyTM := trust.NewManager(trustCfg, "agent", "verify-agent", "test-ns", verifyBroker, s.Logger)
	if err := verifyTM.Init(); err != nil {
		_ = verifyBroker.Disconnect(ctx)
		return fmt.Errorf("verify trust manager init: %w", err)
	}
	if err := verifyTM.StartSubscriber(ctx); err != nil {
		_ = verifyBroker.Disconnect(ctx)
		return fmt.Errorf("verify trust manager start: %w", err)
	}
	if err := verifyTM.WaitForGateways(ctx); err != nil {
		_ = verifyTM.Stop(ctx)
		_ = verifyBroker.Disconnect(ctx)
		return fmt.Errorf("verify trust manager gateway wait: %w", err)
	}

	s.GatewayCards = verifyTM.GatewayCards()
	_ = verifyTM.Stop(ctx)
	_ = verifyBroker.Disconnect(ctx)

	s.Logger.Info("shared enterprise infrastructure verified",
		"gatewayCards", len(s.GatewayCards))

	return nil
}

func (s *SharedEnterpriseInfra) Stop() {
	if s.gateway != nil {
		s.gateway.Cleanup()
	}
	if s.BrokerServer != nil {
		_ = s.BrokerServer.Stop()
	}
	if s.devBroker != nil {
		_ = s.devBroker.Disconnect(context.Background())
	}
}
