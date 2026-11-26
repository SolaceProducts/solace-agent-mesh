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
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
)

const html502 = `<html>
<head><title>502 Bad Gateway</title></head>
<body><center><h1>502 Bad Gateway</h1></center></body>
</html>`

func flakyHandler(failUntil int32, ok http.HandlerFunc) (http.HandlerFunc, *atomic.Int32) {
	var calls atomic.Int32
	h := func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= failUntil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(html502))
			return
		}
		ok(w, r)
	}
	return h, &calls
}

const html504 = `<html>
<head><title>504 Gateway Time-out</title></head>
<body><center><h1>504 Gateway Time-out</h1></center></body>
</html>`

func slowFailHandler(delay time.Duration, status int, body string) (http.HandlerFunc, *atomic.Int32) {
	var calls atomic.Int32
	h := func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(delay)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	return h, &calls
}

func slowStreamHandler(delay time.Duration, chunks []string) http.HandlerFunc {
	inner := streamHandler(chunks)
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		inner(w, r)
	}
}

func newRetryTestClient(t *testing.T, serverURL string, maxRetries *int) *BifrostClient {
	t.Helper()
	return newRetryTestClientWith(t, serverURL, BifrostConfig{MaxRetries: maxRetries})
}

func newRetryTestClientWith(t *testing.T, serverURL string, cfg BifrostConfig) *BifrostClient {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key-123")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg.Provider = "openai"
	cfg.Model = "test-model"
	cfg.BaseURL = serverURL

	client, err := NewBifrostClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Shutdown() })
	return client
}

type retryLogCapture struct {
	buf    *bytes.Buffer
	logger *slog.Logger
}

func newRetryLogCapture() *retryLogCapture {
	buf := &bytes.Buffer{}
	return &retryLogCapture{
		buf:    buf,
		logger: slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

func (c *retryLogCapture) records(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(c.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", line, err)
		}
		if rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

const attemptFailedMsg = "LLM call attempt failed"

func TestRetryAttemptsAreLogged(t *testing.T) {
	handler, _ := flakyHandler(1, nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	logs := newRetryLogCapture()
	client := newRetryTestClientWith(t, server.URL, BifrostConfig{
		MaxRetries: new(1),
		Logger:     logs.logger,
	})

	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}

	recs := logs.records(t, attemptFailedMsg)
	if len(recs) != 1 {
		t.Fatalf("got %d %q records, want 1 (one per failed attempt)", len(recs), attemptFailedMsg)
	}
	rec := recs[0]
	if rec["level"] != "WARN" {
		t.Errorf("level = %v, want WARN (a recovered retry is not an ERROR)", rec["level"])
	}
	for k, want := range map[string]any{
		"attempt":        float64(1),
		"maxAttempts":    float64(2),
		"upstreamStatus": float64(502),
		"willRetry":      true,
		"operation":      "chatCompletion",
		"provider":       "openai",
	} {
		if rec[k] != want {
			t.Errorf("%s = %v (%T), want %v", k, rec[k], rec[k], want)
		}
	}
	for _, k := range []string{"elapsed_ms", "retryDelay_ms", "retryBudget_ms"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing field %q; operators need it to see what a retry cost", k)
		}
	}
	if body := logs.buf.String(); strings.Contains(strings.ToLower(body), "<html") {
		t.Errorf("provider HTML body leaked into logs: %s", body)
	}
}

const budgetSeconds = 2

const slowerThanBudget = budgetSeconds*time.Second + 200*time.Millisecond

func TestRetryBudgetStopsRetryingSlowFailure(t *testing.T) {
	handler, calls := slowFailHandler(slowerThanBudget, http.StatusGatewayTimeout, html504)
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	logs := newRetryLogCapture()
	client := newRetryTestClientWith(t, server.URL, BifrostConfig{
		MaxRetries:         new(2),
		RetryBudgetSeconds: new(budgetSeconds),
		Logger:             logs.logger,
	})

	start := time.Now()
	_, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler called %d times, want 1 (elapsed time must deny the retry)", got)
	}
	if elapsed > 2*slowerThanBudget {
		t.Errorf("call took %v, want roughly one attempt (~%v)", elapsed, slowerThanBudget)
	}

	llmErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("expected *llm.Error, got %T", err)
	}
	if llmErr.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", llmErr.Attempts)
	}
	if llmErr.Elapsed < slowerThanBudget {
		t.Errorf("Elapsed = %v, want at least %v (the real cost of the failure)",
			llmErr.Elapsed, slowerThanBudget)
	}
	recs := logs.records(t, attemptFailedMsg)
	if len(recs) != 1 {
		t.Fatalf("got %d attempt records, want 1", len(recs))
	}
	if got := recs[0]["giveUpReason"]; got != "retryBudgetExhausted" {
		t.Errorf("giveUpReason = %v, want retryBudgetExhausted", got)
	}
}

