// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type gatedBatchRepo struct {
	sseEventStore
	armed   atomic.Bool
	open    chan struct{}
	entered chan struct{}
}

func newGatedBatchRepo(inner sseEventStore) *gatedBatchRepo {
	r := &gatedBatchRepo{sseEventStore: inner, open: make(chan struct{}), entered: make(chan struct{}, 1)}
	r.armed.Store(true)
	return r
}

func (r *gatedBatchRepo) AppendBatch(ctx context.Context, rows []*store.SSEEventBufferRow) (int64, error) {
	if !r.armed.Load() {
		return r.sseEventStore.AppendBatch(ctx, rows)
	}
	select {
	case r.entered <- struct{}{}:
	default:
	}
	select {
	case <-r.open:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return r.sseEventStore.AppendBatch(ctx, rows)
}

type gatedMarkRepo struct {
	sseEventStore
	open    chan struct{}
	entered chan struct{}
}

func (r *gatedMarkRepo) MarkCompletedBatch(ctx context.Context, taskIDs []string) error {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	select {
	case <-r.open:
	case <-ctx.Done():
		return ctx.Err()
	}
	return r.sseEventStore.MarkCompletedBatch(ctx, taskIDs)
}

type rejectingMarkRepo struct {
	sseEventStore
	refused string
}

func (r rejectingMarkRepo) MarkCompletedBatch(ctx context.Context, taskIDs []string) error {
	if slices.Contains(taskIDs, r.refused) {
		return &pgconn.PgError{Code: "22P02"}
	}
	return r.sseEventStore.MarkCompletedBatch(ctx, taskIDs)
}

func (r rejectingMarkRepo) MarkCompleted(ctx context.Context, taskID string) error {
	if taskID == r.refused {
		return &pgconn.PgError{Code: "22P02"}
	}
	return r.sseEventStore.MarkCompleted(ctx, taskID)
}

func waitOn(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

func isCompleted(t *testing.T, el *PersistentEventLog, taskID string) bool {
	t.Helper()
	done, err := el.repo.IsCompleted(t.Context(), taskID)
	return err == nil && done
}

func TestComplete_FinalRowWrittenFirstStillHoldsTheAckUntilTheMark(t *testing.T) {
	el := newTestPersistentEventLog(t)
	repo := &gatedMarkRepo{sseEventStore: el.repo, open: make(chan struct{}), entered: make(chan struct{}, 1)}
	el.repo = repo
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	t.Cleanup(func() { close(repo.open) })
	taskID := tid("final-written-first")
	var acks atomic.Int32

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	flushNow(t.Context(), el)
	if got := dbSeqs(t, el, taskID); len(got) != 1 {
		t.Fatalf("stored %v, want the final row before Complete", got)
	}
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()

	waitOn(t, repo.entered, "the completion mark")
	if n := acks.Load(); n != 0 {
		t.Fatalf("%d acknowledgements released before the completion mark was written, want 0", n)
	}
	repo.open <- struct{}{}
	waitFor(t, func() bool { return isCompleted(t, el, taskID) })
	waitFor(t, func() bool { return acks.Load() == 1 })
}

func TestComplete_AfterStopWritesTheMark(t *testing.T) {
	el := newTestPersistentEventLogAsync(t)
	el.Stop()
	taskID := tid("complete-after-stop")
	var acks atomic.Int32

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if !isCompleted(t, el, taskID) {
		t.Fatal("task not marked completed when Complete returned")
	}
	d.Release()
	if n := acks.Load(); n != 1 {
		t.Fatalf("acks = %d, want 1", n)
	}
}

func TestComplete_OwnTaskWithItsRowsWrittenIsMarkedAtOnce(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("own-task-complete")

	if _, err := el.Append(t.Context(), taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if !isCompleted(t, el, taskID) {
		t.Fatal("task not marked completed when Complete returned")
	}
}

func TestComplete_ReturnsWhileTheFlusherIsStuck(t *testing.T) {
	el := newTestPersistentEventLog(t)
	repo := newGatedBatchRepo(el.repo)
	el.repo = repo
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	t.Cleanup(func() { close(repo.open) })
	taskID := tid("complete-flusher-stuck")
	var acks atomic.Int32

	ctx, d := received("m-final", false, &acks)
	defer d.Release()
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitOn(t, repo.entered, "the flush of the first row")

	done := make(chan error, 1)
	go func() { done <- el.Complete(ctx, taskID) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Complete waited on the stuck flusher")
	}
}

func TestFlush_ARefusedCompletionMarkHoldsBackNoOtherTask(t *testing.T) {
	el := newTestPersistentEventLog(t)
	refused, accepted := tid("mark-refused"), tid("mark-accepted")
	el.repo = rejectingMarkRepo{sseEventStore: el.repo, refused: refused}
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	var acks atomic.Int32

	for _, taskID := range []string{refused, accepted} {
		ctx, d := received("m-"+taskID, false, &acks)
		if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if err := el.Complete(ctx, taskID); err != nil {
			t.Fatal(err)
		}
		d.Release()
	}
	flushNow(t.Context(), el)

	waitFor(t, func() bool { return isCompleted(t, el, accepted) })
	waitFor(t, func() bool { return acks.Load() == 2 })
	el.mu.Lock()
	pending := len(el.pendingComplete)
	el.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d tasks still wait for a completion mark, want none", pending)
	}
}

func TestFlush_TaskForgottenWhileItsRowIsWrittenIsNotTrackedAgain(t *testing.T) {
	el := newTestPersistentEventLog(t)
	repo := newGatedBatchRepo(el.repo)
	el.repo = repo
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("forgotten-in-flight")
	var acks atomic.Int32

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitOn(t, repo.entered, "the flush of the final row")
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()
	el.Forget(taskID)
	close(repo.open)
	waitFor(t, func() bool { return acks.Load() == 1 })

	el.mu.Lock()
	_, buffered := el.buffered[taskID]
	el.mu.Unlock()
	if buffered {
		t.Fatal("the flush tracked a forgotten task again")
	}
}

func TestAppend_RefusesALateEventWhileTheCompletionMarkIsWritten(t *testing.T) {
	el := newTestPersistentEventLog(t)
	repo := &gatedMarkRepo{sseEventStore: el.repo, open: make(chan struct{}), entered: make(chan struct{}, 1)}
	el.repo = repo
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	t.Cleanup(func() { close(repo.open) })
	taskID := tid("late-during-mark")
	var acks atomic.Int32
	appendLate := func(id string) error {
		ctx, d := received(id, false, &acks)
		defer d.Release()
		_, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
		return err
	}

	sctx, sd := received("m-status", false, &acks)
	if _, err := el.Append(sctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	sd.Release()
	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()
	el.Forget(taskID)
	waitOn(t, repo.entered, "the completion mark")

	if err := appendLate("m-late-1"); !errors.Is(err, ErrTaskCompleted) {
		t.Fatalf("late event while the mark is written: err %v, want ErrTaskCompleted", err)
	}
	repo.open <- struct{}{}
	waitFor(t, func() bool { return completedOnTheFinalRow(t, el, taskID, 2) })
	if err := appendLate("m-late-2"); !errors.Is(err, ErrTaskCompleted) {
		t.Fatalf("late event after the mark: err %v, want ErrTaskCompleted", err)
	}
	if got := dbSeqs(t, el, taskID); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("stored %v, want only the status and final rows", got)
	}
}

func TestFlush_MarksTheTaskEventsBuffered(t *testing.T) {
	sdb := store.TestDB(t)
	taskRepo := store.NewTaskRepo(sdb)
	el := NewPersistentEventLog(store.NewSSEEventRepo(sdb), taskRepo, slog.Default())
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("flush-events-buffered")
	if err := taskRepo.Create(t.Context(), &store.Task{ID: taskID, UserID: "u", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	var acks atomic.Int32

	ctx, d := received("m1", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	d.Release()
	waitFor(t, func() bool { return acks.Load() == 1 })

	task, err := taskRepo.GetByID(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !task.EventsBuffered {
		t.Fatal("events_buffered not set after the flush wrote the task's first row")
	}
}

type markFailingRepo struct {
	sseEventStore
	fails  atomic.Int32
	calls  atomic.Int32
	acks   *atomic.Int32
	acksAt [16]atomic.Int32
}

func (r *markFailingRepo) record() error {
	if n := r.calls.Add(1); n <= int32(len(r.acksAt)) {
		r.acksAt[n-1].Store(r.acks.Load())
	}
	if r.fails.Add(-1) >= 0 {
		return errors.New("simulated mark failure")
	}
	return nil
}

func (r *markFailingRepo) MarkCompleted(ctx context.Context, taskID string) error {
	if err := r.record(); err != nil {
		return err
	}
	return r.sseEventStore.MarkCompleted(ctx, taskID)
}

func (r *markFailingRepo) MarkCompletedBatch(ctx context.Context, taskIDs []string) error {
	if err := r.record(); err != nil {
		return err
	}
	return r.sseEventStore.MarkCompletedBatch(ctx, taskIDs)
}

func completedOnTheFinalRow(t *testing.T, el *PersistentEventLog, taskID string, finalSeq int) bool {
	t.Helper()
	states, err := el.repo.SequenceStates(t.Context(), []string{taskID})
	if err != nil {
		t.Fatal(err)
	}
	s := states[taskID]
	return s.MaxSeq == finalSeq && s.Completed
}

func TestComplete_FailedMarkKeepsTheHoldUntilTheRetryWritesIt(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var acks atomic.Int32
	repo := &markFailingRepo{sseEventStore: el.repo, acks: &acks}
	repo.fails.Store(2)
	el.repo = repo
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("mark-fails-then-retried")

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()

	waitFor(t, func() bool { return isCompleted(t, el, taskID) })
	if n := repo.acksAt[2].Load(); n != 0 {
		t.Fatalf("%d acknowledgements released before the retried mark, want 0", n)
	}
	waitFor(t, func() bool { return acks.Load() == 1 })
}

func TestStop_WritesAMarkAFailureHeldBack(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var acks atomic.Int32
	repo := &markFailingRepo{sseEventStore: el.repo, acks: &acks}
	repo.fails.Store(2)
	el.repo = repo
	el.EnableAsyncFlush(50, time.Hour, 1000)
	taskID := tid("stop-writes-held-mark")

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()
	waitFor(t, func() bool { return repo.calls.Load() >= 2 })
	el.Stop()

	if !isCompleted(t, el, taskID) {
		t.Fatal("Stop left the mark unwritten")
	}
	if n := acks.Load(); n != 1 {
		t.Fatalf("acks = %d, want 1", n)
	}
}

func TestCleanup_ReleasesTheHoldOfAnUnwrittenMark(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var acks atomic.Int32
	repo := &markFailingRepo{sseEventStore: el.repo, acks: &acks}
	repo.fails.Store(1000)
	el.repo = repo
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("cleanup-releases-hold")

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()
	waitFor(t, func() bool { return repo.calls.Load() >= 2 })
	if n := acks.Load(); n != 0 {
		t.Fatalf("acks = %d before Cleanup, want 0 while the mark is unwritten", n)
	}

	if err := el.Cleanup(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if n := acks.Load(); n != 1 {
		t.Fatalf("acks = %d after Cleanup, want 1", n)
	}
}

func TestComplete_OwnTaskWhoseMarkFailsIsMarkedByTheFlusher(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var acks atomic.Int32
	repo := &markFailingRepo{sseEventStore: el.repo, acks: &acks}
	repo.fails.Store(1)
	el.repo = repo
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("own-task-mark-fails")

	if _, err := el.Append(t.Context(), taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return isCompleted(t, el, taskID) })
}

func TestComplete_DuringTheFinalDrainMarksTheFinalRow(t *testing.T) {
	el := newTestPersistentEventLog(t)
	repo := newGatedBatchRepo(el.repo)
	repo.armed.Store(false)
	el.repo = repo
	el.EnableAsyncFlush(50, time.Hour, 1000)
	taskID := tid("complete-during-drain")
	var acks atomic.Int32

	if _, err := el.Append(t.Context(), taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	repo.armed.Store(true)
	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { el.Stop(); close(stopped) }()
	waitOn(t, repo.entered, "the final drain")

	completed := make(chan error, 1)
	go func() { completed <- el.Complete(ctx, taskID) }()
	select {
	case <-completed:
		t.Fatal("Complete returned before the final drain stored the final row")
	case <-time.After(200 * time.Millisecond):
	}
	close(repo.open)
	waitOn(t, stopped, "Stop")
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Complete never returned")
	}
	d.Release()

	if !completedOnTheFinalRow(t, el, taskID, 2) {
		t.Fatal("the completion mark is not on the final row")
	}
	if n := acks.Load(); n != 1 {
		t.Fatalf("acks = %d, want 1", n)
	}
}

func TestComplete_AfterAFailedFinalDrainLeavesTheTaskToTheNextGateway(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	fail := &atomic.Bool{}
	first.repo = failAppendBatchRepo{sseEventStore: first.repo, fail: fail}
	first.EnableAsyncFlush(50, time.Hour, 1000)
	taskID := tid("complete-after-failed-drain")
	var acks atomic.Int32

	if _, err := first.Append(t.Context(), taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	ctx, d := received("m-final", false, &acks)
	if _, err := first.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	first.Stop()
	if err := first.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()
	if n := acks.Load(); n != 0 {
		t.Fatalf("acks = %d, want the final message kept for redelivery", n)
	}
	if isCompleted(t, first, taskID) {
		t.Fatal("marked completed with its final row unstored")
	}

	ctx, d = received("m-final", true, &acks)
	defer d.Release()
	seq, err := second.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`))
	if err != nil || seq != 2 {
		t.Fatalf("redelivered final event: seq %d err %v, want it stored as 2", seq, err)
	}
	if err := second.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if !completedOnTheFinalRow(t, second, taskID, 2) {
		t.Fatal("the completion mark is not on the final row")
	}
}

type bufferedMarkFake struct {
	refused string
	fails   atomic.Int32
	mu      sync.Mutex
	marked  map[string]bool
}

func newBufferedMarkFake() *bufferedMarkFake {
	return &bufferedMarkFake{marked: make(map[string]bool)}
}

func (f *bufferedMarkFake) MarkEventsBuffered(ctx context.Context, taskID string) error {
	return f.MarkEventsBufferedBatch(ctx, []string{taskID})
}

func (f *bufferedMarkFake) MarkEventsBufferedBatch(_ context.Context, taskIDs []string) error {
	if f.fails.Add(-1) >= 0 {
		return errors.New("simulated events_buffered failure")
	}
	if f.refused != "" && slices.Contains(taskIDs, f.refused) {
		return &pgconn.PgError{Code: "22P02"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range taskIDs {
		f.marked[id] = true
	}
	return nil
}

func (f *bufferedMarkFake) isMarked(taskID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.marked[taskID]
}

func TestFlush_ARefusedEventsBufferedMarkHoldsBackNoOtherTask(t *testing.T) {
	el := newTestPersistentEventLog(t)
	refused, accepted := tid("buffered-refused"), tid("buffered-accepted")
	marks := newBufferedMarkFake()
	marks.refused = refused
	el.taskRepo = marks
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	var acks atomic.Int32

	for _, taskID := range []string{refused, accepted} {
		ctx, d := received("m-"+taskID, false, &acks)
		if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		d.Release()
	}
	flushNow(t.Context(), el)
	waitFor(t, func() bool { return acks.Load() == 2 })

	if !marks.isMarked(accepted) {
		t.Fatal("the accepted task was not marked events_buffered")
	}
}

func TestFlush_AFailedEventsBufferedMarkIsRetriedWithTheNextRow(t *testing.T) {
	el := newTestPersistentEventLog(t)
	marks := newBufferedMarkFake()
	marks.fails.Store(2)
	el.taskRepo = marks
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("buffered-retried")
	var acks atomic.Int32

	for i := range 2 {
		ctx, d := received(fmt.Sprintf("m%d", i), false, &acks)
		if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		d.Release()
		flushNow(t.Context(), el)
		waitFor(t, func() bool { return acks.Load() == int32(i+1) })
		if i == 0 && marks.isMarked(taskID) {
			t.Fatal("marked although both mark calls failed")
		}
	}
	if !marks.isMarked(taskID) {
		t.Fatal("the failed mark was not retried with the next row")
	}
}

type secondMarkFailsRepo struct {
	sseEventStore
	singles atomic.Int32
}

func (r *secondMarkFailsRepo) MarkCompletedBatch(ctx context.Context, taskIDs []string) error {
	if r.singles.Load() < 3 {
		return errors.New("simulated batch failure")
	}
	return r.sseEventStore.MarkCompletedBatch(ctx, taskIDs)
}

func (r *secondMarkFailsRepo) MarkCompleted(ctx context.Context, taskID string) error {
	if r.singles.Add(1) == 2 {
		return errors.New("simulated mark failure")
	}
	return r.sseEventStore.MarkCompleted(ctx, taskID)
}

func TestFlush_AMarkFailingPartwayKeepsTheHoldsOfTheTasksNotMarked(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.repo = &secondMarkFailsRepo{sseEventStore: el.repo}
	el.EnableAsyncFlush(50, 20*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskIDs := []string{tid("partway-a"), tid("partway-b"), tid("partway-c")}
	var acks atomic.Int32
	ctxs := make([]context.Context, len(taskIDs))
	ds := make([]*delivery.Delivery, len(taskIDs))
	for i, taskID := range taskIDs {
		ctxs[i], ds[i] = received("m-"+taskID, false, &acks)
		if _, err := el.Append(ctxs[i], taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, taskID := range taskIDs {
		waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })
	}
	el.flushMu.Lock()
	for i, taskID := range taskIDs {
		if err := el.Complete(ctxs[i], taskID); err != nil {
			t.Fatal(err)
		}
	}
	el.flushMu.Unlock()
	for _, d := range ds {
		d.Release()
	}

	waitFor(t, func() bool { return acks.Load() == 1 })
	time.Sleep(300 * time.Millisecond)
	if n := acks.Load(); n != 1 {
		t.Fatalf("acks = %d, want only the task marked before the failure", n)
	}
	waitFor(t, func() bool {
		for _, taskID := range taskIDs {
			if !isCompleted(t, el, taskID) {
				return false
			}
		}
		return true
	})
	waitFor(t, func() bool { return acks.Load() == 3 })
}

func TestFlush_AFailedMarkIsRetriedAtMostOnceASecond(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var acks atomic.Int32
	repo := &markFailingRepo{sseEventStore: el.repo, acks: &acks}
	repo.fails.Store(1 << 20)
	el.repo = repo
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("mark-retry-rate")

	ctx, d := received("m-final", false, &acks)
	defer d.Release()
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if n := repo.calls.Load(); n > 2 {
		t.Fatalf("%d mark calls in 600ms of outage, want the retry held back a second", n)
	}
}

func TestComplete_AfterStopReportsAFailedMark(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var acks atomic.Int32
	repo := &markFailingRepo{sseEventStore: el.repo, acks: &acks}
	repo.fails.Store(1000)
	el.repo = repo
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	el.Stop()
	taskID := tid("after-stop-mark-fails")

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(ctx, taskID); err == nil {
		t.Fatal("Complete returned no error for a mark that failed")
	}
	d.Release()
	if n := acks.Load(); n != 0 {
		t.Fatalf("acks = %d, want the final message kept for redelivery", n)
	}
}

func TestComplete_AfterStopKeepsACompletingMessageThatOwnsNoRow(t *testing.T) {
	el := newTestPersistentEventLog(t)
	fail := &atomic.Bool{}
	fail.Store(true)
	el.repo = failAppendBatchRepo{sseEventStore: el.repo, fail: fail}
	el.EnableAsyncFlush(50, time.Hour, 1000)
	taskID := tid("after-stop-keeps-completing-message")
	var rowAcks, completeAcks atomic.Int32

	ctx, d := received("m-row", false, &rowAcks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	d.Release()
	el.Stop()

	ctx, d = received("m-complete", false, &completeAcks)
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()
	if n := completeAcks.Load(); n != 0 {
		t.Fatalf("completing message acknowledged %d times with the task's rows unstored, want 0", n)
	}
}

func TestComplete_AfterStopReportsAnOwnTaskWithRowsUnstored(t *testing.T) {
	el := newTestPersistentEventLog(t)
	fail := &atomic.Bool{}
	fail.Store(true)
	el.repo = failAppendBatchRepo{sseEventStore: el.repo, fail: fail}
	el.EnableAsyncFlush(50, time.Hour, 1000)
	taskID := tid("after-stop-own-task-unstored")

	if _, err := el.Append(t.Context(), taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	el.Stop()
	if err := el.Complete(t.Context(), taskID); err == nil {
		t.Fatal("Complete returned no error for a completion that cannot be recorded")
	}
	if isCompleted(t, el, taskID) {
		t.Fatal("marked completed with its final row unstored")
	}
}
