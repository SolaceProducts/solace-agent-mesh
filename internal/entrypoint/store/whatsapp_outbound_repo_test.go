// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const testGatewayID = "gw-1"

func waRow(recipient, kind, payload string) *WhatsAppOutbound {
	now := time.Now().UnixMilli()
	return &WhatsAppOutbound{
		ID:        idgen.New(),
		GatewayID: testGatewayID,
		Recipient: recipient,
		Kind:      kind,
		Payload:   payload,
		State:     WhatsAppOutboundQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func mustEnqueue(t *testing.T, r *WhatsAppOutboundRepo, row *WhatsAppOutbound) *WhatsAppOutbound {
	t.Helper()
	if err := r.Enqueue(context.Background(), nil, row); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return row
}

func mustSend(t *testing.T, r *WhatsAppOutboundRepo, row *WhatsAppOutbound, wamid string, nowMs int64) {
	t.Helper()
	ctx := context.Background()
	claimed, _, err := r.ClaimNext(ctx, row.GatewayID, row.Recipient, nowMs)
	if err != nil {
		t.Fatalf("ClaimNext for %s: %v", row.ID, err)
	}
	if claimed == nil || claimed.ID != row.ID {
		t.Fatalf("claim for %s got %v; the row must be at the head of its queue", row.ID, claimed)
	}
	if err := r.MarkSent(ctx, nil, row.ID, wamid, nowMs); err != nil {
		t.Fatalf("MarkSent %s: %v", row.ID, err)
	}
}

func TestWhatsAppOutbound_ClaimIsFIFOPerRecipient(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()

	first := mustEnqueue(t, r, waRow("alice", "text", `{"text":"1"}`))
	second := mustEnqueue(t, r, waRow("alice", "text", `{"text":"2"}`))
	other := mustEnqueue(t, r, waRow("bob", "text", `{"text":"b"}`))

	now := time.Now().UnixMilli()
	got, due, err := r.ClaimNext(ctx, testGatewayID, "alice", now)
	if err != nil || got == nil {
		t.Fatalf("ClaimNext: row=%v due=%d err=%v", got, due, err)
	}
	if got.ID != first.ID {
		t.Errorf("claimed %s, want the oldest row %s", got.ID, first.ID)
	}
	if got.State != WhatsAppOutboundSending || got.Attempts != 1 {
		t.Errorf("claimed row = state %q attempts %d, want sending/1", got.State, got.Attempts)
	}

	next, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now)
	if err != nil || next == nil || next.ID != second.ID {
		t.Fatalf("second claim = %v, err %v; want %s", next, err, second.ID)
	}
	bobRow, _, err := r.ClaimNext(ctx, testGatewayID, "bob", now)
	if err != nil || bobRow == nil || bobRow.ID != other.ID {
		t.Fatalf("bob claim = %v, err %v; want %s", bobRow, err, other.ID)
	}
}

func TestWhatsAppOutbound_DeferredHeadBlocksRatherThanSkips(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	head := mustEnqueue(t, r, waRow("alice", "text", `{"text":"1"}`))
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"2"}`))

	claimed, _, claimErr := r.ClaimNext(ctx, testGatewayID, "alice", now)
	if claimErr != nil || claimed == nil {
		t.Fatalf("ClaimNext: %v %v", claimed, claimErr)
	}
	if err := r.Requeue(ctx, head.ID, now+60_000, now, 131056, "rate limited"); err != nil {
		t.Fatalf("Requeue: %v", err)
	}

	row, dueAt, err := r.ClaimNext(ctx, testGatewayID, "alice", now)
	if err != nil {
		t.Fatalf("ClaimNext after requeue: %v", err)
	}
	if row != nil {
		t.Fatalf("claimed %s while the head row is in backoff — FIFO broken", row.ID)
	}
	if dueAt != now+60_000 {
		t.Errorf("dueAt = %d, want the head row's not_before %d", dueAt, now+60_000)
	}

	again, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now+61_000)
	if err != nil || again == nil || again.ID != head.ID {
		t.Fatalf("claim after backoff = %v, err %v; want the head row %s", again, err, head.ID)
	}
	if again.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the requeued attempt was already spent)", again.Attempts)
	}
}

func TestWhatsAppOutbound_EmptyQueueReportsNothingDue(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	row, dueAt, err := r.ClaimNext(context.Background(), testGatewayID, "nobody", time.Now().UnixMilli())
	if err != nil || row != nil || dueAt != 0 {
		t.Fatalf("ClaimNext on an empty queue = %v/%d/%v, want nil/0/nil", row, dueAt, err)
	}
}

func TestWhatsAppOutbound_MarkSentRejectsDuplicateWamid(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	a := mustEnqueue(t, r, waRow("alice", "text", `{"text":"1"}`))
	b := mustEnqueue(t, r, waRow("alice", "text", `{"text":"2"}`))
	mustSend(t, r, a, "wamid.X", now)
	claimed, _, claimErr := r.ClaimNext(ctx, testGatewayID, "alice", now)
	if claimErr != nil || claimed == nil || claimed.ID != b.ID {
		t.Fatalf("ClaimNext = %v, err %v; want the second row claimed", claimed, claimErr)
	}
	err := r.MarkSent(ctx, nil, b.ID, "wamid.X", now)
	if err == nil {
		t.Fatal("a repeated wamid must be rejected: receipts key on it, so two rows sharing one id is unresolvable")
	}
	if errors.Is(err, ErrOutboundRowMoved) {
		t.Errorf("rejected as a moved row (%v); the fence passed, the wamid collision is what must stop it", err)
	}
	if err := r.MarkSent(ctx, nil, b.ID, "", now); err != nil {
		t.Errorf("recording delivery without a wamid must be allowed: %v", err)
	}
}

func TestWhatsAppOutbound_RecoverInFlightRequeuesAndCapsAttempts(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	fresh := mustEnqueue(t, r, waRow("alice", "text", `{"text":"fresh"}`))
	poison := mustEnqueue(t, r, waRow("bob", "text", `{"text":"poison"}`))

	for i := range 4 {
		if _, _, err := r.ClaimNext(ctx, testGatewayID, "bob", now); err != nil {
			t.Fatalf("claim poison: %v", err)
		}
		if i == 3 {
			break
		}
		if requeued, _, err := r.RecoverInFlight(ctx, testGatewayID, 4, 0, 0, now, nil); err != nil {
			t.Fatalf("RecoverInFlight: %v", err)
		} else if requeued != 1 {
			t.Fatalf("run %d requeued %d rows, want 1", i, requeued)
		}
	}
	if _, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now); err != nil {
		t.Fatalf("claim fresh: %v", err)
	}

	requeued, exhausted, err := r.RecoverInFlight(ctx, testGatewayID, 4, 0, 0, now, nil)
	if err != nil {
		t.Fatalf("RecoverInFlight: %v", err)
	}
	if requeued != 1 || exhausted != 1 {
		t.Errorf("final scan = %d requeued, %d exhausted; want 1 fresh row requeued and 1 spent row failed", requeued, exhausted)
	}

	freshRow, err := r.Get(ctx, fresh.ID)
	if err != nil || freshRow == nil {
		t.Fatalf("Get fresh: %v %v", freshRow, err)
	}
	if freshRow.State != WhatsAppOutboundQueued {
		t.Errorf("recovered row state = %q, want queued", freshRow.State)
	}

	poisonRow, err := r.Get(ctx, poison.ID)
	if err != nil || poisonRow == nil {
		t.Fatalf("Get poison: %v %v", poisonRow, err)
	}
	if poisonRow.State != WhatsAppOutboundFailed {
		t.Errorf("row at the attempt cap = %q, want terminal failed — a poison row must not re-send forever", poisonRow.State)
	}
	if poisonRow.Attempts != 4 {
		t.Errorf("attempts = %d, want the cap of 4", poisonRow.Attempts)
	}
}

func TestWhatsAppOutbound_ActiveRecipientsIsOldestFirst(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()

	mustEnqueue(t, r, waRow("alice", "text", `{"text":"1"}`))
	mustEnqueue(t, r, waRow("bob", "text", `{"text":"1"}`))
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"2"}`))
	sent := mustEnqueue(t, r, waRow("carol", "text", `{"text":"1"}`))
	mustSend(t, r, sent, "wamid.C", time.Now().UnixMilli())

	got, err := r.ActiveRecipients(ctx, testGatewayID)
	if err != nil {
		t.Fatalf("ActiveRecipients: %v", err)
	}
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("active recipients = %v, want [alice bob] (carol has nothing queued)", got)
	}
}

