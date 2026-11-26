// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendtest"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type spendServices struct {
	*testServices
}

func (s *spendServices) LLM(string) llm.Client { return spendtest.UsageLLM{} }

const envModelName = "gpt-env"

type spendSTR struct {
	brk     broker.Broker
	worker  *Worker
	ledger  *platstore.DB
	general platstore.ModelConfiguration
}

func startSpendSTR(t *testing.T, inProcess ...*samtoolsdk.ToolDef) *spendSTR {
	t.Helper()
	return startSpendSTRWith(t, nil, inProcess...)
}

func startSpendSTRWith(t *testing.T, setup func(*spendSTR, *testServices), inProcess ...*samtoolsdk.ToolDef) *spendSTR {
	t.Helper()
	return startSpendSTRConfigured(t, setup, nil, inProcess...)
}

func startSpendSTRConfigured(t *testing.T, setup func(*spendSTR, *testServices), configure func(*WorkerConfig), inProcess ...*samtoolsdk.ToolDef) *spendSTR {
	t.Helper()
	ledger := platstore.TestDB(t)
	brk := newTestBroker(t)
	spendtest.StartGovernor(t, brk, "test-ns", ledger)

	s := &spendSTR{brk: brk, ledger: ledger}
	s.general = s.createModel(t, defaultToolModelAlias)
	svc := &spendServices{testServices: &testServices{brk: brk, budgetGate: budgettest.NewGate()}}
	if setup != nil {
		setup(s, svc.testServices)
	}
	worker := newTestWorker(t, svc, writeSummarizeManifest(t))
	worker.cfg.InProcessTools = inProcess
	worker.cfg.EnvModelName = envModelName
	if configure != nil {
		configure(&worker.cfg)
	}
	if err := worker.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	s.worker = worker
	return s
}

func (s *spendSTR) createModel(t *testing.T, alias string) platstore.ModelConfiguration {
	t.Helper()
	price := pricing.Store(&pricing.ModelPrice{Input: mustParseAmount(t, "3"), Output: mustParseAmount(t, "15")})
	mc := platstore.ModelConfiguration{
		Alias: alias, Provider: "openai", ModelName: "gpt-test", ModelAuthType: "apikey",
		ModelAuthConfig: `{}`, ModelParams: `{}`, CreatedBy: "test", UpdatedBy: "test",
		PriceInput: price.Input, PriceOutput: price.Output,
	}
	if err := platstore.NewModelConfigRepo(s.ledger).Create(t.Context(), &mc); err != nil {
		t.Fatalf("create model %s: %v", alias, err)
	}
	return mc
}

func bindTo(svc *testServices, models ...platstore.ModelConfiguration) {
	byAlias := make(map[string]platstore.ModelConfiguration, len(models))
	for _, mc := range models {
		byAlias[mc.Alias] = mc
	}
	svc.bindings = bindings.New(
		bindings.WithClientBuilder(func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) {
			return spendtest.UsageLLM{}, nil
		}),
		bindings.WithFetcher(func(_ context.Context, alias string) (map[string]any, error) {
			mc, ok := byAlias[alias]
			if !ok {
				return nil, nil
			}
			return map[string]any{"model": mc.ModelName, samapp.ModelConfigIDKey: mc.ID}, nil
		}),
	)
}

