// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestApply_RefusesAnUnusableDecisionModelBeforeWriting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, seedType, want string
	}{
		{"alias missing", "", "decision.model_alias \"judge\""},
		{"alias is a chat model", store.ModelTypeChat, "needs a model with modelType decision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			if tc.seedType != "" {
				seedModel(t, svc, "judge", tc.seedType)
			}
			before := platformSnapshot(t, svc)
			dry, applied := applyBoth(t, svc, agentComponent("good_agent"), workflowWithDecisionAndAlias("decision_flow", "good_agent"))
			requireSameFailure(t, opFor(t, dry, kindWorkflow, "decision_flow"), opFor(t, applied, kindWorkflow, "decision_flow"), tc.want)
			if after := platformSnapshot(t, svc); after != before {
				t.Fatal("a build refused for its decision model wrote to the platform")
			}
		})
	}
}

func TestApply_ABadDecisionAliasWritesNoneOfTheBuildsAgents(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	components := []BuildComponentInput{agentComponent("a_one"), agentComponent("a_two"), agentComponent("good_agent"), workflowWithDecisionAndAlias("decision_flow", "good_agent")}
	_, applied := applyBoth(t, svc, components...)
	if got := opFor(t, applied, kindWorkflow, "decision_flow").Status; got != statusFailed {
		t.Fatalf("workflow = %q, want failed", got)
	}
	for _, name := range []string{"a_one", "a_two", "good_agent"} {
		if got := opFor(t, applied, kindAgent, name).Status; got != statusSkipped {
			t.Errorf("agent %q = %q, want skipped", name, got)
		}
	}
	if agents, _ := svc.deps.listAgents(t.Context()); len(agents) != 0 {
		t.Errorf("agents written: %d, want 0", len(agents))
	}
}

func TestApply_DecisionNodesOffIsAWarning(t *testing.T) {
	t.Setenv("SAM_FEATURE_WORKFLOW_DECISION_NODE", "false")
	svc := newTestService(t)
	svc.features = features.NewRegistry()
	if err := features.LoadFlags(svc.features); err != nil {
		t.Fatalf("load feature catalog: %v", err)
	}
	seedModel(t, svc, "judge", store.ModelTypeDecision)
	dry, applied := applyBoth(t, svc, agentComponent("good_agent"), workflowWithDecisionAndAlias("decision_flow", "good_agent"))
	for _, resp := range []*ApplyBuildResponse{dry, applied} {
		op := opFor(t, resp, kindWorkflow, "decision_flow")
		if op.Status == statusFailed || !slices.ContainsFunc(op.Warnings, func(w string) bool { return strings.Contains(w, "workflow_decision_node") }) {
			t.Fatalf("dryRun=%v: workflow = %+v, want accepted with the decision-node warning", resp.DryRun, op)
		}
	}
}

func decisionWorkflowCreate(t *testing.T, svc *Service) (entplatform.CreateWorkflowRequest, BuildOperationResult) {
	t.Helper()
	plans, _, err := svc.Plan(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("good_agent"), workflowWithDecisionAndAlias("decision_flow", "good_agent")}}, testActor)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, kp := range plans {
		if kp.componentKind != kindWorkflow {
			continue
		}
		row := kp.operations(statusPlanned)[0]
		d, err := desiredFor(kp, row)
		if err != nil {
			t.Fatalf("desired workflow: %v", err)
		}
		return d.create.(entplatform.CreateWorkflowRequest), row
	}
	t.Fatal("no workflow in the plan")
	return entplatform.CreateWorkflowRequest{}, BuildOperationResult{}
}

func TestRejectUnusableDecisionModel_LogsAFailureClassNotTheError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		interrupt bool
		wantLevel string
		wantClass string
	}{
		{"an outage is a warning", false, `"level":"WARN"`, `"failureClass":"read_failed"`},
		{"a cancelled request is debug", true, `"level":"DEBUG"`, `"failureClass":"canceled"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			create, row := decisionWorkflowCreate(t, svc)
			var logs bytes.Buffer
			svc.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			ctx := t.Context()
			if tc.interrupt {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else if err := svc.deps.DB.Close(); err != nil {
				t.Fatalf("close the database to fail the read: %v", err)
			}
			if err := svc.rejectUnusableDecisionModel(ctx, create, row); err != nil {
				t.Fatalf("a failed read refused the workflow: %v", err)
			}
			logged := logs.String()
			if !strings.Contains(logged, tc.wantLevel) || !strings.Contains(logged, tc.wantClass) {
				t.Errorf("want %s with %s, got %s", tc.wantLevel, tc.wantClass, logged)
			}
			if strings.Contains(logged, `"error"`) || strings.Contains(logged, "validate decision model") {
				t.Errorf("the raw error was logged: %s", logged)
			}
		})
	}
}

func TestRejectInvalidAgainstStored_LogsAFailureClassNotTheError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		interrupt bool
		wantLevel string
		wantClass string
	}{
		{"an outage is a warning", false, `"level":"WARN"`, `"failureClass":"read_failed"`},
		{"a cancelled request is debug", true, `"level":"DEBUG"`, `"failureClass":"canceled"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			seedStoredShipmentConnector(t, svc, "carrier-api")
			edited := apiConnectorComponent("carrier-api", apiConnectorValues(map[string]any{"allow_list": "getShipment"}))
			plans, _, err := svc.Plan(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{edited}}, testActor)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			kp := plans[0]
			for _, candidate := range plans {
				if candidate.componentKind == kindConnector {
					kp = candidate
				}
			}
			row := kp.operations(statusPlanned)[0]
			d, err := desiredFor(kp, row)
			if err != nil || row.Action != actionUpdate {
				t.Fatalf("want a connector update in the plan: %+v %v", row, err)
			}
			var logs bytes.Buffer
			svc.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			ctx := t.Context()
			if tc.interrupt {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else if err := svc.deps.DB.Close(); err != nil {
				t.Fatalf("close the database to fail the read: %v", err)
			}
			if _, _, err := svc.rejectInvalidAgainstStored(ctx, d, row); err != nil {
				t.Fatalf("a failed read refused the component: %v", err)
			}
			logged := logs.String()
			if !strings.Contains(logged, tc.wantLevel) || !strings.Contains(logged, tc.wantClass) {
				t.Errorf("want %s with %s, got %s", tc.wantLevel, tc.wantClass, logged)
			}
			if strings.Contains(logged, `"error"`) {
				t.Errorf("the raw error was logged: %s", logged)
			}
		})
	}
}
