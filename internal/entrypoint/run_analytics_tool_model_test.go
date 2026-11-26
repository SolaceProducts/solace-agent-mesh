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
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func toolModelMetadata(family, provider string, toolFamilies ...string) map[string]any {
	metadata := modelMetadata(family, provider)
	metadata[a2a.MetadataToolModelFamilies] = toolFamilies
	return metadata
}

func toolModelsOf(t *testing.T, ev productanalytics.Event) []string {
	t.Helper()
	raw, present := ev.Properties[productanalytics.PropToolModels]
	if !present {
		return nil
	}
	families, ok := raw.([]string)
	if !ok {
		t.Fatalf("%s = %#v, want a list of families", productanalytics.PropToolModels, raw)
	}
	return families
}

func TestRunCompletedCarriesToolModelsBesideThePrimaryPair(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-tool-models", nil)

	h.publishAgentTask(t, taskID, toolModelMetadata(
		modelfamily.ClaudeSonnet, "bedrock", modelfamily.GPT4o, modelfamily.GeminiFlash))

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.ClaudeSonnet {
		t.Errorf("model = %v, want the primary family %q untouched", got, modelfamily.ClaudeSonnet)
	}
	if got := ev.Properties[productanalytics.PropProvider]; got != "bedrock" {
		t.Errorf("provider = %v, want %q", got, "bedrock")
	}
	want := []string{modelfamily.GeminiFlash, modelfamily.GPT4o}
	if got := toolModelsOf(t, ev); !slices.Equal(got, want) {
		t.Errorf("toolModels = %v, want %v", got, want)
	}
}

func TestRunCompletedOmitsToolModelsWhenNoToolChoseOne(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-no-tool-models", nil)

	h.publishAgentTask(t, taskID, modelMetadata(modelfamily.ClaudeSonnet, "bedrock"))

	ev := onlyEvent(t, h.drain(t))
	if got, present := ev.Properties[productanalytics.PropToolModels]; present {
		t.Errorf("toolModels = %v, want none on a run whose tools chose no model", got)
	}
}

func TestRunCompletedReadsToolModelsFromTheErrorPathToo(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-tool-models-failed", nil)

	h.publishAgentError(t, taskID, &a2a.Error{
		Code:    -32603,
		Message: "The AI model is currently rate limited.",
		Data: map[string]any{
			a2a.MetadataModelFamily:       modelfamily.GPT5,
			a2a.MetadataModelProvider:     "openai",
			a2a.MetadataToolModelFamilies: []string{modelfamily.ClaudeHaiku},
		},
	})

	ev := onlyEvent(t, h.drain(t))
	if got := toolModelsOf(t, ev); !slices.Equal(got, []string{modelfamily.ClaudeHaiku}) {
		t.Errorf("toolModels = %v, want %v", got, []string{modelfamily.ClaudeHaiku})
	}
}

func TestARawToolModelIdentifierNeverReachesTheEvent(t *testing.T) {
	for _, raw := range []string{
		"ft:gpt-4o:acme-corp:custom:9xYz",
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/acme-legal",
		"acme-prod-gpt4o-westeurope",
		"claude-sonnet-4-5",
	} {
		t.Run(raw, func(t *testing.T) {
			facts := readUsageFacts(&a2a.Task{
				Metadata: toolModelMetadata(modelfamily.GPT5, "openai", raw),
			}, nil)
			props := runReportProperties(&TaskInfo{}, completedTask("task-raw-tool-model"), nil, facts)
			rendered, err := json.Marshal(props)
			if err != nil {
				t.Fatalf("marshal properties: %v", err)
			}
			if strings.Contains(string(rendered), raw) {
				t.Fatalf("the raw identifier %q reached the emitted properties %s", raw, rendered)
			}
			if got, present := props[productanalytics.PropToolModels]; present {
				t.Errorf("toolModels = %v, want the unclassified identifier dropped by the emitter", got)
			}

			h := newRunAnalyticsHarness(t)
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			taskID := h.submit(t, "my-agent", "task-raw-tool-model", nil)
			h.publishAgentTask(t, taskID, toolModelMetadata(modelfamily.GPT5, "openai", raw))

			ev := onlyEvent(t, h.drain(t))
			if got, present := ev.Properties[productanalytics.PropToolModels]; present {
				t.Errorf("toolModels = %v, want the unclassified identifier dropped", got)
			}
			if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.GPT5 {
				t.Errorf("model = %v, want the primary family kept regardless", got)
			}
		})
	}
}

func overTheBroker(t *testing.T, metadata map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	return decoded
}

func TestAnOversizedToolModelSetIsCappedOnArrival(t *testing.T) {
	flood := make([]string, 5000)
	for i := range flood {
		flood[i] = modelfamily.Families[i%len(modelfamily.Families)]
	}
	metadata := toolModelMetadata(modelfamily.GPT5, "openai", flood...)

	for _, c := range []struct {
		name   string
		source map[string]any
	}{
		{"in process", metadata},
		{"over the broker", overTheBroker(t, metadata)},
	} {
		t.Run(c.name, func(t *testing.T) {
			facts := readUsageFacts(&a2a.Task{Metadata: c.source}, nil)
			if got := len(facts.toolModelFamilies); got > len(modelfamily.Families) {
				t.Fatalf("a %d element set arrived as %d families, want at most %d",
					len(flood), got, len(modelfamily.Families))
			}
		})
	}
}

func TestEveryToolModelVocabularyMemberSurvivesTheRoundTrip(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-tool-model-vocabulary", nil)

	h.publishAgentTask(t, taskID, toolModelMetadata("", "", modelfamily.Families...))

	ev := onlyEvent(t, h.drain(t))
	want := slices.Clone(modelfamily.Families)
	slices.Sort(want)
	if got := toolModelsOf(t, ev); !slices.Equal(got, want) {
		t.Errorf("toolModels = %v, want %v", got, want)
	}
}
