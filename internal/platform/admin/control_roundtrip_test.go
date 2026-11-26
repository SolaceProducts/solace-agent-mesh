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
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type stubPlatformComponent struct {
	mp *ManageablePlatform
}

func (s *stubPlatformComponent) Init(_ context.Context, _ config.Config) error { return nil }
func (s *stubPlatformComponent) Start(_ context.Context) error                 { return nil }
func (s *stubPlatformComponent) Stop(_ context.Context) error                  { return nil }
func (s *stubPlatformComponent) Health() error                                 { return nil }

func (s *stubPlatformComponent) ManagementEndpoints() []control.Endpoint {
	return s.mp.ManagementEndpoints()
}

func (s *stubPlatformComponent) HandleManagement(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	return s.mp.HandleManagement(ctx, method, path, body, identity)
}

type controlTestEnv struct {
	Client        *control.Client
	SlowClient    *control.Client
	SkillSvc      *SkillService
	SkillRepo     *store.SkillRepo
	ToolSvc       *ToolService
	AgentSvc      *AgentService
	WorkflowSvc   *WorkflowService
	ConnectorSvc  *ConnectorService
	ConnectorRepo *store.ConnectorRepo
	GatewaySvc    *GatewayService
	A2AAgentSvc   *A2AAgentService
}

func setupControlTest(t *testing.T) *controlTestEnv {
	t.Helper()
	return setupControlTestWithDeps(t, nil)
}

func setupControlTestWithDeps(t *testing.T, mutate func(*RouteDeps)) *controlTestEnv {
	t.Helper()

	namespace := "test-ns"
	logger := slog.Default()

	db := testOpenDB(t)

	agentRepo := store.NewAgentRepo(db)
	agentDeployRepo := store.NewAgentDeploymentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	gatewayRepo := store.NewGatewayRepo(db)
	gwDeployRepo := store.NewGatewayDeploymentRepo(db)
	a2aRepo := store.NewA2AAgentRepo(db)
	skillRepo := store.NewSkillRepo(db)
	toolRepo := store.NewToolRepo(db)

	skillSvc := NewSkillService(db, skillRepo, logger)
	toolSvc := NewToolService(toolRepo, logger)
	agentSvc := NewAgentService(agentRepo, agentDeployRepo, connectorRepo, nil, nil, nil, logger)
	wfDeployRepo := store.NewWorkflowDeploymentRepo(db)
	workflowSvc := NewWorkflowService(workflowRepo, wfDeployRepo, nil, nil, nil, namespace, logger)
	connectorSvc := NewConnectorService(connectorRepo, nil, namespace, logger)
	gatewaySvc := NewGatewayService(gatewayRepo, gwDeployRepo, nil, nil, nil, logger)
	a2aSvc := NewA2AAgentService(a2aRepo, agentDeployRepo, agentRepo, nil, nil, nil, namespace, logger)

	deps := &RouteDeps{
		SkillSvc:     skillSvc,
		ToolSvc:      toolSvc,
		AgentSvc:     agentSvc,
		WorkflowSvc:  workflowSvc,
		ConnectorSvc: connectorSvc,
		GatewaySvc:   gatewaySvc,
		A2AAgentSvc:  a2aSvc,
	}
	if mutate != nil {
		mutate(deps)
	}

	core := platpkg.NewPlatformCore(nil, platpkg.ExtensionPoints{})
	mp := &ManageablePlatform{
		PlatformCore: core,
		deps:         deps,
		logger:       logger,
	}

	stub := &stubPlatformComponent{mp: mp}

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	rt.Register("platform", stub)

	ctrlSvc := control.NewService(rt, namespace)
	rt.Register("control", ctrlSvc)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	t.Cleanup(func() {
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})

	client := control.NewClient(brk, namespace, control.WithTimeout(5*time.Second))

	return &controlTestEnv{
		Client:        client,
		SlowClient:    control.NewClient(brk, namespace),
		SkillSvc:      skillSvc,
		SkillRepo:     skillRepo,
		ToolSvc:       toolSvc,
		AgentSvc:      agentSvc,
		WorkflowSvc:   workflowSvc,
		ConnectorSvc:  connectorSvc,
		ConnectorRepo: connectorRepo,
		GatewaySvc:    gatewaySvc,
		A2AAgentSvc:   a2aSvc,
	}
}

