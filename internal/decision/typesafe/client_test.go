// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
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
		Instructions: "Which team owns `ticket`?",
		Criteria: []decision.Criterion{
			{Key: "billing", Description: "Payment issues"},
			{Key: "technical", Description: "Bugs"},
		},
	},
	{Key: "urgent", Type: decision.TypeBoolean, Instructions: "`ticket` is urgent"},
	{
		Key:          "severity",
		Type:         decision.TypeScore,
		Instructions: "Customer impact",
		Criteria: []decision.Criterion{
			{Key: "low", Description: "cosmetic"},
			{Key: "medium", Description: "degraded"},
			{Key: "high", Description: "down"},
		},
	},
}

const triageResponse = `{
  "model": "jev-1.13.0",
  "answers": {
    "department": {"type": "choice", "choice": "billing", "probabilities": {"billing": 0.93, "technical": 0.07}, "confidence": 0.86},
    "urgent": {"type": "noul", "noul": 0.97},
    "severity": {"type": "score", "score": 1.05, "legend": {"0": "cosmetic", "1": "degraded", "2": "down"}, "probabilities": {"0": 0.0, "1": 0.95, "2": 0.05}, "confidence": 0.92}
  },
  "usage": {"input_tokens": 812, "output_tokens": 0}
}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.transport.Backoff = time.Millisecond
	return c
}

func triageRequest() decision.Request {
	return decision.Request{
		Model:     "jev-1.13.0",
		State:     map[string]any{"ticket": "my card was charged twice"},
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
		_, _ = io.WriteString(w, triageResponse)
	})

	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if gotPath != "/v1/systemone" {
		t.Errorf("path = %q, want /v1/systemone", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	var sent struct {
		Model     string                     `json:"model"`
		State     map[string]any             `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if sent.Model != "jev-1.13.0" || sent.State["ticket"] != "my card was charged twice" {
		t.Errorf("model/state not sent as given: %s", gotBody)
	}
	wantQuestions := map[string]string{
		"urgent":     `{"type":"noul","instructions":"` + "`ticket`" + ` is urgent"}`,
		"department": `{"type":"choice","instructions":"Which team owns ` + "`ticket`" + `?","criteria":{"billing":"Payment issues","technical":"Bugs"}}`,
		"severity":   `{"type":"score","instructions":"Customer impact","criteria":["cosmetic","degraded","down"]}`,
	}
	for key, want := range wantQuestions {
		if got := string(sent.Questions[key]); got != want {
			t.Errorf("question %s went out as\n  %s\nwant\n  %s", key, got, want)
		}
	}
}

func sentQuestion(t *testing.T, q decision.Question) (string, int32, error) {
	t.Helper()
	var calls atomic.Int32
	var sent struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(http.StatusUnprocessableEntity)
	})
	_, err := c.Evaluate(t.Context(), decision.Request{State: map[string]any{}, Questions: []decision.Question{q}})
	return string(sent.Questions[q.Key]), calls.Load(), err
}

