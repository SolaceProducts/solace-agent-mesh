// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"math"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func dollars(n int64) int64 { return n * 10_000_000_000 }

func amount(n int64) *int64 { return new(dollars(n)) }

var october = time.Date(2026, time.October, 20, 15, 0, 0, 0, time.UTC)

func utcDay(year int, month time.Month, d int) int64 {
	return time.Date(year, month, d, 0, 0, 0, 0, time.UTC).UnixMilli()
}

func TestStanding_StatusIsMeasuredAgainstTheEffectiveCap(t *testing.T) {
	topped := Budget{Amount: amount(400), EffectiveAmount: amount(450), WarnPercentage: new(80), TopUp: &TopUp{Amount: dollars(50)}}
	for name, tc := range map[string]struct {
		b     Budget
		spend int64
		want  Status
		pct   *float64
	}{
		"under the warning":               {b: topped, spend: dollars(300), want: StatusOK, pct: new(300.0 / 450 * 100)},
		"at 80% of the effective cap":     {b: topped, spend: dollars(360), want: StatusApproaching, pct: new(80.0)},
		"past the base amount, under cap": {b: topped, spend: dollars(420), want: StatusApproaching, pct: new(420.0 / 450 * 100)},
		"at the cap":                      {b: topped, spend: dollars(450), want: StatusExceeded, pct: new(100.0)},
		"no warning percentage":           {b: Budget{Amount: amount(100), EffectiveAmount: amount(100)}, spend: dollars(99), want: StatusOK, pct: new(99.0)},
		"unlimited":                       {b: Budget{}, spend: dollars(1_000), want: StatusUnlimited},
		"a zero budget with spend":        {b: Budget{Amount: amount(0), EffectiveAmount: amount(0)}, spend: 1, want: StatusExceeded, pct: new(100.0)},
		"a zero budget, nothing spent":    {b: Budget{Amount: amount(0), EffectiveAmount: amount(0), WarnPercentage: new(80)}, spend: 0, want: StatusExceeded, pct: new(100.0)},
		"a top-up on an unlimited budget": {b: Budget{TopUp: &TopUp{Amount: dollars(50)}}, spend: dollars(10), want: StatusUnlimited},
	} {
		t.Run(name, func(t *testing.T) {
			got := tc.b.Standing(tc.spend, october)
			if got.Status != tc.want {
				t.Errorf("status = %s, want %s", got.Status, tc.want)
			}
			if (got.UsagePercent == nil) != (tc.pct == nil) || (tc.pct != nil && !near(*got.UsagePercent, *tc.pct)) {
				t.Errorf("usage = %v, want %v", deref(got.UsagePercent), deref(tc.pct))
			}
			if got.PeriodStart != utcDay(2026, time.October, 1) || got.PeriodEnd != utcDay(2026, time.November, 1) || got.PeriodSpend != tc.spend {
				t.Errorf("period = [%d, %d) spend %d, want October's", got.PeriodStart, got.PeriodEnd, got.PeriodSpend)
			}
		})
	}
}

