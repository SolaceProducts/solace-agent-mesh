// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func AssertLLMRequests(t *testing.T, recorded []mockllm.RecordedRequest, interactions []LLMInteraction) {
	t.Helper()

	reqIdx := 0
	for i, interaction := range interactions {
		if interaction.IsError() {
			reqIdx++
			continue
		}
		if interaction.ExpectedRequest == nil {
			reqIdx++
			continue
		}
		if reqIdx >= len(recorded) {
			t.Errorf("llm_interaction[%d]: expected request assertion but only %d requests were recorded", i, len(recorded))
			reqIdx++
			continue
		}

		assertLLMRequest(t, i, reqIdx, recorded[reqIdx], interaction.ExpectedRequest, recorded)
		reqIdx++
	}
}

func assertLLMRequest(t *testing.T, interactionIdx, reqIdx int, req mockllm.RecordedRequest, expected *ExpectedLLMRequest, allRecorded []mockllm.RecordedRequest) {
	t.Helper()
	prefix := func(msg string) string {
		return strings.Join([]string{"llm_interaction[", itoa(interactionIdx), "]: ", msg}, "")
	}

	for _, name := range expected.ToolsPresent {
		found := false
		for _, tool := range req.Tools {
			if tool.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s tool %q not present in request (have %d tools)", prefix("tools_present"), name, len(req.Tools))
		}
	}

	for _, name := range expected.ToolsAbsent {
		for _, tool := range req.Tools {
			if tool.Name == name {
				t.Errorf("%s tool %q should be absent but was present", prefix("tools_absent"), name)
			}
		}
	}

	assertToolsDeferred(t, prefix, req, expected.ToolsDeferred, true)
	assertToolsDeferred(t, prefix, req, expected.ToolsNotDeferred, false)

	if len(expected.PromptContains) > 0 {
		allContent := concatenateMessageContent(req)
		for _, sub := range expected.PromptContains {
			if !strings.Contains(allContent, sub) {
				t.Errorf("%s prompt does not contain %q", prefix("prompt_contains"), sub)
			}
		}
	}

	if len(expected.PromptNotContains) > 0 {
		allContent := concatenateMessageContent(req)
		for _, sub := range expected.PromptNotContains {
			if strings.Contains(allContent, sub) {
				t.Errorf("%s prompt should not contain %q but does", prefix("prompt_not_contains"), sub)
			}
		}
	}

	for _, msg := range systemPromptAssertionErrors(req, expected) {
		t.Errorf("%s %s", prefix("system_prompt"), msg)
	}

	if len(expected.PromptContainsArtifactSummaryFor) > 0 {
		allContent := concatenateMessageContent(req)
		for _, ref := range expected.PromptContainsArtifactSummaryFor {
			if ref.Filename == "" {
				continue
			}
			h1 := fmt.Sprintf("--- Metadata for artifact '%s' (v%d) ---", ref.Filename, ref.Version)
			h2 := fmt.Sprintf("Artifact: '%s' (version: %d)", ref.Filename, ref.Version)
			h3 := fmt.Sprintf("--- Metadata for artifact '%s' ---", ref.Filename)
			h4 := fmt.Sprintf("--- Metadata for artifact '%s' (vN/A) ---", ref.Filename)
			if !strings.Contains(allContent, h1) &&
				!strings.Contains(allContent, h2) &&
				!strings.Contains(allContent, h3) &&
				!strings.Contains(allContent, h4) {
				t.Errorf("%s prompt does not contain artifact summary for %q v%d\n  expected one of:\n    %s\n    %s\n    %s\n    %s",
					prefix("prompt_contains_artifact_summary_for"), ref.Filename, ref.Version, h1, h2, h3, h4)
			}
		}
	}

	matchedToolMsgIndices := make(map[int]bool)
	for j, trm := range expected.ExpectedToolResponsesInLLMMessages {
		assertToolResponseInMessages(t, prefix("tool_response["+itoa(j)+"]"), reqIdx, req, trm, allRecorded, matchedToolMsgIndices)
	}

	for j, em := range expected.Messages {
		assertExpectedMessage(t, prefix("messages["+itoa(j)+"]"), req, em)
	}

	if expected.SystemMessageHasCacheControl != nil {
		assertSystemMessageCacheControl(t, prefix("system_message_has_cache_control"), req, *expected.SystemMessageHasCacheControl, expected.CacheControlTTL)
	}

	if expected.LastToolHasCacheControl != nil {
		assertLastToolCacheControl(t, prefix("last_tool_has_cache_control"), req, *expected.LastToolHasCacheControl, expected.CacheControlTTL)
	}
}