func TestEvaluateEncodesCriteriaPerQuestionType(t *testing.T) {
	cases := map[string]struct {
		question decision.Question
		want     string
	}{
		"score level without a description falls back to its key": {
			question: decision.Question{Key: "q", Type: decision.TypeScore, Instructions: "i", Criteria: []decision.Criterion{
				{Key: "zeta", Description: "lowest"}, {Key: "alpha"}, {Key: "mid", Description: "highest"},
			}},
			want: `{"type":"score","instructions":"i","criteria":["lowest","alpha","highest"]}`,
		},
		"choice keeps declaration order and sends an empty description as null": {
			question: decision.Question{Key: "q", Type: decision.TypeChoice, Instructions: "i", Criteria: []decision.Criterion{
				{Key: "zeta", Description: "last letter"}, {Key: "alpha"},
			}},
			want: `{"type":"choice","instructions":"i","criteria":{"zeta":"last letter","alpha":null}}`,
		},
		"boolean true and false descriptions pass through": {
			question: decision.Question{Key: "q", Type: decision.TypeBoolean, Instructions: "i", Criteria: []decision.Criterion{
				{Key: "true", Description: "time-sensitive"}, {Key: "false", Description: "no urgency"},
			}},
			want: `{"type":"noul","instructions":"i","criteria":{"true":"time-sensitive","false":"no urgency"}}`,
		},
		"boolean with both sides undescribed sends no criteria": {
			question: decision.Question{Key: "q", Type: decision.TypeBoolean, Instructions: "i", Criteria: []decision.Criterion{
				{Key: "true"}, {Key: "false"},
			}},
			want: `{"type":"noul","instructions":"i"}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, _, _ := sentQuestion(t, tc.question)
			if got != tc.want {
				t.Errorf("question went out as\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

func TestEvaluateRejectsUnencodableCriteriaBeforeSending(t *testing.T) {
	levels := func(n int) []decision.Criterion {
		out := make([]decision.Criterion, n)
		for i := range out {
			out[i] = decision.Criterion{Key: "level" + strconv.Itoa(i)}
		}
		return out
	}
	cases := map[string]decision.Question{
		"one score level":     {Key: "q", Type: decision.TypeScore, Instructions: "i", Criteria: levels(1)},
		"eleven score levels": {Key: "q", Type: decision.TypeScore, Instructions: "i", Criteria: levels(11)},
		"boolean with other keys": {Key: "q", Type: decision.TypeBoolean, Instructions: "i", Criteria: []decision.Criterion{
			{Key: "yes", Description: "urgent"}, {Key: "no", Description: "not urgent"},
		}},
		"boolean with only the true side": {Key: "q", Type: decision.TypeBoolean, Instructions: "i", Criteria: []decision.Criterion{
			{Key: "true", Description: "time-sensitive"},
		}},
		"boolean with an empty false side": {Key: "q", Type: decision.TypeBoolean, Instructions: "i", Criteria: []decision.Criterion{
			{Key: "true", Description: "needs a human"}, {Key: "false"},
		}},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			_, calls, err := sentQuestion(t, q)
			if got := decision.Classify(err); got != decision.FailureConfig {
				t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureConfig)
			}
			if calls != 0 {
				t.Errorf("an unencodable question reached the network (%d calls)", calls)
			}
		})
	}
	t.Run("ten score levels are accepted", func(t *testing.T) {
		got, calls, _ := sentQuestion(t, decision.Question{Key: "q", Type: decision.TypeScore, Instructions: "i", Criteria: levels(10)})
		if calls != 1 || !strings.Contains(got, `"criteria":["level0",`) {
			t.Errorf("a 10-level score was not sent: calls=%d body=%s", calls, got)
		}
	})
}

func TestEvaluateDefaultsTheModel(t *testing.T) {
	var sent map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = io.WriteString(w, triageResponse)
	})
	req := triageRequest()
	req.Model = ""
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if sent["model"] != DefaultModel {
		t.Errorf("model = %v, want %s", sent["model"], DefaultModel)
	}
}

func TestEvaluateNormalizesAnswers(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, triageResponse)
	})

	result, err := c.Evaluate(t.Context(), triageRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := result.Validate(triageQuestions); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.Model != "jev-1.13.0" || result.Usage.InputTokens != 812 {
		t.Errorf("model/usage = %q/%+v", result.Model, result.Usage)
	}
	urgent := result.Answers["urgent"]
	if urgent.Type != decision.TypeBoolean || urgent.Probability != 0.97 {
		t.Errorf("urgent = %+v", urgent)
	}
	dept := result.Answers["department"]
	if dept.Choice != "billing" || dept.Confidence == nil || *dept.Confidence != 0.86 {
		t.Errorf("department = %+v", dept)
	}
	severity := result.Answers["severity"]
	if severity.Score != 1.05 || severity.Confidence == nil || *severity.Confidence != 0.92 {
		t.Errorf("severity = %+v", severity)
	}
	wantOutput := map[string]any{
		"type":          "score",
		"score":         1.05,
		"legend":        map[string]any{"0": "low", "1": "medium", "2": "high"},
		"probabilities": map[string]any{"low": 0.0, "medium": 0.95, "high": 0.05},
		"confidence":    0.92,
	}
	if got := severity.Output(); !reflect.DeepEqual(got, wantOutput) {
		t.Errorf("severity output = %#v\nwant %#v", got, wantOutput)
	}
}

func TestEvaluateBoundsProviderSuppliedFields(t *testing.T) {
	longModel := "jev-" + strings.Repeat("x", 500)
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"`+longModel+`","answers":{
			"department":{"type":"choice","choice":"billing","probabilities":{"billing":0.9,"technical":0.1,"echoed customer text":0.4},"confidence":0.9}}}`)
	})
	req := triageRequest()
	req.Questions = triageQuestions[:1]
	result, err := c.Evaluate(t.Context(), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.Model != longModel[:64] {
		t.Errorf("model = %q (%d bytes), want the first 64 bytes", result.Model, len(result.Model))
	}
	want := map[string]float64{"billing": 0.9, "technical": 0.1}
	if got := result.Answers["department"].Probabilities; !reflect.DeepEqual(got, want) {
		t.Errorf("probabilities = %v, want only the question's options %v", got, want)
	}
}

func TestEvaluateRejectsScoreLevelsOutsideTheQuestion(t *testing.T) {
	cases := map[string]string{
		"probability index past the top level": `"probabilities":{"0":0.5,"3":0.5}`,
		"negative probability index":           `"probabilities":{"-1":1}`,
		"padded probability index":             `"probabilities":{"01":1}`,
		"fractional probability index":         `"probabilities":{"1.0":1}`,
		"author key instead of an index":       `"probabilities":{"low":1}`,
		"legend index past the top level":      `"legend":{"0":"a","1":"b","2":"c","3":"d"}`,
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{"answers":{"severity":{"type":"score","score":1,`+field+`}}}`)
			})
			req := triageRequest()
			req.Questions = triageQuestions[2:]
			_, err := c.Evaluate(t.Context(), req)
			if got := decision.Classify(err); got != decision.FailureInvalidResponse {
				t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureInvalidResponse)
			}
			if calls.Load() != 1 {
				t.Errorf("an invalid response was retried (%d calls)", calls.Load())
			}
		})
	}
}

func TestEvaluateRetriesTransientStatuses(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, statusOverloaded, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) < maxAttempts {
					w.WriteHeader(status)
					return
				}
				_, _ = io.WriteString(w, triageResponse)
			})
			if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if calls.Load() != maxAttempts {
				t.Errorf("calls = %d, want %d", calls.Load(), maxAttempts)
			}
		})
	}
}

func TestEvaluateGivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(statusOverloaded)
	})
	_, err := c.Evaluate(t.Context(), triageRequest())
	se, ok := errors.AsType[*StatusError](err)
	if !ok || se.StatusCode != statusOverloaded {
		t.Fatalf("err = %v, want a 529 StatusError", err)
	}
	if got := decision.Classify(err); got != decision.FailureRateLimited {
		t.Errorf("Classify = %s, want %s", got, decision.FailureRateLimited)
	}
	if calls.Load() != maxAttempts {
		t.Errorf("calls = %d, want %d", calls.Load(), maxAttempts)
	}
}

func TestEvaluateClassifiesStatusFailures(t *testing.T) {
	cases := []struct {
		status    int
		want      decision.FailureKind
		wantCalls int32
	}{
		{status: http.StatusUnauthorized, want: decision.FailureUnauthorized, wantCalls: 1},
		{status: http.StatusForbidden, want: decision.FailureUnauthorized, wantCalls: 1},
		{status: http.StatusUnprocessableEntity, want: decision.FailureConfig, wantCalls: 1},
		{status: http.StatusBadRequest, want: decision.FailureConfig, wantCalls: 1},
		{status: http.StatusNotFound, want: decision.FailureConfig, wantCalls: 1},
		{status: http.StatusRequestTimeout, want: decision.FailureTimeout, wantCalls: maxAttempts},
		{status: http.StatusTooManyRequests, want: decision.FailureRateLimited, wantCalls: maxAttempts},
		{status: statusOverloaded, want: decision.FailureRateLimited, wantCalls: maxAttempts},
		{status: http.StatusInternalServerError, want: decision.FailureUnreachable, wantCalls: 1},
		{status: http.StatusBadGateway, want: decision.FailureUnreachable, wantCalls: maxAttempts},
		{status: http.StatusServiceUnavailable, want: decision.FailureUnreachable, wantCalls: maxAttempts},
		{status: http.StatusGatewayTimeout, want: decision.FailureUnreachable, wantCalls: maxAttempts},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
			})
			_, err := c.Evaluate(t.Context(), triageRequest())
			if got := decision.Classify(err); got != tc.want {
				t.Errorf("Classify(%v) = %s, want %s", err, got, tc.want)
			}
			if got := calls.Load(); got != tc.wantCalls {
				t.Errorf("calls = %d, want %d", got, tc.wantCalls)
			}
			if strings.Count(err.Error(), "typesafe") != 1 {
				t.Errorf("error should name the provider exactly once: %q", err)
			}
		})
	}
}

func TestEvaluateStatusErrorReadsCleanly(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.Evaluate(t.Context(), triageRequest())
	if want := "typesafe: API key rejected (HTTP 401)"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func stallingHandler() (handler http.HandlerFunc, release func()) {
	released := make(chan struct{})
	return func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-released:
		}
	}, func() { close(released) }
}

func TestEvaluateClassifiesTimeouts(t *testing.T) {
	handler, release := stallingHandler()
	c := newTestClient(t, handler)
	t.Cleanup(release)
	c.transport.Timeout = 50 * time.Millisecond
	_, err := c.Evaluate(t.Context(), triageRequest())
	if got := decision.Classify(err); got != decision.FailureTimeout {
		t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureTimeout)
	}
}

func TestASharedClientTimeoutDoesNotCapTimeoutSeconds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			_, _ = io.WriteString(w, triageResponse)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	shared := &http.Client{Timeout: 50 * time.Millisecond}
	c, err := New(Config{BaseURL: srv.URL, APIKey: "test-key", Timeout: 5 * time.Second, HTTPClient: shared})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v; a slower answer within timeout_seconds must not hit the shared client's Timeout", err)
	}
	if shared.Timeout != 50*time.Millisecond {
		t.Errorf("shared client Timeout = %v; New must not modify the caller's client", shared.Timeout)
	}
}

func dropConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Errorf("hijack: %v", err)
		return
	}
	_ = conn.Close()
}

func TestEvaluateRetriesTransportErrors(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < maxAttempts {
			dropConnection(t, w)
			return
		}
		_, _ = io.WriteString(w, triageResponse)
	})
	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if calls.Load() != maxAttempts {
		t.Errorf("calls = %d, want %d", calls.Load(), maxAttempts)
	}
}

func TestEvaluateClassifiesPersistentTransportErrors(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		dropConnection(t, w)
	})
	_, err := c.Evaluate(t.Context(), triageRequest())
	if got := decision.Classify(err); got != decision.FailureUnreachable {
		t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureUnreachable)
	}
	if calls.Load() != maxAttempts {
		t.Errorf("calls = %d, want %d", calls.Load(), maxAttempts)
	}
}

type countingTransport struct {
	calls atomic.Int32
	next  http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.next.RoundTrip(r)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEvaluateRetriesATemporaryDNSFailure(t *testing.T) {
	transport := &countingTransport{next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: r.URL.Hostname(), IsNotFound: true, IsTemporary: true}}
	})}
	c, err := New(Config{BaseURL: "http://decision-provider.example", APIKey: "test-key", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.transport.Backoff = time.Millisecond
	_, err = c.Evaluate(t.Context(), triageRequest())
	if got := decision.Classify(err); got != decision.FailureUnreachable {
		t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureUnreachable)
	}
	if got := transport.calls.Load(); got != maxAttempts {
		t.Errorf("attempts = %d, want %d", got, maxAttempts)
	}
}

func TestEvaluateTreatsAMisconfiguredEndpointAsPermanent(t *testing.T) {
	tlsServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, triageResponse)
	}))
	tlsServer.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsServer.StartTLS()
	t.Cleanup(tlsServer.Close)
	unresolvable := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: r.URL.Hostname(), IsNotFound: true}}
	})
	for name, tc := range map[string]struct {
		baseURL string
		next    http.RoundTripper
	}{
		"unresolvable host":     {baseURL: "http://decision-provider.invalid", next: unresolvable},
		"untrusted certificate": {baseURL: tlsServer.URL, next: http.DefaultTransport},
	} {
		t.Run(name, func(t *testing.T) {
			transport := &countingTransport{next: tc.next}
			c, err := New(Config{BaseURL: tc.baseURL, APIKey: "test-key", HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			c.transport.Backoff = time.Millisecond
			_, err = c.Evaluate(t.Context(), triageRequest())
			if got := decision.Classify(err); got != decision.FailureConfig {
				t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureConfig)
			}
			if got := transport.calls.Load(); got != 1 {
				t.Errorf("attempts = %d, want 1", got)
			}
		})
	}
}

func TestEvaluateDoesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"detail":"echoes the state: secret customer text"}`)
			})
			_, err := c.Evaluate(t.Context(), triageRequest())
			if err == nil {
				t.Fatal("Evaluate succeeded, want an error")
			}
			if calls.Load() != 1 {
				t.Errorf("calls = %d, want 1", calls.Load())
			}
			if strings.Contains(err.Error(), "secret customer text") {
				t.Errorf("error must not carry the response body: %v", err)
			}
		})
	}
}

func TestEvaluateHonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	var first, second time.Time
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			first = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		second = time.Now()
		_, _ = io.WriteString(w, triageResponse)
	})
	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gap := second.Sub(first); gap < 900*time.Millisecond {
		t.Errorf("retried after %s, want at least the 1s Retry-After", gap)
	}
}

func TestEvaluateHonoursRetryAfterDate(t *testing.T) {
	var calls atomic.Int32
	var first, second time.Time
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			first = time.Now()
			w.Header().Set("Retry-After", first.Add(2*time.Second).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		second = time.Now()
		_, _ = io.WriteString(w, triageResponse)
	})
	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gap := second.Sub(first); gap < 900*time.Millisecond {
		t.Errorf("retried after %s, want to wait for the HTTP-date Retry-After", gap)
	}
}

func TestEvaluateCapsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, triageResponse)
	})
	c.transport.RetryAfterCap = 20 * time.Millisecond

	start := time.Now()
	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Evaluate took %s; Retry-After must be capped", elapsed)
	}
}

func TestEvaluateReturnsPromptlyWhenTheDeadlineIsShorterThanTheDelay(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c.transport.Timeout = 2 * time.Second

	start := time.Now()
	_, err := c.Evaluate(t.Context(), triageRequest())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Evaluate took %s; it must not sleep toward a deadline it cannot beat", elapsed)
	}
	if se, ok := errors.AsType[*StatusError](err); !ok || se.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want the last 429", err)
	}
	if got := decision.Classify(err); got != decision.FailureRateLimited {
		t.Errorf("Classify = %s, want %s", got, decision.FailureRateLimited)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

func TestEvaluateStopsRetryingWhenContextEnds(t *testing.T) {
	var calls atomic.Int32
	answered := make(chan struct{}, maxAttempts)
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		answered <- struct{}{}
	})
	c.transport.Backoff = time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		select {
		case <-answered:
			time.Sleep(20 * time.Millisecond)
			cancel()
		case <-ctx.Done():
		}
	}()

	start := time.Now()
	_, err := c.Evaluate(ctx, triageRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Errorf("Evaluate took %s; the backoff must not outlive the context", elapsed)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

func TestEvaluateRejectsOversizedStateBeforeSending(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, triageResponse)
	})
	c.transport.MaxStateBytes = 64

	req := triageRequest()
	req.State = map[string]any{"ticket": strings.Repeat("x", 200)}
	_, err := c.Evaluate(t.Context(), req)
	if _, ok := errors.AsType[*decision.StateTooLargeError](err); !ok {
		t.Fatalf("err = %v, want StateTooLargeError", err)
	}
	if got := decision.Classify(err); got != decision.FailureInputTooLarge {
		t.Errorf("Classify = %s, want %s", got, decision.FailureInputTooLarge)
	}
	if calls.Load() != 0 {
		t.Errorf("an oversized state reached the network")
	}
}

