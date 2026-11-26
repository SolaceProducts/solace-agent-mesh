// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"maps"
	"sync"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type decisionRunTally struct {
	mu    sync.Mutex
	facts platpkg.EvalDecisionFacts
}

func newDecisionRunTally(evaluators []*store.EvalEvaluator) *decisionRunTally {
	t := &decisionRunTally{}
	for _, ev := range evaluators {
		if ev.Type == evaluatorTypeDecision {
			t.facts.Evaluators++
		}
	}
	return t
}

func (t *decisionRunTally) add(out ScoreOutput) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case out.Errored || out.Details == nil:
		t.facts.Errored++
	case out.Details.DecidedBy == decidedByDecision:
		t.facts.Decided++
	case out.Details.DecidedBy == decidedByLLMJudge:
		t.facts.Escalated++
		if t.facts.EscalatedBy == nil {
			t.facts.EscalatedBy = map[string]int{}
		}
		t.facts.EscalatedBy[out.Details.EscalationReason]++
	default:
		t.facts.Errored++
	}
}

func (t *decisionRunTally) snapshot() *platpkg.EvalDecisionFacts {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	facts := t.facts
	facts.EscalatedBy = maps.Clone(t.facts.EscalatedBy)
	return &facts
}
