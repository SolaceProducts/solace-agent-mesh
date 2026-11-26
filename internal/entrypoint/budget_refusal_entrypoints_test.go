// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func blockedSchedulerFixture(t *testing.T) (*ScheduledTaskService, string) {
	t.Helper()
	return gatedSchedulerFixture(t, budgettest.BlockedGate(t, budgetGatePeriodEnd))
}

func gatedSchedulerFixture(t *testing.T, gate *budget.Gate) (*ScheduledTaskService, string) {
	t.Helper()
	svc := passesSvc(t)
	svc.cfg.AutoPauseAfterConsecutiveFailures = 1
	svc.core.SetServices(budgettest.WithGate(svc.core.Services(), gate))
	task := dueTask(t, svc, time.Now().Add(-time.Second), func(st *store.ScheduledTask) {
		st.MaxRetries = 2
		st.RetryDelaySeconds = 1
	})
	return svc, task.ID
}

func TestScheduledTask_ASpentMeshBudgetSkipsTheFireOnceWithTheResetTime(t *testing.T) {
	svc, taskID := blockedSchedulerFixture(t)
	now := time.Now()

	mustRunPasses(t, svc, now)
	mustRunPasses(t, svc, now.Add(2*time.Second))

	execs := taskRuns(t, svc, taskID)
	if len(execs) != 1 {
		t.Fatalf("executions = %d, want 1: a budget refusal is not retried", len(execs))
	}
	if execs[0].Status != "skipped" {
		t.Errorf("execution status = %q, want skipped", execs[0].Status)
	}
	if execs[0].ErrorMessage == nil || !strings.Contains(*execs[0].ErrorMessage, "1 November 2099 00:00 UTC") {
		t.Errorf("error message = %v, want it to say when the budget resets", execs[0].ErrorMessage)
	}
	if execs[0].CompletedAt == nil {
		t.Error("a skipped execution should be stamped completed")
	}
}

func TestScheduledTask_BudgetRefusalsNeitherCountAsFailuresNorAutoPause(t *testing.T) {
	svc, taskID := blockedSchedulerFixture(t)
	now := time.Now()

	for i := range 2 {
		at := now.Add(time.Duration(i) * time.Second)
		if err := svc.repo.Update(t.Context(), taskID, map[string]any{"next_run_at": at.UnixMilli()}); err != nil {
			t.Fatalf("re-arm task: %v", err)
		}
		mustRunPasses(t, svc, at)
	}

	if execs := taskRuns(t, svc, taskID); len(execs) != 2 {
		t.Fatalf("executions = %d, want one per fire", len(execs))
	}
	got := reloadTask(t, svc, taskID)
	if got.ConsecutiveFailureCount != 0 {
		t.Errorf("consecutive_failure_count = %d, want 0", got.ConsecutiveFailureCount)
	}
	if !got.Enabled {
		t.Error("a schedule refused for budget was auto-paused at a threshold of 1")
	}
	if got.NextRunAt == nil {
		t.Error("a schedule refused for budget lost its next run")
	}
}

func TestScheduledTask_AnOwnersSpentBudgetSkipsEachFireWithoutAutoPause(t *testing.T) {
	svc, taskID := gatedSchedulerFixture(t, budgettest.UserBlockedGate(t, budgetGatePeriodEnd, budget.UserKey("", "alice")))
	now := time.Now()

	for i := range 2 {
		at := now.Add(time.Duration(i) * time.Second)
		if err := svc.repo.Update(t.Context(), taskID, map[string]any{"next_run_at": at.UnixMilli()}); err != nil {
			t.Fatalf("re-arm task: %v", err)
		}
		mustRunPasses(t, svc, at)
	}

	execs := taskRuns(t, svc, taskID)
	if len(execs) != 2 {
		t.Fatalf("executions = %d, want one per fire and no retries", len(execs))
	}
	for _, exec := range execs {
		if exec.Status != "skipped" || exec.ErrorMessage == nil || !strings.Contains(*exec.ErrorMessage, "your usage budget") {
			t.Errorf("execution = %s %v, want skipped naming the owner's own budget", exec.Status, exec.ErrorMessage)
		}
	}
	got := reloadTask(t, svc, taskID)
	if got.ConsecutiveFailureCount != 0 || !got.Enabled {
		t.Errorf("schedule = failures %d, enabled %v; want the refusals neither counted nor pausing it", got.ConsecutiveFailureCount, got.Enabled)
	}
}

type countingMonitorGateway struct {
	*GatewayCore
	attempts atomic.Int32
}

func (c *countingMonitorGateway) SubmitTaskAttributedTo(ctx context.Context, agentName, taskID string, message a2a.Message, caller *auth.Subject, attributedUserID string, opts ...SubmitOption) error {
	c.attempts.Add(1)
	return c.GatewayCore.SubmitTaskAttributedTo(ctx, agentName, taskID, message, caller, attributedUserID, opts...)
}

func startedCoreWithGate(t *testing.T, gate *budget.Gate) *GatewayCore {
	t.Helper()
	core, _, _ := setupTestGateway(t)
	core.SetServices(budgettest.WithGate(core.Services(), gate))
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return core
}

