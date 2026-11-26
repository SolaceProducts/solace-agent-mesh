// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llmtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelclient"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func TestProbe_Success(t *testing.T) {
	t.Parallel()

	var seenBody map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		mockllm.AnswerCompletion(w, raw, []byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1,
			"model": "gpt-4o",
			"choices": [{
				"index": 0,
				"message": {"role":"assistant","content":"It is 3pm."},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 4, "total_tokens": 14}
		}`))
	}))
	defer provider.Close()

	cfg := newTestModelConfig(provider.URL, "sk-test")
	ok, msg := Probe(t.Context(), cfg, slog.Default(), nil)
	if !ok {
		t.Fatalf("Probe failed: %s", msg)
	}
	if msg != probeSuccessMessage {
		t.Errorf("message = %q, want %q", msg, probeSuccessMessage)
	}
	if seenBody["model"] == nil {
		t.Error("provider did not receive a model field")
	}
	if _, ok := seenBody["tools"]; !ok {
		t.Error("expected tools in probe request body")
	}
}

func TestProbe_UnknownParamFlowsToOutgoingRequest(t *testing.T) {
	t.Parallel()

	var seenBody map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		mockllm.AnswerCompletion(w, raw, []byte(`{
			"id":"chatcmpl-extra",
			"object":"chat.completion",
			"created":1,
			"model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]
		}`))
	}))
	defer provider.Close()

	cfg := newTestModelConfig(provider.URL, "sk-test")
	ok, msg := Probe(t.Context(), withStoredParams(cfg, map[string]any{
		"abc":   123,
		"top_p": 0.9,
	}), slog.Default(), nil)
	if !ok {
		t.Fatalf("Probe failed: %s", msg)
	}
	if got, present := seenBody["abc"]; !present {
		t.Errorf("abc missing from outgoing request body: %v", seenBody)
	} else if f, ok := got.(float64); !ok || f != 123 {
		t.Errorf("outgoing abc = %v (%T), want 123", got, got)
	}
	if got, present := seenBody["top_p"]; !present {
		t.Errorf("top_p missing from outgoing request body: %v", seenBody)
	} else if f, ok := got.(float64); !ok || f != 0.9 {
		t.Errorf("outgoing top_p = %v (%T), want 0.9", got, got)
	}
}

func TestProbe_TemperatureFlowsToOutgoingRequest(t *testing.T) {
	t.Parallel()

	var seenBody map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		mockllm.AnswerCompletion(w, raw, []byte(`{
			"id":"chatcmpl-temp",
			"object":"chat.completion",
			"created":1,
			"model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]
		}`))
	}))
	defer provider.Close()

	cfg := newTestModelConfig(provider.URL, "sk-test")
	ok, msg := Probe(t.Context(), withStoredParams(cfg, map[string]any{"temperature": 5}), slog.Default(), nil)
	if !ok {
		t.Fatalf("Probe failed: %s", msg)
	}
	got, present := seenBody["temperature"]
	if !present {
		t.Fatalf("temperature missing from outgoing request body: %v", seenBody)
	}
	if f, ok := got.(float64); !ok || f != 5 {
		t.Errorf("outgoing temperature = %v (%T), want 5", got, got)
	}
	_ = msg
}

func TestProbe_ToolCallCountsAsSuccess(t *testing.T) {
	t.Parallel()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mockllm.AnswerCompletion(w, raw, []byte(`{
			"id": "chatcmpl-tool",
			"object": "chat.completion",
			"created": 1,
			"model": "gpt-4o",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {"name":"get_current_time","arguments":"{}"}
					}]
				},
				"finish_reason": "tool_calls"
			}]
		}`))
	}))
	defer provider.Close()

	cfg := newTestModelConfig(provider.URL, "sk-test")
	ok, msg := Probe(t.Context(), cfg, slog.Default(), nil)
	if !ok {
		t.Fatalf("expected tool-call response to count as success; got: %s", msg)
	}
	if msg != probeSuccessMessage {
		t.Errorf("message = %q, want %q", msg, probeSuccessMessage)
	}
}

func TestProbe_ProviderError(t *testing.T) {
	t.Parallel()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`, http.StatusUnauthorized)
	}))
	defer provider.Close()

	cfg := newTestModelConfig(provider.URL, "sk-bad")
	ok, msg := Probe(t.Context(), cfg, slog.Default(), nil)
	if ok {
		t.Fatal("expected failure for 401 from provider")
	}
	if !strings.HasPrefix(msg, probeFailurePrefix) {
		t.Errorf("message = %q, expected prefix %q", msg, probeFailurePrefix)
	}
	if !strings.Contains(msg, "invalid api key") && !strings.Contains(msg, "401") {
		t.Errorf("message = %q, expected it to convey the 401 / auth error signal so a future refactor cannot mask a fraud-pass", msg)
	}
}

