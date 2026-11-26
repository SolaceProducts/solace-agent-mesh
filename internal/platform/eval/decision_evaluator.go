// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const (
	evaluatorTypeLLMJudge  = taxonomy.EvaluatorTypeLLMJudge
	evaluatorTypeDecision  = taxonomy.EvaluatorTypeDecision
	evaluatorTypeHeuristic = taxonomy.EvaluatorTypeHeuristic
)

const (
	scoringChosen          = "chosen"
	scoringExpected        = "expected"
	DefaultDecisionScoring = scoringChosen
)

const (
	readPrompt           = "prompt"
	readResponse         = "response"
	readExpectedResponse = "expected_response"
	readTranscript       = "transcript"
	readArtifacts        = "artifacts"
)

const decisionQuestionKey = "answer"

var allDecisionReads = []string{readPrompt, readResponse, readExpectedResponse, readTranscript, readArtifacts}

func DefaultDecisionReads() []string {
	return []string{readPrompt, readResponse, readExpectedResponse}
}

type DecisionConfig struct {
	Question              EvaluatorQuestion `json:"question"`
	Scoring               string            `json:"scoring"`
	Reads                 []string          `json:"reads"`
	MinConfidence         *float64          `json:"min_confidence,omitempty"`
	EscalateToEvaluatorID *string           `json:"escalate_to_evaluator_id,omitempty"`
}

type decisionRunConfig struct {
	DecisionConfig
	ResolvedModel *DecisionModelSnapshot `json:"resolved_model,omitempty"`
	EscalateTo    map[string]any         `json:"escalate_to,omitempty"`
}

func parseDecisionConfig(raw *string) (*DecisionConfig, error) {
	cfg, err := parseJSONConfig[DecisionConfig](raw)
	if err != nil || cfg == nil || cfg.EscalateToEvaluatorID == nil {
		return cfg, err
	}
	if parsed, perr := uuid.Parse(*cfg.EscalateToEvaluatorID); perr == nil {
		cfg.EscalateToEvaluatorID = new(parsed.String())
	}
	return cfg, nil
}

func parseDecisionRunConfig(raw *string) (*decisionRunConfig, error) {
	return parseJSONConfig[decisionRunConfig](raw)
}

func parseJSONConfig[T any](raw *string) (*T, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	var cfg T
	if err := json.Unmarshal([]byte(*raw), &cfg); err != nil {
		return nil, fmt.Errorf("decode decision_config: %w", err)
	}
	return &cfg, nil
}

func marshalDecisionConfig(cfg DecisionConfig) (*string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode decision_config: %w", err)
	}
	s := string(b)
	return &s, nil
}

func (q EvaluatorQuestion) toDecision() decision.Question {
	criteria := make([]decision.Criterion, len(q.Criteria))
	for i, c := range q.Criteria {
		criteria[i] = decision.Criterion{Key: strings.TrimSpace(c.Key), Description: strings.TrimSpace(c.Description)}
	}
	return decision.Question{
		Key:          decisionQuestionKey,
		Type:         decision.QuestionType(q.Type),
		Instructions: strings.TrimSpace(q.Instructions),
		Criteria:     criteria,
	}
}

func (q EvaluatorQuestion) Normalized() EvaluatorQuestion {
	out := EvaluatorQuestion{Type: strings.TrimSpace(q.Type), Instructions: strings.TrimSpace(q.Instructions)}
	for _, c := range q.Criteria {
		out.Criteria = append(out.Criteria, EvaluatorCriterion{
			Key: strings.TrimSpace(c.Key), Description: strings.TrimSpace(c.Description),
		})
	}
	return out
}

func decisionEvaluatorsEnabled(registry *features.Registry) bool {
	return registry != nil && registry.IsEnabled(features.KeyEvalDecisionEvaluator)
}

func decisionDisabledMessage() string {
	return fmt.Sprintf("decision evaluators are turned off; set %s=true to enable them",
		features.EnvVarName(features.KeyEvalDecisionEvaluator))
}

type fieldErrors map[string][]string

func (fe fieldErrors) add(field, format string, args ...any) {
	fe[field] = append(fe[field], fmt.Sprintf(format, args...))
}

func (fe fieldErrors) addValidation(err error) {
	if ve, ok := errors.AsType[*ValidationError](err); ok {
		fe.add(ve.Field, "%s", ve.Message)
	}
}

func (fe fieldErrors) err() error {
	if len(fe) == 0 {
		return nil
	}
	fields := slices.Sorted(maps.Keys(fe))
	msgs := make([]string, 0, len(fe))
	for _, f := range fields {
		msgs = append(msgs, f+": "+strings.Join(fe[f], "; "))
	}
	return &ValidationError{
		Field:   fields[0],
		Message: strings.Join(msgs, ". "),
		Details: map[string][]string(fe),
	}
}

type decisionSettings struct {
	Model          *string
	PromptTemplate *string
	ChoiceScores   map[string]any
	Config         DecisionConfig
}