func TestEvaluateRefusesImagesAsAConfigurationError(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, triageResponse)
	})
	req := triageRequest()
	req.Model = "acme/decider-4b"
	req.Media = []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png")}}
	_, err := c.Evaluate(t.Context(), req)
	if !errors.Is(err, decision.ErrMediaUnsupported) {
		t.Fatalf("err = %v, want ErrMediaUnsupported", err)
	}
	if got := decision.Classify(err); got != decision.FailureConfig || !got.Permanent() {
		t.Errorf("Classify = %s, want the permanent %s", got, decision.FailureConfig)
	}
	if calls.Load() != 0 {
		t.Errorf("a request with images reached TypeSafe")
	}
}

func TestEvaluateClassifiesTheProvidersInputSizeRejection(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   decision.FailureKind
	}{
		"typesafe token limit": {
			status: http.StatusBadRequest,
			body:   `{"detail":{"error_type":"max_tokens_exceeded"}}`,
			want:   decision.FailureInputTooLarge,
		},
		"token limit through an openrouter pass-through": {
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"HTTP 400: {\"detail\":{\"error_type\":\"max_tokens_exceeded\"}}","code":400}}`,
			want:   decision.FailureInputTooLarge,
		},
		"payload too large": {status: http.StatusRequestEntityTooLarge, body: `{}`, want: decision.FailureInputTooLarge},
		"other bad request": {
			status: http.StatusBadRequest,
			body:   `{"detail":{"error_type":"invalid_question"}}`,
			want:   decision.FailureConfig,
		},
		"string detail":                 {status: http.StatusBadRequest, body: `{"detail":"max_tokens_exceeded"}`, want: decision.FailureConfig},
		"server error naming the limit": {status: http.StatusInternalServerError, body: `{"detail":{"error_type":"max_tokens_exceeded"}}`, want: decision.FailureUnreachable},
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
			if got := decision.Classify(err); got != tc.want {
				t.Errorf("Classify(%v) = %s, want %s", err, got, tc.want)
			}
			if tc.want == decision.FailureInputTooLarge && calls.Load() != 1 {
				t.Errorf("an input-size rejection was retried: %d calls", calls.Load())
			}
			if strings.Contains(err.Error(), "error_type") || strings.Contains(err.Error(), "detail") {
				t.Errorf("the response body leaked into the error: %v", err)
			}
		})
	}
}

func TestEvaluateRejectsMalformedAnswers(t *testing.T) {
	cases := map[string]string{
		"noul without probability":  `{"answers":{"urgent":{"type":"noul"}}}`,
		"choice without choice":     `{"answers":{"department":{"type":"choice"}}}`,
		"unknown type":              `{"answers":{"urgent":{"type":"essay"}}}`,
		"not json":                  `<html>`,
		"huge unknown type":         `{"answers":{"urgent":{"type":"` + strings.Repeat("t", 500_000) + `"}}}`,
		"huge answer key":           `{"answers":{"` + strings.Repeat("k", 500_000) + `":{"type":"noul"}}}`,
		"wrong type under huge key": `{"answers":{"` + strings.Repeat("k", 500_000) + `":{"type":"noul","noul":"high"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, body)
			})
			_, err := c.Evaluate(t.Context(), triageRequest())
			if err == nil {
				t.Fatal("Evaluate succeeded on a malformed response")
			}
			if got := decision.Classify(err); got != decision.FailureInvalidResponse {
				t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureInvalidResponse)
			}
			if len(err.Error()) >= 1024 {
				t.Errorf("error is %d bytes, want provider strings bounded", len(err.Error()))
			}
			if calls.Load() != 1 {
				t.Errorf("a malformed response was retried (%d calls)", calls.Load())
			}
		})
	}
}

