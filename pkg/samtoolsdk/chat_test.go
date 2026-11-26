// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func replyWith(result, rpcErr string) func([]byte, *bufio.Writer) {
	return func(reqLine []byte, w *bufio.Writer) {
		var req struct {
			ID     string     `json:"id"`
			Method string     `json:"method"`
			Params chatParams `json:"params"`
		}
		_ = json.Unmarshal(reqLine, &req)
		if req.Method != "llm/chat" || len(req.Params.Messages) == 0 || req.Params.Tools[0].Name != "read" {
			rpcErr = `{"code":-1,"message":"bad request shape"}`
		}
		line := `{"jsonrpc":"2.0","id":"` + req.ID + `",`
		if rpcErr != "" {
			line += `"error":` + rpcErr + "}"
		} else {
			line += `"result":` + result + "}"
		}
		_, _ = w.WriteString(line + "\n")
		_ = w.Flush()
	}
}

var chatOpts = ChatOptions{Tools: []ChatTool{{Name: "read", Parameters: map[string]any{"type": "object"}}}}

func TestIPCChat_DecodesToolCalls(t *testing.T) {
	c := newFakeIPCServer(t, replyWith(`{"content":"","tool_calls":[{"id":"c1","name":"read","arguments":"{}"}],"finish_reason":"tool_calls","usage":{"total_tokens":7}}`, ""))
	got, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, chatOpts)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.FinishReason != "tool_calls" || len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "c1" || got.Usage.TotalTokens != 7 {
		t.Errorf("got %+v", got)
	}
}

func TestIPCChat_ReturnsCategorizedLLMError(t *testing.T) {
	c := newFakeIPCServer(t, replyWith("", `{"code":-32000,"message":"LLM chat failed: too long","data":{"category":"context_overflow"}}`))
	_, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, chatOpts)
	llmErr, ok := errors.AsType[*LLMError](err)
	if !ok {
		t.Fatalf("want *LLMError, got %T %v", err, err)
	}
	if llmErr.Category != ChatErrorContextOverflow || llmErr.Code != -32000 {
		t.Errorf("got %+v", llmErr)
	}
}

func TestToolContextChat_WithoutIPCIsUnavailable(t *testing.T) {
	_, err := (&ToolContext{}).Chat(context.Background(), nil, ChatOptions{})
	if llmErr, ok := errors.AsType[*LLMError](err); !ok || llmErr.Category != ChatErrorUnavailable {
		t.Errorf("got %v", err)
	}
}

func TestIPCChat_SendsTheModelOnlyWhenOneIsChosen(t *testing.T) {
	for _, model := range []string{"", "opus"} {
		t.Run("model="+model, func(t *testing.T) {
			var sent map[string]json.RawMessage
			c := newFakeIPCServer(t, func(reqLine []byte, w *bufio.Writer) {
				var req struct {
					ID     string                     `json:"id"`
					Params map[string]json.RawMessage `json:"params"`
				}
				_ = json.Unmarshal(reqLine, &req)
				sent = req.Params
				_, _ = w.WriteString(`{"jsonrpc":"2.0","id":"` + req.ID + `","result":{"content":"ok","finish_reason":"stop"}}` + "\n")
				_ = w.Flush()
			})
			if _, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{Model: model}); err != nil {
				t.Fatalf("Chat: %v", err)
			}
			raw, present := sent["model"]
			if model == "" && present {
				t.Errorf("sent model %s, want the field absent so the STR keeps the tool's model", raw)
			}
			if model != "" && string(raw) != `"`+model+`"` {
				t.Errorf("sent model %s, want %q", raw, model)
			}
		})
	}
}
