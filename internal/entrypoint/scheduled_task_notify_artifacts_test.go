// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestBuildNotifyArtifacts(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	owner := "alice@example.com"
	task := &store.ScheduledTask{
		ID: idgen.New(), Name: "artifact-task", Namespace: "test-ns",
		UserID: &owner, CreatedBy: owner,
		ScheduleType: "interval", ScheduleExpression: "300s", Timezone: "UTC",
		TargetAgentName: "ReportAgent", TargetType: "agent",
		TaskMessage: `[{"kind":"text","text":"go"}]`, Source: "ui",
	}
	if err := svc.repo.Create(ctx, task); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	seedExec := func() *store.ScheduledTaskExecution {
		e := &store.ScheduledTaskExecution{
			ID: idgen.New(), ScheduledTaskID: task.ID, Status: "completed", ScheduledFor: 1,
		}
		if err := svc.repo.CreateExecution(ctx, e); err != nil {
			t.Fatalf("seed execution: %v", err)
		}
		return e
	}
	seedArtifact := func(session, filename string) {
		art := svc.core.Services().Artifacts()
		loc := art.Resolve("", owner, session, filename)
		if _, err := art.Inner.Save(ctx, loc, []byte("x"), "text/plain"); err != nil {
			t.Fatalf("seed artifact %s: %v", filename, err)
		}
	}

	t.Run("builds refs with owner/session/filename from the session store", func(t *testing.T) {
		exec := seedExec()
		if err := svc.sessKeyRepo.CreateMapping(ctx, store.GatewayTypeScheduled, exec.ID, "sess-abc"); err != nil {
			t.Fatalf("map session: %v", err)
		}
		seedArtifact("sess-abc", "chart.png")
		seedArtifact("sess-abc", "data.csv")

		arts := svc.buildNotifyArtifacts(ctx, exec, task)
		if len(arts) != 2 {
			t.Fatalf("got %d artifacts, want 2: %+v", len(arts), arts)
		}
		names := map[string]bool{}
		for _, a := range arts {
			if a.AgentName != "" || a.UserID != owner || a.SessionID != "sess-abc" {
				t.Errorf("bad scope on %+v", a)
			}
			names[a.Filename] = true
		}
		if !names["chart.png"] || !names["data.csv"] {
			t.Errorf("missing filenames: %v", names)
		}
	})

	t.Run("no session mapping → nil (gateway delivers text only)", func(t *testing.T) {
		exec := seedExec()
		if got := svc.buildNotifyArtifacts(ctx, exec, task); got != nil {
			t.Errorf("without a session mapping, expected nil, got %+v", got)
		}
	})

	t.Run("session with no artifacts → nil", func(t *testing.T) {
		exec := seedExec()
		if err := svc.sessKeyRepo.CreateMapping(ctx, store.GatewayTypeScheduled, exec.ID, "sess-empty"); err != nil {
			t.Fatalf("map session: %v", err)
		}
		if got := svc.buildNotifyArtifacts(ctx, exec, task); got != nil {
			t.Errorf("an empty session scope should yield nil, got %+v", got)
		}
	})
}
