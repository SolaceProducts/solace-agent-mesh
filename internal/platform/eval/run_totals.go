// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type runTotals struct {
	summary    map[string]float64
	stats      map[string]EvaluatorRunStats
	errorCount int
	successful int
	warnings   int
	decision   *platpkg.EvalDecisionFacts
	shape      *platpkg.EvalRunShape
}

func (t runTotals) finalStatus() string {
	switch {
	case t.errorCount > 0 && t.successful == 0:
		return StatusFailed
	case t.errorCount > 0 || t.warnings > 0:
		return StatusCompletedWithWarnings
	default:
		return StatusCompleted
	}
}

func (s *ExecutionService) computeRunTotals(ctx context.Context, runID string, exp *store.EvalExperiment, evaluators []*store.EvalEvaluator) (runTotals, error) {
	rows, err := s.results.ListByRun(ctx, runID)
	if err != nil {
		return runTotals{}, fmt.Errorf("list results: %w", err)
	}
	return totalRows(rows, exp, evaluators, s.logger), nil
}

func totalRows(rows []*store.EvalRunResultWithScores, exp *store.EvalExperiment, evaluators []*store.EvalEvaluator, logger *slog.Logger) runTotals {
	byID := make(map[string]*store.EvalEvaluator, len(evaluators))
	for _, ev := range evaluators {
		if ev != nil {
			byID[ev.ID] = ev
		}
	}
	scores := map[string][]float64{}
	accs := map[string]*evaluatorStatsAcc{}
	decision := newDecisionRunTally(evaluators)
	shape := newRunShapeTally(exp, evaluators)
	workflow := experimentTargetType(exp) == store.EvalTargetTypeWorkflow
	var t runTotals

	for _, row := range rows {
		if row == nil || row.EvalRunResult == nil {
			continue
		}
		kind := derefString(row.FailureKind)
		switch {
		case row.ErrorMessage != nil:
			t.errorCount++
			if workflow && kind == "" {
				shape.noteEndsUnknown()
			}
		case kind == FailureKindPartialFailure:
			t.warnings++
		}
		shape.noteFailureKind(kind)
		if judgeInputUnreadable(row.EvalRunResult) {
			t.warnings++
		}
		replied := derefString(row.AgentResponse) != "" || hasArtifacts(row.ArtifactsMetadata)
		for _, sc := range row.Scores {
			ev := byID[sc.EvaluatorID]
			errored := sc.Errored != nil && *sc.Errored
			details := scoreDetailsFromRow(sc.Details, sc.ID, logger)
			if errored {
				t.warnings++
			} else {
				scores[sc.EvaluatorID] = append(scores[sc.EvaluatorID], sc.Score)
			}
			if details != nil && (!errored || details.Usage != nil) {
				acc := accs[sc.EvaluatorID]
				if acc == nil {
					acc = &evaluatorStatsAcc{isDecision: ev != nil && ev.Type == evaluatorTypeDecision}
					accs[sc.EvaluatorID] = acc
				}
				acc.add(details, errored)
			}
			if ev != nil && ev.Type == evaluatorTypeDecision && replied {
				decision.add(ScoreOutput{Errored: errored, Details: details})
			}
		}
	}
	t.successful = len(rows) - t.errorCount
	t.summary = meanScores(scores)
	t.stats = evaluatorRunStats(accs)
	t.decision = decision.snapshot()
	t.shape = shape.snapshot()
	return t
}

func hasArtifacts(metaJSON *string) bool {
	if metaJSON == nil || *metaJSON == "" {
		return false
	}
	var metas []json.RawMessage
	return json.Unmarshal([]byte(*metaJSON), &metas) == nil && len(metas) > 0
}

func judgeInputUnreadable(row *store.EvalRunResult) bool {
	for _, role := range []string{store.EvalArtifactRoleInput, store.EvalArtifactRoleExpected} {
		meta := row.ArtifactMetadataByRole(role)
		if meta == nil || *meta == "" {
			continue
		}
		var rec exampleArtifactRecord
		if json.Unmarshal([]byte(*meta), &rec) == nil && rec.JudgeUnreadable {
			return true
		}
	}
	return false
}
