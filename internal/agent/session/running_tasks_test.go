// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func eachRunningTaskStore(t *testing.T, fn func(t *testing.T, store Store)) {
	t.Run("sql", func(t *testing.T) { fn(t, newTestSQLStore(t)) })
	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		fn(t, NewMemoryStore())
	})
}

func listRunning(t *testing.T, store Store, sessionID string, liveAfter time.Time) []string {
	t.Helper()
	ids, err := store.ListRunningTasks(context.Background(), "app", "user", sessionID, liveAfter)
	if err != nil {
		t.Fatalf("list running tasks: %v", err)
	}
	slices.Sort(ids)
	return ids
}

func TestRunningTasksAreListedPerSessionUntilTheirLeaseLapses(t *testing.T) {
	eachRunningTaskStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		now := time.Now()
		sessID, otherSess := idgen.New(), idgen.New()
		live, lapsed, elsewhere := "task-live", "task-lapsed", "task-elsewhere"
		for _, reg := range []struct {
			id, app, user, sess string
			until               time.Time
		}{
			{live, "app", "user", sessID, now.Add(time.Minute)},
			{lapsed, "app", "user", sessID, now.Add(-time.Minute)},
			{elsewhere, "app", "user", otherSess, now.Add(time.Minute)},
			{"task-other-user", "app", "bob", sessID, now.Add(time.Minute)},
			{"task-other-app", "other-app", "user", sessID, now.Add(time.Minute)},
		} {
			task := RunningTask{TaskID: reg.id, AppName: reg.app, UserID: reg.user, SessionID: reg.sess}
			if err := store.RegisterRunningTask(ctx, task, "owner-1", reg.until); err != nil {
				t.Fatalf("register %s: %v", reg.id, err)
			}
		}

		if got := listRunning(t, store, sessID, now); !slices.Equal(got, []string{live}) {
			t.Errorf("running tasks = %v, want only %s: a lapsed lease is a dead owner, and another session's, user's or agent's task is not a sibling", got, live)
		}
	})
}

func TestRunningTaskRenewalAndRemovalAreOwnerScoped(t *testing.T) {
	eachRunningTaskStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		now := time.Now()
		sessID := idgen.New()
		task := RunningTask{TaskID: "task-1", AppName: "app", UserID: "user", SessionID: sessID}
		if err := store.RegisterRunningTask(ctx, task, "first-owner", now.Add(time.Second)); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := store.RegisterRunningTask(ctx, task, "adopter", now.Add(time.Second)); err != nil {
			t.Fatalf("adopter re-register: %v", err)
		}

		renewed, err := store.RenewRunningTasks(ctx, "first-owner", []RunningTask{task}, now.Add(time.Hour))
		if err != nil || renewed != 0 {
			t.Errorf("renewal by the replaced owner = %d, %v; want 0 rows", renewed, err)
		}
		if got := listRunning(t, store, sessID, now.Add(time.Minute)); len(got) != 0 {
			t.Errorf("the replaced owner extended the adopter's lease: running = %v", got)
		}
		if err = store.RemoveRunningTask(ctx, "task-1", "first-owner"); err != nil {
			t.Fatalf("remove by replaced owner: %v", err)
		}
		if got := listRunning(t, store, sessID, now); !slices.Equal(got, []string{"task-1"}) {
			t.Fatalf("the replaced owner erased the adopter's claim: running = %v", got)
		}

		renewed, err = store.RenewRunningTasks(ctx, "adopter", []RunningTask{task}, now.Add(time.Hour))
		if err != nil || renewed != 1 {
			t.Errorf("renewal by the owner = %d, %v; want 1 row", renewed, err)
		}
		if got := listRunning(t, store, sessID, now.Add(time.Minute)); !slices.Equal(got, []string{"task-1"}) {
			t.Errorf("renewed lease not extended: running = %v", got)
		}

		if err := store.RemoveRunningTask(ctx, "task-1", "adopter"); err != nil {
			t.Fatalf("remove by owner: %v", err)
		}
		if got := listRunning(t, store, sessID, now); len(got) != 0 {
			t.Errorf("running = %v after the owner removed it", got)
		}
	})
}

func TestRunningTaskRenewalRestoresAMissingRow(t *testing.T) {
	eachRunningTaskStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		now := time.Now()
		sessID := idgen.New()
		tasks := []RunningTask{
			{TaskID: "task-1", AppName: "app", UserID: "user", SessionID: sessID},
			{TaskID: "task-2", AppName: "app", UserID: "user", SessionID: sessID},
		}
		if err := store.RegisterRunningTask(ctx, tasks[0], "owner", now.Add(time.Second)); err != nil {
			t.Fatalf("register: %v", err)
		}
		renewed, err := store.RenewRunningTasks(ctx, "owner", tasks, now.Add(time.Hour))
		if err != nil || renewed != 2 {
			t.Fatalf("renewal = %d, %v; want both rows renewed or restored", renewed, err)
		}
		if got := listRunning(t, store, sessID, now.Add(time.Minute)); !slices.Equal(got, []string{"task-1", "task-2"}) {
			t.Errorf("running after renewal = %v, want both tasks", got)
		}
	})
}

