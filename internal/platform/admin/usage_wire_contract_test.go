// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

var updateUsageGoldens = flag.Bool("update-usage", false, "rewrite the usage wire-contract goldens in testdata/usage and their web UI copies")

var webUIUsageGoldens = filepath.Join("..", "..", "..", "webui", "src", "testing", "data", "usage")

func TestUsageResponsesArePinnedToTheirGoldens(t *testing.T) {
	fixtures := map[string]any{
		"summary":                 usageSummaryFixture(),
		"summary_building":        usageSummaryBuildingFixture(),
		"agents_list":             usageAgentsListFixture(),
		"models_list_daily":       usageModelsListDailyFixture(),
		"users_list":              usageUsersListFixture(),
		"agent_detail":            usageAgentDetailFixture(),
		"model_detail":            usageModelDetailFixture(),
		"workflow_detail":         usageWorkflowDetailFixture(),
		"workflow_detail_by_name": usageWorkflowByNameFixture(),
		"user_detail":             usageUserDetailFixture(),
		"self":                    usageSelfFixture(),
		"self_unattributed":       usageSelfUnattributedFixture(),
		"savings":                 usageSavingsFixture(),
		"savings_unpriced":        usageSavingsUnpricedFixture(),
		"savings_building":        usageSavingsBuildingFixture(),
		"self_savings":            usageSelfSavingsFixture(),
	}
	for name, response := range fixtures {
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(response, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')
			for _, path := range []string{filepath.Join("testdata", "usage", name+".json"), filepath.Join(webUIUsageGoldens, name+".json")} {
				if *updateUsageGoldens {
					if err = os.WriteFile(path, got, 0o644); err != nil {
						t.Fatalf("write %s: %v", path, err)
					}
					continue
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s drifted from its golden; the web UI builds against it.\ngot:\n%s", path, got)
				}
			}
		})
	}

	for _, dir := range []string{filepath.Join("testdata", "usage"), webUIUsageGoldens} {
		goldens, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil {
			t.Fatalf("list goldens: %v", err)
		}
		for _, path := range goldens {
			if _, ok := fixtures[strings.TrimSuffix(filepath.Base(path), ".json")]; !ok {
				t.Errorf("%s has no fixture; delete it or restore its fixture", path)
			}
		}
	}
}

func TestUsageMoney_IsTenFractionalDigits(t *testing.T) {
	if want := int64(math.Pow10(pricing.CostDecimalPlaces)); usageMoneyUnit != want {
		t.Fatalf("usageMoneyUnit = %d, want 10^CostDecimalPlaces = %d", int64(usageMoneyUnit), want)
	}
	for _, tc := range []struct {
		money UsageMoney
		want  string
	}{
		{0, `"0.0000000000"`},
		{1, `"0.0000000001"`},
		{12_405_800_000_000, `"1240.5800000000"`},
		{2_031_000_000, `"0.2031000000"`},
		{-5_000_000_000, `"-0.5000000000"`},
		{math.MaxInt64, `"922337203.6854775807"`},
	} {
		got, err := json.Marshal(tc.money)
		if err != nil || string(got) != tc.want {
			t.Errorf("marshal %d = %s, %v; want %s", int64(tc.money), got, err, tc.want)
		}
		var back UsageMoney
		if err := json.Unmarshal(got, &back); err != nil || back != tc.money {
			t.Errorf("unmarshal %s = %d, %v; want %d", got, int64(back), err, int64(tc.money))
		}
	}
}

func TestUsageMoney_RefusesWhatIsNotAnAmount(t *testing.T) {
	for _, text := range []string{
		`"1.2.3"`, `"+1"`, `"-"`, `""`, `".5"`, `"1."`, `"1.00000000001"`, `"1e3"`, `"one"`, `12`,
		`"922337203.6854775808"`, `"999999999999"`,
	} {
		var m UsageMoney
		if err := json.Unmarshal([]byte(text), &m); err == nil {
			t.Errorf("unmarshal %s = %d, want refused", text, int64(m))
		}
	}
	m := UsageMoney(7)
	if err := json.Unmarshal([]byte(`null`), &m); err != nil || m != 7 {
		t.Errorf("unmarshal null = %d, %v; want the amount left as it was", int64(m), err)
	}
}

