// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestNotificationConfigIncludesArtifacts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty string", "", false},
		{"unparseable", "not json", false},
		{"flag true", `{"include_artifacts":true}`, true},
		{"flag false", `{"include_artifacts":false}`, false},
		{"flag absent", `{"channels":[{"type":"inbox"}]}`, false},
		{"the default", defaultNotificationConfig, true},
	}
	for _, c := range cases {
		if got := notificationConfigIncludesArtifacts(c.in); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func seedFinishedRun(t *testing.T, svc *ScheduledTaskService, source string, notificationConfig *string, userID *string, createdBy, status string) (*store.ScheduledTask, *store.ScheduledTaskExecution) {
	t.Helper()
	task := &store.ScheduledTask{
		ID: idgen.New(), Name: "report-task", Namespace: "test-ns",
		UserID: userID, CreatedBy: createdBy,
		ScheduleType: "interval", ScheduleExpression: "300s", Timezone: "UTC",
		TargetAgentName: "ReportAgent", TargetType: "agent",
		TaskMessage: `[{"kind":"text","text":"go"}]`, Source: source,
		NotificationConfig: notificationConfig,
	}
	if err := svc.repo.Create(t.Context(), task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	exec := &store.ScheduledTaskExecution{ID: idgen.New(), ScheduledTaskID: task.ID, Status: status, ScheduledFor: 1}
	if err := svc.repo.CreateExecution(t.Context(), exec); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	return task, exec
}

func TestDispatchNotifications_ConfigGate(t *testing.T) {
	alice, bob := "alice@example.com", "bob@example.com"
	cases := []struct {
		name       string
		source     string
		config     *string
		userID     *string
		createdBy  string
		status     string
		wantNotify int
	}{
		{"no config from the web UI", "ui", nil, &alice, alice, "completed", 1},
		{"no config from a workflow", "workflow", nil, &alice, alice, "completed", 1},
		{"no config, failed", "ui", nil, &alice, alice, "failed", 1},
		{"empty config", "ui", strPtr(""), &alice, alice, "completed", 1},
		{"no config, shared task tells both owners", "ui", nil, &alice, bob, "completed", 2},
		{"no config, cancelled", "ui", nil, &alice, alice, "cancelled", 0},
		{"explicit config with both triggers off", "ui", strPtr(`{"on_success":false,"on_failure":false,"channels":[]}`), &alice, alice, "completed", 0},
		{"explicit config with the inbox off", "ui", strPtr(`{"on_success":true,"channels":[{"type":"sse"}]}`), &alice, alice, "completed", 0},
		{"unparseable stored config stays silent", "ui", strPtr("not json"), &alice, alice, "completed", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := newSchedulerSvcForTest(t, 0)
			brk := &notifTestBroker{}
			svc.SetNotifier(NewNotificationService(brk, testLogger()))
			task, exec := seedFinishedRun(t, svc, c.source, c.config, c.userID, c.createdBy, c.status)

			svc.dispatchNotifications(t.Context(), exec.ID, "", task, time.Now())

			if len(brk.published) != c.wantNotify {
				t.Fatalf("published %d NotifyRequests, want %d", len(brk.published), c.wantNotify)
			}
			reloaded, err := svc.repo.GetExecutionByID(t.Context(), exec.ID)
			if err != nil || reloaded == nil {
				t.Fatalf("reload execution: %v", err)
			}
			if c.wantNotify == 0 {
				return
			}
			if reloaded.NotificationsSent == nil {
				t.Fatal("execution records no notification")
			}
			var records []notificationRecord
			if err := json.Unmarshal([]byte(*reloaded.NotificationsSent), &records); err != nil {
				t.Fatalf("notifications_sent is not a record list: %v", err)
			}
			if len(records) != 1 || records[0].Type != "inbox" || records[0].Status != "sent" {
				t.Fatalf("notifications_sent = %+v, want one sent inbox record", records)
			}
		})
	}
}

func TestDispatchNotifications_ArtifactDelivery(t *testing.T) {
	alice := "alice@example.com"
	stored := defaultNotificationConfig
	textOnly := `{"on_success":true,"on_failure":true,"channels":[{"type":"inbox"}]}`
	cases := []struct {
		name         string
		source       string
		config       *string
		status       string
		wantArtifact int
	}{
		{"no config delivers the files", "ui", nil, "completed", 1},
		{"the stored default delivers the files", "chat", &stored, "completed", 1},
		{"a failed run delivers the files", "ui", nil, "failed", 1},
		{"a timed-out run delivers the files", "ui", nil, "timeout", 1},
		{"a config that does not ask for files delivers none", "ui", &textOnly, "completed", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := newSchedulerSvcForTest(t, 0)
			brk := &notifTestBroker{}
			svc.SetNotifier(NewNotificationService(brk, testLogger()))
			task, exec := seedFinishedRun(t, svc, c.source, c.config, &alice, alice, c.status)
			if err := svc.sessKeyRepo.CreateMapping(t.Context(), store.GatewayTypeScheduled, exec.ID, "sess-1"); err != nil {
				t.Fatalf("map session: %v", err)
			}
			art := svc.core.Services().Artifacts()
			if _, err := art.Inner.Save(t.Context(), art.Resolve("", alice, "sess-1", "chart.png"), []byte("x"), "text/plain"); err != nil {
				t.Fatalf("seed artifact: %v", err)
			}

			svc.dispatchNotifications(t.Context(), exec.ID, "sess-1", task, time.Now())

			if len(brk.published) != 1 {
				t.Fatalf("published %d NotifyRequests, want 1", len(brk.published))
			}
			var req a2a.NotifyRequest
			if err := json.Unmarshal(brk.published[0].payload, &req); err != nil {
				t.Fatalf("payload not a NotifyRequest: %v", err)
			}
			if len(req.Artifacts) != c.wantArtifact {
				t.Errorf("got %d artifacts on the wire, want %d", len(req.Artifacts), c.wantArtifact)
			}
		})
	}
}

