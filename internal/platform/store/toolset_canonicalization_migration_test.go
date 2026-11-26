// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestToolsetCanonicalizationMigration(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "canon.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), 72); err != nil {
		t.Fatalf("migrate up to 72: %v", err)
	}

	seedAgent := func(id, name string) {
		t.Helper()
		if _, err := db.RawDB().ExecContext(ctx,
			`INSERT INTO agents (id, name, type, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, 'standard', 'test', 'test', 1, 1)`, id, name); err != nil {
			t.Fatalf("seed agent %s: %v", name, err)
		}
	}
	seedToolset := func(agentID, toolset, excluded string) {
		t.Helper()
		if _, err := db.RawDB().ExecContext(ctx,
			`INSERT INTO agent_toolsets (agent_id, toolset, excluded_tools) VALUES (?, ?, ?)`,
			agentID, toolset, excluded); err != nil {
			t.Fatalf("seed toolset %s/%s: %v", agentID, toolset, err)
		}
	}

	const (
		aliasOnly = "11111111-1111-1111-1111-111111111111"
		bothSpell = "22222222-2222-2222-2222-222222222222"
		untouched = "33333333-3333-3333-3333-333333333333"
	)
	seedAgent(aliasOnly, "alias-only")
	seedAgent(bothSpell, "both-spellings")
	seedAgent(untouched, "untouched")

	seedToolset(aliasOnly, "web_tools", `["web_search_google"]`)
	seedToolset(bothSpell, "web_tools", `["ALIAS_ROW_EXCLUSIONS"]`)
	seedToolset(bothSpell, "builtin_web_request_tools", `["CANONICAL_ROW_EXCLUSIONS"]`)
	seedToolset(untouched, "my_custom_package", "")
	seedToolset(untouched, "general_agent_tools", "")

	if _, err := db.RawDB().ExecContext(ctx,
		`INSERT INTO agent_builtin_toolset_configs (agent_id, toolset_name, config_values)
		 VALUES (?, 'research', '{"deep_research":{"max_iterations":2}}')`, aliasOnly); err != nil {
		t.Fatalf("seed builtin toolset config: %v", err)
	}

	if _, err := db.RawDB().ExecContext(ctx,
		`INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'wf', '{}', 'test', 'test', 1, 1)`, aliasOnly); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if _, err := db.RawDB().ExecContext(ctx,
		`INSERT INTO workflow_toolsets (workflow_id, toolset) VALUES (?, 'image_tools')`, aliasOnly); err != nil {
		t.Fatalf("seed workflow toolset: %v", err)
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), 73); err != nil {
		t.Fatalf("migrate up to 73: %v", err)
	}

	toolsetsFor := func(agentID string) []string {
		t.Helper()
		rows, err := db.RawDB().QueryContext(ctx,
			`SELECT toolset FROM agent_toolsets WHERE agent_id = ? ORDER BY toolset`, agentID)
		if err != nil {
			t.Fatalf("query toolsets: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var ts string
			if err := rows.Scan(&ts); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, ts)
		}
		return out
	}

	if got, want := toolsetsFor(aliasOnly), []string{"builtin_web_request_tools"}; !slices.Equal(got, want) {
		t.Errorf("alias-only agent toolsets = %v, want %v", got, want)
	}

	if got, want := toolsetsFor(bothSpell), []string{"builtin_web_request_tools"}; !slices.Equal(got, want) {
		t.Errorf("both-spellings agent toolsets = %v, want %v (the alias row must collapse, not duplicate)", got, want)
	}
	var survivingExclusions string
	if err := db.RawDB().QueryRowContext(ctx,
		`SELECT COALESCE(excluded_tools, '') FROM agent_toolsets WHERE agent_id = ?`, bothSpell,
	).Scan(&survivingExclusions); err != nil {
		t.Fatalf("query surviving exclusions: %v", err)
	}
	if survivingExclusions != `["CANONICAL_ROW_EXCLUSIONS"]` {
		t.Errorf("surviving exclusions = %q, want the canonical row's", survivingExclusions)
	}

	if got, want := toolsetsFor(untouched), []string{"general_agent_tools", "my_custom_package"}; !slices.Equal(got, want) {
		t.Errorf("non-alias names = %v, want %v (must not be rewritten)", got, want)
	}

	var configToolset string
	if err := db.RawDB().QueryRowContext(ctx,
		`SELECT toolset_name FROM agent_builtin_toolset_configs WHERE agent_id = ?`, aliasOnly,
	).Scan(&configToolset); err != nil {
		t.Fatalf("query builtin toolset config: %v", err)
	}
	if configToolset != "builtin_research_tools" {
		t.Errorf("agent_builtin_toolset_configs.toolset_name = %q, want builtin_research_tools", configToolset)
	}

	var workflowToolset string
	if err := db.RawDB().QueryRowContext(ctx,
		`SELECT toolset FROM workflow_toolsets WHERE workflow_id = ?`, aliasOnly,
	).Scan(&workflowToolset); err != nil {
		t.Fatalf("query workflow toolset: %v", err)
	}
	if workflowToolset != "builtin_image_tools" {
		t.Errorf("workflow_toolsets.toolset = %q, want builtin_image_tools", workflowToolset)
	}

	if err := db.MigrateDown(ctx); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("re-apply migrations: %v", err)
	}
}

func TestToolsetCanonicalizationMigration_CoversEveryAlias(t *testing.T) {
	t.Parallel()

	migrations := map[string]string{}
	for dialect, path := range map[string]string{
		"sqlite":   "migrations/sqlite/00073_canonicalize_toolset_names.sql",
		"postgres": "migrations/postgres/00077_canonicalize_toolset_names.sql",
	} {
		fsys := sqliteMigrationFS
		if dialect == "postgres" {
			fsys = postgresMigrationFS
		}
		sqlBytes, err := fsys.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s migration: %v", dialect, err)
		}
		migrations[dialect] = string(sqlBytes)
	}

	var aliasCount int
	for name := range schema.ValidBuiltinGroups {
		canonical := schema.CanonicalBuiltinGroup(name)
		if canonical == name {
			continue
		}
		aliasCount++
		pair := "('" + name + "', '" + canonical + "')"
		for dialect, migration := range migrations {
			if !strings.Contains(migration, pair) {
				t.Errorf("%s migration is missing the alias pair %s; rows stored as %q would never be backfilled", dialect, pair, name)
			}
		}
	}
	if aliasCount == 0 {
		t.Fatal("expected schema to define at least one builtin-group alias")
	}
}