func money(text string) UsageMoney {
	whole, frac, _ := strings.Cut(text, ".")
	frac += strings.Repeat("0", pricing.CostDecimalPlaces-len(frac))
	w, errW := strconv.ParseInt(whole, 10, 64)
	f, errF := strconv.ParseInt(frac, 10, 64)
	if errW != nil || errF != nil {
		panic("money: " + text)
	}
	return UsageMoney(w*usageMoneyUnit + f)
}

func moneyPtr(text string) *UsageMoney { m := money(text); return &m }

func at(year int, month time.Month, day, hour, minute int) RESTTimestamp {
	return RESTTimestamp(time.Date(year, month, day, hour, minute, 0, 0, time.UTC).UnixMilli())
}

func timestampPtr(t RESTTimestamp) *RESTTimestamp { return &t }

func ptr[T any](v T) *T { return &v }

func pct(part, whole string) float64 {
	return float64(money(part)) / float64(money(whole)) * 100
}

var (
	usageFixtureWindow     = UsageWindow{StartTime: at(2026, time.October, 1, 0, 0), EndTime: at(2026, time.October, 15, 19, 20)}
	usageFixtureComparison = UsageWindow{StartTime: at(2026, time.September, 1, 0, 0), EndTime: at(2026, time.September, 15, 19, 20)}
	usageFixtureDataAsOf   = timestampPtr(at(2026, time.October, 15, 19, 19))
	usageFixturePeriod     = [2]RESTTimestamp{at(2026, time.October, 1, 0, 0), at(2026, time.November, 1, 0, 0)}
	usageFixtureMeshCost   = "1240.58"
	usageFixtureUserCost   = "412.3"
)

func usageFixtureDaily(first, second string, firstActivities, secondActivities int64) []UsageDay {
	return []UsageDay{
		{Date: "2026-10-01", Cost: money(first), Tokens: usageFixtureDayTokens(firstActivities), Activities: firstActivities},
		{Date: "2026-10-02", Cost: money(second), Tokens: usageFixtureDayTokens(secondActivities), Activities: secondActivities},
	}
}

func usageFixtureDayTokens(activities int64) UsageTokens {
	return UsageTokens{UncachedInput: activities * 1_200, Output: activities * 310, CacheRead: activities * 540, CacheWrite: activities * 45}
}

func usageFixtureBuckets(uncached, output, cacheRead, cacheWrite string) UsageCostByBucket {
	return UsageCostByBucket{UncachedInput: money(uncached), Output: money(output), CacheRead: money(cacheRead), CacheWrite: money(cacheWrite)}
}

func usageFixtureAgentTotals() UsageSubjectTotals {
	return UsageSubjectTotals{
		Cost: money("20.44"), Activities: 206, CostPerActivity: moneyPtr("0.0992233009"), ShareOfSpend: ptr(pct("20.44", usageFixtureMeshCost)),
		CostByBucket: usageFixtureBuckets("10.2", "9.04", "0.7", "0.5"),
		Tokens:       UsageTokens{UncachedInput: 1_523_000, Output: 120_400, CacheRead: 88_000, CacheWrite: 4_100},
		Calls:        1_840, UnpricedCalls: 0,
	}
}

func usageFixtureUserTotals(scope UsageScope) UsageSubjectTotals {
	share := ptr(pct(usageFixtureUserCost, usageFixtureMeshCost))
	if scope == UsageScopeSelf {
		share = nil
	}
	return UsageSubjectTotals{
		Cost: money(usageFixtureUserCost), Activities: 120, CostPerActivity: moneyPtr("3.4358333333"), ShareOfSpend: share,
		CostByBucket: usageFixtureBuckets("203.1", "190.2", "11", "8"),
		Tokens:       UsageTokens{UncachedInput: 30_310_000, Output: 2_536_000, CacheRead: 1_375_000, CacheWrite: 64_000},
		Calls:        3_011, UnpricedCalls: 2,
	}
}

