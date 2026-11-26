// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func TestAJudgeCallIsRefusedWithoutReachingTheModelWhileTheBudgetIsSpent(t *testing.T) {
	model := mockllm.NewFIFOClient(nil)
	judge := newRunJudgeClient(NewLLMJudgeAdapter(model), nil, slog.Default(), 0)
	judge.meterOn(nil, budgettest.BlockedGate(t, time.Now().Add(time.Hour)))

	_, err := judge.JudgeScore(t.Context(), "bare-model", "sys", "user", judgeSpend(&pendingTask{taskID: "task-1"}))

	if !errors.Is(err, budget.ErrExhausted) {
		t.Errorf("judge error = %v, want the budget refusal", err)
	}
	if n := len(model.Recorder().Requests()); n != 0 {
		t.Errorf("the judge model received %d requests while the budget was spent, want 0", n)
	}
}

type countingDecisionEvaluator struct{ calls int }

func (e *countingDecisionEvaluator) Evaluate(context.Context, decision.Request) (decision.Result, error) {
	e.calls++
	return decision.Result{}, nil
}

func TestADecisionEvaluationIsRefusedWithoutReachingTheProviderWhileTheBudgetIsSpent(t *testing.T) {
	provider := &countingDecisionEvaluator{}
	clients := newRunDecisionClients(nil, nil, slog.Default())
	clients.meterOn(nil, budgettest.BlockedGate(t, time.Now().Add(time.Hour)))

	_, err := clients.recorded(provider, nil).Evaluate(t.Context(), decision.Request{})

	if !errors.Is(err, budget.ErrExhausted) {
		t.Errorf("evaluation error = %v, want the budget refusal", err)
	}
	if provider.calls != 0 {
		t.Errorf("the provider was called %d times while the budget was spent, want 0", provider.calls)
	}
}

func TestARunStartedWhileTheBudgetIsSpentFailsWithoutDispatchingACase(t *testing.T) {
	var agentRequests broker.Queue
	var agentBroker broker.Broker
	f := startEvalRun(t, evalRunOpts{
		prompts:    []string{"first", "second"},
		budgetGate: budgettest.BlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)),
		startAgent: func(br broker.Broker, namespace string) {
			agentBroker = br
			agentRequests = budgettest.TapAgentRequests(t, br, namespace, "Orchestrator")
		},
	})

	run := waitForTerminalRun(t, f.runRepo, f.runID)

	if run.Status != StatusFailed {
		t.Errorf("run status = %q, want %q", run.Status, StatusFailed)
	}
	if run.ErrorMessage == nil || !strings.Contains(*run.ErrorMessage, "this deployment's usage budget is used up until 1 November 2099") {
		t.Errorf("run error = %v, want the deployment's budget named", run.ErrorMessage)
	}
	budgettest.AssertNotDelivered(t, agentBroker, agentRequests)
}

func TestARunRefusedByItsSystemUsersOwnBudgetNamesTheUserAndWhetherTheDefaultBudgetHoldsIt(t *testing.T) {
	const (
		resets      = "the usage budget is used up until 1 November 2099 00:00 UTC for the service account "
		heldDefault = "which has no budget of its own and so is held to the default user budget"
		nightlyID   = "0b9c4c2e-6f2a-4d47-9a51-3b8e1f6d2c10"
	)
	resetsAt := time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)
	nightly := "system:nightly"
	for name, c := range map[string]struct {
		runAs       *string
		gate        func(t *testing.T) *budget.Gate
		assigned    []string
		wantNamed   string
		wantDefault bool
	}{
		"eval on the default budget": {
			gate:        func(t *testing.T) *budget.Gate { return budgettest.DefaultUsersBlockedGate(t, resetsAt) },
			wantNamed:   resets + `"eval" this run runs as`,
			wantDefault: true,
		},
		"eval on its own budget": {
			gate: func(t *testing.T) *budget.Gate {
				return budgettest.UserBlockedGate(t, resetsAt, budget.UserKey(rbac.EvalSystemUserID, rbac.EvalSystemUserSub))
			},
			assigned:  []string{rbac.EvalSystemUserID},
			wantNamed: resets + `"eval" this run runs as`,
		},
		"a custom service account on the default budget": {
			runAs:       &nightly,
			gate:        func(t *testing.T) *budget.Gate { return budgettest.DefaultUsersBlockedGate(t, resetsAt) },
			wantNamed:   resets + `"nightly" this run runs as`,
			wantDefault: true,
		},
		"a custom service account on its own budget": {
			runAs: &nightly,
			gate: func(t *testing.T) *budget.Gate {
				return budgettest.UserBlockedGate(t, resetsAt, budget.UserKey(nightlyID, nightly))
			},
			assigned:  []string{nightlyID},
			wantNamed: resets + `"nightly" this run runs as`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var agentRequests broker.Queue
			var agentBroker broker.Broker
			f := startEvalRun(t, evalRunOpts{
				prompts:        []string{"first"},
				runAs:          c.runAs,
				systemUsers:    map[string]string{nightly: nightlyID},
				budgetGate:     c.gate(t),
				budgetAssigned: c.assigned,
				startAgent: func(br broker.Broker, namespace string) {
					agentBroker = br
					agentRequests = budgettest.TapAgentRequests(t, br, namespace, "Orchestrator")
				},
			})

			run := waitForTerminalRun(t, f.runRepo, f.runID)

			if run.Status != StatusFailed || run.ErrorMessage == nil {
				t.Fatalf("run = %q %v, want failed with a refusal", run.Status, run.ErrorMessage)
			}
			if !strings.Contains(*run.ErrorMessage, c.wantNamed) {
				t.Errorf("run error = %q, want the service account named: %q", *run.ErrorMessage, c.wantNamed)
			}
			if got := strings.Contains(*run.ErrorMessage, heldDefault); got != c.wantDefault {
				t.Errorf("run error = %q; names the default budget = %v, want %v", *run.ErrorMessage, got, c.wantDefault)
			}
			budgettest.AssertNotDelivered(t, agentBroker, agentRequests)
		})
	}
}

