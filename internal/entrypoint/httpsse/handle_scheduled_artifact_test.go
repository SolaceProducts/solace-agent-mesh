// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

const (
	schedArtTestNamespace = "test-ns"
	schedArtTestOwner     = "sam_dev_user"
	schedArtTestFilename  = "report.txt"
	schedArtTestPayload   = "scheduled-artifact-bytes"
	schedArtTestMIMEType  = "text/plain"
)

type schedArtFixture struct {
	taskID    string
	execID    string
	sessionID string
	owner     string
	namespace string
}

func setupSchedArtHandler(t *testing.T) (*scheduledTaskHandler, *http.ServeMux, *artifact.ScopedService) {
	t.Helper()
	h, _ := setupScheduledTaskTest(t)

	artSvc := &artifact.ScopedService{
		Inner:     memory.New(),
		Scope:     "namespace",
		Namespace: schedArtTestNamespace,
	}
	h.artSvc = artSvc

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/scheduled/{id}/{filename}", h.handleGetScheduledArtifact)
	return h, mux, artSvc
}

func seedScheduledArtifact(t *testing.T, h *scheduledTaskHandler, artSvc *artifact.ScopedService,
	ownerUserID, namespace, filename string, content []byte,
) schedArtFixture {
	t.Helper()
	ctx := context.Background()

	taskID := idgen.New()
	execID := idgen.New()

	owner := ownerUserID
	task := &store.ScheduledTask{
		ID: taskID, Name: "art task", Namespace: namespace,
		UserID: &owner, CreatedBy: owner,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hi"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(ctx, task); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	exec := &store.ScheduledTaskExecution{
		ID: execID, ScheduledTaskID: taskID,
		Status: "completed", ScheduledFor: 1,
	}
	if err := h.repo.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	sessionID, err := h.sessKeyRepo.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeScheduled,
		ExternalKey: execID,
		UserID:      ownerUserID,
	})
	if err != nil {
		t.Fatalf("seed session mapping: %v", err)
	}

	if content != nil {
		loc := artSvc.Resolve("", ownerUserID, sessionID, filename)
		if _, err := artSvc.Inner.Save(ctx, loc, content, schedArtTestMIMEType); err != nil {
			t.Fatalf("seed artifact bytes: %v", err)
		}
	}

	return schedArtFixture{
		taskID: taskID, execID: execID, sessionID: sessionID,
		owner: ownerUserID, namespace: namespace,
	}
}

func requestAs(method, path, requester string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	if requester != "" {
		req = req.WithContext(auth.ContextWithSubject(req.Context(),
			auth.NewTestSubject(auth.SubjectFields{UserID: requester})))
	}
	return req
}

func TestScheduledArtifact_OwnerInlineDownload(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc,
		schedArtTestOwner, schedArtTestNamespace,
		schedArtTestFilename, []byte(schedArtTestPayload))

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+fx.sessionID+"/"+schedArtTestFilename,
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != schedArtTestPayload {
		t.Errorf("body = %q, want %q", body, schedArtTestPayload)
	}
	if got := rec.Header().Get("Content-Type"); got != schedArtTestMIMEType {
		t.Errorf("Content-Type = %q, want %q", got, schedArtTestMIMEType)
	}
	disp := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disp, "inline;") {
		t.Errorf("Content-Disposition = %q, want inline by default", disp)
	}
	if !strings.Contains(disp, "filename*=UTF-8''"+url.PathEscape(schedArtTestFilename)) {
		t.Errorf("Content-Disposition = %q, want UTF-8 filename param", disp)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("Content-Security-Policy = %q, want none on an inline-safe type", csp)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", nosniff)
	}
}

func TestScheduledArtifact_DownloadQueryFlipsToAttachment(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc,
		schedArtTestOwner, schedArtTestNamespace,
		schedArtTestFilename, []byte(schedArtTestPayload))

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+fx.sessionID+"/"+schedArtTestFilename+"?download=true",
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment;") {
		t.Errorf("Content-Disposition = %q, want attachment with download=true", disp)
	}
}