func usageFixtureUserBudget() *UsageBudget {
	return &UsageBudget{
		ID: "01a10c9e-f47a-7e77-be6a-3cdc8d0b976d", Name: "Procurement", Source: UsageBudgetAssigned,
		Amount: moneyPtr("450"), WarningPercentage: ptr(80), TopUp: nil, EffectiveAmount: moneyPtr("450"),
		PeriodStart: usageFixturePeriod[0], PeriodEnd: usageFixturePeriod[1],
		PeriodSpend: money(usageFixtureUserCost), UsagePercent: ptr(pct(usageFixtureUserCost, "450")), Status: UsageBudgetApproaching,
	}
}

func usageFixtureBareSubjectBudget(periodSpend string) *UsageBudget {
	return &UsageBudget{
		ID: "01a10c9e-0000-7e77-be6a-3cdc8d0b9003", Name: "Standard", Source: UsageBudgetDefault,
		Amount: moneyPtr("100"), WarningPercentage: ptr(80), TopUp: nil, EffectiveAmount: moneyPtr("100"),
		PeriodStart: usageFixturePeriod[0], PeriodEnd: usageFixturePeriod[1],
		PeriodSpend: money(periodSpend), UsagePercent: ptr(pct(periodSpend, "100")), Status: UsageBudgetOK,
	}
}

func usageSummaryFixture() UsageSummaryResponse {
	return UsageSummaryResponse{
		Scope: UsageScopeTenant, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
		Window: usageFixtureWindow, ComparisonWindow: &usageFixtureComparison, Currency: "USD",
		Totals: UsageTotals{
			Cost:         money(usageFixtureMeshCost),
			CostByBucket: usageFixtureBuckets("612.1", "540.2", "38", "50.28"),
			Tokens:       UsageTokens{UncachedInput: 91_234_567, Output: 12_345_678, CacheRead: 4_567_890, CacheWrite: 123_456},
			Calls:        182_345, UnpricedCalls: 120, Activities: 23_011,
		},
		CostByKind: UsageCostByKind{Users: money(usageFixtureMeshCost), Agents: money("1102.47"), Workflows: money("877.4"), Models: money(usageFixtureMeshCost)},
		Comparison: &UsageComparison{Cost: money("912.19"), ChangePercent: ptr(pct("328.39", "912.19"))},
		MeshBudget: &UsageBudget{
			ID: "01a10c9e-0000-7e77-be6a-3cdc8d0b9001", Name: "Mesh", Source: UsageBudgetDefault,
			Amount: moneyPtr("2000"), WarningPercentage: ptr(80),
			TopUp:           &UsageTopUp{ID: "01a10c9e-0000-7e77-be6a-3cdc8d0b9002", Amount: money("400"), ExpiresTime: usageFixturePeriod[1]},
			EffectiveAmount: moneyPtr("2400"),
			PeriodStart:     usageFixturePeriod[0], PeriodEnd: usageFixturePeriod[1],
			PeriodSpend: money(usageFixtureMeshCost), UsagePercent: ptr(pct(usageFixtureMeshCost, "2400")), Status: UsageBudgetOK,
		},
		Daily: usageFixtureDaily("55.12", "61.4", 1_504, 1_611),
	}
}

func usageSummaryBuildingFixture() UsageSummaryResponse {
	return UsageSummaryResponse{
		Scope: UsageScopeTenant, DataAsOf: nil, HistoryComplete: false,
		Window: usageFixtureWindow, ComparisonWindow: nil, Currency: "USD",
		Totals:     UsageTotals{},
		CostByKind: UsageCostByKind{},
		Comparison: nil,
		MeshBudget: nil,
		Daily:      usageFixtureDaily("0", "0", 0, 0),
	}
}

