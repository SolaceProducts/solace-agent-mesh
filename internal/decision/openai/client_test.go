// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

var triageQuestions = []decision.Question{
	{
		Key:          "department",
		Type:         decision.TypeChoice,
		Instructions: "Which team owns the ticket?",
		Criteria: []decision.Criterion{
			{Key: "billing", Description: "Payment issues"},
			{Key: "technical", Description: "Bugs"},
			{Key: "other"},
		},
	},
	{
		Key:          "urgent",
		Type:         decision.TypeBoolean,
		Instructions: "Is the ticket urgent?",
	},
	{
		Key:          "severity",
		Type:         decision.TypeScore,
		Instructions: "How badly is the customer blocked?",
		Criteria: []decision.Criterion{
			{Key: "Cosmetic", Description: "Looks wrong, works"},
			{Key: "Workaround available"},
			{Key: "Fully blocked"},
		},
	},
}

func goldenResponse(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/decisions_response.json")
	if err != nil {
		t.Fatalf("read golden response: %v", err)
	}
	return raw
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.transport.Backoff = time.Millisecond
	return c
}

func triageRequest() decision.Request {
	return decision.Request{
		Model:     "gpt-6-luna",
		State:     map[string]any{"ticket": "my card was charged twice", "account": "a-17"},
		Questions: triageQuestions,
	}
}

func TestEvaluateSendsTheWireShape(t *testing.T) {
	var gotBody []byte
	var gotAuth, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write(goldenResponse(t))
	})
	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gotPath != "/v1/decisions" {
		t.Errorf("path = %q, want /v1/decisions", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	var got map[string]any
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("request body: %v", err)
	}
	want := map[string]any{
		"model": "gpt-6-luna",
		"input": `{"account":"a-17","ticket":"my card was charged twice"}`,
		"questions": []any{
			map[string]any{
				"type": "choice", "name": "department", "instructions": "Which team owns the ticket?",
				"choices": []any{
					map[string]any{"value": "billing", "description": "Payment issues"},
					map[string]any{"value": "technical", "description": "Bugs"},
					map[string]any{"value": "other"},
				},
			},
			map[string]any{"type": "predicate", "name": "urgent", "instructions": "Is the ticket urgent?"},
			map[string]any{
				"type": "score", "name": "severity", "instructions": "How badly is the customer blocked?",
				"levels": []any{
					map[string]any{"label": "Cosmetic", "description": "Looks wrong, works"},
					map[string]any{"label": "Workaround available"},
					map[string]any{"label": "Fully blocked"},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request body =\n%s\nwant\n%v", gotBody, want)
	}
}

func TestEncodePassesAStringStateThrough(t *testing.T) {
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := c.encode(decision.Request{State: "plain text ticket", Questions: triageQuestions[1:2]})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got wireRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Input != "plain text ticket" {
		t.Errorf("input = %q, want the string unchanged", got.Input)
	}
	if got.Model != DefaultModel {
		t.Errorf("model = %q, want the default %q", got.Model, DefaultModel)
	}
}

func TestEncodeFoldsBooleanCriteriaIntoInstructions(t *testing.T) {
	cases := map[string]struct {
		criteria []decision.Criterion
		want     string
	}{
		"none": {want: "Is it urgent?"},
		"both": {
			criteria: []decision.Criterion{{Key: "true", Description: "the customer is blocked."}, {Key: "false", Description: "it can wait"}},
			want:     "Is it urgent? True when: the customer is blocked. False when: it can wait.",
		},
		"true only": {
			criteria: []decision.Criterion{{Key: "true", Description: "money is lost"}},
			want:     "Is it urgent? True when: money is lost.",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			q := decision.Question{Key: "urgent", Type: decision.TypeBoolean, Instructions: "Is it urgent?", Criteria: tc.criteria}
			wq, err := encodeQuestion(q)
			if err != nil {
				t.Fatalf("encodeQuestion: %v", err)
			}
			if wq.Type != "predicate" || wq.Instructions != tc.want {
				t.Errorf("got %q %q, want predicate %q", wq.Type, wq.Instructions, tc.want)
			}
		})
	}
}

func TestEncodeRejectsAnUnknownBooleanCriterion(t *testing.T) {
	q := decision.Question{Key: "urgent", Type: decision.TypeBoolean, Instructions: "x", Criteria: []decision.Criterion{{Key: "maybe", Description: "d"}}}
	if _, err := encodeQuestion(q); err == nil {
		t.Fatal("want an error for a criterion other than true/false")
	}
}

func TestEvaluateDecodesTheRecordedResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(goldenResponse(t))
	})
	result, err := c.Evaluate(t.Context(), triageRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := result.Validate(triageQuestions); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.Model != "gpt-6-luna" || result.Usage != (decision.Usage{InputTokens: 403}) {
		t.Errorf("model/usage = %q %+v", result.Model, result.Usage)
	}
	department := result.Answers["department"]
	if department.Choice != "billing" || *department.Confidence != 0.99 ||
		!reflect.DeepEqual(department.Probabilities, map[string]float64{"billing": 0.99, "technical": 0, "other": 0.01}) {
		t.Errorf("department = %+v", department)
	}
	urgent := result.Answers["urgent"]
	if urgent.Type != decision.TypeBoolean || urgent.Probability != 0.46 || urgent.Output()["value"] != false {
		t.Errorf("urgent = %+v", urgent)
	}
	severity := result.Answers["severity"]
	wantLegend := map[string]any{"0": "Cosmetic", "1": "Workaround available", "2": "Fully blocked"}
	wantProbabilities := map[string]float64{"Cosmetic": 0.04, "Workaround available": 0.42, "Fully blocked": 0.54}
	if severity.Score != 1.5 || *severity.Confidence != 0.25 ||
		!reflect.DeepEqual(severity.Legend, wantLegend) || !reflect.DeepEqual(severity.Probabilities, wantProbabilities) {
		t.Errorf("severity = %+v", severity)
	}
}

