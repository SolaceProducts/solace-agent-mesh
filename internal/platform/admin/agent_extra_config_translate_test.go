// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
)

func TestTranslateAgentExtraConfig_NilAndEmptyBlobs(t *testing.T) {
	t.Parallel()

	if got := translateAgentExtraConfig(nil, agentExtraConfigSchema, nil, "agent-id"); got != nil {
		t.Errorf("forward: expected nil for nil input, got %v", got)
	}
	if got := translateAgentExtraConfig(map[string]any{}, agentExtraConfigSchema, nil, "agent-id"); got != nil {
		t.Errorf("forward: expected nil for empty map, got %v", got)
	}

	if got := reverseTranslateAgentExtraConfig(nil, agentExtraConfigSchema); got != nil {
		t.Errorf("reverse: expected nil for nil input, got %v", got)
	}
	empty := map[string]any{}
	if got := reverseTranslateAgentExtraConfig(empty, agentExtraConfigSchema); len(got) != 0 {
		t.Errorf("reverse: expected empty map for empty input, got %v", got)
	}
}

func TestTranslateAgentExtraConfig_RecursesThroughMixedShapes(t *testing.T) {
	t.Parallel()

	in := map[string]any{
		"agentCardPublishing": "broken-shape-on-purpose",
	}
	out := translateAgentExtraConfig(in, agentExtraConfigSchema, nil, "agent-id")
	if got, _ := out["agent_card_publishing"].(string); got != "broken-shape-on-purpose" {
		t.Errorf("expected mis-typed authored value to land under translated key verbatim, got: %v", out)
	}
	if _, present := out["agentCardPublishing"]; present {
		t.Errorf("translated key collision: camelCase form leaked into output: %v", out)
	}
}

func TestReverseTranslateAgentExtraConfig_MisShapedObjectFallback(t *testing.T) {
	t.Parallel()

	in := map[string]any{
		"agent_card_publishing": "broken-shape-on-purpose",
	}
	out := reverseTranslateAgentExtraConfig(in, agentExtraConfigSchema)
	if got, _ := out["agentCardPublishing"].(string); got != "broken-shape-on-purpose" {
		t.Errorf("expected mis-typed value to land under canonical key verbatim, got: %v", out)
	}
}

func TestTranslateAgentExtraConfig_WelcomeSuggestionsRecurse(t *testing.T) {
	t.Parallel()

	in := map[string]any{
		"agentCard": map[string]any{
			"welcome": map[string]any{
				"message": "hi",
				"suggestions": []any{
					map[string]any{
						"label":    "Summarise",
						"prompt":   "Summarise the doc.",
						"autoSend": true,
					},
					map[string]any{
						"label":  "Reply",
						"prompt": "Draft a reply.",
					},
					"non-map-item-should-pass-through",
				},
			},
		},
	}

	out := translateAgentExtraConfig(in, agentExtraConfigSchema, nil, "agent-id")

	card, ok := out["agent_card"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_card map, got: %T", out["agent_card"])
	}
	welcome, ok := card["welcome"].(map[string]any)
	if !ok {
		t.Fatalf("expected welcome map, got: %T", card["welcome"])
	}
	if got, _ := welcome["message"].(string); got != "hi" {
		t.Errorf("expected welcome.message='hi' to round-trip identity, got: %v", welcome["message"])
	}
	sugs, ok := welcome["suggestions"].([]any)
	if !ok {
		t.Fatalf("expected suggestions []any, got: %T", welcome["suggestions"])
	}
	if len(sugs) != 3 {
		t.Fatalf("expected 3 suggestions, got %d (%v)", len(sugs), sugs)
	}

	first, ok := sugs[0].(map[string]any)
	if !ok {
		t.Fatalf("expected suggestions[0] map, got: %T", sugs[0])
	}
	if got, _ := first["label"].(string); got != "Summarise" {
		t.Errorf("expected suggestions[0].label identity, got: %v", first["label"])
	}
	if got, _ := first["prompt"].(string); got != "Summarise the doc." {
		t.Errorf("expected suggestions[0].prompt identity, got: %v", first["prompt"])
	}
	if got, _ := first["auto_send"].(bool); !got {
		t.Errorf("expected suggestions[0].auto_send=true (camelCase autoSend flipped), got: %v", first["auto_send"])
	}
	if _, present := first["autoSend"]; present {
		t.Errorf("camelCase 'autoSend' leaked into translated item: %v", first)
	}

	second, ok := sugs[1].(map[string]any)
	if !ok {
		t.Fatalf("expected suggestions[1] map, got: %T", sugs[1])
	}
	if _, present := second["auto_send"]; present {
		t.Errorf("suggestions[1] omitted autoSend on input; translator must not synthesise it: %v", second)
	}

	if got, _ := sugs[2].(string); got != "non-map-item-should-pass-through" {
		t.Errorf("expected non-map suggestion item to pass through verbatim, got: %v (%T)", sugs[2], sugs[2])
	}
}

func TestReverseTranslateAgentExtraConfig_WelcomeSuggestionsRecurse(t *testing.T) {
	t.Parallel()

	in := map[string]any{
		"agent_card": map[string]any{
			"welcome": map[string]any{
				"message": "hi",
				"suggestions": []any{
					map[string]any{
						"label":     "Summarise",
						"prompt":    "Summarise the doc.",
						"auto_send": true,
					},
				},
			},
		},
	}

	out := reverseTranslateAgentExtraConfig(in, agentExtraConfigSchema)

	card, ok := out["agentCard"].(map[string]any)
	if !ok {
		t.Fatalf("expected agentCard map, got: %T", out["agentCard"])
	}
	welcome, ok := card["welcome"].(map[string]any)
	if !ok {
		t.Fatalf("expected welcome map, got: %T", card["welcome"])
	}
	sugs, ok := welcome["suggestions"].([]any)
	if !ok {
		t.Fatalf("expected suggestions []any, got: %T", welcome["suggestions"])
	}
	if len(sugs) != 1 {
		t.Fatalf("expected 1 suggestion, got %d (%v)", len(sugs), sugs)
	}
	first, ok := sugs[0].(map[string]any)
	if !ok {
		t.Fatalf("expected suggestions[0] map, got: %T", sugs[0])
	}
	if got, _ := first["autoSend"].(bool); !got {
		t.Errorf("expected suggestions[0].autoSend=true (snake_case auto_send flipped back), got: %v", first["autoSend"])
	}
	if _, present := first["auto_send"]; present {
		t.Errorf("snake_case 'auto_send' leaked into reverse-translated item: %v", first)
	}
}
