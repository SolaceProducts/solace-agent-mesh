// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const SpendRollupGrainVersion = 3

const errOpWrap = "%s: %w"

const dayParam = "CAST(? AS BIGINT)"

const SpendRollupDayMillis int64 = 86_400_000

func SpendRollupDayEnd(ms int64) int64 {
	if rem := ms % SpendRollupDayMillis; rem != 0 {
		return ms - rem + SpendRollupDayMillis
	}
	return ms
}

var dailyGrain = []string{
	"user_id", "user_sub", "agent_id", "agent_name", "workflow_id", "workflow_name",
	"root_kind", "root_id", "root_name", "root_node_id", "model_config_id", "model_name",
}

var stagedServed = []struct{ column, expr string }{
	{"served_model_name", "COALESCE(d.resolved_model_name, l.model_name)"},
	{"served_model_provider", "d.resolved_model_provider"},
}

var (
	modelsGrain   = []string{"model_config_id", "model_name", "served_model_name", "served_model_provider"}
	modelsColumns = []string{"model_config_id", "model_name", "resolved_model_name", "resolved_model_provider"}
)

const modelsKeys = "models"

var rollupTables = []string{"token_spend_daily", "token_spend_daily_activities", "token_spend_daily_models"}

var activityGroupings = []struct {
	name    string
	columns []string
	from    string
}{
	{"root", []string{"user_id", "user_sub", "root_kind", "root_id", "root_name"}, stageTable},
	{"agent", []string{"user_id", "user_sub", "agent_id", "agent_name"}, stageTable},
	{"workflow", []string{"user_id", "user_sub", "workflow_id", "workflow_name"}, stagedUnderWorkflows},
	{"model", []string{"user_id", "user_sub", "model_config_id", "model_name"}, stageTable},
}

const stagedUnderWorkflows = `(SELECT w.user_id, w.user_sub, w.root_id AS workflow_id, w.root_name AS workflow_name, w.trace_id
		FROM ` + stageTable + ` w WHERE w.root_kind = 'workflow'
	UNION ALL
	SELECT w.user_id, w.user_sub, w.workflow_id, w.workflow_name, w.trace_id
		FROM ` + stageTable + ` w WHERE w.workflow_id IS NOT NULL OR w.workflow_name IS NOT NULL)`

var stagedSums = []struct{ column, expr string }{
	{"calls", "COUNT(*)"},
	{"unpriced_calls", "COUNT(*) - COUNT(l.total_cost)"},
	{"uncached_input_tokens", "CAST(SUM(l.uncached_input_tokens) AS BIGINT)"},
	{"output_tokens", "CAST(SUM(l.output_tokens) AS BIGINT)"},
	{"cache_read_tokens", "CAST(SUM(l.cache_read_tokens) AS BIGINT)"},
	{"cache_write_tokens", "CAST(SUM(l.cache_write_tokens) AS BIGINT)"},
	{"uncached_input_cost", "CAST(COALESCE(SUM(l.uncached_input_cost), 0) AS BIGINT)"},
	{"output_cost", "CAST(COALESCE(SUM(l.output_cost), 0) AS BIGINT)"},
	{"cache_read_cost", "CAST(COALESCE(SUM(l.cache_read_cost), 0) AS BIGINT)"},
	{"cache_write_cost", "CAST(COALESCE(SUM(l.cache_write_cost), 0) AS BIGINT)"},
	{"total_cost", "CAST(COALESCE(SUM(l.total_cost), 0) AS BIGINT)"},
	{"avoided_input_tokens", "CAST(COALESCE(SUM(l.avoided_input_tokens), 0) AS BIGINT)"},
	{"avoided_input_cost", "CAST(COALESCE(SUM(l.avoided_input_cost), 0) AS BIGINT)"},
	{"measured_calls", "COUNT(l.avoided_input_tokens)"},
	{"priced_measured_calls", "COUNT(l.avoided_input_cost)"},
}

var stagedNames = []struct{ column, expr string }{
	{"resolved_user_name", "MAX(CASE WHEN u.provisioned_via = 'system' THEN COALESCE(NULLIF(u.display_name, ''), NULLIF(u.email, '')) END)"},
	{"resolved_agent_name", "MAX(d.resolved_agent_name)"},
	{"resolved_workflow_name", "MAX(d.resolved_workflow_name)"},
	{"resolved_root_name", "MAX(d.resolved_root_name)"},
	{"resolved_model_name", "MAX(d.resolved_model_name)"},
	{"resolved_model_alias", "MAX(CASE WHEN d.model_key NOT LIKE '" + uuidPattern + "' THEN d.model_key END)"},
}

