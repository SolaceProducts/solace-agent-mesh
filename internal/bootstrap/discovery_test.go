// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestPeerDirectories_OnePerNamespaceUntilClosed(t *testing.T) {
	svc := mountTestServices(t)
	dirs := newPeerDirectories(svc)

	first := dirs.forNamespace("ns1")
	if dirs.forNamespace("ns1") != first {
		t.Fatal("two components of one namespace got different directories")
	}
	if dirs.forNamespace("ns2") == first {
		t.Fatal("two namespaces share a directory")
	}

	if err := first.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	payload, err := json.Marshal(a2a.AgentCard{Name: "peer-x"})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	if err := svc.Broker().Publish(context.Background(), a2a.DiscoveryAgentCardsTopic("ns1"), &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish card: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for first.Registry().Lookup("peer-x") == nil {
		if time.Now().After(deadline) {
			t.Fatal("the shared directory never received a card published on its namespace")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := dirs.close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(first.HealthSnapshot()) != 0 {
		t.Fatalf("closed directory reports %v", first.HealthSnapshot())
	}
	if dirs.forNamespace("ns1") != first {
		t.Fatal("the directory a stopped component holds was dropped, so the next stop would never close it")
	}
	if err := first.Start(); err != nil {
		t.Fatalf("Start after the executor stopped: %v", err)
	}
	if err := dirs.close(context.Background()); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

type discoverySubscriptionCounter struct {
	*broker.DevBroker
	topic string
	mu    sync.Mutex
	count int
}

func (b *discoverySubscriptionCounter) Subscribe(q broker.Queue, topic string) error {
	if topic == b.topic {
		b.mu.Lock()
		b.count++
		b.mu.Unlock()
	}
	return b.DevBroker.Subscribe(q, topic)
}

func (b *discoverySubscriptionCounter) subscriptions() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

func TestNewAWEExecutor_AgentsAndWorkflowsShareOneDiscoverySubscription(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	brk := &discoverySubscriptionCounter{DevBroker: dev, topic: a2a.DiscoveryAgentCardsTopic("ns")}
	svc := runtime.NewRuntime(brk, config.FromMap(nil),
		runtime.WithDefaultLLM(usageLLM{}), runtime.WithSessions(session.NewMemoryStore())).Services()

	exe := NewAWEExecutor(svc)
	for _, name := range []string{"agent-a", "agent-b"} {
		if err := exe.LoadConfig(config.FromMap(map[string]any{
			"kind": "agent", "name": name,
			config.KeyAppConfig: map[string]any{
				"agent_name": name, "namespace": "ns", "model": "test-model", "instruction": "answer",
			},
		})); err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
	}
	if err := exe.LoadConfig(config.FromMap(map[string]any{
		"kind": "workflow", "name": "shared-workflow",
		config.KeyAppConfig: map[string]any{
			"name": "shared-workflow", "namespace": "ns",
			"workflow": map[string]any{
				"output_mapping": map[string]any{"result": "{{call.output}}"},
				"nodes":          []any{map[string]any{"id": "call", "type": "agent", "agent_name": "agent-a"}},
			},
		},
	})); err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })

	if got := brk.subscriptions(); got != 1 {
		t.Fatalf("two agents and a workflow made %d agent-cards subscriptions, want 1 shared", got)
	}
}

func TestPeerDirectories_NilLeavesComponentsTheirOwn(t *testing.T) {
	var dirs *peerDirectories
	if dirs.forNamespace("ns1") != nil {
		t.Fatal("a nil peerDirectories should hand out no directory")
	}
}
