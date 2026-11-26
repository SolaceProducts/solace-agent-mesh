// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"testing"
)

func postDecision(t *testing.T, srv *MockLLMServer, apiKey, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+DecisionPath, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post decision: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read decision response: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, decoded
}

func TestDecisionAnswersEachQuestionType(t *testing.T) {
	srv := startServer(t, mustScenario(t, `{"id":"d","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)

	status, resp := postDecision(t, srv, "any-key", `{"model":"jev-1.13.0","state":"ticket","questions":{
		"urgent":{"type":"noul","instructions":"x"},
		"department":{"type":"choice","instructions":"x","criteria":{"technical":null,"billing":"invoices"}},
		"severity":{"type":"score","instructions":"x","criteria":["low","medium","high"]}}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", status, resp)
	}
	answers, _ := resp["answers"].(map[string]any)
	urgent, _ := answers["urgent"].(map[string]any)
	if urgent["noul"] != mockBooleanProbability {
		t.Errorf("urgent = %v, want a confident true", urgent)
	}
	department, _ := answers["department"].(map[string]any)
	if department["choice"] != "technical" {
		t.Errorf("department = %v, want the first declared option, not the alphabetically first", department)
	}
	severity, _ := answers["severity"].(map[string]any)
	if severity["score"] != float64(0) {
		t.Errorf("severity = %v, want the lowest level", severity)
	}
}

func TestDecisionLowConfidenceMarkerLowersTheChoice(t *testing.T) {
	srv := startServer(t, mustScenario(t, `{"id":"d","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)

	status, resp := postDecision(t, srv, "any-key", `{"model":"jev-1.13.0","state":{"ticket":"vague `+LowConfidenceMarker+`"},"questions":{
		"department":{"type":"choice","instructions":"x","criteria":{"billing":null,"technical":null}},
		"urgent":{"type":"noul","instructions":"x"}}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", status, resp)
	}
	answers, _ := resp["answers"].(map[string]any)
	department, _ := answers["department"].(map[string]any)
	probabilities, _ := department["probabilities"].(map[string]any)
	technical, _ := probabilities["technical"].(float64)
	if department["confidence"] != LowChoiceConfidence || probabilities["billing"] != LowChoiceConfidence || math.Abs(technical-(1-LowChoiceConfidence)) > 1e-9 {
		t.Errorf("department = %v, want the first option at %v", department, LowChoiceConfidence)
	}
	if urgent, _ := answers["urgent"].(map[string]any); urgent["noul"] != mockBooleanProbability {
		t.Errorf("urgent = %v, want the boolean answer unchanged", urgent)
	}
}

func TestDecisionRejectsLikeTheRealAPI(t *testing.T) {
	srv := startServer(t, mustScenario(t, `{"id":"d","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)
	question := `{"model":"jev-1.13.0","state":"x","questions":{"q":{"type":"noul","instructions":"x"}}}`

	cases := []struct {
		name   string
		apiKey string
		body   string
		want   int
	}{
		{"no key", "", question, http.StatusUnauthorized},
		{"rejected key", RejectedDecisionAPIKey, question, http.StatusUnauthorized},
		{"no questions", "any-key", `{"model":"jev-1.13.0","state":"x","questions":{}}`, http.StatusUnprocessableEntity},
		{"unknown question type", "any-key", `{"model":"jev-1.13.0","state":"x","questions":{"q":{"type":"essay","instructions":"x"}}}`, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if status, _ := postDecision(t, srv, tc.apiKey, tc.body); status != tc.want {
				t.Errorf("status = %d, want %d", status, tc.want)
			}
		})
	}
}
