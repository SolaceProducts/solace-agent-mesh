// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type readOnlyTestTool struct {
	testBuiltinTool
	tool.ReadOnlyTool
}

func TestAgentRegisteredToolsDeclareSideEffects(t *testing.T) {
	t.Parallel()
	for _, tl := range []tool.Tool{&subTaskTool{}, &peerAgentTool{peerName: "p"}, &workflowAgentTool{peerName: "w"}} {
		if got := tool.EffectClassOf(tl); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
			t.Errorf("%T: EffectClassOf = %+v; delegating to another agent or workflow can do anything, so it must be declared side_effecting", tl, got)
		}
	}
}

func TestAgentCard_ToolEffectsIncludeOperatorOverride(t *testing.T) {
	t.Parallel()
	ag := New(AgentConfig{Name: "card-effects", Namespace: "ns"})
	for _, tl := range []tool.Tool{
		&readOnlyTestTool{testBuiltinTool: testBuiltinTool{name: "lookup", description: "Looks up"}},
		&readOnlyTestTool{testBuiltinTool: testBuiltinTool{name: "tightened", description: "Tightened"}},
		&testBuiltinTool{name: "unclassified", description: "Says nothing"},
	} {
		if err := ag.Tools().Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	ag.Tools().SetEffectOverrides(map[string]tool.EffectClass{
		"tightened": {Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator},
	})

	want := map[string][2]string{
		"lookup":       {"read_only", "declared"},
		"tightened":    {"side_effecting", "operator"},
		"unclassified": {"side_effecting", "default"},
	}
	card := ag.BuildAgentCard()
	var seen int
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != "https://solace.com/a2a/extensions/sam/tools" {
			continue
		}
		for _, raw := range ext.Params["tools"].([]any) {
			entry := raw.(map[string]any)
			name := entry["name"].(string)
			w, ok := want[name]
			if !ok {
				continue
			}
			seen++
			if entry["effect"] != w[0] || entry["effect_source"] != w[1] {
				t.Errorf("%s: card effect %v/%v, want %s/%s", name, entry["effect"], entry["effect_source"], w[0], w[1])
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("found %d of %d tools on the card", seen, len(want))
	}
}