const uuidPattern = "________-____-____-____-____________"

var dailyLabels = []struct{ column, expr string }{
	{"user_label", "MAX(s.resolved_user_name)"},
	{"agent_label", "COALESCE(MAX(s.resolved_agent_name), MAX(s.agent_name))"},
	{"workflow_label", "COALESCE(MAX(s.resolved_workflow_name), MAX(s.workflow_name))"},
	{"root_label", "COALESCE(MAX(s.resolved_root_name), MAX(s.root_name))"},
	{"model_label", "COALESCE(MAX(s.resolved_model_alias), MAX(s.resolved_model_name), MAX(s.model_name))"},
}

const (
	stageTable = "spend_rollup_stage"
	keysTable  = "spend_rollup_keys"
	dailyKeys  = "daily"
)

type rollupStep struct {
	name string
	sql  string
	args []any
}

type SpendRollupRepo struct {
	db *DB
}

func NewSpendRollupRepo(db *DB) *SpendRollupRepo {
	return &SpendRollupRepo{db: db}
}

func (r *SpendRollupRepo) LedgerCallsByDay(ctx context.Context, from, to int64) (map[int64]int64, error) {
	return r.callsByDay(ctx, "SpendRollupRepo.LedgerCallsByDay", `SELECT completed_time - (completed_time % 86400000), COUNT(*)
		FROM token_spend_ledger WHERE completed_time >= ? AND completed_time < ?
		GROUP BY completed_time - (completed_time % 86400000)`, from, to)
}

func (r *SpendRollupRepo) RollupCallsByDay(ctx context.Context, from, to int64) (map[int64]int64, error) {
	return r.callsByDay(ctx, "SpendRollupRepo.RollupCallsByDay", `SELECT day, CAST(SUM(calls) AS BIGINT)
		FROM token_spend_daily WHERE day >= ? AND day < ? GROUP BY day`, from, to)
}

func (r *SpendRollupRepo) callsByDay(ctx context.Context, op, query string, from, to int64) (map[int64]int64, error) {
	rows, err := r.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf(errOpWrap, op, err)
	}
	defer func() { _ = rows.Close() }()
	calls := map[int64]int64{}
	for rows.Next() {
		var day, n int64
		if err := rows.Scan(&day, &n); err != nil {
			return nil, fmt.Errorf("%s: scan: %w", op, err)
		}
		calls[day] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errOpWrap, op, err)
	}
	return calls, nil
}

