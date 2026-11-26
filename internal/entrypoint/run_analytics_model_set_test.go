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
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func modelUsageMetadata(settled usagefacts.Model, used ...usagefacts.Model) map[string]any {
	var facts usagefacts.Facts
	facts.RecordModel(settled)
	for _, model := range used {
		facts.RecordModel(model)
	}
	metadata := map[string]any{"agent_name": "acme-payroll-assistant"}
	facts.AddTo(metadata)
	return overBroker(metadata)
}

func overBroker(metadata map[string]any) map[string]any {
	raw, err := json.Marshal(metadata)
	if err != nil {
		panic(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		panic(err)
	}
	return decoded
}

func modelUseEvents(events []productanalytics.Event) []usagefacts.Model {
	var used []usagefacts.Model
	for _, event := range events {
		if event.Name != productanalytics.EventModelUsed {
			continue
		}
		family, _ := event.Properties[productanalytics.PropModel].(string)
		provider, _ := event.Properties[productanalytics.PropProvider].(string)
		servedFrom, _ := event.Properties[productanalytics.PropModelType].(string)
		used = append(used, usagefacts.Model{Family: family, Provider: provider, ServedFrom: servedFrom})
	}
	slices.SortFunc(used, usagefacts.CompareModels)
	return used
}

func TestAMultiModelRunReportsOneEventPerModel(t *testing.T) {
	models := []usagefacts.Model{
		{Family: modelfamily.ClaudeSonnet, Provider: "bedrock", ServedFrom: productanalytics.ModelTypeHosted},
		{Family: modelfamily.GPT4o, Provider: "openai", ServedFrom: productanalytics.ModelTypeHosted},
		{Family: modelfamily.Llama, Provider: "openai", ServedFrom: productanalytics.ModelTypeLocal},
	}

	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-multi-model", nil)
	h.publishAgentTask(t, taskID, modelUsageMetadata(models[0], models[1:]...))

	want := slices.Clone(models)
	slices.SortFunc(want, usagefacts.CompareModels)
	if got := modelUseEvents(h.drain(t)); !slices.Equal(got, want) {
		t.Errorf("reported %+v, want one event for each of %+v", got, want)
	}
}

func TestARunOnOneModelReportsItOnce(t *testing.T) {
	model := usagefacts.Model{Family: modelfamily.GPT4o, Provider: "openai", ServedFrom: productanalytics.ModelTypeHosted}

	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-single-model", nil)
	h.publishAgentTask(t, taskID, modelUsageMetadata(model))

	if got := modelUseEvents(h.drain(t)); len(got) != 1 || got[0] != model {
		t.Errorf("reported %+v, want exactly one event for %+v", got, model)
	}
}

func TestTheRunReportCarriesTheServingLocationOfTheModelItNames(t *testing.T) {
	model := usagefacts.Model{Family: modelfamily.Llama, Provider: "openai", ServedFrom: productanalytics.ModelTypeLocal}

	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-model-serving", nil)
	h.publishAgentTask(t, taskID, modelUsageMetadata(model))

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropModelType]; got != productanalytics.ModelTypeLocal {
		t.Errorf("modelType = %v, want %q — an openai-compatible server on the operator's own machine is not hosted",
			got, productanalytics.ModelTypeLocal)
	}
	if got := ev.Properties[productanalytics.PropProvider]; got != "openai" {
		t.Fatalf("provider = %v, want openai — this is not the case under test", got)
	}
}

func TestATimedOutRunStillReportsTheModelItWasOn(t *testing.T) {
	model := usagefacts.Model{Family: modelfamily.ClaudeSonnet, Provider: "bedrock", ServedFrom: productanalytics.ModelTypeLocal}
	info := &TaskInfo{
		TaskOrigin: TaskOrigin{AgentType: productanalytics.AgentTypeUserCreated},
		Model:      model,
	}
	err := timedOut()

	facts := readUsageFacts(nil, err).orAnnounced(info)
	if got := runReportProperties(info, nil, err, facts)[productanalytics.PropModelType]; got != model.ServedFrom {
		t.Errorf("modelType = %v, want %q", got, model.ServedFrom)
	}
	events := usageFactEvents(info, "2026-01-01T00:00:00.000Z", facts)
	var used []usagefacts.Model
	for _, event := range events {
		if event.Name == productanalytics.EventModelUsed {
			used = append(used, usagefacts.Model{
				Family:     event.Properties[productanalytics.PropModel].(string),
				Provider:   event.Properties[productanalytics.PropProvider].(string),
				ServedFrom: event.Properties[productanalytics.PropModelType].(string),
			})
		}
	}
	if len(used) != 1 || used[0] != model {
		t.Errorf("reported %+v, want exactly the announced %+v", used, model)
	}
}

func TestAnUnapprovedModelEntryNeverBecomesAnEvent(t *testing.T) {
	arriving := []any{
		map[string]any{
			a2a.ModelUsageFamily:     modelfamily.GPT4o,
			a2a.ModelUsageProvider:   "openai",
			a2a.ModelUsageServedFrom: productanalytics.ModelTypeHosted,
		},
		map[string]any{
			a2a.ModelUsageFamily:     "ft:gpt-4o-2024-08-06:acme-corp:legal-triage:AbCd1234",
			a2a.ModelUsageProvider:   "acme-internal-vllm-cluster",
			a2a.ModelUsageServedFrom: productanalytics.ModelTypeLocal,
		},
	}
	facts := readUsageFacts(&a2a.Task{
		Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Metadata: map[string]any{a2a.MetadataModelUsages: arriving},
	}, nil)

	events := usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z", facts)
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want only the approved entry's: %+v", len(events), events)
	}
	rendered, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, refused := range []string{"acme-corp", "AbCd1234", "acme-internal-vllm-cluster"} {
		if strings.Contains(string(rendered), refused) {
			t.Errorf("%q reached the events %s", refused, rendered)
		}
	}
}

func TestTheArrivingModelSetIsBoundedOnTheSharedOrder(t *testing.T) {
	build := func(reversed bool) []usagefacts.Model {
		total := usagefacts.MaxModelElements * 2
		entries := make([]any, 0, total)
		for i := range total {
			index := i
			if reversed {
				index = total - 1 - i
			}
			entries = append(entries, map[string]any{
				a2a.ModelUsageFamily:     modelfamily.Families[index%len(modelfamily.Families)],
				a2a.ModelUsageProvider:   productanalytics.ProviderLabels[index%len(productanalytics.ProviderLabels)],
				a2a.ModelUsageServedFrom: productanalytics.ModelTypes[index%len(productanalytics.ModelTypes)],
			})
		}
		facts := readUsageFacts(&a2a.Task{
			Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted},
			Metadata: map[string]any{a2a.MetadataModelUsages: entries},
		}, nil)
		return facts.models
	}

	forward := build(false)
	if len(forward) != usagefacts.MaxModelElements {
		t.Fatalf("kept %d models against a bound of %d", len(forward), usagefacts.MaxModelElements)
	}
	if reversed := build(true); !slices.Equal(reversed, forward) {
		t.Errorf("arriving in reverse kept %+v, want the same set as %+v", reversed, forward)
	}
}
