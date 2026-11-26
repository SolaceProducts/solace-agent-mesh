// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type UsageKind string

const (
	UsageWorkflows UsageKind = "workflows"
	UsageAgents    UsageKind = "agents"
	UsageUsers     UsageKind = "users"
	UsageModels    UsageKind = "models"
)

type UsageSubject struct {
	Kind UsageKind
	ID   string
	Name string
}

func (s UsageSubject) tenant() bool { return s.Kind == "" }

type UsageTotals struct {
	Cost                int64 `db:"cost"`
	UncachedInputCost   int64 `db:"uncached_input_cost"`
	OutputCost          int64 `db:"output_cost"`
	CacheReadCost       int64 `db:"cache_read_cost"`
	CacheWriteCost      int64 `db:"cache_write_cost"`
	UncachedInputTokens int64 `db:"uncached_input_tokens"`
	OutputTokens        int64 `db:"output_tokens"`
	CacheReadTokens     int64 `db:"cache_read_tokens"`
	CacheWriteTokens    int64 `db:"cache_write_tokens"`
	Calls               int64 `db:"calls"`
	UnpricedCalls       int64 `db:"unpriced_calls"`
	Activities          int64 `db:"activities"`
}

type UsageSavings struct {
	AvoidedInputTokens  int64 `db:"avoided_input_tokens"`
	AvoidedInputCost    int64 `db:"avoided_input_cost"`
	Calls               int64 `db:"calls"`
	MeasuredCalls       int64 `db:"measured_calls"`
	PricedMeasuredCalls int64 `db:"priced_measured_calls"`
	UnpricedCalls       int64 `db:"unpriced_calls"`
}

const (
	errTotalsWrap = "SpendUsageRepo.Totals: %w"
	errDailyWrap  = "SpendUsageRepo.Daily: %w"
)

type UsageDay struct {
	Day                 int64 `db:"day"`
	Cost                int64 `db:"cost"`
	UncachedInputTokens int64 `db:"uncached_input_tokens"`
	OutputTokens        int64 `db:"output_tokens"`
	CacheReadTokens     int64 `db:"cache_read_tokens"`
	CacheWriteTokens    int64 `db:"cache_write_tokens"`
	Activities          int64 `db:"activities"`
}

const (
	dayTokenSums = `CAST(SUM(uncached_input_tokens) AS BIGINT) AS uncached_input_tokens, CAST(SUM(output_tokens) AS BIGINT) AS output_tokens,
		CAST(SUM(cache_read_tokens) AS BIGINT) AS cache_read_tokens, CAST(SUM(cache_write_tokens) AS BIGINT) AS cache_write_tokens`
	dayTokenColumns = `r.uncached_input_tokens AS uncached_input_tokens, r.output_tokens AS output_tokens, r.cache_read_tokens AS cache_read_tokens, r.cache_write_tokens AS cache_write_tokens`
	noDayTokens     = `0, 0, 0, 0`
)

type UsageRow struct {
	SubjectID   *string
	SubjectName *string
	Name        string
	Email       *string
	SystemUser  bool
	Live        bool
	Totals      UsageTotals
}

type UsageList struct {
	Rows  []UsageRow
	Count int
}

type usageRowRecord struct {
	SubjectID           *string `db:"subject_id"`
	SubjectName         *string `db:"subject_name"`
	Name                string  `db:"name"`
	Email               *string `db:"email"`
	SystemUser          bool    `db:"system_user"`
	Live                bool    `db:"live_row"`
	Cost                int64   `db:"cost"`
	UncachedInputCost   int64   `db:"uncached_input_cost"`
	OutputCost          int64   `db:"output_cost"`
	CacheReadCost       int64   `db:"cache_read_cost"`
	CacheWriteCost      int64   `db:"cache_write_cost"`
	UncachedInputTokens int64   `db:"uncached_input_tokens"`
	OutputTokens        int64   `db:"output_tokens"`
	CacheReadTokens     int64   `db:"cache_read_tokens"`
	CacheWriteTokens    int64   `db:"cache_write_tokens"`
	Calls               int64   `db:"calls"`
	UnpricedCalls       int64   `db:"unpriced_calls"`
	Activities          int64   `db:"activities"`
	Count               int     `db:"total_count"`
}

func (rec usageRowRecord) row() UsageRow {
	return UsageRow{
		SubjectID: rec.SubjectID, SubjectName: rec.SubjectName, Name: rec.Name, Email: rec.Email, SystemUser: rec.SystemUser, Live: rec.Live,
		Totals: UsageTotals{
			Cost: rec.Cost, UncachedInputCost: rec.UncachedInputCost, OutputCost: rec.OutputCost,
			CacheReadCost: rec.CacheReadCost, CacheWriteCost: rec.CacheWriteCost,
			UncachedInputTokens: rec.UncachedInputTokens, OutputTokens: rec.OutputTokens,
			CacheReadTokens: rec.CacheReadTokens, CacheWriteTokens: rec.CacheWriteTokens,
			Calls: rec.Calls, UnpricedCalls: rec.UnpricedCalls, Activities: rec.Activities,
		},
	}
}

type UsageSort string

const (
	UsageSortCost              UsageSort = "cost"
	UsageSortActivities        UsageSort = "activities"
	UsageSortCostPerActivity   UsageSort = "costPerActivity"
	UsageSortName              UsageSort = "name"
	UsageSortUncachedInputCost UsageSort = "uncachedInputCost"
	UsageSortOutputCost        UsageSort = "outputCost"
	UsageSortCacheReadCost     UsageSort = "cacheReadCost"
	UsageSortCacheWriteCost    UsageSort = "cacheWriteCost"
	UsageSortServedModel       UsageSort = "servedModel"
)

var usageSortExprs = map[UsageSort]string{
	UsageSortCost:              "q.cost",
	UsageSortActivities:        "q.activities",
	UsageSortCostPerActivity:   "CASE WHEN q.activities = 0 THEN NULL ELSE q.cost * 1.0 / q.activities END",
	UsageSortName:              "LOWER(q.name)",
	UsageSortUncachedInputCost: "q.uncached_input_cost",
	UsageSortOutputCost:        "q.output_cost",
	UsageSortCacheReadCost:     "q.cache_read_cost",
	UsageSortCacheWriteCost:    "q.cache_write_cost",
	UsageSortServedModel:       "LOWER(q.served_model)",
}

const nullText = "CAST(NULL AS TEXT)"

const servedModelCTE = `served AS (SELECT subject_key, name FROM (
		SELECT COALESCE('i:' || CAST(x.model_config_id AS TEXT), 'n:' || x.model_name) AS subject_key,
			x.resolved_model_name AS name,
			ROW_NUMBER() OVER (PARTITION BY COALESCE('i:' || CAST(x.model_config_id AS TEXT), 'n:' || x.model_name)
				ORDER BY SUM(x.total_cost) DESC, x.resolved_model_name, x.resolved_model_provider) AS rank
		FROM token_spend_daily_models x
		WHERE x.day >= ? AND x.day < ? AND x.resolved_model_name IS NOT NULL
		GROUP BY COALESCE('i:' || CAST(x.model_config_id AS TEXT), 'n:' || x.model_name), x.resolved_model_name, x.resolved_model_provider) ranked
	WHERE rank = 1)`