func TestWhatsAppOutbound_ScopedByGateway(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()

	mine := waRow("alice", "text", `{"text":"mine"}`)
	theirs := waRow("alice", "text", `{"text":"theirs"}`)
	theirs.GatewayID = "gw-2"
	mustEnqueue(t, r, mine)
	mustEnqueue(t, r, theirs)

	row, _, err := r.ClaimNext(ctx, testGatewayID, "alice", time.Now().UnixMilli())
	if err != nil || row == nil || row.ID != mine.ID {
		t.Fatalf("claim = %v, err %v; want this gateway's row only", row, err)
	}
	if again, _, _ := r.ClaimNext(ctx, testGatewayID, "alice", time.Now().UnixMilli()); again != nil {
		t.Errorf("claimed %s, which belongs to another gateway", again.ID)
	}
}

func TestWhatsAppOutbound_OneParkedRowPerRecipient(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppOutboundRepo(db)
	ctx := context.Background()

	first := waRow("alice", "text", `{"text":"1"}`)
	first.State = WhatsAppOutboundParked
	mustEnqueue(t, r, first)

	second := waRow("alice", "text", `{"text":"2"}`)
	second.State = WhatsAppOutboundParked
	if err := r.Enqueue(ctx, nil, second); err == nil {
		t.Error("a second parked row for one recipient must be refused: the parking bay is newest-wins, not a backlog")
	}

	other := waRow("bob", "text", `{"text":"1"}`)
	other.State = WhatsAppOutboundParked
	mustEnqueue(t, r, other)
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"3"}`))
}

func TestWhatsAppOutbound_EnqueueJoinsCallerTransaction(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppOutboundRepo(db)
	ctx := context.Background()

	errRollback := errors.New("rollback")
	row := waRow("alice", "text", `{"text":"rolled back"}`)
	err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if enqErr := r.Enqueue(ctx, tx, row); enqErr != nil {
			return enqErr
		}
		return errRollback
	})
	if err == nil {
		t.Fatal("expected the transaction to roll back")
	}
	got, err := r.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Error("the row survived a rolled-back transaction, so Enqueue did not join it")
	}
}

func TestWhatsAppOutbound_PruneTerminalKeepsLiveRows(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour).UnixMilli()

	settled := map[string]string{
		WhatsAppOutboundRead:       "read",
		WhatsAppOutboundDelivered:  "delivered",
		WhatsAppOutboundSent:       "sent",
		WhatsAppOutboundFailed:     "failed",
		WhatsAppOutboundSuperseded: "superseded",
	}
	for state, name := range settled {
		row := waRow("alice", "text", `{"text":"`+name+`"}`)
		row.State = state
		row.UpdatedAt = old
		mustEnqueue(t, r, row)
	}
	live := waRow("bob", "text", `{"text":"live"}`)
	live.UpdatedAt = old
	mustEnqueue(t, r, live)
	recent := waRow("carol", "text", `{"text":"recent"}`)
	recent.State = WhatsAppOutboundRead
	mustEnqueue(t, r, recent)

	deleted, err := r.PruneTerminal(ctx, time.Now().Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatalf("PruneTerminal: %v", err)
	}
	if deleted != int64(len(settled)) {
		t.Errorf("deleted = %d, want %d (every settled row past the cutoff)", deleted, len(settled))
	}
	for _, id := range []string{live.ID, recent.ID} {
		got, getErr := r.Get(ctx, id)
		if getErr != nil {
			t.Fatalf("Get: %v", getErr)
		}
		if got == nil {
			t.Errorf("row %s was pruned; only settled rows past the cutoff may go", id)
		}
	}
}

func TestWhatsAppOutbound_ReceiptsAreForwardOnly(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	row := mustEnqueue(t, r, waRow("alice", "text", `{"text":"hi"}`))
	mustSend(t, r, row, "wamid.A", now)

	apply := func(state string) (bool, string) {
		t.Helper()
		moved, got, err := r.UpdateFromReceipt(ctx, testGatewayID,
			WhatsAppReceipt{Wamid: "wamid.A", State: state}, now)
		if err != nil {
			t.Fatalf("UpdateFromReceipt(%s): %v", state, err)
		}
		if got == nil {
			t.Fatalf("UpdateFromReceipt(%s) found no row", state)
		}
		return moved, got.State
	}

	for _, step := range []struct{ receipt, want string }{
		{WhatsAppOutboundDelivered, WhatsAppOutboundDelivered},
		{WhatsAppOutboundRead, WhatsAppOutboundRead},
	} {
		if moved, state := apply(step.receipt); !moved || state != step.want {
			t.Errorf("receipt %q: moved=%v state=%q, want true/%q", step.receipt, moved, state, step.want)
		}
	}
	for _, stale := range []string{WhatsAppOutboundSent, WhatsAppOutboundDelivered, WhatsAppOutboundRead} {
		if moved, state := apply(stale); moved || state != WhatsAppOutboundRead {
			t.Errorf("a %q receipt after read moved the row to %q; late and duplicate receipts must be no-ops", stale, state)
		}
	}
	if moved, state := apply(WhatsAppOutboundFailed); moved || state != WhatsAppOutboundRead {
		t.Errorf("a failure receipt after read moved the row to %q; the user demonstrably got the message", state)
	}
}

func TestWhatsAppOutbound_FailureReceiptPersistsMetaDetail(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()
	const detail = "to: 15551234567 is not a valid WhatsApp user"

	row := mustEnqueue(t, r, waRow("alice", "text", `{"text":"hi"}`))
	mustSend(t, r, row, "wamid.F", now)
	moved, got, err := r.UpdateFromReceipt(ctx, testGatewayID,
		WhatsAppReceipt{Wamid: "wamid.F", State: WhatsAppOutboundFailed, ErrorCode: 131026, ErrorDetail: detail}, now)
	if err != nil || !moved {
		t.Fatalf("UpdateFromReceipt = moved %v, err %v", moved, err)
	}
	if got.State != WhatsAppOutboundFailed || got.ErrorCode != 131026 || got.ErrorDetail != detail {
		t.Errorf("row = %q/%d/%q, want failed/131026 with the reason stored verbatim", got.State, got.ErrorCode, got.ErrorDetail)
	}
}

func TestWhatsAppOutbound_ReceiptForUnknownWamidIsIgnored(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	moved, row, err := r.UpdateFromReceipt(context.Background(), testGatewayID,
		WhatsAppReceipt{Wamid: "wamid.NOTOURS", State: WhatsAppOutboundDelivered}, time.Now().UnixMilli())
	if err != nil || moved || row != nil {
		t.Fatalf("receipt for an unknown wamid = moved %v row %v err %v; want a clean no-op", moved, row, err)
	}
}

func TestWhatsAppOutbound_ReceiptsLeaveParkedAndUnsentRowsAlone(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	for i, state := range []string{WhatsAppOutboundParked, WhatsAppOutboundSuperseded, WhatsAppOutboundQueued, WhatsAppOutboundFailed} {
		row := waRow("alice", "text", `{"text":"x"}`)
		row.State = state
		row.Recipient = "alice-" + state
		row.Wamid = "wamid.P" + string(rune('a'+i))
		mustEnqueue(t, r, row)

		moved, got, err := r.UpdateFromReceipt(ctx, testGatewayID,
			WhatsAppReceipt{Wamid: row.Wamid, State: WhatsAppOutboundDelivered}, now)
		if err != nil {
			t.Fatalf("UpdateFromReceipt(%s): %v", state, err)
		}
		if moved || got == nil || got.State != state {
			t.Errorf("a delivered receipt moved a %q row (moved=%v now %q)", state, moved, got.State)
		}
	}
}

func TestWhatsAppOutbound_SettleWritesAreFencedOnTheClaim(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	row := mustEnqueue(t, r, waRow("alice", "text", `{"text":"hi"}`))
	for name, settle := range map[string]func() error{
		"MarkSent":   func() error { return r.MarkSent(ctx, nil, row.ID, "wamid.Q", now) },
		"Requeue":    func() error { return r.Requeue(ctx, row.ID, now, now, 0, "") },
		"MarkFailed": func() error { return r.MarkFailed(ctx, nil, row.ID, 0, "", now) },
	} {
		if err := settle(); !errors.Is(err, ErrOutboundRowMoved) {
			t.Errorf("%s on an unclaimed row = %v, want ErrOutboundRowMoved", name, err)
		}
	}

	if _, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if err := r.MarkSent(ctx, nil, row.ID, "wamid.Q", now); err != nil {
		t.Fatalf("MarkSent on a claimed row: %v", err)
	}
	if err := r.MarkSent(ctx, nil, row.ID, "wamid.OTHER", now); !errors.Is(err, ErrOutboundRowMoved) {
		t.Errorf("a second MarkSent = %v, want ErrOutboundRowMoved", err)
	}
	if got := mustGetRow(t, r, row.ID); got.Wamid != "wamid.Q" {
		t.Errorf("wamid = %q, want the claiming instance's id to survive", got.Wamid)
	}
}

func TestWhatsAppOutbound_RecoveryLeavesFreshInFlightRowsAlone(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	fresh := mustEnqueue(t, r, waRow("alice", "text", `{"text":"in flight"}`))
	if _, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	requeued, exhausted, err := r.RecoverInFlight(ctx, testGatewayID, 4, 5*time.Minute, 5*time.Minute, now, nil)
	if err != nil {
		t.Fatalf("RecoverInFlight: %v", err)
	}
	if requeued != 0 || exhausted != 0 {
		t.Errorf("scan took %d/%d rows inside the lease; a draining instance's sends must be left alone", requeued, exhausted)
	}
	if got := mustGetRow(t, r, fresh.ID); got.State != WhatsAppOutboundSending {
		t.Errorf("row = %q, want it still in flight", got.State)
	}

	if requeued, _, err = r.RecoverInFlight(ctx, testGatewayID, 4, 5*time.Minute, 5*time.Minute, now+6*60_000, nil); err != nil {
		t.Fatalf("RecoverInFlight past the lease: %v", err)
	}
	if requeued != 1 {
		t.Errorf("requeued %d rows past the lease, want 1", requeued)
	}
}

func TestWhatsAppOutbound_RecoverySkipsTheCallersOwnRecipients(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	mine := mustEnqueue(t, r, waRow("alice", "text", `{"text":"my worker has this"}`))
	theirs := mustEnqueue(t, r, waRow("bob", "text", `{"text":"nobody has this"}`))
	for _, recipient := range []string{"alice", "bob"} {
		if _, _, err := r.ClaimNext(ctx, testGatewayID, recipient, now); err != nil {
			t.Fatalf("ClaimNext %s: %v", recipient, err)
		}
	}

	requeued, _, err := r.RecoverInFlight(ctx, testGatewayID, 4, 0, time.Hour, now, []string{"alice"})
	if err != nil {
		t.Fatalf("RecoverInFlight: %v", err)
	}
	if requeued != 1 {
		t.Errorf("requeued %d rows, want only bob's", requeued)
	}
	if got := mustGetRow(t, r, mine.ID); got.State != WhatsAppOutboundSending {
		t.Errorf("the caller's own in-flight row = %q, want it left in flight", got.State)
	}
	if got := mustGetRow(t, r, theirs.ID); got.State != WhatsAppOutboundQueued {
		t.Errorf("the unowned row = %q, want it requeued", got.State)
	}
}

func TestWhatsAppOutbound_PruneStaleWillNotDeleteALiveRow(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now()

	tenMinutesAgo := now.Add(-10 * time.Minute).UnixMilli()
	inFlight := waRow("alice", "text", `{"text":"mid-send"}`)
	inFlight.CreatedAt, inFlight.UpdatedAt = tenMinutesAgo, tenMinutesAgo
	inFlight = mustEnqueue(t, r, inFlight)
	if _, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now.UnixMilli()); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	waiting := waRow("bob", "text", `{"text":"queued"}`)
	waiting.CreatedAt, waiting.UpdatedAt = tenMinutesAgo, tenMinutesAgo
	waiting = mustEnqueue(t, r, waiting)

	deleted, err := r.PruneStale(ctx, now.Add(-time.Minute), 100)
	if err != nil {
		t.Fatalf("PruneStale: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted %d rows ten minutes old; the floor is 24 hours", deleted)
	}
	for _, row := range []*WhatsAppOutbound{inFlight, waiting} {
		if got := mustGetRow(t, r, row.ID); got == nil {
			t.Errorf("row %s was deleted; a live reply is not retention's to take", row.ID)
		}
	}
}

func TestWhatsAppOutbound_RecoveryWriteLosesToARenewal(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	row := mustEnqueue(t, r, waRow("alice", "text", `{"text":"held by the other pod"}`))
	if _, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	sweepAt := now + 7*60_000
	staleBefore := sweepAt - (6 * time.Minute).Milliseconds()
	if got := mustGetRow(t, r, row.ID); got.UpdatedAt > staleBefore {
		t.Fatalf("row updated_at %d is not below the sweep cutoff %d; the test models the wrong window", got.UpdatedAt, staleBefore)
	}

	if renewed, err := r.RenewClaims(ctx, testGatewayID, []string{row.ID}, sweepAt); err != nil || renewed != 1 {
		t.Fatalf("RenewClaims = %d, %v; want 1, nil", renewed, err)
	}

	moved, err := r.settleRecovered(ctx, testGatewayID, []string{row.ID}, staleBefore,
		`UPDATE whatsapp_outbound SET state = ?, not_before = ?, updated_at = ?`,
		WhatsAppOutboundQueued, sweepAt, sweepAt)
	if err != nil {
		t.Fatalf("settleRecovered: %v", err)
	}
	if moved != 0 {
		t.Errorf("the write took %d rows; the renewal came first, so this row is not abandoned", moved)
	}
	if got := mustGetRow(t, r, row.ID); got.State != WhatsAppOutboundSending {
		t.Errorf("row = %q, want it left in flight", got.State)
	}
}

func TestWhatsAppOutbound_RecoveryHandlesMoreRowsThanThePlaceholderCap(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	const rows = dbutil.InClauseCap + 5
	recipients := make([]string, 0, rows)
	for i := range rows {
		recipient := fmt.Sprintf("caller%04d", i)
		recipients = append(recipients, recipient)
		mustEnqueue(t, r, waRow(recipient, "text", `{"text":"in flight"}`))
		if _, _, err := r.ClaimNext(ctx, testGatewayID, recipient, now); err != nil {
			t.Fatalf("ClaimNext %s: %v", recipient, err)
		}
	}

	requeued, _, err := r.RecoverInFlight(ctx, testGatewayID, 4, 0, time.Hour, now, recipients)
	if err != nil {
		t.Fatalf("RecoverInFlight with %d fenced recipients: %v", rows, err)
	}
	if requeued != 0 {
		t.Errorf("requeued %d rows, want 0: every recipient was fenced", requeued)
	}

	requeued, _, err = r.RecoverInFlight(ctx, testGatewayID, 4, 0, time.Hour, now, nil)
	if err != nil {
		t.Fatalf("RecoverInFlight unfenced: %v", err)
	}
	if requeued != rows {
		t.Errorf("requeued %d rows, want all %d — the write chunks, so none may be dropped", requeued, rows)
	}
}

func TestWhatsAppOutbound_RenewClaimsKeepsARowOutOfAnotherInstancesSweep(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	held := mustEnqueue(t, r, waRow("alice", "text", `{"text":"long media send"}`))
	other := mustEnqueue(t, r, waRow("bob", "text", `{"text":"genuinely abandoned"}`))
	for _, recipient := range []string{"alice", "bob"} {
		if _, _, err := r.ClaimNext(ctx, testGatewayID, recipient, now); err != nil {
			t.Fatalf("ClaimNext %s: %v", recipient, err)
		}
	}

	const sevenMin = 7 * 60_000
	renewed, err := r.RenewClaims(ctx, testGatewayID, []string{held.ID}, now+sevenMin)
	if err != nil {
		t.Fatalf("RenewClaims: %v", err)
	}
	if renewed != 1 {
		t.Fatalf("renewed %d rows, want 1", renewed)
	}

	requeued, _, err := r.RecoverInFlight(ctx, testGatewayID, 4, 6*time.Minute, time.Hour, now+sevenMin, nil)
	if err != nil {
		t.Fatalf("RecoverInFlight: %v", err)
	}
	if requeued != 1 {
		t.Errorf("requeued %d rows, want only the un-renewed one", requeued)
	}
	if got := mustGetRow(t, r, held.ID); got.State != WhatsAppOutboundSending {
		t.Errorf("the renewed row = %q, want it still in flight — the heartbeat is what says so", got.State)
	}
	if got := mustGetRow(t, r, other.ID); got.State != WhatsAppOutboundQueued {
		t.Errorf("the un-renewed row = %q, want it requeued", got.State)
	}
}

func TestWhatsAppOutbound_RenewClaimsIsScopedToTheNamedRows(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	claimed := mustEnqueue(t, r, waRow("alice", "text", `{"text":"claimed"}`))
	queued := mustEnqueue(t, r, waRow("bob", "text", `{"text":"never claimed"}`))
	if _, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	elsewhere := waRow("carol", "text", `{"text":"another gateway"}`)
	elsewhere.GatewayID = "gw-other"
	elsewhere = mustEnqueue(t, r, elsewhere)
	if _, _, err := r.ClaimNext(ctx, "gw-other", "carol", now); err != nil {
		t.Fatalf("ClaimNext gw-other: %v", err)
	}

	renewed, err := r.RenewClaims(ctx, testGatewayID,
		[]string{claimed.ID, queued.ID, elsewhere.ID}, now+1000)
	if err != nil {
		t.Fatalf("RenewClaims: %v", err)
	}
	if renewed != 1 {
		t.Errorf("renewed %d rows, want 1: a queued row and another gateway's row are not this caller's to touch", renewed)
	}
	if got := mustGetRow(t, r, claimed.ID); got.UpdatedAt != now+1000 {
		t.Errorf("updated_at = %d, want the renewal timestamp %d", got.UpdatedAt, now+1000)
	}
	if got := mustGetRow(t, r, elsewhere.ID); got.UpdatedAt == now+1000 {
		t.Error("another gateway's in-flight row was renewed; the heartbeat is scoped to the caller")
	}
	if got, err := r.RenewClaims(ctx, testGatewayID, nil, now+2000); err != nil || got != 0 {
		t.Errorf("RenewClaims(nil) = %d, %v; want 0, nil", got, err)
	}
}

func TestWhatsAppOutbound_HardLeaseOverridesTheRecipientFence(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	stranded := mustEnqueue(t, r, waRow("alice", "text", `{"text":"settled by nobody"}`))
	if _, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	if requeued, _, err := r.RecoverInFlight(ctx, testGatewayID, 4, time.Minute, time.Hour, now+2*60_000, []string{"alice"}); err != nil {
		t.Fatalf("RecoverInFlight inside the hard lease: %v", err)
	} else if requeued != 0 {
		t.Errorf("requeued %d rows inside the hard lease, want 0", requeued)
	}

	requeued, _, err := r.RecoverInFlight(ctx, testGatewayID, 4, time.Minute, time.Hour, now+61*60_000, []string{"alice"})
	if err != nil {
		t.Fatalf("RecoverInFlight past the hard lease: %v", err)
	}
	if requeued != 1 {
		t.Errorf("requeued %d rows past the hard lease, want 1", requeued)
	}
	if got := mustGetRow(t, r, stranded.ID); got.State != WhatsAppOutboundQueued {
		t.Errorf("stranded row = %q, want it requeued", got.State)
	}
}

func TestWhatsAppOutbound_SupersedeQueuedOfKindSparesOtherKindsAndClaimedRows(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	withTask := func(taskID string) *WhatsAppOutbound {
		row := waRow("alice", "text", `{"text":"chunk"}`)
		row.TaskID = taskID
		return mustEnqueue(t, r, row)
	}
	first := withTask("task-1")
	second := withTask("task-1")
	other := withTask("task-2")
	if _, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}

	n, err := r.SupersedeQueuedOfKind(ctx, testGatewayID, "task-1", "text", now)
	if err != nil {
		t.Fatalf("SupersedeQueuedOfKind: %v", err)
	}
	if n != 1 {
		t.Errorf("superseded %d rows, want 1 (the claimed row is not queued)", n)
	}
	if got := mustGetRow(t, r, second.ID); got.State != WhatsAppOutboundSuperseded {
		t.Errorf("sibling chunk = %q, want superseded", got.State)
	}
	if got := mustGetRow(t, r, first.ID); got.State != WhatsAppOutboundSending {
		t.Errorf("the claimed row = %q, want it untouched", got.State)
	}
	if got := mustGetRow(t, r, other.ID); got.State != WhatsAppOutboundQueued {
		t.Errorf("another task's row = %q, want it untouched", got.State)
	}
	if n, err = r.SupersedeQueuedOfKind(ctx, testGatewayID, "", "text", now); err != nil || n != 0 {
		t.Errorf("an empty task id = %d/%v, want 0/nil — a gateway notice has no siblings", n, err)
	}
	notice := waRow("alice", "notice", `{"text":"sorry"}`)
	notice.TaskID = "task-1"
	mustEnqueue(t, r, notice)
	if n, err = r.SupersedeQueuedOfKind(ctx, testGatewayID, "task-1", "text", now); err != nil || n != 0 {
		t.Errorf("withdrawing text chunks touched %d rows, want 0 — the notice is a different kind", n)
	}
	if got := mustGetRow(t, r, notice.ID); got.State != WhatsAppOutboundQueued {
		t.Errorf("the notice is %q; it must survive the withdrawal it explains", got.State)
	}
}

func TestWhatsAppOutbound_PruneStaleReachesUnsettledRows(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour).UnixMilli()

	var stale []string
	for _, state := range []string{WhatsAppOutboundQueued, WhatsAppOutboundSending, WhatsAppOutboundParked} {
		row := waRow("alice-"+state, "text", `{"text":"orphan"}`)
		row.State = state
		row.CreatedAt = old
		mustEnqueue(t, r, row)
		stale = append(stale, row.ID)
	}
	recent := mustEnqueue(t, r, waRow("bob", "text", `{"text":"recent"}`))

	deleted, err := r.PruneStale(ctx, time.Now().Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatalf("PruneStale: %v", err)
	}
	if deleted != int64(len(stale)) {
		t.Errorf("deleted = %d, want %d", deleted, len(stale))
	}
	if got, _ := r.Get(ctx, recent.ID); got == nil {
		t.Error("a recent queued row was pruned; only rows past the window may go")
	}
}

func TestWhatsAppOutbound_ConcurrentClaimAndReceiptHaveOneWinner(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"one row"}`))

	const claimants = 8
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for range claimants {
		wg.Go(func() {
			row, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now)
			if err != nil {
				t.Errorf("ClaimNext: %v", err)
				return
			}
			if row != nil {
				claimed.Add(1)
			}
		})
	}
	wg.Wait()
	if got := claimed.Load(); got != 1 {
		t.Fatalf("%d claimants got the row, want exactly 1", got)
	}

	row, _, err := r.ClaimNext(ctx, testGatewayID, "alice", now)
	if err != nil || row != nil {
		t.Fatalf("a second pass found %v (err %v); the row is already claimed", row, err)
	}

	sent := mustEnqueue(t, r, waRow("bob", "text", `{"text":"receipted"}`))
	mustSend(t, r, sent, "wamid.RACE", now)

	var moved atomic.Int32
	for range claimants {
		wg.Go(func() {
			ok, _, upErr := r.UpdateFromReceipt(ctx, testGatewayID,
				WhatsAppReceipt{Wamid: "wamid.RACE", State: WhatsAppOutboundDelivered}, now)
			if upErr != nil {
				t.Errorf("UpdateFromReceipt: %v", upErr)
				return
			}
			if ok {
				moved.Add(1)
			}
		})
	}
	wg.Wait()
	if got := moved.Load(); got != 1 {
		t.Errorf("%d concurrent receipts reported a transition, want exactly 1", got)
	}
}

