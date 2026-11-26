// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func toolTurn(at time.Time) []Message {
	return []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "lookup", Arguments: "{}"}}, Timestamp: at},
		{Role: "tool", Content: "found", ToolCallResult: ToolCallResult{ToolCallID: "c1"}, Timestamp: at},
	}
}

func seedHistory(t *testing.T, store *SQLStore, msgs []Message) string {
	t.Helper()
	sessID := idgen.New()
	if err := store.Create(t.Context(), &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessages(t.Context(), "app", "u", sessID, msgs); err != nil {
		t.Fatal(err)
	}
	return sessID
}

func retryAppend(t *testing.T, store *SQLStore, sessID string, storedBefore int, unconfirmed, tail []Message) {
	t.Helper()
	sess, err := store.Get(t.Context(), "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	retry := append(append([]Message(nil), unconfirmed...), tail...)
	if AppendLanded(sess.Messages, storedBefore, unconfirmed) {
		retry = retry[len(unconfirmed):]
	}
	if err := store.AppendMessages(t.Context(), "app", "u", sessID, retry); err != nil {
		t.Fatal(err)
	}
}

func storedContents(t *testing.T, store *SQLStore, sessID string) []string {
	t.Helper()
	sess, err := store.Get(t.Context(), "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(sess.Messages))
	for i, m := range sess.Messages {
		out[i] = m.Role + ":" + m.Content
		if len(m.ToolCalls) > 0 {
			out[i] += ":" + m.ToolCalls[0].ID
		}
	}
	return out
}

func assertContents(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("history = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("history = %q, want %q", got, want)
		}
	}
}

func TestRetryAfterCommittedAppendStoresTheBatchOnce(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := t.Context()
	sessID := seedHistory(t, store, []Message{{Role: "user", Content: "earlier"}})

	if err := store.AppendMessages(ctx, "app", "u", sessID, toolTurn(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessages(ctx, "app", "u", sessID, []Message{{Role: "user", Content: "concurrent"}}); err != nil {
		t.Fatal(err)
	}

	retryAppend(t, store, sessID, 1, toolTurn(time.Now().Add(time.Second)), []Message{{Role: "assistant", Content: "answer"}})

	assertContents(t, storedContents(t, store, sessID), []string{
		"user:earlier", "assistant::c1", "tool:found", "user:concurrent", "assistant:answer",
	})
}

func TestRetryRecognizesALandedBatchRecordedWithoutTaskID(t *testing.T) {
	store := newTestSQLStore(t)
	ctx := t.Context()
	sessID := seedHistory(t, store, []Message{{Role: "user", Content: "earlier"}})

	recorded := toolTurn(time.Now())
	if err := store.AppendMessages(ctx, "app", "u", sessID, recorded); err != nil {
		t.Fatal(err)
	}
	rebuilt := toolTurn(time.Now().Add(time.Second))
	for i := range rebuilt {
		rebuilt[i].TaskID = "task-1"
	}
	if !SameMessages(rebuilt, recorded) {
		t.Fatal("a batch rebuilt with a task id does not match the same batch recorded without one")
	}

	retryAppend(t, store, sessID, 1, rebuilt, []Message{{Role: "assistant", Content: "answer", TaskID: "task-1"}})

	assertContents(t, storedContents(t, store, sessID), []string{
		"user:earlier", "assistant::c1", "tool:found", "assistant:answer",
	})
}

func TestRetryAfterUncommittedAppendStoresTheBatch(t *testing.T) {
	store := newTestSQLStore(t)
	sessID := seedHistory(t, store, []Message{{Role: "user", Content: "earlier"}})

	retryAppend(t, store, sessID, 1, toolTurn(time.Now()), []Message{{Role: "assistant", Content: "answer"}})

	assertContents(t, storedContents(t, store, sessID), []string{
		"user:earlier", "assistant::c1", "tool:found", "assistant:answer",
	})
}

func TestAppendLandedIgnoresHistoryBeforeTheFailedAppend(t *testing.T) {
	store := newTestSQLStore(t)
	sessID := seedHistory(t, store, []Message{{Role: "user", Content: "continue"}})

	retryAppend(t, store, sessID, 1, []Message{{Role: "user", Content: "continue"}}, nil)

	assertContents(t, storedContents(t, store, sessID), []string{"user:continue", "user:continue"})
}
