// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"testing"
)

func ptrStr(s string) *string { return &s }
func ptrI64(v int64) *int64   { return &v }

func TestPendingRequestRepo_InsertClaimFirstWins(t *testing.T) {
	ctx := t.Context()
	repo := NewPendingRequestRepo(openTestDB(t))

	addr := "ns/sam/v1/notify/response/agent-1/req-1"
	if err := repo.Insert(ctx, PendingRequest{
		RequestID: "req-1", RequesterAddr: addr, RecipientUserID: "trent",
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	gotAddr, claimed, err := repo.Claim(ctx, "req-1", "answered")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claimed || gotAddr != addr {
		t.Fatalf("first claim should win with addr %q, got claimed=%v addr=%q", addr, claimed, gotAddr)
	}

	_, claimed2, err := repo.Claim(ctx, "req-1", "timed_out")
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if claimed2 {
		t.Fatal("second claim must not win once the request is resolved")
	}

	if _, claimed3, err := repo.Claim(ctx, "nope", "answered"); err != nil || claimed3 {
		t.Fatalf("claim of unknown request: claimed=%v err=%v", claimed3, err)
	}
}

func TestPendingRequestRepo_DedupRejectsDuplicate(t *testing.T) {
	ctx := t.Context()
	repo := NewPendingRequestRepo(openTestDB(t))

	base := PendingRequest{RequesterAddr: "addr", RecipientUserID: "trent", Dedup: ptrStr("dk-1")}
	first := base
	first.RequestID = "r1"
	if err := repo.Insert(ctx, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	second := base
	second.RequestID = "r2"
	if err := repo.Insert(ctx, second); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate dedup must return ErrDuplicate, got %v", err)
	}

	for _, id := range []string{"e1", "e2"} {
		if err := repo.Insert(ctx, PendingRequest{RequestID: id, RequesterAddr: "a", Dedup: ptrStr("")}); err != nil {
			t.Fatalf("empty-dedup insert %s: %v", id, err)
		}
	}
}

func TestPendingRequestRepo_ExpiredAndCleanup(t *testing.T) {
	ctx := t.Context()
	repo := NewPendingRequestRepo(openTestDB(t))

	mustInsert := func(id string, exp *int64) {
		if err := repo.Insert(ctx, PendingRequest{RequestID: id, RequesterAddr: "a", ExpiresAt: exp}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	mustInsert("past", ptrI64(1000))
	mustInsert("future", ptrI64(1<<62))
	mustInsert("none", nil)

	exp, err := repo.Expired(ctx, 5000, 10)
	if err != nil {
		t.Fatalf("Expired: %v", err)
	}
	if len(exp) != 1 || exp[0].RequestID != "past" {
		t.Fatalf("only the past-deadline pending row should be expired, got %+v", exp)
	}

	if _, claimed, cerr := repo.Claim(ctx, "past", "timed_out"); cerr != nil || !claimed {
		t.Fatalf("claim past: claimed=%v err=%v", claimed, cerr)
	}
	if exp, _ := repo.Expired(ctx, 5000, 10); len(exp) != 0 {
		t.Fatalf("claimed row must not reappear as expired, got %+v", exp)
	}
	deleted, err := repo.DeleteResolvedOlderThan(ctx, 1<<62)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteResolvedOlderThan should reclaim the 1 resolved row, got %d (err=%v)", deleted, err)
	}
}

func TestPendingRequestRepo_ReopenRetriesAfterRouteFailure(t *testing.T) {
	ctx := t.Context()
	repo := NewPendingRequestRepo(openTestDB(t))

	if err := repo.Insert(ctx, PendingRequest{RequestID: "r1", RequesterAddr: "addr"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, claimed, _ := repo.Claim(ctx, "r1", "answered"); !claimed {
		t.Fatal("first claim should win")
	}

	if err := repo.Reopen(ctx, "r1", "answered"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	pr, _ := repo.Get(ctx, "r1")
	if pr == nil || pr.Status != "pending" || pr.ResolvedAt != nil {
		t.Fatalf("Reopen must return the row to pending with resolved_at cleared, got %+v", pr)
	}
	if _, claimed, _ := repo.Claim(ctx, "r1", "answered"); !claimed {
		t.Fatal("a re-opened row must be claimable again")
	}

	if err := repo.Reopen(ctx, "r1", "timed_out"); err != nil {
		t.Fatalf("Reopen wrong-status no-op: %v", err)
	}
	if pr, _ := repo.Get(ctx, "r1"); pr.Status != "answered" {
		t.Fatalf("Reopen with a mismatched fromStatus must not change the row, got %s", pr.Status)
	}
}

func TestPendingRequestRepo_StoreErrorsPropagate(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	repo := NewPendingRequestRepo(db)
	if err := repo.Insert(ctx, PendingRequest{RequestID: "x", RequesterAddr: "a"}); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := repo.Insert(ctx, PendingRequest{RequestID: "y", RequesterAddr: "a"}); err == nil {
		t.Error("Insert on a closed store must error")
	}
	if _, _, err := repo.Claim(ctx, "x", "answered"); err == nil {
		t.Error("Claim on a closed store must error")
	}
	if _, err := repo.Get(ctx, "x"); err == nil {
		t.Error("Get on a closed store must error")
	}
	if err := repo.Reopen(ctx, "x", "answered"); err == nil {
		t.Error("Reopen on a closed store must error")
	}
	if _, err := repo.Expired(ctx, 0, 10); err == nil {
		t.Error("Expired on a closed store must error")
	}
	if _, err := repo.DeleteResolvedOlderThan(ctx, 0); err == nil {
		t.Error("DeleteResolvedOlderThan on a closed store must error")
	}
	if _, err := repo.DeleteStalePending(ctx, 0); err == nil {
		t.Error("DeleteStalePending on a closed store must error")
	}
}

func TestPendingRequestRepo_DeleteStalePending(t *testing.T) {
	ctx := t.Context()
	repo := NewPendingRequestRepo(openTestDB(t))

	if err := repo.Insert(ctx, PendingRequest{RequestID: "orphan", RequesterAddr: "a"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if n, err := repo.DeleteStalePending(ctx, 1000); err != nil || n != 0 {
		t.Fatalf("stale prune with past cutoff should delete nothing, got %d (err=%v)", n, err)
	}
	if n, err := repo.DeleteStalePending(ctx, 1<<62); err != nil || n != 1 {
		t.Fatalf("stale prune should reclaim the 1 orphan pending row, got %d (err=%v)", n, err)
	}
}

func TestPendingRequestRepo_AClaimResolvesOnlyWhatItsGuardAdmits(t *testing.T) {
	ctx := t.Context()
	repo := NewPendingRequestRepo(openTestDB(t))
	const set = `{"users":[{"user_id":"bob@example.com"}]}`
	if err := repo.Insert(ctx, PendingRequest{RequestID: "approvers", RequesterAddr: "addr-a", Approvers: ptrStr(set)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(ctx, PendingRequest{RequestID: "single", RequesterAddr: "addr-s", RecipientUserID: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}

	for _, refused := range []struct {
		name, id string
		guard    ClaimGuard
	}{
		{"an unchecked answer to an approver set", "approvers", SingleRecipientOnly()},
		{"an answer checked against another set", "approvers", ApproverSet(`{"users":[{"user_id":"alice@example.com"}]}`)},
		{"an approver-set answer to a single-recipient request", "single", ApproverSet(set)},
	} {
		if _, claimed, err := repo.ClaimResolution(ctx, refused.id, refused.guard, Resolution{Status: "answered"}); err != nil || claimed {
			t.Errorf("%s: claimed=%v err=%v, want refused", refused.name, claimed, err)
		}
	}
	if addr, claimed, err := repo.ClaimResolution(ctx, "approvers", ApproverSet(set), Resolution{Status: "answered"}); err != nil || !claimed || addr != "addr-a" {
		t.Errorf("the checked approver's answer: claimed=%v addr=%q err=%v", claimed, addr, err)
	}
	if _, claimed, err := repo.ClaimResolution(ctx, "single", SingleRecipientOnly(), Resolution{Status: "answered"}); err != nil || !claimed {
		t.Errorf("a single-recipient answer: claimed=%v err=%v", claimed, err)
	}
}
