// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	rosterTaskID    = "10000000-0000-0000-0000-0000000000d8"
	rosterOtherTask = "10000000-0000-0000-0000-0000000000d9"
	rosterHash      = "sha256:roster"
	rosterContent   = `[{"function_declarations":[{"description":"d","name":"t","parameters":{"type":"object"}}]}]`
)

func TestB08_RosterBlobContentIsImmutableForItsHash(t *testing.T) {
	db := newTestDB(t)
	blobs := NewTaskEventBlobRepo(db)
	ctx := t.Context()

	if err := blobs.UpsertBatch(ctx, []*TaskEventBlob{
		{Hash: rosterHash, Kind: "tools", Content: rosterContent, ByteLen: len(rosterContent), FirstSeen: 1000, LastSeen: 1000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := blobs.UpsertBatch(ctx, []*TaskEventBlob{
		{Hash: rosterHash, Kind: "system_instruction", Content: "tampered", ByteLen: 8, FirstSeen: 9000, LastSeen: 9000},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := dbutil.QueryRow[TaskEventBlob](ctx, db,
		`SELECT hash, kind, content, byte_len, first_seen, last_seen FROM task_event_blobs WHERE hash = ?`, rosterHash)
	if err != nil || got == nil {
		t.Fatalf("read blob: %v %v", got, err)
	}
	if got.Content != rosterContent || got.Kind != "tools" || got.ByteLen != len(rosterContent) || got.FirstSeen != 1000 {
		t.Fatalf("re-upsert rewrote the blob: %+v", got)
	}
	if got.LastSeen != 9000 {
		t.Fatalf("last_seen = %d, want the re-upsert's 9000", got.LastSeen)
	}
}

func TestB08_RosterLookupByTaskIDIgnoresEventFiltering(t *testing.T) {
	db := newTestDB(t)
	tasks := NewTaskRepo(db)
	events := NewTaskEventRepo(db)
	ctx := t.Context()
	for _, id := range []string{rosterTaskID, rosterOtherTask} {
		if err := tasks.Create(ctx, &Task{ID: id, UserID: "u1", StartTime: 1, ExecutionMode: "foreground"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := events.Blobs().UpsertBatch(ctx, []*TaskEventBlob{
		{Hash: rosterHash, Kind: "tools", Content: rosterContent, ByteLen: len(rosterContent), FirstSeen: 1, LastSeen: 1},
	}); err != nil {
		t.Fatal(err)
	}
	llmInvocation := `{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"llm_invocation","request":{"config":{"tools":{"$blob":"sha256:roster"}}}}}]}}}}`
	if err := events.CreateBatch(ctx, []*TaskEvent{
		{ID: "b0000000-0000-0000-0000-0000000000f1", TaskID: rosterTaskID, CreatedTime: 1, Topic: "t", Direction: "request", Payload: `{}`, SignalType: new("request")},
		{ID: "b0000000-0000-0000-0000-0000000000f2", TaskID: rosterTaskID, CreatedTime: 2, Topic: "t", Direction: "status", Payload: llmInvocation, SignalType: new("llm_invocation"), ConfigToolsBlob: new(rosterHash)},
		{ID: "b0000000-0000-0000-0000-0000000000f3", TaskID: rosterTaskID, CreatedTime: 3, Topic: "t", Direction: "status", Payload: `{}`, SignalType: new("tool_result")},
		{ID: "b0000000-0000-0000-0000-0000000000f4", TaskID: rosterOtherTask, CreatedTime: 4, Topic: "t", Direction: "status", Payload: `{}`, SignalType: new("llm_invocation")},
	}); err != nil {
		t.Fatal(err)
	}

	judgeStream, err := dbutil.QueryRowPtrs[TaskEvent](ctx, db,
		`SELECT id, task_id, signal_type FROM task_events WHERE task_id = ? AND signal_type <> 'llm_invocation'`, rosterTaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range judgeStream {
		if e.ConfigToolsBlob != nil {
			t.Fatal("the filtered judge stream unexpectedly carries the roster reference")
		}
	}

	for _, tc := range []struct {
		name   string
		taskID string
		want   []string
	}{
		{"B08_task_with_llm_invocation_finds_roster", rosterTaskID, []string{rosterContent}},
		{"B08_task_without_roster_finds_none", rosterOtherTask, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hashes, err := dbutil.QueryVals[string](ctx, db,
				`SELECT DISTINCT config_tools_blob FROM task_events WHERE task_id = ? AND config_tools_blob IS NOT NULL`, tc.taskID)
			if err != nil {
				t.Fatal(err)
			}
			contents, err := events.Blobs().GetContentByHashes(ctx, hashes)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, h := range hashes {
				got = append(got, contents[h])
			}
			if len(got) != len(tc.want) || (len(got) == 1 && got[0] != tc.want[0]) {
				t.Fatalf("roster for task %s = %v, want %v", tc.taskID, got, tc.want)
			}
		})
	}
}
