// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type LLMChatFunc func(ctx context.Context, componentName, model string, req *llm.ChatRequest) (*llm.ChatResponse, error)

const (
	maxChatParamsBytes = 16 << 20

	maxChatMaxTokens = 64000

	ipcCodeLLMFailed     = -32000
	ipcCodeInvalidParams = -32602
)

const (
	chatErrorBudgetExhausted   = "budget_exhausted"
	chatErrorInvalidRequest    = "invalid_request"
	chatErrorUnavailable       = "unavailable"
	chatErrorModelNotPermitted = "model_not_permitted"
)

func (s *IPCServer) SetChat(fn LLMChatFunc) {
	s.llmChat = fn
}

type chatParams struct {
	Messages        []chatMessage `json:"messages"`
	Tools           []chatTool    `json:"tools,omitempty"`
	ToolChoice      string        `json:"tool_choice,omitempty"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	Model           lenientString `json:"model,omitempty"`
}

type lenientString string

func (s *lenientString) UnmarshalJSON(data []byte) error {
	var v string
	if json.Unmarshal(data, &v) == nil {
		*s = lenientString(v)
	}
	return nil
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type chatToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

type chatResult struct {
	Content      string         `json:"content"`
	ToolCalls    []chatToolCall `json:"tool_calls,omitempty"`
	FinishReason string         `json:"finish_reason"`
	Usage        chatUsage      `json:"usage"`
	Model        string         `json:"model,omitempty"`
}

type chatErrorData struct {
	Category string `json:"category"`
}

var validChatRoles = map[string]bool{"system": true, "user": true, "assistant": true, "tool": true}

func (s *IPCServer) handleLLMChat(ctx context.Context, req ipcRequest) *ipcResponse {
	if s.llmChat == nil {
		return chatFailure(req.ID, ipcCodeLLMFailed, chatErrorUnavailable, "LLM chat not available")
	}
	if len(req.Params) > maxChatParamsBytes {
		return chatFailure(req.ID, ipcCodeInvalidParams, chatErrorInvalidRequest,
			fmt.Sprintf("request is %d bytes, over the %d byte limit", len(req.Params), maxChatParamsBytes))
	}
	var params chatParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return chatFailure(req.ID, ipcCodeInvalidParams, chatErrorInvalidRequest, fmt.Sprintf("invalid params: %v", err))
	}
	chatReq, err := params.toLLM()
	if err != nil {
		return chatFailure(req.ID, ipcCodeInvalidParams, chatErrorInvalidRequest, err.Error())
	}

	resp, err := s.llmChat(ctx, s.componentName, string(params.Model), chatReq)
	if err != nil {
		return chatFailure(req.ID, chatErrorCode(err), chatErrorCategory(ctx, err), fmt.Sprintf("LLM chat failed: %v", err))
	}
	return &ipcResponse{JSONRPC: "2.0", ID: req.ID, Result: chatResultFromLLM(resp)}
}

func (p chatParams) toLLM() (*llm.ChatRequest, error) {
	if len(p.Messages) == 0 {
		return nil, errors.New("messages must not be empty")
	}
	req := &llm.ChatRequest{
		ToolChoice:  p.ToolChoice,
		MaxTokens:   min(p.MaxTokens, maxChatMaxTokens),
		Temperature: p.Temperature,
		Messages:    make([]llm.ChatMessage, 0, len(p.Messages)),
	}
	if p.ReasoningEffort != "" {
		req.Reasoning = &llm.ReasoningConfig{Effort: p.ReasoningEffort}
	}
	for i, m := range p.Messages {
		if !validChatRoles[m.Role] {
			return nil, fmt.Errorf("messages[%d]: unknown role %q", i, m.Role)
		}
		msg := llm.ChatMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, ToolName: m.Name}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		req.Messages = append(req.Messages, msg)
	}
	for _, t := range p.Tools {
		if t.Name == "" {
			return nil, errors.New("tools: a tool has no name")
		}
		req.Tools = append(req.Tools, llm.ToolDefinition{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return req, nil
}

func chatResultFromLLM(resp *llm.ChatResponse) chatResult {
	out := chatResult{Content: resp.Content, FinishReason: resp.FinishReason, Model: resp.Model}
	for _, tc := range resp.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, chatToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
	}
	if u := resp.Usage; u != nil {
		out.Usage = chatUsage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      u.TotalTokens,
			CachedTokens:     u.CachedTokens,
		}
	}
	return out
}

func chatErrorCode(err error) int {
	if errors.Is(err, errChatModelNotPermitted) {
		return ipcCodeInvalidParams
	}
	return ipcCodeLLMFailed
}

func chatErrorCategory(ctx context.Context, err error) string {
	if errors.Is(err, errChatModelNotPermitted) {
		return chatErrorModelNotPermitted
	}
	if errors.Is(err, budget.ErrExhausted) {
		return chatErrorBudgetExhausted
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return llm.ErrorCategoryTimeout
	}
	if llmErr, ok := errors.AsType[*llm.Error](err); ok {
		return llmErr.Category()
	}
	return llm.ErrorCategoryUnknown
}

func chatFailure(id string, code int, category, message string) *ipcResponse {
	return &ipcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &ipcErr{Code: code, Message: message, Data: chatErrorData{Category: category}},
	}
}
