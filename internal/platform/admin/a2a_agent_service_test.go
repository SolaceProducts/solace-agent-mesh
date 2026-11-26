// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestComputeA2AAgentConfigHash_Deterministic(t *testing.T) {
	t.Parallel()
	a := &store.A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "test-agent",
		Description:           strPtr("a test agent"),
		AgentCardLocation:     "well_known",
		AgentCardURL:          strPtr("https://example.com/.well-known/agent.json"),
		AgentAuthType:         strPtr("bearer"),
		RequestTimeoutSeconds: 120,
		Skills:                strPtr(`[{"name":"skill1"}]`),
		InputModes:            strPtr(`["text"]`),
		OutputModes:           strPtr(`["text","image"]`),
	}

	hash1 := computeA2AAgentConfigHash(a)
	hash2 := computeA2AAgentConfigHash(a)

	if hash1 != hash2 {
		t.Fatalf("expected deterministic hash, got %s and %s", hash1, hash2)
	}
	if len(hash1) != 64 {
		t.Fatalf("expected 64-char hex hash, got %d chars", len(hash1))
	}
}

func TestComputeA2AAgentConfigHash_DifferentOnFieldChange(t *testing.T) {
	t.Parallel()
	base := &store.A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "test-agent",
		Description:           strPtr("a test agent"),
		AgentCardLocation:     "well_known",
		RequestTimeoutSeconds: 120,
	}

	baseHash := computeA2AAgentConfigHash(base)

	mutations := []struct {
		name   string
		mutate func(a *store.A2AAgent)
	}{
		{"name", func(a *store.A2AAgent) { a.Name = "different-name" }},
		{"agentUrl", func(a *store.A2AAgent) { a.AgentURL = "https://other.com/agent" }},
		{"description", func(a *store.A2AAgent) { a.Description = strPtr("changed") }},
		{"agentCardLocation", func(a *store.A2AAgent) { a.AgentCardLocation = "custom" }},
		{"requestTimeoutSeconds", func(a *store.A2AAgent) { a.RequestTimeoutSeconds = 60 }},
		{"skills", func(a *store.A2AAgent) { a.Skills = strPtr(`[{"name":"new"}]`) }},
		{"inputModes", func(a *store.A2AAgent) { a.InputModes = strPtr(`["audio"]`) }},
		{"outputModes", func(a *store.A2AAgent) { a.OutputModes = strPtr(`["video"]`) }},
		{"agentCardUrl", func(a *store.A2AAgent) { a.AgentCardURL = strPtr("https://x.com/card") }},
		{"agentAuthType", func(a *store.A2AAgent) { a.AgentAuthType = strPtr("apikey") }},
	}

	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			modified := &store.A2AAgent{
				AgentURL:              base.AgentURL,
				Name:                  base.Name,
				Description:           strPtr(derefStr(base.Description)),
				AgentCardLocation:     base.AgentCardLocation,
				RequestTimeoutSeconds: base.RequestTimeoutSeconds,
			}
			m.mutate(modified)
			if computeA2AAgentConfigHash(modified) == baseHash {
				t.Errorf("changing %s should produce a different hash", m.name)
			}
		})
	}
}

func TestComputeA2AAgentConfigHash_ExcludesSecrets(t *testing.T) {
	t.Parallel()
	a := &store.A2AAgent{
		AgentURL:          "https://example.com/agent",
		Name:              "test-agent",
		AgentCardLocation: "well_known",
	}
	hash1 := computeA2AAgentConfigHash(a)

	a.AgentAuthConfig = strPtr(`{"token":"secret123"}`)
	a.AgentCardAuthConfig = strPtr(`{"apiKey":"key456"}`)
	a.TaskHeaders = strPtr(`{"Authorization":"Bearer xyz"}`)
	a.AgentCardHeaders = strPtr(`{"X-Custom":"value"}`)
	hash2 := computeA2AAgentConfigHash(a)

	if hash1 != hash2 {
		t.Fatal("hash should not change when only secret fields are modified")
	}
}

