// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"cmp"
	"encoding/json"
	"slices"

	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

type evaluatorConsistency struct {
	EvaluatorID      string `json:"evaluatorId"`
	EvaluatorName    string `json:"evaluatorName"`
	Examples         int    `json:"examples"`
	PassedEvery      int    `json:"passedEvery"`
	FailedEvery      int    `json:"failedEvery"`
	Inconsistent     int    `json:"inconsistent"`
	TrialsPerExample int    `json:"trialsPerExample"`
}

type runShape struct{ trialsPerExample, examples int }

func shapeOfRun(run *evalplatform.RunResponse) runShape {
	if run == nil {
		return runShape{}
	}
	experiment, _ := run.ConfigSnapshot["experiment"].(map[string]any)
	dataset, _ := run.ConfigSnapshot["dataset"].(map[string]any)
	s := runShape{trialsPerExample: snapshotCount(experiment["runs_per_example"]), examples: snapshotCount(dataset["example_count"])}
	if s.examples == 0 && s.trialsPerExample > 0 && run.ProgressTotal%s.trialsPerExample == 0 {
		s.examples = run.ProgressTotal / s.trialsPerExample
	}
	return s
}

func snapshotCount(v any) int {
	switch n := v.(type) {
	case float64:
		if n > 0 && n == float64(int(n)) {
			return int(n)
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i > 0 {
			return int(i)
		}
	}
	return 0
}

func consistencyByEvaluator(results []evalplatform.RunResultResponse, evalNames map[string]string, shape runShape) []evaluatorConsistency {
	rows := map[string]int{}
	most := 0
	for _, r := range results {
		rows[r.ExampleID]++
		most = max(most, rows[r.ExampleID])
	}
	k := max(shape.trialsPerExample, most)
	if k < 2 {
		return nil
	}
	unseen := max(0, shape.examples-len(rows))
	var out []evaluatorConsistency
	for id, passed := range passesByEvaluator(results) {
		c := evaluatorConsistency{EvaluatorID: id, EvaluatorName: evalLabel(evalNames, id), TrialsPerExample: k}
		for example := range rows {
			c.add(passed[example], k)
		}
		c.Examples += unseen
		c.FailedEvery += unseen
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b evaluatorConsistency) int {
		return cmp.Or(cmp.Compare(a.EvaluatorName, b.EvaluatorName), cmp.Compare(a.EvaluatorID, b.EvaluatorID))
	})
	return out
}

func passesByEvaluator(results []evalplatform.RunResultResponse) map[string]map[string]int {
	passes := map[string]map[string]int{}
	for _, r := range results {
		agentErrored := derefString(r.ErrorMessage) != ""
		for _, s := range r.Scores {
			if s.Passed == nil {
				continue
			}
			if passes[s.EvaluatorID] == nil {
				passes[s.EvaluatorID] = map[string]int{}
			}
			if *s.Passed && !agentErrored {
				passes[s.EvaluatorID][r.ExampleID]++
			}
		}
	}
	return passes
}

func (c *evaluatorConsistency) add(passed, k int) {
	c.Examples++
	switch passed {
	case 0:
		c.FailedEvery++
	case k:
		c.PassedEvery++
	default:
		c.Inconsistent++
	}
}

func writeConsistency(lg *lineLogger, consistency []evaluatorConsistency) {
	if len(consistency) == 0 {
		return
	}
	lg.result("Consistency across trials:\n")
	for _, c := range consistency {
		lg.result("  %s: pass^%d %d/%d examples, %d inconsistent, %d failed every trial\n",
			c.EvaluatorName, c.TrialsPerExample, c.PassedEvery, c.Examples, c.Inconsistent, c.FailedEvery)
	}
}
