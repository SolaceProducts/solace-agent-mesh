// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type requestRecorder struct {
	failGetPath string

	mu    sync.Mutex
	calls []string
}

func (r *requestRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if !isV2ProbeOrPreflight(req) {
		r.mu.Lock()
		r.calls = append(r.calls, req.Method+" "+req.URL.Path)
		r.mu.Unlock()
	}
	if req.Method == http.MethodGet && req.URL.Path == r.failGetPath {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
		return
	}
	if req.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(req.URL.Path, "/dependents") {
		_, _ = w.Write([]byte(`{"data":[],"meta":{}}`))
		return
	}
	if strings.HasPrefix(req.URL.Path, platform.APIV2Prefix+"/") {
		_, _ = w.Write([]byte(`{"data":{"id":"wf-1"}}`))
		return
	}
	_, _ = w.Write([]byte(`{"id":"wf-1"}`))
}

func (r *requestRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

func newRecordingClient(t *testing.T, rec *requestRecorder) *Client {
	t.Helper()
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL})
}

func renameModelPlan() *PlanResult {
	return &PlanResult{
		Models: &kindPlan{
			Kind:    "model",
			Creates: []modelOp{{Name: "jev-new"}},
			Deletes: []modelOp{{Name: "jev-old", ID: "m-old"}},
		},
		Workflows: &workflowKindPlan{
			Kind:    "workflow",
			Updates: []workflowOp{{Name: "triage", ID: "wf-1"}},
		},
	}
}

func TestApplyPlan_ModelDeleteOrder(t *testing.T) {
	cases := []struct {
		name string
		opts ApplyOptions
		want []string
	}{
		{
			name: "deploys on",
			opts: ApplyOptions{Prune: true},
			want: []string{
				"POST " + platform.APIV2Prefix + modelsResource.path,
				"PUT " + platform.APIV2Prefix + workflowsResource.path + "/wf-1",
				"POST " + platform.APIV2Prefix + workflowDeploymentsResource.path,
				"GET " + platform.APIV2Prefix + modelsResource.path + "/m-old/dependents",
				"DELETE " + platform.APIV2Prefix + modelsResource.path + "/m-old",
			},
		},
		{
			name: "no deploy",
			opts: ApplyOptions{Prune: true, NoDeploy: true},
			want: []string{
				"POST " + platform.APIV2Prefix + modelsResource.path,
				"PUT " + platform.APIV2Prefix + workflowsResource.path + "/wf-1",
				"GET " + platform.APIV2Prefix + modelsResource.path + "/m-old/dependents",
				"DELETE " + platform.APIV2Prefix + modelsResource.path + "/m-old",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &requestRecorder{}
			c := newRecordingClient(t, rec)
			plan := renameModelPlan()
			out := &ApplyResult{Plan: plan}
			if err := applyPlan(t.Context(), c, plan, tc.opts, out); err != nil {
				t.Fatalf("applyPlan: %v", err)
			}
			if got := rec.recorded(); !slices.Equal(got, tc.want) {
				t.Errorf("platform calls = %v, want %v", got, tc.want)
			}
			if len(out.Models.Created) != 1 || len(out.Models.Deleted) != 1 || len(out.Models.Failures) != 0 {
				t.Errorf("model outcome = %+v", out.Models)
			}
		})
	}
}

func TestApplyPlan_ModelDeleteRefusedWhileAWorkflowNamesIt(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == platform.APIV2Prefix+modelsResource.path+"/m-old/dependents":
			writeV2Page(w, []apiv2.ModelDependent{
				{Type: "modelDependent", Key: "a-1", Name: "helper", DependentType: "agent", DeploymentStatus: "deployed"},
				{Type: "modelDependent", Key: "wf-1", Name: "triage", DependentType: "workflow", DeploymentStatus: "deployed"},
			})
		case req.Method == http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			writeV2Error(w, http.StatusNotFound, "unexpected", nil)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient(target.Resolved{URL: srv.URL})
	plan := &PlanResult{Models: &kindPlan{
		Kind:    "model",
		Deletes: []modelOp{{Name: "jev-old", ID: "m-old"}},
	}}
	out := &ApplyResult{Plan: plan}

	if err := applyPlan(t.Context(), c, plan, ApplyOptions{Prune: true, NoDeploy: true}, out); err != nil {
		t.Fatalf("applyPlan: %v", err)
	}
	if deleted {
		t.Error("the model was deleted while a workflow still names it")
	}
	if len(out.Models.Failures) != 1 || !strings.Contains(out.Models.Failures[0].Err.Error(), `workflow(s) "triage"`) {
		t.Fatalf("model outcome = %+v, want one failure naming the workflow", out.Models)
	}
	if strings.Contains(out.Models.Failures[0].Err.Error(), "helper") {
		t.Errorf("failure %q names the agent, which the platform undeploys as before", out.Models.Failures[0].Err)
	}
}