func TestScheduledArtifact_HTMLIsNeverInline(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc,
		schedArtTestOwner, schedArtTestNamespace, "report.html", nil)
	loc := artSvc.Resolve("", schedArtTestOwner, fx.sessionID, "report.html")
	if _, err := artSvc.Inner.Save(context.Background(), loc, []byte("<script>alert(1)</script>"), "text/html"); err != nil {
		t.Fatalf("seed artifact bytes: %v", err)
	}

	req := requestAs("GET", "/api/v1/artifacts/scheduled/"+fx.sessionID+"/report.html", schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment;") {
		t.Errorf("Content-Disposition = %q, want attachment for text/html", disp)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "sandbox" {
		t.Errorf("Content-Security-Policy = %q, want sandbox", csp)
	}
}

func TestScheduledArtifact_WrongUserGets404(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc,
		"alice", schedArtTestNamespace,
		schedArtTestFilename, []byte(schedArtTestPayload))

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+fx.sessionID+"/"+schedArtTestFilename,
		"bob")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not 403); body=%s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), schedArtTestPayload) {
		t.Errorf("body leaked artifact bytes across users: %q", body)
	}
}

func TestScheduledArtifact_WrongNamespaceGets404(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc,
		schedArtTestOwner, "other-ns",
		schedArtTestFilename, []byte(schedArtTestPayload))

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+fx.sessionID+"/"+schedArtTestFilename,
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 on namespace mismatch", rec.Code)
	}
}

func TestScheduledArtifact_PathTraversalRejected(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc,
		schedArtTestOwner, schedArtTestNamespace,
		schedArtTestFilename, []byte(schedArtTestPayload))

	cases := []struct {
		name   string
		path   string
		expect int
	}{
		{
			name:   "url-encoded ../",
			path:   "/api/v1/artifacts/scheduled/" + fx.sessionID + "/%2E%2E%2Fpasswd",
			expect: http.StatusBadRequest,
		},
		{
			name:   "url-encoded backslash",
			path:   "/api/v1/artifacts/scheduled/" + fx.sessionID + "/%2E%2E%5Cwin",
			expect: http.StatusBadRequest,
		},
		{
			name:   "url-encoded single dot",
			path:   "/api/v1/artifacts/scheduled/" + fx.sessionID + "/%2E",
			expect: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := requestAs("GET", tc.path, schedArtTestOwner)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.expect {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.expect, rec.Body.String())
			}
		})
	}
}

func TestScheduledArtifact_BadSessionUUID(t *testing.T) {
	_, mux, _ := setupSchedArtHandler(t)

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/not-a-uuid/"+schedArtTestFilename,
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for non-UUID session id", rec.Code)
	}
}

func TestScheduledArtifact_UnknownSessionGets404(t *testing.T) {
	_, mux, _ := setupSchedArtHandler(t)

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+idgen.New()+"/"+schedArtTestFilename,
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown session", rec.Code)
	}
}

func TestScheduledArtifact_MissingBytesGets404(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc,
		schedArtTestOwner, schedArtTestNamespace,
		schedArtTestFilename, nil)

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+fx.sessionID+"/"+schedArtTestFilename,
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for missing artifact bytes", rec.Code)
	}
}

func TestScheduledArtifact_InternalFilenameRejected(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	fx := seedScheduledArtifact(t, h, artSvc,
		schedArtTestOwner, schedArtTestNamespace,
		schedArtTestFilename, []byte(schedArtTestPayload))

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+fx.sessionID+"/report.txt.metadata.json",
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for internal-artifact filename", rec.Code)
	}
}

func TestScheduledArtifact_CreatorButNotOwnerAllowed(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	ctx := context.Background()

	taskID := idgen.New()
	execID := idgen.New()

	task := &store.ScheduledTask{
		ID: taskID, Name: "shared task", Namespace: schedArtTestNamespace,
		UserID: nil, CreatedBy: schedArtTestOwner,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hi"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(ctx, task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	exec := &store.ScheduledTaskExecution{
		ID: execID, ScheduledTaskID: taskID, Status: "completed", ScheduledFor: 1,
	}
	if err := h.repo.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	sessionID, err := h.sessKeyRepo.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeScheduled,
		ExternalKey: execID,
		UserID:      schedArtTestOwner,
	})
	if err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	loc := artSvc.Resolve("", schedArtTestOwner, sessionID, schedArtTestFilename)
	if _, err := artSvc.Inner.Save(ctx, loc, []byte(schedArtTestPayload), schedArtTestMIMEType); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+sessionID+"/"+schedArtTestFilename,
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for creator; body=%s", rec.Code, rec.Body.String())
	}
}