func TestRetryExhaustsAttemptsWhenFailuresAreFast(t *testing.T) {
	handler, calls := slowFailHandler(0, http.StatusBadGateway, html502)
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	logs := newRetryLogCapture()
	client := newRetryTestClientWith(t, server.URL, BifrostConfig{
		MaxRetries:         new(2),
		RetryBudgetSeconds: new(budgetSeconds),
		Logger:             logs.logger,
	})

	_, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("handler called %d times, want 3 (initial + 2 retries)", got)
	}
	recs := logs.records(t, attemptFailedMsg)
	if len(recs) != 3 {
		t.Fatalf("got %d attempt records, want 3 (one per attempt)", len(recs))
	}
	if got := recs[len(recs)-1]["giveUpReason"]; got != "attemptsExhausted" {
		t.Errorf("final giveUpReason = %v, want attemptsExhausted", got)
	}
	llmErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("expected *llm.Error, got %T", err)
	}
	if llmErr.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", llmErr.Attempts)
	}
}

func TestHealthySlowCallSurvivesRetryBudget(t *testing.T) {
	t.Run("stream", func(t *testing.T) {
		handler, calls := flakyHandler(1, slowStreamHandler(slowerThanBudget,
			[]string{"Hello", ", ", "world", "!"}))
		server := mockOpenAIServer(t, handler)
		defer server.Close()

		client := newRetryTestClientWith(t, server.URL, BifrostConfig{
			MaxRetries:         new(1),
			RetryBudgetSeconds: new(budgetSeconds),
		})

		start := time.Now()
		stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
			Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
		})
		if err != nil {
			t.Fatalf("expected stream after retry, got %v", err)
		}
		var content strings.Builder
		for chunk := range stream {
			if chunk.Err != nil {
				t.Fatalf("unexpected stream error: %v", chunk.Err)
			}
			content.WriteString(chunk.ContentDelta)
		}
		if elapsed := time.Since(start); elapsed < slowerThanBudget {
			t.Fatalf("call took %v, under the %v budget — this test proves nothing unless "+
				"the succeeding attempt outlasts it", elapsed, budgetSeconds*time.Second)
		}
		if content.String() != "Hello, world!" {
			t.Errorf("content = %q, want %q (the budget must not truncate a healthy stream)",
				content.String(), "Hello, world!")
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("handler called %d times, want 2", got)
		}
	})

	t.Run("nonStream", func(t *testing.T) {
		handler, _ := flakyHandler(1, func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(slowerThanBudget)
			nonStreamHandler("recovered", "stop")(w, r)
		})
		server := mockOpenAIServer(t, handler)
		defer server.Close()

		client := newRetryTestClientWith(t, server.URL, BifrostConfig{
			MaxRetries:         new(1),
			RetryBudgetSeconds: new(budgetSeconds),
		})

		start := time.Now()
		resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
			Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
		})
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		if elapsed := time.Since(start); elapsed < slowerThanBudget {
			t.Fatalf("call took %v, under the %v budget — this test proves nothing unless "+
				"the succeeding attempt outlasts it", elapsed, budgetSeconds*time.Second)
		}
		if resp.Content != "recovered" {
			t.Errorf("content = %q, want %q", resp.Content, "recovered")
		}
	})
}

func TestStreamRetryBudgetStopsRetryingSlowFailure(t *testing.T) {
	handler, calls := slowFailHandler(slowerThanBudget, http.StatusGatewayTimeout, html504)
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	logs := newRetryLogCapture()
	client := newRetryTestClientWith(t, server.URL, BifrostConfig{
		MaxRetries:         new(2),
		RetryBudgetSeconds: new(budgetSeconds),
		Logger:             logs.logger,
	})

	_, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler called %d times, want 1 (the budget must deny the retry)", got)
	}
	recs := logs.records(t, attemptFailedMsg)
	if len(recs) != 1 {
		t.Fatalf("got %d attempt records, want 1", len(recs))
	}
	if got := recs[0]["operation"]; got != "chatCompletionStream" {
		t.Errorf("operation = %v, want chatCompletionStream", got)
	}
	if msg := UserFacingErrorMessage(err); strings.Contains(strings.ToLower(msg), "<html") {
		t.Errorf("user-facing message leaked provider HTML: %q", msg)
	}
}