func assertExpectedMessage(t *testing.T, prefix string, req mockllm.RecordedRequest, em ExpectedMessage) {
	t.Helper()

	for _, msg := range req.Messages {
		if em.Role != "" && msg.Role != em.Role {
			continue
		}
		if em.ToolCallID != "" && msg.ToolCallID != em.ToolCallID {
			continue
		}
		if em.ContentContains != "" {
			if !strings.Contains(msg.Content, em.ContentContains) {
				t.Errorf("%s: message (role=%s, tool_call_id=%s) does not contain %q\n  content: %s",
					prefix, em.Role, em.ToolCallID, em.ContentContains, truncate(msg.Content, 300))
			}
		}
		if em.ImageURLCount != nil {
			count := 0
			for _, cb := range msg.ContentBlocks {
				if cb.Type == "image_url" {
					count++
				}
			}
			if count != *em.ImageURLCount {
				t.Errorf("%s: message (role=%s) image_url_count = %d, want %d",
					prefix, em.Role, count, *em.ImageURLCount)
			}
		}
		return
	}
	t.Errorf("%s: no matching message found (role=%q, tool_call_id=%q)", prefix, em.Role, em.ToolCallID)
}

func toolResponseTextErrors(content string, trm ToolResponseMatch) []error {
	var errs []error
	for _, sub := range trm.ResponseContains {
		if !strings.Contains(content, sub) {
			errs = append(errs, fmt.Errorf("does not contain %q", sub))
		}
	}
	for _, sub := range trm.ResponseNotContains {
		if strings.Contains(content, sub) {
			errs = append(errs, fmt.Errorf("contains %q, which it must not", sub))
		}
	}
	return errs
}

func assertToolResponseInMessages(t *testing.T, prefix string, reqIdx int, req mockllm.RecordedRequest, trm ToolResponseMatch, allRecorded []mockllm.RecordedRequest, matched map[int]bool) {
	t.Helper()

	for msgIdx, msg := range req.Messages {
		if msg.Role != "tool" {
			continue
		}

		if matched[msgIdx] {
			continue
		}

		if trm.ToolName != "" && msg.ToolName != trm.ToolName {
			continue
		}

		if trm.ToolCallIDMatchesPriorRequestIdx != nil {
			toolCallIdx := *trm.ToolCallIDMatchesPriorRequestIdx
			expectedCallID := findToolCallIDByIndex(allRecorded, reqIdx, toolCallIdx)
			if expectedCallID == "" {
				t.Errorf("%s: prior request tool call index %d not found (reqIdx=%d)", prefix, toolCallIdx, reqIdx)
				return
			}
			if msg.ToolCallID != expectedCallID {
				continue
			}
		}

		matched[msgIdx] = true
		asserted := false

		if len(trm.ResponseContains) > 0 || len(trm.ResponseNotContains) > 0 {
			asserted = true
			for _, textErr := range toolResponseTextErrors(msg.Content, trm) {
				t.Errorf("%s: tool response for %q %v\n  content: %s", prefix, trm.ToolName, textErr, truncate(msg.Content, 200))
			}
		}

		if len(trm.ResponseJSONMatches) > 0 {
			asserted = true
			if err := MatchJSONContains([]byte(msg.Content), trm.ResponseJSONMatches); err != nil {
				t.Errorf("%s: tool response JSON mismatch: %v\n  actual: %s", prefix, err, truncate(msg.Content, 500))
			}
		}

		if len(trm.ResponseJSONContains) > 0 {
			asserted = true
			if err := MatchJSONContains([]byte(msg.Content), trm.ResponseJSONContains); err != nil {
				t.Errorf("%s: tool response JSON contains mismatch: %v\n  actual: %s", prefix, err, truncate(msg.Content, 500))
			}
		}

		_ = asserted
		return
	}

	if trm.ToolName != "" {
		t.Errorf("%s: no tool response found for tool %q", prefix, trm.ToolName)
		return
	}
	t.Errorf("%s: no unmatched tool response found (none present, or an earlier entry consumed it and these assertions belong in that entry)", prefix)
}

