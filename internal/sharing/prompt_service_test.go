// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sharing

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type recordingAuditLogger struct {
	auth.NoopAuditLogger
	mu                sync.Mutex
	grants            []auth.ShareGrantEvent
	levelChanges      []auth.ShareLevelChangeEvent
	revocations       []auth.ShareRevokeEvent
	templateDeletions []auth.TemplateDeletedEvent
}

func recordEvent[E any](r *recordingAuditLogger, dest *[]E, event E) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*dest = append(*dest, event)
}

func (r *recordingAuditLogger) LogShareGrant(_ context.Context, e auth.ShareGrantEvent) {
	recordEvent(r, &r.grants, e)
}
func (r *recordingAuditLogger) LogShareLevelChange(_ context.Context, e auth.ShareLevelChangeEvent) {
	recordEvent(r, &r.levelChanges, e)
}
func (r *recordingAuditLogger) LogShareRevoke(_ context.Context, e auth.ShareRevokeEvent) {
	recordEvent(r, &r.revocations, e)
}
func (r *recordingAuditLogger) LogTemplateDeleted(_ context.Context, e auth.TemplateDeletedEvent) {
	recordEvent(r, &r.templateDeletions, e)
}

func (r *recordingAuditLogger) LogActivationChange(_ context.Context, _ auth.ActivationChangeEvent) {}
func (r *recordingAuditLogger) LogConsentChange(_ context.Context, _ auth.ConsentChangeEvent)       {}

func (r *recordingAuditLogger) snapshot() (
	[]auth.ShareGrantEvent,
	[]auth.ShareLevelChangeEvent,
	[]auth.ShareRevokeEvent,
	[]auth.TemplateDeletedEvent,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.grants),
		slices.Clone(r.levelChanges),
		slices.Clone(r.revocations),
		slices.Clone(r.templateDeletions)
}

func promptShareTestFixture(t *testing.T, db *store.DB, owner string, shares []store.ShareInput) *store.PromptGroup {
	t.Helper()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner,
	}
	if err := store.NewPromptGroupRepo(db).Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	if len(shares) > 0 {
		if _, _, err := store.NewResourceAccessShareRepo(db).BatchShare(
			t.Context(), group.ID, store.ResourceTypePromptGroup, shares, owner,
		); err != nil {
			t.Fatal(err)
		}
	}
	return group
}

func TestPromptService_GetShares_NoCollaborators(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	shares, err := svc.GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shares == nil {
		t.Fatal("got nil; expected empty slice")
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) = %d, want 0", len(shares))
	}
}

func TestPromptService_GetShares_WithCollaborators(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
		{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"},
	})

	shares, err := svc.GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 2 {
		t.Fatalf("len(shares) = %d, want 2", len(shares))
	}

	emails := []string{shares[0].UserEmail, shares[1].UserEmail}
	slices.Sort(emails)
	if want := []string{"editor@example.com", "viewer@example.com"}; !slices.Equal(emails, want) {
		t.Errorf("emails = %v, want %v", emails, want)
	}
	for _, sh := range shares {
		if sh.SharedByEmail != "owner@example.com" {
			t.Errorf("share %q sharedByEmail = %q, want owner@example.com", sh.UserEmail, sh.SharedByEmail)
		}
		if _, err := time.Parse("2006-01-02T15:04:05.000000+00:00", sh.CreatedAt); err != nil {
			t.Errorf("share %q createdAt %q not ISO 8601 (ms-precision): %v", sh.UserEmail, sh.CreatedAt, err)
		}
	}
}

func TestPromptService_BatchShare_NewShare(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	resp, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	}, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalProcessed != 1 {
		t.Errorf("TotalProcessed = %d, want 1", resp.TotalProcessed)
	}
	if len(resp.Created) != 1 {
		t.Fatalf("len(Created) = %d, want 1", len(resp.Created))
	}
	if len(resp.Updated) != 0 {
		t.Errorf("len(Updated) = %d, want 0", len(resp.Updated))
	}
	if got := resp.Created[0].UserEmail; got != "editor@example.com" {
		t.Errorf("Created[0].UserEmail = %q, want editor@example.com", got)
	}
	if got := resp.Created[0].AccessLevel; got != "resource_editor" {
		t.Errorf("Created[0].AccessLevel = %q, want resource_editor", got)
	}
}

