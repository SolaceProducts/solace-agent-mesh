// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func pathSet(eps []control.Endpoint) map[string]bool {
	m := make(map[string]bool, len(eps))
	for _, ep := range eps {
		m[ep.Path] = true
	}
	return m
}

func newActivityExec(t *testing.T, sentinelOn, activityOn bool) *GatewayExecutor {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyObservabilityAgent, Name: "sentinel", Default: sentinelOn})
	reg.Register(features.Definition{Key: features.KeyActivityMonitor, Name: "activity", Default: activityOn})

	rt := runtime.NewRuntime(brk, nil, runtime.WithFeatureRegistry(reg))
	return NewGatewayExecutor(rt.Services(), fakeGatewayFactory)
}

func TestManagementEndpoints_ActivityFlagOff_SentinelSurfaceIntact(t *testing.T) {
	exe := newActivityExec(t, true, false)
	gotJSON := marshalEndpoints(t, exe.ManagementEndpoints())

	baseline := []control.Endpoint{
		{Method: "get", Path: "instances", Description: "List all gateway instances"},
		{Method: "get", Path: "instances/{name}", Description: "Get gateway instance details"},
		{Method: "post", Path: "instances", Description: "Add and start a new gateway instance"},
		{Method: "delete", Path: "instances/{name}", Description: "Stop and remove a gateway instance"},
		{Method: "patch", Path: "instances/{name}", Description: "Enable/disable (start/stop) a gateway instance"},
	}
	want := slices.Concat(baseline, analyticsControlEndpoints())
	wantJSON := marshalEndpoints(t, want)

	if gotJSON != wantJSON {
		t.Errorf("flag-off discovery surface drifted:\n got:  %s\n want: %s", gotJSON, wantJSON)
	}
	if strings.Contains(gotJSON, "activity") {
		t.Errorf("flag-off surface contains 'activity' substring: %s", gotJSON)
	}
}

func TestManagementEndpoints_ActivityFlagOn_AdvertisesAll(t *testing.T) {
	exe := newActivityExec(t, true, true)
	got := pathSet(exe.ManagementEndpoints())

	want := []string{
		"activity/tasks/summary",
		"activity/tasks/failed",
		"activity/tokens/summary",
		"activity/agents/performance",
		"activity/workflows/performance",
		"activity/workflows/breakdown",
		"activity/feedback",
		"activity/feedback/summary",
		"activity/feedback/rows",
		"activity/tasks/{taskId}/trace",
	}
	for _, p := range want {
		if !got[p] {
			t.Errorf("activity flag on but discovery missing %q", p)
		}
	}
}

func TestHandleManagement_ActivityFlagOff_Rejects(t *testing.T) {
	exe := newActivityExec(t, true, false)

	_, err := exe.HandleManagement(context.Background(), "get",
		[]string{"activity", "tasks", "summary"}, nil,
		&auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	if err == nil {
		t.Fatal("flag off: activity call must error")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("flag off error = %v, want message containing 'not enabled'", err)
	}
}

func newStoreBackedActivityExec(t *testing.T) *GatewayExecutor {
	t.Helper()
	return newStoreBackedActivityExecWith(t)
}

func newStoreBackedActivityExecWith(t *testing.T, opts ...runtime.Option) *GatewayExecutor {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyObservabilityAgent, Name: "sentinel", Default: true})
	reg.Register(features.Definition{Key: features.KeyActivityMonitor, Name: "activity", Default: true})
	rt := runtime.NewRuntime(brk, nil, append([]runtime.Option{runtime.WithFeatureRegistry(reg)}, opts...)...)

	factory := func(appConfig map[string]any, s runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(&recordingAdapter{}, appConfig)
		core.SetServices(s)
		return core, nil
	}
	exe := NewGatewayExecutor(rt.Services(), factory)
	cfg := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "gw-1",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestDBURL(t),
		},
	}
	if err := exe.LoadConfig("gw-1", cfg); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	return exe
}

func TestHandleActivity_NilIdentityCannotBecomeAdmin(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	_, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "agents", "performance"},
		[]byte(`{"userId":"bob"}`), nil)
	if !errors.Is(err, analytics.ErrForbiddenCrossUser) {
		t.Errorf("nil identity + userId=bob must be rejected with ErrForbiddenCrossUser; got %v", err)
	}
}

func TestHandleActivity_NilIdentityEmptyBodySucceeds(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	if _, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "agents", "performance"},
		[]byte(`{}`), nil); err != nil {
		t.Errorf("nil identity + empty body (own-scope report) must succeed; got %v", err)
	}
}

