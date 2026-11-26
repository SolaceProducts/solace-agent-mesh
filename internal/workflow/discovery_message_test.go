// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
)

func newDiscoveryTestInstance(namespace string) (*WorkflowInstance, *bytes.Buffer) {
	var buf bytes.Buffer
	w := &WorkflowInstance{
		cfg:    &WorkflowConfig{Name: "workflow_019e0000-0000-7000-8000-0000000000ff", Namespace: namespace},
		logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	return w, &buf
}

func TestCardDirectory_UndecodableCardIsLogged(t *testing.T) {
	t.Parallel()
	const ns = "test-ns"
	const brokerName = "workflow_019f910e-7e42-737a-87eb-c300c9672a4e"
	w, logs := newDiscoveryTestInstance(ns)

	w.cardDirectory().Ingest(&broker.Message{
		Topic:   a2a.DiscoveryAgentCardsTopic(ns),
		Payload: []byte(`{"name":"` + brokerName + `","capabilities":"broken"}`),
	})

	if w.hasDiscoveredCards() {
		t.Errorf("card %q was cached; an undecodable card must not be", brokerName)
	}
	out := logs.String()
	if out == "" {
		t.Fatal("an undecodable agent card was discarded silently; the operator has no way to connect a later unresolved-target failure to this card")
	}
	if !strings.Contains(out, `"level":"WARN"`) {
		t.Errorf("want a WARN for a malformed card on the cards topic, got:\n%s", out)
	}
}

func TestCardDirectory_ValidCardIsCachedQuietly(t *testing.T) {
	t.Parallel()
	const ns = "test-ns"
	const brokerName = "workflow_019f910e-7e42-737a-87eb-c300c9672a4e"
	w, logs := newDiscoveryTestInstance(ns)

	payload, err := json.Marshal(a2a.AgentCard{Name: brokerName})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	w.cardDirectory().Ingest(&broker.Message{Topic: a2a.DiscoveryAgentCardsTopic(ns), Payload: payload})

	if w.peerCard(brokerName) == nil {
		t.Errorf("valid card was not cached under %q", brokerName)
	}
	if out := logs.String(); strings.Contains(out, `"level":"WARN"`) {
		t.Errorf("a valid card must not log a warning; got:\n%s", out)
	}
}

func TestCardDirectory_ReplicaRemovalKeepsTarget(t *testing.T) {
	t.Parallel()
	const ns = "test-ns"
	const brokerName = "workflow_019f910e-7e42-737a-87eb-c300c9672a4e"
	w, _ := newDiscoveryTestInstance(ns)

	payload, err := json.Marshal(a2a.AgentCard{Name: brokerName})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	deliver := func(props map[string]string) {
		w.cardDirectory().Ingest(&broker.Message{Topic: a2a.DiscoveryAgentCardsTopic(ns), Payload: payload, UserProperties: props})
	}
	deliver(map[string]string{"instance_id": "B"})
	deliver(map[string]string{"instance_id": "A"})
	deliver(map[string]string{"instance_id": "A", "removed": "true"})

	if w.peerCard(brokerName) == nil {
		t.Fatal("replica A's removal card dropped the target although replica B still publishes it")
	}

	deliver(map[string]string{"instance_id": "B", "removed": "true"})
	if w.peerCard(brokerName) != nil {
		t.Fatal("the target should be gone after its last replica's removal")
	}
}

func TestCardDirectory_RestartedWorkflowIgnoresCardsFromBefore(t *testing.T) {
	t.Parallel()
	const ns = "test-ns"
	const stale = "agent_019f910e-7e42-737a-87eb-c300c96700aa"
	shared := discovery.NewDirectory(nil, ns, slog.New(slog.DiscardHandler))

	payload, err := json.Marshal(a2a.AgentCard{Name: stale})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	deliver := func() {
		shared.Ingest(&broker.Message{Topic: a2a.DiscoveryAgentCardsTopic(ns), Payload: payload})
	}
	deliver()
	time.Sleep(10 * time.Millisecond)

	w := NewWorkflowInstance(&WorkflowConfig{
		Name:      "restarted-workflow",
		Namespace: ns,
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: stale}},
	})
	if err := w.Init(context.Background(), nil, &stubServices{brk: &publishingBroker{}}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	w.SetPeerDirectory(shared)
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if shared.Registry().Lookup(stale) == nil {
		t.Fatal("the shared directory should still hold the card for its other readers")
	}
	if w.peerCard(stale) != nil || w.hasDiscoveredCards() {
		t.Fatal("a card last published before the workflow started should not be visible to it")
	}

	deliver()
	if w.peerCard(stale) == nil {
		t.Fatal("a card republished after the workflow started should be visible")
	}
}
