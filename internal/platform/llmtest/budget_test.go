// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llmtest

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func countingProvider(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(provider.Close)
	return provider, &hits
}

func spentBudget(t *testing.T) *SpendRecording {
	t.Helper()
	return &SpendRecording{Gate: budgettest.BlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC))}
}

func assertNotTestedForTheBudget(t *testing.T, ok bool, msg string, hits *atomic.Int32) {
	t.Helper()
	if ok {
		t.Fatal("the probe succeeded while the budget was spent")
	}
	if !strings.HasPrefix(msg, "Connection not tested: ") || !strings.Contains(msg, "1 November 2099 00:00 UTC") {
		t.Errorf("message = %q, want it to say the test was not run until the budget resets", msg)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the provider received %d requests while the budget was spent, want 0", n)
	}
}

func TestProbe_IsNotSentWhileTheBudgetIsSpent(t *testing.T) {
	t.Parallel()
	provider, hits := countingProvider(t)

	ok, msg := Probe(t.Context(), newTestModelConfig(provider.URL, "sk-test"), slog.Default(), spentBudget(t))

	assertNotTestedForTheBudget(t, ok, msg, hits)
}

func TestProbeDecision_IsNotSentWhileTheBudgetIsSpent(t *testing.T) {
	t.Parallel()
	provider, hits := countingProvider(t)

	ok, msg := ProbeDecision(t.Context(), "openai", map[string]any{"api_base": provider.URL, "api_key": "sk-test", "model": "gpt-4o"},
		provider.Client(), slog.Default(), spentBudget(t))

	assertNotTestedForTheBudget(t, ok, msg, hits)
}

func TestProbe_NamesTheCallersOwnBudgetWhenThatIsWhatIsSpent(t *testing.T) {
	t.Parallel()
	provider, hits := countingProvider(t)
	const admin = "0b9c4c2e-6f2a-4d47-9a51-3b8e1f6d2c10"
	rec := &SpendRecording{
		Attribution: &llm.SpendAttribution{UserID: admin},
		Gate:        budgettest.UserBlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC), budget.UserKey(admin, "")),
	}

	ok, msg := Probe(t.Context(), newTestModelConfig(provider.URL, "sk-test"), slog.Default(), rec)

	assertNotTestedForTheBudget(t, ok, msg, hits)
	if !strings.Contains(msg, "your usage budget is used up") {
		t.Errorf("message = %q, want the caller's own budget named, not the deployment's", msg)
	}
}