func TestBuildResponse_DeploymentStatus_NotDeployed(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{
		ID:                "agent-1",
		AgentURL:          "https://example.com",
		Name:              "test",
		AgentCardLocation: "well_known",
	}

	resp := svc.buildResponse(a, nil, DisplayStatusNotDeployed)

	if resp.DeploymentStatus != DisplayStatusNotDeployed {
		t.Errorf("expected %q, got %q", DisplayStatusNotDeployed, resp.DeploymentStatus)
	}
	if resp.SyncStatus != nil {
		t.Errorf("expected nil SyncStatus when not deployed, got %v", *resp.SyncStatus)
	}
	if resp.Deployed != nil {
		t.Error("expected nil Deployed when not deployed")
	}
}

func TestBuildResponse_DeploymentStatus_Deployed(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{
		ID:                "agent-1",
		AgentURL:          "https://example.com",
		Name:              "test",
		AgentCardLocation: "well_known",
	}

	hash := computeA2AAgentConfigHash(a)
	deploy := &store.AgentDeployment{
		Status:     "success",
		Action:     DeployActionDeploy,
		ConfigHash: &hash,
	}

	resp := svc.buildResponse(a, deploy, DisplayStatusDeployed)

	if resp.DeploymentStatus != DisplayStatusDeployed {
		t.Errorf("expected %q, got %q", DisplayStatusDeployed, resp.DeploymentStatus)
	}
}

func TestBuildResponse_SyncStatus_InSync(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{
		ID:                    "agent-1",
		AgentURL:              "https://example.com",
		Name:                  "test",
		AgentCardLocation:     "well_known",
		RequestTimeoutSeconds: 120,
	}

	hash := computeA2AAgentConfigHash(a)
	deploy := &store.AgentDeployment{
		Status:     "success",
		Action:     DeployActionDeploy,
		ConfigHash: &hash,
	}

	resp := svc.buildResponse(a, deploy, DisplayStatusDeployed)

	if resp.SyncStatus == nil {
		t.Fatal("expected non-nil SyncStatus")
	}
	if *resp.SyncStatus != "in_sync" {
		t.Errorf("expected in_sync, got %s", *resp.SyncStatus)
	}
}

func TestBuildResponse_SyncStatus_OutOfSync(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{
		ID:                    "agent-1",
		AgentURL:              "https://example.com",
		Name:                  "test",
		AgentCardLocation:     "well_known",
		RequestTimeoutSeconds: 120,
	}

	hash := computeA2AAgentConfigHash(a)
	deploy := &store.AgentDeployment{
		Status:     "success",
		Action:     DeployActionDeploy,
		ConfigHash: &hash,
	}

	a.Name = "changed-name"

	resp := svc.buildResponse(a, deploy, DisplayStatusDeployed)

	if resp.SyncStatus == nil {
		t.Fatal("expected non-nil SyncStatus")
	}
	if *resp.SyncStatus != "out_of_sync" {
		t.Errorf("expected out_of_sync, got %s", *resp.SyncStatus)
	}
}

func TestBuildResponse_SyncStatus_NilWhenNoConfigHash(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{
		ID:                "agent-1",
		AgentURL:          "https://example.com",
		Name:              "test",
		AgentCardLocation: "well_known",
	}

	deploy := &store.AgentDeployment{
		Status: "success",
		Action: DeployActionDeploy,
	}

	resp := svc.buildResponse(a, deploy, DisplayStatusDeployed)

	if resp.DeploymentStatus != DisplayStatusDeployed {
		t.Errorf("expected %q, got %q", DisplayStatusDeployed, resp.DeploymentStatus)
	}
	if resp.SyncStatus != nil {
		t.Errorf("expected nil SyncStatus when no config hash, got %v", *resp.SyncStatus)
	}
}

