// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package systemone

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

const WireTypeBoolean = "noul"

const minScoreLevels = 2

func WireType(t decision.QuestionType) string {
	if t == decision.TypeBoolean {
		return WireTypeBoolean
	}
	return string(t)
}

func ScoreLevels(levels []decision.Criterion) (json.RawMessage, error) {
	if len(levels) < minScoreLevels || len(levels) > decision.MaxScoreLevels {
		return nil, fmt.Errorf("a score question needs %d to %d levels, got %d", minScoreLevels, decision.MaxScoreLevels, len(levels))
	}
	descriptions := make([]string, len(levels))
	for i, level := range levels {
		descriptions[i] = cmp.Or(level.Description, level.Key)
	}
	return json.Marshal(descriptions)
}

type Undescribed int

const (
	UndescribedNull Undescribed = iota
	UndescribedEmpty
)

func (u Undescribed) value() []byte {
	if u == UndescribedEmpty {
		return []byte(`""`)
	}
	return []byte("null")
}

func CriteriaObject(criteria []decision.Criterion, undescribed Undescribed) (json.RawMessage, error) {
	if len(criteria) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, c := range criteria {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(c.Key)
		if err != nil {
			return nil, err
		}
		value := undescribed.value()
		if c.Description != "" {
			if value, err = json.Marshal(c.Description); err != nil {
				return nil, err
			}
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        string             `json:"choice"`
	Score         *float64           `json:"score"`
	Legend        map[string]any     `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    *float64           `json:"confidence"`
}

func DecodeAnswers(raw map[string]Answer, questions []decision.Question) (map[string]decision.Answer, error) {
	answers := make(map[string]decision.Answer, len(raw))
	for key, a := range raw {
		answer, err := decodeAnswer(a)
		if err == nil {
			if q, ok := decision.AskedQuestion(questions, key, answer.Type); ok {
				answer, err = fitToQuestion(answer, q)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("answer %s: %w", decision.BoundedQuote(key), err)
		}
		answers[key] = answer
	}
	return answers, nil
}

func fitToQuestion(a decision.Answer, q decision.Question) (decision.Answer, error) {
	switch a.Type {
	case decision.TypeScore:
		return withAuthorLevels(a, q)
	case decision.TypeChoice:
		return decision.KeepAskedOptions(a, q), nil
	default:
		return a, nil
	}
}

func withAuthorLevels(a decision.Answer, q decision.Question) (decision.Answer, error) {
	for index := range a.Legend {
		if _, err := levelKey(q, index); err != nil {
			return decision.Answer{}, fmt.Errorf("legend: %w", err)
		}
	}
	probabilities := make(map[string]float64, len(a.Probabilities))
	for index, p := range a.Probabilities {
		key, err := levelKey(q, index)
		if err != nil {
			return decision.Answer{}, fmt.Errorf("probabilities: %w", err)
		}
		probabilities[key] = p
	}
	legend := make(map[string]any, len(q.Criteria))
	for i, level := range q.Criteria {
		legend[strconv.Itoa(i)] = level.Key
	}
	a.Probabilities = probabilities
	a.Legend = legend
	return a, nil
}

func levelKey(q decision.Question, index string) (string, error) {
	i, err := strconv.Atoi(index)
	if err != nil || i < 0 || i >= len(q.Criteria) || strconv.Itoa(i) != index {
		return "", fmt.Errorf("level %s is not an index in [0,%d]", decision.BoundedQuote(index), len(q.Criteria)-1)
	}
	return q.Criteria[i].Key, nil
}

func decodeAnswer(a Answer) (decision.Answer, error) {
	switch a.Type {
	case WireTypeBoolean:
		if a.Noul == nil {
			return decision.Answer{}, errors.New("noul answer has no probability")
		}
		return decision.Answer{Type: decision.TypeBoolean, Probability: *a.Noul}, nil
	case string(decision.TypeChoice):
		if a.Choice == "" {
			return decision.Answer{}, errors.New("choice answer has no choice")
		}
		return decision.Answer{
			Type:          decision.TypeChoice,
			Choice:        a.Choice,
			Probabilities: a.Probabilities,
			Confidence:    a.Confidence,
		}, nil
	case string(decision.TypeScore):
		if a.Score == nil {
			return decision.Answer{}, errors.New("score answer has no score")
		}
		return decision.Answer{
			Type:          decision.TypeScore,
			Score:         *a.Score,
			Legend:        a.Legend,
			Probabilities: a.Probabilities,
			Confidence:    a.Confidence,
		}, nil
	default:
		return decision.Answer{}, fmt.Errorf("unknown answer type %s", decision.BoundedQuote(a.Type))
	}
}
