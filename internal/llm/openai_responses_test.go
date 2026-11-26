// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

type recordedCall struct {
	path string
	body map[string]any
}

type fakeOpenAI struct {
	responsesJSON       string
	responsesSSE        []string
	responsesSSEDone    bool
	responsesStatus     int
	responsesMessage    string
	chatRejection       string
	toolSearchRejection string

	mu    sync.Mutex
	calls []recordedCall
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{path: r.URL.Path, body: body})
	f.mu.Unlock()

	switch {
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		f.serveChat(w, body)
	case strings.HasSuffix(r.URL.Path, "/responses"):
		f.serveResponses(w, body)
	default:
		http.NotFound(w, r)
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"message": message,
		"type":    "invalid_request_error",
		"code":    nil,
	}})
}

func (f *fakeOpenAI) serveChat(w http.ResponseWriter, body map[string]any) {
	model, _ := body["model"].(string)
	effort, _ := body["reasoning_effort"].(string)
	tools, _ := body["tools"].([]any)
	if rejection := cmp.Or(f.chatRejection, openAIChatRejection(model, effort, len(tools) > 0)); rejection != "" {
		writeOpenAIError(w, http.StatusBadRequest, rejection)
		return
	}
	if stream, _ := body["stream"].(bool); stream {
		streamHandler([]string{"chat ", "reply"})(w, nil)
		return
	}
	nonStreamHandler("chat reply", "stop")(w, nil)
}

func openAIChatRejection(model, effort string, hasTools bool) string {
	switch {
	case strings.HasPrefix(model, "gpt-5-pro"):
		return "This model is only supported in v1/responses and not in v1/chat/completions."
	case !hasTools || effort == "none":
		return ""
	case strings.HasPrefix(model, "gpt-5.6"), strings.HasPrefix(model, "gpt-6"),
		model == "gpt-5.4-mini" && effort != "":
		return "Function tools with reasoning_effort are not supported for " + model +
			" in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'."
	}
	return ""
}

func (f *fakeOpenAI) serveResponses(w http.ResponseWriter, body map[string]any) {
	if f.toolSearchRejection != "" && defersATool(body) {
		writeOpenAIError(w, http.StatusBadRequest, f.toolSearchRejection)
		return
	}
	if f.responsesStatus != 0 {
		writeOpenAIError(w, f.responsesStatus, cmp.Or(f.responsesMessage, http.StatusText(f.responsesStatus)))
		return
	}
	if stream, _ := body["stream"].(bool); stream {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range f.responsesSSE {
			var typed struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(ev), &typed)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typed.Type, ev)
		}
		if f.responsesSSEDone {
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, f.responsesJSON)
}

func defersATool(body map[string]any) bool {
	tools, _ := body["tools"].([]any)
	return slices.ContainsFunc(tools, func(tl any) bool {
		m, _ := tl.(map[string]any)
		return m["defer_loading"] == true
	})
}

func (f *fakeOpenAI) recorded() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeOpenAI) paths() []string {
	var out []string
	for _, c := range f.recorded() {
		out = append(out, c.path)
	}
	return out
}

func (f *fakeOpenAI) responsesBody(t *testing.T) map[string]any {
	t.Helper()
	bodies := responsesBodies(f)
	if len(bodies) != 1 {
		t.Fatalf("got %d /v1/responses calls, want 1: %v", len(bodies), f.paths())
	}
	return bodies[0]
}

func (f *fakeOpenAI) countCalls(pathSuffix string) int {
	n := 0
	for _, path := range f.paths() {
		if strings.HasSuffix(path, pathSuffix) {
			n++
		}
	}
	return n
}

func (f *fakeOpenAI) onlyCall(t *testing.T, wantPathSuffix string) map[string]any {
	t.Helper()
	calls := f.recorded()
	if len(calls) != 1 {
		t.Fatalf("got %d upstream calls, want 1: %+v", len(calls), calls)
	}
	if !strings.HasSuffix(calls[0].path, wantPathSuffix) {
		t.Fatalf("request went to %s, want %s", calls[0].path, wantPathSuffix)
	}
	return calls[0].body
}

func newFakeOpenAIClient(t *testing.T, f *fakeOpenAI, model string) *BifrostClient {
	t.Helper()
	return newFakeOpenAIClientWithConfig(t, f, BifrostConfig{Model: model})
}

func newFakeOpenAIClientWithConfig(t *testing.T, f *fakeOpenAI, cfg BifrostConfig) *BifrostClient {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key-123")
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg.Provider, cfg.BaseURL = "openai", srv.URL
	client, err := NewBifrostClient(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

var getTimeTool = ToolDefinition{
	Name:        "get_time",
	Description: "Get the current time",
	Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{"tz": map[string]any{"type": "string"}},
		"required":   []any{"tz"},
	},
}

func toolReplayConversation() []ChatMessage {
	return []ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "What time is it in UTC?"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "get_time", Arguments: `{"tz":"UTC"}`}}},
		{Role: "tool", ToolCallID: "call_1", ToolName: "get_time", ContentBlocks: []ContentBlock{{Type: "text", Text: `{"time":"12:00"}`}}},
	}
}

func toolsWithReasoningRequest() *ChatRequest {
	return &ChatRequest{
		Messages:  toolReplayConversation(),
		Tools:     []ToolDefinition{getTimeTool},
		Reasoning: &ReasoningConfig{Effort: "low"},
	}
}

const completedResponseJSON = `{
  "id": "resp_1", "object": "response", "created_at": 1, "model": "gpt-5.6-terra", "status": "completed",
  "output": [
    {"id": "rs_1", "type": "reasoning", "summary": [{"type": "summary_text", "text": "checking the clock"}]},
    {"id": "msg_1", "type": "message", "role": "assistant", "status": "completed",
     "content": [{"type": "output_text", "text": "Checking CET too.", "annotations": []}]},
    {"id": "fc_1", "type": "function_call", "status": "completed", "call_id": "call_2", "name": "get_time", "arguments": "{\"tz\":\"CET\"}"}
  ],
  "usage": {"input_tokens": 100, "input_tokens_details": {"cached_tokens": 40},
            "output_tokens": 20, "output_tokens_details": {"reasoning_tokens": 8}, "total_tokens": 120}
}`