func TestPromptService_BatchShare_UpsertExisting(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	if _, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "editor@example.com", AccessLevel: "resource_viewer"},
	}, "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	resp, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	}, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Created) != 0 {
		t.Errorf("len(Created) = %d, want 0", len(resp.Created))
	}
	if len(resp.Updated) != 1 {
		t.Fatalf("len(Updated) = %d, want 1", len(resp.Updated))
	}
	if got := resp.Updated[0].AccessLevel; got != "resource_editor" {
		t.Errorf("Updated[0].AccessLevel = %q, want resource_editor", got)
	}
}

func TestPromptService_BatchShare_NormalisesEmail(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	resp, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "  Editor@Example.COM  ", AccessLevel: "resource_editor"},
	}, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Created) != 1 {
		t.Fatalf("len(Created) = %d, want 1", len(resp.Created))
	}
	if got := resp.Created[0].UserEmail; got != "editor@example.com" {
		t.Errorf("Created[0].UserEmail = %q, want editor@example.com", got)
	}
}

func TestPromptService_BulkReplaceShares_RevokesAll(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
		{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"},
	})

	result, err := svc.BulkReplaceShares(t.Context(), group.ID, nil, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 0 {
		t.Errorf("len(Created) = %d, want 0", len(result.Created))
	}
	if len(result.Updated) != 0 {
		t.Errorf("len(Updated) = %d, want 0", len(result.Updated))
	}
	if len(result.RevokedEmails) != 2 {
		t.Fatalf("len(RevokedEmails) = %d, want 2", len(result.RevokedEmails))
	}

	shares, err := svc.GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after replace-with-empty = %d, want 0", len(shares))
	}
}

func TestPromptService_BulkReplaceShares_MixedDiff(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "stays@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "promoted@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "dropped@example.com", AccessLevel: "resource_viewer"},
	})

	result, err := svc.BulkReplaceShares(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "stays@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "promoted@example.com", AccessLevel: "resource_editor"},
		{UserEmail: "added@example.com", AccessLevel: "resource_viewer"},
	}, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Created) != 1 || result.Created[0].UserEmail != "added@example.com" {
		t.Errorf("Created = %v, want [added@example.com]", result.Created)
	}
	if len(result.Updated) != 1 || result.Updated[0].UserEmail != "promoted@example.com" {
		t.Errorf("Updated = %v, want [promoted@example.com]", result.Updated)
	}
	if len(result.RevokedEmails) != 1 || result.RevokedEmails[0] != "dropped@example.com" {
		t.Errorf("RevokedEmails = %v, want [dropped@example.com]", result.RevokedEmails)
	}

	shares, err := svc.GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 3 {
		t.Fatalf("len(shares) = %d, want 3", len(shares))
	}
	gotLevels := map[string]string{}
	for _, s := range shares {
		gotLevels[s.UserEmail] = s.AccessLevel
	}
	want := map[string]string{
		"stays@example.com":    "resource_viewer",
		"promoted@example.com": "resource_editor",
		"added@example.com":    "resource_viewer",
	}
	for email, level := range want {
		if gotLevels[email] != level {
			t.Errorf("%s level = %q, want %q", email, gotLevels[email], level)
		}
	}
}

func TestPromptService_BulkReplaceShares_NoOp(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	})

	result, err := svc.BulkReplaceShares(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "EDITOR@Example.COM", AccessLevel: "resource_editor"},
	}, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 0 || len(result.Updated) != 0 || len(result.RevokedEmails) != 0 {
		t.Errorf("expected no diff; got Created=%d, Updated=%d, RevokedEmails=%d",
			len(result.Created), len(result.Updated), len(result.RevokedEmails))
	}
}