func TestHandleActivity_PartialGlobCannotDrillIntoAnotherUser(t *testing.T) {
	for _, granted := range []string{"activity:**:read", "a*:*:read"} {
		t.Run(granted, func(t *testing.T) {
			exe := newStoreBackedActivityExec(t)
			mallory := &auth.Identity{UserID: "mallory", Scopes: []string{granted}}
			_, err := exe.HandleManagement(t.Context(), "get",
				[]string{"activity", "agents", "performance"},
				[]byte(`{"userId":"bob"}`), mallory)
			if !errors.Is(err, analytics.ErrForbiddenCrossUser) {
				t.Errorf("%s targeting bob must return ErrForbiddenCrossUser; got %v", granted, err)
			}
		})
	}
}

func TestHandleActivity_NonAdminCannotDrillIntoAnotherUser(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	_, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "agents", "performance"},
		[]byte(`{"userId":"bob"}`), alice)
	if !errors.Is(err, analytics.ErrForbiddenCrossUser) {
		t.Errorf("alice without activity:*:read targeting bob must return ErrForbiddenCrossUser; got %v", err)
	}
}

func TestHandleActivity_NonAdminSelfSucceeds(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	if _, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "agents", "performance"},
		[]byte(`{"userId":"alice"}`), alice); err != nil {
		t.Errorf("alice targeting her own data must succeed; got %v", err)
	}
}

func TestHandleActivity_ScopedAdminGrantsCrossUser(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}
	_, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "agents", "performance"},
		[]byte(`{"userId":"bob"}`), admin)
	if err != nil {
		t.Errorf("scoped admin + userId=bob should succeed; got %v", err)
	}
}

func TestHandleActivity_NonAdminReportContainsOnlyOwnRows(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	db := exe.Gateways()[0].Store()
	if db == nil {
		t.Fatal("store-backed exec has no database")
	}
	taskRepo := store.NewTaskRepo(db)
	sp := func(s string) *string { return &s }
	ip := func(v int64) *int64 { return &v }

	now := time.Now().UnixMilli()
	mk := func(id, user, agent string) {
		if err := taskRepo.Create(t.Context(), &store.Task{
			ID: id, UserID: user, StartTime: now - 3_600_000, EndTime: ip(now - 3_599_000),
			Status: sp("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: sp(agent), HandlerType: sp(store.TaskHandlerTypeAgent),
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("a0000000-0000-0000-0000-000000000001", "alice", "alice_agent")
	mk("b0000000-0000-0000-0000-000000000001", "bob", "bob_agent")

	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	res, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "agents", "performance"}, []byte(`{}`), alice)
	if err != nil {
		t.Fatalf("alice own-scope report: %v", err)
	}
	page, ok := res.(*analytics.Paginated[analytics.AgentPerformanceRow])
	if !ok {
		t.Fatalf("unexpected response type %T", res)
	}
	var names []string
	for _, row := range page.Data {
		names = append(names, row.Agent.Name)
	}
	if !slices.Contains(names, "alice_agent") {
		t.Errorf("alice's own agent missing from her report: %v", names)
	}
	if slices.Contains(names, "bob_agent") {
		t.Errorf("bob's agent leaked into alice's own-scope report %v — OwnerUserID did not filter the rows", names)
	}
}

func TestHandleActivity_FailedTasks_NonAdminCannotDrillIntoAnotherUser(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	_, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "tasks", "failed"},
		[]byte(`{"userId":"bob"}`), alice)
	if !errors.Is(err, analytics.ErrForbiddenCrossUser) {
		t.Errorf("non-admin listing bob's failed tasks must return ErrForbiddenCrossUser; got %v", err)
	}
}

func TestHandleActivity_FailedTasks_NonAdminSelfSucceeds(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	if _, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "tasks", "failed"},
		[]byte(`{"userId":"alice"}`), alice); err != nil {
		t.Errorf("alice listing her own failed tasks must succeed; got %v", err)
	}
}

func TestHandleActivity_FailedTasks_ScopedAdminGrantsCrossUser(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}
	if _, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "tasks", "failed"},
		[]byte(`{"userId":"bob"}`), admin); err != nil {
		t.Errorf("scoped admin listing bob's failed tasks should succeed; got %v", err)
	}
}