func TestEvaluateDropsProbabilitiesForOptionsNotAsked(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":[{"type":"choice","name":"department","choice":"billing",
			"probabilities":[{"value":"billing","probability":0.9},{"value":"invented","probability":0.1}],"confidence":0.9}]}`)
	})
	result, err := c.Evaluate(t.Context(), decision.Request{State: "x", Questions: triageQuestions[:1]})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if _, ok := result.Answers["department"].Probabilities["invented"]; ok {
		t.Error("a probability for an option the question did not ask reached the result")
	}
}

func TestEvaluateRejectsMalformedAnswers(t *testing.T) {
	cases := map[string]string{
		"not json":              `{`,
		"unknown type":          `{"answers":[{"type":"ranking","name":"urgent"}]}`,
		"predicate no value":    `{"answers":[{"type":"predicate","name":"urgent"}]}`,
		"choice no choice":      `{"answers":[{"type":"choice","name":"department"}]}`,
		"score no score":        `{"answers":[{"type":"score","name":"severity"}]}`,
		"score null index":      `{"answers":[{"type":"score","name":"severity","score":1,"probabilities":[{"value":null,"probability":1}]}]}`,
		"score repeated index":  `{"answers":[{"type":"score","name":"severity","score":0,"probabilities":[{"value":0,"probability":0.5},{"value":0,"probability":0.5}]}]}`,
		"choice null value":     `{"answers":[{"type":"choice","name":"department","choice":"billing","probabilities":[{"value":null,"probability":1}]}]}`,
		"choice repeated value": `{"answers":[{"type":"choice","name":"department","choice":"billing","probabilities":[{"value":"billing","probability":0.5},{"value":"billing","probability":0.5}]}]}`,
		"score label mismatch":  `{"answers":[{"type":"score","name":"severity","score":1,"probabilities":[{"value":0,"label":"Nope","probability":1}]}]}`,
		"score index too high":  `{"answers":[{"type":"score","name":"severity","score":1,"probabilities":[{"value":3,"label":"Fully blocked","probability":1}]}]}`,
		"score index a string":  `{"answers":[{"type":"score","name":"severity","score":1,"probabilities":[{"value":"0","probability":1}]}]}`,
		"choice numeric value":  `{"answers":[{"type":"choice","name":"department","choice":"billing","probabilities":[{"value":3,"probability":1}]}]}`,
		"duplicate answer":      `{"answers":[{"type":"predicate","name":"urgent","probability":0.1},{"type":"predicate","name":"urgent","probability":0.2}]}`,
		"choice no probability": `{"answers":[{"type":"choice","name":"department","choice":"billing","probabilities":[{"value":"billing"}]}]}`,
		"score no probability":  `{"answers":[{"type":"score","name":"severity","score":0,"probabilities":[{"value":0,"label":"Cosmetic"}]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, body)
			})
			_, err := c.Evaluate(t.Context(), triageRequest())
			if kind := decision.Classify(err); kind != decision.FailureInvalidResponse {
				t.Errorf("kind = %q (err %v), want invalid_response", kind, err)
			}
			if calls.Load() != 1 {
				t.Errorf("calls = %d, want 1: an undecodable answer is not retried", calls.Load())
			}
		})
	}
}

