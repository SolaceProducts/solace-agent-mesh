// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	deleteGateNamespace   = "test-ns"
	deleteGateAgentPrompt = "You are a test agent. This system prompt is long enough to satisfy the validator's minimum length requirement."
	agentsPath            = "/api/v1/platform/agents/"
)

type seededDeployment struct {
	action string
	status string
}

type agentDeleteEnv struct {
	db                  *store.DB
	agentRepo           *store.AgentRepo
	a2aRepo             *store.A2AAgentRepo
	agentDeploymentRepo *store.AgentDeploymentRepo
	deploySvc           *DeploymentService
	agentSvc            *AgentService
	a2aSvc              *A2AAgentService
}

func newAgentDeleteEnv(db *store.DB, ctrl *control.Client) *agentDeleteEnv {
	logger := slog.Default()
	agentRepo := store.NewAgentRepo(db)
	a2aRepo := store.NewA2AAgentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	agentDeploymentRepo := store.NewAgentDeploymentRepo(db)
	yamlSvc := NewYAMLService(deleteGateNamespace, logger)
	deployerRegistry := NewDefaultDeployerRegistry(yamlSvc, connectorRepo, nil, nil, a2aRepo, deleteGateNamespace, logger)
	return &agentDeleteEnv{
		db:                  db,
		agentRepo:           agentRepo,
		a2aRepo:             a2aRepo,
		agentDeploymentRepo: agentDeploymentRepo,
		deploySvc:           NewDeploymentService(store.NewDeploymentRepo(db), agentRepo, connectorRepo, nil, yamlSvc, ctrl, deployerRegistry, logger),
		agentSvc:            NewAgentService(agentRepo, agentDeploymentRepo, connectorRepo, nil, nil, nil, logger),
		a2aSvc:              NewA2AAgentService(a2aRepo, agentDeploymentRepo, agentRepo, nil, nil, nil, deleteGateNamespace, nil),
	}
}

func (e *agentDeleteEnv) createAgent(t *testing.T, name string) string {
	t.Helper()
	resp, err := e.agentSvc.Create(t.Context(), CreateAgentRequest{
		Name:         name,
		Description:  "agent used to verify how delete treats a possibly live instance",
		SystemPrompt: deleteGateAgentPrompt,
	}, "test-user")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return resp.ID
}

func (e *agentDeleteEnv) createRemoteAgent(t *testing.T, name string) string {
	t.Helper()
	created, err := e.a2aSvc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "remote agent used to verify how delete treats a possibly live proxy",
		AgentURL:          "https://example.com/agent",
		Name:              name,
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "skill1"}},
		AgentAuthConfig:   map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("create remote agent: %v", err)
	}
	return created.ID
}

func (e *agentDeleteEnv) seedHistory(t *testing.T, agentID string, history []seededDeployment) {
	t.Helper()
	for _, d := range history {
		if seedErr := e.agentDeploymentRepo.Create(t.Context(), &store.AgentDeployment{
			AgentID: agentID, Action: d.action, Status: d.status, CreatedBy: "test-user",
		}); seedErr != nil {
			t.Fatalf("seed deployment: %v", seedErr)
		}
	}
}

func (e *agentDeleteEnv) remoteAgentKept(t *testing.T, id string) bool {
	t.Helper()
	row, err := e.a2aRepo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID remote agent: %v", err)
	}
	return row != nil
}

func (e *agentDeleteEnv) agentKept(t *testing.T, id string) bool {
	t.Helper()
	row, _, _, _, _, err := e.agentRepo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID agent: %v", err)
	}
	return row != nil
}

type workflowDeleteEnv struct {
	workflowRepo   *store.WorkflowRepo
	deploymentRepo *store.WorkflowDeploymentRepo
	deploySvc      *WorkflowDeploymentService
	workflowSvc    *WorkflowService
}

func newWorkflowDeleteEnv(db *store.DB, ctrl *control.Client) *workflowDeleteEnv {
	logger := slog.Default()
	workflowRepo := store.NewWorkflowRepo(db)
	deploymentRepo := store.NewWorkflowDeploymentRepo(db)
	return &workflowDeleteEnv{
		workflowRepo:   workflowRepo,
		deploymentRepo: deploymentRepo,
		deploySvc:      NewWorkflowDeploymentService(deploymentRepo, workflowRepo, nil, nil, nil, nil, ctrl, deleteGateNamespace, logger),
		workflowSvc:    NewWorkflowService(workflowRepo, deploymentRepo, nil, nil, nil, deleteGateNamespace, logger),
	}
}

