// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func prompt(sender, taskID, requestID string) *WhatsAppPendingPrompt {
	now := time.Now().UnixMilli()
	return &WhatsAppPendingPrompt{
		GatewayID:   testGatewayID,
		SenderID:    sender,
		TaskID:      taskID,
		RequestID:   requestID,
		AgentName:   "Builder",
		UserID:      "whatsapp:" + sender,
		SurfaceJSON: `{"surfaceId":"` + requestID + `"}`,
		OutboundID:  "row-" + requestID,
		PromptWamid: "wamid." + requestID,
		ExpiresAt:   now + time.Hour.Milliseconds(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func mustUpsert(t *testing.T, db *DB, r *WhatsAppPendingPromptRepo, row *WhatsAppPendingPrompt) *WhatsAppPendingPrompt {
	t.Helper()
	var displaced *WhatsAppPendingPrompt
	err := db.WithTx(context.Background(), func(tx *dbutil.Tx) error {
		var err error
		displaced, err = r.Upsert(context.Background(), tx, row)
		return err
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return displaced
}

func TestWhatsAppPendingPrompt_UpsertReturnsTheDisplacedPrompt(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppPendingPromptRepo(db)
	ctx := context.Background()

	if displaced := mustUpsert(t, db, r, prompt("alice", "tA", "req-A")); displaced != nil {
		t.Fatalf("first upsert displaced %+v, want nil", displaced)
	}
	displaced := mustUpsert(t, db, r, prompt("alice", "tB", "req-B"))
	if displaced == nil || displaced.TaskID != "tA" {
		t.Fatalf("second upsert displaced %+v, want task tA — the retired task must be handed back so it can be told to stop waiting", displaced)
	}

	live, err := r.Get(ctx, testGatewayID, "alice")
	if err != nil || live == nil {
		t.Fatalf("Get = %v, err %v", live, err)
	}
	if live.TaskID != "tB" || live.RequestID != "req-B" {
		t.Errorf("live prompt = %s/%s, want tB/req-B", live.TaskID, live.RequestID)
	}
}

func TestWhatsAppPendingPrompt_OnePerConversation(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppPendingPromptRepo(db)

	mustUpsert(t, db, r, prompt("alice", "tA", "req-A"))
	mustUpsert(t, db, r, prompt("bob", "tB", "req-B"))
	mustUpsert(t, db, r, prompt("alice", "tC", "req-C"))

	n, err := dbutil.QueryVal[int](context.Background(), db,
		`SELECT COUNT(*) FROM whatsapp_pending_prompts WHERE gateway_id = ?`, testGatewayID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("stored prompts = %d, want 2 (one per conversation)", n)
	}
}

func TestWhatsAppPendingPrompt_DeleteByTaskIsScoped(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppPendingPromptRepo(db)
	ctx := context.Background()
	mustUpsert(t, db, r, prompt("alice", "tB", "req-B"))

	n, err := r.DeleteByTask(ctx, testGatewayID, "alice", "tA")
	if err != nil {
		t.Fatalf("DeleteByTask: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted %d rows for the wrong task, want 0", n)
	}
	if row, _ := r.Get(ctx, testGatewayID, "alice"); row == nil {
		t.Fatal("task A finishing deleted task B's live prompt")
	}

	if n, err = r.DeleteByTask(ctx, testGatewayID, "alice", "tB"); err != nil || n != 1 {
		t.Fatalf("DeleteByTask own task = %d, err %v; want 1", n, err)
	}
}

func TestWhatsAppPendingPrompt_DeleteAnsweredMatchesTaskAndRequestNotAReplacement(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppPendingPromptRepo(db)
	ctx := context.Background()
	mustUpsert(t, db, r, prompt("alice", "tB", "req-B"))

	if n, err := r.DeleteAnswered(ctx, testGatewayID, "alice", "tA", "req-A"); err != nil || n != 0 {
		t.Fatalf("DeleteAnswered for a displaced prompt = %d, err %v; want 0", n, err)
	}
	if row, _ := r.Get(ctx, testGatewayID, "alice"); row == nil {
		t.Fatal("resolving a superseded prompt cleared the live one")
	}
	if n, err := r.DeleteAnswered(ctx, testGatewayID, "alice", "tB", "req-B"); err != nil || n != 1 {
		t.Fatalf("DeleteAnswered for the live prompt = %d, err %v; want 1", n, err)
	}
}

func TestWhatsAppPendingPrompt_DeleteByOutboundWithdrawsOnlyItsOwn(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppPendingPromptRepo(db)
	ctx := context.Background()
	mustUpsert(t, db, r, prompt("alice", "tB", "req-B"))

	if n, err := r.DeleteByOutbound(ctx, nil, testGatewayID, "alice", "row-other"); err != nil || n != 0 {
		t.Fatalf("withdrawing another message's prompt = %d, err %v; want 0", n, err)
	}
	if n, err := r.DeleteByOutbound(ctx, nil, testGatewayID, "alice", "row-req-B"); err != nil || n != 1 {
		t.Fatalf("withdrawing its own prompt = %d, err %v; want 1", n, err)
	}
}

func TestWhatsAppPendingPrompt_ClaimExpiredIsExactlyOnce(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppPendingPromptRepo(db)
	ctx := context.Background()
	row := prompt("alice", "tA", "req-A")
	row.ExpiresAt = time.Now().Add(-time.Minute).UnixMilli()
	mustUpsert(t, db, r, row)

	nowMs := time.Now().UnixMilli()
	first, err := r.ClaimExpired(ctx, testGatewayID, "alice", "req-A", nowMs)
	if err != nil || !first {
		t.Fatalf("first ClaimExpired = %v, err %v; want true", first, err)
	}
	second, err := r.ClaimExpired(ctx, testGatewayID, "alice", "req-A", nowMs)
	if err != nil || second {
		t.Fatalf("second ClaimExpired = %v, err %v; want false", second, err)
	}
}

func TestWhatsAppPendingPrompt_ClaimExpiredLeavesLivePrompts(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppPendingPromptRepo(db)
	ctx := context.Background()
	mustUpsert(t, db, r, prompt("alice", "tA", "req-A"))

	claimed, err := r.ClaimExpired(ctx, testGatewayID, "alice", "req-A", time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ClaimExpired: %v", err)
	}
	if claimed {
		t.Error("a prompt that has not reached its deadline must not be claimable as expired")
	}
}

func TestWhatsAppPendingPrompt_ListExpiredOldestFirst(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppPendingPromptRepo(db)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).UnixMilli()

	older := prompt("alice", "tA", "req-A")
	older.ExpiresAt = past
	mustUpsert(t, db, r, older)
	newer := prompt("bob", "tB", "req-B")
	newer.ExpiresAt = past + 1000
	mustUpsert(t, db, r, newer)
	live := prompt("carol", "tC", "req-C")
	mustUpsert(t, db, r, live)

	rows, err := r.ListExpired(ctx, testGatewayID, time.Now().UnixMilli(), 10)
	if err != nil {
		t.Fatalf("ListExpired: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expired = %d, want 2 (the live prompt must not be swept)", len(rows))
	}
	if rows[0].TaskID != "tA" || rows[1].TaskID != "tB" {
		t.Errorf("expired order = %s,%s; want the oldest deadline first", rows[0].TaskID, rows[1].TaskID)
	}
}
