// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

const budgetTargetAgent = "BudgetAgent"

var budgetResetsAt = time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)

func newMeshBlockedAdapter(t *testing.T) (*Adapter, broker.Queue, *broker.DevBroker) {
	t.Helper()
	a, brk := newAuthzTestAdapter(t, mustParseConfig(t, nil), nil)
	a.toolMap = map[string]toolMapping{"budget_tool": {agentName: budgetTargetAgent}}
	a.agentTools = make(map[string][]string)
	a.tasks = make(map[string]*taskState)

	budgettest.BlockMesh(t, a.core.BudgetGate(), budgetResetsAt)

	return a, budgettest.TapAgentRequests(t, brk, "test-ns", budgetTargetAgent), brk
}

func toolResultText(t *testing.T, result *mcplib.CallToolResult) string {
	t.Helper()
	var texts []string
	for _, c := range result.Content {
		if tc, ok := mcplib.AsTextContent(c); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func TestMCPToolCall_WhileTheMeshBudgetIsSpentAnswersWithAToolErrorNamingTheReset(t *testing.T) {
	a, q, brk := newMeshBlockedAdapter(t)

	result, err := a.handleToolCall(t.Context(), "budget_tool", toolCall("budget_tool", "hello"))
	if err != nil {
		t.Fatalf("handleToolCall returned a protocol error %v, want a tool-result error", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("result = %+v, want a tool result flagged IsError", result)
	}
	text := toolResultText(t, result)
	if want := budget.ResetTimeText(budgetResetsAt); !strings.Contains(text, want) {
		t.Errorf("refusal text %q does not name the reset time %q", text, want)
	}
	budgettest.AssertNotDelivered(t, brk, q)
}

func TestMCPToolCall_ABudgetRefusalReportsBudgetExhaustedAndHoldsNoPendingTask(t *testing.T) {
	a, _, _ := newMeshBlockedAdapter(t)

	_, reason, err := a.callTool(t.Context(), "budget_tool", toolCall("budget_tool", "hello"))
	if err != nil {
		t.Fatalf("callTool returned %v, want a tool-result error", err)
	}
	if reason != taxonomy.RejectBudgetExhausted {
		t.Errorf("refusal reason = %q, want %q", reason, taxonomy.RejectBudgetExhausted)
	}
	a.tasksMu.Lock()
	pending := len(a.tasks)
	a.tasksMu.Unlock()
	if pending != 0 {
		t.Errorf("%d tasks still awaiting a response after the refusal, want none", pending)
	}
}
