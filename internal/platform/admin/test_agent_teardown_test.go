// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type fakeDeploymentControl struct {
	undeployCalls atomic.Int32
	undeployErr   error
}

func (f *fakeDeploymentControl) Undeploy(_ context.Context, _, _ string) error {
	f.undeployCalls.Add(1)
	return f.undeployErr
}

func (f *fakeDeploymentControl) CreateWithOpts(_ context.Context, _ CreateAgentDeploymentRequest, _ string, _ DeployOptions) (*AgentDeploymentResponse, error) {
	return nil, errors.New("CreateWithOpts not stubbed in this test")
}

func newTestAgentSvcWithDeployer(t *testing.T, agentRepo *store.AgentRepo, workflowRepo *store.WorkflowRepo, deployer deploymentControl) *TestAgentService {
	t.Helper()
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	return &TestAgentService{
		agentRepo:     agentRepo,
		workflowRepo:  workflowRepo,
		deploymentSvc: deployer,
		agentSvc:      agentSvc,
		ttl:           time.Hour,
		logger:        slog.Default(),
	}
}

func seedSuccessfulDeploy(t *testing.T, db *store.DB, agentID, userID string) {
	t.Helper()
	repo := store.NewAgentDeploymentRepo(db)
	d := &store.AgentDeployment{
		AgentID:   agentID,
		Action:    DeployActionDeploy,
		Status:    DeployStatusSuccess,
		CreatedBy: userID,
	}
	if err := repo.Create(t.Context(), d); err != nil {
		t.Fatalf("seed deploy row: %v", err)
	}
}

func TestTearDown_UndeployFailureKeepsDBRow(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)

	deployer := &fakeDeploymentControl{undeployErr: errors.New("control plane unreachable")}
	svc := newTestAgentSvcWithDeployer(t, agentRepo, workflowRepo, deployer)

	eph := seedEphemeral(t, agentRepo, "session-A", "user-A", time.Hour)
	seedSuccessfulDeploy(t, db, eph.ID, "user-A")

	err := svc.DeleteForSession(t.Context(), "session-A", "user-A")

	if err == nil {
		t.Fatal("DeleteForSession returned nil, want error from failed Undeploy")
	}
	if !strings.Contains(err.Error(), "control plane unreachable") {
		t.Errorf("error chain doesn't include the underlying cause: %v", err)
	}
	if got := deployer.undeployCalls.Load(); got != 1 {
		t.Errorf("undeployCalls = %d, want 1 (status=deployed must trigger exactly one Undeploy)", got)
	}

	got, _, _, _, _, getErr := agentRepo.GetByID(t.Context(), eph.ID)
	if getErr != nil {
		t.Fatalf("GetByID after failed teardown: %v", getErr)
	}
	if got == nil {
		t.Fatal("agent row was deleted despite Undeploy failure — orphan leak path is open again")
	}
}

func TestTearDown_UndeploySuccessDeletesRow(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)

	deployer := &fakeDeploymentControl{undeployErr: nil}
	svc := newTestAgentSvcWithDeployer(t, agentRepo, workflowRepo, deployer)

	eph := seedEphemeral(t, agentRepo, "session-OK", "user-OK", time.Hour)
	seedSuccessfulDeploy(t, db, eph.ID, "user-OK")

	if err := svc.DeleteForSession(t.Context(), "session-OK", "user-OK"); err != nil {
		t.Fatalf("DeleteForSession: %v, want nil on successful Undeploy", err)
	}
	if got := deployer.undeployCalls.Load(); got != 1 {
		t.Errorf("undeployCalls = %d, want 1", got)
	}

	got, _, _, _, _, getErr := agentRepo.GetByID(t.Context(), eph.ID)
	if getErr != nil {
		t.Fatalf("GetByID after success: %v", getErr)
	}
	if got != nil {
		t.Error("agent row still present after successful Undeploy")
	}
}

func TestTearDown_NotDeployedSkipsUndeploy(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)

	deployer := &fakeDeploymentControl{undeployErr: errors.New("should not be reached")}
	svc := newTestAgentSvcWithDeployer(t, agentRepo, workflowRepo, deployer)

	eph := seedEphemeral(t, agentRepo, "session-N", "user-N", time.Hour)

	if err := svc.DeleteForSession(t.Context(), "session-N", "user-N"); err != nil {
		t.Fatalf("DeleteForSession on not-deployed agent: %v, want nil", err)
	}
	if got := deployer.undeployCalls.Load(); got != 0 {
		t.Errorf("undeployCalls = %d, want 0 (not_deployed status must skip Undeploy)", got)
	}

	got, _, _, _, _, getErr := agentRepo.GetByID(t.Context(), eph.ID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if got != nil {
		t.Error("agent row still present after not-deployed teardown")
	}
}

