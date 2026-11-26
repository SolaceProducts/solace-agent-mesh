// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type sentBodies struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (s *sentBodies) all() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func (s *sentBodies) last(t *testing.T) map[string]any {
	t.Helper()
	bodies := s.all()
	if len(bodies) == 0 {
		t.Fatal("the provider received no request")
	}
	return bodies[len(bodies)-1]
}

const (
	temperatureRefusal         = `{"error":{"message":"Unsupported value: 'temperature' does not support 0.2 with this model. Only the default (1) value is supported.","type":"invalid_request_error","param":"temperature","code":"unsupported_value"}}`
	thinkingTemperatureRefusal = "{\"error\":{\"message\":\"The model returned the following errors: `temperature` may only be set to 1 when thinking is enabled.\",\"type\":\"invalid_request_error\",\"code\":\"400\"}}"
	contextLengthRefusal       = `{"error":{"message":"This model's maximum context length is 128000 tokens.","type":"invalid_request_error","param":"messages","code":"context_length_exceeded"}}`
	temperatureRangeRefusal    = `{"error":{"message":"Invalid 'temperature': decimal above maximum value. Expected a value <= 2, but got 5 instead.","type":"invalid_request_error","param":"temperature","code":"decimal_above_max_value"}}`
)

type refusal func(n int, body map[string]any) string

func answersEvery(int, map[string]any) string { return "" }

func refusesTemperature(errorBody string) refusal {
	return func(_ int, body map[string]any) string {
		if _, carries := body["temperature"]; carries {
			return errorBody
		}
		return ""
	}
}

func refusesFirst(errorBody string) refusal {
	return func(n int, _ map[string]any) string {
		if n == 0 {
			return errorBody
		}
		return ""
	}
}

func recordingProvider(t *testing.T, refuse refusal) (*httptest.Server, *sentBodies) {
	t.Helper()
	sent := &sentBodies{}
	server := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		sent.mu.Lock()
		n := len(sent.bodies)
		sent.bodies = append(sent.bodies, body)
		sent.mu.Unlock()

		if errorBody := refuse(n, body); errorBody != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(errorBody))
			return
		}
		if streamed, _ := body["stream"].(bool); streamed {
			streamHandler([]string{"ok"})(w, r)
			return
		}
		nonStreamHandler("ok", "stop")(w, r)
	})
	t.Cleanup(server.Close)
	return server, sent
}

func storedParamsClient(t *testing.T, serverURL string, configure func(*BifrostConfig)) *BifrostClient {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key-123")
	cfg := BifrostConfig{Provider: "openai", Model: "test-model", BaseURL: serverURL, MaxRetries: new(0)}
	configure(&cfg)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

const overloadedRefusal = `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`

func anthropicRecordingProvider(t *testing.T, overloadedFirst bool) (*httptest.Server, *sentBodies) {
	t.Helper()
	sent := &sentBodies{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		sent.mu.Lock()
		n := len(sent.bodies)
		sent.bodies = append(sent.bodies, body)
		sent.mu.Unlock()

		if overloadedFirst && n == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusProviderOverloaded)
			_, _ = w.Write([]byte(overloadedRefusal))
			return
		}
		if streamed, _ := body["stream"].(bool); streamed {
			w.Header().Set("Content-Type", "text/event-stream")
			writeAnthropicEvent(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
				"id": "msg_1", "type": "message", "role": "assistant", "model": billedUsageModel, "content": []any{},
				"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
			}})
			writeAnthropicEvent(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "text", "text": ""}})
			writeAnthropicEvent(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
				"delta": map[string]any{"type": "text_delta", "text": "ok"}})
			writeAnthropicEvent(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			writeAnthropicEvent(w, "message_delta", map[string]any{"type": "message_delta",
				"delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 1}})
			writeAnthropicEvent(w, "message_stop", map[string]any{"type": "message_stop"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"` + billedUsageModel +
			`","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(server.Close)
	return server, sent
}

func anthropicStoredParamsClient(t *testing.T, serverURL string, stored RequestDefaults) *BifrostClient {
	t.Helper()
	return providerStoredParamsClient(t, "anthropic", billedUsageModel, serverURL, stored)
}

func providerStoredParamsClient(t *testing.T, provider, model, serverURL string, stored RequestDefaults) *BifrostClient {
	t.Helper()
	t.Setenv(strings.ToUpper(provider)+"_API_KEY", "test-key")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: provider, Model: model, BaseURL: serverURL, MaxRetries: new(1), RequestDefaults: stored,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

func recordingProviderFor(t *testing.T, provider string) (*httptest.Server, *sentBodies) {
	t.Helper()
	if provider == "gemini" {
		return geminiRecordingProvider(t)
	}
	return anthropicRecordingProvider(t, false)
}

const geminiAnswer = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`

func geminiRecordingProvider(t *testing.T) (*httptest.Server, *sentBodies) {
	t.Helper()
	sent := &sentBodies{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		sent.mu.Lock()
		sent.bodies = append(sent.bodies, body)
		sent.mu.Unlock()

		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: "+geminiAnswer+"\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, geminiAnswer)
	}))
	t.Cleanup(server.Close)
	return server, sent
}

var callForms = map[string]func(context.Context, *BifrostClient, *ChatRequest) error{
	"unstreamed": func(ctx context.Context, c *BifrostClient, req *ChatRequest) error {
		_, err := c.ChatCompletion(ctx, req)
		return err
	},
	"streamed": func(ctx context.Context, c *BifrostClient, req *ChatRequest) error {
		stream, err := c.ChatCompletionStream(ctx, req)
		if err != nil {
			return err
		}
		for chunk := range stream {
			if chunk.Err != nil {
				return chunk.Err
			}
		}
		return nil
	},
}

func userTurn() *ChatRequest {
	return &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}, WithStoredParams: true}
}

