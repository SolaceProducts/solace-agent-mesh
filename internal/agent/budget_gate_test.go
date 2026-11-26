// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var budgetResetsAt = time.Date(2099, time.November, 1, 0, 0, 0, 0, time.UTC)

func TestHandleTask_ASpentMeshBudgetStopsTheTaskBeforeTheModelIsCalled(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "should never be produced", FinishReason: "stop"})
	useBudgetGate(agent, budgettest.BlockedGate(t, budgetResetsAt))

	resp, err := agent.HandleTask(t.Context(), "task-budget", newUserMessage("Summarize the report"), testUser)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil {
		t.Fatalf("response = %+v, want a JSON-RPC error", resp)
	}
	if resp.Error.Code != -32603 {
		t.Errorf("error code = %d, want -32603", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "usage budget is used up until 1 November 2099 00:00 UTC") {
		t.Errorf("error message = %q, want it to name when the budget resets", resp.Error.Message)
	}
	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("error data = %#v, want a map carrying the failure category", resp.Error.Data)
	}
	if got := data[a2a.ErrorDataLLMErrorCategory]; got != taxonomy.ErrorCategoryBudgetExhausted {
		t.Errorf("failure category = %v, want %q", got, taxonomy.ErrorCategoryBudgetExhausted)
	}
	if provider, present := data[a2a.ErrorDataLLMProvider]; present {
		t.Errorf("failure names provider %v; a budget stop never reached one", provider)
	}
	if calls := mock.getCalls(); len(calls) != 0 {
		t.Errorf("model received %d requests while the budget was spent, want 0", len(calls))
	}
}

func TestHandleTask_RunsNormallyOnceANewerStateLiftsTheBudgetBlock(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "Here is the summary.", FinishReason: "stop"})
	gate := budgettest.BlockedGate(t, budgetResetsAt)
	useBudgetGate(agent, gate)

	blocked, err := agent.HandleTask(t.Context(), "task-blocked", newUserMessage("Summarize the report"), testUser)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Error == nil {
		t.Fatal("the task ran while the budget was spent")
	}

	gate.Apply(t.Context(), budgettest.MeshState(false, budgetResetsAt, time.Now().Add(time.Second)))

	resp, err := agent.HandleTask(t.Context(), "task-admitted", newUserMessage("Summarize the report"), testUser)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("task after the block lifted failed: %+v", resp.Error)
	}
	if calls := mock.getCalls(); len(calls) != 1 {
		t.Errorf("model received %d requests, want 1 from the admitted task only", len(calls))
	}
}

func TestInternalLLMCall_ASpentMeshBudgetRefusesEveryModelBeforeTheProvider(t *testing.T) {
	for name, model := range map[string]string{"agent model": "", "another model": "summarizer-model"} {
		t.Run(name, func(t *testing.T) {
			mock := newMockLLM(&llm.ChatResponse{Content: "should never be produced"})
			h := newInternalLLMHarness(t, mock, "task-internal-budget")
			useBudgetGate(h.agent, budgettest.BlockedGate(t, budgetResetsAt))

			_, err := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, model, "sys", "user", 0.2)

			exhausted, ok := errors.AsType[*budget.ExhaustedError](err)
			if !ok {
				t.Fatalf("internal call error = %v, want a budget refusal", err)
			}
			if !exhausted.ResetsAt.Equal(budgetResetsAt) {
				t.Errorf("refusal resets at %s, want %s", exhausted.ResetsAt, budgetResetsAt)
			}
			if calls := mock.getCalls(); len(calls) != 0 {
				t.Errorf("model received %d requests while the budget was spent, want 0", len(calls))
			}
		})
	}
}

func TestHandleTask_ABudgetStopDuringCompactionIsReportedAsABudgetStop(t *testing.T) {
	client := &countingFailingLLM{
		err:        &llm.Error{StatusCode: 400, Message: "prompt is too long: 213456 tokens > 200000 maximum", Provider: "anthropic"},
		summaryErr: &budget.ExhaustedError{Tier: budget.TierMesh, ResetsAt: budgetResetsAt},
	}
	store := newMemSessionStore()
	agent := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	svc := newMockServices(client, store)
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	seedSession(store, "test-agent", "user-1", "ctx-1")

	resp, err := agent.HandleTask(t.Context(), "task-compaction-budget", newUserMessage("and italy?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "usage budget is used up until 1 November 2099") {
		t.Fatalf("response = %+v, want the budget stop message", resp.Error)
	}
	if got := errorData(t, resp)[a2a.ErrorDataLLMErrorCategory]; got != taxonomy.ErrorCategoryBudgetExhausted {
		t.Errorf("failure category = %v, want %q, so the scheduler does not count it as a failure", got, taxonomy.ErrorCategoryBudgetExhausted)
	}
}

type spendsTheBudgetAfterFirstCall struct {
	*scriptedStreamLLM
	gate  *budget.Gate
	spent budget.EnforcementState
	t     *testing.T
}

func (c spendsTheBudgetAfterFirstCall) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch, err := c.scriptedStreamLLM.ChatCompletionStream(ctx, req)
	c.gate.Apply(c.t.Context(), c.spent)
	return ch, err
}

func TestStructuredInvocation_ARetryRefusedByTheBudgetFailsTheNodeWithTheBudgetMessage(t *testing.T) {
	gate := budgettest.NewGate()
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{outputWithoutResultEmbed, outputWithResultEmbed}}

	spent := budgettest.MeshState(true, budgetResetsAt, time.Now())
	resp, _ := runStructuredInvocationWithBudget(t.Context(), t, "task-si-budget", spendsTheBudgetAfterFirstCall{llmMock, gate, spent, t}, 1, func(a *Agent) {
		useBudgetGate(a, gate)
	})

	if got := llmMock.streamCalls(); got != 1 {
		t.Errorf("LLM stream calls = %d, want only the first, before the budget was spent", got)
	}
	_, data := structuredResultData(t, resp)
	if data["status"] != "error" {
		t.Fatalf("result status = %v, want error: %+v", data["status"], data)
	}
	if msg, _ := data["error_message"].(string); !strings.Contains(msg, "usage budget is used up until 1 November 2099") {
		t.Errorf("error_message = %q, want the budget stop message", msg)
	}
}

