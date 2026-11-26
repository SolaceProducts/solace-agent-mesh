// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newSchedulerExec(t *testing.T, opts ...runtime.Option) *GatewayExecutor {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, opts...)
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
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{"enabled": true},
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

func seedControlTask(t *testing.T, exe *GatewayExecutor, name, owner, source string) *store.ScheduledTask {
	t.Helper()
	svc := exe.Gateways()[0].ScheduledTasks()
	if svc == nil {
		t.Fatal("scheduler service is nil; scheduler not enabled")
	}
	createSource := source
	if source == "config" {
		createSource = TaskSourceChat
	}
	task, err := svc.CreateScheduledTask(t.Context(), CreateScheduledTaskParams{
		Name:               name,
		ScheduleType:       "cron",
		ScheduleExpression: "0 9 * * *",
		TargetType:         "workflow",
		TargetName:         "wf",
		TaskMessage:        `[{"kind":"text","text":"do the thing"}]`,
		Source:             createSource,
		Enabled:            true,
		CreatedBy:          owner,
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if source == "config" {
		if err := svc.repo.Update(t.Context(), task.ID, map[string]any{"source": "config"}); err != nil {
			t.Fatalf("mark seeded task config-managed: %v", err)
		}
		task.Source = "config"
	}
	return task
}

func mustBody(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return b
}

func asDTO(t *testing.T, res any) scheduledTaskDTO {
	t.Helper()
	dto, ok := res.(scheduledTaskDTO)
	if !ok {
		t.Fatalf("result is %T, want scheduledTaskDTO", res)
	}
	return dto
}

func TestScheduledTasksControl_RoutingOwnershipAndLifecycle(t *testing.T) {
	exe := newSchedulerExec(t)
	ctx := t.Context()
	const alice, bob = "alice@example.com", "bob@example.com"
	task := seedControlTask(t, exe, "daily-report", alice, "chat")

	aliceBody := mustBody(t, map[string]any{"userId": alice})
	bobBody := mustBody(t, map[string]any{"userId": bob})

	sched := func(method string, seg []string, body json.RawMessage) (any, error) {
		return exe.HandleManagement(ctx, method, append([]string{"scheduledTasks"}, seg...), body, nil)
	}

	res, err := sched("get", []string{task.ID}, aliceBody)
	if err != nil {
		t.Fatalf("get as owner: %v", err)
	}
	if dto := asDTO(t, res); dto.TaskMessage != "do the thing" {
		t.Errorf("taskMessage = %q, want plain text %q", dto.TaskMessage, "do the thing")
	}

	for _, tc := range []struct {
		method string
		seg    []string
		body   json.RawMessage
	}{
		{"get", []string{task.ID}, bobBody},
		{"patch", []string{task.ID}, mustBody(t, map[string]any{"userId": bob, "name": "hijacked"})},
		{"delete", []string{task.ID}, bobBody},
		{"post", []string{task.ID, "disable"}, bobBody},
	} {
		if _, rejErr := sched(tc.method, tc.seg, tc.body); rejErr == nil || !strings.Contains(rejErr.Error(), "not found") {
			t.Errorf("%s as non-owner = %v, want not-found", tc.method, rejErr)
		}
	}
	if got, _ := exe.Gateways()[0].ScheduledTasks().repo.GetByID(ctx, task.ID); got == nil || got.Name != "daily-report" {
		t.Errorf("task mutated by rejected non-owner calls: %+v", got)
	}

	res, err = sched("patch", []string{task.ID}, mustBody(t, map[string]any{
		"userId": alice, "name": "  Renamed  ", "scheduleExpression": "0 8 * * *",
	}))
	if err != nil {
		t.Fatalf("update as owner: %v", err)
	}
	dto := asDTO(t, res)
	if dto.Name != "Renamed" {
		t.Errorf("name = %q, want trimmed %q", dto.Name, "Renamed")
	}
	if dto.ScheduleExpression != "0 8 * * *" {
		t.Errorf("scheduleExpression = %q, want updated", dto.ScheduleExpression)
	}
	if dto.NextRunTime == nil {
		t.Error("nextRunTime should be populated after re-arming an enabled cron task")
	}

	if _, disErr := sched("post", []string{task.ID, "disable"}, aliceBody); disErr != nil {
		t.Fatalf("disable: %v", disErr)
	}
	listEnabled, err := sched("get", nil, mustBody(t, map[string]any{"userId": alice, "enabledOnly": true}))
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if listContainsID(listEnabled, task.ID) {
		t.Error("disabled task should not appear in enabledOnly list")
	}
	if _, err := sched("post", []string{task.ID, "enable"}, aliceBody); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if _, err := sched("delete", []string{task.ID}, aliceBody); err != nil {
		t.Fatalf("delete: %v", err)
	}
	listAll, _ := sched("get", nil, aliceBody)
	if listContainsID(listAll, task.ID) {
		t.Error("deleted task should not appear in list")
	}
}

func TestScheduledTasksControl_ATaskThatRunsAsASystemUserIsEditedOnlyThroughTheAPI(t *testing.T) {
	type outcome struct {
		Refused bool
		Name    string
		Enabled bool
		RunAs   string
	}
	cases := []struct {
		name   string
		runAs  string
		method string
		seg    []string
		extra  map[string]any
		want   outcome
	}{
		{"an edit of a task that runs as a service account is refused", "system:nightly", "patch", nil, map[string]any{"name": "renamed"},
			outcome{Refused: true, Name: "daily-report", Enabled: true, RunAs: "system:nightly"}},
		{"pausing a task that runs as a service account still works", "system:nightly", "post", []string{"disable"}, nil,
			outcome{Name: "daily-report", Enabled: false, RunAs: "system:nightly"}},
		{"an edit of a task that runs as its creator still works", "", "patch", nil, map[string]any{"name": "renamed"},
			outcome{Name: "renamed", Enabled: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exe := newSchedulerExec(t)
			const alice = "alice@example.com"
			task := seedControlTask(t, exe, "daily-report", alice, "chat")
			repo := exe.Gateways()[0].ScheduledTasks().repo
			if c.runAs != "" {
				if err := repo.Update(t.Context(), task.ID, map[string]any{"run_as": c.runAs}); err != nil {
					t.Fatalf("Update: %v", err)
				}
			}
			body := map[string]any{"userId": alice}
			maps.Copy(body, c.extra)

			_, err := exe.HandleManagement(t.Context(), c.method, append([]string{"scheduledTasks", task.ID}, c.seg...), mustBody(t, body), nil)

			stored, getErr := repo.GetByID(t.Context(), task.ID)
			if getErr != nil || stored == nil {
				t.Fatalf("GetByID: %v, %v", stored, getErr)
			}
			got := outcome{Refused: err != nil, Name: stored.Name, Enabled: stored.Enabled}
			if stored.RunAs != nil {
				got.RunAs = *stored.RunAs
			}
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Errorf("%s of a task that runs as %q mismatch (-want +got):\n%s", c.method, c.runAs, diff)
			}
			if err != nil && !strings.Contains(err.Error(), "runs as a service account") {
				t.Errorf("refusal = %v, want it to say the task runs as a service account", err)
			}
		})
	}
}

func listContainsID(res any, id string) bool {
	m, ok := res.(map[string]any)
	if !ok {
		return false
	}
	tasks, ok := m["tasks"].([]scheduledTaskDTO)
	if !ok {
		return false
	}
	for _, dt := range tasks {
		if dt.ID == id {
			return true
		}
	}
	return false
}

func TestScheduledTasksControl_ConfigManagedGuard(t *testing.T) {
	exe := newSchedulerExec(t)
	ctx := t.Context()
	const owner = "alice@example.com"
	task := seedControlTask(t, exe, "config-task", owner, "config")
	body := mustBody(t, map[string]any{"userId": owner})

	sched := func(method string, seg []string, body json.RawMessage) (any, error) {
		return exe.HandleManagement(ctx, method, append([]string{"scheduledTasks"}, seg...), body, nil)
	}

	if _, err := sched("patch", []string{task.ID}, mustBody(t, map[string]any{"userId": owner, "name": "x"})); err == nil || !strings.Contains(err.Error(), "config-managed") {
		t.Errorf("update config task = %v, want config-managed error", err)
	}
	if _, err := sched("delete", []string{task.ID}, body); err == nil || !strings.Contains(err.Error(), "config-managed") {
		t.Errorf("delete config task = %v, want config-managed error", err)
	}
	if _, err := sched("post", []string{task.ID, "disable"}, body); err != nil {
		t.Errorf("disable config task should be allowed: %v", err)
	}
	if _, err := sched("post", []string{task.ID, "enable"}, body); err != nil {
		t.Errorf("enable config task should be allowed: %v", err)
	}
}

func TestScheduledTasksControl_AuthzRequiresIdentityAndIgnoresSpoof(t *testing.T) {
	exe := newSchedulerExec(t, runtime.WithControlAuthorizer(auth.DenyAllControlAuthorizer{}))
	ctx := t.Context()
	const alice, bob = "alice@example.com", "bob@example.com"
	task := seedControlTask(t, exe, "alice-task", alice, "chat")

	_, err := exe.HandleManagement(ctx, "get", []string{"scheduledTasks"},
		mustBody(t, map[string]any{"userId": alice}), nil)
	if err == nil || !strings.Contains(err.Error(), "authenticated identity is required") {
		t.Fatalf("authz-on + nil identity = %v, want 'authenticated identity is required'", err)
	}

	res, err := exe.HandleManagement(ctx, "get", []string{"scheduledTasks"},
		mustBody(t, map[string]any{"userId": bob}), &auth.Identity{UserID: alice})
	if err != nil {
		t.Fatalf("authz-on with identity: %v", err)
	}
	if !listContainsID(res, task.ID) {
		t.Error("verified identity alice should see her own task despite a spoofed body userId")
	}
}

func TestScheduledTaskManage_RepoErrorsPropagate(t *testing.T) {
	exe := newSchedulerExec(t)
	ctx := t.Context()
	const owner = "alice@example.com"
	svc := exe.Gateways()[0].ScheduledTasks()
	task := seedControlTask(t, exe, "will-error", owner, "chat")

	if err := exe.Gateways()[0].Store().Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if _, _, err := svc.ListTasks(ctx, owner, "mine", false, 10, 0); err == nil {
		t.Error("ListTasks(mine) should error on a closed store")
	}
	if _, _, err := svc.ListTasks(ctx, owner, "namespace", false, 10, 0); err == nil {
		t.Error("ListTasks(namespace) should error on a closed store")
	}
	if _, err := svc.ApplyUpdate(ctx, task.ID, map[string]any{"name": "x"}, false); err == nil {
		t.Error("ApplyUpdate should error on a closed store")
	}
	if _, err := svc.EnableTask(ctx, task.ID); err == nil {
		t.Error("EnableTask should error on a closed store")
	}
	if err := svc.DisableTask(ctx, task.ID); err == nil {
		t.Error("DisableTask should error on a closed store")
	}
	if err := svc.DeleteTask(ctx, task.ID, owner); err == nil {
		t.Error("DeleteTask should error on a closed store")
	}
}

func TestApplyUpdate_ScheduleChangeWritesNextRunAt(t *testing.T) {
	exe := newSchedulerExec(t)
	ctx := t.Context()
	svc := exe.Gateways()[0].ScheduledTasks()
	task := seedControlTask(t, exe, "rescheduled", "alice@example.com", "chat")

	before := time.Now()
	updated, err := svc.ApplyUpdate(ctx, task.ID, map[string]any{
		"schedule_type": "cron", "schedule_expression": "0 8 * * *", "timezone": "UTC",
	}, true)
	if err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if updated.NextRunAt == nil {
		t.Fatal("next_run_at is NULL")
	}
	next := time.UnixMilli(*updated.NextRunAt).UTC()
	if next.Hour() != 8 || next.Minute() != 0 || !next.After(before) || next.Sub(before) > 24*time.Hour {
		t.Fatalf("next_run_at = %s, want the next 08:00 UTC after %s", next, before.UTC())
	}
	stored, err := svc.repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.NextRunAt == nil || *stored.NextRunAt != *updated.NextRunAt {
		t.Fatalf("stored next_run_at = %v, returned %v", stored.NextRunAt, updated.NextRunAt)
	}
}

func TestEnableTask_WritesNextRunAt(t *testing.T) {
	exe := newSchedulerExec(t)
	ctx := t.Context()
	svc := exe.Gateways()[0].ScheduledTasks()
	task := seedControlTask(t, exe, "re-enabled", "alice@example.com", "chat")
	if err := svc.repo.Update(ctx, task.ID, map[string]any{"enabled": 0, "next_run_at": nil}); err != nil {
		t.Fatal(err)
	}

	enabled, err := svc.EnableTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("EnableTask: %v", err)
	}
	if enabled.NextRunAt == nil || *enabled.NextRunAt <= time.Now().UnixMilli()-1000 {
		t.Fatalf("next_run_at = %v, want a future fire time", enabled.NextRunAt)
	}
}

