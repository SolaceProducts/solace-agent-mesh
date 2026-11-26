// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func TestTheAgentsSpendIsFiledUnderTheWorkflowOrScheduleTheTaskCameFrom(t *testing.T) {
	for name, tc := range map[string]struct {
		metadata    map[string]any
		props       map[string]string
		wantTrigger spend.TriggerKind
		wantID      string
		wantNode    string
	}{
		"a workflow node": {map[string]any{"workflow_name": "claims-intake", "node_id": "coverage", "parentTaskId": "P1"},
			map[string]string{a2a.PropDelegatingAgentName: "claims-intake"}, spend.TriggerWorkflow, "claims-intake", "coverage"},
		"a scheduled run": {map[string]any{a2a.MetadataScheduledTaskID: "sched-7"},
			map[string]string{"trigger": a2a.TriggerScheduled}, spend.TriggerSchedule, "sched-7", ""},
		"a workflow name another caller claims": {map[string]any{"workflow_name": "claims-intake", "node_id": "coverage"},
			map[string]string{a2a.PropDelegatingAgentName: peerName}, spend.TriggerAgent, a2a.CanonicalComponentID(peerName), ""},
	} {
		t.Run(name, func(t *testing.T) {
			r, c := connectedRig(t, nil)
			ledger := r.listen(a2a.SpendEventTopic(testNS))
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "T1", "method": methodSend, "params": map[string]any{
				"message": map[string]any{"role": "user", "metadata": tc.metadata, "parts": []any{map[string]any{"kind": "text", "text": "hi"}}}}})
			props := map[string]string{a2a.PropReplyTo: responseTopic("T1"), a2a.PropA2AStatusTopic: statusTopic("T1"),
				a2a.PropUserID: "alice@example.com", a2a.PropAuthToken: "tok:alice:T1"}
			for k, v := range tc.props {
				props[k] = v
			}
			if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
				t.Fatal(err)
			}
			f := c.next(typeTask)
			c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
			c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": 3, "output": 1}}))
			var filed spend.Event
			if err := json.Unmarshal(recv(t, ledger).Payload, &filed); err != nil {
				t.Fatal(err)
			}
			if filed.TriggerKind != tc.wantTrigger || filed.TriggerID != tc.wantID || filed.WorkflowNodeID != tc.wantNode {
				t.Fatalf("filed trigger %q %q node %q", filed.TriggerKind, filed.TriggerID, filed.WorkflowNodeID)
			}
		})
	}
}