func TestNewRequiresAnAPIKey(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("New accepted an empty api key")
	}
	if got := decision.Classify(err); got != decision.FailureConfig {
		t.Errorf("Classify = %s, want %s", got, decision.FailureConfig)
	}
	if err.Error() != "typesafe: api_key is required" {
		t.Errorf("err = %q", err)
	}
}

func TestAKeyWithSurroundingWhitespaceIsSentTrimmed(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, triageResponse)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, APIKey: " test-key\n"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want the trimmed key", gotAuth)
	}
}

func TestAKeyNoHeaderCanCarryIsAConfigFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, triageResponse)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, APIKey: "test\nkey"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.transport.Backoff = time.Millisecond
	_, err = c.Evaluate(t.Context(), triageRequest())
	if got := decision.Classify(err); got != decision.FailureConfig {
		t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureConfig)
	}
	if calls.Load() != 0 {
		t.Errorf("the server was reached %d times by a request net/http should refuse", calls.Load())
	}
}

func TestNewValidatesTheBaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		wantErr bool
	}{
		{baseURL: "api.typesafe.ai", wantErr: true},
		{baseURL: "ftp://x", wantErr: true},
		{baseURL: "https://openrouter.ai/api", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.baseURL, func(t *testing.T) {
			_, err := New(Config{BaseURL: tt.baseURL, APIKey: "test-key"})
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Errorf("New(%q) err = %v, want error %v", tt.baseURL, err, tt.wantErr)
			}
			if err != nil && decision.Classify(err) != decision.FailureConfig {
				t.Errorf("Classify = %s, want %s", decision.Classify(err), decision.FailureConfig)
			}
		})
	}
}

