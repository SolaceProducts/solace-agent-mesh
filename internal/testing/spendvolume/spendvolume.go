// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spendvolume

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Dialect() string
}

const (
	kindUser       = 1
	kindAgent      = 2
	kindRoot       = 3
	kindModel      = 4
	kindBudget     = 5
	kindSystemUser = 6
	kindTopUp      = 7
	kindWorkflow   = 8
)

const (
	UserIDPrefix        = "00000000-0000-7000-8001-"
	BudgetIDPrefix      = "00000000-0000-7000-8005-"
	SystemUserIDPrefix  = "00000000-0000-7000-8006-"
	TopUpIDPrefix       = "00000000-0000-7000-8007-"
	SystemUserSubPrefix = "system:perfseed-"
)

const DefaultTracePrefix = "trace-"

const (
	dayMillis       = 86_400_000
	callsPerTrace   = 8
	activityRoots   = 40
	activityAgents  = 15
	budgetKinds     = 5
	budgetUnit      = 500_000_000_000
	spreadMillis    = 7919
	periodCostUnit  = 10_000_000_000
	periodCostSpan  = 150
	periodCostPrime = 37
)

func UserID(n int) string {
	return fmt.Sprintf("%s%012x", UserIDPrefix, n)
}

type dialect bool

func dialectOf(db DB) dialect { return db.Dialect() == "postgres" }

func (d dialect) uuid(kind int, n string) string {
	prefix := fmt.Sprintf("00000000-0000-7000-800%d-", kind)
	if d {
		return "CAST('" + prefix + "' || lpad(to_hex(" + n + "), 12, '0') AS UUID)"
	}
	return "'" + prefix + "' || printf('%012x', " + n + ")"
}

func (d dialect) randomUUID() string {
	if d {
		return "gen_random_uuid()"
	}
	return "lower(hex(randomblob(16)))"
}

