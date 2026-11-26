// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func failureResponse(t *testing.T, err error) *a2a.Response {
	t.Helper()

	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	rc := &taskRunContext{
		sess:   &session.Session{},
		logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	var allText replyText

	resp, outcome, retErr := agent.handleStreamCallError(t.Context(), state, rc, nil, err, &allText)
	if outcome != loopReturn || retErr != nil {
		t.Fatalf("expected loopReturn / nil err, got outcome=%v err=%v", outcome, retErr)
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("no error response produced for %v", err)
	}
	return resp
}

func errorData(t *testing.T, resp *a2a.Response) map[string]any {
	t.Helper()
	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("Error.Data is %T, want map[string]any", resp.Error.Data)
	}
	return data
}

func TestLLMFailureResponseCarriesItsClassification(t *testing.T) {
	resp := failureResponse(t, &llm.Error{
		Message:    "Rate limit reached for gpt-4o in organization org-abc123 on tokens per min (TPM): Limit 30000",
		StatusCode: 429,
		Type:       "requests",
		Code:       "rate_limit_exceeded",
		Provider:   "openai",
	})

	data := errorData(t, resp)
	if got := data[a2a.ErrorDataLLMErrorCategory]; got != llm.ErrorCategoryRateLimit {
		t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMErrorCategory, got, llm.ErrorCategoryRateLimit)
	}
	if got := data[a2a.ErrorDataLLMProvider]; got != "openai" {
		t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMProvider, got, "openai")
	}
}

func TestLLMFailureResponseNamesNoUnknownProvider(t *testing.T) {
	resp := failureResponse(t, &llm.Error{
		Message:    "Internal server error",
		StatusCode: 500,
		Provider:   "acme-payroll-llm.internal",
	})

	data := errorData(t, resp)
	if got := data[a2a.ErrorDataLLMProvider]; got != llm.ProviderOther {
		t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMProvider, got, llm.ProviderOther)
	}
	for key, val := range data {
		text, ok := val.(string)
		if !ok {
			continue
		}
		for _, fragment := range []string{"acme", "payroll", "internal"} {
			if strings.Contains(strings.ToLower(text), fragment) {
				t.Errorf("%s = %q leaks the provider name", key, text)
			}
		}
	}
}

func TestLLMFailureResponseCarriesNoProviderText(t *testing.T) {
	const secret = "Permission 'aiplatform.endpoints.predict' denied on resource '//aiplatform.googleapis.com/projects/acme-payroll/locations/us-east1'"
	resp := failureResponse(t, &llm.Error{
		Message:    secret,
		StatusCode: 403,
		Provider:   "vertex",
	})

	data := errorData(t, resp)
	for key, val := range data {
		text, ok := val.(string)
		if !ok {
			continue
		}
		if strings.Contains(text, "acme-payroll") || strings.Contains(text, "aiplatform") {
			t.Errorf("%s = %q carries the provider's message text", key, text)
		}
	}
	if got := data[a2a.ErrorDataLLMErrorCategory]; got != llm.ErrorCategoryAuthFailure {
		t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMErrorCategory, got, llm.ErrorCategoryAuthFailure)
	}
}

func TestNonLLMFailureResponseCarriesNoClassification(t *testing.T) {
	resp := failureResponse(t, errors.New("tool dispatch exploded"))

	if resp.Error.Data == nil {
		return
	}
	data := errorData(t, resp)
	for _, key := range []string{a2a.ErrorDataLLMErrorCategory, a2a.ErrorDataLLMProvider} {
		if _, ok := data[key]; ok {
			t.Errorf("%s present on a non-LLM failure: %v", key, data[key])
		}
	}
}

func TestLLMFailureClassificationKeepsTokenUsage(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.TotalUsage = llm.Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150}

	resp := errorResponseWithUsage(state, -32603, "boom")
	attachLLMFailureClass(resp, &llm.Error{StatusCode: 401, Provider: "anthropic"})

	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("Error.Data is %T, want map[string]any", resp.Error.Data)
	}
	if _, ok := data["token_usage"]; !ok {
		t.Error("token_usage was displaced by the failure classification")
	}
	if got := data[a2a.ErrorDataLLMErrorCategory]; got != llm.ErrorCategoryAuthFailure {
		t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMErrorCategory, got, llm.ErrorCategoryAuthFailure)
	}
}

func TestErrorResponseStampsLastInputTokens(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.TotalUsage = llm.Usage{PromptTokens: 150000, CompletionTokens: 900, TotalTokens: 150900}
	state.LastInputTokens = 15000

	resp := errorResponseWithUsage(state, -32603, "boom")

	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("Error.Data is %T, want map[string]any", resp.Error.Data)
	}
	tokenUsage, ok := data["token_usage"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage is %T, want map[string]any", data["token_usage"])
	}
	if got := tokenUsage["last_input_tokens"]; got != 15000 {
		t.Errorf("last_input_tokens = %v, want 15000", got)
	}
	if got := tokenUsage["total_input_tokens"]; got != 150000 {
		t.Errorf("total_input_tokens = %v, want 150000", got)
	}
}

func TestLLMFailureResponseOmitsAnUnattributedProvider(t *testing.T) {
	resp := failureResponse(t, &llm.Error{Message: "stream closed unexpectedly"})

	data := errorData(t, resp)
	if _, ok := data[a2a.ErrorDataLLMProvider]; ok {
		t.Errorf("%s present without a provider: %v", a2a.ErrorDataLLMProvider, data[a2a.ErrorDataLLMProvider])
	}
	if got := data[a2a.ErrorDataLLMErrorCategory]; got != llm.ErrorCategoryUnknown {
		t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMErrorCategory, got, llm.ErrorCategoryUnknown)
	}
}

