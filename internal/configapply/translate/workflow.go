// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

type WorkflowHooks struct {
	ValidateAgent     func(name string) error
	ValidateWorkflow  func(name string) error
	ValidateConnector func(name string) error
	ValidateToolset   func(name string) error
}

type WorkflowV2Hooks = WorkflowHooks