func storedParams() RequestDefaults {
	return RequestDefaults{
		Temperature:   new(0.2),
		MaxTokens:     512,
		StopSequences: []string{"END"},
		ExtraParams:   map[string]any{"top_p": 0.9, "seed": 7},
	}
}

func TestStoredParams_AreSentOnACallThatNamesNone(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, answersEvery)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			if err := call(t.Context(), client, userTurn()); err != nil {
				t.Fatal(err)
			}

			body := sent.last(t)
			want := map[string]any{
				"temperature":           0.2,
				"max_completion_tokens": 512.0,
				"stop":                  []any{"END"},
				"top_p":                 0.9,
				"seed":                  7.0,
			}
			for key, value := range want {
				if !reflect.DeepEqual(body[key], value) {
					t.Errorf("sent %s = %v, want %v (body: %v)", key, body[key], value, body)
				}
			}
		})
	}
}

func TestStoredParams_YieldToWhatTheRequestNames(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, answersEvery)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			req := userTurn()
			req.Temperature = new(0.9)
			req.MaxTokens = 64
			req.StopSequences = []string{"STOP"}
			req.ExtraParams = map[string]any{"top_p": 0.5}
			if err := call(t.Context(), client, req); err != nil {
				t.Fatal(err)
			}

			body := sent.last(t)
			want := map[string]any{
				"temperature":           0.9,
				"max_completion_tokens": 64.0,
				"stop":                  []any{"STOP", "END"},
				"top_p":                 0.5,
				"seed":                  7.0,
			}
			for key, value := range want {
				if !reflect.DeepEqual(body[key], value) {
					t.Errorf("sent %s = %v, want %v (body: %v)", key, body[key], value, body)
				}
			}
			if len(req.ExtraParams) != 1 || len(req.StopSequences) != 1 {
				t.Errorf("the caller's request was changed: ExtraParams = %v, StopSequences = %v", req.ExtraParams, req.StopSequences)
			}
		})
	}
}

func TestStoredParams_AreLeftOffACallThatDoesNotAskForThem(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, answersEvery)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) {
				cfg.RequestDefaults = storedParams()
				cfg.RequestDefaults.ParallelToolCalls = new(false)
			})

			summary := &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Summarize this"}}, Tools: []ToolDefinition{getTimeTool}}
			if err := call(t.Context(), client, summary); err != nil {
				t.Fatal(err)
			}

			body := sent.last(t)
			for _, stored := range []string{"temperature", "max_completion_tokens", "stop", "top_p", "seed", "parallel_tool_calls"} {
				if _, carries := body[stored]; carries {
					t.Errorf("a call SAM makes for its own ends carried the stored %s (body: %v)", stored, body)
				}
			}
		})
	}
}

