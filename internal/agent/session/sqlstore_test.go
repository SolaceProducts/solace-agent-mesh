// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var testTemplate *dbtest.Template

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_SESSION_DB_URL"),
		"session",
		func(u string) error {
			db, openErr := dbutil.Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return NewSQLStore(db).Migrate(context.Background())
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest template: %v\n", err)
		os.Exit(1)
	}
	testTemplate = tmpl
	code := m.Run()
	testTemplate.Close()
	os.Exit(code)
}

func newTestSQLStore(t *testing.T) *SQLStore {
	t.Helper()
	t.Parallel()
	db, err := dbutil.Open(testTemplate.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLStore(db)
}

func TestSQLStoreCreateAndGet(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	sess := &Session{
		AppName:   "app",
		UserID:    "user-1",
		SessionID: sessID,
		Messages:  []Message{{Role: "user", Content: "hello", Timestamp: time.Now()}},
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, "app", "user-1", sessID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("session not found")
	}
	if got.AppName != "app" || got.UserID != "user-1" || got.SessionID != sessID {
		t.Errorf("session = %+v", got)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "hello" {
		t.Errorf("messages = %+v", got.Messages)
	}
	if got.Version != 0 {
		t.Errorf("version = %d, want 0", got.Version)
	}
}

func TestSQLStoreGetNotFound(t *testing.T) {
	store := newTestSQLStore(t)
	got, err := store.Get(context.Background(), "app", "user", idgen.New())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil")
	}
}

func TestSQLStoreGetSurfacesCorruptMessageRow(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	ctxID := idgen.New()
	taskID := idgen.New()
	sess := &Session{
		AppName:   "app",
		UserID:    "u",
		SessionID: sessID,
		ContextID: ctxID,
		TaskID:    &taskID,
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO session_messages (app_name, user_id, session_id, seq, message, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"app", "u", sessID, 1, "{not valid json", time.Now().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert corrupt message row: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		`UPDATE sessions SET last_seq = 1 WHERE app_name = ? AND user_id = ? AND session_id = ?`,
		"app", "u", sessID,
	); err != nil {
		t.Fatalf("bump last_seq: %v", err)
	}

	if _, err := store.Get(ctx, "app", "u", sessID); err == nil {
		t.Error("Get: expected error for corrupt message row, got nil")
	}
	if _, err := store.GetByContextAndTask(ctx, "app", "u", ctxID, taskID); err == nil {
		t.Error("GetByContextAndTask: expected error for corrupt message row, got nil")
	}
}

func TestSQLStoreCreateDuplicate(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sess := &Session{AppName: "app", UserID: "u", SessionID: idgen.New()}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, sess); err == nil {
		t.Error("expected error for duplicate")
	}
}

func TestSQLStoreSave(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	sess := &Session{AppName: "app", UserID: "u", SessionID: sessID}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, "app", "u", sessID)
	got.Metadata = map[string]any{"k": "v"}

	if err := store.Save(ctx, got); err != nil {
		t.Fatal(err)
	}

	reloaded, _ := store.Get(ctx, "app", "u", sessID)
	if reloaded.Metadata["k"] != "v" {
		t.Errorf("metadata = %+v, want k=v", reloaded.Metadata)
	}
	if reloaded.Version != 1 {
		t.Errorf("version = %d, want 1", reloaded.Version)
	}
}

func TestSQLStoreSaveDoesNotTouchMessages(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessages(ctx, "app", "u", sessID, []Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, "app", "u", sessID)
	got.Messages = nil
	got.Metadata = map[string]any{"note": "meta-only"}
	if err := store.Save(ctx, got); err != nil {
		t.Fatal(err)
	}

	reloaded, _ := store.Get(ctx, "app", "u", sessID)
	if len(reloaded.Messages) != 2 {
		t.Errorf("Save dropped history: messages = %d, want 2", len(reloaded.Messages))
	}
	if reloaded.Metadata["note"] != "meta-only" {
		t.Errorf("metadata not saved: %+v", reloaded.Metadata)
	}
}

