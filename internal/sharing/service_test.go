// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sharing

import (
	"context"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestService_FullShareFlow(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(func() *store.DB { return db })
	ctx := context.Background()

	projRepo := store.NewProjectRepo(db)
	projectID := idgen.New()
	_ = projRepo.Create(ctx, &store.Project{ID: projectID, Name: "Test Project", UserID: "owner@example.com"})

	resp, err := svc.GetProjectShares(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if resp.OwnerEmail != "owner@example.com" {
		t.Errorf("expected owner=owner@example.com, got %s", resp.OwnerEmail)
	}
	if len(resp.Shares) != 0 {
		t.Errorf("expected 0 shares, got %d", len(resp.Shares))
	}

	shareResp, err := svc.BatchShare(ctx, projectID, []entrypoint.ShareRequest{
		{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
	}, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if shareResp.TotalProcessed != 1 {
		t.Errorf("expected totalProcessed=1, got %d", shareResp.TotalProcessed)
	}
	if len(shareResp.Created) != 1 {
		t.Errorf("expected 1 created, got %d", len(shareResp.Created))
	}

	has, err := svc.HasViewAccess(ctx, projectID, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected alice to have view access")
	}

	ids, err := svc.GetSharedProjectIDs(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != projectID {
		t.Errorf("expected [p1], got %v", ids)
	}

	resp, err = svc.GetProjectShares(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Shares) != 1 {
		t.Fatalf("expected 1 share, got %d", len(resp.Shares))
	}
	if resp.Shares[0].UserEmail != "alice@example.com" {
		t.Errorf("expected alice@example.com, got %s", resp.Shares[0].UserEmail)
	}
	if resp.Shares[0].AccessLevel != "resource_viewer" {
		t.Errorf("expected resource_viewer, got %s", resp.Shares[0].AccessLevel)
	}

	unshareResp, err := svc.BatchUnshare(ctx, projectID, []string{"alice@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if unshareResp.DeletedCount != 1 {
		t.Errorf("expected deletedCount=1, got %d", unshareResp.DeletedCount)
	}

	has, err = svc.HasViewAccess(ctx, projectID, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("expected alice to no longer have access")
	}
}

func TestService_DeleteAllShares(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(func() *store.DB { return db })
	ctx := context.Background()

	projRepo := store.NewProjectRepo(db)
	projectID := idgen.New()
	_ = projRepo.Create(ctx, &store.Project{ID: projectID, Name: "Test", UserID: "owner"})

	_, _ = svc.BatchShare(ctx, projectID, []entrypoint.ShareRequest{
		{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "bob@example.com", AccessLevel: "resource_viewer"},
	}, "owner")

	if err := svc.DeleteAllShares(ctx, projectID); err != nil {
		t.Fatal(err)
	}

	resp, err := svc.GetProjectShares(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Shares) != 0 {
		t.Errorf("expected 0 shares, got %d", len(resp.Shares))
	}
}
