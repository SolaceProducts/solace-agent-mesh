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

const OpenAIDecisionPath = "/v1/decisions"

const NoCreditsDecisionAPIKey = "mock-no-credits-decision-key"

const RefusalMarker = "mock-refusal"

const mockOpenAIDecisionModel = "gpt-6-luna-mock"

type openAIDecisionChoice struct {
	Value string `json:"value"`
}

type openAIDecisionLevel struct {
	Label string `json:"label"`
}

type openAIDecisionQuestion struct {
	Type    string                 `json:"type"`
	Name    string                 `json:"name"`
	Choices []openAIDecisionChoice `json:"choices"`
	Levels  []openAIDecisionLevel  `json:"levels"`
}

type openAIDecisionRequest struct {
	Model     string                   `json:"model"`
	Questions []openAIDecisionQuestion `json:"questions"`
}

func (s *MockLLMServer) handleOpenAIDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key, hasKey := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch {
	case !hasKey || strings.TrimSpace(key) == "" || key == RejectedDecisionAPIKey:
		writeOpenAIDecisionError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key")
		return
	case key == NoCreditsDecisionAPIKey:
		writeOpenAIDecisionError(w, http.StatusTooManyRequests, "insufficient_quota", "credit_balance_exhausted")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.captureRequest(body)

	var req openAIDecisionRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Questions) == 0 {
		writeOpenAIDecisionError(w, http.StatusBadRequest, "invalid_request_error", "")
		return
	}
	if _, err := OpenAIDecisionInputImages(body); err != nil {
		writeOpenAIDecisionError(w, http.StatusBadRequest, "invalid_request_error", "invalid_input")
		return
	}
	choiceConfidence := 1.0
	if bytes.Contains(body, []byte(LowConfidenceMarker)) {
		choiceConfidence = LowChoiceConfidence
	}
	refuse := bytes.Contains(body, []byte(RefusalMarker))
	answers := make([]map[string]any, 0, len(req.Questions))
	for i, q := range req.Questions {
		if refuse && i == 0 {
			answers = append(answers, map[string]any{"type": "refusal", "name": q.Name, "refusal": "mock refusal"})
			continue
		}
		answer, ok := answerOpenAIDecisionQuestion(q, choiceConfidence)
		if !ok {
			writeOpenAIDecisionError(w, http.StatusBadRequest, "invalid_request_error", "")
			return
		}
		answers = append(answers, answer)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model":   mockOpenAIDecisionModel,
		"answers": answers,
		"usage":   map[string]int{"input_tokens": len(body) / 4, "output_tokens": 0, "total_tokens": len(body) / 4},
	})
}

func answerOpenAIDecisionQuestion(q openAIDecisionQuestion, choiceConfidence float64) (map[string]any, bool) {
	switch q.Type {
	case "predicate":
		return map[string]any{"type": "predicate", "name": q.Name, "probability": mockBooleanProbability}, true
	case "choice":
		if len(q.Choices) == 0 {
			return nil, false
		}
		probabilities := make([]map[string]any, len(q.Choices))
		for i, c := range q.Choices {
			p := choiceConfidence
			if i > 0 {
				p = (1 - choiceConfidence) / float64(len(q.Choices)-1)
			}
			probabilities[i] = map[string]any{"value": c.Value, "probability": p}
		}
		return map[string]any{"type": "choice", "name": q.Name, "choice": q.Choices[0].Value, "probabilities": probabilities, "confidence": choiceConfidence}, true
	case "score":
		if len(q.Levels) == 0 {
			return nil, false
		}
		probabilities := make([]map[string]any, len(q.Levels))
		for i, l := range q.Levels {
			p := 0.0
			if i == 0 {
				p = 1
			}
			probabilities[i] = map[string]any{"value": i, "label": l.Label, "probability": p}
		}
		return map[string]any{"type": "score", "name": q.Name, "score": 0, "probabilities": probabilities, "confidence": 1.0}, true
	default:
		return nil, false
	}
}

func writeOpenAIDecisionError(w http.ResponseWriter, status int, errType, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": "mock decision error", "type": errType, "code": code},
	})
}
