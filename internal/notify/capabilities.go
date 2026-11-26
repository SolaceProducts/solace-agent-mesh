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
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type ChannelCapability struct {
	Advertised      bool
	Notifiable      bool
	Interactable    bool
	ResolvesAddress bool
	ApproverSets    bool
}

func (c ChannelCapability) DeclinesDelivery() bool { return c.Advertised && !c.Notifiable }

func (c ChannelCapability) DeclinesPrompts() bool {
	return c.DeclinesDelivery() || (c.Advertised && !c.Interactable)
}

type ChannelCapabilities interface {
	Capability(channelType string) ChannelCapability
}

const missedHeartbeatsBeforeExpiry = 5

const gatewayCapabilityTTL = missedHeartbeatsBeforeExpiry * time.Duration(config.DefaultCardPublishIntervalSecs) * time.Second

const gatewayCapabilitySweepInterval = time.Minute

const (
	cardPropInstanceID = "instance_id"
	cardPropRemoved    = "removed"
)

type gatewayInstanceKey struct {
	gatewayID  string
	instanceID string
}

type gatewayCapabilityEntry struct {
	caps     a2a.NotifyCapabilities
	lastSeen time.Time
}

type CapabilityChangeKind int

const (
	CapabilityUnchanged CapabilityChangeKind = iota
	CapabilityAdvertised
	CapabilityWithdrawn
)

type CapabilityChange struct {
	Kind         CapabilityChangeKind
	GatewayID    string
	InstanceID   string
	Capabilities a2a.NotifyCapabilities
}

type ExpiredCapability struct {
	GatewayID    string
	InstanceID   string
	Capabilities a2a.NotifyCapabilities
}

type GatewayCapabilityRegistry struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.RWMutex
	entries map[gatewayInstanceKey]gatewayCapabilityEntry
}

func NewGatewayCapabilityRegistry(ttl time.Duration) *GatewayCapabilityRegistry {
	return &GatewayCapabilityRegistry{ttl: ttl, now: time.Now, entries: make(map[gatewayInstanceKey]gatewayCapabilityEntry)}
}

func (r *GatewayCapabilityRegistry) Apply(msg *broker.Message) (CapabilityChange, error) {
	var card a2a.AgentCard
	if err := json.Unmarshal(msg.Payload, &card); err != nil {
		return CapabilityChange{}, fmt.Errorf("decode discovery card: %w", err)
	}
	if !a2a.IsGatewayCard(card) {
		return CapabilityChange{}, nil
	}
	instanceID := msg.UserProperties[cardPropInstanceID]
	if msg.UserProperties[cardPropRemoved] == "true" {
		return r.Remove(card.Name, instanceID), nil
	}
	return r.Observe(card, instanceID), nil
}

func (r *GatewayCapabilityRegistry) Observe(card a2a.AgentCard, instanceID string) CapabilityChange {
	if card.Name == "" || !a2a.IsGatewayCard(card) {
		return CapabilityChange{}
	}
	key := gatewayInstanceKey{gatewayID: card.Name, instanceID: instanceID}
	caps, ok := a2a.NotifyCapabilitiesFromCard(card)
	r.mu.Lock()
	defer r.mu.Unlock()
	prev, known := r.entries[key]
	change := CapabilityChange{GatewayID: card.Name, InstanceID: instanceID}
	if !ok {
		if !known {
			return change
		}
		delete(r.entries, key)
		change.Kind, change.Capabilities = CapabilityWithdrawn, prev.caps
		return change
	}
	r.entries[key] = gatewayCapabilityEntry{caps: caps, lastSeen: r.now()}
	change.Capabilities = caps
	if !known || prev.caps != caps {
		change.Kind = CapabilityAdvertised
	}
	return change
}

func (r *GatewayCapabilityRegistry) Remove(gatewayID, instanceID string) CapabilityChange {
	key := gatewayInstanceKey{gatewayID: gatewayID, instanceID: instanceID}
	r.mu.Lock()
	defer r.mu.Unlock()
	change := CapabilityChange{GatewayID: gatewayID, InstanceID: instanceID}
	if prev, known := r.entries[key]; known {
		delete(r.entries, key)
		change.Kind, change.Capabilities = CapabilityWithdrawn, prev.caps
	}
	return change
}