func usageAgentsListFixture() PaginatedResponse[UsageRowResponse] {
	agent := usageFixtureAgentTotals()
	rows := []UsageRowResponse{
		{
			ID: ptr("01a10c9e-1111-7e77-be6a-3cdc8d0b9101"), Name: ptr("QA Tester"), BookedName: nil,
			Cost: agent.Cost, Activities: agent.Activities, CostPerActivity: agent.CostPerActivity, ShareOfSpend: agent.ShareOfSpend,
			CostByBucket: agent.CostByBucket, Tokens: agent.Tokens, Calls: agent.Calls, UnpricedCalls: agent.UnpricedCalls,
		},
		{
			ID: nil, Name: ptr("Orchestrator"), BookedName: ptr("OrchestratorAgent"),
			Cost: money("0"), Activities: 0, CostPerActivity: nil, ShareOfSpend: ptr(0.0),
			Calls: 0, UnpricedCalls: 0,
		},
	}
	return PaginatedResponse[UsageRowResponse]{Data: rows, Meta: NewPaginationMeta(1, 25, 2)}
}

func usageFixtureServed() []UsageServedModel {
	served := func(name, provider *string, cost string, activities int64) UsageServedModel {
		return UsageServedModel{
			Name: name, Provider: provider, Cost: money(cost), Activities: activities,
			CostByBucket: usageFixtureBuckets(cost, "0", "0", "0"),
			Tokens:       UsageTokens{UncachedInput: activities * 1_000}, Calls: activities * 4,
		}
	}
	return []UsageServedModel{
		served(ptr("claude-sonnet-4-6"), ptr("anthropic"), "12.04", 120),
		served(ptr("claude-sonnet-4-5"), ptr("anthropic"), "8.3", 85),
		served(nil, nil, "0.1", 1),
	}
}

func usageModelsListDailyFixture() PaginatedResponse[UsageModelRowResponse] {
	general := usageFixtureAgentTotals()
	rows := []UsageModelRowResponse{
		{
			UsageRowResponse: UsageRowResponse{
				ID: ptr("01a10c9e-4444-7e77-be6a-3cdc8d0b9401"), Name: ptr("General"), BookedName: nil,
				Cost: general.Cost, Activities: general.Activities, CostPerActivity: general.CostPerActivity, ShareOfSpend: general.ShareOfSpend,
				CostByBucket: general.CostByBucket, Tokens: general.Tokens, Calls: general.Calls, UnpricedCalls: general.UnpricedCalls,
				Daily: usageFixtureDaily("12.04", "8.4", 120, 86),
			},
			Models: usageFixtureServed(),
		},
		{
			UsageRowResponse: UsageRowResponse{
				ID: nil, Name: ptr("gpt-4o-mini"), BookedName: ptr("gpt-4o-mini"),
				Cost: money("0"), Activities: 0, CostPerActivity: nil, ShareOfSpend: ptr(0.0),
				Daily: usageFixtureDaily("0", "0", 0, 0),
			},
			Models: []UsageServedModel{},
		},
	}
	return PaginatedResponse[UsageModelRowResponse]{Data: rows, Meta: NewPaginationMeta(1, 25, 2)}
}

func usageModelDetailFixture() UsageModelResponse {
	return UsageModelResponse{
		UsageSubjectResponse: UsageSubjectResponse{
			Scope: UsageScopeTenant, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
			ID: ptr("01a10c9e-4444-7e77-be6a-3cdc8d0b9401"), Name: "General", BookedName: nil,
			Window: usageFixtureWindow, Totals: usageFixtureAgentTotals(), Daily: usageFixtureDaily("12.04", "8.4", 120, 86),
		},
		Models: usageFixtureServed(),
	}
}

func usageUserRow(id, name *string, t UsageSubjectTotals) UsageRowResponse {
	return UsageRowResponse{
		ID: id, Name: name, BookedName: nil,
		Cost: t.Cost, Activities: t.Activities, CostPerActivity: t.CostPerActivity, ShareOfSpend: t.ShareOfSpend,
		CostByBucket: t.CostByBucket, Tokens: t.Tokens, Calls: t.Calls, UnpricedCalls: t.UnpricedCalls,
	}
}