const bareSubject = "(q.subject_id IS NULL AND q.subject_name IS NOT NULL)"

type UsageListQuery struct {
	FromDay, ToDay int64
	Name           string
	AllSubjects    bool
	Budget         *UserBudgetMatch
	Sort           UsageSort
	Descending     bool
	Offset, Limit  int
	Unpaged        bool
}

type UsageNodeCost struct {
	NodeID string `db:"node_id"`
	Cost   int64  `db:"cost"`
}

type UsageRootCost struct {
	Kind   string  `db:"root_kind"`
	RootID *string `db:"root_id"`
	Root   *string `db:"root_name"`
	Name   string  `db:"name"`
	Cost   int64   `db:"cost"`
}

type UsageRollupState struct {
	RefreshedTime  *int64 `db:"refreshed_time"`
	ReconciledTime *int64 `db:"reconciled_time"`
}

type usageKindSpec struct {
	identity                 []string
	subjectID, subjectName   string
	rows                     string
	activityGrouping         string
	label                    string
	liveJoin, liveName       string
	liveRow                  string
	population               string
	byID, byName             string
	source                   string
	ledgerByID, ledgerByName string
	unnamedByName            bool
	unnamedByID              bool
}

var usageKinds = map[UsageKind]usageKindSpec{
	UsageWorkflows: {
		identity:  []string{"workflow_id", "workflow_name"},
		subjectID: "CAST({T}.workflow_id AS TEXT)", subjectName: "{T}.workflow_name",
		rows:   "({T}.root_kind = 'workflow' OR {T}.workflow_id IS NOT NULL OR {T}.workflow_name IS NOT NULL)",
		source: workflowSpendRows, activityGrouping: "workflow", label: "workflow_label",
		liveJoin: "LEFT JOIN workflows live ON CAST(live.id AS TEXT) = s.subject_id", liveName: "live.name",
		liveRow:    `SELECT name, CAST(NULL AS TEXT) AS email, 0 AS system_user FROM workflows WHERE id = ?`,
		population: `SELECT CAST(id AS TEXT), CAST(NULL AS TEXT) FROM workflows WHERE created_time < ?`,
		byID:       "{T}.workflow_id = ?", byName: "{T}.workflow_name = ?",
		ledgerByID:   "(({T}.root_kind = 'workflow' AND {T}.root_id = ?) OR {T}.workflow_id = ?)",
		ledgerByName: "(({T}.root_kind = 'workflow' AND {T}.root_name = ?) OR {T}.workflow_name = ?)",
	},
	UsageAgents: {
		identity:  []string{"agent_id", "agent_name"},
		subjectID: "CAST({T}.agent_id AS TEXT)", subjectName: "{T}.agent_name",
		rows: "({T}.agent_id IS NOT NULL OR {T}.agent_name IS NOT NULL)", activityGrouping: "agent", label: "agent_label",
		liveJoin: "LEFT JOIN agents live ON CAST(live.id AS TEXT) = s.subject_id", liveName: "live.name",
		liveRow:    `SELECT name, CAST(NULL AS TEXT) AS email, 0 AS system_user FROM agents WHERE id = ?`,
		population: `SELECT CAST(id AS TEXT), CAST(NULL AS TEXT) FROM agents WHERE type NOT IN ('a2a_proxy', 'link') AND is_ephemeral = 0 AND created_time < ?`,
		byID:       "{T}.agent_id = ?", byName: "{T}.agent_name = ?",
	},
	UsageModels: {
		identity:  []string{"model_config_id", "model_name"},
		subjectID: "CAST({T}.model_config_id AS TEXT)", subjectName: "{T}.model_name",
		rows: "1 = 1", activityGrouping: "model", label: "model_label",
		liveJoin: "LEFT JOIN model_configurations live ON CAST(live.id AS TEXT) = s.subject_id", liveName: "live.alias",
		liveRow:    `SELECT alias AS name, CAST(NULL AS TEXT) AS email, 0 AS system_user FROM model_configurations WHERE id = ?`,
		population: `SELECT CAST(id AS TEXT), CAST(NULL AS TEXT) FROM model_configurations WHERE created_time < ?`,
		byID:       "{T}.model_config_id = ?", byName: "{T}.model_name = ?",
	},
	UsageUsers: {
		identity:    []string{"user_id", "user_sub"},
		subjectID:   "CAST({T}.user_id AS TEXT)",
		subjectName: "CASE WHEN {T}.user_id IS NULL THEN {T}.user_sub END",
		rows:        "1 = 1", activityGrouping: "root", label: "user_label",
		liveJoin: "LEFT JOIN users live ON CAST(live.id AS TEXT) = s.subject_id",
		liveName: "COALESCE(NULLIF(live.display_name, ''), NULLIF(live.email, ''))",
		liveRow: `SELECT COALESCE(NULLIF(display_name, ''), NULLIF(email, '')) AS name, NULLIF(email, '') AS email,
			CASE WHEN provisioned_via = 'system' THEN 1 ELSE 0 END AS system_user FROM users WHERE id = ?`,
		population:    `SELECT CAST(id AS TEXT), CAST(NULL AS TEXT) FROM users WHERE active = 1 AND created_time < ?`,
		byID:          "{T}.user_id = ?",
		byName:        "({T}.user_id IS NULL AND {T}.user_sub = ?)",
		unnamedByName: true,
		unnamedByID:   true,
	},
}

const workflowSpendRows = `(SELECT d.day, d.root_kind, d.root_id AS workflow_id, d.root_name AS workflow_name,
		d.root_label AS workflow_label, ` + workflowSpendColumns + `
		FROM token_spend_daily d WHERE d.root_kind = 'workflow'
	UNION ALL
	SELECT d.day, d.root_kind, d.workflow_id, d.workflow_name, d.workflow_label, ` + workflowSpendColumns + `
		FROM token_spend_daily d
		WHERE (d.workflow_id IS NOT NULL OR d.workflow_name IS NOT NULL) AND NOT (d.root_kind = 'workflow' AND (
			(d.root_id IS NOT NULL AND d.workflow_id IS NOT NULL AND d.root_id = d.workflow_id)
			OR (d.root_name IS NOT NULL AND d.workflow_name IS NOT NULL AND d.root_name = d.workflow_name))))`

const workflowSpendColumns = `d.total_cost, d.uncached_input_cost, d.output_cost, d.cache_read_cost, d.cache_write_cost,
	d.uncached_input_tokens, d.output_tokens, d.cache_read_tokens, d.cache_write_tokens, d.calls, d.unpriced_calls,
	d.avoided_input_tokens, d.avoided_input_cost, d.measured_calls, d.priced_measured_calls`