func (r *GatewayCapabilityRegistry) ExpireStale() []ExpiredCapability {
	cutoff := r.now().Add(-r.ttl)
	r.mu.Lock()
	defer r.mu.Unlock()
	var expired []ExpiredCapability
	for key, entry := range r.entries {
		if entry.lastSeen.Before(cutoff) {
			delete(r.entries, key)
			expired = append(expired, ExpiredCapability{GatewayID: key.gatewayID, InstanceID: key.instanceID, Capabilities: entry.caps})
		}
	}
	return expired
}

func (r *GatewayCapabilityRegistry) Capability(channelType string) ChannelCapability {
	var c ChannelCapability
	everyHandlesApproverSets := true
	cutoff := r.now().Add(-r.ttl)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if entry.caps.ChannelType != channelType || entry.lastSeen.Before(cutoff) {
			continue
		}
		c.Advertised = true
		c.Notifiable = c.Notifiable || entry.caps.Notifiable
		c.Interactable = c.Interactable || (entry.caps.Notifiable && entry.caps.Interactable)
		c.ResolvesAddress = c.ResolvesAddress || (entry.caps.Notifiable && entry.caps.ResolvesAddress)
		if entry.caps.Notifiable && !entry.caps.ApproverSets {
			everyHandlesApproverSets = false
		}
	}
	c.ApproverSets = c.Notifiable && everyHandlesApproverSets
	return c
}

func (r *GatewayCapabilityRegistry) NotifiableTypes() []string {
	cutoff := r.now().Add(-r.ttl)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var types []string
	for _, entry := range r.entries {
		caps := entry.caps
		if caps.Notifiable && !entry.lastSeen.Before(cutoff) && !slices.Contains(types, caps.ChannelType) {
			types = append(types, caps.ChannelType)
		}
	}
	slices.Sort(types)
	return types
}

func (n *NotifyCore) startCapabilityDiscovery(loopCtx context.Context) error {
	brk := n.svc.Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create capability discovery subscription: %w", err)
	}
	topic := a2a.DiscoveryGatewayCardsTopic(n.cfg.Namespace)
	if err := brk.Subscribe(q, topic); err != nil {
		_ = brk.DeleteQueue(q.Name())
		return fmt.Errorf("subscribe capability discovery %q: %w", topic, err)
	}
	n.wg.Go(func() {
		defer func() { _ = brk.DeleteQueue(q.Name()) }()
		n.capabilityDiscoveryLoop(loopCtx, q)
	})
	n.wg.Go(func() { n.capabilityExpiryLoop(loopCtx) })
	n.logger.Info("notify capability discovery started", "topic", topic)
	return nil
}

func (n *NotifyCore) capabilityDiscoveryLoop(ctx context.Context, q broker.Queue) {
	bo := backoff.Exponential{Initial: 100 * time.Millisecond, Max: 30 * time.Second, Jitter: 0.2}
	for {
		msg, err := n.svc.Broker().Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			n.logger.Warn("notify: capability discovery receive error; backing off", "error", err, "backoff", bo.Current())
			if bo.Wait(ctx) != nil {
				return
			}
			continue
		}
		bo.Reset()
		if msg == nil {
			continue
		}
		n.handleCapabilityCard(msg)
		msg.Ack()
	}
}

func (n *NotifyCore) handleCapabilityCard(msg *broker.Message) {
	change, err := n.capabilities.Apply(msg)
	if err != nil {
		n.logger.Debug("notify: unparseable gateway card; ignoring", "topic", msg.Topic, "error", err)
		return
	}
	attrs := []any{
		"gatewayID", change.GatewayID, "instanceID", change.InstanceID, "channelType", change.Capabilities.ChannelType,
		"notifiable", change.Capabilities.Notifiable, "interactable", change.Capabilities.Interactable,
		"resolvesAddress", change.Capabilities.ResolvesAddress,
	}
	switch change.Kind {
	case CapabilityAdvertised:
		n.logger.Info("notify: gateway capabilities advertised", attrs...)
	case CapabilityWithdrawn:
		n.logger.Info("notify: gateway capabilities withdrawn", attrs...)
	}
}

func (n *NotifyCore) capabilityExpiryLoop(ctx context.Context) {
	ticker := time.NewTicker(gatewayCapabilitySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, e := range n.capabilities.ExpireStale() {
				n.logger.Info("notify: gateway capabilities expired (TTL)",
					"gatewayID", e.GatewayID, "instanceID", e.InstanceID, "channelType", e.Capabilities.ChannelType)
			}
		}
	}
}
