// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func TestTaskRecordRepo_UpsertOnAnExistingRowRestampsIt(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	now := time.Now()
	id := idgen.New()
	stale := now.Add(-2 * time.Hour).UnixMilli()
	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1", LastSeenAt: stale}); err != nil {
		t.Fatal(err)
	}

	adopted, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if adopted.LastSeenAt < now.UnixMilli() {
		t.Fatalf("the stamp must move to now: was %d, now %d", stale, adopted.LastSeenAt)
	}
	if _, err := repo.DeleteUnseenBefore(ctx, now.Add(-30*time.Minute), 10); err != nil {
		t.Fatal(err)
	}
	if exists, _ := repo.Exists(ctx, id); !exists {
		t.Fatal("an adopted row must survive the reaper")
	}
}

func TestTaskRecordRepo_UpsertCreatesOnceAndFillsEachPartOnce(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	id := idgen.New()

	first, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1", TraceID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.AgentName != "a" || first.TraceID != "t1" || first.GatewayID != "gw-test" {
		t.Fatalf("the first call must return the row it created: %+v", first)
	}
	writes := []*TaskRecord{
		{TaskID: id, AgentName: "b", UserID: "u2", TraceID: "t2", SessionID: "s1", Trigger: "interactive", Surface: "ui", AgentType: "user_created", MachineIngress: true},
		{TaskID: id, AgentName: "c", UserID: "u3", SessionID: "s2", Trigger: "scheduled", Surface: "cli", SystemInitiated: true},
		{TaskID: id, AgentName: "d", UserID: "u4"},
	}
	var last *TaskRecord
	for _, rec := range writes {
		if last, err = repo.Upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	rec, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.AgentName != "a" || rec.UserID != "u1" {
		t.Fatalf("a later call must not change the agent or the user: %+v", rec)
	}
	if rec.TraceID != "t1" || rec.SessionID != "s1" {
		t.Fatalf("first non-empty trace and session must win: %+v", rec)
	}
	if rec.Trigger != "interactive" || rec.Surface != "ui" || rec.AgentType != "user_created" || !rec.MachineIngress || rec.SystemInitiated {
		t.Fatalf("the origin columns must move together with the first trigger: %+v", rec)
	}
	if last.WaitingOn() != rec.WaitingOn() {
		t.Fatalf("waiting flag: returned %q, row %q", last.WaitingOn(), rec.WaitingOn())
	}
	last.WaitingRequestID, rec.WaitingRequestID = nil, nil
	if *last != *rec {
		t.Fatalf("the returned row must be the row as it stands:\n got %+v\nwant %+v", last, rec)
	}
}

func TestTaskRecordRepo_ClaimRunReportOnce(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	id := idgen.New()

	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}

	const callers = 20
	won := make(chan *TaskRecord, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, err := repo.ClaimRunReport(ctx, id)
			if err != nil {
				t.Error(err)
				return
			}
			if rec != nil {
				won <- rec
			}
		}()
	}
	wg.Wait()
	close(won)

	var winners int
	for rec := range won {
		winners++
		if !rec.RunReported {
			t.Errorf("the returned row must show run_reported set: %+v", rec)
		}
	}
	if winners != 1 {
		t.Fatalf("want exactly one winner, got %d", winners)
	}
}

func TestTaskRecordRepo_Delete(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	a, b := idgen.New(), idgen.New()

	for _, id := range []string{a, b} {
		if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "agent", UserID: "u1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Delete(ctx, a); err != nil {
		t.Fatal(err)
	}
	if exists, _ := repo.Exists(ctx, a); exists {
		t.Fatal("deleted row still exists")
	}
	if exists, _ := repo.Exists(ctx, b); !exists {
		t.Fatal("deleting one row must not touch the other")
	}
	if err := repo.Delete(ctx, a); err != nil {
		t.Fatalf("deleting an absent row must not fail: %v", err)
	}
}

func TestTaskRecordRepo_SettleModel(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	id := idgen.New()

	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	announced := usagefacts.Model{Family: "claude-sonnet", Provider: "anthropic", ServedFrom: "hosted"}
	if err := repo.SettleModel(ctx, id, announced); err != nil {
		t.Fatal(err)
	}
	if err := repo.SettleModel(ctx, id, announced); err != nil {
		t.Fatal(err)
	}
	if err := repo.SettleModel(ctx, idgen.New(), announced); err != nil {
		t.Fatalf("settling an absent row must be a no-op, got %v", err)
	}

	rec, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ModelFamily != "claude-sonnet" || rec.ModelProvider != "anthropic" || rec.ModelServedFrom != "hosted" {
		t.Fatalf("model not settled: %+v", rec)
	}
}

