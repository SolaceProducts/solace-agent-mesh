// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func chatCreateParams(name string) CreateScheduledTaskParams {
	return CreateScheduledTaskParams{
		Name:               name,
		ScheduleType:       "one_time",
		ScheduleExpression: "2099-01-01T00:00:00Z",
		TargetType:         "workflow",
		TargetName:         "reconcile",
		TaskMessage:        `[{"kind":"text","text":"do it"}]`,
		Source:             "chat",
		Enabled:            true,
		CreatedBy:          "alice@example.com",
	}
}

func TestCreateScheduledTask_ChatDefaultsInboxNotification(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	task, err := svc.CreateScheduledTask(t.Context(), chatCreateParams("remind-me"))
	if err != nil {
		t.Fatalf("CreateScheduledTask: %v", err)
	}
	if task.Source != "chat" {
		t.Errorf("source = %q, want chat", task.Source)
	}
	if task.NotificationConfig == nil || !strings.Contains(*task.NotificationConfig, `"inbox"`) {
		t.Errorf("chat task should default to an inbox notification, got %v", task.NotificationConfig)
	}
	reloaded, rerr := svc.repo.GetByID(t.Context(), task.ID)
	if rerr != nil || reloaded == nil {
		t.Fatalf("reload created task: row=%v err=%v", reloaded, rerr)
	}
	if reloaded.NotificationConfig == nil || !strings.Contains(*reloaded.NotificationConfig, `"inbox"`) {
		t.Errorf("persisted notification_config lost the inbox channel: %v", reloaded.NotificationConfig)
	}
	if *reloaded.NotificationConfig != defaultNotificationConfig || !notificationConfigIncludesArtifacts(*reloaded.NotificationConfig) {
		t.Errorf("chat task should store the default that also delivers files, got %s", *reloaded.NotificationConfig)
	}
}

func TestCreateScheduledTask_ChatExplicitNotificationWins(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	p := chatCreateParams("custom-notify")
	custom := `{"on_success":true,"on_failure":false,"channels":[{"type":"sse","config":{}}]}`
	p.NotificationConfig = &custom
	task, err := svc.CreateScheduledTask(t.Context(), p)
	if err != nil {
		t.Fatalf("CreateScheduledTask: %v", err)
	}
	if task.NotificationConfig == nil || strings.Contains(*task.NotificationConfig, `"inbox"`) {
		t.Errorf("explicit notification config must win over the inbox default, got %v", task.NotificationConfig)
	}
}

func TestCreateScheduledTask_NonChatStoresNoNotificationConfig(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	p := chatCreateParams("workflow-origin")
	p.Source = "workflow"
	task, err := svc.CreateScheduledTask(t.Context(), p)
	if err != nil {
		t.Fatalf("CreateScheduledTask: %v", err)
	}
	if task.NotificationConfig != nil {
		t.Errorf("non-chat task should store no notification config (the default applies when a run finishes), got %v", *task.NotificationConfig)
	}
}

