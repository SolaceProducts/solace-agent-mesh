// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func migrationVersion(t *testing.T, migrations fs.FS, name string) int64 {
	t.Helper()
	matches, err := fs.Glob(migrations, "*_"+name+".sql")
	if err != nil || len(matches) != 1 {
		t.Fatalf("migration %q: matches %v, err %v; want exactly one", name, matches, err)
	}
	prefix, _, _ := strings.Cut(matches[0], "_")
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		t.Fatalf("migration %q: version prefix %q: %v", matches[0], prefix, err)
	}
	return version
}

func forEachPlatformDialectAt(t *testing.T, migration string, fn func(t *testing.T, db *DB, version int64)) {
	t.Helper()
	forEachPlatformDialect(t, migrationVersion(t, sqliteMigrations(), migration), migrationVersion(t, postgresMigrations(), migration), fn)
}

var agentSurfaceHashColumns = []string{"prompt_hash", "toolset_hash", "capability_hash", "behaviour_hash", "card_hash"}

func surfaceHashFixture() AgentDeployment {
	return AgentDeployment{
		PromptHash:     new("prompt"),
		ToolsetHash:    new("toolset"),
		CapabilityHash: new("capability"),
		BehaviourHash:  new("behaviour"),
		CardHash:       new("card"),
	}
}

func assertSurfaceHashesEqual(t *testing.T, label string, got *AgentDeployment, want AgentDeployment) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: deployment not found", label)
	}
	for _, c := range []struct {
		column    string
		got, want *string
	}{
		{"prompt_hash", got.PromptHash, want.PromptHash},
		{"toolset_hash", got.ToolsetHash, want.ToolsetHash},
		{"capability_hash", got.CapabilityHash, want.CapabilityHash},
		{"behaviour_hash", got.BehaviourHash, want.BehaviourHash},
		{"card_hash", got.CardHash, want.CardHash},
	} {
		switch {
		case c.want == nil && c.got != nil:
			t.Errorf("%s: %s = %q, want NULL", label, c.column, *c.got)
		case c.want != nil && (c.got == nil || *c.got != *c.want):
			t.Errorf("%s: %s = %v, want %q", label, c.column, c.got, *c.want)
		}
	}
}

func createSurfaceHashAgent(t *testing.T, db *DB, name string) string {
	t.Helper()
	agent := &Agent{Name: name, Description: new("surface hash fixture"), CreatedBy: "u", UpdatedBy: "u"}
	if err := NewAgentRepo(db).Create(t.Context(), agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent.ID
}

func TestB03_AgentSurfaceFingerprintsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "agent_surface_fingerprints",
		func(t *testing.T, db *DB, version int64) {
			ctx := t.Context()
			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
				t.Fatalf("migrate up to %d: %v", version, err)
			}
			for _, column := range agentSurfaceHashColumns {
				if err := db.QueryRowContext(ctx, `SELECT COUNT(`+column+`) FROM agent_deployments`).Scan(new(int)); err != nil {
					t.Fatalf("%s absent after migrate up: %v", column, err)
				}
			}

			if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
				t.Fatalf("migrate down: %v", err)
			}
			for _, column := range agentSurfaceHashColumns {
				if err := db.QueryRowContext(ctx, `SELECT COUNT(`+column+`) FROM agent_deployments`).Scan(new(int)); err == nil {
					t.Errorf("%s survived the rollback", column)
				}
			}

			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
				t.Fatalf("re-apply after rollback: %v", err)
			}
		})
}

func TestB04_GatewayPromptTransformHashMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "gateway_prompt_transform_hash",
		func(t *testing.T, db *DB, version int64) {
			ctx := t.Context()
			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
				t.Fatalf("migrate up to %d: %v", version, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(prompt_transform_hash) FROM gateway_deployments`).Scan(new(int)); err != nil {
				t.Fatalf("prompt_transform_hash absent after migrate up: %v", err)
			}

			if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
				t.Fatalf("migrate down: %v", err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(prompt_transform_hash) FROM gateway_deployments`).Scan(new(int)); err == nil {
				t.Error("prompt_transform_hash survived the rollback")
			}

			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
				t.Fatalf("re-apply after rollback: %v", err)
			}
		})
}

