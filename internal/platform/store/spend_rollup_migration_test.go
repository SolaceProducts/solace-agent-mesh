// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var spendRollupSchemaObjects = []string{
	"token_spend_daily",
	"idx_token_spend_daily_user",
	"idx_token_spend_daily_user_sub",
	"idx_token_spend_daily_agent",
	"idx_token_spend_daily_agent_name",
	"idx_token_spend_daily_root",
	"idx_token_spend_daily_root_name",
	"idx_token_spend_daily_model",
	"idx_token_spend_daily_model_name",
	"token_spend_daily_activities",
	"idx_token_spend_daily_activities_user",
	"idx_token_spend_daily_activities_user_sub",
	"idx_token_spend_daily_activities_root",
	"idx_token_spend_daily_activities_root_name",
	"idx_token_spend_daily_activities_agent",
	"idx_token_spend_daily_activities_agent_name",
	"idx_token_spend_daily_activities_model",
	"idx_token_spend_daily_activities_model_name",
	"token_spend_rollup_state",
}

func TestSpendRollupsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "token_spend_rollups", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		assertPresent := func(stage string) {
			t.Helper()
			for _, name := range spendRollupSchemaObjects {
				if !schemaObjectExists(t, db, name) {
					t.Errorf("%s: %s missing", stage, name)
				}
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		assertPresent("after up")

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		for _, name := range spendRollupSchemaObjects {
			if schemaObjectExists(t, db, name) {
				t.Errorf("after down: %s survived the rollback", name)
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		assertPresent("after re-apply")
	})
}

func TestSpendRollupsMigration_Constraints(t *testing.T) {
	t.Parallel()
	const (
		userRow  = "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77"
		agentRow = "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e76"
		rootRow  = "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e78"
		modelRow = "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e79"
		utcDay   = int64(20_000) * 86_400_000
	)
	atRollups := templatesMigratedTo(t, "token_spend_rollups")
	for name, tc := range map[string]struct {
		insert         rollupFixture
		wantConstraint string
	}{
		"a daily row naming every subject by id":   {insert: dailyRow(map[string]any{"user_id": userRow, "agent_id": agentRow, "root_kind": "workflow", "root_id": rootRow, "root_node_id": "node-1", "model_config_id": modelRow})},
		"a daily row with no root and no agent":    {insert: dailyRow(map[string]any{"user_id": userRow, "model_config_id": modelRow})},
		"a daily row naming every subject by name": {insert: dailyRow(map[string]any{"user_sub": "alice", "agent_name": "qa", "workflow_name": "nightly", "root_kind": "agent", "root_name": "qa", "model_name": "gpt-x"})},
		"a daily row of unpriced calls only":       {insert: dailyRow(map[string]any{"user_id": userRow, "model_name": "gpt-x", "unpriced_calls": 2})},

		"a daily row with both user sides":                {insert: dailyRow(map[string]any{"user_id": userRow, "user_sub": "alice", "model_config_id": modelRow}), wantConstraint: "token_spend_daily_one_user"},
		"a daily row with no user":                        {insert: dailyRow(map[string]any{"model_config_id": modelRow}), wantConstraint: "token_spend_daily_one_user"},
		"a daily row with both model sides":               {insert: dailyRow(map[string]any{"user_id": userRow, "model_config_id": modelRow, "model_name": "gpt-x"}), wantConstraint: "token_spend_daily_one_model"},
		"a daily row with no model":                       {insert: dailyRow(map[string]any{"user_id": userRow}), wantConstraint: "token_spend_daily_one_model"},
		"a daily row with both agent sides":               {insert: dailyRow(map[string]any{"user_id": userRow, "agent_id": agentRow, "agent_name": "qa", "model_config_id": modelRow}), wantConstraint: "token_spend_daily_agent_pair"},
		"a daily row with both workflow sides":            {insert: dailyRow(map[string]any{"user_id": userRow, "workflow_id": rootRow, "workflow_name": "nightly", "model_config_id": modelRow}), wantConstraint: "token_spend_daily_workflow_pair"},
		"a daily row with both root sides":                {insert: dailyRow(map[string]any{"user_id": userRow, "root_kind": "agent", "root_id": rootRow, "root_name": "qa", "model_config_id": modelRow}), wantConstraint: "token_spend_daily_root"},
		"a daily row with a root kind but no root":        {insert: dailyRow(map[string]any{"user_id": userRow, "root_kind": "agent", "model_config_id": modelRow}), wantConstraint: "token_spend_daily_root"},
		"a daily row with a root but no kind":             {insert: dailyRow(map[string]any{"user_id": userRow, "root_id": rootRow, "model_config_id": modelRow}), wantConstraint: "token_spend_daily_root"},
		"a daily row with a node but no root":             {insert: dailyRow(map[string]any{"user_id": userRow, "root_node_id": "node-1", "model_config_id": modelRow}), wantConstraint: "token_spend_daily_root_node"},
		"a daily row off a UTC midnight":                  {insert: dailyRow(map[string]any{"day": utcDay + 1, "user_id": userRow, "model_config_id": modelRow}), wantConstraint: "token_spend_daily_utc_day"},
		"a daily row with no calls":                       {insert: dailyRow(map[string]any{"user_id": userRow, "model_config_id": modelRow, "calls": 0}), wantConstraint: "token_spend_daily_calls"},
		"a daily row with more unpriced calls than calls": {insert: dailyRow(map[string]any{"user_id": userRow, "model_config_id": modelRow, "unpriced_calls": 3}), wantConstraint: "token_spend_daily_calls"},
		"a daily row with negative unpriced calls":        {insert: dailyRow(map[string]any{"user_id": userRow, "model_config_id": modelRow, "unpriced_calls": -1}), wantConstraint: "token_spend_daily_calls"},
		"a daily row with negative tokens":                {insert: dailyRow(map[string]any{"user_id": userRow, "model_config_id": modelRow, "output_tokens": -1}), wantConstraint: "token_spend_daily_tokens_non_negative"},
		"a daily row with a negative cost":                {insert: dailyRow(map[string]any{"user_id": userRow, "model_config_id": modelRow, "output_cost": -1}), wantConstraint: "token_spend_daily_costs_non_negative"},

		"a root activity row":                 {insert: activityRow("root", map[string]any{"user_id": userRow, "root_kind": "agent", "root_id": rootRow})},
		"a root activity row with no root":    {insert: activityRow("root", map[string]any{"user_sub": "alice"})},
		"an agent activity row":               {insert: activityRow("agent", map[string]any{"user_id": userRow, "agent_name": "qa"})},
		"an agent activity row with no agent": {insert: activityRow("agent", map[string]any{"user_id": userRow})},
		"a model activity row":                {insert: activityRow("model", map[string]any{"user_id": userRow, "model_name": "gpt-x"})},

		"an unknown grouping":                        {insert: activityRow("tool", map[string]any{"user_id": userRow}), wantConstraint: "token_spend_daily_activities_grouping"},
		"an activity row with both user sides":       {insert: activityRow("root", map[string]any{"user_id": userRow, "user_sub": "alice"}), wantConstraint: "token_spend_daily_activities_one_user"},
		"a root on an agent activity row":            {insert: activityRow("agent", map[string]any{"user_id": userRow, "root_kind": "agent", "root_id": rootRow}), wantConstraint: "token_spend_daily_activities_root"},
		"an activity row with both agent sides":      {insert: activityRow("agent", map[string]any{"user_id": userRow, "agent_id": agentRow, "agent_name": "qa"}), wantConstraint: "token_spend_daily_activities_agent"},
		"an agent on a root activity row":            {insert: activityRow("root", map[string]any{"user_id": userRow, "agent_name": "qa"}), wantConstraint: "token_spend_daily_activities_agent"},
		"a model on an agent activity row":           {insert: activityRow("agent", map[string]any{"user_id": userRow, "agent_name": "qa", "model_name": "gpt-x"}), wantConstraint: "token_spend_daily_activities_model"},
		"a model activity row with no model":         {insert: activityRow("model", map[string]any{"user_id": userRow}), wantConstraint: "token_spend_daily_activities_model"},
		"a model activity row with both model sides": {insert: activityRow("model", map[string]any{"user_id": userRow, "model_config_id": modelRow, "model_name": "gpt-x"}), wantConstraint: "token_spend_daily_activities_model"},
		"a zero activity count":                      {insert: activityRow("agent", map[string]any{"user_id": userRow, "agent_name": "qa", "activities": 0}), wantConstraint: "token_spend_daily_activities_positive"},
		"an activity row off a UTC midnight":         {insert: activityRow("agent", map[string]any{"day": utcDay + 1, "user_id": userRow, "agent_name": "qa"}), wantConstraint: "token_spend_daily_activities_utc_day"},

		"a models row":                            {insert: modelsRow(map[string]any{"model_config_id": modelRow, "resolved_model_name": "claude", "resolved_model_provider": "anthropic"})},
		"a models row with no details":            {insert: modelsRow(map[string]any{"model_name": "gpt-x"})},
		"a models row with both model sides":      {insert: modelsRow(map[string]any{"model_config_id": modelRow, "model_name": "gpt-x"}), wantConstraint: "token_spend_daily_models_one_model"},
		"a models row with no model":              {insert: modelsRow(map[string]any{}), wantConstraint: "token_spend_daily_models_one_model"},
		"a models row with more tasks than calls": {insert: modelsRow(map[string]any{"model_name": "gpt-x", "activities": 3}), wantConstraint: "token_spend_daily_models_activities"},
		"a models row off a UTC midnight":         {insert: modelsRow(map[string]any{"day": utcDay + 1, "model_name": "gpt-x"}), wantConstraint: "token_spend_daily_models_utc_day"},
		"a models row with no calls":              {insert: modelsRow(map[string]any{"model_name": "gpt-x", "calls": 0, "activities": 0}), wantConstraint: "token_spend_daily_models_calls"},

		"the state row":      {insert: rollupFixture{table: "token_spend_rollup_state", values: map[string]any{"id": 1, "grain_version": 1}}},
		"a second state row": {insert: rollupFixture{table: "token_spend_rollup_state", values: map[string]any{"id": 2, "grain_version": 1}}, wantConstraint: "token_spend_rollup_state_singleton"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			forEachPlatformDialectClone(t, atRollups, func(t *testing.T, db *DB) {
				query, args := tc.insert.sql(utcDay)
				_, err := db.ExecContext(t.Context(), db.Rebind(query), args...)
				switch {
				case tc.wantConstraint == "" && err != nil:
					t.Fatalf("insert refused: %v", err)
				case tc.wantConstraint != "" && err == nil:
					t.Fatalf("insert accepted, want %s to refuse it", tc.wantConstraint)
				case tc.wantConstraint != "" && !namesConstraint(err, tc.wantConstraint):
					t.Fatalf("insert refused by %v, want %s", err, tc.wantConstraint)
				}
			})
		})
	}
}

