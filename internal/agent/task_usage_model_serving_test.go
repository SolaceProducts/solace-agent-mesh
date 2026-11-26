// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func runReportingModelServedAt(t *testing.T, modelID, apiBase string) map[string]any {
	t.Helper()
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "done", FinishReason: "stop"})
	agent.cfg.Model = modelID
	agent.cfg.OriginalModel = modelID
	agent.cfg.ModelAPIBase = apiBase

	resp, err := agent.HandleTask(context.Background(), "task-model-serving",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	return finalTaskOf(t, resp).Metadata
}

func TestARunOnAnOpenAICompatibleLocalServerReportsLocal(t *testing.T) {
	cases := []struct {
		name    string
		apiBase string
		want    string
	}{
		{"served from this machine", "http://localhost:11434/v1", taxonomy.ModelTypeLocal},
		{"served from this container's host", "http://host.docker.internal:8000/v1", taxonomy.ModelTypeLocal},
		{"served by the vendor", "https://api.openai.com/v1", taxonomy.ModelTypeHosted},
		{"served by the vendor's default", "", taxonomy.ModelTypeHosted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			metadata := runReportingModelServedAt(t, "gpt-4o", c.apiBase)
			if got := metadata[a2a.MetadataModelProvider]; got != "openai" {
				t.Fatalf("%s = %v, want openai — this is not the case under test", a2a.MetadataModelProvider, got)
			}
			if got := metadata[a2a.MetadataModelType]; got != c.want {
				t.Errorf("%s = %v, want %q", a2a.MetadataModelType, got, c.want)
			}
		})
	}
}

func TestTheApiBaseItselfNeverReachesTheTerminalMessage(t *testing.T) {
	const apiBase = "https://acme-llm.internal.example.com:8443/v1"
	metadata := runReportingModelServedAt(t, "gpt-4o", apiBase)

	rendered, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	for _, fragment := range []string{apiBase, "acme-llm", "internal.example.com", "8443"} {
		if strings.Contains(string(rendered), fragment) {
			t.Errorf("%q reached the terminal metadata %s", fragment, rendered)
		}
	}
	if got := metadata[a2a.MetadataModelType]; got != taxonomy.ModelTypeHosted {
		t.Errorf("%s = %v, want %q — the classification must still be reported", a2a.MetadataModelType, got, taxonomy.ModelTypeHosted)
	}
}

func TestTheRunReportsItsModelAsOneWholeEntry(t *testing.T) {
	metadata := runReportingModelServedAt(t, "claude-sonnet-4-5", "http://localhost:8000/v1")

	want := usagefacts.Model{
		Family:     modelfamily.ClaudeSonnet,
		Provider:   "anthropic",
		ServedFrom: taxonomy.ModelTypeLocal,
	}
	got := usagefacts.ReadModels(metadata[a2a.MetadataModelUsages])
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%s = %+v, want exactly [%+v]", a2a.MetadataModelUsages, got, want)
	}
	if metadata[a2a.MetadataModelFamily] != want.Family ||
		metadata[a2a.MetadataModelProvider] != want.Provider ||
		metadata[a2a.MetadataModelType] != want.ServedFrom {
		t.Errorf("the scalar keys (%v, %v, %v) disagree with the entry %+v",
			metadata[a2a.MetadataModelFamily], metadata[a2a.MetadataModelProvider],
			metadata[a2a.MetadataModelType], want)
	}
}

func TestAnUnresolvedModelReportsNoServingLocation(t *testing.T) {
	var facts taskUsageFacts
	if got := facts.recordModel("", "", ""); got != (usagefacts.Model{}) {
		t.Fatalf("recordModel with nothing resolved returned %+v, want the empty classification", got)
	}
	metadata := map[string]any{}
	facts.AddTo(metadata)
	if len(metadata) != 0 {
		t.Errorf("metadata = %#v, want nothing reported", metadata)
	}
}

func TestTheModelOverrideIsClassifiedByItsOwnApiBase(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.ModelOverride = &llm.BifrostConfig{
		Model:    "gpt-4o",
		Provider: "openai",
		BaseURL:  "http://localhost:11434/v1",
	}

	got := state.usageFacts.recordModel(callModel(state, "claude-sonnet-4-5", "https://api.anthropic.com"))
	want := usagefacts.Model{
		Family:     modelfamily.GPT4o,
		Provider:   "openai",
		ServedFrom: taxonomy.ModelTypeLocal,
	}
	if got != want {
		t.Errorf("recordModel = %+v, want %+v", got, want)
	}
}
