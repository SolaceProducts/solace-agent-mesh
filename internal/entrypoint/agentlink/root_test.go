// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

var rootCases = map[string]struct {
	parent   map[string]string
	wantKind string
	wantID   string
	wantNode string
}{
	"no root": {nil, a2a.RootKindAgent, a2a.CanonicalComponentID(agentName), ""},
	"a whole root": {map[string]string{a2a.PropRootKind: a2a.RootKindWorkflow, a2a.PropRootID: "claims-intake", a2a.PropRootNodeID: "coverage"},
		a2a.RootKindWorkflow, "claims-intake", "coverage"},
	"a partial root": {map[string]string{a2a.PropRootKind: a2a.RootKindWorkflow, a2a.PropRootNodeID: "coverage"},
		a2a.RootKindAgent, a2a.CanonicalComponentID(agentName), ""},
}

func TestAPeerCallAndTheAgentsOwnSpendNameTheSameRoot(t *testing.T) {
	for name, tc := range rootCases {
		t.Run(name, func(t *testing.T) {
			r, c := connectedRig(t, nil)
			requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
			ledger := r.listen(a2a.SpendEventTopic(testNS))
			r.taskOn(c, "T1", tc.parent)
			c.send(peerCall("S1", peerName, "Is policy P-9 active?"))
			up := a2a.DecodeUserProperties(recv(t, requests).UserProperties)
			if up.RootKind != tc.wantKind || up.RootID != tc.wantID || up.RootNodeID != tc.wantNode {
				t.Fatalf("root %q %q %q", up.RootKind, up.RootID, up.RootNodeID)
			}
			c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": 3, "output": 1}}))
			var filed map[string]any
			if err := json.Unmarshal(recv(t, ledger).Payload, &filed); err != nil {
				t.Fatal(err)
			}
			if filed["root_kind"] != tc.wantKind || (filed["root_node_id"] != nil && filed["root_node_id"] != tc.wantNode) {
				t.Fatalf("spend root %v", filed)
			}
		})
	}
}