func TestTaskRecordRepo_SessionDeleteStampsCancelsAndTheSweepClaimsEachOnce(t *testing.T) {
	db := newTestDB(t)
	records := NewTaskRecordRepo(db, "gw-test")
	otherGateway := NewTaskRecordRepo(db, "gw-other")
	sessions := NewSessionRepo(db)
	ctx := context.Background()
	sessionID := idgen.New()
	name, source := "s", ""
	if err := sessions.Create(ctx, &Session{ID: sessionID, UserID: "u1", Name: &name, Source: &source}); err != nil {
		t.Fatal(err)
	}
	inSession, inOtherSession, inOtherGateway := idgen.New(), idgen.New(), idgen.New()
	for repo, rec := range map[*TaskRecordRepo]*TaskRecord{
		records:      {TaskID: inSession, AgentName: "a", UserID: "u1", SessionID: sessionID},
		otherGateway: {TaskID: inOtherGateway, AgentName: "a", UserID: "u1", SessionID: sessionID},
	} {
		if _, err := repo.Upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := records.Upsert(ctx, &TaskRecord{TaskID: inOtherSession, AgentName: "a", UserID: "u1", SessionID: idgen.New()}); err != nil {
		t.Fatal(err)
	}

	if _, err := sessions.SoftDelete(ctx, sessionID, "u1"); err != nil {
		t.Fatal(err)
	}

	first, err := records.ClaimCancels(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].TaskID != inSession || first[0].AgentName != "a" {
		t.Fatalf("the claim must return this gateway's task of the deleted session, got %+v", first)
	}
	if second, _ := records.ClaimCancels(ctx, 10); len(second) != 0 {
		t.Fatalf("a claimed row must not be claimed again, got %+v", second)
	}
	if theirs, _ := otherGateway.ClaimCancels(ctx, 10); len(theirs) != 1 || theirs[0].TaskID != inOtherGateway {
		t.Fatalf("each gateway app claims its own rows, got %+v", theirs)
	}
	if rec, _ := records.Get(ctx, inOtherSession); rec.CancelRequestedAt != nil {
		t.Fatalf("a task of a live session owes no cancel, got requested at %d", *rec.CancelRequestedAt)
	}
	if rec, _ := records.Get(ctx, inSession); rec.CancelRequestedAt == nil || rec.CancelClaimedAt == nil {
		t.Fatalf("a claimed row carries both marks: %+v", rec)
	}
}

func TestTaskRecordRepo_AnExpiredClaimIsClaimedAgain(t *testing.T) {
	db := newTestDB(t)
	records := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	requested := time.Now().Add(-time.Hour).UnixMilli()
	fresh := time.Now().UnixMilli()
	expired := time.Now().Add(-CancelClaimLease - time.Second).UnixMilli()
	held, lapsed := idgen.New(), idgen.New()
	for _, rec := range []*TaskRecord{
		{TaskID: held, AgentName: "a", UserID: "u1", CancelRequestedAt: &requested, CancelClaimedAt: &fresh},
		{TaskID: lapsed, AgentName: "a", UserID: "u1", CancelRequestedAt: &requested, CancelClaimedAt: &expired},
	} {
		if _, err := records.Upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := records.ClaimCancels(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].TaskID != lapsed {
		t.Fatalf("only the row whose lease ran out may be claimed again, got %+v", claimed)
	}
	if *claimed[0].CancelClaimedAt <= expired {
		t.Fatalf("the new claim must carry a fresh stamp, got %d", *claimed[0].CancelClaimedAt)
	}
	if again, _ := records.ClaimCancels(ctx, 10); len(again) != 0 {
		t.Fatalf("a row just claimed is held for the lease, got %+v", again)
	}
}

func TestTaskRecordRepo_StampSeen(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	present, gone := idgen.New(), idgen.New()
	stale := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: present, AgentName: "a", UserID: "u1", LastSeenAt: stale}); err != nil {
		t.Fatal(err)
	}

	seen, err := repo.StampSeen(ctx, []string{present, gone})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != present {
		t.Fatalf("want only the id that still has a row, got %v", seen)
	}
	rec, err := repo.Get(ctx, present)
	if err != nil {
		t.Fatal(err)
	}
	if rec.LastSeenAt <= stale {
		t.Errorf("the stamp must move forward: was %d, now %d", stale, rec.LastSeenAt)
	}

	if seen, err := repo.StampSeen(ctx, nil); err != nil || seen != nil {
		t.Fatalf("no ids: want nil, nil; got %v, %v", seen, err)
	}
	tooMany := make([]string, dbutil.InClauseCap+1)
	for i := range tooMany {
		tooMany[i] = idgen.New()
	}
	if _, err := repo.StampSeen(ctx, tooMany); err == nil {
		t.Fatal("a batch above the IN clause cap must be refused, not truncated")
	}
}

