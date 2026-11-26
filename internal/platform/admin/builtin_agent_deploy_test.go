// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestBuiltinOrchestratorAdditionalConfig_TranslatesToRuntimeKeys(t *testing.T) {
	req, err := buildBuiltinOrchestratorRequest()
	if err != nil {
		t.Fatalf("buildBuiltinOrchestratorRequest: %v", err)
	}

	agent := &store.Agent{ID: "test", AdditionalConfigurations: req.AdditionalConfigurations}

	appConfig := map[string]any{
		"inter_agent_communication": map[string]any{"allow_list": []string{}},
		"agent_card":                map[string]any{"description": "x"},
	}
	mergeAdditionalConfigurations(slog.New(slog.NewTextHandler(io.Discard, nil)), appConfig, agent)

	iac, ok := appConfig["inter_agent_communication"].(map[string]any)
	if !ok {
		t.Fatalf("inter_agent_communication missing or wrong type: %T", appConfig["inter_agent_communication"])
	}
	allow, _ := iac["allow_list"].([]any)
	if len(allow) != 1 || allow[0] != "*" {
		t.Errorf("allow_list = %v, want [*]", iac["allow_list"])
	}

	card, ok := appConfig["agent_card"].(map[string]any)
	if !ok {
		t.Fatalf("agent_card missing: %T", appConfig["agent_card"])
	}
	welcome, ok := card["welcome"].(map[string]any)
	if !ok {
		t.Fatalf("agent_card.welcome missing: %T", card["welcome"])
	}
	if msg, _ := welcome["message"].(string); msg == "" {
		t.Error("welcome.message is empty after merge")
	}
	suggestions, _ := welcome["suggestions"].([]any)
	if len(suggestions) == 0 {
		t.Fatal("welcome.suggestions empty after merge")
	}
	first, _ := suggestions[0].(map[string]any)
	if _, ok := first["auto_send"]; !ok {
		t.Errorf("welcome.suggestions[0] missing runtime key auto_send; got %v", first)
	}
}
