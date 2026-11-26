// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func modelMetadata(family, provider string) map[string]any {
	return map[string]any{
		"agent_name":              "acme-payroll-assistant",
		a2a.MetadataModelFamily:   family,
		a2a.MetadataModelProvider: provider,
	}
}

func TestRunCompletedCarriesTheModelOnASuccessfulRun(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-model-success", nil)

	h.publishAgentTask(t, taskID, modelMetadata(modelfamily.ClaudeSonnet, "bedrock"))

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeSuccess {
		t.Fatalf("outcome = %v, want %q", got, productanalytics.OutcomeSuccess)
	}
	if got := ev.Properties[productanalytics.PropProvider]; got != "bedrock" {
		t.Errorf("provider = %v, want %q", got, "bedrock")
	}
	if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.ClaudeSonnet {
		t.Errorf("model = %v, want %q", got, modelfamily.ClaudeSonnet)
	}
}

func TestRunCompletedCarriesTheModelFromTheErrorPathToo(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-model-failure", nil)

	h.publishAgentError(t, taskID, &a2a.Error{
		Code:    -32603,
		Message: "The AI model is currently rate limited.",
		Data: map[string]any{
			a2a.ErrorDataLLMErrorCategory: llm.ErrorCategoryRateLimit,
			a2a.ErrorDataLLMProvider:      "openai",
			a2a.MetadataModelFamily:       modelfamily.GPT4o,
			a2a.MetadataModelProvider:     "openai",
		},
	})

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.GPT4o {
		t.Errorf("model = %v, want %q", got, modelfamily.GPT4o)
	}
	if got := ev.Properties[productanalytics.PropProvider]; got != "openai" {
		t.Errorf("provider = %v, want %q", got, "openai")
	}
	if got := ev.Properties[productanalytics.PropErrorCategory]; got != llm.ErrorCategoryRateLimit {
		t.Errorf("errorCategory = %v, want %q", got, llm.ErrorCategoryRateLimit)
	}
}

func TestRunCompletedReportsNoModelWhenTheAgentWasNeverHeardFrom(t *testing.T) {
	timedOut := &a2a.Error{Code: TaskTimeoutErrorCode, Message: "task timed out"}

	props := runReportProperties(
		&TaskInfo{TaskOrigin: TaskOrigin{AgentType: productanalytics.AgentTypeUserCreated}},
		nil, timedOut, readUsageFacts(nil, timedOut))
	if got := props[productanalytics.PropOutcome]; got != productanalytics.OutcomeTimeout {
		t.Fatalf("outcome = %v, want %q — this is not the timeout path", got, productanalytics.OutcomeTimeout)
	}
	for _, key := range []string{productanalytics.PropProvider, productanalytics.PropModel, productanalytics.PropToolModels} {
		if got, present := props[key]; present {
			t.Errorf("%s = %v, want a timeout report to carry no model attribution at all", key, got)
		}
	}

	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-model-timeout", nil)
	h.core.fireTaskCompleteHooks(taskID, nil, timedOut)

	ev := onlyEvent(t, h.drain(t))
	if got, present := ev.Properties[productanalytics.PropModel]; present {
		t.Errorf("model = %v, want none on a run the agent never answered", got)
	}
	if got, present := ev.Properties[productanalytics.PropProvider]; present {
		t.Errorf("provider = %v, want none on a run the agent never answered", got)
	}
}

func TestEveryModelVocabularyMemberSurvivesTheRoundTrip(t *testing.T) {
	for _, provider := range llm.ProviderLabels() {
		if !slices.Contains(productanalytics.ProviderLabels, provider) {
			t.Fatalf("the agent can label a run %q, which the catalog does not approve", provider)
		}
	}
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	for i, family := range modelfamily.Families {
		provider := productanalytics.ProviderLabels[i%len(productanalytics.ProviderLabels)]
		taskID := h.submit(t, "my-agent", "task-"+family, nil)
		h.publishAgentTask(t, taskID, modelMetadata(family, provider))
	}

	events := eventsNamed(h.drain(t), productanalytics.EventAgentRunCompleted)
	if len(events) != len(modelfamily.Families) {
		t.Fatalf("emitted %d run reports, want %d", len(events), len(modelfamily.Families))
	}
	for i, ev := range events {
		wantProvider := productanalytics.ProviderLabels[i%len(productanalytics.ProviderLabels)]
		if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.Families[i] {
			t.Errorf("model = %v, want %q", got, modelfamily.Families[i])
		}
		if got := ev.Properties[productanalytics.PropProvider]; got != wantProvider {
			t.Errorf("provider = %v, want %q", got, wantProvider)
		}
	}
}

func TestARawModelIdentifierNeverReachesTheEvent(t *testing.T) {
	for _, raw := range []string{
		"ft:gpt-4o:acme-corp:custom:9xYz",
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/acme-legal",
		"acme-prod-gpt4o-westeurope",
		"claude-sonnet-4-5",
	} {
		t.Run(raw, func(t *testing.T) {
			facts := readUsageFacts(&a2a.Task{Metadata: modelMetadata(raw, "openai")}, nil)
			props := runReportProperties(&TaskInfo{}, completedTask("task-raw"), nil, facts)
			rendered, err := json.Marshal(props)
			if err != nil {
				t.Fatalf("marshal properties: %v", err)
			}
			if strings.Contains(string(rendered), raw) {
				t.Fatalf("the raw identifier %q reached the emitted properties %s", raw, rendered)
			}
			if got, present := props[productanalytics.PropModel]; present {
				t.Errorf("model = %v, want the unclassified identifier dropped", got)
			}

			h := newRunAnalyticsHarness(t)
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			taskID := h.submit(t, "my-agent", "task-raw", nil)
			h.publishAgentTask(t, taskID, modelMetadata(raw, "openai"))

			ev := onlyEvent(t, h.drain(t))
			if got, present := ev.Properties[productanalytics.PropModel]; present {
				t.Errorf("model = %v, want the unclassified identifier dropped", got)
			}
		})
	}
}

func TestAnUnlistedProviderNeverReachesTheEvent(t *testing.T) {
	const unlisted = "acme-internal-vllm"

	facts := readUsageFacts(&a2a.Task{Metadata: modelMetadata(modelfamily.Llama, unlisted)}, nil)
	props := runReportProperties(&TaskInfo{}, completedTask("task-unlisted-provider"), nil, facts)
	if got, present := props[productanalytics.PropProvider]; present {
		t.Errorf("provider = %v, want the unlisted provider dropped by the emitter", got)
	}

	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-unlisted-provider", nil)

	h.publishAgentTask(t, taskID, modelMetadata(modelfamily.Llama, unlisted))

	ev := onlyEvent(t, h.drain(t))
	if got, present := ev.Properties[productanalytics.PropProvider]; present {
		t.Errorf("provider = %v, want the unlisted provider dropped", got)
	}
	if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.Llama {
		t.Errorf("model = %v, want the family kept regardless", got)
	}
}
