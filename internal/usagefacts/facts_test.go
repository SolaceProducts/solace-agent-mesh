// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usagefacts

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const wantWireBudget = 32

func setVocabularies() map[string][]string {
	return map[string][]string{
		a2a.MetadataToolCategories:      taxonomy.ToolCategories,
		a2a.MetadataConnectorCategories: taxonomy.ConnectorCategories,
		a2a.MetadataBuiltinCategories:   taxonomy.BuiltinToolCategories,
		a2a.MetadataToolModelFamilies:   modelfamily.Families,
	}
}

func TestEveryVocabularyFitsTheWireBudget(t *testing.T) {
	if MaxSetElements != wantWireBudget {
		t.Fatalf("MaxSetElements = %d, want %d — widening the wire contract is a deliberate change", MaxSetElements, wantWireBudget)
	}
	for key, vocabulary := range setVocabularies() {
		if len(vocabulary) > MaxSetElements {
			t.Errorf("the vocabulary for %s has %d members against a wire budget of %d: widen the budget deliberately, or the set stops being one message's worth",
				key, len(vocabulary), MaxSetElements)
		}
	}
}

func TestTheArrivalCapClearsTheWireBudget(t *testing.T) {
	if MaxArrivingSetElements <= MaxSetElements {
		t.Errorf("the arrival cap %d does not clear the wire budget %d, so a set the sender was allowed to build is cut short on arrival",
			MaxArrivingSetElements, MaxSetElements)
	}
}

func TestSetsAreBoundedWhereNothingFiltersThem(t *testing.T) {
	const distinctValues = 40
	var facts Facts
	for i := range distinctValues {
		facts.AddToolCategory(string(rune('a' + i)))
		facts.AddConnectorCategory(string(rune('A' + i)))
		facts.AddBuiltinCategory(string(rune('α' + i)))
		facts.AddToolModelFamily(string(rune('①' + i)))
	}

	metadata := map[string]any{}
	facts.AddTo(metadata)
	for key, vocabulary := range setVocabularies() {
		if len(vocabulary) >= distinctValues {
			t.Fatalf("the vocabulary for %s has %d members, so %d unapproved values cannot reach its bound and this proves nothing",
				key, len(vocabulary), distinctValues)
		}
		if got := len(metadata[key].([]string)); got != len(vocabulary) {
			t.Errorf("%s took %d of %d unapproved values, want its vocabulary's %d",
				key, got, distinctValues, len(vocabulary))
		}
	}
}

func TestArrivingSetsAreReadOnlyToTheCap(t *testing.T) {
	approved := taxonomy.BuiltinToolCategories[0]
	arriving := make([]any, 0, MaxArrivingSetElements+1)
	for i := range MaxArrivingSetElements {
		arriving = append(arriving, "acme_toolset_"+string(rune('a'+i%26)))
	}
	arriving = append(arriving, approved)

	var facts Facts
	facts.Merge(map[string]any{a2a.MetadataBuiltinCategories: arriving})
	metadata := map[string]any{}
	facts.AddTo(metadata)
	if _, read := metadata[a2a.MetadataBuiltinCategories]; read {
		t.Errorf("%s = %v, want the element past the arrival cap left unread",
			a2a.MetadataBuiltinCategories, metadata[a2a.MetadataBuiltinCategories])
	}

	arriving[MaxArrivingSetElements-1] = approved
	var within Facts
	within.Merge(map[string]any{a2a.MetadataBuiltinCategories: arriving[:MaxArrivingSetElements]})
	kept := map[string]any{}
	within.AddTo(kept)
	if got, _ := kept[a2a.MetadataBuiltinCategories].([]string); !slices.Equal(got, []string{approved}) {
		t.Errorf("%s = %v, want %q read from inside the cap", a2a.MetadataBuiltinCategories, got, approved)
	}
}

