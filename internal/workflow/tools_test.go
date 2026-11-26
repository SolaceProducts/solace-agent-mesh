// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/connector"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

type fakeTool struct {
	name         string
	scopes       []string
	resultSchema *tool.JSONSchema
	lastToolCtx  *tool.Context
	lastArgs     map[string]any
	returnResult *tool.Result
}

func (t *fakeTool) ResultSchema() *tool.JSONSchema { return t.resultSchema }

func (t *fakeTool) Name() string                 { return t.name }
func (t *fakeTool) Description() string          { return "fake tool for tests" }
func (t *fakeTool) Instructions() string         { return "" }
func (t *fakeTool) IsLongRunning() bool          { return false }
func (t *fakeTool) RequiredScopes() []string     { return t.scopes }
func (t *fakeTool) RawStringArgs() []string      { return nil }
func (t *fakeTool) ContextParam() string         { return "" }
func (t *fakeTool) Parameters() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (t *fakeTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return nil
}
func (t *fakeTool) Execute(_ context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	t.lastArgs = args
	t.lastToolCtx = toolCtx
	if t.returnResult != nil {
		return t.returnResult, nil
	}
	return tool.OK("ok", tool.WithData(map[string]any{"value": "yes"})), nil
}

type fakeConnector struct {
	name string
	tool tool.BuiltinTool
}

func (c *fakeConnector) Name() string                  { return c.name }
func (c *fakeConnector) Type() string                  { return "fake" }
func (c *fakeConnector) Tool() tool.BuiltinTool        { return c.tool }
func (c *fakeConnector) Start(_ context.Context) error { return nil }
func (c *fakeConnector) Stop(_ context.Context) error  { return nil }
func (c *fakeConnector) Health() error                 { return nil }

func newTestWorkflowInstance(cfg *WorkflowConfig, svc runtime.Services) *WorkflowInstance {
	return &WorkflowInstance{
		cfg:            cfg,
		svc:            svc,
		logger:         slog.Default(),
		executions:     make(map[string]*WorkflowExecution),
		executionsByID: make(map[string]*WorkflowExecution),
		subTaskIndex:   make(map[string]string),
	}
}

func registerTestConnectorType(t *testing.T, name string, factory connector.Factory) {
	t.Helper()
	if err := connector.DefaultRegistry().Register(name, factory); err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
	t.Cleanup(func() { connector.DefaultRegistry().Deregister(name) })
}

func TestInitToolsAndConnectors_BuiltinTool(t *testing.T) {
	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Tools: []any{
			map[string]any{
				"tool_type": "builtin",
				"tool_name": "get_current_time",
			},
		},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if w.tools == nil {
		t.Fatal("tools set is nil")
	}
	if w.tools.GetTool("get_current_time") == nil {
		t.Fatal("get_current_time should be registered")
	}
}

func TestInitToolsAndConnectors_AnEffectOverrideMakesABuiltinSideEffecting(t *testing.T) {
	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Tools: []any{
			map[string]any{"tool_type": "builtin", "tool_name": "get_current_time", tool.EffectConfigKey: "side_effecting"},
			map[string]any{"tool_type": "builtin", "tool_name": "artifact_grep"},
		},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	got, _ := w.tools.EffectClassFor("get_current_time")
	if want := (tool.EffectClass{Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator}); got != want {
		t.Fatalf("get_current_time: EffectClassFor = %+v, want %+v", got, want)
	}
	got, _ = w.tools.EffectClassFor("artifact_grep")
	if want := tool.DeclaredEffect(tool.EffectReadOnly); got != want {
		t.Fatalf("artifact_grep: EffectClassFor = %+v, want its declaration %+v", got, want)
	}
}

func TestInitToolsAndConnectors_AnEffectOverrideCannotMakeAToolReadOnly(t *testing.T) {
	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Tools: []any{
			map[string]any{"tool_type": "builtin", "tool_name": "notify_user", tool.EffectConfigKey: "read_only"},
		},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	err := w.initToolsAndConnectors(context.Background(), w.svc)
	if err == nil || !strings.Contains(err.Error(), `may only be "side_effecting"`) {
		t.Fatalf("init error = %v, want the read_only override refused", err)
	}
}

func TestInitToolsAndConnectors_OpenAPIObjStoreSpec(t *testing.T) {
	specYAML := `openapi: 3.0.0
info: {title: Pets, version: "1.0"}
servers: [{url: "http://127.0.0.1:8090"}]
paths:
  /pets:
    get:
      operationId: listPets
      responses: {"200": {description: ok}}
`
	store, err := objectstore.NewResourceStore(map[string]any{"type": "memory"}, objectstore.ResourceStoreOptions{
		YAMLKey: "resource_store", Purpose: "test", Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}
	key := "ns/connectors/c1/pets.openapi.yaml"
	if err := store.Put(t.Context(), key, strings.NewReader(specYAML)); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Tools: []any{
			map[string]any{
				"tool_type":         "openapi",
				"specification_url": "objstore://" + key,
				"base_url":          "http://127.0.0.1:8090",
			},
		},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}, specStore: store})
	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if w.tools.GetTool("list_pets") == nil {
		t.Fatal("list_pets should be registered from the objstore:// spec via the services spec store")
	}
}

