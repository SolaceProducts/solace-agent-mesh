// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func breakTheSessionRecord(t *testing.T, svc *Service) {
	t.Helper()
	if _, err := svc.deps.DB.ExecContext(t.Context(), `DROP TABLE builder_session_resources`); err != nil {
		t.Fatalf("drop the session record: %v", err)
	}
}

func TestRemoveDropped_ASessionRecordThatCannotBeReadFailsClosedAndSaysFailed(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, svc *Service){
		"no session record at all": func(_ *testing.T, svc *Service) { svc.deps.Ledger = nil },
		"a session record that errors": func(t *testing.T, svc *Service) {
			breakTheSessionRecord(t, svc)
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			buildInSession(t, svc, sessionA, false, agentComponent("record_agent"))
			agent := agentNamed(t.Context(), t, svc, "record_agent")
			arrange(t, svc)

			resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(agent.ID)))

			got := resultFor(t, resp, agent.ID)
			if got.Status != removalFailed || got.Error == nil || got.Error.ErrorID == "" || got.Error.HTTPStatus != http.StatusServiceUnavailable {
				t.Fatalf("result = %+v, want failed, retryable (503) and with an errorId", got)
			}
			if strings.Contains(got.Error.Message, "not created") {
				t.Errorf("message %q claims the session did not create it, which was never checked", got.Error.Message)
			}
			if names := storedAgentNames(t, svc); !slices.Equal(names, []string{"record_agent"}) {
				t.Fatalf("agents = %v, want the agent kept", names)
			}
		})
	}
}

func TestRemoveDropped_WithoutTheDeleteScopeIsStillRefusedWhenTheRecordCannotBeRead(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("scope_first"))
	agent := agentNamed(t.Context(), t, svc, "scope_first")
	svc.deps.Ledger = nil

	resp := removeAs(t, svc, testActor, removal(sessionA, agentTarget(agent.ID)))

	if got := resultFor(t, resp, agent.ID); got.Status != removalRefused {
		t.Fatalf("result = %+v, want refused for the missing scope", got)
	}
}

func TestApply_ASessionRecordThatCannotBeWrittenIsSaidAndTheResourceIsNeverOffered(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	breakTheSessionRecord(t, svc)

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{BuilderSessionID: sessionA, Components: []BuildComponentInput{agentComponent("unrecorded_agent")}}, remover())
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("apply: %v %+v", err, resp)
	}
	op := opFor(t, resp, kindAgent, "unrecorded_agent")
	if op.Status != statusSucceeded || !mentions(op.Warnings, "could not record that this Builder session wrote it") {
		t.Fatalf("op = %+v, want a succeeded create that says it could not be recorded", op)
	}
	if !resp.DroppedCheckFailed {
		t.Error("droppedCheckFailed not set while the session record cannot be read")
	}
}

func TestApply_ASessionRecordThatIsNotConfiguredIsSaid(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.deps.Ledger = nil

	preview := buildInSession(t, svc, sessionA, true, agentComponent("no_record_agent"))
	if !preview.DroppedCheckFailed {
		t.Error("droppedCheckFailed not set for a session with no record to read")
	}
	applied := buildInSession(t, svc, sessionA, false, agentComponent("no_record_agent"))
	if op := opFor(t, applied, kindAgent, "no_record_agent"); !mentions(op.Warnings, "could not record that this Builder session wrote it") {
		t.Errorf("warnings = %v, want the unrecorded write said", op.Warnings)
	}
}

func TestRecordWritten_SurvivesTheRequestEnding(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	resp := buildInSession(t, svc, "", false, agentComponent("outlives_the_request"))
	id := opFor(t, resp, kindAgent, "outlives_the_request").ResourceID

	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if !svc.recordWritten(gone, sessionA, testActor.Email(), resp.Operations) {
		t.Fatal("recordWritten failed after the request context ended")
	}
	rows, err := svc.deps.Ledger.ListForSession(t.Context(), testActor.Email(), sessionA)
	if err != nil || len(rows) != 1 || rows[0].ResourceID != id || !rows[0].CreatedBySession {
		t.Fatalf("session record = %+v, %v; want the created agent", rows, err)
	}
}

