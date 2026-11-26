// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestRemoteToolForwardsTheCallsSpendAttributionToTheSTR(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	rt := NewTool(&ToolConfig{ToolName: "summarize", TimeoutSeconds: 5}, exe)
	simulateSTR(t, brk, "summarize", func(corrID, replyTo, _ string) {
		payload, _ := json.Marshal(a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{}}))
		_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
	})

	attribution := &remotetool.SpendAttribution{UserSub: "alice", AgentName: "researcher", TaskID: "task-1", ParentTaskID: "orchestrator-task"}
	toolCtx := &tool.Context{AgentName: testAgent, UserID: "alice", ParentTaskID: "task-1", RemoteSpend: attribution}
	params := captureInvokeParams(t, brk, "summarize", func() {
		if _, err := rt.Execute(t.Context(), map[string]any{}, toolCtx); err != nil {
			t.Errorf("Execute: %v", err)
		}
	})

	if params.Spend == nil || *params.Spend != *attribution {
		t.Errorf("spend on the wire = %+v, want %+v", params.Spend, attribution)
	}
}
