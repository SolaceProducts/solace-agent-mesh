// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

var oneNodeBudgetSecs = int(workflow.DefaultNodeTimeout / time.Second)

func TestParseAgentConfig_DelegationTimeoutsResolveIndependently(t *testing.T) {
	tests := []struct {
		name                            string
		iac                             map[string]any
		wantPeer, wantWorkflow, wantSub int
	}{
		{
			name:         "section present with no timeout gives each path its own default",
			iac:          map[string]any{"allow_list": []any{}},
			wantPeer:     config.DefaultPeerRequestTimeoutSecs,
			wantWorkflow: config.DefaultWorkflowRequestTimeoutSecs,
			wantSub:      config.DefaultSubTaskTimeoutSecs,
		},
		{
			name:         "an explicit request_timeout_seconds above the node floor governs all three",
			iac:          map[string]any{"request_timeout_seconds": float64(2000)},
			wantPeer:     2000,
			wantWorkflow: 2000,
			wantSub:      2000,
		},
		{
			name:         "an explicit request_timeout_seconds below the node floor cannot pull the other two under it",
			iac:          map[string]any{"request_timeout_seconds": float64(45)},
			wantPeer:     45,
			wantWorkflow: oneNodeBudgetSecs,
			wantSub:      oneNodeBudgetSecs,
		},
		{
			name: "a specific key overrides the shared value",
			iac: map[string]any{
				"request_timeout_seconds":          float64(45),
				"workflow_request_timeout_seconds": float64(900),
			},
			wantPeer:     45,
			wantWorkflow: 900,
			wantSub:      oneNodeBudgetSecs,
		},
		{
			name: "specific keys alone leave the peer default untouched",
			iac: map[string]any{
				"workflow_request_timeout_seconds": float64(900),
				"sub_task_timeout_seconds":         float64(1200),
			},
			wantPeer:     config.DefaultPeerRequestTimeoutSecs,
			wantWorkflow: 900,
			wantSub:      1200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac, err := ParseAgentConfig(config.FromMap(map[string]any{
				"agent_name":                "test-agent",
				"inter_agent_communication": tt.iac,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if ac.PeerRequestTimeoutSeconds != tt.wantPeer {
				t.Errorf("PeerRequestTimeoutSeconds = %d, want %d", ac.PeerRequestTimeoutSeconds, tt.wantPeer)
			}
			if ac.WorkflowRequestTimeoutSeconds != tt.wantWorkflow {
				t.Errorf("WorkflowRequestTimeoutSeconds = %d, want %d", ac.WorkflowRequestTimeoutSeconds, tt.wantWorkflow)
			}
			if ac.SubTaskTimeoutSeconds != tt.wantSub {
				t.Errorf("SubTaskTimeoutSeconds = %d, want %d", ac.SubTaskTimeoutSeconds, tt.wantSub)
			}
		})
	}
}

func TestParseAgentConfig_NoInterAgentCommunicationSectionLeavesTimeoutsUnset(t *testing.T) {
	ac, err := ParseAgentConfig(config.FromMap(map[string]any{"agent_name": "test-agent"}))
	if err != nil {
		t.Fatal(err)
	}
	if ac.PeerRequestTimeoutSeconds != 0 || ac.WorkflowRequestTimeoutSeconds != 0 || ac.SubTaskTimeoutSeconds != 0 {
		t.Errorf("expected all delegation timeouts unset, got peer=%d workflow=%d subTask=%d",
			ac.PeerRequestTimeoutSeconds, ac.WorkflowRequestTimeoutSeconds, ac.SubTaskTimeoutSeconds)
	}
}

func TestDelegationTimeoutDefaults_ExceedOneNodeBudget(t *testing.T) {
	nodeBudgetSecs := int(workflow.DefaultNodeTimeout / time.Second)
	if config.DefaultWorkflowRequestTimeoutSecs <= nodeBudgetSecs {
		t.Errorf("workflow delegation default %ds must exceed a workflow node's own default %ds",
			config.DefaultWorkflowRequestTimeoutSecs, nodeBudgetSecs)
	}
	if config.DefaultWorkflowRequestTimeoutSecs <= config.DefaultPeerRequestTimeoutSecs {
		t.Errorf("workflow delegation default %ds must exceed the peer default %ds",
			config.DefaultWorkflowRequestTimeoutSecs, config.DefaultPeerRequestTimeoutSecs)
	}
}