func TestEvaluateFailsTheCallOnARefusal(t *testing.T) {
	const refusalText = "I cannot help with SECRET-CANARY-7731 in this ticket"
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":[
			{"type":"predicate","name":"urgent","probability":0.9},
			{"type":"refusal","name":"department","refusal":"`+refusalText+`"}]}`)
	})
	_, err := c.Evaluate(t.Context(), triageRequest())
	if kind := decision.Classify(err); kind != decision.FailureRefused {
		t.Fatalf("kind = %q (err %v), want refused", kind, err)
	}
	if decision.FailureRefused.Permanent() {
		t.Error("a refusal must be escalatable, not permanent")
	}
	if !strings.Contains(err.Error(), `"department"`) {
		t.Errorf("error %q does not name the refused question", err)
	}

	unasked := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"answers":[{"type":"refusal","name":"MODEL-INVENTED-NAME","refusal":"no"}]}`)
	})
	_, err = unasked.Evaluate(t.Context(), triageRequest())
	if decision.Classify(err) != decision.FailureRefused || strings.Contains(err.Error(), "MODEL-INVENTED-NAME") {
		t.Errorf("err = %v, want a refusal that does not repeat a name the node never asked", err)
	}
	if strings.Contains(err.Error(), "SECRET-CANARY") {
		t.Errorf("error %q carries the refusal text", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1: a refusal is not retried", calls.Load())
	}
}

