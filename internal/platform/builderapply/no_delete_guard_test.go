// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const removalFile = "remove.go"

var removingCalls = []string{"Delete", "DeleteWithUndeploy", "Undeploy"}

func TestNoDelete_OnlyTheRemovalFileDeletesOrUndeploys(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources found: %v", err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if filepath.Ext(file) != ".go" || len(file) > 8 && file[len(file)-8:] == "_test.go" || file == removalFile {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, file, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && slices.Contains(removingCalls, sel.Sel.Name) {
				t.Errorf("%s calls %s outside %s: only the user-confirmed removal may delete or undeploy", fset.Position(call.Pos()), sel.Sel.Name, removalFile)
			}
			return true
		})
	}
}

func TestNoDelete_TheBuildAndRemovalRoutesAreEachOneVerb(t *testing.T) {
	t.Parallel()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: FeatureKey, ReleasePhase: features.PhaseExperimental, Default: true})
	mux := http.NewServeMux()
	RegisterRoutes(mux, &entplatform.RouteDeps{FeatureRegistry: reg}, nil, discardLogger())

	for _, path := range []string{BuildsPath, RemovalsPath, InternalBuildsPath, InternalRemovalsPath} {
		for _, method := range []string{http.MethodDelete, http.MethodPut, http.MethodPatch, http.MethodGet} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405: the route serves POST only", method, path, rec.Code)
			}
		}
	}
	for _, path := range []string{BuildsPath + "/" + sessionA, RemovalsPath + "/" + sessionA, BuildsPath + ":remove", "/api/v1/platform/builder/builds/removals", InternalBuildsPath + "/" + sessionA, InternalBuildsPath + "/removals"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404: there is no other way to reach a removal", path, rec.Code)
		}
	}
}

func TestNoDelete_NoAPIKeyReachesTheBuildOrRemovalRoutes(t *testing.T) {
	t.Parallel()
	allowed := platpkg.APIKeyRoutePatterns()
	for _, pattern := range []string{"POST " + BuildsPath, "POST " + RemovalsPath, "POST " + InternalBuildsPath, "POST " + InternalRemovalsPath} {
		if slices.Contains(allowed, pattern) {
			t.Errorf("%q is on the API key allowlist", pattern)
		}
	}
}

func TestNoDelete_ASaveOfATruncatedManifestRemovesAndUndeploysNothing(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false,
		agentComponent("first_agent"), agentComponent("second_agent"), agentComponent("flow_target"),
		workflowComponent("order_flow", "order_flow", "flow_target"))
	for _, name := range []string{"first_agent", "second_agent", "flow_target"} {
		seedAgentDeployment(t, svc, agentNamed(t.Context(), t, svc, name).ID, "deploy", "success")
	}
	wf := workflowNamed(t, svc, "order_flow")
	seedWorkflowDeployment(t, svc, wf.ID, "deploy", "success")
	deploymentRows := func() int {
		agents := 0
		for _, name := range []string{"first_agent", "second_agent", "flow_target"} {
			agents += countAgentDeployments(t, svc, agentNamed(t.Context(), t, svc, name).ID)
		}
		return agents
	}
	before := deploymentRows()

	for _, dryRun := range []bool{false, true} {
		resp := buildInSession(t, svc, sessionA, dryRun, agentComponent("first_agent"))
		if len(resp.Dropped) != 3 {
			t.Fatalf("dryRun=%v: dropped = %v, want the three components the truncated manifest left out", dryRun, droppedKindNames(resp))
		}
	}

	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"first_agent", "flow_target", "second_agent"}) {
		t.Errorf("agents = %v, want all three still stored", got)
	}
	if list, _ := svc.deps.listWorkflows(t.Context()); len(list) != 1 {
		t.Errorf("workflows = %d, want the workflow still stored", len(list))
	}
	if after := deploymentRows(); after != before {
		t.Errorf("agent deployment rows %d -> %d: a save must not undeploy what the manifest left out", before, after)
	}
	if state, _ := svc.deploymentOf(t.Context(), kindWorkflow, wf.ID); state != deploymentDeployed {
		t.Errorf("workflow deployment = %q, want it still deployed", state)
	}
}

func TestNoDelete_ASaveOfATruncatedManifestKeepsWhatWasNeverDeployed(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("first_agent"), agentComponent("second_agent"), agentComponent("flow_target"), workflowComponent("order_flow", "order_flow", "flow_target"))

	for _, dryRun := range []bool{false, true} {
		buildInSession(t, svc, sessionA, dryRun, agentComponent("first_agent"))
	}

	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"first_agent", "flow_target", "second_agent"}) {
		t.Errorf("agents = %v, want all three still stored", got)
	}
	if list, _ := svc.deps.listWorkflows(t.Context()); len(list) != 1 {
		t.Errorf("workflows = %d, want the workflow still stored", len(list))
	}
}
