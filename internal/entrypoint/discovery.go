// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type AgentRegistry struct {
	cards *discovery.Registry
}

func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{cards: discovery.NewRegistry()}
}

func (r *AgentRegistry) Version() uint64 {
	return r.cards.Version()
}

func (r *AgentRegistry) AddOrUpdate(card a2a.AgentCard) bool {
	return r.AddOrUpdateInstance(card, "")
}

func (r *AgentRegistry) AddOrUpdateInstance(card a2a.AgentCard, instanceID string) bool {
	return r.cards.Observe(card, instanceID) == discovery.Added
}

func (r *AgentRegistry) Remove(name string) {
	r.cards.Delete(name)
}

func (r *AgentRegistry) RemoveInstance(name, instanceID string) bool {
	return r.cards.Remove(name, instanceID).Gone
}

func (r *AgentRegistry) InstanceIDs(name string) []string {
	instances := r.cards.Instances(name)
	ids := make([]string, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	return ids
}

func (r *AgentRegistry) apply(m discovery.Message) discovery.Event {
	return r.cards.Apply(m)
}

func (r *AgentRegistry) Get(name string) *a2a.AgentCard {
	return r.cards.Get(name)
}

func (r *AgentRegistry) DisplayNameResolver() func(string) string {
	return func(slug string) string {
		if card := r.Get(slug); card != nil {
			return platpkg.DisplayName(card)
		}
		return ""
	}
}

func (r *AgentRegistry) HandlerTypeFromName(name string) string {
	if name == "" {
		return ""
	}
	if _, kind, ok := a2a.IDFromInstanceName(name); ok {
		if kind == a2a.KindWorkflow {
			return store.TaskHandlerTypeWorkflow
		}
		return store.TaskHandlerTypeAgent
	}
	if r == nil {
		return ""
	}

	card := r.cards.Lookup(name)
	if card == nil {
		return ""
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != agent.ExtensionURIAgentType {
			continue
		}
		if t, _ := ext.Params["type"].(string); t == "workflow" {
			return store.TaskHandlerTypeWorkflow
		}
	}
	return store.TaskHandlerTypeAgent
}

func (r *AgentRegistry) List() []a2a.AgentCard {
	return r.cards.List()
}

func (r *AgentRegistry) SupportsExtension(name, uri string) bool {
	card := r.cards.Lookup(name)
	if card == nil {
		return false
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == uri {
			return true
		}
	}
	return false
}

func (r *AgentRegistry) InternalNames() []string {
	var names []string
	r.cards.Each(func(card *a2a.AgentCard) {
		for _, ext := range card.Capabilities.Extensions {
			if ext.URI == a2a.ExtensionURIInternal {
				if v, ok := ext.Params["internal"].(bool); ok && v {
					if ss, ok := ext.Params["show_sessions"].(bool); ok && ss {
						break
					}
					names = append(names, card.Name)
					break
				}
			}
		}
	})
	return names
}

func MatchMentionedAgent(cards []a2a.AgentCard, text string) string {
	lower := strings.ToLower(text)
	for _, c := range cards {
		if mentionMatches(lower, c.Name) {
			return c.Name
		}
		if disp := platpkg.DisplayName(&c); disp != "" && disp != c.Name && mentionMatches(lower, disp) {
			return c.Name
		}
	}
	return ""
}

func mentionMatches(text, name string) bool {
	if name == "" {
		return false
	}
	needle := "@" + strings.ToLower(name)
	idx := 0
	for {
		off := strings.Index(text[idx:], needle)
		if off < 0 {
			return false
		}
		end := idx + off + len(needle)
		if end >= len(text) || !isMentionNameChar(text[end]) {
			return true
		}
		idx = end
	}
}

func isMentionNameChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == '.':
		return true
	}
	return false
}

type AgentResolution struct {
	Name          string
	Misconfigured bool
}

func (r *AgentRegistry) ResolveByDefaultName(defaultName string) AgentResolution {
	if defaultName != "" {
		if card := r.FindByConfiguredName(defaultName); card != nil {
			return AgentResolution{Name: card.Name}
		}
		return AgentResolution{Misconfigured: true}
	}
	for _, name := range r.Names() {
		if card := r.cards.Get(name); card != nil && !a2a.IsEphemeralCard(*card) {
			return AgentResolution{Name: name}
		}
	}
	return AgentResolution{}
}

func (r *AgentRegistry) FindByConfiguredName(name string) *a2a.AgentCard {
	if name == "" {
		return nil
	}
	if card := r.cards.Get(name); card != nil {
		return card
	}
	var match *a2a.AgentCard
	r.cards.Each(func(card *a2a.AgentCard) {
		if platpkg.DisplayName(card) != name || a2a.IsEphemeralCard(*card) {
			return
		}
		if match == nil || card.Name < match.Name {
			match = card
		}
	})
	if match == nil {
		return nil
	}
	found := *match
	return &found
}

