// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gemini

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
		Criteria: []decision.Criterion{
			{Key: decision.BooleanCriterionTrue, Description: "The customer cannot work"},
		},
	},
	{
		Key:          "severity",
		Type:         decision.TypeScore,
		Instructions: "How badly is the customer blocked?",
		Criteria: []decision.Criterion{
			{Key: "cosmetic", Description: "Looks wrong, works"},
			{Key: "workaround", Description: "Workaround available"},
			{Key: "blocked", Description: "Fully blocked"},
		},
	},
}

func documentedResponse(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/documented_response.json")
	if err != nil {
		t.Fatalf("read documented response: %v", err)
	}
	return raw
}

func newTestClient(t *testing.T, base string, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL + base, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.transport.Backoff = time.Millisecond
	c.mediaTransport.Backoff = time.Millisecond
	return c
}

func triageRequest() decision.Request {
	return decision.Request{
		State:     map[string]any{"ticket": "my card was charged twice"},
		Questions: triageQuestions,
	}
}

func TestEvaluateSendsTheWireShape(t *testing.T) {
	for _, base := range []string{"", "/v1beta", "/v1beta/"} {
		t.Run("base"+base, func(t *testing.T) {
			var gotBody []byte
			var gotPath, gotKey, gotAuth string
			c := newTestClient(t, base, func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotKey, gotAuth = r.URL.Path, r.Header.Get("x-goog-api-key"), r.Header.Get("Authorization")
				gotBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write(documentedResponse(t))
			})
			if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if gotPath != "/v1beta/decisions" {
				t.Errorf("path = %q, want /v1beta/decisions", gotPath)
			}
			if gotKey != "test-key" || gotAuth != "" {
				t.Errorf("x-goog-api-key = %q, Authorization = %q; want the key in x-goog-api-key alone", gotKey, gotAuth)
			}
			var got map[string]any
			if err := json.Unmarshal(gotBody, &got); err != nil {
				t.Fatalf("request body: %v", err)
			}
			want := map[string]any{
				"model": "gemini-decision-1",
				"input": `{"ticket":"my card was charged twice"}`,
				"questions": map[string]any{
					"department": map[string]any{"type": "choice", "instructions": "Which team owns the ticket?",
						"criteria": map[string]any{"billing": "Payment issues", "technical": "Bugs", "other": ""}},
					"urgent": map[string]any{"type": "noul", "instructions": "Is the ticket urgent?",
						"criteria": map[string]any{"true_description": "The customer cannot work"}},
					"severity": map[string]any{"type": "score", "instructions": "How badly is the customer blocked?",
						"criteria": []any{"Looks wrong, works", "Workaround available", "Fully blocked"}},
				},
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("request body =\n%v\nwant\n%v", got, want)
			}
		})
	}
}

func TestEvaluateSendsImagesAsContentBlocks(t *testing.T) {
	var got struct {
		Input []map[string]string `json:"input"`
	}
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write(documentedResponse(t))
	})
	req := triageRequest()
	req.Media = []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png-bytes")}, {Kind: decision.MediaImage, MIMEType: "image/jpeg", Data: []byte("jpeg-bytes")}}
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	want := []map[string]string{
		{"type": "text", "text": `{"ticket":"my card was charged twice"}`},
		{"type": "image", "data": base64.StdEncoding.EncodeToString([]byte("png-bytes")), "mime_type": "image/png"},
		{"type": "image", "data": base64.StdEncoding.EncodeToString([]byte("jpeg-bytes")), "mime_type": "image/jpeg"},
	}
	if !reflect.DeepEqual(got.Input, want) {
		t.Errorf("input = %v, want %v", got.Input, want)
	}
}

