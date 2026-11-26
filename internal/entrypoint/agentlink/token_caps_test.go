// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func modelFinished(input int) map[string]any {
	return event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": input, "output": 0}})
}

func TestAModelCallReportingMoreTokensThanAnyModelTakesIsRefusedAndNotBilled(t *testing.T) {
	r, c := connectedRig(t, nil)
	ledger := r.listen(a2a.SpendEventTopic(testNS))
	r.taskOn(c, "T1", nil)
	c.send(modelFinished(maxTokensPerModelCall + 1))
	c.refused(errCodeLimit, "T1")
	none(t, ledger)
}

func TestATasksReportedTokensAreBoundedAcrossItsModelCalls(t *testing.T) {
	r, c := connectedRig(t, nil)
	ledger := r.listen(a2a.SpendEventTopic(testNS))
	r.taskOn(c, "T1", nil)
	for range maxTokensPerTask / maxTokensPerModelCall {
		c.send(modelFinished(maxTokensPerModelCall))
		recv(t, ledger)
	}
	c.send(modelFinished(1))
	c.refused(errCodeLimit, "T1")
	none(t, ledger)
}