func TestBuildResponse_DeployedSnapshot(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{
		ID:                "agent-1",
		AgentURL:          "https://example.com",
		Name:              "test",
		AgentCardLocation: "well_known",
	}

	snapshot := `{"name":"test","agentUrl":"https://example.com"}`
	hash := computeA2AAgentConfigHash(a)
	deploy := &store.AgentDeployment{
		Status:              "success",
		Action:              DeployActionDeploy,
		ConfigHash:          &hash,
		AgentConfigSnapshot: &snapshot,
		CreatedBy:           "user-1",
		CreatedTime:         1700000000000,
	}

	resp := svc.buildResponse(a, deploy, DisplayStatusDeployed)

	if resp.Deployed == nil {
		t.Fatal("expected non-nil Deployed snapshot")
	}
	deployed, ok := resp.Deployed.(*DeployedRemoteAgentInfo)
	if !ok {
		t.Fatalf("expected *DeployedRemoteAgentInfo, got %T", resp.Deployed)
	}
	if deployed.DeployedBy != "user-1" {
		t.Errorf("expected deployedBy=user-1, got %v", deployed.DeployedBy)
	}
	if deployed.Name != "test" {
		t.Errorf("expected name=test, got %v", deployed.Name)
	}
}

func TestBuildResponse_DeployedSnapshot_DropsLegacySecrets(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{
		ID:                "agent-1",
		AgentURL:          "https://example.com",
		Name:              "AdditionAgent",
		AgentCardLocation: "well_known",
	}

	legacy := `{"Name":"AdditionAgent","AgentURL":"https://example.com","AgentAuthType":"oauth2",` +
		`"Skills":"[{\"name\":\"add_numbers\"}]",` +
		`"AgentAuthConfig":"{\"type\":\"oauth2\",\"client_secret\":\"SUPER-SECRET\"}",` +
		`"AgentCardAuthConfig":"{\"type\":\"oauth2\",\"client_secret\":\"SUPER-SECRET\"}"}`
	hash := computeA2AAgentConfigHash(a)
	deploy := &store.AgentDeployment{
		Status:              DeployStatusSuccess,
		Action:              DeployActionDeploy,
		ConfigHash:          &hash,
		AgentConfigSnapshot: &legacy,
		CreatedBy:           "user-1",
		CreatedTime:         1700000000000,
	}

	deployed := svc.buildResponse(a, deploy, DisplayStatusDeployed).Deployed
	blob, err := json.Marshal(deployed)
	if err != nil {
		t.Fatalf("marshal deployed: %v", err)
	}
	if strings.Contains(string(blob), "SUPER-SECRET") {
		t.Errorf("deployed snapshot leaked secret: %s", blob)
	}
	for _, forbidden := range []string{"AgentAuthConfig", "agentAuthConfig", "AgentCardAuthConfig", "agentCardAuthConfig", "client_secret"} {
		if strings.Contains(string(blob), forbidden) {
			t.Errorf("legacy snapshot still exposes %q: %s", forbidden, blob)
		}
	}

	dep, ok := deployed.(*DeployedRemoteAgentInfo)
	if !ok {
		t.Fatalf("expected *DeployedRemoteAgentInfo, got %T", deployed)
	}
	if dep.Name != "AdditionAgent" {
		t.Errorf("expected name folded from legacy \"Name\", got %q", dep.Name)
	}
	if dep.AgentAuthType != "oauth2" {
		t.Errorf("expected agentAuthType folded from legacy \"AgentAuthType\", got %q", dep.AgentAuthType)
	}
	if _, ok := dep.Skills.([]any); !ok {
		t.Errorf("expected legacy string skills normalized to []any, got %T", dep.Skills)
	}
}