var migratedTemplateBuilds atomic.Int64

func templatesMigratedTo(t *testing.T, migration string) platformTemplates {
	t.Helper()
	build := func(dbURL string, migrations fs.FS) func() (*dbtest.Template, error) {
		version := migrationVersion(t, migrations, migration)
		name := fmt.Sprintf("platform_%s_%d_%d", migration, os.Getpid(), migratedTemplateBuilds.Add(1))
		tmpl, err := dbtest.NewTemplateFromURL(dbURL, name, func(u string) error {
			db, err := Open(u, nil)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			return dbutil.MigrateUpTo(t.Context(), db.migrateConfig(), version)
		})
		if err != nil {
			t.Fatalf("template at %s: %v", migration, err)
		}
		t.Cleanup(tmpl.Close)
		return func() (*dbtest.Template, error) { return tmpl, nil }
	}
	templates := platformTemplates{sqlite: build("", sqliteMigrations())}
	if postgresLaneActive() {
		templates.postgres = build(platformTestDBURL(), postgresMigrations())
	}
	return templates
}

func TestSpendRollupsMigration_StateRowNeedsItsID(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "token_spend_rollups", func(t *testing.T, db *DB, version int64) {
		if err := dbutil.MigrateUpTo(t.Context(), db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		if _, err := db.ExecContext(t.Context(), `INSERT INTO token_spend_rollup_state (grain_version) VALUES (1)`); err == nil {
			t.Fatal("a state row without an id was accepted")
		}
	})
}