func TestSQLStoreSaveOptimisticLock(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	sess := &Session{AppName: "app", UserID: "u", SessionID: sessID}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	r1, _ := store.Get(ctx, "app", "u", sessID)
	r2, _ := store.Get(ctx, "app", "u", sessID)

	r1.Metadata = map[string]any{"who": "r1"}
	if err := store.Save(ctx, r1); err != nil {
		t.Fatal(err)
	}

	r2.Metadata = map[string]any{"who": "r2"}
	if err := store.Save(ctx, r2); err == nil {
		t.Error("expected optimistic lock failure")
	}
}

func TestSQLStoreDelete(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	sess := &Session{AppName: "app", UserID: "u", SessionID: sessID}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, "app", "u", sessID); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(ctx, "app", "u", sessID)
	if got != nil {
		t.Error("should be deleted")
	}
}

func TestSQLStoreListSessions(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	s1, s2, s3 := idgen.New(), idgen.New(), idgen.New()
	_ = store.Create(ctx, &Session{AppName: "app", UserID: "user1", SessionID: s1,
		Messages: []Message{{Role: "user", Content: "hello"}},
		Metadata: map[string]any{"key": "val1"},
	})
	_ = store.Create(ctx, &Session{AppName: "app", UserID: "user1", SessionID: s2,
		Messages: []Message{{Role: "user", Content: "world"}},
	})
	_ = store.Create(ctx, &Session{AppName: "app", UserID: "user2", SessionID: s3})

	sessions, err := store.ListSessions(ctx, "app", "user1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions for user1, got %d", len(sessions))
	}

	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.SessionID] = true
		if s.AppName != "app" || s.UserID != "user1" {
			t.Errorf("unexpected session: %+v", s)
		}
		if s.Version != 0 {
			t.Errorf("expected version 0, got %d", s.Version)
		}
	}
	if !ids[s1] || !ids[s2] {
		t.Errorf("expected sessions %s and %s, got %v", s1, s2, ids)
	}

	sessions2, err := store.ListSessions(ctx, "app", "user2")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions2) != 1 {
		t.Errorf("expected 1 session for user2, got %d", len(sessions2))
	}
}

func TestSQLStoreListSessionsEmpty(t *testing.T) {
	store := newTestSQLStore(t)

	sessions, err := store.ListSessions(context.Background(), "app", "no-such-user")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected empty slice, got %d sessions", len(sessions))
	}
	if sessions == nil {
		t.Error("expected non-nil empty slice")
	}
}

