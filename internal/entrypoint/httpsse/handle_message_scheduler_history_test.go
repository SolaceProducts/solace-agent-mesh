// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func bubblesJSON(t *testing.T, bubbles ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(bubbles)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoadSchedulerConversationHistory(t *testing.T) {
	adapter, core, sessRepo, chatRepo, _ := setupTestAdapterWithDB(t)
	h := &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo, chatRepo: chatRepo}

	ctx := context.Background()
	sessID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sessID, UserID: "sam_dev_user"})

	_, _ = chatRepo.Upsert(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sessID, UserID: "sam_dev_user", CreatedTime: 1000,
		MessageBubbles: bubblesJSON(t,
			map[string]any{"type": "user", "text": "run the report"},
			map[string]any{"type": "agent", "text": "report done"},
		),
	})
	_, _ = chatRepo.Upsert(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sessID, UserID: "sam_dev_user", CreatedTime: 2000,
		MessageBubbles: bubblesJSON(t,
			map[string]any{"type": "user", "text": "and again"},
			map[string]any{"type": "agent", "text": "", "isError": false},
			map[string]any{"type": "agent", "text": "boom", "isError": true},
			map[string]any{"type": "agent", "text": "second report done"},
		),
	})

	got := h.loadSchedulerConversationHistory(ctx, sessID, "sam_dev_user")
	want := []map[string]string{
		{"role": "user", "content": "run the report"},
		{"role": "assistant", "content": "report done"},
		{"role": "user", "content": "and again"},
		{"role": "assistant", "content": "second report done"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i]["role"] != want[i]["role"] || got[i]["content"] != want[i]["content"] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestLoadSchedulerConversationHistory_ExcludesOtherUsers(t *testing.T) {
	adapter, core, sessRepo, chatRepo, _ := setupTestAdapterWithDB(t)
	h := &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo, chatRepo: chatRepo}

	ctx := context.Background()
	sessID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sessID, UserID: "owner"})

	_ = chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sessID, UserID: "other_user", CreatedTime: 1,
		MessageBubbles: bubblesJSON(t, map[string]any{"type": "user", "text": "secret"}),
	})
	_ = chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sessID, UserID: "owner", CreatedTime: 2,
		MessageBubbles: bubblesJSON(t, map[string]any{"type": "user", "text": "mine"}),
	})

	got := h.loadSchedulerConversationHistory(ctx, sessID, "owner")
	if len(got) != 1 || got[0]["content"] != "mine" {
		t.Fatalf("expected only the owner's turn, got %+v", got)
	}
}

func TestLoadSchedulerConversationHistory_OrdersByCreatedTime(t *testing.T) {
	adapter, core, sessRepo, chatRepo, _ := setupTestAdapterWithDB(t)
	h := &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo, chatRepo: chatRepo}

	ctx := context.Background()
	sessID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sessID, UserID: "sam_dev_user"})

	_, _ = chatRepo.Upsert(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sessID, UserID: "sam_dev_user", CreatedTime: 2000,
		MessageBubbles: bubblesJSON(t, map[string]any{"type": "user", "text": "later"}),
	})
	_, _ = chatRepo.Upsert(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sessID, UserID: "sam_dev_user", CreatedTime: 1000,
		MessageBubbles: bubblesJSON(t, map[string]any{"type": "user", "text": "earlier"}),
	})

	got := h.loadSchedulerConversationHistory(ctx, sessID, "sam_dev_user")
	if len(got) != 2 || got[0]["content"] != "earlier" || got[1]["content"] != "later" {
		t.Fatalf("expected chronological [earlier, later], got %+v", got)
	}
}

func TestLoadSchedulerConversationHistory_Cap(t *testing.T) {
	adapter, core, sessRepo, chatRepo, _ := setupTestAdapterWithDB(t)
	h := &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo, chatRepo: chatRepo}

	ctx := context.Background()
	sessID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sessID, UserID: "sam_dev_user"})

	for i := range 60 {
		_, _ = chatRepo.Upsert(ctx, &store.ChatTask{
			ID: idgen.New(), SessionID: sessID, UserID: "sam_dev_user", CreatedTime: int64(i + 1),
			MessageBubbles: bubblesJSON(t, map[string]any{"type": "user", "text": fmt.Sprintf("msg %d", i)}),
		})
	}

	got := h.loadSchedulerConversationHistory(ctx, sessID, "sam_dev_user")
	if len(got) != maxSchedulerHistoryEntries {
		t.Fatalf("got %d entries, want cap %d", len(got), maxSchedulerHistoryEntries)
	}
	if got[0]["content"] != "msg 10" {
		t.Errorf("oldest retained = %q, want %q", got[0]["content"], "msg 10")
	}
}

