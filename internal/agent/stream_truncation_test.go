// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const truncationUserMessage = "The AI model's response was cut off before it finished. Please try again."

func requireStreamTruncation(t *testing.T, err *llm.Error) {
	t.Helper()
	if !err.IsStreamTruncated() {
		t.Fatalf("fixture %+v drifted from llm's stream_truncated type", err)
	}
}

type truncatingStreamLLM struct {
	partialText string
	truncation  *llm.Error
}

func (s *truncatingStreamLLM) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
}

func (s *truncatingStreamLLM) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 2)
	ch <- llm.StreamChunk{ContentDelta: s.partialText}
	ch <- llm.StreamChunk{Err: s.truncation}
	close(ch)
	return ch, nil
}

func TestMidStreamTruncationFailsTask(t *testing.T) {
	const taskID = "task-stream-truncated-001"

	var buf syncBuffer
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	truncation := &llm.Error{
		Type:     "stream_truncated",
		Provider: "anthropic",
		Message:  "stream ended without a finish reason",
	}
	requireStreamTruncation(t, truncation)
	llmMock := &truncatingStreamLLM{
		partialText: "Here is the start of an answer that ",
		truncation:  truncation,
	}

	agent := New(AgentConfig{
		Name:         "truncation-agent",
		SystemPrompt: "You are a test agent.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})
	if err := agent.Init(t.Context(), nil, newMockServices(llmMock, newMemSessionStore())); err != nil {
		t.Fatalf("Init: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-stream-truncated-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("answer me")},
		ContextID: "ctx-stream-truncated-1",
	}
	resp, err := agent.HandleTask(t.Context(), taskID, msg, a2a.UserProperties{ClientID: "test-client", UserID: "test-user"})
	if err != nil {
		t.Fatalf("HandleTask returned unexpected Go error: %v", err)
	}
	if resp == nil {
		t.Fatal("HandleTask returned nil response")
	}
	if resp.Error == nil {
		t.Fatalf("truncated stream completed the task instead of failing it: result=%+v", resp.Result)
	}
	if resp.Error.Code != -32603 {
		t.Errorf("error code = %d, want -32603", resp.Error.Code)
	}
	if resp.Error.Message != truncationUserMessage {
		t.Errorf("error message = %q, want %q", resp.Error.Message, truncationUserMessage)
	}
	data, _ := resp.Error.Data.(map[string]any)
	if got := data[a2a.ErrorDataLLMErrorCategory]; got != llm.ErrorCategoryUnknown {
		t.Errorf("error data %q = %v, want %q: %+v", a2a.ErrorDataLLMErrorCategory, got, llm.ErrorCategoryUnknown, resp.Error.Data)
	}

	failures := llmCallFailedRecords(buf.Bytes(), taskID)
	if len(failures) != 1 {
		t.Fatalf("want exactly 1 'LLM call failed' ERROR record for %s, got %d\ncaptured:\n%s",
			taskID, len(failures), buf.String())
	}
	for key, want := range map[string]string{
		"errorType": "stream_truncated",
		"provider":  "anthropic",
	} {
		if got := failures[0][key]; got != want {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}
}

func llmCallFailedRecords(logOutput []byte, taskID string) []map[string]any {
	return logRecords(logOutput, taskID, "ERROR", "LLM call failed")
}

func logRecords(logOutput []byte, taskID, level, msg string) []map[string]any {
	var out []map[string]any
	for raw := range bytes.SplitSeq(logOutput, []byte("\n")) {
		var rec map[string]any
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		if rec["level"] == level && rec["msg"] == msg && rec["taskID"] == taskID {
			out = append(out, rec)
		}
	}
	return out
}

type scriptedStreamLLM struct {
	turns       [][]llm.StreamChunk
	beforeFinal func()

	mu         sync.Mutex
	calls      int
	requests   [][]llm.ChatMessage
	overrides  []*llm.BifrostConfig
	reasonings []*llm.ReasoningConfig
}

func (s *scriptedStreamLLM) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
}

func (s *scriptedStreamLLM) ChatCompletionStream(_ context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := s.calls
	s.calls++
	s.requests = append(s.requests, slices.Clone(req.Messages))
	s.overrides = append(s.overrides, req.ModelOverride)
	s.reasonings = append(s.reasonings, req.Reasoning)
	if call >= len(s.turns) {
		return nil, fmt.Errorf("unexpected LLM stream call %d: only %d scripted", call+1, len(s.turns))
	}
	if call == len(s.turns)-1 && s.beforeFinal != nil {
		s.beforeFinal()
	}
	ch := make(chan llm.StreamChunk, len(s.turns[call]))
	for _, chunk := range s.turns[call] {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (s *scriptedStreamLLM) request(t *testing.T, call int) []llm.ChatMessage {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if call >= len(s.requests) {
		t.Fatalf("request(%d): only %d LLM stream calls were made", call, len(s.requests))
	}
	return s.requests[call]
}

func (s *scriptedStreamLLM) override(t *testing.T, call int) *llm.BifrostConfig {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if call >= len(s.overrides) {
		t.Fatalf("override(%d): only %d LLM stream calls were made", call, len(s.overrides))
	}
	return s.overrides[call]
}

func (s *scriptedStreamLLM) reasoning(t *testing.T, call int) *llm.ReasoningConfig {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if call >= len(s.reasonings) {
		t.Fatalf("reasoning(%d): only %d LLM stream calls were made", call, len(s.reasonings))
	}
	return s.reasonings[call]
}

func (s *scriptedStreamLLM) streamCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

var (
	streamTruncation = &llm.Error{
		Type:     "stream_truncated",
		Provider: "anthropic",
		Message:  "stream ended without a finish reason",
	}
	outputWithoutResultEmbed = []llm.StreamChunk{
		{ContentDelta: "Here is my answer, but no result embed."},
		{FinishReason: "stop"},
	}
	truncatedTurn = []llm.StreamChunk{
		{ContentDelta: "Retrying with the result "},
		{Err: streamTruncation},
	}
)

func runStructuredInvocation(ctx context.Context, t *testing.T, taskID string, llmMock llm.Client) (*a2a.Response, []byte) {
	t.Helper()
	return runStructuredInvocationWithBudget(ctx, t, taskID, llmMock, 1)
}

func runStructuredInvocationWithBudget(ctx context.Context, t *testing.T, taskID string, llmMock llm.Client, maxLLMCalls int, setup ...func(*Agent)) (*a2a.Response, []byte) {
	t.Helper()
	return runStructuredInvocationWithMetadata(ctx, t, taskID, llmMock, maxLLMCalls, nil, setup...)
}

func runStructuredInvocationWithMetadata(ctx context.Context, t *testing.T, taskID string, llmMock llm.Client, maxLLMCalls int, metadata map[string]any, setup ...func(*Agent)) (*a2a.Response, []byte) {
	t.Helper()
	return runStructuredInvocationAs(ctx, t, taskID, llmMock, maxLLMCalls, metadata, a2a.UserProperties{ClientID: "test-client", UserID: "test-user"}, setup...)
}

func runStructuredInvocationAs(ctx context.Context, t *testing.T, taskID string, llmMock llm.Client, maxLLMCalls int, metadata map[string]any, userProps a2a.UserProperties, setup ...func(*Agent)) (*a2a.Response, []byte) {
	t.Helper()

	var buf syncBuffer
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	agent := New(AgentConfig{
		Name:         "structured-truncation-agent",
		SystemPrompt: "You are a test agent.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  maxLLMCalls,
		Namespace:    "test-ns",
	})
	svc := newMockServices(llmMock, newMemSessionStore())
	svc.arts = &artifact.ScopedService{Inner: artmem.New()}
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, fn := range setup {
		fn(agent)
	}
	loc := svc.arts.Resolve(agent.cfg.Name, userProps.UserID, "ctx-"+taskID, "summary.json")
	if _, err := svc.arts.Inner.Save(t.Context(), loc, []byte(`{"summary": "ok"}`), "application/json"); err != nil {
		t.Fatalf("seed summary.json: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-" + taskID,
		Role:      a2a.RoleUser,
		ContextID: "ctx-" + taskID,
		Metadata:  metadata,
		Parts: []a2a.Part{
			a2a.NewTextPart("summarize the quarter"),
			a2a.NewDataPart(map[string]any{
				"type":          "structured_invocation_request",
				"workflow_name": "wf",
				"node_id":       "step_1",
			}),
		},
	}
	resp, err := agent.HandleTask(ctx, taskID, msg, userProps)
	if err != nil {
		t.Fatalf("HandleTask returned unexpected Go error: %v", err)
	}
	if resp == nil {
		t.Fatal("HandleTask returned nil response")
	}
	return resp, buf.Bytes()
}

func structuredResultData(t *testing.T, resp *a2a.Response) (a2a.TaskState, map[string]any) {
	t.Helper()
	task, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("response result is %T, want a2a.Task (error=%+v)", resp.Result, resp.Error)
	}
	if task.Status.Message == nil || len(task.Status.Message.Parts) == 0 {
		t.Fatalf("structured response carries no message parts: %+v", task.Status)
	}
	data, ok := task.Status.Message.Parts[0].Data.(map[string]any)
	if !ok || data["type"] != "structured_invocation_result" {
		t.Fatalf("first part is not a structured_invocation_result: %+v", task.Status.Message.Parts[0])
	}
	return task.Status.State, data
}

func TestStructuredInvocationStreamTruncationFailsTask(t *testing.T) {
	const wantRetry = 1
	requireStreamTruncation(t, streamTruncation)

	artifactTurn := []llm.StreamChunk{
		{ContentDelta: "«««save_artifact: filename=\"summary.json\" mime_type=\"application/json\"\n{\"summary\": \"ok\"}\n»»»\n"},
		{FinishReason: "stop"},
	}

	cases := []struct {
		name  string
		turns [][]llm.StreamChunk
	}{
		{
			name:  "validation retry stream",
			turns: [][]llm.StreamChunk{outputWithoutResultEmbed, truncatedTurn},
		},
		{
			name:  "artifact notification follow-up stream",
			turns: [][]llm.StreamChunk{outputWithoutResultEmbed, artifactTurn, truncatedTurn},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskID := "task-si-truncated-" + strings.ReplaceAll(tc.name, " ", "-")
			llmMock := &scriptedStreamLLM{turns: tc.turns}

			resp, logs := runStructuredInvocation(t.Context(), t, taskID, llmMock)

			if got := llmMock.streamCalls(); got != len(tc.turns) {
				t.Errorf("LLM stream calls = %d, want %d", got, len(tc.turns))
			}
			state, data := structuredResultData(t, resp)
			if state != a2a.TaskStateFailed {
				t.Errorf("task state = %q, want %q", state, a2a.TaskStateFailed)
			}
			if data["status"] != "error" {
				t.Errorf("result status = %v, want error: %+v", data["status"], data)
			}
			if data["error_message"] != truncationUserMessage {
				t.Errorf("error_message = %q, want %q", data["error_message"], truncationUserMessage)
			}
			if data["retry_count"] != wantRetry {
				t.Errorf("retry_count = %v, want %d", data["retry_count"], wantRetry)
			}

			failures := llmCallFailedRecords(logs, taskID)
			if len(failures) != 1 {
				t.Fatalf("want exactly 1 'LLM call failed' ERROR record for %s, got %d\ncaptured:\n%s",
					taskID, len(failures), logs)
			}
			if got := failures[0]["errorType"]; got != "stream_truncated" {
				t.Errorf("errorType = %v, want stream_truncated", got)
			}
			if got := failures[0]["retry"]; got != float64(wantRetry) {
				t.Errorf("retry = %v, want %d", got, wantRetry)
			}
		})
	}
}

func TestStructuredInvocationMalformedBlockLogsWarnNotLLMFailure(t *testing.T) {
	const taskID = "task-si-malformed-block"

	unterminatedArtifact := []llm.StreamChunk{
		{ContentDelta: "«««save_artifact: filename=\"summary.json\" mime_type=\"application/json\"\n{\"summary\": \"ok\"}\n"},
		{FinishReason: "stop"},
	}
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{outputWithoutResultEmbed, unterminatedArtifact}}

	resp, logs := runStructuredInvocation(t.Context(), t, taskID, llmMock)

	if _, data := structuredResultData(t, resp); data["status"] != "error" {
		t.Errorf("result status = %v, want error: %+v", data["status"], data)
	}
	if failures := llmCallFailedRecords(logs, taskID); len(failures) != 0 {
		t.Errorf("malformed block logged %d 'LLM call failed' ERROR records, want 0", len(failures))
	}
	warns := logRecords(logs, taskID, "WARN", "block validation error, structured invocation not retried")
	if len(warns) != 1 {
		t.Fatalf("want exactly 1 block-validation WARN record for %s, got %d\ncaptured:\n%s", taskID, len(warns), logs)
	}
	if got := warns[0]["filename"]; got != "summary.json" {
		t.Errorf("filename = %v, want summary.json", got)
	}
}

func TestStructuredInvocationCancelledRetryStreamLogsNoFailure(t *testing.T) {
	const taskID = "task-si-truncated-cancelled"

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	llmMock := &scriptedStreamLLM{
		turns:       [][]llm.StreamChunk{outputWithoutResultEmbed, truncatedTurn},
		beforeFinal: cancel,
	}

	resp, logs := runStructuredInvocation(ctx, t, taskID, llmMock)

	if got := llmMock.streamCalls(); got != 2 {
		t.Errorf("LLM stream calls = %d, want 2", got)
	}
	if _, data := structuredResultData(t, resp); data["status"] != "error" {
		t.Errorf("result status = %v, want error: %+v", data["status"], data)
	}
	if failures := llmCallFailedRecords(logs, taskID); len(failures) != 0 {
		t.Errorf("cancelled task logged %d 'LLM call failed' ERROR records, want 0:\n%+v", len(failures), failures)
	}
}