func TestARefusalTheProviderBilledReportsItsUsage(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":[{"type":"refusal","name":"department","refusal":"no"}],
			"usage":{"input_tokens":40,"output_tokens":3}}`)
	})
	res, err := c.Evaluate(t.Context(), triageRequest())
	if decision.Classify(err) != decision.FailureRefused {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if want := (decision.Usage{InputTokens: 40, OutputTokens: 3}); res.Usage != want || res.Model != "gpt-6-luna" || len(res.Answers) != 0 {
		t.Errorf("result = %+v, want gpt-6-luna billed %+v with no answers", res, want)
	}
}

func TestEvaluateClassifiesStatuses(t *testing.T) {
	cases := map[string]struct {
		status    int
		body      string
		wantKind  decision.FailureKind
		wantCalls int32
	}{
		"401":                      {http.StatusUnauthorized, `{}`, decision.FailureUnauthorized, 1},
		"403":                      {http.StatusForbidden, `{}`, decision.FailureUnauthorized, 1},
		"no credits":               {http.StatusTooManyRequests, `{"error":{"message":"no credits","type":"insufficient_quota","code":"credit_balance_exhausted"}}`, decision.FailureConfig, 1},
		"insufficient quota code":  {http.StatusTooManyRequests, `{"error":{"code":"insufficient_quota"}}`, decision.FailureConfig, 1},
		"credit balance code only": {http.StatusTooManyRequests, `{"error":{"code":"credit_balance_exhausted"}}`, decision.FailureConfig, 1},
		"insufficient quota type":  {http.StatusTooManyRequests, `{"error":{"type":"insufficient_quota"}}`, decision.FailureConfig, 1},
		"rate limited":             {http.StatusTooManyRequests, `{"error":{"type":"requests","code":"rate_limit_exceeded"}}`, decision.FailureRateLimited, decision.MaxAttempts},
		"context length":           {http.StatusBadRequest, `{"error":{"code":"context_length_exceeded"}}`, decision.FailureInputTooLarge, 1},
		"413":                      {http.StatusRequestEntityTooLarge, ``, decision.FailureInputTooLarge, 1},
		"invalid request":          {http.StatusBadRequest, `{"error":{"type":"invalid_request_error","param":"questions[0].type"}}`, decision.FailureConfig, 1},
		"404":                      {http.StatusNotFound, `{}`, decision.FailureConfig, 1},
		"422":                      {http.StatusUnprocessableEntity, `{}`, decision.FailureConfig, 1},
		"408":                      {http.StatusRequestTimeout, ``, decision.FailureTimeout, decision.MaxAttempts},
		"500":                      {http.StatusInternalServerError, ``, decision.FailureUnreachable, decision.MaxAttempts},
		"502":                      {http.StatusBadGateway, ``, decision.FailureUnreachable, decision.MaxAttempts},
		"503":                      {http.StatusServiceUnavailable, ``, decision.FailureUnreachable, decision.MaxAttempts},
		"504":                      {http.StatusGatewayTimeout, ``, decision.FailureUnreachable, decision.MaxAttempts},
		"undecodable error body":   {http.StatusTooManyRequests, `<html>`, decision.FailureRateLimited, decision.MaxAttempts},
		"context length not a 400": {http.StatusForbidden, `{"error":{"code":"context_length_exceeded"}}`, decision.FailureUnauthorized, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := c.Evaluate(t.Context(), triageRequest())
			if kind := decision.Classify(err); kind != tc.wantKind {
				t.Errorf("kind = %q (err %v), want %q", kind, err, tc.wantKind)
			}
			if calls.Load() != tc.wantCalls {
				t.Errorf("calls = %d, want %d", calls.Load(), tc.wantCalls)
			}
		})
	}
}

func TestEvaluateKeepsTheErrorMessageOutOfTheError(t *testing.T) {
	const canary = "ERROR-BODY-CANARY-5521"
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"`+canary+`","type":"invalid_request_error"}}`)
	})
	_, err := c.Evaluate(t.Context(), triageRequest())
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Errorf("err = %v; the provider's error message must not reach it", err)
	}
}

func TestEvaluateRetriesARateLimitThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write(goldenResponse(t))
	})
	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestEvaluateWaitsOutTheRateLimitWindow(t *testing.T) {
	cases := map[string]http.Header{
		"token reset":    {"X-Ratelimit-Reset-Tokens": {"300ms"}},
		"requests reset": {"X-Ratelimit-Reset-Requests": {"300ms"}},
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			var first time.Time
			var gap time.Duration
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					first = time.Now()
					for k, v := range header {
						w.Header()[k] = v
					}
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				gap = time.Since(first)
				_, _ = w.Write(goldenResponse(t))
			})
			if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if gap < 300*time.Millisecond {
				t.Errorf("retried after %s, want at least the 300ms window the 429 named", gap)
			}
		})
	}
}