const textOnlyResponseJSON = `{
  "id": "resp_1", "object": "response", "created_at": 1, "model": "gpt-5.6-terra", "status": "completed",
  "output": [
    {"id": "msg_1", "type": "message", "role": "assistant", "status": "completed",
     "content": [{"type": "output_text", "text": "It is 12:00 UTC.", "annotations": []}]}
  ]
}`

func TestResponsesAPI_ToolsWithReasoningBudget(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	resp, err := client.ChatCompletion(t.Context(), &ChatRequest{
		Messages:  toolReplayConversation(),
		Tools:     []ToolDefinition{getTimeTool},
		MaxTokens: 4096,
		Reasoning: &ReasoningConfig{BudgetTokens: 2048},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	body := fake.responsesBody(t)
	if _, sent := body["reasoning_effort"]; sent {
		t.Error("reasoning_effort is a chat-completions field and must not reach /v1/responses")
	}
	if effort := reasoningEffortIn(body); effort == "" {
		t.Errorf("reasoning.effort missing from body: %v", body["reasoning"])
	}
	if store, ok := body["store"].(bool); !ok || store {
		t.Errorf("store = %v, want false", body["store"])
	}
	assertReplayedItemsCarryNoIDs(t, body)

	if resp.Content != "Checking CET too." {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.ReasoningContent != "checking the clock" {
		t.Errorf("ReasoningContent = %q", resp.ReasoningContent)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0] != (ToolCall{ID: "call_2", Name: "get_time", Arguments: `{"tz":"CET"}`}) {
		t.Errorf("ToolCalls = %+v", resp.ToolCalls)
	}
	if resp.FinishReason != finishReasonToolCalls {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, finishReasonToolCalls)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 100 || resp.Usage.CompletionTokens != 20 ||
		resp.Usage.CachedTokens != 40 || resp.Usage.ReasoningTokens != 8 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func reasoningEffortIn(body map[string]any) string {
	reasoning, _ := body["reasoning"].(map[string]any)
	effort, _ := reasoning["effort"].(string)
	return effort
}

func assertReplayedItemsCarryNoIDs(t *testing.T, body map[string]any) {
	t.Helper()
	input, _ := body["input"].([]any)
	var sawCall, sawOutput bool
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if id, ok := item["id"]; ok {
			t.Errorf("input item %v carries id %v; OpenAI rejects ids it did not issue", item["type"], id)
		}
		switch item["type"] {
		case "function_call":
			sawCall = item["call_id"] == "call_1"
		case "function_call_output":
			sawOutput = item["call_id"] == "call_1"
			if item["output"] != `{"time":"12:00"}` {
				t.Errorf("function_call_output.output = %#v, want the text result as a plain string", item["output"])
			}
		}
	}
	if !sawCall || !sawOutput {
		t.Errorf("replayed function_call/function_call_output with call_id call_1 missing from input: %v", input)
	}
}

func TestResponsesAPI_BudgetMapsToEffortAgainstTheOutputCap(t *testing.T) {
	for _, tc := range []struct {
		maxTokens int
		want      string
	}{
		{4096, "medium"},
		{2048, "high"},
		{100000, "low"},
	} {
		t.Run(fmt.Sprint(tc.maxTokens), func(t *testing.T) {
			fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
			client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

			if _, err := client.ChatCompletion(t.Context(), &ChatRequest{
				Messages:  toolReplayConversation(),
				Tools:     []ToolDefinition{getTimeTool},
				MaxTokens: tc.maxTokens,
				Reasoning: &ReasoningConfig{BudgetTokens: 2048},
			}); err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}

			if got := reasoningEffortIn(fake.responsesBody(t)); got != tc.want {
				t.Errorf("reasoning.effort = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResponsesAPI_ReplaysAssistantTextBeforeItsToolCalls(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")
	const said = "I will look up the UTC time first."
	req := toolsWithReasoningRequest()
	req.Messages[2].Content = said

	if _, err := client.ChatCompletion(t.Context(), req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	input, _ := fake.responsesBody(t)["input"].([]any)
	textAt, callAt := -1, -1
	for i, raw := range input {
		item, _ := raw.(map[string]any)
		switch {
		case item["type"] == "message" && item["role"] == "assistant" && strings.Contains(fmt.Sprint(item["content"]), said):
			textAt = i
		case item["type"] == "function_call" && item["call_id"] == "call_1":
			callAt = i
		}
	}
	if textAt < 0 || callAt < 0 || textAt > callAt {
		t.Errorf("want the assistant text replayed before its function_call; text at %d, call at %d in %v", textAt, callAt, input)
	}
}

func TestResponsesAPI_KeepsReasoningForGPT6(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClient(t, fake, "gpt-6-luna")

	if _, err := client.ChatCompletion(t.Context(), &ChatRequest{
		Messages:  toolReplayConversation(),
		Tools:     []ToolDefinition{getTimeTool},
		Reasoning: &ReasoningConfig{Effort: "high"},
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	if got := reasoningEffortIn(fake.responsesBody(t)); got != "high" {
		t.Errorf("reasoning.effort = %q, want high", got)
	}
}

func TestResponsesAPI_TranslatesChatExtraParams(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	if _, err := client.ChatCompletion(t.Context(), &ChatRequest{
		Messages: toolReplayConversation(),
		Tools:    []ToolDefinition{getTimeTool},
		ExtraParams: map[string]any{
			"reasoning_effort":      "low",
			"top_p":                 0.5,
			"service_tier":          "flex",
			"max_completion_tokens": 900.0,
			"response_format":       map[string]any{"type": "json_object"},
			"frequency_penalty":     0.1,
			"presence_penalty":      0.2,
			"seed":                  7.0,
			"logit_bias":            map[string]any{"50256": -100.0},
			"logprobs":              true,
			"n":                     1.0,
			"verbosity":             "low",
			"stream_options":        map[string]any{"include_usage": true},
			"tool_choice":           map[string]any{"type": "function", "function": map[string]any{"name": "get_time"}},
			"modalities":            []any{"text"},
			"web_search_options":    map[string]any{},
			"audio":                 map[string]any{"voice": "alloy", "format": "wav"},
			"prediction":            map[string]any{"type": "content", "content": "x"},
			"function_call":         "auto",
		},
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	body := fake.responsesBody(t)
	for _, chatOnly := range []string{"reasoning_effort", "max_completion_tokens", "response_format", "verbosity",
		"frequency_penalty", "presence_penalty", "seed", "logit_bias", "logprobs", "n", "stream_options", "modalities", "web_search_options", "audio", "prediction", "function_call"} {
		if _, sent := body[chatOnly]; sent {
			t.Errorf("chat-only param %s reached /v1/responses", chatOnly)
		}
	}
	if got := reasoningEffortIn(body); got != "low" {
		t.Errorf("reasoning.effort = %q, want low", got)
	}
	if body["max_output_tokens"] != 900.0 {
		t.Errorf("max_output_tokens = %v, want 900", body["max_output_tokens"])
	}
	text, _ := body["text"].(map[string]any)
	if format, _ := text["format"].(map[string]any); format["type"] != "json_object" {
		t.Errorf("text.format = %v, want json_object", text["format"])
	}
	if text["verbosity"] != "low" {
		t.Errorf("text.verbosity = %v, want low", text["verbosity"])
	}
	if choice, _ := body["tool_choice"].(map[string]any); choice["type"] != "function" || choice["name"] != "get_time" {
		t.Errorf("tool_choice = %v, want the Responses form {type: function, name: get_time}", body["tool_choice"])
	}
	if body["top_p"] != 0.5 || body["service_tier"] != "flex" {
		t.Errorf("top_p = %v, service_tier = %v; params the Responses API shares must pass through", body["top_p"], body["service_tier"])
	}
}

func TestApplyChatExtraParams_TranslatesToolChoiceObjects(t *testing.T) {
	for _, tc := range []struct {
		name        string
		choice      any
		wantType    string
		wantDropped bool
	}{
		{"named function", map[string]any{"type": "function", "function": map[string]any{"name": "get_time"}}, "function", false},
		{"allowed tools", map[string]any{"type": "allowed_tools", "allowed_tools": map[string]any{
			"mode": "auto", "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "get_time"}}},
		}}, "allowed_tools", false},
		{"custom tool", map[string]any{"type": "custom", "custom": map[string]any{"name": "grep"}}, "custom", false},
		{"mode", map[string]any{"type": "required"}, "required", false},
		{"function without a name", map[string]any{"type": "function"}, "", true},
		{"custom tool without a name", map[string]any{"type": "custom"}, "", true},
		{"allowed tools without tools", map[string]any{"type": "allowed_tools", "allowed_tools": map[string]any{"mode": "auto"}}, "", true},
		{"allowed tools naming a custom tool", map[string]any{"type": "allowed_tools", "allowed_tools": map[string]any{
			"mode": "auto", "tools": []any{map[string]any{"type": "custom", "custom": map[string]any{"name": "grep"}}},
		}}, "", true},
		{"an unknown type", map[string]any{"type": "bogus"}, "", true},
		{"any, which OpenAI does not take", map[string]any{"type": "any"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := &schemas.ResponsesParameters{ExtraParams: map[string]any{"tool_choice": tc.choice}}

			dropped := applyChatExtraParams(params)

			if got := slices.Contains(dropped, "tool_choice"); got != tc.wantDropped {
				t.Fatalf("dropped = %v, want tool_choice dropped: %v", dropped, tc.wantDropped)
			}
			if tc.wantDropped {
				return
			}
			got := params.ToolChoice
			if got == nil || got.ResponsesToolChoiceStruct == nil || string(got.ResponsesToolChoiceStruct.Type) != tc.wantType {
				t.Fatalf("ToolChoice = %+v, want type %s", got, tc.wantType)
			}
			if tc.wantType == "allowed_tools" {
				if tools := got.ResponsesToolChoiceStruct.Tools; len(tools) != 1 || deref(tools[0].Name) != "get_time" ||
					deref(got.ResponsesToolChoiceStruct.Mode) != "auto" {
					t.Errorf("allowed_tools = %+v, want get_time in mode auto", got.ResponsesToolChoiceStruct)
				}
			}
		})
	}
}

func TestApplyChatExtraParams_ReportsEveryParamItCannotSend(t *testing.T) {
	params := &schemas.ResponsesParameters{ExtraParams: map[string]any{
		"max_tokens":       "999",
		"response_format":  "json",
		"verbosity":        3.0,
		"reasoning_effort": "turbo",
		"seed":             7.0,
		"tool_choice":      "auto",
		"top_p":            0.5,
		"functions":        []any{},
	}}

	dropped := applyChatExtraParams(params)

	want := []string{"functions", "max_tokens", "reasoning_effort", "response_format", "seed", "verbosity"}
	if !slices.Equal(dropped, want) {
		t.Errorf("dropped = %v, want %v", dropped, want)
	}
	if len(params.ExtraParams) != 2 || params.ExtraParams["tool_choice"] != "auto" || params.ExtraParams["top_p"] != 0.5 {
		t.Errorf("kept = %v, want tool_choice and top_p", params.ExtraParams)
	}
}

func TestApplyChatExtraParams_MaxCompletionTokensWinsOverMaxTokens(t *testing.T) {
	params := &schemas.ResponsesParameters{ExtraParams: map[string]any{"max_tokens": 100.0, "max_completion_tokens": 900.0}}

	applyChatExtraParams(params)

	if deref(params.MaxOutputTokens) != 900 {
		t.Errorf("MaxOutputTokens = %d, want 900", deref(params.MaxOutputTokens))
	}
}

func TestResponsesAPI_RawReasoningExtraParamReachesTheWire(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	if _, err := client.ChatCompletion(t.Context(), &ChatRequest{
		Messages:    toolReplayConversation(),
		Tools:       []ToolDefinition{getTimeTool},
		ExtraParams: map[string]any{"reasoning": map[string]any{"effort": "high"}},
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	if got := reasoningEffortIn(fake.responsesBody(t)); got != "high" {
		t.Errorf("reasoning.effort = %q, want high", got)
	}
}

func TestResponsesAPI_SwitchesWhenOpenAITurnsChatAway(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		req         ChatRequest
	}{
		{"gpt-5.6 with tools and no reasoning", "gpt-5.6-terra", ChatRequest{Tools: []ToolDefinition{getTimeTool}}},
		{"gpt-6 with tools and an effort", "gpt-6-luna", ChatRequest{Tools: []ToolDefinition{getTimeTool}, Reasoning: &ReasoningConfig{Effort: "high"}}},
		{"gpt-5.4 with tools and an effort", "gpt-5.4-mini", ChatRequest{Tools: []ToolDefinition{getTimeTool}, Reasoning: &ReasoningConfig{Effort: "low"}}},
		{"a Responses-only model", "gpt-5-pro", ChatRequest{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
			client := newFakeOpenAIClient(t, fake, tc.model)

			for range 2 {
				req := tc.req
				req.Messages = []ChatMessage{{Role: "user", Content: "hi"}}
				if _, err := client.ChatCompletion(t.Context(), &req); err != nil {
					t.Fatalf("ChatCompletion: %v", err)
				}
			}

			want := []string{"/v1/chat/completions", "/v1/responses", "/v1/responses"}
			if got := fake.paths(); !slices.Equal(got, want) {
				t.Errorf("upstream calls = %v, want %v: one rejection, then the Responses API for the model", got, want)
			}
		})
	}
}

func TestResponsesAPI_StreamSwitchesWhenOpenAITurnsChatAway(t *testing.T) {
	for _, tc := range []struct {
		model string
		req   ChatRequest
	}{
		{"gpt-5.6-terra", ChatRequest{Tools: []ToolDefinition{getTimeTool}}},
		{"gpt-6-luna", ChatRequest{Tools: []ToolDefinition{getTimeTool}, Reasoning: &ReasoningConfig{Effort: "high"}}},
		{"gpt-5.4-mini", ChatRequest{Tools: []ToolDefinition{getTimeTool}, Reasoning: &ReasoningConfig{Effort: "low"}}},
		{"gpt-5-pro", ChatRequest{}},
	} {
		t.Run(tc.model, func(t *testing.T) {
			fake := &fakeOpenAI{responsesSSE: []string{
				`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"It is noon."}`,
				streamCompleted,
			}}
			client := newFakeOpenAIClient(t, fake, tc.model)

			for range 2 {
				req := tc.req
				req.Messages = []ChatMessage{{Role: "user", Content: "What time is it?"}}
				if res := collectStream(t, client, &req); res.content != "It is noon." || res.final.Err != nil {
					t.Fatalf("content = %q, final = %+v", res.content, res.final)
				}
			}

			want := []string{"/v1/chat/completions", "/v1/responses", "/v1/responses"}
			if got := fake.paths(); !slices.Equal(got, want) {
				t.Errorf("upstream calls = %v, want %v", got, want)
			}
		})
	}
}

func TestResponsesAPI_RequestsChatCompletionsAcceptsStayThere(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		req         ChatRequest
	}{
		{"gpt-5-mini with tools and an effort", "gpt-5-mini", ChatRequest{Tools: []ToolDefinition{getTimeTool}, Reasoning: &ReasoningConfig{Effort: "low"}}},
		{"gpt-5.1 with tools and an effort", "gpt-5.1", ChatRequest{Tools: []ToolDefinition{getTimeTool}, Reasoning: &ReasoningConfig{Effort: "low"}}},
		{"o4-mini with tools", "o4-mini", ChatRequest{Tools: []ToolDefinition{getTimeTool}}},
		{"gpt-5.4 with tools and no reasoning", "gpt-5.4-mini", ChatRequest{Tools: []ToolDefinition{getTimeTool}}},
		{"gpt-oss with tools and an effort", "gpt-oss-120b", ChatRequest{Tools: []ToolDefinition{getTimeTool}, Reasoning: &ReasoningConfig{Effort: "high"}}},
		{"gpt-5.6 with tools and effort none", "gpt-5.6-terra", ChatRequest{Tools: []ToolDefinition{getTimeTool}, Reasoning: &ReasoningConfig{Effort: "none"}}},
		{"gpt-5.6 with reasoning and no tools", "gpt-5.6-terra", ChatRequest{Reasoning: &ReasoningConfig{Effort: "high"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeOpenAI{}
			client := newFakeOpenAIClient(t, fake, tc.model)
			req := tc.req
			req.Messages = []ChatMessage{{Role: "user", Content: "hi"}}

			if _, err := client.ChatCompletion(t.Context(), &req); err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}
			fake.onlyCall(t, "/v1/chat/completions")
		})
	}
}

func TestResponsesAPI_SwitchIsPerModel(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClient(t, fake, "gpt-5.4-mini")

	if _, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest()); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	other := toolsWithReasoningRequest()
	other.Model = "gpt-5-mini"
	if _, err := client.ChatCompletion(t.Context(), other); err != nil {
		t.Fatalf("ChatCompletion for another model: %v", err)
	}

	want := []string{"/v1/chat/completions", "/v1/responses", "/v1/chat/completions"}
	if got := fake.paths(); !slices.Equal(got, want) {
		t.Errorf("upstream calls = %v, want %v", got, want)
	}
}

func TestRedirectedToResponsesAPI_OnlyOnOpenAIsRedirect(t *testing.T) {
	redirect := &Error{StatusCode: http.StatusBadRequest, Message: "Function tools with reasoning_effort are not supported for gpt-5.6-terra " +
		"in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'."}
	for _, tc := range []struct {
		name, provider string
		err            error
		want           bool
	}{
		{"OpenAI's redirect", "openai", redirect, true},
		{"another 400", "openai", &Error{StatusCode: http.StatusBadRequest, Message: "Invalid value for 'temperature'"}, false},
		{"the path in a non-400", "openai", &Error{StatusCode: http.StatusNotFound, Message: "no route for v1/responses"}, false},
		{"a non-LLM error", "openai", errors.New("use /v1/responses"), false},
		{"another provider", "anthropic", redirect, false},
		{"azure", "azure", redirect, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &BifrostClient{cfg: BifrostConfig{Provider: tc.provider}}
			if got := client.redirectedToResponsesAPI(tc.err, "gpt-5.6-terra"); got != tc.want {
				t.Errorf("redirectedToResponsesAPI = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResponsesAPI_RejectionsNothingRedoesStillLogAWarning(t *testing.T) {
	t.Run("the Responses API's own 400", func(t *testing.T) {
		var logs bytes.Buffer
		fake := &fakeOpenAI{responsesStatus: http.StatusBadRequest, responsesMessage: "Unrecognized request URL for POST /v1/responses"}
		client := newFakeOpenAIClientWithConfig(t, fake, BifrostConfig{
			Model:  "gpt-5.6-terra",
			Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		})

		if _, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest()); err == nil {
			t.Fatal("want the Responses API's 400 to surface")
		}

		if !strings.Contains(logs.String(), "operation=responses") {
			t.Errorf("the failed Responses attempt was not logged as a warning:\n%s", logs.String())
		}
		if strings.Contains(logs.String(), "operation=chatCompletion ") {
			t.Errorf("the rejection the client redid was logged as a warning:\n%s", logs.String())
		}
	})
	t.Run("the Azure gateway route", func(t *testing.T) {
		var logs bytes.Buffer
		t.Setenv("AZURE_API_KEY", "az-key")
		fake := &fakeOpenAI{chatRejection: openAIChatRejection("gpt-5.6-terra", "low", true)}
		srv := httptest.NewServer(fake)
		t.Cleanup(srv.Close)
		client, err := NewBifrostClient(t.Context(), BifrostConfig{
			Provider:   "azure",
			Model:      "azure-gpt-5-mini",
			BaseURL:    srv.URL,
			MaxRetries: new(0),
			Logger:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Shutdown)

		if _, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest()); err == nil {
			t.Fatal("want the gateway's rejection to surface")
		}

		if !strings.Contains(logs.String(), "LLM call attempt failed") {
			t.Errorf("a rejection nothing redoes was not logged as a warning:\n%s", logs.String())
		}
	})
}

func TestResponsesAPI_OtherChatRejectionsSurface(t *testing.T) {
	fake := &fakeOpenAI{chatRejection: "Invalid value for 'temperature'"}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	_, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest())

	if llmErr, ok := errors.AsType[*Error](err); !ok || llmErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("err = %v, want the chat-completions 400", err)
	}
	fake.onlyCall(t, "/v1/chat/completions")
}

func TestResponsesAPI_FailedRedoLeavesTheModelOnChat(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			fake := &fakeOpenAI{responsesStatus: http.StatusNotFound}
			client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

			for range 2 {
				var err error
				if stream {
					_, err = client.ChatCompletionStream(t.Context(), toolsWithReasoningRequest())
				} else {
					_, err = client.ChatCompletion(t.Context(), toolsWithReasoningRequest())
				}
				if llmErr, ok := errors.AsType[*Error](err); !ok || llmErr.StatusCode != http.StatusNotFound {
					t.Fatalf("err = %v, want the Responses API's 404", err)
				}
			}

			want := []string{"/v1/chat/completions", "/v1/responses", "/v1/chat/completions", "/v1/responses"}
			if got := fake.paths(); !slices.Equal(got, want) {
				t.Errorf("upstream calls = %v, want %v", got, want)
			}
		})
	}
}

func TestResponsesAPI_AliasKeepsItsReasoningAfterTheSwitch(t *testing.T) {
	redirect := openAIChatRejection("gpt-5.6-terra", "low", true)
	for _, tc := range []struct {
		name, model, wantEffort string
		req                     ChatRequest
	}{
		{"alias with an effort", "general", "high", ChatRequest{Reasoning: &ReasoningConfig{Effort: "high"}}},
		{"alias with minimal, which gpt-5.6 does not take", "general", "low", ChatRequest{Reasoning: &ReasoningConfig{Effort: "minimal"}}},
		{"alias with max", "general", "high", ChatRequest{Reasoning: &ReasoningConfig{Effort: "max"}}},
		{"alias with none keeps the model default", "general", "", ChatRequest{Reasoning: &ReasoningConfig{Effort: "none"}}},
		{"alias with a budget under a large output cap", "general", "low", ChatRequest{MaxTokens: 16384, Reasoning: &ReasoningConfig{BudgetTokens: 2048}}},
		{"alias with a raw effort", "general", "low", ChatRequest{ExtraParams: map[string]any{"reasoning_effort": "low"}}},
		{"alias with a raw reasoning effort over a typed one", "general", "low", ChatRequest{
			Reasoning:   &ReasoningConfig{Effort: "high"},
			ExtraParams: map[string]any{"reasoning": map[string]any{"effort": "low"}},
		}},
		{"recognised name keeps Bifrost's per-model rule", "gpt-5-pro", "low", ChatRequest{Reasoning: &ReasoningConfig{Effort: "none"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeOpenAI{responsesJSON: completedResponseJSON, chatRejection: redirect}
			client := newFakeOpenAIClient(t, fake, tc.model)
			req := tc.req
			req.Messages = toolReplayConversation()
			req.Tools = []ToolDefinition{getTimeTool}

			if _, err := client.ChatCompletion(t.Context(), &req); err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}

			if got := reasoningEffortIn(fake.responsesBody(t)); got != tc.wantEffort {
				t.Errorf("reasoning.effort = %q, want %q", got, tc.wantEffort)
			}
		})
	}
}

func TestResponsesAPI_AliasEffortJoinsARawReasoningObject(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON, chatRejection: openAIChatRejection("gpt-5.6-terra", "low", true)}
	client := newFakeOpenAIClient(t, fake, "general")
	summary := map[string]any{"summary": "auto"}

	if _, err := client.ChatCompletion(t.Context(), &ChatRequest{
		Messages:    toolReplayConversation(),
		Tools:       []ToolDefinition{getTimeTool},
		Reasoning:   &ReasoningConfig{Effort: "high"},
		ExtraParams: map[string]any{"reasoning": summary},
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	reasoning, _ := fake.responsesBody(t)["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Errorf("reasoning = %v, want the configured effort alongside the caller's summary", reasoning)
	}
	if len(summary) != 1 {
		t.Errorf("the caller's reasoning map was modified: %v", summary)
	}
}

func TestResponsesAPI_SwitchIsLoggedOncePerModel(t *testing.T) {
	var logs bytes.Buffer
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClientWithConfig(t, fake, BifrostConfig{
		Model:  "gpt-5.6-terra",
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest()); err != nil {
				t.Errorf("ChatCompletion: %v", err)
			}
		})
	}
	wg.Wait()

	if strings.Contains(logs.String(), "LLM call attempt failed") {
		t.Errorf("OpenAI's expected rejection was logged as a failed attempt:\n%s", logs.String())
	}
	if got := strings.Count(logs.String(), "OpenAI requires the Responses API"); got != 1 {
		t.Errorf("switch logged %d times, want once:\n%s", got, logs.String())
	}
}

func TestResponsesAPI_FinishReasons(t *testing.T) {
	for _, tc := range []struct {
		name, json, want string
	}{
		{"text-only final turn", textOnlyResponseJSON, finishReasonStop},
		{"tool call", completedResponseJSON, finishReasonToolCalls},
		{"incomplete for max_output_tokens", incompleteResponseJSON("max_output_tokens"), finishReasonLength},
		{"incomplete for content_filter", incompleteResponseJSON("content_filter"), finishReasonContentFilter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeOpenAIClient(t, &fakeOpenAI{responsesJSON: tc.json}, "gpt-5.6-terra")

			resp, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest())
			if err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}
			if resp.FinishReason != tc.want {
				t.Errorf("FinishReason = %q, want %q", resp.FinishReason, tc.want)
			}
		})
	}
}

func incompleteResponseJSON(reason string) string {
	return `{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.6-terra",
	  "status":"incomplete","incomplete_details":{"reason":"` + reason + `"},
	  "output":[{"id":"msg_1","type":"message","role":"assistant","status":"incomplete","content":[{"type":"output_text","text":"cut","annotations":[]}]}]}`
}

func failedResponseJSON(code string) string {
	return `{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.6-terra","status":"failed","output":[],
	  "error":{"code":"` + code + `","message":"the request failed upstream"}}`
}

func TestResponsesAPI_InBandErrorIsClassified(t *testing.T) {
	for _, tc := range []struct {
		code       string
		wantStatus int
		wantCalls  int
	}{
		{"server_error", http.StatusBadGateway, 2},
		{"vector_store_timeout", http.StatusBadGateway, 2},
		{"rate_limit_exceeded", http.StatusTooManyRequests, 2},
		{"429", http.StatusTooManyRequests, 2},
		{"context_length_exceeded", http.StatusBadRequest, 1},
		{"invalid_prompt", http.StatusBadRequest, 1},
		{"image_parse_error", http.StatusBadRequest, 1},
		{"unsupported_image_media_type", http.StatusBadRequest, 1},
		{"empty_image_file", http.StatusBadRequest, 1},
	} {
		t.Run(tc.code, func(t *testing.T) {
			shortenRateLimitBackoff(t)
			fake := &fakeOpenAI{responsesJSON: failedResponseJSON(tc.code)}
			client := newFakeOpenAIClientWithConfig(t, fake, BifrostConfig{Model: "gpt-5.6-terra", MaxRetries: new(1)})

			_, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest())

			llmErr, ok := errors.AsType[*Error](err)
			if !ok || llmErr.StatusCode != tc.wantStatus || llmErr.Code != tc.code || llmErr.Provider != "openai" {
				t.Fatalf("err = %#v, want status %d, code %s, provider openai", err, tc.wantStatus, tc.code)
			}
			if got := fake.countCalls("/v1/responses"); got != tc.wantCalls {
				t.Errorf("/v1/responses calls = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

func TestResponsesAPI_InBandContextOverflowReachesContextLimitHandling(t *testing.T) {
	client := newFakeOpenAIClient(t, &fakeOpenAI{responsesJSON: failedResponseJSON("context_length_exceeded")}, "gpt-5.6-terra")

	_, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest())

	if llmErr, ok := errors.AsType[*Error](err); !ok || !llmErr.IsContextLimitError() {
		t.Fatalf("err = %#v, want a context-limit error", err)
	}
}

func TestIsContextLimitError_ResponsesAPIWording(t *testing.T) {
	for _, e := range []*Error{
		{StatusCode: http.StatusBadRequest, Message: "Your input exceeds the context window of this model. Please adjust your input and try again."},
		{StatusCode: http.StatusBadGateway, Message: "request failed", Code: "context_length_exceeded"},
	} {
		if !e.IsContextLimitError() {
			t.Errorf("IsContextLimitError(%+v) = false, want true", e)
		}
	}
}

type streamResult struct {
	content, reasoning string
	calls              map[int]*ToolCall
	final              StreamChunk
}

func collectStream(t *testing.T, client *BifrostClient, req *ChatRequest) streamResult {
	t.Helper()
	ch, err := client.ChatCompletionStream(t.Context(), req)
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	res := streamResult{calls: map[int]*ToolCall{}}
	var content, reasoning strings.Builder
	for chunk := range ch {
		content.WriteString(chunk.ContentDelta)
		reasoning.WriteString(chunk.ReasoningDelta)
		for _, d := range chunk.ToolCallDeltas {
			tc, ok := res.calls[d.Index]
			if !ok {
				tc = &ToolCall{}
				res.calls[d.Index] = tc
			}
			tc.ID = cmp.Or(d.ID, tc.ID)
			tc.Name = cmp.Or(d.Name, tc.Name)
			tc.Arguments += d.ArgumentsDelta
		}
		if chunk.FinishReason != "" || chunk.Err != nil {
			res.final = chunk
		}
	}
	res.content, res.reasoning = content.String(), reasoning.String()
	return res
}

const streamCompleted = `{"type":"response.completed","sequence_number":99,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.6-terra","status":"completed","output":[],"usage":{"input_tokens":50,"input_tokens_details":{"cached_tokens":0},"output_tokens":12,"output_tokens_details":{"reasoning_tokens":4},"total_tokens":62}}}`

func TestResponsesAPI_StreamsTextAndToolCalls(t *testing.T) {
	fake := &fakeOpenAI{responsesSSE: []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.6-terra","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","sequence_number":2,"item_id":"rs_1","output_index":0,"summary_index":0,"delta":"checking the clock"}`,
		`{"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"checking the clock"}]}}`,
		`{"type":"response.output_item.added","sequence_number":4,"output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.output_text.delta","sequence_number":5,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"Checking "}`,
		`{"type":"response.output_text.delta","sequence_number":6,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"CET."}`,
		`{"type":"response.output_item.done","sequence_number":7,"output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Checking CET.","annotations":[]}]}}`,
		`{"type":"response.output_item.added","sequence_number":8,"output_index":2,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_2","name":"get_time","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":9,"item_id":"fc_1","output_index":2,"delta":"{\"tz\":"}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":10,"item_id":"fc_1","output_index":2,"delta":"\"CET\"}"}`,
		`{"type":"response.output_item.done","sequence_number":11,"output_index":2,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_2","name":"get_time","arguments":"{\"tz\":\"CET\"}"}}`,
		streamCompleted,
	}}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	fake.responsesBody(t)
	if res.content != "Checking CET." {
		t.Errorf("content = %q", res.content)
	}
	if res.reasoning != "checking the clock" {
		t.Errorf("reasoning = %q", res.reasoning)
	}
	if len(res.calls) != 1 || *res.calls[0] != (ToolCall{ID: "call_2", Name: "get_time", Arguments: `{"tz":"CET"}`}) {
		t.Errorf("tool calls = %+v", res.calls)
	}
	if res.final.Err != nil || res.final.FinishReason != finishReasonToolCalls {
		t.Fatalf("final chunk = %+v, want finish reason %q", res.final, finishReasonToolCalls)
	}
	if u := res.final.Usage; u == nil || u.PromptTokens != 50 || u.CompletionTokens != 12 || u.ReasoningTokens != 4 {
		t.Errorf("final usage = %+v", u)
	}
}

func TestResponsesAPI_StreamTextOnlyTurnStops(t *testing.T) {
	fake := &fakeOpenAI{responsesSSE: []string{
		`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"It is 12:00 UTC."}`,
		streamCompleted,
	}}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	if res.content != "It is 12:00 UTC." || res.final.FinishReason != finishReasonStop {
		t.Errorf("content = %q, final = %+v; want the text and finish reason %q", res.content, res.final, finishReasonStop)
	}
}

func TestResponsesAPI_StreamItemsDeliveredOnlyAsCompletedItems(t *testing.T) {
	fake := &fakeOpenAI{responsesSSE: []string{
		`{"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello there","annotations":[]}]}}`,
		`{"type":"response.output_item.done","sequence_number":2,"output_index":1,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_2","name":"get_time","arguments":"{\"tz\":\"CET\"}"}}`,
		streamCompleted,
	}}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	if res.content != "hello there" {
		t.Errorf("content = %q, want the text of the completed message item", res.content)
	}
	if len(res.calls) != 1 || *res.calls[0] != (ToolCall{ID: "call_2", Name: "get_time", Arguments: `{"tz":"CET"}`}) {
		t.Errorf("tool calls = %+v", res.calls)
	}
	if res.final.FinishReason != finishReasonToolCalls {
		t.Errorf("finish reason = %q", res.final.FinishReason)
	}
}

func TestResponsesAPI_StreamIncompleteForMaxOutputTokensIsLength(t *testing.T) {
	fake := &fakeOpenAI{responsesSSE: []string{
		`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"cut"}`,
		`{"type":"response.incomplete","sequence_number":2,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.6-terra","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`,
	}}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	if res.final.Err != nil || res.final.FinishReason != finishReasonLength {
		t.Fatalf("final chunk = %+v, want finish reason %q", res.final, finishReasonLength)
	}
}

func TestResponsesAPI_StreamArgumentlessCallDeliveredOnlyAsCompletedItem(t *testing.T) {
	fake := &fakeOpenAI{responsesSSE: []string{
		`{"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_2","name":"get_time","arguments":""}}`,
		streamCompleted,
	}}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	if len(res.calls) != 1 || *res.calls[0] != (ToolCall{ID: "call_2", Name: "get_time"}) {
		t.Errorf("tool calls = %+v, want the call opened with no arguments", res.calls)
	}
}

func TestResponsesAPI_StreamArgumentsOnAddedItemAreNotRepeated(t *testing.T) {
	fake := &fakeOpenAI{responsesSSE: []string{
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_2","name":"get_time","arguments":"{\"tz\":"}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":2,"item_id":"fc_1","output_index":0,"delta":"{\"tz\":"}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":3,"item_id":"fc_1","output_index":0,"delta":"\"CET\"}"}`,
		`{"type":"response.output_item.done","sequence_number":4,"output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_2","name":"get_time","arguments":"{\"tz\":\"CET\"}"}}`,
		streamCompleted,
	}}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	if len(res.calls) != 1 || res.calls[0].Arguments != `{"tz":"CET"}` {
		t.Errorf("tool calls = %+v, want the arguments assembled once", res.calls)
	}
}

func TestResponsesAPI_StreamFailureSurfacesAsError(t *testing.T) {
	fake := &fakeOpenAI{responsesSSE: []string{
		`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"partial"}`,
		`{"type":"response.failed","sequence_number":2,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.6-terra","status":"failed","output":[],"error":{"code":"server_error","message":"upstream exploded"}}}`,
	}}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	if res.final.Err == nil || !strings.Contains(res.final.Err.Error(), "upstream exploded") {
		t.Fatalf("final chunk = %+v, want the provider failure as an error", res.final)
	}
	if llmErr, ok := errors.AsType[*Error](res.final.Err); !ok || llmErr.Provider != "openai" {
		t.Fatalf("final chunk error = %+v, want the configured provider label", res.final.Err)
	}
}

func TestResponsesAPI_StreamEndingWithoutCompletionIsTruncation(t *testing.T) {
	fake := &fakeOpenAI{
		responsesSSE: []string{
			`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"partial"}`,
		},
		responsesSSEDone: true,
	}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	if llmErr, ok := errors.AsType[*Error](res.final.Err); !ok || !llmErr.IsStreamTruncated() {
		t.Fatalf("final chunk = %+v, want a stream-truncated error", res.final)
	}
}

func TestResponsesAPI_StreamConnectionClosedMidResponseFails(t *testing.T) {
	fake := &fakeOpenAI{responsesSSE: []string{
		`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"partial"}`,
	}}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

	res := collectStream(t, client, toolsWithReasoningRequest())

	if llmErr, ok := errors.AsType[*Error](res.final.Err); !ok || llmErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("final chunk = %+v, want a 502 for the dropped connection", res.final)
	}
}

func TestResponsesAPI_MixedToolResultUsesInputBlockTypes(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")
	req := toolsWithReasoningRequest()
	req.Messages[3].ContentBlocks = []ContentBlock{
		{Type: "text", Text: "chart attached"},
		{Type: "image_url", ImageURL: "data:image/png;base64,iVBORw0KGgo="},
	}

	if _, err := client.ChatCompletion(t.Context(), req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	input, _ := fake.responsesBody(t)["input"].([]any)
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if item["type"] != "function_call_output" {
			continue
		}
		blocks, ok := item["output"].([]any)
		if !ok || len(blocks) != 2 {
			t.Fatalf("output = %#v, want both blocks kept", item["output"])
		}
		for _, b := range blocks {
			blockType, _ := b.(map[string]any)["type"].(string)
			if !strings.HasPrefix(blockType, "input_") {
				t.Errorf("tool output block type %q; the Responses API only accepts input_* blocks here: %v", blockType, b)
			}
		}
		return
	}
	t.Fatal("no function_call_output in input")
}

func TestResponsesAPI_AzureGatewayKeepsItsDeploymentRoute(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")
	fake := &fakeOpenAI{chatRejection: "Function tools with reasoning_effort are not supported. To use function tools, use /v1/responses."}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider:   "azure",
		Model:      "azure-gpt-5-mini",
		BaseURL:    srv.URL,
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	if _, err := client.ChatCompletion(t.Context(), &ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:     []ToolDefinition{getTimeTool},
		Reasoning: &ReasoningConfig{Effort: "high"},
	}); err == nil {
		t.Fatal("want the gateway's rejection to surface")
	}

	fake.onlyCall(t, "/openai/deployments/azure-gpt-5-mini/chat/completions")
}

func TestResponsesAPI_NonOpenAIModelsStayOnChatCompletionsWithTheirReasoning(t *testing.T) {
	for _, model := range []string{"qwen/qwen3.7-flash", "google/gemini-3.5-flash-lite", "deepseek/deepseek-v4.1-flash"} {
		t.Run(model, func(t *testing.T) {
			fake := &fakeOpenAI{}
			client := newFakeOpenAIClient(t, fake, model)

			if _, err := client.ChatCompletion(t.Context(), &ChatRequest{
				Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
				Tools:     []ToolDefinition{getTimeTool},
				Reasoning: &ReasoningConfig{Effort: "high"},
			}); err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}

			body := fake.onlyCall(t, "/v1/chat/completions")
			if body["reasoning_effort"] != "high" {
				t.Errorf("reasoning_effort = %v, want high: an OpenAI-compatible endpoint must keep the configured effort", body["reasoning_effort"])
			}
		})
	}
}

func TestResponsesAPI_UsageMarksCacheWritesOnlyWhenACountArrives(t *testing.T) {
	usageWith := func(details string) string {
		return `"usage":{"input_tokens":50,"input_tokens_details":` + details + `,"output_tokens":12,"total_tokens":62}`
	}
	for _, tc := range []struct {
		name, details string
		wantReported  bool
		wantWrites    int
	}{
		{"write count", `{"cached_tokens":30,"cache_write_tokens":20}`, true, 20},
		{"no write count", `{"cached_tokens":30}`, false, 0},
	} {
		t.Run(tc.name+" non-streaming", func(t *testing.T) {
			body := strings.TrimSuffix(textOnlyResponseJSON, "}") + "," + usageWith(tc.details) + "}"
			client := newFakeOpenAIClient(t, &fakeOpenAI{responsesJSON: body}, "gpt-5.6-terra")

			resp, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest())
			if err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}
			if u := resp.Usage; u == nil || u.CacheWritesReported != tc.wantReported || u.CacheWriteTokens != tc.wantWrites {
				t.Errorf("usage = %+v, want writes reported %v with %d writes", u, tc.wantReported, tc.wantWrites)
			}
		})
		t.Run(tc.name+" streaming", func(t *testing.T) {
			completed := `{"type":"response.completed","sequence_number":99,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.6-terra","status":"completed","output":[],` + usageWith(tc.details) + `}}`
			client := newFakeOpenAIClient(t, &fakeOpenAI{responsesSSE: []string{
				`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"It is 12:00 UTC."}`,
				completed,
			}}, "gpt-5.6-terra")

			res := collectStream(t, client, toolsWithReasoningRequest())

			if u := res.final.Usage; u == nil || u.CacheWritesReported != tc.wantReported || u.CacheWriteTokens != tc.wantWrites {
				t.Errorf("final usage = %+v, want writes reported %v with %d writes", u, tc.wantReported, tc.wantWrites)
			}
		})
	}
}