func TestApply_OnlyWhatWasWrittenIsRecorded(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedAgent(t, svc, "m_already_there")
	seedAgent(t, svc, "n_rejected")
	seedAgent(t, svc, "z_skipped_after")
	unchanged := agentComponent("m_already_there")

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{BuilderSessionID: sessionA, Components: []BuildComponentInput{
		agentComponent("a_written_first"), unchanged, agentWithUnknownToolset("n_rejected"), agentWithInstruction("z_skipped_after", "You are an agent changed after the failure."),
	}}, remover())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Summary.Created != 1 || resp.Summary.Failed != 1 || resp.Summary.Skipped != 1 || resp.Summary.Unchanged != 1 {
		t.Fatalf("summary = %+v, want one created, one unchanged, one failed (an update that names its resource), one skipped", resp.Summary)
	}
	if failed := opFor(t, resp, kindAgent, "n_rejected"); failed.Status != statusFailed || failed.ResourceID == "" {
		t.Fatalf("op = %+v, want a failed update that still names the resource it did not write", failed)
	}

	rows, err := svc.deps.Ledger.ListForSession(t.Context(), testActor.Email(), sessionA)
	if err != nil || len(rows) != 1 || rows[0].ResourceID != opFor(t, resp, kindAgent, "a_written_first").ResourceID {
		t.Fatalf("session record = %+v, %v; want only the agent that was written", rows, err)
	}
}

func TestHandleApply_AnEmptyDryRunIsAllowedOnlyForASession(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	caller := auth.NewTestSubject(auth.SubjectFields{UserID: "u-1", Email: "tester@example.com", Authenticated: true, Scopes: append([]string{rbac.ScopeAgentBuilderDelete, rbac.ScopeWorkflowBuilderDelete}, allBuildScopes...)})
	buildAs(t, svc, caller, sessionA, false, agentComponent("empty_plan_one"), agentComponent("empty_plan_two"))

	cases := []struct {
		name string
		body string
		want int
	}{
		{"a dry run for a session", `{"dryRun":true,"builderSessionId":"` + sessionA + `","components":[]}`, http.StatusOK},
		{"a dry run with no session", `{"dryRun":true,"components":[]}`, http.StatusUnprocessableEntity},
		{"an apply for a session", `{"builderSessionId":"` + sessionA + `","components":[]}`, http.StatusUnprocessableEntity},
		{"components omitted", `{"dryRun":true,"builderSessionId":"` + sessionA + `"}`, http.StatusUnprocessableEntity},
		{"components null", `{"dryRun":true,"builderSessionId":"` + sessionA + `","components":null}`, http.StatusUnprocessableEntity},
	}
	agentOnly := auth.NewTestSubject(auth.SubjectFields{UserID: "u-1", Email: "tester@example.com", Authenticated: true, Scopes: []string{rbac.ScopeAgentBuilderCreate, rbac.ScopeAgentBuilderRead}})
	if rec := postBuildAs(t, svc, `{"dryRun":true,"builderSessionId":"`+sessionA+`","components":[]}`, agentOnly); rec.Code != http.StatusOK {
		t.Fatalf("an agent-only caller: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postBuildAs(t, svc, tc.body, caller)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != http.StatusOK {
				if !strings.Contains(rec.Body.String(), "components") {
					t.Errorf("rejection does not name the components field: %s", rec.Body.String())
				}
				return
			}
			var resp ApplyBuildResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if names := droppedKindNames(&resp); !slices.Equal(names, []string{"agent:empty_plan_one", "agent:empty_plan_two"}) {
				t.Fatalf("dropped = %v, want both agents the session created", names)
			}
			if got := storedAgentNames(t, svc); len(got) != 2 {
				t.Errorf("agents = %v, a preview removes nothing", got)
			}
		})
	}
}

