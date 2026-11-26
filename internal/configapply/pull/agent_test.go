// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/registry"
)

type fakeSkillLister struct{ skills []registry.NamedID }

func (f fakeSkillLister) ListSkills(context.Context) ([]registry.NamedID, error) {
	return f.skills, nil
}
func (fakeSkillLister) ListConnectors(context.Context) ([]registry.NamedID, error) { return nil, nil }
func (fakeSkillLister) ListToolsets(context.Context) ([]registry.NamedID, error)   { return nil, nil }

func TestReverseAgentXRefs_PreservesBuiltinSkillRefs(t *testing.T) {
	if !builtinskills.HasName("sam-excel") {
		t.Fatal("sam-excel must be an embedded built-in skill for this test")
	}
	const customUUID = "11111111-2222-3333-4444-555555555555"
	reg := registry.New(fakeSkillLister{skills: []registry.NamedID{{ID: customUUID, Name: "summarize"}}})
	if err := reg.Load(t.Context()); err != nil {
		t.Fatalf("registry load: %v", err)
	}
	reg.SetBuiltinSkills(builtinskills.Names())

	spec := map[string]any{
		"skillIds":  []any{customUUID},
		"skillRefs": []any{"sam-excel", "summarize"},
	}
	if err := reverseAgentXRefs(spec, reg); err != nil {
		t.Fatalf("reverseAgentXRefs: %v", err)
	}
	if _, ok := spec["skillIds"]; ok {
		t.Error("skillIds should be deleted after reverse")
	}
	refs, ok := asStringSlice(spec["skillRefs"])
	if !ok {
		t.Fatalf("skillRefs missing or wrong type: %#v", spec["skillRefs"])
	}
	if !slices.Contains(refs, "sam-excel") {
		t.Errorf("skillRefs %v dropped the built-in sam-excel", refs)
	}
	if !slices.Contains(refs, "summarize") {
		t.Errorf("skillRefs %v lost the custom skill summarize", refs)
	}
}