func TestStoredMaxTokens_LeavesTheStoredLimitForTheAnswerBesideAThinkingBudget(t *testing.T) {
	budgets := map[string]struct {
		budget, wantMaxTokens int
	}{
		"a budget above the stored limit": {budget: 8000, wantMaxTokens: 8000 + 4096},
		"a budget equal to it":            {budget: 4096, wantMaxTokens: 4096 + 4096},
		"a budget below it":               {budget: 2048, wantMaxTokens: 4096},
	}
	for name, tc := range budgets {
		t.Run(name, func(t *testing.T) {
			server, sent := anthropicRecordingProvider(t, false)
			client := anthropicStoredParamsClient(t, server.URL, RequestDefaults{MaxTokens: 4096})

			req := userTurn()
			req.Reasoning = &ReasoningConfig{BudgetTokens: tc.budget}
			if _, err := client.ChatCompletion(t.Context(), req); err != nil {
				t.Fatal(err)
			}

			body := sent.last(t)
			if body["max_tokens"] != float64(tc.wantMaxTokens) {
				t.Errorf("max_tokens = %v, want %d beside a thinking budget of %d (body: %v)", body["max_tokens"], tc.wantMaxTokens, tc.budget, body)
			}
			thinking, _ := body["thinking"].(map[string]any)
			if thinking["budget_tokens"] != float64(tc.budget) {
				t.Errorf("thinking = %v, want the agent's budget of %d", body["thinking"], tc.budget)
			}
		})
	}
}

func TestStoredParams_OfAModelOverrideReplaceTheParentModels(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, answersEvery)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			req := userTurn()
			req.ModelOverride = &BifrostConfig{
				Model:           "override-model",
				RequestDefaults: RequestDefaults{ExtraParams: map[string]any{"presence_penalty": 0.4}},
			}
			if err := call(t.Context(), client, req); err != nil {
				t.Fatal(err)
			}

			body := sent.last(t)
			if body["model"] != "override-model" {
				t.Fatalf("sent model = %v, want override-model", body["model"])
			}
			if body["presence_penalty"] != 0.4 {
				t.Errorf("the override model's stored presence_penalty was not sent (body: %v)", body)
			}
			for _, parentParam := range []string{"temperature", "stop", "top_p", "seed", "max_completion_tokens"} {
				if _, carries := body[parentParam]; carries {
					t.Errorf("the parent model's stored %s reached the override model (body: %v)", parentParam, body)
				}
			}
		})
	}
}

func TestOverrideCacheKey_DifferentStoredParams(t *testing.T) {
	plain := &BifrostConfig{Model: "gpt-4o"}
	withParams := &BifrostConfig{Model: "gpt-4o", RequestDefaults: RequestDefaults{Temperature: new(0.2)}}
	if cacheKey(t, plain) == cacheKey(t, withParams) {
		t.Error("an override whose stored params changed would keep calling through the client built for the old ones")
	}
}

func TestStoredTemperature_RefusedByTheModelIsDroppedAndTheCallRedone(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, refusesTemperature(temperatureRefusal))
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			if err := call(t.Context(), client, userTurn()); err != nil {
				t.Fatalf("the call failed on a stored temperature the model refuses: %v", err)
			}
			bodies := sent.all()
			if len(bodies) != 2 {
				t.Fatalf("provider received %d requests, want the refused one and its redo", len(bodies))
			}
			if _, carries := bodies[1]["temperature"]; carries {
				t.Errorf("the redo still carried the temperature (body: %v)", bodies[1])
			}
			if bodies[1]["top_p"] != 0.9 {
				t.Errorf("the redo lost the other stored params (body: %v)", bodies[1])
			}

			if err := call(t.Context(), client, userTurn()); err != nil {
				t.Fatal(err)
			}
			if got := len(sent.all()); got != 3 {
				t.Errorf("provider received %d requests after the next call, want 3: a refused temperature is not sent again", got)
			}
		})
	}
}

func refusesTemperatureWhileThinking(_ int, body map[string]any) string {
	_, carriesTemperature := body["temperature"]
	_, thinks := body["reasoning_effort"]
	if carriesTemperature && thinks {
		return thinkingTemperatureRefusal
	}
	return ""
}

func TestStoredTemperature_RefusedOnlyWhileThinkingIsKeptOnRequestsThatDoNotThink(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, refusesTemperatureWhileThinking)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })
			thinkingTurn := func() *ChatRequest {
				req := userTurn()
				req.Reasoning = &ReasoningConfig{Effort: "high"}
				return req
			}

			if err := call(t.Context(), client, thinkingTurn()); err != nil {
				t.Fatalf("the thinking turn failed on a stored temperature the model refuses beside thinking: %v", err)
			}
			if bodies := sent.all(); len(bodies) != 2 || bodies[1]["temperature"] != nil {
				t.Fatalf("provider received %v, want the refused request and a redo without the temperature", bodies)
			}

			if err := call(t.Context(), client, userTurn()); err != nil {
				t.Fatal(err)
			}
			if body := sent.last(t); body["temperature"] != 0.2 {
				t.Errorf("a turn without thinking sent temperature = %v, want the stored 0.2 (body: %v)", body["temperature"], body)
			}

			if err := call(t.Context(), client, thinkingTurn()); err != nil {
				t.Fatal(err)
			}
			if got := len(sent.all()); got != 4 {
				t.Errorf("provider received %d requests, want 4: the next thinking turn is sent once, without the temperature", got)
			}
		})
	}
}