func TestInitToolsAndConnectors_NameCollision(t *testing.T) {
	uniqueType := "fake-collision-test"
	registerTestConnectorType(t, uniqueType, func(_ context.Context, cfg connector.Config, _ runtime.Services) (connector.Connector, error) {
		return &fakeConnector{
			name: cfg.Name,
			tool: &fakeTool{name: "get_current_time"},
		}, nil
	})

	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Connectors: []any{
			map[string]any{
				"name": "c1",
				"type": uniqueType,
			},
		},
		Tools: []any{
			map[string]any{
				"tool_type": "builtin",
				"tool_name": "get_current_time",
			},
		},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	err := w.initToolsAndConnectors(context.Background(), w.svc)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !contains(err.Error(), "already registered") {
		t.Fatalf("error %q should mention already registered", err.Error())
	}
}

func TestInitToolsAndConnectors_ConnectorTool(t *testing.T) {
	uniqueType := "fake-connector-test"
	registerTestConnectorType(t, uniqueType, func(_ context.Context, cfg connector.Config, _ runtime.Services) (connector.Connector, error) {
		return &fakeConnector{
			name: cfg.Name,
			tool: &fakeTool{name: "fake_conn_tool_" + cfg.Name},
		}, nil
	})

	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Connectors: []any{
			map[string]any{
				"name": "c1",
				"type": uniqueType,
			},
		},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if w.tools.GetTool("fake_conn_tool_c1") == nil {
		t.Fatal("connector tool should be registered")
	}
	if len(w.subComponents) == 0 {
		t.Fatal("connector should be added as a sub-component")
	}
}

func TestToolNodeSchemas_ExposesToolParameters(t *testing.T) {
	uniqueType := "fake-schema-test"
	registerTestConnectorType(t, uniqueType, func(_ context.Context, cfg connector.Config, _ runtime.Services) (connector.Connector, error) {
		return &fakeConnector{
			name: cfg.Name,
			tool: &fakeTool{name: "schema_tool"},
		}, nil
	})

	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Connectors: []any{
			map[string]any{
				"name": "c1",
				"type": uniqueType,
			},
		},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	in, out := w.toolNodeSchemas(&NodeConfig{Type: "tool", ToolName: "schema_tool"})
	if in == nil {
		t.Fatal("expected input schema from tool Parameters()")
	}
	if out != nil {
		t.Errorf("output schema = %v, want nil (the tool declares no ResultSchema)", out)
	}
}

func newSchemaToolWorkflow(t *testing.T, resultSchema *tool.JSONSchema) *WorkflowInstance {
	t.Helper()
	uniqueType := "fake-result-schema-" + t.Name()
	registerTestConnectorType(t, uniqueType, func(_ context.Context, cfg connector.Config, _ runtime.Services) (connector.Connector, error) {
		return &fakeConnector{
			name: cfg.Name,
			tool: &fakeTool{name: "schema_tool", resultSchema: resultSchema},
		}, nil
	})

	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Connectors: []any{map[string]any{"name": "c1", "type": uniqueType}},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	return w
}

func TestToolNodeSchemas_ExposesToolResultSchema(t *testing.T) {
	w := newSchemaToolWorkflow(t, &tool.JSONSchema{
		Type:       "object",
		Properties: map[string]any{"filename": map[string]any{"type": "string"}},
	})

	_, out := w.toolNodeSchemas(&NodeConfig{Type: "tool", ToolName: "schema_tool"})
	if out == nil {
		t.Fatal("output schema = nil, want the tool's declared ResultSchema")
	}
	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("output schema properties = %T, want map[string]any", out["properties"])
	}
	if _, ok := props["filename"]; !ok {
		t.Errorf("output schema properties = %v, want a \"filename\" property", props)
	}
}

func TestToolNodeSchemas_InputOverrideStillYieldsResultSchema(t *testing.T) {
	w := newSchemaToolWorkflow(t, &tool.JSONSchema{
		Type:       "object",
		Properties: map[string]any{"filename": map[string]any{"type": "string"}},
	})

	override := map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}}
	in, out := w.toolNodeSchemas(&NodeConfig{
		Type:                "tool",
		ToolName:            "schema_tool",
		InputSchemaOverride: override,
	})

	if in == nil || in["properties"] == nil {
		t.Fatalf("input schema = %v, want the override to win", in)
	}
	if out == nil {
		t.Fatal("output schema = nil, want the tool's ResultSchema despite the input override")
	}
}

func TestToolNodeSchemas_OverridesWinOverResultSchema(t *testing.T) {
	w := newSchemaToolWorkflow(t, &tool.JSONSchema{
		Type:       "object",
		Properties: map[string]any{"filename": map[string]any{"type": "string"}},
	})

	override := map[string]any{"type": "object", "properties": map[string]any{"custom": map[string]any{"type": "string"}}}
	_, out := w.toolNodeSchemas(&NodeConfig{
		Type:                 "tool",
		ToolName:             "schema_tool",
		OutputSchemaOverride: override,
	})

	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("output schema properties = %T, want map[string]any", out["properties"])
	}
	if _, ok := props["custom"]; !ok {
		t.Errorf("output schema properties = %v, want the override's \"custom\" property", props)
	}
}

