// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestSlackBudgetRefusalRepliesWithTheResetTimeAndLogsNoError(t *testing.T) {
	resetsAt := time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.DefaultAgentName = "Orchestrator"
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Orchestrator"})
	budgettest.BlockMesh(t, a.core.BudgetGate(), resetsAt)
	var logs bytes.Buffer
	a.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	reason, posted := a.trySubmit(slackMessage())

	if reason != taxonomy.RejectBudgetExhausted || !posted {
		t.Fatalf("trySubmit = (%q, %v), want (%q, true)", reason, posted, taxonomy.RejectBudgetExhausted)
	}
	calls.mu.Lock()
	text := calls.lastPostText
	calls.mu.Unlock()
	if want := entrypoint.BudgetExhaustedText(budgettest.MeshExhausted(resetsAt)); text != want {
		t.Errorf("reply = %q, want %q", text, want)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("an expected budget refusal was logged as an error: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "task refused: a budget is spent") {
		t.Errorf("the refusal left no diagnostic line: %s", logs.String())
	}
}