func TestB03_AgentDeploymentRepos_RoundTripSurfaceHashes(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	agentID := createSurfaceHashAgent(t, db, "surface-hash-roundtrip")
	agentRepo := NewAgentDeploymentRepo(db)
	legacyRepo := NewDeploymentRepo(db)

	known := surfaceHashFixture()
	viaAgentRepo := known
	viaAgentRepo.AgentID, viaAgentRepo.Status, viaAgentRepo.Action, viaAgentRepo.CreatedBy = agentID, "success", "deploy", "u"
	viaAgentRepo.CreatedTime = 1000
	if err := agentRepo.Create(ctx, &viaAgentRepo); err != nil {
		t.Fatalf("AgentDeploymentRepo.Create: %v", err)
	}
	viaLegacyRepo := known
	viaLegacyRepo.AgentID, viaLegacyRepo.Status, viaLegacyRepo.Action, viaLegacyRepo.CreatedBy = agentID, "success", "update", "u"
	viaLegacyRepo.CreatedTime = 2000
	if err := legacyRepo.Create(ctx, &viaLegacyRepo); err != nil {
		t.Fatalf("DeploymentRepo.Create: %v", err)
	}
	undeploy := known
	undeploy.AgentID, undeploy.Status, undeploy.Action, undeploy.CreatedBy = agentID, "success", "undeploy", "u"
	undeploy.CreatedTime = 3000
	if err := agentRepo.Create(ctx, &undeploy); err != nil {
		t.Fatalf("create undeploy row: %v", err)
	}
	unknown := AgentDeployment{AgentID: agentID, Status: "failed", Action: "deploy", CreatedBy: "u", CreatedTime: 4000}
	if err := agentRepo.Create(ctx, &unknown); err != nil {
		t.Fatalf("create hashless row: %v", err)
	}

	byID, err := agentRepo.GetByID(ctx, viaAgentRepo.ID)
	if err != nil {
		t.Fatalf("AgentDeploymentRepo.GetByID: %v", err)
	}
	assertSurfaceHashesEqual(t, "AgentDeploymentRepo.GetByID", byID, known)

	legacyByID, err := legacyRepo.GetByID(ctx, viaLegacyRepo.ID)
	if err != nil {
		t.Fatalf("DeploymentRepo.GetByID: %v", err)
	}
	assertSurfaceHashesEqual(t, "DeploymentRepo.GetByID", legacyByID, known)

	latestSuccessful, err := agentRepo.GetLatestSuccessfulDeploys(ctx, []string{agentID})
	if err != nil {
		t.Fatalf("GetLatestSuccessfulDeploys: %v", err)
	}
	assertSurfaceHashesEqual(t, "GetLatestSuccessfulDeploys", latestSuccessful[agentID], known)

	latestUndeploy, err := agentRepo.GetLatestSuccessfulUndeploy(ctx, agentID)
	if err != nil {
		t.Fatalf("GetLatestSuccessfulUndeploy: %v", err)
	}
	assertSurfaceHashesEqual(t, "GetLatestSuccessfulUndeploy", latestUndeploy, known)

	latest, err := agentRepo.GetLatestByAgentID(ctx, agentID)
	if err != nil {
		t.Fatalf("GetLatestByAgentID: %v", err)
	}
	assertSurfaceHashesEqual(t, "GetLatestByAgentID (hashless row)", latest, AgentDeployment{})

	page, _, err := agentRepo.ListPaginated(ctx, 1, 10, agentID)
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if len(page) != 4 {
		t.Fatalf("ListPaginated returned %d rows, want 4", len(page))
	}
	assertSurfaceHashesEqual(t, "ListPaginated", page[len(page)-1], known)

	legacyPage, _, err := legacyRepo.ListPaginated(ctx, 1, 10)
	if err != nil {
		t.Fatalf("DeploymentRepo.ListPaginated: %v", err)
	}
	if len(legacyPage) != 4 {
		t.Fatalf("DeploymentRepo.ListPaginated returned %d rows, want 4", len(legacyPage))
	}
	assertSurfaceHashesEqual(t, "DeploymentRepo.ListPaginated", &legacyPage[len(legacyPage)-1], known)

	a2a := &A2AAgent{
		AgentURL: "https://example.com/agent", Name: "remote", AgentCardLocation: "none", AgentAuthType: new("none"), AgentAuthConfig: new("{}"),
		RequestTimeoutSeconds: 30, CreatedBy: "u", UpdatedBy: "u",
	}
	if err = NewA2AAgentRepo(db).Create(ctx, a2a); err != nil {
		t.Fatalf("create a2a agent: %v", err)
	}
	a2aRepo := NewA2AAgentDeploymentRepo(db)
	if err = a2aRepo.Create(ctx, &A2AAgentDeployment{A2AAgentID: a2a.ID, AgentDeploymentID: viaAgentRepo.ID}); err != nil {
		t.Fatalf("link a2a deployment: %v", err)
	}
	byA2A, _, err := a2aRepo.ListByA2AAgentID(ctx, a2a.ID, 1, 10)
	if err != nil || len(byA2A) != 1 {
		t.Fatalf("ListByA2AAgentID = %d rows, %v; want 1", len(byA2A), err)
	}
	assertSurfaceHashesEqual(t, "A2AAgentDeploymentRepo.ListByA2AAgentID", byA2A[0], known)
	allA2A, _, err := a2aRepo.ListAllPaginated(ctx, 1, 10)
	if err != nil || len(allA2A) != 1 {
		t.Fatalf("ListAllPaginated = %d rows, %v; want 1", len(allA2A), err)
	}
	assertSurfaceHashesEqual(t, "A2AAgentDeploymentRepo.ListAllPaginated", allA2A[0], known)
}

