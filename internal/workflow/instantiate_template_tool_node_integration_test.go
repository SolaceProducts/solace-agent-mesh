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
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

const instReportBody = `# @@report_title@@

Prepared by @@prepared_by@@.

«««template_liquid: data="sales.json"
{% for row in items %}- {{ row.month }}: {{ row.revenue }}
{% endfor %}»»»
`

const instReportSidecar = `template:
  file: report.md
  output_filename: final_report.md
  mime_type: text/markdown
  description: Sales report
substitutions:
  report_title:
    description: Title
    default: "Untitled"
  prepared_by:
    description: Author
    required: true
data_inputs:
  sales:
    description: One row per month
    artifact: sales.json
    mime_type: application/json
    required: true
    schema:
      type: array
      items:
        type: object
        required: [month, revenue]
        properties:
          month: { type: string }
          revenue: { type: number }
`

func writeInstReportSkill(t *testing.T) (baseDir, skill string) {
	t.Helper()
	base := t.TempDir()
	skill = "report_skill"
	assets := filepath.Join(base, skill, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(base, skill, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", "---\nname: report_skill\n---\nReport skill.\n")
	samt, err := skillruntime.BuildTemplateZip("report.md", instReportBody, instReportSidecar)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "report.md.samt"), samt, 0o644); err != nil {
		t.Fatal(err)
	}
	return base, skill
}

type instTmplServices struct {
	*stubServices
	art *artifact.ScopedService
}

func (s *instTmplServices) Artifacts() *artifact.ScopedService { return s.art }

func loadInstArtifact(t *testing.T, art *artifact.ScopedService, agent, user, session, filename string) []byte {
	t.Helper()
	loc := art.Resolve(agent, user, session, filename)
	content, _, err := art.Inner.Load(context.Background(), loc, nil)
	if err != nil {
		t.Fatalf("load %q: %v", filename, err)
	}
	return content
}

