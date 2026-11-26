// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func eventTypes(n *fakeUserNotifier, userID string) []string {
	var types []string
	for _, c := range n.Calls() {
		if c.UserID == userID {
			types = append(types, c.EventType)
		}
	}
	return types
}

func TestScheduledRun_LifecycleEventsToOwnerAndCreator(t *testing.T) {
	svc := passesSvc(t)
	notifier := &fakeUserNotifier{}
	svc.SetUserNotifier(notifier)
	now := time.Now()
	owner := "bob"
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) { st.UserID = &owner })

	run := sentRun(t, svc, task.ID, now)
	svc.handleAnswer(t.Context(), *run.A2ATaskID, nil, &a2a.Error{Message: "boom"})
	if err := svc.finishPass(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	want := []string{"execution_queued", "execution_started", "session_created"}
	for _, user := range []string{owner, "alice"} {
		got := eventTypes(notifier, user)
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Errorf("%s got %v, want %v", user, got, want)
		}
	}
}

func TestScheduledRun_SendCarriesTheCreatorsIdentity(t *testing.T) {
	core := setupIdentityTestCore(t)
	signer := &recordingSigner{}
	core.SetTaskSigner(signer)
	core.SetIdentityResolver(&stubIdentityResolver{ident: auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice", Scopes: []string{"agent:test-agent:invoke"}, Authenticated: true,
	})})
	svc := core.ScheduledTasks()
	now := time.Now()
	dueTask(t, svc, now.Add(-time.Second), nil)

	mustRunPasses(t, svc, now)

	if id := signer.firstIdentity(); signer.callCount() != 1 || id == nil || id.UserID() != "alice" {
		t.Fatalf("signer calls %d identity %v, want one call as alice", signer.callCount(), id)
	}
}

func TestScheduledRun_NoResolverSignsNothing(t *testing.T) {
	core := setupIdentityTestCore(t)
	signer := &recordingSigner{}
	core.SetTaskSigner(signer)
	svc := core.ScheduledTasks()
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), nil)

	mustRunPasses(t, svc, now)

	if signer.callCount() != 0 {
		t.Fatalf("signer.calls = %d, want 0 without a resolver", signer.callCount())
	}
	if runs := taskRuns(t, svc, task.ID); len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("runs = %+v, want one sent run", runs)
	}
}

func TestScheduledRun_NoAutoPauseAtThresholdZero(t *testing.T) {
	svc := passesSvc(t)
	svc.cfg.AutoPauseAfterConsecutiveFailures = 0
	now := time.Now()
	task := dueTask(t, svc, now.Add(-time.Second), func(st *store.ScheduledTask) {
		st.TargetAgentName = "missing-agent"
		st.ConsecutiveFailureCount = 50
	})

	mustRunPasses(t, svc, now)

	if got := reloadTask(t, svc, task.ID); !got.Enabled || got.ConsecutiveFailureCount != 51 {
		t.Fatalf("task = enabled %v failures %d, want still enabled at 51", got.Enabled, got.ConsecutiveFailureCount)
	}
}

func TestTriggerTaskNow_RecordsAPendingManualRun(t *testing.T) {
	svc := passesSvc(t)
	notifier := &fakeUserNotifier{}
	svc.SetUserNotifier(notifier)
	task := dueTask(t, svc, time.Now().Add(time.Hour), func(st *store.ScheduledTask) {
		st.Enabled = false
		st.RunCount = 4
	})

	execID, err := svc.TriggerTaskNow(t.Context(), task.ID, "carol")
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.repo.GetExecutionByID(t.Context(), execID)
	if err != nil || run == nil {
		t.Fatalf("run = %v, %v", run, err)
	}
	if run.Status != "pending" || run.TriggerType != store.TriggerTypeManual || run.TriggeredBy == nil || *run.TriggeredBy != "carol" {
		t.Fatalf("run = %+v, want a pending manual run by carol", run)
	}
	if got := eventTypes(notifier, "alice"); len(got) != 1 || got[0] != "execution_queued" {
		t.Fatalf("events = %v, want execution_queued", got)
	}

	now := time.Now()
	mustRunPasses(t, svc, now)
	svc.handleAnswer(t.Context(), *run.A2ATaskID, finalTask(a2a.TaskStateCompleted, "done"), nil)
	if got := reloadTask(t, svc, task.ID); got.RunCount != 5 || got.Enabled {
		t.Fatalf("task = run_count %d enabled %v, want 5 and still disabled", got.RunCount, got.Enabled)
	}
}

func TestTriggerTaskNow_RefusesOnlyWhileTheTaskIsRunning(t *testing.T) {
	svc := passesSvc(t)
	task := dueTask(t, svc, time.Now().Add(time.Hour), nil)
	other := dueTask(t, svc, time.Now().Add(time.Hour), func(st *store.ScheduledTask) { st.Name = "other" })

	if _, err := svc.TriggerTaskNow(t.Context(), task.ID, "carol"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TriggerTaskNow(t.Context(), task.ID, "carol"); !errors.Is(err, ErrTaskAlreadyRunning) {
		t.Fatalf("second trigger err = %v, want ErrTaskAlreadyRunning", err)
	}
	if _, err := svc.TriggerTaskNow(t.Context(), other.ID, "carol"); err != nil {
		t.Fatalf("trigger of another task while one runs = %v, want accepted (no scheduler-wide cap)", err)
	}
}
