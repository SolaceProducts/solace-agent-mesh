// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type BudgetPeriodSubjectKind string

const (
	BudgetPeriodMesh    BudgetPeriodSubjectKind = "mesh"
	BudgetPeriodUserID  BudgetPeriodSubjectKind = "id"
	BudgetPeriodUserSub BudgetPeriodSubjectKind = "sub"
)

type BudgetPeriodSubject struct {
	Kind BudgetPeriodSubjectKind
	Ref  string
}

var BudgetPeriodMeshSubject = BudgetPeriodSubject{Kind: BudgetPeriodMesh}

type BudgetPeriodKey struct {
	Subject     BudgetPeriodSubject
	PeriodStart int64
}

type BudgetPeriodTotals struct {
	PricedCalls         int64
	UnpricedCalls       int64
	UncachedInputTokens int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheWriteTokens    int64
	UncachedInputCost   int64
	OutputCost          int64
	CacheReadCost       int64
	CacheWriteCost      int64
	TotalCost           int64
}

var budgetPeriodSumColumns = []string{
	"priced_calls", "unpriced_calls",
	"uncached_input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
	"uncached_input_cost", "output_cost", "cache_read_cost", "cache_write_cost",
	"total_cost",
}

func (t *BudgetPeriodTotals) fields() []int64 {
	return []int64{
		t.PricedCalls, t.UnpricedCalls,
		t.UncachedInputTokens, t.OutputTokens, t.CacheReadTokens, t.CacheWriteTokens,
		t.UncachedInputCost, t.OutputCost, t.CacheReadCost, t.CacheWriteCost,
		t.TotalCost,
	}
}

func (t *BudgetPeriodTotals) scanTargets() []any {
	return []any{
		&t.PricedCalls, &t.UnpricedCalls,
		&t.UncachedInputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens,
		&t.UncachedInputCost, &t.OutputCost, &t.CacheReadCost, &t.CacheWriteCost,
		&t.TotalCost,
	}
}

func (t *BudgetPeriodTotals) add(e *SpendLedgerEntry) {
	t.UncachedInputTokens = SaturatingAdd(t.UncachedInputTokens, e.UncachedInputTokens)
	t.OutputTokens = SaturatingAdd(t.OutputTokens, e.OutputTokens)
	t.CacheReadTokens = SaturatingAdd(t.CacheReadTokens, e.CacheReadTokens)
	t.CacheWriteTokens = SaturatingAdd(t.CacheWriteTokens, e.CacheWriteTokens)
	if e.Cost == nil {
		t.UnpricedCalls = SaturatingAdd(t.UnpricedCalls, 1)
		return
	}
	c := e.Cost
	t.PricedCalls = SaturatingAdd(t.PricedCalls, 1)
	t.UncachedInputCost = SaturatingAdd(t.UncachedInputCost, c.UncachedInput)
	t.OutputCost = SaturatingAdd(t.OutputCost, c.Output)
	t.CacheReadCost = SaturatingAdd(t.CacheReadCost, c.CacheRead)
	t.CacheWriteCost = SaturatingAdd(t.CacheWriteCost, c.CacheWrite)
	t.TotalCost = SaturatingAdd(t.TotalCost, SaturatingAdd(SaturatingAdd(c.UncachedInput, c.Output), SaturatingAdd(c.CacheRead, c.CacheWrite)))
}

func (t *BudgetPeriodTotals) addTotals(o BudgetPeriodTotals) {
	t.PricedCalls = SaturatingAdd(t.PricedCalls, o.PricedCalls)
	t.UnpricedCalls = SaturatingAdd(t.UnpricedCalls, o.UnpricedCalls)
	t.UncachedInputTokens = SaturatingAdd(t.UncachedInputTokens, o.UncachedInputTokens)
	t.OutputTokens = SaturatingAdd(t.OutputTokens, o.OutputTokens)
	t.CacheReadTokens = SaturatingAdd(t.CacheReadTokens, o.CacheReadTokens)
	t.CacheWriteTokens = SaturatingAdd(t.CacheWriteTokens, o.CacheWriteTokens)
	t.UncachedInputCost = SaturatingAdd(t.UncachedInputCost, o.UncachedInputCost)
	t.OutputCost = SaturatingAdd(t.OutputCost, o.OutputCost)
	t.CacheReadCost = SaturatingAdd(t.CacheReadCost, o.CacheReadCost)
	t.CacheWriteCost = SaturatingAdd(t.CacheWriteCost, o.CacheWriteCost)
	t.TotalCost = SaturatingAdd(t.TotalCost, o.TotalCost)
}

func SaturatingAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func budgetPeriodStartOf(ms int64) int64 {
	return budget.PeriodStart(time.UnixMilli(ms)).UnixMilli()
}

func budgetPeriodEndOf(periodStart int64) int64 {
	return budget.PeriodEnd(time.UnixMilli(periodStart)).UnixMilli()
}

func checkBudgetPeriodStart(periodStart int64) error {
	if budgetPeriodStartOf(periodStart) != periodStart {
		return fmt.Errorf("%d is not the start of a budget period", periodStart)
	}
	return nil
}

func budgetPeriodUserOf(e *SpendLedgerEntry) BudgetPeriodSubject {
	if e.UserID != nil {
		return BudgetPeriodSubject{Kind: BudgetPeriodUserID, Ref: *e.UserID}
	}
	return BudgetPeriodSubject{Kind: BudgetPeriodUserSub, Ref: *e.UserSub}
}

func sumUsersByPeriod(entries []*SpendLedgerEntry) map[BudgetPeriodKey]*BudgetPeriodTotals {
	sums := make(map[BudgetPeriodKey]*BudgetPeriodTotals)
	for _, e := range entries {
		key := BudgetPeriodKey{Subject: budgetPeriodUserOf(e), PeriodStart: budgetPeriodStartOf(e.CompletedTime)}
		t, ok := sums[key]
		if !ok {
			t = &BudgetPeriodTotals{}
			sums[key] = t
		}
		t.add(e)
	}
	return sums
}

func sumOfUsers(users budgetPeriodSums) BudgetPeriodTotals {
	var total BudgetPeriodTotals
	for subject, t := range users {
		if subject.Kind != BudgetPeriodMesh {
			total.addTotals(t)
		}
	}
	return total
}

func addToBudgetPeriods(ctx context.Context, tx *dbutil.Tx, entries []*SpendLedgerEntry) error {
	sums := sumUsersByPeriod(entries)
	if len(sums) == 0 {
		return nil
	}
	meshes := make(map[int64]*BudgetPeriodTotals)
	for key, t := range sums {
		mesh, ok := meshes[key.PeriodStart]
		if !ok {
			mesh = &BudgetPeriodTotals{}
			meshes[key.PeriodStart] = mesh
		}
		mesh.addTotals(*t)
	}
	for periodStart, mesh := range meshes {
		sums[BudgetPeriodKey{Subject: BudgetPeriodMeshSubject, PeriodStart: periodStart}] = mesh
	}
	return upsertBudgetPeriods(ctx, tx, sums)
}

func upsertBudgetPeriods(ctx context.Context, tx *dbutil.Tx, sums map[BudgetPeriodKey]*BudgetPeriodTotals) error {
	keys := slices.SortedFunc(maps.Keys(sums), func(a, b BudgetPeriodKey) int {
		return cmp.Or(
			cmp.Compare(a.Subject.Kind, b.Subject.Kind),
			cmp.Compare(a.Subject.Ref, b.Subject.Ref),
			cmp.Compare(a.PeriodStart, b.PeriodStart))
	})
	columnCount := 4 + len(budgetPeriodSumColumns)
	args := make([]any, 0, len(keys)*columnCount)
	for _, key := range keys {
		args = append(args, string(key.Subject.Kind), key.Subject.Ref, key.PeriodStart, budgetPeriodEndOf(key.PeriodStart))
		for _, v := range sums[key].fields() {
			args = append(args, v)
		}
	}
	updates := make([]string, len(budgetPeriodSumColumns))
	for i, c := range budgetPeriodSumColumns {
		updates[i] = fmt.Sprintf(
			"%[1]s = CASE WHEN token_spend_budget_period.%[1]s > %[2]d - excluded.%[1]s THEN %[2]d ELSE token_spend_budget_period.%[1]s + excluded.%[1]s END",
			c, int64(math.MaxInt64))
	}
	query := `INSERT INTO token_spend_budget_period (subject_kind, subject_ref, period_start, period_end, ` + strings.Join(budgetPeriodSumColumns, ", ") + `)
		VALUES ` + valuesRows(columnCount, len(keys)) + `
		ON CONFLICT (subject_kind, subject_ref, period_start) DO UPDATE SET ` + strings.Join(updates, ", ")
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("add to budget period spend: %w", err)
	}
	return nil
}

