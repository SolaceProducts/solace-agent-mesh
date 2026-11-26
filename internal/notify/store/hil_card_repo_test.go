// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"slices"
	"testing"
)

func seedCard(t *testing.T, db *DB, c HILCard) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO notify_hil_cards (request_id, channel_type, card, created_at) VALUES (?, ?, ?, ?)`,
		c.RequestID, c.ChannelType, c.Card, c.CreatedAt); err != nil {
		t.Fatalf("seed card %+v: %v", c, err)
	}
}

func TestHILCardRepo_DeleteListedRemovesOnlyTheRowsItWasGiven(t *testing.T) {
	db := openTestDB(t)
	repo := NewHILCardRepo(db)
	listed := HILCard{RequestID: "req-1", ChannelType: "slack", Card: `{"ts":"1.1"}`, CreatedAt: 100}
	others := []HILCard{
		{RequestID: "req-2", ChannelType: "slack", Card: `{"ts":"1.1"}`, CreatedAt: 100},
		{RequestID: "req-1", ChannelType: "teams", Card: `{"ts":"1.1"}`, CreatedAt: 100},
		{RequestID: "req-1", ChannelType: "slack", Card: `{"ts":"2.2"}`, CreatedAt: 100},
		{RequestID: "req-1", ChannelType: "slack", Card: `{"ts":"1.1"}`, CreatedAt: 101},
	}
	seedCard(t, db, listed)
	for _, c := range others {
		seedCard(t, db, c)
	}

	if err := repo.DeleteListed(t.Context(), []HILCard{listed}); err != nil {
		t.Fatalf("DeleteListed(%+v): %v", listed, err)
	}

	var left []HILCard
	for _, id := range []string{"req-1", "req-2"} {
		cards, err := repo.List(t.Context(), id)
		if err != nil {
			t.Fatalf("List(%s): %v", id, err)
		}
		left = append(left, cards...)
	}
	if len(left) != len(others) {
		t.Fatalf("cards left = %+v, want exactly the %d rows that differ from the listed one in one column", left, len(others))
	}
	for _, want := range others {
		if !slices.Contains(left, want) {
			t.Errorf("row %+v was deleted although it differs from the listed card", want)
		}
	}
	if slices.Contains(left, listed) {
		t.Errorf("the listed card %+v was not deleted", listed)
	}
}

func TestHILCardRepo_ResolvedRequestIDsListsOnlyResolvedRequestsPastTheGrace(t *testing.T) {
	db := openTestDB(t)
	cards, pending := NewHILCardRepo(db), NewPendingRequestRepo(db)
	for _, id := range []string{"resolved", "open"} {
		if err := pending.Insert(t.Context(), PendingRequest{RequestID: id, RequesterAddr: "addr", RecipientUserID: "alice"}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		if err := cards.Add(t.Context(), id, "slack", `{"ts":"1.1"}`); err != nil {
			t.Fatalf("add card for %s: %v", id, err)
		}
	}
	if err := cards.Add(t.Context(), "unknown", "slack", `{"ts":"1.1"}`); err != nil {
		t.Fatalf("add card for an unknown request: %v", err)
	}
	if _, claimed, err := pending.Claim(t.Context(), "resolved", "answered"); err != nil || !claimed {
		t.Fatalf("claim: claimed %v, err %v", claimed, err)
	}
	resolvedAt := *mustGetPending(t, pending, "resolved").ResolvedAt

	before, err := cards.ResolvedRequestIDs(t.Context(), resolvedAt, 10)
	if err != nil || len(before) != 0 {
		t.Errorf("ids with a cutoff at the resolution time = %v (err %v), want none: it is not yet past the grace", before, err)
	}
	after, err := cards.ResolvedRequestIDs(t.Context(), resolvedAt+1, 10)
	if err != nil || !slices.Equal(after, []string{"resolved"}) {
		t.Errorf("ids with a cutoff after the resolution = %v (err %v), want only the resolved request", after, err)
	}
}

func mustGetPending(t *testing.T, repo *PendingRequestRepo, id string) *PendingRequest {
	t.Helper()
	pr, err := repo.Get(t.Context(), id)
	if err != nil || pr == nil {
		t.Fatalf("Get(%s) = %+v (err %v)", id, pr, err)
	}
	return pr
}
