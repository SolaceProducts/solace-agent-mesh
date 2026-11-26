// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestGatewayRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayRepo(db)
	ctx := context.Background()

	desc := "A test Slack gateway"
	vals := `{"token":"xoxb-test"}`
	gw := &Gateway{
		Name:        "test-gateway",
		Description: &desc,
		Type:        "slack",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	err := repo.Create(ctx, gw)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if gw.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := repo.GetByID(ctx, gw.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected gateway")
	}
	if got.Name != "test-gateway" {
		t.Errorf("name = %q, want %q", got.Name, "test-gateway")
	}
	if got.Type != "slack" {
		t.Errorf("type = %q, want %q", got.Type, "slack")
	}

	missing, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != nil {
		t.Fatal("expected nil for missing gateway")
	}

	list, total, err := repo.ListPaginated(ctx, 1, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Errorf("total=%d len=%d, want 1,1", total, len(list))
	}

	_, total, err = repo.ListPaginated(ctx, 1, 10, "slack")
	if err != nil {
		t.Fatalf("list with filter: %v", err)
	}
	if total != 1 {
		t.Errorf("filtered total = %d, want 1", total)
	}

	_, total, err = repo.ListPaginated(ctx, 1, 10, "event_mesh")
	if err != nil {
		t.Fatalf("list with no-match filter: %v", err)
	}
	if total != 0 {
		t.Errorf("filtered total = %d, want 0", total)
	}

	got.Name = "renamed-gateway"
	got.UpdatedBy = "user2"
	err = repo.Update(ctx, got)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got2, err := repo.GetByID(ctx, gw.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got2.Name != "renamed-gateway" {
		t.Errorf("name = %q, want %q", got2.Name, "renamed-gateway")
	}
	if got2.Type != "slack" {
		t.Errorf("type = %q, want %q (type is immutable)", got2.Type, "slack")
	}

	err = repo.Delete(ctx, gw.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	gone, err := repo.GetByID(ctx, gw.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if gone != nil {
		t.Fatal("expected nil after delete")
	}

	err = repo.Delete(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err == nil {
		t.Fatal("expected error deleting non-existent gateway")
	}
}

func TestGatewayRepo_PublicURLRoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayRepo(db)
	ctx := context.Background()

	desc := "public-url gateway"
	vals := `{}`
	publicURL := "https://teams.example.com/api/messages"
	gw := &Gateway{
		Name:        "pub-url-gw",
		Description: &desc,
		Type:        "teams",
		Values:      &vals,
		PublicURL:   &publicURL,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	if err := repo.Create(ctx, gw); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, gw.ID)
	if err != nil || got == nil {
		t.Fatalf("get after create: err=%v got=%v", err, got)
	}
	if got.PublicURL == nil || *got.PublicURL != publicURL {
		t.Fatalf("public_url = %v, want %q", got.PublicURL, publicURL)
	}

	newURL := "https://other.example.com/hook"
	got.PublicURL = &newURL
	got.UpdatedBy = "user2"
	if updateErr := repo.Update(ctx, got); updateErr != nil {
		t.Fatalf("update change url: %v", updateErr)
	}
	got2, err := repo.GetByID(ctx, gw.ID)
	if err != nil || got2 == nil {
		t.Fatalf("get after change: %v", err)
	}
	if got2.PublicURL == nil || *got2.PublicURL != newURL {
		t.Fatalf("public_url after change = %v, want %q", got2.PublicURL, newURL)
	}

	got2.PublicURL = nil
	if clearErr := repo.Update(ctx, got2); clearErr != nil {
		t.Fatalf("update clear url: %v", clearErr)
	}
	got3, err := repo.GetByID(ctx, gw.ID)
	if err != nil || got3 == nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got3.PublicURL != nil {
		t.Fatalf("public_url after clear = %v, want nil", got3.PublicURL)
	}
}

func TestGatewayRepo_CreateLeavesSlugUnset(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayRepo(db)
	ctx := t.Context()

	desc := "slug-test gateway"
	vals := `{}`
	gw := &Gateway{
		Name:        "slug-default-gw",
		Description: &desc,
		Type:        "slack",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	if err := repo.Create(ctx, gw); err != nil {
		t.Fatalf("create: %v", err)
	}
	if gw.Slug != "" {
		t.Errorf("Slug = %q, want empty (unset)", gw.Slug)
	}

	got, err := repo.GetByID(ctx, gw.ID)
	if err != nil || got == nil {
		t.Fatalf("get: err=%v got=%v", err, got)
	}
	if got.Slug != "" {
		t.Errorf("read-back Slug = %q, want empty (NULL coalesced to \"\")", got.Slug)
	}
}

func TestGatewayRepo_CreateAllowsMultipleUnsetSlugs(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayRepo(db)
	ctx := t.Context()

	vals := `{}`
	mk := func(name string) *Gateway {
		desc := "no-slug gateway"
		return &Gateway{Name: name, Description: &desc, Type: "slack", Values: &vals, CreatedBy: "u", UpdatedBy: "u"}
	}
	if err := repo.Create(ctx, mk("no-slug-a")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := repo.Create(ctx, mk("no-slug-b")); err != nil {
		t.Fatalf("second create with unset slug should not collide: %v", err)
	}
}

func TestGatewayRepo_CreateWithExplicitSlug(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayRepo(db)
	ctx := t.Context()

	desc := "slug-test gateway"
	vals := `{}`
	gw := &Gateway{
		Slug:        "my-mcp",
		Name:        "explicit-slug-gw",
		Description: &desc,
		Type:        "mcp",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	if err := repo.Create(ctx, gw); err != nil {
		t.Fatalf("create: %v", err)
	}
	if gw.Slug != "my-mcp" {
		t.Errorf("Slug = %q, want %q", gw.Slug, "my-mcp")
	}

	got, err := repo.GetByID(ctx, gw.ID)
	if err != nil || got == nil {
		t.Fatalf("get: err=%v got=%v", err, got)
	}
	if got.Slug != "my-mcp" {
		t.Errorf("read-back Slug = %q, want %q", got.Slug, "my-mcp")
	}
}

func TestGatewayRepo_DuplicateSlugReturnsTypedError(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayRepo(db)
	ctx := t.Context()

	desc := "slug-test gateway"
	vals := `{}`
	first := &Gateway{
		Slug:        "shared-slug",
		Name:        "first-gw",
		Description: &desc,
		Type:        "mcp",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := &Gateway{
		Slug:        "shared-slug",
		Name:        "second-gw",
		Description: &desc,
		Type:        "mcp",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	err := repo.Create(ctx, second)
	if err == nil {
		t.Fatal("expected error on duplicate slug")
	}
	conflictErr, ok := errors.AsType[*GatewaySlugConflictError](err)
	if !ok {
		t.Fatalf("expected *GatewaySlugConflictError, got %T: %v", err, err)
	}
	if conflictErr.Slug != "shared-slug" {
		t.Errorf("conflict Slug = %q, want %q", conflictErr.Slug, "shared-slug")
	}
}

func TestGatewayRepo_DuplicateNameStillReturnsGenericUniqueViolation(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayRepo(db)
	ctx := t.Context()

	desc := "slug-test gateway"
	vals := `{}`
	first := &Gateway{
		Slug:        "slug-one",
		Name:        "shared-name",
		Description: &desc,
		Type:        "mcp",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := &Gateway{
		Slug:        "slug-two",
		Name:        "shared-name",
		Description: &desc,
		Type:        "mcp",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	err := repo.Create(ctx, second)
	if err == nil {
		t.Fatal("expected error on duplicate name")
	}
	if !dbutil.IsUniqueViolation(err) {
		t.Errorf("expected IsUniqueViolation, got %T: %v", err, err)
	}
	if _, ok := errors.AsType[*GatewaySlugConflictError](err); ok {
		t.Errorf("duplicate name should NOT yield *GatewaySlugConflictError: %v", err)
	}
}

func TestGatewayRepo_ListPaginatedIncludesSlug(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayRepo(db)
	ctx := t.Context()

	desc := "slug-test gateway"
	vals := `{}`
	gw := &Gateway{
		Slug:        "listed-slug",
		Name:        "listed-gw",
		Description: &desc,
		Type:        "mcp",
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	if err := repo.Create(ctx, gw); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, total, err := repo.ListPaginated(ctx, 1, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("total=%d len=%d, want 1,1", total, len(list))
	}
	if list[0].Slug != "listed-slug" {
		t.Errorf("listed Slug = %q, want %q", list[0].Slug, "listed-slug")
	}
}
