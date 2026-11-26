// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type twoCallLLM struct {
	mu        sync.Mutex
	callCount int
	first     *llm.ChatResponse
	laterErr  error
}

func (tc *twoCallLLM) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, errors.New("twoCallLLM: ChatCompletion unused — agent always streams")
}

func (tc *twoCallLLM) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	tc.mu.Lock()
	idx := tc.callCount
	tc.callCount++
	tc.mu.Unlock()

	if idx == 0 {
		resp := tc.first
		ch := make(chan llm.StreamChunk, 10)
		go func() {
			defer close(ch)
			if resp.Content != "" {
				ch <- llm.StreamChunk{ContentDelta: resp.Content}
			}
			for i, t := range resp.ToolCalls {
				ch <- llm.StreamChunk{
					ToolCallDeltas: []llm.ToolCallDelta{{
						Index:          i,
						ID:             t.ID,
						Name:           t.Name,
						ArgumentsDelta: t.Arguments,
					}},
				}
			}
			reason := resp.FinishReason
			if reason == "" {
				if len(resp.ToolCalls) > 0 {
					reason = "tool_calls"
				} else {
					reason = "stop"
				}
			}
			ch <- llm.StreamChunk{FinishReason: reason}
		}()
		return ch, nil
	}

	return nil, tc.laterErr
}

func TestTraceIDEndToEndCorrelation(t *testing.T) {
	const wantTraceID = "test-trace-id-correlation-XYZZY"

	var buf syncBuffer
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	llmMock := &twoCallLLM{
		first: &llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Name: "fail_tool", Arguments: `{}`},
			},
		},
		laterErr: errors.New("upstream LLM provider returned 503"),
	}

	agent := New(AgentConfig{
		Name:         "correlation-agent",
		SystemPrompt: "You are a test agent.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})
	svc := newMockServices(llmMock, newMemSessionStore())
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "fail_tool",
		description: "always fails",
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			return tool.Failed("injected tool failure for correlation test", nil), nil
		},
	}); err != nil {
		t.Fatalf("register fail_tool: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-correlation-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("run correlation test")},
		ContextID: "ctx-correlation-1",
	}
	userProps := a2a.UserProperties{
		ClientID: "test-client",
		UserID:   "test-user",
		TraceID:  wantTraceID,
	}

	resp, err := agent.HandleTask(t.Context(), "task-correlation-001", msg, userProps)
	if err != nil {
		t.Fatalf("HandleTask returned unexpected Go error: %v", err)
	}
	if resp == nil {
		t.Fatal("HandleTask returned nil response (task may have paused unexpectedly)")
	}

	lines := parseJSONLogLines(t, buf.Bytes())

	var (
		withTrace   []jsonLogLine
		errorLines  []jsonLogLine
		distinctMsg = map[string]bool{}
	)
	for _, l := range lines {
		if l.TraceID == wantTraceID {
			withTrace = append(withTrace, l)
			distinctMsg[l.Msg] = true
			if l.Level == "ERROR" {
				errorLines = append(errorLines, l)
			}
		}
	}

	if len(withTrace) < 2 {
		t.Errorf("want ≥2 log lines with traceID=%q, got %d\ncaptured output:\n%s",
			wantTraceID, len(withTrace), buf.String())
	}
	if len(distinctMsg) < 2 {
		t.Errorf("want ≥2 distinct msg values carrying traceID=%q — proves more than one hop, got %v",
			wantTraceID, distinctMsg)
	}
	if len(errorLines) == 0 {
		t.Errorf("want ≥1 ERROR line carrying traceID=%q — task-failure path must be correlatable\ncaptured output:\n%s",
			wantTraceID, buf.String())
	}

	foundLLMErr := false
	for _, l := range errorLines {
		if l.Msg == "LLM call failed" {
			foundLLMErr = true
		}
	}
	if !foundLLMErr {
		t.Errorf("no 'LLM call failed' ERROR line found with traceID=%q\nERROR lines: %+v",
			wantTraceID, errorLines)
	}
}

type jsonLogLine struct {
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	TraceID string `json:"traceID"`
}

func parseJSONLogLines(t *testing.T, data []byte) []jsonLogLine {
	t.Helper()
	var out []jsonLogLine
	for _, raw := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var l jsonLogLine
		if err := json.Unmarshal(raw, &l); err != nil {
			continue
		}
		out = append(out, l)
	}
	return out
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