func seedSession(t *testing.T, store *SQLStore, appName, userID, sessionID string) {
	t.Helper()
	if err := store.Create(context.Background(), &Session{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func TestSQLStoreSaveCheckpoint(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID := idgen.New()
	sessID := idgen.New()
	seedSession(t, store, "app", "u", sessID)

	cp := &TaskCheckpoint{
		TaskID:       taskID,
		AppName:      "app",
		UserID:       "u",
		SessionID:    sessID,
		PendingCount: 2,
		State:        []byte(`{"step": 3}`),
	}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	claimed, _ := store.AdoptCheckpoint(ctx, taskID, "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if claimed != nil {
		t.Error("should not claim with pending > 0")
	}
}

func TestSQLStoreRecordToolResult(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID := idgen.New()
	sessID := idgen.New()
	seedSession(t, store, "app", "u", sessID)

	cp := &TaskCheckpoint{TaskID: taskID, AppName: "app", UserID: "u", SessionID: sessID, PendingCount: 2}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	pending, err := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{
		ToolCallID: "c1", ToolName: "tool_a", Content: "r1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Errorf("pending = %d, want 1", pending)
	}

	pending, err = store.RecordToolResult(ctx, taskID, "", &ToolCallResult{
		ToolCallID: "c2", ToolName: "tool_b", Content: "r2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Errorf("pending = %d, want 0", pending)
	}
}

func TestSQLStoreRecordToolResultNotFound(t *testing.T) {
	store := newTestSQLStore(t)
	_, err := store.RecordToolResult(context.Background(), idgen.New(), "", &ToolCallResult{})
	if err == nil {
		t.Error("expected error")
	}
}

func TestSQLStoreAdoptCheckpoint(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID := idgen.New()
	sessID := idgen.New()
	seedSession(t, store, "app", "u", sessID)

	cp := &TaskCheckpoint{TaskID: taskID, AppName: "app", UserID: "u", SessionID: sessID, PendingCount: 1}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	_, err := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{ToolCallID: "c1", Content: "done"})
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := store.AdoptCheckpoint(ctx, taskID, "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("expected checkpoint")
	}
	if len(claimed.ToolResults) != 1 {
		t.Errorf("results = %d, want 1", len(claimed.ToolResults))
	}
	if string(claimed.State) != "" && claimed.State != nil {
		t.Errorf("state = %q", claimed.State)
	}

	claimed2, _ := store.AdoptCheckpoint(ctx, taskID, "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if claimed2 != nil {
		t.Error("expected nil for already-claimed")
	}
}

func TestSQLStoreDeleteCheckpoint(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID := idgen.New()
	sessID := idgen.New()
	seedSession(t, store, "app", "u", sessID)

	cp := &TaskCheckpoint{TaskID: taskID, AppName: "app", UserID: "u", SessionID: sessID, PendingCount: 0}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteCheckpoint(ctx, taskID); err != nil {
		t.Fatal(err)
	}

	claimed, _ := store.AdoptCheckpoint(ctx, taskID, "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if claimed != nil {
		t.Error("should be deleted")
	}
}

func TestSQLStoreSaveCheckpointUpsert(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID := idgen.New()
	sessID := idgen.New()
	seedSession(t, store, "app", "u", sessID)

	cp1 := &TaskCheckpoint{TaskID: taskID, AppName: "app", UserID: "u", SessionID: sessID, PendingCount: 2, State: []byte("v1")}
	if err := store.SaveCheckpoint(ctx, cp1); err != nil {
		t.Fatal(err)
	}

	cp2 := &TaskCheckpoint{TaskID: taskID, AppName: "app", UserID: "u", SessionID: sessID, PendingCount: 0, State: []byte("v2")}
	if err := store.SaveCheckpoint(ctx, cp2); err != nil {
		t.Fatal(err)
	}

	claimed, _ := store.AdoptCheckpoint(ctx, taskID, "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if claimed == nil {
		t.Fatal("expected checkpoint")
	}
	if string(claimed.State) != "v2" {
		t.Errorf("state = %q, want v2", claimed.State)
	}
}

func TestSQLStoreRecordToolResultConcurrent(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	taskID := idgen.New()
	sessID := idgen.New()
	seedSession(t, store, "app", "u", sessID)

	cp := &TaskCheckpoint{TaskID: taskID, AppName: "app", UserID: "u", SessionID: sessID, PendingCount: 10}
	if err := store.SaveCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(10)
	for i := range 10 {
		go func(idx int) {
			defer wg.Done()
			_, err := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{
				ToolCallID: fmt.Sprintf("c%d", idx),
				Content:    "result",
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	claimed, _ := store.AdoptCheckpoint(ctx, taskID, "owner-test", time.Now(), time.Minute, testTakeoverWindow, false)
	if claimed == nil {
		t.Fatal("expected checkpoint after all results")
	}
	if claimed.PendingCount != 0 {
		t.Errorf("pending = %d, want 0", claimed.PendingCount)
	}
	if len(claimed.ToolResults) != 10 {
		t.Errorf("results = %d, want 10", len(claimed.ToolResults))
	}
}

func TestSQLStoreContextAndTaskRoundTrip(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	parentID := idgen.New()
	childSessID := idgen.New()
	childTaskID := idgen.New()
	mainID := idgen.New()

	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u1", SessionID: parentID}); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	tID := childTaskID
	child := &Session{
		AppName:   "app",
		UserID:    "u1",
		SessionID: childSessID,
		ContextID: parentID,
		TaskID:    &tID,
	}
	if err := store.Create(ctx, child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	main := &Session{AppName: "app", UserID: "u1", SessionID: mainID}
	if err := store.Create(ctx, main); err != nil {
		t.Fatalf("create main: %v", err)
	}

	gotChild, err := store.Get(ctx, "app", "u1", childSessID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if gotChild.ContextID != parentID {
		t.Errorf("child ContextID = %q, want %q", gotChild.ContextID, parentID)
	}
	if gotChild.TaskID == nil || *gotChild.TaskID != childTaskID {
		t.Errorf("child TaskID = %v, want %q", gotChild.TaskID, childTaskID)
	}

	gotMain, err := store.Get(ctx, "app", "u1", mainID)
	if err != nil {
		t.Fatalf("get main: %v", err)
	}
	if gotMain.ContextID != mainID {
		t.Errorf("main ContextID = %q, want %q (self for normal session)", gotMain.ContextID, mainID)
	}
	if gotMain.TaskID != nil {
		t.Errorf("main TaskID = %v, want nil", gotMain.TaskID)
	}

	gotByKeys, err := store.GetByContextAndTask(ctx, "app", "u1", parentID, childTaskID)
	if err != nil {
		t.Fatalf("get by context+task: %v", err)
	}
	if gotByKeys == nil || gotByKeys.SessionID != childSessID {
		t.Errorf("GetByContextAndTask = %+v, want session %q", gotByKeys, childSessID)
	}

	miss, err := store.GetByContextAndTask(ctx, "app", "u1", parentID, idgen.New())
	if err != nil {
		t.Fatalf("get by miss: %v", err)
	}
	if miss != nil {
		t.Errorf("expected nil for non-matching task, got %+v", miss)
	}

	gotChild.Metadata = map[string]any{"note": "hi"}
	if saveErr := store.Save(ctx, gotChild); saveErr != nil {
		t.Fatalf("save child: %v", saveErr)
	}
	reread, err := store.Get(ctx, "app", "u1", childSessID)
	if err != nil {
		t.Fatalf("re-get child: %v", err)
	}
	if reread.ContextID != parentID {
		t.Errorf("after Save, ContextID = %q, want %q", reread.ContextID, parentID)
	}
	if reread.TaskID == nil || *reread.TaskID != childTaskID {
		t.Errorf("after Save, TaskID = %v, want %q", reread.TaskID, childTaskID)
	}

	all, err := store.ListSessions(ctx, "app", "u1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]*Session{}
	for _, s := range all {
		got[s.SessionID] = s
	}
	if got[childSessID] == nil || got[childSessID].ContextID != parentID ||
		got[childSessID].TaskID == nil || *got[childSessID].TaskID != childTaskID {
		t.Errorf("list child = %+v", got[childSessID])
	}
	if got[mainID] == nil || got[mainID].ContextID != mainID || got[mainID].TaskID != nil {
		t.Errorf("list main = %+v", got[mainID])
	}
	if got[parentID] == nil || got[parentID].ContextID != parentID || got[parentID].TaskID != nil {
		t.Errorf("list parent = %+v", got[parentID])
	}
}

func TestSQLStoreUniqueContextAndTask(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	contextID := idgen.New()
	taskID := idgen.New()

	first := &Session{
		AppName:   "app",
		UserID:    "u1",
		SessionID: idgen.New(),
		ContextID: contextID,
		TaskID:    &taskID,
	}
	if err := store.Create(ctx, first); err != nil {
		t.Fatalf("create first: %v", err)
	}

	tID2 := taskID
	second := &Session{
		AppName:   "app",
		UserID:    "u1",
		SessionID: idgen.New(),
		ContextID: contextID,
		TaskID:    &tID2,
	}
	if err := store.Create(ctx, second); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("second create with the same (context, task) err = %v, want ErrSessionExists", err)
	}
}

func TestSQLStoreGetByContextAndTaskPrecondition(t *testing.T) {
	store := newTestSQLStore(t)
	if _, err := store.GetByContextAndTask(context.Background(), "app", "u", idgen.New(), ""); err == nil {
		t.Errorf("expected error for empty taskID")
	}
}

func TestSQLStoreGetByContextAndTaskNotFound(t *testing.T) {
	store := newTestSQLStore(t)
	got, err := store.GetByContextAndTask(context.Background(), "app", "u", idgen.New(), idgen.New())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown (context, task), got %+v", got)
	}
}

func TestSQLStoreAppendMessagesOrdering(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatal(err)
	}

	if err := store.AppendMessages(ctx, "app", "u", sessID, []Message{
		{Role: "user", Content: "m0"},
		{Role: "assistant", Content: "m1"},
	}); err != nil {
		t.Fatalf("append batch 1: %v", err)
	}
	if err := store.AppendMessages(ctx, "app", "u", sessID, []Message{
		{Role: "user", Content: "m2"},
	}); err != nil {
		t.Fatalf("append batch 2: %v", err)
	}
	if err := store.AppendMessages(ctx, "app", "u", sessID, nil); err != nil {
		t.Fatalf("append empty: %v", err)
	}

	got, err := store.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"m0", "m1", "m2"}
	if len(got.Messages) != len(want) {
		t.Fatalf("messages = %d, want %d: %+v", len(got.Messages), len(want), got.Messages)
	}
	for i, w := range want {
		if got.Messages[i].Content != w {
			t.Errorf("message[%d] = %q, want %q", i, got.Messages[i].Content, w)
		}
	}
}

func TestSQLStoreAppendMessagesMissingSession(t *testing.T) {
	store := newTestSQLStore(t)
	err := store.AppendMessages(context.Background(), "app", "u", idgen.New(), []Message{{Role: "user", Content: "x"}})
	if err == nil {
		t.Error("expected error appending to a non-existent session")
	}
}

func TestSQLStoreAppendMessagesLargeBatch(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatal(err)
	}

	const n = messageInsertBatch + 100
	batch := make([]Message, n)
	for i := range batch {
		batch[i] = Message{Role: "user", Content: fmt.Sprintf("m%d", i)}
	}
	if err := store.AppendMessages(ctx, "app", "u", sessID, batch); err != nil {
		t.Fatalf("append large batch: %v", err)
	}

	got, err := store.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != n {
		t.Fatalf("messages = %d, want %d", len(got.Messages), n)
	}
	for i := range got.Messages {
		if want := fmt.Sprintf("m%d", i); got.Messages[i].Content != want {
			t.Fatalf("message[%d] = %q, want %q", i, got.Messages[i].Content, want)
		}
	}
}

func TestSQLStoreAppendMessagesConcurrent(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatal(err)
	}

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := range N {
		go func(idx int) {
			defer wg.Done()
			if err := store.AppendMessages(ctx, "app", "u", sessID, []Message{
				{Role: "user", Content: fmt.Sprintf("fact-%d", idx)},
			}); err != nil {
				t.Errorf("append %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := store.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != N {
		t.Fatalf("persisted %d messages, want %d (concurrent appends lost or duplicated rows)", len(got.Messages), N)
	}
	seen := make(map[string]bool)
	for _, m := range got.Messages {
		seen[m.Content] = true
	}
	for i := range N {
		if !seen[fmt.Sprintf("fact-%d", i)] {
			t.Errorf("missing fact-%d after concurrent appends", i)
		}
	}
}

func TestSQLStoreAppendAfterCreateWithInitialMessages(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	if err := store.Create(ctx, &Session{
		AppName: "app", UserID: "u", SessionID: sessID,
		Messages: []Message{{Role: "user", Content: "m0"}, {Role: "assistant", Content: "m1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessages(ctx, "app", "u", sessID, []Message{
		{Role: "user", Content: "m2"}, {Role: "assistant", Content: "m3"},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"m0", "m1", "m2", "m3"}
	if len(got.Messages) != len(want) {
		t.Fatalf("messages = %d, want %d: %+v", len(got.Messages), len(want), got.Messages)
	}
	for i, w := range want {
		if got.Messages[i].Content != w {
			t.Errorf("message[%d] = %q, want %q", i, got.Messages[i].Content, w)
		}
	}
}

func TestSQLStoreAppendMessagesSelfHealsStaleCounter(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessages(ctx, "app", "u", sessID, []Message{{Role: "user", Content: "m1"}}); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(Message{Role: "assistant", Content: "m2-old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx,
		`INSERT INTO session_messages (app_name, user_id, session_id, seq, message, created_at)
		 VALUES (?, ?, ?, 2, ?, ?)`,
		"app", "u", sessID, string(raw), time.Now().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}

	if err = store.AppendMessages(ctx, "app", "u", sessID, []Message{{Role: "user", Content: "m3"}}); err != nil {
		t.Fatalf("append after stale counter: %v", err)
	}

	got, err := store.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"m1", "m2-old", "m3"}
	if len(got.Messages) != len(want) {
		t.Fatalf("messages = %d, want %d: %+v", len(got.Messages), len(want), got.Messages)
	}
	for i, w := range want {
		if got.Messages[i].Content != w {
			t.Errorf("message[%d] = %q, want %q", i, got.Messages[i].Content, w)
		}
	}
}

func TestSQLStoreDeleteCascadesMessages(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := context.Background()

	sessID := idgen.New()
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID,
		Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessages(ctx, "app", "u", sessID, []Message{{Role: "assistant", Content: "yo"}}); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, "app", "u", sessID); err != nil {
		t.Fatal(err)
	}

	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
	got, err := store.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 0 {
		t.Errorf("expected empty history after delete+recreate, got %d messages", len(got.Messages))
	}
}

func TestFormatDBTimeSortsLexicographicallyByInstant(t *testing.T) {
	base := time.Date(2026, 8, 25, 12, 0, 5, 0, time.UTC)
	instants := []time.Time{
		base.Add(-time.Nanosecond),
		base,
		base.Add(time.Nanosecond),
		base.Add(time.Millisecond),
		base.Add(500 * time.Millisecond),
		base.Add(time.Second),
	}
	for i := 1; i < len(instants); i++ {
		earlier, later := formatDBTime(instants[i-1]), formatDBTime(instants[i])
		if earlier >= later {
			t.Errorf("%v formats to %q which does not sort before %v (%q) — a lease comparison on this column orders by spelling, not by time",
				instants[i-1], earlier, instants[i], later)
		}
	}

	width := len(formatDBTime(base))
	for _, at := range instants {
		if got := len(formatDBTime(at)); got != width {
			t.Errorf("formatDBTime(%v) is %d chars, want %d — a variable-width layout cannot sort correctly", at, got, width)
		}
	}

	if _, err := time.Parse(time.RFC3339Nano, formatDBTime(base)); err != nil {
		t.Errorf("the stored layout no longer parses: %v", err)
	}
}

func TestAbandonedSweepUsesItsIndex(t *testing.T) {
	store := newTestSQLStore(t)
	if store.db.Dialect() != "sqlite" {
		t.Skip("query-plan assertion is sqlite-only; Postgres prefers a seq scan at test-table size")
	}

	rows, err := store.db.QueryContext(context.Background(),
		`EXPLAIN QUERY PLAN
		 SELECT task_id FROM task_checkpoints
		 WHERE agent_name = ? AND pending_count = 0
		   AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?
		 ORDER BY lease_expires_at ASC
		 LIMIT ?`,
		"agent-1", formatDBTime(time.Now()), 10)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if scanErr := rows.Scan(&id, &parent, &notused, &detail); scanErr != nil {
			t.Fatalf("scan plan: %v", scanErr)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("plan rows: %v", rowsErr)
	}

	got := plan.String()
	for _, column := range []string{"agent_name", "pending_count", "lease_expires_at"} {
		if !strings.Contains(got, column) {
			t.Errorf("the sweep's plan does not resolve %s against an index, so it scans every lapsed checkpoint in the deployment every 30s per replica.\nplan:\n%s", column, got)
		}
	}
	if !strings.Contains(got, "USING INDEX") {
		t.Errorf("the sweep is not using an index at all.\nplan:\n%s", got)
	}
}

func TestSQLStoreConcurrentCreateSameKeyYieldsOneWinner(t *testing.T) {
	assertOneConcurrentCreateWins(t, newTestSQLStore(t))
}
