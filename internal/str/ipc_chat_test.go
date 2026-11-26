// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type chatWireResponse struct {
	ID     string     `json:"id"`
	Result chatResult `json:"result"`
	Error  *struct {
		Code    int           `json:"code"`
		Message string        `json:"message"`
		Data    chatErrorData `json:"data"`
	} `json:"error"`
}

func serveChat(t *testing.T, chat LLMChatFunc) (send func(params any) chatWireResponse) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ipcchat") //nolint:usetesting // unix socket path must stay under macOS's ~104-byte limit; t.TempDir embeds the test name
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	server, err := NewIPCServer(filepath.Join(dir, "s.sock"), nil, "sam_coder", slog.Default())
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	if chat != nil {
		server.SetChat(chat)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	go server.Serve(ctx)

	conn, err := net.Dial("unix", server.socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	reader := bufio.NewReader(conn)

	var n int
	return func(params any) chatWireResponse {
		n++
		req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": fmt.Sprint(n), "method": "llm/chat", "params": params})
		if _, err := conn.Write(append(req, '\n')); err != nil {
			t.Fatalf("write: %v", err)
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var resp chatWireResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("unmarshal %s: %v", line, err)
		}
		return resp
	}
}

func TestIPCServer_LLMChatRoundTripsToolCalls(t *testing.T) {
	var got *llm.ChatRequest
	var gotComponent, gotModel string
	send := serveChat(t, func(_ context.Context, comp, model string, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		got, gotComponent, gotModel = req, comp, model
		return &llm.ChatResponse{
			ToolCalls:    []llm.ToolCall{{ID: "c2", Name: "read", Arguments: `{"path":"b.go"}`}},
			FinishReason: "tool_calls",
			Model:        "m1",
			Usage:        &llm.Usage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13, CachedTokens: 8},
		}, nil
	})

	resp := send(map[string]any{
		"messages": []map[string]any{
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "fix it"},
			{"role": "assistant", "tool_calls": []map[string]any{{"id": "c1", "name": "read", "arguments": `{"path":"a.go"}`}}},
			{"role": "tool", "tool_call_id": "c1", "name": "read", "content": "1: package a"},
		},
		"tools":      []map[string]any{{"name": "read", "description": "read a file", "parameters": map[string]any{"type": "object"}}},
		"max_tokens": 1 << 20,
		"model":      "opus",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if gotComponent != "sam_coder" || gotModel != "opus" {
		t.Errorf("component, model = %q, %q, want sam_coder, opus", gotComponent, gotModel)
	}
	if len(got.Messages) != 4 || got.Messages[2].ToolCalls[0].ID != "c1" || got.Messages[3].ToolCallID != "c1" || got.Messages[3].ToolName != "read" {
		t.Errorf("messages not carried through: %+v", got.Messages)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "read" {
		t.Errorf("tools not carried through: %+v", got.Tools)
	}
	if got.MaxTokens != maxChatMaxTokens {
		t.Errorf("max_tokens = %d, want clamp to %d", got.MaxTokens, maxChatMaxTokens)
	}
	r := resp.Result
	if r.FinishReason != "tool_calls" || len(r.ToolCalls) != 1 || r.ToolCalls[0].Arguments != `{"path":"b.go"}` {
		t.Errorf("result = %+v", r)
	}
	if r.Usage.CachedTokens != 8 || r.Usage.TotalTokens != 13 || r.Model != "m1" {
		t.Errorf("usage/model = %+v %q", r.Usage, r.Model)
	}
}

func TestIPCServer_LLMChatIgnoresAModelThatIsNotAString(t *testing.T) {
	gotModel := "<not called>"
	send := serveChat(t, func(_ context.Context, _, model string, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
		gotModel = model
		return &llm.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
	})

	resp := send(map[string]any{"messages": []map[string]any{{"role": "user", "content": "hi"}}, "model": 42})

	if resp.Error != nil || gotModel != "" {
		t.Errorf("error %+v, model %q; want the call served with no model named", resp.Error, gotModel)
	}
}

func TestIPCServer_LLMChatErrorCategories(t *testing.T) {
	userMsg := map[string]any{"messages": []map[string]any{{"role": "user", "content": "hi"}}}
	cases := []struct {
		name     string
		chat     LLMChatFunc
		params   any
		code     int
		category string
	}{
		{name: "no chat bound", params: userMsg, code: ipcCodeLLMFailed, category: chatErrorUnavailable},
		{name: "empty messages", chat: okChat, params: map[string]any{"messages": []any{}}, code: ipcCodeInvalidParams, category: chatErrorInvalidRequest},
		{name: "unknown role", chat: okChat, params: map[string]any{"messages": []map[string]any{{"role": "developer"}}}, code: ipcCodeInvalidParams, category: chatErrorInvalidRequest},
		{name: "oversized", chat: okChat, params: map[string]any{"messages": []map[string]any{{"role": "user", "content": strings.Repeat("x", maxChatParamsBytes)}}}, code: ipcCodeInvalidParams, category: chatErrorInvalidRequest},
		{name: "budget", chat: failChat(fmt.Errorf("gate: %w", budget.ErrExhausted)), params: userMsg, code: ipcCodeLLMFailed, category: chatErrorBudgetExhausted},
		{name: "rate limit", chat: failChat(&llm.Error{StatusCode: 429, Message: "slow down"}), params: userMsg, code: ipcCodeLLMFailed, category: llm.ErrorCategoryRateLimit},
		{name: "deadline", chat: failChat(context.DeadlineExceeded), params: userMsg, code: ipcCodeLLMFailed, category: llm.ErrorCategoryTimeout},
		{name: "other", chat: failChat(errors.New("boom")), params: userMsg, code: ipcCodeLLMFailed, category: llm.ErrorCategoryUnknown},
		{name: "model not permitted", chat: failChat(fmt.Errorf("pick: %w", errChatModelNotPermitted)), params: userMsg, code: ipcCodeInvalidParams, category: chatErrorModelNotPermitted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := serveChat(t, tc.chat)(tc.params)
			if resp.Error == nil {
				t.Fatalf("want error, got result %+v", resp.Result)
			}
			if resp.Error.Code != tc.code || resp.Error.Data.Category != tc.category {
				t.Errorf("got code=%d category=%q, want %d %q", resp.Error.Code, resp.Error.Data.Category, tc.code, tc.category)
			}
		})
	}
}

func okChat(context.Context, string, string, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
}

func failChat(err error) LLMChatFunc {
	return func(context.Context, string, string, *llm.ChatRequest) (*llm.ChatResponse, error) { return nil, err }
}

func TestMarkChatCacheBreakpointsTagsLastToolAndMessage(t *testing.T) {
	req := &llm.ChatRequest{
		Tools:    []llm.ToolDefinition{{Name: "a"}, {Name: "b"}},
		Messages: []llm.ChatMessage{{Role: "system"}, {Role: "user"}},
	}
	markChatCacheBreakpoints(req)
	if req.Tools[0].CacheControl != nil || req.Tools[1].CacheControl == nil {
		t.Errorf("tool breakpoints = %v %v", req.Tools[0].CacheControl, req.Tools[1].CacheControl)
	}
	if req.Messages[0].CacheControl != nil || req.Messages[1].CacheControl == nil {
		t.Errorf("message breakpoints = %v %v", req.Messages[0].CacheControl, req.Messages[1].CacheControl)
	}
}
