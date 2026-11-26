// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestChunkAccumulatorReasoningDelta(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ReasoningDelta: "Let me think..."},
		llm.StreamChunk{ReasoningDelta: " about this."},
		llm.StreamChunk{ContentDelta: "The answer is 42."},
		llm.StreamChunk{FinishReason: "stop"},
	)

	var reasoning, text string
	for c := range chunkAccumulator(context.Background(), stream) {
		reasoning += c.reasoningDelta
		text += c.text
	}

	if reasoning != "Let me think... about this." {
		t.Errorf("reasoning = %q", reasoning)
	}
	if text != "The answer is 42." {
		t.Errorf("text = %q", text)
	}
}

func TestChunkAccumulatorToolCallsSortedByIndex(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 2, ID: "c3", Name: "tool_c", ArgumentsDelta: "{}"},
				{Index: 0, ID: "c1", Name: "tool_a", ArgumentsDelta: "{}"},
				{Index: 1, ID: "c2", Name: "tool_b", ArgumentsDelta: "{}"},
			},
			FinishReason: "tool_calls",
		},
	)

	var toolNames []string
	for c := range chunkAccumulator(context.Background(), stream) {
		if c.toolCall != nil {
			toolNames = append(toolNames, c.toolCall.Name)
		}
	}

	if len(toolNames) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(toolNames))
	}
	if toolNames[0] != "tool_a" || toolNames[1] != "tool_b" || toolNames[2] != "tool_c" {
		t.Errorf("tool names = %v, expected [tool_a, tool_b, tool_c]", toolNames)
	}
}

func TestChunkAccumulatorMultipleFinishReasons(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ID: "c1", Name: "search", ArgumentsDelta: `{"q":"test"}`},
			},
			FinishReason: "tool_calls",
		},
		llm.StreamChunk{ContentDelta: "Done."},
		llm.StreamChunk{FinishReason: "stop"},
	)

	var toolCalls int
	var lastFinish string
	for c := range chunkAccumulator(context.Background(), stream) {
		if c.toolCall != nil {
			toolCalls++
		}
		if c.finishReason != "" {
			lastFinish = c.finishReason
		}
	}

	if toolCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", toolCalls)
	}
	if lastFinish != "stop" {
		t.Errorf("last finish reason = %q, want stop", lastFinish)
	}
}

func TestChunkAccumulatorUsageTracking(t *testing.T) {
	usage := &llm.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "Hi"},
		llm.StreamChunk{FinishReason: "stop", Usage: usage},
	)

	var lastUsage *llm.Usage
	for c := range chunkAccumulator(context.Background(), stream) {
		if c.usage != nil {
			lastUsage = c.usage
		}
	}

	if lastUsage == nil {
		t.Fatal("expected usage to be propagated")
	} else if lastUsage.TotalTokens != 30 {
		t.Errorf("total tokens = %d", lastUsage.TotalTokens)
	}
}

func TestChunkAccumulatorContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	stream := make(chan llm.StreamChunk)
	out := chunkAccumulator(ctx, stream)
	cancel()
	for range out {
	}
}

func TestChunkAccumulatorErrorStopsStream(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "partial"},
		llm.StreamChunk{Err: fmt.Errorf("network error")},
		llm.StreamChunk{ContentDelta: "should not appear"},
	)

	var errChunk error
	var textAfterError string
	seenError := false
	for c := range chunkAccumulator(context.Background(), stream) {
		if c.err != nil {
			errChunk = c.err
			seenError = true
		} else if seenError && c.text != "" {
			textAfterError += c.text
		}
	}

	if errChunk == nil {
		t.Fatal("expected error chunk")
	}
	if textAfterError != "" {
		t.Errorf("expected no text after error, got %q", textAfterError)
	}
}

func TestChunkAccumulatorToolCallDeltaIDUpdate(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, Name: "search"},
			},
		},
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ID: "call-late-id", ArgumentsDelta: `{"q":"x"}`},
			},
			FinishReason: "tool_calls",
		},
	)

	var tc *llm.ToolCall
	for c := range chunkAccumulator(context.Background(), stream) {
		if c.toolCall != nil {
			tc = c.toolCall
		}
	}

	if tc == nil {
		t.Fatal("no tool call")
	} else if tc.ID != "call-late-id" {
		t.Errorf("ID = %q, want call-late-id", tc.ID)
	}
	if tc.Name != "search" {
		t.Errorf("Name = %q, want search", tc.Name)
	}
	if tc.Arguments != `{"q":"x"}` {
		t.Errorf("Arguments = %q", tc.Arguments)
	}
}

func TestOutputBatcherBlockContentBatching(t *testing.T) {
	block := &FencedBlockEvent{Type: FencedBlockArtifact, Filename: "test.csv"}
	in := make(chan fencedOutput, 10)
	in <- fencedOutput{outputType: fencedOutputBlockStart, block: block}
	in <- fencedOutput{outputType: fencedOutputBlockContent, text: "ab", block: block}
	in <- fencedOutput{outputType: fencedOutputBlockContent, text: "cd", block: block}
	in <- fencedOutput{outputType: fencedOutputBlockContent, text: "efghijklmn", block: block}
	in <- fencedOutput{outputType: fencedOutputBlockEnd, block: block}
	close(in)

	batcher := newOutputBatcher(10)
	events := collectEvents(batcher.run(context.Background(), in))

	var blockContentText string
	for _, ev := range events {
		if ev.Type == EventBlockContent {
			blockContentText += ev.Text
		}
	}
	if blockContentText != "abcdefghijklmn" {
		t.Errorf("block content = %q", blockContentText)
	}
}

func TestOutputBatcherReasoningFlushesText(t *testing.T) {
	in := make(chan fencedOutput, 10)
	in <- fencedOutput{outputType: fencedOutputText, text: "partial"}
	in <- fencedOutput{outputType: fencedOutputReasoning, reasoningDelta: "thinking..."}
	close(in)

	batcher := newOutputBatcher(100)
	events := collectEvents(batcher.run(context.Background(), in))

	if len(events) < 3 {
		t.Fatalf("expected at least 3 events (text, reasoning, done), got %d", len(events))
	}
	if events[0].Type != EventText || events[0].Text != "partial" {
		t.Errorf("events[0] = type=%d text=%q", events[0].Type, events[0].Text)
	}
	if events[1].Type != EventThinkingContent || events[1].Text != "thinking..." {
		t.Errorf("events[1] = type=%d text=%q", events[1].Type, events[1].Text)
	}
}

func TestOutputBatcherErrorFlushes(t *testing.T) {
	in := make(chan fencedOutput, 10)
	in <- fencedOutput{outputType: fencedOutputText, text: "before"}
	in <- fencedOutput{outputType: fencedOutputError, err: fmt.Errorf("boom")}
	close(in)

	batcher := newOutputBatcher(100)
	events := collectEvents(batcher.run(context.Background(), in))

	if events[0].Type != EventText || events[0].Text != "before" {
		t.Errorf("events[0] = type=%d text=%q", events[0].Type, events[0].Text)
	}
	found := false
	for _, ev := range events {
		if ev.Type == EventError && ev.Err != nil {
			found = true
		}
	}
	if !found {
		t.Error("missing error event")
	}
	for _, ev := range events {
		if ev.Type == EventDone {
			t.Error("should not emit Done after Error")
		}
	}
}

func TestOutputBatcherEmbedToken(t *testing.T) {
	embedRef := &EmbedRef{Type: "math", Params: map[string]string{"expr": "2+2"}, Raw: "<<math:2+2>>"}
	in := make(chan fencedOutput, 10)
	in <- fencedOutput{outputType: fencedOutputText, text: "result: "}
	in <- fencedOutput{
		outputType: fencedOutputText,
		token:      &Token{Type: EmbedToken, Embed: embedRef},
	}
	in <- fencedOutput{outputType: fencedOutputText, text: " done"}
	close(in)

	batcher := newOutputBatcher(100)
	events := collectEvents(batcher.run(context.Background(), in))

	var hasEmbed bool
	for _, ev := range events {
		if ev.Type == EventEmbed {
			hasEmbed = true
			if ev.Embed.Type != "math" {
				t.Errorf("embed type = %q", ev.Embed.Type)
			}
		}
	}
	if !hasEmbed {
		t.Error("missing embed event")
	}
}

func TestOutputBatcherFinishReasonAndUsage(t *testing.T) {
	usage := &llm.Usage{PromptTokens: 5, CompletionTokens: 10, TotalTokens: 15}
	in := make(chan fencedOutput, 10)
	in <- fencedOutput{outputType: fencedOutputText, text: "hello", finishReason: "stop", usage: usage}
	close(in)

	batcher := newOutputBatcher(1)
	events := collectEvents(batcher.run(context.Background(), in))

	last := events[len(events)-1]
	if last.Type != EventDone {
		t.Fatalf("last event type = %d, want EventDone", last.Type)
	}
	if last.FinishReason != "stop" {
		t.Errorf("finish reason = %q", last.FinishReason)
	}
	if last.Usage == nil || last.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", last.Usage)
	}
}

func TestOutputBatcherMinBatchSizeDefault(t *testing.T) {
	b := newOutputBatcher(0)
	if b.minBatchSize != 1 {
		t.Errorf("minBatchSize = %d, want 1", b.minBatchSize)
	}
	b2 := newOutputBatcher(-5)
	if b2.minBatchSize != 1 {
		t.Errorf("minBatchSize = %d, want 1", b2.minBatchSize)
	}
}

func TestOutputBatcherContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	in := make(chan fencedOutput)
	out := newOutputBatcher(1).run(ctx, in)
	cancel()
	for range out {
	}
}

func TestOutputBatcherBlockStartFlushesText(t *testing.T) {
	block := &FencedBlockEvent{Type: FencedBlockArtifact, Filename: "test.csv"}
	in := make(chan fencedOutput, 10)
	in <- fencedOutput{outputType: fencedOutputText, text: "before"}
	in <- fencedOutput{outputType: fencedOutputBlockStart, block: block}
	in <- fencedOutput{outputType: fencedOutputBlockEnd, block: block}
	close(in)

	batcher := newOutputBatcher(100)
	events := collectEvents(batcher.run(context.Background(), in))

	if events[0].Type != EventText || events[0].Text != "before" {
		t.Errorf("events[0] = type=%d text=%q", events[0].Type, events[0].Text)
	}
	if events[1].Type != EventBlockStart {
		t.Errorf("events[1] type = %d, want EventBlockStart", events[1].Type)
	}
}

func TestSanitizeToolCallsAllValid(t *testing.T) {
	logger := slog.Default()
	calls := []llm.ToolCall{
		{ID: "c1", Name: "search", Arguments: "{}"},
		{ID: "c2", Name: "get_weather", Arguments: "{}"},
		{ID: "c3", Name: "_private_tool", Arguments: "{}"},
		{ID: "c4", Name: "tool-with-hyphens", Arguments: "{}"},
	}

	valid, errs := sanitizeToolCalls(calls, logger)
	if len(valid) != 4 {
		t.Errorf("expected 4 valid calls, got %d", len(valid))
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 error messages, got %d", len(errs))
	}
}

func TestSanitizeToolCallsRejectsInvalid(t *testing.T) {
	logger := slog.Default()
	calls := []llm.ToolCall{
		{ID: "c1", Name: "$FUNCTION_NAME", Arguments: "{}"},
		{ID: "c2", Name: "", Arguments: "{}"},
		{ID: "c3", Name: "tool with spaces", Arguments: "{}"},
		{ID: "c4", Name: "123startswithdigit", Arguments: "{}"},
		{ID: "c5", Name: "valid_tool", Arguments: "{}"},
	}

	valid, errs := sanitizeToolCalls(calls, logger)
	if len(valid) != 1 {
		t.Errorf("expected 1 valid call, got %d", len(valid))
	}
	if valid[0].Name != "valid_tool" {
		t.Errorf("valid[0].Name = %q", valid[0].Name)
	}
	if len(errs) != 4 {
		t.Errorf("expected 4 error messages, got %d", len(errs))
	}

	for _, msg := range errs {
		if msg.Role != "tool" {
			t.Errorf("error message role = %q, want tool", msg.Role)
		}
		if !strings.Contains(msg.Content, "Invalid tool name") {
			t.Errorf("error content = %q", msg.Content)
		}
	}
}

func TestSanitizeToolCallsDollarPrefixed(t *testing.T) {
	logger := slog.Default()
	calls := []llm.ToolCall{
		{ID: "c1", Name: "$search_web", Arguments: "{}"},
	}

	valid, errs := sanitizeToolCalls(calls, logger)
	if len(valid) != 0 {
		t.Error("expected $-prefixed name to be rejected")
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}
	if errs[0].ToolCallID != "c1" {
		t.Errorf("error tool call ID = %q", errs[0].ToolCallID)
	}
}

func TestFilterNotifyArtifactCallsKeepsSystemCalls(t *testing.T) {
	logger := slog.Default()
	calls := []llm.ToolCall{
		{ID: "host-notify-abc", Name: "_notify_artifact_save", Arguments: "{}"},
		{ID: "c2", Name: "search", Arguments: "{}"},
	}

	filtered, errs := filterNotifyArtifactCalls(calls, logger)
	if len(filtered) != 2 {
		t.Errorf("expected 2 calls, got %d", len(filtered))
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 corrective errors for system-injected call, got %d", len(errs))
	}
}