type scopeAuthorizer struct{}

func (scopeAuthorizer) AuthorizeTool(_ context.Context, id *auth.Identity, _ string, required []string) error {
	if id == nil {
		return nil
	}
	have := map[string]bool{}
	for _, s := range id.Scopes {
		have[s] = true
	}
	for _, want := range required {
		if !have[want] {
			return errors.New("missing scope " + want)
		}
	}
	return nil
}

func (scopeAuthorizer) FilterTools(_ context.Context, id *auth.Identity, tools []auth.ToolInfo) []auth.ToolInfo {
	if id == nil {
		return tools
	}
	have := map[string]bool{}
	for _, s := range id.Scopes {
		have[s] = true
	}
	var out []auth.ToolInfo
	for _, ti := range tools {
		allowed := true
		for _, s := range ti.RequiredScopes {
			if !have[s] {
				allowed = false
				break
			}
		}
		if allowed {
			out = append(out, ti)
		}
	}
	return out
}

type authorizedStubServices struct {
	stubServices
	authz auth.ToolAuthorizer
}

func (s *authorizedStubServices) ToolAuthorizer() auth.ToolAuthorizer {
	if s.authz != nil {
		return s.authz
	}
	return auth.NoopToolAuthorizer{}
}

func TestDispatchToolNode_AuthzDenied(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "wf",
		Namespace: "ns",
		Nodes: []*NodeConfig{
			{ID: "t", Type: "tool", ToolName: "scoped_tool"},
		},
		OutputMapping: map[string]any{"x": "{{t.output}}"},
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	svc := &authorizedStubServices{
		stubServices: stubServices{brk: stubBroker{}},
		authz:        scopeAuthorizer{},
	}
	w := newTestWorkflowInstance(cfg, svc)
	w.dag = dag

	w.tools = tool.NewSet()
	w.tools.SetAuthorizer(svc.ToolAuthorizer())
	ft := &fakeTool{name: "scoped_tool", scopes: []string{"need:scope"}}
	if err := w.tools.Register(ft); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	identity := &auth.Identity{UserID: "u1", Scopes: []string{"other:scope"}}

	state := NewExecutionState("wf", "exec")
	execCtx := NewExecutionContext(context.Background(), "task-1", "ctx-1", nil, a2a.UserProperties{}, false)
	execCtx.Identity = identity
	e := &DAGExecutor{
		cfg:      cfg,
		dag:      dag,
		state:    state,
		execCtx:  execCtx,
		instance: w,
		logger:   slog.Default(),
	}
	w.timerManager = NewInProcessTimerManager(slog.Default(), func(execID string, event Event) {
		if execID == state.ExecutionID {
			_ = e.Advance(context.Background(), event)
		}
	})
	go w.timerManager.Start(execCtx.Ctx)
	defer execCtx.Cancel()

	if err := e.Advance(context.Background(), &EventStart{
		TaskID: "task-1", ContextID: "ctx-1", Input: map[string]any{},
		UserProps: a2a.UserProperties{},
	}); err != nil {
		t.Fatalf("advance start: %v", err)
	}

	waitForFinalize(t, e, state)
	e.mu.Lock()
	errState := state.ErrorState
	finalized := state.Finalized
	e.mu.Unlock()
	if !finalized {
		t.Fatal("workflow should finalize after authz failure")
	}
	if errState == nil {
		t.Fatalf("expected ErrorState set after authz denial; outputs: %v", state.NodeOutputs)
	}
	if !contains(errState.ErrorMessage, "access denied") {
		t.Fatalf("expected access denied in error, got %q", errState.ErrorMessage)
	}
}

