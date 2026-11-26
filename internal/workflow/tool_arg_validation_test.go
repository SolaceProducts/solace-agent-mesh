// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type schemaTool struct {
	name     string
	props    map[string]any
	authored bool
}

func (t *schemaTool) AuthoredSchemas() bool { return t.authored }

func (t *schemaTool) Name() string            { return t.name }
func (t *schemaTool) Description() string     { return "tool with a declared parameter schema" }
func (t *schemaTool) Instructions() string    { return "" }
func (t *schemaTool) IsLongRunning() bool     { return false }
func (t *schemaTool) RawStringArgs() []string { return nil }
func (t *schemaTool) ContextParam() string    { return "" }
func (t *schemaTool) RequiredScopes() []string {
	return nil
}
func (t *schemaTool) Parameters() *tool.JSONSchema {
	if t.props == nil {
		return nil
	}
	return &tool.JSONSchema{Type: "object", Properties: t.props}
}
func (t *schemaTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (t *schemaTool) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	return tool.OK("ok"), nil
}

func webRequestLikeTool() *schemaTool {
	return &schemaTool{name: "web_request", authored: true, props: map[string]any{
		"url":                      map[string]any{"type": "string"},
		"method":                   map[string]any{"type": "string"},
		"output_artifact_filename": map[string]any{"type": "string"},
	}}
}

