// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sharing

import (
	"errors"
	"slices"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func skipUnlessPostgres(t *testing.T, db *store.DB) {
	t.Helper()
	if db.Dialect() != "postgres" {
		t.Skip("Postgres only — advisory lock under contention")
	}
}

func grantedTo(events []auth.ShareGrantEvent, email string) bool {
	return slices.ContainsFunc(events, func(e auth.ShareGrantEvent) bool { return e.RecipientEmail == email })
}

func revokedTo(events []auth.ShareRevokeEvent, email string) bool {
	return slices.ContainsFunc(events, func(e auth.ShareRevokeEvent) bool { return e.RecipientEmail == email })
}

func templateDeletedContains(events []auth.TemplateDeletedEvent, email string) bool {
	return slices.ContainsFunc(events, func(e auth.TemplateDeletedEvent) bool {
		return slices.Contains(e.RecipientEmails, email)
	})
}

func TestPromptService_Concurrent_BatchShare_SameRecipient_GrantsOnce(t *testing.T) {
	db := store.TestDB(t)
	skipUnlessPostgres(t, db)

	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
				{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
			}, "owner@example.com")
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("BatchShare error: %v", err)
	}

	shares, err := svc.GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 {
		t.Fatalf("len(shares) = %d, want 1", len(shares))
	}
	if shares[0].UserEmail != "alice@example.com" {
		t.Errorf("shares[0].UserEmail = %q, want alice@example.com", shares[0].UserEmail)
	}

	grants, _, _, _ := audit.snapshot()
	if len(grants) != 1 {
		t.Fatalf("len(grants) = %d, want exactly 1; got %+v", len(grants), grants)
	}
	if grants[0].RecipientEmail != "alice@example.com" {
		t.Errorf("grants[0].RecipientEmail = %q, want alice@example.com", grants[0].RecipientEmail)
	}
}

func TestPromptService_Concurrent_BatchShare_DeleteAll_AuditBalanced(t *testing.T) {
	db := store.TestDB(t)
	skipUnlessPostgres(t, db)

	const iterations = 50
	for i := range iterations {
		audit := &recordingAuditLogger{}
		svc := NewPromptService(func() *store.DB { return db }, audit)
		group := promptShareTestFixture(t, db, "owner@example.com", nil)

		var wg sync.WaitGroup
		var grantErr, deleteErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, grantErr = svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
				{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
			}, "owner@example.com")
		}()
		go func() {
			defer wg.Done()
			deleteErr = svc.DeleteAllShares(t.Context(), group.ID, "owner@example.com")
		}()
		wg.Wait()
		if grantErr != nil {
			t.Fatalf("iter %d BatchShare: %v", i, grantErr)
		}
		if deleteErr != nil {
			t.Fatalf("iter %d DeleteAllShares: %v", i, deleteErr)
		}

		shares, err := svc.GetShares(t.Context(), group.ID)
		if err != nil {
			t.Fatalf("iter %d GetShares: %v", i, err)
		}
		rowPresent := slices.ContainsFunc(shares, func(s entrypoint.PromptShareResponse) bool {
			return s.UserEmail == "alice@example.com"
		})

		grants, _, revocations, templateDeletions := audit.snapshot()
		grantedAlice := grantedTo(grants, "alice@example.com")
		aliceInTemplateDeleted := templateDeletedContains(templateDeletions, "alice@example.com")
		aliceRevoked := revokedTo(revocations, "alice@example.com")

		switch {
		case grantedAlice && rowPresent:
			if aliceInTemplateDeleted {
				t.Errorf("iter %d: grant landed after delete but alice in template-deleted; row present", i)
			}
		case grantedAlice && !rowPresent:
			if !aliceInTemplateDeleted && !aliceRevoked {
				t.Errorf("iter %d: AUDIT LOSS — grant emitted for alice, row gone, no template-deleted nor revoke event. grants=%v templateDeleted=%v", i, grants, templateDeletions)
			}
		case !grantedAlice && rowPresent:
			t.Errorf("iter %d: row present for alice but no grant event recorded", i)
		}
	}
}

