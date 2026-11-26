// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"math/big"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type Budget struct {
	ID              string
	Name            string
	Amount          *int64
	WarnPercentage  *int
	TopUp           *TopUp
	EffectiveAmount *int64
	Source          budget.Source
}

type TopUp struct {
	ID          string
	Amount      int64
	ExpiresTime int64
}

type Status string

const (
	StatusOK          Status = "ok"
	StatusApproaching Status = "approaching"
	StatusExceeded    Status = "exceeded"
	StatusUnlimited   Status = "unlimited"
)

type Standing struct {
	PeriodStart  int64
	PeriodEnd    int64
	PeriodSpend  int64
	UsagePercent *float64
	Status       Status
}

func (b Budget) Standing(periodSpend int64, now time.Time) Standing {
	s := Standing{
		PeriodStart: budget.PeriodStart(now).UnixMilli(),
		PeriodEnd:   budget.PeriodEnd(now).UnixMilli(),
		PeriodSpend: periodSpend,
		Status:      StatusUnlimited,
	}
	if b.EffectiveAmount == nil {
		return s
	}
	limit := *b.EffectiveAmount
	s.UsagePercent = percent(periodSpend, limit)
	switch {
	case periodSpend >= limit:
		s.Status = StatusExceeded
	case b.WarnPercentage != nil && periodSpend >= budget.WarnThreshold(limit, *b.WarnPercentage):
		s.Status = StatusApproaching
	default:
		s.Status = StatusOK
	}
	return s
}

type WindowUsage struct {
	WindowBudget *int64
	UsagePercent *float64
}

func (b Budget) WindowUsage(windowCost, fromDay, toDay int64, now time.Time) WindowUsage {
	if b.Amount == nil || b.EffectiveAmount == nil || toDay <= fromDay {
		return WindowUsage{}
	}
	current := budget.PeriodStart(now)
	total := new(big.Rat)
	for month := budget.PeriodStart(time.UnixMilli(fromDay)); month.UnixMilli() < toDay; month = month.AddDate(0, 1, 0) {
		next := month.AddDate(0, 1, 0)
		from, to := max(month.UnixMilli(), fromDay), min(next.UnixMilli(), toDay)
		amount := *b.Amount
		if month.Equal(current) {
			amount = *b.EffectiveAmount
		}
		share := big.NewRat((to-from)/dayMillis, (next.UnixMilli()-month.UnixMilli())/dayMillis)
		total.Add(total, share.Mul(share, new(big.Rat).SetInt64(amount)))
	}
	windowBudget := roundRat(total)
	return WindowUsage{WindowBudget: &windowBudget, UsagePercent: percent(windowCost, windowBudget)}
}

func percent(spend, limit int64) *float64 {
	p := float64(100)
	if limit > 0 {
		p = float64(spend) / float64(limit) * 100
	}
	return &p
}

func roundRat(r *big.Rat) int64 {
	q, m := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if new(big.Int).Mul(m, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return math.MaxInt64
	}
	return q.Int64()
}

type Budgets struct {
	Assignments *store.BudgetAssignmentsRepo
	Budgets     *store.BudgetsRepo
	TopUps      *store.BudgetTopUpsRepo
}

func (b Budgets) Users(ctx context.Context, ids []string, now time.Time) (map[string]Budget, error) {
	if ids != nil && len(ids) == 0 {
		return map[string]Budget{}, nil
	}
	byID, tierDefault, err := b.userTier(ctx)
	if err != nil {
		return nil, err
	}
	return b.applied(ctx, ids, now, byID, tierDefault)
}

func (b Budgets) ForUsers(ctx context.Context, ids []string, now time.Time) (UserBudgets, error) {
	byID, tierDefault, err := b.userTier(ctx)
	if err != nil {
		return UserBudgets{}, err
	}
	budgets := UserBudgets{ByID: map[string]Budget{}}
	if bare, ok := resolveBudget(nil, tierDefault, nil); ok {
		budgets.Bare = &bare
	}
	if ids != nil && len(ids) == 0 {
		return budgets, nil
	}
	if budgets.ByID, err = b.applied(ctx, ids, now, byID, tierDefault); err != nil {
		return UserBudgets{}, err
	}
	return budgets, nil
}

func (b Budgets) applied(ctx context.Context, ids []string, now time.Time, byID map[string]*store.Budget, tierDefault *store.Budget) (map[string]Budget, error) {
	subjects, err := b.Assignments.ListUserSubjects(ctx, store.UserBudgetSubjectFilter{IDs: ids, NowMs: now.UnixMilli()})
	if err != nil {
		return nil, fmt.Errorf("resolve user budgets: %w", err)
	}
	applied := make(map[string]Budget, len(subjects))
	for _, s := range subjects {
		var assigned *store.Budget
		if s.BudgetID != nil {
			assigned = &store.Budget{ID: *s.BudgetID, Name: *s.BudgetName, Amount: s.BudgetAmount}
			if tiered, ok := byID[*s.BudgetID]; ok {
				assigned.WarnPercentage = tiered.WarnPercentage
			}
		}
		var topUp *TopUp
		if s.TopUpID != nil {
			topUp = &TopUp{ID: *s.TopUpID, Amount: *s.TopUpAmount, ExpiresTime: *s.TopUpExpiresTime}
		}
		if resolved, ok := resolveBudget(assigned, tierDefault, topUp); ok {
			applied[s.ID] = resolved
		}
	}
	return applied, nil
}