func TestDispatchToolNode_IdentityForwarded(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "wf",
		Namespace: "ns",
		Nodes: []*NodeConfig{
			{ID: "t", Type: "tool", ToolName: "echo"},
		},
		OutputMapping: map[string]any{"x": "{{t.output.value}}"},
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	w.dag = dag
	w.tools = tool.NewSet()
	ft := &fakeTool{name: "echo"}
	if err := w.tools.Register(ft); err != nil {
		t.Fatalf("register: %v", err)
	}

	identity := &auth.Identity{UserID: "u1", AuthToken: "auth-xyz"}
	props := a2a.UserProperties{
		UserID:    "u1",
		TraceID:   "trace-123",
		AuthToken: "auth-xyz",
	}

	state := NewExecutionState("wf", "exec")
	execCtx := NewExecutionContext(context.Background(), "task-2", "ctx-2", nil, props, false)
	execCtx.Identity = identity
	e := &DAGExecutor{
		cfg:      cfg,
		dag:      dag,
		state:    state,
		execCtx:  execCtx,
		instance: w,
		logger:   slog.Default(),
	}
	w.timerManager = NewInProcessTimerManager(slog.Default(), func(execID string, event Event) {
		if execID == state.ExecutionID {
			_ = e.Advance(context.Background(), event)
		}
	})
	go w.timerManager.Start(execCtx.Ctx)
	defer execCtx.Cancel()

	if err := e.Advance(context.Background(), &EventStart{
		TaskID: "task-2", ContextID: "ctx-2", Input: map[string]any{"k": "v"},
		UserProps: props,
	}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	waitForFinalize(t, e, state)
	if ft.lastToolCtx == nil {
		t.Fatal("tool never ran")
	}
	if ft.lastToolCtx.Identity == nil || ft.lastToolCtx.Identity.UserID != "u1" {
		t.Fatalf("Identity not forwarded: %+v", ft.lastToolCtx.Identity)
	}
	if ft.lastToolCtx.TraceID != "trace-123" {
		t.Fatalf("TraceID = %q, want trace-123", ft.lastToolCtx.TraceID)
	}
	if ft.lastToolCtx.ParentTaskID != "task-2" {
		t.Fatalf("ParentTaskID = %q, want task-2", ft.lastToolCtx.ParentTaskID)
	}
	if ft.lastToolCtx.AgentName != "wf" {
		t.Fatalf("AgentName = %q, want wf", ft.lastToolCtx.AgentName)
	}
}

type blockingTool struct {
	name      string
	ctxObs    chan struct{}
	enterChan chan struct{}
	enterOnce sync.Once
}

func (b *blockingTool) Name() string                 { return b.name }
func (b *blockingTool) Description() string          { return "blocking tool" }
func (b *blockingTool) Instructions() string         { return "" }
func (b *blockingTool) IsLongRunning() bool          { return false }
func (b *blockingTool) RequiredScopes() []string     { return nil }
func (b *blockingTool) RawStringArgs() []string      { return nil }
func (b *blockingTool) ContextParam() string         { return "" }
func (b *blockingTool) Parameters() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (b *blockingTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return nil
}
func (b *blockingTool) Execute(ctx context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	b.enterOnce.Do(func() { close(b.enterChan) })
	<-ctx.Done()
	close(b.ctxObs)
	return tool.Error("cancelled"), nil
}

func TestDispatchToolNode_CancelMidDispatch(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "wf",
		Namespace: "ns",
		Nodes: []*NodeConfig{
			{ID: "t", Type: "tool", ToolName: "blocker"},
		},
		OutputMapping: map[string]any{"x": "{{t.output}}"},
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	w.dag = dag
	w.tools = tool.NewSet()
	bt := &blockingTool{
		name:      "blocker",
		ctxObs:    make(chan struct{}),
		enterChan: make(chan struct{}),
	}
	if err := w.tools.Register(bt); err != nil {
		t.Fatalf("register: %v", err)
	}

	state := NewExecutionState("wf", "exec")
	execCtx := NewExecutionContext(context.Background(), "task-c", "ctx-c", nil, a2a.UserProperties{}, false)
	e := &DAGExecutor{
		cfg:      cfg,
		dag:      dag,
		state:    state,
		execCtx:  execCtx,
		instance: w,
		logger:   slog.Default(),
	}
	w.timerManager = NewInProcessTimerManager(slog.Default(), func(execID string, event Event) {
		if execID == state.ExecutionID {
			_ = e.Advance(context.Background(), event)
		}
	})
	go w.timerManager.Start(execCtx.Ctx)
	defer execCtx.Cancel()

	if err := e.Advance(context.Background(), &EventStart{
		TaskID: "task-c", ContextID: "ctx-c", Input: map[string]any{},
	}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	select {
	case <-bt.enterChan:
	case <-time.After(2 * time.Second):
		t.Fatal("tool never entered Execute")
	}

	if err := e.Advance(context.Background(), &EventCancel{Reason: "test"}); err != nil {
		t.Fatalf("cancel advance: %v", err)
	}

	select {
	case <-bt.ctxObs:
	case <-time.After(2 * time.Second):
		t.Fatal("tool ctx never cancelled")
	}

	waitForFinalize(t, e, state)
}

func TestToolStatusForwarder_PublishesOnStatusTopic(t *testing.T) {
	brk := &capturingBroker{}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: brk})
	execCtx := NewExecutionContext(context.Background(), "task-s", "ctx-s", nil, a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/gateway/lifecycle-gw/status/task-s",
		TraceID:        "trace-s",
	}, false)
	defer execCtx.Cancel()

	e := &DAGExecutor{
		cfg:      cfg,
		execCtx:  execCtx,
		instance: w,
		logger:   slog.Default(),
	}

	fn := e.toolStatusForwarder("n1", "sub-1")
	if fn == nil {
		t.Fatal("expected non-nil forwarder when A2AStatusTopic is set")
	}
	if err := fn("hello there"); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(msgs))
	}
	p := msgs[0]
	if p.Topic != execCtx.UserProps.A2AStatusTopic {
		t.Fatalf("topic = %q, want %q", p.Topic, execCtx.UserProps.A2AStatusTopic)
	}
	if p.UserProperties[a2a.PropTraceID] != "trace-s" {
		t.Fatalf("traceID = %q, want trace-s", p.UserProperties[a2a.PropTraceID])
	}
	if !contains(string(p.Payload), "hello there") {
		t.Fatalf("payload did not contain status text: %s", string(p.Payload))
	}
	if !contains(string(p.Payload), `"node_id":"n1"`) {
		t.Fatalf("payload missing node_id: %s", string(p.Payload))
	}
}