func TestWindowUsage_ProratesTheBudgetToTheWindow(t *testing.T) {
	b := Budget{Amount: amount(310), EffectiveAmount: amount(372)}
	for name, tc := range map[string]struct {
		from, to int64
		cost     int64
		want     int64
	}{
		"a window ending today, as pace": {from: utcDay(2026, 10, 1), to: utcDay(2026, 10, 21), want: dollars(372) * 20 / 31},
		"this month as a calendar month": {from: utcDay(2026, 10, 1), to: utcDay(2026, 11, 1), want: dollars(372)},
		"last month":                     {from: utcDay(2026, 9, 1), to: utcDay(2026, 10, 1), want: dollars(310)},
		"seven days across the month boundary": {
			from: utcDay(2026, 9, 27), to: utcDay(2026, 10, 4),
			want: dollars(310)*4/30 + dollars(372)*3/31,
		},
		"two whole past months":          {from: utcDay(2026, 8, 1), to: utcDay(2026, 10, 1), want: dollars(620)},
		"across a year boundary":         {from: utcDay(2025, 12, 30), to: utcDay(2026, 1, 3), want: dollars(310)*2/31 + dollars(310)*2/31},
		"a leap February":                {from: utcDay(2028, 2, 1), to: utcDay(2028, 3, 1), want: dollars(310)},
		"half a leap February":           {from: utcDay(2028, 2, 1), to: utcDay(2028, 2, 15), want: dollars(310) * 14 / 29},
		"next month, at the base amount": {from: utcDay(2026, 11, 1), to: utcDay(2026, 12, 1), want: dollars(310)},
	} {
		t.Run(name, func(t *testing.T) {
			got := b.WindowUsage(tc.cost, tc.from, tc.to, october)
			if got.WindowBudget == nil || absDiff(*got.WindowBudget, tc.want) > 1 {
				t.Errorf("window budget = %v, want %d", deref(got.WindowBudget), tc.want)
			}
		})
	}
	usage := b.WindowUsage(dollars(186), utcDay(2026, 10, 1), utcDay(2026, 11, 1), october)
	if usage.UsagePercent == nil || !near(*usage.UsagePercent, 50) {
		t.Errorf("usage of half October's effective cap = %v, want 50", deref(usage.UsagePercent))
	}
	if got := (Budget{}).WindowUsage(dollars(5), utcDay(2026, 10, 1), utcDay(2026, 11, 1), october); got.WindowBudget != nil || got.UsagePercent != nil {
		t.Errorf("unlimited window usage = %+v, want none", got)
	}
	zero := Budget{Amount: amount(0), EffectiveAmount: amount(0)}
	if got := zero.WindowUsage(0, utcDay(2026, 10, 1), utcDay(2026, 11, 1), october); got.WindowBudget == nil || *got.WindowBudget != 0 || got.UsagePercent == nil || *got.UsagePercent != 100 {
		t.Errorf("zero budget window usage = %+v, want a zero window budget used up, as enforcement blocks it", got)
	}
	if got := b.WindowUsage(dollars(5), utcDay(2026, 10, 2), utcDay(2026, 10, 1), october); got.WindowBudget != nil {
		t.Errorf("reversed window usage = %+v, want none", got)
	}
	huge := Budget{Amount: new(int64(math.MaxInt64 / 2)), EffectiveAmount: new(int64(math.MaxInt64 / 2))}
	if got := huge.WindowUsage(0, utcDay(2026, 7, 1), utcDay(2026, 10, 1), october); got.WindowBudget == nil || *got.WindowBudget != math.MaxInt64 {
		t.Errorf("three months of a near-maximal budget = %v, want it saturated", deref(got.WindowBudget))
	}
}

