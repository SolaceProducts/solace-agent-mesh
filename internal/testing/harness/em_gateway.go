// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type EventMeshTestGateway struct {
	brk       broker.Broker
	emConfig  *EventMeshTestConfig
	emInput   *EventMeshInput
	namespace string
	gatewayID string
	logger    *slog.Logger

	adapter    *eventmesh.EventMeshAdapter
	core       *entrypoint.GatewayCore
	svc        runtime.Services
	budgetGate *budget.Gate

	outputQueue broker.Queue

	inputSubs      []string
	hasWildcardSub bool

	mu     sync.Mutex
	events []CollectedEvent
	done   chan struct{}
}

func NewEventMeshTestGateway(
	brk broker.Broker,
	emConfig *EventMeshTestConfig,
	emInput *EventMeshInput,
	namespace, gatewayID string,
	logger *slog.Logger,
) *EventMeshTestGateway {
	return &EventMeshTestGateway{
		brk:       brk,
		emConfig:  emConfig,
		emInput:   emInput,
		namespace: namespace,
		gatewayID: gatewayID,
		logger:    logger,
		done:      make(chan struct{}),
	}
}

func (g *EventMeshTestGateway) SetBudgetGate(gate *budget.Gate) {
	g.budgetGate = gate
}

func (g *EventMeshTestGateway) SetServices(svc runtime.Services) {
	g.svc = svc
}

func (g *EventMeshTestGateway) Setup(ctx context.Context) error {
	appConfig := map[string]any{
		"namespace":             g.namespace,
		"gateway_id":            g.gatewayID,
		"default_user_identity": "test-user",
	}

	if g.emConfig != nil {
		if len(g.emConfig.EventHandlers) > 0 {
			handlers := make([]any, len(g.emConfig.EventHandlers))
			for i, h := range g.emConfig.EventHandlers {
				handlers[i] = h
			}
			appConfig["event_handlers"] = handlers
		}
		if len(g.emConfig.OutputHandlers) > 0 {
			handlers := make([]any, len(g.emConfig.OutputHandlers))
			for i, h := range g.emConfig.OutputHandlers {
				handlers[i] = h
			}
			appConfig["output_handlers"] = handlers
		}
		if g.emConfig.AckPolicy != nil {
			appConfig["acknowledgment_policy"] = g.emConfig.AckPolicy
		}
	}

	appConfig["event_mesh_broker_config"] = map[string]any{}

	g.adapter = eventmesh.NewEventMeshAdapter()
	g.adapter.SetDataBroker(g.brk)

	g.core = entrypoint.NewGatewayCore(g.adapter, appConfig)
	g.core.SetServices(budgettest.WithGate(g.svc, g.budgetGate))

	if err := g.core.Init(ctx, nil); err != nil {
		return fmt.Errorf("em gateway init: %w", err)
	}

	var err error
	g.outputQueue, err = g.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create output queue: %w", err)
	}

	subscribed := make(map[string]bool)
	for _, oh := range g.emConfig.OutputHandlers {
		topicExpr, _ := oh["topic_expression"].(string)
		sub := extractOutputTopicSubscription(topicExpr)
		if sub != "" && !subscribed[sub] {
			if sub == ">" {
				g.hasWildcardSub = true
			}
			if err := g.brk.Subscribe(g.outputQueue, sub); err != nil {
				return fmt.Errorf("subscribe output topic %q: %w", sub, err)
			}
			subscribed[sub] = true
		}
	}
	if len(subscribed) == 0 {
		g.hasWildcardSub = true
		if err := g.brk.Subscribe(g.outputQueue, ">"); err != nil {
			return fmt.Errorf("subscribe wildcard: %w", err)
		}
	}

	if g.hasWildcardSub {
		g.inputSubs = collectInputSubscriptions(g.emConfig)
	}

	if err := g.core.Start(ctx); err != nil {
		return fmt.Errorf("em gateway start: %w", err)
	}

	return nil
}

