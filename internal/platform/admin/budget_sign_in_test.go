// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newBudgetPlatform(t *testing.T) (*RouteDeps, broker.Broker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": platformCoreDBURL(t)},
		platpkg.ExtensionPoints{},
	)
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	deps := &RouteDeps{FeatureRegistry: features.NewRegistry(), IsRBACAuthority: true}
	RegisterRoutesWithDeps(http.NewServeMux(), core, deps)
	return deps, brk
}

func userUpsertHandlerOf(t *testing.T, deps *RouteDeps) *UserUpsertHandler {
	t.Helper()
	for _, svc := range deps.BackgroundServices {
		if h, ok := svc.(*UserUpsertHandler); ok {
			return h
		}
	}
	t.Fatal("the routes registered no user upsert handler")
	return nil
}

func TestBudgetRoutes_SignInBindsPendingBudgets(t *testing.T) {
	deps, brk := newBudgetPlatform(t)
	h := userUpsertHandlerOf(t, deps)
	if err := h.Start(t.Context()); err != nil {
		t.Fatalf("start user upsert handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })

	created, err := deps.BudgetSvc.Create(t.Context(), CreateBudgetRequest{Name: "Engineering", SubjectType: "user", Amount: new("10"), AmountSet: true}, "admin")
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	if _, assignErr := deps.BudgetAssignmentSvc.Create(t.Context(), CreateBudgetAssignmentRequest{
		SubjectType: "user", Identity: &BudgetAssignmentIdentity{Type: "email", Value: "new.hire@example.com"}, BudgetID: created.ID,
	}, "admin"); assignErr != nil {
		t.Fatalf("assign pending: %v", assignErr)
	}

	if resp := upsertRoundTrip(t, brk, "test-ns", "test-gw", userUpsertRequest{Issuer: "iss", Sub: "sub-new-hire", Email: "new.hire@example.com"}); resp.Error != nil {
		t.Fatalf("sign in: %+v", resp.Error)
	}

	page, err := deps.BudgetAssignmentSvc.List(t.Context(), ListBudgetAssignmentsParams{PageNumber: 1, PageSize: 10, BudgetID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].State != store.BudgetAssignmentConcrete || page.Data[0].Subject.ID == nil {
		t.Errorf("after sign-in = %+v, want the pending assignment bound to the new user", page.Data)
	}
}
