// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmemory "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

type e2eAWEWrapper struct {
	exe *awe.Executor
}

func (w *e2eAWEWrapper) Init(_ context.Context, _ config.Config) error { return nil }
func (w *e2eAWEWrapper) Start(_ context.Context) error                 { return nil }
func (w *e2eAWEWrapper) Stop(ctx context.Context) error                { return w.exe.Stop(ctx) }
func (w *e2eAWEWrapper) Health() error                                 { return w.exe.Health() }

func (w *e2eAWEWrapper) ManagementEndpoints() []control.Endpoint {
	return w.exe.ManagementEndpoints()
}

func (w *e2eAWEWrapper) HandleManagement(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	return w.exe.HandleManagement(ctx, method, path, body, identity)
}

type e2eHarness struct {
	t         *testing.T
	ctx       context.Context
	brk       broker.Broker
	rt        *runtime.Runtime
	namespace string

	a2aNamespace string

	aweExe     *awe.Executor
	aweWrapper *e2eAWEWrapper
	awes       *aweStatusTracker

	agentSvc        *AgentService
	connectorSvc    *ConnectorService
	deploySvc       *DeploymentService
	deployRepo      *store.AgentDeploymentRepo
	deploymentRepo  *store.DeploymentRepo
	agentRepo       *store.AgentRepo
	connectorRepo   *store.ConnectorRepo
	toolPackageRepo *store.ToolPackageRepo

	workflowSvc  *WorkflowService
	wfDeploySvc  *WorkflowDeploymentService
	workflowRepo *store.WorkflowRepo
	wfDeployRepo *store.WorkflowDeploymentRepo

	gatewayRepo  *store.GatewayRepo
	gwDeployRepo *store.GatewayDeploymentRepo

	outboxRepo *store.OutboxRepo
	a2aRepo    *store.A2AAgentRepo
	a2aSvc     *A2AAgentService

	db *store.DB

	modelConfigRepo *store.ModelConfigRepo
	modelConfigSvc  *ModelConfigService

	yamlSvc *YAMLService

	llmClient     *mockllm.FIFOClient
	controlClient *control.Client

	gatewayID     string
	responseQueue broker.Queue
	statusQueue   broker.Queue
}

func setupE2E(t *testing.T, llmResponses []*llm.ChatResponse, opts ...e2eOption) *e2eHarness {
	t.Helper()
	ctx := context.Background()
	namespace := "e2e-test"
	logger := slog.Default()

	mockLLM := mockllm.NewFIFOClient(llmResponses)

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load feature flags: %v", err)
	}

	artSvc := &artifact.ScopedService{
		Inner:     artmemory.New(),
		Scope:     "namespace",
		Namespace: namespace,
	}

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	rt := runtime.NewRuntime(brk, config.FromMap(map[string]any{"namespace": namespace}),
		runtime.WithLogger(logger),
		runtime.WithDefaultLLM(mockLLM),
		runtime.WithSessions(session.NewMemoryStore()),
		runtime.WithArtifacts(artSvc),
		runtime.WithFeatureRegistry(flagRegistry),
	)

	exe := bootstrap.NewAWEExecutor(rt.Services())
	wrapper := &e2eAWEWrapper{exe: exe}
	rt.Register("awe", wrapper)

	db := testOpenDB(t)

	agentRepo := store.NewAgentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	deploymentRepo := store.NewDeploymentRepo(db)
	agentDeploymentRepo := store.NewAgentDeploymentRepo(db)
	skillRepo := store.NewSkillRepo(db)
	toolRepo := store.NewToolRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	wfDeploymentRepo := store.NewWorkflowDeploymentRepo(db)
	gatewayRepo := store.NewGatewayRepo(db)
	gwDeploymentRepo := store.NewGatewayDeploymentRepo(db)
	outboxRepo := store.NewOutboxRepo(db)
	a2aAgentRepo := store.NewA2AAgentRepo(db)
	modelConfigRepo := store.NewModelConfigRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)

	deps := &RouteDeps{
		SkillSvc: NewSkillService(db, skillRepo, logger),
		ToolSvc:  NewToolService(toolRepo, logger),
	}
	mp := &ManageablePlatform{deps: deps, logger: logger}
	stub := &stubPlatformComponent{mp: mp}
	rt.Register("platform", stub)

	ctrlSvc := control.NewService(rt, namespace)
	rt.Register("control", ctrlSvc)

	if err := rt.Init(ctx); err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	controlClient := control.NewClient(brk, namespace, control.WithTimeout(10*time.Second))

	yamlSvc := NewYAMLService(namespace, logger)
	deployerRegistry := NewDefaultDeployerRegistry(yamlSvc, connectorRepo, toolPackageRepo, skillRepo, a2aAgentRepo, namespace, logger)
	deployerRegistry.Register(AgentTypeLink, NewLinkAgentDeployer(store.NewLinkAgentRepo(db), yamlSvc, logger))
	deploySvc := NewDeploymentService(deploymentRepo, agentRepo, connectorRepo, toolPackageRepo, yamlSvc, controlClient, deployerRegistry, logger)
	agentSvc := NewAgentService(agentRepo, agentDeploymentRepo, connectorRepo, toolPackageRepo, nil, nil, logger)
	connectorSvc := NewConnectorService(connectorRepo, nil, namespace, logger)
	workflowSvc := NewWorkflowService(workflowRepo, wfDeploymentRepo, connectorRepo, toolPackageRepo, nil, namespace, logger)
	wfDeploySvc := NewWorkflowDeploymentService(wfDeploymentRepo, workflowRepo, connectorRepo, toolPackageRepo, agentRepo, newMemResourceStore(), controlClient, namespace, logger)
	platformAgents := platformAgentsForTest{db: db, brk: brk, namespace: namespace, yamlSvc: yamlSvc, deployers: deployerRegistry, exe: exe, agents: deploySvc, workflows: wfDeploySvc}
	for _, opt := range opts {
		opt(&platformAgents)
	}
	awes := followPlatformAgentsForTest(t, platformAgents)
	a2aSvc := NewA2AAgentService(a2aAgentRepo, agentDeploymentRepo, agentRepo, nil, controlClient, plainTestHTTPClient(), namespace, logger)
	modelConfigSvc := NewModelConfigService(modelConfigRepo, logger)
	modelConfigSvc.SetFeatureRegistry(decisionNodesOn())
	deps.ModelConfigSvc = modelConfigSvc

	a2aNamespace := namespace

	gatewayID := "e2e-gw"
	responseQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create response queue: %v", err)
	}
	if subErr := brk.Subscribe(responseQueue, a2a.GatewayResponseSubscription(a2aNamespace, gatewayID)); subErr != nil {
		t.Fatalf("subscribe response: %v", subErr)
	}

	statusQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create status queue: %v", err)
	}
	if subErr := brk.Subscribe(statusQueue, a2a.GatewayStatusSubscription(a2aNamespace, gatewayID)); subErr != nil {
		t.Fatalf("subscribe status: %v", subErr)
	}

	h := &e2eHarness{
		t:               t,
		awes:            awes,
		ctx:             ctx,
		brk:             brk,
		rt:              rt,
		namespace:       namespace,
		a2aNamespace:    a2aNamespace,
		aweExe:          exe,
		aweWrapper:      wrapper,
		agentSvc:        agentSvc,
		connectorSvc:    connectorSvc,
		deploySvc:       deploySvc,
		deployRepo:      agentDeploymentRepo,
		deploymentRepo:  deploymentRepo,
		agentRepo:       agentRepo,
		connectorRepo:   connectorRepo,
		toolPackageRepo: toolPackageRepo,
		workflowSvc:     workflowSvc,
		wfDeploySvc:     wfDeploySvc,
		workflowRepo:    workflowRepo,
		wfDeployRepo:    wfDeploymentRepo,
		gatewayRepo:     gatewayRepo,
		gwDeployRepo:    gwDeploymentRepo,
		outboxRepo:      outboxRepo,
		db:              db,
		a2aRepo:         a2aAgentRepo,
		a2aSvc:          a2aSvc,
		modelConfigRepo: modelConfigRepo,
		modelConfigSvc:  modelConfigSvc,
		yamlSvc:         yamlSvc,
		llmClient:       mockLLM,
		controlClient:   controlClient,
		gatewayID:       gatewayID,
		responseQueue:   responseQueue,
		statusQueue:     statusQueue,
	}

	t.Cleanup(func() {
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})

	return h
}