func TestFilterNotifyArtifactCallsDropsLLMCalls(t *testing.T) {
	logger := slog.Default()
	calls := []llm.ToolCall{
		{ID: "llm-generated-id", Name: "_notify_artifact_save", Arguments: "{}"},
		{ID: "c2", Name: "search", Arguments: "{}"},
	}

	filtered, errs := filterNotifyArtifactCalls(calls, logger)
	if len(filtered) != 1 {
		t.Errorf("expected 1 call after filtering, got %d", len(filtered))
	}
	if filtered[0].Name != "search" {
		t.Errorf("remaining call = %q", filtered[0].Name)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 corrective tool result, got %d", len(errs))
	}
	if errs[0].Role != "tool" || errs[0].ToolCallID != "llm-generated-id" {
		t.Errorf("corrective message = %+v, want role=tool toolCallID=llm-generated-id", errs[0])
	}
}

func TestFilterNotifyArtifactCallsEmptyInput(t *testing.T) {
	logger := slog.Default()
	filtered, errs := filterNotifyArtifactCalls(nil, logger)
	if len(filtered) != 0 {
		t.Errorf("expected 0 calls, got %d", len(filtered))
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d", len(errs))
	}
}

func TestFilterNotifyArtifactCallsOtherToolsUnaffected(t *testing.T) {
	logger := slog.Default()
	calls := []llm.ToolCall{
		{ID: "c1", Name: "search", Arguments: "{}"},
		{ID: "c2", Name: "get_weather", Arguments: "{}"},
	}

	filtered, errs := filterNotifyArtifactCalls(calls, logger)
	if len(filtered) != 2 {
		t.Errorf("expected 2 calls unchanged, got %d", len(filtered))
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 corrective errors when no notify calls present, got %d", len(errs))
	}
}

func TestIsFilenameSafe(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"valid simple", "data.csv", true},
		{"valid with underscore", "my_file.txt", true},
		{"valid with spaces", "my file.txt", true},
		{"empty", "", false},
		{"whitespace only", "   ", false},
		{"dot only", ".", false},
		{"path traversal", "../etc/passwd", false},
		{"double dot in name", "file..bak", false},
		{"forward slash", "dir/file.txt", false},
		{"backslash", "dir\\file.txt", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isFilenameSafe(tc.filename)
			if got != tc.want {
				t.Errorf("isFilenameSafe(%q) = %v, want %v", tc.filename, got, tc.want)
			}
		})
	}
}

func TestIsTextMIME(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{"text/plain", true},
		{"text/csv", true},
		{"text/html", true},
		{"application/json", true},
		{"application/yaml", true},
		{"application/xml", true},
		{"application/javascript", true},
		{"application/typescript", true},
		{"application/pdf", false},
		{"image/png", false},
		{"application/octet-stream", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.mimeType, func(t *testing.T) {
			got := isTextMIME(tc.mimeType)
			if got != tc.want {
				t.Errorf("isTextMIME(%q) = %v, want %v", tc.mimeType, got, tc.want)
			}
		})
	}
}

func TestStrPtr(t *testing.T) {
	if strPtr("") != nil {
		t.Error("strPtr(\"\") should return nil")
	}
	p := strPtr("hello")
	if p == nil || *p != "hello" {
		t.Error("strPtr(\"hello\") should return pointer to \"hello\"")
	}
}

func TestExtractGatewayContextFromMetadata(t *testing.T) {
	msg := a2a.Message{
		Metadata: map[string]any{
			"system_purpose":  "Help with coding",
			"response_format": "markdown",
			"project_id":      "proj-123",
		},
	}
	gc := gatewayContextFrom(gatewayMetadataOf(msg), a2a.UserProperties{}, false)
	if gc.SystemPurpose != "Help with coding" {
		t.Errorf("SystemPurpose = %q", gc.SystemPurpose)
	}
	if gc.ResponseFormat != "markdown" {
		t.Errorf("ResponseFormat = %q", gc.ResponseFormat)
	}
	if gc.ProjectID != "proj-123" {
		t.Errorf("ProjectID = %q", gc.ProjectID)
	}
}

func TestExtractGatewayContextConversationContext(t *testing.T) {
	msg := a2a.Message{Metadata: map[string]any{"conversation_context": "a slack thread transcript"}}
	gc := gatewayContextFrom(gatewayMetadataOf(msg), a2a.UserProperties{}, false)
	if gc.ConversationContext != "a slack thread transcript" {
		t.Errorf("ConversationContext = %q", gc.ConversationContext)
	}
}

func TestExtractGatewayContextNoMetadata(t *testing.T) {
	msg := a2a.Message{}
	gc := gatewayContextFrom(gatewayMetadataOf(msg), a2a.UserProperties{}, false)
	if gc.SystemPurpose != "" || gc.ResponseFormat != "" || gc.ProjectID != "" {
		t.Errorf("expected empty context, got %+v", gc)
	}
}

func TestExtractGatewayContextWebUIRedirect(t *testing.T) {
	msg := a2a.Message{}

	t.Run("capable", func(t *testing.T) {
		gc := gatewayContextFrom(gatewayMetadataOf(msg), a2a.UserProperties{
			GatewayCapabilities: map[string]bool{"web_ui_redirect": true},
		}, false)
		if !gc.WebUIRedirect {
			t.Error("WebUIRedirect = false, want true when gateway advertises web_ui_redirect")
		}
	})

	t.Run("not capable", func(t *testing.T) {
		gc := gatewayContextFrom(gatewayMetadataOf(msg), a2a.UserProperties{
			GatewayCapabilities: map[string]bool{},
		}, false)
		if gc.WebUIRedirect {
			t.Error("WebUIRedirect = true, want false for non-web gateway")
		}
	})

	t.Run("nil capabilities", func(t *testing.T) {
		gc := gatewayContextFrom(gatewayMetadataOf(msg), a2a.UserProperties{}, false)
		if gc.WebUIRedirect {
			t.Error("WebUIRedirect = true, want false when capabilities are absent")
		}
	})
}

func TestExtractGatewayContextUserProfileString(t *testing.T) {
	msg := a2a.Message{}
	userProps := a2a.UserProperties{
		A2AUserConfig: `{"user_profile": "Senior developer"}`,
	}
	gc := gatewayContextFrom(gatewayMetadataOf(msg), userProps, false)
	if gc.UserProfile != "Senior developer" {
		t.Errorf("UserProfile = %q", gc.UserProfile)
	}
}

func TestExtractGatewayContextUserProfileMap(t *testing.T) {
	msg := a2a.Message{}
	userProps := a2a.UserProperties{
		A2AUserConfig: `{"user_profile": {"name": "Alice", "role": "admin"}}`,
	}
	gc := gatewayContextFrom(gatewayMetadataOf(msg), userProps, false)
	if gc.UserProfile == "" {
		t.Fatal("expected non-empty user profile")
	}
	if !strings.Contains(gc.UserProfile, "Alice") {
		t.Errorf("UserProfile = %q, expected to contain Alice", gc.UserProfile)
	}
}

func TestExtractGatewayContextInvalidJSON(t *testing.T) {
	msg := a2a.Message{}
	userProps := a2a.UserProperties{
		A2AUserConfig: "not-json",
	}
	gc := gatewayContextFrom(gatewayMetadataOf(msg), userProps, false)
	if gc.UserProfile != "" {
		t.Errorf("expected empty user profile for invalid JSON, got %q", gc.UserProfile)
	}
}

func TestBuildLLMToolsAlwaysIncludesNotifyArtifact(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	tools := agent.buildLLMTools(context.Background(), nil, nil, "")
	var hasNotify bool
	for _, td := range tools {
		if td.Name == "_notify_artifact_save" {
			hasNotify = true
		}
	}
	if !hasNotify {
		t.Error("expected _notify_artifact_save in tool definitions")
	}
}

func TestBuildLLMToolsFiltersIndexSearchWhenNoProject(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	err := agent.Tools().Register(&testBuiltinTool{
		name:        "index_search",
		description: "Search project docs",
	})
	if err != nil {
		t.Fatal(err)
	}

	tools := agent.buildLLMTools(context.Background(), nil, nil, "")
	for _, td := range tools {
		if td.Name == "index_search" {
			t.Error("index_search should be filtered when projectID is empty")
		}
	}

	tools = agent.buildLLMTools(context.Background(), nil, nil, "proj-1")
	var found bool
	for _, td := range tools {
		if td.Name == "index_search" {
			found = true
		}
	}
	if !found {
		t.Error("index_search should be included when projectID is set")
	}
}

func TestBuildLLMToolsIncludesRegisteredTools(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})

	err := agent.Tools().Register(&testBuiltinTool{
		name:        "my_tool",
		description: "A custom tool",
	})
	if err != nil {
		t.Fatal(err)
	}

	tools := agent.buildLLMTools(context.Background(), nil, nil, "")
	var found bool
	for _, td := range tools {
		if td.Name == "my_tool" {
			found = true
			if td.Description != "A custom tool" {
				t.Errorf("description = %q", td.Description)
			}
		}
	}
	if !found {
		t.Error("expected my_tool in tool definitions")
	}
}

func TestFormatToolResultNilResult(t *testing.T) {
	agent, _, _ := newTestAgent()
	result, _ := agent.formatToolResult(context.Background(), nil, nil, nil)
	if result != "no result" {
		t.Errorf("expected 'no result', got %q", result)
	}
}

func TestFormatToolResultValidationError(t *testing.T) {
	agent, _, _ := newTestAgent()
	r := &tool.Result{ValidationError: "missing required parameter"}
	result, _ := agent.formatToolResult(context.Background(), r, nil, nil)
	if !strings.Contains(result, "missing required parameter") {
		t.Errorf("result = %q", result)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Errorf("result is not valid JSON: %v", err)
	}
	if _, ok := parsed["error"]; !ok {
		t.Error("expected 'error' key in JSON")
	}
}

func TestFormatToolResultSuccessWithData(t *testing.T) {
	agent, _, _ := newTestAgent()
	r := &tool.Result{
		Status:  "success",
		Message: "done",
		Data:    map[string]any{"count": 42},
	}
	result, _ := agent.formatToolResult(context.Background(), r, nil, nil)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if parsed["status"] != "success" {
		t.Errorf("status = %v", parsed["status"])
	}
	if parsed["message"] != "done" {
		t.Errorf("message = %v", parsed["message"])
	}
	if parsed["count"] != float64(42) {
		t.Errorf("count = %v", parsed["count"])
	}
}

func TestFormatToolResultEmptyMessage(t *testing.T) {
	agent, _, _ := newTestAgent()
	r := &tool.Result{
		Status: "success",
	}
	result, _ := agent.formatToolResult(context.Background(), r, nil, nil)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := parsed["message"]; ok {
		t.Error("empty message should not be in JSON output")
	}
}

func TestInjectBlockValidationFeedback(t *testing.T) {
	agent, _, _ := newTestAgent()
	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())
	bve := &blockValidationError{
		VisibleText: "some visible text",
		RawText:     "raw text with block markers",
		ErrorMsg:    "missing filename parameter",
		Filename:    "test.csv",
		BlockType:   FencedBlockArtifact,
	}

	var messages []llm.ChatMessage
	agent.injectBlockValidationFeedback(context.Background(), sp, bve, &messages)

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	if messages[0].Role != "assistant" {
		t.Errorf("messages[0].Role = %q", messages[0].Role)
	}
	if messages[0].Content != "raw text with block markers" {
		t.Errorf("messages[0].Content = %q", messages[0].Content)
	}
	if len(messages[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(messages[0].ToolCalls))
	}
	if messages[0].ToolCalls[0].Name != "_notify_artifact_save" {
		t.Errorf("tool call name = %q", messages[0].ToolCalls[0].Name)
	}

	if messages[1].Role != "tool" {
		t.Errorf("messages[1].Role = %q", messages[1].Role)
	}
	if !strings.Contains(messages[1].Content, `"status":"error"`) {
		t.Errorf("expected error status in result, got %q", messages[1].Content)
	}
	if !strings.Contains(messages[1].Content, `"version":0`) {
		t.Errorf("expected version 0 in result, got %q", messages[1].Content)
	}
}

func TestInjectBlockValidationFeedbackFallsBackToVisibleText(t *testing.T) {
	agent, _, _ := newTestAgent()
	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())
	bve := &blockValidationError{
		VisibleText: "visible only",
		RawText:     "",
		ErrorMsg:    "error message",
	}

	var messages []llm.ChatMessage
	agent.injectBlockValidationFeedback(context.Background(), sp, bve, &messages)

	if messages[0].Content != "visible only" {
		t.Errorf("expected fallback to VisibleText, got %q", messages[0].Content)
	}
}

func TestProcessFilePartsNoArtifactService(t *testing.T) {
	agent, _, _ := newTestAgent()
	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://agent/user/sess/file.csv"}},
		},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "hello", -1)
	if result != "hello" {
		t.Errorf("expected unchanged text, got %q", result)
	}
}