func (e *workflowDeleteEnv) createWorkflow(t *testing.T, name string) string {
	t.Helper()
	wf := &store.Workflow{Name: name, AppConfig: `{"workflow":{"nodes":[]}}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := e.workflowRepo.Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	return wf.ID
}

func renameTable(t *testing.T, db *store.DB, from, to string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE "+from+" RENAME TO "+to); err != nil {
		t.Fatalf("rename %s: %v", from, err)
	}
}

type recordingAWE struct {
	mu       sync.Mutex
	removals []string
}

func (*recordingAWE) Init(context.Context, config.Config) error { return nil }
func (*recordingAWE) Start(context.Context) error               { return nil }
func (*recordingAWE) Stop(context.Context) error                { return nil }
func (*recordingAWE) Health() error                             { return nil }

func (*recordingAWE) ManagementEndpoints() []control.Endpoint {
	return []control.Endpoint{{Method: "delete", Path: "instances/*", Description: "drop an instance"}}
}

func (a *recordingAWE) HandleManagement(_ context.Context, method string, path []string, _ json.RawMessage, _ *auth.Identity) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removals = append(a.removals, method+" "+strings.Join(path, "/"))
	return map[string]any{"status": "ok"}, nil
}

func (a *recordingAWE) rollOut(_ context.Context, instance, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removals = append(a.removals, "rollout "+instance)
	return nil
}

func (*recordingAWE) notify(context.Context, string) error { return nil }

func (*recordingAWE) anyLive() bool { return true }

func (*recordingAWE) awaitLive(context.Context) error { return nil }

func (*recordingAWE) removeQueues(string) error { return nil }

func (a *recordingAWE) removalCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.removals)
}

func startRecordingAWE(t *testing.T) (*runtime.Runtime, *recordingAWE) {
	t.Helper()
	ctx := context.Background()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	awe := &recordingAWE{}
	rt := runtime.NewRuntime(brk, config.FromMap(map[string]any{"namespace": deleteGateNamespace}),
		runtime.WithLogger(slog.New(slog.DiscardHandler)))
	rt.Register("awe", awe)
	rt.Register("control", control.NewService(rt, deleteGateNamespace))
	if err := rt.Init(ctx); err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})
	return rt, awe
}

func recordingControlClient(t *testing.T) (*control.Client, *recordingAWE) {
	t.Helper()
	rt, awe := startRecordingAWE(t)
	return control.NewClient(rt.Services().Broker(), deleteGateNamespace, control.WithTimeout(10*time.Second)), awe
}

func openReopenableTestDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	dbURL := entTestTemplate.Clone(t)
	db, err := store.Open(dbURL, nil)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dbURL
}

func reopenTestDB(t *testing.T, dbURL string) *store.DB {
	t.Helper()
	db, err := store.Open(dbURL, nil)
	if err != nil {
		t.Fatalf("reopen test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func closeTestDB(t *testing.T, db *store.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close test db: %v", err)
	}
}

func TestUndeploy_LookupFailureIsNotReportedAsNotFound(t *testing.T) {
	t.Parallel()
	const id = "01a0d4ff-0000-7000-8000-000000000001"

	t.Run("agent", func(t *testing.T) {
		t.Parallel()
		db, _ := openReopenableTestDB(t)
		deploySvc := newAgentDeleteEnv(db, nil).deploySvc
		closeTestDB(t, db)

		err := deploySvc.Undeploy(t.Context(), id, "test-user")
		if err == nil {
			t.Fatal("Undeploy succeeded against a closed database")
		}
		if !strings.Contains(err.Error(), "look up agent "+id) || strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %q, want the lookup failure wrapped as look up agent %s", err, id)
		}
	})

	t.Run("workflow", func(t *testing.T) {
		t.Parallel()
		db, _ := openReopenableTestDB(t)
		deploySvc := newWorkflowDeleteEnv(db, nil).deploySvc
		closeTestDB(t, db)

		err := deploySvc.Undeploy(t.Context(), id, "test-user")
		if err == nil {
			t.Fatal("Undeploy succeeded against a closed database")
		}
		if !strings.Contains(err.Error(), "look up workflow "+id) || strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %q, want the lookup failure wrapped as look up workflow %s", err, id)
		}
	})
}

func TestUndeploy_HistoryInsertFailureIsReported(t *testing.T) {
	t.Parallel()

	t.Run("agent", func(t *testing.T) {
		t.Parallel()
		ctrl, awe := recordingControlClient(t)
		env := newAgentDeleteEnv(testOpenDB(t), ctrl)
		env.deploySvc.awes = awe
		id := env.createAgent(t, "undeploy-history-insert-fails")
		renameTable(t, env.db, "agent_deployments", "agent_deployments_unavailable")

		err := env.deploySvc.Undeploy(t.Context(), id, "test-user")
		if got := awe.removalCount(); got != 1 {
			t.Fatalf("AWE rollouts = %d, want 1", got)
		}
		if err == nil || !strings.Contains(err.Error(), "record undeploy for "+id) {
			t.Fatalf("err = %v, want the history insert failure wrapped as record undeploy for %s", err, id)
		}
	})

	t.Run("workflow", func(t *testing.T) {
		t.Parallel()
		ctrl, awe := recordingControlClient(t)
		db := testOpenDB(t)
		env := newWorkflowDeleteEnv(db, ctrl)
		env.deploySvc.awes = awe
		id := env.createWorkflow(t, "Undeploy History Insert Fails")
		renameTable(t, db, "workflow_deployments", "workflow_deployments_unavailable")

		err := env.deploySvc.Undeploy(t.Context(), id, "test-user")
		if got := awe.removalCount(); got != 1 {
			t.Fatalf("AWE rollouts = %d, want 1", got)
		}
		if err == nil || !strings.Contains(err.Error(), "record undeploy for "+id) {
			t.Fatalf("err = %v, want the history insert failure wrapped as record undeploy for %s", err, id)
		}
	})
}