func TestWhatsAppOutbound_EmptyWamidLookupIsRejected(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"unsent"}`))

	row, err := r.GetByWamid(ctx, testGatewayID, "")
	if err != nil || row != nil {
		t.Errorf("GetByWamid(\"\") = %v/%v, want nil/nil", row, err)
	}
	moved, row, err := r.UpdateFromReceipt(ctx, testGatewayID,
		WhatsAppReceipt{Wamid: "", State: WhatsAppOutboundDelivered}, time.Now().UnixMilli())
	if err != nil || moved || row != nil {
		t.Errorf("an empty receipt id = %v/%v/%v, want a clean no-op", moved, row, err)
	}
}

func mustGetRow(t *testing.T, r *WhatsAppOutboundRepo, id string) *WhatsAppOutbound {
	t.Helper()
	row, err := r.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	if row == nil {
		t.Fatalf("row %s is gone", id)
	}
	return row
}

func mustClaimHead(t *testing.T, r *WhatsAppOutboundRepo, recipient string) *WhatsAppOutbound {
	t.Helper()
	row, _, err := r.ClaimNext(context.Background(), testGatewayID, recipient, time.Now().UnixMilli())
	if err != nil || row == nil {
		t.Fatalf("ClaimNext: row=%v err=%v", row, err)
	}
	return row
}

func TestWhatsAppOutbound_ParkHoldsTheReplyForTheRecipient(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"late answer"}`))
	claimed := mustClaimHead(t, r, "alice")

	nowMs := time.Now().UnixMilli()
	superseded, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: claimed.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, ErrorDetail: "to: alice re-engagement required", NowMs: nowMs})
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if superseded != "" {
		t.Errorf("first park superseded %q, want nothing", superseded)
	}

	parked, err := r.GetParked(ctx, testGatewayID, "alice")
	if err != nil || parked == nil {
		t.Fatalf("GetParked = %v, err %v", parked, err)
	}
	if parked.State != WhatsAppOutboundParked || parked.ParkedAt != nowMs {
		t.Errorf("parked row = %q parkedAt %d, want parked at %d", parked.State, parked.ParkedAt, nowMs)
	}
	if parked.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — a park is a deferral, not a spent delivery attempt", parked.Attempts)
	}
	if parked.ErrorDetail != "to: alice re-engagement required" {
		t.Errorf("errorDetail = %q, want Meta's reason kept on the row", parked.ErrorDetail)
	}

	next, _, err := r.ClaimNext(ctx, testGatewayID, "alice", time.Now().UnixMilli())
	if err != nil || next != nil {
		t.Fatalf("ClaimNext after park = %v, err %v; want nothing claimable", next, err)
	}
}

