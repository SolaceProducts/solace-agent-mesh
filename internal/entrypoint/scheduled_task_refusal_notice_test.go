// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const refusalNoticeTimeout = 10 * time.Minute

type refusalNoticeFixture struct {
	*runAsFixture
	brk    *notifTestBroker
	taskID string
	start  time.Time
}

func newRefusalNoticeFixture(t *testing.T, flagOn bool, resolver IdentityResolver) *refusalNoticeFixture {
	t.Helper()
	f := newRunAsFixture(t, flagOn, resolver)
	brk := &notifTestBroker{}
	f.svc.SetNotifier(NewNotificationService(brk, testLogger()))
	task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.TimeoutSeconds = 60 })
	return &refusalNoticeFixture{runAsFixture: f, brk: brk, taskID: task.ID, start: time.Now()}
}

func (f *refusalNoticeFixture) fire(t *testing.T, offset time.Duration) {
	t.Helper()
	fireAt(t, f.svc, f.taskID, f.start.Add(offset))
}

func (f *refusalNoticeFixture) runStatuses(t *testing.T) []string {
	t.Helper()
	runs := taskRuns(t, f.svc, f.taskID)
	statuses := make([]string, len(runs))
	for i, run := range runs {
		statuses[len(runs)-1-i] = run.Status
	}
	return statuses
}

func (f *refusalNoticeFixture) failureNotices(t *testing.T) []string {
	t.Helper()
	var messages []string
	for _, notice := range inboxNotices(t, f.brk) {
		if notice.Metadata["status"] == "failed" {
			messages = append(messages, notice.Message)
		}
	}
	return messages
}

func (f *refusalNoticeFixture) setNotificationConfig(t *testing.T, cfg string) {
	t.Helper()
	if err := f.svc.repo.Update(t.Context(), f.taskID, map[string]any{"notification_config": cfg}); err != nil {
		t.Fatalf("set notification config: %v", err)
	}
}

func (f *refusalNoticeFixture) noticedTriggerTypes(t *testing.T) []string {
	t.Helper()
	triggers := map[string]string{}
	for _, run := range taskRuns(t, f.svc, f.taskID) {
		triggers[run.ID] = run.TriggerType
	}
	var kinds []string
	for _, notice := range inboxNotices(t, f.brk) {
		if notice.Metadata["status"] == "failed" {
			kinds = append(kinds, triggers[notice.Metadata["execution_id"].(string)])
		}
	}
	return kinds
}

func (f *refusalNoticeFixture) assertNothingLeftToFinish(t *testing.T) {
	t.Helper()
	open, err := f.svc.repo.FindUnfinalizedExecutions(t.Context(), f.svc.namespace())
	if err != nil {
		t.Fatalf("FindUnfinalizedExecutions(%s): %v", f.svc.namespace(), err)
	}
	if len(open) != 0 {
		t.Errorf("%d runs are still waiting to be finished, want none: a quiet refusal must still be finalized", len(open))
	}
}

