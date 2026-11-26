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

const testIdleReset = 24 * time.Hour

func record(t *testing.T, r *WhatsAppConversationRepo, row *WhatsAppConversation) int64 {
	t.Helper()
	epoch, err := r.RecordInbound(context.Background(), row, testIdleReset)
	if err != nil {
		t.Fatalf("RecordInbound: %v", err)
	}
	return epoch
}

func conversation(sender string, lastInboundMs int64) *WhatsAppConversation {
	return &WhatsAppConversation{
		GatewayID:     testGatewayID,
		SenderID:      sender,
		WaID:          sender,
		FromID:        sender,
		LastInboundAt: lastInboundMs,
		CreatedAt:     lastInboundMs,
		UpdatedAt:     lastInboundMs,
	}
}

func TestWhatsAppConversation_RecordInboundUpsertsTheWindowClock(t *testing.T) {
	r := NewWhatsAppConversationRepo(newTestDB(t))
	ctx := context.Background()
	first := time.Now().Add(-time.Hour).UnixMilli()

	record(t, r, conversation("alice", first))
	got, err := r.Get(ctx, testGatewayID, "alice")
	if err != nil || got == nil {
		t.Fatalf("Get = %v, err %v", got, err)
	}
	if got.LastInboundAt != first {
		t.Errorf("lastInboundAt = %d, want %d", got.LastInboundAt, first)
	}

	second := first + time.Hour.Milliseconds()
	record(t, r, conversation("alice", second))
	got, err = r.Get(ctx, testGatewayID, "alice")
	if err != nil || got == nil {
		t.Fatalf("Get after upsert = %v, err %v", got, err)
	}
	if got.LastInboundAt != second {
		t.Errorf("lastInboundAt = %d, want the newer %d", got.LastInboundAt, second)
	}
	if got.CreatedAt != first {
		t.Errorf("createdAt = %d, want the original %d — an upsert must not reset first contact", got.CreatedAt, first)
	}
}

func TestWhatsAppConversation_EmptyWaIDKeepsTheStoredOne(t *testing.T) {
	r := NewWhatsAppConversationRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	withPhone := conversation("US.13491208655302741918", now)
	withPhone.WaID = "15551234567"
	record(t, r, withPhone)

	noContacts := conversation("US.13491208655302741918", now+1000)
	noContacts.WaID = ""
	record(t, r, noContacts)

	got, err := r.Get(ctx, testGatewayID, "US.13491208655302741918")
	if err != nil || got == nil {
		t.Fatalf("Get = %v, err %v", got, err)
	}
	if got.WaID != "15551234567" {
		t.Errorf("waID = %q, want the previously stored phone number", got.WaID)
	}
	if got.FromID != "US.13491208655302741918" {
		t.Errorf("fromID = %q, want the opaque sender id stored separately", got.FromID)
	}
}

func TestWhatsAppConversation_GetUnknownSenderIsNil(t *testing.T) {
	r := NewWhatsAppConversationRepo(newTestDB(t))
	got, err := r.Get(context.Background(), testGatewayID, "nobody")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("Get = %+v, want nil for a sender this gateway never heard from", got)
	}
}