func TestProcessFilePartsNonArtifactURI(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "https://example.com/file.csv"}},
		},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "hello", -1)
	if result != "hello" {
		t.Errorf("expected unchanged text for non-artifact URI, got %q", result)
	}
}

func TestProcessFilePartsInvalidArtifactURI(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://short"}},
		},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "hello", -1)
	if !strings.Contains(result, "Invalid artifact URI") {
		t.Errorf("expected error about invalid URI, got %q", result)
	}
}

func TestProcessFilePartsSkipsNonFileParts(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}

	msg := a2a.Message{
		Parts: []a2a.Part{
			a2a.NewTextPart("some text"),
			{Kind: "data", Data: map[string]any{"key": "value"}},
		},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "hello", -1)
	if result != "hello" {
		t.Errorf("expected unchanged text, got %q", result)
	}
}

func TestBuildMessagesWithHistory(t *testing.T) {
	agent, _, _ := newTestAgent()
	falseVal := false
	agent.cfg.InjectCurrentTime = &falseVal
	sess := &session.Session{
		Messages: []session.Message{
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "first answer"},
		},
	}

	msgs, offset := agent.buildMessages(context.Background(), nil, sess, "second question", gatewayContext{})
	if len(msgs) < 4 {
		t.Fatalf("expected at least 4 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msgs[0].Role = %q", msgs[0].Role)
	}
	if offset < 3 {
		t.Errorf("offset = %d, expected at least 3", offset)
	}
	if msgs[len(msgs)-1].Role != "user" || msgs[len(msgs)-1].Content != "second question" {
		t.Errorf("last message = %+v", msgs[len(msgs)-1])
	}
}

func TestBuildMessagesEmptyHistory(t *testing.T) {
	agent, _, _ := newTestAgent()
	falseVal := false
	agent.cfg.InjectCurrentTime = &falseVal
	sess := &session.Session{}

	msgs, _ := agent.buildMessages(context.Background(), nil, sess, "hello", gatewayContext{})
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("msgs[0].Role = %q", msgs[0].Role)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hello" {
		t.Errorf("msgs[1] = %+v", msgs[1])
	}
}

func TestBuildMessagesPadsEmptyUserContent(t *testing.T) {
	agent, _, _ := newTestAgent()
	sess := &session.Session{
		Messages: []session.Message{
			{Role: "user", Content: ""},
			{Role: "assistant", Content: "ok"},
			{Role: "user", Content: "  "},
			{Role: "assistant", Content: "", ToolCalls: []session.ToolCall{{ID: "t1", Name: "x", Arguments: "{}"}}},
		},
	}

	imgBlock := llm.ContentBlock{Type: "image_url", ImageURL: "data:image/png;base64,AAAA"}
	msgs, _ := agent.buildMessages(context.Background(), nil, sess, "", gatewayContext{}, imgBlock)

	for i, m := range msgs {
		if m.Role == "user" && len(m.ContentBlocks) == 0 && m.Content == "" {
			t.Errorf("msgs[%d] is an empty-content user message (Anthropic rejects); want padded: %+v", i, m)
		}
	}

	sawEmptyAssistant, sawBlockUser := false, false
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			sawEmptyAssistant = true
			if m.Content != "" {
				t.Errorf("empty assistant-with-tool-calls should not be padded; got %q", m.Content)
			}
		}
		if m.Role == "user" && len(m.ContentBlocks) > 0 {
			sawBlockUser = true
		}
	}
	if !sawEmptyAssistant {
		t.Error("expected the empty assistant-with-tool-calls message in the result")
	}
	if !sawBlockUser {
		t.Error("expected the image-block user turn in the result")
	}
}

func TestBuildMessagesStampsCurrentTime(t *testing.T) {
	agent, _, _ := newTestAgent()
	sess := &session.Session{}

	msgs, offset := agent.buildMessages(context.Background(), nil, sess, "hello", gatewayContext{})
	got := msgs[len(msgs)-1].Content
	if !strings.HasPrefix(got, "[Current time: ") || !strings.HasSuffix(got, "]\nhello") {
		t.Errorf("user turn should carry the time stamp, got %q", got)
	}
	if offset >= len(msgs) {
		t.Errorf("stamped user turn must be at/after historyOffset so it persists to session history; offset=%d len=%d", offset, len(msgs))
	}

	sess = &session.Session{
		Messages: []session.Message{
			{Role: "user", Content: "[Current time: 2026-07-16T10:00:00Z]\nfirst"},
			{Role: "assistant", Content: "ok"},
		},
	}
	msgs, _ = agent.buildMessages(context.Background(), nil, sess, "second", gatewayContext{})
	var history string
	for _, m := range msgs {
		if m.Role == "user" && strings.HasSuffix(m.Content, "first") {
			history = m.Content
		}
	}
	if history != "[Current time: 2026-07-16T10:00:00Z]\nfirst" {
		t.Errorf("history turn must keep its original stamp, got %q", history)
	}

	msgs, _ = agent.buildMessages(context.Background(), nil, &session.Session{}, "", gatewayContext{})
	if got := msgs[len(msgs)-1].Content; strings.Contains(got, "Current time") {
		t.Errorf("empty turn should not be stamped, got %q", got)
	}
}

func TestBuildMessagesStampsIdempotently(t *testing.T) {
	agent, _, _ := newTestAgent()

	msgs, _ := agent.buildMessages(context.Background(), nil, &session.Session{}, "hello", gatewayContext{})
	stamped := msgs[len(msgs)-1].Content

	msgs, _ = agent.buildMessages(context.Background(), nil, &session.Session{}, stamped, gatewayContext{})
	got := msgs[len(msgs)-1].Content
	if n := strings.Count(got, "[Current time: "); n != 1 {
		t.Errorf("restamping an already-stamped turn must yield exactly one stamp, got %d: %q", n, got)
	}
	if !strings.HasSuffix(got, "]\nhello") {
		t.Errorf("original text must survive restamping, got %q", got)
	}

	lookalike := "[Current time: to leave]\nplease remind me"
	msgs, _ = agent.buildMessages(context.Background(), nil, &session.Session{}, lookalike, gatewayContext{})
	if got := msgs[len(msgs)-1].Content; !strings.HasSuffix(got, "]\n"+lookalike) {
		t.Errorf("user lookalike text must survive stamping, got %q", got)
	}
}

func TestBlockValidationErrorImplementsError(t *testing.T) {
	bve := &blockValidationError{ErrorMsg: "test error"}
	var err error = bve
	if err.Error() != "test error" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestStreamPipelineReasoningFollowedByText(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ReasoningDelta: "Thinking..."},
		llm.StreamChunk{ContentDelta: "The answer."},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var hasThinking, hasText bool
	for _, ev := range events {
		if ev.Type == EventThinkingContent {
			hasThinking = true
			if ev.Text != "Thinking..." {
				t.Errorf("thinking text = %q", ev.Text)
			}
		}
		if ev.Type == EventText {
			hasText = true
		}
	}
	if !hasThinking {
		t.Error("missing thinking event")
	}
	if !hasText {
		t.Error("missing text event")
	}
}

func TestStreamPipelineLargeBatchSize(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "Hello "},
		llm.StreamChunk{ContentDelta: "World"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1000))
	text := combineText(events)
	if !strings.Contains(text, "Hello") || !strings.Contains(text, "World") {
		t.Errorf("text = %q", text)
	}
}

func TestValidToolNameRegexPatterns(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"search", true},
		{"get_data", true},
		{"_private", true},
		{"tool-name", true},
		{"Tool123", true},
		{"", false},
		{"123abc", false},
		{"$func", false},
		{"has space", false},
		{"has.dot", false},
		{"has/slash", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validToolName.MatchString(tc.name)
			if got != tc.valid {
				t.Errorf("validToolName.MatchString(%q) = %v, want %v", tc.name, got, tc.valid)
			}
		})
	}
}

func TestEmitToolCallsSortedEmpty(t *testing.T) {
	toolCalls := map[int]*llm.ToolCall{}
	var emitted []string
	emit := func(c accumulatedChunk) bool {
		if c.toolCall != nil {
			emitted = append(emitted, c.toolCall.Name)
		}
		return true
	}
	ok := emitToolCallsSorted(toolCalls, emit)
	if !ok {
		t.Error("expected true for empty map")
	}
	if len(emitted) != 0 {
		t.Errorf("expected 0 emissions, got %d", len(emitted))
	}
}

func TestEmitToolCallsSortedCancellation(t *testing.T) {
	toolCalls := map[int]*llm.ToolCall{
		0: {Name: "a"},
		1: {Name: "b"},
		2: {Name: "c"},
	}
	var emitted []string
	emit := func(c accumulatedChunk) bool {
		if c.toolCall != nil {
			emitted = append(emitted, c.toolCall.Name)
			if c.toolCall.Name == "b" {
				return false
			}
		}
		return true
	}
	ok := emitToolCallsSorted(toolCalls, emit)
	if ok {
		t.Error("expected false due to cancellation")
	}
	if len(emitted) != 2 {
		t.Errorf("expected 2 emissions before cancel, got %d: %v", len(emitted), emitted)
	}
}

func TestExtractA2AMessageValid(t *testing.T) {
	params := map[string]any{
		"message": map[string]any{
			"kind":       "message",
			"message_id": "msg-1",
			"role":       "user",
			"parts":      []any{map[string]any{"kind": "text", "text": "hello"}},
		},
	}

	msg, err := extractA2AMessage(params)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Kind != "message" {
		t.Errorf("Kind = %q", msg.Kind)
	}
	if msg.Role != "user" {
		t.Errorf("Role = %q", msg.Role)
	}
}

func TestExtractA2AMessageNotMap(t *testing.T) {
	_, err := extractA2AMessage("not a map")
	if err == nil {
		t.Fatal("expected error for non-map params")
	}
	if !strings.Contains(err.Error(), "expected map[string]any") {
		t.Errorf("err = %v", err)
	}
}

func TestExtractA2AMessageNoMessageKey(t *testing.T) {
	params := map[string]any{"other": "value"}
	_, err := extractA2AMessage(params)
	if err == nil {
		t.Fatal("expected error for missing message key")
	}
	if !strings.Contains(err.Error(), "no \"message\" key") {
		t.Errorf("err = %v", err)
	}
}

func TestIsResourceTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"list_skill_resources", true},
		{"read_skill_resource", true},
		{"grep_skill_resources", true},
		{"search", false},
		{"", false},
		{"list_skills", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isResourceTool(tc.name); got != tc.want {
				t.Errorf("isResourceTool(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestReportModelOriginal(t *testing.T) {
	agent := New(AgentConfig{Model: "gpt-4", OriginalModel: "openai/gpt-4"})
	if got := agent.reportModel(); got != "openai/gpt-4" {
		t.Errorf("reportModel() = %q", got)
	}
}

func TestReportModelFallback(t *testing.T) {
	agent := New(AgentConfig{Model: "gpt-4"})
	if got := agent.reportModel(); got != "gpt-4" {
		t.Errorf("reportModel() = %q", got)
	}
}

func TestDataObjectInlineTextEmpty(t *testing.T) {
	agent, _, _ := newTestAgent()
	do := tool.DataObject{Content: nil}
	result := agent.dataObjectInlineText(do, 100)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestDataObjectInlineTextShort(t *testing.T) {
	agent, _, _ := newTestAgent()
	do := tool.DataObject{Content: []byte("hello world")}
	result := agent.dataObjectInlineText(do, 100)
	if result != "hello world" {
		t.Errorf("result = %q", result)
	}
}

func TestDataObjectInlineTextTruncation(t *testing.T) {
	agent, _, _ := newTestAgent()
	content := strings.Repeat("x", 200)
	do := tool.DataObject{Content: []byte(content)}
	result := agent.dataObjectInlineText(do, 50)
	if len(result) > 100 {
		t.Errorf("expected truncation, got len=%d", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("expected truncation marker, got %q", result)
	}
}

func TestBuildCancelledResponse(t *testing.T) {
	resp := buildCancelledResponse("task-1", "ctx-1", "agent-1", taskUsageSnapshot{
		usage:           llm.Usage{PromptTokens: 90, CompletionTokens: 12, CachedTokens: 5, ReasoningTokens: 7, TotalTokens: 102},
		lastInputTokens: 55,
	}, nil)
	if resp == nil {
		t.Fatal("expected non-nil response")
	} else if resp.ID != "task-1" {
		t.Errorf("ID = %v", resp.ID)
	}
	task, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("Result type = %T", resp.Result)
	}
	if task.Status.State != a2a.TaskStateCancelled {
		t.Errorf("State = %q", task.Status.State)
	}
	if task.ContextID != "ctx-1" {
		t.Errorf("ContextID = %q", task.ContextID)
	}
	tu, ok := task.Metadata["token_usage"].(map[string]any)
	if !ok {
		t.Fatalf("cancelled response dropped token_usage: %#v", task.Metadata)
	}
	if tu["total_input_tokens"] != 90 || tu["total_reasoning_tokens"] != 7 {
		t.Errorf("token_usage = %#v, want input 90 / reasoning 7", tu)
	}
	if tu["last_input_tokens"] != 55 {
		t.Errorf("last_input_tokens = %v, want 55 (not the 90 cumulative sum)", tu["last_input_tokens"])
	}
}

func TestBuildCancelledResponse_NoUsageOmitsBlock(t *testing.T) {
	resp := buildCancelledResponse("task-2", "ctx-2", "agent-1", taskUsageSnapshot{}, nil)
	task := resp.Result.(a2a.Task)
	if _, present := task.Metadata["token_usage"]; present {
		t.Error("token_usage must be absent when no usage was recorded")
	}
}

func TestBuildToolResultMessageBasic(t *testing.T) {
	agent, _, _ := newTestAgent()
	result := &tool.Result{Status: "success", Message: "42"}
	tc := llm.ToolCall{ID: "call-1", Name: "add"}

	msg := agent.buildToolResultMessage(context.Background(), result, nil, nil, tc)
	if msg.Role != "tool" {
		t.Errorf("Role = %q", msg.Role)
	}
	if msg.ToolCallID != "call-1" {
		t.Errorf("ToolCallID = %q", msg.ToolCallID)
	}
	if msg.ToolName != "add" {
		t.Errorf("ToolName = %q", msg.ToolName)
	}
	if !strings.Contains(msg.Content, "success") {
		t.Errorf("Content = %q", msg.Content)
	}
}

func TestBuildToolResultMessageWithImageBlocks(t *testing.T) {
	agent, _, _ := newTestAgent()
	result := &tool.Result{
		Status:  "success",
		Message: "image result",
		ImageBlocks: []tool.ImageBlock{
			{Data: []byte("fake-png-data"), MIMEType: "image/png"},
		},
	}
	tc := llm.ToolCall{ID: "call-img", Name: "screenshot"}

	msg := agent.buildToolResultMessage(context.Background(), result, nil, nil, tc)
	if len(msg.ContentBlocks) == 0 {
		t.Fatal("expected ContentBlocks for image result")
	}
	hasImage := false
	for _, b := range msg.ContentBlocks {
		if b.Type == "image_url" {
			hasImage = true
			if !strings.HasPrefix(b.ImageURL, "data:image/png;base64,") {
				t.Errorf("ImageURL = %q", b.ImageURL)
			}
		}
	}
	if !hasImage {
		t.Error("expected image_url content block")
	}
}

func TestFormatToolResultWithDataObjects(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}

	toolCtx := &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: memStore},
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}

	r := &tool.Result{
		Status:  "success",
		Message: "query results",
		DataObjects: []tool.DataObject{
			{
				Name:        "results.csv",
				Content:     []byte("a,b,c\n1,2,3"),
				MIMEType:    "text/csv",
				Disposition: tool.DispositionInline,
			},
		},
	}

	result, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v (result: %q)", err, result)
	}
	if parsed["status"] != "success" {
		t.Errorf("status = %v", parsed["status"])
	}
}