func TestRunningTaskRenewalCountsEveryBatch(t *testing.T) {
	eachRunningTaskStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		now := time.Now()
		sessID := idgen.New()
		tasks := make([]RunningTask, 2*runningTaskUpsertBatch+7)
		for i := range tasks {
			tasks[i] = RunningTask{TaskID: idgen.New(), AppName: "app", UserID: "user", SessionID: sessID}
		}
		renewed, err := store.RenewRunningTasks(ctx, "owner", tasks, now.Add(time.Hour))
		if err != nil || renewed != len(tasks) {
			t.Fatalf("renewal = %d, %v; want %d", renewed, err, len(tasks))
		}
		if got := listRunning(t, store, sessID, now); len(got) != len(tasks) {
			t.Errorf("running = %d tasks, want %d", len(got), len(tasks))
		}
	})
}

func TestDeleteLapsedRunningTasksClearsOnlyThisAgentsDeadClaims(t *testing.T) {
	eachRunningTaskStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		now := time.Now()
		sessID := idgen.New()
		for _, reg := range []struct {
			id, app string
			until   time.Time
		}{
			{"lapsed", "app", now.Add(-time.Hour)},
			{"live", "app", now.Add(time.Hour)},
			{"other-agent-lapsed", "other-app", now.Add(-time.Hour)},
		} {
			task := RunningTask{TaskID: reg.id, AppName: reg.app, UserID: "user", SessionID: sessID}
			if err := store.RegisterRunningTask(ctx, task, "owner", reg.until); err != nil {
				t.Fatalf("register %s: %v", reg.id, err)
			}
		}

		removed, err := store.DeleteLapsedRunningTasks(ctx, "app", now)
		if err != nil || removed != 1 {
			t.Fatalf("sweep removed %d, %v; want only the lapsed row of this agent", removed, err)
		}
		if got := listRunning(t, store, sessID, now.Add(-2*time.Hour)); !slices.Equal(got, []string{"live"}) {
			t.Errorf("app rows after sweep = %v, want [live]", got)
		}
		otherLeft, err := store.ListRunningTasks(ctx, "other-app", "user", sessID, now.Add(-2*time.Hour))
		if err != nil || len(otherLeft) != 1 {
			t.Errorf("another agent's rows = %v, %v; the sweep must not touch them", otherLeft, err)
		}
	})
}

func TestDeletingASessionRemovesItsRunningTasks(t *testing.T) {
	eachRunningTaskStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		now := time.Now()
		sessID, keptSess := idgen.New(), idgen.New()
		for _, s := range []string{sessID, keptSess} {
			if err := store.Create(ctx, &Session{AppName: "app", UserID: "user", SessionID: s, ContextID: s}); err != nil {
				t.Fatalf("create: %v", err)
			}
			task := RunningTask{TaskID: "task-" + s, AppName: "app", UserID: "user", SessionID: s}
			if err := store.RegisterRunningTask(ctx, task, "owner", now.Add(time.Hour)); err != nil {
				t.Fatalf("register: %v", err)
			}
		}

		if err := store.Delete(ctx, "app", "user", sessID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if got := listRunning(t, store, sessID, now); len(got) != 0 {
			t.Errorf("deleted session still lists running tasks %v", got)
		}
		if got := listRunning(t, store, keptSess, now); len(got) != 1 {
			t.Errorf("another session's running tasks = %v, want its one claim kept", got)
		}
	})
}

func TestCheckpointedTasksFindsOnlyThisSessionsCheckpoints(t *testing.T) {
	eachRunningTaskStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		sessID, otherSess := idgen.New(), idgen.New()
		taskHere, taskElsewhere, taskUnknown := idgen.New(), idgen.New(), idgen.New()
		for _, cp := range []struct{ task, sess string }{
			{taskHere, sessID},
			{taskElsewhere, otherSess},
		} {
			if err := store.Create(ctx, &Session{AppName: "app", UserID: "user", SessionID: cp.sess, ContextID: cp.sess}); err != nil {
				t.Fatalf("create: %v", err)
			}
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: cp.task, AppName: "app", AgentName: "app", UserID: "user", SessionID: cp.sess,
				State: []byte(`{"version":1}`),
			}); err != nil {
				t.Fatalf("save checkpoint: %v", err)
			}
		}

		got, err := store.CheckpointedTasks(ctx, sessID, []string{taskHere, taskElsewhere, taskUnknown})
		if err != nil {
			t.Fatalf("checkpointed tasks: %v", err)
		}
		if !slices.Equal(got, []string{taskHere}) {
			t.Errorf("checkpointed tasks = %v, want only the one checkpointed in this session", got)
		}
	})
}

func TestMessageTaskIDSurvivesAppend(t *testing.T) {
	eachRunningTaskStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		sessID := idgen.New()
		if err := store.Create(ctx, &Session{AppName: "app", UserID: "user", SessionID: sessID, ContextID: sessID}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := store.AppendMessages(ctx, "app", "user", sessID, []Message{
			{Role: "user", Content: "q", TaskID: "task-a"},
			{Role: "user", Content: "legacy"},
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
		sess, err := store.Get(ctx, "app", "user", sessID)
		if err != nil || sess == nil || len(sess.Messages) != 2 {
			t.Fatalf("get: %v, %+v", err, sess)
		}
		if sess.Messages[0].TaskID != "task-a" || sess.Messages[1].TaskID != "" {
			t.Errorf("task ids = %q, %q; want the writer kept and an unattributed row left empty",
				sess.Messages[0].TaskID, sess.Messages[1].TaskID)
		}
	})
}