func TestChatCompletionRetriesTransient502(t *testing.T) {
	handler, calls := flakyHandler(1, nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, new(1))

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("content = %q, want %q", resp.Content, "recovered")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("handler called %d times, want 2 (1 failure + 1 retry)", got)
	}
}

func TestChatCompletionNoRetryWhenDisabled(t *testing.T) {
	handler, calls := flakyHandler(1, nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, new(0))

	_, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error when retries disabled, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler called %d times, want 1 (no retry)", got)
	}
}

func TestChatCompletionStreamRetriesTransient502(t *testing.T) {
	handler, calls := flakyHandler(1, streamHandler([]string{"Hello", ", ", "world", "!"}))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, new(1))

	stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("expected stream after retry, got error: %v", err)
	}

	var content strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		content.WriteString(chunk.ContentDelta)
	}
	if content.String() != "Hello, world!" {
		t.Errorf("content = %q, want %q", content.String(), "Hello, world!")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("handler called %d times, want 2 (1 failure + 1 retry)", got)
	}
}

func TestIsRetryableBifrostError(t *testing.T) {
	status := func(c int) *int { return &c }
	str := func(s string) *string { return &s }

	tests := []struct {
		name string
		err  *schemas.BifrostError
		want bool
	}{
		{"nil", nil, false},
		{"429", &schemas.BifrostError{StatusCode: status(429)}, true},
		{"504", &schemas.BifrostError{StatusCode: status(504)}, true},
		{"529 overloaded, as Bifrost retries it", &schemas.BifrostError{StatusCode: status(529)}, true},
		{"400 not retryable", &schemas.BifrostError{StatusCode: status(400)}, false},
		{"501 not retryable (Bifrost's set, not >=500)", &schemas.BifrostError{StatusCode: status(501)}, false},
		{
			name: "synthesized error never retried even with a retryable status",
			err:  &schemas.BifrostError{IsBifrostError: true, StatusCode: status(504)},
			want: false,
		},
		{
			name: "caller cancellation never retried",
			err: &schemas.BifrostError{
				StatusCode: status(500),
				Error:      &schemas.ErrorField{Type: str(schemas.RequestCancelled)},
			},
			want: false,
		},
		{
			name: "network error by message, no status",
			err:  &schemas.BifrostError{Error: &schemas.ErrorField{Message: schemas.ErrProviderNetworkError}},
			want: true,
		},
		{
			name: "request-execution failure by message, no status",
			err:  &schemas.BifrostError{Error: &schemas.ErrorField{Message: schemas.ErrProviderDoRequest}},
			want: true,
		},
		{
			name: "rate limit sniffed from the message on a non-retryable status",
			err: &schemas.BifrostError{
				StatusCode: status(400),
				Error:      &schemas.ErrorField{Message: "Quota exceeded for this project"},
			},
			want: true,
		},
		{
			name: "rate limit sniffed from the code",
			err: &schemas.BifrostError{
				StatusCode: status(400),
				Error:      &schemas.ErrorField{Code: str("rate_limit_exceeded")},
			},
			want: true,
		},
		{
			name: "plain 4xx with a message is not retried",
			err: &schemas.BifrostError{
				StatusCode: status(400),
				Error:      &schemas.ErrorField{Message: "invalid tool schema"},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableBifrostError(tt.err); got != tt.want {
				t.Errorf("isRetryableBifrostError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGiveUpReason(t *testing.T) {
	cancelled := context.Canceled

	tests := []struct {
		name                             string
		retryable, exhausted, overBudget bool
		ctxErr                           error
		want                             string
	}{
		{name: "retryable with allowance and budget left", retryable: true, want: ""},
		{name: "not retryable", want: "notRetryable"},
		{name: "not retryable outranks a cancelled context", ctxErr: cancelled, want: "notRetryable"},
		{name: "cancelled context", retryable: true, ctxErr: cancelled, want: "contextDone"},
		{
			name:      "cancellation outranks a spent allowance",
			retryable: true, exhausted: true, ctxErr: cancelled, want: "contextDone",
		},
		{name: "allowance spent", retryable: true, exhausted: true, want: "attemptsExhausted"},
		{name: "over budget", retryable: true, overBudget: true, want: "retryBudgetExhausted"},
		{
			name:      "allowance spent outranks over budget",
			retryable: true, exhausted: true, overBudget: true, want: "attemptsExhausted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := giveUpReason(tt.retryable, tt.exhausted, tt.overBudget, tt.ctxErr)
			if got != tt.want {
				t.Errorf("giveUpReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNoRetryOnBadRequest(t *testing.T) {
	handler, calls := slowFailHandler(0, http.StatusBadRequest, `{"error":{"message":"bad"}}`)
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	logs := newRetryLogCapture()
	client := newRetryTestClientWith(t, server.URL, BifrostConfig{
		MaxRetries: new(2),
		Logger:     logs.logger,
	})

	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}); err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler called %d times, want 1 (a 4xx is not retryable)", got)
	}
	recs := logs.records(t, attemptFailedMsg)
	if len(recs) != 1 {
		t.Fatalf("got %d attempt records, want 1", len(recs))
	}
	if got := recs[0]["giveUpReason"]; got != "notRetryable" {
		t.Errorf("giveUpReason = %v, want notRetryable", got)
	}
}

func TestTransportFailureLogsClass(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedURL := "http://" + lis.Addr().String()
	_ = lis.Close()

	logs := newRetryLogCapture()
	client := newRetryTestClientWith(t, refusedURL, BifrostConfig{
		MaxRetries: new(0),
		Logger:     logs.logger,
	})

	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}); err == nil {
		t.Fatal("expected an error, got nil")
	}

	recs := logs.records(t, attemptFailedMsg)
	if len(recs) != 1 {
		t.Fatalf("got %d attempt records, want 1", len(recs))
	}
	rec := recs[0]
	if got := rec["upstreamStatus"]; got != float64(502) {
		t.Errorf("upstreamStatus = %v, want the synthesized 502", got)
	}
	class, _ := rec["failureClass"].(string)
	if class != schemas.ErrProviderDoRequest && class != schemas.ErrProviderNetworkError {
		t.Errorf("failureClass = %q, want a transport sentinel", class)
	}
}

func TestRetryAbandonedOnContextCancel(t *testing.T) {
	handler, calls := slowFailHandler(0, http.StatusBadGateway, html502)
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClientWith(t, server.URL, BifrostConfig{MaxRetries: new(5)})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler called %d times, want 1 (cancellation must stop the loop)", got)
	}
	if elapsed > time.Second {
		t.Errorf("call took %v; the backoff did not honor cancellation", elapsed)
	}
}

func TestStreamOpenErrorIsSynchronousWithoutBifrostRetries(t *testing.T) {
	handler, calls := flakyHandler(1, streamHandler([]string{"unused"}))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, new(0))

	stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatalf("expected a synchronous error, got a stream (chunk-channel delivery would break retry safety)")
	}
	if stream != nil {
		t.Errorf("stream = %v, want nil alongside a synchronous error", stream)
	}
	llmErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("expected *llm.Error, got %T: %v", err, err)
	}
	if llmErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want 502", llmErr.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler called %d times, want 1 (retries disabled)", got)
	}
}