func (h *e2eHarness) createAgent(name, systemPrompt string) string {
	h.t.Helper()
	resp, err := h.agentSvc.Create(h.ctx, CreateAgentRequest{
		Name:         name,
		Description:  "E2E test agent",
		SystemPrompt: systemPrompt,
		Toolsets:     []string{},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create agent %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eHarness) createAgentWithConnectors(name, systemPrompt string, connectorIDs []string) string {
	h.t.Helper()
	resp, err := h.agentSvc.Create(h.ctx, CreateAgentRequest{
		Name:         name,
		Description:  "E2E test agent with connectors",
		SystemPrompt: systemPrompt,
		Toolsets:     []string{},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
		Connectors:   connectorIDs,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create agent %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eHarness) createSQLConnector(name, subtype string, values map[string]any) string {
	h.t.Helper()
	resp, err := h.connectorSvc.Create(h.ctx, CreateConnectorRequest{
		Name:        name,
		Description: "E2E test connector for fixture purposes",
		Type:        "sql",
		Subtype:     subtype,
		Values:      values,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create connector %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eHarness) deploy(agentID string) *AgentDeploymentResponse {
	h.t.Helper()
	resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: agentID,
		Action:  DeployActionDeploy,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("deploy agent %s: %v", agentID, err)
	}
	if resp.Status != DeployStatusSuccess {
		h.t.Fatalf("deploy status = %q, want %q (error: %s)", resp.Status, DeployStatusSuccess, resp.ErrorMessage)
	}
	return resp
}

func (h *e2eHarness) undeploy(agentID string) {
	h.t.Helper()
	if err := h.deploySvc.Undeploy(h.ctx, agentID, "e2e-user"); err != nil {
		h.t.Fatalf("undeploy agent %s: %v", agentID, err)
	}
}

func (h *e2eHarness) agentInstanceName(agentID string) string {
	h.t.Helper()
	return "agent_" + strings.ReplaceAll(agentID, "-", "_")
}

func workflowInstanceName(workflowID string) string {
	return "workflow_" + strings.ReplaceAll(workflowID, "-", "_")
}

func (h *e2eHarness) createWorkflow(name string, appConfig map[string]any) string {
	h.t.Helper()
	resp, err := h.workflowSvc.Create(h.ctx, CreateWorkflowRequest{
		Name:        name,
		Description: "E2E test workflow for testing",
		AppConfig:   appConfig,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create workflow %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eHarness) deployWorkflow(workflowID string) *WorkflowDeploymentResponse {
	h.t.Helper()
	resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: workflowID,
		Action:     DeployActionDeploy,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("deploy workflow %s: %v", workflowID, err)
	}
	if resp.Status != DeployStatusSuccess {
		h.t.Fatalf("workflow deploy status = %q, want %q (error: %s)", resp.Status, DeployStatusSuccess, resp.ErrorMessage)
	}
	return resp
}

func (h *e2eHarness) createA2AAgent(name, agentURL string) string {
	h.t.Helper()
	resp, err := h.a2aSvc.Create(h.ctx, CreateRemoteAgentRequest{
		Name:              name,
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          agentURL,
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "default-skill"}},
		AgentAuthConfig:   map[string]any{},
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create A2A agent %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eHarness) deployA2AAgent(a2aID string) *AgentDeploymentResponse {
	h.t.Helper()
	resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: a2aID,
		Action:  DeployActionDeploy,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("deploy A2A agent %s: %v", a2aID, err)
	}
	return resp
}

func (h *e2eHarness) undeployA2AAgent(a2aID string) {
	h.t.Helper()
	_, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{
		AgentID: a2aID,
		Action:  DeployActionUndeploy,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("undeploy A2A agent %s: %v", a2aID, err)
	}
}

func (h *e2eHarness) deleteAgent(agentID string) {
	h.t.Helper()
	if err := h.agentSvc.Delete(h.ctx, agentID); err != nil {
		h.t.Fatalf("delete agent %s: %v", agentID, err)
	}
}

func (h *e2eHarness) deleteA2AAgent(a2aID string) {
	h.t.Helper()
	if err := h.a2aSvc.Delete(h.ctx, a2aID); err != nil {
		h.t.Fatalf("delete A2A agent %s: %v", a2aID, err)
	}
}

func (h *e2eHarness) waitForInstance(instanceName string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := h.aweExe.GetInstance(instanceName)
		if err == nil && info != nil && info.Status == awe.StatusRunning {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("instance %q never reached running state after %v", instanceName, timeout)
}

func (h *e2eHarness) waitForInstanceGone(instanceName string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := h.aweExe.GetInstance(instanceName)
		if err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("instance %q still present after %v", instanceName, timeout)
}

type pendingOutboxEvent struct {
	ID         string  `db:"id"`
	EventType  string  `db:"event_type"`
	RetryCount int     `db:"retry_count"`
	LastError  *string `db:"last_error"`
	Dead       bool    `db:"dead"`
}

func (e pendingOutboxEvent) String() string {
	desc := fmt.Sprintf("%s(retries=%d", e.EventType, e.RetryCount)
	if e.Dead {
		desc += ", dead"
	}
	if e.LastError != nil && *e.LastError != "" {
		desc += ", lastError=" + *e.LastError
	}
	return desc + ")"
}

func (h *e2eHarness) unpublishedOutboxEvents() ([]pendingOutboxEvent, error) {
	return dbutil.QueryRows[pendingOutboxEvent](h.ctx, h.db,
		`SELECT id, event_type, retry_count, last_error, dead
		 FROM outbox_events WHERE published = 0 ORDER BY created_at ASC`)
}

func (h *e2eHarness) waitForOutboxDrained(timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		pending, err := h.unpublishedOutboxEvents()
		if err != nil {
			h.t.Fatalf("query unpublished outbox events: %v", err)
		}
		if len(pending) == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			h.t.Fatalf("outbox not drained after %v: %d event(s) unpublished: %v", timeout, len(pending), pending)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type collectedEvent struct {
	Type    string
	Payload json.RawMessage
}

func (h *e2eHarness) sendMessage(agentName, text string, timeout time.Duration) []collectedEvent {
	h.t.Helper()
	return h.sendMessageWithContext(agentName, text, "", timeout)
}

func (h *e2eHarness) sendMessageWithContext(agentName, text, contextID string, timeout time.Duration) []collectedEvent {
	h.t.Helper()

	taskID := fmt.Sprintf("e2e-task-%d", time.Now().UnixNano())

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-" + taskID,
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{{Kind: "text", Text: text}},
		ContextID: contextID,
	}

	req := a2a.NewRequest(taskID, "message/send", map[string]any{
		"message": msg,
	})

	payload, err := json.Marshal(req)
	if err != nil {
		h.t.Fatalf("marshal request: %v", err)
	}

	responseTopic := a2a.GatewayResponseTopic(h.a2aNamespace, h.gatewayID, taskID)
	statusTopic := a2a.GatewayStatusTopic(h.a2aNamespace, h.gatewayID, taskID)

	up := &a2a.UserProperties{
		Timestamp:      time.Now().UnixMilli(),
		ClientID:       h.gatewayID,
		UserID:         "e2e-user",
		ReplyTo:        responseTopic,
		A2AStatusTopic: statusTopic,
	}

	topic := a2a.AgentRequestTopic(h.a2aNamespace, agentName)
	if err := h.brk.Publish(h.ctx, topic, &broker.Message{
		Payload:        payload,
		UserProperties: a2a.EncodeUserProperties(up),
	}); err != nil {
		h.t.Fatalf("publish A2A request: %v", err)
	}

	return h.collectEvents(timeout)
}

func (h *e2eHarness) collectEvents(timeout time.Duration) []collectedEvent {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, timeout)
	defer cancel()

	var (
		mu     sync.Mutex
		events []collectedEvent
		done   = make(chan struct{})
	)

	go func() {
		msg, err := h.brk.Receive(ctx, h.responseQueue)
		if err != nil || msg == nil {
			return
		}
		msg.Ack()
		mu.Lock()
		events = append(events, collectedEvent{Type: "response", Payload: msg.Payload})
		mu.Unlock()
		close(done)
	}()

	go func() {
		for {
			msg, err := h.brk.Receive(ctx, h.statusQueue)
			if err != nil || msg == nil {
				return
			}
			msg.Ack()
			mu.Lock()
			events = append(events, collectedEvent{Type: "status", Payload: msg.Payload})
			mu.Unlock()
		}
	}()

	select {
	case <-done:
		time.Sleep(100 * time.Millisecond)
	case <-ctx.Done():
	}

	mu.Lock()
	defer mu.Unlock()
	return events
}

func (h *e2eHarness) assertFinalResponse(events []collectedEvent, containsText string) {
	h.t.Helper()
	for _, evt := range events {
		if evt.Type != "response" {
			continue
		}
		if strings.Contains(string(evt.Payload), containsText) {
			return
		}
	}
	h.t.Errorf("no response event containing %q found in %d events", containsText, len(events))
	for i, evt := range events {
		h.t.Logf("  event[%d] type=%s payload=%s", i, evt.Type, truncate(string(evt.Payload), 200))
	}
}

func (h *e2eHarness) assertHasResponseEvent(events []collectedEvent) {
	h.t.Helper()
	for _, evt := range events {
		if evt.Type == "response" {
			return
		}
	}
	h.t.Fatal("no response event found")
}

func (h *e2eHarness) latestDeployment(agentID string) *store.AgentDeployment {
	h.t.Helper()
	d, err := h.deployRepo.GetLatestByAgentID(h.ctx, agentID)
	if err != nil {
		h.t.Fatalf("get latest deployment: %v", err)
	}
	if d == nil {
		h.t.Fatalf("no deployment found for agent %s", agentID)
	}
	return d
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type strCapture struct {
	mu       sync.Mutex
	requests []remotetool.InvokeParams
}

func (c *strCapture) add(p remotetool.InvokeParams) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, p)
}

func (c *strCapture) Requests() []remotetool.InvokeParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]remotetool.InvokeParams, len(c.requests))
	copy(out, c.requests)
	return out
}

func (h *e2eHarness) createAgentWithToolsets(name, systemPrompt string, toolsets []string) string {
	h.t.Helper()
	resp, err := h.agentSvc.Create(h.ctx, CreateAgentRequest{
		Name:         name,
		Description:  "E2E test agent",
		SystemPrompt: systemPrompt,
		Toolsets:     toolsets,
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create agent %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eHarness) createToolPackage(name string, toolNames ...string) string {
	h.t.Helper()
	pkg := &store.ToolPackage{
		Name:            name,
		DiscoveryStatus: "pending",
		CreatedBy:       "e2e-user",
		UpdatedBy:       "e2e-user",
	}
	if err := h.toolPackageRepo.Create(h.ctx, pkg); err != nil {
		h.t.Fatalf("create tool package %q: %v", name, err)
	}
	for _, tn := range toolNames {
		if err := h.toolPackageRepo.UpsertToolFile(h.ctx, &store.ToolPackageFile{
			PackageID: pkg.ID,
			ToolName:  tn,
		}); err != nil {
			h.t.Fatalf("upsert tool file %q for %q: %v", tn, name, err)
		}
	}
	if err := h.toolPackageRepo.UpdateDiscoveryStatus(h.ctx, pkg.ID, "ready", nil); err != nil {
		h.t.Fatalf("flip package %q to ready: %v", name, err)
	}
	return pkg.ID
}

func (h *e2eHarness) createToolPackageWithSchema(packageName, toolName, configSchemaJSON string) string {
	h.t.Helper()
	pkg := &store.ToolPackage{
		Name:            packageName,
		DiscoveryStatus: "pending",
		CreatedBy:       "e2e-user",
		UpdatedBy:       "e2e-user",
	}
	if err := h.toolPackageRepo.Create(h.ctx, pkg); err != nil {
		h.t.Fatalf("create tool package %q: %v", packageName, err)
	}
	if err := h.toolPackageRepo.UpsertToolFile(h.ctx, &store.ToolPackageFile{
		PackageID:    pkg.ID,
		ToolName:     toolName,
		ConfigSchema: &configSchemaJSON,
	}); err != nil {
		h.t.Fatalf("upsert tool file %q for %q: %v", toolName, packageName, err)
	}
	if err := h.toolPackageRepo.UpdateDiscoveryStatus(h.ctx, pkg.ID, "ready", nil); err != nil {
		h.t.Fatalf("flip package %q to ready: %v", packageName, err)
	}
	return pkg.ID
}

func mustGetAgent(t *testing.T, repo *store.AgentRepo, agentID string) *store.Agent {
	t.Helper()
	agent, _, _, _, _, err := repo.GetByID(context.Background(), agentID)
	if err != nil {
		t.Fatalf("get agent %s: %v", agentID, err)
	}
	if agent == nil {
		t.Fatalf("agent %s not found", agentID)
	}
	return agent
}

func (h *e2eHarness) setToolsetConfig(agentID, packageID string, configMap map[string]any) {
	h.t.Helper()
	b, err := json.Marshal(configMap)
	if err != nil {
		h.t.Fatalf("marshal config map: %v", err)
	}
	vals := string(b)
	if err := h.toolPackageRepo.SetToolsetConfigsForAgent(h.ctx, agentID, []store.AgentToolsetConfig{
		{AgentID: agentID, PackageID: packageID, ConfigValues: &vals},
	}); err != nil {
		h.t.Fatalf("set toolset config for agent %s package %s: %v", agentID, packageID, err)
	}
}

func (h *e2eHarness) newRealToolsetService() *ToolsetService {
	logger := slog.New(slog.DiscardHandler)
	resourceStore := objectstore.NewStore(objectstore.NewMemoryClient())
	storage := newToolsetStorage(resourceStore, logger)
	return NewToolsetService(h.toolPackageRepo.DB(), h.toolPackageRepo, storage, h.outboxRepo, h.namespace, logger)
}

func (h *e2eHarness) patchToolsetConfig(packageID string, configMap map[string]any) {
	h.t.Helper()
	_, err := h.newRealToolsetService().PatchConfig(h.ctx, packageID, ToolsetConfigPatchRequest{
		Values: configMap,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("PatchConfig package=%s values=%v: %v", packageID, configMap, err)
	}
}

func (h *e2eHarness) simulateSTR(toolName string, results ...map[string]any) *strCapture {
	h.t.Helper()

	q, err := h.brk.CreateDirectSubscription()
	if err != nil {
		h.t.Fatalf("create STR queue: %v", err)
	}
	invokeTopic := remotetool.InvokeTopic(h.a2aNamespace, toolName)
	if err := h.brk.Subscribe(q, invokeTopic); err != nil {
		h.t.Fatalf("subscribe STR invoke: %v", err)
	}

	capture := &strCapture{}

	go func() {
		ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
		defer cancel()

		for _, result := range results {
			msg, err := h.brk.Receive(ctx, q)
			if err != nil || msg == nil {
				return
			}
			msg.Ack()

			replyTo := msg.UserProperties[remotetool.PropReplyTo]
			if replyTo == "" {
				continue
			}

			var envelope struct {
				ID     string          `json:"id"`
				Params json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal(msg.Payload, &envelope)

			var params remotetool.InvokeParams
			_ = json.Unmarshal(envelope.Params, &params)
			capture.add(params)

			invokeResult := remotetool.InvokeResult{
				ToolResult:      result,
				ExecutionTimeMs: 1,
			}
			resp := a2a.NewResponse(envelope.ID, invokeResult)
			payload, _ := json.Marshal(resp)

			_ = h.brk.Publish(ctx, replyTo, &broker.Message{
				Payload:       payload,
				CorrelationID: envelope.ID,
			})
		}
	}()

	return capture
}

type e2eGWEHarness struct {
	*e2eHarness
	gweExe       *entrypoint.GatewayExecutor
	gweProxy     *entrypoint.GatewayProxy
	proxyAddr    string
	gatewaySvc   *GatewayService
	gwDeploySvc  *GatewayDeploymentService
	gatewayRepo  *store.GatewayRepo
	gwDeployRepo *store.GatewayDeploymentRepo
}

func setupE2EWithGWE(t *testing.T, llmResponses []*llm.ChatResponse) *e2eGWEHarness {
	t.Helper()
	ctx := context.Background()
	namespace := "e2e-test"
	logger := slog.Default()

	mockLLM := mockllm.NewFIFOClient(llmResponses)

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load feature flags: %v", err)
	}

	artSvc := &artifact.ScopedService{
		Inner:     artmemory.New(),
		Scope:     "namespace",
		Namespace: namespace,
	}

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	rt := runtime.NewRuntime(brk, config.FromMap(map[string]any{"namespace": namespace}),
		runtime.WithLogger(logger),
		runtime.WithDefaultLLM(mockLLM),
		runtime.WithSessions(session.NewMemoryStore()),
		runtime.WithArtifacts(artSvc),
		runtime.WithFeatureRegistry(flagRegistry),
	)

	exe := bootstrap.NewAWEExecutor(rt.Services())
	aweWrapper := &e2eAWEWrapper{exe: exe}
	rt.Register("awe", aweWrapper)

	gweExe := entrypoint.NewGatewayExecutor(rt.Services(), bootstrap.DefaultGatewayFactory)
	gweExe.AllowRuntimeManagement = true
	rt.Register("gateway", gweExe)

	gweProxy := entrypoint.NewGatewayProxy(":0", logger)
	gweExe.OnGatewayReady = func(name string, handler http.Handler) {
		gweProxy.RegisterHandler(name, handler)
	}
	gweExe.OnGatewayRemoved = func(name string) {
		gweProxy.UnregisterHandler(name)
	}

	db := testOpenDB(t)

	agentRepo := store.NewAgentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	deploymentRepo := store.NewDeploymentRepo(db)
	agentDeploymentRepo := store.NewAgentDeploymentRepo(db)
	skillRepo := store.NewSkillRepo(db)
	toolRepo := store.NewToolRepo(db)
	gatewayRepo := store.NewGatewayRepo(db)
	gwDeploymentRepo := store.NewGatewayDeploymentRepo(db)

	deps := &RouteDeps{
		SkillSvc: NewSkillService(db, skillRepo, logger),
		ToolSvc:  NewToolService(toolRepo, logger),
	}
	mp := &ManageablePlatform{deps: deps, logger: logger}
	stub := &stubPlatformComponent{mp: mp}
	rt.Register("platform", stub)

	ctrlSvc := control.NewService(rt, namespace)
	rt.Register("control", ctrlSvc)
	rt.Register("proxy", gweProxy)

	if err := rt.Init(ctx); err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	controlClient := control.NewClient(brk, namespace, control.WithTimeout(30*time.Second))

	yamlSvc := NewYAMLService(namespace, logger)
	deployerRegistry := NewDefaultDeployerRegistry(yamlSvc, connectorRepo, nil, skillRepo, nil, namespace, logger)
	deploySvc := NewDeploymentService(deploymentRepo, agentRepo, connectorRepo, nil, yamlSvc, controlClient, deployerRegistry, logger)
	agentSvc := NewAgentService(agentRepo, agentDeploymentRepo, connectorRepo, nil, nil, nil, logger)
	connectorSvc := NewConnectorService(connectorRepo, nil, namespace, logger)
	gatewaySvc := NewGatewayService(gatewayRepo, gwDeploymentRepo, nil, controlClient, flagRegistry, logger)
	gwDeploySvc := NewGatewayDeploymentService(gwDeploymentRepo, gatewayRepo, yamlSvc, controlClient, logger)
	gwDeploySvc.gwes = newGWEStatusTracker(brk, namespace)
	followCtx, stopFollowing := context.WithCancel(context.Background())
	t.Cleanup(stopFollowing)
	if err := gwDeploySvc.gwes.follow(followCtx); err != nil {
		t.Fatalf("follow GWE status: %v", err)
	}

	a2aNamespace := namespace

	gatewayID := "e2e-gw"
	responseQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create response queue: %v", err)
	}
	if subErr := brk.Subscribe(responseQueue, a2a.GatewayResponseSubscription(a2aNamespace, gatewayID)); subErr != nil {
		t.Fatalf("subscribe response: %v", subErr)
	}

	statusQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create status queue: %v", err)
	}
	if subErr := brk.Subscribe(statusQueue, a2a.GatewayStatusSubscription(a2aNamespace, gatewayID)); subErr != nil {
		t.Fatalf("subscribe status: %v", subErr)
	}

	workflowRepo := store.NewWorkflowRepo(db)
	wfDeploymentRepo := store.NewWorkflowDeploymentRepo(db)
	workflowSvc := NewWorkflowService(workflowRepo, wfDeploymentRepo, connectorRepo, nil, nil, namespace, logger)
	wfDeploySvc := NewWorkflowDeploymentService(wfDeploymentRepo, workflowRepo, connectorRepo, nil, agentRepo, newMemResourceStore(), controlClient, namespace, logger)
	awes := followPlatformAgentsForTest(t, platformAgentsForTest{db: db, brk: brk, namespace: namespace, yamlSvc: yamlSvc, deployers: deployerRegistry, exe: exe, agents: deploySvc, workflows: wfDeploySvc})

	base := &e2eHarness{
		t:             t,
		awes:          awes,
		ctx:           ctx,
		brk:           brk,
		rt:            rt,
		namespace:     namespace,
		a2aNamespace:  a2aNamespace,
		aweExe:        exe,
		agentSvc:      agentSvc,
		connectorSvc:  connectorSvc,
		deploySvc:     deploySvc,
		deployRepo:    agentDeploymentRepo,
		agentRepo:     agentRepo,
		connectorRepo: connectorRepo,
		workflowSvc:   workflowSvc,
		wfDeploySvc:   wfDeploySvc,
		workflowRepo:  workflowRepo,
		wfDeployRepo:  wfDeploymentRepo,
		gatewayRepo:   gatewayRepo,
		gwDeployRepo:  gwDeploymentRepo,
		db:            db,
		yamlSvc:       yamlSvc,
		llmClient:     mockLLM,
		controlClient: controlClient,
		gatewayID:     gatewayID,
		responseQueue: responseQueue,
		statusQueue:   statusQueue,
	}

	t.Cleanup(func() {
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})

	return &e2eGWEHarness{
		e2eHarness:   base,
		gweExe:       gweExe,
		gweProxy:     gweProxy,
		proxyAddr:    gweProxy.Addr(),
		gatewaySvc:   gatewaySvc,
		gwDeploySvc:  gwDeploySvc,
		gatewayRepo:  gatewayRepo,
		gwDeployRepo: gwDeploymentRepo,
	}
}

func (h *e2eGWEHarness) createGateway(name string) string {
	h.t.Helper()
	resp, err := h.gatewaySvc.Create(h.ctx, CreateGatewayRequest{
		Name:        name,
		Description: "E2E test gateway for GWE testing",
		Type:        "httpsse",
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create gateway %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eGWEHarness) gatewaySlug(gatewayID string) string {
	h.t.Helper()
	gw, err := h.gatewayRepo.GetByID(h.ctx, gatewayID)
	if err != nil {
		h.t.Fatalf("get gateway %q for slug: %v", gatewayID, err)
	}
	if gw == nil {
		h.t.Fatalf("gateway %q not found", gatewayID)
	}
	if gw.Slug != "" {
		return gw.Slug
	}
	return gw.ID
}

func (h *e2eGWEHarness) deployGateway(gatewayID string) {
	h.t.Helper()
	_, err := h.gwDeploySvc.Deploy(h.ctx, CreateGatewayDeploymentRequest{
		GatewayID: gatewayID,
		Action:    DeployActionDeploy,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("deploy gateway %s: %v", gatewayID, err)
	}
}

func (h *e2eGWEHarness) waitForGWEInstance(name string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := h.gweExe.GetInstance(name)
		if err == nil && info != nil && info.Status == entrypoint.StatusRunning {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("GWE instance %q not running after %v", name, timeout)
}

func (h *e2eGWEHarness) httpPostMessage(gwSlug, agentName, text string) string {
	h.t.Helper()

	reqID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"id": %q,
		"method": "message/stream",
		"params": {
			"message": {
				"kind": "message",
				"role": "user",
				"parts": [{"kind": "text", "text": %q}],
				"metadata": {"agent_name": %q}
			}
		}
	}`, reqID, text, agentName)

	url := fmt.Sprintf("http://%s/gw/%s/api/v1/message:stream", h.proxyAddr, gwSlug)
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("HTTP POST to gateway: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("HTTP POST status %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		h.t.Fatalf("decode JSON-RPC response: %v", err)
	}
	if rpcResp.Error != nil {
		h.t.Fatalf("JSON-RPC error: %s", rpcResp.Error.Message)
	}
	if rpcResp.Result.ID == "" {
		h.t.Fatal("empty task ID in response")
	}
	return rpcResp.Result.ID
}

type sseEvent struct {
	ID    string
	Event string
	Data  string
}

func (h *e2eGWEHarness) httpSubscribeSSE(gwSlug, taskID string, timeout time.Duration) []sseEvent {
	h.t.Helper()

	url := fmt.Sprintf("http://%s/gw/%s/api/v1/sse/subscribe/%s", h.proxyAddr, gwSlug, taskID)
	ctx, cancel := context.WithTimeout(h.ctx, timeout)
	defer cancel()

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if reqErr != nil {
		h.t.Fatalf("create SSE request: %v", reqErr)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("SSE subscribe: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("SSE subscribe returned %d: %s", resp.StatusCode, string(body))
	}

	var events []sseEvent
	var cur sseEvent
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				idx := strings.Index(string(buf), "\n\n")
				if idx < 0 {
					break
				}
				frame := string(buf[:idx])
				buf = buf[idx+2:]
				for _, line := range strings.Split(frame, "\n") {
					if strings.HasPrefix(line, "id: ") {
						cur.ID = strings.TrimPrefix(line, "id: ")
					} else if strings.HasPrefix(line, "event: ") {
						cur.Event = strings.TrimPrefix(line, "event: ")
					} else if strings.HasPrefix(line, "data: ") {
						cur.Data = strings.TrimPrefix(line, "data: ")
					}
				}
				if cur.Event != "" {
					events = append(events, cur)
					if cur.Event == "final_response" || cur.Event == "error" {
						return events
					}
				}
				cur = sseEvent{}
			}
		}
		if readErr != nil {
			break
		}
	}
	return events
}

func (h *e2eGWEHarness) publishAgentCardToGateway(agentInstanceName string) {
	h.t.Helper()
	card := a2a.AgentCard{
		Name:               agentInstanceName,
		Description:        "E2E test agent",
		URL:                "solace:" + a2a.AgentRequestTopic(h.a2aNamespace, agentInstanceName),
		Version:            "1.0.0",
		ProtocolVersion:    "0.3.0",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             []a2a.AgentSkill{},
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
		},
	}
	data, err := json.Marshal(card)
	if err != nil {
		h.t.Fatalf("marshal agent card: %v", err)
	}
	topic := a2a.DiscoveryAgentCardsTopic(h.a2aNamespace)
	if err := h.brk.Publish(h.ctx, topic, &broker.Message{Payload: data}); err != nil {
		h.t.Fatalf("publish agent card: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
}

var (
	e2eGoTestToolPath string
	e2eGoTestToolOnce sync.Once
)

func getE2EGoTestTool(t *testing.T) string {
	t.Helper()
	e2eGoTestToolOnce.Do(func() {
		pkgDir, err := os.Getwd()
		if err != nil {
			t.Fatalf("get the package directory: %v", err)
		}
		srcDir := filepath.Join(pkgDir, "..", "..", "str", "testdata", "go-test-tool")

		tmpDir, err := os.MkdirTemp("", "e2e-go-test-tool-*") //nolint:usetesting // sync.Once cache shared by every test; t.TempDir would delete it after the first
		if err != nil {
			t.Fatalf("create temp dir for go-test-tool: %v", err)
		}
		binPath := filepath.Join(tmpDir, "e2e-go-test-tool")

		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Dir = srcDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			t.Fatalf("build go-test-tool: %v\n%s", err, out)
		}
		e2eGoTestToolPath = binPath
	})
	if e2eGoTestToolPath == "" {
		t.Fatal("go-test-tool binary not built")
	}
	return e2eGoTestToolPath
}

func generateSTRManifest(t *testing.T, toolBinary string) string {
	t.Helper()
	dir := t.TempDir()
	content := fmt.Sprintf("version: 1\ntools:\n  _discovery_0:\n    executable: %s\n", toolBinary)
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write STR manifest: %v", err)
	}
	return path
}

type e2eSTRHarness struct {
	*e2eHarness
	strWorker *str.Worker
}

func setupE2EWithSTR(t *testing.T, llmResponses []*llm.ChatResponse) *e2eSTRHarness {
	t.Helper()
	base := setupE2E(t, llmResponses)

	toolBin := getE2EGoTestTool(t)
	manifestPath := generateSTRManifest(t, toolBin)

	workerCfg := str.WorkerConfig{
		WorkerID:      "e2e-str",
		Namespace:     base.a2aNamespace,
		ManifestPaths: []string{manifestPath},
		Sandbox: str.SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 8,
		},
	}
	worker := str.NewWorker(base.rt.Services(), workerCfg)
	if err := worker.Init(base.ctx, nil); err != nil {
		t.Fatalf("str worker init: %v", err)
	}
	if err := worker.Start(base.ctx); err != nil {
		t.Fatalf("str worker start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	t.Cleanup(func() {
		_ = worker.Stop(context.Background())
	})

	return &e2eSTRHarness{e2eHarness: base, strWorker: worker}
}

type a2aServerOpt func(*a2aTestServer)

type a2aTestServer struct {
	*httptest.Server

	card         *a2a.AgentCard
	responseText string

	cardDisabled  bool
	cardAuthToken string
	taskAuthKey   string

	mu           sync.Mutex
	taskRequests []json.RawMessage
}

func withCardDisabled() a2aServerOpt {
	return func(s *a2aTestServer) { s.cardDisabled = true }
}

func withCardAuth(bearerToken string) a2aServerOpt {
	return func(s *a2aTestServer) { s.cardAuthToken = bearerToken }
}

func withTaskAuth(apiKey string) a2aServerOpt {
	return func(s *a2aTestServer) { s.taskAuthKey = apiKey }
}

func withA2AResponseText(text string) a2aServerOpt {
	return func(s *a2aTestServer) { s.responseText = text }
}

func newA2ATestServer(t *testing.T, opts ...a2aServerOpt) *a2aTestServer {
	t.Helper()

	s := &a2aTestServer{
		responseText: "Hello from A2A test agent",
		card: &a2a.AgentCard{
			Name:               "test-a2a-agent",
			Description:        "A2A protocol-compliant test agent",
			Version:            "1.0.0",
			ProtocolVersion:    "0.3.0",
			DefaultInputModes:  []string{"text"},
			DefaultOutputModes: []string{"text"},
			Skills:             []a2a.AgentSkill{{ID: "echo", Name: "echo", Description: "echoes input"}},
			Capabilities:       a2a.AgentCapabilities{Streaming: false},
		},
	}
	for _, opt := range opts {
		opt(s)
	}

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "//") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
			return
		}

		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "agent-card.json"):
			s.serveCard(w, r)
		case r.Method == http.MethodPost:
			s.serveTask(w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	s.card.URL = s.URL
	t.Cleanup(s.Close)
	return s
}

func (s *a2aTestServer) serveCard(w http.ResponseWriter, r *http.Request) {
	if s.cardDisabled {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
		return
	}
	if s.cardAuthToken != "" {
		if r.Header.Get("Authorization") != "Bearer "+s.cardAuthToken {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"Unauthorized"}`))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.card)
}

func (s *a2aTestServer) serveTask(w http.ResponseWriter, r *http.Request) {
	if s.taskAuthKey != "" {
		if r.Header.Get("X-API-Key") != s.taskAuthKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"Unauthorized"}`))
			return
		}
	}

	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.taskRequests = append(s.taskRequests, json.RawMessage(body))
	s.mu.Unlock()

	var req a2a.Request
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse error"}}`))
		return
	}

	if params, ok := req.Params.(map[string]any); ok {
		if msg, ok := params["message"].(map[string]any); ok {
			if taskID, has := msg["taskId"]; has && taskID != nil && taskID != "" {
				resp := a2a.Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &a2a.Error{
						Code:    -32001,
						Message: fmt.Sprintf("Task %v was specified but does not exist", taskID),
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
		}
	}

	resp := a2a.Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					Kind:      "message",
					MessageID: "resp-1",
					Role:      a2a.RoleAgent,
					Parts:     []a2a.Part{{Kind: "text", Text: s.responseText}},
				},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *a2aTestServer) getTaskRequests() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]json.RawMessage, len(s.taskRequests))
	copy(out, s.taskRequests)
	return out
}

func (h *e2eHarness) createAgentWithModelProvider(name, systemPrompt, modelProvider string) string {
	h.t.Helper()
	resp, err := h.agentSvc.Create(h.ctx, CreateAgentRequest{
		Name:          name,
		Description:   "E2E test agent with model provider",
		SystemPrompt:  systemPrompt,
		Toolsets:      []string{},
		InputModes:    []string{"text"},
		OutputModes:   []string{"text"},
		ModelProvider: []string{modelProvider},
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("create agent %q: %v", name, err)
	}
	return resp.ID
}

func (h *e2eSTRHarness) createConnectorForSTRTool(connectorName, toolName string) string {
	h.t.Helper()
	return h.createSQLConnector(connectorName, "postgres", map[string]any{
		"tool_name": toolName,
		"hostname":  "localhost",
		"port":      "5432",
		"database":  "testdb",
		"username":  "testuser",
		"password":  "testpass",
	})
}
