// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func blockMeshBudget(t *testing.T, rig *webhookRig, resetsAt time.Time) {
	t.Helper()
	budgettest.BlockMesh(t, rig.core.BudgetGate(), resetsAt)
}

func TestWebhook_WhileTheMeshBudgetIsSpentAnswers429WithRetryAfterAndPublishesNothing(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)
	resetsAt := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	blockMeshBudget(t, rig, resetsAt)

	resp := rig.post("/token", `{"message":"hello"}`, bearer("dev-webhook-token"))
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	retryAfter, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After = %q, want whole seconds", resp.Header.Get("Retry-After"))
	}
	if remaining := time.Until(resetsAt); retryAfter < int(remaining.Seconds())-5 || retryAfter > int(remaining.Seconds())+1 {
		t.Errorf("Retry-After = %ds, want about %s until the reset", retryAfter, remaining.Round(time.Second))
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["message"] != entrypoint.BudgetExhaustedText(budgettest.MeshExhausted(resetsAt)) {
		t.Errorf("message = %q, want %q", body["message"], entrypoint.BudgetExhaustedText(budgettest.MeshExhausted(resetsAt)))
	}
	if id, _ := body["errorId"].(string); id == "" {
		t.Error("body carries no errorId")
	}
	if len(body) != 2 {
		t.Errorf("body = %v, want only message and errorId", body)
	}

	select {
	case req := <-ch:
		t.Fatalf("a budget-refused webhook reached the agent as task %q", req.taskID)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWebhook_ABlockPastItsPeriodEndAsksForARetryShortly(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	blockMeshBudget(t, rig, time.Now().Add(-time.Minute))

	resp := rig.post("/token", `{"message":"hello"}`, bearer("dev-webhook-token"))
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want \"30\"", got)
	}
}

func TestWebhook_ABudgetRefusalReportsBudgetExhausted(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	blockMeshBudget(t, rig, time.Now().Add(time.Hour))

	if got := rig.refuse("/token", `{"message":"hi"}`, bearer("dev-webhook-token")); got != taxonomy.RejectBudgetExhausted {
		t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectBudgetExhausted)
	}
}