func dailySource(s UsageSubject) string {
	if spec := usageKinds[s.Kind]; spec.source != "" {
		return spec.source
	}
	return "token_spend_daily"
}

type SpendUsageRepo struct {
	db            *DB
	hiddenUserIDs []string
}

const usageReadTimeout = 15 * time.Second

func (r *SpendUsageRepo) read(ctx context.Context, fn func(ctx context.Context, q dbutil.Querier) error) error {
	if r.db.Dialect() != "postgres" {
		bounded, cancel := context.WithTimeout(ctx, usageReadTimeout)
		defer cancel()
		return fn(bounded, r.db)
	}
	return r.db.WithBoundedReadSnapshot(ctx, usageReadTimeout, func(tx *dbutil.Tx) error { return fn(ctx, tx) })
}

func readRows[T any](ctx context.Context, r *SpendUsageRepo, query string, args ...any) ([]T, error) {
	var out []T
	err := r.read(ctx, func(ctx context.Context, q dbutil.Querier) (err error) {
		out, err = dbutil.QueryRows[T](ctx, q, query, args...)
		return err
	})
	return out, err
}

func readRow[T any](ctx context.Context, r *SpendUsageRepo, query string, args ...any) (*T, error) {
	var out *T
	err := r.read(ctx, func(ctx context.Context, q dbutil.Querier) (err error) {
		out, err = dbutil.QueryRow[T](ctx, q, query, args...)
		return err
	})
	return out, err
}

func readVals[T any](ctx context.Context, r *SpendUsageRepo, query string, args ...any) ([]T, error) {
	var out []T
	err := r.read(ctx, func(ctx context.Context, q dbutil.Querier) (err error) {
		out, err = dbutil.QueryVals[T](ctx, q, query, args...)
		return err
	})
	return out, err
}

func readVal[T any](ctx context.Context, r *SpendUsageRepo, query string, args ...any) (T, error) {
	var out T
	err := r.read(ctx, func(ctx context.Context, q dbutil.Querier) (err error) {
		out, err = dbutil.QueryVal[T](ctx, q, query, args...)
		return err
	})
	return out, err
}

func NewSpendUsageRepo(db *DB, hiddenUserIDs []string) *SpendUsageRepo {
	return &SpendUsageRepo{db: db, hiddenUserIDs: hiddenUserIDs}
}

func (r *SpendUsageRepo) population(kind UsageKind, spec usageKindSpec) (string, []any, error) {
	if kind != UsageUsers || len(r.hiddenUserIDs) == 0 {
		return spec.population, nil, nil
	}
	in, args, err := dbutil.InClause(r.hiddenUserIDs)
	if err != nil {
		return "", nil, err
	}
	return spec.population + ` AND CAST(id AS TEXT) NOT IN (` + in + `)`, args, nil
}

func on(alias, expr string) string {
	return strings.ReplaceAll(expr, aliasPlaceholder, alias+".")
}

const aliasPlaceholder = "{T}."

func subjectFilter(alias string, s UsageSubject) (string, []any, error) {
	return filterSubject(alias, s, func(spec usageKindSpec) (string, string) { return spec.byID, spec.byName })
}

func ledgerSubjectFilter(alias string, s UsageSubject) (string, []any, error) {
	return filterSubject(alias, s, func(spec usageKindSpec) (string, string) {
		if spec.ledgerByID != "" {
			return spec.ledgerByID, spec.ledgerByName
		}
		return spec.byID, spec.byName
	})
}

func filterSubject(alias string, s UsageSubject, predicates func(usageKindSpec) (byID, byName string)) (string, []any, error) {
	if s.tenant() {
		return "1 = 1", nil, nil
	}
	if s.ID != "" && s.Name != "" {
		return "", nil, fmt.Errorf("usage subject of kind %q names both an id and a name", s.Kind)
	}
	s.ID = strings.ToLower(s.ID)
	spec, ok := usageKinds[s.Kind]
	if !ok {
		return "", nil, fmt.Errorf("unknown usage kind %q", s.Kind)
	}
	byID, byName := predicates(spec)
	switch {
	case s.ID != "":
		return on(alias, byID), repeatedArg(byID, s.ID), nil
	case s.Name != "":
		return on(alias, byName), repeatedArg(byName, s.Name), nil
	}
	return "", nil, fmt.Errorf("usage subject of kind %q names neither an id nor a name", s.Kind)
}

func repeatedArg(predicate string, arg any) []any {
	args := make([]any, strings.Count(predicate, "?"))
	for i := range args {
		args[i] = arg
	}
	return args
}

func activityGroupingOf(s UsageSubject) string {
	if s.tenant() {
		return "root"
	}
	return usageKinds[s.Kind].activityGrouping
}

var sumColumnNames = []string{
	"cost", "uncached_input_cost", "output_cost", "cache_read_cost", "cache_write_cost",
	"uncached_input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "calls", "unpriced_calls",
}

func sumOf(alias string, columns []string) string {
	sums := make([]string, len(columns))
	for i, c := range columns {
		sums[i] = "CAST(SUM(" + alias + "." + c + ") AS BIGINT) AS " + c
	}
	return strings.Join(sums, ", ")
}

const sumColumns = `CAST(COALESCE(SUM({T}.total_cost), 0) AS BIGINT) AS cost,
	CAST(COALESCE(SUM({T}.uncached_input_cost), 0) AS BIGINT) AS uncached_input_cost,
	CAST(COALESCE(SUM({T}.output_cost), 0) AS BIGINT) AS output_cost,
	CAST(COALESCE(SUM({T}.cache_read_cost), 0) AS BIGINT) AS cache_read_cost,
	CAST(COALESCE(SUM({T}.cache_write_cost), 0) AS BIGINT) AS cache_write_cost,
	CAST(COALESCE(SUM({T}.uncached_input_tokens), 0) AS BIGINT) AS uncached_input_tokens,
	CAST(COALESCE(SUM({T}.output_tokens), 0) AS BIGINT) AS output_tokens,
	CAST(COALESCE(SUM({T}.cache_read_tokens), 0) AS BIGINT) AS cache_read_tokens,
	CAST(COALESCE(SUM({T}.cache_write_tokens), 0) AS BIGINT) AS cache_write_tokens`

func (r *SpendUsageRepo) Totals(ctx context.Context, s UsageSubject, fromDay, toDay int64) (UsageTotals, error) {
	filter, args, err := subjectFilter("r", s)
	if err != nil {
		return UsageTotals{}, fmt.Errorf(errTotalsWrap, err)
	}
	totals, err := readRow[UsageTotals](ctx, r, `SELECT `+on("r", sumColumns)+`,
		CAST(COALESCE(SUM(r.calls), 0) AS BIGINT) AS calls,
		CAST(COALESCE(SUM(r.unpriced_calls), 0) AS BIGINT) AS unpriced_calls,
		0 AS activities
		FROM `+dailySource(s)+` r
		WHERE r.day >= ? AND r.day < ? AND `+filter, append([]any{fromDay, toDay}, args...)...)
	if err != nil {
		return UsageTotals{}, fmt.Errorf(errTotalsWrap, err)
	}
	activities, err := r.activities(ctx, s, fromDay, toDay)
	if err != nil {
		return UsageTotals{}, fmt.Errorf(errTotalsWrap, err)
	}
	totals.Activities = activities
	return *totals, nil
}