func TestFormatToolResultWithArtifactDataObject(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}

	toolCtx := &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: memStore},
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}

	bigContent := strings.Repeat("data\n", 1000)
	r := &tool.Result{
		Status:  "success",
		Message: "big data",
		DataObjects: []tool.DataObject{
			{
				Name:        "big.csv",
				Content:     []byte(bigContent),
				MIMEType:    "text/csv",
				Disposition: tool.DispositionArtifact,
			},
		},
	}

	result, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed["status"] != "success" {
		t.Errorf("status = %v", parsed["status"])
	}
	artifacts, ok := parsed["artifacts_created"].([]any)
	if !ok || len(artifacts) == 0 {
		t.Errorf("expected artifacts_created, got %v", parsed["artifacts_created"])
	}
}

func TestFormatToolResultWithAutoDisposition(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}

	toolCtx := &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: memStore},
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}

	r := &tool.Result{
		Status:  "success",
		Message: "small data",
		DataObjects: []tool.DataObject{
			{
				Name:        "small.txt",
				Content:     []byte("hello"),
				MIMEType:    "text/plain",
				Disposition: tool.DispositionAuto,
			},
		},
	}

	result, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed["content"] != "hello" {
		t.Errorf("small auto text should be inlined in full; content = %v", parsed["content"])
	}
	if _, hasArtifacts := parsed["artifacts_created"]; !hasArtifacts {
		t.Error("small auto text should also be saved as a durable artifact")
	}
}

func TestFormatToolResultBinaryAutoSavesAsArtifact(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}

	toolCtx := &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: memStore},
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}

	r := &tool.Result{
		Status:  "success",
		Message: "image",
		DataObjects: []tool.DataObject{
			{
				Name:        "photo.png",
				Content:     []byte("fake-png"),
				MIMEType:    "image/png",
				Disposition: tool.DispositionAuto,
			},
		},
	}

	result, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, hasArtifacts := parsed["artifacts_created"]; !hasArtifacts {
		t.Error("binary data should create artifact with auto disposition")
	}
}

func TestFormatToolResultAutoWithArtifactRefEmitsArtifactsCreated(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}

	toolCtx := &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: memStore},
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}

	r := &tool.Result{
		Status:  "success",
		Message: "Org chart retrieved.",
		Data:    map[string]any{"markdown": "- Alice\n  - Bob\n"},
		DataObjects: []tool.DataObject{{
			Name:        "org_chart.json",
			Content:     nil,
			MIMEType:    "application/json",
			Disposition: tool.DispositionAuto,
			ArtifactRef: &tool.ArtifactRef{
				Filename:  "org_chart.json",
				Version:   0,
				SizeBytes: 5468,
			},
		}},
	}

	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}

	arts, ok := parsed["artifacts_created"].([]any)
	if !ok || len(arts) != 1 {
		t.Fatalf("artifacts_created missing or wrong length: %v", parsed)
	}
	entry := arts[0].(map[string]any)
	if entry["filename"] != "org_chart.json" {
		t.Errorf("filename = %v, want org_chart.json", entry["filename"])
	}
	if _, hasContent := parsed["content"]; hasContent {
		t.Errorf("unexpected inline content field: %v", parsed["content"])
	}
	if _, hasInline := parsed["inline_outputs"]; hasInline {
		t.Errorf("unexpected inline_outputs field: %v", parsed["inline_outputs"])
	}
}

func TestSaveDataObjectAsArtifactWithArtifactRef(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore}

	do := tool.DataObject{
		Name:     "test.csv",
		MIMEType: "text/csv",
		ArtifactRef: &tool.ArtifactRef{
			Filename:  "test.csv",
			Version:   3,
			SizeBytes: 100,
		},
	}
	toolCtx := &tool.Context{
		ArtifactService: scopedSvc,
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}

	ref, info, _ := agent.saveDataObjectAsArtifact(context.Background(), do, toolCtx, nil)
	if info == nil {
		t.Fatal("expected non-nil info")
	} else if info.Filename != "test.csv" {
		t.Errorf("Filename = %q", info.Filename)
	}
	if info.Version != 3 {
		t.Errorf("Version = %d", info.Version)
	}
	if !strings.Contains(ref, "test.csv") {
		t.Errorf("ref = %q", ref)
	}
}

func TestSaveDataObjectAsArtifactWithArtifactRefPublishesSignals(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore}

	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mb.Disconnect(t.Context()) }()

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-1"
	sp := NewStatusPublisher(mb, StatusPublisherConfig{StatusTopic: statusTopic, TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	q, err := mb.CreateQueue("test-signals", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := mb.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}

	do := tool.DataObject{
		Name:        "presentation.pptx",
		MIMEType:    "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		Description: "PowerPoint presentation",
		ArtifactRef: &tool.ArtifactRef{
			Filename:  "presentation.pptx",
			Version:   0,
			SizeBytes: 124218,
		},
	}
	toolCtx := &tool.Context{
		ArtifactService: scopedSvc,
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}

	ref, info, _ := agent.saveDataObjectAsArtifact(t.Context(), do, toolCtx, sp)
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if !strings.Contains(ref, "presentation.pptx") {
		t.Errorf("ref = %q", ref)
	}

	var signalTypes []string
	for range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		msg, err := mb.Receive(ctx, q)
		cancel()
		if err != nil {
			t.Fatalf("expected signal message, got error: %v", err)
		}
		var resp a2a.Response
		if err := json.Unmarshal(msg.Payload, &resp); err != nil {
			continue
		}
		resultBytes, _ := json.Marshal(resp.Result)
		var event a2a.TaskStatusUpdateEvent
		if err := json.Unmarshal(resultBytes, &event); err != nil {
			continue
		}
		for _, p := range event.Status.Message.Parts {
			if p.Kind == "data" {
				if dm, ok := p.Data.(map[string]any); ok {
					if st, ok := dm["type"].(string); ok {
						signalTypes = append(signalTypes, st)
					}
				}
			}
		}
	}

	hasProgress := false
	hasSaved := false
	for _, st := range signalTypes {
		if st == "artifact_creation_progress" {
			hasProgress = true
		}
		if st == "artifact_saved" {
			hasSaved = true
		}
	}
	if !hasProgress {
		t.Error("expected artifact_creation_progress signal")
	}
	if !hasSaved {
		t.Error("expected artifact_saved signal")
	}
}

func TestSaveDataObjectAsArtifactSuppressesSignalsWhenHookWired(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore}

	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mb.Disconnect(t.Context()) }()

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-1"
	sp := NewStatusPublisher(mb, StatusPublisherConfig{StatusTopic: statusTopic, TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	q, err := mb.CreateQueue("test-suppress", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := mb.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}

	do := tool.DataObject{
		Name:        "presentation.pptx",
		MIMEType:    "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		Description: "PowerPoint presentation",
		ArtifactRef: &tool.ArtifactRef{Filename: "presentation.pptx", Version: 0, SizeBytes: 124218},
	}
	toolCtx := &tool.Context{
		ArtifactService:     scopedSvc,
		AgentName:           "test-agent",
		UserID:              "user-1",
		SessionID:           "sess-1",
		NotifyArtifactSaved: func(context.Context, tool.ArtifactSavedInfo) {},
	}

	if _, info, _ := agent.saveDataObjectAsArtifact(t.Context(), do, toolCtx, sp); info == nil {
		t.Fatal("expected non-nil info (ref path still returns the saved-artifact ref)")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if msg, err := mb.Receive(ctx, q); err == nil {
		t.Fatalf("expected no signal when the hook is wired, got: %s", msg.Payload)
	}
}

func TestProcessFilePartsWithValidArtifact(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore}
	agent.svc.(*mockServices).arts = scopedSvc

	loc := scopedSvc.Resolve("test-agent", "user-1", "sess-1", "data.csv")
	_, err := memStore.Save(context.Background(), loc, []byte("a,b,c\n1,2,3"), "text/csv")
	if err != nil {
		t.Fatal(err)
	}

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://user-1/sess-1/data.csv"}},
		},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "hello", -1)
	if !strings.Contains(result, "artifact") || !strings.Contains(result, "data.csv") {
		t.Errorf("expected artifact metadata, got %q", result)
	}
}

var tinyPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0D, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func saveArtifact(t *testing.T, scoped *artifact.ScopedService, agentName, userID, sessID, filename string, content []byte, mimeType string) {
	t.Helper()
	ctx := context.Background()
	loc := scoped.Resolve(agentName, userID, sessID, filename)
	ver, err := scoped.Inner.Save(ctx, loc, content, mimeType)
	if err != nil {
		t.Fatalf("save %s: %v", filename, err)
	}
	if err := scoped.Inner.SaveMetadata(ctx, loc, ver, &artifact.VersionMetadata{
		MIMEType:  mimeType,
		SizeBytes: int64(len(content)),
	}); err != nil {
		t.Fatalf("save metadata %s: %v", filename, err)
	}
}

func TestProcessFilePartsInlineVisionDisabledByDefault(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "photo.png", tinyPNG, "image/png")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/photo.png"}}},
	}
	result, imgs, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "describe this", -1)
	if len(imgs) != 0 {
		t.Fatalf("expected no image blocks when inline vision is disabled, got %d", len(imgs))
	}
	if !strings.Contains(result, "photo.png") {
		t.Errorf("expected metadata summary to mention filename, got %q", result)
	}
}

func TestProcessFilePartsInlineVisionEnabledEmitsImageBlock(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlineVision = true
	agent.cfg.MaxInlineVisionImages = 5
	agent.cfg.MaxInlineVisionBytes = 20 * 1024 * 1024
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "photo.png", tinyPNG, "image/png")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/photo.png"}}},
	}
	result, imgs, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "describe this", -1)
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image block, got %d", len(imgs))
	}
	if imgs[0].Type != "image_url" {
		t.Errorf("expected image_url block, got %q", imgs[0].Type)
	}
	if !strings.HasPrefix(imgs[0].ImageURL, "data:image/png;base64,") {
		t.Errorf("expected data URI prefix, got %q", imgs[0].ImageURL[:40])
	}
	if strings.Contains(result, "photo.png") {
		t.Errorf("expected text metadata to be skipped for inlined image, got %q", result)
	}
}

