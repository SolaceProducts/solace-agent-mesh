// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func buildRequestBody(t *testing.T, dryRun bool, comps ...BuildComponentInput) string {
	t.Helper()
	raw, err := json.Marshal(ApplyBuildRequest{Components: comps, DryRun: dryRun})
	if err != nil {
		t.Fatalf("marshal build request: %v", err)
	}
	return string(raw)
}

func TestBuildsNeedTheUpdateScopeToTouchAWorkflowUnderAnyRespelling(t *testing.T) {
	t.Parallel()
	spellings := []struct {
		name    string
		stored  string
		request string
	}{
		{"exact", "Order Flow", "Order Flow"},
		{"case", "Order Flow", "ORDER FLOW"},
		{"spaces", "Order Flow", "OrderFlow"},
		{"case and spaces", "OrderFlow", "order flow"},
		{"non-ASCII case", "École Flow", "école flow"},
		{"non-ASCII spaces", "ÉcoleFlow", "École Flow"},
	}
	scopeSets := []struct {
		name      string
		scopes    []string
		mayUpdate bool
	}{
		{"create only", withReads(rbac.ScopeWorkflowBuilderCreate), false},
		{"create and update", withReads(rbac.ScopeWorkflowBuilderCreate, rbac.ScopeWorkflowBuilderUpdate), true},
	}

	for _, spelling := range spellings {
		for _, scopes := range scopeSets {
			for _, dryRun := range []bool{false, true} {
				name := spelling.name + "/" + scopes.name + "/real"
				if dryRun {
					name = spelling.name + "/" + scopes.name + "/dry run"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					svc := newTestService(t)
					seeded := seedStoredWorkflow(t, svc, spelling.stored, workflowComponent("order_wf", spelling.stored, "Fulfilment"))
					before := storedSnapshot(t, seeded)

					rec := postBuild(t, svc, buildRequestBody(t, dryRun, workflowComponent("order_wf", spelling.request, "Shipping")), scopes.scopes)

					list, err := svc.deps.listWorkflows(t.Context())
					if err != nil || len(list) != 1 {
						t.Fatalf("workflows = %d, err = %v; a build must never add a second row for a taken name", len(list), err)
					}
					if !scopes.mayUpdate {
						if rec.Code != http.StatusForbidden {
							t.Fatalf("status = %d, want 403 for a create-only caller; body %s", rec.Code, rec.Body.String())
						}
						if got := storedSnapshot(t, list[0]); got != before {
							t.Errorf("the refused build changed the workflow:\n before %s\n after  %s", before, got)
						}
						return
					}
					if rec.Code != http.StatusOK {
						t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
					}
					var resp ApplyBuildResponse
					if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
						t.Fatalf("decode response: %v", err)
					}
					op := opFor(t, &resp, kindWorkflow, "order_wf")
					if op.Action != actionUpdate {
						t.Errorf("action = %q, want update", op.Action)
					}
					if dryRun {
						if op.Status != statusPlanned {
							t.Errorf("dry run status = %q, want planned", op.Status)
						}
						if got := storedSnapshot(t, list[0]); got != before {
							t.Errorf("a dry run changed the workflow:\n before %s\n after  %s", before, got)
						}
						return
					}
					if op.Status != statusSucceeded {
						t.Errorf("status = %q, want succeeded; op = %+v", op.Status, op)
					}
					if list[0].ID != seeded.ID || list[0].Name != spelling.request {
						t.Errorf("stored = %s %q, want the same row %s renamed to %q", list[0].ID, list[0].Name, seeded.ID, spelling.request)
					}
				})
			}
		}
	}
}

func TestBuildCreateRacingAWorkflowCreateConflictsInsteadOfOverwriting(t *testing.T) {
	t.Parallel()
	for _, taken := range []string{"Order Flow", "OrderFlow", "ORDER FLOW", "École Flow"} {
		t.Run(taken, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			requested := "Order Flow"
			if taken == "École Flow" {
				requested = "école flow"
			}

			plans, reg, err := svc.Plan(ctx, ApplyBuildRequest{Components: []BuildComponentInput{workflowComponent("order_wf", requested, "Shipping")}}, testActor)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if got := len(plans[0].plan.Creates); got != 1 {
				t.Fatalf("the plan has %d creates; the race below would be vacuous", got)
			}

			winner := seedStoredWorkflow(t, svc, taken, workflowComponent("order_wf", taken, "Fulfilment"))
			before := storedSnapshot(t, winner)

			resp := svc.ApplyPlanned(ctx, plans, reg, false, "racer", "")

			op := opFor(t, resp, kindWorkflow, "order_wf")
			if op.Status != statusFailed || op.Error == nil || op.Error.HTTPStatus != http.StatusConflict {
				t.Errorf("op = %+v, want a failed operation carrying a 409", op)
			}
			if resp.Summary.Created != 0 {
				t.Errorf("summary.created = %d, want 0", resp.Summary.Created)
			}
			list, listErr := svc.deps.listWorkflows(ctx)
			if listErr != nil || len(list) != 1 {
				t.Fatalf("workflows = %d, err = %v", len(list), listErr)
			}
			if got := storedSnapshot(t, list[0]); got != before {
				t.Errorf("the racing create changed the winner:\n before %s\n after  %s", before, got)
			}
		})
	}
}