func usageUsersListFixture() PaginatedResponse[UsageUserRowResponse] {
	pipeline := UsageSubjectTotals{
		Cost: money("130.5"), Activities: 3_200, CostPerActivity: moneyPtr("0.04078125"), ShareOfSpend: ptr(pct("130.5", usageFixtureMeshCost)),
		CostByBucket: usageFixtureBuckets("80", "40.5", "6", "4"), Calls: 9_400,
	}
	unlimited := UsageSubjectTotals{
		Cost: money("88"), Activities: 41, CostPerActivity: moneyPtr("2.1463414634"), ShareOfSpend: ptr(pct("88", usageFixtureMeshCost)),
		CostByBucket: usageFixtureBuckets("50", "38", "0", "0"), Calls: 610,
	}
	unattributed := UsageSubjectTotals{
		Cost: money("3.1"), Activities: 4, CostPerActivity: moneyPtr("0.775"), ShareOfSpend: ptr(pct("3.1", usageFixtureMeshCost)),
		CostByBucket: usageFixtureBuckets("2", "1.1", "0", "0"), Calls: 41,
	}
	rows := []UsageUserRowResponse{
		{
			UsageRowResponse: usageUserRow(ptr("01a10c9e-2222-7e77-be6a-3cdc8d0b9201"), ptr("Parminder Procurement"), usageFixtureUserTotals(UsageScopeTenant)),
			User:             &UsageUser{Email: ptr("parminder@example.com"), IsSystemUser: false},
			Budget:           usageFixtureUserBudget(),
			BudgetUsage:      &UsageBudgetWindow{WindowBudget: money("450"), UsagePercent: pct(usageFixtureUserCost, "450")},
		},
		{
			UsageRowResponse: usageUserRow(ptr("01a10c9e-2222-7e77-be6a-3cdc8d0b9202"), ptr("Nightly Pipeline"), pipeline),
			User:             &UsageUser{Email: nil, IsSystemUser: true},
			Budget: &UsageBudget{
				ID: "01a10c9e-0000-7e77-be6a-3cdc8d0b9003", Name: "Standard", Source: UsageBudgetDefault,
				Amount: moneyPtr("100"), WarningPercentage: ptr(80),
				TopUp:           &UsageTopUp{ID: "01a10c9e-0000-7e77-be6a-3cdc8d0b9004", Amount: money("25"), ExpiresTime: usageFixturePeriod[1]},
				EffectiveAmount: moneyPtr("125"),
				PeriodStart:     usageFixturePeriod[0], PeriodEnd: usageFixturePeriod[1],
				PeriodSpend: money("130.5"), UsagePercent: ptr(pct("130.5", "125")), Status: UsageBudgetExceeded,
			},
			BudgetUsage: &UsageBudgetWindow{WindowBudget: money("125"), UsagePercent: pct("130.5", "125")},
		},
		{
			UsageRowResponse: usageUserRow(ptr("01a10c9e-2222-7e77-be6a-3cdc8d0b9203"), ptr("Uma Unlimited"), unlimited),
			User:             &UsageUser{Email: ptr("uma@example.com"), IsSystemUser: false},
			Budget: &UsageBudget{
				ID: "01a10c9e-0000-7e77-be6a-3cdc8d0b9005", Name: "Research", Source: UsageBudgetAssigned,
				PeriodStart: usageFixturePeriod[0], PeriodEnd: usageFixturePeriod[1],
				PeriodSpend: money("88"), Status: UsageBudgetUnlimited,
			},
			BudgetUsage: nil,
		},
		{
			UsageRowResponse: usageUserRow(nil, nil, unattributed),
			User:             nil,
			Budget:           usageFixtureBareSubjectBudget("3.1"),
			BudgetUsage:      &UsageBudgetWindow{WindowBudget: money("100"), UsagePercent: pct("3.1", "100")},
		},
	}
	return PaginatedResponse[UsageUserRowResponse]{Data: rows, Meta: NewPaginationMeta(1, 25, 4)}
}

