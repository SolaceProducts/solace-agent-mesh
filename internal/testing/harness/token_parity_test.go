// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

const activityLogTimeout = 10 * time.Second

type tokenParityCase struct {
	scenario       string
	tasks          int
	decisionTokens buckets
	servesCache    bool
	configure      func(t *testing.T, db *platstore.DB, ledger *SpendLedger)
}

const decisionWorkflowComponent = "workflow-DecisionRoutingWorkflow"

type activityRow struct {
	ID                string  `db:"id"`
	Kind              string  `db:"kind"`
	EndTime           *int64  `db:"end_time"`
	InputTokens       *int64  `db:"total_input_tokens"`
	OutputTokens      *int64  `db:"total_output_tokens"`
	CachedInputTokens *int64  `db:"total_cached_input_tokens"`
	UsageDetails      *string `db:"token_usage_details"`
}

const activitySelect = `SELECT id, kind, end_time, total_input_tokens, total_output_tokens,
	total_cached_input_tokens, token_usage_details FROM tasks`

func (r activityRow) tokens(t *testing.T) buckets {
	t.Helper()
	var details struct {
		CacheWrite int64 `json:"total_cache_write_tokens"`
	}
	if r.UsageDetails != nil {
		if err := json.Unmarshal([]byte(*r.UsageDetails), &details); err != nil {
			t.Fatalf("task %s token_usage_details: %v", r.ID, err)
		}
	}
	input, cached := orZero(r.InputTokens), orZero(r.CachedInputTokens)
	return buckets{input: input - cached, output: orZero(r.OutputTokens), cacheRead: cached - details.CacheWrite, cacheWrite: details.CacheWrite}
}

func orZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func (b buckets) plus(o buckets) buckets {
	return buckets{b.input + o.input, b.output + o.output, b.cacheRead + o.cacheRead, b.cacheWrite + o.cacheWrite}
}

func servedTokens(t *testing.T, scenario *Scenario) buckets {
	t.Helper()
	var total buckets
	for i, interaction := range scenario.LLMInteractions {
		if interaction.IsError() {
			continue
		}
		raw, err := interaction.StaticResponseJSON()
		if err != nil {
			t.Fatalf("llm_interaction[%d]: %v", i, err)
		}
		resp, err := mockllm.ConvertOpenAIResponse(raw)
		if err != nil {
			t.Fatalf("llm_interaction[%d]: %v", i, err)
		}
		if resp.Usage == nil {
			continue
		}
		b := resp.Usage.Buckets()
		total = total.plus(buckets{int64(b.Input), int64(b.Output), int64(b.CacheRead), int64(b.CacheWrite)})
	}
	return total
}

func otelTokens(metrics *observability.RecordingMetrics) buckets {
	sum := func(tokenType string) int64 {
		return metrics.CounterSum("gen_ai.tokens.used", "gen_ai.token.type", tokenType)
	}
	return buckets{sum("input"), sum("output"), sum("cache_read"), sum("cache_write")}
}

