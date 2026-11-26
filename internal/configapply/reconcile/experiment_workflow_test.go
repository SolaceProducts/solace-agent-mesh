// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func workflowExperimentFixture(workflow string) map[string]any {
	return experimentFixture(func(m map[string]any) {
		delete(m, "targetAgent")
		m["targetWorkflow"] = workflow
	})
}

func workflowExperimentResponse(workflow string) wire.ExperimentResponse {
	return experimentResponseFixture(func(r *wire.ExperimentResponse) {
		r.TargetType, r.TargetAgent, r.TargetWorkflow = "workflow", "", new(workflow)
	})
}

func planOneExperiment(t *testing.T, spec map[string]any, actual wire.ExperimentResponse) *experimentKindPlan {
	t.Helper()
	plan, err := planExperiments([]resolve.ResolvedResource{resolvedExperiment("nightly", spec)},
		[]wire.ExperimentResponse{actual}, experimentTestHooks())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

func assertPatchTargetsOnlyWorkflow(t *testing.T, plan *experimentKindPlan, workflow string) {
	t.Helper()
	if len(plan.Updates) != 1 {
		t.Fatalf("plan = %+v, want one update", plan)
	}
	patch := plan.Updates[0].Patch
	if patch.TargetAgent.Set {
		t.Errorf("patch TargetAgent = %+v; the platform refuses a request naming both targets", patch.TargetAgent)
	}
	wire := marshalExperimentPatch(t, patch)
	if wire["targetWorkflow"] != workflow {
		t.Errorf("patch targetWorkflow = %v, want %q", wire["targetWorkflow"], workflow)
	}
	if _, sent := wire["targetAgent"]; sent {
		t.Errorf("patch also sent targetAgent %v", wire["targetAgent"])
	}
}

func TestPlanExperiments_WorkflowTarget(t *testing.T) {
	t.Run("unchanged when the platform already targets the workflow", func(t *testing.T) {
		plan := planOneExperiment(t, workflowExperimentFixture("order_flow"), workflowExperimentResponse("order_flow"))
		if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
			t.Errorf("plan = %+v, want unchanged", plan)
		}
	})

	t.Run("a changed workflow name updates and sends only the workflow", func(t *testing.T) {
		plan := planOneExperiment(t, workflowExperimentFixture("order_flow_v2"), workflowExperimentResponse("order_flow"))
		assertPatchTargetsOnlyWorkflow(t, plan, "order_flow_v2")
	})

	t.Run("another field changing on a workflow experiment sends neither target", func(t *testing.T) {
		spec := workflowExperimentFixture("order_flow")
		spec["runsPerExample"] = 3
		plan := planOneExperiment(t, spec, workflowExperimentResponse("order_flow"))
		wire := marshalExperimentPatch(t, plan.Updates[0].Patch)
		if _, sent := wire["targetAgent"]; sent {
			t.Errorf("patch sent targetAgent %v", wire["targetAgent"])
		}
		if _, sent := wire["targetWorkflow"]; sent {
			t.Errorf("patch sent the unchanged targetWorkflow %v", wire["targetWorkflow"])
		}
	})

	t.Run("an agent experiment retargeted at a workflow updates and sends only the workflow", func(t *testing.T) {
		plan := planOneExperiment(t, workflowExperimentFixture("order_flow"), experimentResponseFixture(nil))
		assertPatchTargetsOnlyWorkflow(t, plan, "order_flow")
	})

	t.Run("a workflow experiment retargeted at an agent sends only the agent", func(t *testing.T) {
		plan := planOneExperiment(t, experimentFixture(nil), workflowExperimentResponse("order_flow"))
		wire := marshalExperimentPatch(t, plan.Updates[0].Patch)
		if wire["targetAgent"] != "Orchestrator" {
			t.Errorf("patch targetAgent = %v", wire["targetAgent"])
		}
		if _, sent := wire["targetWorkflow"]; sent {
			t.Errorf("an agent patch sent targetWorkflow %v", wire["targetWorkflow"])
		}
	})
}

func workflowListServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != platform.APIV2Prefix+workflowsResource.path {
			http.NotFound(w, r)
			return
		}
		rows := make([]wire.WorkflowResponse, 0, len(names))
		for i, n := range names {
			rows = append(rows, wire.WorkflowResponse{ID: "0f9a1b2c-3d4e-5f60-7182-93a4b5c6d7e" + string(rune('0'+i)), Name: n})
		}
		writeV2Page(w, rows)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestValidateExperimentWorkflowRefs(t *testing.T) {
	r := resolve.New(t.TempDir(), &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{}}, resolve.Options{})
	cases := []struct {
		name    string
		target  string
		known   []string
		wantErr string
	}{
		{"a platform workflow", "order_flow", []string{"order_flow"}, ""},
		{"a dangling name", "order_flow", []string{"other_flow"}, `workflow "order_flow" not found`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
				m["targetWorkflow"] = tc.target
			}))}

			err := validateExperimentWorkflowRefs(context.Background(), newClientAgainstServer(workflowListServer(t, tc.known...)), r, desired)

			if tc.wantErr == "" && err != nil {
				t.Fatalf("validate: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}

	t.Run("an agent-only manifest makes no workflow call", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("listed workflows for a manifest whose experiments name none")
		}))
		t.Cleanup(srv.Close)
		desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(nil))}
		if err := validateExperimentWorkflowRefs(context.Background(), newClientAgainstServer(srv), r, desired); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})
}