func TestSpendRollupsMigration_PostgresFillfactor(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "token_spend_rollups", func(t *testing.T, db *DB, version int64) {
		if db.Dialect() != "postgres" {
			return
		}
		if err := dbutil.MigrateUpTo(t.Context(), db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		requireFillfactor80(t, db, "token_spend_daily", "token_spend_daily_activities", "token_spend_daily_models")
	})
}

func requireFillfactor80(t *testing.T, db *DB, tables ...string) {
	t.Helper()
	for _, table := range tables {
		options, err := dbutil.QueryVal[string](t.Context(), db,
			`SELECT COALESCE(array_to_string(reloptions, ','), '') FROM pg_class WHERE relname = $1`, table)
		if err != nil {
			t.Fatalf("read %s options: %v", table, err)
		}
		if options != "fillfactor=80" {
			t.Errorf("%s options = %q, want fillfactor=80", table, options)
		}
	}
}

type rollupFixture struct {
	table  string
	values map[string]any
}

func dailyRow(values map[string]any) rollupFixture {
	row := map[string]any{
		"grain_key": "k", "calls": 2, "unpriced_calls": 0,
		"uncached_input_tokens": 0, "output_tokens": 0, "cache_read_tokens": 0, "cache_write_tokens": 0,
		"uncached_input_cost": 0, "output_cost": 0, "cache_read_cost": 0, "cache_write_cost": 0, "total_cost": 0,
	}
	maps.Copy(row, values)
	return rollupFixture{table: "token_spend_daily", values: row}
}