func TestPromptService_UpdateShare_ChangesLevel(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	})

	before, err := svc.GetShares(t.Context(), group.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("seed: shares = %+v, err = %v", before, err)
	}
	originalCreatedAt := before[0].CreatedAt

	resp, err := svc.UpdateShare(t.Context(), group.ID, "editor@example.com", "resource_viewer", "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("got nil response, want updated share")
	}
	if resp.UserEmail != "editor@example.com" {
		t.Errorf("UserEmail = %q, want editor@example.com", resp.UserEmail)
	}
	if resp.AccessLevel != "resource_viewer" {
		t.Errorf("AccessLevel = %q, want resource_viewer (downgrade)", resp.AccessLevel)
	}
	if resp.SharedByEmail != "owner@example.com" {
		t.Errorf("SharedByEmail = %q, want owner@example.com (preserved on update)", resp.SharedByEmail)
	}
	if resp.CreatedAt != originalCreatedAt {
		t.Errorf("CreatedAt = %q, want %q (preserved on update)", resp.CreatedAt, originalCreatedAt)
	}
	if resp.UpdatedAt == "" {
		t.Error("UpdatedAt is empty")
	}
}

func TestPromptService_DeleteShare_RemovesShare(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	})

	if err := svc.DeleteShare(t.Context(), group.ID, "editor@example.com", "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	shares, err := svc.GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after DeleteShare = %d, want 0", len(shares))
	}
}

func TestPromptService_DeleteShare_NotFound(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	err := svc.DeleteShare(t.Context(), group.ID, "ghost@example.com", "owner@example.com")
	if !errors.Is(err, entrypoint.ErrShareNotFound) {
		t.Errorf("err = %v, want ErrShareNotFound", err)
	}
}

func TestPromptService_UpdateShare_NotFound(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	resp, err := svc.UpdateShare(t.Context(), group.ID, "ghost@example.com", "resource_viewer", "owner@example.com")
	if !errors.Is(err, entrypoint.ErrShareNotFound) {
		t.Errorf("err = %v, want ErrShareNotFound", err)
	}
	if resp != nil {
		t.Errorf("expected nil response on not-found, got %+v", resp)
	}
}

func TestPromptService_BatchShare_EmitsGrantPerCreated(t *testing.T) {
	db := store.TestDB(t)
	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	_, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	}, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.grants) != 1 {
		t.Fatalf("len(grants) = %d, want 1", len(audit.grants))
	}
	g := audit.grants[0]
	if g.RecipientEmail != "editor@example.com" {
		t.Errorf("RecipientEmail = %q, want editor@example.com", g.RecipientEmail)
	}
	if g.AccessLevel != "resource_editor" {
		t.Errorf("AccessLevel = %q, want resource_editor", g.AccessLevel)
	}
	if g.ResourceID != group.ID {
		t.Errorf("ResourceID = %q, want %q", g.ResourceID, group.ID)
	}
	if g.ResourceType != "prompt_group" {
		t.Errorf("ResourceType = %q, want prompt_group", g.ResourceType)
	}
	if g.Actor != "owner@example.com" {
		t.Errorf("Actor = %q, want owner@example.com", g.Actor)
	}
}

func TestPromptService_BatchShare_EmitsLevelChangeOnUpsert(t *testing.T) {
	db := store.TestDB(t)
	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_viewer"},
	})

	if _, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	}, "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	if len(audit.grants) != 0 {
		t.Errorf("len(grants) = %d, want 0 (existing recipient is an update, not a grant)", len(audit.grants))
	}
	if len(audit.levelChanges) != 1 {
		t.Fatalf("len(levelChanges) = %d, want 1", len(audit.levelChanges))
	}
	ev := audit.levelChanges[0]
	if ev.RecipientEmail != "editor@example.com" {
		t.Errorf("RecipientEmail = %q, want editor@example.com", ev.RecipientEmail)
	}
	if ev.AccessLevel != "resource_editor" {
		t.Errorf("AccessLevel = %q, want resource_editor (the new level)", ev.AccessLevel)
	}
	if ev.Actor != "owner@example.com" {
		t.Errorf("Actor = %q, want owner@example.com", ev.Actor)
	}
}