func (r *AgentRegistry) Names() []string {
	return r.cards.Names()
}

func (r *AgentRegistry) Count() int {
	return r.cards.Count()
}

func (r *AgentRegistry) ExpireOlderThan(cutoff time.Time) []string {
	return r.cards.ExpireOlderThan(cutoff)
}

const gatewayCardVersion = "0.1.0"

type gatewayCardID struct {
	namespace  string
	gatewayID  string
	instanceID string
}

type cardState struct {
	healthProblems func() []health.Problem
	notifyCaps     func() *a2a.NotifyCapabilities
}

func publishGatewayCard(ctx context.Context, brk broker.Broker, id gatewayCardID, state cardState) error {
	card := a2a.AgentCard{
		Name:            id.gatewayID,
		URL:             fmt.Sprintf("solace:%s", a2a.GatewayResponseSubscription(id.namespace, id.gatewayID)),
		Version:         gatewayCardVersion,
		ProtocolVersion: "0.3.0",
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
			Extensions: []a2a.AgentExtension{
				{
					URI:         platpkg.ExtensionURIGatewayRole,
					Description: "Identifies this card as a gateway",
					Params: map[string]any{
						"gateway_type": "http_sse",
						"namespace":    id.namespace,
					},
				},
			},
		},
	}

	if problems := state.healthProblems(); len(problems) > 0 {
		card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.HealthProblemsExtension(problems))
	}
	if state.notifyCaps != nil {
		if caps := state.notifyCaps(); caps != nil {
			card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.NotifyCapabilitiesExtension(*caps))
		}
	}

	data, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal gateway card: %w", err)
	}

	topic := a2a.DiscoveryGatewayCardsTopic(id.namespace)
	if pubErr := brk.Publish(ctx, topic, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"instance_id": id.instanceID},
	}); pubErr != nil {
		return fmt.Errorf("publish gateway card: %w", pubErr)
	}

	return nil
}

func publishGatewayRemovalCard(ctx context.Context, brk broker.Broker, id gatewayCardID, logger *slog.Logger) {
	card := a2a.AgentCard{
		Name:            id.gatewayID,
		URL:             fmt.Sprintf("solace:%s", a2a.GatewayResponseSubscription(id.namespace, id.gatewayID)),
		Version:         gatewayCardVersion,
		ProtocolVersion: "0.3.0",
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
			Extensions: []a2a.AgentExtension{
				{
					URI:         platpkg.ExtensionURIGatewayRole,
					Description: "Identifies this card as a gateway",
					Params: map[string]any{
						"gateway_type": "http_sse",
						"namespace":    id.namespace,
					},
				},
			},
		},
	}

	data, err := json.Marshal(card)
	if err != nil {
		logger.Warn("failed to marshal gateway removal card", "error", err)
		return
	}

	topic := a2a.DiscoveryGatewayCardsTopic(id.namespace)
	if pubErr := brk.Publish(ctx, topic, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"removed": "true", "instance_id": id.instanceID},
	}); pubErr != nil {
		logger.Warn("failed to publish gateway removal card", "topic", topic, "error", pubErr)
	} else {
		logger.Info("published gateway removal card", "gatewayID", id.gatewayID)
	}
}

const cardPublishWarmupDuration = 3 * time.Minute
const cardPublishWarmupMaxInterval = 10 * time.Second

func startCardPublisher(ctx context.Context, brk broker.Broker, id gatewayCardID, intervalSeconds int, wg *sync.WaitGroup, logger *slog.Logger, state cardState) {
	if err := publishGatewayCard(ctx, brk, id, state); err != nil {
		logger.Warn("failed to publish initial gateway card", "error", err)
	} else {
		logger.Info("published gateway card", "gatewayID", id.gatewayID)
	}

	if intervalSeconds <= 0 {
		return
	}

	normalInterval := time.Duration(intervalSeconds) * time.Second
	warmupInterval := normalInterval
	if warmupInterval > cardPublishWarmupMaxInterval {
		warmupInterval = cardPublishWarmupMaxInterval
	}

	publishLoop := func() {
		warmupDeadline := time.Now().Add(cardPublishWarmupDuration)
		ticker := time.NewTicker(warmupInterval)
		defer ticker.Stop()
		inWarmup := warmupInterval != normalInterval
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := publishGatewayCard(ctx, brk, id, state); err != nil {
					logger.Warn("failed to publish gateway card", "error", err)
				}
				if inWarmup && time.Now().After(warmupDeadline) {
					ticker.Reset(normalInterval)
					inWarmup = false
				}
			}
		}
	}

	if wg != nil {
		wg.Go(publishLoop)
	} else {
		go publishLoop()
	}
}