func (b Budgets) Default(ctx context.Context) (*Budget, error) {
	_, tierDefault, err := b.userTier(ctx)
	if err != nil {
		return nil, err
	}
	resolved, ok := resolveBudget(nil, tierDefault, nil)
	if !ok {
		return nil, nil
	}
	return &resolved, nil
}

type UserBudgets struct {
	ByID map[string]Budget
	Bare *Budget
}

func (u UserBudgets) For(row store.UsageRow) (Budget, bool) {
	switch {
	case row.SubjectID != nil:
		b, ok := u.ByID[*row.SubjectID]
		return b, ok
	case row.SubjectName != nil && u.Bare != nil:
		return *u.Bare, true
	}
	return Budget{}, false
}

func (b Budgets) Mesh(ctx context.Context, now time.Time) (*Budget, error) {
	defaults, err := b.Budgets.List(ctx, store.BudgetFilter{SubjectType: store.BudgetSubjectMesh, DefaultOnly: true, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("read the mesh budget: %w", err)
	}
	if len(defaults) == 0 {
		return nil, nil
	}
	active, err := b.TopUps.Active(ctx, store.BudgetSubjectMesh, store.BudgetMeshSubjectRef, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("read the mesh's top-up: %w", err)
	}
	var topUp *TopUp
	if active != nil {
		topUp = &TopUp{ID: active.ID, Amount: active.Amount, ExpiresTime: active.ExpiresTime}
	}
	resolved, _ := resolveBudget(nil, defaults[0], topUp)
	return &resolved, nil
}

func (b Budgets) userTier(ctx context.Context) (map[string]*store.Budget, *store.Budget, error) {
	budgets, err := b.Budgets.List(ctx, store.BudgetFilter{SubjectType: store.BudgetSubjectUser})
	if err != nil {
		return nil, nil, fmt.Errorf("read user budgets: %w", err)
	}
	byID := make(map[string]*store.Budget, len(budgets))
	var tierDefault *store.Budget
	for _, bud := range budgets {
		byID[bud.ID] = bud
		if bud.IsDefault {
			tierDefault = bud
		}
	}
	return byID, tierDefault, nil
}

func resolveBudget(assigned, tierDefault *store.Budget, topUp *TopUp) (Budget, bool) {
	applied, source := budget.Resolve(assigned, tierDefault)
	if applied == nil {
		return Budget{}, false
	}
	var topUpAmount int64
	if topUp != nil {
		topUpAmount = topUp.Amount
	}
	return Budget{
		ID: applied.ID, Name: applied.Name, Amount: applied.Amount, WarnPercentage: applied.WarnPercentage,
		TopUp: topUp, EffectiveAmount: budget.EffectiveCap(applied.Amount, topUpAmount), Source: source,
	}, true
}

type BudgetSort string

const (
	SortByBudget      BudgetSort = "budget"
	SortByBudgetUsage BudgetSort = "budgetUsage"
)

type budgetSortGroup int

const (
	groupBudgeted budgetSortGroup = iota
	groupUnlimited
	groupNoBudget
	groupNoUser
)

func SortUsers(rows []store.UsageRow, budgets UserBudgets, usage map[string]WindowUsage, by BudgetSort, descending bool) []store.UsageRow {
	type keyed struct {
		row   store.UsageRow
		group budgetSortGroup
		value float64
	}
	sorted := make([]keyed, len(rows))
	for i, row := range rows {
		k := keyed{row: row, group: groupNoUser}
		if row.SubjectID != nil {
			k.group = groupNoBudget
		}
		if b, ok := budgets.For(row); ok {
			k.group, k.value = budgetSortValue(b, usage[store.UsageRowKey(row)], by)
		}
		sorted[i] = k
	}
	slices.SortStableFunc(sorted, func(a, b keyed) int {
		if c := cmp.Compare(a.group, b.group); c != 0 {
			return c
		}
		if a.group == groupBudgeted {
			c := cmp.Compare(a.value, b.value)
			if descending {
				c = -c
			}
			return c
		}
		return 0
	})
	out := make([]store.UsageRow, len(sorted))
	for i, k := range sorted {
		out[i] = k.row
	}
	return out
}

func budgetSortValue(b Budget, u WindowUsage, by BudgetSort) (budgetSortGroup, float64) {
	switch {
	case by == SortByBudget && b.EffectiveAmount == nil:
		return groupBudgeted, math.Inf(1)
	case by == SortByBudget:
		return groupBudgeted, float64(*b.EffectiveAmount)
	case b.EffectiveAmount == nil:
		return groupUnlimited, 0
	case u.UsagePercent == nil:
		return groupBudgeted, 0
	}
	return groupBudgeted, *u.UsagePercent
}
