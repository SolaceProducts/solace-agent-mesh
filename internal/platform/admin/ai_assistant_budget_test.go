// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func TestGenerateAgent_ASpentBudgetIsA429WithRetryAfterAndNoModelCall(t *testing.T) {
	t.Parallel()
	model := mockllm.NewFIFOClient(nil)
	svc := newAIAssistantWithEmptyRepos(t, fifoResolver(model))
	svc.SetBudgetGate(budgettest.BlockedGate(t, time.Now().Add(2*time.Hour)))
	h := &aiAssistantHandler{svc: svc, logger: slog.Default()}

	rec := httptest.NewRecorder()
	h.handleGenerateAgent(rec, httptest.NewRequest(http.MethodPost, "/api/v1/platform/aiAssistant/generateAgent",
		strings.NewReader(`{"description":"a test agent that does things"}`)))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body)
	}
	if seconds, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || seconds < 7000 || seconds > 7200 {
		t.Errorf("Retry-After = %q, want about two hours in seconds", rec.Header().Get("Retry-After"))
	}
	var body errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || !strings.Contains(body.Message, "usage budget is used up until") {
		t.Errorf("body = %+v (decode %v), want the budget refusal", body, err)
	}
	if n := len(model.Recorder().Requests()); n != 0 {
		t.Errorf("the model received %d requests while the budget was spent, want 0", n)
	}
}