func (r *SpendUsageRepo) Savings(ctx context.Context, s UsageSubject, fromDay, toDay int64) (UsageSavings, error) {
	filter, args, err := subjectFilter("r", s)
	if err != nil {
		return UsageSavings{}, fmt.Errorf(errSavingsWrap, err)
	}
	savings, err := readRow[UsageSavings](ctx, r, `SELECT
		CAST(COALESCE(SUM(r.avoided_input_tokens), 0) AS BIGINT) AS avoided_input_tokens,
		CAST(COALESCE(SUM(r.avoided_input_cost), 0) AS BIGINT) AS avoided_input_cost,
		CAST(COALESCE(SUM(r.calls), 0) AS BIGINT) AS calls,
		CAST(COALESCE(SUM(r.measured_calls), 0) AS BIGINT) AS measured_calls,
		CAST(COALESCE(SUM(r.priced_measured_calls), 0) AS BIGINT) AS priced_measured_calls,
		CAST(COALESCE(SUM(r.unpriced_calls), 0) AS BIGINT) AS unpriced_calls
		FROM `+dailySource(s)+` r
		WHERE r.day >= ? AND r.day < ? AND `+filter, append([]any{fromDay, toDay}, args...)...)
	if err != nil {
		return UsageSavings{}, fmt.Errorf(errSavingsWrap, err)
	}
	return *savings, nil
}

const errSavingsWrap = "SpendUsageRepo.Savings: %w"

func (r *SpendUsageRepo) activities(ctx context.Context, s UsageSubject, fromDay, toDay int64) (int64, error) {
	filter, args, err := subjectFilter("a", s)
	if err != nil {
		return 0, err
	}
	return readVal[int64](ctx, r, `SELECT CAST(COALESCE(SUM(a.activities), 0) AS BIGINT)
		FROM token_spend_daily_activities a
		WHERE a.grouping = ? AND a.day >= ? AND a.day < ? AND `+filter,
		append([]any{activityGroupingOf(s), fromDay, toDay}, args...)...)
}

func (r *SpendUsageRepo) LedgerCost(ctx context.Context, s UsageSubject, fromMillis, toMillis int64) (int64, error) {
	filter, args, err := ledgerSubjectFilter("l", s)
	if err != nil {
		return 0, fmt.Errorf("SpendUsageRepo.LedgerCost: %w", err)
	}
	cost, err := readVal[int64](ctx, r, `SELECT CAST(COALESCE(SUM(l.total_cost), 0) AS BIGINT)
		FROM token_spend_ledger l
		WHERE l.completed_time >= ? AND l.completed_time < ? AND `+filter, append([]any{fromMillis, toMillis}, args...)...)
	if err != nil {
		return 0, fmt.Errorf("SpendUsageRepo.LedgerCost: %w", err)
	}
	return cost, nil
}

func (r *SpendUsageRepo) Daily(ctx context.Context, s UsageSubject, fromDay, toDay int64) ([]UsageDay, error) {
	costFilter, costArgs, err := subjectFilter("r", s)
	if err != nil {
		return nil, fmt.Errorf(errDailyWrap, err)
	}
	actFilter, actArgs, err := subjectFilter("a", s)
	if err != nil {
		return nil, fmt.Errorf(errDailyWrap, err)
	}
	query := `SELECT day, CAST(SUM(cost) AS BIGINT) AS cost, ` + dayTokenSums + `, CAST(SUM(activities) AS BIGINT) AS activities FROM (
		SELECT r.day AS day, r.total_cost AS cost, ` + dayTokenColumns + `, 0 AS activities FROM ` + dailySource(s) + ` r
		WHERE r.day >= ? AND r.day < ? AND ` + costFilter + `
		UNION ALL
		SELECT a.day, 0, ` + noDayTokens + `, a.activities FROM token_spend_daily_activities a
		WHERE a.grouping = ? AND a.day >= ? AND a.day < ? AND ` + actFilter + `
	) d GROUP BY day ORDER BY day`
	args := append(append([]any{fromDay, toDay}, costArgs...), activityGroupingOf(s), fromDay, toDay)
	days, err := readRows[UsageDay](ctx, r, query, append(args, actArgs...)...)
	if err != nil {
		return nil, fmt.Errorf(errDailyWrap, err)
	}
	return days, nil
}

func UsageRowKey(row UsageRow) string {
	if row.SubjectID != nil {
		return "i:" + *row.SubjectID
	}
	if row.SubjectName != nil {
		return "n:" + *row.SubjectName
	}
	return ""
}

func (r *SpendUsageRepo) DailyByRow(ctx context.Context, kind UsageKind, rows []UsageRow, fromDay, toDay int64) (map[string][]UsageDay, error) {
	byRow := make(map[string][]UsageDay, len(rows))
	spec, ok := usageKinds[kind]
	if !ok {
		return nil, fmt.Errorf("SpendUsageRepo.DailyByRow: unknown usage kind %q", kind)
	}
	keys := make([]any, 0, len(rows))
	for _, row := range rows {
		if key := UsageRowKey(row); key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return byRow, nil
	}
	key := func(alias string) string {
		return "COALESCE('i:' || " + on(alias, spec.subjectID) + ", 'n:' || " + on(alias, spec.subjectName) + ")"
	}
	identity, identityArgs := rowsIdentityFilter(spec, rows)
	in := placeholderList(len(keys))
	query := `SELECT subject_key, day, CAST(SUM(cost) AS BIGINT) AS cost, ` + dayTokenSums + `, CAST(SUM(activities) AS BIGINT) AS activities FROM (
		SELECT ` + key("r") + ` AS subject_key, r.day AS day, r.total_cost AS cost, ` + dayTokenColumns + `, 0 AS activities
		FROM ` + dailySource(UsageSubject{Kind: kind}) + ` r
		WHERE r.day >= ? AND r.day < ? AND ` + on("r", spec.rows) + ` AND ` + on("r", identity) + `
		UNION ALL
		SELECT ` + key("a") + `, a.day, 0, ` + noDayTokens + `, a.activities FROM token_spend_daily_activities a
		WHERE a.grouping = ? AND a.day >= ? AND a.day < ? AND ` + on("a", spec.rows) + ` AND ` + on("a", identity) + `
	) d WHERE subject_key IN (` + in + `) GROUP BY subject_key, day ORDER BY subject_key, day`
	type record struct {
		Key                 string `db:"subject_key"`
		Day                 int64  `db:"day"`
		Cost                int64  `db:"cost"`
		UncachedInputTokens int64  `db:"uncached_input_tokens"`
		OutputTokens        int64  `db:"output_tokens"`
		CacheReadTokens     int64  `db:"cache_read_tokens"`
		CacheWriteTokens    int64  `db:"cache_write_tokens"`
		Activities          int64  `db:"activities"`
	}
	args := append([]any{fromDay, toDay}, identityArgs...)
	args = append(append(args, spec.activityGrouping, fromDay, toDay), identityArgs...)
	records, err := readRows[record](ctx, r, query, append(args, keys...)...)
	if err != nil {
		return nil, fmt.Errorf("SpendUsageRepo.DailyByRow: %w", err)
	}
	for _, rec := range records {
		byRow[rec.Key] = append(byRow[rec.Key], UsageDay{
			Day: rec.Day, Cost: rec.Cost, Activities: rec.Activities,
			UncachedInputTokens: rec.UncachedInputTokens, OutputTokens: rec.OutputTokens, CacheReadTokens: rec.CacheReadTokens, CacheWriteTokens: rec.CacheWriteTokens,
		})
	}
	return byRow, nil
}

