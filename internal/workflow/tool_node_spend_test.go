// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	spendToolName     = "summarize_page"
	spendToolEnvModel = "gpt-env"
)

type strServices struct {
	*stubServices
}

func (strServices) LLM(string) llm.Client { return spendtest.UsageLLM{} }

type toolNodeSpendRow struct {
	UserSub        *string `db:"user_sub"`
	AgentName      *string `db:"agent_name"`
	WorkflowID     *string `db:"workflow_id"`
	WorkflowName   *string `db:"workflow_name"`
	RootKind       *string `db:"root_kind"`
	RootName       *string `db:"root_name"`
	RootNodeID     *string `db:"root_node_id"`
	TriggerKind    *string `db:"trigger_kind"`
	TriggerID      *string `db:"trigger_id"`
	Surface        string  `db:"surface"`
	TaskID         *string `db:"task_id"`
	TraceID        *string `db:"trace_id"`
	UncachedInput  int64   `db:"uncached_input_tokens"`
	Output         int64   `db:"output_tokens"`
	ToolName       *string `db:"tool_name"`
	WorkflowNodeID *string `db:"workflow_node_id"`
	ParentTaskID   *string `db:"parent_task_id"`
	SessionID      *string `db:"session_id"`
}

type toolNodeSpendRun struct {
	e      *DAGExecutor
	ledger *platstore.DB
}

func newToolNodeSpendRun(t *testing.T, cfg *WorkflowConfig, props a2a.UserProperties) *toolNodeSpendRun {
	t.Helper()
	ledger := platstore.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	spendtest.StartGovernor(t, brk, cfg.Namespace, ledger)
	startSpendRecordingSTR(t, brk, cfg.Namespace)

	exe := remote.NewExecutor(brk, cfg.Namespace, cfg.Name)
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	t.Cleanup(exe.Stop)
	tools := tool.NewSet()
	if err := tools.Register(remote.NewTool(&remote.ToolConfig{ToolName: spendToolName, TimeoutSeconds: 30}, exe)); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: brk})
	w.dag = dag
	w.tools = tools
	execCtx := NewExecutionContext(context.Background(), "wf-task", "wf-session", nil, props, false)
	t.Cleanup(execCtx.Cancel)
	return &toolNodeSpendRun{
		e: &DAGExecutor{
			cfg: cfg, dag: dag, state: NewExecutionState(cfg.Name, "exec"),
			execCtx: execCtx, instance: w, logger: slog.Default(),
		},
		ledger: ledger,
	}
}

