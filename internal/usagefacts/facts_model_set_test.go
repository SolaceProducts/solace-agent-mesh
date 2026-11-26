// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usagefacts

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func nodeModels() []Model {
	return []Model{
		{Family: modelfamily.GPT4o, Provider: "openai", ServedFrom: taxonomy.ModelTypeHosted},
		{Family: modelfamily.ClaudeSonnet, Provider: "anthropic", ServedFrom: taxonomy.ModelTypeHosted},
		{Family: modelfamily.Llama, Provider: "openai", ServedFrom: taxonomy.ModelTypeLocal},
	}
}

func nodeMessage(model Model) map[string]any {
	var facts Facts
	facts.RecordModel(model)
	message := map[string]any{}
	facts.AddTo(message)
	return message
}

func foldedModels(t *testing.T, models []Model) []Model {
	t.Helper()
	var facts Facts
	for _, model := range models {
		facts.Merge(nodeMessage(model))
	}
	message := map[string]any{}
	facts.AddTo(message)
	return ReadModels(message[a2a.MetadataModelUsages])
}

func TestAMultiModelRunReportsEveryModelItUsed(t *testing.T) {
	want := nodeModels()
	got := foldedModels(t, want)
	for _, model := range want {
		if !slices.Contains(got, model) {
			t.Errorf("%+v is missing from the reported set %+v", model, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("reported %d models, want %d: %+v", len(got), len(want), got)
	}
}

func TestTheReportedModelSetDoesNotDependOnArrivalOrder(t *testing.T) {
	models := nodeModels()
	want := foldedModels(t, models)

	for _, order := range [][]int{{2, 1, 0}, {1, 2, 0}, {0, 2, 1}} {
		permuted := make([]Model, 0, len(order))
		for _, i := range order {
			permuted = append(permuted, models[i])
		}
		if got := foldedModels(t, permuted); !slices.Equal(got, want) {
			t.Errorf("nodes finishing in order %v reported %+v, want %+v", order, got, want)
		}
	}
}

func TestRepeatedCallsOnOneModelReportItOnce(t *testing.T) {
	model := nodeModels()[0]
	var facts Facts
	for range 1000 {
		facts.RecordModel(model)
	}
	message := map[string]any{}
	facts.AddTo(message)
	if got := ReadModels(message[a2a.MetadataModelUsages]); len(got) != 1 || got[0] != model {
		t.Errorf("reported %+v, want exactly [%+v]", got, model)
	}
}

func TestAPartlyUnapprovedEntryIsDroppedWhole(t *testing.T) {
	approved := Model{Family: modelfamily.GPT4o, Provider: "openai", ServedFrom: taxonomy.ModelTypeHosted}
	arriving := []any{
		map[string]any{
			a2a.ModelUsageFamily:     approved.Family,
			a2a.ModelUsageProvider:   approved.Provider,
			a2a.ModelUsageServedFrom: approved.ServedFrom,
		},
		map[string]any{
			a2a.ModelUsageFamily:     modelfamily.ClaudeSonnet,
			a2a.ModelUsageProvider:   "acme-internal-vllm-cluster",
			a2a.ModelUsageServedFrom: taxonomy.ModelTypeLocal,
		},
		map[string]any{
			a2a.ModelUsageFamily:   "ft:gpt-4o-2024-08-06:acme-corp:legal-triage:AbCd1234",
			a2a.ModelUsageProvider: "openai",
		},
		map[string]any{
			a2a.ModelUsageFamily:     modelfamily.Llama,
			a2a.ModelUsageProvider:   "groq",
			a2a.ModelUsageServedFrom: "acme-datacenter",
		},
	}

	got := ReadModels(arriving)
	if len(got) != 1 || got[0] != approved {
		t.Fatalf("kept %+v, want only the fully approved entry %+v", got, approved)
	}
	rendered, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, refused := range []string{"acme-internal-vllm-cluster", "acme-corp", "AbCd1234", "acme-datacenter", modelfamily.ClaudeSonnet, "groq"} {
		if strings.Contains(string(rendered), refused) {
			t.Errorf("%q survived from a refused entry: %s", refused, rendered)
		}
	}
}

func TestTheModelSetIsBounded(t *testing.T) {
	build := func(reversed bool) []Model {
		var facts Facts
		for i := range MaxModelElements * 3 {
			index := i
			if reversed {
				index = MaxModelElements*3 - 1 - i
			}
			facts.RecordModel(Model{
				Family:     modelfamily.Families[index%len(modelfamily.Families)],
				Provider:   taxonomy.ProviderLabels[index%len(taxonomy.ProviderLabels)],
				ServedFrom: taxonomy.ModelTypes[index%len(taxonomy.ModelTypes)],
			})
		}
		message := map[string]any{}
		facts.AddTo(message)
		return ReadModels(message[a2a.MetadataModelUsages])
	}

	forward := build(false)
	if len(forward) != MaxModelElements {
		t.Fatalf("reported %d models against a bound of %d", len(forward), MaxModelElements)
	}
	if reversed := build(true); !slices.Equal(reversed, forward) {
		t.Errorf("recording in reverse truncated to %+v, want the same %d as %+v", reversed, MaxModelElements, forward)
	}
}

func TestTheModelSetSurvivesBeingSplitByASnapshot(t *testing.T) {
	models := nodeModels()
	want := foldedModels(t, models)

	for split := range len(models) + 1 {
		var before Facts
		for _, model := range models[:split] {
			before.Merge(nodeMessage(model))
		}
		snapshot, err := json.Marshal(&before)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var after Facts
		if err := json.Unmarshal(snapshot, &after); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, model := range models[split:] {
			after.Merge(nodeMessage(model))
		}
		message := map[string]any{}
		after.AddTo(message)
		if got := ReadModels(message[a2a.MetadataModelUsages]); !slices.Equal(got, want) {
			t.Errorf("a pause after %d nodes reported %+v, want %+v", split, got, want)
		}
	}
}

func TestAScalarOnlyNodeStillJoinsTheSet(t *testing.T) {
	var facts Facts
	facts.Merge(map[string]any{
		a2a.MetadataModelFamily:   modelfamily.GeminiPro,
		a2a.MetadataModelProvider: "vertex",
		a2a.MetadataModelType:     taxonomy.ModelTypeHosted,
	})
	message := map[string]any{}
	facts.AddTo(message)

	want := Model{Family: modelfamily.GeminiPro, Provider: "vertex", ServedFrom: taxonomy.ModelTypeHosted}
	if got := ReadModels(message[a2a.MetadataModelUsages]); len(got) != 1 || got[0] != want {
		t.Errorf("reported %+v, want [%+v]", got, want)
	}
}

func TestTheSettledModelIsAlwaysAMemberOfTheSet(t *testing.T) {
	models := nodeModels()
	var facts Facts
	for _, model := range models {
		facts.Merge(nodeMessage(model))
	}
	message := map[string]any{}
	facts.AddTo(message)

	settled := Model{
		Family:     fmt.Sprint(message[a2a.MetadataModelFamily]),
		Provider:   fmt.Sprint(message[a2a.MetadataModelProvider]),
		ServedFrom: fmt.Sprint(message[a2a.MetadataModelType]),
	}
	if !slices.Contains(ReadModels(message[a2a.MetadataModelUsages]), settled) {
		t.Errorf("the settled model %+v is not in the reported set %+v", settled, ReadModels(message[a2a.MetadataModelUsages]))
	}
}