func TestProcessFilePartsInlinePDFImageBasedEmitsFileBlock(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlinePDF = true
	agent.cfg.MaxInlinePDFBytes = 20 * 1024 * 1024
	agent.cfg.MaxInlinePDFPages = 100
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "scan.pdf", makeImagePDF(t), "application/pdf")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/scan.pdf"}}},
	}
	result, blocks, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read this", -1)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 file block for image-based PDF, got %d", len(blocks))
	}
	if blocks[0].Type != "file" {
		t.Errorf("expected file block, got %q", blocks[0].Type)
	}
	if !strings.HasPrefix(blocks[0].FileData, "data:application/pdf;base64,") {
		t.Errorf("expected PDF data URI, got %q", blocks[0].FileData[:40])
	}
	if blocks[0].FileName != "scan.pdf" || blocks[0].FileType != "application/pdf" {
		t.Errorf("unexpected file metadata: name=%q type=%q", blocks[0].FileName, blocks[0].FileType)
	}
	if strings.Contains(result, "scan.pdf") {
		t.Errorf("expected text metadata to be skipped for inlined PDF, got %q", result)
	}
}

func TestProcessFilePartsInlinePDFTextRichFallsBack(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlinePDF = true
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "report.pdf", readTextPDF(t), "application/pdf")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/report.pdf"}}},
	}
	result, blocks, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read this", -1)
	if len(blocks) != 0 {
		t.Fatalf("expected no file block for text-rich PDF, got %d", len(blocks))
	}
	if !strings.Contains(result, "report.pdf") {
		t.Errorf("expected metadata summary mentioning filename for text PDF, got %q", result)
	}
}

func TestProcessFilePartsInlinePDFDisabledByDefault(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "scan.pdf", makeImagePDF(t), "application/pdf")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/scan.pdf"}}},
	}
	_, blocks, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read this", -1)
	if len(blocks) != 0 {
		t.Fatalf("expected no file block when inline PDF is disabled, got %d", len(blocks))
	}
}

func TestProcessFilePartsInlinePDFBytesCap(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlinePDF = true
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	pdf := makeImagePDF(t)
	agent.cfg.MaxInlinePDFBytes = len(pdf)
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "scan1.pdf", pdf, "application/pdf")
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "scan2.pdf", pdf, "application/pdf")

	msg := a2a.Message{Parts: []a2a.Part{
		{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/scan1.pdf"}},
		{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/scan2.pdf"}},
	}}
	result, blocks, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read these", -1)
	if len(blocks) != 1 {
		t.Fatalf("expected exactly 1 file block under the byte cap, got %d", len(blocks))
	}
	if !strings.Contains(result, "scan2.pdf") {
		t.Errorf("expected the capped second PDF to fall back to a text note, got %q", result)
	}
}

func TestProcessFilePartsInlinePDFOversizeFileFallsBack(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlinePDF = true
	agent.cfg.MaxInlinePDFBytes = 32
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "scan.pdf", makeImagePDF(t), "application/pdf")

	msg := a2a.Message{Parts: []a2a.Part{
		{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/scan.pdf"}},
	}}
	result, blocks, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read this", -1)
	if len(blocks) != 0 {
		t.Fatalf("expected no file block for an oversize PDF, got %d", len(blocks))
	}
	if !strings.Contains(result, "scan.pdf") {
		t.Errorf("expected oversize PDF to fall back to a text note, got %q", result)
	}
}

func TestProcessFilePartsInlinePDFCountCap(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlinePDF = true
	agent.cfg.MaxInlinePDFCount = 2
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	var parts []a2a.Part
	for i := range 4 {
		name := fmt.Sprintf("scan%d.pdf", i)
		saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", name, makeImagePDF(t), "application/pdf")
		parts = append(parts, a2a.Part{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/" + name}})
	}
	msg := a2a.Message{Parts: parts}
	result, blocks, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read these", -1)
	if len(blocks) != 2 {
		t.Fatalf("expected exactly 2 inlined PDFs under the count cap, got %d", len(blocks))
	}
	if !strings.Contains(result, "scan2.pdf") || !strings.Contains(result, "scan3.pdf") {
		t.Errorf("expected PDFs past the count cap to fall back to text notes, got %q", result)
	}
}

func TestProcessFilePartsInlinePDFPageCap(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlinePDF = true
	agent.cfg.MaxInlinePDFPages = 1
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "big.pdf", makeMixedPDF(t), "application/pdf")

	msg := a2a.Message{Parts: []a2a.Part{
		{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/big.pdf"}},
	}}
	result, blocks, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read this", -1)
	if len(blocks) != 0 {
		t.Fatalf("expected no file block when page count exceeds the cap, got %d", len(blocks))
	}
	if !strings.Contains(result, "big.pdf") {
		t.Errorf("expected over-cap PDF to fall back to a text note, got %q", result)
	}
}

func TestProcessFilePartsInlinePDFCorruptFallsBack(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlinePDF = true
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "broken.pdf", []byte("%PDF-1.4 not a real pdf"), "application/pdf")

	msg := a2a.Message{Parts: []a2a.Part{
		{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/broken.pdf"}},
	}}
	result, blocks, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read this", -1)
	if len(blocks) != 0 {
		t.Fatalf("expected no file block for a corrupt PDF, got %d", len(blocks))
	}
	if !strings.Contains(result, "broken.pdf") {
		t.Errorf("expected corrupt PDF to fall back to a text note, got %q", result)
	}
}

func TestProcessFilePartsInlineVisionNonImageReturnsText(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlineVision = true
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "notes.pdf", []byte("%PDF-1.4"), "application/pdf")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/notes.pdf"}}},
	}
	result, imgs, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "read this", -1)
	if len(imgs) != 0 {
		t.Fatalf("expected no image blocks for non-image artifact, got %d", len(imgs))
	}
	if !strings.Contains(result, "notes.pdf") {
		t.Errorf("expected metadata summary for pdf, got %q", result)
	}
}

func TestProcessFilePartsInlineVisionSvgReturnsText(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlineVision = true
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "logo.svg", []byte("<svg/>"), "image/svg+xml")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/logo.svg"}}},
	}
	result, imgs, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "look", -1)
	if len(imgs) != 0 {
		t.Fatalf("SVG must never be inlined, got %d image blocks", len(imgs))
	}
	if !strings.Contains(result, "logo.svg") {
		t.Errorf("expected metadata summary for svg, got %q", result)
	}
}

func TestProcessFilePartsInlineVisionMaxImagesCap(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlineVision = true
	agent.cfg.MaxInlineVisionImages = 2
	agent.cfg.MaxInlineVisionBytes = 20 * 1024 * 1024
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "a.png", tinyPNG, "image/png")
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "b.png", tinyPNG, "image/png")
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "c.png", tinyPNG, "image/png")

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/a.png"}},
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/b.png"}},
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/c.png"}},
		},
	}
	result, imgs, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "see", -1)
	if len(imgs) != 2 {
		t.Fatalf("expected cap at 2 images, got %d", len(imgs))
	}
	if !strings.Contains(result, "c.png") {
		t.Errorf("expected third image to fall back to text metadata, got %q", result)
	}
	if strings.Contains(result, "a.png") || strings.Contains(result, "b.png") {
		t.Errorf("first two images should have been inlined (no text), got %q", result)
	}
}

func TestProcessFilePartsInlineVisionMaxBytesCap(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.EnableInlineVision = true
	agent.cfg.MaxInlineVisionImages = 10
	agent.cfg.MaxInlineVisionBytes = len(tinyPNG)
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "a.png", tinyPNG, "image/png")
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "b.png", tinyPNG, "image/png")

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/a.png"}},
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/b.png"}},
		},
	}
	result, imgs, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "see", -1)
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image inlined then cap hit, got %d", len(imgs))
	}
	if !strings.Contains(result, "b.png") {
		t.Errorf("expected second image to fall back to text metadata, got %q", result)
	}
}

func TestSetSkillManager(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	if agent.SkillManager() != nil {
		t.Error("expected nil skill manager initially")
	}
	agent.SetSkillManager(nil)
	if agent.SkillManager() != nil {
		t.Error("expected nil after setting nil")
	}
}

func TestSetSkillRehydrator(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	agent.SetSkillRehydrator(nil)
}

type mockStartable struct {
	started bool
	stopped bool
}

func (m *mockStartable) Start(_ context.Context) error { m.started = true; return nil }
func (m *mockStartable) Stop()                         { m.stopped = true }

func TestAddSubComponent(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	sc := &mockStartable{}
	agent.AddSubComponent(sc)
	if len(agent.subComponents) != 1 {
		t.Errorf("expected 1 sub-component, got %d", len(agent.subComponents))
	}
}

func TestAddSubComponentFromConcurrentCallers(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { agent.AddSubComponent(&mockStartable{}) })
		wg.Go(func() { _ = agent.subComponentsSnapshot() })
	}
	wg.Wait()
	if got := len(agent.subComponentsSnapshot()); got != 16 {
		t.Errorf("sub-components = %d, want 16", got)
	}
}

func TestASubComponentAddedAfterStopIsStoppedAndRestartedWithTheAgent(t *testing.T) {
	agent := New(AgentConfig{Name: "late-component", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = newMockBroker()
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := agent.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	late := &mockStartable{}
	agent.AddSubComponent(late)
	if !late.stopped {
		t.Error("a component added after the agent stopped was left running")
	}

	if err := agent.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })
	if !late.started {
		t.Error("the restarted agent did not start the component added while it was stopped")
	}
	running := &mockStartable{}
	agent.AddSubComponent(running)
	if running.stopped {
		t.Error("a component added to a running agent was stopped")
	}
}

func TestExtractAuthResponseValid(t *testing.T) {
	agent, _, _ := newTestAgent()

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "data", Data: map[string]any{
				"type":           "auth_response",
				"credential_key": "my-key",
				"code":           "abc123",
				"target_agent":   "agent-1",
			}},
		},
	}

	resp := agent.extractAuthResponse(msg)
	if resp == nil {
		t.Fatal("expected non-nil auth response")
	} else if resp.CredentialKey != "my-key" {
		t.Errorf("CredentialKey = %q", resp.CredentialKey)
	}
	if resp.Code != "abc123" {
		t.Errorf("Code = %q", resp.Code)
	}
}

func TestExtractAuthResponseNoParts(t *testing.T) {
	agent, _, _ := newTestAgent()
	msg := a2a.Message{}
	resp := agent.extractAuthResponse(msg)
	if resp != nil {
		t.Error("expected nil for no parts")
	}
}

func TestExtractAuthResponseWrongType(t *testing.T) {
	agent, _, _ := newTestAgent()
	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "data", Data: map[string]any{
				"type": "other_type",
			}},
		},
	}
	resp := agent.extractAuthResponse(msg)
	if resp != nil {
		t.Error("expected nil for wrong type")
	}
}

func TestExtractAuthResponseSkipsTextParts(t *testing.T) {
	agent, _, _ := newTestAgent()
	msg := a2a.Message{
		Parts: []a2a.Part{
			a2a.NewTextPart("hello"),
			{Kind: "data", Data: map[string]any{
				"type":           "auth_response",
				"credential_key": "k1",
			}},
		},
	}
	resp := agent.extractAuthResponse(msg)
	if resp == nil {
		t.Fatal("expected auth response after text part")
	}
}

func TestRegisterAndUnregisterHILRequest(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	ch := agent.registerHILRequest("req-1", hilTestOwner, "")
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}

	agent.pendingHILMu.Lock()
	_, exists := agent.pendingHIL["req-1"]
	agent.pendingHILMu.Unlock()
	if !exists {
		t.Error("expected req-1 in pending map")
	}

	agent.unregisterHILRequest("req-1")
	agent.pendingHILMu.Lock()
	_, exists = agent.pendingHIL["req-1"]
	agent.pendingHILMu.Unlock()
	if exists {
		t.Error("expected req-1 removed from pending map")
	}
}

func TestBuildSystemPromptWithGatewayContext(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.InjectSystemPurpose = true
	agent.cfg.InjectResponseFormat = true
	agent.cfg.InjectUserProfile = true

	gc := gatewayContext{
		SystemPurpose:  "Help users code",
		ResponseFormat: "markdown",
		UserProfile:    "Senior Engineer",
	}

	prompt := agent.buildSystemPrompt(context.Background(), nil, gc, promptSkillState{}, false)
	if !strings.Contains(prompt, "Help users code") {
		t.Error("expected system purpose in prompt")
	}
	if !strings.Contains(prompt, "markdown") {
		t.Error("expected response format in prompt")
	}

	if strings.Contains(prompt, "Senior Engineer") {
		t.Error("user profile must not enter the system prompt: it would make the cached prefix per-user")
	}
	msgs := agent.perRequestContextMessages(gc)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "Senior Engineer") {
		t.Errorf("expected the user profile to be delivered per-request, got %d message(s)", len(msgs))
	}
}