func TestToolStatusForwarder_NilWhenNoStatusTopic(t *testing.T) {
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	execCtx := NewExecutionContext(context.Background(), "t", "c", nil, a2a.UserProperties{}, false)
	defer execCtx.Cancel()
	e := &DAGExecutor{cfg: cfg, execCtx: execCtx, instance: w, logger: slog.Default()}

	if fn := e.toolStatusForwarder("n1", "s1"); fn != nil {
		t.Fatal("forwarder should be nil when no A2AStatusTopic is set")
	}
}

func TestBuildAgentCard_ToolNodeSchemaExposed(t *testing.T) {
	uniqueType := "fake-card-test"
	registerTestConnectorType(t, uniqueType, func(_ context.Context, cfg connector.Config, _ runtime.Services) (connector.Connector, error) {
		return &fakeConnector{name: cfg.Name, tool: &fakeTool{name: "card_tool"}}, nil
	})

	cfg := &WorkflowConfig{
		Name:      "wf",
		Namespace: "ns",
		Connectors: []any{
			map[string]any{"name": "c1", "type": uniqueType},
		},
		Nodes: []*NodeConfig{
			{ID: "t", Type: "tool", ToolName: "card_tool"},
		},
		OutputMapping: map[string]any{"x": "{{t.output}}"},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}

	card := w.BuildAgentCard()
	var foundToolNode bool
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != "https://solace.com/a2a/extensions/sam/workflow-visualization" {
			continue
		}
		wf, ok := ext.Params["workflow_config"].(map[string]any)
		if !ok {
			t.Fatalf("workflow_config not a map: %T", ext.Params["workflow_config"])
		}
		nodes, ok := wf["nodes"].([]map[string]any)
		if !ok {
			t.Fatalf("nodes not a []map[string]any: %T", wf["nodes"])
		}
		for _, n := range nodes {
			if n["id"] != "t" {
				continue
			}
			foundToolNode = true
			if n["tool_name"] != "card_tool" {
				t.Fatalf("tool_name = %v, want card_tool", n["tool_name"])
			}
			if _, ok := n["input_schema"]; !ok {
				t.Fatal("tool node missing input_schema on viz card")
			}
		}
	}
	if !foundToolNode {
		t.Fatal("tool node not present in viz card")
	}
}

func waitForFinalize(t *testing.T, e *DAGExecutor, state *WorkflowExecutionState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := state.Finalized || state.ErrorState != nil
		e.mu.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("workflow did not finalize within 2s")
}

func TestInitToolsAndConnectors_WarnsOnUnexpandedToolsets(t *testing.T) {
	var logBuf strings.Builder
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Toolsets: []string{"echo-tools"}}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	w.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(logBuf.String(), "unexpanded toolsets") {
		t.Errorf("expected an unexpanded-toolsets warning, got log: %s", logBuf.String())
	}
}

func TestBuildToolConfigIndex(t *testing.T) {
	sizeCfg := map[string]any{"max_response_size_bytes": 1024}
	mcpCfg := map[string]any{"mcp_tool_response_save_threshold_bytes": 1}

	records := []toolinit.EntryRecord{
		{ToolMap: map[string]any{"tool_type": "builtin", "tool_name": "web_request", "tool_config": sizeCfg}, ToolNames: []string{"web_request"}},
		{ToolMap: map[string]any{"tool_type": "builtin", "tool_name": "no_config"}, ToolNames: []string{"no_config"}},
		{ToolMap: map[string]any{"tool_type": "mcp", "tool_name": "search", "tool_name_prefix": "gh", "tool_config": mcpCfg}, ToolNames: []string{"gh_search"}},
		{ToolMap: map[string]any{"tool_type": "mcp", "tool_name": "search-repos", "tool_config": mcpCfg}, ToolNames: []string{"search_repos"}},
		{ToolMap: map[string]any{"tool_type": "mcp", "allow_list": []any{"alpha", "beta"}, "tool_config": mcpCfg}, ToolNames: []string{"alpha", "beta"}},
	}

	idx := buildToolConfigIndex(records, nil)

	for _, name := range []string{"gh_search", "search_repos", "alpha", "beta"} {
		if idx[name]["mcp_tool_response_save_threshold_bytes"] != 1 {
			t.Errorf("idx[%q] = %v, want the entry's mcp tool_config (registered name must be the key)", name, idx[name])
		}
	}
	if idx["web_request"]["max_response_size_bytes"] != 1024 {
		t.Errorf("idx[web_request] = %v, want max_response_size_bytes=1024", idx["web_request"])
	}
	for _, authored := range []string{"search", "search-repos"} {
		if _, ok := idx[authored]; ok {
			t.Errorf("authored name %q must not be a key; only registered names are addressable", authored)
		}
	}
	if _, ok := idx["no_config"]; ok {
		t.Errorf("no_config should be absent (entry declares no tool_config)")
	}
	if len(idx) != 5 {
		t.Errorf("index size = %d, want 5 (web_request, gh_search, search_repos, alpha, beta); got %v", len(idx), idx)
	}
}