func TestHandleActivity_FailedTasks_NonAdminReportContainsOnlyOwnRows(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	db := exe.Gateways()[0].Store()
	if db == nil {
		t.Fatal("store-backed exec has no database")
	}
	taskRepo := store.NewTaskRepo(db)
	sp := func(s string) *string { return &s }
	ip := func(v int64) *int64 { return &v }

	now := time.Now().UnixMilli()
	mk := func(id, user, request string) {
		if err := taskRepo.Create(t.Context(), &store.Task{
			ID: id, UserID: user, StartTime: now - 3_600_000, EndTime: ip(now - 3_599_000),
			Status: sp("failed"), InitialRequestText: sp(request),
			ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	const aliceTask = "a0000000-0000-0000-0000-0000000000fa"
	const bobTask = "b0000000-0000-0000-0000-0000000000fb"
	mk(aliceTask, "alice", "alice failing request")
	mk(bobTask, "bob", "bob failing request")

	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	res, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "tasks", "failed"}, []byte(`{}`), alice)
	if err != nil {
		t.Fatalf("alice own-scope failed-tasks report: %v", err)
	}
	page, ok := res.(*analytics.Paginated[analytics.TaskHealthRow])
	if !ok {
		t.Fatalf("unexpected response type %T", res)
	}
	var ids []string
	for _, row := range page.Data {
		ids = append(ids, row.ID)
	}
	if !slices.Contains(ids, aliceTask) {
		t.Errorf("alice's own failed task missing from her report: %v", ids)
	}
	if slices.Contains(ids, bobTask) {
		t.Errorf("bob's failed task leaked into alice's own-scope report %v — OwnerUserID did not filter the rows", ids)
	}
}

func TestHandleActivity_Feedback_NonAdminCannotDrillIntoAnotherUser(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	_, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "feedback"},
		[]byte(`{"userId":"bob"}`), alice)
	if !errors.Is(err, analytics.ErrForbiddenCrossUser) {
		t.Errorf("non-admin listing bob's feedback must return ErrForbiddenCrossUser; got %v", err)
	}
}

func TestHandleActivity_Feedback_NonAdminSelfSucceeds(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	if _, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "feedback"},
		[]byte(`{"userId":"alice"}`), alice); err != nil {
		t.Errorf("alice listing her own feedback must succeed; got %v", err)
	}
}

func TestHandleActivity_Feedback_ScopedAdminGrantsCrossUser(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}
	if _, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "feedback"},
		[]byte(`{"userId":"bob"}`), admin); err != nil {
		t.Errorf("scoped admin listing bob's feedback should succeed; got %v", err)
	}
}

func TestHandleActivity_Feedback_NonAdminReportContainsOnlyOwnRows(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	db := exe.Gateways()[0].Store()
	if db == nil {
		t.Fatal("store-backed exec has no database")
	}
	mk := func(taskID, sessionID, userID, subjectID string) {
		store.SeedFeedback(t, db, &store.Feedback{
			TaskID: taskID, SessionID: sessionID, UserID: userID, Rating: "up",
			SubjectID: subjectID, SubjectType: store.SubjectTypeAgent,
			EntrypointType: store.EntrypointTypeWebUI,
		})
	}
	const aliceTask = "a0000000-0000-0000-0000-0000000000fc"
	const bobTask = "b0000000-0000-0000-0000-0000000000fd"
	mk(aliceTask, "a0000000-0000-0000-0000-000000000001", "alice", "AliceAgent")
	mk(bobTask, "b0000000-0000-0000-0000-000000000002", "bob", "BobAgent")

	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	res, err := exe.HandleManagement(t.Context(), "get",
		[]string{"activity", "feedback"}, []byte(`{}`), alice)
	if err != nil {
		t.Fatalf("alice own-scope feedback report: %v", err)
	}
	page, ok := res.(*analytics.Paginated[analytics.ActivityFeedbackRow])
	if !ok {
		t.Fatalf("unexpected response type %T", res)
	}
	var ids []string
	for _, row := range page.Data {
		ids = append(ids, row.TaskID)
	}
	if !slices.Contains(ids, aliceTask) {
		t.Errorf("alice's own feedback missing from her report: %v", ids)
	}
	if slices.Contains(ids, bobTask) {
		t.Errorf("bob's feedback leaked into alice's own-scope report %v — OwnerUserID did not filter the rows", ids)
	}
}

func TestHandleActivity_Trace_IDOR(t *testing.T) {
	exe := newStoreBackedActivityExec(t)

	db := exe.Gateways()[0].Store()
	if db == nil {
		t.Fatal("expected a store-backed gateway")
	}
	bobTask := "019f0000-0000-7000-8000-0000000000bb"
	status := "completed"
	if err := store.NewTaskRepo(db).Create(t.Context(), &store.Task{
		ID: bobTask, UserID: "bob", StartTime: 1_700_000_000_000,
		Status: &status, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
	}); err != nil {
		t.Fatalf("seed bob's task: %v", err)
	}

	tracePath := []string{"activity", "tasks", bobTask, "trace"}

	alice := &auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	if _, err := exe.HandleManagement(t.Context(), "get", tracePath, nil, alice); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("non-admin reading bob's task tree must be rejected as not-found; got %v", err)
	}

	bob := &auth.Identity{UserID: "bob", Scopes: []string{rbac.ScopeAgentInvokeAny}}
	if _, err := exe.HandleManagement(t.Context(), "get", tracePath, nil, bob); err != nil {
		t.Errorf("owner reading own task tree must succeed; got %v", err)
	}

	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}
	if _, err := exe.HandleManagement(t.Context(), "get", tracePath, nil, admin); err != nil {
		t.Errorf("admin reading any task tree must succeed; got %v", err)
	}
}

