// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"maps"
	"slices"
	"sync"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type runShapeTally struct {
	evaluatorTypes []string
	multiTrial     bool
	workflow       bool

	mu           sync.Mutex
	failureKinds map[string]struct{}
	unknownEnds  bool
}

func newRunShapeTally(exp *store.EvalExperiment, evaluators []*store.EvalEvaluator) *runShapeTally {
	types := map[string]struct{}{}
	for _, ev := range evaluators {
		if ev != nil {
			types[ev.Type] = struct{}{}
		}
	}
	return &runShapeTally{
		evaluatorTypes: slices.Sorted(maps.Keys(types)),
		multiTrial:     exp.RunsPerExample > 1,
		workflow:       experimentTargetType(exp) == store.EvalTargetTypeWorkflow,
		failureKinds:   map[string]struct{}{},
	}
}

func (t *runShapeTally) noteFailureKind(kind string) {
	if t == nil || kind == "" || kind == FailureKindPartialFailure {
		return
	}
	if !slices.Contains(taxonomy.EvalWorkflowTrialFailureKinds, kind) {
		t.noteEndsUnknown()
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failureKinds[kind] = struct{}{}
}

func (t *runShapeTally) noteAbandoned(kind string) {
	if t == nil {
		return
	}
	if kind != "" {
		t.noteFailureKind(kind)
		return
	}
	t.noteEndsUnknown()
}

func (t *runShapeTally) noteEndsUnknown() {
	if t == nil || !t.workflow {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.unknownEnds = true
}

func (t *runShapeTally) snapshot() *platpkg.EvalRunShape {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return &platpkg.EvalRunShape{
		EvaluatorTypes:           slices.Clone(t.evaluatorTypes),
		MultiTrial:               t.multiTrial,
		TrialFailureKinds:        slices.Sorted(maps.Keys(t.failureKinds)),
		TrialFailureKindsUnknown: t.unknownEnds,
	}
}
