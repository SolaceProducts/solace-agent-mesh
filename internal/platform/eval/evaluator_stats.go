// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type EvaluatorRunStats struct {
	Scored           int               `json:"scored"`
	Escalated        int               `json:"escalated,omitempty"`
	WithConfidence   int               `json:"withConfidence,omitempty"`
	EscalatedPercent *float64          `json:"escalatedPercent,omitempty"`
	MeanConfidence   *float64          `json:"meanConfidence,omitempty"`
	JudgeUsage       JudgeUsageDetails `json:"judgeUsage"`
}

type evaluatorStatsAcc struct {
	isDecision    bool
	scored        int
	escalated     int
	confidenceSum float64
	answered      int
	decision      *JudgeTokenUsage
	llmJudge      *JudgeTokenUsage
}

func (a *evaluatorStatsAcc) add(d *ScoreDetails, errored bool) {
	if d.Usage != nil {
		a.decision = addUsage(a.decision, d.Usage.Decision)
		a.llmJudge = addUsage(a.llmJudge, d.Usage.LLMJudge)
	}
	if errored {
		return
	}
	a.scored++
	if d.Escalated {
		a.escalated++
	}
	if d.Answer != nil && d.Answer.Confidence != nil {
		a.confidenceSum += *d.Answer.Confidence
		a.answered++
	}
}

func addUsage(total, u *JudgeTokenUsage) *JudgeTokenUsage {
	if u == nil {
		return total
	}
	if total == nil {
		total = &JudgeTokenUsage{}
	}
	total.InputTokens += u.InputTokens
	total.OutputTokens += u.OutputTokens
	return total
}

func (a *evaluatorStatsAcc) stats() EvaluatorRunStats {
	s := EvaluatorRunStats{
		Scored: a.scored, Escalated: a.escalated, WithConfidence: a.answered,
		JudgeUsage: JudgeUsageDetails{Decision: a.decision, LLMJudge: a.llmJudge},
	}
	if a.isDecision && a.scored > 0 {
		p := 100 * float64(a.escalated) / float64(a.scored)
		s.EscalatedPercent = &p
	}
	if a.answered > 0 {
		m := a.confidenceSum / float64(a.answered)
		s.MeanConfidence = &m
	}
	return s
}

func scoreDetailsFromRow(raw *string, scoreID string, logger *slog.Logger) *ScoreDetails {
	if raw == nil || *raw == "" {
		return nil
	}
	var d ScoreDetails
	if err := json.Unmarshal([]byte(*raw), &d); err != nil {
		orDiscard(logger).Warn("eval: unreadable score details; returning the score without them", "scoreID", scoreID, "error", err)
		return nil
	}
	return &d
}

func orDiscard(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return logger
}

func evaluatorStatsFromRow(r *store.EvalRun, logger *slog.Logger) map[string]EvaluatorRunStats {
	if r.EvaluatorStats == nil || *r.EvaluatorStats == "" {
		return nil
	}
	var stats map[string]EvaluatorRunStats
	if err := json.Unmarshal([]byte(*r.EvaluatorStats), &stats); err != nil {
		orDiscard(logger).Warn("eval: unreadable evaluator stats; returning the run without them", "runID", r.ID, "error", err)
		return nil
	}
	return stats
}

func evaluatorRunStats(accs map[string]*evaluatorStatsAcc) map[string]EvaluatorRunStats {
	if len(accs) == 0 {
		return nil
	}
	out := make(map[string]EvaluatorRunStats, len(accs))
	for id, a := range accs {
		out[id] = a.stats()
	}
	return out
}