type countingFailingLLM struct {
	err        error
	summaryErr error
	summaries  atomic.Int32
	streams    atomic.Int32
}

func (c *countingFailingLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	c.summaries.Add(1)
	if c.summaryErr != nil {
		return nil, c.summaryErr
	}
	return nil, c.err
}

func (c *countingFailingLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	c.streams.Add(1)
	return nil, c.err
}

func TestThrottleWordedInTokensDoesNotTriggerOverflowRecovery(t *testing.T) {
	overflow := &llm.Error{StatusCode: 400, Message: "prompt is too long: 213456 tokens > 200000 maximum", Provider: "anthropic"}
	cases := []struct {
		name          string
		err           *llm.Error
		summaryErr    error
		wantSummarize bool
		wantProvider  string
	}{
		{
			name: "bedrock token throttle relayed as a 400",
			err:  &llm.Error{StatusCode: 400, Message: "Too many tokens, please wait before trying again.", Provider: "bedrock"},
		},
		{
			name: "anthropic per-minute token limit relayed as a 400",
			err:  &llm.Error{StatusCode: 400, Message: "Number of request tokens has exceeded your per-minute token limit.", Provider: "anthropic"},
		},
		{
			name: "a gateway token limit returned as a 429",
			err:  &llm.Error{StatusCode: 429, Message: "Token limit is exceeded. Try again in 6 seconds.", Provider: "azure"},
		},
		{
			name:          "a real context overflow",
			err:           overflow,
			wantSummarize: true,
			wantProvider:  "anthropic",
		},
		{
			name:          "a real context overflow whose summary failed outside the provider",
			err:           overflow,
			summaryErr:    errors.New("summarizer unavailable"),
			wantSummarize: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &countingFailingLLM{err: tc.err, summaryErr: tc.summaryErr}
			store := newMemSessionStore()
			agent := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
			agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
			svc := newMockServices(client, store)
			agent.svc = svc
			if err := agent.Init(t.Context(), nil, svc); err != nil {
				t.Fatalf("Init: %v", err)
			}
			seedSession(store, "test-agent", "user-1", "ctx-1")

			resp, err := agent.HandleTask(t.Context(), "task-throttle", newUserMessage("and italy?"),
				a2a.UserProperties{ClientID: "user-1", UserID: "user-1"})
			if err != nil {
				t.Fatalf("HandleTask: %v", err)
			}
			if resp == nil || resp.Error == nil {
				t.Fatalf("resp = %+v, want an error response", resp)
			}

			if summaries := client.summaries.Load(); (summaries > 0) != tc.wantSummarize {
				t.Errorf("summarization calls = %d (task calls %d), want summarization attempted %v",
					summaries, client.streams.Load(), tc.wantSummarize)
			}
			if tc.wantSummarize {
				if !strings.HasPrefix(resp.Error.Message, taxonomy.HistoryTooLongPrefix) {
					t.Errorf("message = %q, want an overflow reported as a too-long conversation", resp.Error.Message)
				}
				data := errorData(t, resp)
				if got := data[a2a.ErrorDataLLMErrorCategory]; got != llm.ErrorCategoryContextOverflow {
					t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMErrorCategory, got, llm.ErrorCategoryContextOverflow)
				}
				if got, _ := data[a2a.ErrorDataLLMProvider].(string); got != tc.wantProvider {
					t.Errorf("%s = %q, want %q", a2a.ErrorDataLLMProvider, got, tc.wantProvider)
				}
				return
			}
			if strings.HasPrefix(resp.Error.Message, taxonomy.HistoryTooLongPrefix) ||
				strings.Contains(resp.Error.Message, "too long") {
				t.Errorf("message = %q, a throttle must not be reported as a too-long conversation", resp.Error.Message)
			}
			if got := errorData(t, resp)[a2a.ErrorDataLLMErrorCategory]; got != llm.ErrorCategoryRateLimit {
				t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMErrorCategory, got, llm.ErrorCategoryRateLimit)
			}
		})
	}
}

func TestFreeLLMRefusalsReachTheResponseWithTheirCategory(t *testing.T) {
	cases := []struct {
		err          *llm.Error
		wantCategory string
		wantMessage  string
	}{
		{&llm.Error{StatusCode: 403, Type: llm.ErrorTypeTermsAcceptanceRequired, Message: "Accept the terms."},
			llm.ErrorCategoryFreeLLMTermsRequired, llm.FreeLLMTermsRequiredUserMessage},
		{&llm.Error{StatusCode: 503, Type: llm.ErrorTypeFreeTierPaused, Message: "Paused."},
			llm.ErrorCategoryFreeLLMPaused, llm.FreeLLMPausedUserMessage},
		{&llm.Error{StatusCode: 400, Type: llm.ErrorTypeFreeTierExhausted, Message: "Your free credits are used up."},
			llm.ErrorCategoryAllowanceExhausted, llm.FreeTierExhaustedUserMessage},
	}
	for _, c := range cases {
		t.Run(c.wantCategory, func(t *testing.T) {
			resp := failureResponse(t, c.err)
			if got := errorData(t, resp)[a2a.ErrorDataLLMErrorCategory]; got != c.wantCategory {
				t.Errorf("%s = %v, want %q", a2a.ErrorDataLLMErrorCategory, got, c.wantCategory)
			}
			if !strings.Contains(resp.Error.Message, c.wantMessage) {
				t.Errorf("message = %q, want it to carry %q", resp.Error.Message, c.wantMessage)
			}
		})
	}
}
