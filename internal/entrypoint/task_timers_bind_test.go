// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func twoGatewayInstances(t *testing.T) (active, standby *GatewayCore) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	dbURL := store.TestDBURL(t)
	newCore := func() *GatewayCore {
		core := NewGatewayCore(&recordingAdapter{}, map[string]any{
			"namespace":            "test-ns",
			"gateway_id":           "test-gw",
			"task_timeout_seconds": 60,
			"session_service": map[string]any{
				"type":         "sql",
				"database_url": dbURL,
			},
		})
		core.SetServices(runtime.NewRuntime(brk, nil).Services())
		if err := core.Init(t.Context(), nil); err != nil {
			t.Fatalf("Init: %v", err)
		}
		core.logger = silentLogger()
		return core
	}
	active = newCore()
	if err := active.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = active.Stop(context.Background()) })
	if !active.eventBindActive.Load() {
		t.Fatal("the started instance must hold the bind on the dev broker")
	}
	waitFor(t, 3*time.Second, func() bool { return !active.rearmTimers.Load() })
	standby = newCore()
	t.Cleanup(func() { _ = standby.db.Close() })
	return active, standby
}

func instanceNamedOnRow(t *testing.T, core *GatewayCore, taskID string) string {
	t.Helper()
	rec, err := store.NewTaskRecordRepo(core.db, "test-gw").Get(t.Context(), taskID)
	if err != nil || rec == nil {
		t.Fatalf("read row %s: %v", taskID, err)
	}
	return rec.GatewayInstanceID
}