func TestARunRefusedByTheMeshBudgetLooksUpNoAssignment(t *testing.T) {
	var lookups atomic.Int32
	counted := assignmentCounterFunc(func() (int64, error) {
		lookups.Add(1)
		return 0, nil
	})
	s := &ExecutionService{
		budgetGate:        budgettest.BlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)),
		budgetAssignments: counted,
		logger:            slog.New(slog.DiscardHandler),
	}

	refusal := s.runBudgetRefusal(t.Context(), "run-1", &store.EvalRun{}, nil)

	if !strings.Contains(refusal, "this deployment's usage budget is used up") {
		t.Errorf("refusal = %q, want the deployment's budget named", refusal)
	}
	if n := lookups.Load(); n != 0 {
		t.Errorf("budget assignments were looked up %d times for a mesh refusal, want 0", n)
	}
}

type assignmentCounterFunc func() (int64, error)

func (f assignmentCounterFunc) Count(context.Context, store.BudgetAssignmentFilter) (int64, error) {
	return f()
}

func TestARefusalClaimsTheDefaultBudgetOnlyWhenItKnowsNoBudgetIsAssigned(t *testing.T) {
	none := assignmentCounterFunc(func() (int64, error) { return 0, nil })
	failing := assignmentCounterFunc(func() (int64, error) { return 0, errors.New("database unavailable") })
	for name, c := range map[string]struct {
		assignments BudgetAssignmentCounter
		userID      string
	}{
		"no way to look assignments up": {userID: rbac.EvalSystemUserID},
		"a lookup that fails":           {assignments: failing, userID: rbac.EvalSystemUserID},
		"a user with no id":             {assignments: none},
	} {
		t.Run(name, func(t *testing.T) {
			s := &ExecutionService{budgetAssignments: c.assignments, logger: slog.New(slog.DiscardHandler)}
			if s.heldToDefaultBudget(t.Context(), "run-1", c.userID) {
				t.Error("heldToDefaultBudget = true, want false when it cannot tell")
			}
		})
	}
}

type budgetRefusingJudge struct {
	refusal error
	calls   atomic.Int32
}

func (j *budgetRefusingJudge) JudgeScore(context.Context, string, string, string, *llm.SpendAttribution) (string, error) {
	j.calls.Add(1)
	return "", j.refusal
}

func assertNoWarning(t *testing.T, logs *strings.Builder) {
	t.Helper()
	if strings.Contains(logs.String(), "level=WARN") || strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a budget refusal was logged as a fault:\n%s", logs)
	}
}

func TestADecisionCaseTheBudgetRefusedIsErroredWithoutBlamingTheProvider(t *testing.T) {
	refusal := budgettest.BlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)).Check("")
	judge := &countingJudge{}
	var logs strings.Builder
	out := ScoreResult(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)), ScoreInputs{
		Evaluator: decisionEvaluatorRow(t, baseDecisionConfig(true)), Prompt: "What is 2+2?", Response: "4", Expected: new("4"),
		Decision: &fakeDecision{err: refusal}, DecisionEnabled: true,
	}, judge)

	if !out.Errored || out.Reasoning == nil || !strings.Contains(*out.Reasoning, "Not scored: this deployment's usage budget is used up until 1 November 2099") {
		t.Fatalf("out = %+v, want an errored score carrying the refusal", out)
	}
	if out.Details != nil && (out.Details.ProviderError != "" || out.Details.Escalated) {
		t.Errorf("details = %+v, want no provider failure and no escalation", out.Details)
	}
	if n := judge.calls.Load(); n != 0 {
		t.Errorf("the escalation judge was called %d times, want 0", n)
	}
	assertNoWarning(t, &logs)
}

