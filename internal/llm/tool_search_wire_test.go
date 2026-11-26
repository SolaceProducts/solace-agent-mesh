// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const toolSearchModel = "claude-sonnet-4-6"

type capturedAnthropicCall struct {
	body   map[string]any
	header http.Header
}

func captureAnthropicCall(t *testing.T, tools []ToolDefinition) capturedAnthropicCall {
	t.Helper()
	return captureAnthropicConversation(t, []ChatMessage{{Role: "user", Content: "Hi"}}, tools)
}

func captureAnthropicConversation(t *testing.T, msgs []ChatMessage, tools []ToolDefinition) capturedAnthropicCall {
	t.Helper()
	model := toolSearchModel
	t.Setenv("ANTHROPIC_API_KEY", "test-key-123")

	calls := make(chan capturedAnthropicCall, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		select {
		case calls <- capturedAnthropicCall{body: body, header: r.Header.Clone()}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","model":"` + model + `",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{Provider: "anthropic", Model: model, BaseURL: srv.URL, MaxRetries: new(0)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	req := &ChatRequest{Model: model, Messages: msgs, MaxTokens: 1024, Tools: tools}
	if _, err := client.ChatCompletion(ctx, req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	select {
	case c := <-calls:
		return c
	default:
		t.Fatal("no request reached the Anthropic stand-in")
		return capturedAnthropicCall{}
	}
}

func objectTool(name string) ToolDefinition {
	return ToolDefinition{
		Name:        name,
		Description: "Look up " + name,
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}},
	}
}

func sentTools(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, _ := body["tools"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("tool entry is not an object: %#v", r)
		}
		out = append(out, m)
	}
	return out
}

func toolByName(tools []map[string]any, name string) map[string]any {
	for _, tl := range tools {
		if tl["name"] == name {
			return tl
		}
	}
	return nil
}

func TestToolSearch_AnthropicDefersMarkedToolsBehindBM25Search(t *testing.T) {
	deferred := largeDeferredTool("get_jira_issue")
	loaded := objectTool("load_artifact")

	call := captureAnthropicCall(t, []ToolDefinition{loaded, deferred})
	tools := sentTools(t, call.body)

	if len(tools) != 3 {
		t.Fatalf("sent %d tools, want the search tool plus 2: %#v", len(tools), tools)
	}
	if tools[0]["type"] != "tool_search_tool_bm25_20251119" || tools[0]["name"] != "tool_search_tool_bm25" {
		t.Errorf("first tool = %#v, want the BM25 tool search server tool", tools[0])
	}
	if got := toolByName(tools, "get_jira_issue")["defer_loading"]; got != true {
		t.Errorf("deferred tool defer_loading = %#v, want true", got)
	}
	if _, has := toolByName(tools, "load_artifact")["defer_loading"]; has {
		t.Error("loaded tool carries defer_loading")
	}
	if beta := strings.Join(call.header.Values("anthropic-beta"), ","); !strings.Contains(beta, "tool-search-tool-2025-10-19") {
		t.Errorf("anthropic-beta = %q, want the tool search beta", beta)
	}
}

func TestToolSearch_SystemPromptNamesDeferredTools(t *testing.T) {
	smallDeferredTool := func(name string) ToolDefinition {
		t := objectTool(name)
		t.DeferLoading = true
		return t
	}
	cases := map[string]struct {
		deferredTool func(string) ToolDefinition
		wantSection  bool
	}{
		"tools deferred":        {largeDeferredTool, true},
		"catalog below cut-off": {smallDeferredTool, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			msgs := []ChatMessage{
				{Role: "system", Content: "You are a helpful agent.", CacheControl: &CacheControl{Type: "ephemeral"}},
				{Role: "user", Content: "Open ABC-1"},
			}

			tools := []ToolDefinition{objectTool("load_skill"), tc.deferredTool("get_jira_issue"), tc.deferredTool("list_pull_requests")}
			call := captureAnthropicConversation(t, msgs, tools)

			block := systemBlockContaining(t, call.body, "## Tools to load on demand")
			if (block != nil) != tc.wantSection {
				t.Fatalf("deferred tools section sent = %v, want %v: %#v", block != nil, tc.wantSection, call.body["system"])
			}
			if msgs[0].Content != "You are a helpful agent." {
				t.Errorf("caller's system message changed to %q", msgs[0].Content)
			}
			if !tc.wantSection {
				return
			}
			if text := toString(block["text"]); !strings.Contains(text, "get_jira_issue, list_pull_requests.") || strings.Contains(text, "load_skill") {
				t.Errorf("section = %q, want exactly the deferred tools named", text)
			}
			if block["cache_control"] == nil {
				t.Error("deferred tools section sits outside the system prompt's cache breakpoint")
			}
		})
	}
}

