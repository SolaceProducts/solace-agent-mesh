// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func newToolSearchAgent(t *testing.T, flagOn bool) *Agent {
	t.Helper()
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyToolSearch, Default: flagOn})
	agent.svc.(*mockServices).featureRegistry = reg

	for _, name := range []string{"jira_search", "load_artifact", "peer_research", LoadSkillToolName} {
		if err := agent.Tools().Register(&testBuiltinTool{name: name, description: name}); err != nil {
			t.Fatal(err)
		}
	}
	return agent
}

func deferredNames(defs []llm.ToolDefinition) map[string]bool {
	out := map[string]bool{}
	for _, d := range defs {
		if d.DeferLoading {
			out[d.Name] = true
		}
	}
	return out
}

func TestBuildLLMTools_ToolSearchDefersAllButLoadSkillAndInternalTools(t *testing.T) {
	agent := newToolSearchAgent(t, true)

	defs := agent.buildLLMTools(t.Context(), nil, nil, "")
	got := deferredNames(defs)

	for _, name := range []string{"jira_search", "load_artifact", "peer_research"} {
		if !got[name] {
			t.Errorf("%s not deferred; deferred = %v", name, got)
		}
	}
	for _, d := range defs {
		if (d.Name == LoadSkillToolName || strings.HasPrefix(d.Name, "_")) && d.DeferLoading {
			t.Errorf("%s deferred, want it loaded", d.Name)
		}
	}
}

func TestBuildLLMTools_ToolSearchFlagOffDefersNothing(t *testing.T) {
	agent := newToolSearchAgent(t, false)

	if got := deferredNames(agent.buildLLMTools(t.Context(), nil, nil, "")); len(got) > 0 {
		t.Errorf("deferred = %v with the flag off, want none", got)
	}
}