func waitForActivityRows(t *testing.T, db *store.DB, want int) []activityRow {
	t.Helper()
	deadline := time.Now().Add(activityLogTimeout)
	for {
		rows, err := dbutil.QueryRows[activityRow](t.Context(), db, activitySelect)
		if err != nil {
			t.Fatalf("read tasks: %v", err)
		}
		if len(rows) >= want && openTasks(rows) == 0 {
			return rows
		}
		if time.Now().After(deadline) {
			t.Errorf("after %s the task logger holds %d tasks, %d still open, want %d ended", activityLogTimeout, len(rows), openTasks(rows), want)
			return rows
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func openTasks(rows []activityRow) int {
	open := 0
	for _, row := range rows {
		if row.EndTime == nil {
			open++
		}
	}
	return open
}

func runTokenParity(t *testing.T, tc tokenParityCase) {
	t.Helper()
	platformDB := platstore.TestDB(t)
	gatewayDB, err := store.Open(store.TestDBURL(t), slog.Default())
	if err != nil {
		t.Fatalf("open gateway db: %v", err)
	}
	t.Cleanup(func() { _ = gatewayDB.Close() })

	model := seedPricedModel(t, platformDB, "priced-model")
	ledger := &SpendLedger{DB: platformDB, ModelAlias: model.Alias, ModelName: model.ModelName, ModelConfigID: model.ID, FlushInterval: governorFlushInterval}
	if tc.configure != nil {
		tc.configure(t, platformDB, ledger)
	}
	metrics := observability.NewRecordingMetrics()

	scenario, err := LoadScenario(tc.scenario)
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	served := servedTokens(t, scenario).plus(tc.decisionTokens)
	if served.input == 0 || served.output == 0 {
		t.Fatalf("%s serves %+v; parity over no tokens proves nothing", tc.scenario, served)
	}
	if tc.servesCache && (served.cacheRead == 0 || served.cacheWrite == 0) {
		t.Fatalf("%s serves %+v, want cache reads and writes to compare", tc.scenario, served)
	}
	h := New(t, scenario)
	t.Cleanup(h.Cleanup)
	h.SpendLedger, h.ActivityLog, h.Metrics = ledger, &ActivityLog{DB: gatewayDB}, metrics
	if err = h.Setup(t.Context()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	collected, err := h.Run(t.Context())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	h.AssertEvents(t, collected, scenario.ExpectedGatewayOutput)

	activity := waitForActivityRows(t, gatewayDB, tc.tasks)
	ledgerRows := waitForLedgerRowsTotalling(t, platformDB, served)

	activityByTask := map[string]buckets{}
	var activityTotal buckets
	for _, row := range activity {
		if row.Kind != store.TaskKindNormal {
			continue
		}
		activityByTask[row.ID] = row.tokens(t)
		activityTotal = activityTotal.plus(row.tokens(t))
	}
	ledgerByTask := map[string]buckets{}
	var ledgerTotal buckets
	for _, row := range ledgerRows {
		ledgerByTask[deref(row.TaskID)] = ledgerByTask[deref(row.TaskID)].plus(row.tokens())
		ledgerTotal = ledgerTotal.plus(row.tokens())
	}

	if len(activityByTask) != tc.tasks {
		t.Errorf("activity logged %d tasks, want %d", len(activityByTask), tc.tasks)
	}
	if activityTotal != served {
		t.Errorf("activity holds %+v, want the served %+v", activityTotal, served)
	}
	if ledgerTotal != served {
		t.Errorf("ledger holds %+v, want the served %+v", ledgerTotal, served)
	}
	if got := otelTokens(metrics); got != served {
		t.Errorf("otel counted %+v, want the served %+v", got, served)
	}
	if want := tc.decisionTokens.input + tc.decisionTokens.output; want > 0 {
		got := metrics.CounterSum("gen_ai.tokens.used", "component.name", decisionWorkflowComponent, "gen_ai.request.model", decisionModelName)
		if got != want {
			t.Errorf("otel counted %d decision tokens under %s/%s, want %d; counted %+v",
				got, decisionWorkflowComponent, decisionModelName, want, metrics.Counters("gen_ai.tokens.used"))
		}
	}
	for task, want := range ledgerByTask {
		if got := activityByTask[task]; got != want {
			t.Errorf("task %s: activity holds %+v, ledger %+v", task, got, want)
		}
	}
}

func waitForLedgerRowsTotalling(t *testing.T, db *platstore.DB, want buckets) []ledgerRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := dbutil.QueryRows[ledgerRow](t.Context(), db, ledgerSelect)
		if err != nil {
			t.Fatalf("read ledger: %v", err)
		}
		var total buckets
		for _, row := range rows {
			total = total.plus(row.tokens())
		}
		if total == want || time.Now().After(deadline) {
			time.Sleep(pastOneGovernorFlush)
			rows, err = dbutil.QueryRows[ledgerRow](t.Context(), db, ledgerSelect)
			if err != nil {
				t.Fatalf("read ledger: %v", err)
			}
			return rows
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestTokenParity_SurfacesRecordTheTokensServed(t *testing.T) {
	cases := map[string]tokenParityCase{
		"delegation": {scenario: "testdata/spend_delegation.yaml", tasks: 2, servesCache: true},
		"decision workflow on a model alias": {
			scenario: "testdata/spend_decision.yaml", tasks: 2,
			decisionTokens: primedDecisionUsage,
			configure: func(t *testing.T, db *platstore.DB, ledger *SpendLedger) {
				decision := seedPricedDecisionModel(t, db)
				ledger.DecisionAlias, ledger.DecisionConfigID, ledger.DecisionModelName = decision.Alias, decision.ID, decision.ModelName
			},
		},
		"decision workflow on an inline endpoint": {
			scenario: "testdata/spend_decision.yaml", tasks: 2,
			decisionTokens: primedDecisionUsage,
		},
		"context compaction": {scenario: "testdata/token_parity_compaction.yaml", tasks: 2, servesCache: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) { runTokenParity(t, tc) })
	}
}