func systemBlockContaining(t *testing.T, body map[string]any, text string) map[string]any {
	t.Helper()
	blocks, _ := body["system"].([]any)
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok && strings.Contains(toString(m["text"]), text) {
			return m
		}
	}
	return nil
}

func TestToolSearch_NoDeferredToolsSendsNoSearchTool(t *testing.T) {
	call := captureAnthropicCall(t, []ToolDefinition{objectTool("load_artifact"), objectTool("get_jira_issue")})
	tools := sentTools(t, call.body)

	if len(tools) != 2 {
		t.Fatalf("sent %d tools, want 2: %#v", len(tools), tools)
	}
	for _, tl := range tools {
		if _, has := tl["defer_loading"]; has {
			t.Errorf("tool %v carries defer_loading", tl["name"])
		}
		if strings.HasPrefix(toString(tl["type"]), "tool_search") {
			t.Errorf("search tool sent without any deferred tool: %#v", tl)
		}
	}
}

func TestToolSearch_CacheMarkedToolStaysLoaded(t *testing.T) {
	deferred := largeDeferredTool("get_jira_issue")
	marked := objectTool("_notify_artifact_save")
	marked.DeferLoading = true
	marked.CacheControl = &CacheControl{Type: "ephemeral"}

	tools := sentTools(t, captureAnthropicCall(t, []ToolDefinition{deferred, marked}).body)

	got := toolByName(tools, "_notify_artifact_save")
	if got == nil {
		t.Fatalf("cache-marked tool missing: %#v", tools)
	}
	if _, has := got["defer_loading"]; has {
		t.Error("cache-marked tool was deferred, so its breakpoint marks nothing in the prompt")
	}
	if got["cache_control"] == nil {
		t.Error("cache-marked tool lost its cache_control")
	}
}

func TestToolSearch_OpenAIGatewaySendsEveryToolInFull(t *testing.T) {
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 10, 0) }}
	client := newGatewayClient(t, gw, "gpt-5.6", nil)

	req := &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}, Tools: []ToolDefinition{objectTool("load_artifact"), largeDeferredTool("get_jira_issue")}}
	if _, err := client.ChatCompletion(t.Context(), req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	raw, _ := gw.raw(t, 0)
	if strings.Contains(string(raw), "defer_loading") || strings.Contains(string(raw), "tool_search") {
		t.Errorf("OpenAI gateway request carries tool search fields: %s", raw)
	}
	if strings.Contains(string(raw), "Tools to load on demand") {
		t.Errorf("chat completions request names deferred tools in the system prompt: %s", raw)
	}
	if tools := sentTools(t, gw.body(t, 0)); len(tools) != 2 {
		t.Errorf("sent %d tools, want both in full: %s", len(tools), raw)
	}
}