type UsageServedModel struct {
	Name     *string
	Provider *string
	Totals   UsageTotals
}

type usageServedRecord struct {
	Key                 string  `db:"subject_key"`
	Name                *string `db:"resolved_model_name"`
	Provider            *string `db:"resolved_model_provider"`
	Cost                int64   `db:"cost"`
	UncachedInputCost   int64   `db:"uncached_input_cost"`
	OutputCost          int64   `db:"output_cost"`
	CacheReadCost       int64   `db:"cache_read_cost"`
	CacheWriteCost      int64   `db:"cache_write_cost"`
	UncachedInputTokens int64   `db:"uncached_input_tokens"`
	OutputTokens        int64   `db:"output_tokens"`
	CacheReadTokens     int64   `db:"cache_read_tokens"`
	CacheWriteTokens    int64   `db:"cache_write_tokens"`
	Calls               int64   `db:"calls"`
	UnpricedCalls       int64   `db:"unpriced_calls"`
	Activities          int64   `db:"activities"`
}

func (r *SpendUsageRepo) ServedModels(ctx context.Context, rows []UsageRow, fromDay, toDay int64) (map[string][]UsageServedModel, error) {
	served := make(map[string][]UsageServedModel, len(rows))
	keys := make([]any, 0, len(rows))
	for _, row := range rows {
		if key := UsageRowKey(row); key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return served, nil
	}
	in := placeholderList(len(keys))
	query := `SELECT subject_key, resolved_model_name, resolved_model_provider,
			` + sumOf("m", append(sumColumnNames, "activities")) + `
		FROM (SELECT COALESCE('i:' || CAST(x.model_config_id AS TEXT), 'n:' || x.model_name) AS subject_key,
				x.resolved_model_name, x.resolved_model_provider, x.total_cost AS cost,
				x.uncached_input_cost, x.output_cost, x.cache_read_cost, x.cache_write_cost,
				x.uncached_input_tokens, x.output_tokens, x.cache_read_tokens, x.cache_write_tokens,
				x.calls, x.unpriced_calls, x.activities
			FROM token_spend_daily_models x WHERE x.day >= ? AND x.day < ?) m
		WHERE subject_key IN (` + in + `)
		GROUP BY subject_key, resolved_model_name, resolved_model_provider
		ORDER BY subject_key, 4 DESC, resolved_model_name, resolved_model_provider`
	records, err := readRows[usageServedRecord](ctx, r, query, append([]any{fromDay, toDay}, keys...)...)
	if err != nil {
		return nil, fmt.Errorf("SpendUsageRepo.ServedModels: %w", err)
	}
	for _, rec := range records {
		served[rec.Key] = append(served[rec.Key], UsageServedModel{Name: rec.Name, Provider: rec.Provider, Totals: UsageTotals{
			Cost: rec.Cost, UncachedInputCost: rec.UncachedInputCost, OutputCost: rec.OutputCost,
			CacheReadCost: rec.CacheReadCost, CacheWriteCost: rec.CacheWriteCost,
			UncachedInputTokens: rec.UncachedInputTokens, OutputTokens: rec.OutputTokens,
			CacheReadTokens: rec.CacheReadTokens, CacheWriteTokens: rec.CacheWriteTokens,
			Calls: rec.Calls, UnpricedCalls: rec.UnpricedCalls, Activities: rec.Activities,
		}})
	}
	return served, nil
}