func TestPurgeExpired_KeepsRowWhenUndeployFails(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)

	deployer := &flakyDeployerByAgent{failing: map[string]bool{}}
	svc := newTestAgentSvcWithDeployer(t, agentRepo, workflowRepo, deployer)

	now := time.Now().UnixMilli()
	bad := seedEphemeralWithExpiry(t, agentRepo, "s-bad", "u-bad", now-1000)
	good := seedEphemeralWithExpiry(t, agentRepo, "s-good", "u-good", now-1000)
	seedSuccessfulDeploy(t, db, bad.ID, "u-bad")
	seedSuccessfulDeploy(t, db, good.ID, "u-good")
	deployer.failing[bad.ID] = true

	reaped, err := svc.PurgeExpired(t.Context())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped = %d, want 1 (only the good one)", reaped)
	}

	gotBad, _, _, _, _, _ := agentRepo.GetByID(t.Context(), bad.ID)
	if gotBad == nil {
		t.Error("bad agent row was deleted despite Undeploy failure; the sweeper used to silently orphan these")
	}
	gotGood, _, _, _, _, _ := agentRepo.GetByID(t.Context(), good.ID)
	if gotGood != nil {
		t.Error("good agent row still present; one failure blocked the rest of the sweep")
	}
}

type flakyDeployerByAgent struct {
	failing map[string]bool
}

func (f *flakyDeployerByAgent) Undeploy(_ context.Context, agentID, _ string) error {
	if f.failing[agentID] {
		return errors.New("control plane unreachable for " + agentID)
	}
	return nil
}

func (f *flakyDeployerByAgent) CreateWithOpts(_ context.Context, _ CreateAgentDeploymentRequest, _ string, _ DeployOptions) (*AgentDeploymentResponse, error) {
	return nil, errors.New("CreateWithOpts not stubbed in this test")
}

func TestCreateForSession_RefusesWhenPriorCleanupFails(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)

	deployer := &fakeDeploymentControl{undeployErr: errors.New("control plane unreachable")}
	svc := newTestAgentSvcWithDeployer(t, agentRepo, workflowRepo, deployer)

	prompt := "Helpful assistant. Long enough to satisfy the prompt-length validator and keep platform conventions happy."
	desc := "target"
	target := &store.Agent{
		Name:         "TargetComponent",
		Type:         AgentTypeStandard,
		Description:  &desc,
		SystemPrompt: &prompt,
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := agentRepo.Create(t.Context(), target, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	const sessionID = "019e045e-1491-7ba0-9611-717bd7cc595e"
	const userID = "u-create-fails"
	prior := seedEphemeral(t, agentRepo, sessionID, userID, time.Hour)
	seedSuccessfulDeploy(t, db, prior.ID, userID)

	got, err := svc.CreateForSession(t.Context(), sessionID, target.Name, ComponentKindAgent, userID)
	if err == nil {
		t.Fatal("CreateForSession returned nil error despite cleanup failure")
	}
	if got != nil {
		t.Errorf("CreateForSession returned a response %v alongside a still-deployed prior; the old behavior leaked stacked agents", got)
	}
	if !strings.Contains(err.Error(), "control plane unreachable") {
		t.Errorf("error chain doesn't include the underlying cause: %v", err)
	}

	stillThere, _, _, _, _, _ := agentRepo.GetByID(t.Context(), prior.ID)
	if stillThere == nil {
		t.Error("prior agent was deleted despite Undeploy failure — caller's retry path is now broken")
	}
}

func TestTearDown_AfterFailedUndeployStillUndeploys(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)

	deployer := &fakeDeploymentControl{}
	svc := newTestAgentSvcWithDeployer(t, agentRepo, workflowRepo, deployer)

	eph := seedEphemeral(t, agentRepo, "session-F", "user-F", time.Hour)
	seedSuccessfulDeploy(t, db, eph.ID, "user-F")
	if err := store.NewAgentDeploymentRepo(db).Create(t.Context(), &store.AgentDeployment{
		AgentID: eph.ID, Action: DeployActionUndeploy, Status: DeployStatusFailed, CreatedBy: "user-F",
	}); err != nil {
		t.Fatalf("seed failed undeploy: %v", err)
	}

	if err := svc.DeleteForSession(t.Context(), "session-F", "user-F"); err != nil {
		t.Fatalf("DeleteForSession: %v", err)
	}
	if got := deployer.undeployCalls.Load(); got != 1 {
		t.Errorf("undeployCalls = %d, want 1 (a failed undeploy leaves the instance live)", got)
	}
}

func TestTearDown_DeploymentLookupFailureKeepsRow(t *testing.T) {
	t.Parallel()
	db, dbURL := openReopenableTestDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)

	deployer := &fakeDeploymentControl{}
	svc := newTestAgentSvcWithDeployer(t, agentRepo, workflowRepo, deployer)

	eph := seedEphemeral(t, agentRepo, "session-L", "user-L", time.Hour)
	seedSuccessfulDeploy(t, db, eph.ID, "user-L")
	closeTestDB(t, db)

	err := svc.tearDown(t.Context(), eph.ID, "user-L")
	if err == nil {
		t.Fatal("tearDown returned nil, want the deployment lookup error")
	}
	if !strings.Contains(err.Error(), "check for a live deployment") {
		t.Errorf("error = %v, want it wrapped with \"check for a live deployment\"", err)
	}
	if got := deployer.undeployCalls.Load(); got != 0 {
		t.Errorf("undeployCalls = %d, want 0 (no undeploy without a readable deployment history)", got)
	}

	got, _, _, _, _, getErr := store.NewAgentRepo(reopenTestDB(t, dbURL)).GetByID(t.Context(), eph.ID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if got == nil {
		t.Fatal("agent row was deleted although the deployment lookup failed")
	}
}