func TestConversationContextDeliveredOutsideTheSystemPrompt(t *testing.T) {
	agent, _, _ := newTestAgent()
	gc := gatewayContext{ConversationContext: "--- BEGIN SLACK CONTEXT ---\n<@U1>: hello\n--- END SLACK CONTEXT ---"}

	if prompt := agent.buildSystemPrompt(context.Background(), nil, gc, promptSkillState{}, false); strings.Contains(prompt, "BEGIN SLACK CONTEXT") {
		t.Error("conversation context must not enter the system prompt: it carries the cache breakpoint")
	}

	msgs := agent.perRequestContextMessages(gc)
	if len(msgs) != 1 {
		t.Fatalf("expected one per-request context message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "BEGIN SLACK CONTEXT") {
		t.Error("expected the conversation context to be delivered")
	}
	if !strings.Contains(msgs[0].Content, "Conversation Context") {
		t.Error("expected the Conversation Context section header")
	}
}

func TestBuildSystemPromptOmitsEmptyConversationContext(t *testing.T) {
	agent, _, _ := newTestAgent()
	if got := agent.perRequestContextMessages(gatewayContext{}); got != nil {
		t.Errorf("empty conversation context should produce no message, got %d", len(got))
	}
}

func TestConversationContextNotPersistedAcrossTurnsAndStaysLastMessage(t *testing.T) {
	agent, mock, store := newTestAgent(
		&llm.ChatResponse{Content: "answer 1"},
		&llm.ChatResponse{Content: "answer 2"},
	)
	const marker = "--- BEGIN SLACK CONTEXT ---"
	withCtx := func(text string) a2a.Message {
		m := newUserMessage(text)
		m.Metadata = map[string]any{"conversation_context": marker + "\n> <@U1>: hi\n--- END SLACK CONTEXT ---"}
		return m
	}

	for _, tn := range []struct{ id, text string }{
		{"task-001", "first question"},
		{"task-002", "second question"},
	} {
		if _, err := agent.HandleTask(context.Background(), tn.id, withCtx(tn.text),
			a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}); err != nil {
			t.Fatalf("turn %s: %v", tn.id, err)
		}
	}

	calls := mock.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected >=2 LLM calls, got %d", len(calls))
	}
	msgs := calls[len(calls)-1].Messages
	if strings.Contains(msgs[0].Content, marker) {
		t.Errorf("context is in the system prompt, which invalidates the cached prefix:\n%s", msgs[0].Content)
	}
	seen, at := 0, -1
	for i, m := range msgs {
		if strings.Contains(m.Content, marker) {
			seen++
			at = i
		}
	}
	if seen != 1 {
		t.Errorf("context should appear exactly once per request, found %d (0 = dropped, >1 = compounding)", seen)
	}
	if at != len(msgs)-1 {
		t.Errorf("context at index %d of %d — it must be the trailing message so every "+
			"cache breakpoint sits before it and the history repairs do not scan it",
			at, len(msgs))
	}

	sess, err := store.Get(context.Background(), "test-agent", "user-1", "ctx-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range sess.Messages {
		if strings.Contains(m.Content, marker) {
			t.Errorf("conversation context was persisted to session history: role=%s content=%q", m.Role, m.Content)
		}
	}
	if len(sess.Messages) != 4 {
		t.Errorf("expected 4 persisted messages (2 user + 2 assistant), got %d", len(sess.Messages))
	}
}

func TestBuildSystemPromptWithProjectIDRequiresIndexSearchBeforeAnswering(t *testing.T) {
	agent, _, _ := newTestAgent()
	gc := gatewayContext{ProjectID: "proj-42"}

	prompt := agent.buildSystemPrompt(context.Background(), nil, gc, promptSkillState{}, true)
	if !strings.Contains(prompt, "index_search") {
		t.Error("expected index_search instructions when the tool is available")
	}
	if !strings.Contains(prompt, "MUST call the index_search tool") {
		t.Error("expected imperative index_search directive (MUST call ... BEFORE answering) when projectID is set")
	}
	if !strings.Contains(prompt, "BEFORE answering") {
		t.Error("expected the BEFORE-answering ordering directive when projectID is set")
	}
}

func TestBuildSystemPromptOmitsIndexSearchWhenUnavailable(t *testing.T) {
	agent, _, _ := newTestAgent()
	gc := gatewayContext{ProjectID: "proj-42"}

	prompt := agent.buildSystemPrompt(context.Background(), nil, gc, promptSkillState{}, false)
	if strings.Contains(prompt, "index_search") {
		t.Error("index_search directive must be omitted when the tool is unavailable, even with a project set")
	}
}

func TestBuildSystemPromptRedirectCapabilityNote(t *testing.T) {
	stub := func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("ok"), nil
	}

	t.Run("injected when redirect tool registered and gateway not capable", func(t *testing.T) {
		agent := newTestAgentWithStubTool(t, "redirect_user", stub)
		prompt := agent.buildSystemPrompt(context.Background(), nil, gatewayContext{WebUIRedirect: false}, promptSkillState{}, false)
		if !strings.Contains(prompt, "Entrypoint Redirect Capability") {
			t.Error("expected redirect-capability note for non-web gateway with redirect tool")
		}
	})

	t.Run("omitted when only scheduled_task_redirect is registered", func(t *testing.T) {
		agent := newTestAgentWithStubTool(t, "scheduled_task_redirect", stub)
		prompt := agent.buildSystemPrompt(context.Background(), nil, gatewayContext{WebUIRedirect: false}, promptSkillState{}, false)
		if strings.Contains(prompt, "Entrypoint Redirect Capability") {
			t.Error("redirect-capability note should not appear for scheduled_task_redirect (scheduling is in-chat now)")
		}
	})

	t.Run("omitted when gateway is web-capable", func(t *testing.T) {
		agent := newTestAgentWithStubTool(t, "redirect_user", stub)
		prompt := agent.buildSystemPrompt(context.Background(), nil, gatewayContext{WebUIRedirect: true}, promptSkillState{}, false)
		if strings.Contains(prompt, "Entrypoint Redirect Capability") {
			t.Error("redirect-capability note should not appear on web gateway")
		}
	})

	t.Run("omitted when no redirect tool registered", func(t *testing.T) {
		agent, _, _ := newTestAgent()
		prompt := agent.buildSystemPrompt(context.Background(), nil, gatewayContext{WebUIRedirect: false}, promptSkillState{}, false)
		if strings.Contains(prompt, "Entrypoint Redirect Capability") {
			t.Error("redirect-capability note should not appear without a redirect tool")
		}
	})
}

func TestBuildSystemPromptDisableTemplates(t *testing.T) {
	agent, _, _ := newTestAgent()
	falseVal := false
	agent.cfg.EnableTemplateInstructions = &falseVal

	prompt := agent.buildSystemPrompt(context.Background(), nil, gatewayContext{}, promptSkillState{}, false)
	if !strings.Contains(prompt, "You are a helpful assistant") {
		t.Error("expected base instruction")
	}
}

func TestBuildSystemPromptNeverContainsTime(t *testing.T) {
	agent, _, _ := newTestAgent()

	prompt := agent.buildSystemPrompt(context.Background(), nil, gatewayContext{}, promptSkillState{}, false)
	if strings.Contains(prompt, "Current Time") {
		t.Error("system prompt must never contain the current time (busts the prompt cache)")
	}
}

type mockAuthHandler struct {
	key string
}

func (m *mockAuthHandler) CredentialKey() string { return m.key }
func (m *mockAuthHandler) HandleAuthResponse(_ context.Context, _ *a2a.AuthResponseData, _ *tool.Context) error {
	return nil
}

func TestRegisterAuthHandler(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	handler := &mockAuthHandler{key: "my-cred-key"}
	agent.RegisterAuthHandler(handler)
	if _, ok := agent.authHandlers["my-cred-key"]; !ok {
		t.Error("expected handler to be registered")
	}
}

func TestRegisterMCPAuthHandler(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	handler := &mockAuthHandler{key: "mcp-key"}
	agent.RegisterMCPAuthHandler(handler)
	if _, ok := agent.authHandlers["mcp-key"]; !ok {
		t.Error("expected MCP handler to be registered")
	}
}

func TestTaskStateStoreStoreAndLoad(t *testing.T) {
	store := newTaskStateStore()
	state := &TaskExecutionState{TaskID: "task-1"}
	store.Store(state)

	loaded := store.Load("task-1")
	if loaded == nil {
		t.Fatal("expected non-nil state")
	} else if loaded.TaskID != "task-1" {
		t.Errorf("TaskID = %q", loaded.TaskID)
	}
}

func TestTaskStateStoreLoadNotFound(t *testing.T) {
	store := newTaskStateStore()
	loaded := store.Load("nonexistent")
	if loaded != nil {
		t.Error("expected nil for missing task")
	}
}

func TestTaskStateStoreDelete(t *testing.T) {
	store := newTaskStateStore()
	store.Store(&TaskExecutionState{TaskID: "task-1"})
	store.Delete("task-1")
	if store.Load("task-1") != nil {
		t.Error("expected nil after delete")
	}
}

func TestTaskExecutionStateDecrementPending(t *testing.T) {
	state := &TaskExecutionState{TotalPending: 3}
	remaining := state.DecrementPending()
	if remaining != 2 {
		t.Errorf("remaining = %d, want 2", remaining)
	}
	remaining = state.DecrementPending()
	if remaining != 1 {
		t.Errorf("remaining = %d, want 1", remaining)
	}
}

func TestDeliverHILResponseToRegistered(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	ch := agent.registerHILRequest("req-1", hilTestOwner, "")

	resp := &a2a.UserInputResponseData{RequestID: "req-1"}
	delivered := agent.deliverHILResponseAsOwner(resp)
	if !delivered {
		t.Error("expected delivery to succeed")
	}

	select {
	case got := <-ch:
		if got.RequestID != "req-1" {
			t.Errorf("RequestID = %q", got.RequestID)
		}
	default:
		t.Error("expected response on channel")
	}
}

func TestDeliverHILResponseToUnregistered(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	resp := &a2a.UserInputResponseData{RequestID: "req-nonexistent"}
	delivered := agent.deliverHILResponseAsOwner(resp)
	if delivered {
		t.Error("expected delivery to fail for unregistered request")
	}
}

func TestMarkAuthResponseProcessed_Dedupes(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})

	if already := agent.markAuthResponseProcessed("fc-1"); already {
		t.Error("first call should report !already")
	}
	if already := agent.markAuthResponseProcessed("fc-1"); !already {
		t.Error("second call should report already")
	}
	if already := agent.markAuthResponseProcessed("fc-2"); already {
		t.Error("new ID should not be marked already")
	}
}

func TestMarkAuthResponseProcessed_EvictsOldest(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})

	for i := 0; i < authRespDedupMaxEntries; i++ {
		if already := agent.markAuthResponseProcessed(fmt.Sprintf("fc-%d", i)); already {
			t.Fatalf("fc-%d should be new", i)
		}
	}
	if already := agent.markAuthResponseProcessed("fc-overflow"); already {
		t.Error("overflow should be new")
	}
	if already := agent.markAuthResponseProcessed("fc-0"); already {
		t.Error("evicted ID fc-0 should be treated as new after eviction")
	}
}

func TestMarkAuthResponseProcessed_ConcurrentSafe(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agent.markAuthResponseProcessed(fmt.Sprintf("concurrent-%d", id))
		}(i)
	}
	wg.Wait()
}

func TestProcessFilePartsInlinesSmallJSONArtifact(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 2048
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	body := []byte(`{"customer_id":"abc","value":42}`)
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "input.json", body, "application/json")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/input.json"}}},
	}
	result, imgs, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "process this", -1)
	if len(imgs) != 0 {
		t.Fatalf("no image blocks expected, got %d", len(imgs))
	}
	if !strings.Contains(result, string(body)) {
		t.Errorf("expected inlined artifact content in result, got %q", result)
	}
	if !strings.Contains(result, "input.json") {
		t.Errorf("expected filename mentioned in result, got %q", result)
	}
	if !strings.Contains(result, "Begin content of") {
		t.Errorf("expected inline-content header, got %q", result)
	}
}

func TestProcessFilePartsSkipsInlineWhenOverThreshold(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 16
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	body := []byte(`{"a":"this exceeds sixteen bytes for sure"}`)
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "big.json", body, "application/json")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/big.json"}}},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "process", -1)
	if strings.Contains(result, "Begin content of") {
		t.Errorf("artifact should not be inlined when over threshold, got %q", result)
	}
	if !strings.Contains(result, "big.json") {
		t.Errorf("metadata summary should still mention filename, got %q", result)
	}
}

func TestProcessFilePartsSkipsInlineForBinaryMIME(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 1 << 20
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "blob.bin", []byte{0x00, 0x01, 0x02}, "application/octet-stream")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/blob.bin"}}},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "see", -1)
	if strings.Contains(result, "Begin content of") {
		t.Errorf("binary MIME must never be inlined, got %q", result)
	}
}

func TestProcessFilePartsInlineDisabledWhenThresholdZero(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 0
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "sess-1", "in.json", []byte(`{"a":1}`), "application/json")

	msg := a2a.Message{
		Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/sess-1/in.json"}}},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "see", -1)
	if strings.Contains(result, "Begin content of") {
		t.Errorf("inline must be disabled when MaxInlineArtifactBytes=0, got %q", result)
	}
}

