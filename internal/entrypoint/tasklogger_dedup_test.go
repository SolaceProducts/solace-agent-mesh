// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/tracededup"
)

const dedupTestTaskID = "10000000-0000-0000-0000-0000000000dd"

const dedupTestPayload = `{"jsonrpc":"2.0","id":"` + dedupTestTaskID + `","result":{"kind":"status-update","final":false,"taskId":"` + dedupTestTaskID + `","contextId":"ctx","metadata":{"agent_name":"Orchestrator"},"status":{"state":"working","message":{"kind":"message","role":"agent","messageId":"m1","parts":[{"kind":"data","data":{"type":"llm_invocation","usage":null,"request":{"model":"openai/gpt","contents":[{"role":"user","parts":[{"text":"hello"}]}],"config":{"system_instruction":"You are the Orchestrator. You coordinate specialist agents, keep the user informed of progress, prefer the smallest tool that answers the question, never fabricate results, and summarise what was done at the end of every task in two or three sentences so that a busy reader can act on it without opening the transcript.","tools":[{"function_declarations":[{"name":"a_tool","description":"a"},{"name":"b_tool","description":"b"}]}]}}}}]}}}}`

const dedupTestForwardedPayload = `{"jsonrpc":"2.0","id":"` + dedupTestTaskID + `","result":{"kind":"status-update","final":false,"taskId":"` + dedupTestTaskID + `","contextId":"ctx","status":{"state":"working","message":{"kind":"message","role":"agent","messageId":"m2","metadata":{"agent_name":"Orchestrator","forwarded_from_peer":"Researcher"},"parts":[{"kind":"data","data":{"type":"llm_invocation","usage":null,"request":{"model":"openai/gpt","contents":[{"role":"user","parts":[{"text":"hello"}]}],"config":{"system_instruction":"You are the Researcher. You search, read, and report findings with sources, and you never speculate beyond what the documents you retrieved say. Keep answers under two hundred words unless asked otherwise.","tools":[{"function_declarations":[{"name":"web_search","description":"s"}]}]}}}}]}}}}`

func dedupJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return v
}