func TestUndeclaredToolArgs(t *testing.T) {
	tests := []struct {
		name  string
		tl    tool.Tool
		input map[string]any
		want  []string
	}{
		{
			name:  "the misspelling that was silently dropped",
			tl:    webRequestLikeTool(),
			input: map[string]any{"url": "https://x", "output_filename": "source_image"},
			want:  []string{"output_filename"},
		},
		{
			name:  "correct names are accepted",
			tl:    webRequestLikeTool(),
			input: map[string]any{"url": "https://x", "output_artifact_filename": "source_image"},
			want:  nil,
		},
		{
			name:  "several undeclared args come back sorted",
			tl:    webRequestLikeTool(),
			input: map[string]any{"zeta": 1, "alpha": 2, "url": "https://x"},
			want:  []string{"alpha", "zeta"},
		},
		{
			name:  "absent schema is not enforced",
			tl:    &schemaTool{name: "discovered"},
			input: map[string]any{"anything": 1},
			want:  nil,
		},
		{
			name:  "schema declaring no properties is not enforced",
			tl:    &schemaTool{name: "empty", props: map[string]any{}},
			input: map[string]any{"anything": 1},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := undeclaredToolArgs(tt.tl, tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("undeclaredToolArgs = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("undeclaredToolArgs = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestDeclaredToolArgs_SortedForTheErrorMessage(t *testing.T) {
	got := declaredToolArgs(webRequestLikeTool())
	want := []string{"method", "output_artifact_filename", "url"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("declaredToolArgs = %v, want %v", got, want)
	}
	if declaredToolArgs(&schemaTool{name: "no-schema"}) != nil {
		t.Error("declaredToolArgs on an absent schema should be nil")
	}
}

func TestDispatchToolNode_UndeclaredArgOnBuiltin_FailsNode(t *testing.T) {
	node := &NodeConfig{
		ID:       "fetch_image",
		Type:     "tool",
		ToolName: "web_request",
		Input: map[string]any{
			"url":             "{{workflow.input.url}}",
			"output_filename": "source_image",
		},
	}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()

	ts := tool.NewSet()
	if err := ts.Register(webRequestLikeTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs = upstreamOutputs()

	e.dispatchToolNode(context.Background(), node, "sub-1")
	e.instance.wg.Wait()

	if e.state.ErrorState == nil {
		t.Fatal("dispatch accepted an argument the tool does not declare")
	}
	msg := e.state.ErrorState.ErrorMessage
	for _, want := range []string{"output_filename", "output_artifact_filename"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}

	for _, want := range []string{"INPUT parameter names", "not the tool's result field names", "{{fetch_image.output"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q is missing the input-vs-result warning %q", msg, want)
		}
	}
}

func TestDispatchToolNode_UndeclaredArgOnDiscoveredSchema_DoesNotFailNode(t *testing.T) {
	node := &NodeConfig{
		ID:       "detect",
		Type:     "tool",
		ToolName: "detect_objects",
		Input:    map[string]any{"undeclared_but_maybe_real": "x"},
	}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()

	rec := &recordingSchemaTool{
		schemaTool: &schemaTool{
			name:  "detect_objects",
			props: map[string]any{"image": map[string]any{"type": "string"}},
		},
		ran: make(chan struct{}),
	}
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs = upstreamOutputs()

	e.dispatchToolNode(context.Background(), node, "sub-1")
	e.instance.wg.Wait()

	if !rec.executed.Load() {
		t.Error("a discovered schema blocked the call instead of warning")
	}
	if e.state.ErrorState != nil && strings.Contains(e.state.ErrorState.ErrorMessage, "does not accept") {
		t.Errorf("a discovered schema rejected an argument: %q", e.state.ErrorState.ErrorMessage)
	}
}

func TestDispatchToolNode_ImplicitInputPassthrough_DoesNotFailNode(t *testing.T) {
	fetch := &NodeConfig{ID: "fetch", Type: "tool", ToolName: "web_request",
		Input: map[string]any{"url": "https://x"}}
	consume := &NodeConfig{ID: "consume", Type: "tool", ToolName: "web_request",
		DependsOn: []string{"fetch"}}
	nodes := []*NodeConfig{fetch, consume}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()

	rec := newRecordingSchemaTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs = map[string]map[string]any{
		"fetch": {"output": map[string]any{
			"filename": "page.html",
			"status":   "success",
			"message":  "fetched",
		}},
	}

	e.dispatchToolNode(context.Background(), consume, "sub-1")
	e.instance.wg.Wait()

	if !rec.executed.Load() {
		t.Error("implicit input passthrough blocked the call")
	}
	if e.state.ErrorState != nil {
		t.Errorf("implicit input passthrough failed the node: %q", e.state.ErrorState.ErrorMessage)
	}
}

type recordingSchemaTool struct {
	*schemaTool
	executed atomic.Bool
	ran      chan struct{}
}

func newRecordingSchemaTool() *recordingSchemaTool {
	return &recordingSchemaTool{schemaTool: webRequestLikeTool(), ran: make(chan struct{})}
}

func (t *recordingSchemaTool) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	if !t.executed.Swap(true) {
		close(t.ran)
	}
	return tool.OK("ok"), nil
}

func TestInvokeToolNode_IterationPaths_CatchUndeclaredArg(t *testing.T) {
	for _, tc := range []struct {
		name    string
		routing NodeCompletion
	}{
		{"map template", NodeCompletion{NodeID: "fetch_each", SubTaskID: "sub-1", MapNodeID: "each", ItemIndex: 0}},
		{"loop body", NodeCompletion{NodeID: "fetch_each", SubTaskID: "sub-1", LoopNodeID: "until_done", ItemIndex: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &NodeConfig{
				ID:       "fetch_each",
				Type:     "tool",
				ToolName: "web_request",
				Input: map[string]any{
					"url":             "{{_map_item}}",
					"output_filename": "source_image",
				},
			}
			nodes := []*NodeConfig{node}
			cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatalf("dag: %v", err)
			}

			e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
			defer e.execCtx.Cancel()

			rec := newRecordingSchemaTool()
			ts := tool.NewSet()
			if err := ts.Register(rec); err != nil {
				t.Fatalf("register: %v", err)
			}
			e.instance.tools = ts
			e.state.NodeOutputs = upstreamOutputs()

			e.invokeToolNode(node, map[string]any{
				"url":             "https://x",
				"output_filename": "source_image",
			}, tc.routing)

			e.instance.wg.Wait()
			if rec.executed.Load() {
				t.Error("the tool was invoked with an argument it does not declare")
			}
		})
	}
}

func TestInvokeToolNode_IterationPath_AcceptsDeclaredArgs(t *testing.T) {
	node := &NodeConfig{
		ID:       "fetch_each",
		Type:     "tool",
		ToolName: "web_request",
		Input:    map[string]any{"url": "{{_map_item}}", "output_artifact_filename": "source_image"},
	}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()

	rec := newRecordingSchemaTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs = upstreamOutputs()

	e.invokeToolNode(node, map[string]any{
		"url":                      "https://x",
		"output_artifact_filename": "source_image",
	}, NodeCompletion{NodeID: node.ID, SubTaskID: "sub-1", MapNodeID: "each", ItemIndex: 0})

	e.instance.wg.Wait()
	if !rec.executed.Load() {
		t.Error("declared arguments were blocked on the iteration path")
	}
}

func TestArgCheck_ThroughAdvance_DoesNotDeadlock(t *testing.T) {
	node := &NodeConfig{
		ID:       "fetch_image",
		Type:     "tool",
		ToolName: "web_request",
		Input: map[string]any{
			"url":             "http://x",
			"output_filename": "source_image",
		},
	}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, FailFast: true}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-advance-1"))
	defer e.execCtx.Cancel()

	ts := tool.NewSet()
	if err := ts.Register(webRequestLikeTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts

	done := make(chan struct{})
	go func() {
		_ = e.Advance(context.Background(), &EventStart{})
		e.instance.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: the arg check reported failure while e.mu was held")
	}

	if e.state.ErrorState == nil {
		t.Fatal("the undeclared argument did not fail the node through the real path")
	}
	if !strings.Contains(e.state.ErrorState.ErrorMessage, "output_filename") {
		t.Errorf("error does not name the offending argument: %q", e.state.ErrorState.ErrorMessage)
	}
	if len(e.state.PendingNodes) != 0 {
		t.Errorf("node left pending after failure: %v", e.state.PendingNodes)
	}
}

func TestPermissiveToolValidation_DowngradesTheArgCheck(t *testing.T) {
	node := &NodeConfig{
		ID:       "fetch_image",
		Type:     "tool",
		ToolName: "web_request",
		Input: map[string]any{
			"url":             "{{workflow.input.url}}",
			"output_filename": "source_image",
		},
	}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-permissive"))
	defer e.execCtx.Cancel()

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "workflow_tool_validation_permissive", Default: true})
	e.instance.svc = &stubServices{brk: stubBroker{}, features: reg}

	rec := newRecordingSchemaTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs = upstreamOutputs()

	e.dispatchToolNode(context.Background(), node, "sub-1")
	e.instance.wg.Wait()

	if !rec.executed.Load() {
		t.Error("the permissive flag did not let the call through")
	}
	if e.state.ErrorState != nil {
		t.Errorf("permissive mode failed the node: %v", e.state.ErrorState.ErrorMessage)
	}
}

func TestDispatchToolNode_OperatorInputPassthrough_DoesNotFailNode(t *testing.T) {
	fetch := &NodeConfig{ID: "fetch", Type: "tool", ToolName: "web_request",
		Input: map[string]any{"url": "https://example.com"}}
	consume := &NodeConfig{
		ID: "consume", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch"},
		Input: map[string]any{"coalesce": []any{
			"{{fetch.output}}",
			map[string]any{"url": "https://fallback.example.com"},
		}},
	}
	nodes := []*NodeConfig{fetch, consume}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-operator"))
	defer e.execCtx.Cancel()

	rec := newRecordingSchemaTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs = map[string]map[string]any{
		"fetch": {"output": map[string]any{
			"filename": "x.jpg", "status": "success", "message": "ok",
		}},
	}

	e.dispatchToolNode(context.Background(), consume, "sub-1")
	e.instance.wg.Wait()

	if e.state.ErrorState != nil {
		t.Errorf("a coalesce passthrough was rejected as undeclared arguments: %v",
			e.state.ErrorState.ErrorMessage)
	}
	if !rec.executed.Load() {
		t.Error("the tool was never dispatched")
	}
}

func TestDispatchToolNode_OperatorExemptionDoesNotCoverPlainInput(t *testing.T) {
	node := &NodeConfig{
		ID: "consume", Type: "tool", ToolName: "web_request",
		Input: map[string]any{"url": "https://example.com", "output_filename": "x"},
	}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-plain"))
	defer e.execCtx.Cancel()

	ts := tool.NewSet()
	if err := ts.Register(webRequestLikeTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs = upstreamOutputs()

	e.dispatchToolNode(context.Background(), node, "sub-1")
	e.instance.wg.Wait()

	if e.state.ErrorState == nil {
		t.Fatal("an undeclared argument in a plain input: was accepted")
	}
}

func TestUndeclaredToolArgs_EmptySchemaTierIsProvenanceGated(t *testing.T) {
	authoredNoParams := &schemaTool{name: "list_artifacts", authored: true, props: map[string]any{}}
	discoveredNoParams := &schemaTool{name: "mcp_thing", props: map[string]any{}}
	input := map[string]any{"filter": "*.png"}

	if got := undeclaredToolArgs(authoredNoParams, input); len(got) != 1 || got[0] != "filter" {
		t.Errorf("authored zero-parameter tool: got %v, want [filter]", got)
	}
	if got := undeclaredToolArgs(discoveredNoParams, input); got != nil {
		t.Errorf("discovered empty schema should stay unenforced, got %v", got)
	}
}

func TestUndeclaredToolArgs_RawSchemaIsNotEnforced(t *testing.T) {
	raw := &rawSchemaTool{schemaTool: schemaTool{name: "passthrough", authored: true}}
	input := map[string]any{"anything": 1, "at_all": 2}

	if got := undeclaredToolArgs(raw, input); got != nil {
		t.Errorf("a Raw passthrough schema was enforced, reporting %v as undeclared", got)
	}
	if got := declaredToolArgs(raw); got != nil {
		t.Errorf("a Raw passthrough schema named %v as its accepted parameters", got)
	}
}

type rawSchemaTool struct {
	schemaTool
}

func (r *rawSchemaTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{Raw: map[string]any{"type": "object", "properties": map[string]any{}}}
}

func TestPermissiveToolValidation_ReadsTheEnvironmentVariable(t *testing.T) {
	t.Setenv("SAM_FEATURE_WORKFLOW_TOOL_VALIDATION_PERMISSIVE", "true")

	node := &NodeConfig{
		ID:       "fetch_image",
		Type:     "tool",
		ToolName: "web_request",
		Input: map[string]any{
			"url":             "{{workflow.input.url}}",
			"output_filename": "source_image",
		},
	}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-env-permissive"))
	defer e.execCtx.Cancel()

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "workflow_tool_validation_permissive", Default: false})
	e.instance.svc = &stubServices{brk: stubBroker{}, features: reg}

	rec := newRecordingSchemaTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs = upstreamOutputs()

	e.dispatchToolNode(context.Background(), node, "sub-1")
	e.instance.wg.Wait()

	if !rec.executed.Load() {
		t.Error("the environment variable did not reach the arg check")
	}
	if e.state.ErrorState != nil {
		t.Errorf("node failed despite the flag being set in the environment: %v",
			e.state.ErrorState.ErrorMessage)
	}
}
