// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func (n *NotifyCore) PublishDeliver(ctx context.Context, channelType string, d a2a.NotifyDeliver) error {
	if !a2a.ValidNotifyChannelType(channelType) {
		return fmt.Errorf("invalid channel type %q for a notify deliver topic", channelType)
	}
	payload, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("marshal notify deliver: %w", err)
	}
	topic := a2a.NotifyDeliverTopic(n.cfg.Namespace, channelType, d.RequestID)
	if err := n.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		return fmt.Errorf("publish notify deliver to %s: %w", topic, err)
	}
	return nil
}

func (n *NotifyCore) SignalInboxCreated(ctx context.Context, created a2a.NotifyInboxCreated) error {
	payload, err := json.Marshal(created)
	if err != nil {
		return fmt.Errorf("marshal notify inbox created: %w", err)
	}
	topic := a2a.NotifyInboxCreatedTopic(n.cfg.Namespace)
	if err := n.svc.Broker().Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		return fmt.Errorf("publish notify inbox created to %s: %w", topic, err)
	}
	return nil
}

func (n *NotifyCore) ChannelsFor(ctx context.Context, userID string) ([]LearnedChannel, error) {
	rows, err := n.channels.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	learned := make([]LearnedChannel, 0, len(rows))
	for _, uc := range rows {
		if !gatewayDeliverable(uc.ChannelType) {
			continue
		}
		lc := LearnedChannel{ChannelType: uc.ChannelType}
		if uc.Confirmed() {
			lc.Address, lc.Status = uc.ConfirmedAddress, AddressConfirmed
		}
		learned = append(learned, lc)
	}
	slices.SortFunc(learned, func(a, b LearnedChannel) int { return strings.Compare(a.ChannelType, b.ChannelType) })
	return learned, nil
}
