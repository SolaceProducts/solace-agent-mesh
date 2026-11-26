// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/email"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
)

var connectIMAP = func(ctx context.Context, cfg *email.EmailConfig, logger *slog.Logger) error {
	var provider email.EmailProvider
	if cfg.UsesBasicAuth() {
		provider = email.NewImapProviderBasicAuth(cfg, logger)
	} else {
		provider = email.NewImapProviderOAuth2(cfg, logger)
	}
	if err := provider.Connect(ctx); err != nil {
		return err
	}
	_ = provider.Disconnect(ctx)
	return nil
}

var testBrokerConnection = defaultTestBrokerConnection

var probeWhatsAppCredentials = whatsapp.ProbeCredentials

var probeGoogleChat = googlechat.Probe

type TestConnectionRequest struct {
	GatewayType string `json:"type"`
	Values      any    `json:"values"`
	GatewayID   string `json:"gatewayId" validate:"omitempty,uuid"`
}

type TestConnectionResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
	Stage      string `json:"stage,omitempty"`
}