func TestEvaluateDoesNotRetryAKeyNoHeaderCanCarry(t *testing.T) {
	var buf bytes.Buffer
	c, err := New(Config{APIKey: "sk-bad\nkey"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := triageRequest()
	req.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	_, err = c.Evaluate(t.Context(), req)
	if decision.Classify(err) != decision.FailureConfig {
		t.Fatalf("err = %v, want a config failure", err)
	}
	if strings.Contains(buf.String(), "retrying") {
		t.Errorf("a malformed key was retried:\n%s", buf.String())
	}
}

func TestRetryDelay(t *testing.T) {
	c := &Client{transport: decision.Transport{RetryAfterCap: 5 * time.Second}}
	cases := map[string]struct {
		retryAfter, reset, tokenReset string
		want                          time.Duration
	}{
		"none":                    {want: 0},
		"retry-after wins":        {retryAfter: "2", reset: "4s", want: 2 * time.Second},
		"reset header":            {reset: "1.5s", want: 1500 * time.Millisecond},
		"reset header capped":     {reset: "6m0s", want: 5 * time.Second},
		"reset header unparsable": {reset: "soon", want: 0},
		"reset header negative":   {reset: "-1s", want: 0},
		"token window is longer":  {reset: "200ms", tokenReset: "3s", want: 3 * time.Second},
		"token window alone":      {tokenReset: "1s", want: time.Second},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			if tc.retryAfter != "" {
				h.Set("Retry-After", tc.retryAfter)
			}
			if tc.reset != "" {
				h.Set("x-ratelimit-reset-requests", tc.reset)
			}
			if tc.tokenReset != "" {
				h.Set("x-ratelimit-reset-tokens", tc.tokenReset)
			}
			if got := c.retryDelay(http.StatusTooManyRequests, h); got != tc.want {
				t.Errorf("retryDelay = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRetryDelayReadsTheResetHeaderOnlyOnARateLimit(t *testing.T) {
	c := &Client{transport: decision.Transport{RetryAfterCap: 5 * time.Second}}
	h := http.Header{}
	h.Set("x-ratelimit-reset-requests", "6m0s")
	if got := c.retryDelay(http.StatusServiceUnavailable, h); got != 0 {
		t.Errorf("retryDelay on a 503 = %s, want 0: the reset header describes the rate limit only", got)
	}
	h.Set("Retry-After", "2")
	if got := c.retryDelay(http.StatusServiceUnavailable, h); got != 2*time.Second {
		t.Errorf("retryDelay on a 503 with Retry-After = %s, want 2s", got)
	}
}

func TestEvaluateRejectsAnOversizedState(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, APIKey: "k", MaxStateBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Evaluate(t.Context(), decision.Request{State: strings.Repeat("x", 17), Questions: triageQuestions[1:2]})
	if kind := decision.Classify(err); kind != decision.FailureInputTooLarge {
		t.Errorf("kind = %q (err %v), want input_too_large", kind, err)
	}
	if _, ok := errors.AsType[*decision.StateTooLargeError](err); !ok {
		t.Errorf("err = %v, want a StateTooLargeError", err)
	}
	if calls.Load() != 0 {
		t.Error("an oversized state reached the provider")
	}
}

func TestNewValidatesTheEndpoint(t *testing.T) {
	cases := map[string]struct {
		cfg     Config
		wantErr bool
		want    string
	}{
		"default base":        {cfg: Config{APIKey: "k"}, want: "https://api.openai.com/v1/decisions"},
		"base without /v1":    {cfg: Config{APIKey: "k", BaseURL: "https://eu.api.openai.com"}, want: "https://eu.api.openai.com/v1/decisions"},
		"base with /v1 slash": {cfg: Config{APIKey: "k", BaseURL: "https://proxy.example/v1/"}, want: "https://proxy.example/v1/decisions"},
		"missing key":         {cfg: Config{APIKey: "  "}, wantErr: true},
		"relative base":       {cfg: Config{APIKey: "k", BaseURL: "api.openai.com"}, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := New(tc.cfg)
			if tc.wantErr {
				if decision.Classify(err) != decision.FailureConfig {
					t.Errorf("err = %v, want a config failure", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c.transport.Endpoint != tc.want {
				t.Errorf("endpoint = %q, want %q", c.transport.Endpoint, tc.want)
			}
		})
	}
}

func TestEvaluateGivesUpWhenTheContextIsDone(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := c.Evaluate(ctx, triageRequest()); decision.Classify(err) != decision.FailureTimeout {
		t.Errorf("err = %v, want a timeout", err)
	}
}

func TestEvaluateLogsRetriesOnTheRequestLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(goldenResponse(t))
	})
	req := triageRequest()
	req.Logger = logger
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !strings.Contains(buf.String(), "logger_name=decision-openai") {
		t.Errorf("retry line missing the provider logger name:\n%s", buf.String())
	}
}