func findToolCallIDByIndex(allRecorded []mockllm.RecordedRequest, reqIdx, toolCallIdx int) string {
	if reqIdx < 0 || reqIdx >= len(allRecorded) {
		return ""
	}
	req := allRecorded[reqIdx]

	var lastToolCalls []string
	for _, msg := range req.Messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			lastToolCalls = nil
			for _, tc := range msg.ToolCalls {
				lastToolCalls = append(lastToolCalls, tc.ID)
			}
		}
	}

	if toolCallIdx >= len(lastToolCalls) {
		return ""
	}
	return lastToolCalls[toolCallIdx]
}

func concatenateMessageContent(req mockllm.RecordedRequest) string {
	var b strings.Builder
	for _, msg := range req.Messages {
		b.WriteString(msg.Content)
		b.WriteString("\n")
		for _, tc := range msg.ToolCalls {
			b.WriteString(tc.Arguments)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func systemPromptAssertionErrors(req mockllm.RecordedRequest, expected *ExpectedLLMRequest) []string {
	if len(expected.SystemPromptContains)+len(expected.SystemPromptNotContains) == 0 {
		return nil
	}
	systemPrompt, hasSystem := systemMessageContent(req)
	if !hasSystem {
		return []string{"request has no system message, so a system-prompt assertion cannot hold"}
	}
	var errs []string
	for _, sub := range expected.SystemPromptContains {
		if !strings.Contains(systemPrompt, sub) {
			errs = append(errs, fmt.Sprintf("system prompt does not contain %q", sub))
		}
	}
	for _, sub := range expected.SystemPromptNotContains {
		if strings.Contains(systemPrompt, sub) {
			errs = append(errs, fmt.Sprintf("system prompt should not contain %q but does", sub))
		}
	}
	return errs
}

func systemMessageContent(req mockllm.RecordedRequest) (string, bool) {
	var b strings.Builder
	found := false
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			found = true
			b.WriteString(msg.Content)
			b.WriteString("\n")
		}
	}
	return b.String(), found
}

func assertSystemMessageCacheControl(t *testing.T, prefix string, req mockllm.RecordedRequest, expectSet bool, expectedTTL *string) {
	t.Helper()
	for _, msg := range req.Messages {
		if msg.Role != "system" {
			continue
		}
		hasCC := msg.CacheControl != nil
		if expectSet && !hasCC {
			t.Errorf("%s: system message should have cache_control but doesn't", prefix)
		} else if !expectSet && hasCC {
			t.Errorf("%s: system message should not have cache_control but does", prefix)
		}
		if hasCC && expectedTTL != nil {
			if msg.CacheControl.TTL != *expectedTTL {
				t.Errorf("%s: system message cache_control TTL = %q, want %q", prefix, msg.CacheControl.TTL, *expectedTTL)
			}
		}
		return
	}
	if expectSet {
		t.Errorf("%s: no system message found to check cache_control", prefix)
	}
}

func assertLastToolCacheControl(t *testing.T, prefix string, req mockllm.RecordedRequest, expectSet bool, expectedTTL *string) {
	t.Helper()
	if len(req.Tools) == 0 {
		if expectSet {
			t.Errorf("%s: no tools present to check cache_control", prefix)
		}
		return
	}
	lastTool := req.Tools[len(req.Tools)-1]
	hasCC := lastTool.CacheControl != nil
	if expectSet && !hasCC {
		t.Errorf("%s: last tool %q should have cache_control but doesn't", prefix, lastTool.Name)
	} else if !expectSet && hasCC {
		t.Errorf("%s: last tool %q should not have cache_control but does", prefix, lastTool.Name)
	}
	if hasCC && expectedTTL != nil {
		if lastTool.CacheControl.TTL != *expectedTTL {
			t.Errorf("%s: last tool cache_control TTL = %q, want %q", prefix, lastTool.CacheControl.TTL, *expectedTTL)
		}
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

func assertToolsDeferred(t *testing.T, prefix func(string) string, req mockllm.RecordedRequest, names []string, want bool) {
	t.Helper()
	key := "tools_not_deferred"
	if want {
		key = "tools_deferred"
	}
	for _, name := range names {
		idx := slices.IndexFunc(req.Tools, func(d llm.ToolDefinition) bool { return d.Name == name })
		switch {
		case idx < 0:
			t.Errorf("%s tool %q not present in request (have %d tools)", prefix(key), name, len(req.Tools))
		case req.Tools[idx].DeferLoading != want:
			t.Errorf("%s tool %q DeferLoading = %v, want %v", prefix(key), name, req.Tools[idx].DeferLoading, want)
		}
	}
}
