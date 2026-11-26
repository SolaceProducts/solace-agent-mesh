// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const budgetStopTitle = "identity-test-task stopped by a usage budget"

func noticeSchedule(t *testing.T, gate *budget.Gate, edit func(*store.ScheduledTask)) (*ScheduledTaskService, *notifTestBroker, string) {
	t.Helper()
	svc := passesSvc(t)
	svc.cfg.AutoPauseAfterConsecutiveFailures = 1
	if gate != nil {
		svc.core.SetServices(budgettest.WithGate(svc.core.Services(), gate))
	}
	brk := &notifTestBroker{}
	svc.SetNotifier(NewNotificationService(brk, testLogger()))
	task := dueTask(t, svc, time.Now().Add(-time.Second), edit)
	return svc, brk, task.ID
}

func fireAt(t *testing.T, svc *ScheduledTaskService, taskID string, at time.Time) {
	t.Helper()
	if err := svc.repo.Update(t.Context(), taskID, map[string]any{"next_run_at": at.UnixMilli()}); err != nil {
		t.Fatalf("re-arm task: %v", err)
	}
	mustRunPasses(t, svc, at)
}

func answerSentRuns(t *testing.T, svc *ScheduledTaskService, taskID string, at time.Time, task *a2a.Task, a2aErr *a2a.Error) {
	t.Helper()
	for _, r := range taskRuns(t, svc, taskID) {
		if r.Status == "running" {
			svc.handleAnswer(t.Context(), *r.A2ATaskID, task, a2aErr)
		}
	}
	mustRunPasses(t, svc, at)
}

func inboxNotices(t *testing.T, brk *notifTestBroker) []a2a.NotifyRequest {
	t.Helper()
	var out []a2a.NotifyRequest
	for _, msg := range brk.published {
		var req a2a.NotifyRequest
		if err := json.Unmarshal(msg.payload, &req); err != nil {
			t.Fatalf("payload not a NotifyRequest: %v", err)
		}
		out = append(out, req)
	}
	return out
}

func budgetNotices(t *testing.T, brk *notifTestBroker) []a2a.NotifyRequest {
	t.Helper()
	var out []a2a.NotifyRequest
	for _, req := range inboxNotices(t, brk) {
		if req.Title == budgetStopTitle {
			out = append(out, req)
		}
	}
	return out
}

func budgetStops(t *testing.T, svc *ScheduledTaskService, taskID string) []string {
	t.Helper()
	runs := taskRuns(t, svc, taskID)
	stops := make([]string, len(runs))
	for i, r := range runs {
		if r.BudgetStop != nil {
			stops[len(runs)-1-i] = *r.BudgetStop
		}
	}
	return stops
}

func TestScheduledTask_ABlockedScheduleTellsItsOwnerOnceWhileTheBlockHolds(t *testing.T) {
	svc, brk, taskID := noticeSchedule(t, budgettest.BlockedGate(t, budgetGatePeriodEnd), nil)
	now := time.Now()

	for i := range 3 {
		fireAt(t, svc, taskID, now.Add(time.Duration(i)*time.Second))
	}

	notices := inboxNotices(t, brk)
	if len(notices) != 1 {
		t.Fatalf("notifications = %d, want 1 for three fires under one block", len(notices))
	}
	notice := notices[0]
	if notice.Title != budgetStopTitle || notice.Category != a2a.CategoryScheduledTask {
		t.Errorf("notice = %q in %q, want %q in the scheduled task category", notice.Title, notice.Category, budgetStopTitle)
	}
	if !strings.HasPrefix(notice.Message, "This run was skipped: ") || !strings.Contains(notice.Message, "1 November 2099 00:00 UTC") || !strings.Contains(notice.Message, budgetStreakNote) {
		t.Errorf("notice message = %q, want the skipped run, the reset time and that later runs are skipped quietly", notice.Message)
	}
	if got := budgetStops(t, svc, taskID); strings.Join(got, ",") != "first,repeat,repeat" {
		t.Errorf("budget stops = %v, want first,repeat,repeat", got)
	}
	runs := taskRuns(t, svc, taskID)
	if want := scheduledInboxDedupPrefix + runs[len(runs)-1].ID; notice.Dedup != want {
		t.Errorf("notice dedup = %q, want %q so a resent notice is dropped", notice.Dedup, want)
	}
}

func TestScheduledTask_TheBlockNoticeComesBackAfterARunTheAgentAnswers(t *testing.T) {
	gate := budgettest.BlockedGate(t, budgetGatePeriodEnd)
	svc, brk, taskID := noticeSchedule(t, gate, nil)
	now := time.Now()

	fireAt(t, svc, taskID, now)
	gate.Apply(t.Context(), budgettest.MeshState(false, budgetGatePeriodEnd, time.Now()))
	fireAt(t, svc, taskID, now.Add(time.Second))
	answerSentRuns(t, svc, taskID, now.Add(2*time.Second), finalTask(a2a.TaskStateCompleted, "pong"), nil)
	gate.Apply(t.Context(), budgettest.MeshState(true, budgetGatePeriodEnd, time.Now()))
	fireAt(t, svc, taskID, now.Add(3*time.Second))

	if got := len(budgetNotices(t, brk)); got != 2 {
		t.Errorf("block notices = %d, want one per block", got)
	}
	if got := budgetStops(t, svc, taskID); strings.Join(got, ",") != "first,,first" {
		t.Errorf("budget stops = %v, want the streak to restart after the answered run", got)
	}
}