func TestControlProtocol_ListSkills(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := context.Background()

	tags1 := `["tag1"]`
	desc1 := "First skill"
	desc2 := "Second skill"
	if err := env.SkillRepo.UpsertByName(ctx, &store.Skill{Name: "ctrl-skill-1", Description: &desc1, Tags: &tags1, Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if err := env.SkillRepo.UpsertByName(ctx, &store.Skill{Name: "ctrl-skill-2", Description: &desc2, Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}); err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	raw, err := env.Client.Custom(ctx, "platform", "get", "skills", nil)
	if err != nil {
		t.Fatalf("custom request: %v", err)
	}

	var resp PaginatedResponse[SkillDetailResponse]
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 skills, got %d", len(resp.Data))
	}
}

func TestControlProtocol_GetSkillByID(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := context.Background()

	desc := "Skill for ID lookup"
	if err := env.SkillRepo.UpsertByName(ctx, &store.Skill{Name: "lookup-skill", Description: &desc, Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	listRaw, err := env.Client.Custom(ctx, "platform", "get", "skills", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listResp PaginatedResponse[SkillDetailResponse]
	if unmarshalErr := json.Unmarshal(listRaw, &listResp); unmarshalErr != nil {
		t.Fatalf("unmarshal list: %v", unmarshalErr)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(listResp.Data))
	}
	skillID := listResp.Data[0].ID

	raw, err := env.Client.Custom(ctx, "platform", "get", "skills/"+skillID, nil)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}

	var resp SkillDetailResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Name != "lookup-skill" {
		t.Errorf("name = %q, want lookup-skill", resp.Name)
	}
}

func TestControlProtocol_ListTools(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := context.Background()

	env.ToolSvc.UpsertFromInit(ctx, remotetool.InitParams{
		ToolName:    "ctrl-tool-1",
		Description: "First tool",
		Parameters:  map[string]any{"type": "object"},
	})
	env.ToolSvc.UpsertFromInit(ctx, remotetool.InitParams{
		ToolName:    "ctrl-tool-2",
		Description: "Second tool",
	})

	raw, err := env.Client.Custom(ctx, "platform", "get", "tools", nil)
	if err != nil {
		t.Fatalf("custom request: %v", err)
	}

	var resp PaginatedResponse[ToolDetailResponse]
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 tools, got %d", len(resp.Data))
	}
}

func TestControlProtocol_GetToolByID(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := context.Background()

	env.ToolSvc.UpsertFromInit(ctx, remotetool.InitParams{
		ToolName:    "lookup-tool",
		Description: "Tool for ID lookup",
	})

	listRaw, err := env.Client.Custom(ctx, "platform", "get", "tools", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listResp PaginatedResponse[ToolDetailResponse]
	if unmarshalErr := json.Unmarshal(listRaw, &listResp); unmarshalErr != nil {
		t.Fatalf("unmarshal list: %v", unmarshalErr)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(listResp.Data))
	}
	toolID := listResp.Data[0].ID

	raw, err := env.Client.Custom(ctx, "platform", "get", "tools/"+toolID, nil)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}

	var resp ToolDetailResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Name != "lookup-tool" {
		t.Errorf("name = %q, want lookup-tool", resp.Name)
	}
}