func placeholderList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func rowsIdentityFilter(spec usageKindSpec, rows []UsageRow) (string, []any) {
	var ids, names []any
	for _, row := range rows {
		switch {
		case row.SubjectID != nil:
			ids = append(ids, *row.SubjectID)
		case row.SubjectName != nil:
			names = append(names, *row.SubjectName)
		}
	}
	var parts []string
	var args []any
	idColumn, nameColumn := aliasPlaceholder+spec.identity[0], aliasPlaceholder+spec.identity[1]
	if len(ids) > 0 {
		parts, args = append(parts, idColumn+" IN ("+placeholderList(len(ids))+")"), append(args, ids...)
	}
	if len(names) > 0 {
		parts, args = append(parts, nameColumn+" IN ("+placeholderList(len(names))+")"), append(args, names...)
	}
	if len(parts) == 0 {
		return "1 = 0", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func (r *SpendUsageRepo) List(ctx context.Context, kind UsageKind, q UsageListQuery) (UsageList, error) {
	spec, ok := usageKinds[kind]
	if !ok {
		return UsageList{}, fmt.Errorf("SpendUsageRepo.List: unknown usage kind %q", kind)
	}
	sortExpr, ok := usageSortExprs[q.Sort]
	if !ok {
		return UsageList{}, fmt.Errorf("SpendUsageRepo.List: unknown sort %q", q.Sort)
	}
	if !q.Unpaged && q.Limit <= 0 {
		return UsageList{}, fmt.Errorf("SpendUsageRepo.List: limit %d is not positive", q.Limit)
	}
	if q.Budget != nil && kind != UsageUsers {
		return UsageList{}, fmt.Errorf("SpendUsageRepo.List: a budget filter applies to users only, not %q", kind)
	}
	sortsByServedModel := q.Sort == UsageSortServedModel
	if sortsByServedModel && kind != UsageModels {
		return UsageList{}, fmt.Errorf("SpendUsageRepo.List: a served-model sort applies to models only, not %q", kind)
	}
	key := func(alias string) string {
		return "COALESCE('i:' || " + on(alias, spec.subjectID) + ", 'n:' || " + on(alias, spec.subjectName) + ")"
	}
	labelExpr := nullText
	if spec.label != "" {
		labelExpr = "MAX(r." + spec.label + ")"
	}
	identity := strings.Join(prefixed("r", spec.identity), ", ")
	actIdentity := strings.Join(prefixed("a", spec.identity), ", ")
	ctes := []string{
		`spend_by_identity AS (SELECT ` + identity + `, ` + labelExpr + ` AS label, ` + on("r", sumColumns) + `,
			CAST(SUM(r.calls) AS BIGINT) AS calls, CAST(SUM(r.unpriced_calls) AS BIGINT) AS unpriced_calls
			FROM ` + dailySource(UsageSubject{Kind: kind}) + ` r
			WHERE r.day >= ? AND r.day < ? AND ` + on("r", spec.rows) + `
			GROUP BY ` + identity + `)`,
		`spend AS (SELECT ` + key("x") + ` AS subject_key, ` + on("x", spec.subjectID) + ` AS subject_id, ` + on("x", spec.subjectName) + ` AS subject_name,
			MAX(x.label) AS label, ` + sumOf("x", sumColumnNames) + `
			FROM spend_by_identity x
			GROUP BY ` + key("x") + `, ` + on("x", spec.subjectID) + `, ` + on("x", spec.subjectName) + `)`,
		`acts_by_identity AS (SELECT ` + actIdentity + `, CAST(SUM(a.activities) AS BIGINT) AS activities
			FROM token_spend_daily_activities a
			WHERE a.grouping = ? AND a.day >= ? AND a.day < ? AND ` + on("a", spec.rows) + `
			GROUP BY ` + actIdentity + `)`,
		`acts AS (SELECT ` + key("x") + ` AS subject_key, ` + sumOf("x", []string{"activities"}) + `
			FROM acts_by_identity x
			GROUP BY ` + key("x") + `)`,
	}
	subjects := `SELECT subject_id, subject_name FROM spend`
	var subjectArgs []any
	if q.AllSubjects {
		population, hiddenArgs, err := r.population(kind, spec)
		if err != nil {
			return UsageList{}, fmt.Errorf("SpendUsageRepo.List: %w", err)
		}
		subjects += ` UNION ` + population
		subjectArgs = append([]any{q.ToDay}, hiddenArgs...)
	}
	ctes = append(ctes, `subjects AS (`+subjects+`)`)
	servedExpr, servedJoin := nullText, ""
	if sortsByServedModel {
		ctes = append(ctes, servedModelCTE)
		subjectArgs = append(subjectArgs, q.FromDay, q.ToDay)
		servedExpr = "sv.name"
		servedJoin = "LEFT JOIN served sv ON sv.subject_key = COALESCE('i:' || s.subject_id, 'n:' || s.subject_name)\n\t\t"
	}
	emailExpr, systemExpr := nullText, "0"
	if kind == UsageUsers {
		emailExpr, systemExpr = "NULLIF(live.email, '')", "CASE WHEN live.provisioned_via = 'system' THEN 1 ELSE 0 END"
	}
	names := []string{spec.liveName, "sp.label", "s.subject_name"}
	if !spec.unnamedByID {
		names = append(names, "s.subject_id")
	}
	nameExpr := "COALESCE(" + strings.Join(names, ", ") + ")"
	if spec.unnamedByName {
		nameExpr = "CASE WHEN s.subject_id IS NOT NULL THEN " + nameExpr + " END"
	}
	inner := `SELECT s.subject_id, s.subject_name, ` + nameExpr + ` AS name,
			` + emailExpr + ` AS email, ` + systemExpr + ` AS system_user,
			CASE WHEN live.id IS NULL THEN 0 ELSE 1 END AS live_row,
			COALESCE(sp.cost, 0) AS cost, COALESCE(sp.uncached_input_cost, 0) AS uncached_input_cost,
			COALESCE(sp.output_cost, 0) AS output_cost, COALESCE(sp.cache_read_cost, 0) AS cache_read_cost,
			COALESCE(sp.cache_write_cost, 0) AS cache_write_cost,
			COALESCE(sp.uncached_input_tokens, 0) AS uncached_input_tokens, COALESCE(sp.output_tokens, 0) AS output_tokens,
			COALESCE(sp.cache_read_tokens, 0) AS cache_read_tokens, COALESCE(sp.cache_write_tokens, 0) AS cache_write_tokens,
			COALESCE(sp.calls, 0) AS calls, COALESCE(sp.unpriced_calls, 0) AS unpriced_calls,
			COALESCE(ac.activities, 0) AS activities, ` + servedExpr + ` AS served_model,
			COALESCE('i:' || s.subject_id, 'n:' || s.subject_name) AS subject_key
		FROM subjects s
		LEFT JOIN spend sp ON sp.subject_key = COALESCE('i:' || s.subject_id, 'n:' || s.subject_name)
		LEFT JOIN acts ac ON ac.subject_key = COALESCE('i:' || s.subject_id, 'n:' || s.subject_name)
		` + servedJoin + spec.liveJoin
	args := []any{q.FromDay, q.ToDay, spec.activityGrouping, q.FromDay, q.ToDay}
	args = append(args, subjectArgs...)
	var conds []string
	if q.Name != "" {
		predicate, arg := dbutil.LikeContainsFold("q.name", q.Name)
		conds = append(conds, predicate)
		args = append(args, arg)
	}
	if q.Budget != nil {
		predicate, budgetArgs := budgetAppliesCondition(q.Budget, "q.subject_id")
		onBudget := "(q.live_row = 1 AND " + predicate + ")"
		if q.Budget.IsDefault {
			onBudget = "(" + onBudget + " OR " + bareSubject + ")"
		}
		conds = append(conds, onBudget)
		args = append(args, budgetArgs...)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	direction := "ASC"
	if q.Descending {
		direction = "DESC"
	}
	with := `WITH ` + strings.Join(ctes, ", ")
	query := with + `
		SELECT q.subject_id, q.subject_name, COALESCE(q.name, '') AS name, q.email, q.system_user, q.live_row, q.cost, q.uncached_input_cost, q.output_cost,
			q.cache_read_cost, q.cache_write_cost, q.uncached_input_tokens, q.output_tokens, q.cache_read_tokens,
			q.cache_write_tokens, q.calls, q.unpriced_calls, q.activities, COUNT(*) OVER () AS total_count
		FROM (` + inner + `) q` + where + `
		ORDER BY (` + sortExpr + `) IS NULL, ` + sortExpr + ` ` + direction + `, q.subject_key`
	pageArgs := args
	if !q.Unpaged {
		query += ` LIMIT ? OFFSET ?`
		pageArgs = append(append([]any{}, args...), q.Limit, q.Offset)
	}
	records, err := readRows[usageRowRecord](ctx, r, query, pageArgs...)
	if err != nil {
		return UsageList{}, fmt.Errorf("SpendUsageRepo.List: %w", err)
	}
	list := UsageList{Rows: make([]UsageRow, len(records))}
	for i, rec := range records {
		list.Rows[i] = rec.row()
	}
	switch {
	case len(records) > 0:
		list.Count = records[0].Count
	case q.Offset > 0:
		list.Count, err = readVal[int](ctx, r, with+` SELECT COUNT(*) FROM (`+inner+`) q`+where, args...)
		if err != nil {
			return UsageList{}, fmt.Errorf("SpendUsageRepo.List: count: %w", err)
		}
	}
	return list, nil
}

func (r *SpendUsageRepo) WorkflowNodeCosts(ctx context.Context, workflow UsageSubject, fromDay, toDay int64) ([]UsageNodeCost, error) {
	return r.nodeCosts(ctx, "SpendUsageRepo.WorkflowNodeCosts", "token_spend_daily", "day", workflow, fromDay, toDay)
}

func (r *SpendUsageRepo) LedgerWorkflowNodeCosts(ctx context.Context, workflow UsageSubject, fromMillis, toMillis int64) ([]UsageNodeCost, error) {
	return r.nodeCosts(ctx, "SpendUsageRepo.LedgerWorkflowNodeCosts", "token_spend_ledger", "completed_time", workflow, fromMillis, toMillis)
}

func (r *SpendUsageRepo) nodeCosts(ctx context.Context, op, table, timeColumn string, workflow UsageSubject, from, to int64) ([]UsageNodeCost, error) {
	if workflow.Kind != UsageWorkflows {
		return nil, fmt.Errorf("%s: subject is a %q, not a workflow", op, workflow.Kind)
	}
	filter, args, err := filterSubject("x", workflow, workflowRootPredicates)
	if err != nil {
		return nil, fmt.Errorf(errOpWrap, op, err)
	}
	costs, err := readRows[UsageNodeCost](ctx, r, `SELECT x.root_node_id AS node_id,
		CAST(COALESCE(SUM(x.total_cost), 0) AS BIGINT) AS cost
		FROM `+table+` x WHERE x.`+timeColumn+` >= ? AND x.`+timeColumn+` < ? AND x.root_node_id IS NOT NULL AND `+filter+`
		GROUP BY x.root_node_id ORDER BY x.root_node_id`, append([]any{from, to}, args...)...)
	if err != nil {
		return nil, fmt.Errorf(errOpWrap, op, err)
	}
	return costs, nil
}

func workflowRootPredicates(usageKindSpec) (byID, byName string) {
	return "{T}.root_kind = 'workflow' AND {T}.root_id = ?", "{T}.root_kind = 'workflow' AND {T}.root_name = ?"
}

func (r *SpendUsageRepo) UserRootCosts(ctx context.Context, user UsageSubject, fromDay, toDay int64, limit int) ([]UsageRootCost, error) {
	if user.Kind != UsageUsers {
		return nil, fmt.Errorf("SpendUsageRepo.UserRootCosts: subject is a %q, not a user", user.Kind)
	}
	filter, args, err := subjectFilter("r", user)
	if err != nil {
		return nil, fmt.Errorf("SpendUsageRepo.UserRootCosts: %w", err)
	}
	query := `SELECT q.root_kind, q.root_id, q.root_name,
			COALESCE(CASE q.root_kind WHEN 'agent' THEN ag.name WHEN 'workflow' THEN wf.name END, q.label, q.root_name, q.root_id) AS name,
			q.cost
		FROM (SELECT r.root_kind, CAST(r.root_id AS TEXT) AS root_id, r.root_name, MAX(r.root_label) AS label,
				CAST(SUM(r.total_cost) AS BIGINT) AS cost
			FROM token_spend_daily r
			WHERE r.day >= ? AND r.day < ? AND r.root_kind IN ('agent', 'workflow') AND ` + filter + `
			GROUP BY r.root_kind, r.root_id, r.root_name) q
		LEFT JOIN agents ag ON q.root_kind = 'agent' AND CAST(ag.id AS TEXT) = q.root_id
		LEFT JOIN workflows wf ON q.root_kind = 'workflow' AND CAST(wf.id AS TEXT) = q.root_id
		ORDER BY q.cost DESC, q.root_kind, COALESCE(q.root_id, q.root_name)
		LIMIT ?`
	costs, err := readRows[UsageRootCost](ctx, r, query, append(append([]any{fromDay, toDay}, args...), limit)...)
	if err != nil {
		return nil, fmt.Errorf("SpendUsageRepo.UserRootCosts: %w", err)
	}
	return costs, nil
}

type UsageKindCosts struct {
	Users     int64 `db:"users_cost"`
	Agents    int64 `db:"agents_cost"`
	Workflows int64 `db:"workflows_cost"`
	Models    int64 `db:"models_cost"`
}

type tenantTotalsRow struct {
	Cost                int64 `db:"cost"`
	UncachedInputCost   int64 `db:"uncached_input_cost"`
	OutputCost          int64 `db:"output_cost"`
	CacheReadCost       int64 `db:"cache_read_cost"`
	CacheWriteCost      int64 `db:"cache_write_cost"`
	UncachedInputTokens int64 `db:"uncached_input_tokens"`
	OutputTokens        int64 `db:"output_tokens"`
	CacheReadTokens     int64 `db:"cache_read_tokens"`
	CacheWriteTokens    int64 `db:"cache_write_tokens"`
	Calls               int64 `db:"calls"`
	UnpricedCalls       int64 `db:"unpriced_calls"`
	UsersCost           int64 `db:"users_cost"`
	AgentsCost          int64 `db:"agents_cost"`
	WorkflowsCost       int64 `db:"workflows_cost"`
	ModelsCost          int64 `db:"models_cost"`
}

func (r *SpendUsageRepo) TenantTotals(ctx context.Context, fromDay, toDay int64) (UsageTotals, UsageKindCosts, error) {
	costOf := func(kind UsageKind, column string) string {
		return "CAST(COALESCE(SUM(CASE WHEN " + on("r", usageKinds[kind].rows) + " THEN r.total_cost END), 0) AS BIGINT) AS " + column
	}
	row, err := readRow[tenantTotalsRow](ctx, r, `SELECT `+on("r", sumColumns)+`,
		CAST(COALESCE(SUM(r.calls), 0) AS BIGINT) AS calls,
		CAST(COALESCE(SUM(r.unpriced_calls), 0) AS BIGINT) AS unpriced_calls,
		`+costOf(UsageUsers, "users_cost")+`, `+costOf(UsageAgents, "agents_cost")+`,
		`+costOf(UsageWorkflows, "workflows_cost")+`, `+costOf(UsageModels, "models_cost")+`
		FROM token_spend_daily r
		WHERE r.day >= ? AND r.day < ?`, fromDay, toDay)
	if err != nil {
		return UsageTotals{}, UsageKindCosts{}, fmt.Errorf("SpendUsageRepo.TenantTotals: %w", err)
	}
	activities, err := r.activities(ctx, UsageSubject{}, fromDay, toDay)
	if err != nil {
		return UsageTotals{}, UsageKindCosts{}, fmt.Errorf("SpendUsageRepo.TenantTotals: %w", err)
	}
	totals := UsageTotals{
		Cost: row.Cost, UncachedInputCost: row.UncachedInputCost, OutputCost: row.OutputCost, CacheReadCost: row.CacheReadCost, CacheWriteCost: row.CacheWriteCost,
		UncachedInputTokens: row.UncachedInputTokens, OutputTokens: row.OutputTokens, CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
		Calls: row.Calls, UnpricedCalls: row.UnpricedCalls, Activities: activities,
	}
	return totals, UsageKindCosts{Users: row.UsersCost, Agents: row.AgentsCost, Workflows: row.WorkflowsCost, Models: row.ModelsCost}, nil
}

func (r *SpendUsageRepo) Cost(ctx context.Context, s UsageSubject, fromDay, toDay int64) (int64, error) {
	filter, args, err := subjectFilter("r", s)
	if err != nil {
		return 0, fmt.Errorf("SpendUsageRepo.Cost: %w", err)
	}
	cost, err := readVal[int64](ctx, r, `SELECT CAST(COALESCE(SUM(r.total_cost), 0) AS BIGINT)
		FROM `+dailySource(s)+` r
		WHERE r.day >= ? AND r.day < ? AND `+filter, append([]any{fromDay, toDay}, args...)...)
	if err != nil {
		return 0, fmt.Errorf("SpendUsageRepo.Cost: %w", err)
	}
	return cost, nil
}

func (r *SpendUsageRepo) UserCosts(ctx context.Context, ids []string, fromDay, toDay int64) (map[string]int64, error) {
	lowered := make([]any, len(ids))
	for i, id := range ids {
		lowered[i] = strings.ToLower(id)
	}
	costs, err := r.costsBy(ctx, `CAST(r.user_id AS TEXT)`, `r.user_id`, "1 = 1", lowered, fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("SpendUsageRepo.UserCosts: %w", err)
	}
	return costs, nil
}

func (r *SpendUsageRepo) SubCosts(ctx context.Context, subs []string, fromDay, toDay int64) (map[string]int64, error) {
	keys := make([]any, len(subs))
	for i, sub := range subs {
		keys[i] = sub
	}
	costs, err := r.costsBy(ctx, `r.user_sub`, `r.user_sub`, "r.user_id IS NULL", keys, fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("SpendUsageRepo.SubCosts: %w", err)
	}
	return costs, nil
}

func (r *SpendUsageRepo) costsBy(ctx context.Context, key, column, where string, keys []any, fromDay, toDay int64) (map[string]int64, error) {
	costs := make(map[string]int64, len(keys))
	if len(keys) == 0 {
		return costs, nil
	}
	type keyCost struct {
		Key  string `db:"subject_key"`
		Cost int64  `db:"cost"`
	}
	rows, err := readRows[keyCost](ctx, r, `SELECT `+key+` AS subject_key, CAST(COALESCE(SUM(r.total_cost), 0) AS BIGINT) AS cost
		FROM token_spend_daily r
		WHERE r.day >= ? AND r.day < ? AND `+where+` AND `+column+` IN (`+placeholderList(len(keys))+`)
		GROUP BY `+key, append([]any{fromDay, toDay}, keys...)...)
	if err != nil {
		return nil, err
	}
	for _, c := range rows {
		costs[c.Key] = c.Cost
	}
	return costs, nil
}

type UsageSubjectInfo struct {
	Name       string
	Email      *string
	SystemUser bool
	Live       bool
}

type usageLiveRow struct {
	Name       *string `db:"name"`
	Email      *string `db:"email"`
	SystemUser bool    `db:"system_user"`
}

func (r *SpendUsageRepo) SubjectInfo(ctx context.Context, s UsageSubject, fromDay, toDay int64) (UsageSubjectInfo, bool, error) {
	filter, args, err := subjectFilter("r", s)
	if err != nil {
		return UsageSubjectInfo{}, false, fmt.Errorf("SpendUsageRepo.SubjectInfo: %w", err)
	}
	spec := usageKinds[s.Kind]
	var live *usageLiveRow
	if IsCanonicalUUID(s.ID) {
		if live, err = readRow[usageLiveRow](ctx, r, spec.liveRow, strings.ToLower(s.ID)); err != nil {
			return UsageSubjectInfo{}, false, fmt.Errorf("SpendUsageRepo.SubjectInfo: live row: %w", err)
		}
		if live != nil && live.Name != nil {
			return UsageSubjectInfo{Name: *live.Name, Email: live.Email, SystemUser: live.SystemUser, Live: true}, true, nil
		}
	}
	type booked struct {
		Label *string `db:"label"`
		Rows  int64   `db:"row_count"`
	}
	got, err := readRow[booked](ctx, r, `SELECT MAX(r.`+spec.label+`) AS label, COUNT(*) AS row_count
		FROM `+dailySource(s)+` r
		WHERE r.day >= ? AND r.day < ? AND `+filter, append([]any{fromDay, toDay}, args...)...)
	if err != nil {
		return UsageSubjectInfo{}, false, fmt.Errorf("SpendUsageRepo.SubjectInfo: %w", err)
	}
	if live == nil && got.Rows == 0 {
		return UsageSubjectInfo{}, false, nil
	}
	info := UsageSubjectInfo{Name: s.Name}
	if live != nil {
		info.Email, info.SystemUser, info.Live = live.Email, live.SystemUser, true
	}
	switch {
	case got.Label != nil:
		info.Name = *got.Label
	case info.Name == "" && !spec.unnamedByID:
		info.Name = strings.ToLower(s.ID)
	}
	return info, true, nil
}

func IsCanonicalUUID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil && len(id) == 36
}

type UserSubjectOwner struct {
	UserID string
	Shared bool
}

func (r *SpendUsageRepo) UserSubjectOwnership(ctx context.Context, sub string) (UserSubjectOwner, error) {
	ids, err := readVals[string](ctx, r, `SELECT CAST(id AS TEXT) FROM users WHERE sub = ? ORDER BY 1 LIMIT 2`, sub)
	if err != nil {
		return UserSubjectOwner{}, fmt.Errorf("SpendUsageRepo.UserSubjectOwnership: %w", err)
	}
	switch len(ids) {
	case 0:
		return UserSubjectOwner{}, nil
	case 1:
		return UserSubjectOwner{UserID: ids[0]}, nil
	}
	return UserSubjectOwner{Shared: true}, nil
}

func (r *SpendUsageRepo) RollupState(ctx context.Context) (UsageRollupState, error) {
	state, err := readRow[UsageRollupState](ctx, r,
		`SELECT refreshed_time, reconciled_time FROM token_spend_rollup_state WHERE id = 1`)
	if err != nil {
		return UsageRollupState{}, fmt.Errorf("SpendUsageRepo.RollupState: %w", err)
	}
	if state == nil {
		return UsageRollupState{}, nil
	}
	return *state, nil
}