func TestProcessFilePartsInlinesSmallInlineBytes(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 2048
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	body := []byte(`{"input_value":42}`)
	b64 := base64.StdEncoding.EncodeToString(body)
	msg := a2a.Message{
		Parts: []a2a.Part{
			{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     "wf_classify_input_xyz.json",
					MIMEType: "application/json",
					Bytes:    b64,
				},
			},
		},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "Classify the input value", -1)
	if !strings.Contains(result, string(body)) {
		t.Errorf("expected inlined content from inline-bytes file part, got %q", result)
	}
	if !strings.Contains(result, "wf_classify_input_xyz.json") {
		t.Errorf("expected file part name mentioned, got %q", result)
	}
	if !strings.Contains(result, "Begin content of") {
		t.Errorf("expected inline-content header, got %q", result)
	}
}

func TestProcessFilePartsInlineBytesOverThresholdSummarized(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 8
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	body := []byte(`{"input_value":42}`)
	b64 := base64.StdEncoding.EncodeToString(body)
	msg := a2a.Message{
		ContextID: "sess-1",
		Parts: []a2a.Part{
			{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     "big_input.json",
					MIMEType: "application/json",
					Bytes:    b64,
				},
			},
		},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{UserID: "user-1"}, msg.ContextID, "go", -1)
	if !strings.Contains(result, "big_input.json") {
		t.Errorf("expected summary to mention the filename, got %q", result)
	}
	loc := scopedSvc.Resolve("test-agent", "user-1", "sess-1", "big_input.json")
	content, mime, err := scopedSvc.Inner.Load(context.Background(), loc, nil)
	if err != nil {
		t.Fatalf("expected artifact saved under agent namespace, load error: %v", err)
	}
	if string(content) != string(body) {
		t.Errorf("saved content differs from input")
	}
	if mime != "application/json" {
		t.Errorf("mime = %q, want application/json", mime)
	}
}

const structuredInputMarker = "end_of_input_marker"

func structuredInputOfSize(t *testing.T, size int) (body []byte, part a2a.Part) {
	t.Helper()
	padding := strings.Repeat("r", size-len(`{"rows":""}`)-len(structuredInputMarker))
	body = []byte(`{"rows":"` + padding + structuredInputMarker + `"}`)
	return body, a2a.NewFilePartWithBytes(base64.StdEncoding.EncodeToString(body), "note_input.json", "application/json")
}

func newInlineTestAgent(inlineMaxBytes int) (*Agent, *artifact.ScopedService) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = inlineMaxBytes
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc
	return agent, scopedSvc
}

func processStructuredInput(agent *Agent, input a2a.Part) string {
	msg := a2a.Message{ContextID: "sess-1", Parts: []a2a.Part{input}}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{UserID: "user-1"}, msg.ContextID, "Write the note.", 0)
	return result
}

func assertSavedAsArtifact(t *testing.T, scopedSvc *artifact.ScopedService, filename string, want []byte) {
	t.Helper()
	content, _, err := scopedSvc.Inner.Load(context.Background(), scopedSvc.Resolve("test-agent", "user-1", "sess-1", filename), nil)
	if err != nil {
		t.Fatalf("expected %s saved as an artifact, load error: %v", filename, err)
	}
	if string(content) != string(want) {
		t.Errorf("saved artifact has %d bytes, want %d", len(content), len(want))
	}
}

func TestProcessFilePartsInlinesStructuredInputAboveAttachmentThreshold(t *testing.T) {
	agent, _ := newInlineTestAgent(2048)

	body, input := structuredInputOfSize(t, 3000)
	msg := a2a.Message{ContextID: "sess-1", Parts: []a2a.Part{input, input}}

	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{UserID: "user-1"}, msg.ContextID, "Write the note.", 0)

	if got := strings.Count(result, string(body)); got != 1 {
		t.Fatalf("structured input inlined %d times, want once (only the input part, not the same-sized attachment); prompt:\n%s", got, result)
	}
	if !strings.Contains(result, "--- Metadata for artifact 'note_input.json'") {
		t.Errorf("the attachment over MaxInlineArtifactBytes should still be summarised as an artifact; prompt:\n%s", result)
	}
}

func TestProcessFilePartsInlinesStructuredInputAtInlineLimit(t *testing.T) {
	agent, _ := newInlineTestAgent(2048)

	body, input := structuredInputOfSize(t, structuredInputInlineMaxBytes)

	if result := processStructuredInput(agent, input); !strings.Contains(result, string(body)) {
		t.Errorf("an input of exactly structuredInputInlineMaxBytes should be inlined")
	}
}

func TestProcessFilePartsStructuredInputOverInlineLimitIsSavedAsArtifact(t *testing.T) {
	agent, scopedSvc := newInlineTestAgent(2048)

	body, input := structuredInputOfSize(t, structuredInputInlineMaxBytes+1)
	result := processStructuredInput(agent, input)

	if strings.Contains(result, structuredInputMarker) {
		t.Errorf("an input over structuredInputInlineMaxBytes must not be inlined")
	}
	if !strings.Contains(result, "--- Metadata for artifact 'note_input.json'") {
		t.Errorf("expected the prompt to name the saved input file, got %q", result)
	}
	assertSavedAsArtifact(t, scopedSvc, "note_input.json", body)
}

func TestProcessFilePartsStructuredInputUsesLargerConfiguredLimit(t *testing.T) {
	agent, _ := newInlineTestAgent(64 << 10)

	body, input := structuredInputOfSize(t, 20<<10)

	if result := processStructuredInput(agent, input); !strings.Contains(result, string(body)) {
		t.Errorf("a 20 KiB input should be inlined when MaxInlineArtifactBytes is 64 KiB")
	}
}

func TestProcessFilePartsStructuredInputNotInlinedWhenInliningDisabled(t *testing.T) {
	for _, inlineMaxBytes := range []int{0, -1} {
		t.Run(fmt.Sprint(inlineMaxBytes), func(t *testing.T) {
			agent, _ := newInlineTestAgent(inlineMaxBytes)

			_, input := structuredInputOfSize(t, 100)

			if result := processStructuredInput(agent, input); strings.Contains(result, structuredInputMarker) {
				t.Errorf("MaxInlineArtifactBytes=%d must disable inlining, got %q", inlineMaxBytes, result)
			}
		})
	}
}

func TestProcessFilePartsInlinesUnencodedStructuredInput(t *testing.T) {
	agent, _ := newInlineTestAgent(2048)

	raw := `{"customer":"alice@example.com","rows":[{"revenue":120.5}]}`
	input := a2a.NewFilePartWithBytes(raw, "note_input.json", "application/json")

	if result := processStructuredInput(agent, input); !strings.Contains(result, raw) {
		t.Errorf("unencoded JSON input should be inlined as the validator reads it, got %q", result)
	}
}

func TestProcessFilePartsJustOverThresholdIsSavedAsArtifact(t *testing.T) {
	agent, scopedSvc := newInlineTestAgent(2048)

	body, input := structuredInputOfSize(t, 2050)
	msg := a2a.Message{ContextID: "sess-1", Parts: []a2a.Part{input}}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{UserID: "user-1"}, msg.ContextID, "Write the note.", -1)

	if strings.Contains(result, "exceeds the inline threshold") {
		t.Errorf("a part just over the threshold should be saved as an artifact, not dropped with a note: %q", result)
	}
	assertSavedAsArtifact(t, scopedSvc, "note_input.json", body)
}

func TestProcessFilePartsInlineBytesBinarySkipped(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.MaxInlineArtifactBytes = 1 << 20
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	b64 := base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02})
	msg := a2a.Message{
		Parts: []a2a.Part{
			{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     "blob.bin",
					MIMEType: "application/octet-stream",
					Bytes:    b64,
				},
			},
		},
	}
	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{}, msg.ContextID, "see", -1)
	if strings.Contains(result, "Begin content of") {
		t.Errorf("binary inline-bytes must not be inlined, got %q", result)
	}
}

func TestIsInlineableTextMIME(t *testing.T) {
	cases := map[string]bool{
		"text/plain":                   true,
		"text/csv":                     true,
		"text/markdown; charset=utf-8": true,
		"application/json":             true,
		"application/yaml":             true,
		"application/x-yaml":           true,
		"application/xml":              true,
		"application/vnd.api+json":     true,
		"image/png":                    false,
		"application/pdf":              false,
		"application/octet-stream":     false,
		"":                             false,
	}
	for mt, want := range cases {
		if got := isInlineableTextMIME(mt); got != want {
			t.Errorf("isInlineableTextMIME(%q) = %v, want %v", mt, got, want)
		}
	}
}

func TestSynthesizeResultEmbedSuccess(t *testing.T) {
	got := synthesizeResultEmbed("output.json", "success", "")
	want := "\u00abresult:artifact=output.json status=success\u00bb"
	if got != want {
		t.Errorf("synthesizeResultEmbed = %q, want %q", got, want)
	}
}

func TestSynthesizeResultEmbedErrorWithMessage(t *testing.T) {
	got := synthesizeResultEmbed("err.json", "error", "upstream failed")
	want := `«result:artifact=err.json status=error message="upstream failed"»`
	if got != want {
		t.Errorf("synthesizeResultEmbed = %q, want %q", got, want)
	}
}

func TestSynthesizeResultEmbedEscapesQuotes(t *testing.T) {
	got := synthesizeResultEmbed("e.json", "error", `a "quoted" thing`)
	want := `«result:artifact=e.json status=error message="a \"quoted\" thing"»`
	if got != want {
		t.Errorf("synthesizeResultEmbed = %q, want %q", got, want)
	}
}

func TestParseBlockParamsCapturesStatusAndMessage(t *testing.T) {
	block := &FencedBlockEvent{Type: FencedBlockArtifact}
	parseBlockParams(`filename="r.json" status="success"`, block)
	if block.Status != "success" {
		t.Errorf("Status = %q, want %q", block.Status, "success")
	}
	if block.Filename != "r.json" {
		t.Errorf("Filename = %q, want %q", block.Filename, "r.json")
	}

	block2 := &FencedBlockEvent{Type: FencedBlockArtifact}
	parseBlockParams(`filename="e.json" status="error" message="boom"`, block2)
	if block2.Status != "error" || block2.Message != "boom" {
		t.Errorf("got status=%q msg=%q, want error/boom", block2.Status, block2.Message)
	}
}

func TestFinalizeArtifactBlockEmitsResultEmbedInStructuredInvocation(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	var visible, raw strings.Builder
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		Filename:    "out.json",
		FilenameSet: true,
		MIMEType:    "application/json",
		Content:     `{"ok":true}`,
		Status:      "success",
	}
	info := &streamCallInfo{
		UserID:                 "user-1",
		SessionID:              "sess-1",
		IsStructuredInvocation: true,
	}

	record := agent.finalizeArtifactBlock(context.Background(), sp, block, &streamPipelineState{
		visibleText:         &visible,
		rawText:             &raw,
		info:                info,
		artBytesTransferred: len(block.Content),
	})
	if record == nil || record.Status != "success" {
		t.Fatalf("expected successful record, got %+v", record)
	}

	if !strings.Contains(visible.String(), "«result:artifact=out.json status=success»") {
		t.Errorf("expected synthesized result embed in visibleText, got %q", visible.String())
	}
	if !strings.Contains(raw.String(), "«result:artifact=out.json status=success»") {
		t.Errorf("expected synthesized result embed in rawText, got %q", raw.String())
	}
}

func TestFinalizeArtifactBlockOmitsResultEmbedWhenNotStructured(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	var visible, raw strings.Builder
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		Filename:    "out.json",
		FilenameSet: true,
		MIMEType:    "application/json",
		Content:     `{"ok":true}`,
		Status:      "success",
	}
	info := &streamCallInfo{
		UserID:                 "user-1",
		SessionID:              "sess-1",
		IsStructuredInvocation: false,
	}

	agent.finalizeArtifactBlock(context.Background(), sp, block, &streamPipelineState{
		visibleText:         &visible,
		rawText:             &raw,
		info:                info,
		artBytesTransferred: len(block.Content),
	})
	if strings.Contains(visible.String(), "«result:") {
		t.Errorf("must not emit result embed outside structured invocation, got %q", visible.String())
	}
}

func TestFinalizeArtifactBlockExemptsWorkflowContentFromWorkingTag(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	var visible, raw strings.Builder
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		Filename:    "refined_content.md",
		FilenameSet: true,
		MIMEType:    "text/markdown",
		Content:     "# Draft",
		Status:      "success",
	}
	info := &streamCallInfo{
		UserID:                 "user-1",
		SessionID:              "sess-1",
		IsStructuredInvocation: true,
		IsWorkflowDispatch:     true,
	}

	record := agent.finalizeArtifactBlock(context.Background(), sp, block, &streamPipelineState{
		visibleText:         &visible,
		rawText:             &raw,
		info:                info,
		artBytesTransferred: len(block.Content),
	})
	if record == nil {
		t.Fatal("expected a record")
	}
	if slices.Contains(record.Tags, a2a.ArtifactTagWorking) {
		t.Errorf("workflow-produced content artifact must not be tagged %q, got tags %v", a2a.ArtifactTagWorking, record.Tags)
	}
}