func TestProbe_EmptyResponse(t *testing.T) {
	t.Parallel()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mockllm.AnswerCompletion(w, raw, []byte(`{
			"id": "chatcmpl-empty",
			"object": "chat.completion",
			"created": 1,
			"model": "gpt-4o",
			"choices": [{
				"index": 0,
				"message": {"role":"assistant","content":""},
				"finish_reason": "stop"
			}]
		}`))
	}))
	defer provider.Close()

	cfg := newTestModelConfig(provider.URL, "sk-test")
	ok, msg := Probe(t.Context(), cfg, slog.Default(), nil)
	if ok {
		t.Fatal("expected failure when response has no content or tool calls")
	}
	if msg != probeNoResponseMessage {
		t.Errorf("message = %q, want %q", msg, probeNoResponseMessage)
	}
}

func TestProbe_NilConfig(t *testing.T) {
	t.Parallel()
	ok, msg := Probe(t.Context(), nil, slog.Default(), nil)
	if ok {
		t.Fatal("expected failure for nil config")
	}
	if !strings.HasPrefix(msg, probeFailurePrefix) {
		t.Errorf("message = %q, expected prefix %q", msg, probeFailurePrefix)
	}
}

func storedParamsConfig(params map[string]any) map[string]any {
	raw := map[string]any{"model": "openai/gpt-4o"}
	samapp.WithModelParams(raw, params)
	return raw
}

func TestProbeRequest_OutputLimitIsTheTestsOwnOnlyWhenNoneIsStored(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		stored int
		want   int
	}{
		"none stored bounds the test's cost":                        {stored: 0, want: probeMaxTokens},
		"a stored limit is the client's to send, whatever its size": {stored: 10000, want: 0},
		"a small stored limit is sent as stored":                    {stored: 32, want: 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &modelclient.Client{Model: "gpt-4o", RequestDefaults: llm.RequestDefaults{MaxTokens: c.stored}}
			if got := probeRequest(client, storedParamsConfig(nil)).MaxTokens; got != c.want {
				t.Errorf("MaxTokens = %d, want %d", got, c.want)
			}
		})
	}
}

func TestProbeRequest_ThinkingBudgetRaisesTheTestsOwnLimitAboveIt(t *testing.T) {
	t.Parallel()
	req := probeRequest(&modelclient.Client{Model: "gpt-4o"}, storedParamsConfig(map[string]any{
		"thinking": map[string]any{"budget_tokens": 2048},
	}))
	if req.Reasoning == nil || req.Reasoning.BudgetTokens != 2048 {
		t.Fatalf("Reasoning = %+v, want BudgetTokens=2048", req.Reasoning)
	}
	if req.MaxTokens <= 2048 {
		t.Errorf("MaxTokens=%d must exceed BudgetTokens=2048: Anthropic refuses a limit at or below the budget", req.MaxTokens)
	}
}

