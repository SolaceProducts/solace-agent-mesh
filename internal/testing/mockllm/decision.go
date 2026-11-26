// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const DecisionPath = "/v1/systemone"

const RejectedDecisionAPIKey = "mock-rejected-decision-key"

const mockDecisionModel = "jev-mock"

const mockBooleanProbability = 0.98

const (
	LowConfidenceMarker = "mock-low-confidence"
	LowChoiceConfidence = 0.42
)

type decisionQuestion struct {
	Type     string          `json:"type"`
	Criteria json.RawMessage `json:"criteria"`
}

type decisionRequest struct {
	Model     string                      `json:"model"`
	Questions map[string]decisionQuestion `json:"questions"`
}

func (s *MockLLMServer) handleDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key, hasKey := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !hasKey || strings.TrimSpace(key) == "" || key == RejectedDecisionAPIKey {
		writeDecisionError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.captureRequest(body)

	var req decisionRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Questions) == 0 {
		writeDecisionError(w, http.StatusUnprocessableEntity, "a request needs at least one question")
		return
	}
	choiceConfidence := 1.0
	if bytes.Contains(body, []byte(LowConfidenceMarker)) {
		choiceConfidence = LowChoiceConfidence
	}
	answers := make(map[string]any, len(req.Questions))
	for name, q := range req.Questions {
		answer, ok := answerDecisionQuestion(q, choiceConfidence)
		if !ok {
			writeDecisionError(w, http.StatusUnprocessableEntity, "unsupported question "+name)
			return
		}
		answers[name] = answer
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model":   mockDecisionModel,
		"answers": answers,
		"usage":   map[string]int{"input_tokens": len(body) / 4, "output_tokens": 0},
	})
}

func answerDecisionQuestion(q decisionQuestion, choiceConfidence float64) (map[string]any, bool) {
	switch q.Type {
	case "noul":
		return map[string]any{"type": "noul", "noul": mockBooleanProbability}, true
	case "choice":
		options := orderedObjectKeys(q.Criteria)
		if len(options) == 0 {
			return nil, false
		}
		probabilities := make(map[string]float64, len(options))
		for _, option := range options[1:] {
			probabilities[option] = (1 - choiceConfidence) / float64(len(options)-1)
		}
		probabilities[options[0]] = choiceConfidence
		return map[string]any{"type": "choice", "choice": options[0], "probabilities": probabilities, "confidence": choiceConfidence}, true
	case "score":
		var levels []string
		if err := json.Unmarshal(q.Criteria, &levels); err != nil || len(levels) == 0 {
			return nil, false
		}
		return map[string]any{"type": "score", "score": 0, "probabilities": map[string]float64{"0": 1}, "confidence": 1.0}, true
	default:
		return nil, false
	}
}

func orderedObjectKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := tok.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil
		}
	}
	return keys
}

func writeDecisionError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
