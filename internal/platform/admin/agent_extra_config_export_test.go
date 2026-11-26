// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"reflect"
	"testing"
)

func TestAgentExtraConfigKeys_MirrorsTheRegistry(t *testing.T) {
	keys := AgentExtraConfigKeys()
	if len(keys) != len(agentExtraConfigSchema) {
		t.Fatalf("exported %d keys, registry has %d", len(keys), len(agentExtraConfigSchema))
	}
	card := keys["agentCard"]
	if card.Runtime != "agent_card" || card.Fields["documentationUrl"].Runtime != "documentationUrl" {
		t.Errorf("agentCard = %+v", card)
	}
	suggestions := card.Fields["welcome"].Fields["suggestions"]
	if suggestions.Fields != nil || suggestions.Items["autoSend"].Runtime != "auto_send" {
		t.Errorf("a typed list must expose Items, not Fields: %+v", suggestions)
	}
	if out := keys["outputSchema"]; out.Runtime != "output_schema" || out.Fields != nil || out.Items != nil {
		t.Errorf("outputSchema must be opaque: %+v", out)
	}
}

func TestReverseTranslateAgentExtraConfig_KeepsExplicitNull(t *testing.T) {
	got := ReverseTranslateAgentExtraConfig(map[string]any{
		"output_schema": nil, "auto_summarization": map[string]any{"summary_max_tokens": nil},
	})
	want := map[string]any{"outputSchema": nil, "autoSummarization": map[string]any{"summaryMaxTokens": nil}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
