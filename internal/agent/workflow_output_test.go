// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func workflowCard(description string, outputSchema, outputMapping map[string]any) a2a.AgentCard {
	card := a2a.AgentCard{Name: "workflow_doc", Description: description}
	if outputSchema != nil {
		card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
			URI:    ExtensionURISchemas,
			Params: map[string]any{"output_schema": outputSchema},
		})
	}
	if outputMapping != nil {
		card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
			URI:    a2a.ExtensionURIWorkflowVisualization,
			Params: map[string]any{"workflow_config": map[string]any{"output_mapping": outputMapping}},
		})
	}
	return card
}

func TestWorkflowToolDescription_UnchangedWhenNoOutputDeclared(t *testing.T) {
	t.Parallel()
	card := a2a.AgentCard{Name: "workflow_doc", Description: "Summarizes."}
	wt := &workflowAgentTool{peerName: card.Name, card: card}
	if got, want := wt.Description(), `Invoke the "workflow_doc" workflow. Summarizes.`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWorkflowToolDescription_NamesTheOutputTheCardDeclares(t *testing.T) {
	t.Parallel()
	mapping := map[string]any{"spanish_summary": "{{t.output.translation}}", "english_summary": "{{s.output.summary}}"}
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"translation": map[string]any{"type": "string"},
		"keywords":    map[string]any{"type": "array"},
		"untyped":     map[string]any{},
	}}

	tests := []struct {
		name string
		card a2a.AgentCard
		want string
	}{
		{"output_mapping keys when there is no output_schema", workflowCard("Summarizes.", nil, mapping),
			`Invoke the "workflow_doc" workflow. Summarizes. Declared output fields: english_summary, spanish_summary.`},
		{"output_schema properties win over the mapping", workflowCard("Summarizes.", schema, mapping),
			`Invoke the "workflow_doc" workflow. Summarizes. Declared output fields: keywords (array), translation (string), untyped.`},
		{"a schema without properties falls back to the mapping", workflowCard("Summarizes.", map[string]any{"type": "object"}, mapping),
			`Invoke the "workflow_doc" workflow. Summarizes. Declared output fields: english_summary, spanish_summary.`},
		{"empty properties fall back to the mapping", workflowCard("Summarizes.", map[string]any{"type": "object", "properties": map[string]any{}}, mapping),
			`Invoke the "workflow_doc" workflow. Summarizes. Declared output fields: english_summary, spanish_summary.`},
		{"no description still names the fields", workflowCard("", nil, mapping),
			`Invoke the "workflow_doc" workflow. Declared output fields: english_summary, spanish_summary.`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wt := &workflowAgentTool{peerName: tt.card.Name, card: tt.card}
			if got := wt.Description(); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestWorkflowToolDescription_OutputTextIsBounded(t *testing.T) {
	t.Parallel()
	props := map[string]any{}
	for i := range 2000 {
		props[fmt.Sprintf("field_%04d", i)] = map[string]any{"type": "string"}
	}
	card := workflowCard("Summarizes.", map[string]any{"type": "object", "properties": props}, nil)
	wt := &workflowAgentTool{peerName: card.Name, card: card}
	desc := wt.Description()
	_, declared, found := strings.Cut(desc, a2a.DeclaredOutputLabel)
	if !found {
		t.Fatalf("description names no declared output: %q", desc)
	}
	if len(declared) > a2a.MaxDeclaredOutputBytes || !strings.Contains(declared, ", and ") || !strings.HasSuffix(declared, " more.") {
		t.Fatalf("declared output is %d bytes and not cut as expected (cap %d): ...%s", len(declared), a2a.MaxDeclaredOutputBytes, declared[len(declared)-40:])
	}
	for range 5 {
		if wt.Description() != desc {
			t.Fatal("description is not deterministic")
		}
	}
}

func TestToolDescriptionsRoundTripThroughDeclared(t *testing.T) {
	t.Parallel()
	mapping := map[string]any{"b": "{{x}}", "a": "{{y}}"}
	skills := []a2a.AgentSkill{{Name: "translate"}, {Name: "detect"}}
	caller := New(AgentConfig{Name: "caller"})

	tests := []struct {
		name string
		desc string
		want string
	}{
		{"workflow", (&workflowAgentTool{peerName: "wf", card: workflowCard("Summarizes then translates.", nil, mapping)}).Description(),
			"Summarizes then translates. Declared output fields: a, b."},
		{"workflow declaring nothing", (&workflowAgentTool{peerName: "wf", card: a2a.AgentCard{Name: "wf"}}).Description(), ""},
		{"workflow with only a mapping", (&workflowAgentTool{peerName: "wf", card: workflowCard("", nil, mapping)}).Description(),
			"Declared output fields: a, b."},
		{"agent", (&peerAgentTool{peerName: "ag", agent: caller, card: a2a.AgentCard{Name: "ag", Description: "Translates to Spanish."}}).Description(),
			"Translates to Spanish."},
		{"agent with a description and skills", (&peerAgentTool{peerName: "ag", agent: caller, card: a2a.AgentCard{Name: "ag", Description: "Translates to Spanish.", Skills: skills}}).Description(),
			"Translates to Spanish."},
		{"agent with skills and no description", (&peerAgentTool{peerName: "ag", agent: caller, card: a2a.AgentCard{Name: "ag", Skills: skills}}).Description(), ""},
		{"agent declaring nothing", (&peerAgentTool{peerName: "ag", agent: caller, card: a2a.AgentCard{Name: "ag"}}).Description(), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := a2a.Declared(tt.desc); got != tt.want {
				t.Errorf("Declared(%q) = %q, want %q", tt.desc, got, tt.want)
			}
		})
	}
}

func TestPeerToolDescription_CarriesNoDeclaredOutput(t *testing.T) {
	t.Parallel()
	desc := (&peerAgentTool{peerName: "ag", agent: New(AgentConfig{Name: "caller"}), card: a2a.AgentCard{Name: "ag", Description: "Translates."}}).Description()
	if strings.Contains(desc, a2a.DeclaredOutputLabel) {
		t.Errorf("an agent tool must not carry declared output fields: %q", desc)
	}
}