func TestTaskRecordRepo_DeleteUnseenBefore(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	now := time.Now()

	abandoned := &TaskRecord{TaskID: idgen.New(), AgentName: "a", UserID: "u1", LastSeenAt: now.Add(-2 * time.Hour).UnixMilli()}
	parkedOnAHuman := &TaskRecord{TaskID: idgen.New(), AgentName: "a", UserID: "u1",
		CreatedAt: now.Add(-48 * time.Hour).UnixMilli(), LastSeenAt: now.UnixMilli()}
	for _, rec := range []*TaskRecord{abandoned, parkedOnAHuman} {
		if _, err := repo.Upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := repo.DeleteUnseenBefore(ctx, now.Add(-30*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("want only the abandoned row deleted, got %d", deleted)
	}
	if exists, _ := repo.Exists(ctx, parkedOnAHuman.TaskID); !exists {
		t.Fatal("a two-day-old task whose owner is alive must keep its row")
	}
}

func TestTaskRecordRepo_ReaperKeepsARowRestampedWhileItWaited(t *testing.T) {
	db := newTestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("SQLite serialises writers; the interleaving needs Postgres row locks")
	}
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	now := time.Now()
	id := idgen.New()
	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1", LastSeenAt: now.Add(-2 * time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}

	tx, err := db.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE task_records SET last_seen_at = $1 WHERE task_id = $2`, now.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}

	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := repo.DeleteUnseenBefore(ctx, now.Add(-30*time.Minute), 10)
		done <- result{n, err}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.RawDB().QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			  AND query LIKE 'DELETE FROM task_records%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reaper never waited on the restamp's row lock; the test models nothing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.n != 0 {
		t.Errorf("the reaper deleted %d rows; a row restamped while it waited must be kept", res.n)
	}
	if exists, _ := repo.Exists(ctx, id); !exists {
		t.Fatal("a row restamped while the reaper waited on it must survive")
	}
}

func TestTaskRecordRepo_UpsertStampsLiveness(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	id := idgen.New()
	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	rec, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.LastSeenAt == 0 || rec.LastSeenAt != rec.CreatedAt {
		t.Fatalf("a new row must be stamped as seen when it is created: %+v", rec)
	}
}

func TestTaskRecordRepo_ClosedDatabaseReportsErrors(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := context.Background()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	checks := map[string]func() error{
		"Upsert": func() error {
			_, err := repo.Upsert(ctx, &TaskRecord{TaskID: "x", AgentName: "a", UserID: "u"})
			return err
		},
		"Get":                func() error { _, err := repo.Get(ctx, "x"); return err },
		"SettleModel":        func() error { return repo.SettleModel(ctx, "x", usagefacts.Model{Family: "llama"}) },
		"Exists":             func() error { _, err := repo.Exists(ctx, "x"); return err },
		"ClaimRunReport":     func() error { _, err := repo.ClaimRunReport(ctx, "x"); return err },
		"Delete":             func() error { return repo.Delete(ctx, "x") },
		"StampSeen":          func() error { _, err := repo.StampSeen(ctx, []string{"x"}); return err },
		"ExistingIDs":        func() error { _, err := repo.ExistingIDs(ctx, []string{"x"}); return err },
		"ClaimCancels":       func() error { _, err := repo.ClaimCancels(ctx, 10); return err },
		"DeleteUnseenBefore": func() error { _, err := repo.DeleteUnseenBefore(ctx, time.Now(), 10); return err },
		"Take":               func() error { _, err := repo.Take(ctx, "x", "i"); return err },
		"TakeAll":            func() error { _, err := repo.TakeAll(ctx, "i"); return err },
		"SetWaiting":         func() error { return repo.SetWaiting(ctx, "x", "r") },
		"ClearWaiting":       func() error { _, err := repo.ClearWaiting(ctx, "x", "r"); return err },
	}
	for name, call := range checks {
		if err := call(); err == nil {
			t.Errorf("%s must report an error on a closed database", name)
		}
	}
}

func TestTaskRecordRepo_TheCancelClaimIsScopedByAppAndLookupsAreNot(t *testing.T) {
	db := newTestDB(t)
	webui := NewTaskRecordRepo(db, "webui")
	slack := NewTaskRecordRepo(db, "slack")
	sessions := NewSessionRepo(db)
	ctx := context.Background()
	sessionID := idgen.New()
	name, source := "s", ""
	if err := sessions.Create(ctx, &Session{ID: sessionID, UserID: "u1", Name: &name, Source: &source}); err != nil {
		t.Fatal(err)
	}
	webuiTask, slackTask := idgen.New(), idgen.New()
	if _, err := webui.Upsert(ctx, &TaskRecord{TaskID: webuiTask, AgentName: "agent-x", UserID: "u1", SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := slack.Upsert(ctx, &TaskRecord{TaskID: slackTask, AgentName: "agent-x", UserID: "u1", SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.SoftDelete(ctx, sessionID, "u1"); err != nil {
		t.Fatal(err)
	}

	claimed, err := webui.ClaimCancels(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].TaskID != webuiTask {
		t.Fatalf("an app must claim only its own rows, got %+v", claimed)
	}
	if exists, _ := slack.Exists(ctx, webuiTask); !exists {
		t.Fatal("a lookup by task id stays visible to every gateway app")
	}
}

func TestTaskRecordRepo_TakeNamesTheInstanceAndRestamps(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := t.Context()
	id := idgen.New()
	stale := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1", LastSeenAt: stale}); err != nil {
		t.Fatal(err)
	}

	taken, err := repo.Take(ctx, id, "instance-b")
	if err != nil {
		t.Fatal(err)
	}
	if taken == nil || taken.GatewayInstanceID != "instance-b" || taken.AgentName != "a" {
		t.Fatalf("Take must return the row named with the caller: %+v", taken)
	}
	if taken.LastSeenAt <= stale {
		t.Errorf("Take must restamp: was %d, now %d", stale, taken.LastSeenAt)
	}

	again, err := repo.Take(ctx, id, "instance-c")
	if err != nil {
		t.Fatal(err)
	}
	if again.GatewayInstanceID != "instance-c" {
		t.Fatalf("a later Take replaces the name, got %q", again.GatewayInstanceID)
	}

	gone, err := repo.Take(ctx, idgen.New(), "instance-c")
	if err != nil || gone != nil {
		t.Fatalf("Take on an absent row: want nil, nil; got %+v, %v", gone, err)
	}
}

func TestTaskRecordRepo_TakeAllTakesEveryRowOfThisAppOnly(t *testing.T) {
	db := newTestDB(t)
	webui := NewTaskRecordRepo(db, "webui")
	slack := NewTaskRecordRepo(db, "slack")
	ctx := t.Context()
	stale := time.Now().Add(-time.Hour).UnixMilli()
	fresh, byOther, mine, theirs := idgen.New(), idgen.New(), idgen.New(), idgen.New()
	for repo, rec := range map[*TaskRecordRepo]*TaskRecord{
		webui: {TaskID: fresh, AgentName: "a", UserID: "u1", LastSeenAt: stale},
		slack: {TaskID: theirs, AgentName: "a", UserID: "u1", GatewayInstanceID: "slack-1", LastSeenAt: stale},
	} {
		if _, err := repo.Upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := webui.Upsert(ctx, &TaskRecord{TaskID: byOther, AgentName: "a", UserID: "u1", GatewayInstanceID: "webui-dead", LastSeenAt: stale}); err != nil {
		t.Fatal(err)
	}
	if _, err := webui.Upsert(ctx, &TaskRecord{TaskID: mine, AgentName: "a", UserID: "u1", GatewayInstanceID: "webui-1", LastSeenAt: stale}); err != nil {
		t.Fatal(err)
	}

	taken, err := webui.TakeAll(ctx, "webui-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(taken) != 3 {
		t.Fatalf("want every row of the app, got %d: %+v", len(taken), taken)
	}
	for _, rec := range taken {
		if rec.GatewayID != "webui" || rec.GatewayInstanceID != "webui-1" {
			t.Errorf("every row returned is this app's and named with the caller: %+v", rec)
		}
		if rec.LastSeenAt <= stale {
			t.Errorf("every row returned is restamped: %+v", rec)
		}
	}
	other, err := slack.Get(ctx, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if other.GatewayInstanceID != "slack-1" || other.LastSeenAt != stale {
		t.Fatalf("another app's row must not be touched: %+v", other)
	}
}

func TestTaskRecordRepo_WaitingIsClearedOnlyForTheAnsweredRequest(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := t.Context()
	id := idgen.New()
	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}

	if err := repo.SetWaiting(ctx, id, "req-1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetWaiting(ctx, id, "req-2"); err != nil {
		t.Fatal(err)
	}
	rec, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.WaitingOn() != "req-2" {
		t.Fatalf("a later request replaces the earlier one, got %q", rec.WaitingOn())
	}

	if cleared, clearErr := repo.ClearWaiting(ctx, id, "req-1"); clearErr != nil || cleared {
		t.Fatalf("an answer to the earlier request must not end the later wait: cleared %v, err %v", cleared, clearErr)
	}
	if cleared, clearErr := repo.ClearWaiting(ctx, id, ""); clearErr != nil || cleared {
		t.Fatalf("an empty request id clears nothing: cleared %v, err %v", cleared, clearErr)
	}
	if cleared, clearErr := repo.ClearWaiting(ctx, id, "req-2"); clearErr != nil || !cleared {
		t.Fatalf("the answer to the named request ends the wait: cleared %v, err %v", cleared, clearErr)
	}
	if cleared, clearErr := repo.ClearWaiting(ctx, id, "req-2"); clearErr != nil || cleared {
		t.Fatalf("a second answer finds nothing to clear: cleared %v, err %v", cleared, clearErr)
	}
	rec, err = repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.WaitingRequestID == nil || *rec.WaitingRequestID != "" {
		t.Fatalf("the wait must be over and recorded as such, got %v", rec.WaitingRequestID)
	}
	if err := repo.SetWaiting(ctx, idgen.New(), "req-3"); err != nil {
		t.Fatalf("setting the flag on an absent row must not fail: %v", err)
	}
}

func TestTaskRecordRepo_UpsertLeavesInstanceAndWaitingOnAnExistingRow(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := t.Context()
	id := idgen.New()
	first, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1", GatewayInstanceID: "i1", WaitingRequestID: strp("r1")})
	if err != nil {
		t.Fatal(err)
	}
	if first.GatewayInstanceID != "i1" || first.WaitingOn() != "r1" {
		t.Fatalf("the first call writes both: %+v", first)
	}
	second, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1", GatewayInstanceID: "i2", WaitingRequestID: strp("r2")})
	if err != nil {
		t.Fatal(err)
	}
	if second.GatewayInstanceID != "i1" || second.WaitingOn() != "r1" {
		t.Fatalf("a later call changes neither: %+v", second)
	}
}

func strp(s string) *string { return &s }

func TestTaskRecordRepo_UpsertRecordsNoWaitAsNull(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := t.Context()
	rec, err := repo.Upsert(ctx, &TaskRecord{TaskID: idgen.New(), AgentName: "a", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.WaitingRequestID != nil {
		t.Fatalf("a new row records no wait, got %q", *rec.WaitingRequestID)
	}
}

func TestTaskRecordRepo_ClearWaitingEndsTheWaitOfARowWithNoFlag(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRecordRepo(db, "gw-test")
	ctx := t.Context()
	id := idgen.New()
	if _, err := repo.Upsert(ctx, &TaskRecord{TaskID: id, AgentName: "a", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	rec, err := repo.Get(ctx, id)
	if err != nil || rec.WaitingRequestID != nil {
		t.Fatalf("setup: the row must read as having no flag, got %v, %v", rec.WaitingRequestID, err)
	}

	cleared, err := repo.ClearWaiting(ctx, id, "req-old")
	if err != nil || !cleared {
		t.Fatalf("the answer must end the wait of a row with no flag: cleared %v, err %v", cleared, err)
	}
	rec, err = repo.Get(ctx, id)
	if err != nil || rec.WaitingRequestID == nil || *rec.WaitingRequestID != "" {
		t.Fatalf("the row must now record no wait, got %v, %v", rec.WaitingRequestID, err)
	}
}