func TestB04_GatewayDeploymentRepo_RoundTripsPromptTransformHash(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	gw := &Gateway{Name: "prompt-transform-gw", Description: new("prompt transform hash fixture"), Type: "webhook", Values: new("{}"), CreatedBy: "u", UpdatedBy: "u"}
	if err := NewGatewayRepo(db).Create(ctx, gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	repo := NewGatewayDeploymentRepo(db)

	hash := "prompt-transform-hash"
	withHash := &GatewayDeployment{GatewayID: gw.ID, Status: "success", Action: "deploy", PromptTransformHash: &hash, CreatedBy: "u", CreatedTime: 1000}
	if err := repo.Create(ctx, withHash); err != nil {
		t.Fatalf("create: %v", err)
	}
	withoutHash := &GatewayDeployment{GatewayID: gw.ID, Status: "failed", Action: "undeploy", CreatedBy: "u", CreatedTime: 2000}
	if err := repo.Create(ctx, withoutHash); err != nil {
		t.Fatalf("create hashless: %v", err)
	}

	byID, err := repo.GetByID(ctx, withHash.ID)
	if err != nil || byID == nil {
		t.Fatalf("GetByID: %v, %v", byID, err)
	}
	if byID.PromptTransformHash == nil || *byID.PromptTransformHash != hash {
		t.Errorf("GetByID prompt_transform_hash = %v, want %q", byID.PromptTransformHash, hash)
	}

	latestSuccessful, err := repo.GetLatestSuccessfulDeploys(ctx, []string{gw.ID})
	if err != nil {
		t.Fatalf("GetLatestSuccessfulDeploys: %v", err)
	}
	if got := latestSuccessful[gw.ID]; got == nil || got.PromptTransformHash == nil || *got.PromptTransformHash != hash {
		t.Errorf("GetLatestSuccessfulDeploys prompt_transform_hash = %+v, want %q", got, hash)
	}

	latest, err := repo.GetLatestByGatewayID(ctx, gw.ID)
	if err != nil || latest == nil {
		t.Fatalf("GetLatestByGatewayID: %v, %v", latest, err)
	}
	if latest.PromptTransformHash != nil {
		t.Errorf("hashless row reads back prompt_transform_hash %q", *latest.PromptTransformHash)
	}

	page, _, err := repo.ListPaginated(ctx, 1, 10, gw.ID)
	if err != nil || len(page) != 2 {
		t.Fatalf("ListPaginated: %d rows, %v", len(page), err)
	}
	if got := page[1].PromptTransformHash; got == nil || *got != hash {
		t.Errorf("ListPaginated prompt_transform_hash = %v, want %q", got, hash)
	}
}