func newDedupTestService(t *testing.T) (*TaskLoggerService, *store.TaskEventRepo, *store.DB) {
	t.Helper()
	db := store.TestDB(t)
	ctx := context.Background()
	if err := store.NewTaskRepo(db).Create(ctx, &store.Task{ID: dedupTestTaskID, UserID: "u1", StartTime: 1, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	eventRepo := store.NewTaskEventRepo(db)
	s := &TaskLoggerService{
		eventRepo:    eventRepo,
		logger:       slog.Default(),
		pendingBlobs: make(map[string]*store.TaskEventBlob),
		seenBlobs:    make(map[string]int64),
	}
	return s, eventRepo, db
}

func minBlobLastSeen(t *testing.T, db *store.DB) int64 {
	t.Helper()
	var got int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT COALESCE(MIN(last_seen), 0) FROM task_event_blobs`).Scan(&got); err != nil {
		t.Fatalf("read min last_seen: %v", err)
	}
	return got
}

func stompBlobLastSeen(t *testing.T, db *store.DB) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `UPDATE task_event_blobs SET last_seen = 1`); err != nil {
		t.Fatalf("stomp last_seen: %v", err)
	}
}

func TestTaskLogger_DedupsConfigOnWriteAndRehydratesOnRead(t *testing.T) {
	s, eventRepo, db := newDedupTestService(t)
	ctx := context.Background()
	seq := int64(7)
	evt := TaskLogEvent{TaskID: dedupTestTaskID, Direction: "status", EventKind: "status-update",
		Payload: []byte(dedupTestPayload), EventSeq: &seq, PublisherID: "agent-1"}

	var batch []*store.TaskEvent
	for i := range 3 {
		te := buildTaskEvent(evt, int64(1000+i))
		s.dedupAndClassify(te, evt, int64(1000+i))
		if !tracededup.HasRefs([]byte(te.Payload)) {
			t.Fatalf("row %d still carries the inline config block", i)
		}
		if len(te.Payload) >= len(dedupTestPayload) {
			t.Fatalf("row %d not smaller after dedup: %d >= %d", i, len(te.Payload), len(dedupTestPayload))
		}
		batch = append(batch, te)
	}
	if len(s.pendingBlobs) != 2 {
		t.Fatalf("pending blobs = %d, want 2 (one prompt, one tool block, shared by all rows)", len(s.pendingBlobs))
	}
	s.flushTaskEvents(ctx, batch)
	if len(s.pendingBlobs) != 0 || len(s.seenBlobs) != 2 {
		t.Fatalf("after flush pending=%d seen=%d, want 0/2", len(s.pendingBlobs), len(s.seenBlobs))
	}

	n, _, err := eventRepo.Blobs().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("blob rows = %d, want 2", n)
	}
	got, err := eventRepo.GetByTaskID(ctx, dedupTestTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("events = %d, want 3", len(got))
	}
	for _, e := range got {
		if !reflect.DeepEqual(dedupJSON(t, e.Payload), dedupJSON(t, dedupTestPayload)) {
			t.Fatalf("rehydrated payload differs from the received event:\n%s", e.Payload)
		}
		if e.SignalType == nil || *e.SignalType != "llm_invocation" || e.AgentName == nil || *e.AgentName != "Orchestrator" {
			t.Fatalf("typed columns not stamped: signal=%v agent=%v", e.SignalType, e.AgentName)
		}
		if e.EventSeq == nil || *e.EventSeq != 7 || e.PublisherID == nil || *e.PublisherID != "agent-1" {
			t.Fatalf("sequence columns not stamped: seq=%v publisher=%v", e.EventSeq, e.PublisherID)
		}
		if e.ConfigPromptBlob == nil || e.ConfigToolsBlob == nil || *e.ConfigPromptBlob == *e.ConfigToolsBlob {
			t.Fatalf("config hash columns not stamped: prompt=%v tools=%v", e.ConfigPromptBlob, e.ConfigToolsBlob)
		}
		if e.ForwardedFrom != nil {
			t.Fatalf("direct event stamped forwarded_from %q", *e.ForwardedFrom)
		}
	}

	te := buildTaskEvent(evt, 2000)
	s.dedupAndClassify(te, evt, time.Now().UnixMilli())
	if len(s.pendingBlobs) != 0 {
		t.Fatalf("repeat config queued %d blobs, want 0", len(s.pendingBlobs))
	}

	var stampedAt int64
	for _, seenAt := range s.seenBlobs {
		stampedAt = max(stampedAt, seenAt)
	}
	stompBlobLastSeen(t, db)
	stale := buildTaskEvent(evt, 1500)
	s.dedupAndClassify(stale, evt, stampedAt+seenBlobTTL.Milliseconds())
	if len(s.pendingBlobs) != 2 {
		t.Fatalf("expired cache entries queued %d blobs, want 2", len(s.pendingBlobs))
	}
	s.flushTaskEvents(ctx, []*store.TaskEvent{stale})
	if len(s.pendingBlobs) != 0 || len(s.seenBlobs) != 2 {
		t.Fatalf("after re-upsert pending=%d seen=%d, want 0/2", len(s.pendingBlobs), len(s.seenBlobs))
	}
	for h, seenAt := range s.seenBlobs {
		if seenAt < stampedAt {
			t.Fatalf("blob %s cache stamp %d not refreshed by the re-upsert (was %d)", h, seenAt, stampedAt)
		}
	}
	if last := minBlobLastSeen(t, db); last <= 1 {
		t.Fatalf("min blob last_seen = %d, want the re-upsert to bump it past the stomped 1", last)
	}

	fwd := buildTaskEvent(TaskLogEvent{TaskID: dedupTestTaskID, Direction: "status", EventKind: "status-update",
		Payload: []byte(dedupTestForwardedPayload)}, 2500)
	s.dedupAndClassify(fwd, evt, 2500)
	s.flushTaskEvents(ctx, []*store.TaskEvent{fwd})
	got, err = eventRepo.GetByTaskID(ctx, dedupTestTaskID)
	if err != nil {
		t.Fatal(err)
	}
	last := got[len(got)-1]
	if last.AgentName == nil || *last.AgentName != "Orchestrator" || last.ForwardedFrom == nil || *last.ForwardedFrom != "Researcher" {
		t.Fatalf("forwarded row agent/forwarded = %v/%v, want Orchestrator/Researcher", last.AgentName, last.ForwardedFrom)
	}
}

func TestTaskLogger_RowsWrittenBeforeDedupReadBackUnchanged(t *testing.T) {
	s, eventRepo, _ := newDedupTestService(t)
	ctx := context.Background()

	legacyIDs := []string{"b0000000-0000-0000-0000-0000000000f1", "b0000000-0000-0000-0000-0000000000f2"}
	legacyPayloads := []string{dedupTestPayload, dedupTestForwardedPayload}
	for i, id := range legacyIDs {
		if err := eventRepo.Create(ctx, &store.TaskEvent{ID: id, TaskID: dedupTestTaskID, CreatedTime: int64(i + 1), Topic: "t", Direction: "status", Payload: legacyPayloads[i]}); err != nil {
			t.Fatal(err)
		}
	}
	evt := TaskLogEvent{TaskID: dedupTestTaskID, Direction: "status", EventKind: "status-update", Payload: []byte(dedupTestPayload)}
	live := buildTaskEvent(evt, 1000)
	s.dedupAndClassify(live, evt, 1000)
	s.flushTaskEvents(ctx, []*store.TaskEvent{live})

	got, err := eventRepo.GetByTaskID(ctx, dedupTestTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("events = %d, want 2 legacy rows and 1 live row", len(got))
	}
	for i, e := range got[:2] {
		if e.Payload != legacyPayloads[i] {
			t.Fatalf("legacy row %s read back changed:\n%s", e.ID, e.Payload)
		}
		if e.SignalType != nil || e.AgentName != nil || e.ConfigPromptBlob != nil || e.ConfigToolsBlob != nil || e.ForwardedFrom != nil {
			t.Fatalf("legacy row %s gained typed columns: %+v", e.ID, e)
		}
	}
	if !reflect.DeepEqual(dedupJSON(t, got[2].Payload), dedupJSON(t, dedupTestPayload)) {
		t.Fatalf("live row does not rehydrate to the received event:\n%s", got[2].Payload)
	}
	if got[2].ConfigPromptBlob == nil || got[2].ConfigToolsBlob == nil {
		t.Fatalf("live row not stamped: prompt=%v tools=%v", got[2].ConfigPromptBlob, got[2].ConfigToolsBlob)
	}
}

const dedupTestDivergentPayload = `{"jsonrpc":"2.0","id":"` + dedupTestTaskID + `","result":{"kind":"status-update","final":false,"taskId":"` + dedupTestTaskID + `","metadata":{"agent_name":"Orchestrator"},"status":{"state":"working","message":{"kind":"message","parts":[{"kind":"data","data":{"type":"llm_invocation","request":{"model":"openai/gpt","contents":[],"config":{"system_instruction":"You are the first agent.","tools":[{"function_declarations":[{"name":"a_tool","description":"a"}]}]}}}},{"kind":"data","data":{"type":"llm_invocation","request":{"model":"openai/gpt","contents":[],"config":{"system_instruction":"You are the second agent.","tools":[{"function_declarations":[{"name":"a_tool","description":"a"}]}]}}}}]}}}}`

func TestTaskLogger_DivergentConfigStaysInline(t *testing.T) {
	s, eventRepo, _ := newDedupTestService(t)
	ctx := context.Background()

	evt := TaskLogEvent{TaskID: dedupTestTaskID, UserID: "u1", Topic: "t", Direction: "status", EventKind: "status-update", Payload: []byte(dedupTestDivergentPayload)}
	te := buildTaskEvent(evt, 1000)
	s.dedupAndClassify(te, evt, 1000)
	if te.Payload != dedupTestDivergentPayload {
		t.Fatalf("live path altered a divergent payload:\n%s", te.Payload)
	}
	if te.ConfigPromptBlob != nil || te.ConfigToolsBlob != nil || len(s.pendingBlobs) != 0 {
		t.Fatalf("live path stamped prompt=%v tools=%v pending=%d, want none", te.ConfigPromptBlob, te.ConfigToolsBlob, len(s.pendingBlobs))
	}
	if te.SignalType == nil || *te.SignalType != "llm_invocation" || te.AgentName == nil || *te.AgentName != "Orchestrator" {
		t.Fatalf("live path lost typed columns: signal=%v agent=%v", te.SignalType, te.AgentName)
	}

	s.flushTaskEvents(ctx, []*store.TaskEvent{te})
	got, err := eventRepo.GetByTaskID(ctx, dedupTestTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Payload != dedupTestDivergentPayload || got[0].ConfigPromptBlob != nil || got[0].ConfigToolsBlob != nil {
		t.Fatalf("stored divergent row = %+v, want it inline and unstamped", got)
	}
	n, _, err := eventRepo.Blobs().Count(ctx)
	if err != nil || n != 0 {
		t.Fatalf("blobs written for a divergent payload: n=%d err=%v", n, err)
	}
}