func TestStoredParams_AreKeptThroughAContextTooLongForTheModel(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, refusesFirst(contextLengthRefusal))
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			if err := call(t.Context(), client, userTurn()); err == nil {
				t.Fatal("the call succeeded, want the provider's error")
			}
			if got := len(sent.all()); got != 1 {
				t.Fatalf("provider received %d requests, want 1: the call fails the same way without the stored params", got)
			}
			if err := call(t.Context(), client, userTurn()); err != nil {
				t.Fatal(err)
			}
			if body := sent.last(t); body["temperature"] != 0.2 || body["top_p"] != 0.9 {
				t.Errorf("the next call lost the stored params (body: %v)", body)
			}
		})
	}
}

const (
	unknownParamRefusal = `{"error":{"message":"Unrecognized request argument supplied: drop_params","type":"invalid_request_error","param":null,"code":null}}`
	invalidImageRefusal = `{"error":{"message":"Invalid image URL.","type":"invalid_request_error","param":"messages","code":"invalid_image_url"}}`
)

var storedParamNames = []string{"temperature", "max_completion_tokens", "stop", "top_p", "seed"}

func storedParamsIn(body map[string]any) []string {
	var carried []string
	for _, name := range storedParamNames {
		if _, ok := body[name]; ok {
			carried = append(carried, name)
		}
	}
	return carried
}

func storedParamNamesWithout(dropped ...string) []string {
	return slices.DeleteFunc(slices.Clone(storedParamNames), func(name string) bool { return slices.Contains(dropped, name) })
}

func thinkingTurn() *ChatRequest {
	req := userTurn()
	req.Reasoning = &ReasoningConfig{Effort: "medium"}
	return req
}

const (
	unknownTopPRefusal = `{"error":{"message":"Unknown parameter: 'top_p'.","type":"invalid_request_error","param":"top_p","code":"unknown_parameter"}}`
	throttledRefusal   = `{"error":{"message":"Too many requests, please wait before trying again.","type":"ThrottlingException","code":"ThrottlingException"}}`
)

func TestStoredParams_ARefusalThatNamesAParamDropsOnlyThatParamFromThenOn(t *testing.T) {
	refusals := map[string]struct{ body, dropped string }{
		"a temperature out of range":         {body: temperatureRangeRefusal, dropped: "temperature"},
		"a top_p the provider does not know": {body: unknownTopPRefusal, dropped: "top_p"},
	}
	for name, tc := range refusals {
		for form, call := range callForms {
			t.Run(name+"/"+form, func(t *testing.T) {
				server, sent := recordingProvider(t, refusesFirst(tc.body))
				client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

				if err := call(t.Context(), client, userTurn()); err != nil {
					t.Fatalf("the call failed on a stored param the model refuses: %v", err)
				}
				if err := call(t.Context(), client, userTurn()); err != nil {
					t.Fatal(err)
				}

				bodies := sent.all()
				if len(bodies) != 3 {
					t.Fatalf("provider received %d requests, want the refused one, its redo and the next call", len(bodies))
				}
				want := storedParamNamesWithout(tc.dropped)
				for i, body := range bodies[1:] {
					if got := storedParamsIn(body); !slices.Equal(got, want) {
						t.Errorf("request %d carried stored params %v, want %v: only %s was refused", i+2, got, want, tc.dropped)
					}
				}
			})
		}
	}
}

func TestStoredParams_ARefusalThatNamesNoneOfThemIsRedoneWithoutThemAndNothingIsRemembered(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, refusesFirst(unknownParamRefusal))
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			for range 2 {
				if err := call(t.Context(), client, userTurn()); err != nil {
					t.Fatalf("the call failed on a refusal the redo without stored params answers: %v", err)
				}
			}

			bodies := sent.all()
			if len(bodies) != 3 {
				t.Fatalf("provider received %d requests, want the refused one, its redo and the next call", len(bodies))
			}
			if got := storedParamsIn(bodies[1]); len(got) != 0 {
				t.Errorf("the redo carried stored params %v, want none", got)
			}
			if got := storedParamsIn(bodies[2]); !slices.Equal(got, storedParamNames) {
				t.Errorf("the next call carried stored params %v, want all of %v: nothing was refused by name", got, storedParamNames)
			}
		})
	}
}