func TestBuildToolConfigIndex_Empty(t *testing.T) {
	if idx := buildToolConfigIndex(nil, nil); len(idx) != 0 {
		t.Errorf("no entries should yield an empty index, got %v", idx)
	}
	records := []toolinit.EntryRecord{{ToolMap: map[string]any{"tool_config": map[string]any{"v": 1}}}}
	if idx := buildToolConfigIndex(records, nil); len(idx) != 0 {
		t.Errorf("record with no ToolNames should yield an empty index, got %v", idx)
	}
}

func TestBuildToolConfigIndex_PerToolConfigBeatsGroupDefault(t *testing.T) {
	perToolEntry := toolinit.EntryRecord{
		ToolMap:   map[string]any{"tool_type": "builtin", "tool_name": "load_artifact", "tool_config": map[string]any{"marker": "per-tool"}},
		ToolNames: []string{"load_artifact"},
	}
	groupEntry := toolinit.EntryRecord{
		ToolMap:   map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management", "tool_config": map[string]any{"marker": "group"}},
		ToolNames: []string{"list_artifacts", "load_artifact", "delete_artifact"},
	}

	for _, tc := range []struct {
		name    string
		records []toolinit.EntryRecord
	}{
		{name: "group_listed_second", records: []toolinit.EntryRecord{perToolEntry, groupEntry}},
		{name: "group_listed_first", records: []toolinit.EntryRecord{groupEntry, perToolEntry}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx := buildToolConfigIndex(tc.records, nil)

			if got := idx["load_artifact"]["marker"]; got != "per-tool" {
				t.Errorf("idx[load_artifact][marker] = %v, want %q — the explicit per-tool entry must win regardless of YAML order", got, "per-tool")
			}
			for _, name := range []string{"list_artifacts", "delete_artifact"} {
				if got := idx[name]["marker"]; got != "group" {
					t.Errorf("idx[%q][marker] = %v, want %q", name, got, "group")
				}
			}
		})
	}
}

func TestBuildToolConfigIndex_OverlappingGroupsReportTheCollision(t *testing.T) {
	records := []toolinit.EntryRecord{
		{
			ToolMap:   map[string]any{"tool_type": "builtin-group", "group_name": "web_tools", "tool_config": map[string]any{"marker": "web"}},
			ToolNames: []string{"web_request", "web_search_google"},
		},
		{
			ToolMap:   map[string]any{"tool_type": "builtin-group", "group_name": "research", "tool_config": map[string]any{"marker": "research"}},
			ToolNames: []string{"web_search_google", "summarize"},
		},
	}

	var logBuf strings.Builder
	idx := buildToolConfigIndex(records, slog.New(slog.NewTextHandler(&logBuf, nil)))

	if got := idx["web_search_google"]["marker"]; got != "web" {
		t.Errorf("idx[web_search_google][marker] = %v, want %q — the first entry wins the tie", got, "web")
	}
	if got := idx["web_request"]["marker"]; got != "web" {
		t.Errorf("idx[web_request][marker] = %v, want %q", got, "web")
	}
	if got := idx["summarize"]["marker"]; got != "research" {
		t.Errorf("idx[summarize][marker] = %v, want %q", got, "research")
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "web_search_google") || !strings.Contains(logged, "research") {
		t.Errorf("collision was not reported; log must name the contested tool and the losing entry, got:\n%s", logged)
	}
}

func TestBuildToolConfigIndex_DroppedConfigIsLogged(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		record  toolinit.EntryRecord
		wantLog []string
	}{
		{
			name: "tool_config is not a map",
			record: toolinit.EntryRecord{
				ToolMap:   map[string]any{"tool_type": "builtin", "tool_name": "web_request", "tool_config": "max_response_size_bytes=1024"},
				ToolNames: []string{"web_request"},
			},
			wantLog: []string{"web_request"},
		},
		{
			name: "config present but the entry registered no names",
			record: toolinit.EntryRecord{
				ToolMap: map[string]any{"tool_type": "mcp", "tool_name": "search", "tool_config": map[string]any{"v": 1}},
			},
			wantLog: []string{"mcp"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logBuf strings.Builder
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))

			idx := buildToolConfigIndex([]toolinit.EntryRecord{tc.record}, logger)

			if len(idx) != 0 {
				t.Errorf("index = %v, want empty: there is no valid registered name to key the config on", idx)
			}
			out := logBuf.String()
			if out == "" {
				t.Fatal("an authored tool_config was dropped with no log; the tool silently runs on its defaults")
			}
			if !strings.Contains(out, "level=WARN") {
				t.Errorf("want a WARN, got: %s", out)
			}
			for _, want := range tc.wantLog {
				if !strings.Contains(out, want) {
					t.Errorf("log should name %q so the operator can find the entry, got: %s", want, out)
				}
			}
		})
	}
}