func TestEncodeLeavesOutUndescribedBooleanCriteria(t *testing.T) {
	criteria, err := encodeCriteria(decision.Question{Key: "urgent", Type: decision.TypeBoolean})
	if err != nil || criteria != nil {
		t.Errorf("criteria = %s, %v; want none", criteria, err)
	}
	_, err = encodeCriteria(decision.Question{Key: "urgent", Type: decision.TypeBoolean, Criteria: []decision.Criterion{{Key: "maybe"}}})
	if err == nil {
		t.Error("an unknown boolean criterion encoded without error")
	}
}

func TestEvaluateDecodesTheDocumentedResponse(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(documentedResponse(t))
	})
	result, err := c.Evaluate(t.Context(), triageRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.Provider != decision.ProviderGoogleAIStudio || result.Model != DefaultModel {
		t.Errorf("provider, model = %q, %q; want google_ai_studio and the requested model", result.Provider, result.Model)
	}
	if result.Usage.InputTokens != 212 || result.Usage.OutputTokens != 0 {
		t.Errorf("usage = %+v, want 212 input tokens", result.Usage)
	}
	if got := result.Answers["department"]; got.Choice != "billing" || got.Confidence == nil || *got.Confidence != 0.91 {
		t.Errorf("department = %+v", got)
	}
	if got := result.Answers["urgent"]; got.Type != decision.TypeBoolean || got.Probability != 0.03 {
		t.Errorf("urgent = %+v", got)
	}
	severity := result.Answers["severity"]
	wantProbabilities := map[string]float64{"cosmetic": 0.1, "workaround": 0.6, "blocked": 0.3}
	wantLegend := map[string]any{"0": "cosmetic", "1": "workaround", "2": "blocked"}
	if severity.Score != 1.2 || !reflect.DeepEqual(severity.Probabilities, wantProbabilities) || !reflect.DeepEqual(severity.Legend, wantLegend) {
		t.Errorf("severity = %+v, want levels keyed by the author's criteria", severity)
	}
}