func TestDeployedRemoteAgentInfo_CamelCaseNoSecrets(t *testing.T) {
	t.Parallel()
	blob, err := json.Marshal(DeployedRemoteAgentInfo{
		Name:          "AdditionAgent",
		AgentURL:      "https://example.com",
		AgentAuthType: "bearer",
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	s := string(blob)
	if !strings.Contains(s, `"name":"AdditionAgent"`) {
		t.Errorf("expected camelCase name, got: %s", s)
	}
	for _, forbidden := range []string{"AgentAuthConfig", "agentAuthConfig", "AgentCardAuthConfig", "agentCardAuthConfig"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("curated snapshot must not carry auth-config objects, found %q: %s", forbidden, s)
		}
	}
}

func TestDecodeDeployedSnapshot(t *testing.T) {
	t.Parallel()

	if got, err := decodeDeployedSnapshot[DeployedRemoteAgentInfo](nil); got != nil || err != nil {
		t.Errorf("nil snapshot: got (%v, %v), want (nil, nil)", got, err)
	}

	bad := `{"name": "x"`
	if got, err := decodeDeployedSnapshot[DeployedRemoteAgentInfo](&bad); got != nil || err == nil {
		t.Errorf("malformed snapshot: got (%v, %v), want (nil, error)", got, err)
	}

	ok := `{"name":"AdditionAgent"}`
	got, err := decodeDeployedSnapshot[DeployedRemoteAgentInfo](&ok)
	if err != nil || got == nil {
		t.Fatalf("valid snapshot: got (%v, %v), want (&dto, nil)", got, err)
	}
	if got.Name != "AdditionAgent" {
		t.Errorf("decoded name = %q, want AdditionAgent", got.Name)
	}
}

func TestBuildResponse_DeployFailed_ShowsSyncStatus(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{
		ID:                "agent-1",
		AgentURL:          "https://example.com",
		Name:              "test",
		AgentCardLocation: "well_known",
	}

	hash := computeA2AAgentConfigHash(a)
	deploy := &store.AgentDeployment{
		Status:     "failed",
		Action:     DeployActionDeploy,
		ConfigHash: &hash,
	}

	resp := svc.buildResponse(a, deploy, DisplayStatusDeployFailed)

	if resp.DeploymentStatus != DisplayStatusDeployFailed {
		t.Errorf("expected %q, got %q", DisplayStatusDeployFailed, resp.DeploymentStatus)
	}
	if resp.SyncStatus == nil {
		t.Fatal("expected non-nil SyncStatus for deploy_failed")
	}
	if *resp.SyncStatus != "in_sync" {
		t.Errorf("expected in_sync, got %s", *resp.SyncStatus)
	}
}

func TestA2AAgentService_CreateGetUpdate_WithDeployment(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	agentDeployRepo := store.NewAgentDeploymentRepo(db)
	agentRepo := store.NewAgentRepo(db)

	svc := NewA2AAgentService(a2aRepo, agentDeployRepo, agentRepo, nil, nil, nil, "test-ns", nil)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com/agent",
		Name:              "integ-test",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "skill1"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.DeploymentStatus != DisplayStatusNotDeployed {
		t.Errorf("new agent should be not_deployed, got %s", created.DeploymentStatus)
	}
	if created.SyncStatus != nil {
		t.Error("new agent should have nil SyncStatus")
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DeploymentStatus != DisplayStatusNotDeployed {
		t.Errorf("expected not_deployed, got %s", got.DeploymentStatus)
	}

	a2aAgent, _ := a2aRepo.GetByID(ctx, created.ID)

	configHash := computeA2AAgentConfigHash(a2aAgent)
	snapshot := `{"name":"integ-test"}`
	deployment := &store.AgentDeployment{
		AgentID:             created.ID,
		Status:              "success",
		Action:              DeployActionDeploy,
		ConfigHash:          &configHash,
		AgentConfigSnapshot: &snapshot,
		CreatedBy:           "test-user",
	}
	if deployErr := agentDeployRepo.Create(ctx, deployment); deployErr != nil {
		t.Fatalf("create deployment: %v", deployErr)
	}

	got, err = svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get after deploy: %v", err)
	}
	if got.DeploymentStatus != DisplayStatusDeployed {
		t.Errorf("expected deployed, got %s", got.DeploymentStatus)
	}
	if got.SyncStatus == nil || *got.SyncStatus != "in_sync" {
		t.Errorf("expected in_sync, got %v", got.SyncStatus)
	}
	if got.Deployed == nil {
		t.Error("expected non-nil Deployed snapshot")
	}

	updated, err := svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{
		Name: "changed-name",
	}, "test-user")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.SyncStatus == nil || *updated.SyncStatus != "out_of_sync" {
		t.Errorf("expected out_of_sync after name change, got %v", updated.SyncStatus)
	}
}