func TestBuildControlUpdateFields(t *testing.T) {
	base := &store.ScheduledTask{
		ScheduleType:       "cron",
		ScheduleExpression: "0 9 * * *",
		Timezone:           "UTC",
	}
	str := func(s string) *string { return &s }

	t.Run("trims name", func(t *testing.T) {
		fields, _, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{Name: str("  Daily  ")})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if fields["name"] != "Daily" {
			t.Errorf("name = %v, want trimmed 'Daily'", fields["name"])
		}
	})

	t.Run("blank name dropped", func(t *testing.T) {
		fields, _, _ := buildControlUpdateFields(base, scheduledTaskUpdateRequest{Name: str("   ")})
		if _, ok := fields["name"]; ok {
			t.Error("all-whitespace name should be dropped, not stored")
		}
	})

	t.Run("message encoded as parts array", func(t *testing.T) {
		fields, _, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{Message: str("hello")})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if fields["task_message"] != `[{"kind":"text","text":"hello"}]` {
			t.Errorf("task_message = %v, want create-path parts array", fields["task_message"])
		}
	})

	t.Run("blank message is an error, not a silent success", func(t *testing.T) {
		for _, blank := range []string{"   ", "\n\t "} {
			fields, _, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{Name: str("Renamed"), Message: str(blank)})
			if err == nil {
				t.Errorf("message %q returned no error and fields %v", blank, fields)
			}
		}
	})

	t.Run("empty message is left unchanged", func(t *testing.T) {
		fields, _, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{Name: str("Renamed"), Message: str("")})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if _, ok := fields["task_message"]; ok {
			t.Error("an empty message must leave the stored message unchanged")
		}
	})

	t.Run("invalid scheduleType rejected", func(t *testing.T) {
		if _, _, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{ScheduleType: str("weekly")}); err == nil {
			t.Error("bad scheduleType should error")
		}
	})

	t.Run("invalid timezone rejected", func(t *testing.T) {
		for _, tz := range []string{"Mars/Phobos", "Local"} {
			if _, _, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{Timezone: str(tz)}); err == nil {
				t.Errorf("timezone %q should error", tz)
			}
		}
	})

	t.Run("bad cron expression rejected via effective validation", func(t *testing.T) {
		if _, _, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{ScheduleExpression: str("not a cron")}); err == nil {
			t.Error("bad cron expression should error")
		}
	})

	t.Run("timezone-only change validated against existing type+expr", func(t *testing.T) {
		fields, changed, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{Timezone: str("America/New_York")})
		if err != nil {
			t.Fatalf("valid tz change should pass: %v", err)
		}
		if !changed || fields["timezone"] != "America/New_York" {
			t.Errorf("expected schedule change with new timezone, got fields=%v changed=%v", fields, changed)
		}
	})

	t.Run("empty request yields no fields and no schedule change", func(t *testing.T) {
		fields, changed, err := buildControlUpdateFields(base, scheduledTaskUpdateRequest{})
		if err != nil || len(fields) != 0 || changed {
			t.Errorf("empty request: fields=%v changed=%v err=%v", fields, changed, err)
		}
	})
}