func TestMergeReadsBothListShapesAndRefusesAnythingElse(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  []string
	}{
		{"the broker's decoded array", []any{"builtin", "custom"}, []string{"builtin", "custom"}},
		{"a same-process slice", []string{"builtin"}, []string{"builtin"}},
		{"a list carrying a non-string", []any{"builtin", 7}, nil},
		{"a bare string", "builtin", nil},
		{"a number", 7, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var facts Facts
			facts.Merge(map[string]any{a2a.MetadataToolCategories: c.value})
			metadata := map[string]any{}
			facts.AddTo(metadata)
			got, _ := metadata[a2a.MetadataToolCategories].([]string)
			if !slices.Equal(got, c.want) {
				t.Errorf("%s = %v, want %v", a2a.MetadataToolCategories, got, c.want)
			}
		})
	}
}

func TestNothingRecordedWritesNothing(t *testing.T) {
	var facts Facts
	facts.Merge(nil)
	facts.Merge(map[string]any{"agent_name": "acme-payroll-assistant"})

	metadata := map[string]any{}
	facts.AddTo(metadata)
	if len(metadata) != 0 {
		t.Errorf("metadata = %#v, want nothing written", metadata)
	}
}

func TestMergeKeepsOnlyApprovedBuiltinCategories(t *testing.T) {
	approved := taxonomy.BuiltinToolCategories[0]
	var facts Facts
	facts.Merge(map[string]any{
		a2a.MetadataBuiltinCategories: []any{
			approved, "acme_internal_toolset", "builtin_artifact_tools_v2", "list_artifacts",
		},
	})

	metadata := map[string]any{}
	facts.AddTo(metadata)
	got, _ := metadata[a2a.MetadataBuiltinCategories].([]string)
	if !slices.Equal(got, []string{approved}) {
		t.Errorf("%s = %v, want only %q kept", a2a.MetadataBuiltinCategories, got, approved)
	}
}

func TestEveryApprovedMemberOfAVocabularyIsReported(t *testing.T) {
	var facts Facts
	for _, category := range taxonomy.ToolCategories {
		facts.AddToolCategory(category)
	}
	for _, category := range taxonomy.ConnectorCategories {
		facts.AddConnectorCategory(category)
	}
	for _, category := range taxonomy.BuiltinToolCategories {
		facts.AddBuiltinCategory(category)
	}
	for _, family := range modelfamily.Families {
		facts.AddToolModelFamily(family)
	}

	metadata := map[string]any{}
	facts.AddTo(metadata)
	for key, vocabulary := range setVocabularies() {
		want := slices.Sorted(slices.Values(vocabulary))
		got, _ := metadata[key].([]string)
		if !slices.Equal(got, want) {
			t.Errorf("%s reported %d of %d approved members: %v", key, len(got), len(want), got)
		}
	}
}

func nodeModelPairs() []map[string]any {
	return []map[string]any{
		{a2a.MetadataModelFamily: modelfamily.GPT4o, a2a.MetadataModelProvider: "openai"},
		{a2a.MetadataModelFamily: modelfamily.ClaudeSonnet, a2a.MetadataModelProvider: "anthropic"},
		{a2a.MetadataModelFamily: modelfamily.GeminiFlash, a2a.MetadataModelProvider: "vertex"},
		{a2a.MetadataModelFamily: modelfamily.Llama, a2a.MetadataModelProvider: "groq"},
	}
}

func foldedPair(t *testing.T, sources []map[string]any) (family, provider string) {
	t.Helper()
	var facts Facts
	for _, source := range sources {
		facts.Merge(source)
	}
	metadata := map[string]any{}
	facts.AddTo(metadata)
	family, _ = metadata[a2a.MetadataModelFamily].(string)
	provider, _ = metadata[a2a.MetadataModelProvider].(string)
	return family, provider
}

func TestTheFoldedModelPairDoesNotDependOnArrivalOrder(t *testing.T) {
	sources := nodeModelPairs()
	wantFamily, wantProvider := foldedPair(t, sources)
	if wantFamily == "" || wantProvider == "" {
		t.Fatalf("the fold reported (%q, %q), so this test asserts nothing", wantFamily, wantProvider)
	}

	for _, order := range [][]int{
		{3, 2, 1, 0}, {1, 3, 0, 2}, {2, 0, 3, 1}, {0, 2, 1, 3},
	} {
		permuted := make([]map[string]any, 0, len(order))
		for _, i := range order {
			permuted = append(permuted, sources[i])
		}
		family, provider := foldedPair(t, permuted)
		if family != wantFamily || provider != wantProvider {
			t.Errorf("nodes finishing in order %v reported (%q, %q), want (%q, %q) — the same run must report the same model",
				order, family, provider, wantFamily, wantProvider)
		}
	}
}