func TestSortUsers_RanksBudgetedFirstInEitherDirection(t *testing.T) {
	row := func(id, sub string) store.UsageRow {
		if id != "" {
			return store.UsageRow{SubjectID: &id, Name: id}
		}
		return store.UsageRow{SubjectName: &sub, Name: sub}
	}
	rows := []store.UsageRow{row("", "sub-only"), row("none", ""), row("unlimited", ""), row("high", ""), row("low", ""), row("mid", ""), row("idle", ""), row("twin", "")}
	budgets := map[string]Budget{
		"unlimited": {}, "high": {EffectiveAmount: amount(10)}, "low": {EffectiveAmount: amount(1)}, "mid": {EffectiveAmount: amount(5)},
		"idle": {EffectiveAmount: amount(3)}, "twin": {EffectiveAmount: amount(5)},
	}
	usage := map[string]WindowUsage{
		"i:high": {UsagePercent: new(90.0)}, "i:low": {UsagePercent: new(10.0)}, "i:mid": {UsagePercent: new(50.0)}, "i:twin": {UsagePercent: new(50.0)},
	}
	names := func(rows []store.UsageRow) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Name
		}
		return out
	}
	for name, tc := range map[string]struct {
		by   BudgetSort
		desc bool
		want []string
	}{
		"usage, most first, ties as they came":          {by: SortByBudgetUsage, desc: true, want: []string{"high", "mid", "twin", "low", "idle", "unlimited", "none", "sub-only"}},
		"usage, least first, a capped idler at 0%":      {by: SortByBudgetUsage, want: []string{"idle", "low", "mid", "twin", "high", "unlimited", "none", "sub-only"}},
		"budget, most first, unlimited the largest":     {by: SortByBudget, desc: true, want: []string{"unlimited", "high", "mid", "twin", "idle", "low", "none", "sub-only"}},
		"budget, least first, unlimited after the caps": {by: SortByBudget, want: []string{"low", "idle", "mid", "twin", "high", "unlimited", "none", "sub-only"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := names(SortUsers(rows, UserBudgets{ByID: budgets}, usage, tc.by, tc.desc))
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("order = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestSortUsers_RanksABareSubjectByTheTierDefault(t *testing.T) {
	sub, high, low := "bare", "high", "low"
	rows := []store.UsageRow{{SubjectName: &sub, Name: sub}, {SubjectID: &high, Name: high}, {SubjectID: &low, Name: low}}
	budgets := UserBudgets{
		ByID: map[string]Budget{high: {EffectiveAmount: amount(10)}, low: {EffectiveAmount: amount(1)}},
		Bare: &Budget{EffectiveAmount: amount(5)},
	}
	usage := map[string]WindowUsage{
		"i:high": {UsagePercent: new(90.0)}, "i:low": {UsagePercent: new(10.0)}, "n:bare": {UsagePercent: new(50.0)},
	}

	got := SortUsers(rows, budgets, usage, SortByBudgetUsage, true)

	if got[0].Name != high || got[1].Name != sub || got[2].Name != low {
		t.Errorf("order = %v, want high, bare, low", []string{got[0].Name, got[1].Name, got[2].Name})
	}
}

func TestSortUsers_KeepsTheStoresOrderForTies(t *testing.T) {
	row := func(id string) store.UsageRow { return store.UsageRow{SubjectID: new(id), Name: id} }
	rows := []store.UsageRow{row("c-most"), row("a-mid"), row("b-least")}

	got := SortUsers(rows, UserBudgets{}, nil, SortByBudgetUsage, true)

	if got[0].Name != "c-most" || got[1].Name != "a-mid" || got[2].Name != "b-least" {
		t.Errorf("order = %v, want the input's", []string{got[0].Name, got[1].Name, got[2].Name})
	}
}

type budgetFixture struct {
	t  *testing.T
	db *store.DB
}

func (f budgetFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f budgetFixture) user(sub string) string {
	id := idgen.New()
	f.exec(`INSERT INTO users (id, issuer, sub, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'https://idp', ?, 'test', 'test', 0, 0)`, id, sub)
	return id
}

func (f budgetFixture) budget(name string, amt *int64, warn *int) string {
	id := idgen.New()
	f.exec(`INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, 'user', ?, ?, 'test', 'test', 0, 0)`, id, name, name, amt, warn)
	return id
}

func (f budgetFixture) assign(user, budgetID string) {
	f.exec(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'user', ?, ?, 'test', 'test', 0, 0)`, idgen.New(), user, budgetID)
}

func (f budgetFixture) topUp(subjectType, ref string, amt int64, expires int64) string {
	id := idgen.New()
	f.exec(`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, 'test', 'test', 0, 0)`, id, subjectType, ref, amt, expires)
	return id
}

func (f budgetFixture) budgets() Budgets {
	return Budgets{
		Assignments: store.NewBudgetAssignmentsRepo(f.db), Budgets: store.NewBudgetsRepo(f.db), TopUps: store.NewBudgetTopUpsRepo(f.db),
	}
}

func TestBudgets_UsersResolveAsTheBudgetsAPIDoes(t *testing.T) {
	f := budgetFixture{t: t, db: store.TestDB(t)}
	f.exec(`UPDATE budgets SET amount = ?, warn_percentage = 75 WHERE subject_type = 'user' AND is_default = 1`, dollars(100))
	procurement := f.budget("Procurement", amount(450), new(80))
	unlimited := f.budget("No Cap", nil, nil)
	assigned, defaulted, uncapped, topped, lapsed := f.user("assigned"), f.user("defaulted"), f.user("uncapped"), f.user("topped"), f.user("lapsed")
	lapsingNow, lapsingNext := f.user("lapsing-now"), f.user("lapsing-next")
	inactive := f.user("inactive")
	f.exec(`UPDATE users SET active = 0 WHERE id = ?`, inactive)
	f.assign(assigned, procurement)
	f.assign(uncapped, unlimited)
	f.assign(topped, procurement)
	f.assign(lapsed, procurement)
	f.assign(lapsingNow, procurement)
	f.assign(lapsingNext, procurement)
	novemberFirst := utcDay(2026, 11, 1)
	activeTopUp := f.topUp("user", topped, dollars(50), novemberFirst)
	f.topUp("user", lapsed, dollars(50), utcDay(2026, 10, 1))
	f.topUp("user", lapsingNow, dollars(50), october.UnixMilli())
	lastMillisecond := f.topUp("user", lapsingNext, dollars(50), october.UnixMilli()+1)

	got, err := f.budgets().Users(t.Context(), nil, october)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	for name, tc := range map[string]struct {
		id        string
		budgetID  string
		source    budget.Source
		effective *int64
		warn      *int
		topUp     string
	}{
		"an assignment":            {id: assigned, budgetID: procurement, source: budget.SourceAssigned, effective: amount(450), warn: new(80)},
		"the tier default":         {id: defaulted, source: budget.SourceDefault, effective: amount(100), warn: new(75)},
		"an unlimited assignment":  {id: uncapped, budgetID: unlimited, source: budget.SourceAssigned},
		"an active top-up":         {id: topped, budgetID: procurement, source: budget.SourceAssigned, effective: amount(500), warn: new(80), topUp: activeTopUp},
		"a top-up that has lapsed": {id: lapsed, budgetID: procurement, source: budget.SourceAssigned, effective: amount(450), warn: new(80)},
		"a top-up lapsing now":     {id: lapsingNow, budgetID: procurement, source: budget.SourceAssigned, effective: amount(450), warn: new(80)},
		"a top-up in its last millisecond": {
			id: lapsingNext, budgetID: procurement, source: budget.SourceAssigned, effective: amount(500), warn: new(80), topUp: lastMillisecond,
		},
		"an inactive user": {id: inactive, source: budget.SourceDefault, effective: amount(100), warn: new(75)},
	} {
		t.Run(name, func(t *testing.T) {
			b, ok := got[tc.id]
			if !ok {
				t.Fatal("no budget resolved")
			}
			if tc.budgetID != "" && b.ID != tc.budgetID {
				t.Errorf("budget = %s, want %s", b.Name, tc.budgetID)
			}
			if b.Source != tc.source || deref(b.EffectiveAmount) != deref(tc.effective) || deref(b.WarnPercentage) != deref(tc.warn) {
				t.Errorf("budget = %+v (effective %v, warn %v), want source %s, effective %v, warn %v",
					b, deref(b.EffectiveAmount), deref(b.WarnPercentage), tc.source, deref(tc.effective), deref(tc.warn))
			}
			if (b.TopUp == nil) != (tc.topUp == "") || (b.TopUp != nil && b.TopUp.ID != tc.topUp) {
				t.Errorf("top-up = %+v, want %q", b.TopUp, tc.topUp)
			}
		})
	}

	only, err := f.budgets().Users(t.Context(), []string{assigned}, october)
	if err != nil || len(only) != 1 || deref(only[assigned].EffectiveAmount) != deref(got[assigned].EffectiveAmount) || only[assigned].ID != got[assigned].ID {
		t.Errorf("resolve one user = %+v, %v; want the same budget as resolving everyone", only, err)
	}
	none, err := f.budgets().Users(t.Context(), []string{}, october)
	if err != nil || len(none) != 0 {
		t.Errorf("resolve no users = %d budgets, %v; want none, not everyone", len(none), err)
	}
}

func TestBudgets_AUserOnATierWithNoDefaultHasNoBudget(t *testing.T) {
	f := budgetFixture{t: t, db: store.TestDB(t)}
	user := f.user("nobody")
	f.exec(`UPDATE budgets SET is_default = 0 WHERE subject_type = 'user'`)

	got, err := f.budgets().Users(t.Context(), []string{user}, october)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if b, ok := got[user]; ok {
		t.Errorf("budget = %+v, want none", b)
	}
}

func TestBudgets_TheMeshIsItsTierDefaultRaisedByItsTopUp(t *testing.T) {
	f := budgetFixture{t: t, db: store.TestDB(t)}
	f.exec(`UPDATE budgets SET amount = ?, warn_percentage = 80 WHERE subject_type = 'mesh'`, dollars(2_000))
	f.topUp("mesh", "mesh", dollars(400), utcDay(2026, 11, 1))

	got, err := f.budgets().Mesh(t.Context(), october)
	if err != nil {
		t.Fatalf("mesh: %v", err)
	}
	if got == nil || deref(got.Amount) != dollars(2_000) || deref(got.EffectiveAmount) != dollars(2_400) || got.TopUp == nil || deref(got.WarnPercentage) != 80 {
		t.Errorf("mesh = %+v, want $2,000 raised to $2,400 by its top-up, warning at 80%%", got)
	}
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
