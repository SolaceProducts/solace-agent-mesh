// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestTeamsBudgetRefusalRepliesWithTheResetTimeAndLogsNoError(t *testing.T) {
	resetsAt := time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)
	a, brk, q := newRunAsTeamsFixture(t, "TestAgent")
	budgettest.BlockMesh(t, a.core.BudgetGate(), resetsAt)
	var logs bytes.Buffer
	a.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var mu sync.Mutex
	var replies []string
	a.botClient.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/activities") {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			replies = append(replies, string(body))
			mu.Unlock()
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"tok","token_type":"Bearer","expires_in":3600,"id":"sent"}`)),
		}, nil
	})}
	act := teamsActivity(ConversationTypePersonal)
	act.ServiceURL = "https://smba.example.test/"

	if got := a.trySubmitReason(t, act, nil); got != taxonomy.RejectBudgetExhausted {
		t.Fatalf("refusal reason = %q, want %q", got, taxonomy.RejectBudgetExhausted)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(replies) != 1 || !strings.Contains(replies[0], budget.ResetTimeText(resetsAt)) {
		t.Errorf("replies = %v, want one naming the reset time", replies)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("an expected budget refusal was logged as an error: %s", logs.String())
	}
	budgettest.AssertNotDelivered(t, brk, q)
}