func (r *SpendLedgerRepo) DeleteBudgetPeriodsEndedBy(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM token_spend_budget_period WHERE period_end <= ?`, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("SpendLedgerRepo.DeleteBudgetPeriodsEndedBy: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("SpendLedgerRepo.DeleteBudgetPeriodsEndedBy: rows affected: %w", err)
	}
	return n, nil
}

func (r *SpendLedgerRepo) PeriodCost(ctx context.Context, subject BudgetPeriodSubject, periodStart int64) (int64, error) {
	if err := checkBudgetPeriodStart(periodStart); err != nil {
		return 0, fmt.Errorf("SpendLedgerRepo.PeriodCost: %w", err)
	}
	cost, err := dbutil.QueryVal[int64](ctx, r.db,
		`SELECT total_cost FROM token_spend_budget_period WHERE subject_kind = ? AND subject_ref = ? AND period_start = ?`,
		string(subject.Kind), subject.Ref, periodStart)
	if err != nil {
		return 0, fmt.Errorf("SpendLedgerRepo.PeriodCost: %w", err)
	}
	return cost, nil
}

func (s BudgetPeriodSubject) UserKey() string {
	switch s.Kind {
	case BudgetPeriodUserID:
		return budget.UserKey(s.Ref, "")
	case BudgetPeriodUserSub:
		return budget.UserKey("", s.Ref)
	default:
		return ""
	}
}

func (r *SpendLedgerRepo) PeriodCostsByUser(ctx context.Context, periodStart int64) (map[BudgetPeriodSubject]int64, error) {
	if err := checkBudgetPeriodStart(periodStart); err != nil {
		return nil, fmt.Errorf("SpendLedgerRepo.PeriodCostsByUser: %w", err)
	}
	type row struct {
		Kind string `db:"subject_kind"`
		Ref  string `db:"subject_ref"`
		Cost int64  `db:"total_cost"`
	}
	rows, err := dbutil.QueryRows[row](ctx, r.db,
		`SELECT subject_kind, subject_ref, total_cost FROM token_spend_budget_period
		WHERE period_start = ? AND subject_kind <> ?`, periodStart, string(BudgetPeriodMesh))
	if err != nil {
		return nil, fmt.Errorf("SpendLedgerRepo.PeriodCostsByUser: %w", err)
	}
	costs := make(map[BudgetPeriodSubject]int64, len(rows))
	for _, row := range rows {
		costs[BudgetPeriodSubject{Kind: BudgetPeriodSubjectKind(row.Kind), Ref: row.Ref}] = row.Cost
	}
	return costs, nil
}

type BudgetPeriodMeshCheck struct {
	PeriodStart int64
	Mesh        BudgetPeriodTotals
	Users       BudgetPeriodTotals
}

func (c BudgetPeriodMeshCheck) Consistent() bool {
	return c.Mesh == c.Users
}

func (c BudgetPeriodMeshCheck) Columns() []string {
	return differingColumns(c.Mesh, c.Users)
}

func (r *SpendLedgerRepo) CheckMeshPeriod(ctx context.Context, periodStart int64) (BudgetPeriodMeshCheck, error) {
	check := BudgetPeriodMeshCheck{PeriodStart: periodStart}
	mesh, users, err := r.meshAndUserSums(ctx, periodStart)
	if err != nil {
		return check, fmt.Errorf("SpendLedgerRepo.CheckMeshPeriod: %w", err)
	}
	check.Mesh, check.Users = mesh, users
	return check, nil
}

func (r *SpendLedgerRepo) meshAndUserSums(ctx context.Context, periodStart int64) (mesh, users BudgetPeriodTotals, err error) {
	if startErr := checkBudgetPeriodStart(periodStart); startErr != nil {
		return mesh, users, startErr
	}
	var rows *sql.Rows
	if r.db.Dialect() == "postgres" {
		capped := make([]string, len(budgetPeriodSumColumns))
		for i, c := range budgetPeriodSumColumns {
			capped[i] = postgresCappedSum(c)
		}
		rows, err = r.db.QueryContext(ctx,
			`SELECT CASE WHEN subject_kind = 'mesh' THEN 'mesh' ELSE 'users' END, '', `+strings.Join(capped, ", ")+`
			FROM token_spend_budget_period WHERE period_start = ?
			GROUP BY CASE WHEN subject_kind = 'mesh' THEN 'mesh' ELSE 'users' END`, periodStart)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT subject_kind, subject_ref, `+strings.Join(budgetPeriodSumColumns, ", ")+`
			FROM token_spend_budget_period WHERE period_start = ?`, periodStart)
	}
	if err != nil {
		return mesh, users, err
	}
	sums, err := scanBudgetPeriodSums(rows)
	if err != nil {
		return mesh, users, err
	}
	return sums[BudgetPeriodMeshSubject], sumOfUsers(sums), nil
}

