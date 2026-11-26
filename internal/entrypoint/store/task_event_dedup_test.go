// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/tracededup"
)

const dedupTaskID = "10000000-0000-0000-0000-0000000000dd"

const llmInvocationPayload = `{"jsonrpc":"2.0","id":"` + dedupTaskID + `","result":{"kind":"status-update","final":false,"taskId":"` + dedupTaskID + `","contextId":"ctx","metadata":{"agent_name":"Orchestrator"},"status":{"state":"working","message":{"kind":"message","role":"agent","messageId":"m1","parts":[{"kind":"data","data":{"type":"llm_invocation","usage":null,"request":{"model":"openai/gpt","contents":[{"role":"user","parts":[{"text":"hello"}]}],"config":{"system_instruction":"You are the Orchestrator. You coordinate specialist agents, keep the user informed of progress, prefer the smallest tool that answers the question, never fabricate results, and summarise what was done at the end of every task in two or three sentences so that a busy reader can act on it without opening the transcript.","tools":[{"function_declarations":[{"name":"a_tool","description":"a"},{"name":"b_tool","description":"b"}]}]}}}}]}}}}`

func jsonValue(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return v
}

func createDedupTask(t *testing.T, db *DB) {
	t.Helper()
	if err := NewTaskRepo(db).Create(context.Background(), &Task{ID: dedupTaskID, UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskEventRepo_DedupedPayloadRehydratesOnRead(t *testing.T) {
	db := newTestDB(t)
	createDedupTask(t, db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	res := tracededup.Process([]byte(llmInvocationPayload))
	if !res.Changed || len(res.Blobs) != 2 {
		t.Fatalf("fixture did not dedup: changed=%v blobs=%d", res.Changed, len(res.Blobs))
	}
	blobs := make([]*TaskEventBlob, 0, len(res.Blobs))
	for _, b := range res.Blobs {
		blobs = append(blobs, &TaskEventBlob{Hash: b.Hash, Kind: b.Kind, Content: b.Content, ByteLen: len(b.Content), FirstSeen: 1, LastSeen: 1})
	}
	if err := repo.Blobs().UpsertBatch(ctx, blobs); err != nil {
		t.Fatal(err)
	}
	if err := repo.Blobs().UpsertBatch(ctx, blobs); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	signal := "llm_invocation"
	if err := repo.Create(ctx, &TaskEvent{
		ID: "b0000000-0000-0000-0000-0000000000d1", TaskID: dedupTaskID, CreatedTime: 1000,
		Topic: "t", Direction: "status", Payload: string(res.Payload), SignalType: &signal,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByTaskID(ctx, dedupTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	if !reflect.DeepEqual(jsonValue(t, got[0].Payload), jsonValue(t, llmInvocationPayload)) {
		t.Fatalf("rehydrated payload differs from original:\n%s", got[0].Payload)
	}
	if got[0].SignalType == nil || *got[0].SignalType != "llm_invocation" {
		t.Fatalf("signal_type not round-tripped: %v", got[0].SignalType)
	}

	n, bytes, err := repo.Blobs().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || bytes == 0 {
		t.Fatalf("blob count = %d/%d bytes, want 2 rows with content", n, bytes)
	}

	if _, err = db.ExecContext(ctx, "DELETE FROM task_event_blobs WHERE hash = ?", res.PromptHash); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetByTaskID(ctx, dedupTaskID)
	if err != nil {
		t.Fatalf("read after blob loss: %v", err)
	}
	if !strings.Contains(got[0].Payload, res.PromptHash) {
		t.Fatalf("swept prompt reference was not left in place:\n%s", got[0].Payload)
	}
	if strings.Contains(got[0].Payload, res.ToolsHash) {
		t.Fatalf("surviving tools blob was not rehydrated:\n%s", got[0].Payload)
	}
}

func TestTaskEventRepo_StampsCommitXIDOnPostgres(t *testing.T) {
	db := newTestDB(t)
	createDedupTask(t, db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, &TaskEvent{ID: "b0000000-0000-0000-0000-0000000000a1", TaskID: dedupTaskID, CreatedTime: 1, Topic: "t", Direction: "status", Payload: `{}`}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateBatch(ctx, []*TaskEvent{
		{ID: "b0000000-0000-0000-0000-0000000000a2", TaskID: dedupTaskID, CreatedTime: 2, Topic: "t", Direction: "status", Payload: `{}`},
		{ID: "b0000000-0000-0000-0000-0000000000a3", TaskID: dedupTaskID, CreatedTime: 3, Topic: "t", Direction: "status", Payload: `{}`},
	}); err != nil {
		t.Fatal(err)
	}
	type row struct {
		N int `db:"n"`
	}
	out, err := dbutil.QueryRow[row](ctx, db, `SELECT COUNT(*) AS n FROM task_events WHERE commit_xid IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	if db.Dialect() == "postgres" {
		want = 3
	}
	if out.N != want {
		t.Fatalf("rows with commit_xid = %d, want %d on %s", out.N, want, db.Dialect())
	}
}

func TestTaskEventRepo_OrdersByCreatedTimeThenID(t *testing.T) {
	db := newTestDB(t)
	createDedupTask(t, db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()

	for _, id := range []string{"b0000000-0000-0000-0000-0000000000c3", "b0000000-0000-0000-0000-0000000000c1", "b0000000-0000-0000-0000-0000000000c2"} {
		if err := repo.Create(ctx, &TaskEvent{ID: id, TaskID: dedupTaskID, CreatedTime: 5000, Topic: "t", Direction: "status", Payload: `{"id":"` + id + `"}`}); err != nil {
			t.Fatal(err)
		}
	}
	for _, fetch := range []func() ([]*TaskEvent, error){
		func() ([]*TaskEvent, error) { return repo.GetByTaskID(ctx, dedupTaskID) },
		func() ([]*TaskEvent, error) { return repo.GetByTaskIDs(ctx, []string{dedupTaskID}) },
	} {
		got, err := fetch()
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, e := range got {
			ids = append(ids, e.ID)
		}
		want := "b0000000-0000-0000-0000-0000000000c1,b0000000-0000-0000-0000-0000000000c2,b0000000-0000-0000-0000-0000000000c3"
		if strings.Join(ids, ",") != want {
			t.Fatalf("order = %v, want id ascending on a created_time tie", ids)
		}
	}
}

func TestTaskEventBlobRepo_DeleteUnreferencedOlderThan(t *testing.T) {
	db := newTestDB(t)
	createDedupTask(t, db)
	repo := NewTaskEventRepo(db)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	old := now - 2*24*60*60*1000

	blobs := []*TaskEventBlob{
		{Hash: "sha256:referenced", Kind: "system_instruction", Content: "p", ByteLen: 1, FirstSeen: old, LastSeen: old},
		{Hash: "sha256:orphan-old", Kind: "tools", Content: "t", ByteLen: 1, FirstSeen: old, LastSeen: old},
		{Hash: "sha256:orphan-old-2", Kind: "tools", Content: "t3", ByteLen: 2, FirstSeen: old, LastSeen: old},
		{Hash: "sha256:orphan-fresh", Kind: "tools", Content: "t2", ByteLen: 2, FirstSeen: now, LastSeen: now},
	}
	if err := repo.Blobs().UpsertBatch(ctx, blobs); err != nil {
		t.Fatal(err)
	}
	ref := "sha256:referenced"
	if err := repo.Create(ctx, &TaskEvent{ID: "b0000000-0000-0000-0000-0000000000b1", TaskID: dedupTaskID, CreatedTime: old,
		Topic: "t", Direction: "status", Payload: `{}`, ConfigPromptBlob: &ref}); err != nil {
		t.Fatal(err)
	}

	deleted, err := repo.Blobs().DeleteUnreferencedOlderThan(ctx, time.UnixMilli(now-60*60*1000), 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	left, err := repo.Blobs().GetContentByHashes(ctx, []string{"sha256:referenced", "sha256:orphan-old", "sha256:orphan-old-2", "sha256:orphan-fresh"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := left["sha256:referenced"]; !ok {
		t.Fatal("referenced blob was swept")
	}
	if _, ok := left["sha256:orphan-fresh"]; !ok {
		t.Fatal("recently seen blob was swept inside the guard")
	}
	if _, ok := left["sha256:orphan-old"]; ok {
		t.Fatal("unreferenced old blob survived")
	}
	if _, ok := left["sha256:orphan-old-2"]; ok {
		t.Fatal("second unreferenced old blob survived: the sweep stopped after one batch")
	}
}

func TestTaskEventBlobRepo_UpsertBatchBumpsLastSeen(t *testing.T) {
	db := newTestDB(t)
	blobs := NewTaskEventRepo(db).Blobs()
	ctx := context.Background()

	if err := blobs.UpsertBatch(ctx, []*TaskEventBlob{
		{Hash: "sha256:rebumped", Kind: "tools", Content: "r", ByteLen: 1, FirstSeen: 1000, LastSeen: 1000},
		{Hash: "sha256:stale", Kind: "tools", Content: "s", ByteLen: 1, FirstSeen: 1000, LastSeen: 1000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := blobs.UpsertBatch(ctx, []*TaskEventBlob{
		{Hash: "sha256:rebumped", Kind: "tools", Content: "r", ByteLen: 1, FirstSeen: 1000, LastSeen: 5000},
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := blobs.DeleteUnreferencedOlderThan(ctx, time.UnixMilli(3000), 10)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want only the hash that was never re-upserted", deleted)
	}
	left, err := blobs.GetContentByHashes(ctx, []string{"sha256:rebumped", "sha256:stale"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := left["sha256:rebumped"]; !ok {
		t.Fatal("re-upserted blob was swept: last_seen was not bumped on conflict")
	}
	if _, ok := left["sha256:stale"]; ok {
		t.Fatal("blob older than the cutoff survived: the cutoff is not being applied")
	}
}