func TestWhatsAppConversation_PruneStaleDropsOnlyAgedRows(t *testing.T) {
	r := NewWhatsAppConversationRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now()

	record(t, r, conversation("old", now.Add(-48*time.Hour).UnixMilli()))
	record(t, r, conversation("recent", now.UnixMilli()))

	deleted, err := r.PruneStale(ctx, now.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("PruneStale: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	if row, _ := r.Get(ctx, testGatewayID, "old"); row != nil {
		t.Error("the aged conversation should have been pruned")
	}
	if row, _ := r.Get(ctx, testGatewayID, "recent"); row == nil {
		t.Error("a recent conversation must survive the sweep")
	}
}

func TestWhatsAppConversation_FirstContactOpensAnEpoch(t *testing.T) {
	r := NewWhatsAppConversationRepo(newTestDB(t))
	first := time.Now().Add(-3 * time.Hour).UnixMilli()

	if got := record(t, r, conversation("alice", first)); got != first {
		t.Errorf("epoch on first contact = %d, want the message's own timestamp %d", got, first)
	}
	row, err := r.Get(context.Background(), testGatewayID, "alice")
	if err != nil || row == nil {
		t.Fatalf("Get = %v, err %v", row, err)
	}
	if row.SessionEpoch != first {
		t.Errorf("stored sessionEpoch = %d, want %d", row.SessionEpoch, first)
	}
}

func TestWhatsAppConversation_EpochHoldsWhileTheSenderKeepsTalking(t *testing.T) {
	r := NewWhatsAppConversationRepo(newTestDB(t))
	start := time.Now().Add(-30 * time.Hour).UnixMilli()

	opened := record(t, r, conversation("alice", start))
	for hop := 1; hop <= 3; hop++ {
		at := start + int64(hop)*(10*time.Hour).Milliseconds()
		if got := record(t, r, conversation("alice", at)); got != opened {
			t.Fatalf("epoch at hop %d = %d, want the session opened at %d", hop, got, opened)
		}
	}
}

func TestWhatsAppConversation_EpochRollsAfterAnIdleGap(t *testing.T) {
	r := NewWhatsAppConversationRepo(newTestDB(t))
	start := time.Now().Add(-72 * time.Hour).UnixMilli()

	opened := record(t, r, conversation("alice", start))

	justInside := start + testIdleReset.Milliseconds()
	if got := record(t, r, conversation("alice", justInside)); got != opened {
		t.Errorf("epoch at exactly the idle reset = %d, want the session still open at %d", got, opened)
	}

	afterSilence := justInside + testIdleReset.Milliseconds() + 1
	got := record(t, r, conversation("alice", afterSilence))
	if got == opened {
		t.Fatalf("epoch after %s idle = %d, unchanged: the gap was measured against the row this write had already advanced",
			testIdleReset, got)
	}
	if got != afterSilence {
		t.Errorf("rolled epoch = %d, want the message that broke the silence %d", got, afterSilence)
	}
	row, err := r.Get(context.Background(), testGatewayID, "alice")
	if err != nil || row == nil {
		t.Fatalf("Get = %v, err %v", row, err)
	}
	if row.SessionEpoch != afterSilence || row.LastInboundAt != afterSilence {
		t.Errorf("stored row = epoch %d / lastInbound %d, want both %d",
			row.SessionEpoch, row.LastInboundAt, afterSilence)
	}
	if row.CreatedAt != start {
		t.Errorf("createdAt = %d, want the original %d — a rolled session is not a new conversation", row.CreatedAt, start)
	}
}

func TestWhatsAppConversation_EpochIsPerSender(t *testing.T) {
	r := NewWhatsAppConversationRepo(newTestDB(t))
	start := time.Now().Add(-48 * time.Hour).UnixMilli()

	alice := record(t, r, conversation("alice", start))
	bob := record(t, r, conversation("bob", start))

	rolled := start + testIdleReset.Milliseconds() + 1
	if got := record(t, r, conversation("alice", rolled)); got == alice {
		t.Error("alice's epoch did not roll after her own silence")
	}
	if got := record(t, r, conversation("bob", start+time.Hour.Milliseconds())); got != bob {
		t.Errorf("bob's epoch = %d, want %d — alice going quiet must not roll his session", got, bob)
	}
}

const versionBeforeSessionEpoch = 41

func TestWhatsAppConversation_SessionEpochMigrationRoundTrips(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewWhatsAppConversationRepo(db)

	at := time.Now().Add(-2 * time.Hour).UnixMilli()
	if got := record(t, r, conversation("alice", at)); got != at {
		t.Fatalf("epoch before the round-trip = %d, want %d", got, at)
	}

	if err := db.MigrateDownTo(ctx, versionBeforeSessionEpoch); err != nil {
		t.Fatalf("MigrateDownTo(%d): %v", versionBeforeSessionEpoch, err)
	}
	if _, err := dbutil.QueryVal[int64](ctx, db, `SELECT session_epoch FROM whatsapp_conversations`); err == nil {
		t.Fatal("session_epoch is still readable after the down migration: the column was never dropped")
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate back up: %v", err)
	}

	got, err := r.Get(ctx, testGatewayID, "alice")
	if err != nil || got == nil {
		t.Fatalf("Get = %v, err %v", got, err)
	}
	if got.SessionEpoch != at {
		t.Errorf("epoch after the round-trip = %d, want %d: a row that predates the column has to come back carrying its own clock, not a zero",
			got.SessionEpoch, at)
	}
}

func TestWhatsAppConversation_AnUnsetEpochIsReopened(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := NewWhatsAppConversationRepo(db)

	stamped := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO whatsapp_conversations (gateway_id, sender_id, wa_id, from_id, last_inbound_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		testGatewayID, "alice", "alice", "alice", stamped, stamped, stamped); err != nil {
		t.Fatalf("seed a row written without the column: %v", err)
	}

	next := stamped + time.Minute.Milliseconds()
	if got := record(t, r, conversation("alice", next)); got != next {
		t.Errorf("epoch after a message onto an unset row = %d, want %d: a zero was carried forward as though it were a live session",
			got, next)
	}
}
