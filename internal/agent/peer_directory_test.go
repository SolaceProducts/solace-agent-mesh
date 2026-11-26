// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
)

func newSharedDirectory() *discovery.Directory {
	return discovery.NewDirectory(nil, "ns", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newDirectoryAgent(t *testing.T, dir *discovery.Directory, cfg AgentConfig) *Agent {
	t.Helper()
	cfg.Namespace = "ns"
	agent := New(cfg)
	if err := agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())); err != nil {
		t.Fatalf("Init: %v", err)
	}
	agent.SetPeerDirectory(dir)
	return agent
}

func ingestCard(t *testing.T, dir *discovery.Directory, card a2a.AgentCard, props map[string]string) {
	t.Helper()
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	dir.Ingest(&broker.Message{Payload: data, UserProperties: props})
}

func TestSharedDirectory_EveryAgentSeesPeers(t *testing.T) {
	dir := newSharedDirectory()
	a := newDirectoryAgent(t, dir, AgentConfig{Name: "agent-a"})
	b := newDirectoryAgent(t, dir, AgentConfig{Name: "agent-b"})

	ingestCard(t, dir, a2a.AgentCard{Name: "agent-a"}, nil)
	ingestCard(t, dir, a2a.AgentCard{Name: "agent-b"}, nil)
	ingestCard(t, dir, a2a.AgentCard{Name: "peer-x"}, nil)

	if knownPeer(a, "peer-x") == nil || knownPeer(b, "peer-x") == nil {
		t.Fatal("a card the shared directory received should be a peer of every agent reading it")
	}
	if knownPeer(a, "agent-b") == nil || knownPeer(b, "agent-a") == nil {
		t.Fatal("agents sharing a directory should see each other")
	}
	if knownPeer(a, "agent-a") != nil {
		t.Fatal("an agent must not list itself as a peer")
	}
	if a.tools.GetTool("peer_peer-x") == nil || b.tools.GetTool("peer_peer-x") == nil {
		t.Fatal("each agent should register its own tool for the shared peer")
	}
}

func TestSharedDirectory_NewAgentSeesKnownPeersAtOnce(t *testing.T) {
	dir := newSharedDirectory()
	ingestCard(t, dir, a2a.AgentCard{Name: "peer-x"}, map[string]string{"instance_id": "A"})

	late := newDirectoryAgent(t, dir, AgentConfig{Name: "late-agent"})

	if knownPeer(late, "peer-x") == nil {
		t.Fatal("an agent added after the card arrived should see peer-x without another card")
	}
	if late.tools.GetTool("peer_peer-x") == nil {
		t.Fatal("the late agent should register a tool for peer-x")
	}
}

func setPeerClock(a *Agent, offset time.Duration) {
	a.peers.mu.Lock()
	defer a.peers.mu.Unlock()
	a.peers.now = func() time.Time { return time.Now().Add(offset) }
}

func TestSharedDirectory_EachAgentAppliesItsOwnTTL(t *testing.T) {
	dir := newSharedDirectory()
	short := newDirectoryAgent(t, dir, AgentConfig{Name: "short", HealthCheckTTLSeconds: 10, HealthCheckIntervalSeconds: 1})
	long := newDirectoryAgent(t, dir, AgentConfig{Name: "long", HealthCheckTTLSeconds: 300, HealthCheckIntervalSeconds: 1})
	ingestCard(t, dir, a2a.AgentCard{Name: "peer-x"}, nil)
	if knownPeer(short, "peer-x") == nil || knownPeer(long, "peer-x") == nil {
		t.Fatal("both agents should see peer-x while its card is fresh")
	}

	setPeerClock(short, 20*time.Second)
	setPeerClock(long, 20*time.Second)
	if knownPeer(short, "peer-x") != nil {
		t.Fatal("the agent with a 10s TTL kept a peer that stopped publishing 20s ago")
	}
	if knownPeer(long, "peer-x") == nil {
		t.Fatal("the agent with a 300s TTL dropped a peer last seen 20s ago")
	}
}