func TestPromptService_BatchShare_NoOpReshare_NoLevelChangeEvent(t *testing.T) {
	db := store.TestDB(t)
	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_viewer"},
	})

	if _, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "editor@example.com", AccessLevel: "resource_viewer"},
	}, "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	if len(audit.grants) != 0 {
		t.Errorf("len(grants) = %d, want 0 (existing recipient is not a grant)", len(audit.grants))
	}
	if len(audit.levelChanges) != 0 {
		t.Errorf("len(levelChanges) = %d, want 0 (level did not change)", len(audit.levelChanges))
	}
}

func TestPromptService_BulkReplaceShares_EmitsAuditPerOutcome(t *testing.T) {
	db := store.TestDB(t)
	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "stays@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "promoted@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "dropped@example.com", AccessLevel: "resource_viewer"},
	})

	_, err := svc.BulkReplaceShares(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "stays@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "promoted@example.com", AccessLevel: "resource_editor"},
		{UserEmail: "added@example.com", AccessLevel: "resource_viewer"},
	}, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}

	if len(audit.grants) != 1 || audit.grants[0].RecipientEmail != "added@example.com" {
		t.Errorf("grants = %v, want one for added@example.com", audit.grants)
	}
	if len(audit.levelChanges) != 1 || audit.levelChanges[0].RecipientEmail != "promoted@example.com" {
		t.Errorf("levelChanges = %v, want one for promoted@example.com", audit.levelChanges)
	}
	if len(audit.revocations) != 1 || audit.revocations[0].RecipientEmail != "dropped@example.com" {
		t.Errorf("revocations = %v, want one for dropped@example.com", audit.revocations)
	}
}

func TestPromptService_UpdateShare_EmitsLevelChange(t *testing.T) {
	db := store.TestDB(t)
	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	})

	if _, err := svc.UpdateShare(t.Context(), group.ID, "editor@example.com", "resource_viewer", "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	if len(audit.levelChanges) != 1 {
		t.Fatalf("len(levelChanges) = %d, want 1", len(audit.levelChanges))
	}
	ev := audit.levelChanges[0]
	if ev.RecipientEmail != "editor@example.com" {
		t.Errorf("RecipientEmail = %q, want editor@example.com", ev.RecipientEmail)
	}
	if ev.AccessLevel != "resource_viewer" {
		t.Errorf("AccessLevel = %q, want resource_viewer (the new level)", ev.AccessLevel)
	}
	if ev.ResourceID != group.ID {
		t.Errorf("ResourceID = %q, want %q", ev.ResourceID, group.ID)
	}
}

func TestPromptService_DeleteAllShares_EmitsTemplateDeleted(t *testing.T) {
	db := store.TestDB(t)
	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "alice@example.com", AccessLevel: "resource_editor"},
		{UserEmail: "bob@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "carol@example.com", AccessLevel: "resource_viewer"},
	})

	if err := svc.DeleteAllShares(t.Context(), group.ID, "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	if len(audit.templateDeletions) != 1 {
		t.Fatalf("len(templateDeletions) = %d, want 1", len(audit.templateDeletions))
	}
	ev := audit.templateDeletions[0]
	if ev.Actor != "owner@example.com" {
		t.Errorf("Actor = %q, want owner@example.com", ev.Actor)
	}
	if ev.ResourceID != group.ID {
		t.Errorf("ResourceID = %q, want %q", ev.ResourceID, group.ID)
	}
	if ev.ResourceType != string(store.ResourceTypePromptGroup) {
		t.Errorf("ResourceType = %q, want %q", ev.ResourceType, store.ResourceTypePromptGroup)
	}
	if len(ev.RecipientEmails) != 3 {
		t.Fatalf("len(RecipientEmails) = %d, want 3; got %v", len(ev.RecipientEmails), ev.RecipientEmails)
	}
	want := map[string]bool{"alice@example.com": true, "bob@example.com": true, "carol@example.com": true}
	for _, e := range ev.RecipientEmails {
		if !want[e] {
			t.Errorf("unexpected recipient %q in event", e)
		}
	}
}