func TestA2AAgentService_UpdateToCardNoneRequiresSkills(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewA2AAgentService(store.NewA2AAgentRepo(db), store.NewAgentDeploymentRepo(db), store.NewAgentRepo(db), nil, nil, nil, "test-ns", nil)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "well-known agent, skills come from the fetched card",
		AgentURL:          "https://example.com/agent",
		Name:              "flip-to-none",
		AgentCardLocation: "well_known",
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create well_known: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{
		AgentCardLocation: "none",
	}, "test-user")
	if err == nil {
		t.Fatal("expected flip to card=none without skills to be rejected")
	}
	if !strings.Contains(err.Error(), "skills are required") {
		t.Errorf("error = %q, want it to mention skills are required", err.Error())
	}

	updated, err := svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "skill1"}},
	}, "test-user")
	if err != nil {
		t.Fatalf("flip to card=none WITH skills should succeed: %v", err)
	}
	if updated.AgentCardLocation != "none" {
		t.Errorf("agentCardLocation = %q, want none", updated.AgentCardLocation)
	}

	if _, err := svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{Description: "renamed"}, "test-user"); err != nil {
		t.Fatalf("editing a card=none agent that already has skills should succeed: %v", err)
	}
}

func TestA2AAgentService_UndeployResetsDeploymentStatus(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	agentDeployRepo := store.NewAgentDeploymentRepo(db)
	agentRepo := store.NewAgentRepo(db)

	svc := NewA2AAgentService(a2aRepo, agentDeployRepo, agentRepo, nil, nil, nil, "test-ns", nil)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "regression fixture",
		AgentURL:          "https://example.com/agent",
		Name:              "undeploy-regression",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "skill1"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	a2aAgent, _ := a2aRepo.GetByID(ctx, created.ID)
	configHash := computeA2AAgentConfigHash(a2aAgent)
	snapshot := `{"name":"undeploy-regression"}`
	if deployErr := agentDeployRepo.Create(ctx, &store.AgentDeployment{
		AgentID:             created.ID,
		Status:              DeployStatusSuccess,
		Action:              DeployActionDeploy,
		ConfigHash:          &configHash,
		AgentConfigSnapshot: &snapshot,
		CreatedBy:           "test-user",
	}); deployErr != nil {
		t.Fatalf("create deploy row: %v", deployErr)
	}

	deployed, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get after deploy: %v", err)
	}
	if deployed.DeploymentStatus != DisplayStatusDeployed {
		t.Fatalf("precondition: expected deployed, got %s", deployed.DeploymentStatus)
	}

	if undeployErr := agentDeployRepo.Create(ctx, &store.AgentDeployment{
		AgentID:   created.ID,
		Status:    DeployStatusSuccess,
		Action:    DeployActionUndeploy,
		CreatedBy: "test-user",
	}); undeployErr != nil {
		t.Fatalf("create undeploy row: %v", undeployErr)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get after undeploy: %v", err)
	}
	if got.DeploymentStatus != DisplayStatusNotDeployed {
		t.Errorf("after undeploy expected %q, got %q", DisplayStatusNotDeployed, got.DeploymentStatus)
	}
	if got.SyncStatus != nil {
		t.Errorf("expected nil SyncStatus after undeploy, got %v", *got.SyncStatus)
	}
	if got.Deployed != nil {
		t.Error("expected nil Deployed snapshot after undeploy")
	}

	list, err := svc.ListPaginated(ctx, 1, 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, r := range list.Data {
		if r.ID == created.ID {
			found = true
			if r.DeploymentStatus != DisplayStatusNotDeployed {
				t.Errorf("list: after undeploy expected %q, got %q", DisplayStatusNotDeployed, r.DeploymentStatus)
			}
		}
	}
	if !found {
		t.Fatal("created agent missing from list")
	}
}