func TestScheduledArtifact_OwnerWithoutCreatorMatchGets404(t *testing.T) {
	h, mux, artSvc := setupSchedArtHandler(t)
	ctx := context.Background()

	taskID := idgen.New()
	execID := idgen.New()
	owner := "alice"
	creator := "carol"

	task := &store.ScheduledTask{
		ID: taskID, Name: "shared", Namespace: schedArtTestNamespace,
		UserID: &owner, CreatedBy: creator,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hi"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(ctx, task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := h.repo.CreateExecution(ctx, &store.ScheduledTaskExecution{
		ID: execID, ScheduledTaskID: taskID, Status: "completed", ScheduledFor: 1,
	}); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	sessionID, err := h.sessKeyRepo.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeScheduled, ExternalKey: execID, UserID: owner,
	})
	if err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	loc := artSvc.Resolve("", owner, sessionID, schedArtTestFilename)
	if _, err := artSvc.Inner.Save(ctx, loc, []byte(schedArtTestPayload), schedArtTestMIMEType); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+sessionID+"/"+schedArtTestFilename,
		owner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for owner-but-not-creator; body=%s", rec.Code, rec.Body.String())
	}
}

func TestScheduledArtifact_PreMuxDispatch_RoutesPublicPath(t *testing.T) {
	a := &HTTPSSEAdapter{}
	gotID, gotFilename := "", ""
	a.scheduledArtifactHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.PathValue("id")
		gotFilename = r.PathValue("filename")
		w.WriteHeader(http.StatusOK)
	})

	muxCalled := false
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		muxCalled = true
		w.WriteHeader(http.StatusNotFound)
	})

	cases := []struct {
		name         string
		method       string
		path         string
		wantID       string
		wantFilename string
		wantMux      bool
	}{
		{
			name:         "happy path",
			method:       "GET",
			path:         "/api/v1/artifacts/scheduled/019e4bca-0000-7000-8000-000000000001/report.txt",
			wantID:       "019e4bca-0000-7000-8000-000000000001",
			wantFilename: "report.txt",
		},
		{
			name:         "filename literally named versions",
			method:       "GET",
			path:         "/api/v1/artifacts/scheduled/019e4bca-0000-7000-8000-000000000002/versions",
			wantID:       "019e4bca-0000-7000-8000-000000000002",
			wantFilename: "versions",
		},
		{
			name:    "non-GET falls through",
			method:  "POST",
			path:    "/api/v1/artifacts/scheduled/019e4bca-0000-7000-8000-000000000003/x",
			wantMux: true,
		},
		{
			name:    "missing filename falls through",
			method:  "GET",
			path:    "/api/v1/artifacts/scheduled/019e4bca-0000-7000-8000-000000000004",
			wantMux: true,
		},
		{
			name:    "extra slash falls through",
			method:  "GET",
			path:    "/api/v1/artifacts/scheduled/019e4bca/extra/segments",
			wantMux: true,
		},
		{
			name:    "different prefix falls through",
			method:  "GET",
			path:    "/api/v1/artifacts/019e4bca/file.txt",
			wantMux: true,
		},
	}

	dispatcher := a.preMuxDispatch(mux)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotFilename, muxCalled = "", "", false
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			dispatcher.ServeHTTP(rec, req)

			if tc.wantMux {
				if !muxCalled {
					t.Fatalf("expected fall-through to mux for %q, but scheduled handler was invoked", tc.path)
				}
				return
			}
			if muxCalled {
				t.Fatalf("expected scheduled handler for %q, but mux was invoked", tc.path)
			}
			if gotID != tc.wantID {
				t.Errorf("id = %q, want %q", gotID, tc.wantID)
			}
			if gotFilename != tc.wantFilename {
				t.Errorf("filename = %q, want %q", gotFilename, tc.wantFilename)
			}
		})
	}
}