func TestStructuredInvocation_ASystemUsersSpentBudgetNamesTheSystemUser(t *testing.T) {
	gate := budgettest.NewGate()
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{outputWithoutResultEmbed, outputWithResultEmbed}}
	spent := budgettest.UserBlockedState(budgetResetsAt, time.Now(), budget.UserKey("", "system:nightly"))
	runAs := a2a.UserProperties{ClientID: "system:nightly", UserID: "system:nightly"}

	resp, _ := runStructuredInvocationAs(t.Context(), t, "task-si-system-user", spendsTheBudgetAfterFirstCall{llmMock, gate, spent, t}, 1, nil, runAs, func(a *Agent) {
		useBudgetGate(a, gate)
	})

	_, data := structuredResultData(t, resp)
	msg, _ := data["error_message"].(string)
	if !strings.Contains(msg, `usage budget is used up until 1 November 2099 00:00 UTC for the service account "nightly" this runs as`) {
		t.Errorf("error_message = %q, want the node failed naming the service account it runs as", msg)
	}
	if strings.Contains(msg, "your usage budget") {
		t.Errorf("error_message = %q, want no claim that the reader's own budget is spent", msg)
	}
}

func TestHandleStreamCallError_ACancelledTaskReportsTheCancelOverABudgetStop(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	rc := &taskRunContext{sess: &session.Session{}, logger: slog.New(slog.DiscardHandler)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var allText replyText

	resp, _, _ := agent.handleStreamCallError(ctx, state, rc, nil, &budget.ExhaustedError{Tier: budget.TierMesh, ResetsAt: budgetResetsAt}, &allText)

	if resp == nil || resp.Error != nil {
		t.Fatalf("response = %+v, want the cancelled response", resp)
	}
}

func TestHandleTask_ABudgetSpentWhileAToolRunsStopsTheNextLLMCall(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "lookup", Arguments: `{}`}}},
		&llm.ChatResponse{Content: "should never be produced", FinishReason: "stop"},
	)
	gate := budgettest.NewGate()
	useBudgetGate(agent, gate)
	toolRuns := 0
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "lookup",
		description: "Looks something up",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			toolRuns++
			gate.Apply(t.Context(), budgettest.MeshState(true, budgetResetsAt, time.Now()))
			return tool.OK("found it"), nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(t.Context(), "task-tool-loop-budget", newUserMessage("Look it up"), testUser)
	if err != nil {
		t.Fatal(err)
	}

	if toolRuns != 1 {
		t.Errorf("tool ran %d times, want once, before the budget was spent", toolRuns)
	}
	if calls := mock.getCalls(); len(calls) != 1 {
		t.Errorf("model received %d requests, want only the first, before the budget was spent", len(calls))
	}
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "usage budget is used up until 1 November 2099") {
		t.Fatalf("response error = %+v, want the budget stop message", resp.Error)
	}
	if got := errorData(t, resp)[a2a.ErrorDataLLMErrorCategory]; got != taxonomy.ErrorCategoryBudgetExhausted {
		t.Errorf("failure category = %v, want %q", got, taxonomy.ErrorCategoryBudgetExhausted)
	}
}

func TestHandleTask_AUsersSpentBudgetStopsOnlyTheirTasks(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "Here is the summary.", FinishReason: "stop"})
	gate := budgettest.NewGate()
	gate.Apply(t.Context(), budgettest.UserBlockedState(budgetResetsAt, time.Now(), budget.UserKey("", testUser.UserID)))
	useBudgetGate(agent, gate)

	stopped, err := agent.HandleTask(t.Context(), "task-spent-user", newUserMessage("Summarize the report"), testUser)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Error == nil || !strings.Contains(stopped.Error.Message, "your usage budget is used up until 1 November 2099 00:00 UTC") {
		t.Fatalf("response = %+v, want the task stopped naming the user's own budget", stopped.Error)
	}
	if calls := mock.getCalls(); len(calls) != 0 {
		t.Fatalf("model received %d requests for a user whose budget is spent, want 0", len(calls))
	}

	other := a2a.UserProperties{ClientID: "user-2", UserID: "user-2"}
	resp, err := agent.HandleTask(t.Context(), "task-other-user", newUserMessage("Summarize the report"), other)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Errorf("another user's task failed: %+v", resp.Error)
	}
}

func TestHandleTask_ASystemUsersSpentBudgetNamesTheSystemUser(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "Here is the summary.", FinishReason: "stop"})
	gate := budgettest.NewGate()
	gate.Apply(t.Context(), budgettest.UserBlockedState(budgetResetsAt, time.Now(), budget.UserKey("", "system:nightly")))
	useBudgetGate(agent, gate)

	runAs := a2a.UserProperties{ClientID: "system:nightly", UserID: "system:nightly"}
	stopped, err := agent.HandleTask(t.Context(), "task-system-user", newUserMessage("Summarize the report"), runAs)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Error == nil || !strings.Contains(stopped.Error.Message, `usage budget is used up until 1 November 2099 00:00 UTC for the service account "nightly" this runs as`) {
		t.Fatalf("response = %+v, want the task stopped naming the service account it runs as", stopped.Error)
	}
}

func useBudgetGate(a *Agent, gate *budget.Gate) {
	a.svc = budgettest.WithGate(a.svc, gate)
}