func TestScheduledTask_ABudgetStopPartwaySendsTheBlockNoticeInsteadOfAFailure(t *testing.T) {
	svc, brk, taskID := noticeSchedule(t, nil, nil)
	now := time.Now()

	fireAt(t, svc, taskID, now)
	answerSentRuns(t, svc, taskID, now.Add(time.Second), nil, budgetStopError(taxonomy.ErrorCategoryBudgetExhausted))
	budgettest.BlockMesh(t, svc.core.BudgetGate(), budgetGatePeriodEnd)
	fireAt(t, svc, taskID, now.Add(2*time.Second))

	notices := inboxNotices(t, brk)
	if len(notices) != 1 || notices[0].Title != budgetStopTitle {
		t.Fatalf("notifications = %+v, want only the block notice: the stopped run began the block", notices)
	}
	if !strings.HasPrefix(notices[0].Message, agentBudgetStopMessage) {
		t.Errorf("notice message = %q, want the agent's stop message first", notices[0].Message)
	}
	if got := budgetStops(t, svc, taskID); strings.Join(got, ",") != "first,repeat" {
		t.Errorf("budget stops = %v, want the stopped run first and the refused fire a repeat", got)
	}
}

func TestScheduledTask_ARunStoppedPartwayAfterTheBlockLiftedSendsANewBlockNotice(t *testing.T) {
	gate := budgettest.BlockedGate(t, budgetGatePeriodEnd)
	svc, brk, taskID := noticeSchedule(t, gate, nil)
	now := time.Now()

	fireAt(t, svc, taskID, now)
	gate.Apply(t.Context(), budgettest.MeshState(false, budgetGatePeriodEnd, time.Now()))
	fireAt(t, svc, taskID, now.Add(time.Second))
	answerSentRuns(t, svc, taskID, now.Add(2*time.Second), nil, budgetStopError(taxonomy.ErrorCategoryBudgetExhausted))

	if got := budgetStops(t, svc, taskID); strings.Join(got, ",") != "first,first" {
		t.Errorf("budget stops = %v, want the run the gate admitted to begin a new block", got)
	}
	if got := len(budgetNotices(t, brk)); got != 2 {
		t.Errorf("block notices = %d, want one for each block", got)
	}
}

func TestScheduledTask_AFailedBlockNoticeIsSentAgainBeforeTheRunIsFinished(t *testing.T) {
	svc, brk, taskID := noticeSchedule(t, budgettest.BlockedGate(t, budgetGatePeriodEnd), nil)
	brk.down = true
	now := time.Now()

	fireAt(t, svc, taskID, now)
	mustRunPasses(t, svc, now.Add(time.Second))
	if run := taskRuns(t, svc, taskID)[0]; run.FinalizedAt != nil {
		t.Fatalf("run finalized at %d with its block notice unsent, want it kept for a resend", *run.FinalizedAt)
	}

	brk.down = false
	mustRunPasses(t, svc, now.Add(budgetNoticeRetryInterval/2))
	if got := len(budgetNotices(t, brk)); got != 0 {
		t.Fatalf("block notices = %d before the retry interval passed, want 0", got)
	}
	mustRunPasses(t, svc, now.Add(2*budgetNoticeRetryInterval))

	if got := len(budgetNotices(t, brk)); got != 1 {
		t.Errorf("block notices = %d, want the resent one", got)
	}
	if run := taskRuns(t, svc, taskID)[0]; run.FinalizedAt == nil {
		t.Error("run unfinalized after its block notice was sent")
	}
}

func TestScheduledTask_ABlockNoticeNoChannelCanSendIsGivenUpAfterTheRetryWindow(t *testing.T) {
	svc, brk, taskID := noticeSchedule(t, budgettest.BlockedGate(t, budgetGatePeriodEnd), nil)
	brk.down = true
	now := time.Now()

	fireAt(t, svc, taskID, now)
	mustRunPasses(t, svc, now.Add(budgetNoticeRetryWindow-time.Second))
	if run := taskRuns(t, svc, taskID)[0]; run.FinalizedAt != nil {
		t.Fatal("run finalized inside the retry window with its block notice unsent")
	}
	mustRunPasses(t, svc, now.Add(budgetNoticeRetryWindow))

	if run := taskRuns(t, svc, taskID)[0]; run.FinalizedAt == nil {
		t.Error("run unfinalized after the retry window, want it finished without the notice")
	}
}