func TestSharedDirectory_ExpiredPeerReturnsOnItsNextCard(t *testing.T) {
	dir := newSharedDirectory()
	agent := newDirectoryAgent(t, dir, AgentConfig{Name: "self", HealthCheckTTLSeconds: 10, HealthCheckIntervalSeconds: 60})
	card := a2a.AgentCard{Name: "peer-x"}
	ingestCard(t, dir, card, map[string]string{"instance_id": "A"})

	setPeerClock(agent, 2*time.Minute)
	if knownPeer(agent, "peer-x") != nil {
		t.Fatal("peer-x should be hidden once its card is older than the TTL")
	}

	setPeerClock(agent, 0)
	ingestCard(t, dir, card, map[string]string{"instance_id": "A"})
	if knownPeer(agent, "peer-x") == nil {
		t.Fatal("peer-x published again but stayed hidden until the next recheck")
	}
	if agent.tools.GetTool("peer_peer-x") == nil {
		t.Fatal("the returning peer's tool should be registered again")
	}
}

func TestSharedDirectory_DifferingReplicaCardsDoNotRebuildTasks(t *testing.T) {
	dir := newSharedDirectory()
	agent := newDirectoryAgent(t, dir, AgentConfig{Name: "self"})
	healthy := a2a.AgentCard{Name: "peer-x", Description: "healthy"}
	degraded := a2a.AgentCard{Name: "peer-x", Description: "degraded"}
	ingestCard(t, dir, healthy, map[string]string{"instance_id": "A"})
	ingestCard(t, dir, degraded, map[string]string{"instance_id": "B"})
	v := agent.peerVersion()

	for range 5 {
		ingestCard(t, dir, healthy, map[string]string{"instance_id": "A"})
		ingestCard(t, dir, degraded, map[string]string{"instance_id": "B"})
	}
	if got := agent.peerVersion(); got != v {
		t.Fatalf("peerVersion moved from %d to %d while replicas repeated their own cards", v, got)
	}
}

func TestSharedDirectory_HeartbeatsDoNotRebuildTasks(t *testing.T) {
	dir := newSharedDirectory()
	agent := newDirectoryAgent(t, dir, AgentConfig{Name: "self"})
	card := a2a.AgentCard{Name: "peer-x", Description: "v1"}
	ingestCard(t, dir, card, map[string]string{"instance_id": "A"})
	v := agent.peerVersion()

	ingestCard(t, dir, card, map[string]string{"instance_id": "A"})
	ingestCard(t, dir, card, map[string]string{"instance_id": "B"})
	if got := agent.peerVersion(); got != v {
		t.Fatalf("peerVersion moved from %d to %d on identical heartbeats", v, got)
	}

	card.Description = "v2"
	ingestCard(t, dir, card, map[string]string{"instance_id": "B"})
	if agent.peerVersion() == v {
		t.Fatal("peerVersion should move when a peer's card changes")
	}
	if got := agent.tools.GetTool("peer_peer-x"); got == nil {
		t.Fatal("the peer tool should survive a card change")
	}
	if got := knownPeer(agent, "peer-x").Description; got != "v2" {
		t.Fatalf("peer card description = %q, want v2", got)
	}
}

func TestSharedDirectory_ReplicaStopKeepsPeer(t *testing.T) {
	dir := newSharedDirectory()
	agent := newDirectoryAgent(t, dir, AgentConfig{Name: "self"})
	card := a2a.AgentCard{Name: "peer-x"}
	ingestCard(t, dir, card, map[string]string{"instance_id": "B"})
	ingestCard(t, dir, card, map[string]string{"instance_id": "A"})

	ingestCard(t, dir, card, map[string]string{"instance_id": "A", "removed": "true"})
	if knownPeer(agent, "peer-x") == nil || agent.tools.GetTool("peer_peer-x") == nil {
		t.Fatal("replica A stopping removed peer-x although replica B still runs it")
	}

	ingestCard(t, dir, card, map[string]string{"instance_id": "B", "removed": "true"})
	if knownPeer(agent, "peer-x") != nil || agent.tools.GetTool("peer_peer-x") != nil {
		t.Fatal("peer-x should be gone once its last replica stopped")
	}
}

func TestSharedDirectory_DiscoveryDisabledAgentIgnoresIt(t *testing.T) {
	dir := newSharedDirectory()
	disabled := false
	agent := newDirectoryAgent(t, dir, AgentConfig{Name: "isolated", DiscoveryEnabled: &disabled})
	ingestCard(t, dir, a2a.AgentCard{Name: "peer-x"}, nil)

	if knownPeer(agent, "peer-x") != nil {
		t.Fatal("an agent with discovery disabled must not pick up peers from the process's shared directory")
	}
}
