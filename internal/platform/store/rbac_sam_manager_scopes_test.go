// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const samManagerNarrowMigration = "rbac_sam_manager_narrow_content_reads"

const samManagerRoleID = "00000000-0000-0000-0000-000000000001"

var samManagerWildcardsBeforeNarrowing = []string{"agent:*:*", "analytics:*:*", "notify:*:*", "workflow:*:*"}

var samManagerScopesAfterNarrowing = []string{
	"*:*:read_usage", "agent:*:invoke", "agent:*:read_feedback", "agent_builder:*:*",
	"analytics:*:read", "budget:*:*", "builder:*:*", "connector:*:*", "deployment:*:*",
	"entrypoint:*:*", "evaluation:*:*", "model_config:*:*", "notify:*:send",
	"notify:*:send_others", "profile_provider:*:*", "skill:*:*", "tool:*:*",
	"toolset:*:*", "webui_settings:*:*", "workflow:*:invoke",
	"workflow:*:read_feedback", "workflow_builder:*:*",
}

var samManagerCoveredScopes = []string{
	rbac.ScopeReadUsage,
	rbac.ScopeAgentInvokeAny,
	rbac.ScopeAgentReadFeedbackAny,
	rbac.ScopeAgentBuilderCreate,
	rbac.ScopeAgentBuilderDelete,
	rbac.ScopeAgentBuilderDeploy,
	rbac.ScopeAgentBuilderRead,
	rbac.ScopeAgentBuilderTest,
	rbac.ScopeAgentBuilderUpdate,
	rbac.ScopeAnalyticsRead,
	rbac.ScopeBudgetAssign,
	rbac.ScopeBudgetCreate,
	rbac.ScopeBudgetDelete,
	rbac.ScopeBudgetRead,
	rbac.ScopeBudgetUpdate,
	rbac.ScopeBuilderUse,
	rbac.ScopeConnectorCreate,
	rbac.ScopeConnectorDelete,
	rbac.ScopeConnectorRead,
	rbac.ScopeConnectorUpdate,
	rbac.ScopeDeploymentRead,
	rbac.ScopeEntrypointCreate,
	rbac.ScopeEntrypointDelete,
	rbac.ScopeEntrypointDeploy,
	rbac.ScopeEntrypointRead,
	rbac.ScopeEntrypointUpdate,
	rbac.ScopeEvaluationCreate,
	rbac.ScopeEvaluationDelete,
	rbac.ScopeEvaluationInvoke,
	rbac.ScopeEvaluationRead,
	rbac.ScopeEvaluationUpdate,
	rbac.ScopeModelConfigCreate,
	rbac.ScopeModelConfigDelete,
	rbac.ScopeModelConfigRead,
	rbac.ScopeModelConfigUpdate,
	rbac.ScopeNotifySend,
	rbac.ScopeNotifySendOthers,
	rbac.ScopeProfileProviderRead,
	rbac.ScopeProfileProviderUpdate,
	rbac.ScopeSkillCreate,
	rbac.ScopeSkillDelete,
	rbac.ScopeSkillRead,
	rbac.ScopeSkillUpdate,
	rbac.ScopeToolAny,
	rbac.ScopeToolDatadogLogs,
	rbac.ScopeToolEmailSend,
	rbac.ScopeToolRead,
	rbac.ScopeToolsetCreate,
	rbac.ScopeToolsetDelete,
	rbac.ScopeToolsetRead,
	rbac.ScopeToolsetUpdate,
	rbac.ScopeWebuiSettingsRead,
	rbac.ScopeWebuiSettingsUpdate,
	rbac.ScopeWorkflowInvokeAny,
	rbac.ScopeWorkflowReadFeedbackAny,
	rbac.ScopeWorkflowBuilderCreate,
	rbac.ScopeWorkflowBuilderDelete,
	rbac.ScopeWorkflowBuilderDeploy,
	rbac.ScopeWorkflowBuilderRead,
	rbac.ScopeWorkflowBuilderUpdate,
}