func TestA2AAgentService_FailedDeploySurfacesDeployFailed(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	agentDeployRepo := store.NewAgentDeploymentRepo(db)
	agentRepo := store.NewAgentRepo(db)

	svc := NewA2AAgentService(a2aRepo, agentDeployRepo, agentRepo, nil, nil, nil, "test-ns", nil)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "failed-deploy fixture",
		AgentURL:          "https://example.com/agent",
		Name:              "failed-deploy-regression",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "skill1"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if deployErr := agentDeployRepo.Create(ctx, &store.AgentDeployment{
		AgentID:   created.ID,
		Status:    DeployStatusFailed,
		Action:    DeployActionDeploy,
		CreatedBy: "test-user",
	}); deployErr != nil {
		t.Fatalf("create failed deploy row: %v", deployErr)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get after failed deploy: %v", err)
	}
	if got.DeploymentStatus != DisplayStatusDeployFailed {
		t.Errorf("after failed deploy expected %q, got %q", DisplayStatusDeployFailed, got.DeploymentStatus)
	}

	list, err := svc.ListPaginated(ctx, 1, 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, r := range list.Data {
		if r.ID == created.ID {
			found = true
			if r.DeploymentStatus != DisplayStatusDeployFailed {
				t.Errorf("list: after failed deploy expected %q, got %q", DisplayStatusDeployFailed, r.DeploymentStatus)
			}
		}
	}
	if !found {
		t.Fatal("created agent missing from list")
	}
}

func TestAgentRepo_AllowsMultipleA2AProxyRows(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	ctx := t.Context()

	for _, name := range []string{"proxy-one", "proxy-two"} {
		a := &store.Agent{
			Description: strPtr("test agent for fixture purposes"),
			Name:        name,
			Type:        "a2a_proxy",
			CreatedBy:   "test-user",
			UpdatedBy:   "test-user",
		}
		if err := agentRepo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
}

func TestA2AAgentService_RuntimeStatusUsesInstanceName(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	agentDeployRepo := store.NewAgentDeploymentRepo(db)
	agentRepo := store.NewAgentRepo(db)
	registry := platpkg.NewAgentRegistry()

	svc := NewA2AAgentService(a2aRepo, agentDeployRepo, agentRepo, registry, nil, nil, "test-ns", nil)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com/agent",
		Name:              "runtime-status-probe",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "skill1"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	a2aAgent, _ := a2aRepo.GetByID(ctx, created.ID)
	instanceName := A2AAgentInstanceName(a2aAgent)
	registry.AddOrUpdate(a2a.AgentCard{Name: instanceName, URL: "solace:test-ns/a2a/v1/agent/request/" + instanceName})

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RuntimeStatus.Status() != RuntimeStatusRunning {
		t.Errorf("RuntimeStatus = %q, want running (lookup key mismatch regression)", got.RuntimeStatus.Status())
	}
}

func TestA2AAgentService_DeleteRejectedWhenDeployed(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	agentDeployRepo := store.NewAgentDeploymentRepo(db)
	agentRepo := store.NewAgentRepo(db)

	svc := NewA2AAgentService(a2aRepo, agentDeployRepo, agentRepo, nil, nil, nil, "test-ns", nil)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com/agent",
		Name:              "delete-probe",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "skill1"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if delErr := svc.Delete(ctx, created.ID); delErr != nil {
		t.Fatalf("delete of undeployed agent: %v", delErr)
	}

	recreated, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com/agent",
		Name:              "delete-probe",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "skill1"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	snapshot := `{"name":"delete-probe"}`
	hash := "h"
	deployment := &store.AgentDeployment{
		AgentID:             recreated.ID,
		Status:              DeployStatusSuccess,
		Action:              DeployActionDeploy,
		ConfigHash:          &hash,
		AgentConfigSnapshot: &snapshot,
		CreatedBy:           "test-user",
	}
	if depErr := agentDeployRepo.Create(ctx, deployment); depErr != nil {
		t.Fatalf("seed deployment: %v", depErr)
	}

	err = svc.Delete(ctx, recreated.ID)
	if err == nil {
		t.Fatal("expected delete to be rejected for deployed agent")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}

	failedUndeploy := &store.AgentDeployment{
		AgentID:   recreated.ID,
		Status:    DeployStatusFailed,
		Action:    DeployActionUndeploy,
		CreatedBy: "test-user",
	}
	if depErr := agentDeployRepo.Create(ctx, failedUndeploy); depErr != nil {
		t.Fatalf("seed failed undeploy: %v", depErr)
	}
	if delErr := svc.Delete(ctx, recreated.ID); delErr == nil {
		t.Fatal("expected delete to be rejected after a failed undeploy")
	} else if _, ok := delErr.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError after failed undeploy, got %T: %v", delErr, delErr)
	}

	failedDeploy := &store.AgentDeployment{
		AgentID:   recreated.ID,
		Status:    DeployStatusFailed,
		Action:    DeployActionDeploy,
		CreatedBy: "test-user",
	}
	if depErr := agentDeployRepo.Create(ctx, failedDeploy); depErr != nil {
		t.Fatalf("seed failed deploy: %v", depErr)
	}
	if delErr := svc.Delete(ctx, recreated.ID); delErr == nil {
		t.Fatal("expected delete to be rejected after a failed deploy")
	} else if _, ok := delErr.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError after failed deploy, got %T: %v", delErr, delErr)
	}

	undeploy := &store.AgentDeployment{
		AgentID:             recreated.ID,
		Status:              DeployStatusSuccess,
		Action:              DeployActionUndeploy,
		ConfigHash:          &hash,
		AgentConfigSnapshot: &snapshot,
		CreatedBy:           "test-user",
	}
	if depErr := agentDeployRepo.Create(ctx, undeploy); depErr != nil {
		t.Fatalf("seed undeploy: %v", depErr)
	}
	if delErr := svc.Delete(ctx, recreated.ID); delErr != nil {
		t.Fatalf("delete after undeploy: %v", delErr)
	}

	existing, _, _, _, _, _ := agentRepo.GetByID(ctx, recreated.ID)
	if existing != nil {
		t.Errorf("paired agents row should be deleted, got %+v", existing)
	}
}