func TestApplyPlan_ModelDeletesSkippedWithoutPrune(t *testing.T) {
	rec := &requestRecorder{}
	c := newRecordingClient(t, rec)
	plan := &PlanResult{Models: &kindPlan{
		Kind:    "model",
		Deletes: []modelOp{{Name: "jev-old", ID: "m-old"}},
	}}
	out := &ApplyResult{Plan: plan}
	if err := applyPlan(t.Context(), c, plan, ApplyOptions{}, out); err != nil {
		t.Fatalf("applyPlan: %v", err)
	}
	if got := rec.recorded(); len(got) != 0 {
		t.Errorf("platform calls = %v, want none", got)
	}
	if len(out.Models.Skipped) != 1 || out.Models.Skipped[0].Err != nil || len(out.Models.Deleted) != 0 {
		t.Errorf("model outcome = %+v", out.Models)
	}
}

func abortingSyncPlan() *PlanResult {
	return &PlanResult{
		Models: &kindPlan{
			Kind:    "model",
			Deletes: []modelOp{{Name: "jev-old", ID: "m-old"}},
		},
		Agents: &agentKindPlan{
			Kind:    "agent",
			Creates: []agentOp{{Name: "helper"}},
		},
	}
}

func TestApplyPlan_AbortWithoutPruneKeepsThePruneSkip(t *testing.T) {
	c := newRecordingClient(t, &requestRecorder{failGetPath: platform.APIV2Prefix + skillsResource.path})
	plan := abortingSyncPlan()
	out := &ApplyResult{Plan: plan}
	if err := applyPlan(t.Context(), c, plan, ApplyOptions{}, out); err == nil {
		t.Fatal("applyPlan: want the skill placeholder failure")
	}
	if len(out.Models.Skipped) != 1 || out.Models.Skipped[0].Err != nil {
		t.Errorf("model outcome = %+v, want one plain prune skip", out.Models)
	}
}

func TestApplyPlan_AbortRecordsModelDeletesAsSkipped(t *testing.T) {
	for _, noDeploy := range []bool{false, true} {
		rec := &requestRecorder{failGetPath: platform.APIV2Prefix + skillsResource.path}
		c := newRecordingClient(t, rec)
		plan := abortingSyncPlan()
		out := &ApplyResult{Plan: plan}
		err := applyPlan(t.Context(), c, plan, ApplyOptions{Prune: true, NoDeploy: noDeploy}, out)
		if err == nil || !strings.Contains(err.Error(), "resolve skill placeholders") {
			t.Fatalf("noDeploy=%v: applyPlan error = %v, want the skill placeholder failure", noDeploy, err)
		}
		if slices.Contains(rec.recorded(), "DELETE "+platform.APIV2Prefix+modelsResource.path+"/m-old") {
			t.Errorf("noDeploy=%v: the model was deleted after the abort", noDeploy)
		}
		if len(out.Models.Skipped) != 1 || !errors.Is(out.Models.Skipped[0].Err, errModelDeletesNotReached) {
			t.Fatalf("noDeploy=%v: model outcome = %+v, want the delete recorded as not attempted", noDeploy, out.Models)
		}
		var buf bytes.Buffer
		renderApplyKind(&buf, out.Models, "models", FormatOptions{NoColor: true})
		if !strings.Contains(buf.String(), "delete skipped: "+errModelDeletesNotReached.Error()) {
			t.Errorf("noDeploy=%v: rendered outcome = %q, want the not-attempted reason", noDeploy, buf.String())
		}
	}
}

func isV2ProbeOrPreflight(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	return r.URL.Path == versionEndpoint || r.URL.Path == APIV2ProbePath && "?"+r.URL.RawQuery == APIV2ProbeQuery
}