func TestToolSearch_StreamedSearchQueryNeverReachesDeferredToolArguments(t *testing.T) {
	handler := func(release <-chan struct{}) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			ev := func(name string, data map[string]any) { data["type"] = name; writeAnthropicEvent(w, name, data) }
			ev("message_start", map[string]any{"message": map[string]any{"id": "msg_1", "type": "message", "role": "assistant",
				"model": billedUsageModel, "content": []any{}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1}}})
			ev("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{
				"type": "server_tool_use", "id": "srvtoolu_01", "name": "tool_search_tool_bm25", "input": map[string]any{}}})
			ev("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"query": "ji`}})
			ev("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": `ra issue"}`}})
			ev("content_block_stop", map[string]any{"index": 0})
			ev("content_block_start", map[string]any{"index": 1, "content_block": map[string]any{
				"type": "tool_search_tool_result", "tool_use_id": "srvtoolu_01",
				"content": map[string]any{"type": "tool_search_tool_search_result", "tool_references": []any{map[string]any{"type": "tool_reference", "tool_name": "get_jira_issue"}}}}})
			ev("content_block_stop", map[string]any{"index": 1})
			ev("content_block_start", map[string]any{"index": 2, "content_block": map[string]any{
				"type": "tool_use", "id": "toolu_01", "name": "get_jira_issue", "input": map[string]any{}}})
			ev("content_block_delta", map[string]any{"index": 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"id": `}})
			ev("content_block_delta", map[string]any{"index": 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": `"ABC-1"}`}})
			ev("content_block_stop", map[string]any{"index": 2})
			ev("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 20}})
			ev("message_stop", map[string]any{})
		}
	}
	client := newAnthropicTestClient(t, handler)

	stream, err := client.ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Open ABC-1"}}, MaxTokens: 1024, Tools: []ToolDefinition{largeDeferredTool("get_jira_issue")},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	args := map[int]string{}
	ids := map[int]string{}
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		for _, d := range chunk.ToolCallDeltas {
			args[d.Index] += d.ArgumentsDelta
			if d.ID != "" {
				ids[d.Index] = d.ID
			}
		}
	}

	if len(args) != 1 || ids[0] != "toolu_01" {
		t.Fatalf("tool calls = %#v (ids %#v), want exactly toolu_01", args, ids)
	}
	if want := `{"id": "ABC-1"}`; args[0] != want {
		t.Errorf("arguments = %q, want %q", args[0], want)
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

func TestToolSearch_DefersOnlyAboveTheCutoff(t *testing.T) {
	cases := map[string]struct {
		tools      []ToolDefinition
		wantSearch bool
	}{
		"large catalog": {largeDeferredCatalog(), true},
		"small catalog": {[]ToolDefinition{deferredTool("get_jira_issue", 0)}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tools := sentTools(t, captureAnthropicCall(t, tc.tools).body)
			hasSearch := tools[0]["type"] == "tool_search_tool_bm25_20251119"
			if hasSearch != tc.wantSearch {
				t.Errorf("search tool sent = %v, want %v", hasSearch, tc.wantSearch)
			}
			for _, tl := range tools {
				if _, deferred := tl["defer_loading"]; deferred && !tc.wantSearch {
					t.Errorf("tool %v deferred although the catalog is below the cutoff", tl["name"])
				}
			}
		})
	}
}

func TestToolSearch_RejectedSearchFallsBackToEveryToolLoaded(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "stream"}[stream], func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "test-key-123")
			var mu sync.Mutex
			var bodies []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(raw))
				mu.Unlock()
				if strings.Contains(string(raw), "defer_loading") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"tools.1.defer_loading: Extra inputs are not permitted"}}`))
					return
				}
				writeAnthropicReply(w, strings.Contains(string(raw), `"stream":true`))
			}))
			t.Cleanup(srv.Close)
			client, err := NewBifrostClient(t.Context(), BifrostConfig{Provider: "anthropic", Model: toolSearchModel, BaseURL: srv.URL, MaxRetries: new(0)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Shutdown)

			req := &ChatRequest{Messages: []ChatMessage{{Role: "system", Content: "You help."}, {Role: "user", Content: "Hi"}}, MaxTokens: 1024,
				Tools: []ToolDefinition{objectTool("load_artifact"), largeDeferredTool("get_jira_issue")}}
			for call := 1; call <= 2; call++ {
				attempts, err := sendForAttempts(t, client, req, stream)
				if err != nil {
					t.Fatalf("call %d failed instead of falling back: %v", call, err)
				}
				if want := map[int]int{1: 2, 2: 1}[call]; attempts != want {
					t.Errorf("call %d took %d attempts, want %d", call, attempts, want)
				}
			}

			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != 3 {
				t.Fatalf("got %d requests, want the rejected one, its redo and one more call", len(bodies))
			}
			for i, body := range bodies[1:] {
				if strings.Contains(body, "defer_loading") || strings.Contains(body, "tool_search") {
					t.Errorf("request %d after the rejection still carries tool search: %s", i+2, body)
				}
				if strings.Contains(body, "Tools to load on demand") {
					t.Errorf("request %d after the rejection still names deferred tools in the system prompt", i+2)
				}
			}
		})
	}
}