func TestEvaluateRejectsAMalformedAnswer(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"urgent":{"type":"noul"}},"usage":{"total_input_tokens":9}}`))
	})
	result, err := c.Evaluate(t.Context(), triageRequest())
	if de, ok := errors.AsType[*decision.Error](err); !ok || de.Kind != decision.FailureInvalidResponse {
		t.Fatalf("err = %v, want an invalid response", err)
	}
	if result.Usage.InputTokens != 9 {
		t.Errorf("usage = %+v, want the billed tokens kept", result.Usage)
	}
}

func TestEvaluateClassifiesStatuses(t *testing.T) {
	cases := []struct {
		status      int
		body        string
		kind        decision.FailureKind
		retryable   bool
		keyRejected bool
	}{
		{http.StatusBadRequest, `{"error":{"code":"safety","message":"Request blocked"}}`, decision.FailureRefused, false, false},
		{http.StatusBadRequest, `{"error":{"code":"prohibited_content","message":"Request blocked"}}`, decision.FailureRefused, false, false},
		{http.StatusBadRequest, `{"error":{"code":"blocklist","message":"Request blocked"}}`, decision.FailureRefused, false, false},
		{http.StatusBadRequest, `{"error":{"code":"image_safety","message":"Request blocked"}}`, decision.FailureRefused, false, false},
		{http.StatusBadRequest, `{"error":{"code":"content_blocked","message":"Request blocked"}}`, decision.FailureRefused, false, false},
		{http.StatusInternalServerError, `{"error":{"code":"safety","message":"Request blocked"}}`, decision.FailureUnreachable, true, false},
		{http.StatusServiceUnavailable, `{"error":{"code":503,"message":"API key not valid","details":[{"reason":"API_KEY_INVALID"}]}}`, decision.FailureUnreachable, true, false},
		{http.StatusForbidden, `{"error":{"code":403,"message":"API key not valid","details":[{"reason":"API_KEY_INVALID"}]}}`, decision.FailureUnauthorized, false, false},
		{http.StatusBadRequest, `{"error":{"code":400,"message":"API key not valid","status":"INVALID_ARGUMENT",` +
			`"details":[{"reason":"API_KEY_INVALID"}]}}`, decision.FailureUnauthorized, false, true},
		{http.StatusBadRequest, `{"error":{"code":400,"message":"API key not valid","status":"INVALID_ARGUMENT"}}`, decision.FailureConfig, false, false},
		{http.StatusUnauthorized, `{}`, decision.FailureUnauthorized, false, true},
		{http.StatusForbidden, `{}`, decision.FailureUnauthorized, false, false},
		{http.StatusNotFound, `{}`, decision.FailureConfig, false, false},
		{http.StatusRequestTimeout, `{}`, decision.FailureTimeout, true, false},
		{http.StatusRequestEntityTooLarge, `{}`, decision.FailureInputTooLarge, false, false},
		{http.StatusBadRequest, `{"error":{"code":"invalid_request","message":"The input token count exceeds the maximum number of tokens allowed 262144."}}`,
			decision.FailureConfig, false, false},
		{http.StatusBadRequest, `{"error":{"code":"invalid_request","message":"Unsupported mime_type 'application/octet-stream' in image content."}}`,
			decision.FailureConfig, false, false},
		{http.StatusTooManyRequests, `{}`, decision.FailureRateLimited, true, false},
		{http.StatusInternalServerError, `{}`, decision.FailureUnreachable, true, false},
		{http.StatusBadGateway, `{}`, decision.FailureUnreachable, true, false},
		{http.StatusServiceUnavailable, `{}`, decision.FailureUnreachable, true, false},
		{http.StatusGatewayTimeout, `{}`, decision.FailureUnreachable, true, false},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status)+tc.body, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.Evaluate(t.Context(), triageRequest())
			de, ok := errors.AsType[*decision.Error](err)
			if !ok || de.Kind != tc.kind {
				t.Fatalf("err = %v, want kind %s", err, tc.kind)
			}
			if retried := calls.Load() > 1; retried != tc.retryable {
				t.Errorf("calls = %d, want retried = %v", calls.Load(), tc.retryable)
			}
			if got := decision.KeyRejected(err); got != tc.keyRejected {
				t.Errorf("KeyRejected = %v, want %v: an evaluation run stops on a rejected key", got, tc.keyRejected)
			}
			if strings.Contains(err.Error(), "Request blocked") || strings.Contains(err.Error(), "API key not valid") {
				t.Errorf("error %q carries the provider's message", err)
			}
		})
	}
}

func TestPostReadsRetryAfterOnARateLimit(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, wait, err := c.post(t.Context(), []byte(`{}`), nil, DefaultModel, false)
	if se, ok := errors.AsType[*StatusError](err); !ok || se.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want the 429 status error", err)
	}
	if wait != 2*time.Second {
		t.Errorf("wait = %v, want the 2s Retry-After", wait)
	}
}

func TestEvaluateRejectsAnOversizedState(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an oversized state reached the provider")
	})
	req := triageRequest()
	req.State = strings.Repeat("x", DefaultMaxStateBytes+1)
	_, err := c.Evaluate(t.Context(), req)
	if de, ok := errors.AsType[*decision.Error](err); !ok || de.Kind != decision.FailureInputTooLarge {
		t.Errorf("err = %v, want input too large", err)
	}
}

func TestNewRequiresAKey(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New without a key succeeded")
	}
}

func TestStatusErrorNamesTheReasonButNotTheMessage(t *testing.T) {
	err := newStatusError(http.StatusBadRequest, []byte(`{"error":{"code":400,"message":"API key not valid for ticket 42",`+
		`"status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`), false)
	if got := err.Error(); !strings.Contains(got, "API_KEY_INVALID") || strings.Contains(got, "ticket 42") {
		t.Errorf("error = %q, want the reason and not the message", got)
	}
	free := newStatusError(http.StatusBadRequest, []byte(`{"error":{"code":"free text, with spaces","status":"INVALID_ARGUMENT"}}`), false)
	if free.Reason != "INVALID_ARGUMENT" {
		t.Errorf("reason = %q, want the status when the code is not an identifier", free.Reason)
	}
}

func TestEvaluateSendsTheRequestedModel(t *testing.T) {
	var gotModel string
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		gotModel = body.Model
		_, _ = w.Write(documentedResponse(t))
	})
	req := triageRequest()
	req.Model = "gemini-decision-2-preview"
	result, err := c.Evaluate(t.Context(), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gotModel != req.Model || result.Model != req.Model {
		t.Errorf("sent %q, result %q; want both %q", gotModel, result.Model, req.Model)
	}
}