func (r *SpendRollupRepo) RefreshDay(ctx context.Context, day int64, removeStale bool) error {
	if day%SpendRollupDayMillis != 0 {
		return fmt.Errorf("SpendRollupRepo.RefreshDay: %d is not the start of a UTC day", day)
	}
	end := day + SpendRollupDayMillis
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		steps := []rollupStep{
			{"create the stage", r.createTempSQL(stageTable, stageSelect()+` WHERE 1 = 0 GROUP BY `+stageGroupBy()), nil},
			{"stage the day", `INSERT INTO ` + stageTable + ` ` + stageSelect() + `
				WHERE l.completed_time >= ? AND l.completed_time < ? GROUP BY ` + stageGroupBy(), []any{day, end}},
			{"upsert daily", r.upsertDailySQL(), []any{day}},
		}
		for _, g := range activityGroupings {
			steps = append(steps, rollupStep{"upsert " + g.name + " activities", r.upsertActivitiesSQL(g.name, g.columns, g.from), []any{day}})
		}
		steps = append(steps, rollupStep{"upsert models", r.upsertModelsSQL(), []any{day}})
		steps = append(steps, r.removeStaleModelsSteps(day)...)
		if removeStale {
			steps = append(steps, r.removeStaleSteps(day)...)
		}
		steps = append(steps, r.dropTempSteps()...)
		for _, step := range steps {
			if _, err := tx.ExecContext(ctx, step.sql, step.args...); err != nil {
				return fmt.Errorf(errOpWrap, step.name, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("SpendRollupRepo.RefreshDay: %w", err)
	}
	return nil
}

func (r *SpendRollupRepo) removeStaleModelsSteps(day int64) []rollupStep {
	return []rollupStep{
		{"create the keys", r.createKeysSQL(), nil},
		{"record models keys", `INSERT INTO ` + keysTable + ` (grouping, grain_key)
			SELECT DISTINCT '` + modelsKeys + `', ` + grainKeyExpr("s", modelsGrain) + ` FROM ` + stageTable + ` s`, nil},
		{"delete stale models", `DELETE FROM token_spend_daily_models WHERE day = ? AND NOT EXISTS (
			SELECT 1 FROM ` + keysTable + ` k WHERE k.grouping = '` + modelsKeys + `' AND k.grain_key = token_spend_daily_models.grain_key)`, []any{day}},
	}
}

func (r *SpendRollupRepo) removeStaleSteps(day int64) []rollupStep {
	steps := []rollupStep{
		{"record daily keys", `INSERT INTO ` + keysTable + ` (grouping, grain_key)
			SELECT DISTINCT '` + dailyKeys + `', ` + grainKeyExpr("s", dailyGrain) + ` FROM ` + stageTable + ` s`, nil},
		{"delete stale daily", `DELETE FROM token_spend_daily WHERE day = ? AND NOT EXISTS (
			SELECT 1 FROM ` + keysTable + ` k WHERE k.grouping = '` + dailyKeys + `' AND k.grain_key = token_spend_daily.grain_key)`, []any{day}},
	}
	for _, g := range activityGroupings {
		steps = append(steps,
			rollupStep{"record " + g.name + " keys", `INSERT INTO ` + keysTable + ` (grouping, grain_key)
				SELECT DISTINCT '` + g.name + `', ` + grainKeyExpr("s", g.columns) + ` FROM ` + g.from + ` s
				WHERE s.trace_id IS NOT NULL`, nil},
			rollupStep{"delete stale " + g.name + " activities", `DELETE FROM token_spend_daily_activities
				WHERE day = ? AND grouping = '` + g.name + `' AND NOT EXISTS (
				SELECT 1 FROM ` + keysTable + ` k WHERE k.grouping = '` + g.name + `'
				AND k.grain_key = token_spend_daily_activities.grain_key)`, []any{day}},
		)
	}
	return steps
}

func (r *SpendRollupRepo) DeleteDaysBefore(ctx context.Context, day int64) (int64, error) {
	days, err := r.daysBefore(ctx, day)
	if err != nil {
		return 0, fmt.Errorf("SpendRollupRepo.DeleteDaysBefore: %w", err)
	}
	var deleted int64
	for _, d := range days {
		n, err := r.deleteDay(ctx, d)
		if err != nil {
			return deleted, fmt.Errorf("SpendRollupRepo.DeleteDaysBefore: %w", err)
		}
		deleted += n
	}
	return deleted, nil
}

func (r *SpendRollupRepo) EnsureGrainVersion(ctx context.Context) (bool, error) {
	var version int
	err := r.db.QueryRowContext(ctx, `SELECT grain_version FROM token_spend_rollup_state WHERE id = 1`).Scan(&version)
	switch {
	case err == nil && version == SpendRollupGrainVersion:
		return false, nil
	case err == nil && version > SpendRollupGrainVersion:
		return false, fmt.Errorf("SpendRollupRepo.EnsureGrainVersion: the rollups were built at grain %d by a newer release; this release builds grain %d", version, SpendRollupGrainVersion)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("SpendRollupRepo.EnsureGrainVersion: read state: %w", err)
	}
	cleared := err == nil
	if cleared {
		if _, err := r.db.ExecContext(ctx, `UPDATE token_spend_rollup_state
			SET refreshed_time = NULL, reconciled_time = NULL WHERE id = 1`); err != nil {
			return false, fmt.Errorf("SpendRollupRepo.EnsureGrainVersion: forget the old times: %w", err)
		}
		if err := r.clearAllDays(ctx); err != nil {
			return false, fmt.Errorf("SpendRollupRepo.EnsureGrainVersion: %w", err)
		}
	}
	onConflict := `DO NOTHING`
	if cleared {
		onConflict = `DO UPDATE SET grain_version = excluded.grain_version, refreshed_time = NULL, reconciled_time = NULL`
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO token_spend_rollup_state (id, grain_version, refreshed_time, reconciled_time)
		VALUES (1, ?, NULL, NULL) ON CONFLICT (id) `+onConflict,
		SpendRollupGrainVersion); err != nil {
		return false, fmt.Errorf("SpendRollupRepo.EnsureGrainVersion: record the grain: %w", err)
	}
	return cleared, nil
}

func (r *SpendRollupRepo) StoredGrainVersion(ctx context.Context) (int, error) {
	var version int
	err := r.db.QueryRowContext(ctx, `SELECT grain_version FROM token_spend_rollup_state WHERE id = 1`).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return SpendRollupGrainVersion, nil
	case err != nil:
		return 0, fmt.Errorf("SpendRollupRepo.StoredGrainVersion: %w", err)
	}
	return version, nil
}

func (r *SpendRollupRepo) clearAllDays(ctx context.Context) error {
	days, err := r.daysBefore(ctx, math.MaxInt64)
	if err != nil {
		return err
	}
	for _, day := range days {
		if _, err := r.deleteDay(ctx, day); err != nil {
			return err
		}
	}
	return nil
}

func (r *SpendRollupRepo) daysBefore(ctx context.Context, day int64) ([]int64, error) {
	selects := make([]string, 0, len(rollupTables))
	args := make([]any, 0, len(rollupTables))
	for _, table := range rollupTables {
		selects = append(selects, `SELECT day FROM `+table+` WHERE day < ?`)
		args = append(args, day)
	}
	days, err := dbutil.QueryVals[int64](ctx, r.db, strings.Join(selects, ` UNION `), args...)
	if err != nil {
		return nil, fmt.Errorf("list days: %w", err)
	}
	return days, nil
}

func (r *SpendRollupRepo) deleteDay(ctx context.Context, day int64) (int64, error) {
	var deleted int64
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		for _, table := range rollupTables {
			result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE day = ?`, day)
			if err != nil {
				return fmt.Errorf("delete from %s: %w", table, err)
			}
			n, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("rows affected in %s: %w", table, err)
			}
			deleted += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func (r *SpendRollupRepo) MarkRefreshed(ctx context.Context, at int64) error {
	return r.markState(ctx, "SpendRollupRepo.MarkRefreshed", "refreshed_time", at)
}

func (r *SpendRollupRepo) MarkReconciled(ctx context.Context, at int64) error {
	return r.markState(ctx, "SpendRollupRepo.MarkReconciled", "reconciled_time", at)
}

func (r *SpendRollupRepo) markState(ctx context.Context, op, column string, at int64) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO token_spend_rollup_state (id, grain_version, `+column+`)
		VALUES (1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET `+column+` = excluded.`+column,
		SpendRollupGrainVersion, at)
	if err != nil {
		return fmt.Errorf(errOpWrap, op, err)
	}
	return nil
}

func (r *SpendRollupRepo) createTempSQL(table, query string) string {
	if r.db.Dialect() == "postgres" {
		return `CREATE TEMPORARY TABLE ` + table + ` ON COMMIT DROP AS ` + query
	}
	return `CREATE TEMPORARY TABLE ` + table + ` AS ` + query
}

func (r *SpendRollupRepo) createKeysSQL() string {
	onCommit := ""
	if r.db.Dialect() == "postgres" {
		onCommit = " ON COMMIT DROP"
	}
	return `CREATE TEMPORARY TABLE ` + keysTable + ` (grouping TEXT NOT NULL, grain_key TEXT NOT NULL,
		PRIMARY KEY (grouping, grain_key))` + onCommit
}

func (r *SpendRollupRepo) dropTempSteps() []rollupStep {
	if r.db.Dialect() == "postgres" {
		return nil
	}
	return []rollupStep{
		{"drop the stage", `DROP TABLE IF EXISTS temp.` + stageTable, nil},
		{"drop the keys", `DROP TABLE IF EXISTS temp.` + keysTable, nil},
	}
}

func (r *SpendRollupRepo) isDistinct() string {
	if r.db.Dialect() == "postgres" {
		return "IS DISTINCT FROM"
	}
	return "IS NOT"
}

func stageSelect() string {
	selects := append(prefixed("l", dailyGrain), "l.trace_id")
	for _, c := range stagedServed {
		selects = append(selects, c.expr+" AS "+c.column)
	}
	for _, c := range stagedSums {
		selects = append(selects, c.expr+" AS "+c.column)
	}
	for _, c := range stagedNames {
		selects = append(selects, c.expr+" AS "+c.column)
	}
	return `SELECT ` + strings.Join(selects, ", ") + `
		FROM token_spend_ledger l LEFT JOIN token_spend_ledger_details d ON d.id = l.id
		LEFT JOIN users u ON u.id = l.user_id`
}

func stageGroupBy() string {
	columns := append(prefixed("l", dailyGrain), "l.trace_id")
	for _, c := range stagedServed {
		columns = append(columns, c.expr)
	}
	return strings.Join(columns, ", ")
}

func (r *SpendRollupRepo) upsertDailySQL() string {
	columns := append([]string{"day", "grain_key"}, dailyGrain...)
	selects := append([]string{dayParam, grainKeyExpr("s", dailyGrain)}, prefixed("s", dailyGrain)...)
	var current, updated, sets []string
	update := func(column, value string) {
		current, updated = append(current, "token_spend_daily."+column), append(updated, value)
		sets = append(sets, column+" = "+value)
	}
	for _, c := range dailyLabels {
		columns, selects = append(columns, c.column), append(selects, c.expr)
		update(c.column, "COALESCE(excluded."+c.column+", token_spend_daily."+c.column+")")
	}
	for _, c := range stagedSums {
		columns = append(columns, c.column)
		selects = append(selects, "CAST(SUM(s."+c.column+") AS BIGINT)")
		update(c.column, "excluded."+c.column)
	}
	return `INSERT INTO token_spend_daily (` + strings.Join(columns, ", ") + `)
		SELECT ` + strings.Join(selects, ", ") + ` FROM ` + stageTable + ` s
		WHERE 1 = 1
		GROUP BY ` + strings.Join(prefixed("s", dailyGrain), ", ") + `
		ORDER BY 2
		ON CONFLICT (day, grain_key) DO UPDATE SET ` + strings.Join(sets, ", ") + `
		WHERE (` + strings.Join(current, ", ") + `) ` + r.isDistinct() + ` (` + strings.Join(updated, ", ") + `)`
}

func (r *SpendRollupRepo) upsertModelsSQL() string {
	columns := append([]string{"day", "grain_key"}, modelsColumns...)
	selects := append([]string{dayParam, grainKeyExpr("s", modelsGrain)}, prefixed("s", modelsGrain)...)
	var updated []string
	for _, c := range stagedSums {
		columns = append(columns, c.column)
		selects = append(selects, "CAST(SUM(s."+c.column+") AS BIGINT)")
		updated = append(updated, c.column)
	}
	columns, selects, updated = append(columns, "activities"), append(selects, "COUNT(DISTINCT s.trace_id)"), append(updated, "activities")
	return dbutil.UpsertWhereSQL(r.db.Dialect(), `INSERT INTO token_spend_daily_models (`+strings.Join(columns, ", ")+`)
		SELECT `+strings.Join(selects, ", ")+` FROM `+stageTable+` s
		WHERE 1 = 1
		GROUP BY `+strings.Join(prefixed("s", modelsGrain), ", ")+`
		ORDER BY 2`, "day, grain_key", updated,
		`(`+strings.Join(prefixed("token_spend_daily_models", updated), ", ")+`) `+r.isDistinct()+` (`+strings.Join(prefixed("excluded", updated), ", ")+`)`)
}

func (r *SpendRollupRepo) upsertActivitiesSQL(grouping string, grain []string, from string) string {
	columns := append([]string{"day", "grouping", "grain_key"}, grain...)
	selects := append([]string{dayParam, "'" + grouping + "'", grainKeyExpr("s", grain)}, prefixed("s", grain)...)
	return dbutil.UpsertWhereSQL(r.db.Dialect(), `INSERT INTO token_spend_daily_activities (`+strings.Join(columns, ", ")+`, activities)
		SELECT `+strings.Join(selects, ", ")+`, COUNT(DISTINCT s.trace_id) FROM `+from+` s
		WHERE s.trace_id IS NOT NULL
		GROUP BY `+strings.Join(prefixed("s", grain), ", ")+`
		ORDER BY 3`, "day, grouping, grain_key", []string{"activities"},
		"token_spend_daily_activities.activities <> excluded.activities")
}

func grainKeyExpr(alias string, columns []string) string {
	parts := make([]string, len(columns))
	for i, c := range columns {
		column := alias + "." + c
		value := "CAST(" + column + " AS TEXT)"
		parts[i] = "CASE WHEN " + column + " IS NULL THEN '~' ELSE CAST(length(" + value + ") AS TEXT) || ':' || " + value + " END"
	}
	return strings.Join(parts, " || '|' || ")
}

func prefixed(alias string, columns []string) []string {
	out := make([]string, len(columns))
	for i, c := range columns {
		out[i] = alias + "." + c
	}
	return out
}