func paddedTriageResponse(size int) string {
	const prefix = `{"padding":"`
	rest := `",` + triageResponse[1:]
	return prefix + strings.Repeat("x", size-len(prefix)-len(rest)) + rest
}

func TestEvaluateBoundsTheResponseBody(t *testing.T) {
	cases := map[string]struct {
		size    int
		wantErr bool
	}{
		"at the cap":   {size: maxResponseBytes},
		"over the cap": {size: maxResponseBytes + 1, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := paddedTriageResponse(tc.size)
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			})
			_, err := c.Evaluate(t.Context(), triageRequest())
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Evaluate: %v", err)
				}
				return
			}
			if got := decision.Classify(err); got != decision.FailureInvalidResponse {
				t.Errorf("Classify(%v) = %s, want %s", err, got, decision.FailureInvalidResponse)
			}
		})
	}
}

func TestEvaluateReadsOnlyTheHeadOfAnErrorBody(t *testing.T) {
	const sizeRejection = `{"detail":{"error_type":"max_tokens_exceeded"}}`
	padding := strings.Repeat(" ", maxErrorBodyBytes)
	cases := map[string]struct {
		body string
		want decision.FailureKind
	}{
		"rejection inside the cap, body past it": {body: sizeRejection + padding, want: decision.FailureInputTooLarge},
		"rejection past the cap":                 {body: padding + sizeRejection, want: decision.FailureConfig},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := c.Evaluate(t.Context(), triageRequest())
			if got := decision.Classify(err); got != tc.want {
				t.Errorf("Classify(%v) = %s, want %s", err, got, tc.want)
			}
		})
	}
}