func TestEvaluateReadsOutputTokens(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{},"usage":{"total_input_tokens":7,"total_output_tokens":3}}`))
	})
	result, err := c.Evaluate(t.Context(), decision.Request{State: "x", Questions: nil})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.Usage.InputTokens != 7 || result.Usage.OutputTokens != 3 {
		t.Errorf("usage = %+v, want 7 in and 3 out", result.Usage)
	}
}

func TestEncodeRefusesMoreImagesThanOneCallTakes(t *testing.T) {
	c := newTestClient(t, "", func(http.ResponseWriter, *http.Request) {
		t.Error("a request with too many images reached the provider")
	})
	req := triageRequest()
	req.Media = make([]decision.Media, decision.MaxImagesPerRequest+1)
	for i := range req.Media {
		req.Media[i] = decision.Media{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png")}
	}
	if _, err := c.Evaluate(t.Context(), req); err == nil {
		t.Error("too many images were sent without error")
	}
}

func TestEncodeBooleanCriteria(t *testing.T) {
	cases := map[string]struct {
		criteria []decision.Criterion
		want     string
	}{
		"false only": {[]decision.Criterion{{Key: decision.BooleanCriterionFalse, Description: "Can wait"}}, `{"false_description":"Can wait"}`},
		"both": {[]decision.Criterion{{Key: decision.BooleanCriterionTrue, Description: "Blocked"}, {Key: decision.BooleanCriterionFalse, Description: "Can wait"}},
			`{"true_description":"Blocked","false_description":"Can wait"}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := encodeCriteria(decision.Question{Key: "urgent", Type: decision.TypeBoolean, Criteria: tc.criteria})
			if err != nil || string(got) != tc.want {
				t.Errorf("criteria = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}

const contextExceededBody = `{"error":{"code":"invalid_request","message":"The input token count exceeds the maximum number of tokens allowed 262144."}}`

func TestEvaluateReadsAContextOverflowOnlyForARequestCarryingMedia(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(contextExceededBody))
	})
	req := triageRequest()
	req.Media = []decision.Media{{Kind: decision.MediaAudio, MIMEType: "audio/wav", Data: []byte("wav")}}
	_, err := c.Evaluate(t.Context(), req)
	if kind := decision.Classify(err); kind != decision.FailureInputTooLarge {
		t.Fatalf("kind = %q (err %v), want input_too_large", kind, err)
	}
	if !strings.Contains(err.Error(), "fewer or shorter media files") {
		t.Errorf("error %q does not advise fewer or shorter media files", err)
	}

	req.Media = []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png")}}
	_, err = c.Evaluate(t.Context(), req)
	if kind := decision.Classify(err); kind != decision.FailureConfig {
		t.Errorf("image-only kind = %q (err %v), want config as without media support", kind, err)
	}
}

func TestEvaluateWordsATextOnlyTooLargeErrorAsBefore(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	})
	_, err := c.Evaluate(t.Context(), triageRequest())
	se, ok := errors.AsType[*StatusError](err)
	if !ok {
		t.Fatalf("err = %v, want a status error", err)
	}
	const want = "input exceeds the model's context limit (HTTP 413); pass only the fields the questions need"
	if got := se.Error(); got != want {
		t.Errorf("error text = %q, want exactly %q", got, want)
	}
}
