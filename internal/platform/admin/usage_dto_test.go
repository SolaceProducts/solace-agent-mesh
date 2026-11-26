// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func TestUsageWireEnums_MapEveryReportedValue(t *testing.T) {
	for _, source := range []budget.Source{budget.SourceAssigned, budget.SourceDefault} {
		if usageBudgetSources[source] == "" {
			t.Errorf("budget source %q has no wire value", source)
		}
	}
	for _, status := range []usage.Status{usage.StatusOK, usage.StatusApproaching, usage.StatusExceeded, usage.StatusUnlimited} {
		if usageBudgetStatuses[status] == "" {
			t.Errorf("budget status %q has no wire value", status)
		}
	}
	for _, kind := range []spend.RootKind{spend.RootAgent, spend.RootWorkflow} {
		if usageRootKinds[string(kind)] == "" {
			t.Errorf("root kind %q has no wire value", kind)
		}
	}
	for _, kind := range []string{"agent", "workflow", "tool", "switch", "loop", "map", "decision"} {
		if string(usageNodeKinds[kind]) != kind {
			t.Errorf("node type %q maps to %q", kind, usageNodeKinds[kind])
		}
	}
	wf := toUsageWorkflowResponse(usage.Workflow{Nodes: []usage.Node{{NodeID: "n", Kind: new("parallel")}}})
	if wf.Nodes[0].Kind != nil {
		t.Errorf("an unlisted node type = %q, want null", *wf.Nodes[0].Kind)
	}
}