func TestProbeRequest_StoredLimitBelowTheStoredBudgetIsLeftToFail(t *testing.T) {
	t.Parallel()
	client := &modelclient.Client{Model: "gpt-4o", RequestDefaults: llm.RequestDefaults{MaxTokens: 128}}
	req := probeRequest(client, storedParamsConfig(map[string]any{
		"max_tokens": 128,
		"thinking":   map[string]any{"budget_tokens": 2048},
	}))
	if req.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0 so the client sends the stored 128 as a chat does", req.MaxTokens)
	}
}

func TestProbeRequest_SendsTheStoredReasoningEffortAnAgentTurnSends(t *testing.T) {
	t.Parallel()
	req := probeRequest(&modelclient.Client{Model: "gpt-4o"}, storedParamsConfig(map[string]any{
		"thinking": map[string]any{"effort": "high", "budget_tokens": 2048},
	}))
	if req.Reasoning == nil || req.Reasoning.Effort != "high" || req.Reasoning.BudgetTokens != 0 {
		t.Errorf("Reasoning = %+v, want effort high alone, as resolveReasoningTier sends for an agent", req.Reasoning)
	}
}

func TestProbeRequest_UnusableThinkingSendsNoReasoning(t *testing.T) {
	t.Parallel()
	req := probeRequest(&modelclient.Client{Model: "gpt-4o"}, storedParamsConfig(map[string]any{"thinking": "not-a-map"}))
	if req.Reasoning != nil {
		t.Errorf("Reasoning = %+v, want nil", req.Reasoning)
	}
}

func TestFormatProbeError_StripsRedactedSentinel(t *testing.T) {
	t.Parallel()
	msg := formatProbeError(&testErr{msg: "invalid api key: <REDACTED>"})
	if strings.Contains(msg, "<REDACTED>") {
		t.Errorf("sentinel leaked to FE: %q", msg)
	}
}

func TestFormatProbeError_Truncates(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", maxProbeErrorLen+50)
	msg := formatProbeError(&testErr{msg: long})
	if !strings.HasPrefix(msg, probeFailurePrefix) {
		t.Errorf("missing prefix: %q", msg)
	}
	if !strings.HasSuffix(msg, "...") {
		t.Errorf("long error should be truncated with '...': %q", msg[len(msg)-10:])
	}
	wantLen := len(probeFailurePrefix) + maxProbeErrorLen + 3
	if len(msg) != wantLen {
		t.Errorf("truncated length = %d, want %d", len(msg), wantLen)
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func newTestModelConfig(baseURL, apiKey string) map[string]any {
	return map[string]any{
		"model":    "openai/gpt-4o",
		"api_base": baseURL,
		"api_key":  apiKey,
	}
}

func withStoredParams(raw, params map[string]any) map[string]any {
	samapp.WithModelParams(raw, params)
	return raw
}

func TestFormatProbeError_AzureSetupProblemNamesCauseFixAndAzureReply(t *testing.T) {
	t.Parallel()
	msg := formatProbeError(&llm.Error{
		Provider:   "azure",
		StatusCode: http.StatusNotFound,
		Code:       "DeploymentNotFound",
		Message:    "The API deployment for this resource does not exist.",
	})
	want := probeFailurePrefix +
		"Azure OpenAI has no deployment with this model name at this endpoint (DeploymentNotFound). " +
		"Check the model's endpoint and deployment name, and that the deployment still exists. " +
		`Details: "The API deployment for this resource does not exist."`
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
}

func TestClientInitErrorRendersAsItsCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("bifrost init: provider openai: token url is not absolute")
	marked := &clientInitError{cause: cause}

	if got := marked.Error(); got != cause.Error() {
		t.Errorf("Error() = %q, want the cause verbatim %q", got, cause.Error())
	}
	if got, want := formatProbeError(marked), formatProbeError(cause); got != want {
		t.Errorf("rendered = %q, want %q", got, want)
	}
	if !errors.Is(marked, ErrClientInit) {
		t.Error("a marked failure does not match ErrClientInit")
	}
	if !errors.Is(marked, cause) {
		t.Error("a marked failure no longer wraps its cause")
	}
}

func TestLogProbeFailure_RecordsWhyWithoutTheProviderMessage(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logProbeFailure(slog.New(slog.NewTextHandler(&logs, nil)), "azure", &llm.Error{
		StatusCode: http.StatusNotFound,
		Type:       "invalid_request_error",
		Code:       "DeploymentNotFound",
		Message:    "provider body that must not be logged",
	})
	got := logs.String()
	for _, want := range []string{"test connection failed", "provider=azure", "upstreamStatus=404", "errorCode=DeploymentNotFound"} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "provider body") {
		t.Errorf("log carries the provider message: %s", got)
	}
}

