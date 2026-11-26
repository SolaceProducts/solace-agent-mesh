// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsToolChoiceUnsupportedError(t *testing.T) {
	for msg, want := range map[string]bool{
		`tool_choice: type "tool" and "any" are not supported for this model.`: true,
		"This model doesn't support the toolConfig.toolChoice.any field.":      true,
		"Tool_Choice Is UNSUPPORTED for this deployment":                       true,
		"this model does not support a forced tool choice":                     true,
		"`temperature` is deprecated for this model.":                          false,
		"messages: text content blocks must be non-empty":                      false,
		"this model does not support the requested context length":             false,
		"tool_choice references a tool that is not in tools":                   false,
	} {
		if got := (&Error{StatusCode: 400, Message: msg}).IsToolChoiceUnsupportedError(); got != want {
			t.Errorf("%q -> %v, want %v", msg, got, want)
		}
	}
	if (&Error{StatusCode: 503, Message: "tool_choice not supported"}).IsToolChoiceUnsupportedError() {
		t.Error("a 503 was taken for a refused tool choice")
	}
}

func TestModelRefusesForcedToolChoiceNamesOnlyTheTwoPointFiveModels(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-sonnet-5-5":                    true,
		"claude-opus-5-5":                      true,
		"anthropic/claude-sonnet-5-5":          true,
		"anthropic/claude-opus-5-5":            true,
		"Anthropic/Claude-Sonnet-5-5":          true,
		"anthropic.claude-sonnet-5-5-v1:0":     true,
		"us.anthropic.claude-opus-5-5-v1:0":    true,
		"claude-sonnet-5-5-20261001":           true,
		"claude-sonnet-5.5":                    true,
		"claude-sonnet-5":                      false,
		"claude-opus-5":                        false,
		"claude-haiku-5-5":                     false,
		"claude-sonnet-4-6":                    false,
		"claude-opus-4-8":                      false,
		"claude-sonnet-5-50":                   false,
		"claude-sonnet-5-55":                   false,
		"anthropic/claude-sonnet-4-5-20250929": false,
		"gpt-5.5":                              false,
		"general":                              false,
		"":                                     false,
	} {
		if got := ModelRefusesForcedToolChoice(model); got != want {
			t.Errorf("ModelRefusesForcedToolChoice(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestRefusesToolChoiceLooksThroughTheWholeErrorTree(t *testing.T) {
	refusal := &Error{StatusCode: 400, Message: `tool_choice: type "tool" and "any" are not supported for this model.`}
	temperature := &Error{StatusCode: 400, Message: "`temperature` is deprecated for this model."}
	for name, err := range map[string]error{
		"alone":         refusal,
		"wrapped":       fmt.Errorf("model call: %w", refusal),
		"joined second": errors.Join(temperature, refusal),
		"nested":        fmt.Errorf("call: %w", errors.Join(temperature, fmt.Errorf("retry: %w", refusal))),
	} {
		if !RefusesToolChoice(err) {
			t.Errorf("%s: the refusal was not found", name)
		}
	}
	for name, err := range map[string]error{
		"nil":         nil,
		"temperature": errors.Join(temperature, context.DeadlineExceeded),
		"other":       errors.New("tool_choice not supported"),
	} {
		if RefusesToolChoice(err) {
			t.Errorf("%s: taken for a refused tool choice", name)
		}
	}
}
