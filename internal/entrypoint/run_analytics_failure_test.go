// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func (h *runAnalyticsHarness) publishAgentError(t *testing.T, taskID string, a2aErr *a2a.Error) {
	t.Helper()
	payload, err := json.Marshal(a2a.Response{JSONRPC: "2.0", ID: taskID, Error: a2aErr})
	if err != nil {
		t.Fatalf("marshal error response: %v", err)
	}
	topic := a2a.GatewayResponseTopic(h.core.Namespace(), h.core.cfg.GatewayID, taskID)
	if err := h.core.svc.Broker().PublishGuaranteed(context.Background(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish error response: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.core.tasks.Get(taskID) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the error response was never delivered to the gateway")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRunCompletedCarriesTheFailureClassOverTheBroker(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-llm-failure", nil)

	h.publishAgentError(t, taskID, &a2a.Error{
		Code:    -32603,
		Message: "The AI model is currently rate limited. Please try again shortly.",
		Data: map[string]any{
			"token_usage":                 map[string]any{"prompt_tokens": 120},
			a2a.ErrorDataLLMErrorCategory: llm.ErrorCategoryRateLimit,
			a2a.ErrorDataLLMProvider:      "anthropic",
		},
	})

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeError {
		t.Fatalf("outcome = %v, want %q", got, productanalytics.OutcomeError)
	}
	if got := ev.Properties[productanalytics.PropErrorCategory]; got != llm.ErrorCategoryRateLimit {
		t.Errorf("errorCategory = %v, want %q", got, llm.ErrorCategoryRateLimit)
	}
	if got := ev.Properties[productanalytics.PropProvider]; got != "anthropic" {
		t.Errorf("provider = %v, want %q", got, "anthropic")
	}
}

func TestRunCompletedOmitsTheFailureClassWhenThereIsNone(t *testing.T) {
	cases := []struct {
		name   string
		task   *a2a.Task
		a2aErr *a2a.Error
	}{
		{"a successful run", completedTask("task-ok"), nil},
		{"a cancelled run", &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil},
		{"a gateway timeout", nil, &a2a.Error{Code: TaskTimeoutErrorCode, Message: "The request timed out."}},
		{"a failure that was not an LLM call", nil, &a2a.Error{Code: -32603, Message: "tool dispatch failed"}},
		{"a failure carrying only its usage block", nil, &a2a.Error{
			Code: -32603,
			Data: map[string]any{"token_usage": map[string]any{"prompt_tokens": 10}},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newRunAnalyticsHarness(t)
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			taskID := h.submit(t, "my-agent", "task-no-class", nil)
			h.core.fireTaskCompleteHooks(taskID, c.task, c.a2aErr)

			ev := onlyEvent(t, h.drain(t))
			for _, key := range []string{productanalytics.PropErrorCategory, productanalytics.PropProvider} {
				if got, ok := ev.Properties[key]; ok {
					t.Errorf("%s = %v, want absent", key, got)
				}
			}
		})
	}
}

func TestRunCompletedDropsAnUnapprovedFailureClass(t *testing.T) {
	forged := &a2a.Error{
		Code: -32603,
		Data: map[string]any{
			a2a.ErrorDataLLMErrorCategory: "billing account acme-payroll suspended",
			a2a.ErrorDataLLMProvider:      "acme-payroll-llm.internal",
		},
	}
	if category, provider := llmFailureClass(forged); category != "" || provider != "" {
		t.Errorf("llmFailureClass = (%q, %q), want both refused before the event was built", category, provider)
	}

	info := &TaskInfo{TaskOrigin: TaskOrigin{AgentType: productanalytics.AgentTypeUserCreated}}
	props := runReportProperties(info, nil, forged, usageFacts{})
	for _, key := range []string{productanalytics.PropErrorCategory, productanalytics.PropProvider} {
		if got, ok := props[key]; ok {
			t.Errorf("%s = %v, want the emitter to have refused it", key, got)
		}
	}
}

func TestRunCompletedKeepsAnApprovedFailureClass(t *testing.T) {
	classified := &a2a.Error{
		Code: -32603,
		Data: map[string]any{
			a2a.ErrorDataLLMErrorCategory: productanalytics.ErrorCategories[0],
			a2a.ErrorDataLLMProvider:      productanalytics.ProviderLabels[0],
		},
	}
	category, provider := llmFailureClass(classified)
	if category != productanalytics.ErrorCategories[0] || provider != productanalytics.ProviderLabels[0] {
		t.Errorf("llmFailureClass = (%q, %q), want the approved pair kept", category, provider)
	}
}