func TestTheFoldedModelPairSurvivesBeingSplitByASnapshot(t *testing.T) {
	sources := nodeModelPairs()
	wantFamily, wantProvider := foldedPair(t, sources)

	for split := range len(sources) + 1 {
		var before Facts
		for _, source := range sources[:split] {
			before.Merge(source)
		}
		snapshot, err := json.Marshal(&before)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var after Facts
		if err := json.Unmarshal(snapshot, &after); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, source := range sources[split:] {
			after.Merge(source)
		}

		metadata := map[string]any{}
		after.AddTo(metadata)
		if metadata[a2a.MetadataModelFamily] != wantFamily || metadata[a2a.MetadataModelProvider] != wantProvider {
			t.Errorf("a pause after %d nodes reported (%v, %v), want (%q, %q)",
				split, metadata[a2a.MetadataModelFamily], metadata[a2a.MetadataModelProvider], wantFamily, wantProvider)
		}
	}
}

func TestAPartialPairNeverDisplacesHalfOfACompleteOne(t *testing.T) {
	complete := map[string]any{
		a2a.MetadataModelFamily:   modelfamily.GPT4o,
		a2a.MetadataModelProvider: "openai",
	}
	partial := map[string]any{
		a2a.MetadataModelFamily:   "acme-internal-vllm-cluster",
		a2a.MetadataModelProvider: "anthropic",
	}
	for _, order := range [][]map[string]any{
		{complete, partial}, {partial, complete},
	} {
		family, provider := foldedPair(t, order)
		if family != modelfamily.GPT4o || provider != "openai" {
			t.Errorf("folding %v reported (%q, %q), want the complete pair (%q, openai)",
				order, family, provider, modelfamily.GPT4o)
		}
	}
}

func TestTheAgentAndTheWorkflowSettleThePairTheSameWay(t *testing.T) {
	sources := nodeModelPairs()
	wantFamily, wantProvider := foldedPair(t, sources)

	var recorded Facts
	for _, source := range sources {
		recorded.RecordModel(Model{
			Family:   source[a2a.MetadataModelFamily].(string),
			Provider: source[a2a.MetadataModelProvider].(string),
		})
	}
	metadata := map[string]any{}
	recorded.AddTo(metadata)
	if metadata[a2a.MetadataModelFamily] != wantFamily || metadata[a2a.MetadataModelProvider] != wantProvider {
		t.Errorf("RecordModel settled on (%v, %v) where Merge settled on (%q, %q)",
			metadata[a2a.MetadataModelFamily], metadata[a2a.MetadataModelProvider], wantFamily, wantProvider)
	}
}

func TestSnapshotRestoreFiltersLikeAMessage(t *testing.T) {
	tampered, err := json.Marshal(map[string]any{
		a2a.MetadataToolCategories: []string{taxonomy.ToolCategoryBuiltin, "acme_ledger_tool"},
		a2a.MetadataModelFamily:    "acme-internal-vllm-cluster",
		a2a.MetadataModelProvider:  modelfamily.Other,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var facts Facts
	if err := json.Unmarshal(tampered, &facts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	metadata := map[string]any{}
	facts.AddTo(metadata)
	if got, want := metadata[a2a.MetadataToolCategories], []string{taxonomy.ToolCategoryBuiltin}; !slices.Equal(got.([]string), want) {
		t.Errorf("%s = %v, want %v", a2a.MetadataToolCategories, got, want)
	}
	if _, present := metadata[a2a.MetadataModelFamily]; present {
		t.Errorf("%s = %v, want the unapproved family refused", a2a.MetadataModelFamily, metadata[a2a.MetadataModelFamily])
	}
}