func TestHandleManagement_ActivityTrace_MalformedTaskID_Rejected(t *testing.T) {
	exe := newActivityExec(t, true, true)

	_, err := exe.HandleManagement(context.Background(), "get",
		[]string{"activity", "tasks", "not-a-uuid", "trace"}, nil,
		&auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	if err == nil {
		t.Fatal("malformed taskId: trace handler must reject with a validation error")
	}
	if !strings.Contains(err.Error(), "valid UUID") {
		t.Errorf("error = %v, want message mentioning 'valid UUID'", err)
	}

	_, err = exe.HandleManagement(context.Background(), "get",
		[]string{"activity", "tasks", "", "trace"}, nil,
		&auth.Identity{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	if err == nil {
		t.Fatal("empty taskId: trace handler must reject")
	}
	if !strings.Contains(err.Error(), "required") && !strings.Contains(err.Error(), "valid UUID") {
		t.Errorf("empty taskId error = %v, want required-or-uuid message", err)
	}
}

func TestActivityRequestToQuery_PageSize(t *testing.T) {
	t.Run("groupBy=day over 100 errors", func(t *testing.T) {
		_, err := (activityRequest{GroupBy: "day", PageSize: maxAnalyticsPageSize + 1}).toQuery("tokens/summary")
		if err == nil {
			t.Fatal("expected error for pageSize over the day/default limit")
		}
		if !strings.Contains(err.Error(), "between 1 and 100") {
			t.Errorf("error = %q, want it to name the 100 ceiling", err.Error())
		}
	})
	t.Run("groupBy=day at 100 ok", func(t *testing.T) {
		q, err := activityRequest{GroupBy: "day", PageSize: maxAnalyticsPageSize}.toQuery("tokens/summary")
		if err != nil || q.PageSize != maxAnalyticsPageSize {
			t.Errorf("pageSize=%d err=%v, want %d/nil", q.PageSize, err, maxAnalyticsPageSize)
		}
	})
	t.Run("tokens/summary groupBy=user over 100 but under 5000 ok — the exception", func(t *testing.T) {
		q, err := activityRequest{GroupBy: "user", PageSize: maxAnalyticsPageSize + 1}.toQuery("tokens/summary")
		if err != nil || q.PageSize != maxAnalyticsPageSize+1 {
			t.Errorf("pageSize=%d err=%v, want %d/nil", q.PageSize, err, maxAnalyticsPageSize+1)
		}
	})
	t.Run("tokens/summary groupBy=user at 5000 ok", func(t *testing.T) {
		q, err := activityRequest{GroupBy: "user", PageSize: maxTopUsersPageSize}.toQuery("tokens/summary")
		if err != nil || q.PageSize != maxTopUsersPageSize {
			t.Errorf("pageSize=%d err=%v, want %d/nil", q.PageSize, err, maxTopUsersPageSize)
		}
	})
	t.Run("tokens/summary groupBy=user over 5000 errors", func(t *testing.T) {
		_, err := (activityRequest{GroupBy: "user", PageSize: maxTopUsersPageSize + 1}).toQuery("tokens/summary")
		if err == nil {
			t.Fatal("expected error for pageSize over the groupBy=user limit")
		}
		if !strings.Contains(err.Error(), "between 1 and 5000") {
			t.Errorf("error = %q, want it to name the 5000 ceiling", err.Error())
		}
	})
	t.Run("negative pageSize errors regardless of groupBy", func(t *testing.T) {
		if _, err := (activityRequest{GroupBy: "user", PageSize: -1}).toQuery("tokens/summary"); err == nil {
			t.Error("expected error for negative pageSize")
		}
	})
	t.Run("groupBy=user on a DIFFERENT report does NOT get the 5000 exception", func(t *testing.T) {
		for _, report := range []string{"tasks/failed", "agents/performance", "workflows/performance", "workflows/breakdown"} {
			_, err := (activityRequest{GroupBy: "user", PageSize: maxAnalyticsPageSize + 1}).toQuery(report)
			if err == nil {
				t.Errorf("report %q: expected pageSize %d to be rejected (only tokens/summary gets the 5000 exception)",
					report, maxAnalyticsPageSize+1)
			}
		}
	})
}

func marshalEndpoints(t *testing.T, eps []control.Endpoint) string {
	t.Helper()
	b, err := json.Marshal(eps)
	if err != nil {
		t.Fatalf("marshal endpoints: %v", err)
	}
	return string(b)
}