func TestNonStream502CarriesStatusCode(t *testing.T) {
	handler, _ := flakyHandler(1, nonStreamHandler("unused", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, new(0))

	_, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	llmErr, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("expected *llm.Error, got %T: %v", err, err)
	}
	if llmErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want 502", llmErr.StatusCode)
	}
	if !llmErr.IsRetryable() {
		t.Errorf("IsRetryable() = false, want true for a 502")
	}
}

func TestOAuth2TokenRefreshedBeforeRetry(t *testing.T) {
	var tokenFetches atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := tokenFetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"tok-%d","expires_in":1}`, n)
	}))
	defer tokenSrv.Close()

	const firstAttemptDuration = 1100 * time.Millisecond
	var mu sync.Mutex
	var seenAuth []string
	handler, _ := flakyHandler(1, nonStreamHandler("recovered", "stop"))
	llmSrv := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		first := len(seenAuth) == 1
		mu.Unlock()
		if first {
			time.Sleep(firstAttemptDuration)
		}
		handler(w, r)
	})
	defer llmSrv.Close()

	client := newRetryTestClientWith(t, llmSrv.URL, BifrostConfig{
		MaxRetries:         new(1),
		RetryBudgetSeconds: new(30),
		OAuth2: &oauth2cc.Config{
			TokenURL:              tokenSrv.URL,
			ClientID:              "client",
			ClientSecret:          "secret",
			AllowInsecureTokenURL: true,
		},
	})

	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenAuth) != 2 {
		t.Fatalf("upstream saw %d attempts, want 2", len(seenAuth))
	}
	if seenAuth[0] == "" || seenAuth[1] == "" {
		t.Fatalf("an attempt carried no Authorization header: %q", seenAuth)
	}
	if seenAuth[0] == seenAuth[1] {
		t.Errorf("the retry reused the expired token (%d token fetches)", tokenFetches.Load())
	}
}

func TestChatCompletionReportsTheAttemptsASuccessfulCallTook(t *testing.T) {
	handler, _ := flakyHandler(1, nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()
	client := newRetryTestClient(t, server.URL, new(1))

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if resp.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2 (1 failure + 1 success)", resp.Attempts)
	}
}

func TestChatCompletionStreamChunksReportTheAttemptsTheStreamTook(t *testing.T) {
	handler, _ := flakyHandler(1, streamHandler([]string{"Hello", " world"}))
	server := mockOpenAIServer(t, handler)
	defer server.Close()
	client := newRetryTestClient(t, server.URL, new(1))

	stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	var last StreamChunk
	chunks := 0
	for chunk := range stream {
		last = chunk
		chunks++
	}
	if chunks == 0 || last.Attempts != 2 {
		t.Errorf("final chunk Attempts = %d over %d chunks, want 2", last.Attempts, chunks)
	}
}

func rateLimitedHandler(failUntil int32, retryAfter string, ok http.HandlerFunc) (http.HandlerFunc, *atomic.Int32) {
	var calls atomic.Int32
	h := func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= failUntil {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Rate limit exceeded"}`))
			return
		}
		ok(w, r)
	}
	return h, &calls
}

