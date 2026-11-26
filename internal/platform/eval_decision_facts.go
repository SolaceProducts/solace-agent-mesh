// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type EvalDecisionFacts struct {
	Evaluators                  int
	Decided, Escalated, Errored int
	EscalatedBy                 map[string]int
}
