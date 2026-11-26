// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build harness

package awe

import "github.com/SolaceProducts/solace-agent-mesh/pkg/harnesstools"

func lookupAdditionalBuiltinGroup(group string) []string {
	switch group {
	case LifecycleHarnessBuiltinToolset:
		return []string{harnesstools.EchoBuiltinName}
	default:
		return nil
	}
}

const LifecycleHarnessBuiltinToolset = "lifecycle_harness_toolset"