type BudgetPeriodDrift struct {
	Subject BudgetPeriodSubject
	Stored  BudgetPeriodTotals
	Ledger  BudgetPeriodTotals
}

func (d BudgetPeriodDrift) Columns() []string {
	return differingColumns(d.Stored, d.Ledger)
}

func differingColumns(a, b BudgetPeriodTotals) []string {
	af, bf := a.fields(), b.fields()
	var columns []string
	for i, c := range budgetPeriodSumColumns {
		if af[i] != bf[i] {
			columns = append(columns, c)
		}
	}
	return columns
}

func (r *SpendLedgerRepo) PeriodDrift(ctx context.Context, periodStart int64, timeout time.Duration) ([]BudgetPeriodDrift, error) {
	if err := checkBudgetPeriodStart(periodStart); err != nil {
		return nil, fmt.Errorf("SpendLedgerRepo.PeriodDrift: %w", err)
	}
	var drift []BudgetPeriodDrift
	err := r.db.WithBoundedReadSnapshot(ctx, timeout, func(tx *dbutil.Tx) error {
		ledger, err := ledgerPeriod(ctx, tx, periodStart, budgetPeriodEndOf(periodStart))
		if err != nil {
			return err
		}
		ledger[BudgetPeriodMeshSubject] = sumOfUsers(ledger)
		stored, err := storedPeriod(ctx, tx, periodStart)
		if err != nil {
			return err
		}
		drift = compareBudgetPeriods(stored, ledger)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("SpendLedgerRepo.PeriodDrift: %w", err)
	}
	return drift, nil
}

type budgetPeriodSums map[BudgetPeriodSubject]BudgetPeriodTotals

func postgresCappedSum(expr string) string {
	return fmt.Sprintf("CAST(LEAST(%[2]d, COALESCE(SUM(%[1]s), 0)) AS BIGINT)", expr, int64(math.MaxInt64))
}

func ledgerPeriod(ctx context.Context, tx *dbutil.Tx, from, to int64) (budgetPeriodSums, error) {
	if tx.Dialect() != "postgres" {
		return ledgerPeriodInGo(ctx, tx, from, to)
	}
	capped := postgresCappedSum
	sums := []string{
		capped("CASE WHEN total_cost IS NULL THEN 0 ELSE 1 END"),
		capped("CASE WHEN total_cost IS NULL THEN 1 ELSE 0 END"),
		capped("uncached_input_tokens"), capped("output_tokens"),
		capped("cache_read_tokens"), capped("cache_write_tokens"),
		capped("uncached_input_cost"), capped("output_cost"),
		capped("cache_read_cost"), capped("cache_write_cost"),
		capped("total_cost"),
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT CASE WHEN user_id IS NULL THEN 'sub' ELSE 'id' END, COALESCE(CAST(user_id AS TEXT), user_sub), `+
			strings.Join(sums, ", ")+`
		FROM token_spend_ledger
		WHERE completed_time >= ? AND completed_time < ?
		GROUP BY user_id, user_sub`,
		from, to)
	if err != nil {
		return nil, fmt.Errorf("sum ledger period: %w", err)
	}
	return scanBudgetPeriodSums(rows)
}

func ledgerPeriodInGo(ctx context.Context, tx *dbutil.Tx, from, to int64) (budgetPeriodSums, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT user_id, user_sub,
			uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			uncached_input_cost, output_cost, cache_read_cost, cache_write_cost
		FROM token_spend_ledger
		WHERE completed_time >= ? AND completed_time < ?`,
		from, to)
	if err != nil {
		return nil, fmt.Errorf("read ledger period: %w", err)
	}
	defer func() { _ = rows.Close() }()
	sums := make(budgetPeriodSums)
	for rows.Next() {
		var e SpendLedgerEntry
		var input, output, cacheRead, cacheWrite *int64
		if err := rows.Scan(&e.UserID, &e.UserSub,
			&e.UncachedInputTokens, &e.OutputTokens, &e.CacheReadTokens, &e.CacheWriteTokens,
			&input, &output, &cacheRead, &cacheWrite); err != nil {
			return nil, fmt.Errorf("scan ledger period: %w", err)
		}
		if input != nil && output != nil && cacheRead != nil && cacheWrite != nil {
			e.Cost = &spend.Cost{UncachedInput: *input, Output: *output, CacheRead: *cacheRead, CacheWrite: *cacheWrite}
		}
		subject := budgetPeriodUserOf(&e)
		t := sums[subject]
		t.add(&e)
		sums[subject] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan ledger period: %w", err)
	}
	return sums, nil
}

func storedPeriod(ctx context.Context, tx *dbutil.Tx, periodStart int64) (budgetPeriodSums, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT subject_kind, subject_ref, `+strings.Join(budgetPeriodSumColumns, ", ")+`
		FROM token_spend_budget_period WHERE period_start = ?`, periodStart)
	if err != nil {
		return nil, fmt.Errorf("read budget period: %w", err)
	}
	return scanBudgetPeriodSums(rows)
}

func scanBudgetPeriodSums(rows *sql.Rows) (budgetPeriodSums, error) {
	defer func() { _ = rows.Close() }()
	sums := make(budgetPeriodSums)
	for rows.Next() {
		var kind string
		var subject BudgetPeriodSubject
		var t BudgetPeriodTotals
		if err := rows.Scan(append([]any{&kind, &subject.Ref}, t.scanTargets()...)...); err != nil {
			return nil, fmt.Errorf("scan budget period: %w", err)
		}
		subject.Kind = BudgetPeriodSubjectKind(kind)
		sums[subject] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan budget period: %w", err)
	}
	return sums, nil
}

func compareBudgetPeriods(stored, ledger budgetPeriodSums) []BudgetPeriodDrift {
	var drift []BudgetPeriodDrift
	for subject, l := range ledger {
		if s := stored[subject]; s != l {
			drift = append(drift, BudgetPeriodDrift{Subject: subject, Stored: s, Ledger: l})
		}
	}
	for subject, s := range stored {
		if _, inLedger := ledger[subject]; !inLedger && s != (BudgetPeriodTotals{}) {
			drift = append(drift, BudgetPeriodDrift{Subject: subject, Stored: s})
		}
	}
	slices.SortFunc(drift, func(a, b BudgetPeriodDrift) int {
		return cmp.Or(cmp.Compare(a.Subject.Kind, b.Subject.Kind), cmp.Compare(a.Subject.Ref, b.Subject.Ref))
	})
	return drift
}
