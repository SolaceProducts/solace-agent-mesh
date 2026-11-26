// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"sync"
	"testing"
)

func TestGatewaySessionKeyRepo_GetOrCreate_CreatesSession(t *testing.T) {
	db := newTestDB(t)
	repo := NewGatewaySessionKeyRepo(db)
	ctx := context.Background()

	id, err := repo.GetOrCreateByExternalKey(ctx, GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeSlack,
		ExternalKey: "C12345:T67890",
		UserID:      "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected non-empty session id")
	}

	sessRepo := NewSessionRepo(db)
	got, err := sessRepo.GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected sessions row to exist")
	}
	if got.UserID != "u1" {
		t.Errorf("user_id = %s, want u1", got.UserID)
	}
}

func TestGatewaySessionKeyRepo_GetOrCreate_Idempotent(t *testing.T) {
	db := newTestDB(t)
	repo := NewGatewaySessionKeyRepo(db)
	ctx := context.Background()

	args := GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeTeams,
		ExternalKey: "personal:user-abc:20260506",
		UserID:      "u1",
	}

	first, err := repo.GetOrCreateByExternalKey(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.GetOrCreateByExternalKey(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("expected idempotent result, got %s != %s", first, second)
	}
}

func TestGatewaySessionKeyRepo_GetOrCreate_AfterSoftDelete_ReturnsFresh(t *testing.T) {
	db := newTestDB(t)
	repo := NewGatewaySessionKeyRepo(db)
	sessRepo := NewSessionRepo(db)
	ctx := context.Background()

	args := GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeSlack,
		ExternalKey: "C-deleteme:T-1",
		UserID:      "u1",
	}

	first, err := repo.GetOrCreateByExternalKey(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if _, delErr := sessRepo.SoftDelete(ctx, first, "u1"); delErr != nil {
		t.Fatal(delErr)
	}

	second, err := repo.GetOrCreateByExternalKey(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Errorf("expected fresh session after soft-delete, got same id %s", first)
	}
	if got, _ := sessRepo.GetByID(ctx, second); got == nil {
		t.Error("expected new session row to be alive")
	}
}