func workingStatusMessage(t *testing.T, taskID string) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(a2a.Response{Result: &a2a.TaskStatusUpdateEvent{
		Kind:   "status-update",
		TaskID: taskID,
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewTextPart("working")}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &broker.Message{Topic: "test-ns/a2a/v1/gateway/status/test-gw/" + taskID, Payload: payload}
}

func TestActiveSweepTakesTasksSubmittedOnAStandby(t *testing.T) {
	active, standby := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := standby.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	standby.startTaskTimeout(taskID)
	if idleTimerArmed(standby, taskID) {
		t.Fatal("the standby instance must not arm a timer for the task it submitted")
	}

	active.RunTaskSweepNow()

	waitFor(t, 3*time.Second, func() bool { return idleTimerArmed(active, taskID) })
	if !active.tasks.Holds(taskID) {
		t.Error("the active instance must hold the task after the sweep")
	}
	if got := instanceNamedOnRow(t, active, taskID); got != active.instanceID {
		t.Errorf("row names %q, want the active instance %q", got, active.instanceID)
	}
	if info := active.tasks.Get(taskID); info == nil || info.TraceID != "trace-1" || info.AgentName != "agent-a" {
		t.Errorf("the local copy must come from the row: %+v", info)
	}
}

func TestActiveSweepTakesAWaitingTaskWithTheHardTimeoutAlone(t *testing.T) {
	active, standby := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := standby.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	standby.tasks.SetWaiting(taskID, "req-1")

	active.RunTaskSweepNow()

	waitFor(t, 3*time.Second, func() bool { return idleTimerArmed(active, taskID) })
	if !onlyHardTimeoutArmed(active, taskID) {
		t.Error("a task waiting on user input must get the hard timeout alone")
	}
	if info := active.tasks.Get(taskID); info == nil || info.WaitingRequestID != "req-1" {
		t.Errorf("the local copy must carry the wait: %+v", info)
	}
}

func TestActiveSweepLeavesExistingTimersAlone(t *testing.T) {
	active, _ := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := active.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	active.startTaskTimeout(taskID)
	before, _, armed := armedTimerTiers(active, taskID)
	if !armed {
		t.Fatal("precondition: the active instance arms at submit")
	}

	time.Sleep(5 * time.Millisecond)
	active.takeAppTasks(t.Context(), active.tasks.(taskRecordSweeper))

	after, _, armed := armedTimerTiers(active, taskID)
	if !armed || !after.Equal(before) {
		t.Errorf("the sweep moved the deadline from %v to %v (armed %v)", before, after, armed)
	}
}

func TestActiveSweepArmsTheHardTimeoutForAHeldTaskWithNoTimer(t *testing.T) {
	active, standby := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := active.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	active.pauseTaskTimeoutForUserInput(taskID, "req-1")
	standby.tasks.ClearWaiting(taskID, "req-1")

	active.takeAppTasks(t.Context(), active.tasks.(taskRecordSweeper))

	next, tiers, armed := armedTimerTiers(active, taskID)
	if !armed || tiers != 1 {
		t.Fatalf("want the hard timeout alone, got armed %v with %d tiers", armed, tiers)
	}
	if want := time.Duration(active.cfg.TaskHardTimeoutSeconds) * time.Second; time.Until(next) < want-time.Minute {
		t.Errorf("deadline in %v, want the hard timeout %v", time.Until(next), want)
	}
}

func TestActiveSweepGivesASignInPauseNoIdleNotice(t *testing.T) {
	active, _ := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := active.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	active.startTaskTimeout(taskID)
	active.cancelTaskTimeout(taskID)

	active.takeAppTasks(t.Context(), active.tasks.(taskRecordSweeper))

	active.taskTimers.mu.Lock()
	entry, ok := active.taskTimers.timers[taskID]
	active.taskTimers.mu.Unlock()
	if !ok || !entry.softDeadline.IsZero() || entry.hardDeadline.IsZero() {
		t.Errorf("want the hard timeout alone, got %+v", entry)
	}
}

func TestActivationRearmsEveryHeldTask(t *testing.T) {
	active, _ := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := active.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	active.startTaskTimeout(taskID)

	active.onEventBindState(broker.BindStandby)
	if idleTimerArmed(active, taskID) {
		t.Fatal("standby must clear the timer")
	}
	active.onEventBindState(broker.BindActive)

	waitFor(t, 3*time.Second, func() bool { return idleTimerArmed(active, taskID) })
}

func TestEventPathTakesATaskThisInstanceDoesNotHold(t *testing.T) {
	active, standby := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := standby.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}

	active.handleStatusEvent(t.Context(), workingStatusMessage(t, taskID))

	if !active.tasks.Holds(taskID) {
		t.Fatal("the active instance must hold the task after its first event")
	}
	if !idleTimerArmed(active, taskID) {
		t.Error("the first event must arm the timers")
	}
	if got := instanceNamedOnRow(t, active, taskID); got != active.instanceID {
		t.Errorf("row names %q, want the active instance %q", got, active.instanceID)
	}
}

func TestEventPathArmsNothingForATaskWithNoRow(t *testing.T) {
	active, _ := twoGatewayInstances(t)
	taskID := idgen.New()

	active.handleStatusEvent(t.Context(), workingStatusMessage(t, taskID))

	if active.tasks.Holds(taskID) || idleTimerArmed(active, taskID) {
		t.Error("a task with no row must be neither held nor timed")
	}
}

func TestAnswerOnAStandbyClearsTheWaitForTheActiveInstance(t *testing.T) {
	active, standby := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := standby.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	active.handleStatusEvent(t.Context(), workingStatusMessage(t, taskID))
	emitInputRequired(t, active, taskID, "req-1")
	if !onlyHardTimeoutArmed(active, taskID) || !taskWaiting(active, taskID) {
		t.Fatal("precondition: the active instance records the wait and keeps the hard timeout alone")
	}

	active.handleStatusEvent(t.Context(), workingStatusMessage(t, taskID))
	if !taskWaiting(active, taskID) || !onlyHardTimeoutArmed(active, taskID) {
		t.Fatal("an event during a wait the row still records must not end it or arm the idle notice")
	}

	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-1", Status: "answered"}
	if err := standby.SubmitUserInputResponse(t.Context(), "agent-a", taskID, resp, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatal(err)
	}
	if idleTimerArmed(standby, taskID) {
		t.Error("the standby instance must not arm a timer")
	}

	active.handleStatusEvent(t.Context(), workingStatusMessage(t, taskID))

	if taskWaiting(active, taskID) {
		t.Error("the agent's next event must bring the cleared wait to the active instance")
	}
	if !idleTimerArmed(active, taskID) || onlyHardTimeoutArmed(active, taskID) {
		t.Error("the active instance must arm both timers once the wait is over")
	}
}