func TestStoredParams_ARefusalOnAThinkingTurnIsRememberedOnlyForThinkingTurns(t *testing.T) {
	refusesTopPWhileThinking := func(_ int, body map[string]any) string {
		_, carriesTopP := body["top_p"]
		_, thinks := body["reasoning_effort"]
		if carriesTopP && thinks {
			return unknownTopPRefusal
		}
		return ""
	}
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, refusesTopPWhileThinking)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			for _, turn := range []*ChatRequest{thinkingTurn(), userTurn(), thinkingTurn()} {
				if err := call(t.Context(), client, turn); err != nil {
					t.Fatal(err)
				}
			}

			bodies := sent.all()
			if len(bodies) != 4 {
				t.Fatalf("provider received %d requests, want the refused thinking turn, its redo, the plain turn and the next thinking turn", len(bodies))
			}
			wantTopP := []bool{true, false, true, false}
			for i, body := range bodies {
				if _, carries := body["top_p"]; carries != wantTopP[i] {
					t.Errorf("request %d carries top_p = %v, want %v (body: %v)", i+1, carries, wantTopP[i], body)
				}
			}
		})
	}
}

func TestStoredParams_ARefusalOnAPlainTurnIsRememberedForThinkingTurnsToo(t *testing.T) {
	refusesTopP := func(_ int, body map[string]any) string {
		if _, carriesTopP := body["top_p"]; carriesTopP {
			return unknownTopPRefusal
		}
		return ""
	}
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, refusesTopP)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			for _, turn := range []*ChatRequest{userTurn(), thinkingTurn()} {
				if err := call(t.Context(), client, turn); err != nil {
					t.Fatal(err)
				}
			}

			bodies := sent.all()
			if len(bodies) != 3 {
				t.Fatalf("provider received %d requests, want the refused plain turn, its redo and the thinking turn", len(bodies))
			}
			if _, carries := bodies[2]["top_p"]; carries {
				t.Errorf("the thinking turn carried top_p after a plain turn learned the model refuses it (body: %v)", bodies[2])
			}
		})
	}
}

func TestStoredParams_ARefusalThatIsAThrottleIsNotRedoneWithoutThem(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			throttled := func(int, map[string]any) string { return throttledRefusal }
			server, sent := recordingProvider(t, throttled)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			if err := call(t.Context(), client, userTurn()); err == nil {
				t.Fatal("the call succeeded, want the provider's throttle")
			}
			for i, body := range sent.all() {
				if got := storedParamsIn(body); !slices.Equal(got, storedParamNames) {
					t.Errorf("request %d carried stored params %v, want all of %v: a throttle says nothing about them", i+1, got, storedParamNames)
				}
			}
		})
	}
}

