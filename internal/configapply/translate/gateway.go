// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

type GatewayHooks struct {
	ValidateAgent    func(name string) error
	ValidateWorkflow func(name string) error
}

type EntrypointV2Hooks = GatewayHooks