func usageAgentDetailFixture() UsageSubjectResponse {
	return UsageSubjectResponse{
		Scope: UsageScopeTenant, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
		ID: ptr("01a10c9e-1111-7e77-be6a-3cdc8d0b9101"), Name: "QA Tester", BookedName: nil,
		Window: usageFixtureWindow, Totals: usageFixtureAgentTotals(), Daily: usageFixtureDaily("0.74", "1.02", 9, 12),
	}
}

func usageWorkflowTotals() UsageSubjectTotals {
	return UsageSubjectTotals{
		Cost: money("57.4"), Activities: 286, CostPerActivity: moneyPtr("0.2006993006"), ShareOfSpend: ptr(pct("57.4", usageFixtureMeshCost)),
		CostByBucket: usageFixtureBuckets("30", "25.4", "1", "1"),
		Tokens:       UsageTokens{UncachedInput: 4_400_000, Output: 330_000, CacheRead: 120_000, CacheWrite: 9_000},
		Calls:        2_002,
	}
}

func usageWorkflowDetailFixture() UsageWorkflowResponse {
	return UsageWorkflowResponse{
		Scope: UsageScopeTenant, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
		ID: ptr("01a10c9e-3333-7e77-be6a-3cdc8d0b9301"), Name: "Expense Audit", BookedName: nil,
		Window: usageFixtureWindow, ComparisonWindow: &usageFixtureComparison,
		Totals: usageWorkflowTotals(), Daily: usageFixtureDaily("1.8", "2.2", 9, 11),
		Nodes: []UsageWorkflowNode{
			{NodeID: "code_assistant", Name: "Code Assistant", Kind: ptr(UsageNodeAgent), Cost: money("32.97"), ShareOfWorkflow: pct("32.97", "57.4"), ChangePercent: ptr(pct("1.87", "31.1"))},
			{NodeID: "review", Name: "Policy Reviewer", Kind: ptr(UsageNodeAgent), Cost: money("24.03"), ShareOfWorkflow: pct("24.03", "57.4"), ChangePercent: nil},
			{NodeID: "notify", Name: "send_email", Kind: ptr(UsageNodeTool), Cost: money("0"), ShareOfWorkflow: 0, ChangePercent: nil},
			{NodeID: "old_step", Name: "old_step", Kind: nil, Cost: money("0.4"), ShareOfWorkflow: pct("0.4", "57.4"), ChangePercent: nil},
		},
	}
}

func usageWorkflowByNameFixture() UsageWorkflowResponse {
	return UsageWorkflowResponse{
		Scope: UsageScopeTenant, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
		ID: nil, Name: "Nightly Report", BookedName: ptr("nightly-report"),
		Window:           UsageWindow{StartTime: at(2026, time.July, 18, 0, 0), EndTime: at(2026, time.October, 15, 19, 20)},
		ComparisonWindow: nil,
		Totals:           usageWorkflowTotals(), Daily: usageFixtureDaily("0.6", "0.7", 3, 3),
		Nodes: []UsageWorkflowNode{
			{NodeID: "summarize", Name: "summarize", Kind: nil, Cost: money("57.4"), ShareOfWorkflow: 100, ChangePercent: nil},
		},
	}
}

func usageFixtureUserDetail(scope UsageScope) UsageUserResponse {
	return UsageUserResponse{
		Scope: scope, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
		ID: ptr("01a10c9e-2222-7e77-be6a-3cdc8d0b9201"), Name: ptr("Parminder Procurement"),
		User:   &UsageUser{Email: ptr("parminder@example.com"), IsSystemUser: false},
		Window: usageFixtureWindow, Totals: usageFixtureUserTotals(scope), Daily: usageFixtureDaily("30.1", "27.4", 9, 8),
		Budget: usageFixtureUserBudget(),
		TopRoots: []UsageRoot{
			{Kind: UsageRootAgent, ID: ptr("01a10c9e-1111-7e77-be6a-3cdc8d0b9102"), Name: "Customer Support", Cost: money("193.16"), ShareOfUser: pct("193.16", usageFixtureUserCost)},
			{Kind: UsageRootWorkflow, ID: nil, BookedName: ptr("nightly-report"), Name: "Nightly Report", Cost: money("138.32"), ShareOfUser: pct("138.32", usageFixtureUserCost)},
		},
		OtherCost: money("80.82"), OtherShareOfUser: pct("80.82", usageFixtureUserCost),
	}
}