func writeAnthropicReply(w http.ResponseWriter, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","model":"` + toolSearchModel + `",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ev := func(name string, data map[string]any) { data["type"] = name; writeAnthropicEvent(w, name, data) }
	ev("message_start", map[string]any{"message": map[string]any{"id": "msg_test", "type": "message", "role": "assistant",
		"model": toolSearchModel, "content": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}})
	ev("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	ev("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "ok"}})
	ev("content_block_stop", map[string]any{"index": 0})
	ev("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 1}})
	ev("message_stop", map[string]any{})
}

func sendForAttempts(t *testing.T, client *BifrostClient, req *ChatRequest, stream bool) (int, error) {
	t.Helper()
	if !stream {
		resp, err := client.ChatCompletion(t.Context(), req)
		if err != nil {
			return 0, err
		}
		return resp.Attempts, nil
	}
	chunks, err := client.ChatCompletionStream(t.Context(), req)
	if err != nil {
		return 0, err
	}
	attempts := 0
	for chunk := range chunks {
		if chunk.Err != nil {
			return 0, chunk.Err
		}
		attempts = max(attempts, chunk.Attempts)
	}
	return attempts, nil
}

func TestToolSearch_GPTOnResponsesAPIUsesOpenAISearch(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")
	req := &ChatRequest{
		Messages: toolReplayConversation(), MaxTokens: 4096,
		Tools: []ToolDefinition{getTimeTool, largeDeferredTool("get_jira_issue")},
	}

	for call := 1; call <= 2; call++ {
		if _, err := client.ChatCompletion(t.Context(), req); err != nil {
			t.Fatalf("call %d: %v", call, err)
		}
	}

	bodies := responsesBodies(fake)
	if len(bodies) != 2 {
		t.Fatalf("got %d /v1/responses calls, want 2: %v", len(bodies), fake.paths())
	}
	if raw, _ := json.Marshal(bodies[0]); strings.Contains(string(raw), "defer_loading") {
		t.Errorf("call redirected from chat completions deferred tools: %s", raw)
	}
	tools := sentTools(t, bodies[1])
	var search bool
	for _, tl := range tools {
		search = search || tl["type"] == "tool_search"
	}
	if !search {
		t.Errorf("no tool_search tool sent to /v1/responses: %v", tools)
	}
	if got := toolByName(tools, "get_jira_issue")["defer_loading"]; got != true {
		t.Errorf("deferred tool defer_loading = %v, want true", got)
	}
	if _, has := toolByName(tools, "get_time")["defer_loading"]; has {
		t.Error("loaded tool carries defer_loading")
	}
}

func TestToolSearch_RejectedOnResponsesAPIFallsBackToEveryToolLoaded(t *testing.T) {
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON, toolSearchRejection: "Invalid tool type: 'tool_search' is not supported."}
	client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")
	req := &ChatRequest{
		Messages: toolReplayConversation(), MaxTokens: 4096,
		Tools: []ToolDefinition{getTimeTool, largeDeferredTool("get_jira_issue")},
	}

	if _, err := client.ChatCompletion(t.Context(), req); err != nil {
		t.Fatalf("call moving the model to the Responses API: %v", err)
	}
	resp, err := client.ChatCompletion(t.Context(), req)
	if err != nil {
		t.Fatalf("deferring call failed instead of falling back: %v", err)
	}
	if resp.Attempts != 2 {
		t.Errorf("attempts = %d, want the rejected call and its redo", resp.Attempts)
	}

	bodies := responsesBodies(fake)
	if len(bodies) != 3 {
		t.Fatalf("got %d /v1/responses calls, want the first, the rejected one and its redo: %v", len(bodies), fake.paths())
	}
	raw, _ := json.Marshal(bodies[2])
	if strings.Contains(string(raw), "defer_loading") || strings.Contains(string(raw), "tool_search") {
		t.Errorf("redo still carries tool search: %s", raw)
	}
	if strings.Contains(string(raw), "Tools to load on demand") {
		t.Error("redo still names deferred tools in the system prompt")
	}
}

func responsesBodies(f *fakeOpenAI) []map[string]any {
	var bodies []map[string]any
	for _, c := range f.recorded() {
		if strings.HasSuffix(c.path, "/responses") {
			bodies = append(bodies, c.body)
		}
	}
	return bodies
}