func shortenRateLimitBackoff(t *testing.T) {
	t.Helper()
	saved := rateLimitBackoff
	rateLimitBackoff.Initial, rateLimitBackoff.Max = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { rateLimitBackoff = saved })
}

func TestRateLimitedCallGetsMoreRetries(t *testing.T) {
	shortenRateLimitBackoff(t)
	handler, calls := rateLimitedHandler(defaultLLMMaxRetries+1, "", nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if err != nil {
		t.Fatalf("expected success once the rate limit lifted, got %v", err)
	}
	if resp.Content != "recovered" || calls.Load() != defaultLLMMaxRetries+2 {
		t.Errorf("content = %q after %d calls, want %q after %d", resp.Content, calls.Load(), "recovered", defaultLLMMaxRetries+2)
	}
}

func TestRateLimitedCallWaitsForRetryAfter(t *testing.T) {
	shortenRateLimitBackoff(t)
	handler, calls := rateLimitedHandler(1, "1", nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	start := time.Now()
	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}); err != nil {
		t.Fatalf("expected success after the hinted wait, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < time.Second || elapsed > 5*time.Second {
		t.Errorf("succeeded after %v, want about 1 s (the Retry-After)", elapsed)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("handler called %d times, want 2", got)
	}
}

func TestRateLimitedCallGivesUpWhenTheHintOutlastsTheBudget(t *testing.T) {
	shortenRateLimitBackoff(t)
	handler, calls := rateLimitedHandler(10, "30", nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClientWith(t, server.URL, BifrostConfig{RetryBudgetSeconds: new(5)})

	start := time.Now()
	_, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if err == nil {
		t.Fatal("expected the rate limit to fail the call")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second || calls.Load() != 1 {
		t.Errorf("gave up after %v and %d calls, want at once after 1 call", elapsed, calls.Load())
	}
	if msg := UserFacingErrorMessage(err); !strings.HasPrefix(msg, RateLimitedMessagePrefix) {
		t.Errorf("user-facing message = %q, want the rate-limit wording", msg)
	}
	if llmErr, ok := errors.AsType[*Error](err); !ok || llmErr.Attempts != 1 {
		t.Errorf("err = %#v, want an *Error recording 1 attempt", err)
	}
}

func TestRateLimitedCallGivesUpWhenTheHintOutlastsTheDeadline(t *testing.T) {
	shortenRateLimitBackoff(t)
	handler, calls := rateLimitedHandler(10, "30", nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.ChatCompletion(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if llmErr, ok := errors.AsType[*Error](err); !ok || !llmErr.IsRateLimited() {
		t.Fatalf("err = %v, want the rate limit", err)
	}
	if ctx.Err() != nil || calls.Load() != 1 {
		t.Errorf("gave up after %d calls with ctx.Err() = %v, want at once after 1 call, deadline unspent", calls.Load(), ctx.Err())
	}
}

func sequenceHandler(statuses []int, body string, ok http.HandlerFunc) (http.HandlerFunc, *atomic.Int32) {
	var calls atomic.Int32
	h := func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		if n <= len(statuses) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statuses[n-1])
			_, _ = w.Write([]byte(body))
			return
		}
		ok(w, r)
	}
	return h, &calls
}

func TestOverQuotaCallKeepsTheGenericRetries(t *testing.T) {
	shortenRateLimitBackoff(t)
	statuses := slices.Repeat([]int{http.StatusTooManyRequests}, 10)
	handler, calls := sequenceHandler(statuses,
		`{"error":{"code":"insufficient_quota","type":"insufficient_quota","message":"You exceeded your current quota"}}`,
		nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	_, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if err == nil {
		t.Fatal("expected the exhausted quota to fail the call")
	}
	if got := calls.Load(); got != defaultLLMMaxRetries+1 {
		t.Errorf("handler called %d times, want %d (the generic allowance)", got, defaultLLMMaxRetries+1)
	}
	if msg := UserFacingErrorMessage(err); strings.HasPrefix(msg, RateLimitedMessagePrefix) {
		t.Errorf("user-facing message = %q, want the provider's quota wording, not a rate limit", msg)
	}
}

func TestRateLimitedCallRunsToItsAllowance(t *testing.T) {
	shortenRateLimitBackoff(t)
	const wantAttempts = 6
	handler, calls := rateLimitedHandler(math.MaxInt32, "", nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	_, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	llmErr, ok := errors.AsType[*Error](err)
	if !ok || !llmErr.IsRateLimited() || llmErr.Attempts != wantAttempts {
		t.Fatalf("err = %#v, want the rate limit after %d attempts", err, wantAttempts)
	}
	if got := calls.Load(); got != wantAttempts {
		t.Errorf("handler called %d times, want %d", got, wantAttempts)
	}
}

func TestRateLimitedCallLeavesTheSharedBackoffUntouched(t *testing.T) {
	shortenRateLimitBackoff(t)
	before := rateLimitBackoff
	handler, _ := rateLimitedHandler(3, "", nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}); err != nil {
		t.Fatalf("expected success once the rate limit lifted, got %v", err)
	}
	if rateLimitBackoff != before {
		t.Errorf("rateLimitBackoff = %+v after the call, want it unchanged (%+v)", rateLimitBackoff, before)
	}
}

func TestRateLimitedCallCountsElapsedTimeAgainstTheBudget(t *testing.T) {
	saved := rateLimitBackoff
	rateLimitBackoff = backoff.Exponential{Initial: 400 * time.Millisecond, Max: 400 * time.Millisecond}
	t.Cleanup(func() { rateLimitBackoff = saved })
	handler, calls := rateLimitedHandler(100, "", nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClientWith(t, server.URL, BifrostConfig{RetryBudgetSeconds: new(1)})

	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}); err == nil {
		t.Fatal("expected the rate limit to fail the call")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("handler called %d times, want 3 (the third wait would end past the 1 s budget)", got)
	}
}

func TestCodelessQuotaCallKeepsTheGenericRetries(t *testing.T) {
	statuses := slices.Repeat([]int{http.StatusTooManyRequests}, 10)
	handler, calls := sequenceHandler(statuses,
		`{"error":{"message":"You exceeded your current quota, please check your plan and billing details."}}`,
		nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	_, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if err == nil {
		t.Fatal("expected the exhausted quota to fail the call")
	}
	if got := calls.Load(); got != defaultLLMMaxRetries+1 {
		t.Errorf("handler called %d times, want %d (the generic allowance)", got, defaultLLMMaxRetries+1)
	}
	if msg := UserFacingErrorMessage(err); strings.HasPrefix(msg, RateLimitedMessagePrefix) {
		t.Errorf("user-facing message = %q, want the provider's quota wording, not a rate limit", msg)
	}
}

func TestRateLimitedCallWaitsOnTheRateLimitBackoff(t *testing.T) {
	handler, calls := rateLimitedHandler(1, "", nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	start := time.Now()
	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}); err != nil {
		t.Fatalf("expected success after one rate-limit wait, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond || elapsed > 5*time.Second {
		t.Errorf("succeeded after %v, want the 2 s rate-limit step within its jitter", elapsed)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("handler called %d times, want 2", got)
	}
}

func TestRateLimitWordingOnA503GetsTheRateLimitRetries(t *testing.T) {
	shortenRateLimitBackoff(t)
	statuses := slices.Repeat([]int{http.StatusServiceUnavailable}, defaultLLMMaxRetries+1)
	handler, calls := sequenceHandler(statuses, `{"error":{"message":"Rate limit exceeded, slow down"}}`,
		nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}); err != nil {
		t.Fatalf("expected success once the rate limit lifted, got %v", err)
	}
	if got := calls.Load(); got != defaultLLMMaxRetries+2 {
		t.Errorf("handler called %d times, want %d", got, defaultLLMMaxRetries+2)
	}
}

func TestRateLimitedCallKeepsItsAllowanceThroughAnotherFailure(t *testing.T) {
	shortenRateLimitBackoff(t)
	tooMany, unavailable := http.StatusTooManyRequests, http.StatusServiceUnavailable
	statuses := []int{tooMany, tooMany, tooMany, unavailable, unavailable}
	handler, calls := sequenceHandler(statuses, `{"error":{"message":"busy"}}`, nonStreamHandler("recovered", "stop"))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	if _, err := client.ChatCompletion(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}); err != nil {
		t.Fatalf("expected success on the sixth call, got %v", err)
	}
	if got := calls.Load(); got != int32(len(statuses)+1) {
		t.Errorf("handler called %d times, want %d", got, len(statuses)+1)
	}
}

func TestChatCompletionStreamRetriesRateLimit(t *testing.T) {
	shortenRateLimitBackoff(t)
	handler, calls := rateLimitedHandler(defaultLLMMaxRetries+1, "", streamHandler([]string{"Hello", "!"}))
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newRetryTestClient(t, server.URL, nil)

	stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if err != nil {
		t.Fatalf("expected a stream once the rate limit lifted, got %v", err)
	}
	var content strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		content.WriteString(chunk.ContentDelta)
	}
	if content.String() != "Hello!" || calls.Load() != defaultLLMMaxRetries+2 {
		t.Errorf("content = %q after %d calls, want %q after %d", content.String(), calls.Load(), "Hello!", defaultLLMMaxRetries+2)
	}
}