func TestLoadSchedulerConversationHistory_NoChatRepo(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	h := &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo, chatRepo: nil}
	if got := h.loadSchedulerConversationHistory(context.Background(), idgen.New(), "sam_dev_user"); got != nil {
		t.Errorf("expected nil with no chatRepo, got %+v", got)
	}
}

func TestMaybeAttachSchedulerHistory_SourceGate(t *testing.T) {
	adapter, core, sessRepo, chatRepo, _ := setupTestAdapterWithDB(t)
	h := &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo, chatRepo: chatRepo}
	ctx := context.Background()

	scheduler := "scheduler"
	chat := "chat"
	mkSessionWithTask := func(source *string) string {
		sid := idgen.New()
		_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user", Source: source})
		_ = chatRepo.Create(ctx, &store.ChatTask{
			ID: idgen.New(), SessionID: sid, UserID: "sam_dev_user", CreatedTime: 1,
			MessageBubbles: bubblesJSON(t, map[string]any{"type": "user", "text": "prior turn"}),
		})
		return sid
	}

	tests := []struct {
		name     string
		source   *string
		wantKey  bool
		seedTask bool
	}{
		{name: "scheduler source attaches", source: &scheduler, wantKey: true, seedTask: true},
		{name: "chat source does not attach", source: &chat, wantKey: false, seedTask: true},
		{name: "nil source does not attach", source: nil, wantKey: false, seedTask: true},
		{name: "scheduler but no chat_tasks does not attach", source: &scheduler, wantKey: false, seedTask: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sid string
			if tc.seedTask {
				sid = mkSessionWithTask(tc.source)
			} else {
				sid = idgen.New()
				_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user", Source: tc.source})
			}
			sess, _ := sessRepo.GetByID(ctx, sid)
			msg := a2a.Message{Kind: "message", Role: a2a.RoleUser, ContextID: sid}
			h.maybeAttachSchedulerHistory(ctx, &msg, sess, sid, "sam_dev_user")
			_, hasKey := msg.Metadata["schedulerConversationHistory"]
			if hasKey != tc.wantKey {
				t.Errorf("schedulerConversationHistory present=%v, want %v", hasKey, tc.wantKey)
			}
		})
	}
}

func TestSchedulerHistory_SurvivesJSONWireSeam(t *testing.T) {
	adapter, core, sessRepo, chatRepo, _ := setupTestAdapterWithDB(t)
	h := &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo, chatRepo: chatRepo}
	ctx := context.Background()

	sessID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sessID, UserID: "sam_dev_user"})
	_ = chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sessID, UserID: "sam_dev_user", CreatedTime: 1,
		MessageBubbles: bubblesJSON(t,
			map[string]any{"type": "user", "text": "hello"},
			map[string]any{"type": "agent", "text": "hi back"},
		),
	})

	history := h.loadSchedulerConversationHistory(ctx, sessID, "sam_dev_user")
	if len(history) != 2 {
		t.Fatalf("got %d entries, want 2", len(history))
	}

	payload, err := json.Marshal(map[string]any{"schedulerConversationHistory": history})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}

	raw, ok := decoded["schedulerConversationHistory"].([]any)
	if !ok {
		t.Fatalf("after round-trip, value is %T, internal/agent expects []any", decoded["schedulerConversationHistory"])
	}
	if len(raw) != 2 {
		t.Fatalf("round-tripped %d entries, want 2", len(raw))
	}
	first, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatalf("entry is %T, internal/agent expects map[string]any", raw[0])
	}
	if role, _ := first["role"].(string); role != "user" {
		t.Errorf("first role = %q, want user", role)
	}
	if content, _ := first["content"].(string); content != "hello" {
		t.Errorf("first content = %q, want hello", content)
	}
}
