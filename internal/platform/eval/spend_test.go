// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendtest"
)

const defaultJudgeAlias = "general"

type judgeSpendRow struct {
	UserID         *string `db:"user_id"`
	UserSub        *string `db:"user_sub"`
	EntrypointName *string `db:"entrypoint_name"`
	Channel        *string `db:"channel"`
	TriggerKind    *string `db:"trigger_kind"`
	TriggerID      *string `db:"trigger_id"`
	TaskID         *string `db:"task_id"`
	TraceID        *string `db:"trace_id"`
	Surface        string  `db:"surface"`
	ModelConfigID  *string `db:"model_config_id"`
	OutputCost     *int64  `db:"output_cost"`
}

type judgedRun struct {
	db            *store.DB
	configIDs     map[string]string
	judgeProvider *judgeProbeServer

	mu       sync.Mutex
	traceIDs []string
}

func (j *judgedRun) observeAgentRequest(msg *broker.Message) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.traceIDs = append(j.traceIDs, msg.UserProperties[a2a.PropTraceID])
}

func (j *judgedRun) agentTraceIDs() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.traceIDs)
}

func pricedModel(t *testing.T, db *store.DB, alias string) string {
	t.Helper()
	price := pricing.Store(&pricing.ModelPrice{Input: mustAmount(t, "3"), Output: mustAmount(t, "15")})
	mc := store.ModelConfiguration{
		Alias: alias, Provider: "openai", ModelName: alias, ModelAuthType: "apikey",
		ModelAuthConfig: `{}`, ModelParams: `{}`, CreatedBy: "test", UpdatedBy: "test",
		PriceInput: price.Input, PriceOutput: price.Output,
	}
	if err := store.NewModelConfigRepo(db).Create(t.Context(), &mc); err != nil {
		t.Fatalf("create model %s: %v", alias, err)
	}
	return mc.ID
}

