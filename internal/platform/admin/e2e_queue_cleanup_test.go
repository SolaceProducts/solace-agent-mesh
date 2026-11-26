// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestE2E_UndeployRemovesBrokerQueue(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, []*llm.ChatResponse{
		{Content: "ack", FinishReason: "stop"},
	})

	devBrk, ok := h.brk.(*broker.DevBroker)
	if !ok {
		t.Fatalf("e2e harness broker is %T, want *broker.DevBroker for queue introspection", h.brk)
	}

	agentID := h.createAgent("queue-cleanup-probe",
		"You are a test agent used to verify that the durable request queue is removed from the broker on undeploy.")
	h.deploy(agentID)

	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	expectedQueueName := a2a.QueueName(h.a2aNamespace, "a2a", instanceName)

	deadline := time.Now().Add(2 * time.Second)
	queueAppeared := false
	for time.Now().Before(deadline) {
		if devBrk.HasQueue(expectedQueueName) {
			queueAppeared = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !queueAppeared {
		t.Fatalf("durable request queue %q never appeared on the broker after deploy — agent.Start regression?",
			expectedQueueName)
	}

	h.undeploy(agentID)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !devBrk.HasQueue(expectedQueueName) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("durable request queue %q still on broker after undeploy: orphan-queue regression.\n"+
		"Every undeploy leaks one queue until the broker's endpoint quota runs out.",
		expectedQueueName)
}
