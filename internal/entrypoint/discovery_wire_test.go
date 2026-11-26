// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
)

func TestHandleDiscovery_IgnoresStaleInstanceRemoval(t *testing.T) {
	core, _ := newAgentLossCore(t)
	cardJSON, err := json.Marshal(a2a.AgentCard{Name: "agent_x"})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}

	deliver := func(instanceID string, removed bool) {
		props := map[string]string{"instance_id": instanceID}
		if removed {
			props["removed"] = "true"
		}
		core.handleDiscovery(&broker.Message{Payload: cardJSON, UserProperties: props})
	}

	deliver("A", false)
	deliver("B", false)
	deliver("A", true)

	if core.agents.Get("agent_x") == nil {
		t.Fatal("stale removal from superseded instance A evicted the live agent")
	}

	deliver("B", true)
	if core.agents.Get("agent_x") != nil {
		t.Fatal("removal from the current instance B should deregister the agent")
	}
}

func TestHandleDiscovery_LegacyRemovalNoInstanceID(t *testing.T) {
	core, _ := newAgentLossCore(t)
	cardJSON, _ := json.Marshal(a2a.AgentCard{Name: "agent_y"})

	core.handleDiscovery(&broker.Message{Payload: cardJSON})
	core.handleDiscovery(&broker.Message{
		Payload:        cardJSON,
		UserProperties: map[string]string{"removed": "true"},
	})

	if core.agents.Get("agent_y") != nil {
		t.Fatal("legacy removal without an instance id should be honored")
	}
}

func TestHandleDiscovery_IDlessRemovalSparesInstancesWithIDs(t *testing.T) {
	core, _ := newAgentLossCore(t)
	cardJSON, _ := json.Marshal(a2a.AgentCard{Name: "agent_y"})

	core.handleDiscovery(&broker.Message{
		Payload:        cardJSON,
		UserProperties: map[string]string{"instance_id": "A"},
	})
	core.handleDiscovery(&broker.Message{
		Payload:        cardJSON,
		UserProperties: map[string]string{"removed": "true"},
	})

	if core.agents.Get("agent_y") == nil {
		t.Fatal("a removal without an instance id evicted an agent whose instance advertises one")
	}
}

func TestHandleDiscovery_RemovalLeavesLiveTasksToTheirTimers(t *testing.T) {
	for _, instanceID := range []string{"", "A"} {
		t.Run("instance "+instanceID, func(t *testing.T) {
			core, adapter := newAgentLossCore(t)
			const agentName = "agent_gone"
			const taskID = "task-live"
			_ = core.tasks.Register(taskID, agentName, "user-1", TaskIdentity{})
			core.tasks.SetWaiting(taskID, "req-open-form")
			t.Cleanup(func() { core.ForgetTask(taskID) })

			cardJSON, err := json.Marshal(a2a.AgentCard{Name: agentName})
			if err != nil {
				t.Fatalf("marshal card: %v", err)
			}
			props := map[string]string{}
			if instanceID != "" {
				props["instance_id"] = instanceID
			}
			core.handleDiscovery(&broker.Message{Payload: cardJSON, UserProperties: props})
			removal := map[string]string{"removed": "true"}
			if instanceID != "" {
				removal["instance_id"] = instanceID
			}
			core.handleDiscovery(&broker.Message{Payload: cardJSON, UserProperties: removal})

			if core.agents.Get(agentName) != nil {
				t.Fatal("the removal card did not deregister the agent")
			}
			if idleTimerArmed(core, taskID) {
				t.Error("the removal card armed a timer on the task")
			}
			if !core.IsTaskRunning(taskID) || len(adapter.Errors()) != 0 {
				t.Errorf("the removal card ended the task: running=%v errors=%d", core.IsTaskRunning(taskID), len(adapter.Errors()))
			}
		})
	}
}

type discoveryCountingAdapter struct {
	LoggingAdapter
	discovered atomic.Int64
	removed    atomic.Int64
}

func (a *discoveryCountingAdapter) OnAgentDiscovered(*a2a.AgentCard) { a.discovered.Add(1) }

func (a *discoveryCountingAdapter) OnAgentRemoved(string) { a.removed.Add(1) }