func modelsRow(values map[string]any) rollupFixture {
	row := map[string]any{
		"grain_key": "k", "calls": 2, "unpriced_calls": 0, "activities": 1,
		"uncached_input_tokens": 0, "output_tokens": 0, "cache_read_tokens": 0, "cache_write_tokens": 0,
		"uncached_input_cost": 0, "output_cost": 0, "cache_read_cost": 0, "cache_write_cost": 0, "total_cost": 0,
	}
	maps.Copy(row, values)
	return rollupFixture{table: "token_spend_daily_models", values: row}
}

func activityRow(grouping string, values map[string]any) rollupFixture {
	row := map[string]any{"grouping": grouping, "grain_key": "k", "activities": 1}
	maps.Copy(row, values)
	return rollupFixture{table: "token_spend_daily_activities", values: row}
}

func (f rollupFixture) sql(defaultDay int64) (string, []any) {
	values := maps.Clone(f.values)
	if _, ok := values["day"]; !ok && f.table != "token_spend_rollup_state" {
		values["day"] = defaultDay
	}
	columns := slices.Sorted(maps.Keys(values))
	args := make([]any, len(columns))
	for i, column := range columns {
		args[i] = values[column]
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", ")
	return "INSERT INTO " + f.table + " (" + strings.Join(columns, ", ") + ") VALUES (" + placeholders + ")", args
}

func namesConstraint(err error, constraint string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(constraint) + `\b`).MatchString(err.Error())
}