func mustParseAmount(t *testing.T, s string) pricing.Amount {
	t.Helper()
	a, err := pricing.ParseAmount(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func writeSummarizeManifest(t *testing.T) string {
	t.Helper()
	manifest := fmt.Sprintf(`version: 1
tools:
  summarize:
    executable: %s
    parameters:
      type: object
      properties:
        text: {type: string}
`, getGoTestTool(t))
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (s *spendSTR) summarize(t *testing.T, params remotetool.InvokeParams) {
	t.Helper()
	params.ToolName = "summarize"
	params.Args = map[string]any{"text": "The quick brown fox."}
	s.invoke(t, params, nil)
}

func (s *spendSTR) invoke(t *testing.T, params remotetool.InvokeParams, props map[string]string) {
	t.Helper()
	replyQ := s.publishInvoke(t, params, props)
	recvCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	resp, err := s.brk.Receive(recvCtx, replyQ)
	if err != nil {
		t.Fatalf("no response: %v", err)
	}
	resp.Ack()
	if isErrorResponse(resp.Payload) {
		code, msg := parseErrorResponse(t, resp.Payload)
		t.Fatalf("%s failed: %s: %s", params.ToolName, code, msg)
	}
}

func (s *spendSTR) publishInvoke(t *testing.T, params remotetool.InvokeParams, props map[string]string) broker.Queue {
	t.Helper()
	replyTopic := "test-ns/reply/" + params.ToolName + "/" + idgen.New()
	replyQ, err := s.brk.CreateQueue(replyTopic, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := s.brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}
	reqID := idgen.New()
	payload, err := json.Marshal(a2a.NewRequest(reqID, remotetool.MethodInvoke, params))
	if err != nil {
		t.Fatal(err)
	}
	envelope := map[string]string{
		remotetool.PropReplyTo: replyTopic, remotetool.PropClientID: "test-agent", remotetool.PropUserID: params.UserID,
	}
	maps.Copy(envelope, props)
	if pubErr := s.brk.Publish(t.Context(), remotetool.InvokeTopic("test-ns", params.ToolName),
		&broker.Message{Payload: payload, UserProperties: envelope, CorrelationID: reqID}); pubErr != nil {
		t.Fatal(pubErr)
	}
	return replyQ
}

type toolSpendRow struct {
	UserID        *string `db:"user_id"`
	UserSub       *string `db:"user_sub"`
	AgentName     *string `db:"agent_name"`
	TriggerKind   *string `db:"trigger_kind"`
	Surface       string  `db:"surface"`
	ModelConfigID *string `db:"model_config_id"`
	TaskID        *string `db:"task_id"`
	TraceID       *string `db:"trace_id"`
	UncachedInput int64   `db:"uncached_input_tokens"`
	Output        int64   `db:"output_tokens"`
	OutputCost    *int64  `db:"output_cost"`
	ToolName      *string `db:"tool_name"`
	ModelKey      *string `db:"model_key"`
	ModelName     *string `db:"sent_model_name"`
	ParentTaskID  *string `db:"parent_task_id"`
	SessionID     *string `db:"session_id"`
}

func (s *spendSTR) filedRows(t *testing.T, want int) []toolSpendRow {
	t.Helper()
	return spendtest.FiledRows[toolSpendRow](t, s.ledger, "tool spend", want,
		`SELECT l.user_id, l.user_sub, l.agent_name, l.trigger_kind, l.surface, l.model_config_id, l.task_id,
			l.trace_id, l.uncached_input_tokens, l.output_tokens, l.output_cost,
			d.tool_name, d.model_key, d.sent_model_name, d.parent_task_id, d.session_id
		 FROM token_spend_ledger l JOIN token_spend_ledger_details d ON d.id = l.id`)
}

func is(got *string, want string) bool { return got != nil && *got == want }

func isEmpty(got *string) bool { return got == nil || *got == "" }

func TestSTRBooksAToolsLLMCallToTheInvokingAgentsTask(t *testing.T) {
	s := startSpendSTRWith(t, func(s *spendSTR, svc *testServices) { bindTo(svc, s.general) })
	userID := idgen.New()

	s.summarize(t, remotetool.InvokeParams{TaskID: idgen.New(), UserID: "alice"})

	s.summarize(t, remotetool.InvokeParams{
		TaskID: idgen.New(), UserID: "alice", ParentTaskID: "agent-task", SessionID: "sess-1",
		Spend: &remotetool.SpendAttribution{
			UserID: userID, AgentName: "researcher", TriggerKind: "chat", Channel: "webui",
			EntrypointName: "webui-gw", TaskID: "agent-task", ParentTaskID: "orchestrator-task",
			TraceID: "trace-1", SessionID: "sess-1",
		},
	})

	row := s.filedRows(t, 1)[0]
	if !is(row.UserID, userID) || row.UserSub != nil || !is(row.AgentName, "researcher") {
		t.Errorf("booked to user %v/%v agent %v, want %s and researcher", row.UserID, row.UserSub, row.AgentName, userID)
	}
	if row.Surface != "tool" || !is(row.ToolName, "summarize") || !is(row.TriggerKind, "chat") {
		t.Errorf("surface %s tool %v trigger %v, want tool/summarize/chat", row.Surface, row.ToolName, row.TriggerKind)
	}
	if !is(row.TaskID, "agent-task") || !is(row.ParentTaskID, "orchestrator-task") {
		t.Errorf("task %v parent %v, want agent-task delegated by orchestrator-task", row.TaskID, row.ParentTaskID)
	}
	if !is(row.TraceID, "trace-1") || !is(row.SessionID, "sess-1") {
		t.Errorf("trace %v session %v, want trace-1 sess-1", row.TraceID, row.SessionID)
	}
	if !is(row.ModelKey, defaultToolModelAlias) || !is(row.ModelConfigID, s.general.ID) || row.OutputCost == nil {
		t.Errorf("priced by key %v model %v at %v, want the general alias's model %s", row.ModelKey, row.ModelConfigID, row.OutputCost, s.general.ID)
	}
	if row.UncachedInput != 120 || row.Output != 30 {
		t.Errorf("tokens = %d/%d, want 120/30", row.UncachedInput, row.Output)
	}
}

func TestSTRBooksAToolsCallUnderTheModelAliasItsToolConfigNames(t *testing.T) {
	var custom platstore.ModelConfiguration
	s := startSpendSTRWith(t, func(s *spendSTR, svc *testServices) {
		custom = s.createModel(t, "custom")
		bindTo(svc, s.general, custom)
	})

	s.summarize(t, remotetool.InvokeParams{
		TaskID: idgen.New(), UserID: "alice", ToolConfig: map[string]any{ToolConfigModelAliasKey: "custom"},
		Spend: &remotetool.SpendAttribution{UserSub: "alice", TaskID: "agent-task"},
	})

	row := s.filedRows(t, 1)[0]
	if !is(row.ModelKey, "custom") || !is(row.ModelConfigID, custom.ID) || row.OutputCost == nil {
		t.Errorf("priced by key %v model %v at %v, want the custom alias's model %s", row.ModelKey, row.ModelConfigID, row.OutputCost, custom.ID)
	}
}

func TestSTRFilesACallTheEnvClientServedUnderNoModel(t *testing.T) {
	s := startSpendSTR(t)

	s.summarize(t, remotetool.InvokeParams{
		TaskID: idgen.New(), UserID: "alice",
		Spend: &remotetool.SpendAttribution{UserSub: "alice", TaskID: "agent-task"},
	})

	row := s.filedRows(t, 1)[0]
	if !isEmpty(row.ModelKey) || row.ModelConfigID != nil || row.OutputCost != nil {
		t.Errorf("filed under key %v model %v at %v, want no key, no model and no price", row.ModelKey, row.ModelConfigID, row.OutputCost)
	}
	if !is(row.ModelName, "openai/"+envModelName) {
		t.Errorf("filed as sent to %v, want the env client's openai/%s", row.ModelName, envModelName)
	}
	if row.Output != 30 {
		t.Errorf("output tokens = %d, want the env client's 30", row.Output)
	}
}

func TestSTRWarnsOnceThatAToolsLLMCallsCarryNoSpendAttribution(t *testing.T) {
	const quiet = "quiet_tool"
	td := samtoolsdk.NewTool(quiet, "Answers without the LLM.",
		func(context.Context, struct{}, *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			return samtoolsdk.OK("ok"), nil
		})
	var logs syncBuf
	s := startSpendSTRWith(t, func(_ *spendSTR, svc *testServices) {
		svc.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}, td)

	s.invoke(t, remotetool.InvokeParams{ToolName: quiet, TaskID: idgen.New(), UserID: "alice", Args: map[string]any{}}, nil)
	for range 2 {
		s.summarize(t, remotetool.InvokeParams{TaskID: idgen.New(), UserID: "alice", AppName: "old-agent"})
	}
	s.summarize(t, remotetool.InvokeParams{
		TaskID: idgen.New(), UserID: "alice", AppName: "old-agent", Spend: &remotetool.SpendAttribution{UserSub: "alice"},
	})

	out := logs.String()
	if n := strings.Count(out, missingSpendMessage); n != 1 {
		t.Errorf("warned %d times about the missing attribution, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "tool=summarize") || !strings.Contains(out, "agentName=old-agent") || strings.Contains(out, "tool="+quiet) {
		t.Errorf("warning names the wrong tool or agent, want summarize from old-agent only:\n%s", out)
	}
}

func TestSTRBooksAToolsSpendToTheVerifiedUserNotTheOneTheAttributionNames(t *testing.T) {
	userA, userB := idgen.New(), idgen.New()
	s := startSpendSTRWith(t, func(_ *spendSTR, svc *testServices) {
		svc.authenticator = &fakeAuthenticator{identity: &auth.Identity{UserID: "alice", PlatformUserID: userA}}
	})

	s.invoke(t, remotetool.InvokeParams{
		ToolName: "summarize", TaskID: idgen.New(), UserID: "bob", Args: map[string]any{"text": "x"},
		Spend: &remotetool.SpendAttribution{UserID: userB, UserSub: "bob", AgentName: "researcher", TaskID: "agent-task"},
	}, map[string]string{remotetool.PropAuthToken: "alice-token"})

	row := s.filedRows(t, 1)[0]
	if !is(row.UserID, userA) || row.UserSub != nil {
		t.Errorf("booked to user %v/%v, want the verified user's row %s", row.UserID, row.UserSub, userA)
	}
	if !is(row.AgentName, "researcher") || !is(row.TaskID, "agent-task") {
		t.Errorf("agent %v task %v, want the rest of the attribution as sent", row.AgentName, row.TaskID)
	}
}

func TestAStoppedSTRHandsOutNoSpendPublisherAndFilesNothingAfterTheStop(t *testing.T) {
	s := startSpendSTR(t)
	if s.worker.spendPublisher() == nil {
		t.Fatal("a running STR has no spend publisher")
	}

	if err := s.worker.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.worker.spendPublisher() != nil {
		t.Error("a stopped STR handed out a spend publisher")
	}
	call := s.worker.newToolLLMCallback(spendtest.UsageLLM{})
	if _, err := call(t.Context(), "", "summarize", &remotetool.InvokeParams{Spend: &remotetool.SpendAttribution{UserSub: "bob"}}, "s", "u", 0); err != nil {
		t.Fatalf("call after stop: %v", err)
	}

	s.filedRows(t, 0)
}

func TestSTRStampsTheConfigurationTheToolsAliasIsBoundTo(t *testing.T) {
	var bound platstore.ModelConfiguration
	s := startSpendSTRWith(t, func(s *spendSTR, svc *testServices) {
		bound = s.createModel(t, "general-bound")
		svc.bindings = bindings.New(
			bindings.WithClientBuilder(func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) {
				return spendtest.UsageLLM{}, nil
			}),
			bindings.WithFetcher(func(_ context.Context, alias string) (map[string]any, error) {
				if alias != defaultToolModelAlias {
					return nil, nil
				}
				return map[string]any{"model": bound.ModelName, samapp.ModelConfigIDKey: bound.ID}, nil
			}),
		)
	})

	call := s.worker.newToolLLMCallback(nil)
	if _, err := call(t.Context(), "", "summarize", &remotetool.InvokeParams{Spend: &remotetool.SpendAttribution{UserSub: "bob"}}, "s", "u", 0); err != nil {
		t.Fatalf("call: %v", err)
	}

	if row := s.filedRows(t, 1)[0]; !is(row.ModelConfigID, bound.ID) {
		t.Errorf("priced by %v, want the bound configuration %s rather than the one the key names", row.ModelConfigID, bound.ID)
	}
}

func TestSTRBooksAnInProcessToolsLLMCall(t *testing.T) {
	const toolName = "summarize_text"
	td := samtoolsdk.NewTool(toolName, "Summarizes text with the LLM.",
		func(ctx context.Context, _ struct{}, tc *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			if _, err := tc.CallLLM(ctx, "sys", "user", 0.2); err != nil {
				return samtoolsdk.Error(err.Error()), nil
			}
			return samtoolsdk.OK("ok"), nil
		})
	s := startSpendSTR(t, td)

	s.invoke(t, remotetool.InvokeParams{
		ToolName: toolName, TaskID: idgen.New(), UserID: "alice", Args: map[string]any{},
		Spend: &remotetool.SpendAttribution{UserSub: "alice", AgentName: "researcher", TaskID: "agent-task"},
	}, nil)

	row := s.filedRows(t, 1)[0]
	if !is(row.UserSub, "alice") || !is(row.ToolName, toolName) || !is(row.TaskID, "agent-task") {
		t.Errorf("booked to %v for tool %v task %v, want alice/%s/agent-task", row.UserSub, row.ToolName, row.TaskID, toolName)
	}
}

type chatModelSTR struct {
	*spendSTR
	model   string
	chatErr error
	opus    platstore.ModelConfiguration
}

const (
	chatModelToolName = "review_change"
	unbuildableAlias  = "unbuildable"
)

type clientlessBindings struct {
	*bindings.BindingsRegistry
}

func (b clientlessBindings) ResolveOrFetch(ctx context.Context, alias string) (llm.Client, error) {
	if alias == unbuildableAlias {
		return nil, errors.New("no client for this configuration")
	}
	return b.BindingsRegistry.ResolveOrFetch(ctx, alias)
}

func (b clientlessBindings) ResolveWithRawOrFetch(ctx context.Context, alias string) (llm.Client, map[string]any, error) {
	if alias == unbuildableAlias {
		return nil, nil, errors.New("no client for this configuration")
	}
	return b.BindingsRegistry.ResolveWithRawOrFetch(ctx, alias)
}

func startChatModelSTR(t *testing.T, enabled bool) *chatModelSTR {
	t.Helper()
	st := &chatModelSTR{}
	td := samtoolsdk.NewTool(chatModelToolName, "Reviews a change on a stronger model.",
		func(ctx context.Context, _ struct{}, tc *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			_, st.chatErr = tc.Chat(ctx, []samtoolsdk.ChatMessage{{Role: "user", Content: "review"}}, samtoolsdk.ChatOptions{Model: st.model})
			return samtoolsdk.OK("ok"), nil
		})
	st.spendSTR = startSpendSTRConfigured(t, func(s *spendSTR, svc *testServices) {
		st.opus = s.createModel(t, "opus")
		models := map[string]map[string]any{
			defaultToolModelAlias: {"model": s.general.ModelName, samapp.ModelConfigIDKey: s.general.ID},
			"opus":                {"model": st.opus.ModelName, samapp.ModelConfigIDKey: st.opus.ID},
			"jev":                 {"model": "typesafe", samapp.ModelTypeKey: samapp.DecisionModelType},
			unbuildableAlias:      {"model": "gpt-test"},
		}
		svc.bindings = clientlessBindings{BindingsRegistry: bindings.New(
			bindings.WithClientBuilder(func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) {
				return spendtest.UsageLLM{}, nil
			}),
			bindings.WithFetcher(func(_ context.Context, alias string) (map[string]any, error) {
				return models[alias], nil
			}),
		)}
	}, func(cfg *WorkerConfig) { cfg.ToolChatModel = enabled }, td)
	return st
}

func (st *chatModelSTR) chat(t *testing.T, model string, toolConfig map[string]any) {
	t.Helper()
	st.model = model
	st.invoke(t, remotetool.InvokeParams{
		ToolName: chatModelToolName, TaskID: idgen.New(), UserID: "alice", Args: map[string]any{},
		ToolConfig: toolConfig,
		Spend:      &remotetool.SpendAttribution{UserSub: "alice", TaskID: "agent-task"},
	}, nil)
}

func TestSTRBooksAToolsChatUnderTheModelAliasItNames(t *testing.T) {
	st := startChatModelSTR(t, true)

	st.chat(t, "opus", nil)
	if st.chatErr != nil {
		t.Fatalf("chat on opus: %v", st.chatErr)
	}
	row := st.filedRows(t, 1)[0]
	if !is(row.ModelKey, "opus") || !is(row.ModelConfigID, st.opus.ID) || row.OutputCost == nil {
		t.Errorf("priced by key %v model %v at %v, want the opus alias's model %s", row.ModelKey, row.ModelConfigID, row.OutputCost, st.opus.ID)
	}
}

func TestSTRRunsAnInProcessToolsNamedModelOnTheToolsModelWithTheFlagOff(t *testing.T) {
	st := startChatModelSTR(t, false)

	st.chat(t, "opus", nil)
	if st.chatErr != nil {
		t.Fatalf("chat on opus with the flag off: %v", st.chatErr)
	}
	if row := st.filedRows(t, 1)[0]; !is(row.ModelKey, defaultToolModelAlias) || !is(row.ModelConfigID, st.general.ID) {
		t.Errorf("priced by key %v model %v, want the tool's own model %s", row.ModelKey, row.ModelConfigID, st.general.ID)
	}
}

func TestSTRRefusesAChatModelAliasItCannotServe(t *testing.T) {
	st := startChatModelSTR(t, true)
	tests := []struct {
		name        string
		model       string
		toolConfig  map[string]any
		wantMessage string
	}{
		{name: "a decision model", model: "jev", wantMessage: `model alias "jev" is a decision model`},
		{name: "an alias that does not resolve", model: "missing", wantMessage: `model alias "missing" is not resolvable`},
		{name: "an alias whose client cannot be built", model: unbuildableAlias, wantMessage: `model alias "unbuildable" is not resolvable: no client`},
		{name: "an alias the toolset does not list", model: "opus", toolConfig: map[string]any{ToolConfigModelAliasesKey: "sonnet"},
			wantMessage: `model alias "opus" is not listed in tool_config "_model_aliases"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st.chat(t, tc.model, tc.toolConfig)
			llmErr, ok := errors.AsType[*samtoolsdk.LLMError](st.chatErr)
			if !ok || llmErr.Category != samtoolsdk.ChatErrorModelNotPermitted || llmErr.Code != ipcCodeInvalidParams || !strings.Contains(llmErr.Message, tc.wantMessage) {
				t.Errorf("chat on %q: error = %#v, want code %d category %s naming %q", tc.model, st.chatErr, ipcCodeInvalidParams, samtoolsdk.ChatErrorModelNotPermitted, tc.wantMessage)
			}
		})
	}
	if rows := st.filedRows(t, 0); len(rows) != 0 {
		t.Errorf("filed %d spend rows for refused calls, want none", len(rows))
	}
}

func TestAStoppingSTRFilesTheSpendOfAToolStillRunning(t *testing.T) {
	const toolName = "slow_summarize"
	started, release := make(chan struct{}), make(chan struct{})
	td := samtoolsdk.NewTool(toolName, "Summarizes text with the LLM once released.",
		func(ctx context.Context, _ struct{}, tc *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			close(started)
			<-release
			if _, err := tc.CallLLM(ctx, "sys", "user", 0.2); err != nil {
				return samtoolsdk.Error(err.Error()), nil
			}
			return samtoolsdk.OK("ok"), nil
		})
	s := startSpendSTR(t, td)

	s.publishInvoke(t, remotetool.InvokeParams{
		ToolName: toolName, TaskID: idgen.New(), UserID: "alice", Args: map[string]any{},
		Spend: &remotetool.SpendAttribution{UserSub: "alice", TaskID: "agent-task"},
	}, nil)
	<-started

	stopped := make(chan error, 1)
	go func() { stopped <- s.worker.Stop(context.Background()) }()
	for !s.worker.stopped.Load() {
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("stop: %v", err)
	}

	if row := s.filedRows(t, 1)[0]; !is(row.ToolName, toolName) || !is(row.TaskID, "agent-task") {
		t.Errorf("filed tool %v task %v, want %s/agent-task", row.ToolName, row.TaskID, toolName)
	}
}

type unreachableToolLLM struct{ t *testing.T }

func (m unreachableToolLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.t.Error("the tool's model was called while the budget was spent")
	return spendtest.UsageLLM{}.ChatCompletion(context.Background(), nil)
}

func (m unreachableToolLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("not streamed")
}

func TestSTRRefusesAToolsLLMCallWhileTheBudgetIsSpent(t *testing.T) {
	s := startSpendSTR(t)
	budgettest.BlockMesh(t, s.worker.svc.BudgetGate(), time.Now().Add(time.Hour))
	call := s.worker.newToolLLMCallback(unreachableToolLLM{t})

	_, err := call(t.Context(), "", "summarize", &remotetool.InvokeParams{Spend: &remotetool.SpendAttribution{UserSub: "bob"}}, "s", "u", 0)

	if !errors.Is(err, budget.ErrExhausted) {
		t.Errorf("tool call error = %v, want the budget refusal", err)
	}
	s.filedRows(t, 0)
}