func mustAmount(t *testing.T, s string) pricing.Amount {
	t.Helper()
	a, err := pricing.ParseAmount(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func boundClientTo(t *testing.T, url string) llm.Client {
	t.Helper()
	const keyEnv = "SAMTEST_JUDGE_SPEND_OPENAI_KEY"
	_ = os.Setenv(keyEnv, "test-key") //nolint:usetesting // parallel via store.TestDB; key is unique to this helper
	c, err := llm.NewBifrostClient(t.Context(), llm.BifrostConfig{
		Provider: "openai", Model: "bound-model", BaseURL: url, APIKeyEnvVar: keyEnv,
	})
	if err != nil {
		t.Fatalf("build bound client: %v", err)
	}
	t.Cleanup(c.Shutdown)
	return c
}

func runJudgedExperiment(t *testing.T, evaluatorModel, runAs *string) *judgedRun {
	t.Helper()
	db := store.TestDB(t)
	br := newConnectedBroker(t)
	configIDs := map[string]string{
		defaultJudgeAlias: pricedModel(t, db, defaultJudgeAlias),
		"judge-cfg":       pricedModel(t, db, "judge-cfg"),
	}
	publisher := fileSpendInto(t, db, br)

	boundProvider := newJudgeProbeServer(t, `{"choice":"Yes","reasoning":"bound"}`)
	dedicatedProvider := newJudgeProbeServer(t, `{"choice":"Yes","reasoning":"dedicated"}`)
	run := &judgedRun{db: db, configIDs: configIDs, judgeProvider: dedicatedProvider}
	runJudgedExample(t, db, br, judgedExample{
		evaluatorModel: evaluatorModel,
		runAs:          runAs,
		llmClient:      NewLLMJudgeAdapter(boundClientTo(t, boundProvider.srv.URL)),
		judgeResolver: &configJudgeResolver{configs: map[string]resolvedJudge{
			"judge-cfg":       {raw: providerRaw(dedicatedProvider.srv.URL), configID: configIDs["judge-cfg"], dedicated: true},
			defaultJudgeAlias: {raw: providerRaw(boundProvider.srv.URL), configID: configIDs[defaultJudgeAlias]},
		}},
		defaultModelAlias: defaultJudgeAlias,
		spendPublisher:    publisher,
		onAgentRequest:    run.observeAgentRequest,
	})
	return run
}

func fileSpendInto(t *testing.T, db *store.DB, br broker.Broker) *spend.Publisher {
	t.Helper()
	spendtest.StartGovernor(t, br, "test-ns", db)
	publisher := spend.NewPublisher(br, "test-ns", slog.Default())
	t.Cleanup(func() { _ = publisher.Close(context.Background()) })
	return publisher
}

type resolvedJudge struct {
	raw       map[string]any
	configID  string
	dedicated bool
}

type configJudgeResolver struct{ configs map[string]resolvedJudge }

func (c *configJudgeResolver) Resolve(_ context.Context, ref string) (map[string]any, string, bool, error) {
	r, ok := c.configs[ref]
	if !ok {
		return nil, "", false, nil
	}
	return r.raw, r.configID, r.dedicated, nil
}

func (j *judgedRun) judgeRows(t *testing.T, want int) []judgeSpendRow {
	t.Helper()
	return judgeRowsIn(t, j.db, want)
}

func judgeRowsIn(t *testing.T, db *store.DB, want int) []judgeSpendRow {
	t.Helper()
	return spendtest.FiledRows[judgeSpendRow](t, db, "judge spend", want,
		`SELECT user_id, user_sub, entrypoint_name, channel, trigger_kind, trigger_id, task_id, trace_id, surface,
			model_config_id, output_cost
		 FROM token_spend_ledger WHERE surface = ?`, string(spend.SurfaceEvalJudge))
}

func assertJudgeRow(t *testing.T, row judgeSpendRow, wantConfigID string) {
	t.Helper()
	if row.UserID == nil || *row.UserID != rbac.EvalSystemUserID || row.UserSub != nil {
		t.Errorf("booked to user %v/%v, want the eval system user %s", row.UserID, row.UserSub, rbac.EvalSystemUserID)
	}
	if row.TriggerKind == nil || *row.TriggerKind != string(spend.TriggerEval) || row.TriggerID != nil {
		t.Errorf("trigger = %v/%v, want kind eval with no id, as the run's agent turns carry", row.TriggerKind, row.TriggerID)
	}
	if row.Channel == nil || *row.Channel != a2a.ChannelEval || row.EntrypointName == nil || *row.EntrypointName != evalClientID {
		t.Errorf("entered through %v on %v, want %s on %s", row.EntrypointName, row.Channel, evalClientID, a2a.ChannelEval)
	}
	if row.TaskID == nil {
		t.Error("judge row carries no task id")
	}
	if row.ModelConfigID == nil || *row.ModelConfigID != wantConfigID || row.OutputCost == nil {
		t.Errorf("priced by model %v at %v, want %s", row.ModelConfigID, row.OutputCost, wantConfigID)
	}
}

func TestJudgeBooksItsSpendToTheEvalSystemUserWhateverTheRunPrincipalIs(t *testing.T) {
	dedicated := "judge-cfg"
	for name, runAs := range map[string]*string{"default principal": nil, "custom principal": new("system:qa-bot")} {
		t.Run(name, func(t *testing.T) {
			run := runJudgedExperiment(t, &dedicated, runAs)

			row := run.judgeRows(t, 1)[0]
			assertJudgeRow(t, row, run.configIDs["judge-cfg"])
			if traces := run.agentTraceIDs(); len(traces) != 1 || traces[0] == "" || !holds(row.TraceID, traces[0]) {
				t.Errorf("judge row trace = %v, want the trace %v its trial's task was published under", row.TraceID, traces)
			}
		})
	}
}

func TestJudgeSpendIsPricedByTheConfigurationThatServedIt(t *testing.T) {
	general := defaultJudgeAlias
	for name, evaluatorModel := range map[string]*string{
		"the evaluator names no model":          nil,
		"the evaluator names the general model": &general,
	} {
		t.Run(name, func(t *testing.T) {
			run := runJudgedExperiment(t, evaluatorModel, nil)

			assertJudgeRow(t, run.judgeRows(t, 1)[0], run.configIDs[defaultJudgeAlias])
		})
	}
}

func TestJudgeSpendOfABareModelIsRecordedUnpriced(t *testing.T) {
	bare := "bare-model"
	run := runJudgedExperiment(t, &bare, nil)

	row := run.judgeRows(t, 1)[0]
	if row.ModelConfigID != nil || row.OutputCost != nil {
		t.Errorf("judge row priced by %v at %v, want unpriced", row.ModelConfigID, row.OutputCost)
	}
}

type spendCapturingClient struct {
	mu    sync.Mutex
	spend []*llm.SpendAttribution
}

func (c *spendCapturingClient) ChatCompletion(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spend = append(c.spend, req.Spend)
	return &llm.ChatResponse{Content: `{"choice":"A"}`}, nil
}

func (c *spendCapturingClient) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("not implemented")
}

func TestAJudgeFallingBackToTheDefaultModelIsPricedByIt(t *testing.T) {
	cs := `{"Yes":1.0,"No":0.0}`
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	resolver := &configJudgeResolver{configs: map[string]resolvedJudge{
		defaultJudgeAlias: {raw: map[string]any{"model": "openai/general-model"}, configID: "general-id"},
	}}
	for name, envModel := range map[string]string{
		"without LLM_SERVICE_GENERAL_MODEL_NAME":  "",
		"with LLM_SERVICE_GENERAL_MODEL_NAME set": "env-model",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", envModel)
			provider := &spendCapturingClient{}
			judge := newRunJudgeClient(NewLLMJudgeAdapter(provider), resolver, slog.Default(), 0)

			out := ScoreResult(t.Context(), slog.Default(), ScoreInputs{
				Evaluator:       &store.EvalEvaluator{Name: "qa", Type: "llm_judge", PromptTemplate: &tmpl, ChoiceScores: &cs},
				Prompt:          "Q?",
				Response:        "A.",
				DefaultModel:    "openai/general-model",
				DefaultModelKey: defaultJudgeAlias,
				DefaultModelID:  "general-id",
				Spend:           judgeSpend(&pendingTask{taskID: "task-1"}),
			}, judge)

			if out.Errored || len(provider.spend) != 1 {
				t.Fatalf("judge errored %t after %d calls, want one graded call", out.Errored, len(provider.spend))
			}
			if got := provider.spend[0]; got.ModelKey != defaultJudgeAlias || got.ModelConfigID != "general-id" {
				t.Errorf("judge call keyed %q priced by %q, want %q and %q", got.ModelKey, got.ModelConfigID, defaultJudgeAlias, "general-id")
			}
		})
	}
}

