// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

type ExperimentHooks struct {
	LookupDatasetID     func(name string) (string, error)
	LookupEvaluatorID   func(name string) (string, error)
	LookupDatasetName   func(id string) (string, error)
	LookupEvaluatorName func(id string) (string, error)
}

type ExperimentV2Hooks = ExperimentHooks