func TestPreMuxDispatch_EmitsMetricsWithCanonicalTemplate(t *testing.T) {
	cases := []struct {
		name         string
		method       string
		path         string
		wantTemplate string
	}{
		{
			name:         "scheduled artifact",
			method:       "GET",
			path:         "/api/v1/artifacts/scheduled/019e4bca-0000-7000-8000-000000000001/report.txt",
			wantTemplate: "/api/v1/artifacts/scheduled/{id}/{filename}",
		},
		{
			name:         "scheduled execution GET",
			method:       "GET",
			path:         "/api/v1/scheduledTasks/executions/019e4bca-0000-7000-8000-000000000002",
			wantTemplate: "/api/v1/scheduledTasks/executions/{id}",
		},
		{
			name:         "scheduled execution DELETE",
			method:       "DELETE",
			path:         "/api/v1/scheduledTasks/executions/019e4bca-0000-7000-8000-000000000003",
			wantTemplate: "/api/v1/scheduledTasks/executions/{id}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := observability.NewRecordingMetrics()

			noop := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			a := &HTTPSSEAdapter{
				scheduledArtifactHandler:        noop,
				scheduledExecutionGetHandler:    noop,
				scheduledExecutionDeleteHandler: noop,
				metricsWrap:                     observability.MetricsMiddleware(rec, "test-gw", slog.Default()),
			}

			mux := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Fatal("mux fallback hit — preMuxDispatch did not intercept the request")
			})

			req := httptest.NewRequest(tc.method, tc.path, nil)
			a.preMuxDispatch(mux).ServeHTTP(httptest.NewRecorder(), req)

			if !rec.HasCounter("entrypoint.requests",
				"route.template", tc.wantTemplate,
				"http.method", tc.method,
				"entrypoint.name", "test-gw",
			) {
				t.Errorf("counter missing canonical route.template=%q; recorded: %+v",
					tc.wantTemplate, rec.Counters("entrypoint.requests"))
			}

			if got := rec.HistogramCount("entrypoint.duration",
				"entrypoint.name", "test-gw",
			); got != 1 {
				t.Errorf("expected 1 entrypoint.duration observation, got %d", got)
			}
		})
	}
}

func TestPreMuxDispatch_NoMetricsWrapSkipsEmission(t *testing.T) {
	called := false
	a := &HTTPSSEAdapter{
		scheduledArtifactHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}),
	}

	mux := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("mux fallback hit")
	})

	req := httptest.NewRequest("GET",
		"/api/v1/artifacts/scheduled/019e4bca-0000-7000-8000-000000000004/x.txt", nil)
	a.preMuxDispatch(mux).ServeHTTP(httptest.NewRecorder(), req)

	if !called {
		t.Fatal("scheduledArtifactHandler not invoked")
	}
}

func TestScheduledArtifact_PreMuxDispatch_NilHandlerFallsThrough(t *testing.T) {
	a := &HTTPSSEAdapter{}
	muxCalled := false
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		muxCalled = true
	})

	req := httptest.NewRequest("GET",
		"/api/v1/artifacts/scheduled/019e4bca-0000-7000-8000-000000000005/x.txt", nil)
	a.preMuxDispatch(mux).ServeHTTP(httptest.NewRecorder(), req)

	if !muxCalled {
		t.Fatal("expected fall-through to mux when scheduledArtifactHandler is nil")
	}
}

func TestScheduledArtifact_NoArtSvcGets404(t *testing.T) {
	h, _ := setupScheduledTaskTest(t)
	h.artSvc = nil

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/scheduled/{id}/{filename}", h.handleGetScheduledArtifact)

	req := requestAs("GET",
		"/api/v1/artifacts/scheduled/"+idgen.New()+"/"+schedArtTestFilename,
		schedArtTestOwner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when artSvc nil", rec.Code)
	}
}