func TestControlProtocol_ListAndGetAgents(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	_, err := env.AgentSvc.Create(ctx, CreateAgentRequest{
		Name:         "ctrl-agent-1",
		Description:  "First control agent for testing",
		SystemPrompt: "You are a test agent. Answer questions about testing infrastructure and help verify system behavior.",
	}, "ctrl-user")
	if err != nil {
		t.Fatalf("create agent 1: %v", err)
	}

	agent2, err := env.AgentSvc.Create(ctx, CreateAgentRequest{
		Name:         "ctrl-agent-2",
		Description:  "Second control agent for testing",
		SystemPrompt: "You are another test agent. Answer questions about testing infrastructure and help verify system behavior.",
	}, "ctrl-user")
	if err != nil {
		t.Fatalf("create agent 2: %v", err)
	}

	raw, err := env.Client.Custom(ctx, "platform", "get", "agents", nil)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	var listResp PaginatedResponse[AgentResponse]
	if unmarshalErr := json.Unmarshal(raw, &listResp); unmarshalErr != nil {
		t.Fatalf("unmarshal list: %v", unmarshalErr)
	}
	if len(listResp.Data) != 2 {
		t.Errorf("expected 2 agents, got %d", len(listResp.Data))
	}

	raw, err = env.Client.Custom(ctx, "platform", "get", "agents/"+agent2.ID, nil)
	if err != nil {
		t.Fatalf("get agent by ID: %v", err)
	}
	var getResp AgentResponse
	if unmarshalErr := json.Unmarshal(raw, &getResp); unmarshalErr != nil {
		t.Fatalf("unmarshal get: %v", unmarshalErr)
	}
	if getResp.Name != "ctrl-agent-2" {
		t.Errorf("name = %q, want ctrl-agent-2", getResp.Name)
	}
}

func TestControlProtocol_ListAndGetWorkflows(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	wf, err := env.WorkflowSvc.Create(ctx, CreateWorkflowRequest{
		Name:        "ctrl-workflow",
		Description: "Workflow for control protocol testing",
		AppConfig:   map[string]any{"key": "value"},
	}, "ctrl-user")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	raw, err := env.Client.Custom(ctx, "platform", "get", "workflows", nil)
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	var listResp PaginatedResponse[WorkflowResponse]
	if unmarshalErr := json.Unmarshal(raw, &listResp); unmarshalErr != nil {
		t.Fatalf("unmarshal list: %v", unmarshalErr)
	}
	if len(listResp.Data) != 1 {
		t.Errorf("expected 1 workflow, got %d", len(listResp.Data))
	}

	raw, err = env.Client.Custom(ctx, "platform", "get", "workflows/"+wf.ID, nil)
	if err != nil {
		t.Fatalf("get workflow by ID: %v", err)
	}
	var getResp WorkflowResponse
	if unmarshalErr := json.Unmarshal(raw, &getResp); unmarshalErr != nil {
		t.Fatalf("unmarshal get: %v", unmarshalErr)
	}
	if getResp.Name != "ctrl-workflow" {
		t.Errorf("name = %q, want ctrl-workflow", getResp.Name)
	}
}

func TestControlProtocol_ListAndGetGateways(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	gw, err := env.GatewaySvc.Create(ctx, CreateGatewayRequest{
		Name:        "ctrl-gateway",
		Description: "Gateway for control protocol testing",
		Type:        "httpsse",
	}, "ctrl-user")
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	raw, err := env.Client.Custom(ctx, "platform", "get", "gateways", nil)
	if err != nil {
		t.Fatalf("list gateways: %v", err)
	}
	var listResp PaginatedResponse[GatewayResponse]
	if unmarshalErr := json.Unmarshal(raw, &listResp); unmarshalErr != nil {
		t.Fatalf("unmarshal list: %v", unmarshalErr)
	}
	if len(listResp.Data) != 1 {
		t.Errorf("expected 1 gateway, got %d", len(listResp.Data))
	}

	raw, err = env.Client.Custom(ctx, "platform", "get", "gateways/"+gw.ID, nil)
	if err != nil {
		t.Fatalf("get gateway by ID: %v", err)
	}
	var getResp GatewayResponse
	if unmarshalErr := json.Unmarshal(raw, &getResp); unmarshalErr != nil {
		t.Fatalf("unmarshal get: %v", unmarshalErr)
	}
	if getResp.Name != "ctrl-gateway" {
		t.Errorf("name = %q, want ctrl-gateway", getResp.Name)
	}
	if getResp.Type != "httpsse" {
		t.Errorf("type = %q, want httpsse", getResp.Type)
	}
}

