// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func TestSystemPromptAssertionsLookOnlyAtTheSystemMessage(t *testing.T) {
	const marker = "SKILL_MARKER"
	inSystem := mockllm.RecordedRequest{Messages: []llm.ChatMessage{{Role: "system", Content: "you are " + marker}, {Role: "user", Content: "hi"}}}
	inToolResult := mockllm.RecordedRequest{Messages: []llm.ChatMessage{{Role: "system", Content: "you are an agent"}, {Role: "tool", Content: marker}}}
	noSystem := mockllm.RecordedRequest{Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	contains := &ExpectedLLMRequest{SystemPromptContains: []string{marker}}
	notContains := &ExpectedLLMRequest{SystemPromptNotContains: []string{marker}}

	cases := []struct {
		name     string
		req      mockllm.RecordedRequest
		expected *ExpectedLLMRequest
		wantFail bool
	}{
		{"contains: marker in the system message", inSystem, contains, false},
		{"contains: marker only in a tool result", inToolResult, contains, true},
		{"not contains: marker in the system message", inSystem, notContains, true},
		{"not contains: marker only in a tool result", inToolResult, notContains, false},
		{"not contains: no system message to check", noSystem, notContains, true},
		{"contains: no system message to check", noSystem, contains, true},
		{"no system-prompt assertion", noSystem, &ExpectedLLMRequest{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := systemPromptAssertionErrors(tc.req, tc.expected)
			if got := len(errs) > 0; got != tc.wantFail {
				t.Errorf("failed = %v (%v), want %v", got, errs, tc.wantFail)
			}
		})
	}
}
