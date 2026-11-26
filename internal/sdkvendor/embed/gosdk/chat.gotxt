// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ChatMessage is one turn in a multi-turn conversation with the tool's model.
// Role is "system", "user", "assistant" or "tool". An assistant turn that asked
// for tools carries ToolCalls; the tool turn answering one sets ToolCallID and
// Name to the call it answers.
type ChatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

// ChatToolCall is a tool call the model asked for. Arguments is the raw JSON
// object the model produced.
type ChatToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatTool declares a function the model may call. Parameters is a JSON Schema
// object.
type ChatTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ChatOptions shapes one Chat call. ToolChoice is "auto" (the default when
// Tools is set), "none" or "required". MaxTokens is capped by the STR.
// ReasoningEffort (low, medium, high) makes the model think before it answers;
// empty leaves thinking off.
//
// Model is experimental and may change or be removed. It names the SAM model
// alias (the alias of a model configuration, not a provider model id) the call
// runs on, the way an agent references a model alias. It takes effect only on
// an STR with the experimental feature on; elsewhere it is ignored and the call
// runs on the tool's model. Empty runs the call on the tool's model. An alias
// that does not resolve, names a decision model, or is outside the aliases the
// toolset permits fails with an *LLMError of category
// ChatErrorModelNotPermitted before any model is called.
type ChatOptions struct {
	Tools           []ChatTool
	ToolChoice      string
	MaxTokens       int
	Temperature     *float64
	ReasoningEffort string
	Model           string
}

type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

type ChatResult struct {
	Content      string         `json:"content"`
	ToolCalls    []ChatToolCall `json:"tool_calls,omitempty"`
	FinishReason string         `json:"finish_reason"`
	Usage        ChatUsage      `json:"usage"`
	Model        string         `json:"model,omitempty"`
}

const (
	ChatErrorBudgetExhausted   = "budget_exhausted"
	ChatErrorContextOverflow   = "context_overflow"
	ChatErrorRateLimit         = "rate_limit"
	ChatErrorTimeout           = "timeout"
	ChatErrorInvalidRequest    = "invalid_request"
	ChatErrorUnavailable       = "unavailable"
	ChatErrorModelNotPermitted = "model_not_permitted"
)

// LLMError is a Chat failure reported by the STR. Category is one of the
// ChatError* constants, or another provider category the STR passes through.
type LLMError struct {
	Code     int
	Category string
	Message  string
}

func (e *LLMError) Error() string {
	if e.Category == "" {
		return fmt.Sprintf("llm chat error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("llm chat error %d (%s): %s", e.Code, e.Category, e.Message)
}

// Chat sends a multi-turn conversation, optionally with tools, to the tool's
// model through the STR (see ChatOptions.Model to name another model alias). The STR meters it against the invocation's budget and
// releases the tool's compute slot while the call is in flight. Failures the
// STR reports come back as *LLMError.
func (tc *ToolContext) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (*ChatResult, error) {
	if tc.chatFn != nil {
		return tc.chatFn(ctx, messages, opts)
	}
	if tc.ipc == nil {
		return nil, &LLMError{Category: ChatErrorUnavailable, Message: "LLM chat not available: no IPC connection to STR"}
	}
	return tc.ipc.Chat(ctx, messages, opts)
}

type chatParams struct {
	Messages        []ChatMessage `json:"messages"`
	Tools           []ChatTool    `json:"tools,omitempty"`
	ToolChoice      string        `json:"tool_choice,omitempty"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	Model           string        `json:"model,omitempty"`
}

func (c *ipcClient) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (*ChatResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultLLMCallTimeout)
		defer cancel()
	}

	raw, err := c.roundTrip(ctx, "llm/chat", chatParams{
		Messages:        messages,
		Tools:           opts.Tools,
		ToolChoice:      opts.ToolChoice,
		MaxTokens:       opts.MaxTokens,
		Temperature:     opts.Temperature,
		ReasoningEffort: opts.ReasoningEffort,
		Model:           opts.Model,
	})
	if rpcErr, ok := errors.AsType[*ipcError](err); ok {
		return nil, rpcErr.toLLMError()
	}
	if err != nil {
		return nil, err
	}

	var result ChatResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling chat result: %w", err)
	}
	return &result, nil
}

func (e *ipcError) toLLMError() *LLMError {
	out := &LLMError{Code: e.Code, Message: e.Message}
	var data struct {
		Category string `json:"category"`
	}
	if len(e.Data) > 0 && json.Unmarshal(e.Data, &data) == nil {
		out.Category = data.Category
	}
	return out
}
