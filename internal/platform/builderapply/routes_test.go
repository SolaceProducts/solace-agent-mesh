// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var allBuildScopes = append([]string{
	rbac.ScopeConnectorCreate, rbac.ScopeConnectorUpdate,
	rbac.ScopeAgentBuilderCreate, rbac.ScopeAgentBuilderUpdate,
	rbac.ScopeWorkflowBuilderCreate, rbac.ScopeWorkflowBuilderUpdate,
	rbac.ScopeEntrypointCreate, rbac.ScopeEntrypointUpdate,
}, buildReadScopes...)

var buildReadScopes = []string{
	rbac.ScopeConnectorRead, rbac.ScopeSkillRead,
	rbac.ScopeAgentBuilderRead, rbac.ScopeWorkflowBuilderRead, rbac.ScopeEntrypointRead,
}

func withReads(scopes ...string) []string {
	return append(scopes, buildReadScopes...)
}

func postBuild(t *testing.T, svc *Service, body string, scopes []string) *httptest.ResponseRecorder {
	t.Helper()
	h := &handler{svc: svc, logger: discardLogger()}

	req := httptest.NewRequest(http.MethodPost, BuildsPath, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "u-1", Email: "tester@example.com", Authenticated: true, Scopes: scopes,
	})))

	rec := httptest.NewRecorder()
	h.handleApply(rec, req)
	return rec
}

func TestRegisterRoutes_RespectsFeatureFlag(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enabled    bool
		wantStatus int
	}{
		{"enabled", true, http.StatusOK},
		{"disabled", false, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SAM_FEATURE_"+strings.ToUpper(FeatureKey), "")
			reg := features.NewRegistry()
			reg.Register(features.Definition{
				Key:          FeatureKey,
				ReleasePhase: features.PhaseExperimental,
				Default:      tc.enabled,
			})

			mux := http.NewServeMux()
			RegisterRoutes(mux, &entplatform.RouteDeps{FeatureRegistry: reg}, nil, discardLogger())

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, BuildsPath, nil))
			if tc.enabled && rec.Code == http.StatusNotFound {
				t.Errorf("flag on: route not mounted (status %d)", rec.Code)
			}
			if !tc.enabled && rec.Code != http.StatusNotFound {
				t.Errorf("flag off: route is reachable (status %d)", rec.Code)
			}
		})
	}
}

func TestHandleApply_AppliesBuild(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := postBuild(t, svc, `{"components":[
		{"kind":"connector","name":"test-db","content":"{\"name\":\"test-db\",\"description\":\"A connector used in tests.\",\"type\":\"sql\",\"subtype\":\"postgres\",\"values\":{\"hostname\":\"db\"}}"}
	]}`, allBuildScopes)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp ApplyBuildResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.Created != 1 {
		t.Errorf("Created = %d, want 1 (%+v)", resp.Summary.Created, resp.Operations)
	}
}

func TestHandleApply_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := postBuild(t, svc, `{"components":[{"kind":"connector","name":"c","content":"{}"}],"tpyo":true}`, allBuildScopes)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tpyo") {
		t.Errorf("response should name the offending field: %s", rec.Body.String())
	}
}

func TestHandleApply_RejectsEmptyComponents(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := postBuild(t, svc, `{"components":[]}`, allBuildScopes)
	if rec.Code == http.StatusOK {
		t.Fatalf("an empty build should not succeed; body = %s", rec.Body.String())
	}
}

func TestHandleApply_RejectsUnpersistedKind(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := postBuild(t, svc, `{"components":[{"kind":"skill","name":"s","content":"name: s"}]}`, allBuildScopes)
	if rec.Code == http.StatusOK {
		t.Fatalf("skill components must be rejected; body = %s", rec.Body.String())
	}
}

func TestHandleApply_ForbidsMissingPerKindScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := postBuild(t, svc, `{"components":[
		{"kind":"connector","name":"test-db","content":"{\"name\":\"test-db\",\"description\":\"A connector used in tests.\",\"type\":\"sql\",\"subtype\":\"postgres\",\"values\":{\"hostname\":\"db\"}}"}
	]}`, withReads(rbac.ScopeAgentBuilderCreate))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), rbac.ScopeConnectorCreate) {
		t.Errorf("403 should name the missing scope: %s", rec.Body.String())
	}

	list, err := svc.deps.listConnectors(t.Context())
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	if len(list) != 0 {
		t.Error("a forbidden build wrote to the platform anyway")
	}
}