func TestSweepEndsTheAppsTasksAtAnUndeployedAgent(t *testing.T) {
	active, standby := twoGatewayInstances(t)
	mine, theirs, waiting, carded := idgen.New(), idgen.New(), idgen.New(), idgen.New()
	register := func(core *GatewayCore, id, agent string) {
		if err := core.tasks.Register(id, agent, "user-1", TaskIdentity{}); err != nil {
			t.Fatal(err)
		}
	}
	register(active, mine, "agent-undeployed")
	register(standby, theirs, "agent-undeployed")
	register(standby, waiting, "agent-restarting")
	register(standby, carded, "agent-alive")
	active.agents.AddOrUpdate(a2a.AgentCard{Name: "agent-alive"})
	asked := map[string]int{}
	active.SetTargetLookup(func(_ context.Context, name string) control.Target {
		asked[name]++
		if name == "agent-undeployed" {
			return control.Target{State: control.TargetNotDeployed, CardName: name}
		}
		return control.Target{State: control.TargetDeployed, CardName: name}
	})

	active.takeAppTasks(t.Context(), active.tasks.(taskRecordSweeper))

	for _, id := range []string{mine, theirs} {
		if active.IsTaskRunning(id) {
			t.Errorf("task %s at the undeployed agent is still running", id)
		}
	}
	for _, id := range []string{waiting, carded} {
		if !active.IsTaskRunning(id) {
			t.Errorf("task %s ended, want it left to its timers", id)
		}
		active.cancelTaskTimeout(id)
	}
	if asked["agent-undeployed"] != 1 || asked["agent-alive"] != 0 {
		t.Errorf("lookups = %v, want one for the undeployed agent and none for the agent with a card", asked)
	}
}

func TestHardTimeoutDefersWhenTheRowCannotBeRead(t *testing.T) {
	db := store.TestDB(t)
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	core.tasks = NewDBTaskRegistry(store.NewTaskRecordRepo(db, "gw-test"), silentLogger())
	taskID := idgen.New()
	if err := core.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	armedAt := time.Now()
	core.onTaskHardTimeout(t.Context(), taskID, 2)

	if len(adapter.Errors()) != 0 {
		t.Fatalf("a timeout must not fire on an unread row: %+v", adapter.Errors())
	}
	next, tiers, armed := armedTimerTiers(core, taskID)
	if !armed || tiers != 1 {
		t.Fatalf("the hard timeout must re-arm one tier, got armed %v tiers %d", armed, tiers)
	}
	if got := next.Sub(armedAt); got < unreadRowRetry-time.Second || got > unreadRowRetry+time.Second {
		t.Errorf("re-armed for %v, want about %v", got, unreadRowRetry)
	}
	core.cancelTaskTimeout(taskID)
}

func TestHardTimeoutRetryKeepsTimersArmedMeanwhile(t *testing.T) {
	db := store.TestDB(t)
	core, adapter := newCapturingCoreWithHard(t, 60, 120)
	core.tasks = NewDBTaskRegistry(store.NewTaskRecordRepo(db, "gw-test"), silentLogger())
	fresh, waiting := idgen.New(), idgen.New()
	for _, id := range []string{fresh, waiting} {
		if err := core.tasks.Register(id, "agent-a", "user-1", TaskIdentity{}); err != nil {
			t.Fatal(err)
		}
	}
	core.tasks.SetWaiting(waiting, "req-1")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	core.startTaskTimeout(fresh)
	before, tiers, armed := armedTimerTiers(core, fresh)
	if !armed || tiers != 2 {
		t.Fatalf("precondition: fresh two-tier timers, got armed %v tiers %d", armed, tiers)
	}

	core.onTaskHardTimeout(t.Context(), fresh, 120)
	core.onTaskHardTimeout(t.Context(), waiting, 120)

	after, tiers, armed := armedTimerTiers(core, fresh)
	if !armed || tiers != 2 || !after.Equal(before) {
		t.Errorf("the retry must leave fresh timers alone: armed %v tiers %d before %v after %v", armed, tiers, before, after)
	}
	if !onlyHardTimeoutArmed(core, waiting) {
		t.Error("a task waiting on user input gets the retry too, or a lost agent leaves it with no timer")
	}
	core.cancelTaskTimeout(waiting)
	if len(adapter.Errors()) != 0 {
		t.Fatalf("no timeout may fire on an unread row: %+v", adapter.Errors())
	}
	core.cancelTaskTimeout(fresh)
}