func TestFinalizeArtifactBlockKeepsWorkingTagForWorkflowHandoffArtifact(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	var visible, raw strings.Builder
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		Filename:    "wf_refine_content_output_abc123.json",
		FilenameSet: true,
		MIMEType:    "application/json",
		Content:     `{"refined_filename":"refined_content.md"}`,
		Status:      "success",
	}
	info := &streamCallInfo{
		UserID:                 "user-1",
		SessionID:              "sess-1",
		IsStructuredInvocation: true,
		IsWorkflowDispatch:     true,
	}

	record := agent.finalizeArtifactBlock(context.Background(), sp, block, &streamPipelineState{
		visibleText:         &visible,
		rawText:             &raw,
		info:                info,
		artBytesTransferred: len(block.Content),
	})
	if record == nil {
		t.Fatal("expected a record")
	}
	if !slices.Contains(record.Tags, a2a.ArtifactTagWorking) {
		t.Errorf("workflow handoff artifact must stay tagged %q, got tags %v", a2a.ArtifactTagWorking, record.Tags)
	}
}

func TestFinalizeArtifactBlockKeepsWorkingTagForNonWorkflowStructuredInvocation(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	var visible, raw strings.Builder
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		Filename:    "some_output.json",
		FilenameSet: true,
		MIMEType:    "application/json",
		Content:     `{"ok":true}`,
		Status:      "success",
	}
	info := &streamCallInfo{
		UserID:                 "user-1",
		SessionID:              "sess-1",
		IsStructuredInvocation: true,
		IsWorkflowDispatch:     false,
	}

	record := agent.finalizeArtifactBlock(context.Background(), sp, block, &streamPipelineState{
		visibleText:         &visible,
		rawText:             &raw,
		info:                info,
		artBytesTransferred: len(block.Content),
	})
	if record == nil {
		t.Fatal("expected a record")
	}
	if !slices.Contains(record.Tags, a2a.ArtifactTagWorking) {
		t.Errorf("generic structured invocation artifact must stay tagged %q, got tags %v", a2a.ArtifactTagWorking, record.Tags)
	}
}

func TestFinalizeArtifactBlockEmitsErrorResultEmbed(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scopedSvc

	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	var visible, raw strings.Builder
	block := &FencedBlockEvent{
		Type:        FencedBlockArtifact,
		Filename:    "err.json",
		FilenameSet: true,
		MIMEType:    "application/json",
		Content:     `{"detail":"x"}`,
		Status:      "error",
		Message:     "upstream API failed",
	}
	info := &streamCallInfo{
		UserID:                 "user-1",
		SessionID:              "sess-1",
		IsStructuredInvocation: true,
	}

	agent.finalizeArtifactBlock(context.Background(), sp, block, &streamPipelineState{
		visibleText:         &visible,
		rawText:             &raw,
		info:                info,
		artBytesTransferred: len(block.Content),
	})
	want := `«result:artifact=err.json status=error message="upstream API failed"»`
	if !strings.Contains(visible.String(), want) {
		t.Errorf("expected error result embed %q in visibleText, got %q", want, visible.String())
	}
}

func TestFormatToolResultStatusFailedWithDetail(t *testing.T) {
	agent, _, _ := newTestAgent()

	r := tool.Failed("process crashed", &tool.ToolFailedDetail{
		Code:       "TOOL_EXIT_NONZERO",
		Message:    "exited with code 2",
		StderrTail: "some stderr output",
		StackTrace: "panic: out of memory",
	})

	result, _ := agent.formatToolResult(context.Background(), r, &tool.Context{}, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed["status"] != tool.StatusFailed {
		t.Errorf("status = %v, want %q", parsed["status"], tool.StatusFailed)
	}
	if parsed["code"] != "TOOL_EXIT_NONZERO" {
		t.Errorf("code = %v, want TOOL_EXIT_NONZERO", parsed["code"])
	}
	if parsed["stderr_tail"] != "some stderr output" {
		t.Errorf("stderr_tail = %v", parsed["stderr_tail"])
	}
	if parsed["stack_trace"] != "panic: out of memory" {
		t.Errorf("stack_trace = %v", parsed["stack_trace"])
	}
	if parsed["retryable"] != false {
		t.Errorf("retryable = %v, want false on a StatusFailed result", parsed["retryable"])
	}
}

func TestFormatToolResultFailedCarriesGuidance(t *testing.T) {
	agent, _, _ := newTestAgent()

	r := tool.Failed(`remote tool "slow": timed out after 300s`, &tool.ToolFailedDetail{
		Code:     "TOOL_TIMEOUT",
		Message:  "timed out after 300s",
		Guidance: "Do NOT retry this call and do NOT tell the user to retry.",
	})

	result, _ := agent.formatToolResult(context.Background(), r, &tool.Context{}, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed["retryable"] != false {
		t.Errorf("retryable = %v, want false", parsed["retryable"])
	}
	if got, _ := parsed["guidance"].(string); !strings.Contains(got, "Do NOT retry") {
		t.Errorf("guidance = %q, want the non-retry directive", got)
	}
}

func TestFormatToolResultFailedGuidanceNotShadowedByData(t *testing.T) {
	agent, _, _ := newTestAgent()

	r := tool.Failed("timed out", &tool.ToolFailedDetail{
		Code:     "TOOL_TIMEOUT",
		Guidance: "Do NOT retry this call.",
	})
	r.Data = map[string]any{"retryable": true, "guidance": "please try again"}

	result, _ := agent.formatToolResult(context.Background(), r, &tool.Context{}, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed["retryable"] != false {
		t.Errorf("retryable = %v, want the framework's false to win over tool Data", parsed["retryable"])
	}
	if got, _ := parsed["guidance"].(string); got != "Do NOT retry this call." {
		t.Errorf("guidance = %q, want the framework's value to win over tool Data", got)
	}
}

func TestFormatToolResultStatusErrorWithDetailOmitsRetryableVerdict(t *testing.T) {
	agent, _, _ := newTestAgent()

	r := tool.Error("connection refused")
	r.FailedDetail = &tool.ToolFailedDetail{
		Code:       "CONN_ERR",
		Message:    "connection refused",
		StderrTail: "ECONNREFUSED",
		StackTrace: "net.Dial failed",
	}

	result, _ := agent.formatToolResult(context.Background(), r, &tool.Context{}, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed["status"] != tool.StatusError {
		t.Errorf("status = %v, want %q", parsed["status"], tool.StatusError)
	}
	if parsed["code"] != "CONN_ERR" {
		t.Errorf("code = %v", parsed["code"])
	}
	if parsed["stderr_tail"] != "ECONNREFUSED" {
		t.Errorf("stderr_tail = %v", parsed["stderr_tail"])
	}
	if _, present := parsed["retryable"]; present {
		t.Error("retryable must be absent on StatusError; those failures are LLM-recoverable")
	}
	if _, present := parsed["guidance"]; present {
		t.Error("guidance must be absent when the failure detail carries none")
	}
}

func TestFormatToolResultStatusErrorSurfacesGuidanceWithoutRetryableVerdict(t *testing.T) {
	agent, _, _ := newTestAgent()

	r := tool.Error(`remote tool "slow": timed out after 5m10s`)
	r.FailedDetail = &tool.ToolFailedDetail{
		Code:     "SANDBOX_TIMEOUT",
		Message:  "timed out after 5m10s",
		Guidance: "The agent stopped waiting for this tool before it answered.",
	}

	result, _ := agent.formatToolResult(context.Background(), r, &tool.Context{}, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got, _ := parsed["guidance"].(string); !strings.Contains(got, "stopped waiting") {
		t.Errorf("guidance = %q, want the wait-window explanation", got)
	}
	if _, present := parsed["retryable"]; present {
		t.Error("retryable must stay absent on StatusError even when guidance is present")
	}
}

func TestFormatToolResultStderrTailTruncation(t *testing.T) {
	agent, _, _ := newTestAgent()

	longStderr := strings.Repeat("x", 3000)
	r := tool.Failed("crashed", &tool.ToolFailedDetail{
		Code:       "TOOL_EXIT_NONZERO",
		Message:    "crashed",
		StderrTail: longStderr,
	})

	result, _ := agent.formatToolResult(context.Background(), r, &tool.Context{}, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	stderrVal, _ := parsed["stderr_tail"].(string)
	if len(stderrVal) > 2048+len("[... truncated]") {
		t.Errorf("stderr_tail not truncated: len=%d", len(stderrVal))
	}
	if !strings.HasSuffix(stderrVal, "[... truncated]") {
		t.Errorf("truncation marker missing: %q", stderrVal[max(0, len(stderrVal)-30):])
	}
}

func TestFormatToolResultStackTraceTruncation(t *testing.T) {
	agent, _, _ := newTestAgent()

	longTrace := strings.Repeat("y", 5000)
	r := tool.Failed("crashed", &tool.ToolFailedDetail{
		Code:       "TOOL_EXIT_NONZERO",
		Message:    "crashed",
		StackTrace: longTrace,
	})

	result, _ := agent.formatToolResult(context.Background(), r, &tool.Context{}, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	stackVal, _ := parsed["stack_trace"].(string)
	if len(stackVal) > 4096+len("[... truncated]") {
		t.Errorf("stack_trace not truncated: len=%d", len(stackVal))
	}
	if !strings.HasSuffix(stackVal, "[... truncated]") {
		t.Errorf("truncation marker missing: %q", stackVal[max(0, len(stackVal)-30):])
	}
}

func TestFormatToolResultSTRSpillDoesNotResave(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore}
	agent.svc.(*mockServices).arts = scopedSvc

	toolCtx := &tool.Context{
		ArtifactService: scopedSvc,
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}

	r := &tool.Result{
		Status:  "success",
		Message: "Max result size exceeded. Result stored to artifact \"bulk.txt\" version 0 (50000 bytes).",
		DataObjects: []tool.DataObject{{
			Name:        "bulk.txt",
			MIMEType:    "text/plain",
			Disposition: tool.DispositionArtifact,
			Content:     nil,
			ArtifactRef: &tool.ArtifactRef{Filename: "bulk.txt", Version: 0, SizeBytes: 50000},
			Metadata: map[string]any{
				"spilled_by_str":      true,
				"source_tool":         "bulk_output",
				"original_size_bytes": 50000,
				"reason":              "max_result_size_exceeded",
			},
		}},
	}

	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}

	arts, ok := parsed["artifacts_created"].([]any)
	if !ok || len(arts) != 1 {
		t.Fatalf("artifacts_created missing or wrong length: %v", parsed)
	}
	entry := arts[0].(map[string]any)
	if entry["filename"] != "bulk.txt" {
		t.Errorf("filename = %v, want bulk.txt", entry["filename"])
	}
	if v, _ := entry["version"].(float64); int(v) != 0 {
		t.Errorf("version = %v, want 0", entry["version"])
	}

	keys, err := memStore.ListKeys(context.Background(), artifact.KeyScope{
		AppName: "test-agent", UserID: "user-1", SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("agent re-saved the spilled artifact: store keys = %v", keys)
	}

	if _, hasInline := parsed["inline_outputs"]; hasInline {
		t.Errorf("unexpected inline_outputs field for spilled artifact: %v", parsed["inline_outputs"])
	}
}

func promptSectionText(t *testing.T, prompt, label string) string {
	t.Helper()
	start := strings.Index(prompt, "## "+label+"\n")
	if start < 0 {
		t.Fatalf("prompt has no %q section", label)
	}
	rest := prompt[start+len(label)+4:]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestRedirectDirectiveCallsAnswersUnregisteredBlock(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "c1", Name: "save_artifact", Arguments: `{"filename":"report.md"}`},
		{ID: "c2", Name: "search", Arguments: "{}"},
	}
	none := func(string) bool { return false }

	kept, errs := redirectDirectiveCalls(calls, none, slog.Default())
	if len(kept) != 1 || kept[0].Name != "search" {
		t.Fatalf("kept = %+v, want only the search call", kept)
	}
	if len(errs) != 1 || errs[0].Role != "tool" || errs[0].ToolCallID != "c1" {
		t.Fatalf("corrective messages = %+v, want one tool result for c1", errs)
	}
	if !strings.Contains(errs[0].Content, BlockPrefixSaveArtifact) {
		t.Errorf("corrective message %q does not show the fenced block form", errs[0].Content)
	}
}

func TestRedirectDirectiveCallsKeepsRegisteredTool(t *testing.T) {
	calls := []llm.ToolCall{{ID: "c1", Name: "save_artifact", Arguments: "{}"}}
	registered := func(name string) bool { return name == "save_artifact" }

	kept, errs := redirectDirectiveCalls(calls, registered, slog.Default())
	if len(kept) != 1 || len(errs) != 0 {
		t.Fatalf("kept=%d errs=%d, want the registered tool dispatched untouched", len(kept), len(errs))
	}
}

func TestRedirectDirectiveCallsAnswersInlineEmbed(t *testing.T) {
	calls := []llm.ToolCall{{ID: "c1", Name: "status_update", Arguments: `{"text":"checking"}`}}

	kept, errs := redirectDirectiveCalls(calls, func(string) bool { return false }, slog.Default())
	if len(kept) != 0 || len(errs) != 1 {
		t.Fatalf("kept=%d errs=%d, want the status_update call answered", len(kept), len(errs))
	}
	if !strings.Contains(errs[0].Content, EmbedOpen+"status_update:") {
		t.Errorf("corrective message %q does not show the inline embed form", errs[0].Content)
	}
}