func TestError_RefusesTheRequestItself(t *testing.T) {
	cases := map[string]struct {
		err  Error
		want bool
	}{
		"a 400 from the provider":      {err: Error{StatusCode: 400, Message: "Unknown parameter: 'top_p'."}, want: true},
		"a 400 Bifrost raised itself":  {err: Error{StatusCode: 400, Message: "reasoning.max_tokens must be >= 1024", raisedByBifrost: true}},
		"a 400 for a context too long": {err: Error{StatusCode: 400, Code: "context_length_exceeded", Message: "too long"}},
		"a 400 that is a throttle":     {err: Error{StatusCode: 400, Type: "ThrottlingException", Message: "Rate exceeded"}},
		"a rate limit":                 {err: Error{StatusCode: 429, Message: "Rate exceeded"}},
		"a failure of the provider":    {err: Error{StatusCode: 500, Message: "internal error"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.err.refusesTheRequestItself(); got != tc.want {
				t.Errorf("refusesTheRequestItself() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBifrostErrorToError_RemembersThatBifrostRaisedIt(t *testing.T) {
	status := 400
	raised := bifrostErrorToError(&schemas.BifrostError{IsBifrostError: true, StatusCode: &status, Error: &schemas.ErrorField{Message: "x"}}, "anthropic")
	if raised.refusesTheRequestItself() {
		t.Error("an error Bifrost raised itself counts as the provider refusing the request")
	}
	refused := bifrostErrorToError(&schemas.BifrostError{StatusCode: &status, Error: &schemas.ErrorField{Message: "x"}}, "anthropic")
	if !refused.refusesTheRequestItself() {
		t.Error("a 400 from the provider does not count as refusing the request")
	}
}

func TestStoredMaxTokens_BesideAReasoningEffortLeavesRoomForTheMinimumThinkingBudget(t *testing.T) {
	limits := map[string]struct{ stored, wantMaxTokens int }{
		"a limit below the minimum budget": {stored: 512, wantMaxTokens: 512 + 1024},
		"a limit at the minimum budget":    {stored: 1024, wantMaxTokens: 1024 + 1024},
		"a limit above the minimum budget": {stored: 4096, wantMaxTokens: 4096},
	}
	for name, tc := range limits {
		t.Run(name, func(t *testing.T) {
			server, sent := anthropicRecordingProvider(t, false)
			client := anthropicStoredParamsClient(t, server.URL, RequestDefaults{MaxTokens: tc.stored})

			if _, err := client.ChatCompletion(t.Context(), thinkingTurn()); err != nil {
				t.Fatalf("a Claude turn with a reasoning effort failed beside a stored limit of %d: %v", tc.stored, err)
			}

			bodies := sent.all()
			if len(bodies) != 1 {
				t.Fatalf("provider received %d requests, want 1", len(bodies))
			}
			body := bodies[0]
			if body["max_tokens"] != float64(tc.wantMaxTokens) {
				t.Errorf("max_tokens = %v, want %d (body: %v)", body["max_tokens"], tc.wantMaxTokens, body)
			}
			thinking, _ := body["thinking"].(map[string]any)
			budget, _ := thinking["budget_tokens"].(float64)
			if budget < 1024 || budget >= body["max_tokens"].(float64) {
				t.Errorf("thinking budget = %v, want at least 1024 and below max_tokens %v", thinking["budget_tokens"], body["max_tokens"])
			}
		})
	}
}

func TestStoredMaxTokens_BesideNoThinkingOrAModelThatTakesEffortNativelyIsLeftAlone(t *testing.T) {
	t.Run("an effort of none on Claude", func(t *testing.T) {
		server, sent := anthropicRecordingProvider(t, false)
		client := anthropicStoredParamsClient(t, server.URL, RequestDefaults{MaxTokens: 512})
		req := userTurn()
		req.Reasoning = &ReasoningConfig{Effort: "none"}

		if _, err := client.ChatCompletion(t.Context(), req); err != nil {
			t.Fatal(err)
		}
		if body := sent.last(t); body["max_tokens"] != float64(512) {
			t.Errorf("max_tokens = %v, want the stored 512 (body: %v)", body["max_tokens"], body)
		}
	})
	t.Run("an effort on a model that is not Claude", func(t *testing.T) {
		server, sent := recordingProvider(t, answersEvery)
		client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = RequestDefaults{MaxTokens: 512} })

		if _, err := client.ChatCompletion(t.Context(), thinkingTurn()); err != nil {
			t.Fatal(err)
		}
		if body := sent.last(t); body["max_completion_tokens"] != float64(512) {
			t.Errorf("max_completion_tokens = %v, want the stored 512 (body: %v)", body["max_completion_tokens"], body)
		}
	})
}

func TestStoredParams_AreKeptWhenTheCallFailsWithoutThemToo(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			refusesTwice := func(n int, _ map[string]any) string {
				if n < 2 {
					return invalidImageRefusal
				}
				return ""
			}
			server, sent := recordingProvider(t, refusesTwice)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) { cfg.RequestDefaults = storedParams() })

			if err := call(t.Context(), client, userTurn()); err == nil {
				t.Fatal("the call succeeded, want the provider's error")
			}
			if got := len(sent.all()); got != 2 {
				t.Fatalf("provider received %d requests, want the refused one and its redo without the stored params", got)
			}
			if err := call(t.Context(), client, userTurn()); err != nil {
				t.Fatal(err)
			}
			if body := sent.last(t); body["temperature"] != 0.2 || body["top_p"] != 0.9 {
				t.Errorf("the next call lost the stored params over a refusal they did not cause (body: %v)", body)
			}
		})
	}
}

func TestStoredParallelToolCalls_IsSentOnlyOnACallWithTools(t *testing.T) {
	cases := map[string]struct {
		tools    []ToolDefinition
		wantSent bool
	}{
		"with tools":    {tools: []ToolDefinition{getTimeTool}, wantSent: true},
		"without tools": {tools: nil, wantSent: false},
	}
	for name, tc := range cases {
		for form, call := range callForms {
			t.Run(name+"/"+form, func(t *testing.T) {
				server, sent := recordingProvider(t, answersEvery)
				client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) {
					cfg.RequestDefaults = RequestDefaults{ParallelToolCalls: new(false)}
				})

				req := userTurn()
				req.Tools = tc.tools
				if err := call(t.Context(), client, req); err != nil {
					t.Fatal(err)
				}

				value, carried := sent.last(t)["parallel_tool_calls"]
				if carried != tc.wantSent || (carried && value != false) {
					t.Errorf("parallel_tool_calls = %v (sent: %v), want sent: %v with the stored false", value, carried, tc.wantSent)
				}
			})
		}
	}
}