func TestA2AAgentService_DeleteFreesDisplayNameForReuse(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	agentDeployRepo := store.NewAgentDeploymentRepo(db)
	agentRepo := store.NewAgentRepo(db)

	svc := NewA2AAgentService(a2aRepo, agentDeployRepo, agentRepo, nil, nil, nil, "test-ns", nil)
	ctx := t.Context()

	first, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com/a",
		Name:              "reusable-name",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "s"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if existing, _, _, _, _, _ := agentRepo.GetByID(ctx, first.ID); existing == nil {
		t.Fatal("expected paired agents row after Create")
	}
	if delErr := svc.Delete(ctx, first.ID); delErr != nil {
		t.Fatalf("delete: %v", delErr)
	}

	if _, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com/b",
		Name:              "reusable-name",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "s"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user"); err != nil {
		t.Fatalf("create second: %v", err)
	}
}

func TestA2ABuildResponse_InstanceNameAndRuntimeStatus(t *testing.T) {
	t.Parallel()
	registry := platpkg.NewAgentRegistry()
	svc := &A2AAgentService{agentRegistry: registry}

	a := &store.A2AAgent{
		ID:                "11111111-1111-1111-1111-111111111111",
		AgentURL:          "https://example.com/agent",
		Name:              "Web Search",
		AgentCardLocation: "well_known",
	}
	wantInstance := A2AAgentInstanceName(a)
	const expectInstance = "agent_11111111_1111_1111_1111_111111111111"
	if wantInstance != expectInstance {
		t.Fatalf("A2AAgentInstanceName = %q, want %q", wantInstance, expectInstance)
	}

	t.Run("instance_name_populated", func(t *testing.T) {
		resp := svc.buildResponse(a, nil, DisplayStatusNotDeployed)
		if resp.InstanceName != wantInstance {
			t.Errorf("InstanceName = %q, want %q", resp.InstanceName, wantInstance)
		}
	})

	t.Run("running_when_registry_has_instance_card", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{Name: wantInstance})
		resp := svc.buildResponse(a, nil, DisplayStatusNotDeployed)
		if resp.RuntimeStatus.Status() != RuntimeStatusRunning {
			t.Errorf("RuntimeStatus = %q, want running", resp.RuntimeStatus.Status())
		}
	})

	t.Run("degraded_when_registry_card_reports_active_problems", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{
			Name: wantInstance,
			Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
				a2a.HealthProblemsExtension([]health.Problem{{ID: "remote:fetch", Severity: health.Degraded, Detail: "boom"}}),
			}},
		})
		resp := svc.buildResponse(a, nil, DisplayStatusNotDeployed)
		if resp.RuntimeStatus.Status() != RuntimeStatusDegraded {
			t.Errorf("RuntimeStatus.Status() = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusDegraded)
		}
		if len(resp.RuntimeStatus.Problems()) != 1 || resp.RuntimeStatus.Problems()[0].ID != "remote:fetch" {
			t.Errorf("RuntimeStatus.Problems() = %v, want one problem with id remote:fetch", resp.RuntimeStatus.Problems())
		}
	})

	t.Run("stopped_when_only_legacy_key_present", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{Name: "agent_" + a.ID})
		resp := svc.buildResponse(a, nil, DisplayStatusNotDeployed)
		if resp.RuntimeStatus.Status() == "running" {
			t.Errorf("RuntimeStatus = %q, want not running (legacy key must not match)", resp.RuntimeStatus.Status())
		}
	})

	t.Run("disconnected_when_deployed_but_no_card", func(t *testing.T) {
		registry.Clear()
		hash := "h"
		snap := `{"name":"Web Search"}`
		latestDeploy := &store.AgentDeployment{
			AgentID:             a.ID,
			Status:              DeployStatusSuccess,
			Action:              DeployActionDeploy,
			ConfigHash:          &hash,
			AgentConfigSnapshot: &snap,
		}
		resp := svc.buildResponse(a, latestDeploy, DisplayStatusDeployed)
		if resp.DeploymentStatus != DisplayStatusDeployed {
			t.Fatalf("precondition: DeploymentStatus = %q, want deployed", resp.DeploymentStatus)
		}
		if resp.RuntimeStatus.Status() != RuntimeStatusDisconnected {
			t.Errorf("RuntimeStatus = %q, want disconnected (deployed + registry empty)", resp.RuntimeStatus.Status())
		}
	})

	t.Run("stopped_when_not_deployed_and_no_card", func(t *testing.T) {
		registry.Clear()
		resp := svc.buildResponse(a, nil, DisplayStatusNotDeployed)
		if resp.DeploymentStatus != DisplayStatusNotDeployed {
			t.Fatalf("precondition: DeploymentStatus = %q, want not_deployed", resp.DeploymentStatus)
		}
		if resp.RuntimeStatus.Status() != RuntimeStatusStopped {
			t.Errorf("RuntimeStatus = %q, want stopped (not deployed + registry empty)", resp.RuntimeStatus.Status())
		}
	})
}