func TestTimerCallbacksLeaveATaskAnotherInstanceHolds(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewTaskRecordRepo(db, "gw-test")
	core, adapter := newCapturingCoreWithHard(t, 60, 120)
	core.tasks = NewDBTaskRegistry(repo, silentLogger())
	taskID := idgen.New()
	if err := core.tasks.Register(taskID, "", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	core.startTaskTimeout(taskID)
	if _, err := repo.Take(t.Context(), taskID, "another-instance"); err != nil {
		t.Fatal(err)
	}

	core.onTaskIdleWarning(t.Context(), taskID, 60)
	core.onTaskHardTimeout(t.Context(), taskID, 120)

	if len(adapter.Warnings()) != 0 || len(adapter.Errors()) != 0 {
		t.Fatalf("callbacks must not act on a task another instance holds: %+v %+v", adapter.Warnings(), adapter.Errors())
	}
	if idleTimerArmed(core, taskID) {
		t.Error("the hard callback must drop this instance's timer for the task")
	}
	if exists, _ := repo.Exists(t.Context(), taskID); !exists {
		t.Error("the row of a task another instance holds must stay")
	}
	if _, err := repo.Take(t.Context(), taskID, core.instanceID); err != nil {
		t.Fatal(err)
	}
	core.onTaskHardTimeout(t.Context(), taskID, 120)
	if len(adapter.Errors()) != 1 {
		t.Fatalf("once the row names this instance the hard timeout acts, got %d errors", len(adapter.Errors()))
	}
}

func TestClearWaitingRetriesOnTheNextSweep(t *testing.T) {
	db := store.TestDB(t)
	reg := NewDBTaskRegistry(store.NewTaskRecordRepo(db, "gw-test"), silentLogger()).(*dbTaskRegistry)
	ctx := t.Context()
	taskID := idgen.New()
	if err := reg.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	reg.SetWaiting(taskID, "req-1")
	if _, err := db.ExecContext(ctx, `ALTER TABLE task_records RENAME TO task_records_hidden`); err != nil {
		t.Fatal(err)
	}

	reg.ClearWaiting(taskID, "req-1")

	if reg.Get(taskID).WaitingRequestID != "" {
		t.Fatal("the local copy must read as cleared at once")
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE task_records_hidden RENAME TO task_records`); err != nil {
		t.Fatal(err)
	}
	row, err := reg.repo.Get(ctx, taskID)
	if err != nil || row.WaitingOn() != "req-1" {
		t.Fatalf("setup: the row must still record the wait, got %v, %v", row, err)
	}
	held, ok := reg.takeAll(ctx, "instance-1")
	if !ok || len(held) != 1 || held[0].waiting {
		t.Fatalf("a sweep must not bring the wait back while the clear is pending, got %+v ok=%v", held, ok)
	}
	row, err = reg.repo.Get(ctx, taskID)
	if err != nil || row.WaitingOn() != "" {
		t.Fatalf("the sweep must retry the clear on the row, got %v, %v", row, err)
	}
	reg.mu.RLock()
	_, pending := reg.pendingClears[taskID]
	reg.mu.RUnlock()
	if pending {
		t.Error("a clear that reached the row must leave the retry set")
	}
}

func TestEventBindState_StandbyClearsTimersAndActiveRearms(t *testing.T) {
	active, _ := twoGatewayInstances(t)
	taskID := idgen.New()
	if err := active.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	active.startTaskTimeout(taskID)

	active.onEventBindState(broker.BindStandby)

	if active.eventBindActive.Load() {
		t.Error("the instance must read as standby")
	}
	if idleTimerArmed(active, taskID) {
		t.Error("a standby report must clear the task's timer")
	}
	active.startTaskTimeout(taskID)
	if idleTimerArmed(active, taskID) {
		t.Error("no timer may be armed while on standby")
	}

	active.onEventBindState(broker.BindActive)

	if !active.eventBindActive.Load() {
		t.Error("the instance must read as active")
	}
	waitFor(t, 3*time.Second, func() bool { return idleTimerArmed(active, taskID) })
}

func TestSoftWarningSkipsWhenTheRowCannotBeRead(t *testing.T) {
	db := store.TestDB(t)
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	core.tasks = NewDBTaskRegistry(store.NewTaskRecordRepo(db, "gw-test"), silentLogger())
	taskID := idgen.New()
	if err := core.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	core.onTaskIdleWarning(t.Context(), taskID, 1)

	if len(adapter.Warnings()) != 0 {
		t.Fatalf("a still-working notice must not be sent on an unread row: %+v", adapter.Warnings())
	}
}

func TestTimerCallbacksDoNothingOnStandby(t *testing.T) {
	core, adapter := newCapturingCoreWithHard(t, 1, 2)
	taskID := "task-standby-callback"
	_ = core.tasks.Register(taskID, "", "u1", TaskIdentity{})
	core.eventBindActive.Store(false)

	core.onTaskHardTimeout(t.Context(), taskID, 2)
	core.onTaskIdleWarning(t.Context(), taskID, 1)

	if len(adapter.Errors()) != 0 || len(adapter.Warnings()) != 0 {
		t.Fatalf("callbacks on standby must send nothing: %+v %+v", adapter.Errors(), adapter.Warnings())
	}
	if !core.IsTaskRunning(taskID) {
		t.Error("callbacks on standby must not forget the task")
	}
}

func TestRemoveRetriesAFailedDeleteOnTheNextSweep(t *testing.T) {
	db := store.TestDB(t)
	reg := NewDBTaskRegistry(store.NewTaskRecordRepo(db, "gw-test"), silentLogger()).(*dbTaskRegistry)
	ctx := t.Context()
	taskID := idgen.New()
	if err := reg.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE task_records RENAME TO task_records_hidden`); err != nil {
		t.Fatal(err)
	}

	reg.Remove(taskID)

	reg.mu.RLock()
	_, pending := reg.pendingDeletes[taskID]
	reg.mu.RUnlock()
	if !pending {
		t.Fatal("a failed delete must be kept for retry")
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE task_records_hidden RENAME TO task_records`); err != nil {
		t.Fatal(err)
	}

	held, ok := reg.takeAll(ctx, "instance-1")
	if !ok {
		t.Fatal("takeAll must succeed once the table is back")
	}
	if len(held) != 0 || reg.Holds(taskID) {
		t.Errorf("a task being deleted must not be taken again, got %+v", held)
	}
	if exists, _ := reg.repo.Exists(ctx, taskID); exists {
		t.Error("the sweep must retry the delete")
	}
	reg.mu.RLock()
	_, pending = reg.pendingDeletes[taskID]
	reg.mu.RUnlock()
	if pending {
		t.Error("a delete that succeeded must leave the retry set")
	}
}

func TestRegisterAfterAFailedDeleteKeepsTheRow(t *testing.T) {
	db := store.TestDB(t)
	reg := NewDBTaskRegistry(store.NewTaskRecordRepo(db, "gw-test"), silentLogger()).(*dbTaskRegistry)
	ctx := t.Context()
	taskID := idgen.New()
	if err := reg.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE task_records RENAME TO task_records_hidden`); err != nil {
		t.Fatal(err)
	}
	reg.Remove(taskID)
	if _, err := db.ExecContext(ctx, `ALTER TABLE task_records_hidden RENAME TO task_records`); err != nil {
		t.Fatal(err)
	}

	if err := reg.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.takeAll(ctx, "instance-1"); !ok {
		t.Fatal("takeAll must succeed")
	}
	if exists, _ := reg.repo.Exists(ctx, taskID); !exists {
		t.Error("the sweep deleted the row of a task registered again")
	}
	if !reg.Holds(taskID) {
		t.Error("a task registered again must be held")
	}
}

func TestTimerCallbacksRunUnderADeadlineOnABoundedPool(t *testing.T) {
	r := newTaskTimerRegistry()
	defer r.stopAll()
	const callbacks = 3 * timerCallbackWorkers
	var running, peak, withDeadline atomic.Int32
	var wg sync.WaitGroup
	wg.Add(callbacks)
	for i := range callbacks {
		r.startOrReset(string(rune('a'+i)), time.Millisecond, func(ctx context.Context) {
			defer wg.Done()
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timerCallbackDeadline {
				withDeadline.Add(1)
			}
			now := running.Add(1)
			for {
				seen := peak.Load()
				if now <= seen || peak.CompareAndSwap(seen, now) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
		}, 0, nil)
	}
	wg.Wait()

	if got := withDeadline.Load(); got != callbacks {
		t.Errorf("%d of %d callbacks ran under the deadline", got, callbacks)
	}
	if got := peak.Load(); got > timerCallbackWorkers || got < 2 {
		t.Errorf("peak concurrency %d, want between 2 and %d", got, timerCallbackWorkers)
	}
}

func TestDBTaskRegistry_WaitingFollowsTheRow(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewTaskRecordRepo(db, "gw-test")
	reg := NewDBTaskRegistry(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	taskID := idgen.New()
	if err := reg.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}

	reg.SetWaiting(taskID, "req-1")
	rec, _ := repo.Get(t.Context(), taskID)
	if rec.WaitingOn() != "req-1" || reg.Get(taskID).WaitingRequestID != "req-1" {
		t.Fatalf("row %q local %q, want req-1 on both", rec.WaitingOn(), reg.Get(taskID).WaitingRequestID)
	}
	reg.ClearWaiting(taskID, "req-0")
	rec, _ = repo.Get(t.Context(), taskID)
	if rec.WaitingOn() != "req-1" || reg.Get(taskID).WaitingRequestID != "req-1" {
		t.Fatal("a clear for another request must change nothing")
	}
	reg.ClearWaiting(taskID, "req-1")
	rec, _ = repo.Get(t.Context(), taskID)
	if rec.WaitingOn() != "" || reg.Get(taskID).WaitingRequestID != "" {
		t.Fatal("the clear for the named request must end the wait on both")
	}
}

func TestPendingUserInput_ThreeStates(t *testing.T) {
	db := store.TestDB(t)
	core, _ := newCapturingCoreWithHard(t, 1, 2)
	core.tasks = NewDBTaskRegistry(store.NewTaskRecordRepo(db, "gw-test"), silentLogger())
	taskID := idgen.New()
	if err := core.tasks.Register(taskID, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	if id, recorded := core.PendingUserInput(taskID); id != "" || recorded {
		t.Errorf("a task never asked a question has no recorded wait: %q %v", id, recorded)
	}
	core.tasks.SetWaiting(taskID, "req-1")
	if id, recorded := core.PendingUserInput(taskID); id != "req-1" || !recorded {
		t.Errorf("a waiting task names its request: %q %v", id, recorded)
	}
	core.tasks.ClearWaiting(taskID, "req-1")
	if id, recorded := core.PendingUserInput(taskID); id != "" || !recorded {
		t.Errorf("an answered task waits on nothing and says so: %q %v", id, recorded)
	}
	if id, recorded := core.PendingUserInput(idgen.New()); id != "" || !recorded {
		t.Errorf("a task with no row waits on nothing: %q %v", id, recorded)
	}
}