func TestScheduledTask_AnUnconfirmedBudgetStateSendsNoBlockNotice(t *testing.T) {
	svc, brk, taskID := noticeSchedule(t, budgettest.UnconfirmedGate(t), nil)
	withFireTimeout(t, svc, taskID, 3)
	now := time.Now()

	mustRunPasses(t, svc, now)
	mustRunPasses(t, svc, now.Add(4*time.Second))

	runs := taskRuns(t, svc, taskID)
	if len(runs) != 1 || runs[0].Status != "skipped" {
		t.Fatalf("runs = %d, want one skipped after waiting out its timeout", len(runs))
	}
	if runs[0].BudgetStop != nil {
		t.Errorf("budget stop = %q, want none: no budget is spent", *runs[0].BudgetStop)
	}
	if n := len(inboxNotices(t, brk)); n != 0 {
		t.Errorf("notifications = %d, want 0", n)
	}
}

func TestScheduledTask_TheBlockNoticeFollowsTheTasksFailureSetting(t *testing.T) {
	svc, brk, taskID := noticeSchedule(t, budgettest.BlockedGate(t, budgetGatePeriodEnd), func(st *store.ScheduledTask) {
		st.NotificationConfig = new(`{"on_success":true,"on_failure":false,"channels":[{"type":"inbox"}]}`)
	})

	fireAt(t, svc, taskID, time.Now())

	if n := len(inboxNotices(t, brk)); n != 0 {
		t.Errorf("notifications = %d, want 0 for a task that turned off failure notifications", n)
	}
}

func TestScheduledTask_RunNowIsRefusedOnlyWhileTheOwnersBudgetIsSpent(t *testing.T) {
	gate := budgettest.UserBlockedGate(t, budgetGatePeriodEnd, budget.UserKey("", "alice"))
	svc, _, taskID := noticeSchedule(t, gate, nil)
	fireAt(t, svc, taskID, time.Now())

	_, err := svc.TriggerTaskNow(t.Context(), taskID, "alice")
	refusal, spent := BudgetRefusal(err)
	if !spent {
		t.Fatalf("run now err = %v, want a spent-budget refusal", err)
	}
	if text := ScheduledRunRefusedText(refusal, true); !strings.HasPrefix(text, "This task can't run now: your usage budget is used up until 1 November 2099 00:00 UTC") {
		t.Errorf("refusal text = %q, want the owner's spent budget and its reset time", text)
	}
	if text := ScheduledRunRefusedText(refusal, false); !strings.HasPrefix(text, "This task can't run now: the usage budget is used up until 1 November 2099 00:00 UTC for this task's owner") {
		t.Errorf("refusal text to another caller = %q, want the budget named as the owner's", text)
	}
	if runs := taskRuns(t, svc, taskID); len(runs) != 1 {
		t.Fatalf("runs = %d, want only the refused fire: a refused run now records nothing", len(runs))
	}

	gate.Apply(t.Context(), budgettest.MeshState(false, budgetGatePeriodEnd, time.Now()))
	if _, err := svc.TriggerTaskNow(t.Context(), taskID, "alice"); err != nil {
		t.Fatalf("run now after the block lifted = %v, want accepted while the schedule's streak is still open", err)
	}
}

func TestScheduledTask_RunNowChecksTheOwnersPlatformUserNotTheCaller(t *testing.T) {
	const alice, bob = "0190d6a4-1f3c-7b6e-8a52-6c1d9e0f4a21", "0190d6a4-1f3c-7b6e-8a52-6c1d9e0f4a22"
	gate := budgettest.UserBlockedGate(t, budgetGatePeriodEnd, budget.UserKey(alice, ""))
	svc, _, taskID := noticeSchedule(t, gate, nil)
	svc.core.SetIdentityResolver(&stubIdentityResolver{ident: auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice", PlatformUserID: alice, Scopes: []string{"agent:test-agent:invoke"}, Authenticated: true,
	})})

	if _, err := svc.TriggerTaskNow(t.Context(), taskID, "bob"); !IsBudgetRefusal(err) {
		t.Fatalf("run now = %v, want refused for the owner's spent budget whoever asks", err)
	}
	fireAt(t, svc, taskID, time.Now())
	if got := budgetStops(t, svc, taskID); strings.Join(got, ",") != "first" {
		t.Errorf("budget stops = %v, want the pass refusing the fire under the same key", got)
	}

	gate.Apply(t.Context(), budgettest.UserBlockedState(budgetGatePeriodEnd, time.Now(), budget.UserKey(bob, "")))
	if _, err := svc.TriggerTaskNow(t.Context(), taskID, "bob"); err != nil {
		t.Errorf("run now while only the caller's budget is spent = %v, want accepted", err)
	}
}

func TestScheduledTask_RunNowLeavesAnUnconfirmedBudgetStateToThePass(t *testing.T) {
	svc, _, taskID := noticeSchedule(t, budgettest.UnconfirmedGate(t), nil)

	if _, err := svc.TriggerTaskNow(t.Context(), taskID, "alice"); err != nil {
		t.Errorf("run now while the budget state is unconfirmed = %v, want accepted for the pass to wait out", err)
	}
}