func TestSentinel_ABudgetRefusalHoldsTheCooldownSoRepeatTriggersDoNotResubmit(t *testing.T) {
	gate := budgettest.BlockedGate(t, budgetGatePeriodEnd)
	gw := &countingMonitorGateway{GatewayCore: startedCoreWithGate(t, gate)}
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		_ = gw.Tasks().Register(id, "FlakyAgent", "alice", TaskIdentity{})
	}
	cfg := defaultMonitorCfg()
	cfg.MaxConcurrent = 10
	s := newTestMonitor(t, cfg, gw)
	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()
	s.HandleTaskComplete("task-2", nil, &a2a.Error{Message: "boom again"})
	s.wg.Wait()
	if n := gw.attempts.Load(); n != 1 {
		t.Fatalf("submit attempts within the cooldown = %d, want 1", n)
	}

	gate.Apply(t.Context(), budgettest.MeshState(false, budgetGatePeriodEnd, time.Now().Add(time.Second)))
	s.now = func() time.Time { return base.Add(time.Duration(cfg.CooldownSeconds+1) * time.Second) }
	s.HandleTaskComplete("task-3", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()
	if n := gw.attempts.Load(); n != 2 {
		t.Errorf("submit attempts after the cooldown = %d, want 2", n)
	}
}

func withFireTimeout(t *testing.T, svc *ScheduledTaskService, taskID string, seconds int) {
	t.Helper()
	if err := svc.repo.Update(t.Context(), taskID, map[string]any{"timeout_seconds": seconds}); err != nil {
		t.Fatalf("set timeout: %v", err)
	}
}

func TestScheduledTask_AFireWaitsForTheBudgetStateToBeConfirmed(t *testing.T) {
	gate := budgettest.UnconfirmedGate(t)
	svc, taskID := gatedSchedulerFixture(t, gate)
	withFireTimeout(t, svc, taskID, 3)
	now := time.Now()

	mustRunPasses(t, svc, now)
	if execs := taskRuns(t, svc, taskID); len(execs) != 1 || execs[0].Status != "pending" {
		t.Fatalf("executions = %+v, want the fire waiting while the budget state is unconfirmed", execs)
	}
	gate.Apply(t.Context(), budgettest.MeshState(false, time.Now().Add(time.Hour), time.Now()))
	mustRunPasses(t, svc, now.Add(time.Second))

	execs := taskRuns(t, svc, taskID)
	if len(execs) != 1 || execs[0].Status != "running" || execs[0].SubmittedAt == nil {
		t.Errorf("executions = %+v, want the fire sent once the budget state was confirmed", execs)
	}
}

func TestScheduledTask_AnUnconfirmedBudgetStateSkipsTheFireWithoutCountingIt(t *testing.T) {
	svc, taskID := gatedSchedulerFixture(t, budgettest.UnconfirmedGate(t))
	withFireTimeout(t, svc, taskID, 2)
	now := time.Now()

	mustRunPasses(t, svc, now)
	mustRunPasses(t, svc, now.Add(3*time.Second))

	execs := taskRuns(t, svc, taskID)
	if len(execs) != 1 || execs[0].Status != "skipped" {
		t.Fatalf("executions = %+v, want one skipped", execs)
	}
	if execs[0].ErrorMessage == nil || *execs[0].ErrorMessage != BudgetUnconfirmedText {
		t.Errorf("error message = %v, want %q", execs[0].ErrorMessage, BudgetUnconfirmedText)
	}
	if stored := reloadTask(t, svc, taskID); stored.ConsecutiveFailureCount != 0 {
		t.Errorf("consecutive failures = %d, want an unconfirmed state not counted", stored.ConsecutiveFailureCount)
	}
}

func TestScheduledTask_AFireWaitingForTheBudgetStateResolvesItsOwnerOnceSent(t *testing.T) {
	gate := budgettest.UnconfirmedGate(t)
	svc, taskID := gatedSchedulerFixture(t, gate)
	resolver := &stubIdentityResolver{ident: auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice", Scopes: []string{"agent:test-agent:invoke"}, Authenticated: true,
	})}
	svc.core.SetIdentityResolver(resolver)
	now := time.Now()

	for i := range 3 {
		mustRunPasses(t, svc, now.Add(time.Duration(i)*time.Second))
	}
	if got := resolver.callCount(); got != 0 {
		t.Fatalf("owner resolved %d times while the budget state was unconfirmed, want 0", got)
	}
	gate.Apply(t.Context(), budgettest.MeshState(false, time.Now().Add(time.Hour), time.Now()))
	mustRunPasses(t, svc, now.Add(3*time.Second))

	if got := resolver.callCount(); got != 1 {
		t.Errorf("owner resolved %d times, want once for the send", got)
	}
	execs := taskRuns(t, svc, taskID)
	if len(execs) != 1 || execs[0].Status != "running" || execs[0].ErrorMessage != nil {
		t.Errorf("executions = %+v, want one running run with the wait note cleared", execs)
	}
}

func TestScheduledTask_ARunMissedWhileTheBudgetStateIsUnconfirmedIsNotBlamedOnTheBudget(t *testing.T) {
	svc, taskID := gatedSchedulerFixture(t, budgettest.UnconfirmedGate(t))
	task := reloadTask(t, svc, taskID)
	now := time.Now()
	missed := svc.pendingRun(task, now.Add(-2*time.Hour), 1, store.TriggerTypeScheduled, nil)
	if err := svc.repo.CreateExecution(t.Context(), missed); err != nil {
		t.Fatal(err)
	}

	mustRunPasses(t, svc, now)

	got, err := svc.repo.GetExecutionByID(t.Context(), missed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "timeout" || got.ErrorMessage == nil || *got.ErrorMessage != neverSentMessage {
		t.Errorf("run = {status %q, error %v}, want timeout, not sent before its timeout", got.Status, got.ErrorMessage)
	}
}