func TestHandleDiscovery_RepeatedCardsNotifyAdapterOnce(t *testing.T) {
	core, _ := newAgentLossCore(t)
	adapter := &discoveryCountingAdapter{
		LoggingAdapter: *NewLoggingAdapter(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	core.adapter = adapter

	cardJSON, err := json.Marshal(a2a.AgentCard{Name: "agent_z"})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	deliver := func(props map[string]string) {
		core.handleDiscovery(&broker.Message{Payload: cardJSON, UserProperties: props})
	}

	live := map[string]string{"instance_id": "A"}
	deliver(live)
	deliver(live)
	deliver(live)

	if got := adapter.discovered.Load(); got != 1 {
		t.Fatalf("OnAgentDiscovered fired %d times for three cards of one agent, want 1", got)
	}

	deliver(map[string]string{"instance_id": "A", "removed": "true"})
	if got := adapter.removed.Load(); got != 1 {
		t.Fatalf("OnAgentRemoved fired %d times for one removal card, want 1", got)
	}

	deliver(map[string]string{"instance_id": "B"})
	if got := adapter.discovered.Load(); got != 2 {
		t.Fatalf("OnAgentDiscovered fired %d times total after re-registration, want 2", got)
	}
}

func TestHandleDiscovery_SupersededRemovalDoesNotDeregisterFromAdapter(t *testing.T) {
	core, _ := newAgentLossCore(t)
	adapter := &discoveryCountingAdapter{
		LoggingAdapter: *NewLoggingAdapter(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	core.adapter = adapter

	cardJSON, err := json.Marshal(a2a.AgentCard{Name: "agent_w"})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	deliver := func(props map[string]string) {
		core.handleDiscovery(&broker.Message{Payload: cardJSON, UserProperties: props})
	}

	deliver(map[string]string{"instance_id": "A"})
	deliver(map[string]string{"instance_id": "B"})
	deliver(map[string]string{"instance_id": "A", "removed": "true"})

	if got := adapter.removed.Load(); got != 0 {
		t.Fatalf("OnAgentRemoved fired %d times for a superseded instance, want 0", got)
	}
	if core.agents.Get("agent_w") == nil {
		t.Fatal("registry evicted the live agent on a superseded removal card, so the adapter count proves nothing")
	}
}

func TestHandleDiscovery_SupersededRemovalDoesNotFailLiveTasks(t *testing.T) {
	core, _ := newAgentLossCore(t)

	const agentName = "agent_v"
	const taskID = "task-live"
	_ = core.tasks.Register(taskID, agentName, "user-1", TaskIdentity{})
	core.tasks.SetWaiting(taskID, "req-open-form")
	t.Cleanup(func() {
		core.cancelTaskTimeout(taskID)
		core.ForgetTask(taskID)
	})

	cardJSON, err := json.Marshal(a2a.AgentCard{Name: agentName})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	deliver := func(props map[string]string) {
		core.handleDiscovery(&broker.Message{Payload: cardJSON, UserProperties: props})
	}

	deliver(map[string]string{"instance_id": "A"})
	deliver(map[string]string{"instance_id": "B"})
	deliver(map[string]string{"instance_id": "A", "removed": "true"})

	if core.agents.Get(agentName) == nil {
		t.Fatal("registry evicted the live agent on a superseded removal card")
	}

	if idleTimerArmed(core, taskID) {
		t.Error("superseded removal armed a hard-timeout timer on a task whose agent never left")
	}
}

func TestHandleDiscovery_ReplicaStopKeepsAgent(t *testing.T) {
	core, _ := newAgentLossCore(t)
	adapter := &discoveryCountingAdapter{
		LoggingAdapter: *NewLoggingAdapter(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	core.adapter = adapter

	const agentName = "agent_r"
	const taskID = "task-on-replicated-agent"
	_ = core.tasks.Register(taskID, agentName, "user-1", TaskIdentity{})
	core.tasks.SetWaiting(taskID, "req-open-form")
	t.Cleanup(func() {
		core.cancelTaskTimeout(taskID)
		core.ForgetTask(taskID)
	})

	cardJSON, err := json.Marshal(a2a.AgentCard{Name: agentName})
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	deliver := func(props map[string]string) {
		core.handleDiscovery(&broker.Message{Payload: cardJSON, UserProperties: props})
	}

	deliver(map[string]string{"instance_id": "B"})
	deliver(map[string]string{"instance_id": "A"})
	deliver(map[string]string{"instance_id": "A", "removed": "true"})

	if core.agents.Get(agentName) == nil {
		t.Fatal("replica A stopping deregistered the agent although replica B still runs it")
	}
	if got := adapter.removed.Load(); got != 0 {
		t.Fatalf("OnAgentRemoved fired %d times while replica B still runs the agent, want 0", got)
	}

	deliver(map[string]string{"instance_id": "B", "removed": "true"})
	if core.agents.Get(agentName) != nil {
		t.Fatal("the last replica's removal should deregister the agent")
	}
	if got := adapter.removed.Load(); got != 1 {
		t.Fatalf("OnAgentRemoved fired %d times after the last replica left, want 1", got)
	}
	if !core.IsTaskRunning(taskID) {
		t.Fatal("the last replica leaving ended the task; it waits for the agent")
	}
}

func TestAgentRegistry_ExpiryKeepsAgentWithLiveReplica(t *testing.T) {
	reg := NewAgentRegistry()
	card := a2a.AgentCard{Name: "agent_e"}
	reg.AddOrUpdateInstance(card, "A")
	time.Sleep(10 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(10 * time.Millisecond)
	reg.AddOrUpdateInstance(card, "B")

	if removed := reg.ExpireOlderThan(cutoff); len(removed) != 0 {
		t.Fatalf("expired %v although replica B is still publishing", removed)
	}
	if reg.Get("agent_e") == nil {
		t.Fatal("agent_e should still be registered")
	}
}

func TestLoadReportedCard_RegistersTheAgent(t *testing.T) {
	core, _ := newAgentLossCore(t)
	adapter := &discoveryCountingAdapter{
		LoggingAdapter: *NewLoggingAdapter(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	core.adapter = adapter

	reported := discovery.ReportedCard{Card: a2a.AgentCard{Name: "fresh"}, InstanceID: "B", LastSeen: time.Now()}
	core.loadReportedCard(reported)
	core.loadReportedCard(reported)

	if got := adapter.discovered.Load(); got != 1 {
		t.Fatalf("OnAgentDiscovered fired %d times, want 1", got)
	}
	if core.agents.Get("fresh") == nil {
		t.Fatal("the reported card was not registered")
	}
}