func TestWhatsAppOutbound_ParkSupersedesTheOlderPark(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"first"}`))
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"second"}`))

	first := mustClaimHead(t, r, "alice")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: first.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("first Park: %v", err)
	}
	second := mustClaimHead(t, r, "alice")
	superseded, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: second.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, NowMs: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("second Park: %v", err)
	}
	if superseded != first.ID {
		t.Fatalf("superseded = %q, want the older park %q", superseded, first.ID)
	}

	older, err := r.Get(ctx, first.ID)
	if err != nil || older == nil {
		t.Fatalf("Get older = %v, err %v", older, err)
	}
	if older.State != WhatsAppOutboundSuperseded {
		t.Errorf("older park = %q, want superseded — a dropped reply must stay visible, and 'failed' has to keep meaning genuine non-delivery", older.State)
	}
	parked, _ := r.GetParked(ctx, testGatewayID, "alice")
	if parked == nil || parked.ID != second.ID {
		t.Errorf("live park = %v, want the newest reply %s", parked, second.ID)
	}
}

func TestWhatsAppOutbound_ParkIsPerRecipient(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"a"}`))
	mustEnqueue(t, r, waRow("bob", "text", `{"text":"b"}`))

	alice := mustClaimHead(t, r, "alice")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: alice.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("park alice: %v", err)
	}
	bob := mustClaimHead(t, r, "bob")
	superseded, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: bob.ID, Recipient: "bob", MayYieldToTask: "text", ErrorCode: 131047, NowMs: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("park bob: %v", err)
	}
	if superseded != "" {
		t.Errorf("parking bob's reply superseded %q — parks are per recipient", superseded)
	}
	if parked, _ := r.GetParked(ctx, testGatewayID, "alice"); parked == nil {
		t.Error("alice's park must survive bob's")
	}
}

func TestWhatsAppOutbound_UnparkRequeuesWithTheRewrittenPayload(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"late answer"}`))
	claimed := mustClaimHead(t, r, "alice")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: claimed.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("Park: %v", err)
	}

	moved, err := r.Unpark(ctx, claimed.ID, time.Now().UnixMilli())
	if err != nil || !moved {
		t.Fatalf("Unpark = %v, err %v; want true", moved, err)
	}
	next := mustClaimHead(t, r, "alice")
	if next.ID != claimed.ID {
		t.Fatalf("claimed %s after unpark, want the released reply %s", next.ID, claimed.ID)
	}
	if next.Payload != `{"text":"late answer"}` {
		t.Errorf("payload = %q, want the text the agent produced; the prefix is applied at send time", next.Payload)
	}
	if next.ParkedAt == 0 {
		t.Error("parkedAt must survive the unpark: it is both the park TTL's clock and the marker that earns the prefix")
	}

	if moved, err = r.Unpark(ctx, claimed.ID, time.Now().UnixMilli()); err != nil || moved {
		t.Errorf("second Unpark = %v, err %v; want false", moved, err)
	}
}