func startSpendRecordingSTR(t *testing.T, brk broker.Broker, namespace string) {
	t.Helper()
	summarize := samtoolsdk.NewTool(spendToolName, "Summarizes a page with the LLM.",
		func(ctx context.Context, _ struct {
			Page string `json:"page"`
		}, tc *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			if _, err := tc.CallLLM(ctx, "sys", "user", 0.2); err != nil {
				return samtoolsdk.Error(err.Error()), nil
			}
			return samtoolsdk.OK("ok"), nil
		})
	manifest := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(manifest, []byte("version: 1\ntools: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := features.NewRegistry()
	worker := str.NewWorker(strServices{&stubServices{brk: brk, features: flags}}, str.WorkerConfig{
		WorkerID:       "spend-str",
		Namespace:      namespace,
		ManifestPaths:  []string{manifest},
		Sandbox:        str.SandboxConfig{Mode: "direct"},
		EnvModelName:   spendToolEnvModel,
		InProcessTools: []*samtoolsdk.ToolDef{summarize},
	})
	if err := worker.Init(t.Context(), nil); err != nil {
		t.Fatalf("init STR: %v", err)
	}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatalf("start STR: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
}

func (r *toolNodeSpendRun) execute(t *testing.T, input map[string]any) {
	t.Helper()
	r.e.state.NodeOutputs["workflow_input"] = map[string]any{"output": input}
	if err := r.e.Advance(context.Background(), &EventStart{TaskID: "wf-task", ContextID: "wf-session", Input: input}); err != nil {
		t.Fatalf("advance start: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		r.e.mu.Lock()
		done, failed := r.e.state.Finalized, r.e.state.ErrorState
		r.e.mu.Unlock()
		if failed != nil {
			t.Fatalf("workflow failed: %+v", failed)
		}
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("workflow did not finish within 15s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *toolNodeSpendRun) filedRows(t *testing.T, want int) []toolNodeSpendRow {
	t.Helper()
	return spendtest.FiledRows[toolNodeSpendRow](t, r.ledger, "tool node spend", want,
		`SELECT l.user_sub, l.agent_name, l.workflow_id, l.workflow_name, l.root_kind, l.root_name, l.root_node_id,
			l.trigger_kind, l.trigger_id, l.surface, l.task_id, l.trace_id, l.uncached_input_tokens, l.output_tokens,
			d.tool_name, d.workflow_node_id, d.parent_task_id, d.session_id
		 FROM token_spend_ledger l JOIN token_spend_ledger_details d ON d.id = l.id`)
}

func strIs(got *string, want string) bool { return got != nil && *got == want }

func strDeref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestAToolNodesLLMCallOnTheSTRIsBookedToTheWorkflowAtThatNode(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:          "nightly-digest",
		Namespace:     "ns",
		Nodes:         []*NodeConfig{{ID: "summarize", Type: "tool", ToolName: spendToolName}},
		OutputMapping: map[string]any{"summary": "{{summarize.output}}"},
	}
	run := newToolNodeSpendRun(t, cfg, a2a.UserProperties{UserID: "alice", TraceID: "trace-1"})

	run.execute(t, map[string]any{})

	row := run.filedRows(t, 1)[0]
	if row.Surface != "tool" || !strIs(row.ToolName, spendToolName) || !strIs(row.WorkflowNodeID, "summarize") {
		t.Errorf("row surface=%s tool=%s node=%s, want the tool surface at summarize running %s",
			row.Surface, strDeref(row.ToolName), strDeref(row.WorkflowNodeID), spendToolName)
	}
	if !strIs(row.UserSub, "alice") || row.AgentName != nil || row.WorkflowID != nil || !strIs(row.WorkflowName, "nightly-digest") ||
		!strIs(row.RootKind, a2a.RootKindWorkflow) || !strIs(row.RootName, "nightly-digest") || !strIs(row.RootNodeID, "summarize") {
		t.Errorf("row user=%s agent=%s workflow=%s root=%s/%s/%s, want alice's nightly-digest run rooted at its summarize node",
			strDeref(row.UserSub), strDeref(row.AgentName), strDeref(row.WorkflowName),
			strDeref(row.RootKind), strDeref(row.RootName), strDeref(row.RootNodeID))
	}
	if !strIs(row.TaskID, "wf-task") || !strIs(row.TraceID, "trace-1") || !strIs(row.SessionID, "wf-session") || row.ParentTaskID != nil {
		t.Errorf("row task=%s trace=%s session=%s parent=%s, want the undelegated workflow task wf-task",
			strDeref(row.TaskID), strDeref(row.TraceID), strDeref(row.SessionID), strDeref(row.ParentTaskID))
	}
	if row.UncachedInput != 120 || row.Output != 30 {
		t.Errorf("row tokens in=%d out=%d, want the 120/30 the provider reported", row.UncachedInput, row.Output)
	}
}

func TestADeployedWorkflowsToolNodeIsBookedToItsPlatformRow(t *testing.T) {
	const workflowID = "0199f3a2-7c1e-7b3d-9a4e-5f6a7b8c9d0e"
	cfg := &WorkflowConfig{
		Name:      a2a.InstanceName(a2a.KindWorkflow, workflowID),
		Namespace: "ns",
		Nodes:     []*NodeConfig{{ID: "summarize", Type: "tool", ToolName: spendToolName}},
	}
	run := newToolNodeSpendRun(t, cfg, a2a.UserProperties{UserID: "alice"})

	run.execute(t, map[string]any{})

	row := run.filedRows(t, 1)[0]
	if !strIs(row.WorkflowID, workflowID) || row.WorkflowName != nil {
		t.Errorf("row workflow id=%s name=%s, want the platform row %s and no name",
			strDeref(row.WorkflowID), strDeref(row.WorkflowName), workflowID)
	}
}

func TestAToolNodeRunAsAMapTemplateBooksEachCallUnderTheMapNode(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "page-digest",
		Namespace: "ns",
		Nodes: []*NodeConfig{
			{ID: "each", Type: "map", ItemsExpression: "{{workflow_input.output.pages}}", TemplateNode: "summarize"},
			{ID: "summarize", Type: "tool", ToolName: spendToolName, Input: map[string]any{"page": "{{_map_item}}"}},
		},
		OutputMapping: map[string]any{"summaries": "{{each.output}}"},
	}
	run := newToolNodeSpendRun(t, cfg, a2a.UserProperties{UserID: "alice"})

	run.execute(t, map[string]any{"pages": []any{"page-0", "page-1", "page-2"}})

	for i, row := range run.filedRows(t, 3) {
		if !strIs(row.WorkflowNodeID, "summarize") || !strIs(row.RootNodeID, "each") || !strIs(row.TaskID, "wf-task") {
			t.Errorf("row %d node=%s root node=%s task=%s, want a call by summarize under the map node each of wf-task",
				i, strDeref(row.WorkflowNodeID), strDeref(row.RootNodeID), strDeref(row.TaskID))
		}
	}
}

func TestAScheduledWorkflowsToolNodeIsBookedToItsSystemUserAndSchedule(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "nightly-digest",
		Namespace: "ns",
		Nodes:     []*NodeConfig{{ID: "summarize", Type: "tool", ToolName: spendToolName}},
	}
	run := newToolNodeSpendRun(t, cfg, a2a.UserProperties{UserID: rbac.DefaultSystemUserSub, Trigger: a2a.TriggerScheduled})
	run.e.execCtx.SpendOrigin = agent.SpendOrigin{ScheduledTaskID: "sched-1"}

	run.execute(t, map[string]any{})

	row := run.filedRows(t, 1)[0]
	if !strIs(row.UserSub, rbac.DefaultSystemUserSub) || !strIs(row.TriggerKind, string(spend.TriggerSchedule)) ||
		!strIs(row.TriggerID, "sched-1") || !strIs(row.WorkflowNodeID, "summarize") {
		t.Errorf("row user=%s trigger=%s/%s node=%s, want %s's run of scheduled task sched-1 at summarize",
			strDeref(row.UserSub), strDeref(row.TriggerKind), strDeref(row.TriggerID), strDeref(row.WorkflowNodeID),
			rbac.DefaultSystemUserSub)
	}
}

func TestAToolNodeOfAResumedWorkflowIsBookedWithTheSpendOriginItPausedWith(t *testing.T) {
	w, _ := newHILInstance(t)
	w.dag = &DependencyGraph{}
	state := NewExecutionState("wf", "exec-1")
	state.AddPending("approve")
	execCtx := NewExecutionContext(w.ctx, "task-1", "ctx-1", nil, a2a.UserProperties{UserID: "alice", TraceID: "trace-1"}, false)
	execCtx.SpendOrigin = agent.SpendOrigin{WorkflowName: "parent-wf", WorkflowNodeID: "child", ParentTaskID: "parent-task"}
	paused := &DAGExecutor{cfg: w.cfg, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}
	if err := w.pauseWorkflowNode(t.Context(), paused, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-1", RequestID: "req-1",
		Surface: []byte(`{"surfaceId":"s"}`), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("pauseWorkflowNode: %v", err)
	}
	exec := parkAndRebuild(t, w, paused, nil)
	cfg := &WorkflowConfig{
		Name:      "nightly-digest",
		Namespace: "ns",
		Nodes:     []*NodeConfig{{ID: "summarize", Type: "tool", ToolName: spendToolName}},
	}
	run := newToolNodeSpendRun(t, cfg, a2a.UserProperties{})
	run.e.execCtx = exec.Context

	run.execute(t, map[string]any{})

	row := run.filedRows(t, 1)[0]
	if !strIs(row.UserSub, "alice") || !strIs(row.TaskID, "task-1") || !strIs(row.TraceID, "trace-1") ||
		!strIs(row.ParentTaskID, "parent-task") || !strIs(row.WorkflowNodeID, "summarize") ||
		!strIs(row.TriggerKind, string(spend.TriggerWorkflow)) || !strIs(row.TriggerID, "parent-wf") {
		t.Errorf("row user=%s task=%s trace=%s parent=%s node=%s trigger=%s/%s, want alice's task-1 at summarize, delegated by parent-task of workflow parent-wf",
			strDeref(row.UserSub), strDeref(row.TaskID), strDeref(row.TraceID), strDeref(row.ParentTaskID),
			strDeref(row.WorkflowNodeID), strDeref(row.TriggerKind), strDeref(row.TriggerID))
	}
}
