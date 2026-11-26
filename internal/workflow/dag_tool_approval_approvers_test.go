// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestApprovalGate_ApproverSetFailsClosedInAWorkflow(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	cfg := gatedCallConfig()
	gate := cfg["gated_call"]
	gate.Approvers = tool.HILApprovers{Users: []string{"alice@example.com"}}
	cfg["gated_call"] = gate
	e.instance.hilToolConfigs = cfg
	e.instance.tools.SetHILConfigs(cfg)

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if rec.executed() {
		t.Fatal("a gate naming approvers ran its tool in a workflow")
	}
	rows, err := e.instance.pausedRepo.Expired(t.Context(), e.instance.cfg.Name, time.Now().Add(24*time.Hour).UnixMilli(), 10)
	if err != nil {
		t.Fatalf("list paused: %v", err)
	}
	if len(rows) != 0 {
		t.Error("the workflow parked on a prompt its own user would answer")
	}
	if got := audit.only(t).ApprovalRefusal; got != string(tool.RefusalNoApprovalRoute) {
		t.Errorf("approvalRefusal = %q, want %s", got, tool.RefusalNoApprovalRoute)
	}
}
