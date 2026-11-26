// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type EvalRunShape struct {
	EvaluatorTypes           []string
	MultiTrial               bool
	TrialFailureKinds        []string
	TrialFailureKindsUnknown bool
}