func TestRateLimitDelay(t *testing.T) {
	bo := backoff.Exponential{Initial: 2 * time.Second, Max: 30 * time.Second, Jitter: 0.2}
	if got := rateLimitDelay(0, &bo); got < 1600*time.Millisecond || got > 2400*time.Millisecond {
		t.Errorf("no hint: delay = %v, want the 2 s backoff step within its jitter", got)
	}
	if got := rateLimitDelay(10*time.Second, &bo); got < 10*time.Second || got > 12*time.Second {
		t.Errorf("10 s hint: delay = %v, want at least the hint plus at most 20%% jitter", got)
	}
	if got := rateLimitDelay(time.Second, &bo); got < 6400*time.Millisecond || got > 9600*time.Millisecond {
		t.Errorf("1 s hint under an 8 s step: delay = %v, want the step", got)
	}
}

func TestErrorIsRateLimited(t *testing.T) {
	tests := []struct {
		name string
		err  Error
		want bool
	}{
		{"429 with a neutral message", Error{StatusCode: 429, Message: "slow down"}, true},
		{"400 naming a rate limit only in its message", Error{StatusCode: 400, Message: "Rate limit exceeded"}, false},
		{"400 typed as a throttle (Bedrock)", Error{StatusCode: 400, Type: "ThrottlingException", Message: "Rate exceeded"}, true},
		{"400 schema naming a rate-limit tool", Error{StatusCode: 400, Message: "Invalid schema for function 'get_rate_limit_status': 'additionalProperties' is required"}, false},
		{"400 batch limit", Error{StatusCode: 400, Message: "Invalid 'batch': at most 100 requests per batch"}, false},
		{"422 batch limit", Error{StatusCode: 422, Message: "too many requests in batch; limit exceeded"}, false},
		{"429 request larger than the per-minute limit", Error{StatusCode: 429, Code: "rate_limit_exceeded",
			Message: "Request too large for gpt-4o in organization org-abc on tokens per min (TPM): Limit 30000, Requested 45000. The input or output tokens must be reduced in order to run successfully."}, false},
		{"typed 400 schema naming a rate-limit tool", Error{StatusCode: 400, Type: "invalid_request_error",
			Message: "Invalid schema for function 'get_rate_limit_status': 'additionalProperties' is required"}, false},
		{"typed 422 batch limit", Error{StatusCode: 422, Type: "invalid_request_error", Message: "too many requests in batch"}, false},
		{"4xx typed as a rate limit with no throttle words", Error{StatusCode: 400, Type: "rate_limit_error", Message: "slow down"}, true},
		{"Groq 413 request larger than the per-minute limit", Error{StatusCode: 413, Code: "rate_limit_exceeded",
			Message: "Request too large for model on tokens per minute (TPM): Limit 6000, Requested 9000, please reduce your message size"}, false},
		{"429 naming only 'request too large'", Error{StatusCode: 429, Message: "Request too large for gpt-4o on tokens per min (TPM)"}, false},
		{"429 naming only 'must be reduced'", Error{StatusCode: 429, Message: "Rate limit: the input or output tokens must be reduced"}, false},
		{"LiteLLM-wrapped exhausted quota", Error{StatusCode: 429, Code: "429",
			Message: "litellm.RateLimitError: RateLimitError: OpenAIException - Error code: 429 - {'error': {'message': 'You exceeded your current quota, please check your plan and billing details.', 'type': 'insufficient_quota'}}"}, false},
		{"LiteLLM-wrapped budget", Error{StatusCode: 429, Message: "litellm.RateLimitError: Budget has been exceeded! Current cost: 5.0, Max budget: 5.0"}, false},
		{"LiteLLM-wrapped credits", Error{StatusCode: 429, Message: "litellm.RateLimitError: Insufficient credits. Please purchase more credits"}, false},
		{"Gemini quota with a retry hint keeps the generic retries", Error{StatusCode: 429, Type: "RESOURCE_EXHAUSTED",
			Message: "You exceeded your current quota. Quota exceeded for metric: generate_content_free_tier_requests, limit: 15. Please retry in 51.2s."}, false},
		{"429 per-minute token limit", Error{StatusCode: 429, Code: "rate_limit_exceeded",
			Message: "Rate limit reached for gpt-4o in organization org-abc on tokens per min (TPM): Limit 30000, Used 29000, Requested 2000. Please try again in 2s."}, true},
		{"500 naming a rate limit", Error{StatusCode: 500, Message: "Rate limit exceeded"}, true},
		{"no status, naming a rate limit", Error{Message: "Rate limit exceeded"}, true},
		{"500 with a neutral message", Error{StatusCode: 500, Message: "Internal server error"}, false},
		{"429 over quota by code", Error{StatusCode: 429, Code: "insufficient_quota", Message: "You exceeded your current quota"}, false},
		{"429 over a credit balance", Error{StatusCode: 429, Message: "Your credit balance is too low"}, false},
		{"429 over the context window", Error{StatusCode: 429, Code: "context_length_exceeded", Message: "Too many tokens"}, false},
		{"401 naming a rate limit is the key", Error{StatusCode: 401, Message: "Rate limit exceeded"}, false},
		{"429 over quota without a code", Error{StatusCode: 429, Message: "You exceeded your current quota, please check your plan and billing details."}, false},
		{"429 over a proxy budget", Error{StatusCode: 429, Message: "Budget has been exceeded! Current cost: 5.0, Max budget: 5.0"}, false},
		{"429 over a balance", Error{StatusCode: 429, Message: "Insufficient balance"}, false},
		{"429 over a daily quota", Error{StatusCode: 429, Message: "Quota exceeded for metric: generate_content_requests, limit: 1500 PerDay"}, false},
		{"429 over a per-minute quota keeps the generic retries", Error{StatusCode: 429, Message: "Quota exceeded for metric: generate_content_requests, limit: 15 per minute"}, false},
		{"Gemini zero quota with a retry hint", Error{StatusCode: 429, Type: "RESOURCE_EXHAUSTED",
			Message: "You exceeded your current quota. Quota exceeded for metric: generate_content_free_tier_input_token_count, limit: 0, model: gemini-2.5-pro. Please retry in 20.4s."}, false},
		{"credits with a retry hint", Error{StatusCode: 429, Message: "Insufficient credits. Rate limit; please retry in 10s"}, false},
		{"budget with a retry hint", Error{StatusCode: 429, Message: "Budget has been exceeded! Current cost: 5.0. Try again in 5s"}, false},
		{"balance per minute", Error{StatusCode: 429, Message: "Insufficient balance per minute"}, false},
		{"account named only in the type", Error{StatusCode: 429, Type: "ExceededBudget", Message: "slow down"}, false},
		{"account named only in the code", Error{StatusCode: 429, Code: "billing_hard_limit_reached", Message: "Rate limit"}, false},
		{"500 naming a rate limit and a balance", Error{StatusCode: 500, Message: "rate limit: insufficient balance"}, false},
		{"no status, naming a rate limit and credits", Error{Message: "rate limit exceeded: insufficient credits"}, false},
		{"400 size limit", Error{StatusCode: 400, Message: "Request size limit exceeded: max 20MB"}, false},
		{"413 payload limit", Error{StatusCode: 413, Message: "Payload limit exceeded"}, false},
		{"400 tool count limit", Error{StatusCode: 400, Message: "tools: limit exceeded, at most 128 tools"}, false},
		{"500 usage limit", Error{StatusCode: 500, Message: "internal error: usage limit"}, false},
		{"no status, a disk quota", Error{Message: "disk quota exceeded"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.IsRateLimited(); got != tt.want {
				t.Errorf("IsRateLimited() = %v, want %v", got, tt.want)
			}
		})
	}
}