func TestAJudgeCaseRefusedOnTheJudgesOwnBudgetSaysSo(t *testing.T) {
	judgeUser := budget.UserKey(rbac.EvalSystemUserID, rbac.EvalSystemUserSub)
	refusal := budgettest.UserBlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC), judgeUser).Check(judgeUser)
	cs := `{"Yes":1,"No":0}`
	ev := &store.EvalEvaluator{ID: "j", Name: "quality", Type: "llm_judge", Model: new("gpt"),
		PromptTemplate: new("Grade {{Response}}"), ChoiceScores: &cs}

	out := ScoreResult(t.Context(), slog.New(slog.DiscardHandler), ScoreInputs{
		Evaluator: ev, Prompt: "What is 2+2?", Response: "4",
	}, &budgetRefusingJudge{refusal: refusal})

	if !out.Errored || out.Reasoning == nil || !strings.Contains(*out.Reasoning, "Not scored: the usage budget is used up until 1 November 2099 00:00 UTC for the service account \"eval\" the eval judge runs as") {
		t.Fatalf("out = %+v, want the judge's own budget named", out)
	}
}

func TestAJudgeCaseTheBudgetRefusedIsErroredWithoutAWarning(t *testing.T) {
	refusal := budgettest.BlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)).Check("")
	cs := `{"Yes":1,"No":0}`
	ev := &store.EvalEvaluator{ID: "j", Name: "quality", Type: "llm_judge", Model: new("gpt"),
		PromptTemplate: new("Grade {{Response}}"), ChoiceScores: &cs}
	var logs strings.Builder

	out := ScoreResult(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)), ScoreInputs{
		Evaluator: ev, Prompt: "What is 2+2?", Response: "4",
	}, &budgetRefusingJudge{refusal: refusal})

	if !out.Errored || out.Reasoning == nil || !strings.Contains(*out.Reasoning, "Not scored: this deployment's usage budget is used up until 1 November 2099") {
		t.Fatalf("out = %+v, want an errored score carrying the refusal", out)
	}
	assertNoWarning(t, &logs)
}

func TestAnLLMJudgedRunFailsWithoutDispatchingACaseWhileTheJudgesBudgetIsSpent(t *testing.T) {
	var agentRequests broker.Queue
	var agentBroker broker.Broker
	runAs := "system:nightly"
	f := startEvalRun(t, evalRunOpts{
		prompts:     []string{"first"},
		runAs:       &runAs,
		systemUsers: map[string]string{runAs: "0b9c4c2e-6f2a-4d47-9a51-3b8e1f6d2c10"},
		llmJudge:    true,
		budgetGate: budgettest.UserBlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC),
			budget.UserKey(rbac.EvalSystemUserID, rbac.EvalSystemUserSub)),
		startAgent: func(br broker.Broker, namespace string) {
			agentBroker = br
			agentRequests = budgettest.TapAgentRequests(t, br, namespace, "Orchestrator")
		},
	})

	run := waitForTerminalRun(t, f.runRepo, f.runID)

	if run.Status != StatusFailed {
		t.Errorf("run status = %q, want %q", run.Status, StatusFailed)
	}
	if run.ErrorMessage == nil || !strings.Contains(*run.ErrorMessage, "the usage budget is used up until 1 November 2099 00:00 UTC for the service account \"eval\" the eval judge runs as") {
		t.Errorf("run error = %v, want the judge's budget named", run.ErrorMessage)
	}
	budgettest.AssertNotDelivered(t, agentBroker, agentRequests)
}

func TestGradingCallsAModelOnlyForAJudgeOrAnEnabledDecisionEvaluator(t *testing.T) {
	heuristic := &store.EvalEvaluator{Type: evaluatorTypeHeuristic}
	judge := &store.EvalEvaluator{Type: evaluatorTypeLLMJudge}
	decisionEv := &store.EvalEvaluator{Type: evaluatorTypeDecision}
	cases := []struct {
		name            string
		evaluators      []*store.EvalEvaluator
		decisionEnabled bool
		want            bool
	}{
		{"heuristics only", []*store.EvalEvaluator{heuristic}, true, false},
		{"an LLM judge", []*store.EvalEvaluator{heuristic, judge}, false, true},
		{"a decision evaluator turned on", []*store.EvalEvaluator{decisionEv}, true, true},
		{"a decision evaluator turned off", []*store.EvalEvaluator{decisionEv}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gradingCallsAModel(c.evaluators, c.decisionEnabled); got != c.want {
				t.Errorf("gradingCallsAModel = %v, want %v", got, c.want)
			}
		})
	}
}