func TestStoredParallelToolCalls_ReachesAnthropicOnItsToolChoice(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := anthropicRecordingProvider(t, false)
			client := anthropicStoredParamsClient(t, server.URL, RequestDefaults{ParallelToolCalls: new(false)})

			req := userTurn()
			req.Tools = []ToolDefinition{getTimeTool}
			if err := call(t.Context(), client, req); err != nil {
				t.Fatal(err)
			}

			body := sent.last(t)
			if _, carried := body["parallel_tool_calls"]; carried {
				t.Errorf("parallel_tool_calls reached the Messages API, which refuses keys it does not know (body: %v)", body)
			}
			toolChoice, _ := body["tool_choice"].(map[string]any)
			if toolChoice["disable_parallel_tool_use"] != true {
				t.Errorf("tool_choice = %v, want disable_parallel_tool_use: true", body["tool_choice"])
			}
		})
	}
}

func TestStoredExtraParams_AreSentOnEveryAttempt(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := anthropicRecordingProvider(t, true)
			client := anthropicStoredParamsClient(t, server.URL, RequestDefaults{ExtraParams: map[string]any{"inference_geo": "us"}})

			if err := call(t.Context(), client, userTurn()); err != nil {
				t.Fatal(err)
			}

			bodies := sent.all()
			if len(bodies) != 2 {
				t.Fatalf("provider received %d requests, want the overloaded one and its retry", len(bodies))
			}
			for attempt, body := range bodies {
				if body["inference_geo"] != "us" {
					t.Errorf("attempt %d sent inference_geo = %v, want the stored us", attempt+1, body["inference_geo"])
				}
			}
		})
	}
}

func TestStoredSamplingParams_SavedAsTextAreSentAsText(t *testing.T) {
	for form, call := range callForms {
		t.Run(form, func(t *testing.T) {
			server, sent := recordingProvider(t, answersEvery)
			client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) {
				cfg.RequestDefaults = RequestDefaults{ExtraParams: map[string]any{"top_p": "0.9", "seed": "7"}}
			})

			if err := call(t.Context(), client, userTurn()); err != nil {
				t.Fatal(err)
			}

			body := sent.last(t)
			if body["top_p"] != "0.9" || body["seed"] != "7" {
				t.Errorf("top_p = %#v, seed = %#v, want the stored text \"0.9\" and \"7\" for the provider to judge", body["top_p"], body["seed"])
			}
		})
	}
}

func TestStoredSamplingParams_ReachEachProviderInItsOwnForm(t *testing.T) {
	stored := RequestDefaults{ExtraParams: map[string]any{
		"top_p": 0.9, "top_k": 40.0, "seed": 7.0, "frequency_penalty": 0.1, "presence_penalty": 0.2,
	}}
	providers := map[string]struct {
		model            string
		sentAsNamed      map[string]any
		generationConfig map[string]any
	}{
		"anthropic": {
			model:       billedUsageModel,
			sentAsNamed: map[string]any{"top_p": 0.9, "top_k": 40.0},
		},
		"gemini": {
			model:            "gemini-2.5-pro",
			sentAsNamed:      map[string]any{},
			generationConfig: map[string]any{"topP": 0.9, "frequencyPenalty": 0.1, "presencePenalty": 0.2},
		},
	}
	for provider, tc := range providers {
		for form, call := range callForms {
			t.Run(provider+"/"+form, func(t *testing.T) {
				server, sent := recordingProviderFor(t, provider)
				client := providerStoredParamsClient(t, provider, tc.model, server.URL, stored)

				if err := call(t.Context(), client, userTurn()); err != nil {
					t.Fatal(err)
				}

				body := sent.last(t)
				for key := range stored.ExtraParams {
					want, named := tc.sentAsNamed[key]
					if got, sentTopLevel := body[key]; sentTopLevel != named || got != want {
						t.Errorf("top-level %s = %v (sent: %v), want %v (sent: %v)", key, got, sentTopLevel, want, named)
					}
				}
				config, _ := body["generationConfig"].(map[string]any)
				for key, want := range tc.generationConfig {
					if config[key] != want {
						t.Errorf("generationConfig.%s = %v, want %v (generationConfig: %v)", key, config[key], want, config)
					}
				}
			})
		}
	}
}