func TestBuildToolConfigIndex_NoConfigStaysQuiet(t *testing.T) {
	t.Parallel()
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	buildToolConfigIndex([]toolinit.EntryRecord{
		{ToolMap: map[string]any{"tool_type": "builtin", "tool_name": "no_config"}, ToolNames: []string{"no_config"}},
		{ToolMap: map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management"}},
	}, logger)

	if out := logBuf.String(); out != "" {
		t.Errorf("entries declaring no tool_config must not warn; got: %s", out)
	}
}

func TestToolSignalForwarder_PublishesRAGSignal(t *testing.T) {
	brk := &capturingBroker{}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: brk})
	execCtx := NewExecutionContext(context.Background(), "task-s", "ctx-s", nil, a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/gateway/lifecycle-gw/status/task-s",
		TraceID:        "trace-s",
	}, false)
	defer execCtx.Cancel()
	e := &DAGExecutor{cfg: cfg, execCtx: execCtx, instance: w, logger: slog.Default()}

	fn := e.toolSignalForwarder("n1", "web_search_google", "sub-1")
	if fn == nil {
		t.Fatal("expected non-nil signal forwarder when A2AStatusTopic is set")
	}
	if err := fn(a2a.RAGInfoUpdateData{
		Type:       string(a2a.SignalRAGInfoUpdate),
		Query:      "solace agent mesh",
		SearchType: "web_search",
		SearchTurn: 2,
		Sources:    []a2a.RAGSource{{CitationID: "s2r0", SourceURL: "https://solace.com"}},
		IsComplete: true,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(msgs))
	}
	payload := string(msgs[0].Payload)
	if msgs[0].Topic != execCtx.UserProps.A2AStatusTopic {
		t.Fatalf("topic = %q, want %q", msgs[0].Topic, execCtx.UserProps.A2AStatusTopic)
	}
	for _, want := range []string{
		`"kind":"data"`, `"type":"rag_info_update"`, `"s2r0"`, `"web_search"`,
		`"search_turn":2`, `"node_id":"n1"`, `"state":"working"`, `"final":false`,
	} {
		if !contains(payload, want) {
			t.Fatalf("payload missing %s: %s", want, payload)
		}
	}
}

func TestToolSignalForwarder_RefusesInteractiveSignals(t *testing.T) {
	brk := &capturingBroker{}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: brk})
	execCtx := NewExecutionContext(context.Background(), "task-s", "ctx-s", nil, a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/gateway/lifecycle-gw/status/task-s",
	}, false)
	defer execCtx.Cancel()
	e := &DAGExecutor{cfg: cfg, execCtx: execCtx, instance: w, logger: slog.Default()}
	fn := e.toolSignalForwarder("n1", "web_search_google", "sub-1")

	for _, sig := range []any{
		&a2a.AuthenticationRequiredData{},
		a2a.DeepResearchPlanData{Type: string(a2a.SignalDeepResearchPlan)},
		struct{}{},
	} {
		if err := fn(sig); err == nil {
			t.Errorf("%T was accepted; callers treating nil as success will report a flow nothing can complete", sig)
		}
	}
	if msgs := brk.snapshot(); len(msgs) != 0 {
		t.Fatalf("an interactive signal was published: %d messages", len(msgs))
	}
}

func TestToolSignalForwarder_ForwardsEveryAllowlistedType(t *testing.T) {
	signals := []struct {
		name string
		sig  any
	}{
		{"rag_info_update", a2a.RAGInfoUpdateData{Type: string(a2a.SignalRAGInfoUpdate)}},
		{"deep_research_progress", a2a.DeepResearchProgressData{Type: string(a2a.SignalDeepResearchProgress)}},
		{"deep_research_report", a2a.DeepResearchReportData{Type: string(a2a.SignalDeepResearchReport)}},
		{"deep_research_plan_stale", a2a.DeepResearchPlanStaleData{Type: string(a2a.SignalDeepResearchPlanStale)}},
		{"builder_component_progress", a2a.BuilderComponentProgressData{Type: string(a2a.SignalBuilderComponentProgress)}},
	}
	if len(signals) != len(workflowForwardableSignals) {
		t.Fatalf("covering %d signal types but the allowlist has %d", len(signals), len(workflowForwardableSignals))
	}
	for _, c := range signals {
		t.Run(c.name, func(t *testing.T) {
			brk := &capturingBroker{}
			cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
			w := newTestWorkflowInstance(cfg, &stubServices{brk: brk})
			execCtx := NewExecutionContext(context.Background(), "task-s", "ctx-s", nil, a2a.UserProperties{
				A2AStatusTopic: "ns/a2a/v1/gateway/lifecycle-gw/status/task-s",
			}, false)
			defer execCtx.Cancel()
			e := &DAGExecutor{cfg: cfg, execCtx: execCtx, instance: w, logger: slog.Default()}

			if err := e.toolSignalForwarder("n1", "some_tool", "sub-1")(c.sig); err != nil {
				t.Fatalf("allowlisted signal refused: %v", err)
			}
			msgs := brk.snapshot()
			if len(msgs) != 1 {
				t.Fatalf("published %d messages, want 1", len(msgs))
			}
			if !contains(string(msgs[0].Payload), `"signal_type":"`+c.name+`"`) {
				t.Errorf("payload missing signal_type %s: %s", c.name, string(msgs[0].Payload))
			}
		})
	}
}

