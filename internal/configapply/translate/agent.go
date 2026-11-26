// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

type AgentHooks struct {
	LookupSkillID       func(name string) (string, error)
	LookupConnectorID   func(name string) (string, error)
	ValidateToolset     func(name string) error
	LookupSkillName     func(id string) (string, error)
	LookupConnectorName func(id string) (string, error)
}

type AgentV2Hooks = AgentHooks