func TestWhatsAppOutbound_SupersedeParkRetiresUnsent(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"stale"}`))
	claimed := mustClaimHead(t, r, "alice")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: claimed.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("Park: %v", err)
	}

	retired, err := r.SupersedePark(ctx, claimed.ID, time.Now().UnixMilli())
	if err != nil || !retired {
		t.Fatalf("SupersedePark = %v, err %v; want true", retired, err)
	}
	row, _ := r.Get(ctx, claimed.ID)
	if row == nil || row.State != WhatsAppOutboundSuperseded {
		t.Errorf("row = %v, want superseded", row)
	}
	if again, _ := r.SupersedePark(ctx, claimed.ID, time.Now().UnixMilli()); again {
		t.Error("SupersedePark must be a no-op on a row that is no longer parked")
	}
}

func TestWhatsAppOutbound_ReceiptDoesNotMoveAParkedRow(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	row := waRow("alice", "text", `{"text":"parked"}`)
	row.Wamid = "wamid.PARKED"
	mustEnqueue(t, r, row)
	claimed := mustClaimHead(t, r, "alice")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: claimed.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("Park: %v", err)
	}

	moved, got, err := r.UpdateFromReceipt(ctx, testGatewayID,
		WhatsAppReceipt{Wamid: "wamid.PARKED", State: WhatsAppOutboundDelivered}, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("UpdateFromReceipt: %v", err)
	}
	if moved {
		t.Error("a receipt moved a parked row")
	}
	if got == nil || got.State != WhatsAppOutboundParked {
		t.Errorf("row = %v, want it still parked", got)
	}
}

func TestWhatsAppOutbound_SettleInsideACallerTransaction(t *testing.T) {
	db := newTestDB(t)
	r := NewWhatsAppOutboundRepo(db)
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"tx"}`))
	claimed := mustClaimHead(t, r, "alice")

	wantErr := errors.New("dependent write failed")
	err := r.WithTx(ctx, func(q dbutil.Querier) error {
		if err := r.MarkSent(ctx, q, claimed.ID, "wamid.TX", time.Now().UnixMilli()); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTx err = %v, want %v", err, wantErr)
	}
	row, _ := r.Get(ctx, claimed.ID)
	if row == nil || row.State != WhatsAppOutboundSending {
		t.Fatalf("row = %v, want the send NOT recorded — a rolled-back dependent write must roll the state back with it", row)
	}

	if err := r.WithTx(ctx, func(q dbutil.Querier) error {
		return r.MarkSent(ctx, q, claimed.ID, "wamid.TX", time.Now().UnixMilli())
	}); err != nil {
		t.Fatalf("committing WithTx: %v", err)
	}
	row, _ = r.Get(ctx, claimed.ID)
	if row == nil || row.State != WhatsAppOutboundSent || row.Wamid != "wamid.TX" {
		t.Errorf("row = %v, want sent with its wamid", row)
	}
}

