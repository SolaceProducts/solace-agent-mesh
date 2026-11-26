// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const missingResultEmbedFeedback = "ERROR: You failed to provide the mandatory result embed"

var outputWithResultEmbed = []llm.StreamChunk{
	{ContentDelta: "Done. «result:artifact=summary.json:0 status=success»"},
	{FinishReason: "stop"},
}

func countMessages(msgs []llm.ChatMessage, role, contentPrefix string) int {
	n := 0
	for _, m := range msgs {
		if m.Role == role && strings.HasPrefix(m.Content, contentPrefix) {
			n++
		}
	}
	return n
}

func TestStructuredInvocationSendsQueuedRetryOnce(t *testing.T) {
	const taskID = "task-si-queued-retry"
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{outputWithoutResultEmbed, outputWithResultEmbed}}

	resp, _ := runStructuredInvocation(t.Context(), t, taskID, llmMock)

	if got := llmMock.streamCalls(); got != 2 {
		t.Fatalf("LLM stream calls = %d, want 2", got)
	}
	state, data := structuredResultData(t, resp)
	if state != a2a.TaskStateCompleted || data["status"] != "success" {
		t.Errorf("task state = %q, result status = %v, want completed/success: %+v", state, data["status"], data)
	}
	if data["retry_count"] != 1 {
		t.Errorf("retry_count = %v, want 1: one retry call was made", data["retry_count"])
	}

	retry := llmMock.request(t, 1)
	rejected := outputWithoutResultEmbed[0].ContentDelta
	if n := countMessages(retry, "assistant", rejected); n != 1 {
		t.Errorf("retry request carries the rejected turn %d times, want 1", n)
	}
	if n := countMessages(retry, "user", missingResultEmbedFeedback); n != 1 {
		t.Errorf("retry request carries the retry feedback %d times, want 1", n)
	}
}

func TestStructuredInvocationArtifactFollowUpKeepsTheModelOverride(t *testing.T) {
	artifactTurn := []llm.StreamChunk{
		{ContentDelta: "«««save_artifact: filename=\"summary.json\" mime_type=\"application/json\"\n{\"summary\": \"ok\"}\n»»»\n"},
		{FinishReason: "stop"},
	}
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{outputWithoutResultEmbed, artifactTurn, outputWithResultEmbed}}
	metadata := map[string]any{"model_override": map[string]any{"model_id": "sonnet-alias"}}

	resp, _ := runStructuredInvocationWithMetadata(t.Context(), t, "task-si-followup-override", llmMock, 1, metadata, func(a *Agent) {
		a.svc.(*mockServices).modelResolver = &stubResolver{resp: map[string]any{"model": "anthropic/claude-sonnet-4-5"}}
	})

	if got := llmMock.streamCalls(); got != 3 {
		t.Fatalf("LLM stream calls = %d, want 3 (main, retry, artifact follow-up)", got)
	}
	for call := range 3 {
		if o := llmMock.override(t, call); o == nil || o.Model != "claude-sonnet-4-5" {
			t.Errorf("call %d ModelOverride = %+v, want the task's override", call, o)
		}
	}
	if _, data := structuredResultData(t, resp); data["status"] != "success" {
		t.Errorf("result status = %v, want success: %+v", data["status"], data)
	}
}

var artifactSaveStreamTurn = []llm.StreamChunk{
	{ContentDelta: "«««save_artifact: filename=\"summary.json\" mime_type=\"application/json\"\n{\"summary\": \"ok\"}\n»»»\n"},
	{FinishReason: "stop"},
}

func TestStructuredInvocationRetryAndFollowUpKeepReasoningAndCaching(t *testing.T) {
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{outputWithoutResultEmbed, artifactSaveStreamTurn, outputWithResultEmbed}}

	runStructuredInvocationWithMetadata(t.Context(), t, "task-si-reasoning-cache", llmMock, 1, nil, func(a *Agent) {
		a.cfg.ReasoningEffort = "high"
		a.cfg.CacheStrategy = "5m"
	})

	if got := llmMock.streamCalls(); got != 3 {
		t.Fatalf("LLM stream calls = %d, want 3 (main, retry, artifact follow-up)", got)
	}
	for call := range 3 {
		if r := llmMock.reasoning(t, call); r == nil || r.Effort != "high" {
			t.Errorf("call %d Reasoning = %+v, want effort high", call, r)
		}
		msgs := llmMock.request(t, call)
		lastCacheable := len(msgs) - 1
		for lastCacheable > 0 && msgs[lastCacheable].NoCacheBreakpoint {
			lastCacheable--
		}
		if last := msgs[lastCacheable]; last.CacheControl == nil {
			t.Errorf("call %d: last cacheable message (%s) carries no cache breakpoint", call, last.Role)
		}
	}
}