func (d dialect) series(lo, hi int64) (with, from string) {
	if d {
		return "", fmt.Sprintf("generate_series(CAST(%d AS BIGINT), CAST(%d AS BIGINT)) AS g", lo, hi)
	}
	return fmt.Sprintf("WITH RECURSIVE c(g) AS (SELECT %d UNION ALL SELECT g + 1 FROM c WHERE g < %d) ", lo, hi), "c"
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func exec(ctx context.Context, db DB, name, query string) (int64, error) {
	res, err := db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("spendvolume %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

const (
	uncachedInputTokens = 100
	avoidedInputTokens  = 40
)

type Cost struct {
	UncachedInput, Output, CacheRead, CacheWrite int64
}

type Ledger struct {
	Rows          int
	Users         int
	UserStride    int
	Days          int
	FirstDay      int64
	TracePrefix   string
	Cost          Cost
	BatchRows     int
	WorkflowEvery int
}

func (l Ledger) tracePrefix() string {
	if l.TracePrefix == "" {
		return DefaultTracePrefix
	}
	return l.TracePrefix
}

func InsertLedger(ctx context.Context, db DB, l Ledger) error {
	d := dialectOf(db)
	stride := max(l.UserStride, 1)
	batch := l.Rows
	if l.BatchRows > 0 {
		batch = min(l.BatchRows, l.Rows)
	}
	user := d.uuid(kindUser, fmt.Sprintf("((g / %d) * %d) %% %d", callsPerTrace, stride, l.Users))
	agent := d.uuid(kindAgent, fmt.Sprintf("g %% %d", activityAgents))
	rootIndex := fmt.Sprintf("(g / %d) %% %d", callsPerTrace, activityRoots)
	rootKind, root := "'agent'", d.uuid(kindRoot, rootIndex)
	if l.WorkflowEvery > 0 {
		underWorkflow := fmt.Sprintf("(g / %d) %% %d = 0", callsPerTrace, l.WorkflowEvery)
		rootKind = "CASE WHEN " + underWorkflow + " THEN 'workflow' ELSE 'agent' END"
		root = "CASE WHEN " + underWorkflow + " THEN " + d.uuid(kindWorkflow, rootIndex) + " ELSE " + root + " END"
	}
	model := d.uuid(kindModel, "(g / 3) % 4")
	id := d.randomUUID()
	for lo := 1; lo <= l.Rows; lo += batch {
		hi := min(lo+batch-1, l.Rows)
		with, from := d.series(int64(lo), int64(hi))
		query := fmt.Sprintf(`INSERT INTO token_spend_ledger (id, completed_time, user_id, agent_id, root_kind, root_id, surface, model_config_id, trace_id,
				uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
				uncached_input_cost, output_cost, cache_read_cost, cache_write_cost,
				avoided_input_tokens, avoided_input_cost)
			%[1]sSELECT %[2]s,
				CAST(%[3]d AS BIGINT) + ((g * CAST(%[4]d AS BIGINT)) / CAST(%[5]d AS BIGINT)) * %[6]d + (g * %[7]d) %% %[6]d,
				%[8]s, %[9]s, %[22]s, %[10]s,
				'agent', %[11]s, %[12]s || (g / %[13]d),
				%[19]d, 20, 50, 5, %[14]d, %[15]d, %[16]d, %[17]d,
				CASE WHEN g %% %[13]d = 0 THEN NULL ELSE %[20]d END,
				CASE WHEN g %% %[13]d = 0 THEN NULL ELSE %[21]d END
			FROM %[18]s`,
			with, id, l.FirstDay, l.Days, l.Rows+1, dayMillis, spreadMillis,
			user, agent, root, model, quote(l.tracePrefix()), callsPerTrace,
			l.Cost.UncachedInput, l.Cost.Output, l.Cost.CacheRead, l.Cost.CacheWrite, from,
			uncachedInputTokens, avoidedInputTokens, l.Cost.UncachedInput*avoidedInputTokens/uncachedInputTokens, rootKind)
		if _, err := exec(ctx, db, "ledger", query); err != nil {
			return err
		}
	}
	return nil
}

func InsertLedgerDetails(ctx context.Context, db DB, tracePrefix string) error {
	_, err := exec(ctx, db, "details", `INSERT INTO token_spend_ledger_details (id, resolved_agent_name, resolved_root_name, resolved_model_name)
		SELECT id, 'Agent ' || substr(CAST(agent_id AS TEXT), 25), CASE WHEN root_kind = 'workflow' THEN 'Workflow ' ELSE 'Root ' END || substr(CAST(root_id AS TEXT), 25),
			'Model ' || substr(CAST(model_config_id AS TEXT), 25)
		FROM token_spend_ledger WHERE trace_id LIKE `+quote(tracePrefix+"%"))
	return err
}

type Users struct {
	Count  int
	Issuer string
}

func InsertUsers(ctx context.Context, db DB, u Users) error {
	d := dialectOf(db)
	issuer := u.Issuer
	if issuer == "" {
		issuer = "https://idp"
	}
	with, from := d.series(0, int64(u.Count-1))
	_, err := exec(ctx, db, "users", fmt.Sprintf(`INSERT INTO users (id, issuer, sub, display_name, email, created_by, updated_by, created_time, updated_time)
		%sSELECT %s, %s, 'sub-' || g, 'User ' || g, 'user' || g || '@example.com', 'test', 'test', 0, 0
		FROM %s`, with, d.uuid(kindUser, "g"), quote(issuer), from))
	return err
}

func SetTierDefault(ctx context.Context, db DB, amount int64) error {
	_, err := exec(ctx, db, "tier default", fmt.Sprintf(
		`UPDATE budgets SET amount = %d, warn_percentage = 80 WHERE subject_type = 'user' AND is_default = 1`, amount))
	return err
}

type Budgets struct {
	NamePrefix string
}

func InsertBudgets(ctx context.Context, db DB, b Budgets) error {
	d := dialectOf(db)
	prefix := b.NamePrefix
	if prefix == "" {
		prefix = "Budget "
	}
	with, from := d.series(0, budgetKinds-1)
	_, err := exec(ctx, db, "budgets", fmt.Sprintf(`INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage, created_by, updated_by, created_time, updated_time)
		%[1]sSELECT %[2]s, %[3]s || g, %[4]s || g, 'user',
			CASE WHEN g = %[6]d THEN NULL ELSE (g + 1) * %[7]d END, CASE WHEN g = %[6]d THEN NULL ELSE 80 END, 'test', 'test', 0, 0
		FROM %[5]s`, with, d.uuid(kindBudget, "g"), quote(prefix), quote(strings.ToLower(prefix)), from, budgetKinds-1, budgetUnit))
	return err
}

func InsertAssignments(ctx context.Context, db DB, count int) error {
	d := dialectOf(db)
	with, from := d.series(0, int64(count-1))
	_, err := exec(ctx, db, "assignments", fmt.Sprintf(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		%sSELECT %s, 'user', CAST(%s AS TEXT), %s, 'test', 'test', 0, 0
		FROM %s WHERE g %% %d <> 0`, with, d.randomUUID(), d.uuid(kindUser, "g"), d.uuid(kindBudget, fmt.Sprintf("g %% %d", budgetKinds)), from, budgetKinds))
	return err
}

func InsertSystemUsers(ctx context.Context, db DB, count int) error {
	d := dialectOf(db)
	with, from := d.series(0, int64(count-1))
	if _, err := exec(ctx, db, "system users", fmt.Sprintf(`INSERT INTO users (id, issuer, sub, display_name, provisioned_via, created_by, updated_by, created_time, updated_time)
		%sSELECT %s, 'sam:system', %s || g, 'Perf system user ' || g, 'system', 'test', 'test', 0, 0
		FROM %s`, with, d.uuid(kindSystemUser, "g"), quote(SystemUserSubPrefix), from)); err != nil {
		return err
	}
	_, err := exec(ctx, db, "system user assignments", fmt.Sprintf(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		%sSELECT %s, 'user', CAST(%s AS TEXT), %s, 'test', 'test', 0, 0
		FROM %s`, with, d.randomUUID(), d.uuid(kindSystemUser, "g"), d.uuid(kindBudget, fmt.Sprintf("g %% %d", budgetKinds)), from))
	return err
}

type TopUps struct {
	Users, Every int
	Amount       int64
	Created      time.Time
	Expires      time.Time
}

func InsertTopUps(ctx context.Context, db DB, t TopUps) error {
	d := dialectOf(db)
	with, from := d.series(0, int64(t.Users-1))
	_, err := exec(ctx, db, "top-ups", fmt.Sprintf(`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
		%sSELECT %s, 'user', CAST(%s AS TEXT), %d, %d, 'test', 'test', %d, %d
		FROM %s WHERE g %% %d = 0`,
		with, d.uuid(kindTopUp, "g"), d.uuid(kindUser, "g"), t.Amount, t.Expires.UnixMilli(), t.Created.UnixMilli(), t.Created.UnixMilli(), from, max(t.Every, 1)))
	return err
}

var periodSumColumns = []string{
	"priced_calls", "unpriced_calls", "uncached_input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
	"uncached_input_cost", "output_cost", "cache_read_cost", "cache_write_cost", "total_cost",
}

const periodInsert = `INSERT INTO token_spend_budget_period (subject_kind, subject_ref, period_start, period_end, ` +
	`priced_calls, unpriced_calls, uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, ` +
	`uncached_input_cost, output_cost, cache_read_cost, cache_write_cost, total_cost) `

func meshUpsert() string {
	sets := make([]string, len(periodSumColumns))
	for i, c := range periodSumColumns {
		sets[i] = fmt.Sprintf("%[1]s = token_spend_budget_period.%[1]s + EXCLUDED.%[1]s", c)
	}
	return " ON CONFLICT (subject_kind, subject_ref, period_start) DO UPDATE SET " + strings.Join(sets, ", ")
}

func sumList() string {
	sums := make([]string, len(periodSumColumns))
	for i, c := range periodSumColumns {
		sums[i] = "SUM(" + c + ")"
	}
	return strings.Join(sums, ", ")
}

func isGeneratedSubject(column string) string {
	return fmt.Sprintf("(subject_kind = 'id' AND %[1]s LIKE '%[2]s%%') OR (subject_kind = 'sub' AND %[1]s LIKE '%[3]s%%')",
		column, UserIDPrefix, SystemUserSubPrefix)
}

type PeriodRows struct {
	Users, SystemUsers int
	Start, End         int64
}

func InsertPeriodRows(ctx context.Context, db DB, p PeriodRows) error {
	d := dialectOf(db)
	cost := fmt.Sprintf("((g * %d) %% %d) * %d", periodCostPrime, periodCostSpan, periodCostUnit)
	insert := func(name, kind, ref string, count int) error {
		if count == 0 {
			return nil
		}
		with, from := d.series(0, int64(count-1))
		_, err := exec(ctx, db, name, fmt.Sprintf(`%[1]s%[2]sSELECT %[3]s, %[4]s, %[5]d, %[6]d, 10, 0, 100, 20, 50, 5, %[7]s, 0, 0, 0, %[7]s FROM %[8]s`,
			periodInsert, with, quote(kind), ref, p.Start, p.End, cost, from))
		return err
	}
	if err := insert("period rows", "id", "CAST("+d.uuid(kindUser, "g")+" AS TEXT)", p.Users); err != nil {
		return err
	}
	if err := insert("system user period rows", "sub", quote(SystemUserSubPrefix)+" || g", p.SystemUsers); err != nil {
		return err
	}
	_, err := exec(ctx, db, "mesh period row", fmt.Sprintf(`%sSELECT 'mesh', '', %d, %d, %s FROM token_spend_budget_period
		WHERE subject_kind <> 'mesh' AND period_start = %d AND (%s) GROUP BY period_start%s`,
		periodInsert, p.Start, p.End, sumList(), p.Start, isGeneratedSubject("subject_ref"), meshUpsert()))
	return err
}

func InsertPeriodRowsFromLedger(ctx context.Context, db DB, tracePrefix string) error {
	monthStart, monthEnd := sqliteMonth("start of month"), sqliteMonth("start of month', '+1 month")
	if dialectOf(db) {
		monthStart, monthEnd = postgresMonthStart, postgresMonthEnd
	}
	sums := `SUM(CASE WHEN uncached_input_cost IS NULL THEN 0 ELSE 1 END), SUM(CASE WHEN uncached_input_cost IS NULL THEN 1 ELSE 0 END),
		SUM(uncached_input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens),
		SUM(COALESCE(uncached_input_cost, 0)), SUM(COALESCE(output_cost, 0)), SUM(COALESCE(cache_read_cost, 0)), SUM(COALESCE(cache_write_cost, 0)),
		SUM(COALESCE(uncached_input_cost, 0) + COALESCE(output_cost, 0) + COALESCE(cache_read_cost, 0) + COALESCE(cache_write_cost, 0))`
	where := "trace_id LIKE " + quote(tracePrefix+"%") + " AND user_id IS NOT NULL"
	if _, err := exec(ctx, db, "period rows from the ledger", fmt.Sprintf(`%sSELECT 'id', CAST(user_id AS TEXT), %s, %s, %s
		FROM token_spend_ledger WHERE %s GROUP BY user_id, %s, %s`,
		periodInsert, monthStart, monthEnd, sums, where, monthStart, monthEnd)); err != nil {
		return err
	}
	_, err := exec(ctx, db, "mesh period rows from the ledger", fmt.Sprintf(`%sSELECT 'mesh', '', %s, %s, %s
		FROM token_spend_ledger WHERE %s GROUP BY %s, %s%s`,
		periodInsert, monthStart, monthEnd, sums, where, monthStart, monthEnd, meshUpsert()))
	return err
}

const alertCapKey = "perf-volume"

func InsertAlertStatesFromPeriods(ctx context.Context, db DB) error {
	_, err := exec(ctx, db, "alert states from the period rows", `INSERT INTO budget_alert_state
		(subject_type, subject_ref, period_start, alerted_level, cap_key, updated_time)
		SELECT CASE WHEN subject_kind = 'mesh' THEN 'mesh' ELSE 'user' END,
			CASE WHEN subject_kind = 'mesh' THEN 'mesh' ELSE subject_kind || ':' || subject_ref END,
			period_start, 'warn', `+quote(alertCapKey)+`, period_start
		FROM token_spend_budget_period WHERE subject_kind = 'mesh' OR (`+isGeneratedSubject("subject_ref")+`)
		ON CONFLICT (subject_type, subject_ref, period_start) DO NOTHING`)
	return err
}

func Analyze(ctx context.Context, db DB) error {
	_, err := exec(ctx, db, "analyze", "ANALYZE")
	return err
}

type RemovedRows struct {
	Ledger, Users, Budgets, Assignments, TopUps, PeriodRows, AlertStates int64
}

const removeLedgerBatch = 200_000

func Remove(ctx context.Context, db DB, tracePrefix string) (RemovedRows, error) {
	var out RemovedRows
	var err error
	sets := make([]string, len(periodSumColumns))
	for i, c := range periodSumColumns {
		sets[i] = fmt.Sprintf("%[1]s = token_spend_budget_period.%[1]s - s.%[1]s", c)
	}
	sums := make([]string, len(periodSumColumns))
	for i, c := range periodSumColumns {
		sums[i] = fmt.Sprintf("SUM(%[1]s) AS %[1]s", c)
	}
	if _, err = exec(ctx, db, "subtract from the mesh period rows", fmt.Sprintf(`UPDATE token_spend_budget_period SET %s
		FROM (SELECT period_start, %s FROM token_spend_budget_period WHERE subject_kind <> 'mesh' AND (%s) GROUP BY period_start) s
		WHERE token_spend_budget_period.subject_kind = 'mesh' AND token_spend_budget_period.period_start = s.period_start`,
		strings.Join(sets, ", "), strings.Join(sums, ", "), isGeneratedSubject("subject_ref"))); err != nil {
		return out, err
	}
	if out.PeriodRows, err = exec(ctx, db, "period rows", `DELETE FROM token_spend_budget_period WHERE subject_kind <> 'mesh' AND (`+isGeneratedSubject("subject_ref")+`)`); err != nil {
		return out, err
	}
	if _, err = exec(ctx, db, "emptied mesh period rows", `DELETE FROM token_spend_budget_period WHERE subject_kind = 'mesh' AND priced_calls = 0 AND unpriced_calls = 0 AND total_cost = 0`); err != nil {
		return out, err
	}
	for {
		var n int64
		n, err = exec(ctx, db, "ledger", fmt.Sprintf(`DELETE FROM token_spend_ledger WHERE id IN
			(SELECT id FROM token_spend_ledger WHERE trace_id LIKE %s LIMIT %d)`, quote(tracePrefix+"%"), removeLedgerBatch))
		if err != nil {
			return out, err
		}
		out.Ledger += n
		if n == 0 {
			break
		}
	}
	steps := []struct {
		name, query string
		count       *int64
	}{
		{"alert states", `DELETE FROM budget_alert_state WHERE subject_ref LIKE 'id:` + UserIDPrefix + `%' OR subject_ref LIKE 'sub:` +
			SystemUserSubPrefix + `%' OR (subject_type = 'mesh' AND cap_key = ` + quote(alertCapKey) + `)`, &out.AlertStates},
		{"top-ups", `DELETE FROM budget_top_ups WHERE CAST(id AS TEXT) LIKE '` + TopUpIDPrefix + `%'`, &out.TopUps},
		{"assignments", `DELETE FROM budget_assignments WHERE subject_ref LIKE '` + UserIDPrefix + `%' OR subject_ref LIKE '` + SystemUserIDPrefix + `%'`, &out.Assignments},
		{"budgets", `DELETE FROM budgets WHERE CAST(id AS TEXT) LIKE '` + BudgetIDPrefix + `%'`, &out.Budgets},
		{"users", `DELETE FROM users WHERE CAST(id AS TEXT) LIKE '` + UserIDPrefix + `%' OR CAST(id AS TEXT) LIKE '` + SystemUserIDPrefix + `%'`, &out.Users},
	}
	for _, s := range steps {
		if *s.count, err = exec(ctx, db, s.name, s.query); err != nil {
			return out, err
		}
	}
	return out, nil
}

func Timed(fn func()) time.Duration {
	start := time.Now()
	fn()
	return time.Since(start)
}

func Format(name string, dur time.Duration, attrs ...any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VOLUME name=%s", name)
	for i := 0; i+1 < len(attrs); i += 2 {
		fmt.Fprintf(&b, " %v=%v", attrs[i], attrs[i+1])
	}
	precision := time.Millisecond
	if dur < 10*time.Millisecond {
		precision = time.Microsecond
	}
	fmt.Fprintf(&b, " dur=%s", dur.Round(precision))
	return b.String()
}

func sqliteMonth(modifiers string) string {
	return "CAST(strftime('%s', completed_time / 1000, 'unixepoch', '" + modifiers + "') AS INTEGER) * 1000"
}

const (
	postgresMonthStart = `CAST(EXTRACT(EPOCH FROM date_trunc('month', to_timestamp(completed_time / 1000.0) AT TIME ZONE 'UTC')) * 1000 AS BIGINT)`
	postgresMonthEnd   = `CAST(EXTRACT(EPOCH FROM date_trunc('month', to_timestamp(completed_time / 1000.0) AT TIME ZONE 'UTC') + INTERVAL '1 month') * 1000 AS BIGINT)`
)