func TestProbe_GrantModelIsNeverCalledWithAGrantToken(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer provider.Close()
	raw := map[string]any{"model": "openai/gpt-4o", "api_base": provider.URL, llm.GrantIDKey: "grant-1"}

	err := ProbeError(t.Context(), raw, slog.Default(), nil)

	if !errors.Is(err, ErrClientInit) {
		t.Errorf("err = %v, want ErrClientInit: no client may be built for a grant model here", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("the host named by the request received %d requests, want none", got)
	}
}

func TestProbe_UnreadableConfigNeverRendersTheParseError(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"model": "openai/gpt-4o", "api_key_file": "/nonexistent/sk-secret-path"}

	err := ProbeError(t.Context(), raw, slog.Default(), nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	ok, msg := Probe(t.Context(), raw, slog.Default(), nil)
	if ok || msg != probeInvalidConfigMessage {
		t.Errorf("Probe = %v, %q; want the fixed sentence %q, which quotes nothing from the config", ok, msg, probeInvalidConfigMessage)
	}
}

const answeredCompletion = `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

func TestProbe_StoredOutputLimitReachesTheWireAsStored(t *testing.T) {
	t.Parallel()
	var seenBody map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		mockllm.AnswerCompletion(w, raw, []byte(answeredCompletion))
	}))
	defer provider.Close()

	cfg := withStoredParams(newTestModelConfig(provider.URL, "sk-test"), map[string]any{"max_tokens": 4096})
	if ok, msg := Probe(t.Context(), cfg, slog.Default(), nil); !ok {
		t.Fatalf("Probe failed: %s", msg)
	}
	if got := seenBody["max_completion_tokens"]; got != 4096.0 {
		t.Errorf("max_completion_tokens = %v, want the stored 4096 an agent turn sends (body: %v)", got, seenBody)
	}
}

func TestProbe_StoredTemperatureTheModelRefusesFailsTheTest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if _, carries := body["temperature"]; carries {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'temperature' does not support 0.2 with this model. Only the default (1) value is supported.","type":"invalid_request_error","param":"temperature","code":"unsupported_value"}}`))
			return
		}
		mockllm.AnswerCompletion(w, raw, []byte(answeredCompletion))
	}))
	defer provider.Close()

	cfg := withStoredParams(newTestModelConfig(provider.URL, "sk-test"), map[string]any{"temperature": 0.2})
	err := ProbeError(t.Context(), cfg, slog.Default(), nil)
	if llmErr, ok := errors.AsType[*llm.Error](err); !ok || !llmErr.IsTemperatureUnsupportedError() {
		t.Fatalf("err = %v, want the provider's temperature refusal", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("provider received %d requests, want 1: a test neither drops the refused temperature nor retries", got)
	}
}

func TestAwaitAnswer_AStreamCutOffByTheDeadlineReportsTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	<-ctx.Done()
	cutOff := &llm.Error{Message: "stream cancelled", Type: "stream_cancelled"}
	stream := make(chan llm.StreamChunk, 2)
	stream <- llm.StreamChunk{ContentDelta: "It is"}
	stream <- llm.StreamChunk{Err: cutOff}
	close(stream)

	err := awaitAnswer(ctx, stream)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline the test ran out of", err)
	}
	if got, _ := errors.AsType[*llm.Error](err); got != cutOff {
		t.Errorf("err = %v, want it to keep the stream's own error for the failure log", err)
	}
}