func TestStructuredInvocationRetryNeverSendsAnEmptyTurn(t *testing.T) {
	whitespaceOnly := []llm.StreamChunk{{ContentDelta: "  \n "}, {FinishReason: "stop"}}

	cases := []struct {
		name  string
		turns [][]llm.StreamChunk
	}{
		{name: "rejected by the main loop", turns: [][]llm.StreamChunk{whitespaceOnly, outputWithResultEmbed}},
		{name: "rejected by the finalizer", turns: [][]llm.StreamChunk{outputWithoutResultEmbed, whitespaceOnly, outputWithResultEmbed}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskID := "task-si-empty-turn-" + strings.ReplaceAll(tc.name, " ", "-")
			llmMock := &scriptedStreamLLM{turns: tc.turns}

			resp, _ := runStructuredInvocation(t.Context(), t, taskID, llmMock)

			lastCall := len(tc.turns) - 1
			if got := llmMock.streamCalls(); got != len(tc.turns) {
				t.Fatalf("LLM stream calls = %d, want %d", got, len(tc.turns))
			}
			if _, data := structuredResultData(t, resp); data["status"] != "success" {
				t.Errorf("result status = %v, want success: %+v", data["status"], data)
			}
			retry := llmMock.request(t, lastCall)
			if n := countMessages(retry, "assistant", "(The agent produced the artifact but did not include a text response.)"); n != 1 {
				t.Errorf("retry request carries the empty-turn placeholder %d times, want 1", n)
			}
			for _, m := range retry {
				if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
					t.Errorf("retry request carries an empty assistant turn: %+v", m)
				}
			}
		})
	}
}

func TestStructuredInvocationFinalizerKeepsFullRetryBudget(t *testing.T) {
	const taskID = "task-si-full-retry-budget"
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{
		outputWithoutResultEmbed,
		outputWithoutResultEmbed,
		outputWithResultEmbed,
	}}

	resp, _ := runStructuredInvocation(t.Context(), t, taskID, llmMock)

	if got := llmMock.streamCalls(); got != 3 {
		t.Fatalf("LLM stream calls = %d, want 3: the initial turn plus MaxRetries=2 retries", got)
	}
	_, data := structuredResultData(t, resp)
	if data["status"] != "success" {
		t.Errorf("result status = %v, want success: %+v", data["status"], data)
	}
	if data["retry_count"] != 2 {
		t.Errorf("retry_count = %v, want 2", data["retry_count"])
	}
}

func TestStructuredInvocationAnsweredRetryIsJudgedAgain(t *testing.T) {
	const taskID = "task-si-answered-retry"
	toolCallTurn := []llm.StreamChunk{
		{ContentDelta: "Let me look that up."},
		{ToolCallDeltas: []llm.ToolCallDelta{{Index: 0, ID: "call-1", Name: "lookup_status", ArgumentsDelta: "{}"}}},
		{FinishReason: "tool_calls"},
	}
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{outputWithoutResultEmbed, toolCallTurn, outputWithResultEmbed}}

	resp, _ := runStructuredInvocationWithBudget(t.Context(), t, taskID, llmMock, 2)

	if got := llmMock.streamCalls(); got != 3 {
		t.Fatalf("LLM stream calls = %d, want 3", got)
	}
	_, data := structuredResultData(t, resp)
	if data["status"] != "success" {
		t.Errorf("result status = %v, want success: %+v", data["status"], data)
	}
	if data["retry_count"] != 2 {
		t.Errorf("retry_count = %v, want 2: the answered retry was judged and retried once more", data["retry_count"])
	}
	if n := countMessages(llmMock.request(t, 2), "user", missingResultEmbedFeedback); n != 2 {
		t.Errorf("finalizer request carries the retry feedback %d times, want 2 (one per real retry)", n)
	}
}

func TestStructuredInvocationMalformedBlockAnswersPendingRetry(t *testing.T) {
	const taskID = "task-si-malformed-block-answer"
	unterminatedArtifact := []llm.StreamChunk{
		{ContentDelta: "«««save_artifact: filename=\"summary.json\" mime_type=\"application/json\"\n{\"summary\": \"ok\"}\n"},
		{FinishReason: "stop"},
	}
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{
		outputWithoutResultEmbed,
		unterminatedArtifact,
		outputWithoutResultEmbed,
		outputWithResultEmbed,
	}}

	resp, logs := runStructuredInvocationWithBudget(t.Context(), t, taskID, llmMock, 2)

	if n := len(logRecords(logs, taskID, "WARN", "block validation error, retrying")); n != 1 {
		t.Fatalf("block-validation soft retries = %d, want 1: turn 2 must take the malformed-block path", n)
	}
	if got := llmMock.streamCalls(); got != 3 {
		t.Fatalf("LLM stream calls = %d, want 3: the initial turn plus MaxRetries=2 retries", got)
	}
	_, data := structuredResultData(t, resp)
	if data["status"] != "error" {
		t.Errorf("result status = %v, want error once retries are spent: %+v", data["status"], data)
	}
	if data["retry_count"] != 2 {
		t.Errorf("retry_count = %v, want 2", data["retry_count"])
	}
}