func TestScheduledRunAs_ARefusedStreakNotifiesTheOwnerOnceAndNeverPausesTheTask(t *testing.T) {
	f := newRefusalNoticeFixture(t, false, newRolloutResolver(0))
	f.svc.cfg.AutoPauseAfterConsecutiveFailures = 1

	for i := range 3 {
		f.fire(t, time.Duration(i)*time.Minute)
	}
	mustRunPasses(t, f.svc, f.start.Add(time.Hour))

	if diff := cmp.Diff([]string{"failed", "failed", "failed"}, f.runStatuses(t)); diff != "" {
		t.Errorf("run statuses mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{runAsDisabledMessage}, f.failureNotices(t)); diff != "" {
		t.Errorf("failure notices mismatch (-want +got):\n%s", diff)
	}
	f.assertUncounted(t, f.taskID)
	f.assertNothingLeftToFinish(t)
}

func TestScheduledRunAs_AnyOtherOutcomeBetweenRefusalsStartsANewStreak(t *testing.T) {
	cases := []struct {
		name         string
		between      func(t *testing.T, f *refusalNoticeFixture, dir entitlementDirectory)
		wantStatuses []string
		wantNotices  []string
	}{
		{
			name: "a counted failure",
			between: func(t *testing.T, f *refusalNoticeFixture, dir entitlementDirectory) {
				delete(dir, runAsPrincipal)
				f.setFlag(true)
				f.fire(t, time.Minute)
			},
			wantStatuses: []string{"failed", "failed", "failed"},
			wantNotices:  []string{runAsDisabledMessage, runAsUnknownPrincipalMessage, runAsDisabledMessage},
		},
		{
			name: "a completed run",
			between: func(t *testing.T, f *refusalNoticeFixture, _ entitlementDirectory) {
				f.setFlag(true)
				f.fire(t, time.Minute)
				answerSentRuns(t, f.svc, f.taskID, f.start.Add(2*time.Minute), finalTask(a2a.TaskStateCompleted, "pong"), nil)
			},
			wantStatuses: []string{"failed", "completed", "failed"},
			wantNotices:  []string{runAsDisabledMessage, runAsDisabledMessage},
		},
		{
			name: "a run that timed out",
			between: func(t *testing.T, f *refusalNoticeFixture, _ entitlementDirectory) {
				f.setFlag(true)
				f.fire(t, time.Minute)
				mustRunPasses(t, f.svc, f.start.Add(refusalNoticeTimeout))
			},
			wantStatuses: []string{"failed", "timeout", "failed"},
			wantNotices:  []string{runAsDisabledMessage, runAsDisabledMessage},
		},
		{
			name: "a fire skipped for being late",
			between: func(t *testing.T, f *refusalNoticeFixture, _ entitlementDirectory) {
				f.svc.cfg.MisfireGraceTimeSeconds = 60
				if err := f.svc.repo.Update(t.Context(), f.taskID, map[string]any{"next_run_at": f.start.Add(time.Minute).UnixMilli()}); err != nil {
					t.Fatalf("re-arm task: %v", err)
				}
				mustRunPasses(t, f.svc, f.start.Add(5*time.Minute))
			},
			wantStatuses: []string{"failed", "skipped", "failed"},
			wantNotices:  []string{runAsDisabledMessage, runAsDisabledMessage},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := superAdminDirectory()
			f := newRefusalNoticeFixture(t, false, &directoryResolver{dir: dir})
			f.svc.cfg.AutoPauseAfterConsecutiveFailures = 10

			f.fire(t, 0)
			tc.between(t, f, dir)
			f.setFlag(false)
			f.fire(t, 30*time.Minute)

			if diff := cmp.Diff(tc.wantStatuses, f.runStatuses(t)); diff != "" {
				t.Errorf("run statuses mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantNotices, f.failureNotices(t)); diff != "" {
				t.Errorf("failure notices mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestScheduledRunAs_ARefusalWithADifferentMessageNotifiesAgain(t *testing.T) {
	f := newRefusalNoticeFixture(t, false, newRolloutResolver(1000))

	f.fire(t, 0)
	f.setFlag(true)
	f.fire(t, time.Minute)
	mustRunPasses(t, f.svc, f.start.Add(refusalNoticeTimeout))

	if diff := cmp.Diff([]string{"failed", "failed"}, f.runStatuses(t)); diff != "" {
		t.Errorf("run statuses mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{runAsDisabledMessage, runAsPlatformTooOldMessage}, f.failureNotices(t)); diff != "" {
		t.Errorf("failure notices mismatch (-want +got):\n%s", diff)
	}
}

func TestScheduledRunAs_AnOlderPlatformNotifiesOnceAcrossRepeatedRefusals(t *testing.T) {
	f := newRefusalNoticeFixture(t, true, newRolloutResolver(1000))

	f.fire(t, 0)
	mustRunPasses(t, f.svc, f.start.Add(refusalNoticeTimeout))
	f.fire(t, refusalNoticeTimeout+time.Minute)
	mustRunPasses(t, f.svc, f.start.Add(2*refusalNoticeTimeout))

	if diff := cmp.Diff([]string{"failed", "failed"}, f.runStatuses(t)); diff != "" {
		t.Errorf("run statuses mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{runAsPlatformTooOldMessage}, f.failureNotices(t)); diff != "" {
		t.Errorf("failure notices mismatch (-want +got):\n%s", diff)
	}
	f.assertUncounted(t, f.taskID)
	f.assertNothingLeftToFinish(t)
}

func TestScheduledRunAs_ARepeatedCountedFailureStillNotifiesEveryTime(t *testing.T) {
	dir := superAdminDirectory()
	delete(dir, runAsPrincipal)
	f := newRefusalNoticeFixture(t, true, &directoryResolver{dir: dir})
	f.svc.cfg.AutoPauseAfterConsecutiveFailures = 10

	f.fire(t, 0)
	f.fire(t, time.Minute)

	if diff := cmp.Diff([]string{unknownSystemUserText, unknownSystemUserText}, f.failureNotices(t)); diff != "" {
		t.Errorf("failure notices mismatch (-want +got):\n%s", diff)
	}
	if stored := reloadTask(t, f.svc, f.taskID); stored.ConsecutiveFailureCount != 2 {
		t.Errorf("consecutive failures = %d, want both failures counted", stored.ConsecutiveFailureCount)
	}
}

func TestScheduledRunAs_FiresSkippedWhileARefusalWaitsDoNotBreakItsStreak(t *testing.T) {
	f := newRefusalNoticeFixture(t, true, newRolloutResolver(1000))

	for _, offset := range []time.Duration{0, 20 * time.Second, 40 * time.Second} {
		f.fire(t, offset)
	}
	mustRunPasses(t, f.svc, f.start.Add(90*time.Second))
	for _, offset := range []time.Duration{2 * time.Minute, 2*time.Minute + 20*time.Second, 2*time.Minute + 40*time.Second} {
		f.fire(t, offset)
	}
	mustRunPasses(t, f.svc, f.start.Add(4*time.Minute))

	wantStatuses := []string{"failed", "skipped", "skipped", "failed", "skipped", "skipped"}
	if diff := cmp.Diff(wantStatuses, f.runStatuses(t)); diff != "" {
		t.Errorf("run statuses mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{runAsPlatformTooOldMessage}, f.failureNotices(t)); diff != "" {
		t.Errorf("failure notices mismatch (-want +got):\n%s", diff)
	}
}

func TestScheduledRunAs_ARefusalFollowsAFirstNoticeThatWasNotDelivered(t *testing.T) {
	const onFailureOff = `{"on_success":true,"on_failure":false,"include_artifacts":true,"channels":[{"type":"inbox"}]}`
	const onFailureOn = `{"on_success":true,"on_failure":true,"include_artifacts":true,"channels":[{"type":"inbox"}]}`
	const noChannels = `{"on_success":true,"on_failure":true,"include_artifacts":true,"channels":[]}`
	cases := []struct {
		name   string
		before func(t *testing.T, f *refusalNoticeFixture)
		repair func(t *testing.T, f *refusalNoticeFixture)
	}{
		{
			name:   "the inbox channel failed",
			before: func(_ *testing.T, f *refusalNoticeFixture) { f.brk.down = true },
			repair: func(_ *testing.T, f *refusalNoticeFixture) { f.brk.down = false },
		},
		{
			name:   "failure notices were switched off",
			before: func(t *testing.T, f *refusalNoticeFixture) { f.setNotificationConfig(t, onFailureOff) },
			repair: func(t *testing.T, f *refusalNoticeFixture) { f.setNotificationConfig(t, onFailureOn) },
		},
		{
			name:   "no channel was configured",
			before: func(t *testing.T, f *refusalNoticeFixture) { f.setNotificationConfig(t, noChannels) },
			repair: func(t *testing.T, f *refusalNoticeFixture) { f.setNotificationConfig(t, onFailureOn) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefusalNoticeFixture(t, false, newRolloutResolver(0))

			tc.before(t, f)
			f.fire(t, 0)
			tc.repair(t, f)
			f.fire(t, time.Minute)
			f.fire(t, 2*time.Minute)

			if diff := cmp.Diff([]string{"failed", "failed", "failed"}, f.runStatuses(t)); diff != "" {
				t.Errorf("run statuses mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{runAsDisabledMessage}, f.failureNotices(t)); diff != "" {
				t.Errorf("failure notices mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestScheduledRunAs_AManualRunOnARefusedTaskAlwaysNotifiesAndLeavesTheStreakAlone(t *testing.T) {
	cases := []struct {
		name            string
		before          []time.Duration
		after           []time.Duration
		wantNoticeKinds []string
	}{
		{"inside a streak", []time.Duration{-time.Hour}, []time.Duration{time.Hour}, []string{store.TriggerTypeScheduled, store.TriggerTypeManual}},
		{"before any scheduled refusal", nil, []time.Duration{time.Hour, 2 * time.Hour}, []string{store.TriggerTypeManual, store.TriggerTypeScheduled}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefusalNoticeFixture(t, false, newRolloutResolver(0))

			for _, offset := range tc.before {
				f.fire(t, offset)
			}
			if _, err := f.svc.TriggerTaskNow(t.Context(), f.taskID, "alice"); err != nil {
				t.Fatalf("TriggerTaskNow(%s): %v", f.taskID, err)
			}
			mustRunPasses(t, f.svc, f.start.Add(time.Minute))
			for _, offset := range tc.after {
				f.fire(t, offset)
			}

			if diff := cmp.Diff(tc.wantNoticeKinds, f.noticedTriggerTypes(t)); diff != "" {
				t.Errorf("trigger types of the notified runs mismatch (-want +got):\n%s", diff)
			}
			f.assertUncounted(t, f.taskID)
		})
	}
}