func TestRunCompletedReportsAnUnrecoveredContextOverflow(t *testing.T) {
	unrecovered := &a2a.Error{
		Code:    -32603,
		Message: taxonomy.HistoryTooLongPrefix + "We could not further summarize your conversation history. " + taxonomy.StartNewConversationSuffix,
		Data:    map[string]any{a2a.ErrorDataLLMErrorCategory: llm.ErrorCategoryContextOverflow},
	}
	info := &TaskInfo{TaskOrigin: TaskOrigin{AgentType: productanalytics.AgentTypeUserCreated}}
	props := runReportProperties(info, nil, unrecovered, usageFacts{})
	if got := props[productanalytics.PropErrorCategory]; got != llm.ErrorCategoryContextOverflow {
		t.Errorf("%s = %v, want %q", productanalytics.PropErrorCategory, got, llm.ErrorCategoryContextOverflow)
	}
}

func workflowTypeExt() a2a.AgentExtension {
	return a2a.AgentExtension{
		URI:    agent.ExtensionURIAgentType,
		Params: map[string]any{"type": "workflow"},
	}
}

func TestRunCompletedClassifiesWorkflowsApartFromAgents(t *testing.T) {
	cases := []struct {
		name      string
		card      a2a.AgentCard
		agentType string
	}{
		{
			"a deployed workflow is a workflow",
			testAgentCard("workflow_0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000", displayNameExt("Claims Intake")),
			productanalytics.AgentTypeWorkflow,
		},
		{
			"a config-defined workflow is a workflow",
			testAgentCard("claims-intake", workflowTypeExt()),
			productanalytics.AgentTypeWorkflow,
		},
		{
			"a deployed agent is not a workflow",
			testAgentCard("agent_0199bbbb-cccc-7ddd-8eee-ffff00001111", displayNameExt("Support Bot")),
			productanalytics.AgentTypeUserCreated,
		},
		{
			"an agent whose name merely mentions workflows is not a workflow",
			testAgentCard("workflow-helper-agent"),
			productanalytics.AgentTypeUserCreated,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newRunAnalyticsHarness(t)
			h.core.agents.AddOrUpdate(c.card)
			taskID := h.submit(t, c.card.Name, "task-workflow-class", nil)
			h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

			ev := onlyEvent(t, h.drain(t))
			if got := ev.Properties[productanalytics.PropAgentType]; got != c.agentType {
				t.Errorf("agentType = %v, want %q", got, c.agentType)
			}
		})
	}
}

func TestRunCompletedCountsAWorkflowOnceNotPerNode(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	card := testAgentCard("workflow_0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000", displayNameExt("Claims Intake"))
	h.core.agents.AddOrUpdate(card)
	taskID := h.submit(t, card.Name, "task-workflow-parent", nil)

	for _, nodeTaskID := range []string{taskID + "-node-classify", taskID + "-node-extract"} {
		h.core.fireTaskCompleteHooks(nodeTaskID, completedTask(nodeTaskID), nil)
	}
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropAgentType]; got != productanalytics.AgentTypeWorkflow {
		t.Errorf("agentType = %v, want %q", got, productanalytics.AgentTypeWorkflow)
	}
}

func TestRunCompletedReportsTheCategoryOfAFailedWorkflow(t *testing.T) {
	failed := func(category string) *a2a.Task {
		return &a2a.Task{ID: "t", Status: a2a.TaskStatus{State: a2a.TaskStateFailed},
			Metadata: map[string]any{a2a.ErrorDataLLMErrorCategory: category}}
	}
	info := &TaskInfo{TaskOrigin: TaskOrigin{AgentType: productanalytics.AgentTypeUserCreated}}

	props := runReportProperties(info, failed(taxonomy.ErrorCategoryBudgetExhausted), nil, usageFacts{})
	if got := props[productanalytics.PropErrorCategory]; got != taxonomy.ErrorCategoryBudgetExhausted {
		t.Errorf("%s = %v, want %q", productanalytics.PropErrorCategory, got, taxonomy.ErrorCategoryBudgetExhausted)
	}
	props = runReportProperties(info, failed("billing account acme-payroll suspended"), nil, usageFacts{})
	if got, ok := props[productanalytics.PropErrorCategory]; ok {
		t.Errorf("%s = %v, want an unapproved category refused", productanalytics.PropErrorCategory, got)
	}
}