func TestScheduledTaskManage_ListUpdateEnableDisableDelete(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	const owner = "alice@example.com"

	task, err := svc.CreateScheduledTask(ctx, chatCreateParams("nightly"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, scope := range []string{"mine", "namespace"} {
		tasks, total, lerr := svc.ListTasks(ctx, owner, scope, false, 50, 0)
		if lerr != nil {
			t.Fatalf("ListTasks(%s): %v", scope, lerr)
		}
		if total < 1 || !containsTaskID(tasks, task.ID) {
			t.Errorf("ListTasks(%s) missing task %q (total=%d)", scope, task.ID, total)
		}
	}

	updated, err := svc.ApplyUpdate(ctx, task.ID, map[string]any{
		"name":                "renamed",
		"schedule_expression": "2099-06-01T00:00:00Z",
	}, true)
	if err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if updated.Name != "renamed" || updated.ScheduleExpression != "2099-06-01T00:00:00Z" {
		t.Errorf("update not applied: %+v", updated)
	}

	if disErr := svc.DisableTask(ctx, task.ID); disErr != nil {
		t.Fatalf("DisableTask: %v", disErr)
	}
	if got, _ := svc.repo.GetByID(ctx, task.ID); got == nil || got.Enabled {
		t.Errorf("task should be disabled, got %+v", got)
	}
	reEnabled, err := svc.EnableTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("EnableTask: %v", err)
	}
	if !reEnabled.Enabled {
		t.Errorf("task should be enabled after EnableTask, got %+v", reEnabled)
	}

	if err := svc.DeleteTask(ctx, task.ID, owner); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	got, _ := svc.repo.GetByID(ctx, task.ID)
	if got == nil || got.DeletedAt == nil {
		t.Errorf("task should be soft-deleted, got %+v", got)
	}
	tasks, _, _ := svc.ListTasks(ctx, owner, "namespace", false, 50, 0)
	if containsTaskID(tasks, task.ID) {
		t.Errorf("deleted task should not appear in ListTasks")
	}
}

func TestApplyUpdate_NameConflict(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	first, err := svc.CreateScheduledTask(ctx, chatCreateParams("first"))
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if _, createErr := svc.CreateScheduledTask(ctx, chatCreateParams("second")); createErr != nil {
		t.Fatalf("create second: %v", createErr)
	}

	_, err = svc.ApplyUpdate(ctx, first.ID, map[string]any{"name": "second"}, false)
	if !errors.Is(err, ErrTaskNameConflict) {
		t.Errorf("ApplyUpdate name collision = %v, want ErrTaskNameConflict", err)
	}
}

func TestApplyUpdate_EnablingWritesNextRun(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	p := chatCreateParams("enable-later")
	p.Enabled = false
	task, err := svc.CreateScheduledTask(ctx, p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.NextRunAt != nil {
		t.Fatalf("disabled task next_run_at = %d, want none", *task.NextRunAt)
	}

	updated, err := svc.ApplyUpdate(ctx, task.ID, map[string]any{"enabled": 1}, false)
	if err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if reloaded, _ := svc.repo.GetByID(ctx, task.ID); updated.NextRunAt == nil || reloaded.NextRunAt == nil || *reloaded.NextRunAt != *updated.NextRunAt {
		t.Fatalf("next_run_at after enabling = %v / %v, want one written", updated.NextRunAt, reloaded.NextRunAt)
	}
}

func TestOwnsTask(t *testing.T) {
	alice, bob := "alice@example.com", "bob@example.com"
	cases := []struct {
		name string
		task *store.ScheduledTask
		want bool
	}{
		{"namespace-level (nil owner) visible to anyone", &store.ScheduledTask{}, true},
		{"own task", &store.ScheduledTask{UserID: &alice}, true},
		{"another user's task", &store.ScheduledTask{UserID: &bob}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownsTask(tc.task, alice); got != tc.want {
				t.Errorf("ownsTask = %v, want %v", got, tc.want)
			}
		})
	}
}

func containsTaskID(tasks []*store.ScheduledTask, id string) bool {
	for _, t := range tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

func TestApplyUpdate_EnablingResetsFailures(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	p := chatCreateParams("paused")
	p.Enabled = false
	task, err := svc.CreateScheduledTask(ctx, p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err = svc.repo.Update(ctx, task.ID, map[string]any{"consecutive_failure_count": 5}); err != nil {
		t.Fatal(err)
	}

	updated, err := svc.ApplyUpdate(ctx, task.ID, map[string]any{"enabled": 1}, false)
	if err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if updated.ConsecutiveFailureCount != 0 {
		t.Fatalf("consecutive_failure_count = %d, want 0 after enabling", updated.ConsecutiveFailureCount)
	}
}

func TestApplyUpdate_EditOfAnEnabledTaskKeepsItsFailures(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	task, err := svc.CreateScheduledTask(ctx, chatCreateParams("keeps-failures"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err = svc.repo.Update(ctx, task.ID, map[string]any{"consecutive_failure_count": 4}); err != nil {
		t.Fatal(err)
	}

	updated, err := svc.ApplyUpdate(ctx, task.ID, map[string]any{"enabled": 1, "name": "renamed"}, false)
	if err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if updated.ConsecutiveFailureCount != 4 {
		t.Fatalf("consecutive_failure_count = %d, want 4 kept on an edit of an enabled task", updated.ConsecutiveFailureCount)
	}
}
