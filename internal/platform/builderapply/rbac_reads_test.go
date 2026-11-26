// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestPlan_ChargesTheReadScopesItUses(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	writeOnly := identityWith(rbac.ScopeAgentBuilderCreate, rbac.ScopeAgentBuilderUpdate)
	_, _, err := svc.Plan(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("probe_agent")},
	}, writeOnly)

	if err == nil {
		t.Fatal("planning enumerates connectors and skills; a caller without those read scopes must be refused")
	}
	if _, ok := err.(*entplatform.ForbiddenError); !ok {
		t.Fatalf("err is %T, want *ForbiddenError", err)
	}
	for _, scope := range []string{rbac.ScopeConnectorRead, rbac.ScopeSkillRead} {
		if !strings.Contains(err.Error(), scope) {
			t.Errorf("403 should name %q; got %q", scope, err)
		}
	}
}

func TestPlan_RefusesReadsBeforeResolvingReferences(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("secret-inventory-name")},
	}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}

	agent := agentComponent("probe_agent")
	agent.Connectors = []string{"a-name-that-does-not-exist"}

	_, _, err := svc.Plan(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agent}},
		identityWith(rbac.ScopeAgentBuilderCreate))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), "a-name-that-does-not-exist") {
		t.Errorf("the read refusal must come before reference resolution; got %q", err)
	}
}

func TestPlan_CredentialGuessIsIndistinguishableWithoutUpdateScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	stored := fullSQLValues()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", stored)},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	probe := func(password string) ApplyBuildRequest {
		values := fullSQLValues()
		values["password"] = password
		return ApplyBuildRequest{
			DryRun:     true,
			Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", values)},
		}
	}
	noUpdate := identityWith(append([]string{rbac.ScopeConnectorCreate}, buildReadScopes...)...)

	hit, _, hitErr := svc.Plan(ctx, probe("real-password"), noUpdate)
	miss, _, missErr := svc.Plan(ctx, probe("a-guess-that-is-wrong"), noUpdate)

	if (hitErr == nil) != (missErr == nil) {
		t.Fatalf("a right and a wrong guess produced different errors (%v vs %v) — that is the oracle", hitErr, missErr)
	}
	if planShape(hit) != planShape(miss) {
		t.Errorf("plan shapes differ: hit=%s miss=%s — a caller without %s learned whether their guess was right",
			planShape(hit), planShape(miss), rbac.ScopeConnectorUpdate)
	}
}

func TestPlan_UnwritableRotationIsReportedNotSwallowed(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	rotated := fullSQLValues()
	rotated["password"] = "a-freshly-rotated-password"
	noUpdate := identityWith(append([]string{
		rbac.ScopeConnectorCreate, rbac.ScopeAgentBuilderCreate,
	}, buildReadScopes...)...)

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{
			sqlConnector("db_conn", "postgres", rotated),
			agentComponent("unrelated_agent"),
		},
	}, noUpdate)
	if err != nil {
		t.Fatalf("the build must not be refused wholesale: %v", err)
	}

	op := opFor(t, resp, kindConnector, "db_conn")
	if op.Action != actionUnchanged {
		t.Errorf("Action = %q, want unchanged — the caller cannot write this credential", op.Action)
	}
	warnings := strings.Join(op.Warnings, " ")
	if !strings.Contains(warnings, "password") {
		t.Errorf("warnings = %v, want the unapplied credential field named", op.Warnings)
	}
	if !strings.Contains(warnings, rbac.ScopeConnectorUpdate) {
		t.Errorf("warnings = %v, want the scope that would apply it named", op.Warnings)
	}
	if !op.ChangesDeclined {
		t.Error("ChangesDeclined = false, want true for a credential the save could not apply")
	}

	if got := opFor(t, resp, kindAgent, "unrelated_agent"); got.Status != statusSucceeded {
		t.Errorf("unrelated component = %+v, want succeeded", got)
	}
}

func TestApply_IdenticalResaveIsNotDeclined(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	build := ApplyBuildRequest{Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())}}

	if _, err := svc.Apply(ctx, build, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	resp, err := svc.Apply(ctx, build, testActor)
	if err != nil {
		t.Fatalf("re-save: %v", err)
	}

	op := opFor(t, resp, kindConnector, "db_conn")
	if op.Action != actionUnchanged || op.ChangesDeclined {
		t.Errorf("op = %+v, want unchanged with nothing declined", op)
	}
}

func TestApply_SparseResaveKeepsStoredValuesWithoutDeclining(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	sparse := fullSQLValues()
	delete(sparse, "port")
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", sparse)},
	}, testActor)
	if err != nil {
		t.Fatalf("sparse re-save: %v", err)
	}

	op := opFor(t, resp, kindConnector, "db_conn")
	if !mentions(op.Warnings, "port") {
		t.Errorf("warnings = %v, want the kept stored value named", op.Warnings)
	}
	if op.ChangesDeclined {
		t.Errorf("op = %+v, want the kept value preserved, not declined", op)
	}
}

func TestPlan_ComparesStoredSecretsForCallersWhoMayWrite(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		DryRun:     true,
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindConnector, "db_conn"); op.Action != actionUnchanged {
		t.Errorf("Action = %q, want unchanged — an unrotated credential must not plan a write for a caller who can compare", op.Action)
	}
}

func planShape(plans []kindPlan) string {
	var parts []string
	for _, kp := range plans {
		parts = append(parts, fmt.Sprintf("%s:c=%d,u=%d,n=%d",
			kp.componentKind, len(kp.plan.Creates), len(kp.plan.Updates), len(kp.plan.Unchanged)))
	}
	slices.Sort(parts)
	return strings.Join(parts, "|")
}