func TestControlProtocol_ListAndGetConnectors(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	conn, err := env.ConnectorSvc.Create(ctx, CreateConnectorRequest{
		Description: "E2E test connector for fixture purposes",
		Name:        "ctrl-connector",
		Type:        "sql",
		Subtype:     "postgres",
		Values: map[string]any{
			"host": "localhost",
			"port": 5432,
		},
	}, "ctrl-user")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}

	raw, err := env.Client.Custom(ctx, "platform", "get", "connectors", nil)
	if err != nil {
		t.Fatalf("list connectors: %v", err)
	}
	var listResp PaginatedResponse[ConnectorResponse]
	if unmarshalErr := json.Unmarshal(raw, &listResp); unmarshalErr != nil {
		t.Fatalf("unmarshal list: %v", unmarshalErr)
	}
	if len(listResp.Data) != 1 {
		t.Errorf("expected 1 connector, got %d", len(listResp.Data))
	}

	raw, err = env.Client.Custom(ctx, "platform", "get", "connectors/"+conn.ID, nil)
	if err != nil {
		t.Fatalf("get connector by ID: %v", err)
	}
	var getResp ConnectorResponse
	if unmarshalErr := json.Unmarshal(raw, &getResp); unmarshalErr != nil {
		t.Fatalf("unmarshal get: %v", unmarshalErr)
	}
	if getResp.Name != "ctrl-connector" {
		t.Errorf("name = %q, want ctrl-connector", getResp.Name)
	}
	if getResp.Type != "sql" {
		t.Errorf("type = %q, want sql", getResp.Type)
	}

	raw, err = env.Client.Custom(ctx, "platform", "get", "connectors/schemas", nil)
	if err != nil {
		t.Fatalf("get connector schemas: %v", err)
	}
	var schemas []ConnectorSchemaResponse
	if unmarshalErr := json.Unmarshal(raw, &schemas); unmarshalErr != nil {
		t.Fatalf("unmarshal schemas: %v", unmarshalErr)
	}
	if len(schemas) == 0 {
		t.Error("expected non-empty connector schemas")
	}
}

func TestControlProtocol_GetConnectorByName_CleanError(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	_, err := env.Client.Custom(ctx, "platform", "get", "connectors/pet diet", nil)
	if err == nil {
		t.Fatal("expected an error getting a connector by a non-UUID id, got nil")
	}
	msg := err.Error()
	if !strings.Contains(strings.ToLower(msg), "uuid") {
		t.Errorf("error should explain the id must be a UUID, got: %q", msg)
	}
	for _, leak := range []string{"SQLSTATE", "invalid input syntax"} {
		if strings.Contains(msg, leak) {
			t.Errorf("error must not leak the raw DB error (%q), got: %q", leak, msg)
		}
	}
}

func TestControlProtocol_ListAndGetA2AAgents(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	a2a, err := env.A2AAgentSvc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		Name:              "ctrl-remote-agent",
		AgentURL:          "https://remote.example.com",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "test-skill"}},
		AgentAuthConfig:   map[string]any{},
	}, "ctrl-user")
	if err != nil {
		t.Fatalf("create A2A agent: %v", err)
	}

	raw, err := env.Client.Custom(ctx, "platform", "get", "a2a-agents", nil)
	if err != nil {
		t.Fatalf("list a2a agents: %v", err)
	}
	var listResp PaginatedResponse[RemoteAgentResponse]
	if unmarshalErr := json.Unmarshal(raw, &listResp); unmarshalErr != nil {
		t.Fatalf("unmarshal list: %v", unmarshalErr)
	}
	if len(listResp.Data) != 1 {
		t.Errorf("expected 1 A2A agent, got %d", len(listResp.Data))
	}

	raw, err = env.Client.Custom(ctx, "platform", "get", "a2a-agents/"+a2a.ID, nil)
	if err != nil {
		t.Fatalf("get a2a agent by ID: %v", err)
	}
	var getResp RemoteAgentResponse
	if unmarshalErr := json.Unmarshal(raw, &getResp); unmarshalErr != nil {
		t.Fatalf("unmarshal get: %v", unmarshalErr)
	}
	if getResp.Name != "ctrl-remote-agent" {
		t.Errorf("name = %q, want ctrl-remote-agent", getResp.Name)
	}
}
