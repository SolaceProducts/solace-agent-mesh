// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools_test

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestBuilderEscalationAnswerMatchesTheEngine(t *testing.T) {
	for _, targetType := range []string{"agent", "tool", "workflow"} {
		for _, mode := range []string{"", workflow.EscalationModeReroute} {
			router := &workflow.NodeConfig{ID: "triage", Type: "decision", RouteOn: "department", Escalate: "reclassify", EscalationMode: mode}
			target := &workflow.NodeConfig{ID: "reclassify", Type: targetType}
			want := workflow.EscalationAnswerTemplate(router, target)

			builderRouter := &tools.WfNodeInfo{ID: "triage", Type: "decision", RouteOn: "department", Escalate: "reclassify", EscalationMode: mode}
			nodeByID := map[string]*tools.WfNodeInfo{"reclassify": {ID: "reclassify", Type: targetType}}
			if got := tools.WfEscalationAnswer(builderRouter, nodeByID); got != want {
				t.Errorf("%s target, escalation_mode %q: Builder reads %q, engine reads %q", targetType, mode, got, want)
			}
		}
	}
}

func TestBuilderLeavesAHandoffEscalationUnread(t *testing.T) {
	router := &tools.WfNodeInfo{ID: "triage", Type: "decision", RouteOn: "department", Escalate: "reclassify", EscalationMode: workflow.EscalationModeHandoff}
	nodeByID := map[string]*tools.WfNodeInfo{"reclassify": {ID: "reclassify", Type: "agent"}}
	if got := tools.WfEscalationAnswer(router, nodeByID); got != "" {
		t.Errorf("handoff escalation: Builder reads %q, want no answer reference", got)
	}
}
