// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"gopkg.in/yaml.v3"
)

type EventMeshHTTPGateway struct {
	emConfig   *EventMeshTestConfig
	emInput    *EventMeshInput
	namespace  string
	gatewayID  string
	brokerAddr string
	logger     *slog.Logger

	cmd        *exec.Cmd
	configPath string
	client     broker.Broker

	outputQueue    broker.Queue
	inputSubs      []string
	hasWildcardSub bool
	mu             sync.Mutex
	events         []CollectedEvent
	done           chan struct{}
}

var _ Gateway = (*EventMeshHTTPGateway)(nil)

func NewEventMeshHTTPGateway(
	emConfig *EventMeshTestConfig,
	emInput *EventMeshInput,
	namespace, gatewayID, brokerAddr string,
	logger *slog.Logger,
) *EventMeshHTTPGateway {
	return &EventMeshHTTPGateway{
		emConfig:   emConfig,
		emInput:    emInput,
		namespace:  namespace,
		gatewayID:  gatewayID,
		brokerAddr: brokerAddr,
		logger:     logger,
		done:       make(chan struct{}),
	}
}

func (g *EventMeshHTTPGateway) Setup(ctx context.Context) error {
	configPath, err := g.writeConfig()
	if err != nil {
		return fmt.Errorf("write em gateway config: %w", err)
	}
	g.configPath = configPath

	brokerHost, brokerPort, err := net.SplitHostPort(g.brokerAddr)
	if err != nil {
		return fmt.Errorf("parse broker addr %q: %w", g.brokerAddr, err)
	}

	binary := findGatewayBinary()
	cmd := exec.CommandContext(ctx, binary, "--config", configPath)

	var env []string
	for _, e := range os.Environ() {
		key := strings.SplitN(e, "=", 2)[0]
		if strings.HasPrefix(key, "SOLACE_BROKER_") || strings.HasPrefix(key, "DATAPLANE_") {
			continue
		}
		env = append(env, e)
	}
	env = append(env,
		"SAM_DEV_BROKER_HOST="+brokerHost,
		"SAM_DEV_BROKER_PORT="+brokerPort,
		"DEV_BROKER_HOST="+brokerHost,
		"DEV_BROKER_PORT="+brokerPort,
		"SOLACE_DEV_MODE=true",
	)
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	g.logger.Info("starting em gateway subprocess", "cmd", cmd.String())
	if startErr := cmd.Start(); startErr != nil {
		return fmt.Errorf("start em gateway subprocess: %w", startErr)
	}
	g.cmd = cmd

	readyCh := make(chan struct{}, 1)
	go g.scanOutput("stdout", stdout, readyCh)
	go g.scanOutput("stderr", stderr, readyCh)

	select {
	case <-readyCh:
		g.logger.Info("em gateway subprocess started")
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("timeout waiting for em gateway to start")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	}

	netBroker := broker.NewNetworkDevBroker(g.brokerAddr, "em-test-io", "em-test-io-q", g.logger)
	if connErr := netBroker.Connect(ctx); connErr != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("connect data plane broker: %w", connErr)
	}
	g.client = netBroker

	g.outputQueue, err = g.client.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create output queue: %w", err)
	}

	subscribed := make(map[string]bool)
	if g.emConfig != nil {
		for _, oh := range g.emConfig.OutputHandlers {
			topicExpr, _ := oh["topic_expression"].(string)
			sub := extractOutputTopicSubscription(topicExpr)
			if sub != "" && !subscribed[sub] {
				if sub == ">" {
					g.hasWildcardSub = true
				}
				if err := g.client.Subscribe(g.outputQueue, sub); err != nil {
					return fmt.Errorf("subscribe output topic %q: %w", sub, err)
				}
				subscribed[sub] = true
			}
		}
	}
	if len(subscribed) == 0 {
		g.hasWildcardSub = true
		if err := g.client.Subscribe(g.outputQueue, ">"); err != nil {
			return fmt.Errorf("subscribe wildcard: %w", err)
		}
	}

	if g.hasWildcardSub {
		g.inputSubs = collectInputSubscriptions(g.emConfig)
	}

	return nil
}

func (g *EventMeshHTTPGateway) SendRequest(ctx context.Context, _ string, _ *GatewayInput) (string, error) {
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

	if err := g.client.Publish(ctx, g.emInput.Topic, msg); err != nil {
		return "", fmt.Errorf("publish to data plane: %w", err)
	}

	return "", nil
}

func (g *EventMeshHTTPGateway) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
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

func (g *EventMeshHTTPGateway) receiveLoop(ctx context.Context) {
	for {
		msg, err := g.client.Receive(ctx, g.outputQueue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			g.logger.Warn("em http output receive error", "error", err)
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

func (g *EventMeshHTTPGateway) SendCancel(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("cancel not supported for event mesh HTTP gateway")
}

func (g *EventMeshHTTPGateway) Cleanup() {
	if g.outputQueue != nil && g.client != nil {
		_ = g.client.DeleteQueue(g.outputQueue.Name())
	}
	if g.client != nil {
		_ = g.client.Disconnect(context.Background())
	}
	if g.cmd != nil && g.cmd.Process != nil {
		_ = g.cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- g.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = g.cmd.Process.Kill()
			<-done
		}
	}
	if g.configPath != "" {
		_ = os.Remove(g.configPath)
	}
}

func (g *EventMeshHTTPGateway) writeConfig() (string, error) {
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

	brokerHost, brokerPort, _ := net.SplitHostPort(g.brokerAddr)
	appConfig["event_mesh_broker_config"] = map[string]any{
		"broker_url":      fmt.Sprintf("tcp://%s:%s", brokerHost, brokerPort),
		"broker_vpn":      "default",
		"broker_username": "test",
		"broker_password": "test",
	}

	appEntry := map[string]any{
		"name":       "em_gateway",
		"app_exec":   findGatewayBinary(),
		"app_config": appConfig,
	}

	fullConfig := map[string]any{
		"apps": []any{appEntry},
	}

	data, err := yaml.Marshal(fullConfig)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	tmpFile, err := os.CreateTemp(os.TempDir(), "sam-em-gateway-test-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	_ = tmpFile.Close()

	g.logger.Debug("wrote em gateway config", "path", tmpFile.Name(),
		"config", string(data))

	return tmpFile.Name(), nil
}

func (g *EventMeshHTTPGateway) scanOutput(name string, r io.Reader, readyCh chan<- struct{}) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	sent := false

	for scanner.Scan() {
		line := scanner.Text()
		g.logger.Debug("em-gateway output", "name", name, "line", line)

		if !sent {
			if strings.Contains(line, "event mesh adapter started") ||
				strings.Contains(line, "gateway started") {
				select {
				case readyCh <- struct{}{}:
					sent = true
				default:
				}
			}
		}
	}
}
