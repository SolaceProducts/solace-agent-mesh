// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

func (g *GatewayCore) PublishNotifyPresence(ctx context.Context, channelType, userID, email string, address any) {
	if userID == "" || channelType == "" || address == nil {
		return
	}
	if g.svc == nil {
		return
	}
	reg := g.svc.FeatureRegistry()
	if reg == nil || !reg.IsEnabled(features.KeyNotifyService) {
		return
	}
	brk := g.svc.Broker()
	if brk == nil {
		return
	}

	addrJSON, err := json.Marshal(address)
	if err != nil {
		g.logger.Warn("notify presence: marshal address failed; skipping", "channelType", channelType, "error", err)
		return
	}
	payload, err := json.Marshal(a2a.NotifyPresence{
		UserID:      userID,
		ChannelType: channelType,
		Address:     addrJSON,
		Email:       email,
		GatewayID:   g.GatewayID(),
	})
	if err != nil {
		g.logger.Warn("notify presence: marshal event failed; skipping", "channelType", channelType, "error", err)
		return
	}
	topic := a2a.NotifyPresenceTopic(g.Namespace(), userID)
	if err := brk.PublishGuaranteed(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		g.logger.Warn("notify presence: publish failed; will retry on next inbound message",
			"channelType", channelType, "userFP", strutil.PIIFingerprint(userID), "errorType", fmt.Sprintf("%T", err))
	}
}