func (g *EventMeshTestGateway) SendRequest(ctx context.Context, _ string, _ *GatewayInput) (string, error) {
	if g.emInput == nil {
		return "", fmt.Errorf("no event_mesh_input configured")
	}

	var payload []byte
	switch v := g.emInput.Payload.(type) {
	case string:
		payload = []byte(v)
	default:
		var err error
		payload, err = json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal event mesh payload: %w", err)
		}
	}

	msg := &broker.Message{
		Payload:        payload,
		UserProperties: g.emInput.UserProperties,
	}

	if err := g.brk.Publish(ctx, g.emInput.Topic, msg); err != nil {
		return "", fmt.Errorf("publish to data plane: %w", err)
	}

	return "", nil
}

func (g *EventMeshTestGateway) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
	collectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var wg sync.WaitGroup
	wg.Go(func() {
		g.receiveLoop(collectCtx)
	})

	select {
	case <-g.done:
		time.Sleep(100 * time.Millisecond)
		cancel()
	case <-collectCtx.Done():
	}

	wg.Wait()

	g.mu.Lock()
	defer g.mu.Unlock()
	events := make([]CollectedEvent, len(g.events))
	copy(events, g.events)
	return events, nil
}

func (g *EventMeshTestGateway) receiveLoop(ctx context.Context) {
	for {
		msg, err := g.brk.Receive(ctx, g.outputQueue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			g.logger.Warn("em output receive error", "error", err)
			continue
		}
		if msg == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		if isControlPlaneTopic(msg.Topic, g.namespace) {
			msg.Ack()
			continue
		}

		if g.hasWildcardSub && isInputTopic(msg.Topic, g.inputSubs) {
			msg.Ack()
			continue
		}

		evt := CollectedEvent{
			Type:      "response",
			Topic:     msg.Topic,
			Payload:   json.RawMessage(msg.Payload),
			Timestamp: time.Now(),
		}

		g.mu.Lock()
		g.events = append(g.events, evt)
		g.mu.Unlock()

		msg.Ack()

		select {
		case <-g.done:
		default:
			close(g.done)
		}
	}
}

func (g *EventMeshTestGateway) SendCancel(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("cancel not supported for event mesh gateway")
}

func (g *EventMeshTestGateway) Cleanup() {
	ctx := context.Background()
	if g.core != nil {
		_ = g.core.Stop(ctx)
	}
	if g.outputQueue != nil {
		_ = g.brk.DeleteQueue(g.outputQueue.Name())
	}
}

func extractOutputTopicSubscription(topicExpr string) string {
	if len(topicExpr) > 7 && topicExpr[:7] == "static:" {
		return topicExpr[7:]
	}
	if topicExpr != "" {
		return ">"
	}
	return ""
}

func isControlPlaneTopic(topic, namespace string) bool {
	prefix := namespace + "/"
	if len(topic) >= len(prefix) && topic[:len(prefix)] == prefix {
		return true
	}
	return false
}

func collectInputSubscriptions(emConfig *EventMeshTestConfig) []string {
	if emConfig == nil {
		return nil
	}
	var subs []string
	for _, eh := range emConfig.EventHandlers {
		rawSubs, ok := eh["subscriptions"]
		if !ok {
			continue
		}
		subList, ok := rawSubs.([]any)
		if !ok {
			continue
		}
		for _, s := range subList {
			subMap, ok := s.(map[string]any)
			if !ok {
				continue
			}
			if topic, ok := subMap["topic"].(string); ok && topic != "" {
				subs = append(subs, topic)
			}
		}
	}
	return subs
}

func topicMatchesSubscription(topic, pattern string) bool {
	topicParts := strings.Split(topic, "/")
	patternParts := strings.Split(pattern, "/")

	for i, pp := range patternParts {
		if pp == ">" {
			return i < len(topicParts)
		}
		if i >= len(topicParts) {
			return false
		}
		if pp == "*" {
			continue
		}
		if pp != topicParts[i] {
			return false
		}
	}
	return len(topicParts) == len(patternParts)
}

func isInputTopic(topic string, inputSubs []string) bool {
	for _, sub := range inputSubs {
		if topicMatchesSubscription(topic, sub) {
			return true
		}
	}
	return false
}

var _ Gateway = (*EventMeshTestGateway)(nil)