func TestSAMManagerEffectiveScopes_Pinned(t *testing.T) {
	forEachPlatformDialectAt(t, samManagerNarrowMigration, func(t *testing.T, db *DB, _ int64) {
		if err := db.Migrate(t.Context()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		granted := sentinelScopes(t.Context(), t, db)

		var covered []string
		for _, scope := range rbac.DeclaredScopes() {
			if auth.HasScope(granted, scope) {
				covered = append(covered, scope)
			}
		}

		for _, scope := range covered {
			if !slices.Contains(samManagerCoveredScopes, scope) {
				t.Errorf("sam_manager now covers %q. Add it to samManagerCoveredScopes if a manager should hold it; otherwise narrow the role with a migration", scope)
			}
		}
		for _, scope := range samManagerCoveredScopes {
			if !slices.Contains(covered, scope) {
				t.Errorf("sam_manager no longer covers %q; remove it from samManagerCoveredScopes if that is intended", scope)
			}
		}
	})
}

func TestSAMManagerEffectiveScopes_ContentReadPredicates(t *testing.T) {
	forEachPlatformDialectAt(t, samManagerNarrowMigration, func(t *testing.T, db *DB, _ int64) {
		if err := db.Migrate(t.Context()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		granted := sentinelScopes(t.Context(), t, db)

		reach := rbac.ResolveFeedbackReach(granted)
		if !reach.AgentRead.All || !reach.WorkflowRead.All {
			t.Errorf("sam_manager cannot read every feedback row: %+v", reach)
		}
		if !reach.AgentTrace.IsEmpty() {
			t.Errorf("sam_manager reaches traces: %+v", reach.AgentTrace)
		}
		if !rbac.HasStrictScope(granted, rbac.ScopeReadUsage) {
			t.Errorf("sam_manager cannot read the usage dashboard; scopes = %v", granted)
		}
		for _, denied := range []string{rbac.ScopeAnalyticsReadContent, rbac.ScopeNotifyReadOthers, rbac.ScopeActivityRead} {
			if auth.HasScope(granted, denied) {
				t.Errorf("sam_manager holds %q", denied)
			}
		}
		for _, invoke := range []string{
			rbac.AgentInvokeScope("orchestrator"),
			rbac.InvokeScopeForInstance("0193a2b0-1c2d-7e3f-8a9b-0c1d2e3f4a5b"),
			rbac.WorkflowInvokeScope("0193a2b0-1c2d-7e3f-8a9b-0c1d2e3f4a5c"),
		} {
			if !auth.HasScope(granted, invoke) {
				t.Errorf("sam_manager cannot invoke: %q", invoke)
			}
		}
	})
}

func TestSAMManagerNarrowMigration_RoundTrip(t *testing.T) {
	forEachPlatformDialectAt(t, samManagerNarrowMigration, func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		before := sentinelScopes(ctx, t, db)
		for _, scope := range samManagerWildcardsBeforeNarrowing {
			if !slices.Contains(before, scope) {
				t.Fatalf("precondition: sam_manager lacks %q before narrowing: %v", scope, before)
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		if got := sentinelScopes(ctx, t, db); !slices.Equal(got, samManagerScopesAfterNarrowing) {
			t.Errorf("after up: sam_manager scopes = %v, want %v", got, samManagerScopesAfterNarrowing)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		if got := sentinelScopes(ctx, t, db); !slices.Equal(got, before) {
			t.Errorf("after down: sam_manager scopes = %v, want %v", got, before)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply %d: %v", version, err)
		}
		if got := sentinelScopes(ctx, t, db); !slices.Equal(got, samManagerScopesAfterNarrowing) {
			t.Errorf("after re-apply: sam_manager scopes = %v, want %v", got, samManagerScopesAfterNarrowing)
		}
	})
}

func TestSAMManagerNarrowMigration_LeavesOtherRolesAlone(t *testing.T) {
	forEachPlatformDialectAt(t, samManagerNarrowMigration, func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}

		const customID = "11111111-1111-1111-1111-111111111111"
		customScopes := append(slices.Clone(samManagerWildcardsBeforeNarrowing), "agent:*:invoke", "analytics:*:read")
		slices.Sort(customScopes)
		if _, err := db.ExecContext(ctx,
			`INSERT INTO rbac_roles (id, name, description, created_by, updated_by, created_time, updated_time)
			 VALUES (?, 'sam_manager_copy', '', 'seed', 'seed', 0, 0)`, customID); err != nil {
			t.Fatalf("seed customer role: %v", err)
		}
		for _, scope := range customScopes {
			if _, err := db.ExecContext(ctx,
				`INSERT INTO rbac_role_scopes (role_id, scope) VALUES (?, ?)`, customID, scope); err != nil {
				t.Fatalf("seed customer scope %q: %v", scope, err)
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		if got := roleScopes(ctx, t, db, customID); !slices.Equal(got, customScopes) {
			t.Errorf("customer scopes after up = %v, want %v", got, customScopes)
		}
		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		if got := roleScopes(ctx, t, db, customID); !slices.Equal(got, customScopes) {
			t.Errorf("customer scopes after down = %v, want %v", got, customScopes)
		}
	})
}

func TestSAMManagerNarrowMigration_DeletedRoleStaysDeleted(t *testing.T) {
	forEachPlatformDialectAt(t, samManagerNarrowMigration, func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM rbac_role_scopes WHERE role_id = ?`, samManagerRoleID); err != nil {
			t.Fatalf("delete sam_manager scopes: %v", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM rbac_roles WHERE id = ?`, samManagerRoleID); err != nil {
			t.Fatalf("delete sam_manager: %v", err)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		if got := sentinelScopes(ctx, t, db); len(got) != 0 {
			t.Errorf("up resurrected scopes for the deleted role: %v", got)
		}
		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		if got := sentinelScopes(ctx, t, db); len(got) != 0 {
			t.Errorf("down resurrected scopes for the deleted role: %v", got)
		}
	})
}

func sentinelScopes(ctx context.Context, t *testing.T, db *DB) []string {
	t.Helper()
	return roleScopes(ctx, t, db, samManagerRoleID)
}

func roleScopes(ctx context.Context, t *testing.T, db *DB, roleID string) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx,
		`SELECT scope FROM rbac_role_scopes WHERE role_id = ?`, roleID)
	if err != nil {
		t.Fatalf("query role %s scopes: %v", roleID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			t.Fatalf("scan scope: %v", err)
		}
		out = append(out, scope)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate role %s scopes: %v", roleID, err)
	}
	slices.Sort(out)
	return out
}