func TestWhatsAppOutbound_ReparkKeepsTheOriginalParkTime(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"answer"}`))
	claimed := mustClaimHead(t, r, "alice")

	firstPark := time.Now().Add(-20 * time.Hour).UnixMilli()
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: claimed.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, NowMs: firstPark}); err != nil {
		t.Fatalf("first Park: %v", err)
	}
	if _, err := r.Unpark(ctx, claimed.ID, time.Now().UnixMilli()); err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	mustClaimHead(t, r, "alice")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: claimed.ID, Recipient: "alice", MayYieldToTask: "text", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("second Park: %v", err)
	}

	row, _ := r.Get(ctx, claimed.ID)
	if row == nil || row.ParkedAt != firstPark {
		t.Fatalf("parkedAt = %d, want the original %d — a re-park that resets the clock is a row that never ages out", row.ParkedAt, firstPark)
	}
}

func TestWhatsAppOutbound_ParkYieldsOnlyToItsOwnTask(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()

	prose := waRow("alice", "text", `{"text":"the answer"}`)
	prose.TaskID = "t1"
	mustEnqueue(t, r, prose)
	sameTask := waRow("alice", "media", `{"media":{"filename":"report.pdf"}}`)
	sameTask.TaskID = "t1"
	mustEnqueue(t, r, sameTask)
	otherTask := waRow("alice", "media", `{"media":{"filename":"chart.png"}}`)
	otherTask.TaskID = "t2"
	mustEnqueue(t, r, otherTask)

	first := mustClaimHead(t, r, "alice")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: first.ID, Recipient: "alice", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("park the answer: %v", err)
	}

	own := mustClaimHead(t, r, "alice")
	superseded, yielded, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: own.ID, Recipient: "alice", MayYieldToTask: "t1", ErrorCode: 131047, NowMs: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("park its own attachment: %v", err)
	}
	if !yielded || superseded != "" {
		t.Fatalf("own-task attachment = yielded %v superseded %q, want it to stand aside", yielded, superseded)
	}
	if parked, _ := r.GetParked(ctx, testGatewayID, "alice"); parked == nil || parked.ID != first.ID {
		t.Fatalf("live park = %v, want the answer %s", parked, first.ID)
	}
	if row, _ := r.Get(ctx, own.ID); row == nil || row.State != WhatsAppOutboundSuperseded {
		t.Errorf("the yielding attachment = %v, want superseded", row)
	}

	other := mustClaimHead(t, r, "alice")
	superseded, yielded, err = r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: other.ID, Recipient: "alice", MayYieldToTask: "t2", ErrorCode: 131047, NowMs: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("park another task's attachment: %v", err)
	}
	if yielded {
		t.Error("a different task's row stood aside; newest wins across tasks")
	}
	if superseded != first.ID {
		t.Errorf("superseded = %q, want the older park %s", superseded, first.ID)
	}
}

func TestWhatsAppOutbound_ListStaleParksFindsOnlyAgedOnes(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	now := time.Now().UnixMilli()

	mustEnqueue(t, r, waRow("old", "text", `{"text":"a"}`))
	mustEnqueue(t, r, waRow("fresh", "text", `{"text":"b"}`))
	oldRow := mustClaimHead(t, r, "old")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: oldRow.ID, Recipient: "old", MayYieldToTask: "text", ErrorCode: 131047, NowMs: now - 48*time.Hour.Milliseconds()}); err != nil {
		t.Fatalf("park old: %v", err)
	}
	freshRow := mustClaimHead(t, r, "fresh")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: freshRow.ID, Recipient: "fresh", MayYieldToTask: "text", ErrorCode: 131047, NowMs: now}); err != nil {
		t.Fatalf("park fresh: %v", err)
	}

	stale, err := r.ListStaleParks(ctx, testGatewayID, now-24*time.Hour.Milliseconds(), 10)
	if err != nil {
		t.Fatalf("ListStaleParks: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != oldRow.ID {
		t.Fatalf("stale parks = %v, want only the aged one; a park nobody comes back for must not live forever", stale)
	}
}

func TestWhatsAppOutbound_SupersedeRetiresAClaimedRow(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	mustEnqueue(t, r, waRow("alice", "text", `{"text":"stale"}`))
	claimed := mustClaimHead(t, r, "alice")

	if err := r.Supersede(ctx, claimed.ID, time.Now().UnixMilli()); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	row, _ := r.Get(ctx, claimed.ID)
	if row == nil || row.State != WhatsAppOutboundSuperseded {
		t.Fatalf("row = %v, want superseded", row)
	}
	if err := r.Supersede(ctx, claimed.ID, time.Now().UnixMilli()); !errors.Is(err, ErrOutboundRowMoved) {
		t.Errorf("a second Supersede = %v, want ErrOutboundRowMoved", err)
	}
}

func TestWhatsAppOutbound_SupersedeQueuedOfKindReachesAParkedRow(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()
	row := waRow("alice", "interactive_prompt", `{"text":"pick"}`)
	row.TaskID = "t1"
	mustEnqueue(t, r, row)
	claimed := mustClaimHead(t, r, "alice")
	if _, _, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: claimed.ID, Recipient: "alice", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("Park: %v", err)
	}

	n, err := r.SupersedeQueuedOfKind(ctx, testGatewayID, "t1", "interactive_prompt", time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("SupersedeQueuedOfKind: %v", err)
	}
	if n != 1 {
		t.Fatalf("withdrew %d rows, want the parked prompt", n)
	}
	if parked, _ := r.GetParked(ctx, testGatewayID, "alice"); parked != nil {
		t.Error("the parked prompt survived its task's end; the user's next message would answer a dead task")
	}
}

func TestWhatsAppOutbound_ConcurrentParksLeaveExactlyOne(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := context.Background()

	const parkers = 8
	claimed := make([]*WhatsAppOutbound, 0, parkers)
	for i := range parkers {
		mustEnqueue(t, r, waRow("alice", "text", fmt.Sprintf(`{"text":"%d"}`, i)))
		claimed = append(claimed, mustClaimHead(t, r, "alice"))
	}

	var start, done sync.WaitGroup
	start.Add(1)
	var parked atomic.Int64
	for _, row := range claimed {
		done.Add(1)
		go func(id string) {
			defer done.Done()
			start.Wait()
			if _, yielded, err := r.Park(ctx, WhatsAppParkRequest{GatewayID: testGatewayID, ID: id, Recipient: "alice", ErrorCode: 131047, NowMs: time.Now().UnixMilli()}); err == nil && !yielded {
				parked.Add(1)
			}
		}(row.ID)
	}
	start.Done()
	done.Wait()

	live, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM whatsapp_outbound WHERE gateway_id = ? AND recipient = ? AND state = ?`,
		testGatewayID, "alice", WhatsAppOutboundParked)
	if err != nil {
		t.Fatalf("count parked: %v", err)
	}
	if live != 1 {
		t.Fatalf("parked rows = %d, want exactly 1; the partial unique index is the whole policy", live)
	}
	if parked.Load() == 0 {
		t.Error("no park succeeded at all")
	}
}