func TestHandleApply_DryRunIsAuthorizedLikeAnApply(t *testing.T) {
	t.Parallel()
	const body = `{"dryRun":true,"components":[
		{"kind":"connector","name":"test-db","content":"{\"name\":\"test-db\",\"description\":\"A connector used in tests.\",\"type\":\"sql\",\"subtype\":\"postgres\",\"values\":{\"hostname\":\"db\"}}"}
	]}`

	t.Run("with the scope the plan needs", func(t *testing.T) {
		svc := newTestService(t)
		rec := postBuild(t, svc, body, withReads(rbac.ScopeConnectorCreate))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var resp ApplyBuildResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.DryRun || len(resp.Operations) != 1 {
			t.Errorf("want a one-operation preview, got %+v", resp)
		}
		if resp.Operations[0].Status != statusPlanned {
			t.Errorf("status = %q, want planned", resp.Operations[0].Status)
		}
	})

	t.Run("without it", func(t *testing.T) {
		svc := newTestService(t)
		rec := postBuild(t, svc, body, withReads(rbac.ScopeAgentBuilderCreate))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 — a preview discloses existence and ids", rec.Code)
		}
	})
}

func TestApply_SucceededMeansSomethingWasWritten(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("written"), agentComponent("written_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, op := range resp.Operations {
		if op.Status != statusSucceeded {
			continue
		}
		if op.Action == actionUnchanged {
			continue
		}
		if op.ResourceID == "" {
			t.Errorf("%s %q reports succeeded with no resource id", op.Kind, op.Name)
		}
	}

	connectors, err := svc.deps.listConnectors(ctx)
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	if got := len(connectors) + len(agents); got != resp.Summary.Created {
		t.Errorf("summary claims %d created but the store holds %d", resp.Summary.Created, got)
	}
}

func TestHandleApply_DryRunReportsARejectedComponentOnTheWire(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := postBuild(t, svc, `{"dryRun":true,"components":[
		{"kind":"connector","name":"ab","content":"{\"name\":\"ab\",\"description\":\"short\",\"type\":\"sql\",\"subtype\":\"postgres\",\"values\":{\"hostname\":\"db\"}}"}
	]}`, allBuildScopes)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		DryRun     bool `json:"dryRun"`
		Operations []struct {
			Status string `json:"status"`
			Error  struct {
				Message           string              `json:"message"`
				ErrorID           string              `json:"errorId"`
				HTTPStatus        int                 `json:"httpStatus"`
				ValidationDetails map[string][]string `json:"validationDetails"`
			} `json:"error"`
		} `json:"operations"`
		Summary struct {
			Failed int `json:"failed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.DryRun || len(resp.Operations) != 1 || resp.Summary.Failed != 1 {
		t.Fatalf("response = %s, want a one-row preview with one failure", rec.Body.String())
	}
	op := resp.Operations[0]
	if op.Status != statusFailed || op.Error.ErrorID == "" || op.Error.HTTPStatus != http.StatusUnprocessableEntity {
		t.Errorf("row = %+v, want a failed row with an errorId and a 422", op)
	}
	if !strings.Contains(op.Error.Message, "name must be at least 3") || !strings.Contains(op.Error.Message, "description must be at least 10") {
		t.Errorf("message = %q, want every field error named", op.Error.Message)
	}
	for _, field := range []string{"name", "description"} {
		if len(op.Error.ValidationDetails[field]) == 0 {
			t.Errorf("validationDetails misses %q: %v", field, op.Error.ValidationDetails)
		}
	}

	list, err := svc.deps.listConnectors(t.Context())
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	if len(list) != 0 {
		t.Error("a dry run wrote to the platform")
	}
}

func TestHandleApply_PartialFailureIsStill200(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	rec := postBuild(t, svc, `{"components":[
		{"kind":"connector","name":"broken","content":"{\"name\":\"broken\",\"description\":\"x\",\"type\":\"sql\",\"subtype\":\"postgres\",\"values\":{}}"}
	]}`, allBuildScopes)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a per-operation failure; body = %s", rec.Code, rec.Body.String())
	}
	var resp ApplyBuildResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.Failed != 1 {
		t.Fatalf("Failed = %d, want 1 (%+v)", resp.Summary.Failed, resp.Operations)
	}
	if resp.Operations[0].Error == nil || resp.Operations[0].Error.ErrorID == "" {
		t.Error("the failed operation must carry an errorId")
	}
}
