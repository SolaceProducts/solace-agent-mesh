// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestALoadReportsTheSkillVersionItDelivers(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "documents"})
	loader := &mockSkillLoader{result: map[string]any{"instructions": "v1", "content_hash": "hash-v1"}}
	lt := NewLoadSkillTool(mgr, loader, &countingRegistrar{})
	load := func(state *mockSkillState) *tool.Result {
		t.Helper()
		res, err := lt.Execute(t.Context(), map[string]any{"name": "documents"}, &tool.Context{SkillState: state})
		if err != nil || res.Status != tool.StatusSuccess {
			t.Fatalf("load: %v %+v", err, res)
		}
		return res
	}

	first := &mockSkillState{loaded: map[string]bool{}}
	res := load(first)
	if res.LoadedSkillContentHash != "hash-v1" {
		t.Errorf("first load reports %q, want hash-v1", res.LoadedSkillContentHash)
	}
	if got := res.LLMData[agent.SkillInstructionsDataKey]; got != "v1" {
		t.Errorf("first load delivers instructions %v, want v1", got)
	}
	loader.result = map[string]any{"instructions": "v2", "content_hash": "hash-v2"}
	second := load(&mockSkillState{loaded: map[string]bool{}})
	if second.LoadedSkillContentHash != "hash-v1" {
		t.Errorf("a second session's load reports %q, want hash-v1: the agent still holds the first copy", second.LoadedSkillContentHash)
	}
	if got := second.LLMData[agent.SkillInstructionsDataKey]; got != "v1" {
		t.Errorf("a second session's load delivers instructions %v, want v1 to match the version it reports", got)
	}
	repeat := load(first)
	if repeat.LoadedSkillContentHash != "hash-v1" {
		t.Errorf("a repeat load in the first session reports %q, want hash-v1", repeat.LoadedSkillContentHash)
	}
	if got := repeat.LLMData[agent.SkillInstructionsDataKey]; got != "v1" {
		t.Errorf("a repeat load delivers %v, want the held v1 instructions again", got)
	}

	plain := NewManager()
	plain.AddAvailable(&SkillInfo{Name: "notes"})
	unarchived := NewLoadSkillTool(plain, &mockSkillLoader{result: map[string]any{"instructions": "notes"}}, &countingRegistrar{})
	res, err := unarchived.Execute(t.Context(), map[string]any{"name": "notes"},
		&tool.Context{SkillState: &mockSkillState{loaded: map[string]bool{}}})
	if err != nil || res.LoadedSkillContentHash != "" {
		t.Errorf("a skill with no archive -> %+v (%v), want no version", res, err)
	}
}