func TestPromptService_Concurrent_UpdateShare_DeleteShare(t *testing.T) {
	db := store.TestDB(t)
	skipUnlessPostgres(t, db)

	const iterations = 30
	for i := range iterations {
		audit := &recordingAuditLogger{}
		svc := NewPromptService(func() *store.DB { return db }, audit)
		group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
			{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
		})

		var wg sync.WaitGroup
		var updateErr, deleteErr error
		var updateResp *entrypoint.PromptShareResponse
		wg.Add(2)
		go func() {
			defer wg.Done()
			updateResp, updateErr = svc.UpdateShare(t.Context(), group.ID, "editor@example.com", "resource_viewer", "owner@example.com")
		}()
		go func() {
			defer wg.Done()
			deleteErr = svc.DeleteShare(t.Context(), group.ID, "editor@example.com", "owner@example.com")
		}()
		wg.Wait()

		shares, err := svc.GetShares(t.Context(), group.ID)
		if err != nil {
			t.Fatalf("iter %d GetShares: %v", i, err)
		}
		_, levelChanges, revocations, _ := audit.snapshot()

		updateSucceeded := updateErr == nil
		deleteSucceeded := deleteErr == nil
		updateMissed := errors.Is(updateErr, entrypoint.ErrShareNotFound)
		deleteMissed := errors.Is(deleteErr, entrypoint.ErrShareNotFound)

		if !updateSucceeded && !updateMissed {
			t.Fatalf("iter %d: unexpected UpdateShare error: %v", i, updateErr)
		}
		if !deleteSucceeded && !deleteMissed {
			t.Fatalf("iter %d: unexpected DeleteShare error: %v", i, deleteErr)
		}

		switch {
		case updateSucceeded && deleteSucceeded:
			if len(shares) != 0 {
				t.Errorf("iter %d: both succeeded but shares=%v; row should be gone", i, shares)
			}
			if len(levelChanges) != 1 {
				t.Errorf("iter %d: both succeeded; want 1 level-change, got %d", i, len(levelChanges))
			}
			if len(revocations) != 1 {
				t.Errorf("iter %d: both succeeded; want 1 revoke, got %d", i, len(revocations))
			}
			if updateResp == nil {
				t.Errorf("iter %d: update succeeded but resp is nil", i)
			}
		case updateMissed && deleteSucceeded:
			if len(shares) != 0 {
				t.Errorf("iter %d: delete won; shares should be empty, got %v", i, shares)
			}
			if len(levelChanges) != 0 {
				t.Errorf("iter %d: update missed; want 0 level-change events, got %d", i, len(levelChanges))
			}
			if len(revocations) != 1 {
				t.Errorf("iter %d: delete won; want 1 revoke, got %d", i, len(revocations))
			}
		case updateSucceeded && deleteMissed:
			t.Errorf("iter %d: update succeeded but delete missed — inconsistent (row state) shares=%v", i, shares)
		case updateMissed && deleteMissed:
			t.Errorf("iter %d: both ops missed pre-seeded row; inconsistent", i)
		}
	}
}

func TestPromptService_Concurrent_BulkReplace_BatchShare(t *testing.T) {
	db := store.TestDB(t)
	skipUnlessPostgres(t, db)

	const iterations = 30
	for i := range iterations {
		audit := &recordingAuditLogger{}
		svc := NewPromptService(func() *store.DB { return db }, audit)
		group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
			{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
		})

		var wg sync.WaitGroup
		var bulkErr, batchErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, bulkErr = svc.BulkReplaceShares(t.Context(), group.ID, []entrypoint.ShareRequest{
				{UserEmail: "bob@example.com", AccessLevel: "resource_viewer"},
			}, "owner@example.com")
		}()
		go func() {
			defer wg.Done()
			_, batchErr = svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
				{UserEmail: "bob@example.com", AccessLevel: "resource_viewer"},
			}, "owner@example.com")
		}()
		wg.Wait()
		if bulkErr != nil {
			t.Fatalf("iter %d BulkReplaceShares: %v", i, bulkErr)
		}
		if batchErr != nil {
			t.Fatalf("iter %d BatchShare: %v", i, batchErr)
		}

		shares, err := svc.GetShares(t.Context(), group.ID)
		if err != nil {
			t.Fatalf("iter %d GetShares: %v", i, err)
		}
		finalEmails := make(map[string]bool, len(shares))
		for _, s := range shares {
			finalEmails[s.UserEmail] = true
		}

		grants, _, revocations, _ := audit.snapshot()

		for email := range finalEmails {
			if email == "alice@example.com" {
				continue
			}
			if !grantedTo(grants, email) {
				t.Errorf("iter %d: %q in final state but no grant event; grants=%v", i, email, grants)
			}
		}

		aliceInFinal := finalEmails["alice@example.com"]
		aliceRevoked := revokedTo(revocations, "alice@example.com")
		if !aliceInFinal && !aliceRevoked {
			t.Errorf("iter %d: alice gone from final state but no revoke event; final=%v revocations=%v", i, finalEmails, revocations)
		}
		if aliceInFinal && aliceRevoked {
			t.Errorf("iter %d: alice still in final state but revoke event fired; revocations=%v", i, revocations)
		}
	}
}