func usageUserDetailFixture() UsageUserResponse { return usageFixtureUserDetail(UsageScopeTenant) }

func usageSelfFixture() UsageSelfResponse {
	return UsageSelfResponse{
		UsageUserResponse: usageFixtureUserDetail(UsageScopeSelf),
		Months: []UsageMonth{
			{Month: "2026-08", Cost: money("51.2"), BudgetAmount: moneyPtr("450"), PartialFromDate: ptr("2026-08-12")},
			{Month: "2026-09", Cost: money("49.75"), BudgetAmount: moneyPtr("450"), PartialFromDate: nil},
			{Month: "2026-10", Cost: money(usageFixtureUserCost), BudgetAmount: moneyPtr("450"), PartialFromDate: nil},
		},
	}
}

func usageSavingsFixture() UsageSavingsResponse {
	return UsageSavingsResponse{
		Scope: UsageScopeTenant, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
		Window: usageFixtureWindow, Currency: "USD",
		AvoidedInputTokens: new(int64(1_750_000)), AvoidedCost: moneyPtr("5.25"),
		Calls: 1_200, MeasuredCalls: 1_180, UnpricedCalls: 20,
	}
}

func usageSavingsUnpricedFixture() UsageSavingsResponse {
	return UsageSavingsResponse{
		Scope: UsageScopeTenant, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
		Window: usageFixtureWindow, Currency: "USD",
		AvoidedInputTokens: new(int64(250_000)), AvoidedCost: nil,
		Calls: 60, MeasuredCalls: 40, UnpricedCalls: 40,
	}
}

func usageSavingsBuildingFixture() UsageSavingsResponse {
	return UsageSavingsResponse{
		Scope: UsageScopeTenant, DataAsOf: nil, HistoryComplete: false,
		Window: usageFixtureWindow, Currency: "USD",
		AvoidedInputTokens: nil, AvoidedCost: nil,
		Calls: 0, MeasuredCalls: 0, UnpricedCalls: 0,
	}
}

func usageSelfSavingsFixture() UsageSavingsResponse {
	return UsageSavingsResponse{
		Scope: UsageScopeSelf, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
		Window: usageFixtureWindow, Currency: "USD",
		AvoidedInputTokens: new(int64(12_000)), AvoidedCost: moneyPtr("0.036"),
		Calls: 9, MeasuredCalls: 9, UnpricedCalls: 0,
	}
}

func usageSelfUnattributedFixture() UsageSelfResponse {
	return UsageSelfResponse{
		UsageUserResponse: UsageUserResponse{
			Scope: UsageScopeSelf, DataAsOf: usageFixtureDataAsOf, HistoryComplete: true,
			ID: nil, Name: nil, User: nil,
			Window: usageFixtureWindow, Totals: UsageSubjectTotals{}, Daily: usageFixtureDaily("0", "0", 0, 0),
			Budget: usageFixtureBareSubjectBudget("0"), TopRoots: []UsageRoot{}, OtherCost: money("0"), OtherShareOfUser: 0,
		},
		Months: []UsageMonth{
			{Month: "2026-08", Cost: money("3.1"), BudgetAmount: moneyPtr("100"), PartialFromDate: nil},
			{Month: "2026-09", Cost: money("0"), BudgetAmount: moneyPtr("100"), PartialFromDate: nil},
			{Month: "2026-10", Cost: money("0"), BudgetAmount: moneyPtr("100"), PartialFromDate: nil},
		},
	}
}