type billedFailure struct{}

func (billedFailure) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, &llm.Error{Message: "upstream timeout", StatusCode: 504, BilledUsage: &llm.Usage{PromptTokens: 40, CompletionTokens: 7}}
}

func (billedFailure) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("not implemented")
}

func TestAJudgeCallThatFailsAfterTheProviderBilledItIsRecorded(t *testing.T) {
	db := store.TestDB(t)
	publisher := fileSpendInto(t, db, newConnectedBroker(t))
	judge := newRunJudgeClient(NewLLMJudgeAdapter(billedFailure{}), nil, slog.Default(), 0)
	judge.meterOn(publisher, budgettest.NewGate())

	if _, err := judge.JudgeScore(t.Context(), "bare-model", "sys", "user", judgeSpend(&pendingTask{taskID: "task-1", traceID: "trace-1"})); err == nil {
		t.Fatal("judge call succeeded, want the provider's failure")
	}

	row := judgeRowsIn(t, db, 1)[0]
	if !holds(row.TaskID, "task-1") || !holds(row.TraceID, "trace-1") {
		t.Errorf("failed judge call filed under task %v trace %v, want task-1 trace-1", row.TaskID, row.TraceID)
	}
}

type opaqueJudge struct{}

func (opaqueJudge) JudgeScore(context.Context, string, string, string, *llm.SpendAttribution) (string, error) {
	return "", nil
}

func TestAJudgeWhoseSpendCannotBeRecordedSaysSo(t *testing.T) {
	var logs bytes.Buffer
	judge := newRunJudgeClient(opaqueJudge{}, nil, slog.New(slog.NewTextHandler(&logs, nil)), 0)

	publisher := spend.NewPublisher(newConnectedBroker(t), "test-ns", slog.Default())
	t.Cleanup(func() { _ = publisher.Close(context.Background()) })

	judge.meterOn(publisher, nil)

	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "judge spend not recorded") {
		t.Errorf("want a WARN that the judge's spend goes unrecorded:\n%s", logs.String())
	}
}

func holds(got *string, want string) bool {
	return got != nil && *got == want
}