func TestIsNotifiableStatus(t *testing.T) {
	for status, want := range map[string]bool{"completed": true, "failed": true, "timeout": true, "cancelled": false, "skipped": false, "running": false, "": false} {
		if got := isNotifiableStatus(status); got != want {
			t.Errorf("isNotifiableStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

type listSpy struct {
	artifact.Service
	calls int
}

func (l *listSpy) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	l.calls++
	return l.Service.ListKeys(ctx, scope)
}

func TestDispatchNotifications_ResolvesFilesOnlyForARunThatCanNotify(t *testing.T) {
	alice := "alice@example.com"
	for status, wantListings := range map[string]int{"cancelled": 0, "skipped": 0, "completed": 1, "failed": 1, "timeout": 1} {
		t.Run(status, func(t *testing.T) {
			spy := &listSpy{Service: artmem.New()}
			svc := newSchedulerSvcWithArtifactStore(t, 0, spy)
			svc.SetNotifier(NewNotificationService(&notifTestBroker{}, testLogger()))
			task, exec := seedFinishedRun(t, svc, "ui", nil, &alice, alice, status)
			if err := svc.sessKeyRepo.CreateMapping(t.Context(), store.GatewayTypeScheduled, exec.ID, "sess-1"); err != nil {
				t.Fatalf("map session: %v", err)
			}

			svc.dispatchNotifications(t.Context(), exec.ID, "sess-1", task, time.Now())

			if spy.calls != wantListings {
				t.Errorf("listed the session %d times, want %d", spy.calls, wantListings)
			}
		})
	}
}

func TestBuildNotifyArtifacts_SkipsInternal(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	owner := "alice@example.com"
	task := &store.ScheduledTask{
		ID: idgen.New(), Name: "internal-skip-task", Namespace: "test-ns",
		UserID: &owner, CreatedBy: owner,
		ScheduleType: "interval", ScheduleExpression: "300s", Timezone: "UTC",
		TargetAgentName: "ReportAgent", TargetType: "agent",
		TaskMessage: `[{"kind":"text","text":"go"}]`, Source: "ui",
	}
	if err := svc.repo.Create(ctx, task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	exec := &store.ScheduledTaskExecution{
		ID: idgen.New(), ScheduledTaskID: task.ID, Status: "completed", ScheduledFor: 1,
	}
	if err := svc.repo.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("seed execution: %v", err)
	}
	if err := svc.sessKeyRepo.CreateMapping(ctx, store.GatewayTypeScheduled, exec.ID, "sess-1"); err != nil {
		t.Fatalf("map session: %v", err)
	}
	art := svc.core.Services().Artifacts()
	for _, name := range []string{"chart.png", "chart.png" + artifact.MetadataSuffix} {
		if _, err := art.Inner.Save(ctx, art.Resolve("", owner, "sess-1", name), []byte("x"), "text/plain"); err != nil {
			t.Fatalf("seed artifact %s: %v", name, err)
		}
	}

	arts := svc.buildNotifyArtifacts(ctx, exec, task)
	if len(arts) != 1 || arts[0].Filename != "chart.png" {
		t.Fatalf("internal/companion artifacts must be filtered out, got %+v", arts)
	}
}