func postBuildAs(t *testing.T, svc *Service, body string, who *auth.Subject) *httptest.ResponseRecorder {
	t.Helper()
	h := &handler{svc: svc, logger: discardLogger()}
	req := httptest.NewRequest(http.MethodPost, BuildsPath, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(t.Context(), who))
	rec := httptest.NewRecorder()
	h.handleApply(rec, req)
	return rec
}

func TestRegisterRoutes_TheRemovalRouteIsGatedByTheDeleteScopes(t *testing.T) {
	t.Parallel()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: FeatureKey, ReleasePhase: features.PhaseExperimental, Default: true})
	mux := http.NewServeMux()
	RegisterRoutes(mux, &entplatform.RouteDeps{FeatureRegistry: reg}, nil, discardLogger())

	body := `{"builderSessionId":"` + sessionA + `","resources":[{"kind":"agent","id":"` + sessionB + `"}]}`
	cases := []struct {
		name   string
		scopes []string
		want   int
	}{
		{"no scope", nil, http.StatusForbidden},
		{"build scopes only", allBuildScopes, http.StatusForbidden},
		{"agent read only", []string{rbac.ScopeAgentBuilderRead}, http.StatusForbidden},
		{"the agent delete scope", []string{rbac.ScopeAgentBuilderDelete}, http.StatusOK},
		{"the workflow delete scope", []string{rbac.ScopeWorkflowBuilderDelete}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, RemovalsPath, strings.NewReader(body))
			req = req.WithContext(auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "u-1", Email: "t@example.com", Authenticated: true, Scopes: tc.scopes})))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestRemoveDropped_WithoutADeployServiceOnlyAResourceKnownToBeStoppedIsRemoved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		history [][2]string
		want    string
	}{
		{"never deployed", nil, removalRemoved},
		{"undeployed", [][2]string{{"deploy", "success"}, {"undeploy", "success"}}, removalRemoved},
		{"deployed", [][2]string{{"deploy", "success"}}, removalFailed},
		{"deploy failed", [][2]string{{"deploy", "failed"}}, removalFailed},
		{"undeploy failed", [][2]string{{"deploy", "success"}, {"undeploy", "failed"}}, removalFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			buildInSession(t, svc, sessionA, false, agentComponent("liveness_probe"))
			agent := agentNamed(t.Context(), t, svc, "liveness_probe")
			for _, step := range tc.history {
				seedAgentDeployment(t, svc, agent.ID, step[0], step[1])
			}

			resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(agent.ID)))

			if got := resultFor(t, resp, agent.ID); got.Status != tc.want {
				t.Fatalf("status = %q (%+v), want %q", got.Status, got.Error, tc.want)
			}
		})
	}
}

func TestRemoveDropped_AGoneResourceIsForgotten(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("gone_agent"), agentComponent("flow_target"), workflowComponent("gone_flow", "gone_flow", "flow_target"))
	agent := agentNamed(t.Context(), t, svc, "gone_agent")
	wf := workflowNamed(t, svc, "gone_flow")
	if err := svc.deps.Agents.Delete(t.Context(), agent.ID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if err := svc.deps.Workflows.Delete(t.Context(), wf.ID); err != nil {
		t.Fatalf("delete workflow: %v", err)
	}

	resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(agent.ID), workflowTarget(wf.ID)))

	for _, id := range []string{agent.ID, wf.ID} {
		if got := resultFor(t, resp, id); got.Status != removalNotFound {
			t.Errorf("%s: result = %+v, want not_found", id, got)
		}
	}
	rows, _ := svc.deps.Ledger.ListForSession(t.Context(), testActor.Email(), sessionA)
	for _, row := range rows {
		if row.ResourceID == agent.ID || row.ResourceID == wf.ID {
			t.Errorf("session record still holds a gone resource: %+v", row)
		}
	}
}

func TestRemoveDropped_AnUppercaseIDNamesTheSameResource(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("shouted_agent"))
	agent := agentNamed(t.Context(), t, svc, "shouted_agent")

	resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(strings.ToUpper(agent.ID))))

	if got := resultFor(t, resp, agent.ID); got.Status != removalRemoved {
		t.Fatalf("result = %+v, want removed", got)
	}
}