func TestPromptService_DeleteAllShares_NoShares_EmitsNothing(t *testing.T) {
	db := store.TestDB(t)
	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	if err := svc.DeleteAllShares(t.Context(), group.ID, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(audit.templateDeletions) != 0 {
		t.Errorf("len(templateDeletions) = %d, want 0 (no recipients to log)", len(audit.templateDeletions))
	}
}

func TestPromptService_DeleteShare_EmitsRevoke(t *testing.T) {
	db := store.TestDB(t)
	audit := &recordingAuditLogger{}
	svc := NewPromptService(func() *store.DB { return db }, audit)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
	})

	if err := svc.DeleteShare(t.Context(), group.ID, "editor@example.com", "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	if len(audit.revocations) != 1 {
		t.Fatalf("len(revocations) = %d, want 1", len(audit.revocations))
	}
	ev := audit.revocations[0]
	if ev.RecipientEmail != "editor@example.com" {
		t.Errorf("RecipientEmail = %q, want editor@example.com", ev.RecipientEmail)
	}
	if ev.ResourceID != group.ID {
		t.Errorf("ResourceID = %q, want %q", ev.ResourceID, group.ID)
	}
}

func TestPromptService_BulkReplaceShares_RollbackOnRevokeFailure(t *testing.T) {
	db := store.TestDB(t)
	if db.Dialect() != "sqlite" {
		t.Skip("trigger-based fault injection is SQLite-only; the composite WithTx atomicity contract is dialect-independent")
	}
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", []store.ShareInput{
		{UserEmail: "dropped@example.com", AccessLevel: "resource_viewer"},
	})

	if _, err := db.ExecContext(t.Context(), `
		CREATE TRIGGER force_unshare_failure
		BEFORE DELETE ON resource_access_shares
		BEGIN
			SELECT RAISE(ABORT, 'simulated unshare failure');
		END
	`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	_, err := svc.BulkReplaceShares(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "added@example.com", AccessLevel: "resource_viewer"},
	}, "owner@example.com")
	if err == nil {
		t.Fatal("expected error from BulkReplaceShares; got nil")
	}

	if _, dropErr := db.ExecContext(t.Context(), `DROP TRIGGER force_unshare_failure`); dropErr != nil {
		t.Fatalf("drop trigger: %v", dropErr)
	}

	shares, err := svc.GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 {
		t.Fatalf("len(shares) after rollback = %d, want 1; shares = %+v", len(shares), shares)
	}
	if shares[0].UserEmail != "dropped@example.com" {
		t.Errorf("shares[0].userEmail = %q, want dropped@example.com — pre-call state corrupted", shares[0].UserEmail)
	}
}

func TestPromptService_BatchShare_EmptyUserEmail_Errors(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	_, err := svc.BatchShare(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "  ", AccessLevel: "resource_viewer"},
	}, "owner@example.com")
	if err == nil {
		t.Fatal("expected error for empty userEmail; got nil")
	}
}

func TestPromptService_BulkReplaceShares_EmptyUserEmail_Errors(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	_, err := svc.BulkReplaceShares(t.Context(), group.ID, []entrypoint.ShareRequest{
		{UserEmail: "  ", AccessLevel: "resource_viewer"},
	}, "owner@example.com")
	if err == nil {
		t.Fatal("expected error for empty userEmail; got nil")
	}
}

func TestPromptService_GetShares_IgnoresOtherResourceTypes(t *testing.T) {
	db := store.TestDB(t)
	svc := NewPromptService(func() *store.DB { return db }, nil)
	group := promptShareTestFixture(t, db, "owner@example.com", nil)

	if _, _, err := store.NewResourceAccessShareRepo(db).BatchShare(
		t.Context(), group.ID, store.ResourceTypeProject,
		[]store.ShareInput{{UserEmail: "wrong@example.com", AccessLevel: "resource_viewer"}},
		"owner@example.com",
	); err != nil {
		t.Fatal(err)
	}

	shares, err := svc.GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) = %d, want 0 (project-type share must not leak)", len(shares))
	}
}