func validateDecisionSettings(s decisionSettings, fe fieldErrors) {
	if s.Model == nil || strings.TrimSpace(*s.Model) == "" {
		fe.add("model", "model is required for decision evaluators; name a model registered with modelType decision")
	}
	if s.PromptTemplate != nil && strings.TrimSpace(*s.PromptTemplate) != "" {
		fe.add("promptTemplate", "promptTemplate is not used by decision evaluators; the question's instructions and criteria replace it")
	}

	if validateDecisionQuestion(s.Config.Question, fe) {
		validateDecisionChoiceScores(s.Config.Question, s.ChoiceScores, fe)
	}
	if s.Config.Scoring != scoringChosen && s.Config.Scoring != scoringExpected {
		fe.add("scoring", "scoring must be %q or %q, got %q", scoringChosen, scoringExpected, s.Config.Scoring)
	}
	validateDecisionReads(s.Config.Reads, fe)
	if mc := s.Config.MinConfidence; mc != nil {
		if *mc <= 0 || *mc > 1 {
			fe.add("minConfidence", "minConfidence must be greater than 0 and at most 1, got %v", *mc)
		}
		if s.Config.EscalateToEvaluatorID == nil {
			fe.add("minConfidence", "minConfidence needs escalateToEvaluatorId: without an evaluator to escalate to, every case is scored by the decision model")
		}
		if s.Config.Question.Type == string(decision.TypeBoolean) && *mc <= 0.5 {
			fe.add("minConfidence", "a boolean answer is always at least 50%% confident, so minConfidence must be above 0.5 to ever escalate")
		}
	}
}

func validateDecisionQuestion(q EvaluatorQuestion, fe fieldErrors) bool {
	if strings.TrimSpace(q.Type) == "" && strings.TrimSpace(q.Instructions) == "" && len(q.Criteria) == 0 {
		fe.add("question", "question is required for decision evaluators")
		return false
	}
	if err := q.toDecision().Validate(); err != nil {
		fe.add("question", "%s", err.Error())
		return false
	}
	if q.Type == string(decision.TypeChoice) && len(q.Criteria) > maxChoiceScores {
		fe.add("question", "a choice question takes at most %d options, got %d", maxChoiceScores, len(q.Criteria))
	}
	return true
}

func validateDecisionReads(reads []string, fe fieldErrors) {
	if !slices.Contains(reads, readResponse) {
		fe.add("reads", "reads must include %q", readResponse)
	}
	seen := map[string]bool{}
	for _, r := range reads {
		if !slices.Contains(allDecisionReads, r) {
			fe.add("reads", "unknown read %q; use one of %s", r, strings.Join(allDecisionReads, ", "))
		}
		if seen[r] {
			fe.add("reads", "%q is listed more than once", r)
		}
		seen[r] = true
	}
}

func validateDecisionChoiceScores(q EvaluatorQuestion, scores map[string]any, fe fieldErrors) {
	if q.Type != string(decision.TypeChoice) {
		if len(scores) > 0 {
			fe.add("choiceScores", "choiceScores is only used by choice questions; a %s question maps its answer to a score itself", q.Type)
		}
		return
	}
	if len(scores) == 0 {
		fe.add("choiceScores", "choiceScores is required for a choice question: give each option its score between 0 and 1")
		return
	}
	missing, extra := compareChoiceScores(q, scores, fe)
	if len(missing) > 0 {
		fe.add("choiceScores", "choiceScores has no score for option(s) %s", strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		fe.add("choiceScores", "choiceScores names %s, which the question does not offer", strings.Join(extra, ", "))
	}
}

func compareChoiceScores(q EvaluatorQuestion, scores map[string]any, fe fieldErrors) (missing, extra []string) {
	options := make(map[string]bool, len(q.Criteria))
	for _, c := range q.Criteria {
		options[strings.TrimSpace(c.Key)] = true
		if _, ok := scores[strings.TrimSpace(c.Key)]; !ok {
			missing = append(missing, strings.TrimSpace(c.Key))
		}
	}
	for k, v := range scores {
		if !options[k] {
			extra = append(extra, k)
		} else if f, ok := asFloat(v); !ok || f < 0 || f > 1 {
			fe.add("choiceScores", "choiceScores.%s must be a number from 0 to 1", k)
		}
	}
	slices.Sort(missing)
	slices.Sort(extra)
	return missing, extra
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func rejectDecisionOnlyFields(evType string, question *EvaluatorQuestion, scoring string, reads []string,
	minConfidence *float64, escalateTo *string, fe fieldErrors) {
	msg := "%s is only used by decision evaluators, not " + evType
	if question != nil {
		fe.add("question", msg, "question")
	}
	if scoring != "" {
		fe.add("scoring", msg, "scoring")
	}
	if reads != nil {
		fe.add("reads", msg, "reads")
	}
	if minConfidence != nil {
		fe.add("minConfidence", msg, "minConfidence")
	}
	if escalateTo != nil {
		fe.add("escalateToEvaluatorId", msg, "escalateToEvaluatorId")
	}
}