func TestStoredParams_TheResponsesAPICannotTakeAreReportedOnce(t *testing.T) {
	var logs bytes.Buffer
	fake := &fakeOpenAI{responsesJSON: completedResponseJSON}
	client := newFakeOpenAIClientWithConfig(t, fake, BifrostConfig{
		Model:  "gpt-5.6-terra",
		Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		RequestDefaults: RequestDefaults{
			StopSequences: []string{"END"},
			ExtraParams:   map[string]any{"seed": 7.0, "top_p": 0.5},
		},
	})

	for range 2 {
		if _, err := client.ChatCompletion(t.Context(), &ChatRequest{
			Messages:         toolReplayConversation(),
			Tools:            []ToolDefinition{getTimeTool},
			ExtraParams:      map[string]any{"frequency_penalty": 0.1},
			WithStoredParams: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	reported := map[string]int{}
	for line := range strings.Lines(logs.String()) {
		var record struct{ Msg, Key string }
		if err := json.Unmarshal([]byte(line), &record); err == nil && record.Msg == "stored model parameter is not sent on the OpenAI Responses API" {
			reported[record.Key]++
		}
	}
	if want := map[string]int{"seed": 1, "stop": 1}; !reflect.DeepEqual(reported, want) {
		t.Errorf("reported stored params not sent = %v, want %v", reported, want)
	}
}

func TestStoredParams_RefusalFailsAStrictClient(t *testing.T) {
	refusals := map[string]string{
		"a refused temperature":                  temperatureRefusal,
		"a parameter the provider does not know": unknownParamRefusal,
	}
	for name, errorBody := range refusals {
		for form, call := range callForms {
			t.Run(name+"/"+form, func(t *testing.T) {
				server, sent := recordingProvider(t, refusesFirst(errorBody))
				client := storedParamsClient(t, server.URL, func(cfg *BifrostConfig) {
					cfg.RequestDefaults = storedParams()
					cfg.StrictRequestDefaults = true
				})

				err := call(t.Context(), client, userTurn())
				if llmErr, ok := errors.AsType[*Error](err); !ok || !llmErr.IsBadRequest() {
					t.Fatalf("err = %v, want the provider's refusal", err)
				}
				if got := len(sent.all()); got != 1 {
					t.Errorf("provider received %d requests, want 1", got)
				}
			})
		}
	}
}

func TestRequestTemperature_RefusalIsLeftToTheCaller(t *testing.T) {
	server, sent := recordingProvider(t, refusesTemperature(temperatureRefusal))
	client := storedParamsClient(t, server.URL, func(*BifrostConfig) {})

	req := userTurn()
	req.Temperature = new(0.2)
	if _, err := client.ChatCompletion(t.Context(), req); err == nil {
		t.Fatal("a temperature the caller set was dropped without the caller being told")
	}
	if got := len(sent.all()); got != 1 {
		t.Errorf("provider received %d requests, want 1", got)
	}
}

func TestIntParam_ReadsOnlyWholeNumbers(t *testing.T) {
	cases := map[string]struct {
		value  any
		want   int
		wantOK bool
	}{
		"an int":                   {value: 7, want: 7, wantOK: true},
		"a whole JSON number":      {value: 7.0, want: 7, wantOK: true},
		"a fractional number":      {value: 7.5},
		"a fractional float32":     {value: float32(7.5)},
		"an infinite number":       {value: math.Inf(1)},
		"a whole json.Number":      {value: json.Number("7"), want: 7, wantOK: true},
		"a fractional json.Number": {value: json.Number("7.5")},
		"text":                     {value: "7"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := IntParam(tc.value)
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Errorf("IntParam(%v) = %d, %t, want %d, %t", tc.value, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestIsProviderConfigKey(t *testing.T) {
	cases := []struct {
		provider, key string
		want          bool
	}{
		{"azure", "endpoint", true},
		{"azure", "api_version", true},
		{"azure", "azure_chat_route", true},
		{"azure", "token_url", true},
		{"azure", "client_secret", true},
		{"bedrock", "aws_secret_access_key", true},
		{"bedrock", "aws_secret_access_key_file", true},
		{"bedrock", "secret_key", true},
		{"bedrock", "aws_region_name", true},
		{"vertex", "vertex_credentials", true},
		{"vertex", "project_id", true},
		{"ollama", "url", true},
		{"azure", "top_p", false},
		{"bedrock", "temperature", false},
		{"openai", "endpoint", false},
		{"openai", "seed", false},
	}
	for _, c := range cases {
		if got := IsProviderConfigKey(c.provider, c.key); got != c.want {
			t.Errorf("IsProviderConfigKey(%q, %q) = %v, want %v", c.provider, c.key, got, c.want)
		}
	}
}