func TestWhatsAppOutbound_TasksWithFinalRow(t *testing.T) {
	r := NewWhatsAppOutboundRepo(newTestDB(t))
	ctx := t.Context()
	withTask := func(row *WhatsAppOutbound, task string) *WhatsAppOutbound { row.TaskID = task; return row }

	mustEnqueue(t, r, withTask(waRow("alice", "text", `{"text":"done","final":true}`), "t-ended"))
	mustEnqueue(t, r, withTask(waRow("alice", "text", `{"text":"half"}`), "t-running"))
	mustEnqueue(t, r, withTask(waRow("bob", "text", `{"text":"x","final":true}`), "t-bob"))

	got, err := r.TasksWithFinalRow(ctx, testGatewayID, "alice", []string{"t-ended", "t-running", "t-bob", "t-none"})
	if err != nil {
		t.Fatalf("TasksWithFinalRow: %v", err)
	}
	if len(got) != 1 || got[0] != "t-ended" {
		t.Fatalf("TasksWithFinalRow = %v, want [t-ended]: another recipient's task and a task with no final row do not count", got)
	}
	if got, err := r.TasksWithFinalRow(ctx, testGatewayID, "alice", nil); err != nil || got != nil {
		t.Fatalf("no task ids = %v, %v; want nothing", got, err)
	}
}
