// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"
)

func TestGatewayDeploymentRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	gwRepo := NewGatewayRepo(db)
	repo := NewGatewayDeploymentRepo(db)
	ctx := context.Background()

	desc := "Test gateway for deployments"
	gw := &Gateway{Name: "deploy-gw", Description: &desc, Type: "slack", Values: new("{}"), CreatedBy: "u", UpdatedBy: "u"}
	if err := gwRepo.Create(ctx, gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	snapshot := `{"name":"deploy-gw"}`
	d := &GatewayDeployment{
		GatewayID:             gw.ID,
		Status:                "pending",
		Action:                "deploy",
		GatewayConfigSnapshot: &snapshot,
		CreatedBy:             "user1",
	}
	err := repo.Create(ctx, d)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected deployment")
	}
	if got.Status != "pending" {
		t.Errorf("status = %q, want %q", got.Status, "pending")
	}
}

func TestGatewayDeploymentRepo_GetByID_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewGatewayDeploymentRepo(db)
	ctx := context.Background()

	got, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for missing deployment")
	}
}

func TestGatewayDeploymentRepo_ListPaginated(t *testing.T) {
	db := TestDB(t)
	gwRepo := NewGatewayRepo(db)
	repo := NewGatewayDeploymentRepo(db)
	ctx := context.Background()

	desc := "gateway for list test"
	gw := &Gateway{Name: "list-gw", Description: &desc, Type: "slack", Values: new("{}"), CreatedBy: "u", UpdatedBy: "u"}
	if err := gwRepo.Create(ctx, gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	baseTime := time.Now().UnixMilli()
	for i := range 3 {
		d := &GatewayDeployment{
			GatewayID:   gw.ID,
			Status:      "pending",
			Action:      "deploy",
			CreatedBy:   "user1",
			CreatedTime: baseTime + int64(i*1000),
		}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create deployment %d: %v", i, err)
		}
	}

	list, total, err := repo.ListPaginated(ctx, 1, 2, gw.ID)
	if err != nil {
		t.Fatalf("list paginated: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}

	listAll, totalAll, err := repo.ListPaginated(ctx, 1, 10, "")
	if err != nil {
		t.Fatalf("list paginated all: %v", err)
	}
	if totalAll != 3 {
		t.Errorf("total all = %d, want 3", totalAll)
	}
	if len(listAll) != 3 {
		t.Errorf("len all = %d, want 3", len(listAll))
	}
}

func TestGatewayDeploymentRepo_UpdateStatus(t *testing.T) {
	db := TestDB(t)
	gwRepo := NewGatewayRepo(db)
	repo := NewGatewayDeploymentRepo(db)
	ctx := context.Background()

	desc := "gateway for update status"
	gw := &Gateway{Name: "update-status-gw", Description: &desc, Type: "slack", Values: new("{}"), CreatedBy: "u", UpdatedBy: "u"}
	if err := gwRepo.Create(ctx, gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	d := &GatewayDeployment{
		GatewayID: gw.ID,
		Status:    "pending",
		Action:    "deploy",
		CreatedBy: "user1",
	}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatalf("create: %v", err)
	}

	errMsg := "deployment failed"
	if err := repo.UpdateStatus(ctx, d.ID, "failed", &errMsg); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("status = %q, want %q", got.Status, "failed")
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != errMsg {
		t.Errorf("error_message = %v, want %q", got.ErrorMessage, errMsg)
	}
}

func TestGatewayDeploymentRepo_GetLatestByGatewayID(t *testing.T) {
	db := TestDB(t)
	gwRepo := NewGatewayRepo(db)
	repo := NewGatewayDeploymentRepo(db)
	ctx := context.Background()

	desc := "gateway for latest"
	gw := &Gateway{Name: "latest-gw", Description: &desc, Type: "slack", Values: new("{}"), CreatedBy: "u", UpdatedBy: "u"}
	if err := gwRepo.Create(ctx, gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	baseTime := time.Now().UnixMilli()
	older := &GatewayDeployment{
		GatewayID:   gw.ID,
		Status:      "success",
		Action:      "deploy",
		CreatedBy:   "user1",
		CreatedTime: baseTime,
	}
	if err := repo.Create(ctx, older); err != nil {
		t.Fatalf("create older: %v", err)
	}

	newer := &GatewayDeployment{
		GatewayID:   gw.ID,
		Status:      "pending",
		Action:      "update",
		CreatedBy:   "user1",
		CreatedTime: baseTime + 5000,
	}
	if err := repo.Create(ctx, newer); err != nil {
		t.Fatalf("create newer: %v", err)
	}

	latest, err := repo.GetLatestByGatewayID(ctx, gw.ID)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest deployment")
	}
	if latest.ID != newer.ID {
		t.Errorf("latest id = %q, want %q", latest.ID, newer.ID)
	}
}

func TestGatewayDeploymentRepo_ListByStatus(t *testing.T) {
	db := TestDB(t)
	gwRepo := NewGatewayRepo(db)
	repo := NewGatewayDeploymentRepo(db)
	ctx := context.Background()

	desc := "gateway for list-by-status"
	gw := &Gateway{Name: "status-gw", Description: &desc, Type: "slack", Values: new("{}"), CreatedBy: "u", UpdatedBy: "u"}
	if err := gwRepo.Create(ctx, gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	for _, status := range []string{"pending", "pending", "success"} {
		d := &GatewayDeployment{
			GatewayID: gw.ID,
			Status:    status,
			Action:    "deploy",
			CreatedBy: "user1",
		}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create %s deployment: %v", status, err)
		}
	}

	pending, err := repo.ListByStatus(ctx, "pending")
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("pending count = %d, want 2", len(pending))
	}

	success, err := repo.ListByStatus(ctx, "success")
	if err != nil {
		t.Fatalf("list by status success: %v", err)
	}
	if len(success) != 1 {
		t.Errorf("success count = %d, want 1", len(success))
	}
}

func seedGatewayForDeploys(ctx context.Context, t *testing.T, db *DB, name string) string {
	t.Helper()
	values := "{}"
	desc := "gateway for GetLatestSuccessfulDeploys tests"
	gw := &Gateway{Name: name, Description: &desc, Type: "slack", Values: &values, CreatedBy: "u", UpdatedBy: "u"}
	if err := NewGatewayRepo(db).Create(ctx, gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return gw.ID
}

func TestGatewayDeploymentRepo_GetLatestSuccessfulDeploys_SkipsFailedRowInTheLatestMillisecond(t *testing.T) {
	db := TestDB(t)
	ctx := context.Background()
	repo := NewGatewayDeploymentRepo(db)
	gwID := seedGatewayForDeploys(ctx, t, db, "gw-filter-half")

	stamp := time.Now().UnixMilli()
	serving, neverStarted := `{"name":"serving"}`, `{"name":"never-started"}`
	rows := []*GatewayDeployment{
		{ID: "01a00000-0000-7000-8000-00000000000a", GatewayID: gwID, Status: "success", Action: "deploy",
			GatewayConfigSnapshot: &serving, CreatedBy: "u", CreatedTime: stamp},
		{ID: "01a00000-0000-7000-8000-00000000000f", GatewayID: gwID, Status: "failed", Action: "deploy",
			GatewayConfigSnapshot: &neverStarted, CreatedBy: "u", CreatedTime: stamp},
	}
	for _, r := range rows {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("seed row %s: %v", r.ID, err)
		}
	}

	got, err := repo.GetLatestSuccessfulDeploys(ctx, []string{gwID})
	if err != nil {
		t.Fatalf("GetLatestSuccessfulDeploys: %v", err)
	}
	d := got[gwID]
	if d == nil {
		t.Fatal("no latest successful deploy returned")
	}
	if d.Status != "success" {
		t.Errorf("returned a %q row as the latest successful deploy", d.Status)
	}
	if d.GatewayConfigSnapshot == nil || *d.GatewayConfigSnapshot != serving {
		t.Errorf("snapshot = %v, want the serving config %q", d.GatewayConfigSnapshot, serving)
	}
}

func TestGatewayDeploymentRepo_GetLatestSuccessfulDeploys_SameMillisecondSuccessesResolveToTheHighestID(t *testing.T) {
	db := TestDB(t)
	ctx := context.Background()
	repo := NewGatewayDeploymentRepo(db)
	gwID := seedGatewayForDeploys(ctx, t, db, "gw-tiebreak-half")

	stamp := time.Now().UnixMilli()
	const (
		lowestID  = "01a00000-0000-7000-8000-00000000000a"
		highestID = "01a00000-0000-7000-8000-00000000000c"
		middleID  = "01a00000-0000-7000-8000-00000000000b"
	)
	for _, id := range []string{lowestID, highestID, middleID} {
		snap := `{"id":"` + id + `"}`
		if err := repo.Create(ctx, &GatewayDeployment{
			ID: id, GatewayID: gwID, Status: "success", Action: "deploy",
			GatewayConfigSnapshot: &snap, CreatedBy: "u", CreatedTime: stamp,
		}); err != nil {
			t.Fatalf("seed row %s: %v", id, err)
		}
	}

	got, err := repo.GetLatestSuccessfulDeploys(ctx, []string{gwID})
	if err != nil {
		t.Fatalf("GetLatestSuccessfulDeploys: %v", err)
	}
	d := got[gwID]
	if d == nil {
		t.Fatal("no latest successful deploy returned")
	}
	if d.ID != highestID {
		t.Errorf("same-millisecond tie resolved to %s, want the highest id %s", d.ID, highestID)
	}
}