func startInstSTRWorker(t *testing.T, svc *instTmplServices, baseDir, namespace string) {
	t.Helper()
	manifestDir := t.TempDir()
	manifestPath := filepath.Join(manifestDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worker := str.NewWorker(svc, str.WorkerConfig{
		WorkerID:      "inst-tmpl-str",
		Namespace:     namespace,
		ManifestPaths: []string{manifestPath},
		SkillsDir:     baseDir,
		Sandbox:       str.SandboxConfig{Mode: "direct", MaxConcurrentExecutions: 4},
	})
	worker.SetEmbedResolveCheck(func(ctx context.Context, candidate string, arts *artifact.ScopedService, agentName, userID, sessionID string) (string, error) {
		rctx := &embed.ResolveContext{
			Artifacts: arts,
			AgentName: agentName,
			UserID:    userID,
			SessionID: sessionID,
			Config: embed.ResolveConfig{
				MaxDepth:             12,
				MaxArtifactSizeBytes: embed.DefaultMaxArtifactSizeBytes,
				MaxTotalSizeBytes:    embed.DefaultMaxTotalSizeBytes,
			},
			Logger: slog.Default(),
		}
		resolved, _, rErr := embed.ResolveEmbedsInString(ctx, candidate, embed.ModeA2AMessageToUser, rctx)
		return resolved, rErr
	})
	if err := worker.Init(context.Background(), nil); err != nil {
		t.Fatalf("str worker init: %v", err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("str worker start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
}

func runInstantiateToolScenario(t *testing.T, salesJSON string, subs map[string]any) (*artifact.ScopedService, *WorkflowExecutionState) {
	t.Helper()

	baseDir, skill := writeInstReportSkill(t)
	t.Setenv("SAM_SKILLS_DIR", baseDir)

	art := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	svc := &instTmplServices{stubServices: &stubServices{brk: brk}, art: art}
	startInstSTRWorker(t, svc, baseDir, "ns")

	buildInput := map[string]any{
		"skill_name":  skill,
		"asset":       "report.md.samt",
		"data_inputs": map[string]any{"sales": "{{ make_data.output.filename }}"},
	}
	if len(subs) > 0 {
		buildInput["substitutions"] = subs
	}

	cfg := &WorkflowConfig{
		Name:          "wf",
		Namespace:     "ns",
		FailFast:      true,
		OutputMapping: map[string]any{"report": "{{ build.output.filename }}"},
		Tools: []any{
			map[string]any{"tool_type": "builtin", "tool_name": "produce_data"},
			map[string]any{"tool_type": "builtin", "tool_name": "instantiate_template"},
		},
		Nodes: []*NodeConfig{
			{ID: "make_data", Type: "tool", ToolName: "produce_data"},
			{
				ID: "build", Type: "tool", DependsOn: []string{"make_data"},
				ToolName: "instantiate_template",
				Input:    buildInput,
			},
		},
	}

	w := newTestWorkflowInstance(cfg, svc)
	w.SetToolInitOptions(toolinit.Options{
		AdditionalBuiltin: func(name string, _ map[string]any) tool.BuiltinTool {
			if name == "produce_data" {
				return &instProduceDataTool{filename: "raw_sales.json", content: salesJSON, mime: "application/json"}
			}
			return nil
		},
	})
	if err := w.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := w.startSubComponents(context.Background()); err != nil {
		t.Fatalf("start sub-components: %v", err)
	}
	t.Cleanup(w.stopSubComponents)

	state := NewExecutionState(cfg.Name, "exec")
	state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{}}
	execCtx := NewExecutionContext(context.Background(), "task-t", "ctx-t", nil, a2a.UserProperties{UserID: "u1"}, false)
	t.Cleanup(execCtx.Cancel)
	e := &DAGExecutor{cfg: cfg, dag: w.dag, state: state, execCtx: execCtx, instance: w, logger: slog.Default()}
	w.timerManager = NewInProcessTimerManager(slog.Default(), func(id string, ev Event) {
		if id == state.ExecutionID {
			_ = e.Advance(context.Background(), ev)
		}
	})
	go w.timerManager.Start(execCtx.Ctx)

	if err := e.Advance(context.Background(), &EventStart{TaskID: "task-t", ContextID: "ctx-t", Input: map[string]any{}}); err != nil {
		t.Fatalf("advance start: %v", err)
	}
	waitForFinalize(t, e, state)
	return art, state
}

func TestInstantiateTemplateToolNode_RealSTR_SuccessfulInstantiation(t *testing.T) {
	art, state := runInstantiateToolScenario(t,
		`[{"month":"Jan","revenue":100},{"month":"Feb","revenue":250}]`,
		map[string]any{"report_title": "My Report", "prepared_by": "Casey"})

	if state.ErrorState != nil {
		t.Fatalf("workflow failed unexpectedly: %+v", state.ErrorState)
	}
	if _, ok := state.CompletedNodes["build"]; !ok {
		t.Fatalf("instantiate_template node did not complete; completed=%v", state.CompletedNodes)
	}

	if staged := loadInstArtifact(t, art, "wf", "u1", "ctx-t", "sales.json"); staged != nil {
		t.Fatalf("binding must not stage a copy under sales.json; got:\n%s", staged)
	}

	content := loadInstArtifact(t, art, "wf", "u1", "ctx-t", "final_report.md")
	got := string(content)
	if !strings.Contains(got, "# My Report") || !strings.Contains(got, "Prepared by Casey.") {
		t.Fatalf("substitutions not applied; artifact:\n%s", got)
	}
	if !strings.Contains(got, "template_liquid: data=\"raw_sales.json:0\"") {
		t.Fatalf("template_liquid block not preserved with a pinned bound reference; artifact:\n%s", got)
	}
}

func TestInstantiateTemplateToolNode_RealSTR_ContractFailure_FailsNode(t *testing.T) {
	_, state := runInstantiateToolScenario(t,
		`[{"month":"Jan"},{"month":"Feb"}]`,
		map[string]any{"prepared_by": "Casey"})

	if state.ErrorState == nil {
		t.Fatal("expected the workflow to fail on a contract violation, but it succeeded")
	}
	if _, ok := state.CompletedNodes["build"]; ok {
		t.Fatal("instantiate_template node completed despite a contract violation; the failure was swallowed")
	}
}

type instProduceDataTool struct{ filename, content, mime string }

func (t *instProduceDataTool) Name() string                                      { return "produce_data" }
func (t *instProduceDataTool) Description() string                               { return "test data producer" }
func (t *instProduceDataTool) Instructions() string                              { return "" }
func (t *instProduceDataTool) IsLongRunning() bool                               { return false }
func (t *instProduceDataTool) RequiredScopes() []string                          { return nil }
func (t *instProduceDataTool) RawStringArgs() []string                           { return nil }
func (t *instProduceDataTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (t *instProduceDataTool) ContextParam() string                              { return "" }
func (t *instProduceDataTool) Parameters() *tool.JSONSchema                      { return &tool.JSONSchema{Type: "object"} }

func (t *instProduceDataTool) Execute(ctx context.Context, _ map[string]any, tc *tool.Context) (*tool.Result, error) {
	_, err := artifact.SaveWithMetadata(ctx, tc.ArtifactService, tc.AgentName, tc.UserID, tc.SessionID,
		t.filename, []byte(t.content), t.mime, artifact.MetadataOpts{Description: "test data", SuppressSchema: true}, slog.Default())
	if err != nil {
		return tool.Error(err.Error()), nil
	}
	return tool.OK("produced", tool.WithData(map[string]any{"filename": t.filename})), nil
}