func TestGatewaySessionKeyRepo_GetOrCreate_RollOnOwnerChange(t *testing.T) {
	db := newTestDB(t)
	repo := NewGatewaySessionKeyRepo(db)
	sessRepo := NewSessionRepo(db)
	ctx := context.Background()

	get := func(key, userID string, roll bool) string {
		t.Helper()
		id, err := repo.GetOrCreateByExternalKey(ctx, GetOrCreateByExternalKeyArgs{
			GatewayType: GatewayTypeWhatsApp, ExternalKey: key, UserID: userID, RollOnOwnerChange: roll,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	first := get("whatsapp:15551234567:1", "whatsapp:15551234567", true)
	if again := get("whatsapp:15551234567:1", "whatsapp:15551234567", true); again != first {
		t.Fatalf("same owner got a new session %s, want %s", again, first)
	}
	rolled := get("whatsapp:15551234567:1", "alice@example.com", true)
	if rolled == first {
		t.Fatal("a different owner reused the mapped session")
	}
	if got, _ := sessRepo.GetByID(ctx, rolled); got == nil || got.UserID != "alice@example.com" {
		t.Errorf("rolled session = %+v, want one owned by the new principal", got)
	}
	if got, _ := sessRepo.GetByID(ctx, first); got == nil {
		t.Error("the previous owner's session was not left alive")
	}

	shared := get("slack:C1:T1", "u1", false)
	if reused := get("slack:C1:T1", "u2", false); reused != shared {
		t.Errorf("without the flag a different caller got %s, want the mapped %s", reused, shared)
	}
}

func TestGatewaySessionKeyRepo_SoftDelete_ClearsMapping(t *testing.T) {
	db := newTestDB(t)
	repo := NewGatewaySessionKeyRepo(db)
	sessRepo := NewSessionRepo(db)
	ctx := context.Background()

	args := GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeEmail,
		ExternalKey: "<thread@example.com>",
		UserID:      "u1",
	}

	id, err := repo.GetOrCreateByExternalKey(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if _, delErr := sessRepo.SoftDelete(ctx, id, "u1"); delErr != nil {
		t.Fatal(delErr)
	}

	got, err := repo.GetByGatewayAndExternalKeys(ctx, GatewayTypeEmail, []string{args.ExternalKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[args.ExternalKey]; ok {
		t.Errorf("expected mapping to be cleared after soft-delete, got %v", got)
	}
}

func TestGatewaySessionKeyRepo_GetByGatewayAndSessionIDs(t *testing.T) {
	db := newTestDB(t)
	repo := NewGatewaySessionKeyRepo(db)
	ctx := context.Background()

	idA, _ := repo.GetOrCreateByExternalKey(ctx, GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeScheduled,
		ExternalKey: "exec-a",
		UserID:      "u1",
	})
	idB, _ := repo.GetOrCreateByExternalKey(ctx, GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeScheduled,
		ExternalKey: "exec-b",
		UserID:      "u1",
	})
	idC, _ := repo.GetOrCreateByExternalKey(ctx, GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeSlack,
		ExternalKey: "C-x:T-y",
		UserID:      "u1",
	})

	got, err := repo.GetByGatewayAndSessionIDs(ctx, GatewayTypeScheduled, []string{idA, idB, idC})
	if err != nil {
		t.Fatal(err)
	}
	if got[idA] != "exec-a" {
		t.Errorf("got[idA] = %q, want exec-a", got[idA])
	}
	if got[idB] != "exec-b" {
		t.Errorf("got[idB] = %q, want exec-b", got[idB])
	}
	if _, present := got[idC]; present {
		t.Error("slack session must not appear in scheduled lookup")
	}
}

func TestGatewaySessionKeyRepo_CreateSessionWithMapping_Atomic(t *testing.T) {
	db := newTestDB(t)
	sessRepo := NewSessionRepo(db)
	repo := NewGatewaySessionKeyRepo(db)
	ctx := context.Background()

	sess := &Session{
		ID:     "00000000-0000-0000-0000-00000000aaa1",
		UserID: "u1",
	}
	if err := repo.CreateSessionWithMapping(ctx, sess, GatewayTypeScheduled, "exec-aaa1"); err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if got, err := sessRepo.GetByID(ctx, sess.ID); err != nil {
		t.Fatal(err)
	} else if got == nil {
		t.Fatal("session row missing after CreateSessionWithMapping")
	}
	mappings, err := repo.GetByGatewayAndExternalKeys(ctx, GatewayTypeScheduled, []string{"exec-aaa1"})
	if err != nil {
		t.Fatal(err)
	}
	if mappings["exec-aaa1"] != sess.ID {
		t.Errorf("mapping = %s, want %s", mappings["exec-aaa1"], sess.ID)
	}

	prior := &Session{ID: "00000000-0000-0000-0000-00000000bbb1", UserID: "u1"}
	if createErr := sessRepo.Create(ctx, prior); createErr != nil {
		t.Fatal(createErr)
	}
	if mapErr := repo.CreateMapping(ctx, GatewayTypeScheduled, "exec-conflict", prior.ID); mapErr != nil {
		t.Fatal(mapErr)
	}

	conflict := &Session{
		ID:     "00000000-0000-0000-0000-00000000bbb2",
		UserID: "u1",
	}
	if conflictErr := repo.CreateSessionWithMapping(ctx, conflict, GatewayTypeScheduled, "exec-conflict"); conflictErr == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if got, getErr := sessRepo.GetByID(ctx, conflict.ID); getErr != nil {
		t.Fatal(getErr)
	} else if got != nil {
		t.Errorf("orphan session row created on rollback path: %s", got.ID)
	}
	mappings, err = repo.GetByGatewayAndExternalKeys(ctx, GatewayTypeScheduled, []string{"exec-conflict"})
	if err != nil {
		t.Fatal(err)
	}
	if mappings["exec-conflict"] != prior.ID {
		t.Errorf("conflict mapping mutated: got %s, want %s", mappings["exec-conflict"], prior.ID)
	}
}

func TestGatewaySessionKeyRepo_CreateMapping_LinksExistingSession(t *testing.T) {
	db := newTestDB(t)
	sessRepo := NewSessionRepo(db)
	repo := NewGatewaySessionKeyRepo(db)
	ctx := context.Background()

	preID := "00000000-0000-0000-0000-0000000abcde"
	if err := sessRepo.Create(ctx, &Session{ID: preID, UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateMapping(ctx, GatewayTypeScheduled, "exec-pre", preID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByGatewayAndExternalKeys(ctx, GatewayTypeScheduled, []string{"exec-pre"})
	if err != nil {
		t.Fatal(err)
	}
	if got["exec-pre"] != preID {
		t.Errorf("mapping = %s, want %s", got["exec-pre"], preID)
	}
}

func TestGatewaySessionKeyRepo_GetByGatewayAndSessionIDs_Empty(t *testing.T) {
	db := newTestDB(t)
	repo := NewGatewaySessionKeyRepo(db)

	got, err := repo.GetByGatewayAndSessionIDs(context.Background(), GatewayTypeSlack, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestGatewaySessionKeyRepo_GetOrCreate_ConcurrentCallersConvergeOnOneSession(t *testing.T) {
	db := newTestDB(t)
	repo := NewGatewaySessionKeyRepo(db)
	sessRepo := NewSessionRepo(db)
	ctx := context.Background()

	const goroutines = 16
	args := GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeSlack,
		ExternalKey: "C99999:RACE",
		UserID:      "race-user",
	}

	results := make([]string, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			id, err := repo.GetOrCreateByExternalKey(ctx, args)
			results[i] = id
			errs[i] = err
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	winner := results[0]
	for i, got := range results {
		if got != winner {
			t.Errorf("goroutine %d returned %s, want %s", i, got, winner)
		}
	}
	if got, err := sessRepo.GetByID(ctx, winner); err != nil {
		t.Fatal(err)
	} else if got == nil {
		t.Fatal("winning session row missing")
	}
	mappings, err := repo.GetByGatewayAndSessionIDs(ctx, GatewayTypeSlack, []string{winner})
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 {
		t.Errorf("expected exactly one mapping row for winner, got %d", len(mappings))
	}
}