func TestValidateExperimentWorkflowRefsKnowsDeclaredAndBrokerNames(t *testing.T) {
	repo := t.TempDir()
	writeRepoFiles(t, repo, map[string]string{"workflows/new_flow.yaml": "kind: workflow\nname: new_flow\nspec: {}\n"})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"workflows": {{Name: "new_flow", From: "new_flow"}}}}
	r := resolve.New(repo, m, resolve.Options{})
	srv := workflowListServer(t, "order_flow")
	brokerName := a2a.InstanceName(a2a.KindWorkflow, "0f9a1b2c-3d4e-5f60-7182-93a4b5c6d7e0")

	for _, target := range []string{"new_flow", brokerName} {
		desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
			m["targetWorkflow"] = target
		}))}
		if err := validateExperimentWorkflowRefs(context.Background(), newClientAgainstServer(srv), r, desired); err != nil {
			t.Errorf("%s: %v", target, err)
		}
	}
}

func TestPlanFailsOnAnExperimentNamingNoWorkflow(t *testing.T) {
	c := newWorkflowEvalPlatform(t)
	repo := t.TempDir()
	writeRepoFiles(t, repo, map[string]string{
		"datasets/orders.yaml":         "kind: dataset\nname: orders\ndescription: Orders.\nspec:\n  examples_file: orders.examples.csv\n",
		"datasets/orders.examples.csv": "sequence_number,prompt,expected_response\n1,order O-1,\n",
		"experiments/order-regression.yaml": "kind: experiment\nname: order-regression\ndescription: Orders.\nspec:\n" +
			"  datasetId: orders\n  targetWorkflow: no_such_flow\n  evaluatorIds: [\"Valid JSON\"]\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"datasets":    {{Name: "orders", From: "orders"}},
		"experiments": {{Name: "order-regression", From: "order-regression"}},
	}}

	_, err := planAll(t.Context(), c, repo, m, resolve.New(repo, m, resolve.Options{}), BuildOptions{})

	if err == nil || !strings.Contains(err.Error(), `workflow "no_such_flow" not found`) {
		t.Errorf("plan err = %v, want the dangling targetWorkflow named", err)
	}
}

func TestChangedExperimentsLeavesUnchangedOnesOut(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedExperiment("created", experimentFixture(nil)),
		resolvedExperiment("updated", experimentFixture(nil)),
		resolvedExperiment("unchanged", experimentFixture(nil)),
	}
	plan := &experimentKindPlan{
		Creates:   []experimentOp{{Name: "created"}},
		Updates:   []experimentOp{{Name: "updated"}},
		Unchanged: []experimentOp{{Name: "unchanged"}},
	}

	var names []string
	for _, d := range changedExperiments(desired, plan) {
		names = append(names, d.Name)
	}

	if strings.Join(names, ",") != "created,updated" {
		t.Errorf("checked = %v, want only the created and updated experiments", names)
	}
	if len(desired) != 3 {
		t.Errorf("desired was changed in place: %d left", len(desired))
	}
}

func TestAnUnchangedExperimentWhoseWorkflowIsGoneDoesNotBlockThePlan(t *testing.T) {
	c := newWorkflowEvalPlatform(t)
	repo := t.TempDir()
	writeRepoFiles(t, repo, map[string]string{
		"rbac/roles/orders_invoke.yaml":     "kind: rbacRole\nname: orders_invoke\nspec:\n  scopes:\n    - \"workflow:*:invoke\"\n",
		"rbac/system-users/orders-bot.yaml": "kind: systemUser\nname: orders-bot\nspec:\n  roleNames:\n    - orders_invoke\n",
		"datasets/orders.yaml":              "kind: dataset\nname: orders\ndescription: Orders.\nspec:\n  examples_file: orders.examples.csv\n",
		"datasets/orders.examples.csv":      "sequence_number,prompt,expected_response\n1,order O-1,\n",
		"experiments/order-regression.yaml": "kind: experiment\nname: order-regression\ndescription: Orders.\nspec:\n" +
			"  datasetId: orders\n  targetWorkflow: order_flow\n  evaluatorIds: [\"Valid JSON\"]\n  runAs: orders-bot\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"rbacRoles":   {{Name: "orders_invoke", From: "orders_invoke"}},
		"systemUsers": {{Name: "orders-bot", From: "orders-bot"}},
		"datasets":    {{Name: "orders", From: "orders"}},
		"experiments": {{Name: "order-regression", From: "order-regression"}},
	}}
	plan, err := planAll(t.Context(), c, repo, m, resolve.New(repo, m, resolve.Options{}), BuildOptions{})
	if err != nil {
		t.Fatalf("first plan: %v", err)
	}
	applied := &ApplyResult{Plan: plan}
	if applyErr := applyPlan(t.Context(), c, plan, ApplyOptions{}, applied); applyErr != nil || anyFailures(applied) {
		t.Fatalf("apply: %v, experiments %+v", applyErr, applied.Experiments)
	}
	platformURL, err := url.Parse(c.resolved.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(platformURL)
	noWorkflows := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, platform.APIV2Prefix+workflowsResource.path) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "meta": map[string]any{"pagination": map[string]any{}}})
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(noWorkflows.Close)

	again, err := planAll(t.Context(), newClientAgainstServer(noWorkflows), repo, m, resolve.New(repo, m, resolve.Options{}), BuildOptions{})

	if err != nil {
		t.Fatalf("second plan: %v", err)
	}
	if again.Experiments == nil || len(again.Experiments.Unchanged) != 1 {
		t.Errorf("experiments = %+v, want the one experiment unchanged", again.Experiments)
	}
}