func TestToolSignalForwarder_AuthErrorNamesAuthorization(t *testing.T) {
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: &capturingBroker{}})
	execCtx := NewExecutionContext(context.Background(), "task-s", "ctx-s", nil, a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/gateway/lifecycle-gw/status/task-s",
	}, false)
	defer execCtx.Cancel()
	e := &DAGExecutor{cfg: cfg, execCtx: execCtx, instance: w, logger: slog.Default()}

	err := e.toolSignalForwarder("n1", "salesforce_query", "sub-1")(&a2a.AuthenticationRequiredData{})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"authorization", "salesforce_query", "workflow"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
	if contains(err.Error(), "untyped") {
		t.Errorf("auth-required reported as an untyped signal: %v", err)
	}
	if got := tool.AuthPromptUndelivered(err).ErrorCode; got != "DISPATCH_USER_INPUT_UNAVAILABLE" {
		t.Errorf("refused sign-in coded %q, want DISPATCH_USER_INPUT_UNAVAILABLE", got)
	}
}

func TestToolSignalForwarder_RefusesSignInWithoutAStatusTopic(t *testing.T) {
	brk := &capturingBroker{}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: brk})
	execCtx := NewExecutionContext(context.Background(), "t", "c", nil, a2a.UserProperties{}, false)
	defer execCtx.Cancel()
	e := &DAGExecutor{cfg: cfg, execCtx: execCtx, instance: w, logger: slog.Default()}

	fn := e.toolSignalForwarder("n1", "salesforce_query", "s1")
	if fn == nil {
		t.Fatal("want a forwarder even with no A2AStatusTopic")
	}
	err := fn(&a2a.AuthenticationRequiredData{})
	if got := tool.AuthPromptUndelivered(err).ErrorCode; got != "DISPATCH_USER_INPUT_UNAVAILABLE" {
		t.Errorf("refused sign-in coded %q, want DISPATCH_USER_INPUT_UNAVAILABLE", got)
	}
	if err := fn(a2a.RAGInfoUpdateData{Type: string(a2a.SignalRAGInfoUpdate)}); err != nil {
		t.Errorf("allowlisted signal refused with no topic to publish to: %v", err)
	}
	if err := fn(a2a.DeepResearchPlanData{Type: string(a2a.SignalDeepResearchPlan)}); err == nil {
		t.Error("an interactive signal was accepted; callers treating nil as success will wait for a reply nothing can send")
	}
	if msgs := brk.snapshot(); len(msgs) != 0 {
		t.Fatalf("published %d messages with no status topic, want 0", len(msgs))
	}
}

func TestBuildToolContext_WiresSendSignal(t *testing.T) {
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: &capturingBroker{}})
	execCtx := NewExecutionContext(context.Background(), "task-s", "ctx-s", nil, a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/gateway/lifecycle-gw/status/task-s",
	}, false)
	defer execCtx.Cancel()
	e := &DAGExecutor{cfg: cfg, state: NewExecutionState("wf", "exec-1"), execCtx: execCtx, instance: w, logger: slog.Default()}

	tc := e.buildToolContext(&NodeConfig{ID: "n1", Type: "tool", ToolName: "web_search_google"}, NodeCompletion{NodeID: "n1", SubTaskID: "sub-1"})
	if tc.SendSignal == nil {
		t.Fatal("buildToolContext must wire SendSignal, or a tool's citations are dropped silently")
	}
	err := tc.SendSignal(&a2a.AuthenticationRequiredData{})
	if err == nil {
		t.Fatal("auth-required should be refused")
	}
	if !contains(err.Error(), "web_search_google") {
		t.Errorf("error does not name the tool, so the operator cannot tell which one: %v", err)
	}
}

func TestInitToolsAndConnectors_BuiltinGroupToolConfigReachesMembers(t *testing.T) {
	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		Tools: []any{
			map[string]any{
				"tool_type":   "builtin-group",
				"group_name":  "artifact_management",
				"tool_config": map[string]any{"tool_output_save_threshold_bytes": 1000},
			},
		},
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if err := w.initToolsAndConnectors(context.Background(), w.svc); err != nil {
		t.Fatalf("init: %v", err)
	}

	members := []string{"list_artifacts", "load_artifact", "delete_artifact"}
	for _, name := range members {
		got := w.toolConfigs[name]
		if got == nil {
			t.Errorf("toolConfigs[%q] is nil: the group's tool_config reached no member, so the tool runs on defaults", name)
			continue
		}
		if got["tool_output_save_threshold_bytes"] != 1000 {
			t.Errorf("toolConfigs[%q] = %v, want the group's tool_config", name, got)
		}
	}
}
