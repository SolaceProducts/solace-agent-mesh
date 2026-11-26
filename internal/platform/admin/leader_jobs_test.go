// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestThePlatformRoutesOfferEveryPlatformLeaderJob(t *testing.T) {
	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": platformCoreDBURL(t)},
		platpkg.ExtensionPoints{},
	)
	rt := runtime.NewRuntime(connectedDevBroker(t), nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	deps := &RouteDeps{IsRBACAuthority: true}
	RegisterRoutesWithDeps(http.NewServeMux(), core, deps)

	offered := map[leader.JobName]leader.Job{}
	for _, job := range deps.LeaderJobs {
		offered[job.Name] = job
	}
	for _, name := range []leader.JobName{
		leader.JobPlatformTestAgentPurge,
		leader.JobPlatformEntrypointBindingSweep,
		leader.JobPlatformToolsetDiscoveryExpiry,
		leader.JobPlatformSkillDiscoveryExpiry,
		leader.JobPlatformOutboxCleanup,
		leader.JobPlatformMCPOAuthStateExpiry,
	} {
		job, ok := offered[name]
		if !ok {
			t.Errorf("the platform routes offer no %s job", name)
			continue
		}
		if job.Run == nil || job.Every <= 0 {
			t.Errorf("%s = %+v, want a run on an interval", name, job)
		}
	}
}

func TestTheSkillDiscoveryJobFailsOnlyTheSkillsPendingPastTheTimeout(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	ctx := t.Context()
	stale := time.Now().Add(-2 * defaultDiscoveryPendingTimeout).UnixMilli()
	stuck := &store.Skill{
		ID: "f0000000-0000-0000-0000-000000000011", Name: "stuck-skill", Origin: "platform", DiscoveryStatus: "pending",
		CreatedBy: "system", UpdatedBy: "system", CreatedTime: stale, UpdatedTime: stale,
	}
	fresh := &store.Skill{
		ID: "f0000000-0000-0000-0000-000000000012", Name: "fresh-skill", Origin: "platform", DiscoveryStatus: "pending",
		CreatedBy: "system", UpdatedBy: "system",
	}
	for _, s := range []*store.Skill{stuck, fresh} {
		if err := repo.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	job := NewSkillDiscoveryChecker(NewSkillService(db, repo, slog.Default())).LeaderJob()
	if job.Name != leader.JobPlatformSkillDiscoveryExpiry || job.Every != defaultDiscoveryCheckInterval {
		t.Errorf("job = %s every %s, want %s every %s", job.Name, job.Every, leader.JobPlatformSkillDiscoveryExpiry, defaultDiscoveryCheckInterval)
	}
	if err := job.Run(ctx, leader.Term{Number: 1}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{stuck.ID: "failed", fresh.ID: "pending"} {
		got, err := repo.GetByID(ctx, id)
		if err != nil || got == nil {
			t.Fatalf("GetByID(%s) = %v (%v)", id, got, err)
		}
		if got.DiscoveryStatus != want {
			t.Errorf("skill %s is %q after the job ran, want %q", got.Name, got.DiscoveryStatus, want)
		}
	}
}

func TestTheToolsetDiscoveryJobFailsOnlyThePackagesPendingPastTheTimeout(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	ctx := t.Context()
	stale := time.Now().Add(-2 * defaultDiscoveryPendingTimeout).UnixMilli()
	stuck := &store.ToolPackage{Name: "stuck-pkg", DiscoveryStatus: "pending", CreatedBy: "system", UpdatedBy: "system", CreatedTime: stale, UpdatedTime: stale}
	fresh := &store.ToolPackage{Name: "fresh-pkg", DiscoveryStatus: "pending", CreatedBy: "system", UpdatedBy: "system"}
	for _, p := range []*store.ToolPackage{stuck, fresh} {
		if err := repo.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	job := NewToolsetDiscoveryChecker(NewToolsetService(db, repo, nil, nil, "test-ns", slog.Default())).LeaderJob()
	if job.Name != leader.JobPlatformToolsetDiscoveryExpiry || job.Every != defaultDiscoveryCheckInterval {
		t.Errorf("job = %s every %s, want %s every %s", job.Name, job.Every, leader.JobPlatformToolsetDiscoveryExpiry, defaultDiscoveryCheckInterval)
	}
	if err := job.Run(ctx, leader.Term{Number: 1}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{stuck.ID: "failed", fresh.ID: "pending"} {
		got, err := repo.GetByID(ctx, id)
		if err != nil || got == nil {
			t.Fatalf("GetByID(%s) = %v (%v)", id, got, err)
		}
		if got.DiscoveryStatus != want {
			t.Errorf("package %s is %q after the job ran, want %q", got.Name, got.DiscoveryStatus, want)
		}
	}
}
